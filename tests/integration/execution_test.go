package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/config"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	redisinfra "github.com/Daniel-Cpz/FlowForge/internal/infrastructure/redis"
	"github.com/Daniel-Cpz/FlowForge/internal/service/dispatch"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/Daniel-Cpz/FlowForge/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	redisclient "github.com/redis/go-redis/v9"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Only these randomly generated keys are deleted. Never FLUSHDB/FLUSHALL or
// touch the application's flowforge:jobs:v1 stream/consumer group.
func redisQueue(t *testing.T) (*redisinfra.Queue, *redisclient.Client, string, string) {
	t.Helper()
	addr := os.Getenv("FLOWFORGE_TEST_REDIS_ADDR")
	password := os.Getenv("FLOWFORGE_TEST_REDIS_PASSWORD")
	db := 0
	if addr == "" && os.Getenv("FLOWFORGE_INTEGRATION") == "1" {
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		addr, password, db = cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB
	}
	if addr == "" {
		t.Skip("FLOWFORGE_TEST_REDIS_ADDR unset; Redis integration NOT EXECUTED")
	}
	client := redisinfra.NewClient(addr, password, db)
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal("test Redis unavailable")
	}
	key := "flowforge:test:" + uuid.New().String()
	group := "test-workers"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Del(ctx, key).Err(); err != nil {
			t.Error("test Redis namespace cleanup failed")
		}
		_ = client.Close()
	})
	return redisinfra.NewQueue(client, key, group), client, key, group
}
func createSleep(t *testing.T, pool *pgxpool.Pool, payload string) *job.Job {
	t.Helper()
	j, err := service.New(postgres.NewJobRepository(pool)).Create(t.Context(), service.CreateInput{Type: "SLEEP", Payload: json.RawMessage(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func receive(t *testing.T, q *redisinfra.Queue) *job.Delivery {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	msg, err := q.Receive(ctx, "integration-consumer")
	if err != nil || msg == nil {
		t.Fatal("queue did not deliver", err)
	}
	return msg
}
func pendingCount(t *testing.T, client *redisclient.Client, key, group string) int64 {
	t.Helper()
	p, err := client.XPending(t.Context(), key, group).Result()
	if err != nil {
		t.Fatal(err)
	}
	return p.Count
}
func attemptCount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM job_attempts WHERE job_id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJobOutboxAtomicCreation(t *testing.T) {
	pool := migratedDatabase(t)
	ctx := t.Context()
	s := service.New(postgres.NewJobRepository(pool))
	if _, err := s.Create(ctx, service.CreateInput{Type: "SLEEP", Payload: json.RawMessage(`"\u0000"`)}); !errors.Is(err, job.ErrInvalidInput) {
		t.Fatal("job failure not rejected", err)
	}
	var jobs, dispatches int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM job_dispatch)`).Scan(&jobs, &dispatches); err != nil || jobs != 0 || dispatches != 0 {
		t.Fatal("job failure committed intent", jobs, dispatches, err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE job_dispatch ADD CONSTRAINT test_reject_intent CHECK (false)`); err != nil {
		t.Fatal(err)
	}
	w := send(api(pool), "POST", "/api/v1/jobs", `{"type":"SLEEP","payload":{"duration_ms":0}}`)
	checkHTTPError(t, w, 500, "INTERNAL_ERROR")
	if strings.Contains(w.Body.String(), "test_reject_intent") {
		t.Fatal("driver details leaked")
	}
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM job_dispatch)`).Scan(&jobs, &dispatches); err != nil || jobs != 0 || dispatches != 0 {
		t.Fatal("outbox failure did not roll back job", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE job_dispatch DROP CONSTRAINT test_reject_intent`); err != nil {
		t.Fatal(err)
	}
	createSleep(t, pool, `{"duration_ms":0}`)
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM job_dispatch)`).Scan(&jobs, &dispatches); err != nil || jobs != 1 || dispatches != 1 {
		t.Fatal(jobs, dispatches, err)
	}
}

func TestPostOutboxRedisWorkerE2E(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, client, key, group := redisQueue(t)
	w := send(api(pool), "POST", "/api/v1/jobs", `{"type":"SLEEP","payload":{"duration_ms":5}}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var j job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.Status != job.Queued || j.AttemptCount != 0 {
		t.Fatal(j)
	}
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	msg := receive(t, q)
	if msg.JobID != j.ID.String() || msg.Version != "1" || msg.Malformed {
		t.Fatal(msg)
	}
	if _, err := execution.New(repo, q, execution.Sleep{}, testLogger()).Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != job.Succeeded || got.AttemptCount != 1 || got.StartedAt == nil || got.FinishedAt == nil || got.AssignedWorker == nil || got.FinishedAt.Before(*got.StartedAt) || !strings.Contains(string(got.Result), "slept") {
		t.Fatal(got)
	}
	if attemptCount(t, pool, j.ID) != 1 || pendingCount(t, client, key, group) != 0 {
		t.Fatal("attempt or ACK boundary")
	}
	var status string
	var finished *time.Time
	var result json.RawMessage
	if err := pool.QueryRow(t.Context(), `SELECT status,finished_at,result FROM job_attempts WHERE job_id=$1`, j.ID).Scan(&status, &finished, &result); err != nil || status != "SUCCEEDED" || finished == nil || string(result) != string(got.Result) {
		t.Fatal("attempt finalization", err)
	}
	if err := q.Publish(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.New(repo, q, execution.Sleep{}, testLogger()).Handle(t.Context(), receive(t, q)); err != nil {
		t.Fatal(err)
	}
	if attemptCount(t, pool, j.ID) != 1 {
		t.Fatal("terminal duplicate executed")
	}
}

func TestRedisOutageAndDispatcherRestart(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, _, _, _ := redisQueue(t)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	bad := redisinfra.NewClient("127.0.0.1:1", "", 0)
	defer bad.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := dispatch.New(repo, redisinfra.NewQueue(bad, "unused", "unused"), testLogger()).Once(ctx); err == nil {
		t.Fatal("outage pretended publication")
	}
	var marked *time.Time
	if err := pool.QueryRow(t.Context(), `SELECT published_at FROM job_dispatch WHERE job_id=$1`, j.ID).Scan(&marked); err != nil || marked != nil {
		t.Fatal("outage lost intent", err)
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Queued {
		t.Fatal("outage lost job", err)
	}
	// A new dispatcher instance recovers the database record, without memory state.
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.New(repo, q, execution.Sleep{}, testLogger()).Handle(t.Context(), receive(t, q)); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Succeeded {
		t.Fatal(got, err)
	}
}

func TestPublishMarkerFailureDuplicateAndRedisLoss(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, client, key, group := redisQueue(t)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_dispatch ADD CONSTRAINT test_marker_failure CHECK (published_at IS NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err == nil {
		t.Fatal("marker failure not surfaced")
	}
	if length, err := client.XLen(t.Context(), key).Result(); err != nil || length != 1 {
		t.Fatal("publish did not precede marker", length, err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_dispatch DROP CONSTRAINT test_marker_failure`); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	worker := execution.New(repo, q, execution.Sleep{}, testLogger())
	for range 2 {
		if _, err := worker.Handle(t.Context(), receive(t, q)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Succeeded || got.AttemptCount != 1 || attemptCount(t, pool, j.ID) != 1 || pendingCount(t, client, key, group) != 0 {
		t.Fatal("duplicate execution", got, err)
	}
	// Simulate losing ONLY this test stream before claim. Database reconciliation
	// republishes queued work even though the prior publication was marked.
	j2 := createSleep(t, pool, `{"duration_ms":0}`)
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(t.Context(), key).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE job_dispatch SET published_at=clock_timestamp()-INTERVAL '31 seconds' WHERE job_id=$1`, j2.ID); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Handle(t.Context(), receive(t, q)); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetByID(t.Context(), j2.ID)
	if err != nil || got.Status != job.Succeeded {
		t.Fatal("queued reconciliation failed", got, err)
	}
}

func TestAtomicClaimAndOwnerFinalize(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	var wg sync.WaitGroup
	claimed := make(chan *job.Job, 10)
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner := uuid.New()
			if err := repo.RegisterWorker(t.Context(), owner, 1); err != nil {
				errs <- err
				return
			}
			got, err := repo.Claim(t.Context(), j.ID, owner)
			if err == nil {
				claimed <- got
			} else if !errors.Is(err, job.ErrInvalidTransition) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(claimed)
	close(errs)
	if len(claimed) != 1 || len(errs) != 0 || attemptCount(t, pool, j.ID) != 1 {
		t.Fatal("CAS permitted duplicate claim", len(claimed), len(errs))
	}
	winner := <-claimed
	copy := *winner
	wrong := uuid.New()
	copy.AssignedWorker = &wrong
	if err := repo.Finalize(t.Context(), &copy, job.Succeeded, json.RawMessage(`{}`)); !errors.Is(err, job.ErrInvalidTransition) {
		t.Fatal("wrong owner accepted", err)
	}
	copy = *winner
	copy.AttemptCount++
	if err := repo.Finalize(t.Context(), &copy, job.Succeeded, json.RawMessage(`{}`)); !errors.Is(err, job.ErrInvalidTransition) {
		t.Fatal("wrong attempt accepted", err)
	}
	if err := repo.Finalize(t.Context(), winner, job.Succeeded, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finalize(t.Context(), winner, job.Failed, json.RawMessage(`{}`)); err == nil {
		t.Fatal("terminal transition accepted")
	}
}

func TestRunningPoisonUnsupportedAndInvalidSleep(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, client, key, group := redisQueue(t)
	worker := execution.New(repo, q, execution.Sleep{}, testLogger())
	j := createSleep(t, pool, `{"duration_ms":0}`)
	owner := uuid.New()
	if err := repo.RegisterWorker(t.Context(), owner, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(t.Context(), j.ID, owner); err != nil {
		t.Fatal(err)
	}
	if err := q.Publish(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Handle(t.Context(), receive(t, q)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Running || attemptCount(t, pool, j.ID) != 1 {
		t.Fatal("RUNNING duplicate executed", err)
	}
	for _, values := range []map[string]any{{"job_id": "bad", "v": "1"}, {"job_id": uuid.New().String(), "v": "1"}, {"job_id": uuid.New().String(), "v": "2"}, {"job_id": uuid.New().String(), "v": "1", "payload": "must-not-use"}, {"v": "1"}} {
		if err := client.XAdd(t.Context(), &redisclient.XAddArgs{Stream: key, Values: values}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.Handle(t.Context(), receive(t, q)); err != nil {
			t.Fatal("poison killed worker", err)
		}
	}
	for _, in := range []service.CreateInput{{Type: "unsupported", Payload: json.RawMessage(`{}`)}, {Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":10001}`)}} {
		j, err := service.New(repo).Create(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(t.Context(), j.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.Handle(t.Context(), receive(t, q)); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetByID(t.Context(), j.ID)
		if err != nil || got.Status != job.DeadLetter || got.Result == nil || got.AttemptCount != 1 {
			t.Fatal("deterministic failure missing", got, err)
		}
	}
	j = createSleep(t, pool, `{"duration_ms":0}`)
	if err := q.Publish(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Handle(t.Context(), receive(t, q)); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Succeeded || pendingCount(t, client, key, group) != 0 {
		t.Fatal("worker unhealthy after poison", err)
	}
}

func TestClaimAndFinalizeDatabaseFailures(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, client, key, group := redisQueue(t)
	worker := execution.New(repo, q, execution.Sleep{}, testLogger())
	j := createSleep(t, pool, `{"duration_ms":0}`)
	if err := q.Publish(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	msg := receive(t, q)
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_attempts ADD CONSTRAINT test_claim_failure CHECK (false)`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.Handle(t.Context(), msg); err == nil || claimed {
		t.Fatal("claim error pretended execution", err)
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Queued || got.AttemptCount != 0 || attemptCount(t, pool, j.ID) != 0 || pendingCount(t, client, key, group) != 1 {
		t.Fatal("claim transaction or ACK boundary", got, err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_attempts DROP CONSTRAINT test_claim_failure`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_attempts ADD CONSTRAINT test_finalize_failure CHECK (finished_at IS NULL)`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.Handle(t.Context(), msg); err == nil || !claimed {
		t.Fatal("finalize error pretended success", err)
	}
	got, err = repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Running || got.Result != nil || got.FinishedAt != nil || pendingCount(t, client, key, group) != 1 {
		t.Fatal("finalize rollback/ACK boundary", got, err)
	}
}

func TestWorkerGracefulIdleAndSleepShutdown(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "sleep"}[active], func(t *testing.T) {
			pool := migratedDatabase(t)
			repo := postgres.NewJobRepository(pool)
			q, _, _, _ := redisQueue(t)
			var j *job.Job
			if active {
				j = createSleep(t, pool, `{"duration_ms":10000}`)
				if err := q.Publish(t.Context(), j.ID); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- execution.New(repo, q, execution.Sleep{}, testLogger()).Run(ctx) }()
			if active {
				deadline := time.Now().Add(3 * time.Second)
				for {
					got, err := repo.GetByID(t.Context(), j.ID)
					if err != nil {
						t.Fatal(err)
					}
					if got.Status == job.Running {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("worker did not claim")
					}
					time.Sleep(5 * time.Millisecond)
				}
			} else {
				time.Sleep(30 * time.Millisecond)
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not complete")
			}
			if active {
				got, err := repo.GetByID(t.Context(), j.ID)
				if err != nil || got.Status != job.Retrying || got.RetryAt == nil || got.FinishedAt != nil || !strings.Contains(string(got.Result), "execution_cancelled") || attemptCount(t, pool, j.ID) != 1 {
					t.Fatal("shutdown failure not persisted", got, err)
				}
			}
		})
	}
}

func TestOutboxMigrationBackfillAndDown(t *testing.T) {
	pool := migratedDatabase(t)
	ctx := t.Context()
	// Remove execution control, retry and worker migrations before historical outbox down.
	if err := migrations.Run(ctx, pool, "down"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Run(ctx, pool, "down"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Run(ctx, pool, "down"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Run(ctx, pool, "down"); err != nil {
		t.Fatal(err)
	}
	var table *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('job_dispatch')::text`).Scan(&table); err != nil || table != nil {
		t.Fatal("outbox down failed", err)
	}
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,type,payload) VALUES($1,'SLEEP','{"duration_ms":0}')`, id); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Run(ctx, pool, "up"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_dispatch WHERE job_id=$1 AND published_at IS NULL`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("existing queued job backfill failed", err)
	}
	for range 6 {
		if err := migrations.Run(ctx, pool, "down"); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.Run(ctx, pool, "up"); err != nil {
		t.Fatal("full schema recreation failed", err)
	}
}

func TestPostgresUnavailableDoesNotPublishOrAck(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	q, client, key, group := redisQueue(t)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	pool.Close()
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err == nil {
		t.Fatal("unavailable PostgreSQL pretended publication")
	}
	if n, err := client.XLen(t.Context(), key).Result(); err != nil || n != 0 {
		t.Fatal("published without reading durable intent", n, err)
	}
	if err := q.Publish(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := execution.New(repo, q, execution.Sleep{}, testLogger()).Handle(t.Context(), receive(t, q)); err == nil || claimed {
		t.Fatal("unavailable PostgreSQL pretended execution", err)
	}
	if pendingCount(t, client, key, group) != 1 {
		t.Fatal("acknowledged without authoritative state")
	}
}
