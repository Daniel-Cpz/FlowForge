CREATE TABLE jobs (
    id UUID PRIMARY KEY,
    type VARCHAR(128) NOT NULL CHECK (length(btrim(type)) > 0),
    status TEXT NOT NULL DEFAULT 'QUEUED' CHECK (status IN
        ('QUEUED','RUNNING','SUCCEEDED','FAILED','RETRYING','DEAD_LETTER','CANCELLED','TIMED_OUT')),
    priority INTEGER NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 100),
    payload JSONB NOT NULL,
    result JSONB,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 100),
    timeout INTEGER NOT NULL DEFAULT 300 CHECK (timeout BETWEEN 1 AND 86400),
    idempotency_key VARCHAR(255) CHECK (length(btrim(idempotency_key)) > 0),
    assigned_worker UUID,
    lease_expiry TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at)
);
CREATE INDEX jobs_created_at_id_idx ON jobs (created_at DESC, id DESC);
COMMENT ON COLUMN jobs.timeout IS 'Seconds; execution timeout is not enforced in Phase 0';
COMMENT ON COLUMN jobs.idempotency_key IS 'Reserved metadata only; no uniqueness or deduplication guarantee';
