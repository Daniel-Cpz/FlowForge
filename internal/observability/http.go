package observability

import (
	"bufio"
	"fmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Normalize route templates even for errors; never reflect arbitrary URL/method.
func Route(path string) string {
	switch path {
	case "/health", "/ready", "/metrics", "/api/v1/ws", "/api/v1/jobs", "/api/v1/dead-letter", "/api/v1/workers", "/api/v1/schedules", "/api/v1/dashboard/summary":
		return path
	}
	p := strings.Split(path, "/")
	if len(p) >= 5 && p[1] == "api" && p[2] == "v1" && (p[3] == "jobs" || p[3] == "schedules") && p[4] != "" {
		base := "/api/v1/" + p[3] + "/{id}"
		if len(p) == 5 {
			return base
		}
		if len(p) == 6 && (p[5] == "cancel" || p[3] == "jobs" && (p[5] == "retry" || p[5] == "attempts")) {
			return base + "/" + p[5]
		}
	}
	return "unmatched"
}

type response struct {
	http.ResponseWriter
	code int
}

func (w *response) WriteHeader(code int) {
	if w.code != 0 {
		return
	}
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *response) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *response) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacking unavailable")
	}
	c, b, e := h.Hijack()
	if e == nil {
		w.code = 101
	}
	return c, b, e
}
func Middleware(next http.Handler, m *Metrics, logger *slog.Logger, metricsEnabled ...bool) http.Handler {
	enabled := len(metricsEnabled) == 0 || metricsEnabled[0]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			if !enabled {
				http.NotFound(w, r)
				return
			}
			if r.Method != "GET" && r.Method != "HEAD" {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", 405)
				return
			}
			m.Handler().ServeHTTP(w, r)
			return
		}
		method := r.Method
		switch method {
		case "GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS":
		default:
			method = "OTHER"
		}
		route := Route(r.URL.Path)
		ctx, span := Start(Extract(r.Context(), propagation.HeaderCarrier(r.Header)), "http.request", attribute.String("http.route", route), attribute.String("http.request.method", method))
		defer span.End()
		started := time.Now()
		rw := &response{ResponseWriter: w}
		next.ServeHTTP(rw, r.WithContext(WithMetrics(ctx, m)))
		code := rw.code
		if code == 0 {
			code = 200
		}
		span.SetAttributes(attribute.Int("http.response.status_code", code))
		if enabled {
			m.HTTP.WithLabelValues(route, method, fmt.Sprintf("%dxx", code/100)).Observe(time.Since(started).Seconds())
		}
		Correlated(ctx, logger).Info("HTTP request completed", "event", "http_request_finished", "route", route, "method", method, "status", code)
	})
}
