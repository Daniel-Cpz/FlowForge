package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	httptransport "github.com/Daniel-Cpz/FlowForge/internal/transport/http"
	"github.com/Daniel-Cpz/FlowForge/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func migratedDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := database(t)
	if err := migrations.Run(t.Context(), pool, "up"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func api(pool *pgxpool.Pool) http.Handler {
	return httptransport.NewRouter(service.New(postgres.NewJobRepository(pool)), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func send(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func checkHTTPError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var response struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != status || response.Error.Code != code || response.Error.Message == "" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatal("error contract", w.Code, w.Body.String())
	}
}

func TestRepositoryAllFieldsReadback(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	ctx := t.Context()
	created := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.FixedZone("test", 11*3600))
	started, finished, lease := created.Add(time.Second), created.Add(2*time.Second), created.Add(3*time.Second)
	workerID, key := uuid.New(), " retained key "
	j := &domain.Job{ID: uuid.New(), Type: "inner  space", Status: domain.Succeeded, Priority: 100,
		Payload: json.RawMessage(`{"b":[1,true,null],"a":9007199254740993}`), Result: json.RawMessage(`{"success":true}`),
		AttemptCount: 2, MaxAttempts: 5, Timeout: 86400, IdempotencyKey: &key, AssignedWorker: &workerID,
		CreatedAt: created, StartedAt: &started, FinishedAt: &finished, LeaseExpiry: &lease}
	if _, err := repo.Create(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, j.ID)
	if err != nil || !reflect.DeepEqual(j, got) {
		t.Fatal("create response and stored data differ", err)
	}
	for _, timestamp := range []*time.Time{&got.CreatedAt, got.StartedAt, got.FinishedAt, got.LeaseExpiry} {
		if timestamp.Location() != time.UTC || timestamp.IsZero() {
			t.Fatal("timestamps must be valid UTC")
		}
	}
	if !strings.Contains(string(got.Payload), "9007199254740993") || *got.IdempotencyKey != key {
		t.Fatal("stored values were changed")
	}
	if _, err := repo.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("not found mapping", err)
	}
}

func TestHTTPPersistenceContract(t *testing.T) {
	pool := migratedDatabase(t)
	h := api(pool)
	for index, payload := range []string{`{"nested":{"ok":true}}`, `[1,true,null]`, `"hello"`, `9007199254740993`, `true`, `null`, `{"a":1,"a":2}`, `"\ud83d\ude00"`} {
		t.Run(payload, func(t *testing.T) {
			key := " shared key " + strconv.Itoa(index)
			body := `{"type":"  example  ","payload":` + payload + `,"priority":100,"max_attempts":100,"timeout":86400,"idempotency_key":"` + key + `"}`
			w := send(h, "POST", "/api/v1/jobs", body)
			if w.Code != 201 || w.Header().Get("Content-Type") != "application/json" {
				t.Fatal(w.Code, w.Body.String())
			}
			var j domain.Job
			if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
				t.Fatal(err)
			}
			if j.ID == uuid.Nil || j.Type != "example" || j.Status != domain.Queued || j.AttemptCount != 0 ||
				j.Priority != 100 || j.MaxAttempts != 100 || j.Timeout != 86400 || *j.IdempotencyKey != key ||
				j.CreatedAt.IsZero() || j.CreatedAt.Location() != time.UTC || string(j.Result) != "null" || j.AssignedWorker != nil ||
				j.LeaseExpiry != nil || j.StartedAt != nil || j.FinishedAt != nil || w.Header().Get("Location") != "/api/v1/jobs/"+j.ID.String() {
				t.Fatal("creation invariants", w.Body.String())
			}
			get := send(h, "GET", w.Header().Get("Location"), "")
			if get.Code != 200 || get.Body.String() != w.Body.String() {
				t.Fatal("POST response must match GET readback", get.Code, get.Body.String())
			}
		})
	}
	// No-key submissions remain independent Jobs.
	var first, second domain.Job
	for _, destination := range []*domain.Job{&first, &second} {
		w := send(h, "POST", "/api/v1/jobs", `{"type":"x","payload":null}`)
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), destination) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if first.ID == second.ID {
		t.Fatal("no-key submissions deduplicated")
	}
	checkHTTPError(t, send(h, "GET", "/api/v1/jobs/"+uuid.NewString(), ""), 404, "JOB_NOT_FOUND")
	checkHTTPError(t, send(h, "GET", "/api/v1/jobs/not-a-uuid", ""), 400, "INVALID_ID")
}

func TestHTTPJSONBAndInvalidInputRegression(t *testing.T) {
	pool := migratedDatabase(t)
	h := api(pool)
	for _, payload := range []string{`"\u0000"`, `{"\u0000":1}`, `"\ud800"`, `"\udc00"`, `{"nested":["\ud800"]}`, `1e1000000`} {
		w := send(h, "POST", "/api/v1/jobs", `{"type":"x","payload":`+payload+`}`)
		checkHTTPError(t, w, 400, "INVALID_INPUT")
		if strings.Contains(w.Body.String(), "PostgreSQL") || strings.Contains(w.Body.String(), "unicode") {
			t.Fatal("raw database detail leaked")
		}
	}
	for _, body := range []string{`{`, `{"type":"x"}`, `{"Type":"x","payload":{}}`,
		`{"type":"x","type":"y","payload":{}}`, `{"type":"x","payload":{},"status":"SUCCEEDED"}`,
		`{"type":"x","payload":{},"priority":-1}`, `{"type":"x","payload":{},"timeout":null}`} {
		if w := send(h, "POST", "/api/v1/jobs", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected input persisted", count, err)
	}
}

func TestKeysetPaginationConcurrentInsert(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	s := service.New(repo)
	h := api(pool)
	ctx := t.Context()
	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-0000-0000-000000000003"),
		uuid.MustParse("00000000-0000-0000-0000-000000000005"),
		uuid.MustParse("00000000-0000-0000-0000-000000000007"),
		uuid.MustParse("00000000-0000-0000-0000-000000000009"),
	}
	for _, id := range ids {
		j := &domain.Job{ID: id, Type: "x", Status: domain.Queued, Payload: json.RawMessage(`null`), MaxAttempts: 3, Timeout: 300, CreatedAt: timestamp}
		if _, err := repo.Create(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	type response struct {
		Jobs []domain.Job `json:"jobs"`
		Next *string      `json:"next_cursor"`
	}
	getPage := func(path string) response {
		t.Helper()
		w := send(h, "GET", path, "")
		var page response
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return page
	}
	first := getPage("/api/v1/jobs?limit=2")
	if len(first.Jobs) != 2 || first.Jobs[0].ID != ids[3] || first.Jobs[1].ID != ids[2] || first.Next == nil {
		t.Fatal("same-timestamp first page ordering", first)
	}
	// Insert a newer row between requests, without sleeps or concurrent timing races.
	newest := &domain.Job{ID: uuid.New(), Type: "new", Status: domain.Queued, Payload: json.RawMessage(`null`),
		MaxAttempts: 3, Timeout: 300, CreatedAt: timestamp.Add(time.Second)}
	if _, err := repo.Create(ctx, newest); err != nil {
		t.Fatal(err)
	}
	second := getPage("/api/v1/jobs?limit=2&cursor=" + url.QueryEscape(*first.Next))
	if len(second.Jobs) != 2 || second.Jobs[0].ID != ids[1] || second.Jobs[1].ID != ids[0] || second.Next != nil {
		t.Fatal("concurrent insert duplicated or skipped older jobs", second)
	}
	all := getPage("/api/v1/jobs?limit=100")
	want := []uuid.UUID{newest.ID, ids[3], ids[2], ids[1], ids[0]}
	if len(all.Jobs) != len(want) || all.Next != nil {
		t.Fatal("full page", all)
	}
	for i, id := range want {
		if all.Jobs[i].ID != id {
			t.Fatal("tuple ordering mismatch")
		}
	}
	boundary := &domain.PageCursor{CreatedAt: timestamp, ID: ids[0]}
	empty, err := s.List(ctx, 1, boundary)
	if err != nil || len(empty.Jobs) != 0 || empty.Jobs == nil || empty.NextCursor != nil {
		t.Fatal("exclusive final boundary", empty, err)
	}
	for _, limit := range []int{1, 2, 20, 100} {
		var cursor *domain.PageCursor
		seen := []uuid.UUID{}
		for {
			page, err := s.List(ctx, limit, cursor)
			if err != nil || len(page.Jobs) > limit {
				t.Fatal("page limit", err)
			}
			for _, j := range page.Jobs {
				seen = append(seen, j.ID)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = page.NextCursor
			if len(seen) > len(want) {
				t.Fatal("pagination did not progress")
			}
		}
		if !reflect.DeepEqual(seen, want) {
			t.Fatal("traversal skipped or repeated a job", limit, seen)
		}
	}
}

func TestRepositoryContextAndConstraints(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	j, err := service.New(repo).Create(t.Context(), service.CreateInput{Type: "x", Payload: json.RawMessage(`null`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE jobs SET priority=-100`, `UPDATE jobs SET priority=101`, `UPDATE jobs SET max_attempts=0`,
		`UPDATE jobs SET max_attempts=101`, `UPDATE jobs SET attempt_count=-1`, `UPDATE jobs SET status='INVALID'`,
		`UPDATE jobs SET timeout=0`, `UPDATE jobs SET timeout=86401`} {
		if _, err := pool.Exec(t.Context(), query); err == nil {
			t.Fatal("constraint missing", query)
		}
	}
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		want := error(context.Canceled)
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		if _, err := repo.GetByID(ctx, j.ID); !errors.Is(err, want) {
			t.Fatal("get context lost", err)
		}
		if _, err := repo.List(ctx, 20, nil); !errors.Is(err, want) {
			t.Fatal("list context lost", err)
		}
		copy := *j
		copy.ID = uuid.New()
		if _, err := repo.Create(ctx, &copy); !errors.Is(err, want) {
			t.Fatal("create context lost", err)
		}
		cancel()
	}
}

func TestHTTPDatabaseFailureAndCorruptData(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	h := api(pool)
	j, err := service.New(repo).Create(t.Context(), service.CreateInput{Type: "x", Payload: json.RawMessage(`null`)})
	if err != nil {
		t.Fatal(err)
	}
	// A test-only constraint simulates a DB rejection after application validation.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE jobs ADD CONSTRAINT test_reject_type CHECK (type <> 'blocked')`); err != nil {
		t.Fatal(err)
	}
	w := send(h, "POST", "/api/v1/jobs", `{"type":"blocked","payload":null}`)
	checkHTTPError(t, w, 500, "INTERNAL_ERROR")
	if strings.Contains(w.Body.String(), "test_reject_type") {
		t.Fatal("constraint name leaked")
	}
	// Damage only this random test schema, modeling externally corrupted data.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE jobs DROP CONSTRAINT jobs_status_check`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='CORRUPT' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(t.Context(), j.ID); !errors.Is(err, domain.ErrInvalidStoredData) {
		t.Fatal("corrupt status accepted", err)
	}
	if _, err := repo.List(t.Context(), 20, nil); !errors.Is(err, domain.ErrInvalidStoredData) {
		t.Fatal("corrupt list accepted", err)
	}
	checkHTTPError(t, send(h, "GET", "/api/v1/jobs/"+j.ID.String(), ""), 500, "INTERNAL_ERROR")
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='QUEUED', created_at='0001-01-01T00:00:00Z' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(t.Context(), j.ID); !errors.Is(err, domain.ErrInvalidStoredData) {
		t.Fatal("zero timestamp accepted", err)
	}
	pool.Close()
	for _, operation := range [][2]string{{"POST", "/api/v1/jobs"}, {"GET", "/api/v1/jobs/" + j.ID.String()}, {"GET", "/api/v1/jobs"}} {
		checkHTTPError(t, send(h, operation[0], operation[1], `{"type":"x","payload":null}`), 500, "INTERNAL_ERROR")
	}
}
