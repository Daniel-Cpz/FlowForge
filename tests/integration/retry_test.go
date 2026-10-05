package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	"github.com/Daniel-Cpz/FlowForge/internal/service/dispatch"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
	"testing"
	"time"
)

// Test-only DB clock manipulation avoids sleeping in repository race tests.
func forceDue(t *testing.T, pool *pgxpool.Pool, repo *postgres.JobRepository, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET retry_at=clock_timestamp()-INTERVAL '1 second' WHERE id=$1 AND status='RETRYING'`, id); err != nil {
		t.Fatal(err)
	}
	if ids, err := repo.PromoteRetries(t.Context(), 100); err != nil || len(ids) != 1 {
		t.Fatal("due retry not promoted", ids, err)
	}
}

type transientExecutor struct{}

func (transientExecutor) Execute(_ context.Context, j *job.Job) execution.Outcome {
	if j.AttemptCount == 1 {
		return execution.Outcome{Status: job.Failed, Result: json.RawMessage(`{"error":"transient_test_failure"}`), Failure: job.Failure{Class: job.Retryable, Code: "transient_test_failure"}}
	}
	return execution.Sleep{}.Execute(context.Background(), j)
}

func TestRetrySchedulingFailureRetainsDeliveryAndRollsBackAttempt(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	q, client, key, group := redisQueue(t)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	if _, e := pool.Exec(t.Context(), `ALTER TABLE jobs ADD CONSTRAINT reject_retry_schedule CHECK(status<>'RETRYING')`); e != nil {
		t.Fatal(e)
	}
	if e := dispatch.New(repo, q, testLogger()).Once(t.Context()); e != nil {
		t.Fatal(e)
	}
	if claimed, e := execution.New(repo, q, transientExecutor{}, testLogger()).Handle(t.Context(), receive(t, q)); e == nil || !claimed {
		t.Fatal("retry transaction failure hidden", e)
	}
	got, e := repo.GetByID(t.Context(), j.ID)
	if e != nil || got.Status != job.Running || got.RetryAt != nil || pendingCount(t, client, key, group) != 1 {
		t.Fatal("failed retry ACKed or changed state", e)
	}
	var running int
	if e := pool.QueryRow(t.Context(), `SELECT count(*) FROM job_attempts WHERE job_id=$1 AND status='RUNNING' AND finished_at IS NULL AND error IS NULL`, j.ID).Scan(&running); e != nil || running != 1 {
		t.Fatal("attempt finalization escaped rollback", e)
	}
	if _, e := pool.Exec(t.Context(), `ALTER TABLE jobs DROP CONSTRAINT reject_retry_schedule`); e != nil {
		t.Fatal(e)
	}
	expire(t, pool, j.ID)
	if records, e := repo.RecoverExpired(t.Context(), 1); e != nil || len(records) != 1 {
		t.Fatal("failure not recoverable", e)
	}
	pool.Close()
	if e := repo.Finalize(t.Context(), got, job.Failed, json.RawMessage(`{}`), job.Failure{Class: job.Retryable, Code: "transient_failure"}); e == nil {
		t.Fatal("unavailable DB fabricated retry")
	}
}
func TestDurableRetryRedisRestartAndACK(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	q, client, key, group := redisQueue(t)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	if err := dispatch.New(repo, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := execution.New(repo, q, transientExecutor{}, testLogger())
	if _, err := first.Handle(t.Context(), receive(t, q)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Retrying || got.RetryAt == nil || got.AttemptCount != 1 || got.AssignedWorker != nil || pendingCount(t, client, key, group) != 0 {
		t.Fatal("retry/ACK boundary", got, err)
	}
	if ids, err := repo.PromoteRetries(t.Context(), 100); err != nil || len(ids) != 0 {
		t.Fatal("early promotion", err)
	}
	if pending, err := repo.PendingDispatch(t.Context(), 100); err != nil || len(pending) != 0 {
		t.Fatal("retry published early", err)
	}
	// Simulated process restart reads the durable schedule, no in-memory timer.
	restarted := leaseRepo(t, pool)
	forceDue(t, pool, restarted, j.ID)
	if err := client.Del(t.Context(), key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(restarted, q, testLogger()).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.New(restarted, q, transientExecutor{}, testLogger()).Handle(t.Context(), receive(t, q)); err != nil {
		t.Fatal(err)
	}
	got, err = restarted.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Succeeded || got.AttemptCount != 2 || got.RetryAt != nil || attemptCount(t, pool, j.ID) != 2 {
		t.Fatal("retry not completed", got, err)
	}
	var failures, successes int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE status='FAILED' AND error='transient_test_failure'),count(*) FILTER(WHERE status='SUCCEEDED') FROM job_attempts WHERE job_id=$1`, j.ID).Scan(&failures, &successes); err != nil || failures != 1 || successes != 1 {
		t.Fatal("attempt history", err)
	}
}
func TestRetryBudgetExactExhaustionIncludingLeases(t *testing.T) {
	for _, budget := range []int{1, 3} {
		for _, crash := range []bool{false, true} {
			t.Run(uuid.NewString(), func(t *testing.T) {
				pool := migratedDatabase(t)
				repo := leaseRepo(t, pool)
				owner := registered(t, repo)
				j := createSleep(t, pool, `{"duration_ms":0}`)
				if _, e := pool.Exec(t.Context(), `UPDATE jobs SET max_attempts=$2 WHERE id=$1`, j.ID, budget); e != nil {
					t.Fatal(e)
				}
				for n := 1; n <= budget; n++ {
					current, e := repo.Claim(t.Context(), j.ID, owner)
					if e != nil || current.AttemptCount != n {
						t.Fatal("claim budget", e)
					}
					if crash {
						expire(t, pool, j.ID)
						records, e := repo.RecoverExpired(t.Context(), 1)
						if e != nil || len(records) != 1 {
							t.Fatal(e)
						}
					} else {
						if e := repo.Finalize(t.Context(), current, job.Failed, json.RawMessage(`{"error":"transient_failure"}`), job.Failure{Class: job.Retryable, Code: "transient_failure"}); e != nil {
							t.Fatal(e)
						}
					}
					got, e := repo.GetByID(t.Context(), j.ID)
					if e != nil {
						t.Fatal(e)
					}
					if n < budget {
						if got.Status != job.Retrying {
							t.Fatal(got)
						}
						forceDue(t, pool, repo, j.ID)
					} else {
						if got.Status != job.DeadLetter || got.RetryAt != nil {
							t.Fatal(got)
						}
					}
				}
				if _, e := repo.Claim(t.Context(), j.ID, owner); !errors.Is(e, job.ErrInvalidTransition) {
					t.Fatal("N+1 attempt", e)
				}
				if ids, e := repo.PromoteRetries(t.Context(), 100); e != nil || len(ids) != 0 || attemptCount(t, pool, j.ID) != budget {
					t.Fatal("budget exceeded", e)
				}
			})
		}
	}
}
func TestRetryPromotionRaceAndIntentRollback(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	owner := registered(t, repo)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	current, e := repo.Claim(t.Context(), j.ID, owner)
	if e != nil {
		t.Fatal(e)
	}
	if e := repo.Finalize(t.Context(), current, job.Failed, json.RawMessage(`{}`), job.Failure{Class: job.Retryable, Code: "transient_failure"}); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(t.Context(), `UPDATE jobs SET retry_at=clock_timestamp()-INTERVAL '1 second'; UPDATE job_dispatch SET published_at=clock_timestamp(); ALTER TABLE job_dispatch ADD CONSTRAINT fail_retry_intent CHECK(published_at IS NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	if _, e := repo.PromoteRetries(t.Context(), 100); e == nil {
		t.Fatal("intent rollback hidden")
	}
	got, e := repo.GetByID(t.Context(), j.ID)
	if e != nil || got.Status != job.Retrying || got.RetryAt == nil || got.AttemptCount != 1 {
		t.Fatal("promotion escaped rollback", e)
	}
	if _, e := pool.Exec(t.Context(), `ALTER TABLE job_dispatch DROP CONSTRAINT fail_retry_intent`); e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	results := make(chan int, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ids, e := repo.PromoteRetries(t.Context(), 1)
			results <- len(ids)
			errs <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	total := 0
	for n := range results {
		total += n
	}
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if total != 1 || attemptCount(t, pool, j.ID) != 1 {
		t.Fatal("multiple promotions", total)
	}
	for _, n := range []int{0, 101} {
		if _, e := repo.PromoteRetries(t.Context(), n); !errors.Is(e, job.ErrInvalidInput) {
			t.Fatal("unbounded promoter")
		}
	}
	pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, e := repo.PromoteRetries(ctx, 1); e == nil {
		t.Fatal("unavailable DB fabricated promotion")
	}
}
