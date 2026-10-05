# Retry

Implemented in Phase 5: pure bounded exponential cap and equal jitter policy.
Repository persists DB-time schedules and enforces the total Attempt budget;
worker maintenance promotes due retries. See ADR 0006 and job lifecycle.
