# Dashboard contract (Phase 8)

PostgreSQL is authoritative. This is a local/demo presentation and REST control
plane. No authentication, persistent application-log store,
UI replay/reliable delivery or exactly-once execution is provided.

Phase 9 adds a link to the real provisioned Grafana overview at localhost:3000
when the optional observability profile is running. API `/metrics`, internal
Worker metrics and OTLP are independent diagnostics; Dashboard state remains
REST/PG. See [metric definitions and setup](observability.md). No scaling or
latency chart is fabricated in this React UI.

## Read API

`GET /api/v1/dashboard/summary` accepts no query and returns jobs counts for all
eight persisted statuses, workers counts ONLINE/IDLE/BUSY/DRAINING/OFFLINE, schedules
ACTIVE/CANCELLED counts, queue_depth and active_jobs. Missing states are explicit
zero; counts use one PostgreSQL statement snapshot.

Queue depth = QUEUED + remaining Attempt budget + null/due scheduled_at, at DB
statement_timestamp(). Future Jobs/RETRYING are excluded; jobs without a capability
match are included. active_jobs sums non-OFFLINE registry counts, which are
heartbeat-derived and may differ from instantaneous RUNNING counts. FAILED and
TIMED_OUT are normally intermediate Job outcomes and can be zero; true Attempt
history remains available on Job detail.

`GET /api/v1/workers?limit=20&cursor=<UUID>` returns workers and nullable
next_cursor. Sort is immutable worker_id DESC; cursor is a canonical nonzero
UUID exclusive boundary. Heartbeat updates cannot move rows across boundaries.
Reserved hostname is empty; no secrets/connection details are exposed.

`GET /api/v1/schedules?limit=20&cursor=...` returns schedules and nullable
next_cursor with the existing versioned creation timestamp/UUID cursor, DESC.
Both lists default to 20, require limit 1..100, and fetch <=101 for lookahead.
Unknown/duplicate/blank/invalid queries return 400, empty arrays are []. Pages
are live, not a frozen multi-request snapshot. DB deadline is 5s; failure returns
generic INTERNAL_ERROR. These REST reads remain usable during Redis outages.

Jobs/DLQ/details/Attempts reuse existing APIs. Cancel Job, redrive and cancel
Schedule remain REST POST with server validation and the stable error envelope.

## WebSocket contract and bounds

`GET /api/v1/ws` upgrades to WS. Hints are <=1 KiB:

```json
{"version":1,"event":"job.changed","resource_id":"11111111-1111-4111-8111-111111111111","occurred_at":"2026-10-05T08:00:00Z","hint":{"status":"RUNNING"}}
```

job.changed, worker.changed and schedule.changed invalidate visible resources.
system.changed uses zero UUID and LIVE/DEGRADED subscriber state. New connections
get an initial system hint; subscription recovery emits LIVE and requests resync
for existing clients too. No full payload/result/key/driver error enters hints.

After durable commit, a nonblocking observer enqueues a hint in a separate
128-slot queue and separate two-connection Redis pool/process. One publisher has
250ms deadlines. Saturation/failure drops the
hint with a sanitized warning; business results/dispatch/ACK stay independent.
Queued hints can be lost on shutdown. Each API subscribes independently to
`<FLOWFORGE_REDIS_STREAM>:ui:v1`; Job Streams/groups are not reused. Pub/Sub is
transient, at-most-once-ish: no replay, ack or retention. Duplicate/out-of-order
hints are invalidations; occurred_at is publication time, not a commit sequence.

Default limits: 100 connections/API including pending handshakes, 16 outbound
messages/client, 1 KiB read limit, 5s handshake, 2s writes, 15s ping and 45s pong
deadline. Buffer full closes that peer; broadcast never waits for socket writes.
All WS application commands are rejected; commands use REST. Malformed/oversized
frames and idle peers close. Shutdown closes/join readers/writers and cancels,
closes and joins the Redis subscription loop. Publication queues are bounded;
existing business-data retention remains a separate limitation.

Origin must be same-host http(s), exactly allowlisted, or absent for non-browser
clients. Defaults: http://localhost:5173 and http://127.0.0.1:5173. Configure
FLOWFORGE_WS_ORIGINS with <=8 comma-separated exact origins, empty for same-origin
only. Wildcards/userinfo/path/query are rejected. No broad REST CORS is added.
Vite proxies /api including WS, preserving browser same-origin development.

## Browser consistency and controls

Navigation fetches REST. Socket open/reconnect refreshes all current visible
resources. Hints coalesce into one refresh per 250ms window. There is one socket,
retry timer and coalescing timer; retry doubles 1s..15s, reset on open. Every 30s,
visible resources reconcile even if a hint was missed. Old requests are aborted
and their results discarded on revision/navigation changes. Page 2 stays page 2
on invalidation; use Previous for newer submissions. No FSM reconstruction/replay.

Connection, loading/resync, empty and error states are explicit. Failed refresh
preserves the last snapshot with a stale warning. Job and Attempts are fetched
separately and are not a transactional pair; subsequent refresh converges.
Controls require confirmation, display real 200/400/409/500 results and refresh
after completion/errors. No optimistic authoritative mutation. Concurrent
controllers can still cause a real conflict that the UI must show.

Times are local with complete timestamp tooltips. Collapsed React text/pre JSON
is escaped and render-limited to 16 KiB per field. No dangerous HTML, secrets in
localStorage, fabricated logs, throughput or latency charts.

## Acceptance and local setup

See [frontend setup](../web/README.md). Use the current schema-8 API for fresh
v1 acceptance; Phase 8 itself added no migration. Retained development schema 4 is not acceptance data and is not repaired or
upgraded. On a fresh DB only, Compose's dashboard profile starts the local stack.
`./scripts/phase8-smoke.ps1` allocates a random DB/stream/channel and loopback ports,
two APIs/two Workers/Vite, then removes only generated resources and audits retained
data. It verifies cross-process QUEUED/RUNNING/SUCCEEDED, killed owner ->
lease_expired -> Attempt 2 success, OFFLINE hint, completion while disconnected
-> reconnect REST resync, DLQ history/redrive, schedules and Worker fields.

Browser E2E NOT RUN; real WS clients plus frontend component/hook tests,
typecheck/build and HTTP/WS process smoke are the evidence. This is not a benchmark
or cloud/browser production evidence. The Phase 10 static Caddy gateway is tested
separately; optional external exposure still requires auth/TLS/origin review.

## Phase 10 production serving

Independent gateway image serves production static dist with SPA fallback and
same-origin REST/WS. Chosen private mode uses browser 127.0.0.1:8180 through SSH;
optional public mode authenticates static/API/WS at the HTTPS gateway. Application
users/session/RBAC remain unimplemented. Match exact WS origin and tunnel port;
Grafana link requires the 3000 tunnel. Local production image/TLS/auth/WS tests
are separate from cloud/browser E2E evidence. See [deployment](deployment.md).
