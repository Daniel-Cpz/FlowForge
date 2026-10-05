package observability

import (
	"context"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/dashboard"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"time"
)

// Each process owns one private registry. No default/global registration and no
// IDs, error strings, capabilities or user input are accepted as labels.
type Metrics struct {
	Registry                                                                         *prometheus.Registry
	Submitted, Attempts, Retries, Recoveries, Redrives, Dispatch, Published, Dropped *prometheus.CounterVec
	Duration, HTTP                                                                   *prometheus.HistogramVec
	Active, Capacity, Utilisation, Connections                                       prometheus.Gauge
	RenewFailures, HeartbeatFailures, SlowClients                                    prometheus.Counter
}
type metricsKey struct{}

// Configure once before serving. Scrapes read the Worker's actual atomic slot
// count; concurrent execution notifications cannot leave an out-of-order gauge.
func (m *Metrics) BindWorker(active func() int64, capacity int64) {
	m.Registry.Unregister(m.Active)
	m.Registry.Unregister(m.Utilisation)
	m.Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "flowforge_worker_active_jobs", Help: "worker_active_jobs for this process."}, func() float64 { return float64(active()) }), prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "flowforge_worker_utilisation_ratio", Help: "worker_utilisation_ratio for this process."}, func() float64 { return float64(active()) / float64(capacity) }))
	m.Capacity.Set(float64(capacity))
}

func WithMetrics(ctx context.Context, m *Metrics) context.Context {
	return context.WithValue(ctx, metricsKey{}, m)
}
func MetricsFrom(ctx context.Context) *Metrics { m, _ := ctx.Value(metricsKey{}).(*Metrics); return m }
func NewMetrics(snapshot func(context.Context) (*dashboard.Summary, error)) *Metrics {
	m := &Metrics{Registry: prometheus.NewRegistry()}
	counter := func(name, label string, values ...string) *prometheus.CounterVec {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "flowforge_" + name, Help: name + "; process-local, resets at restart."}, []string{label})
		m.Registry.MustRegister(c)
		for _, v := range values {
			c.WithLabelValues(v)
		}
		return c
	}
	gauge := func(name string) prometheus.Gauge {
		g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "flowforge_" + name, Help: name + " for this process."})
		m.Registry.MustRegister(g)
		return g
	}
	simple := func(name string) prometheus.Counter {
		c := prometheus.NewCounter(prometheus.CounterOpts{Name: "flowforge_" + name, Help: name + "; process-local."})
		m.Registry.MustRegister(c)
		return c
	}
	m.Submitted = counter("jobs_submitted_total", "disposition", "created", "replayed", "conflict")
	m.Attempts = counter("job_attempts_total", "outcome", "succeeded", "retryable_failed", "permanent_failed", "timed_out", "cancelled", "lease_expired")
	m.Retries = counter("job_retries_total", "reason", "retryable_failed", "timed_out", "lease_expired")
	m.Recoveries = counter("job_recoveries_total", "result", "settled", "error")
	m.Redrives = counter("job_redrives_total", "result", "success", "conflict", "error")
	m.Dispatch = counter("dispatch_total", "result", "success", "error")
	m.Published = counter("realtime_events_published_total", "result", "success", "error")
	m.Dropped = counter("realtime_events_dropped_total", "reason", "queue_full", "publish_failed", "subscription_lost")
	m.Duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "flowforge_job_attempt_duration_seconds", Help: "Committed Attempt started_at to finished_at, including recovery wait.", Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2, 5, 10, 30, 60, 300}}, []string{"outcome"})
	m.HTTP = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "flowforge_http_request_duration_seconds", Help: "HTTP lifecycle seconds; WS includes connection lifetime.", Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2, 5, 10}}, []string{"route", "method", "status_class"})
	m.Registry.MustRegister(m.Duration, m.HTTP)
	m.Active = gauge("worker_active_jobs")
	m.Capacity = gauge("worker_concurrency")
	m.Utilisation = gauge("worker_utilisation_ratio")
	m.Connections = gauge("websocket_connections")
	m.RenewFailures = simple("worker_lease_renew_failures_total")
	m.HeartbeatFailures = simple("worker_heartbeat_failures_total")
	m.SlowClients = simple("websocket_slow_client_disconnects_total")
	if snapshot != nil {
		m.Registry.MustRegister(newSnapshotCollector(snapshot))
	}
	return m
}
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{MaxRequestsInFlight: 2, Timeout: 2 * time.Second})
}
func (m *Metrics) Worker(active, capacity int64) {
	if m == nil {
		return
	}
	m.Active.Set(float64(active))
	m.Capacity.Set(float64(capacity))
	if capacity > 0 {
		m.Utilisation.Set(float64(active) / float64(capacity))
	}
}
func (m *Metrics) Attempt(outcome string, seconds float64, retrying bool) {
	if m == nil {
		return
	}
	switch outcome {
	case "succeeded", "retryable_failed", "permanent_failed", "timed_out", "cancelled", "lease_expired":
	default:
		return
	}
	m.Attempts.WithLabelValues(outcome).Inc()
	m.Duration.WithLabelValues(outcome).Observe(max(0, seconds))
	if retrying && (outcome == "retryable_failed" || outcome == "timed_out" || outcome == "lease_expired") {
		m.Retries.WithLabelValues(outcome).Inc()
	}
}

type snapshotCollector struct {
	read                                func(context.Context) (*dashboard.Summary, error)
	gate                                chan struct{}
	jobs, workers, schedules, depth, up *prometheus.Desc
}

func newSnapshotCollector(read func(context.Context) (*dashboard.Summary, error)) *snapshotCollector {
	desc := func(n string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc("flowforge_"+n, "Authoritative PostgreSQL snapshot; use max across API replicas.", labels, nil)
	}
	return &snapshotCollector{read: read, gate: make(chan struct{}, 1), jobs: desc("jobs_current", "status"), workers: desc("workers_current", "status"), schedules: desc("schedules_current", "status"), depth: desc("queue_depth"), up: desc("snapshot_up")}
}
func (c *snapshotCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.jobs, c.workers, c.schedules, c.depth, c.up} {
		ch <- d
	}
}
func (c *snapshotCollector) Collect(ch chan<- prometheus.Metric) {
	emit := func(d *prometheus.Desc, n float64, label ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, n, label...)
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	default:
		emit(c.up, 0)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := c.read(ctx)
	if err != nil || s == nil {
		emit(c.up, 0)
		return
	}
	emit(c.up, 1)
	for _, v := range []string{"QUEUED", "RUNNING", "SUCCEEDED", "FAILED", "RETRYING", "DEAD_LETTER", "CANCELLED", "TIMED_OUT"} {
		emit(c.jobs, float64(s.Jobs[v]), v)
	}
	for _, v := range []string{"ONLINE", "IDLE", "BUSY", "DRAINING", "OFFLINE"} {
		emit(c.workers, float64(s.Workers[v]), v)
	}
	for _, v := range []string{"ACTIVE", "CANCELLED"} {
		emit(c.schedules, float64(s.Schedules[v]), v)
	}
	emit(c.depth, float64(s.QueueDepth))
}
