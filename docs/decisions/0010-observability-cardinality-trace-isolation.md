# ADR 0010: Bounded telemetry and durable trace context

Status: Accepted (Phase 9).

Process-private Prometheus registries avoid accidental duplicate/global metric
registration. Finite enum labels and normalized HTTP route templates prevent
user input from creating unbounded series. Global gauges read a fresh PG
statement; replicas use max, process counters use sum/rate. Separate bounded
telemetry connections isolate scrapes from Worker business slots.

First-create W3C traceparent is durable internal Job metadata in append-only
migration 000008. It does not alter canonical submission identity, replay, API
JSON or Redis delivery. Async dispatch/Worker execution reload it from PG;
retry/recovery/redrive retain it, recurring materialization creates a context.
IDs are trace/log attributes only. Sampling/export are diagnostic, never authority.

OTLP HTTP uses a bounded nonblocking SDK queue and bounded export/flush, with
sanitized failures. Metrics only count committed Attempt outcomes; telemetry
reads can be lost but cannot roll back or invent success. Worker metrics servers
join on shutdown. Grafana/Prometheus/Collector are optional local components;
authentication/TLS/alerting/deployment remain separate future work.

Tradeoffs: process counters reset/may miss crashed-process increments, snapshot
scans cost grows with tables, histograms approximate quantiles, and lossy traces
cannot replace durable Attempt history. Reproducible isolated HTTP load tests and
failure experiments supply evidence within recorded workload/host limits.
