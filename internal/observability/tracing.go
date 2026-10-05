package observability

import (
	"context"
	"errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"log/slog"
	"math"
	"time"
)

var propagator = propagation.TraceContext{}

func ValidRatio(r float64) bool { return !math.IsNaN(r) && !math.IsInf(r, 0) && r >= 0 && r <= 1 }

// Disabled does not construct an exporter or initiate any network work.
func SetupTracing(ctx context.Context, enabled bool, endpoint string, ratio float64, process string) (func(context.Context) error, error) {
	if !ValidRatio(ratio) {
		return nil, errors.New("invalid trace sample ratio")
	}
	if !enabled {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) { slog.Warn("Telemetry export failed", "event", "trace_export_failed") }))
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint), otlptracehttp.WithTimeout(time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
	if err != nil {
		return nil, errors.New("trace exporter configuration failed")
	}
	provider := sdk.NewTracerProvider(sdk.WithSampler(sdk.ParentBased(sdk.TraceIDRatioBased(ratio))), sdk.WithResource(resource.NewSchemaless(attribute.String("service.name", "flowforge-"+process))), sdk.WithBatcher(exporter, sdk.WithMaxQueueSize(256), sdk.WithMaxExportBatchSize(64), sdk.WithBatchTimeout(time.Second), sdk.WithExportTimeout(time.Second)))
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("flowforge").Start(ctx, name, trace.WithAttributes(attrs...))
}
func TraceParent(ctx context.Context) string {
	c := propagation.MapCarrier{}
	propagator.Inject(ctx, c)
	return c.Get("traceparent")
}
func Restore(ctx context.Context, parent string) context.Context {
	if len(parent) != 55 {
		return ctx
	}
	restored := propagator.Extract(ctx, propagation.MapCarrier{"traceparent": parent})
	if !trace.SpanContextFromContext(restored).IsValid() {
		return ctx
	}
	return restored
}
func Extract(ctx context.Context, c propagation.TextMapCarrier) context.Context {
	return propagator.Extract(ctx, c)
}
func Correlated(ctx context.Context, logger *slog.Logger) *slog.Logger {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		return logger.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	return logger
}
