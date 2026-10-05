ALTER TABLE jobs ADD COLUMN cancel_requested_at TIMESTAMPTZ NULL;
-- Preserve submission identity when explicit redrive increases execution budget.
ALTER TABLE jobs ADD COLUMN submission_max_attempts INTEGER;
UPDATE jobs SET submission_max_attempts=max_attempts;
ALTER TABLE jobs ALTER COLUMN submission_max_attempts SET NOT NULL;
ALTER TABLE jobs ADD CONSTRAINT jobs_submission_budget CHECK (submission_max_attempts BETWEEN 1 AND 100);
ALTER TABLE jobs ADD CONSTRAINT jobs_cancel_request_state CHECK (cancel_requested_at IS NULL OR status IN ('RUNNING','CANCELLED'));
CREATE INDEX jobs_queued_priority_idx ON jobs(priority DESC, created_at ASC, id ASC)
 WHERE status='QUEUED' AND attempt_count<max_attempts;
CREATE INDEX jobs_dead_letter_page_idx ON jobs(created_at DESC,id DESC) WHERE status='DEAD_LETTER';
COMMENT ON COLUMN jobs.submission_max_attempts IS 'Original canonical submission budget; redrive changes max_attempts only';
COMMENT ON COLUMN jobs.cancel_requested_at IS 'Durable user intent; RUNNING settles only through fenced finalize or expiry recovery';
