package integration

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	"github.com/Daniel-Cpz/FlowForge/internal/service/dispatch"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type gatedSleep struct {
	entered chan uuid.UUID
	release chan struct{}
	calls   atomic.Int64
}

func (e *gatedSleep) Execute(ctx context.Context, j *job.Job) execution.Outcome {
	e.calls.Add(1)
	select {
	case e.entered <- j.ID:
	case <-ctx.Done():
		return execution.Sleep{}.Execute(ctx, j)
	}
	select {
	case <-e.release:
	case <-ctx.Done():
	}
	return execution.Sleep{}.Execute(ctx, j)
}
func awaitIntegration[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatal("integration barrier timeout")
		var zero T
		return zero
	}
}
func TestMultipleWorkerPoolsAndDispatchers(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, client, key, group := redisQueue(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	jobs := make([]uuid.UUID, 0, 10)
	for range 8 {
		jobs = append(jobs, createSleep(t, pool, `{"duration_ms":0}`).ID)
	}
	for _, input := range []service.CreateInput{{Type: "unsupported", Payload: json.RawMessage(`{}`)}, {Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":10001}`)}} {
		j, err := service.New(repo).Create(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j.ID)
	}
	// Two dispatchers scan the same durable intent set. Explicit additional
	// publication ensures duplicates even if the first marker wins a fast race.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- dispatch.New(repo, q, testLogger()).Once(ctx) }()
	}
	wg.Wait()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range jobs {
		if err := q.Publish(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	e1, e2 := &gatedSleep{entered: make(chan uuid.UUID, 10), release: make(chan struct{})}, &gatedSleep{entered: make(chan uuid.UUID, 10), release: make(chan struct{})}
	w1, err := execution.NewWithConcurrency(repo, q, e1, testLogger(), 2)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := execution.NewWithConcurrency(repo, q, e2, testLogger(), 2)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { done <- w1.RunWithDispatcher(ctx, dispatch.New(repo, q, testLogger()).Run) }()
	go func() { done <- w2.RunWithDispatcher(ctx, dispatch.New(repo, q, testLogger()).Run) }()
	for range 2 {
		awaitIntegration(t, ctx, e1.entered)
		awaitIntegration(t, ctx, e2.entered)
	}
	rows, err := pool.Query(ctx, `SELECT assigned_worker,count(*) FROM jobs WHERE status='RUNNING' GROUP BY assigned_worker`)
	if err != nil {
		t.Fatal(err)
	}
	owners := map[uuid.UUID]int{}
	for rows.Next() {
		var owner uuid.UUID
		var n int
		if err := rows.Scan(&owner, &n); err != nil {
			t.Fatal(err)
		}
		owners[owner] = n
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if len(owners) != 2 {
		t.Fatal("two instances did not execute", owners)
	}
	for _, n := range owners {
		if n != 2 {
			t.Fatal("process saturation", owners)
		}
	}
	close(e1.release)
	close(e2.release)
	// Real I/O completion is observed with bounded polling; execution correctness
	// and saturation above rely on barriers rather than elapsed-time assertions.
	for {
		var terminal int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE status IN ('SUCCEEDED','DEAD_LETTER')`).Scan(&terminal); err != nil {
			t.Fatal(err)
		}
		pending, err := client.XPending(ctx, key, group).Result()
		if err != nil {
			t.Fatal(err)
		}
		if terminal == len(jobs) && pending.Count == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("pool did not complete")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	joinCtx, joinCancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer joinCancel()
	for range 2 {
		if err := awaitIntegration(t, joinCtx, done); err != nil {
			t.Fatal(err)
		}
	}
	var attempts, succeeded, failed, inconsistent int
	if err := pool.QueryRow(joinCtx, `SELECT (SELECT count(*) FROM job_attempts),(SELECT count(*) FROM jobs WHERE status='SUCCEEDED'),(SELECT count(*) FROM jobs WHERE status='DEAD_LETTER'),(SELECT count(*) FROM jobs j JOIN job_attempts a ON a.job_id=j.id WHERE (j.status<>a.status AND NOT(j.status='DEAD_LETTER' AND a.status='FAILED')) OR (j.assigned_worker IS NOT NULL AND j.assigned_worker<>a.worker_id) OR j.attempt_count<>a.attempt_number OR j.result IS DISTINCT FROM a.result OR j.finished_at IS DISTINCT FROM a.finished_at)`).Scan(&attempts, &succeeded, &failed, &inconsistent); err != nil {
		t.Fatal(err)
	}
	if attempts != 10 || succeeded != 8 || failed != 2 || inconsistent != 0 || e1.calls.Load()+e2.calls.Load() != 10 {
		t.Fatal("execution/attempt mismatch", attempts, succeeded, failed, inconsistent, e1.calls.Load(), e2.calls.Load())
	}
}

// Force two different Worker instances to reach Claim from the same QUEUED
// snapshot; PostgreSQL, not a process mutex, decides the only winner.
type claimBarrierStore struct {
	*postgres.JobRepository
	ready   chan struct{}
	release chan struct{}
}

func (s *claimBarrierStore) GetByID(ctx context.Context, id uuid.UUID) (*job.Job, error) {
	j, err := s.JobRepository.GetByID(ctx, id)
	if err == nil {
		select {
		case s.ready <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return j, err
}
func TestTwoWorkersConcurrentClaimDuplicateACKOwnership(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, client, key, group := redisQueue(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	j := createSleep(t, pool, `{"duration_ms":0}`)
	for range 2 {
		if err := q.Publish(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
	}
	first, err := q.Receive(ctx, "worker-one:0")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	second, err := q.Receive(ctx, "worker-two:0")
	if err != nil || second == nil {
		t.Fatal(err)
	}
	store := &claimBarrierStore{repo, make(chan struct{}, 2), make(chan struct{})}
	executor := &gatedSleep{entered: make(chan uuid.UUID, 2), release: make(chan struct{})}
	w1, w2 := execution.New(store, q, executor, testLogger()), execution.New(store, q, executor, testLogger())
	done := make(chan error, 2)
	go func() { _, err := w1.Handle(ctx, first); done <- err }()
	go func() { _, err := w2.Handle(ctx, second); done <- err }()
	for range 2 {
		awaitIntegration(t, ctx, store.ready)
	}
	close(store.release)
	awaitIntegration(t, ctx, executor.entered)
	// Only the rejected duplicate returns; the actual delivery remains pending.
	if err := awaitIntegration(t, ctx, done); err != nil {
		t.Fatal(err)
	}
	if pendingCount(t, client, key, group) != 1 || attemptCount(t, pool, j.ID) != 1 || executor.calls.Load() != 1 {
		t.Fatal("original delivery ACKed or duplicate executed")
	}
	close(executor.release)
	if err := awaitIntegration(t, ctx, done); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, j.ID)
	if err != nil || got.Status != job.Succeeded || got.AttemptCount != 1 || pendingCount(t, client, key, group) != 0 {
		t.Fatal("winner did not finalize", err)
	}
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT worker_id FROM job_attempts WHERE job_id=$1`, j.ID).Scan(&owner); err != nil || owner != *got.AssignedWorker {
		t.Fatal("owner mismatch", err)
	}
}
func TestMultiplePoolUnavailableStoreAfterClaim(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, client, key, group := redisQueue(t)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	for range 2 {
		j := createSleep(t, pool, `{"duration_ms":10000}`)
		if err := q.Publish(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
	}
	e := &gatedSleep{entered: make(chan uuid.UUID, 2), release: make(chan struct{})}
	worker, err := execution.NewWithConcurrency(repo, q, e, testLogger(), 2)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- worker.RunWithDispatcher(ctx, dispatch.New(repo, q, testLogger()).Run) }()
	for range 2 {
		awaitIntegration(t, ctx, e.entered)
	}
	cfg := pool.Config().Copy()
	pool.Close()
	cancel()
	joinCtx, joinCancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer joinCancel()
	if err := awaitIntegration(t, joinCtx, done); err == nil {
		t.Fatal("closed store after claim reported success")
	}
	if pendingCount(t, client, key, group) != 2 {
		t.Fatal("unpersisted deliveries ACKed")
	}
	diagnostic, err := pgxpool.NewWithConfig(joinCtx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer diagnostic.Close()
	var running, attempts int
	if err := diagnostic.QueryRow(joinCtx, `SELECT (SELECT count(*) FROM jobs WHERE status='RUNNING' AND result IS NULL),(SELECT count(*) FROM job_attempts WHERE status='RUNNING')`).Scan(&running, &attempts); err != nil || running != 2 || attempts != 2 {
		t.Fatal("outage fabricated terminal state", running, attempts, err)
	}
}
