# Local observability

Phase 9 adds Prometheus client_golang 1.23.2, OTel Go 1.38.0 with OTLP/HTTP,
Grafana provisioning and disposable failure/benchmark harnesses. These endpoints
have no production authentication/TLS/SLA; keep them internal or loopback.

## Running

On a fresh compatible database only:

```sh
FLOWFORGE_OTEL_ENABLED=true docker compose --profile observability up --build --scale worker=2
```

Prometheus is localhost:9090, Grafana localhost:3000/d/flowforge-overview (anonymous
Viewer only), API /metrics on its existing listener, Worker /metrics internally
on :9091. Collector accepts internal OTLP HTTP :4318 and exports sanitized spans
to its debug logs. No trace UI, Loki, Tempo, Jaeger or leader election is needed.
Prometheus DNS A discovery queries `worker` every 5s; real scaled targets must be
UP. Run `scripts/phase9-failure.ps1` for disposable acceptance rather than touching
retained data. `observability/experiment.yml` uses uniquely named isolated networks,
ephemeral PG/Redis and tmpfs telemetry data. No Docker socket is mounted.

The retained development DB is schema 4 with one duplicate non-null key group.
000005 continues to reject upgrade; Phase 9 does not repair/bypass it. Migration
000008 appends an internal traceparent column; stop old processes before upgrade.
Down loses trace metadata, preserves Jobs/Attempts, and is not failure recovery.
Mixed historical/current binaries are unsupported.

## Metrics inventory and definitions

All metric names below have a `flowforge_` prefix. Each process owns a private
registry; process counters reset on restart and may be missed between scrapes.
They are diagnostic observations, not a durable audit/ billing source.

| Metric | Meaning / finite labels |
|---|---|
| jobs_submitted_total | Successfully committed created/replayed; conflict counts actual conflicting Create calls; `disposition` |
| job_attempts_total | Committed finished Attempt, `outcome`: succeeded, retryable_failed, permanent_failed, timed_out, cancelled, lease_expired |
| job_retries_total | Committed settlement to RETRYING, `reason`: retryable_failed, timed_out, lease_expired; not a retry notification count |
| job_recoveries_total | Settled expired Attempts or failed recovery scan, `result`: settled/error |
| job_redrives_total | Successful commit or failed control call, `result`: success/conflict/error |
| dispatch_total | Redis publication call result: success/error; success is notification publication, not execution |
| jobs_current | PostgreSQL snapshot count, all eight `status` values including explicit zeros |
| workers_current | Registry counts ONLINE/IDLE/BUSY/DRAINING/OFFLINE; historical identities remain |
| schedules_current | Snapshot ACTIVE/CANCELLED count |
| queue_depth | Due QUEUED rows with remaining budget; includes unmatched capabilities; excludes future scheduled rows and RETRYING |
| snapshot_up | 1 for valid fresh PG snapshot; 0 on read error/busy; failed scrape omits state samples rather than inventing zeros |
| worker_active_jobs | Actual atomic occupied execution slots at collection; includes final persistence/ACK phase |
| worker_concurrency | Configured C; no worker UUID label |
| worker_utilisation_ratio | active/C; not CPU utilisation |
| worker_lease_renew_failures_total | Failed renewal before local execution cancellation |
| worker_heartbeat_failures_total | Heartbeat failure before fail-fast drain |
| job_attempt_duration_seconds | PG Attempt finished_at-started_at, by outcome; recovery includes lease-expiry wait, not pure executor CPU/time |
| http_request_duration_seconds | Request lifecycle, normalized route/method/status_class; WS includes connection lifetime, excluded from API quantile panels |
| websocket_connections | Established clients in this API hub; pending handshakes excluded |
| websocket_slow_client_disconnects_total | Full bounded per-client buffer caused disconnect |
| realtime_events_published_total | Transient publish success/error (zero subscribers is still publication success) |
| realtime_events_dropped_total | queue_full/publish_failed/subscription_lost; subscription loss counts disconnects, not an unknowable exact missed-event total |

Global state uses one PG statement per scrape with a one-second deadline and
one concurrent collection. A separate lazy two-connection telemetry PG pool
isolates scrapes/Attempt measurements from the execution pool. Post-commit Attempt
measurement reads have a one-second bound, retain the caller/batch cancellation
deadline and can be lost on DB/telemetry failure;
they cannot change the business commit or ACK authority. Scrape requests are capped
at two with a two-second HTTP bound. Worker metrics HTTP has bounded reads/writes
and joined shutdown; listener failure is reported and initiates drain.

Use `max without(instance,job)(flowforge_jobs_current)` (similarly Workers,
Schedules), never sum duplicate global state from API replicas. Sum process
counters/rates and per-instance Worker gauges as appropriate. Histogram quantiles:
`histogram_quantile(0.95,sum by(le)(rate(flowforge_job_attempt_duration_seconds_bucket[1m])))`.
P50/P99 use 0.5/0.99. Bucket interpolation estimates differ from loadgen's exact
nearest-rank sample quantiles. At low sample rates/empty windows, quantiles may
be NaN; no synthetic performance values are substituted.

Labels never include Job/Worker/Schedule IDs, idempotency keys, trace IDs, payload,
raw errors, capabilities or arbitrary task types. Unknown routes normalize to
`unmatched`; unknown methods to `OTHER`. Routes use `{id}` templates. HTTP status
labels are finite classes. Trace/log identifiers are attributes, not metric labels.

## Tracing and failure isolation

Incoming valid W3C traceparent is extracted (no baggage). Job Create starts a
span and persists its context with Job/dispatch creation in the same transaction.
Keyed replay keeps the winner's context; metadata is excluded from identity and
ordinary API JSON. Recurring Jobs persist the materialization context. Dispatch
loads the durable parent; queue receive reloads PG then restores it. Claim,
Execute and Finalize form the execution branch; retry/recovery/redrive preserve
the Job context. Maintenance/control committed observations correlate Job/Attempt
logs. Redis delivery stays exactly version + Job ID. Lost Streams/republication
does not erase the PG trace parent. No payload/results/keys/driver errors enter spans.

Tracing defaults disabled: no exporter/network side effect. Enable with
FLOWFORGE_OTEL_ENABLED, FLOWFORGE_OTEL_EXPORTER_OTLP_ENDPOINT (HTTP URL),
FLOWFORGE_OTEL_SAMPLE_RATIO (finite 0..1). Parent-based ratio sampling retains
valid parent sampling decisions. The SDK uses 256 queued spans, max batch 64,
one-second batch/export deadlines, no retries, and a two-second shutdown flush.
Saturation/outage drops traces and emits a sanitized warning, never rollback.
OTLP uses HTTP only; gRPC/protobuf modules are indirect official exporter/protocol
dependencies, with no gRPC listener/client configured by FlowForge.

Structured execution/request logs include trace_id/span_id when active and
job_id/worker_id/attempt_number at relevant boundaries. Histogram/counter lifetime
and lossy traces cannot prove exactly-once execution or side effects.

## Failure injection and benchmarking

`scripts/phase9-failure.ps1` runs two Workers, Prometheus, Grafana, Collector and
API in a generated project. It verifies hard crash/expired Attempt/takeover,
Redis loss/durable queued intent/republication, one-owner network partition and
fencing, exact API Pub/Sub socket loss/DEGRADED-LIVE/REST repair, exporter outage,
metrics confidentiality, discovery, queries and real Job/Attempt trace correlation.
Cleanup validates ownership and exact paths; finally removes only its generated
containers/ephemeral volumes/network/env file. Retained schema/duplicate audit must remain unchanged.

`scripts/phase9-benchmark.ps1` fixes C=1, counts 1/4/8/16, 500 Jobs, SLEEP 25ms,
request concurrency 16, 50 warmup Jobs (excluded), >=3 repeats; every repeat gets
a fresh PG/Redis project. Tracing is disabled, metrics enabled, logging WARN.
Loadgen is HTTP-only, bounded to 128 requests/pollers, 50ms poll floor, five-second
request timeout and overall max-wait; it outputs JSON on success or partial timeout.
No unbounded per-Job polling goroutines. Throughput = successful Jobs divided by
whole measured submission+poll completion wall time. Queue latency = first
started_at-max(created_at,scheduled_at); end-to-end = finished_at-created_at;
only single-attempt samples qualify. Nonnegative clamps avoid negative clock-skew
values; host/DB clock consistency is a measurement limitation. Nearest-rank:
sorted[ceil(q*N)-1], empty=0, singleton=itself. Error rates report failed submissions,
terminal errors and overall non-success separately. Poll errors are counted too.

See [baseline](benchmarks/phase-9-baseline.md) for actual machine/repetitions,
variance and limited interpretation. This is not a CPU saturation proof, distributed
load test, benchmark of real CPU work or a production SLA. Heavy matrices are
manual; CI tests logic, configuration and cleanup guards only.

References: [Prometheus Go client](https://pkg.go.dev/github.com/prometheus/client_golang/prometheus),
[DNS discovery](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#dns_sd_config),
[OTel Go exporters](https://opentelemetry.io/docs/languages/go/exporters/).
