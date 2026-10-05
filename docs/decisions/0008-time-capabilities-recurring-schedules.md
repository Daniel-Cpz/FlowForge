# 0008 — DB-time eligibility, immutable capabilities and recurring occurrences

Status: Accepted

## Context

Time and capability restrictions must compose with priority, leases, retries,
dead letters and durable dispatch. Redis consumer assignment alone cannot decide
eligibility. Multiple worker processes need to generate recurring work safely.

## Decision

Keep delayed Jobs QUEUED. PostgreSQL PendingDispatch and Claim require
scheduled_at to be null or due at clock_timestamp(). Worker liveness and the
registry's capability superset are Claim fences. Priority compares only Jobs due
and compatible with that worker. A higher GPU requirement cannot block a CPU
worker's eligible backlog. A rejected notification is ACKed without an Attempt;
the durable intent is republished at the existing 30-second reconciliation cadence.

Capabilities are at most 16 input items, each trimmed and ASCII lowercased,
1–32 characters matching [a-z0-9][a-z0-9._-]*. Controls and non-ASCII token content
are rejected, then duplicates removed and values sorted. Empty is unrestricted.
The same parser handles comma-separated Worker configuration. PostgreSQL checks
canonical arrays and a trigger prohibits changing a registered identity's set.
Heartbeat changes liveness only; restart obtains a fresh UUID.

Normalize request timestamps to UTC at microsecond precision (fractional
nanoseconds truncated). Include scheduled_at and the canonical requirement set
in submission identity; offsets representing the same instant replay. Missing
and null mean immediate, while a supplied past timestamp remains part of identity.

Store fixed-interval templates in job_schedules. Intervals are integer seconds
1..604800; next_run_at is optional at creation and defaults to PostgreSQL now.
Schedule creation has no submission idempotency key. Each worker's existing
maintenance loop selects at most 100 due ACTIVE schedules FOR UPDATE SKIP LOCKED.
One bounded transaction inserts the ordinary QUEUED Job and dispatch intent,
then advances next_run_at. A foreign key, paired schedule_id/scheduled_for and
unique occurrence constraint provide durable identity. There is no second executor.

Missed runs are coalesced: materialize the oldest due occurrence once per
schedule per pass; preserve its scheduled_for and advance along the original
interval grid to the first boundary strictly after DB time at advancement.
Intermediate missed intervals are skipped. No catch-up storm or execution
exactly-once claim is made. A crash before commit rolls back all three writes;
after commit, a new process sees the advanced cursor and durable occurrence.
Redis failures preserve the intent for later delivery.

Schedule cancellation locks the same schedule row and conditionally changes
ACTIVE to CANCELLED. Repeated cancel succeeds without rewriting updated_at.
If materialization wins the lock first, that occurrence exists; if cancel wins,
it is excluded. Existing Jobs continue their own lifecycle. Cancelling an
occurrence Job does not cancel its parent. Retry and redrive retain time,
priority, capability and occurrence identity, and do not generate another Job.

## Consequences

Without a capable live worker a Job remains QUEUED backlog, consuming no budget.
Global stream mismatch can delay matching by 30 seconds or more under repeated
consumer mismatch; there is no routing/fairness/latency SLA. PG availability and
a healthy worker maintenance loop are needed for timely materialization.
Bounded scheduling transactions favor correctness over maximum throughput;
priority still permits starvation among eligible Jobs. Waiting is excluded from
the per-attempt deadline. External effects still require business idempotency.

Migration 000007 is append-only; 000001–000006 are retained unchanged. Stop
processes before migration/downgrade. Down retains Jobs and Attempts but removes
schedule templates, occurrence attribution, eligibility and capability metadata;
it is not operational recovery or a safe mixed-binary rollout. The retained
development schema remains Phase 4 until separately authorized legacy duplicate
key resolution permits 000005 and subsequent migrations.

## Alternatives

Capability-specific streams or a routing service would add topology and recovery
contracts. An application-only capability/time check would lack a DB fence.
Unbounded interval catch-up would create a restart storm. Cron, schedule editing,
pause/resume, dashboards and new infrastructure are outside this phase.
