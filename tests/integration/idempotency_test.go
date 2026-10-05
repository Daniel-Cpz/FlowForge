package integration

import (
	"encoding/json"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/Daniel-Cpz/FlowForge/migrations"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentKeyedHTTPReplayConflictAndNoKey(t *testing.T) {
	pool := migratedDatabase(t)
	h := api(pool)
	body := `{"type":" SLEEP ","payload":{"duration_ms":0,"nested":{"a":1,"b":[true,null]}},"idempotency_key":" Exact Key "}`
	type response struct {
		code     int
		j        job.Job
		location string
		body     string
	}
	results := make(chan response, 16)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := send(h, "POST", "/api/v1/jobs", body)
			var j job.Job
			_ = json.Unmarshal(w.Body.Bytes(), &j)
			results <- response{w.Code, j, w.Header().Get("Location"), w.Body.String()}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var id uuid.UUID
	created, replayed := 0, 0
	for r := range results {
		if r.code == 201 {
			created++
		} else if r.code == 200 {
			replayed++
		} else {
			t.Fatal(r.code, r.body)
		}
		if id == uuid.Nil {
			id = r.j.ID
		}
		if id != r.j.ID || r.location != "/api/v1/jobs/"+id.String() {
			t.Fatal("different replay identity")
		}
	}
	if created != 1 || replayed != 15 {
		t.Fatal(created, replayed)
	}
	var jobs, intents, attempts int
	checkCounts := func(wantJobs, wantIntents int) {
		t.Helper()
		if e := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM job_dispatch),(SELECT count(*) FROM job_attempts)`).Scan(&jobs, &intents, &attempts); e != nil || jobs != wantJobs || intents != wantIntents || attempts != 0 {
			t.Fatal("dedup persistence", jobs, intents, attempts, e)
		}
	}
	checkCounts(1, 1)
	semantic := `{"type":"SLEEP","payload":{"nested":{"b":[true,null],"a":1.0},"duration_ms":0.0},"max_attempts":3,"timeout":300,"priority":0,"idempotency_key":" Exact Key "}`
	if w := send(h, "POST", "/api/v1/jobs", semantic); w.Code != 200 {
		t.Fatal("JSONB semantic replay", w.Code, w.Body.String())
	}
	for _, different := range []string{
		strings.Replace(body, `"duration_ms":0`, `"duration_ms":1`, 1), strings.Replace(body, ` SLEEP `, `OTHER`, 1),
		strings.Replace(body, `"idempotency_key"`, `"priority":1,"idempotency_key"`, 1),
		strings.Replace(body, `"idempotency_key"`, `"timeout":301,"idempotency_key"`, 1),
		strings.Replace(body, `"idempotency_key"`, `"max_attempts":2,"idempotency_key"`, 1),
	} {
		checkHTTPError(t, send(h, "POST", "/api/v1/jobs", different), 409, "IDEMPOTENCY_CONFLICT")
	}
	checkCounts(1, 1)
	// Key whitespace/case are preserved, and null keys never deduplicate.
	for _, key := range []string{`"Exact Key"`, `" exact key "`, `null`, `null`} {
		w := send(h, "POST", "/api/v1/jobs", `{"type":"SLEEP","payload":{},"idempotency_key":`+key+`}`)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	checkCounts(5, 5)
	noKeyIDs := make(chan uuid.UUID, 8)
	errs := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := send(h, "POST", "/api/v1/jobs", `{"type":"SLEEP","payload":{}}`)
			var j job.Job
			_ = json.Unmarshal(w.Body.Bytes(), &j)
			noKeyIDs <- j.ID
			errs <- w.Code
		}()
	}
	wg.Wait()
	close(noKeyIDs)
	close(errs)
	seen := map[uuid.UUID]bool{}
	for id := range noKeyIDs {
		if id == uuid.Nil || seen[id] {
			t.Fatal("no-key dedup")
		}
		seen[id] = true
	}
	for code := range errs {
		if code != 201 {
			t.Fatal(code)
		}
	}
	checkCounts(13, 13)
}

func TestIdempotencyReplayAcrossExecutionStates(t *testing.T) {
	for _, target := range []job.Status{job.Running, job.Retrying, job.Succeeded, job.DeadLetter} {
		t.Run(string(target), func(t *testing.T) {
			pool := migratedDatabase(t)
			repo := leaseRepo(t, pool)
			key := "state-key"
			input := service.CreateInput{Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":0}`), IdempotencyKey: &key}
			s := service.New(repo)
			j, e := s.Create(t.Context(), input)
			if e != nil {
				t.Fatal(e)
			}
			owner := registered(t, repo)
			current, e := repo.Claim(t.Context(), j.ID, owner)
			if e != nil {
				t.Fatal(e)
			}
			if target == job.Succeeded {
				e = repo.Finalize(t.Context(), current, job.Succeeded, json.RawMessage(`{}`))
			} else if target == job.Retrying {
				e = repo.Finalize(t.Context(), current, job.Failed, json.RawMessage(`{}`), job.Failure{Class: job.Retryable, Code: "transient_failure"})
			} else if target == job.DeadLetter {
				e = repo.Finalize(t.Context(), current, job.Failed, json.RawMessage(`{}`), job.Failure{Class: job.Permanent, Code: "unsupported_job_type"})
			}
			if e != nil {
				t.Fatal(e)
			}
			if e := repo.MarkPublished(t.Context(), j.ID); e != nil {
				t.Fatal(e)
			}
			replay, disposition, e := s.CreateWithDisposition(t.Context(), input)
			if e != nil || disposition != job.Replayed || replay.ID != j.ID || replay.Status != target {
				t.Fatal("state replay", replay, disposition, e)
			}
			var count, marked int
			if e := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM job_dispatch),(SELECT count(*) FROM job_dispatch WHERE published_at IS NOT NULL)`).Scan(&count, &marked); e != nil || count != 1 || marked != 1 || attemptCount(t, pool, j.ID) != 1 {
				t.Fatal("replay redispatched", e)
			}
			w := send(api(pool), "POST", "/api/v1/jobs", `{"type":"SLEEP","payload":{"duration_ms":0},"idempotency_key":"state-key"}`)
			if w.Code != 200 || w.Header().Get("Location") != "/api/v1/jobs/"+j.ID.String() {
				t.Fatal("HTTP state replay", w.Code, w.Body.String())
			}
		})
	}
}

func TestIdempotentFirstCreateRollbackAndSafeDatabaseError(t *testing.T) {
	pool := migratedDatabase(t)
	h := api(pool)
	body := `{"type":"SLEEP","payload":{},"idempotency_key":"rollback-key"}`
	if _, e := pool.Exec(t.Context(), `ALTER TABLE job_dispatch ADD CONSTRAINT reject_idempotent_intent CHECK(false)`); e != nil {
		t.Fatal(e)
	}
	checkHTTPError(t, send(h, "POST", "/api/v1/jobs", body), 500, "INTERNAL_ERROR")
	var n int
	if e := pool.QueryRow(t.Context(), `SELECT count(*) FROM jobs`).Scan(&n); e != nil || n != 0 {
		t.Fatal("key consumed by rollback", e)
	}
	if _, e := pool.Exec(t.Context(), `ALTER TABLE job_dispatch DROP CONSTRAINT reject_idempotent_intent`); e != nil {
		t.Fatal(e)
	}
	if w := send(h, "POST", "/api/v1/jobs", body); w.Code != 201 {
		t.Fatal(w.Code)
	}
	pool.Close()
	for _, b := range []string{body, strings.Replace(body, `SLEEP`, `OTHER`, 1)} {
		w := send(h, "POST", "/api/v1/jobs", b)
		checkHTTPError(t, w, 500, "INTERNAL_ERROR")
		if strings.Contains(w.Body.String(), "closed") || strings.Contains(w.Body.String(), "pgx") {
			t.Fatal("driver text leaked")
		}
	}
}

func TestPhase5MigrationDuplicatePreflightAndNormalization(t *testing.T) {
	pool := migratedDatabase(t)
	ctx := t.Context()
	if e := migrations.Run(ctx, pool, "down"); e != nil {
		t.Fatal(e)
	}
	id1, id2 := uuid.New(), uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO jobs(id,type,payload,idempotency_key) VALUES($1,'SLEEP','{}','legacy-duplicate'),($2,'SLEEP','{}','legacy-duplicate')`, id1, id2); e != nil {
		t.Fatal(e)
	}
	if e := migrations.Run(ctx, pool, "up"); e == nil || !strings.Contains(e.Error(), "legacy duplicate") {
		t.Fatal("duplicate preflight missing", e)
	}
	var version, count, columns int
	if e := pool.QueryRow(ctx, `SELECT (SELECT max(version) FROM schema_migrations),(SELECT count(*) FROM jobs),(SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='jobs' AND column_name='retry_at')`).Scan(&version, &count, &columns); e != nil || version != 4 || count != 2 || columns != 0 {
		t.Fatal("migration escaped rollback", version, count, columns, e)
	}
	// Explicit test fixture resolution, never production auto-resolution.
	if _, e := pool.Exec(ctx, `UPDATE jobs SET idempotency_key=NULL WHERE id=$1`, id2); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `UPDATE jobs SET attempt_count=2,max_attempts=1 WHERE id=$1`, id1); e != nil {
		t.Fatal(e)
	}
	if e := migrations.Run(ctx, pool, "up"); e != nil {
		t.Fatal(e)
	}
	repo := postgres.NewJobRepository(pool)
	got, e := repo.GetByID(ctx, id1)
	if e != nil || got.AttemptCount != 2 || got.MaxAttempts != 2 || got.Status != job.DeadLetter {
		t.Fatal("legacy budget not preserved/frozen", got, e)
	}
	if e := migrations.Run(ctx, pool, "down"); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `UPDATE jobs SET attempt_count=101 WHERE id=$1`, id1); e != nil {
		t.Fatal(e)
	}
	if e := migrations.Run(ctx, pool, "up"); e == nil || !strings.Contains(e.Error(), "exceeds supported budget") {
		t.Fatal("unsupported history silently changed", e)
	}
	if _, e := pool.Exec(ctx, `UPDATE jobs SET attempt_count=2 WHERE id=$1`, id1); e != nil {
		t.Fatal(e)
	}
	if e := migrations.Run(ctx, pool, "up"); e != nil {
		t.Fatal(e)
	}
	if _, e := repo.Claim(ctx, id1, uuid.New()); !errors.Is(e, job.ErrInvalidTransition) {
		t.Fatal("normalized terminal restarted", e)
	}
}
