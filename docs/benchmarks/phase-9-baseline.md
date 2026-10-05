# Phase 9 local SLEEP baseline

Date: 2026-10-05 UTC (Australia/Sydney UTC+11).
Measured source: **4ca87bc6c20e7c496080d942d9fb89150ad8bc95**.
Command: `./scripts/phase9-benchmark.ps1` (default parameters).
Final matrix PASS: 12 repetitions, 6,000/6,000 measured Jobs SUCCEEDED;
zero submit errors, terminal errors, unfinished Jobs or poll errors.

## Environment

| Item | Recorded value |
|---|---|
| Host OS | Microsoft Windows 10.0.26200, Docker Desktop Linux containers |
| CPU | Intel(R) Core(TM) Ultra 7 270K Plus, 24 logical cores |
| Host RAM | 33,847,914,496 bytes (~31.52 GiB) |
| Docker engine | 29.8.0; Linux VM reports 24 CPUs and 16,511,033,344 bytes (~15.38 GiB) RAM |
| Go | 1.26.8 tools/build image, module targets Go 1.26 |
| Dependencies | PostgreSQL 18-alpine; Redis 8.2-alpine |
| Background load | Windows desktop and retained development services remained running; documentation editing/short progress reads occurred. No dedicated/idle-machine claim. Go tests and fault smoke finished before this matrix. |
| CPU/memory profiling | NOT RUN; no saturation attribution or resource-normalized result |

Final projects start at ffp9-20261005101713-6e5b08ad and end at ffp9-20261005102254-0380f949.
Generated project timestamps are UTC. Each repetition starts a fresh migrated
schema-8 PostgreSQL/Redis pair on its own network and namespace; cleanup audit
preserves retained schema=4 and duplicate groups=1. Tmpfs PG/Redis storage is a
deliberate local measurement condition, not a production durability test.

## Fixed parameters and measurement

Worker processes 1/4/8/16, **C=1** each; one API; HTTP request concurrency 16;
500 immediate SLEEP Jobs, duration 25ms, max_attempts=1. Each repetition runs
50 warmup Jobs first, excluded from the measured summary (their rows remain in
that repetition's otherwise fresh DB). Three independent repeats per count.
Metrics enabled, tracing disabled, WARN logging; Prometheus/Grafana/Collector
profile disabled during measurement. Identical API/PG/Redis and lease parameters
(3s lease, 1s renewal/heartbeat, 4s offline) from experiment.yml; PG connection
limit 300. Docker builds are excluded from measured wall time.

Loadgen uses ordinary HTTP only; fixed request/poll pools, five-second request
timeout, 50ms poll floor, five-minute measured completion deadline. Throughput
is successful Jobs / whole submission-and-poll-completion wall time. It includes
poll detection/API/dispatch/DB effects and does not isolate executor time.
Queue latency is first started_at - max(created_at,scheduled_at); end-to-end is
finished_at - created_at. Only single-Attempt complete timestamp samples qualify.
Nonnegative clamps handle negative clock deltas; clock consistency is a limitation.
P50/P95/P99 use sorted[ceil(q*N)-1], **nearest rank**, not histogram interpolation.
All 500 Jobs succeed with one Attempt in each measured repetition.

## Raw repetitions

Seconds for wall/latencies. Quantile cells show P50 / P95 / P99.

| Workers/repeat | Wall s | Jobs/s | Queue s | End-to-end s | Submit/terminal/poll errors |
|---|---|---|---|---|---|
| 1/1 | 15.083 | 33.15 | 7.790 / 14.271 / 14.848 | 7.818 / 14.298 / 14.874 | 0 / 0 / 0 |
| 1/2 | 15.076 | 33.17 | 7.775 / 14.229 / 14.803 | 7.802 / 14.256 / 14.830 | 0 / 0 / 0 |
| 1/3 | 15.077 | 33.16 | 7.785 / 14.262 / 14.838 | 7.811 / 14.288 / 14.865 | 0 / 0 / 0 |
| 4/1 | 28.088 | 17.80 | 17.396 / 27.797 / 27.940 | 17.423 / 27.824 / 27.967 | 0 / 0 / 0 |
| 4/2 | 3.912 | 127.80 | 1.793 / 3.645 / 3.786 | 1.820 / 3.671 / 3.813 | 0 / 0 / 0 |
| 4/3 | 19.822 | 25.22 | 10.652 / 18.545 / 19.442 | 10.679 / 18.572 / 19.469 | 0 / 0 / 0 |
| 8/1 | 4.744 | 105.39 | 2.235 / 3.526 / 4.596 | 2.261 / 3.553 / 4.623 | 0 / 0 / 0 |
| 8/2 | 5.209 | 95.99 | 2.221 / 5.020 / 5.079 | 2.247 / 5.046 / 5.106 | 0 / 0 / 0 |
| 8/3 | 2.275 | 219.78 | 1.292 / 2.079 / 2.138 | 1.318 / 2.106 / 2.165 | 0 / 0 / 0 |
| 16/1 | 31.039 | 16.11 | 30.486 / 30.863 / 30.897 | 30.512 / 30.889 / 30.924 | 0 / 0 / 0 |
| 16/2 | 30.786 | 16.24 | 3.517 / 30.622 / 30.655 | 3.544 / 30.648 / 30.682 | 0 / 0 / 0 |
| 16/3 | 6.128 | 81.59 | 2.834 / 5.225 / 5.513 | 2.860 / 5.252 / 5.540 | 0 / 0 / 0 |

Machine-readable full-precision summaries (including rates/classification):
[final results](phase-9-results.json). No slow repetition was omitted.

## Aggregates and variance

Arithmetic means of the three per-run throughputs and per-run P95 values;
**means of quantiles are not a pooled-job percentile**. SD is sample standard
deviation (N-1); CV=SD/mean. These are descriptive results, not confidence bounds.

| Workers | Mean jobs/s | SD jobs/s | CV | Min–max jobs/s | Mean queue P95 s | Mean end-to-end P95 s | Relative to 1 |
|---|---|---|---|---|---|---|---|
| 1 | 33.16 | 0.01 | 0.03% | 33.15–33.17 | 14.254 | 14.281 | 1.00x |
| 4 | 56.94 | 61.48 | 107.96% | 17.80–127.80 | 16.662 | 16.689 | 1.72x |
| 8 | 140.39 | 68.92 | 49.09% | 95.99–219.78 | 3.542 | 3.568 | 4.23x |
| 16 | 37.98 | 37.77 | 99.44% | 16.11–81.59 | 22.236 | 22.263 | 1.15x |

The final 1-to-4 mean throughput change is 71.72%.
Eight Workers average 140.39 jobs/s; sixteen average
37.98 jobs/s, a -72.95%
change from eight. Large variation and slower high-count runs prevent a stable
horizontal/linear scaling claim. Queue P95 does not improve monotonically.

The system's existing global priority Claim arbitration, bounded notification
deferrals and 30s QUEUED republication are plausible contributors to stalls.
This is an **inference**, not a measured causal diagnosis; CPU profiling, queue
timeline attribution and query/lock profiling were NOT RUN. CPU saturation is
not established. No scheduler changes were made to improve these numbers.

## Preliminary matrix and limits

Before the caller-cancellation protection, the same matrix also passed on
source 6915f6764190a3557251870de1b4dcb692a0f6bb: 6,000 successful measured Jobs.
Its complete [preliminary summaries](phase-9-preliminary-results.json) remain
separate; they are not blended into the final-source aggregate. Runtime change
retains diagnostic read cancellation/deadlines; final matrix was repeated in full.
The final report checkpoint adds documentation/results and a consistent WS
exclusion in the Grafana error-rate panel; application/loadgen benchmark binaries
retain the measured source implementation.

This workload sleeps rather than executing CPU work; it uses a single host/VM,
tmpfs storage, no tracing/scraper load, small samples and coarse fixed polling.
It does not predict multi-host throughput, production storage/CPU workloads,
long-term soak behaviour, exactly-once effects or a latency/recovery SLA.
Normal retained data is not repaired/deployed by these experiments. Repeat on
a recorded machine/configuration before comparing results; preserve all repeats.
