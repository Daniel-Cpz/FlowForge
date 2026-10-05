# 0007 — PostgreSQL priority, attempt deadlines and explicit user control

Status: Accepted

## Context

Phase 5 provided at-least-once notifications, DB leases and budgeted retries.
Redis backlog order cannot establish priority, context cancellation alone cannot
record durable user intent, and unrestricted terminal transitions erase history.
Manual redrive changes execution budget but must not change submission identity.

## Decision

- Rank pending intents/eligible QUEUED Claims by priority DESC, created_at ASC,
  id ASC. Serialize only short Claim transactions with a DB transaction advisory
  lock, then recheck ranked eligibility in conditional UPDATE. Execution remains
  concurrent and non-preemptive. Three 250ms waits permit peer claims; deferred
  notification ACK retains QUEUED intent for bounded 30s reconciliation.
- Start Job.timeout deadline immediately before executor invocation. SLEEP honors
  context. TIMED_OUT is a completed Attempt outcome and intermediate Job graph
  step, retryable toward RETRYING/DEAD_LETTER using the existing total budget.
  Stop/join renewer at deadline; retain owner/attempt/unexpired-lease Finalize fence.
- Cancel QUEUED/RETRYING atomically to CANCELLED and remove intent. RUNNING records
  cancel_requested_at; renewal observes it and cooperatively stops execution.
  Cancel/Finalize row-lock arbitration honors committed intent before late success
  or timeout. Expired recovery settles requested cancellation as CANCELLED, without
  retry/new Attempt. SKIP LOCKED may defer to a subsequent maintenance scan.
- Keep DEAD_LETTER terminal for ordinary graph transitions. Explicit redrive locks
  only DEAD_LETTER below budget cap, grants +1 max_attempts, preserves count/history,
  clears terminal metadata and resets intent atomically. Concurrent second redrive
  conflicts. Preserve canonical submission_max_attempts separately from mutable
  execution budget; keyed replay after redrive still identifies the original Job.
- Reuse creation-time cursor order for filtered DLQ list; return full ordered
  Attempt history bounded by cap 100. Sanitize legacy error text. No persistent
  per-job logs, UI or generic administrative subsystem.
- Append 000006; leave historical migrations immutable and legacy-key resolution
  to an explicitly authorized operator task. Isolated acceptance is not rollout.

## Consequences

Priority can starve low-ranked work; no aging or global FIFO SLA. A global short
Claim lock trades scheduling throughput for deterministic arbitration; no benchmark
is claimed. Notification deferral can add about 30s plus scheduling/dependency delay.
User cancellation is cooperative and observes renewal cadence, not immediate API
termination. Side effects already performed cannot be undone. Uncooperative
executors remain unsupported; SLEEP is the only production executor.

TIMED_OUT Terminal() changes from true to false; Attempt state still retains the
timeout outcome. Historical TIMED_OUT Job rows are retained without automatic
rescheduling. Manual redrive changes max_attempts visible on GET, while replay
compares original canonical submission budget. At most 100 total Attempts persist.
000006 down loses request/original-budget metadata and requires stopped processes;
it is not a recovery path. No exactly-once external-effect guarantee or new auth.

## Alternatives

Publish-order-only priority fails with existing Redis backlog. Unbounded requeue
loops consume resources without durable correctness. Directly cancelling RUNNING
in the API falsely claims executor termination. Ordinary DEAD_LETTER -> QUEUED and
resetting counters/history hide execution and violate budget semantics. Comparing
redrive-adjusted budget on keyed replay changes Phase 5 submission identity.
Completion-time DLQ cursors require a second format without improving this scope.
