CREATE TABLE workers (
    worker_id UUID PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('ONLINE','IDLE','BUSY','DRAINING','OFFLINE')),
    last_heartbeat TIMESTAMPTZ NOT NULL,
    concurrency INTEGER NOT NULL CHECK (concurrency BETWEEN 1 AND 32),
    active_jobs INTEGER NOT NULL DEFAULT 0 CHECK (active_jobs BETWEEN 0 AND concurrency),
    stopped_at TIMESTAMPTZ,
    offline_reason TEXT CHECK (offline_reason IN ('heartbeat_expired','graceful_shutdown','fatal_error'))
);
CREATE INDEX workers_heartbeat_idx ON workers(last_heartbeat,worker_id) WHERE status<>'OFFLINE';
CREATE INDEX jobs_expired_lease_idx ON jobs(lease_expiry,id) WHERE status='RUNNING';
-- Prior owned RUNNING attempts had no lease. They become immediately eligible;
-- the reaper still verifies owner, attempt and matching history transactionally.
UPDATE jobs SET lease_expiry=clock_timestamp() WHERE status='RUNNING' AND lease_expiry IS NULL;
COMMENT ON COLUMN jobs.lease_expiry IS 'PostgreSQL clock authority; owner and attempt fence lease renewal/finalization';
