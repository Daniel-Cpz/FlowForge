# Job lifecycle

Status: Phases 6–7 control, delayed/capability eligibility and recurring scheduling implemented.

| From | Allowed targets |
|---|---|
| QUEUED | RUNNING, CANCELLED |
| RUNNING | SUCCEEDED, FAILED, TIMED_OUT, CANCELLED |
| FAILED | RETRYING, DEAD_LETTER |
| RETRYING | QUEUED, CANCELLED |
| TIMED_OUT | RETRYING, DEAD_LETTER |
| SUCCEEDED, DEAD_LETTER, CANCELLED | none |

Unknown/self/absent edges fail without changing the Job. The graph is centralized
in domain; durable authority requires repository transactions and SQL fences.
HTTP exposes explicit Cancel and Redrive commands, not arbitrary state mutation.

```mermaid
stateDiagram-v2
    [*] --> QUEUED
    QUEUED --> RUNNING: atomic Claim + Attempt
    RUNNING --> SUCCEEDED: fenced success + ACK after commit
    RUNNING --> FAILED: fenced failure or expired lease
    FAILED --> RETRYING: retryable and budget remains
    FAILED --> DEAD_LETTER: permanent or budget exhausted
    RETRYING --> QUEUED: due DB time + dispatch intent
```

FAILED is an intermediate logical transition for Phase 5 failures; one transaction
persists RETRYING or DEAD_LETTER, so observers need not see transient FAILED.
Historical FAILED Jobs remain unchanged and are not automatically retried.

## Job and Attempt

Creation starts QUEUED/count 0. Claim alone increments count and inserts a unique
(job_id,attempt_number) Attempt. max_attempts is the total execution budget (1..100),
including business failures, expired leases and operational interruptions.
Duplicate delivery, failed claim, replay and retry promotion consume no budget.
No N+1 Attempt is created after exhaustion. Sequence and old Attempts are retained.

Attempt states record execution, not scheduling: RUNNING, SUCCEEDED, FAILED and
TIMED_OUT/CANCELLED. A failed Attempt records stable error/result/end time;
the corresponding Job may be RETRYING or DEAD_LETTER. Success Job/Attempt share
result and completion time. Historical attempts need not match current Job status.

retry_at is a nullable TIMESTAMPTZ, UTC RFC3339 on HTTP. Only RETRYING has it;
RETRYING has no assigned_worker/lease and must have remaining budget. Success and
DEAD_LETTER clear retry_at/lease. DEAD_LETTER also clears current assigned_worker;
its Attempt retains worker identity. QUEUED/RUNNING start with retry_at null.

## Failure, scheduling and ACK

Only SLEEP executes in production: exactly one duration_ms integer in 0..10000.
Unsupported type/invalid SLEEP payload are permanent failures after one Attempt,
ending DEAD_LETTER. execution_cancelled is a retryable process interruption,
not user cancellation. lease_expired uses the identical retry budget/policy.
Panic or ambiguous renewal leaves RUNNING for expiry recovery without guessing.

Equal jitter uses a bounded exponential cap, default base 1s/max 30s, actual delay
in [cap/2,cap]. PostgreSQL time establishes schedule and due eligibility. Delay is
never zero. RETRYING is durable across worker/process restart and Redis loss.
Bounded concurrent promotion commits RETRYING -> QUEUED plus intent reset together;
dispatch later publishes. No transaction crosses SLEEP, Redis or a waiting timer.

Finalize requires RUNNING + matching owner/attempt + currently valid DB lease.
Recovery requires an expired DB lease and locks exact old execution. Both close
Attempt and settle Job atomically, using RUNNING -> FAILED -> target. Stale owner
cannot renew or overwrite recovery. Missing/corrupt history rolls back the batch.

ACK follows success, DEAD_LETTER or RETRYING commit. Failed settlement leaves
RUNNING/pending and stops the process; failed ACK leaves the committed outcome
intact. Malformed/missing/non-QUEUED deliveries ACK only themselves. Ambiguous
commit is resolved from DB truth. Old pending messages are retained; no reclamation
or retention management is added. Healthy worker/DB/Redis are required for progress.

## Submission idempotency

The exact non-null global key identifies canonical type, JSONB payload, priority,
original submission max_attempts, timeout, normalized scheduled_at and canonical capabilities. First submission returns 201; same request replay returns
200 with original Job/Location at any state; different request returns 409
IDEMPOTENCY_CONFLICT and changes nothing. Null/absent keys always create distinct
Jobs. Replay creates no Attempt/intent and does not restart terminal work.

## Constraints and migrations

Database constrains status membership, numeric bounds, attempt_count<=max_attempts,
retry/status consistency and key uniqueness. It does not duplicate the transition
graph in triggers. Domain and scan validation reject corrupt stored data.

000001 jobs, 000002 Attempts, 000003 dispatch and 000004 worker leases remain
immutable. 000005 appends retries/idempotency. Duplicate-key preflight fails
atomically without deleting/merging Jobs. Legacy count>100 requires explicit review;
over-budget <=100 counts freeze max_attempts at the actual count and exhausted
QUEUED Jobs normalize administratively to DEAD_LETTER, preserving history.
Legacy RETRYING gets a DB-time one-second schedule. Down removes new objects but
never erases history or restarts normalized work. Stop workers before schema changes.

## Priority and attempt deadline

Claim snapshots rank eligible QUEUED by priority DESC / created_at ASC / id ASC;
RUNNING is never preempted. Lower-ranked deferral changes no Job/Attempt/budget.
Bounded peer-claim waits precede ACK; intent reconciliation is at 30 seconds.
Aging/fairness and strict global FIFO are unimplemented; high backlog may starve.
Retry promotion and manual redrive rejoin ordinary priority eligibility.

Worker deadline starts at executor invocation, excludes queuing/backoff, and
stops renewal at timeout. Timeout is retryable: RUNNING -> TIMED_OUT -> RETRYING
when budget remains, otherwise -> DEAD_LETTER. TIMED_OUT is an intermediate Job
graph outcome, preserved as the completed Attempt status/error execution_timeout.
No externally observable persisted TIMED_OUT Job is required. Historical such
rows are retained, without automatic retry. Finalize retains all owner/attempt/
unexpired-lease fences; DB failure leaves RUNNING for expiry recovery and no ACK.

## Cancellation and manual redrive

QUEUED/RETRYING cancellation atomically persists CANCELLED, clears retry_at and
invalidates intent. RUNNING cancellation persists cancel_requested_at while Job
remains RUNNING; the executor stops cooperatively at the renewal check. Finalize
and Cancel lock the same row: a committed request overrides late local success
or timeout with Job/Attempt CANCELLED + user_cancelled. If successful Finalize wins
first, Cancel conflicts. Crash-after-request recovery also CANCELLED with no retry.
SKIP LOCKED may settle at the next scan. No cancellation path creates extra Attempt.
Operational interruption execution_cancelled still follows the retry policy.

Repeated CANCELLED cancel succeeds; other terminal states conflict. Manual redrive
accepts only DEAD_LETTER below max budget 100, atomically increases max_attempts by
one and restores QUEUED/intent. It is a controlled management exception, never an
ordinary DEAD_LETTER -> QUEUED graph edge. Old Attempts/count remain, terminal
metadata clears; Claim creates the next Attempt. Original submission budget is
immutable for replay even after redrive. DLQ uses bounded creation-time pagination;
Attempts returns full ordered history. No per-job persistent logs or DLQ UI.

Append-only 000006 provides cancellation metadata, original budget and partial
indexes, without changing 000001–000005 or resolving retained legacy keys. Its
down loses cancellation/original-budget metadata; stop processes before schema
changes and do not use rollback to resume work. Old/new binaries cannot mix.
## Phase 7 eligibility and recurring occurrences

Future scheduled_at retains QUEUED, with no dispatch/Claim/Attempt/budget change
before PostgreSQL due time. Waiting is excluded from timeout. Claim also requires
all capabilities in the live worker's immutable set; priority compares only its
eligible Jobs. Missing requirements means empty set. No capable worker is QUEUED
backlog. Rejected notifications ACK without execution; intent reconciles at 30s.

Templates are ACTIVE/CANCELLED, fixed interval 1..604800 seconds. Maintenance
materializes one oldest due ordinary Job per template per pass, atomically with
intent and first future interval boundary advancement. Missed middle runs are
skipped. Unique occurrence identity survives process restart. Schedule cancel
prevents future materialization only; existing Job states/history continue.
Job cancel leaves its parent ACTIVE. RetryAt is distinct from scheduled_at;
retry/redrive preserves original capabilities, priority and occurrence identity.
See [scheduling contract](scheduling.md) for normalization, API and migration rules.
