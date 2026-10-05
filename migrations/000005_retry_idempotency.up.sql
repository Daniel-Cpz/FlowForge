-- No arbitrary survivor is selected for historical duplicate keys.
-- Lock blocks concurrent writes between preflight and index creation.
LOCK TABLE jobs IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM jobs WHERE idempotency_key IS NOT NULL GROUP BY idempotency_key HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'Phase 5 migration blocked: legacy duplicate non-null idempotency keys require explicit operator resolution';
 END IF;
 IF EXISTS (SELECT 1 FROM jobs WHERE attempt_count > 100) THEN
  RAISE EXCEPTION 'Phase 5 migration blocked: legacy attempt count exceeds supported budget 100; preserve history and review explicitly';
 END IF;
END $$;
ALTER TABLE jobs ADD COLUMN retry_at TIMESTAMPTZ;
-- Preserve every historic Attempt. Freeze an already exceeded legacy budget at
-- its actual count rather than decreasing counters or permitting extra runs.
UPDATE jobs SET max_attempts=attempt_count WHERE attempt_count>max_attempts;
-- Administrative normalization of Phase 4 requeues with no remaining budget.
UPDATE jobs SET status='DEAD_LETTER', assigned_worker=NULL, lease_expiry=NULL,
 finished_at=clock_timestamp(), result='{"error":"attempt_budget_exhausted"}'::jsonb
 WHERE status='QUEUED' AND attempt_count>=max_attempts;
UPDATE jobs SET retry_at=clock_timestamp()+INTERVAL '1 second', assigned_worker=NULL,
 lease_expiry=NULL,finished_at=NULL WHERE status='RETRYING';
ALTER TABLE jobs ADD CONSTRAINT jobs_attempt_budget CHECK (attempt_count<=max_attempts);
ALTER TABLE jobs ADD CONSTRAINT jobs_retry_schedule CHECK (
 (status='RETRYING' AND retry_at IS NOT NULL AND assigned_worker IS NULL AND lease_expiry IS NULL AND attempt_count<max_attempts)
 OR (status<>'RETRYING' AND retry_at IS NULL));
CREATE INDEX jobs_due_retry_idx ON jobs(retry_at,id) WHERE status='RETRYING';
CREATE UNIQUE INDEX jobs_idempotency_key_unique ON jobs(idempotency_key) WHERE idempotency_key IS NOT NULL;
COMMENT ON COLUMN jobs.idempotency_key IS 'Exact global submission key; same canonical request replays, different request conflicts';
COMMENT ON COLUMN jobs.max_attempts IS 'Total execution attempt budget, including crashes and operational interruptions';
