# FlowForge Phase 2 — Redis Queue + Single Worker Execution

## Current Background

Phase 1 is complete and certified on branch `codex/phase1-api-correctness`.

Phase 1 delivered:
- strict Job HTTP creation semantics;
- PostgreSQL-backed canonical persistence;
- deterministic cursor pagination;
- sanitized failure mapping;
- real PostgreSQL integration coverage;
- machine-readable phase automation with manual / automation prompt-source tracking.

Current known boundaries:
- Redis is connected only for readiness/infrastructure;
- no Redis-backed job queue exists;
- worker process starts but performs no job execution;
- idempotency keys do not suppress duplicates;
- priority and timeout are metadata only;
- no heartbeat, lease renewal, crash recovery, retry/backoff, DLQ, scheduling, dashboard, tracing, benchmark or cloud deployment exists;
- the project does not claim exactly-once execution.

Phase 2 must implement only the next roadmap step:

**Redis Queue + Single Worker Execution**, including a deliberate solution for the PostgreSQL/Redis dual-write failure.

Correctness > Feature Count.
Reliability > UI Complexity.

Do not implement future phases early.

---

## Phase 2 Goal

Create the first end-to-end asynchronous execution path:

`POST job -> durable PostgreSQL job + durable dispatch intent -> Redis queue -> one worker -> SLEEP execution -> persisted terminal result`

The main engineering objective is not raw throughput. It is establishing a failure-aware handoff from durable PostgreSQL state to Redis without silently losing persisted jobs when either system fails.

Use at-least-once delivery semantics. Do not claim exactly-once execution.

---

## Required Design Decision: PostgreSQL / Redis Dual-Write

Do **not** implement the unsafe sequence:

1. insert job into PostgreSQL;
2. immediately push to Redis;
3. assume both succeeded.

A PostgreSQL commit followed by Redis failure must not permanently strand a valid queued job.

Implement a durable database-backed dispatch/outbox mechanism, or an equivalently strong design that provides the same failure guarantees.

Preferred direction for this phase:

- create the Job row and a durable dispatch/outbox record in one PostgreSQL transaction;
- a dispatcher publishes pending dispatch records to Redis;
- after successful publish, persist publication state;
- if the dispatcher crashes before publication, the durable outbox remains and can be retried;
- if Redis publish succeeds but marking the outbox published fails, duplicate Redis delivery is allowed and must be safely handled;
- never rely on an in-memory-only pending queue.

If you choose another design, document why it provides equivalent durability and why it remains appropriate for later multi-worker/recovery phases.

Record the final decision in an ADR.

Do not add Kafka, RabbitMQ, Temporal, a workflow engine, ORM, or another infrastructure system merely to solve this phase.

---

## Redis Queue Primitive

Prefer a Redis primitive that supports durable-enough consumer semantics for the upcoming worker phases.

Redis Streams + consumer group is a reasonable default because future phases will need multiple workers and recovery, but do not overbuild full stale-message reclamation, heartbeat or lease recovery in Phase 2.

If you choose Redis Lists or another primitive, explicitly document:
- message-loss behavior;
- duplicate behavior;
- worker crash behavior;
- how Phase 3 / Phase 4 can extend the design safely.

The queue message should contain only the minimum identity needed to reconstruct authoritative work from PostgreSQL, preferably the Job ID and protocol/version metadata.

PostgreSQL remains the source of truth for Job state and payload.

Do not serialize the full authoritative Job object into Redis and then trust it over the database.

---

## Scope

### 1. Durable Dispatch / Outbox

Add the minimum schema and repository/service support required for a durable dispatch intent.

Requirements:
- inserting a new Job and its dispatch intent must be atomic;
- existing Phase 1 validation semantics must remain intact;
- migration files already applied must not be edited;
- add a new forward migration and matching rollback migration;
- migration must be safe and deterministic;
- dispatch records must be identifiable by Job ID;
- duplicate publishing must be possible without corrupting Job state;
- do not enforce exactly-once publication.

A suitable outbox record may include:
- job_id;
- created_at;
- published_at or equivalent state;
- optional attempt/publication metadata only if required by the implementation.

Do not add a large generic event-bus abstraction.

---

### 2. Dispatcher

Add a small dispatcher component that:
- reads pending durable dispatch records;
- publishes them to the Redis queue;
- marks them published only after Redis confirms publication;
- uses bounded database and Redis operations;
- responds to context cancellation;
- does not busy-spin when idle;
- logs structured events without leaking credentials or raw sensitive payloads.

Failure requirements:
- PostgreSQL unavailable: no false success;
- Redis unavailable: pending outbox remains durable;
- Redis success + DB publication-marker failure: duplicate future publish is acceptable;
- process crash before publish: outbox remains pending;
- process crash after publish before marker update: duplicate publish is acceptable.

Do not silently delete a pending dispatch on failure.

---

### 3. Single Worker

Turn the current worker skeleton into one real worker.

The worker must:
- receive one queued message at a time;
- extract the Job ID;
- reload the authoritative Job from PostgreSQL;
- transition the Job through the existing domain lifecycle using server-owned state;
- execute only the supported Phase 2 demonstration Job type;
- persist success/failure result safely;
- acknowledge/remove the Redis message only at the appropriate point;
- handle duplicate queue deliveries idempotently at the Job-state boundary;
- shut down cleanly on context cancellation / SIGINT / SIGTERM.

Do not implement multi-worker concurrency yet.

A duplicate queue message for a Job that is already RUNNING or terminal must not cause a second SLEEP execution.

Use an atomic database state transition / compare-and-set where necessary rather than:
1. SELECT state;
2. trust it;
3. UPDATE unconditionally.

Single-worker today must not bake in a race-prone transition that Phase 3 immediately has to replace.

---

### 4. SLEEP Demonstration Job

Implement exactly one deliberately simple executable Job type, e.g. `SLEEP`.

Define and document an exact payload contract.

Recommended contract:

```json
{
  "duration_ms": 250
}
```

Requirements:
- bounded integer duration;
- reject malformed or unsupported payload at execution time in a deterministic way;
- use context-aware sleeping, not an uninterruptible raw sleep;
- successful execution writes an explicit result;
- execution timestamps/status follow existing domain semantics;
- unsupported Job types must have a clearly documented behavior.

Keep the supported duration small enough that tests are fast and deterministic.

Do not turn this into a generic shell-command runner, code executor, HTTP callback runner or plugin system.

---

### 5. Job State Transitions

Reuse the centralized domain state graph.

Phase 2 must establish the minimum authoritative execution transitions, typically:

`QUEUED -> RUNNING -> SUCCEEDED / FAILED`

Do not bypass the domain model with arbitrary SQL state writes except through repository methods that enforce the expected transition.

At worker claim time, the database must determine whether the Job is still claimable.

Duplicate delivery behavior:
- terminal Job: do not execute again;
- already RUNNING Job: do not execute again;
- missing Job: handle safely and visibly;
- malformed queue message: do not crash the worker.

Document the exact acknowledge/drop/retry behavior for each case.

---

## Explicit Crash Boundary

Phase 2 does **not** implement full worker crash recovery.

If a worker claims a Job and then dies while it is RUNNING, that Job may remain RUNNING until a later recovery phase.

This limitation must be explicit in:
- README;
- architecture/lifecycle docs;
- Phase 2 completion report.

Do not quietly introduce heartbeat, lease expiry, stale-owner reclaim, fencing or distributed recovery here; those belong to Phase 4.

However, Redis queue semantics must not make later recovery impossible.

---

## Attempts

The repository already has JobAttempt-related baseline structures.

Use them only if they are necessary to represent one execution attempt correctly.

Do not implement retry policy in Phase 2.

If Phase 2 creates an attempt record:
- exactly one normal worker execution should create one attempt;
- duplicate delivery that is rejected before execution must not create a duplicate business attempt;
- retry/backoff semantics remain Phase 5.

Do not invent attempt numbers or retry loops without need.

---

## Out of Scope

Do not implement:
- multiple workers or bounded parallel worker pools;
- worker heartbeat;
- lease renewal;
- stale worker reclaim;
- fencing tokens;
- full crash recovery;
- retries;
- exponential backoff;
- jitter;
- duplicate-suppression by idempotency_key;
- priority scheduling;
- execution timeout policy beyond the SLEEP operation's context-aware cancellation needed for clean shutdown;
- user cancellation API;
- DLQ;
- scheduled jobs;
- capability-aware scheduling;
- dashboard;
- WebSocket;
- Prometheus/Grafana;
- distributed tracing;
- benchmark claims;
- cloud deployment;
- authentication;
- arbitrary code or shell execution.

These belong to later phases.

---

## Failure / Edge Cases

At minimum test and document:

1. Job insert fails -> no outbox record is committed.
2. Outbox insert fails -> Job insert is rolled back.
3. Job + outbox commit succeeds while Redis is unavailable -> Job remains durable and eventually publishable.
4. Dispatcher restarts with pending outbox -> pending item can publish.
5. Redis publish succeeds but DB publication marker fails -> later duplicate publish is safe.
6. Duplicate Redis message -> Job executes at most once through the database claim boundary.
7. Queue message references missing Job -> worker remains healthy and behavior is explicit.
8. Malformed queue message -> worker remains healthy.
9. Unsupported Job type -> deterministic persisted failure or documented non-execution behavior.
10. Invalid SLEEP payload -> deterministic failure; worker does not crash.
11. Graceful shutdown while idle.
12. Graceful shutdown during SLEEP.
13. PostgreSQL failure during claim/finalization -> no fabricated success.
14. Redis temporary failure -> bounded error handling; no tight loop.
15. terminal Job receives duplicate queue delivery -> no second execution.
16. RUNNING Job receives duplicate queue delivery -> no second execution.
17. migration up/down behavior in an isolated test database.
18. no secret/password/raw driver detail appears in public API or normal logs.

Do not simulate reliability only with mocks if a real Redis/PostgreSQL integration test is practical in the existing Docker test environment.

---

## Minimum Necessary Test Scope

Run focused tests first.

Required minimum:
- unit tests for new outbox/dispatcher logic;
- unit tests for worker execution and SLEEP payload validation;
- state-transition / duplicate-delivery tests;
- PostgreSQL integration tests for atomic Job + outbox creation;
- Redis integration tests for publish/consume behavior;
- combined PostgreSQL + Redis end-to-end integration for:
  `POST -> outbox -> Redis -> worker -> SUCCEEDED`;
- Redis-unavailable dual-write test proving the persisted Job/outbox is not lost;
- duplicate-publish / duplicate-delivery test;
- graceful cancellation test;
- migration up/down validation.

After focused tests pass, run the project's existing relevant acceptance checks:
- `go test ./...`
- `go vet ./...`
- `go build ./...`
- formatting checks;
- phase-state validator;
- `docker compose config --quiet`;
- a controlled Docker Compose smoke test if the environment permits.

Do not make performance/throughput claims without a benchmark.

Tests must not mutate real user/application data. Continue using isolated schemas / disposable Redis keys or test namespaces.

---

## README / Documentation Updates

Update only documentation affected by real Phase 2 behavior.

Required:
- `README.md`
  - Redis queue now implemented if and only if validated;
  - worker execution status;
  - SLEEP payload example;
  - exact current delivery semantics;
  - explicit current crash-recovery limitation;
  - no exactly-once claim.
- `docs/architecture.md`
  - PostgreSQL source of truth;
  - durable outbox/dispatch path;
  - Redis queue role;
  - worker claim/finalization boundary.
- `docs/job-lifecycle.md`
  - actual Phase 2 state path and duplicate delivery handling.
- `docs/development-roadmap.md`
  - mark only Phase 2 capabilities actually delivered;
  - keep Phase 3+ Planned.
- add an ADR for the DB/Redis handoff and Redis queue primitive.
- update any migration/operations documentation required to run the worker.

Implemented / Experimental / Planned must remain clearly separated.

---

## Phase Automation Protocol

This Phase 2 Prompt was generated by external Automation.

When Codex officially claims Phase 2, update state to:

```json
{
  "current_phase": 2,
  "status": "in_progress",
  "prompt_source": "automation",
  "prompt_path": "automation/prompts/phase-2.md",
  "report": null,
  "next_prompt": null,
  "last_processed_phase": 1
}
```

Preserve the real branch and timestamp fields required by the repository schema.

Do not modify `last_processed_phase` to 2. That field remains owned by the external Automation after Phase 2 completion review.

Do not generate Phase 3 yourself.

If the user explicitly changes Phase 2 scope while it is in progress, record that change in the final report.

---

## Phase 2 Completion Requirements

Phase 2 may be marked COMPLETED only when all of the following are true:

- durable Job + dispatch intent creation is atomic;
- Redis handoff cannot silently lose a committed Job due to simple DB/Redis dual-write failure;
- one worker actually consumes queue messages;
- SLEEP Job executes end-to-end;
- Job status/result persist correctly;
- duplicate delivery does not execute the same Job twice through the claim boundary;
- required focused and integration tests pass;
- no Phase 3+ feature is falsely claimed;
- README and relevant docs match real behavior;
- migration evidence is valid;
- Git evidence is real;
- completion report exists and passes the phase-state validator.

Create:

`docs/reports/phase-2-report.md`

using the repository report contract.

The report must include:
- Phase / Status;
- Prompt Source = automation;
- Prompt Path = automation/prompts/phase-2.md;
- Summary;
- Implemented;
- Not Implemented;
- Experimental;
- Planned;
- exact test commands and results;
- DB/Redis dual-write failure evidence;
- duplicate-delivery evidence;
- worker crash limitation;
- Known Limitations;
- Git branch / commit / annotated tag if the established workflow uses one;
- documentation updated;
- Next Recommended Phase = Phase 3 only as a recommendation.

Do not set `next_prompt` yourself.

---

## Git / GitHub Safety

Before commit:
- inspect `git status`;
- inspect the intended diff;
- scan for `.env`, credentials, tokens, private keys, Redis/PostgreSQL passwords, generated data and large artifacts;
- do not commit local runtime data;
- keep migration history append-only.

After required tests pass:
- create clear commits on the actual Phase 2 work branch;
- preserve existing history;
- no `git reset --hard` for cleanup;
- no rebase solely to beautify history;
- no force push;
- no force-with-lease;
- do not merge to `main` unless the established project workflow explicitly authorizes it;
- if using an annotated phase tag, point it to the real validated implementation/report checkpoint.

If GitHub push fails, report the real failure and do not claim remote completion.

---

## Final Codex Response

On successful completion, output a concise but complete Phase 2 completion log containing:
- Phase 2 status;
- implemented execution path;
- dual-write solution;
- Redis primitive;
- worker behavior;
- SLEEP contract;
- failure cases verified;
- exact tests and result;
- known crash/recovery limitation;
- report path;
- branch;
- commit;
- tag;
- push result;
- resulting automation state;
- confirmation that Phase 3 was **not** started or generated by Codex.

If blocked, output BLOCKED with the precise step, reason, tests completed, Git state and recommended action.