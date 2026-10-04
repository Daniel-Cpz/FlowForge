package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/google/uuid"
)

type memoryRepo struct {
	jobs []domain.Job
	err  error
}

func (m *memoryRepo) Create(ctx context.Context, j *domain.Job) error {
	if m.err != nil {
		return m.err
	}
	m.jobs = append(m.jobs, *j)
	return nil
}
func (m *memoryRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, j := range m.jobs {
		if j.ID == id {
			return &j, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (m *memoryRepo) List(ctx context.Context, limit, offset int) ([]domain.Job, error) {
	if m.err != nil {
		return nil, m.err
	}
	if offset >= len(m.jobs) {
		return []domain.Job{}, nil
	}
	return m.jobs[offset:min(offset+limit, len(m.jobs))], nil
}
func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestJobHTTP(t *testing.T) {
	r := &memoryRepo{}
	h := NewRouter(service.New(r), testLogger())
	w := request(h, "POST", "/api/v1/jobs", `{"type":"example","payload":{"value":42}}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var j domain.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.Status != domain.Queued || j.AttemptCount != 0 || j.MaxAttempts != 3 || j.Timeout != 300 || j.ID == uuid.Nil || j.CreatedAt.Location() != time.UTC || string(j.Result) != "null" {
		t.Fatalf("bad defaults: %+v", j)
	}
	w = request(h, "GET", w.Header().Get("Location"), "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = request(h, "GET", "/api/v1/jobs?limit=1&offset=0", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), j.ID.String()) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestInvalidHTTP(t *testing.T) {
	r := &memoryRepo{}
	h := NewRouter(service.New(r), testLogger())
	for _, body := range []string{`{`, `null`, `[]`, `{}`, `{"type":"x","payload":{}} {}`, `{"type":"x","payload":{},"status":"RUNNING"}`,
		`{"type":"x","payload":{},"priority":101}`, `{"type":"x","payload":{},"max_attempts":0}`, `{"type":"x","payload":{},"timeout":-1}`,
		`{"type":"x","payload":{},"idempotency_key":""}`, `{"type":" ","payload":{}}`, `{"type":"x"}`, `{"type":"x\u0000","payload":{}}`,
	} {
		if w := request(h, "POST", "/api/v1/jobs", body); w.Code != 400 {
			t.Errorf("%s: got %d", body, w.Code)
		}
	}
	if len(r.jobs) != 0 {
		t.Fatal("invalid input was persisted")
	}
	if w := request(h, "POST", "/api/v1/jobs", `{"type":"x","payload":"`+strings.Repeat("x", 1<<20)+`"}`); w.Code != 413 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/api/v1/jobs/nope", "/api/v1/jobs?limit=101", "/api/v1/jobs?offset=-1", "/api/v1/jobs?limit=x", "/api/v1/jobs?offset=1000001", "/api/v1/jobs?unknown=x", "/api/v1/jobs?limit=1&limit=2"} {
		if w := request(h, "GET", path, ""); w.Code != 400 {
			t.Errorf("%s: got %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/v1/jobs/" + uuid.NewString(), "/unknown"} {
		if w := request(h, "GET", path, ""); w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := request(h, "DELETE", "/api/v1/jobs", ""); w.Code != 405 || w.Header().Get("Allow") == "" {
		t.Fatal(w.Code)
	}
	r.err = errors.New("postgres password=secret SQL internal path")
	w := request(h, "GET", "/api/v1/jobs", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "SQL") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHealthAndReadiness(t *testing.T) {
	h := NewRouter(service.New(&memoryRepo{}), testLogger(), func(ctx context.Context) error { return errors.New("password") })
	if w := request(h, "GET", "/health", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"status":"ok"}` {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/ready", ""); w.Code != 503 || strings.Contains(w.Body.String(), "password") {
		t.Fatal(w.Code, w.Body.String())
	}
	h = NewRouter(service.New(&memoryRepo{}), testLogger(), func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("readiness must have deadline")
		}
		return nil
	})
	if w := request(h, "GET", "/ready", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestServerShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := NewServer("127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), testLogger())
	if err := Run(ctx, s, testLogger()); err != nil {
		t.Fatal(err)
	}
}
