package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/infrastructure/postgres"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	"github.com/Daniel-Cpz/FlowForge/internal/service/execution"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"strings"
	"testing"
	"time"
)

func TestDurableTraceReplayWorkerAndCommittedMetrics(t *testing.T) {
	pool := migratedDatabase(t)
	repo := postgres.NewJobRepository(pool)
	m := observability.NewMetrics(repo.DashboardSummary)
	ctx := observability.WithMetrics(t.Context(), m)
	ex := tracetest.NewInMemoryExporter()
	p := sdk.NewTracerProvider(sdk.WithSyncer(ex))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer func() { otel.SetTracerProvider(old); p.Shutdown(context.Background()) }()
	key := "private-observability-key"
	input := service.CreateInput{Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":1}`), IdempotencyKey: &key}
	first, e := service.New(repo).Create(ctx, input)
	if e != nil || len(first.TraceParent) != 55 {
		t.Fatal(first, e)
	}
	replay, e := service.New(repo).Create(ctx, input)
	if e != nil || replay.TraceParent != first.TraceParent {
		t.Fatal("replay overwrote trace context", e)
	}
	data, _ := json.Marshal(first)
	if strings.Contains(string(data), "traceparent") || strings.Contains(string(data), first.TraceParent) {
		t.Fatal("API exposed trace metadata")
	}
	q, _, _, _ := redisQueue(t)
	if e = q.Publish(ctx, first.ID); e != nil {
		t.Fatal(e)
	}
	w := execution.New(repo, q, execution.Sleep{}, testLogger())
	if claimed, e := w.Handle(ctx, receive(t, q)); e != nil || !claimed {
		t.Fatal(claimed, e)
	}
	if testutil.ToFloat64(m.Submitted.WithLabelValues("created")) != 1 || testutil.ToFloat64(m.Submitted.WithLabelValues("replayed")) != 1 || testutil.ToFloat64(m.Attempts.WithLabelValues("succeeded")) != 1 {
		t.Fatal("committed counts")
	}
	spans := ex.GetSpans()
	seen := map[string]bool{}
	var traceID string
	for _, s := range spans {
		if s.Name == "job.create" && traceID == "" {
			traceID = s.SpanContext.TraceID().String()
		}
		if s.Name == "queue.receive" || s.Name == "attempt.execute" || s.Name == "claim" || s.Name == "finalize" {
			seen[s.Name] = true
			if s.SpanContext.TraceID().String() != traceID {
				t.Fatal("async trace lost", s.Name)
			}
		}
		for _, a := range s.Attributes {
			if strings.Contains(a.Value.AsString(), key) || strings.Contains(string(a.Key), "payload") {
				t.Fatal("span secret")
			}
		}
	}
	for _, n := range []string{"queue.receive", "attempt.execute", "claim", "finalize"} {
		if !seen[n] {
			t.Fatal("missing span", n)
		}
	}
	if _, e = pool.Exec(ctx, `UPDATE jobs SET traceparent='malformed' WHERE id=$1`, first.ID); e == nil {
		t.Fatal("trace constraint")
	}
}

func TestCommittedTelemetryHonorsCallerCancellation(t *testing.T) {
	pool := migratedDatabase(t)
	cfg := pool.Config()
	cfg.MaxConns = 1
	telemetry, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer telemetry.Close()
	held, err := telemetry.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release() // Saturate only the independent telemetry pool.
	repo := leaseRepo(t, pool).WithTelemetryPool(telemetry)
	id := registered(t, repo)
	j := createSleep(t, pool, `{"duration_ms":0}`)
	claimed, err := repo.Claim(t.Context(), j.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	m := observability.NewMetrics(nil)
	ctx, cancel := context.WithCancel(observability.WithMetrics(t.Context(), m))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- repo.Finalize(ctx, claimed, job.Succeeded, json.RawMessage(`{}`)) }()
	// Observe the real commit before cancelling a blocked diagnostic read.
	deadline := time.Now().Add(2 * time.Second)
	for {
		stored, e := repo.GetByID(t.Context(), j.ID)
		if e != nil {
			t.Fatal(e)
		}
		if stored.Status == job.Succeeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("business commit not visible")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal("telemetry changed committed result", e)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("telemetry detached from caller cancellation")
	}
	if testutil.ToFloat64(m.Attempts.WithLabelValues("succeeded")) != 0 {
		t.Fatal("blocked diagnostic read fabricated measurement")
	}
}

type failedTraceExporter struct{}

func (failedTraceExporter) ExportSpans(context.Context, []sdk.ReadOnlySpan) error {
	return errors.New("private exporter failure")
}
func (failedTraceExporter) Shutdown(context.Context) error { return nil }
func TestExporterFailureKeepsBusinessTruth(t *testing.T) {
	pool := migratedDatabase(t)
	p := sdk.NewTracerProvider(sdk.WithBatcher(failedTraceExporter{}, sdk.WithMaxQueueSize(1), sdk.WithMaxExportBatchSize(1), sdk.WithBatchTimeout(time.Millisecond)))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer func() {
		otel.SetTracerProvider(old)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		p.Shutdown(ctx)
	}()
	repo := postgres.NewJobRepository(pool)
	j, e := service.New(repo).Create(t.Context(), service.CreateInput{Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":0}`)})
	if e != nil {
		t.Fatal(e)
	}
	cancelled, e := repo.Cancel(t.Context(), j.ID)
	if e != nil || cancelled.Status != "CANCELLED" {
		t.Fatal(cancelled, e)
	}
	var intent int
	if e = pool.QueryRow(t.Context(), `SELECT count(*) FROM job_dispatch WHERE job_id=$1`, j.ID).Scan(&intent); e != nil || intent != 0 {
		t.Fatal("business intent", intent, e)
	}
	// Failed fenced finalize must not count a proposed success.
	m := observability.NewMetrics(nil)
	ctx := observability.WithMetrics(t.Context(), m)
	j.ID = uuid.New()
	if e = repo.Finalize(ctx, j, "SUCCEEDED", json.RawMessage(`{}`)); e == nil {
		t.Fatal("invalid finalize succeeded")
	}
	if testutil.ToFloat64(m.Attempts.WithLabelValues("succeeded")) != 0 {
		t.Fatal("false success metric")
	}
}
