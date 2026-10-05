# Recovery

Implemented: DB-time lease recovery and stale-worker fencing live in the
PostgreSQL adapter and execution service maintenance loop. Phase 5 settles
expired Attempts through the shared retry budget and durable schedule.

This directory is a navigation placeholder, not a second recovery implementation.
See job lifecycle and ADRs 0005–0006.
