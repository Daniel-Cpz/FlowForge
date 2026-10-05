# Scheduler

Status: Priority scheduling implemented in Phase 6 repository Claim/dispatch.

PostgreSQL ranks eligible QUEUED work by priority DESC / created_at ASC / id ASC.
Execution is non-preemptive. See ADR 0007 and infrastructure/postgres; no redundant
scheduler component is needed. Aging, scheduled and capability-aware work remain
Planned and require an externally prepared future Phase prompt.
