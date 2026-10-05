package observability

import (
	"context"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/dashboard"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrivateRegistrySnapshotAndBoundedRoutes(t *testing.T) {
	fail := false
	m := NewMetrics(func(context.Context) (*dashboard.Summary, error) {
		if fail {
			return nil, errors.New("private driver detail")
		}
		return &dashboard.Summary{Jobs: map[string]int64{"QUEUED": 3}, QueueDepth: 2}, nil
	})
	_ = NewMetrics(nil)
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(409) }), m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for n := 0; n < 100; n++ {
		for _, path := range []string{"/api/v1/jobs/" + uuid.NewString(), "/arbitrary/" + uuid.NewString()} {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("SECRET_METHOD", path, nil))
		}
	}
	families, e := m.Registry.Gather()
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range families {
		for _, sample := range f.Metric {
			for _, label := range sample.Label {
				if strings.Contains(label.GetValue(), "SECRET") || strings.Contains(label.GetValue(), "arbitrary/") {
					t.Fatal("unbounded label")
				}
			}
		}
		if f.GetName() == "flowforge_http_request_duration_seconds" && len(f.Metric) != 2 {
			t.Fatal("route cardinality", len(f.Metric))
		}
	}
	if e := testutil.CollectAndCompare(newSnapshotCollector(func(context.Context) (*dashboard.Summary, error) { return nil, errors.New("private") }), strings.NewReader("# HELP flowforge_snapshot_up Authoritative PostgreSQL snapshot; use max across API replicas.\n# TYPE flowforge_snapshot_up gauge\nflowforge_snapshot_up 0\n"), "flowforge_snapshot_up"); e != nil {
		t.Fatal(e)
	}
	m.Attempt("succeeded", .05, false)
	m.Attempt(uuid.NewString(), 99, true)
	if testutil.ToFloat64(m.Attempts.WithLabelValues("succeeded")) != 1 {
		t.Fatal("attempt count")
	}
	m.Worker(1, 4)
	if testutil.ToFloat64(m.Utilisation) != .25 {
		t.Fatal("utilisation")
	}
	fail = true
	rw := httptest.NewRecorder()
	m.Handler().ServeHTTP(rw, httptest.NewRequest("GET", "/metrics", nil))
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "flowforge_snapshot_up 0") || strings.Contains(rw.Body.String(), "private") {
		t.Fatal("scrape failure handling")
	}
}
func TestRouteTemplates(t *testing.T) {
	for p, want := range map[string]string{"/api/v1/jobs/abc": "/api/v1/jobs/{id}", "/api/v1/jobs/abc/attempts": "/api/v1/jobs/{id}/attempts", "/api/v1/schedules/abc/cancel": "/api/v1/schedules/{id}/cancel", "/private/foo": "unmatched", "/api/v1/jobs/abc/arbitrary": "unmatched"} {
		if Route(p) != want {
			t.Fatal(p, Route(p))
		}
	}
}
func TestWorkerMetricsJoinedShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- RunWorker(ctx, "127.0.0.1:0", NewMetrics(nil), func(ctx context.Context) error { <-ctx.Done(); return nil })
	}()
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join")
	}
}

func TestBoundAtomicWorkerGauge(t *testing.T) {
	m := NewMetrics(nil)
	m.BindWorker(func() int64 { return 2 }, 4)
	families, e := m.Registry.Gather()
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range families {
		if f.GetName() == "flowforge_worker_active_jobs" && f.Metric[0].Gauge.GetValue() != 2 {
			t.Fatal("active count")
		}
		if f.GetName() == "flowforge_worker_utilisation_ratio" && f.Metric[0].Gauge.GetValue() != .5 {
			t.Fatal("ratio")
		}
	}
}
