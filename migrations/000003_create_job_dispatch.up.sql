CREATE TABLE job_dispatch (
    job_id UUID PRIMARY KEY REFERENCES jobs(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMPTZ
);
CREATE INDEX job_dispatch_pending_idx ON job_dispatch (published_at, created_at, job_id);
-- Existing queued work must receive durable dispatch intent too.
INSERT INTO job_dispatch(job_id) SELECT id FROM jobs WHERE status = 'QUEUED';
