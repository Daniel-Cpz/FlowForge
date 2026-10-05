DROP INDEX jobs_dead_letter_page_idx;
DROP INDEX jobs_queued_priority_idx;
ALTER TABLE jobs DROP CONSTRAINT jobs_cancel_request_state;
ALTER TABLE jobs DROP CONSTRAINT jobs_submission_budget;
ALTER TABLE jobs DROP COLUMN submission_max_attempts;
ALTER TABLE jobs DROP COLUMN cancel_requested_at;
-- Stop new workers before down. Pending cancellation intent and original
-- submission budgets cannot be represented by the old schema.
