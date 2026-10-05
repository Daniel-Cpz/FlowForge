# Scheduler

Status: Priority and time/capability scheduling implemented in Phases 6–7.

PostgreSQL ranks eligible QUEUED work by priority DESC / created_at ASC / id ASC.
Execution is non-preemptive. Claim ranks only the live worker's due, compatible
Jobs. Each worker's existing maintenance loop materializes at most 100 fixed-
interval schedules via PostgreSQL row locks and atomic occurrence/intent/cursor
writes. Missed middle intervals are skipped; cancel schedule leaves existing Jobs
alone. See ADRs 0007–0008 and infrastructure/postgres; no separate scheduler service.
Aging, cron, editable templates and capability routing remain Planned.
