# 0005 — DB-time leases, fencing and atomic recovery

Status: Accepted, Phase 4. Supersedes the post-claim crash limitations and worker
connection budget in ADRs 0003/0004; preserves their dispatch and fixed-slot decisions.

## Context

Conditional Claim alone prevents concurrent duplicate notification execution,
but an abruptly lost owner strands RUNNING. Recovery must preserve Attempt
history, restore dispatch atomically and reject a late owner without relying on
different workers' wall clocks. No business retry/idempotency subsystem is in scope.

## Decision

PostgreSQL clock_timestamp() grants and evaluates leases. Default lease=15s,
renew=5s, heartbeat=2s, offline=10s, reaper=1s; integer-second bounds are documented
in worker operations. Lease/offline windows exceed twice their update interval.
Each fresh process UUID registers once. Heartbeat persists status/count/time;
stale/OFFLINE IDs cannot revive, and restart uses a new UUID. Fresh live registry
is required for Claim/Renew; worker-row share locks serialize with offline scans.
Liveness is not capability scheduling or the Job's execution authority.

Claim atomically sets owner, increased attempt sequence and lease, then inserts
Attempt. Renew requires RUNNING + owner + attempt + unexpired DB lease. Finalize
uses the same execution predicates and updates Job/Attempt atomically. An old
lease cannot be renewed after expiry even if recovery has not run yet. The
owner UUID and monotonic Attempt number fence DB result writes; external services
receive no fencing token and their side effects have no exactly-once guarantee.

Each process supervises one heartbeat, one reaper and one dispatcher alongside
C fixed slots. At most C renew goroutines exist and are joined before Finalize
or dependency closure, including on executor panic. Renew or heartbeat failure
drains the process. Reaper failures log safely and retry at the configured period.
PG and Redis worker connection caps are C+4; API remains ten each.

The reaper transaction locks <=100 expired RUNNING rows using FOR UPDATE SKIP
LOCKED. It closes the matching old Attempt as FAILED/error/result=lease_expired,
clears current Job execution metadata, sets QUEUED, and inserts/resets the outbox
publication marker. Owner/attempt/exact selected expiry are checked again.
Corrupt history or an intent failure rolls back the entire bounded batch. No
transaction spans SLEEP or Redis. Existing dispatcher publishes a fresh message;
old pending Redis entries are retained rather than reused as execution authority.

Crash recovery can create attempts beyond stored max_attempts; it never retries
a terminal business FAILED Job. An integer overflow guard bounds the sequence.
Phase 5 must define unified retry accounting. Ordinary domain RUNNING -> QUEUED
stays invalid; a separate expiry-guarded operation expresses recovery.

Graceful cancellation attempts FAILED/execution_cancelled before ACK only while
its lease remains valid. Expiry wins races. Heartbeat/renew/reaper stop and join;
registry records graceful_shutdown or fatal_error when reachable. Abrupt loss
becomes heartbeat_expired. Shared finalization cancellation is five seconds,
rollback up to five more, and registry stop up to three; Compose grants fifteen.

## Consequences

Progress requires reachable PostgreSQL and a healthy reaper/dispatcher. No lease
SLA follows from timer values; pauses, clock adjustment, locks or network latency
can expire healthy work. Ambiguous commit responses are never guessed as success:
database truth may be RUNNING, terminal or recovered. External side effects may
repeat or overlap; only the trusted context-aware SLEEP executor is implemented.
Stale DB writes are rejected, not arbitrary external side effects.

Registry, attempts, streams/consumers and outbox retain history without a cleanup
policy. A corrupt oldest recovery batch requires operator repair. Migration
000004 appends minimal registry/indexes and expires pre-lease RUNNING records;
consistent owner/Attempt history is still required. Mixed old/new worker binaries
and live schema downgrade are unsupported; coordinate upgrades with workers stopped.

## Alternatives

Redis pending-entry reclaim alone cannot prove DB execution ownership or restore
the DB/outbox atomically. Heartbeat alone cannot fence an individual attempt.
Worker wall-clock expiry risks skew. Deleting old Attempts loses crash evidence.
A separate global reaper service or full retry engine adds unnecessary components
for this phase; concurrent bounded reapers reuse existing worker processes.
