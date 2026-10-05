package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/service/dispatch"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	transport "github.com/Daniel-Cpz/FlowForge/internal/transport/http"
	"github.com/Daniel-Cpz/FlowForge/migrations"
	"github.com/google/uuid"
)

func TestPriorityAuthorityDeferredDeliveryAndNonPreemption(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	ctx := t.Context()
	a, b := registered(t, repo), registered(t, repo)
	low := createSleep(t, pool, `{"duration_ms":0}`)
	q, client, key, group := redisQueue(t)
	if err := dispatch.New(repo, q, testLogger()).Once(ctx); err != nil {
		t.Fatal(err)
	}
	stale := receive(t, q) // Low notification already read before new high enqueue.
	high, err := service.New(repo).Create(ctx, service.CreateInput{Type: "SLEEP", Payload: []byte(`{"duration_ms":0}`), Priority: 100})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := execution.New(repo, q, execution.Sleep{}, testLogger()).Handle(ctx, stale); err != nil || claimed {
		t.Fatal("low crossed priority barrier", err)
	}
	if attemptCount(t, pool, low.ID) != 0 || pendingCount(t, client, key, group) != 0 {
		t.Fatal("deferral consumed budget or notification not bounded")
	}
	// Repeated direct low claims demonstrate documented starvation under backlog.
	for range 5 {
		if _, err := repo.Claim(ctx, low.ID, a); !errors.Is(err, job.ErrPriorityDeferred) {
			t.Fatal(err)
		}
	}
	first, err := repo.Claim(ctx, high.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	runningLow, err := repo.Claim(ctx, low.ID, b)
	if err != nil {
		t.Fatal(err)
	}
	later, err := service.New(repo).Create(ctx, service.CreateInput{Type: "SLEEP", Payload: []byte(`{"duration_ms":0}`), Priority: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetByID(ctx, low.ID); err != nil || got.Status != job.Running {
		t.Fatal("preempted", err)
	}
	// Multiple workers may win distinct highest-priority Jobs, never double claim.
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, err := repo.Claim(ctx, later.ID, b); results <- err })
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, job.ErrInvalidTransition) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal(wins)
	}
	if err := repo.Finalize(ctx, first, job.Failed, []byte(`{}`), job.Failure{Class: job.Retryable, Code: "test_retry"}); err != nil {
		t.Fatal(err)
	}
	forceDue(t, pool, repo, high.ID)
	another := createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := repo.Claim(ctx, another.ID, a); !errors.Is(err, job.ErrPriorityDeferred) {
		t.Fatal("promoted high bypassed", err)
	}
	if err := repo.Finalize(ctx, runningLow, job.Succeeded, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPriorityStableTiesAndConcurrentHighLow(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	ctx := t.Context()
	a, b := registered(t, repo), registered(t, repo)
	one, two := createSleep(t, pool, `{"duration_ms":0}`), createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET created_at='2026-10-05T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if one.ID.String() > two.ID.String() {
		one, two = two, one
	}
	ids, err := repo.PendingDispatch(ctx, 100)
	if err != nil || len(ids) != 2 || ids[0] != one.ID {
		t.Fatal(ids, err)
	}
	if _, err := repo.Claim(ctx, two.ID, a); !errors.Is(err, job.ErrPriorityDeferred) {
		t.Fatal("tie order", err)
	}
	if _, err := repo.Claim(ctx, one.ID, a); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, two.ID, b); err != nil {
		t.Fatal(err)
	}
	high, err := service.New(repo).Create(ctx, service.CreateInput{Type: "SLEEP", Payload: []byte(`{"duration_ms":0}`), Priority: 99})
	if err != nil {
		t.Fatal(err)
	}
	low := createSleep(t, pool, `{"duration_ms":0}`)
	var wg sync.WaitGroup
	wg.Go(func() {
		_, err := repo.Claim(ctx, high.ID, a)
		if err != nil {
			t.Error(err)
		}
	})
	wg.Go(func() {
		j, err := repo.Claim(ctx, low.ID, b)
		if err == nil {
			var state string
			if e := pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, high.ID).Scan(&state); e != nil || state != "RUNNING" {
				t.Error("low won before high", e)
			}
			_ = j
		} else if !errors.Is(err, job.ErrPriorityDeferred) {
			t.Error(err)
		}
	})
	wg.Wait()
}

type timeoutThenSuccess struct{}

func (timeoutThenSuccess) Execute(ctx context.Context, j *job.Job) execution.Outcome {
	if j.AttemptCount == 1 {
		<-ctx.Done()
		return execution.Outcome{Status: job.Succeeded, Result: []byte(`{}`)}
	}
	return execution.Sleep{}.Execute(ctx, j)
}

func TestAttemptTimeoutBudgetRetryAndPersistenceFailure(t *testing.T) {
	for _, budget := range []int{1, 2} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			pool := migratedDatabase(t)
			repo := leaseRepo(t, pool)
			q, client, key, group := redisQueue(t)
			ctx := t.Context()
			j, err := service.New(repo).Create(ctx, service.CreateInput{Type: "SLEEP", Payload: []byte(`{"duration_ms":1500}`), Timeout: ptrInt(1), MaxAttempts: &budget})
			if err != nil {
				t.Fatal(err)
			}
			if err := dispatch.New(repo, q, testLogger()).Once(ctx); err != nil {
				t.Fatal(err)
			}
			w, err := execution.NewWithLeasePolicy(repo, q, timeoutThenSuccess{}, testLogger(), 1, shortLease())
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if claimed, err := w.Handle(ctx, receive(t, q)); err != nil || !claimed {
				t.Fatal(err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("deadline ignored")
			}
			got, err := repo.GetByID(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := job.DeadLetter
			if budget == 2 {
				want = job.Retrying
			}
			if got.Status != want || got.AttemptCount != 1 || pendingCount(t, client, key, group) != 0 {
				t.Fatal(got)
			}
			attempts, err := repo.Attempts(ctx, j.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != job.TimedOut || attempts[0].FinishedAt == nil || *attempts[0].Error != "execution_timeout" {
				t.Fatal(attempts, err)
			}
			if budget == 2 {
				forceDue(t, pool, repo, j.ID)
				if _, err := pool.Exec(ctx, `UPDATE jobs SET payload='{"duration_ms":0}' WHERE id=$1`, j.ID); err != nil {
					t.Fatal(err)
				}
				if err := dispatch.New(repo, q, testLogger()).Once(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := w.Handle(ctx, receive(t, q)); err != nil {
					t.Fatal(err)
				}
				got, err = repo.GetByID(ctx, j.ID)
				if err != nil || got.Status != job.Succeeded || got.AttemptCount != 2 {
					t.Fatal(got, err)
				}
			}
			if _, err := repo.Claim(ctx, j.ID, registered(t, repo)); !errors.Is(err, job.ErrInvalidTransition) {
				t.Fatal("N+1", err)
			}
		})
	}
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	current, err := repo.Claim(t.Context(), j.ID, registered(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE job_attempts ADD CONSTRAINT reject_timeout CHECK(status<>'TIMED_OUT')`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finalize(t.Context(), current, job.TimedOut, []byte(`{"error":"execution_timeout"}`), job.Failure{Class: job.Retryable, Code: "execution_timeout"}); err == nil {
		t.Fatal("failed transaction fabricated timeout")
	}
	got, err := repo.GetByID(t.Context(), j.ID)
	if err != nil || got.Status != job.Running || got.RetryAt != nil {
		t.Fatal(got, err)
	}
	expire(t, pool, j.ID)
	if _, err := repo.RecoverExpired(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finalize(t.Context(), current, job.TimedOut, []byte(`{}`), job.Failure{Class: job.Retryable, Code: "execution_timeout"}); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("stale timeout", err)
	}
}

func TestCancelConcurrentWithExpiryAndFailureRollback(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	ctx := t.Context()
	for range 10 {
		j := createSleep(t, pool, `{"duration_ms":0}`)
		current, err := repo.Claim(ctx, j.ID, registered(t, repo))
		if err != nil {
			t.Fatal(err)
		}
		expire(t, pool, j.ID)
		var wg sync.WaitGroup
		wg.Go(func() {
			if _, err := repo.Cancel(ctx, j.ID); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, err := repo.RecoverExpired(ctx, 100); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := repo.Finalize(ctx, current, job.Succeeded, []byte(`{}`)); !errors.Is(err, job.ErrLeaseLost) {
				t.Error(err)
			}
		})
		wg.Wait()
		// SKIP LOCKED may skip the row while Cancel holds it. The next bounded
		// maintenance pass must converge after the durable request commits.
		if _, err := repo.RecoverExpired(ctx, 100); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetByID(ctx, j.ID)
		if err != nil || got.Status != job.Cancelled || got.RetryAt != nil || attemptCount(t, pool, j.ID) != 1 {
			t.Fatal(got, err)
		}
	}
	queued := createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := pool.Exec(ctx, `ALTER TABLE jobs ADD CONSTRAINT reject_cancel CHECK(status<>'CANCELLED') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Cancel(ctx, queued.ID); err == nil {
		t.Fatal("cancel DB failure hidden")
	}
	got, err := repo.GetByID(ctx, queued.ID)
	if err != nil || got.Status != job.Queued || got.CancelRequestedAt != nil {
		t.Fatal("cancel escaped rollback", got, err)
	}
	var intents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_dispatch WHERE job_id=$1`, queued.ID).Scan(&intents); err != nil || intents != 1 {
		t.Fatal(intents, err)
	}
}

func TestRedriveRejoinsPriorityAndFencesOldAttempt(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	ctx := t.Context()
	owner := registered(t, repo)
	low := createSleep(t, pool, `{"duration_ms":0}`)
	high, err := service.New(repo).Create(ctx, service.CreateInput{Type: "SLEEP", Priority: 100, Payload: []byte(`{"duration_ms":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	current, err := repo.Claim(ctx, high.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	stale := *current
	if err := repo.Finalize(ctx, current, job.Failed, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RedriveDeadLetter(ctx, high.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, low.ID, owner); !errors.Is(err, job.ErrPriorityDeferred) {
		t.Fatal("redriven priority bypass", err)
	}
	next, err := repo.Claim(ctx, high.ID, owner)
	if err != nil || next.AttemptCount != 2 {
		t.Fatal(next, err)
	}
	if err := repo.Finalize(ctx, &stale, job.Succeeded, []byte(`{}`)); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatal("old redrive owner crossed fence", err)
	}
	if err := repo.Finalize(ctx, next, job.Succeeded, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestTimeoutFinalizeFailureDoesNotACK(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	ctx := t.Context()
	q, client, key, group := redisQueue(t)
	j, err := service.New(repo).Create(ctx, service.CreateInput{Type: "SLEEP", Payload: []byte(`{"duration_ms":2000}`), Timeout: ptrInt(1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE job_attempts ADD CONSTRAINT reject_timeout CHECK(status<>'TIMED_OUT')`); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(repo, q, testLogger()).Once(ctx); err != nil {
		t.Fatal(err)
	}
	w, err := execution.NewWithLeasePolicy(repo, q, execution.Sleep{}, testLogger(), 1, shortLease())
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := w.Handle(ctx, receive(t, q)); err == nil || !claimed {
		t.Fatal("false timeout success", err)
	}
	got, err := repo.GetByID(ctx, j.ID)
	if err != nil || got.Status != job.Running || pendingCount(t, client, key, group) != 1 {
		t.Fatal("failed timeout ACKed", got, err)
	}
	history, err := repo.Attempts(ctx, j.ID)
	if err != nil || len(history) != 1 || history[0].Status != job.Running || history[0].FinishedAt != nil {
		t.Fatal(history, err)
	}
}
func ptrInt(n int) *int { return &n }

func TestCancellationQueuedRetryingAndHTTPContract(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	ctx := t.Context()
	h := transport.NewRouter(service.New(repo), testLogger())
	q, _, _, _ := redisQueue(t)
	queued := createSleep(t, pool, `{"duration_ms":0}`)
	if err := dispatch.New(repo, q, testLogger()).Once(ctx); err != nil {
		t.Fatal(err)
	}
	old := receive(t, q)
	for range 2 {
		r := send(h, "POST", "/api/v1/jobs/"+queued.ID.String()+"/cancel", "")
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if claimed, err := execution.New(repo, q, execution.Sleep{}, testLogger()).Handle(ctx, old); err != nil || claimed || attemptCount(t, pool, queued.ID) != 0 {
		t.Fatal("cancelled delivery ran", err)
	}
	var intents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_dispatch WHERE job_id=$1`, queued.ID).Scan(&intents); err != nil || intents != 0 {
		t.Fatal(intents, err)
	}
	retry := createSleep(t, pool, `{"duration_ms":0}`)
	current, err := repo.Claim(ctx, retry.ID, registered(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Finalize(ctx, current, job.Failed, []byte(`{}`), job.Failure{Class: job.Retryable, Code: "test_transient"}); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Cancel(ctx, retry.ID); err != nil || got.Status != job.Cancelled || got.RetryAt != nil {
		t.Fatal(got, err)
	}
	if ids, err := repo.PromoteRetries(ctx, 100); err != nil || len(ids) != 0 {
		t.Fatal(ids, err)
	}
	for _, path := range []string{"cancel", "retry", "attempts"} {
		method := "POST"
		if path == "attempts" {
			method = "GET"
		}
		checkHTTPError(t, send(h, method, "/api/v1/jobs/"+uuid.NewString()+"/"+path, ""), 404, "JOB_NOT_FOUND")
	}
	checkHTTPError(t, send(h, "POST", "/api/v1/jobs/bad/cancel", ""), 400, "INVALID_ID")
	checkHTTPError(t, send(h, "POST", "/api/v1/jobs/"+queued.ID.String()+"/cancel", "{}"), 400, "INVALID_INPUT")
	list := send(h, "GET", "/api/v1/jobs/"+queued.ID.String()+"/attempts", "")
	if list.Code != 200 || list.Body.String() != "{\"attempts\":[]}\n" {
		t.Fatal(list.Body.String())
	}
}

func TestRunningCancellationCooperativeAndRecoveryRaces(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	ctx := t.Context()
	q, _, _, _ := redisQueue(t)
	j := createSleep(t, pool, `{"duration_ms":10000}`)
	if err := dispatch.New(repo, q, testLogger()).Once(ctx); err != nil {
		t.Fatal(err)
	}
	w, err := execution.NewWithLeasePolicy(repo, q, execution.Sleep{}, testLogger(), 1, shortLease())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	msg := receive(t, q)
	go func() { _, err := w.Handle(ctx, msg); done <- err }()
	await(t, func() bool { got, _ := repo.GetByID(ctx, j.ID); return got != nil && got.Status == job.Running })
	requested, err := repo.Cancel(ctx, j.ID)
	if err != nil || requested.Status != job.Running || requested.CancelRequestedAt == nil {
		t.Fatal(requested, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cooperative cancel did not stop")
	}
	got, err := repo.GetByID(ctx, j.ID)
	if err != nil || got.Status != job.Cancelled || got.AttemptCount != 1 || got.RetryAt != nil {
		t.Fatal(got, err)
	}
	history, err := repo.Attempts(ctx, j.ID)
	if err != nil || len(history) != 1 || history[0].Status != job.Cancelled || *history[0].Error != "user_cancelled" {
		t.Fatal(history, err)
	}
	// Crash after durable intent, reaper/finalize race: request always wins.
	for range 5 {
		j := createSleep(t, pool, `{"duration_ms":0}`)
		current, err := repo.Claim(ctx, j.ID, registered(t, repo))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Cancel(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
		expire(t, pool, j.ID)
		var wg sync.WaitGroup
		wg.Go(func() {
			_, err := repo.RecoverExpired(ctx, 100)
			if err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			err := repo.Finalize(ctx, current, job.Succeeded, []byte(`{}`))
			if !errors.Is(err, job.ErrLeaseLost) {
				t.Error("stale owner", err)
			}
		})
		wg.Wait()
		got, err := repo.GetByID(ctx, j.ID)
		if err != nil || got.Status != job.Cancelled || attemptCount(t, pool, j.ID) != 1 {
			t.Fatal(got, err)
		}
	}
	// Cancellation wins a valid-lease late success without pretending the API stopped it.
	late := createSleep(t, pool, `{"duration_ms":0}`)
	current, err := repo.Claim(ctx, late.ID, registered(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Cancel(ctx, late.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finalize(ctx, current, job.Succeeded, []byte(`{}`)); err != nil || current.Status != job.Cancelled {
		t.Fatal("late success ignored intent", err)
	}
}

func TestDeadLetterRedrivePaginationHistoryAndAtomicity(t *testing.T) {
	pool := migratedDatabase(t)
	repo := leaseRepo(t, pool)
	ctx := t.Context()
	h := transport.NewRouter(service.New(repo), testLogger())
	owner := registered(t, repo)
	var jobs []*job.Job
	for n := 0; n < 4; n++ {
		key := fmt.Sprint("dlq-", n)
		budget := 1
		j, err := service.New(repo).Create(ctx, service.CreateInput{Type: "SLEEP", Payload: []byte(`{"duration_ms":0}`), MaxAttempts: &budget, IdempotencyKey: &key})
		if err != nil {
			t.Fatal(err)
		}
		current, err := repo.Claim(ctx, j.ID, owner)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Finalize(ctx, current, job.Failed, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	extra := createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := repo.Cancel(ctx, extra.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET created_at='2026-10-05T00:00:00Z' WHERE status='DEAD_LETTER'`); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/dead-letter?limit=2"
	seen := map[uuid.UUID]bool{}
	for range 2 {
		res := send(h, "GET", path, "")
		if res.Code != 200 {
			t.Fatal(res.Code, res.Body.String())
		}
		var page struct {
			Jobs []job.Job `json:"jobs"`
			Next *string   `json:"next_cursor"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Jobs) != 2 {
			t.Fatal(page)
		}
		for _, j := range page.Jobs {
			if j.Status != job.DeadLetter || seen[j.ID] {
				t.Fatal("pagination duplication/filter")
			}
			seen[j.ID] = true
		}
		if page.Next != nil {
			path = "/api/v1/dead-letter?limit=2&cursor=" + *page.Next
		}
	}
	checkHTTPError(t, send(h, "GET", "/api/v1/dead-letter?cursor=bad", ""), 400, "INVALID_CURSOR")
	checkHTTPError(t, send(h, "GET", "/api/v1/dead-letter?limit=101", ""), 400, "INVALID_INPUT")
	j := jobs[0]
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Go(func() { results <- send(h, "POST", "/api/v1/jobs/"+j.ID.String()+"/retry", "").Code })
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for n := range results {
		counts[n]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
	got, err := repo.GetByID(ctx, j.ID)
	if err != nil || got.MaxAttempts != 2 || got.AttemptCount != 1 || got.Status != job.Queued || got.Result != nil || got.FinishedAt != nil {
		t.Fatal(got, err)
	}
	replay := send(h, "POST", "/api/v1/jobs", `{"type":"SLEEP","payload":{"duration_ms":0},"max_attempts":1,"idempotency_key":"dlq-0"}`)
	if replay.Code != 200 {
		t.Fatal("redrive changed submission identity", replay.Code, replay.Body.String())
	}
	current, err := repo.Claim(ctx, j.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Finalize(ctx, current, job.Succeeded, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	history, err := repo.Attempts(ctx, j.ID)
	if err != nil || len(history) != 2 || history[0].Status != job.Failed || history[1].Status != job.Succeeded || history[1].AttemptNumber != 2 {
		t.Fatal(history, err)
	}
	attemptsHTTP := send(h, "GET", "/api/v1/jobs/"+j.ID.String()+"/attempts", "")
	if attemptsHTTP.Code != 200 {
		t.Fatal(attemptsHTTP.Body.String())
	}
	checkHTTPError(t, send(h, "POST", "/api/v1/jobs/"+j.ID.String()+"/cancel", ""), 409, "JOB_CONTROL_CONFLICT")
	checkHTTPError(t, send(h, "POST", "/api/v1/jobs/"+jobs[1].ID.String()+"/cancel", ""), 409, "JOB_CONTROL_CONFLICT")
	if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=100 WHERE id=$1`, jobs[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RedriveDeadLetter(ctx, jobs[1].ID); !errors.Is(err, job.ErrControlConflict) {
		t.Fatal("100 cap", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE job_dispatch ADD CONSTRAINT reject_redrive CHECK(published_at IS NOT NULL) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RedriveDeadLetter(ctx, jobs[2].ID); err == nil {
		t.Fatal("outbox failure hidden")
	}
	if got, err := repo.GetByID(ctx, jobs[2].ID); err != nil || got.Status != job.DeadLetter || got.MaxAttempts != 1 || got.AttemptCount != 1 {
		t.Fatal("redrive escaped rollback", got, err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE job_dispatch DROP CONSTRAINT reject_redrive`); err != nil {
		t.Fatal(err)
	}
	// Redrive/cancel race has one budget grant at most. Cancel may conflict if it
	// linearizes first, or terminate the freshly redriven QUEUED Job.
	target := jobs[3]
	wg.Go(func() {
		_, err := repo.RedriveDeadLetter(ctx, target.ID)
		if err != nil {
			t.Error(err)
		}
	})
	wg.Go(func() {
		_, err := repo.Cancel(ctx, target.ID)
		if err != nil && !errors.Is(err, job.ErrControlConflict) {
			t.Error(err)
		}
	})
	wg.Wait()
	got, err = repo.GetByID(ctx, target.ID)
	if err != nil || got.MaxAttempts != 2 || got.AttemptCount != 1 || (got.Status != job.Queued && got.Status != job.Cancelled) {
		t.Fatal(got, err)
	}
	pool.Close()
	checkHTTPError(t, send(h, "GET", "/api/v1/dead-letter", ""), 500, "INTERNAL_ERROR")
}

func TestPhase6MigrationUpDownConstraints(t *testing.T) {
	pool := migratedDatabase(t)
	ctx := t.Context()
	repo := leaseRepo(t, pool)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET cancel_requested_at=clock_timestamp() WHERE id=$1`, j.ID); err == nil {
		t.Fatal("invalid cancel metadata accepted")
	}
	if err := migrations.Run(ctx, pool, "down"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Run(ctx, pool, "down"); err != nil {
		t.Fatal(err)
	}
	var cols int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='jobs' AND column_name IN ('cancel_requested_at','submission_max_attempts')`).Scan(&cols); err != nil || cols != 0 {
		t.Fatal(cols, err)
	}
	if err := migrations.Run(ctx, pool, "up"); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetByID(ctx, j.ID); err != nil || got.Status != job.Queued {
		t.Fatal(got, err)
	}
}

func await(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for claim barrier")
}
