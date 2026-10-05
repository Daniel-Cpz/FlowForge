DROP INDEX jobs_idempotency_key_unique;
DROP INDEX jobs_due_retry_idx;
ALTER TABLE jobs DROP CONSTRAINT jobs_retry_schedule;
ALTER TABLE jobs DROP CONSTRAINT jobs_attempt_budget;
ALTER TABLE jobs DROP COLUMN retry_at;
COMMENT ON COLUMN jobs.idempotency_key IS 'Reserved metadata only; no uniqueness or deduplication guarantee';
-- Historic normalization is intentionally not reversed: counters/Attempts and
-- terminal outcomes must never be erased or automatically restarted by down.
