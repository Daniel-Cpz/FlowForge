package config

import "testing"

func TestTelemetryConfig(t *testing.T) {
	for _, v := range []struct{ k, v string }{{"FLOWFORGE_METRICS_ENABLED", "invalid"}, {"FLOWFORGE_METRICS_ADDR", ":0"}, {"FLOWFORGE_OTEL_ENABLED", "invalid"}, {"FLOWFORGE_OTEL_SAMPLE_RATIO", "NaN"}, {"FLOWFORGE_OTEL_SAMPLE_RATIO", "1.1"}, {"FLOWFORGE_OTEL_SAMPLE_RATIO", "-0.1"}, {"FLOWFORGE_OTEL_EXPORTER_OTLP_ENDPOINT", "grpc://bad"}, {"FLOWFORGE_OTEL_EXPORTER_OTLP_ENDPOINT", "http://user:secret@host:4318"}} {
		t.Run(v.k+v.v, func(t *testing.T) {
			defaults(t)
			t.Setenv(v.k, v.v)
			if _, e := Load(); e == nil {
				t.Fatal("invalid config")
			}
		})
	}
}
