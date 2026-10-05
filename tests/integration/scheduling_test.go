package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	"github.com/Daniel-Cpz/FlowForge/internal/service/dispatch"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/Daniel-Cpz/FlowForge/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"slices"
	"sync"
	"testing"
	"time"
)

func downgradeTo(t *testing.T, pool *pgxpool.Pool, target int) {
	t.Helper()
	var current int
	if err := pool.QueryRow(t.Context(), `SELECT max(version) FROM schema_migrations`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	for current > target {
		if err := migrations.Run(t.Context(), pool, "down"); err != nil {
			t.Fatal(err)
		}
		current--
	}
}
func capWorker(t *testing.T, r *postgres.JobRepository, values ...string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := r.RegisterWorkerCapabilities(t.Context(), id, 2, values); err != nil {
		t.Fatal(err)
	}
	return id
}
func schedulingJob(t *testing.T, r *postgres.JobRepository, priority int, due *time.Time, values ...string) *job.Job {
	t.Helper()
	j, err := service.New(r).Create(t.Context(), service.CreateInput{Type: "SLEEP", Payload: []byte(`{"duration_ms":0}`), Priority: priority, ScheduledAt: due, RequiredCapabilities: values})
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func recurring(t *testing.T, r *postgres.JobRepository, first time.Time, interval int) *schedule.Schedule {
	t.Helper()
	s, err := service.New(r).CreateSchedule(t.Context(), service.ScheduleInput{Template: service.CreateInput{Type: "SLEEP", Payload: []byte(`{"duration_ms":0}`), RequiredCapabilities: []string{" CPU "}}, IntervalSeconds: interval, NextRunAt: &first})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDelayedAndCapabilityEligibilityAuthority(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	cpu := capWorker(t, r, " CPU ", "cpu")
	both := capWorker(t, r, "cpu", "gpu")
	var future time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()+interval '1 hour'`).Scan(&future); err != nil {
		t.Fatal(err)
	}
	delayed := schedulingJob(t, r, 100, &future, "cpu")
	gpu := schedulingJob(t, r, 90, nil, "gpu")
	low := schedulingJob(t, r, 0, nil, "cpu")
	ids, err := r.PendingDispatch(ctx, 100)
	if err != nil || len(ids) != 2 || ids[0] != gpu.ID || ids[1] != low.ID {
		t.Fatal(ids, err)
	}
	for _, id := range []uuid.UUID{delayed.ID, gpu.ID} {
		if _, err := r.Claim(ctx, id, cpu); !errors.Is(err, job.ErrInvalidTransition) {
			t.Fatal(err)
		}
		if attemptCount(t, pool, id) != 0 {
			t.Fatal("ineligible consumed budget")
		}
	}
	if _, err := r.Claim(ctx, low.ID, both); !errors.Is(err, job.ErrPriorityDeferred) {
		t.Fatal("capable worker skipped GPU priority", err)
	}
	owned, err := r.Claim(ctx, low.ID, cpu)
	if err != nil {
		t.Fatal("GPU blocked CPU", err)
	}
	if err := r.Finalize(ctx, owned, job.Succeeded, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	// SQL at the DB boundary proves due-time authority independently of API clock.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET scheduled_at=clock_timestamp() WHERE id=$1`, delayed.ID); err != nil {
		t.Fatal(err)
	}
	ids, err = r.PendingDispatch(ctx, 100)
	if err != nil || len(ids) != 2 || ids[0] != delayed.ID {
		t.Fatal(ids, err)
	}
	if _, err := r.Claim(ctx, delayed.ID, cpu); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, gpu.ID, both); err != nil {
		t.Fatal(err)
	}
	futureCancel := schedulingJob(t, r, 0, &future)
	if got, err := r.Cancel(ctx, futureCancel.ID); err != nil || got.Status != job.Cancelled || got.AttemptCount != 0 {
		t.Fatal(got, err)
	}
	if _, err := r.Claim(ctx, futureCancel.ID, both); !errors.Is(err, job.ErrInvalidTransition) {
		t.Fatal(err)
	}
	past := future.Add(-2 * time.Hour)
	pastJob := schedulingJob(t, r, 0, &past, "cpu")
	if _, err := r.Claim(ctx, pastJob.ID, cpu); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulingHTTPIdempotencyAndValidation(t *testing.T) {
	pool := migratedDatabase(t)
	h := api(pool)
	body := `{"type":"SLEEP","payload":{},"scheduled_at":"2026-10-05T17:00:00.123456+11:00","required_capabilities":[" GPU ","CPU","cpu"],"idempotency_key":"scheduled"}`
	first := send(h, "POST", "/api/v1/jobs", body)
	if first.Code != 201 {
		t.Fatal(first.Code, first.Body.String())
	}
	var j job.Job
	if err := json.Unmarshal(first.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(j.RequiredCapabilities, []string{"cpu", "gpu"}) || j.ScheduledAt.Location() != time.UTC {
		t.Fatal(j)
	}
	replay := `{"type":"SLEEP","payload":{},"scheduled_at":"2026-10-05T06:00:00.123456Z","required_capabilities":["cpu","gpu"],"idempotency_key":"scheduled"}`
	if w := send(h, "POST", "/api/v1/jobs", replay); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{"type":"SLEEP","payload":{},"scheduled_at":null,"required_capabilities":["cpu","gpu"],"idempotency_key":"scheduled"}`, `{"type":"SLEEP","payload":{},"scheduled_at":"2026-10-05T06:00:00.123457Z","required_capabilities":["cpu","gpu"],"idempotency_key":"scheduled"}`, `{"type":"SLEEP","payload":{},"scheduled_at":"2026-10-05T06:00:00.123456Z","required_capabilities":["cpu"],"idempotency_key":"scheduled"}`} {
		checkHTTPError(t, send(h, "POST", "/api/v1/jobs", body), 409, "IDEMPOTENCY_CONFLICT")
	}
	for _, body := range []string{`{"type":"SLEEP","payload":{},"required_capabilities":[""]}`, `{"type":"SLEEP","payload":{},"required_capabilities":["a/b"]}`, `{"type":"SLEEP","payload":{},"required_capabilities":["cpu\n"]}`, `{"type":"SLEEP","payload":{},"required_capabilities":[null]}`} {
		checkHTTPError(t, send(h, "POST", "/api/v1/jobs", body), 400, "INVALID_INPUT")
	}
	for _, body := range []string{`{"type":"SLEEP","payload":{},"scheduled_at":null,"required_capabilities":null}`, `{"type":"SLEEP","payload":{}}`} {
		w := send(h, "POST", "/api/v1/jobs", body)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := send(h, "POST", "/api/v1/schedules", `{"type":"SLEEP","payload":{},"interval_seconds":2,"required_capabilities":["CPU","cpu"]}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var s schedule.Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if w := send(h, "GET", "/api/v1/schedules/"+s.ID.String(), ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for range 2 {
		w := send(h, "POST", "/api/v1/schedules/"+s.ID.String()+"/cancel", "")
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	for _, n := range []int{0, -1, 604801} {
		checkHTTPError(t, send(h, "POST", "/api/v1/schedules", fmt.Sprintf(`{"type":"SLEEP","payload":{},"interval_seconds":%d}`, n)), 400, "INVALID_INPUT")
	}
	checkHTTPError(t, send(h, "GET", "/api/v1/schedules/"+uuid.NewString(), ""), 404, "SCHEDULE_NOT_FOUND")
	checkHTTPError(t, send(h, "GET", "/api/v1/schedules/invalid", ""), 400, "INVALID_ID")
	checkHTTPError(t, send(h, "DELETE", "/api/v1/schedules/"+s.ID.String(), ""), 405, "METHOD_NOT_ALLOWED")
}

func TestWorkerCapabilitiesImmutableLivenessAndRaces(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	a := capWorker(t, r, "gpu", "cpu")
	b := capWorker(t, r, "gpu")
	if err := r.Heartbeat(ctx, a, 0); err != nil {
		t.Fatal(err)
	}
	var caps []string
	if err := pool.QueryRow(ctx, `SELECT capabilities FROM workers WHERE worker_id=$1`, a).Scan(&caps); err != nil || !slices.Equal(caps, []string{"cpu", "gpu"}) {
		t.Fatal(caps, err)
	}
	if err := r.RegisterWorkerCapabilities(ctx, a, 2, []string{"cpu"}); err == nil {
		t.Fatal("identity reused")
	}
	if _, err := pool.Exec(ctx, `UPDATE workers SET capabilities='{cpu}' WHERE worker_id=$1`, a); err == nil {
		t.Fatal("capability mutation accepted")
	}
	if err := r.RegisterWorkerCapabilities(ctx, uuid.New(), 2, []string{"bad/"}); !errors.Is(err, job.ErrInvalidInput) {
		t.Fatal(err)
	}
	target := schedulingJob(t, r, 0, nil, "gpu")
	var wg sync.WaitGroup
	wins := make(chan error, 8)
	for n := range 8 {
		workerID := a
		if n%2 == 0 {
			workerID = b
		}
		wg.Go(func() { _, err := r.Claim(ctx, target.ID, workerID); wins <- err })
	}
	wg.Wait()
	close(wins)
	count := 0
	for err := range wins {
		if err == nil {
			count++
		} else if !errors.Is(err, job.ErrInvalidTransition) {
			t.Fatal(err)
		}
	}
	if count != 1 || attemptCount(t, pool, target.ID) != 1 {
		t.Fatal(count)
	}
	stale := capWorker(t, r, "gpu")
	if _, err := pool.Exec(ctx, `UPDATE workers SET last_heartbeat=clock_timestamp()-interval '1 hour' WHERE worker_id=$1`, stale); err != nil {
		t.Fatal(err)
	}
	second := schedulingJob(t, r, 0, nil, "gpu")
	if _, err := r.Claim(ctx, second.ID, stale); !errors.Is(err, job.ErrInvalidTransition) {
		t.Fatal(err)
	}
	if _, err := r.DetectOffline(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, second.ID, stale); !errors.Is(err, job.ErrInvalidTransition) {
		t.Fatal(err)
	}
	// Fresh constructors always get a new identity when changing capabilities.
	q, _, _, _ := redisQueue(t)
	one, err := execution.NewWithLeasePolicy(r, q, execution.Sleep{}, testLogger(), 1, shortLease(), "cpu")
	if err != nil {
		t.Fatal(err)
	}
	two, err := execution.NewWithLeasePolicy(r, q, execution.Sleep{}, testLogger(), 1, shortLease(), "gpu")
	if err != nil {
		t.Fatal(err)
	}
	delivery := &job.Delivery{Version: "1", JobID: second.ID.String(), MessageID: "0-0"}
	if claimed, err := one.Handle(ctx, delivery); err != nil || claimed {
		t.Fatal(claimed, err)
	}
	if claimed, err := two.Handle(ctx, delivery); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	var identities int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT worker_id) FROM workers WHERE capabilities='{cpu}' OR capabilities='{gpu}'`).Scan(&identities); err != nil || identities < 3 {
		t.Fatal(identities, err)
	}
}

func TestIncapableNotificationACKThenCapableArrives(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	q, client, key, group := redisQueue(t)
	target := schedulingJob(t, r, 100, nil, "gpu")
	low := schedulingJob(t, r, 0, nil, "cpu")
	if err := dispatch.New(r, q, testLogger()).Once(ctx); err != nil {
		t.Fatal(err)
	}
	cpu, err := execution.NewWithLeasePolicy(r, q, execution.Sleep{}, testLogger(), 1, shortLease(), "cpu")
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := cpu.Handle(ctx, receive(t, q)); err != nil || claimed {
		t.Fatal(err)
	}
	if claimed, err := cpu.Handle(ctx, receive(t, q)); err != nil || !claimed {
		t.Fatal(err)
	}
	got, err := r.GetByID(ctx, target.ID)
	if err != nil || got.Status != job.Queued || got.AttemptCount != 0 || pendingCount(t, client, key, group) != 0 {
		t.Fatal(got, err)
	}
	if got, err := r.GetByID(ctx, low.ID); err != nil || got.Status != job.Succeeded {
		t.Fatal(got, err)
	}
	// Accelerate ONLY the isolated intent's documented 30s reconciliation clock.
	if _, err := pool.Exec(ctx, `UPDATE job_dispatch SET published_at=clock_timestamp()-interval '31 seconds' WHERE job_id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.New(r, q, testLogger()).Once(ctx); err != nil {
		t.Fatal(err)
	}
	gpu, err := execution.NewWithLeasePolicy(r, q, execution.Sleep{}, testLogger(), 1, shortLease(), "gpu")
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := gpu.Handle(ctx, receive(t, q)); err != nil || !claimed {
		t.Fatal(err)
	}
	got, err = r.GetByID(ctx, target.ID)
	if err != nil || got.Status != job.Succeeded || got.AttemptCount != 1 {
		t.Fatal(got, err)
	}
}

func TestRecurringConcurrentCoalescingRestartAndCancellation(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	var first time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()-interval '10 minutes'`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	s := recurring(t, r, first, 60)
	var wg sync.WaitGroup
	results := make(chan []uuid.UUID, 8)
	for range 8 {
		wg.Go(func() {
			ids, err := r.MaterializeDue(ctx, 100)
			if err != nil {
				t.Error(err)
			}
			results <- ids
		})
	}
	wg.Wait()
	close(results)
	count := 0
	var id uuid.UUID
	for ids := range results {
		count += len(ids)
		if len(ids) > 0 {
			id = ids[0]
		}
	}
	if count != 1 {
		t.Fatal("duplicate catch-up", count)
	}
	j, err := r.GetByID(ctx, id)
	if err != nil || j.ScheduleID == nil || *j.ScheduleID != s.ID || !j.ScheduledFor.Equal(first) || !slices.Equal(j.RequiredCapabilities, []string{"cpu"}) {
		t.Fatal(j, err)
	}
	var ahead bool
	if err := pool.QueryRow(ctx, `SELECT next_run_at>clock_timestamp() AND next_run_at<=clock_timestamp()+make_interval(secs=>interval_seconds) FROM job_schedules WHERE id=$1`, s.ID).Scan(&ahead); err != nil || !ahead {
		t.Fatal(ahead, err)
	}
	restarted := postgres.NewJobRepository(pool)
	if ids, err := restarted.MaterializeDue(ctx, 100); err != nil || len(ids) != 0 {
		t.Fatal("restart duplicate", ids, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,type,payload,schedule_id,scheduled_for,scheduled_at) VALUES($1,'SLEEP','{}',$2,$3,$3)`, uuid.New(), s.ID, first); err == nil {
		t.Fatal("DB duplicate occurrence allowed")
	}
	if _, err := r.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	parent, err := r.GetSchedule(ctx, s.ID)
	if err != nil || parent.Status != "ACTIVE" {
		t.Fatal(parent, err)
	}
	for range 2 {
		if got, err := r.CancelSchedule(ctx, s.ID); err != nil || got.Status != "CANCELLED" {
			t.Fatal(got, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE job_schedules SET next_run_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, s.ID); err != nil {
		t.Fatal(err)
	}
	if ids, err := r.MaterializeDue(ctx, 100); err != nil || len(ids) != 0 {
		t.Fatal(ids, err)
	}
	got, err := r.GetByID(ctx, id)
	if err != nil || got.Status != job.Cancelled {
		t.Fatal("parent cancel rewrote job", got, err)
	}
}

func TestRecurringTransactionRollbackAndPostCommitRedisFailure(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	first := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	s := recurring(t, r, first, 60)
	// A transaction fault after the occurrence INSERT represents pre-commit crash.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_occurrence_intent() RETURNS TRIGGER LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected intent failure';END$$;CREATE TRIGGER reject_occurrence_intent BEFORE INSERT ON job_dispatch FOR EACH ROW EXECUTE FUNCTION reject_occurrence_intent()`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MaterializeDue(ctx, 100); err == nil {
		t.Fatal("fault ignored")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial occurrence", count, err)
	}
	stored, err := r.GetSchedule(ctx, s.ID)
	if err != nil || !stored.NextRunAt.Equal(first) {
		t.Fatal("advance escaped rollback", stored, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_occurrence_intent ON job_dispatch;DROP FUNCTION reject_occurrence_intent()`); err != nil {
		t.Fatal(err)
	}
	ids, err := r.MaterializeDue(ctx, 100)
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	// Redis failure after PostgreSQL commit retains an unpublished durable intent.
	if err := dispatch.New(r, unavailablePublisher{}, testLogger()).Once(ctx); err == nil {
		t.Fatal("Redis failure hidden")
	}
	if ids, err := postgres.NewJobRepository(pool).MaterializeDue(ctx, 100); err != nil || len(ids) != 0 {
		t.Fatal("postcommit replay", ids, err)
	}
	pending, err := r.PendingDispatch(ctx, 100)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	q, _, _, _ := redisQueue(t)
	if err := q.Publish(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := q.Publish(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	worker, err := execution.NewWithLeasePolicy(r, q, execution.Sleep{}, testLogger(), 1, shortLease(), "cpu")
	if err != nil {
		t.Fatal(err)
	}
	for n := range 2 {
		claimed, err := worker.Handle(ctx, receive(t, q))
		if err != nil || claimed != (n == 0) {
			t.Fatal(claimed, err)
		}
	}
	if attemptCount(t, pool, ids[0]) != 1 {
		t.Fatal("duplicate notification executed twice")
	}
}

type unavailablePublisher struct{}

func (unavailablePublisher) Publish(context.Context, uuid.UUID) error {
	return errors.New("simulated Redis unavailable")
}

func TestScheduleCancelMaterializerRaceAndRetryRedriveCapabilities(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	for range 10 {
		s := recurring(t, r, time.Now().UTC().Add(-time.Hour), 60)
		var wg sync.WaitGroup
		wg.Go(func() {
			_, err := r.MaterializeDue(ctx, 100)
			if err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			_, err := r.CancelSchedule(ctx, s.ID)
			if err != nil {
				t.Error(err)
			}
		})
		wg.Wait()
		before := 0
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE schedule_id=$1`, s.ID).Scan(&before); err != nil || before > 1 {
			t.Fatal(before, err)
		}
		if _, err := r.MaterializeDue(ctx, 100); err != nil {
			t.Fatal(err)
		}
		after := 0
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE schedule_id=$1`, s.ID).Scan(&after); err != nil || before != after {
			t.Fatal(before, after, err)
		}
	}
	// Separate schedule keeps these occurrences independent of the race fixtures.
	s := recurring(t, r, time.Now().UTC().Add(-time.Hour), 3600)
	if _, err := pool.Exec(ctx, `UPDATE job_schedules SET required_capabilities='{gpu}',max_attempts=2 WHERE id=$1`, s.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := r.MaterializeDue(ctx, 100)
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	gpu := capWorker(t, r, "gpu")
	cpu := capWorker(t, r, "cpu")
	owned, err := r.Claim(ctx, ids[0], gpu)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Finalize(ctx, owned, job.TimedOut, []byte(`{}`), job.Failure{Class: job.Retryable, Code: "execution_timeout"}); err != nil {
		t.Fatal(err)
	}
	forceDue(t, pool, r, ids[0])
	if _, err := r.Claim(ctx, ids[0], cpu); !errors.Is(err, job.ErrInvalidTransition) {
		t.Fatal(err)
	}
	owned, err = r.Claim(ctx, ids[0], gpu)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Finalize(ctx, owned, job.TimedOut, []byte(`{}`), job.Failure{Class: job.Retryable, Code: "execution_timeout"}); err != nil {
		t.Fatal(err)
	}
	redriven, err := r.RedriveDeadLetter(ctx, ids[0])
	if err != nil || !slices.Equal(redriven.RequiredCapabilities, []string{"gpu"}) || redriven.ScheduleID == nil {
		t.Fatal(redriven, err)
	}
	if _, err := r.Claim(ctx, ids[0], cpu); !errors.Is(err, job.ErrInvalidTransition) {
		t.Fatal(err)
	}
	if _, err := r.CancelSchedule(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := r.GetByID(ctx, ids[0]); err != nil || got.Status != job.Queued || got.AttemptCount != 2 {
		t.Fatal("schedule cancel changed retry", got, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE schedule_id=$1`, s.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry created another occurrence", count, err)
	}
}

func TestPhase7MigrationConstraintsAndHistoricalCompatibility(t *testing.T) {
	pool := migratedDatabase(t)
	ctx := t.Context()
	r := leaseRepo(t, pool)
	j := schedulingJob(t, r, 0, nil)
	w := capWorker(t, r)
	for _, q := range []string{`UPDATE jobs SET required_capabilities='{gpu,cpu}'`, `UPDATE jobs SET required_capabilities='{cpu,cpu}'`, `UPDATE jobs SET required_capabilities='{CPU}'`, `UPDATE jobs SET required_capabilities=ARRAY[NULL]::text[]`, `UPDATE jobs SET scheduled_at='infinity'`, `UPDATE jobs SET scheduled_for=clock_timestamp()`} {
		if _, err := pool.Exec(ctx, q); err == nil {
			t.Fatal("invalid stored contract", q)
		}
	}
	if _, err := r.MaterializeDue(ctx, 101); !errors.Is(err, job.ErrInvalidInput) {
		t.Fatal(err)
	}
	for _, interval := range []int{0, 604801} {
		if _, err := pool.Exec(ctx, `INSERT INTO job_schedules(id,status,type,payload,priority,max_attempts,timeout,interval_seconds,next_run_at) VALUES($1,'ACTIVE','SLEEP','{}',0,1,1,$2,clock_timestamp())`, uuid.New(), interval); err == nil {
			t.Fatal("interval constraint missing")
		}
	}
	downgradeTo(t, pool, 6)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE id=$1`, j.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := migrations.Run(ctx, pool, "up"); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByID(ctx, j.ID)
	if err != nil || len(got.RequiredCapabilities) != 0 || got.ScheduledAt != nil {
		t.Fatal(got, err)
	}
	var capabilities []string
	if err := pool.QueryRow(ctx, `SELECT capabilities FROM workers WHERE worker_id=$1`, w).Scan(&capabilities); err != nil || len(capabilities) != 0 {
		t.Fatal(capabilities, err)
	}
	if _, err := r.Claim(ctx, j.ID, w); err != nil {
		t.Fatal("legacy no-cap job cannot run", err)
	}
}

func TestRecurringBackendCrashBeforeCommit(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	first := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	s := recurring(t, r, first, 60)
	marker := "phase7-crash-" + uuid.NewString()
	// Only this random-schema trigger marks its own transaction/backend.
	query := fmt.Sprintf(`CREATE FUNCTION crash_barrier() RETURNS TRIGGER LANGUAGE plpgsql AS $$BEGIN PERFORM set_config('application_name','%s',true);PERFORM pg_sleep(10);RETURN NEW;END$$;CREATE TRIGGER crash_barrier BEFORE INSERT ON job_dispatch FOR EACH ROW EXECUTE FUNCTION crash_barrier()`, marker)
	if _, err := pool.Exec(ctx, query); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.MaterializeDue(ctx, 100); done <- err }()
	var pid int
	await(t, func() bool {
		return pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE application_name=$1 AND wait_event='PgSleep'`, marker).Scan(&pid) == nil
	})
	var terminated bool
	if err := pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatal(terminated, err)
	}
	if err := <-done; err == nil {
		t.Fatal("backend crash reported success")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("crash left partial Job", count, err)
	}
	got, err := r.GetSchedule(ctx, s.ID)
	if err != nil || !got.NextRunAt.Equal(first) {
		t.Fatal("crash advanced schedule", got, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER crash_barrier ON job_dispatch;DROP FUNCTION crash_barrier()`); err != nil {
		t.Fatal(err)
	}
	if ids, err := r.MaterializeDue(ctx, 100); err != nil || len(ids) != 1 {
		t.Fatal("restart did not recover occurrence", ids, err)
	}
}

func TestRecurringBatchBoundAndSkipLocked(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	first := time.Now().UTC().Add(-time.Hour)
	s := recurring(t, r, first, 60)
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(context.Background())
	if _, err := locked.Exec(ctx, `SELECT id FROM job_schedules WHERE id=$1 FOR UPDATE`, s.ID); err != nil {
		t.Fatal(err)
	}
	for range 101 {
		recurring(t, r, first, 60)
	}
	future := recurring(t, r, time.Now().UTC().Add(time.Hour), 60)
	ids, err := r.MaterializeDue(ctx, 100)
	if err != nil || len(ids) != 100 {
		t.Fatal("batch not bounded", len(ids), err)
	}
	ids, err = r.MaterializeDue(ctx, 100)
	if err != nil || len(ids) != 1 {
		t.Fatal("locked row blocked independent schedules", len(ids), err)
	}
	if err := locked.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	ids, err = r.MaterializeDue(ctx, 100)
	if err != nil || len(ids) != 1 {
		t.Fatal("unlocked occurrence missing", ids, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE schedule_id=$1`, future.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("future schedule ran", count, err)
	}
}

func TestDueHighGPUStillAllowsLowerCPU(t *testing.T) {
	pool := migratedDatabase(t)
	r := leaseRepo(t, pool)
	ctx := t.Context()
	cpu := capWorker(t, r, "cpu")
	gpu := capWorker(t, r, "gpu")
	var future time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()+interval '1 hour'`).Scan(&future); err != nil {
		t.Fatal(err)
	}
	high := schedulingJob(t, r, 100, &future, "gpu")
	low := schedulingJob(t, r, 0, nil, "cpu")
	if _, err := pool.Exec(ctx, `UPDATE jobs SET scheduled_at=clock_timestamp() WHERE id=$1`, high.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, high.ID, cpu); !errors.Is(err, job.ErrInvalidTransition) {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, low.ID, cpu); err != nil {
		t.Fatal("newly due GPU blocks CPU", err)
	}
	if _, err := r.Claim(ctx, high.ID, gpu); err != nil {
		t.Fatal(err)
	}
}
