package observability

import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPropagationAndMemoryExporter(t *testing.T) {
	ex := tracetest.NewInMemoryExporter()
	p := sdk.NewTracerProvider(sdk.WithSyncer(ex))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	t.Cleanup(func() { otel.SetTracerProvider(old); p.Shutdown(context.Background()) })
	ctx, s := Start(t.Context(), "create", attribute.String("job.id", "safe-test-id"))
	parent := TraceParent(ctx)
	if len(parent) != 55 {
		t.Fatal(parent)
	}
	s.End()
	linked, a := Start(Restore(t.Context(), parent), "execute")
	if trace.SpanContextFromContext(linked).TraceID() != trace.SpanContextFromContext(ctx).TraceID() {
		t.Fatal("async context changed")
	}
	a.End()
	spans := ex.GetSpans()
	if len(spans) != 2 || spans[1].Parent.SpanID() != spans[0].SpanContext.SpanID() {
		t.Fatal("parent correlation")
	}
	for _, bad := range []string{"", "garbage", "00-00000000000000000000000000000000-0000000000000000-01", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-zz"} {
		if trace.SpanContextFromContext(Restore(t.Context(), bad)).IsValid() {
			t.Fatal("accepted malformed parent")
		}
	}
}
func TestDisabledAndExporterFailureBoundedFlush(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	old := otel.GetTracerProvider()
	defer otel.SetTracerProvider(old)
	close, e := SetupTracing(t.Context(), false, server.URL, 1, "test")
	if e != nil {
		t.Fatal(e)
	}
	_, s := Start(t.Context(), "noop")
	s.End()
	close(t.Context())
	if calls.Load() != 0 || s.SpanContext().IsValid() {
		t.Fatal("disabled exported")
	}
	for _, ratio := range []float64{-1, 1.1, math.NaN(), math.Inf(1)} {
		if _, e := SetupTracing(t.Context(), true, server.URL, ratio, "test"); e == nil {
			t.Fatal("invalid sampler")
		}
	}
	close, e = SetupTracing(t.Context(), true, server.URL, 1, "test")
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 1000; n++ {
		_, s := Start(t.Context(), "overflow")
		s.End()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = close(ctx)
	if time.Since(start) > time.Second {
		t.Fatal("unbounded flush")
	}
}
