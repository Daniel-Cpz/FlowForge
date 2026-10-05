package httptransport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/google/uuid"
)

func assertError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var response struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != status || response.Error.Code != code || response.Error.Message == "" ||
		w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected error contract: %d %s", w.Code, w.Body.String())
	}
}

func TestCreateContractValid(t *testing.T) {
	for _, payload := range []string{`{}`, `[]`, `"hello"`, `42`, `true`, `null`, `{"a":1,"a":2}`} {
		t.Run(payload, func(t *testing.T) {
			repo := &memoryRepo{}
			h := NewRouter(service.New(repo), testLogger())
			w := request(h, "POST", "/api/v1/jobs", `{"type":"  inner  space  ","payload":`+payload+`}`)
			if w.Code != 201 || w.Header().Get("Content-Type") != "application/json" {
				t.Fatal(w.Code, w.Body.String())
			}
			var j domain.Job
			if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
				t.Fatal(err)
			}
			if j.Type != "inner  space" || j.Status != domain.Queued || j.AttemptCount != 0 || j.ID == uuid.Nil ||
				j.CreatedAt.IsZero() || j.CreatedAt.Location() != time.UTC || j.Priority != 0 || j.MaxAttempts != 3 || j.Timeout != 300 ||
				w.Header().Get("Location") != "/api/v1/jobs/"+j.ID.String() {
				t.Fatal("creation invariants", w.Body.String())
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"result", "idempotency_key", "assigned_worker", "lease_expiry", "started_at", "finished_at"} {
				if string(fields[name]) != "null" {
					t.Errorf("%s must be present and null", name)
				}
			}
		})
	}
	for _, body := range []string{
		`{"type":"` + strings.Repeat("界", 128) + `","payload":null,"priority":100,"max_attempts":100,"timeout":86400,"idempotency_key":"` + strings.Repeat("界", 255) + `"}`,
		`{"type":"x","payload":null,"priority":0,"max_attempts":1,"timeout":1,"idempotency_key":null}`,
		`{"type":"x","payload":false,"idempotency_key":" key with spaces "}`,
		`{"type":"\ud83d\ude00","payload":null,"idempotency_key":"\ud83d\ude00"}`,
		`{"type":"literal \\ud800","payload":null,"idempotency_key":"\ufffd"}`,
	} {
		repo := &memoryRepo{}
		w := request(NewRouter(service.New(repo), testLogger()), "POST", "/api/v1/jobs", body)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestCreateContractInvalid(t *testing.T) {
	for _, body := range []string{
		``, `{`, `null`, `[]`, `{}`, `{"payload":null}`, `{"type":null,"payload":{}}`,
		`{"type":"","payload":{}}`, `{"type":" \t ","payload":{}}`, `{"type":"x"}`,
		`{"type":1,"payload":{}}`, `{"type":"` + strings.Repeat("界", 129) + `","payload":{}}`,
		`{"Type":"x","payload":{}}`, `{"type":"x","Payload":{}}`,
		`{"type":"a","type":"b","payload":{}}`, `{"type":"x","payload":{},"payload":null}`,
		`{"type":"x","\u0074ype":"y","payload":{}}`,
		`{"type":"x","payload":{},"unknown":1}`, `{"type":"x","payload":{}}{}`,
		`{"type":"x","payload":{},"idempotency_key":""}`,
		`{"type":"x","payload":{},"idempotency_key":"   "}`,
		`{"type":"x","payload":{},"idempotency_key":true}`,
		`{"type":"x","payload":{},"idempotency_key":"` + strings.Repeat("界", 256) + `"}`,
		`{"type":"x","payload":{},"idempotency_key":"\u0000"}`,
		`{"type":"x\u0000","payload":{}}`,
		`{"type":"\ud800","payload":{}}`, `{"type":"\udc00","payload":{}}`,
		`{"type":"\ud800a","payload":{}}`, `{"type":"\ud800\u0041","payload":{}}`,
		`{"type":"x","payload":{},"idempotency_key":"\ud800"}`,
		"{\"type\":\"\xff\",\"payload\":{}}",
	} {
		t.Run(body, func(t *testing.T) {
			repo := &memoryRepo{}
			w := request(NewRouter(service.New(repo), testLogger()), "POST", "/api/v1/jobs", body)
			if w.Code != 400 || len(repo.jobs) != 0 {
				t.Fatal("invalid input persisted or accepted", w.Code, w.Body.String())
			}
		})
	}
	for field, values := range map[string][]string{
		"priority":     {`null`, `-1`, `101`, `1.5`, `1e0`, `"1"`, `true`, `999999999999999999999`},
		"max_attempts": {`null`, `0`, `-1`, `101`, `1.5`, `"3"`, `[]`},
		"timeout":      {`null`, `0`, `-1`, `86401`, `1.5`, `"300"`, `{}`},
	} {
		for _, value := range values {
			t.Run(field+"_"+value, func(t *testing.T) {
				repo := &memoryRepo{}
				body := `{"type":"x","payload":{},"` + field + `":` + value + `}`
				w := request(NewRouter(service.New(repo), testLogger()), "POST", "/api/v1/jobs", body)
				if w.Code != 400 || len(repo.jobs) != 0 {
					t.Fatal("invalid numeric field accepted", w.Code)
				}
			})
		}
	}
	for _, field := range []string{"id", "status", "result", "attempt_count", "assigned_worker", "lease_expiry", "created_at", "started_at", "finished_at",
		"Priority", "Max_attempts", "Timeout", "Idempotency_key"} {
		repo := &memoryRepo{}
		w := request(NewRouter(service.New(repo), testLogger()), "POST", "/api/v1/jobs", `{"type":"x","payload":{},"`+field+`":null}`)
		assertError(t, w, 400, "INVALID_JSON")
		if len(repo.jobs) != 0 {
			t.Fatal("injected field persisted")
		}
	}
}

func TestBodyLimit(t *testing.T) {
	const prefix, suffix = `{"type":"x","payload":"`, `"}`
	valid := prefix + strings.Repeat("x", (1<<20)-len(prefix)-len(suffix)) + suffix
	h := NewRouter(service.New(&memoryRepo{}), testLogger())
	if w := request(h, "POST", "/api/v1/jobs", valid); w.Code != 201 {
		t.Fatal("body at limit rejected", w.Code)
	}
	for _, body := range []string{valid + " ", strings.Repeat("!", (1<<20)+1)} {
		r := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(body))
		r.ContentLength = -1
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assertError(t, w, 413, "BODY_TOO_LARGE")
	}
}

func TestListContract(t *testing.T) {
	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ids := []uuid.UUID{uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002"), uuid.MustParse("00000000-0000-0000-0000-000000000003")}
	repo := &memoryRepo{}
	for _, id := range ids {
		repo.jobs = append(repo.jobs, domain.Job{ID: id, CreatedAt: timestamp})
	}
	h := NewRouter(service.New(repo), testLogger())
	var next *string
	for i, want := range []uuid.UUID{ids[2], ids[1], ids[0]} {
		path := "/api/v1/jobs?limit=1"
		if next != nil {
			path += "&cursor=" + url.QueryEscape(*next)
		}
		w := request(h, "GET", path, "")
		var page struct {
			Jobs []domain.Job `json:"jobs"`
			Next *string      `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(page.Jobs) != 1 || page.Jobs[0].ID != want || (page.Next != nil) != (i < 2) {
			t.Fatal("incorrect page", w.Code, w.Body.String())
		}
		next = page.Next
	}
	for _, path := range []string{"/api/v1/jobs", "/api/v1/jobs?limit=100"} {
		if w := request(h, "GET", path, ""); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	empty := request(NewRouter(service.New(&memoryRepo{}), testLogger()), "GET", "/api/v1/jobs", "")
	if strings.TrimSpace(empty.Body.String()) != `{"jobs":[],"next_cursor":null}` {
		t.Fatal("empty response", empty.Body.String())
	}
	for _, query := range []string{"limit=0", "limit=-1", "limit=101", "limit=x", "limit=", "limit=1&limit=2", "offset=0", "unknown=1", "limit=%xx", "limit=1;cursor=x"} {
		assertError(t, request(h, "GET", "/api/v1/jobs?"+query, ""), 400, "INVALID_INPUT")
	}
	for _, cursor := range []string{"", "bad", "%", strings.Repeat("A", 513), base64.RawURLEncoding.EncodeToString([]byte(`{}`))} {
		assertError(t, request(h, "GET", "/api/v1/jobs?cursor="+url.QueryEscape(cursor), ""), 400, "INVALID_CURSOR")
	}
	assertError(t, request(h, "GET", "/api/v1/jobs?cursor=a&cursor=b", ""), 400, "INVALID_CURSOR")
}

type contextRepo struct{ seen context.Context }

func (r *contextRepo) Create(ctx context.Context, j *domain.Job) (domain.CreateDisposition, error) {
	r.seen = ctx
	return domain.Created, ctx.Err()
}
func (r *contextRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	r.seen = ctx
	return nil, ctx.Err()
}
func (r *contextRepo) List(ctx context.Context, limit int, after *domain.PageCursor) ([]domain.Job, error) {
	r.seen = ctx
	return nil, ctx.Err()
}

func TestRequestCancellationAndErrorMapping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, methodPath := range [][2]string{{"POST", "/api/v1/jobs"}, {"GET", "/api/v1/jobs"}, {"GET", "/api/v1/jobs/" + uuid.NewString()}} {
		repo := &contextRepo{}
		h := NewRouter(service.New(repo), testLogger())
		r := httptest.NewRequest(methodPath[0], methodPath[1], strings.NewReader(`{"type":"x","payload":null}`)).WithContext(ctx)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if repo.seen != ctx || !errors.Is(repo.seen.Err(), context.Canceled) {
			t.Fatal("request context did not reach repository")
		}
		assertError(t, w, 500, "INTERNAL_ERROR")
	}
	h := NewRouter(service.New(&memoryRepo{err: errors.New("postgres password=secret schema private SQL")}), testLogger())
	for _, methodPath := range [][2]string{{"POST", "/api/v1/jobs"}, {"GET", "/api/v1/jobs"}, {"GET", "/api/v1/jobs/" + uuid.NewString()}} {
		w := request(h, methodPath[0], methodPath[1], `{"type":"x","payload":null}`)
		assertError(t, w, 500, "INTERNAL_ERROR")
		if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "SQL") {
			t.Fatal("driver detail leaked")
		}
	}
	h = NewRouter(service.New(&memoryRepo{}), testLogger())
	assertError(t, request(h, "GET", "/api/v1/jobs/nope", ""), 400, "INVALID_ID")
	assertError(t, request(h, "GET", "/api/v1/jobs/"+uuid.NewString(), ""), 404, "JOB_NOT_FOUND")
}
