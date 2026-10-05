package integration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	domainworker "github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	redisinfra "github.com/Daniel-Cpz/FlowForge/internal/infrastructure/redis"
	"github.com/Daniel-Cpz/FlowForge/internal/service/dispatch"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
	"github.com/Daniel-Cpz/FlowForge/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func shortLease() domainworker.LeasePolicy {
	return domainworker.LeasePolicy{LeaseDuration: 3 * time.Second, RenewInterval: time.Second, HeartbeatInterval: time.Second, OfflineAfter: 4 * time.Second, RecoveryInterval: time.Second}
}
func leaseRepo(t *testing.T, pool *pgxpool.Pool) *postgres.JobRepository {
	t.Helper()
	repo, err := postgres.NewJobRepositoryWithPolicy(pool, shortLease())
	if err != nil {
		t.Fatal(err)
	}
	return repo
}
func registered(t *testing.T, repo *postgres.JobRepository) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := repo.RegisterWorker(t.Context(), id, 2); err != nil {
		t.Fatal(err)
	}
	return id
}
func expire(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET lease_expiry=clock_timestamp()-INTERVAL '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseRecoveryConcurrentFencingAndHistory(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	a, b := registered(t, repo), registered(t, repo)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	// max_attempts is stored metadata until Phase 5 accounting, not a limit
	// that can permanently strand a crashed RUNNING job during this phase.
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET max_attempts=1 WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	old, err := repo.Claim(t.Context(), j.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	var dbNow time.Time
	if err := pool.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	if old.LeaseExpiry == nil || old.LeaseExpiry.Before(dbNow) || old.LeaseExpiry.Sub(dbNow) > 3*time.Second {
		t.Fatal("claim has no DB lease")
	}
	renewed, err := repo.Renew(t.Context(), old)
	if err != nil || !renewed.After(*old.LeaseExpiry) {
		t.Fatal("renew did not extend", err)
	}
	wrong := *old
	wrong.AssignedWorker = &b
	if _, err := repo.Renew(t.Context(), &wrong); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("wrong owner renew", err)
	}
	wrong = *old
	wrong.AttemptCount++
	if _, err := repo.Renew(t.Context(), &wrong); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("wrong attempt renew", err)
	}
	if records, err := repo.RecoverExpired(t.Context(), 100); err != nil || len(records) != 0 {
		t.Fatal("unexpired recovered", err)
	}
	expire(t, pool, j.ID)
	if _, err := repo.Renew(t.Context(), old); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("expired renewed", err)
	}
	if err := repo.Finalize(t.Context(), old, job.Succeeded, json.RawMessage(`{}`)); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("expired finalized", err)
	}
	var wg sync.WaitGroup
	records := make(chan []job.RecoveredAttempt, 8)
	errs := make(chan error, 8)
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; r, e := repo.RecoverExpired(t.Context(), 1); records <- r; errs <- e }()
	}
	close(start)
	wg.Wait()
	close(records)
	close(errs)
	total := 0
	for r := range records {
		total += len(r)
		if len(r) == 1 && (r[0].WorkerID != a || r[0].AttemptNumber != 1) {
			t.Fatal("recovery metadata")
		}
	}
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if total != 1 {
		t.Fatal("multiple effective recoveries", total)
	}
	queued, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || queued.Status != job.Queued || queued.AttemptCount != 1 || queued.LeaseExpiry != nil || queued.AssignedWorker != nil {
		t.Fatal("not safely requeued", err)
	}
	var marked *time.Time
	if err := pool.QueryRow(t.Context(), `SELECT published_at FROM job_dispatch WHERE job_id=$1`, j.ID).Scan(&marked); err != nil || marked != nil {
		t.Fatal("intent not restored", err)
	}
	newer, err := repo.Claim(t.Context(), j.ID, b)
	if err != nil || newer.AttemptCount != 2 {
		t.Fatal("new attempt missing", err)
	}
	if _, err := repo.Renew(t.Context(), old); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("old lease revived", err)
	}
	if err := repo.Finalize(t.Context(), old, job.Succeeded, json.RawMessage(`{"late":true}`)); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("stale finalize", err)
	}
	if err := repo.Finalize(t.Context(), newer, job.Succeeded, json.RawMessage(`{"new_owner":true}`)); err != nil {
		t.Fatal(err)
	}
	var status, errorCode string
	var finished *time.Time
	var result json.RawMessage
	if err := pool.QueryRow(t.Context(), `SELECT status,error,finished_at,result FROM job_attempts WHERE job_id=$1 AND attempt_number=1`, j.ID).Scan(&status, &errorCode, &finished, &result); err != nil || status != "FAILED" || errorCode != "lease_expired" || finished == nil || string(result) != `{"error": "lease_expired"}` {
		t.Fatal("old attempt overwritten", status, errorCode, err)
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Succeeded || string(got.Result) != `{"new_owner": true}` || attemptCount(t, pool, j.ID) != 2 {
		t.Fatal("new owner result lost", err)
	}
}

func TestLeaseRecoveryIntentFailureRollsBackAndMissingAttempt(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	a := registered(t, repo)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := repo.Claim(t.Context(), j.ID, a); err != nil {
		t.Fatal(err)
	}
	expire(t, pool, j.ID)
	if _, err := pool.Exec(t.Context(), `UPDATE job_dispatch SET published_at=clock_timestamp(); ALTER TABLE job_dispatch ADD CONSTRAINT test_recovery_intent CHECK(published_at IS NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecoverExpired(t.Context(), 100); err == nil {
		t.Fatal("intent failure reported recovery")
	}
	var running int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM job_attempts WHERE status='RUNNING' AND finished_at IS NULL`).Scan(&running); err != nil || running != 1 {
		t.Fatal("attempt closure escaped rollback", err)
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Running {
		t.Fatal("job escaped rollback", err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_dispatch DROP CONSTRAINT test_recovery_intent; DELETE FROM job_attempts`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecoverExpired(t.Context(), 100); !errors.Is(err, job.ErrInvalidStoredData) {
		t.Fatal("corrupt history accepted", err)
	}
	got, err = repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Running {
		t.Fatal("corrupt recovery changed job", err)
	}
	for _, n := range []int{0, 101} {
		if _, err := repo.RecoverExpired(t.Context(), n); !errors.Is(err, job.ErrInvalidInput) {
			t.Fatal("unbounded recovery")
		}
		if _, err := repo.DetectOffline(t.Context(), n); !errors.Is(err, job.ErrInvalidInput) {
			t.Fatal("unbounded registry scan")
		}
	}
}

func TestRegistryOfflineCannotReviveOrClaim(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	id := registered(t, repo)
	if err := repo.Heartbeat(t.Context(), id, 2); err != nil {
		t.Fatal(err)
	}
	if err := repo.Heartbeat(t.Context(), id, 3); err == nil {
		t.Fatal("active count exceeds capacity")
	}
	var status string
	var active int
	if err := pool.QueryRow(t.Context(), `SELECT status,active_jobs FROM workers WHERE worker_id=$1`, id).Scan(&status, &active); err != nil || status != "BUSY" || active != 2 {
		t.Fatal("registry persistence", err)
	}
	j := createSleep(t, pool, `{"duration_ms":0}`)
	owned, err := repo.Claim(t.Context(), j.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE workers SET last_heartbeat=clock_timestamp()-INTERVAL '5 seconds' WHERE worker_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	// A paused worker cannot revive its registration even before an offline scan.
	if err := repo.Heartbeat(t.Context(), id, 0); !errors.Is(err, domainworker.ErrOffline) {
		t.Fatal("stale heartbeat revived", err)
	}
	ids, err := repo.DetectOffline(t.Context(), 100)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatal("offline transition", err)
	}
	if ids, err := repo.DetectOffline(t.Context(), 100); err != nil || len(ids) != 0 {
		t.Fatal("repeated offline", err)
	}
	if err := repo.RegisterWorker(t.Context(), id, 1); !errors.Is(err, domainworker.ErrOffline) {
		t.Fatal("reused identity revived", err)
	}
	if err := repo.Heartbeat(t.Context(), id, 0); !errors.Is(err, domainworker.ErrOffline) {
		t.Fatal("offline heartbeat revived", err)
	}
	if _, err := repo.Renew(t.Context(), owned); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("offline renewed", err)
	}
	j2 := createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := repo.Claim(t.Context(), j2.ID, id); err == nil {
		t.Fatal("offline claimed")
	}
	if err := repo.StopWorker(t.Context(), id, "graceful_shutdown"); !errors.Is(err, domainworker.ErrOffline) {
		t.Fatal(err)
	}
	var reason string
	if err := pool.QueryRow(t.Context(), `SELECT offline_reason FROM workers WHERE worker_id=$1`, id).Scan(&reason); err != nil || reason != "heartbeat_expired" {
		t.Fatal("crash evidence overwritten", err)
	}
	fresh := registered(t, repo)
	if err := repo.MarkDraining(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(t.Context(), j2.ID, fresh); err == nil {
		t.Fatal("draining claimed")
	}
	if err := repo.StopWorker(t.Context(), fresh, "graceful_shutdown"); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveredDispatchPublishMarkerFailureAndNotificationLoss(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	id := registered(t, repo)
	q, client, key, group := redisQueue(t)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := repo.Claim(t.Context(), j.ID, id); err != nil {
		t.Fatal(err)
	}
	expire(t, pool, j.ID)
	if r, err := repo.RecoverExpired(t.Context(), 100); err != nil || len(r) != 1 {
		t.Fatal(err)
	}
	bad := redisinfra.NewClient("127.0.0.1:1", "", 0)
	defer bad.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := dispatch.New(repo, redisinfra.NewQueue(bad, "unused", "unused"), testLogger()).Once(ctx); err == nil {
		t.Fatal("recovered publish false success")
	}
	var marked *time.Time
	if err := pool.QueryRow(t.Context(), `SELECT published_at FROM job_dispatch WHERE job_id=$1`, j.ID).Scan(&marked); err != nil || marked != nil {
		t.Fatal("recovered intent lost", err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_dispatch ADD CONSTRAINT test_recovered_marker CHECK(published_at IS NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err == nil {
		t.Fatal("marker failure hidden")
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_dispatch DROP CONSTRAINT test_recovered_marker`); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(t.Context(), key).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE job_dispatch SET published_at=clock_timestamp()-INTERVAL '31 seconds' WHERE job_id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.New(repo, q, execution.Sleep{}, testLogger()).Handle(t.Context(), receive(t, q)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Succeeded || got.AttemptCount != 2 || pendingCount(t, client, key, group) != 0 {
		t.Fatal("recovered delivery lost", err)
	}
}

func TestLeaseRenewalBeyondOriginalExpiryAndGracefulStop(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	q, _, _, _ := redisQueue(t)
	j := createSleep(t, pool, `{"duration_ms":4200}`)
	if err := q.Publish(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	w, err := execution.NewWithLeasePolicy(repo, q, execution.Sleep{}, testLogger(), 1, shortLease())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(8 * time.Second)
	for {
		got, err := repo.GetByID(t.Context(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == job.Succeeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lease renewal did not sustain SLEEP")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	join, stop := context.WithTimeout(t.Context(), 4*time.Second)
	defer stop()
	if err := awaitIntegration(t, join, done); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := pool.QueryRow(t.Context(), `SELECT status,offline_reason FROM workers`).Scan(&status, &reason); err != nil || status != "OFFLINE" || reason != "graceful_shutdown" {
		t.Fatal("graceful treated as crash", err)
	}
	if attemptCount(t, pool, j.ID) != 1 {
		t.Fatal("renewed job recovered")
	}
}

func TestFinalizeExpiryRecoveryRace(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	owner := registered(t, repo)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	old, err := repo.Claim(t.Context(), j.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	expire(t, pool, j.ID)
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		done <- repo.Finalize(t.Context(), old, job.Failed, json.RawMessage(`{"error":"execution_cancelled"}`))
	}()
	go func() { <-start; _, err := repo.RecoverExpired(t.Context(), 1); done <- err }()
	close(start)
	errs := []error{<-done, <-done}
	lost := 0
	for _, err := range errs {
		if errors.Is(err, job.ErrLeaseLost) {
			lost++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if lost != 1 {
		t.Fatal("expired graceful finalize accepted")
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Queued {
		t.Fatal("expiry race stranded job", err)
	}
}

func TestLeaseDatabaseUnavailableDoesNotFabricateSuccess(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	id := registered(t, repo)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	owned, err := repo.Claim(t.Context(), j.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if err := repo.Heartbeat(t.Context(), id, 1); err == nil {
		t.Fatal("heartbeat fabricated")
	}
	if _, err := repo.Renew(t.Context(), owned); err == nil {
		t.Fatal("renew fabricated")
	}
	if _, err := repo.RecoverExpired(t.Context(), 100); err == nil {
		t.Fatal("recovery fabricated")
	}
	if _, err := repo.DetectOffline(t.Context(), 100); err == nil {
		t.Fatal("offline fabricated")
	}
}

func TestWorkerLeaseMigrationBackfillAndDown(t *testing.T) {
	pool := migratedDatabase(t)
	ctx := t.Context()
	if err := migrations.Run(ctx, pool, "down"); err != nil {
		t.Fatal(err)
	}
	owner, id := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,type,payload,status,assigned_worker,attempt_count,started_at) VALUES($1,'SLEEP','{}','RUNNING',$2,1,clock_timestamp());`, id, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO job_attempts(id,job_id,worker_id,attempt_number,status,started_at) VALUES($1,$2,$3,1,'RUNNING',clock_timestamp())`, uuid.New(), id, owner); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Run(ctx, pool, "up"); err != nil {
		t.Fatal(err)
	}
	repo := leaseRepo(t, pool)
	if recovered, err := repo.RecoverExpired(ctx, 100); err != nil || len(recovered) != 1 {
		t.Fatal("legacy running not recoverable", err)
	}
	if err := migrations.Run(ctx, pool, "down"); err != nil {
		t.Fatal(err)
	}
	var table *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('workers')::text`).Scan(&table); err != nil || table != nil {
		t.Fatal("workers down", err)
	}
	if attemptCount(t, pool, id) != 1 {
		t.Fatal("down destroyed attempts")
	}
	if err := migrations.Run(ctx, pool, "up"); err != nil {
		t.Fatal(err)
	}
}
