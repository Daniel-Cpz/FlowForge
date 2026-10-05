CREATE FUNCTION flowforge_capabilities_valid(values_ TEXT[]) RETURNS BOOLEAN
LANGUAGE SQL IMMUTABLE STRICT AS $$
 SELECT cardinality(values_)<=16
 AND (cardinality(values_)=0 OR (array_ndims(values_)=1 AND array_lower(values_,1)=1))
 AND NOT EXISTS(SELECT 1 FROM unnest(values_) v WHERE v IS NULL OR length(v)>32 OR v COLLATE "C" !~ '^[a-z0-9][a-z0-9._-]*$')
 AND values_=COALESCE((SELECT array_agg(v ORDER BY v COLLATE "C") FROM (SELECT DISTINCT v FROM unnest(values_) v) d),'{}'::TEXT[])
$$;
ALTER TABLE workers ADD COLUMN capabilities TEXT[] NOT NULL DEFAULT '{}'
 CHECK(flowforge_capabilities_valid(capabilities));
CREATE FUNCTION flowforge_worker_capabilities_fixed() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.worker_id IS DISTINCT FROM OLD.worker_id OR NEW.capabilities IS DISTINCT FROM OLD.capabilities THEN
  RAISE EXCEPTION 'worker identity capabilities are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER workers_capabilities_fixed BEFORE UPDATE ON workers FOR EACH ROW EXECUTE FUNCTION flowforge_worker_capabilities_fixed();
CREATE TABLE job_schedules (
 id UUID PRIMARY KEY,
 status TEXT NOT NULL CHECK(status IN ('ACTIVE','CANCELLED')),
 type VARCHAR(128) NOT NULL CHECK(length(btrim(type))>0),
 payload JSONB NOT NULL,
 priority INTEGER NOT NULL CHECK(priority BETWEEN 0 AND 100),
 max_attempts INTEGER NOT NULL CHECK(max_attempts BETWEEN 1 AND 100),
 timeout INTEGER NOT NULL CHECK(timeout BETWEEN 1 AND 86400),
 required_capabilities TEXT[] NOT NULL DEFAULT '{}' CHECK(flowforge_capabilities_valid(required_capabilities)),
 interval_seconds INTEGER NOT NULL CHECK(interval_seconds BETWEEN 1 AND 604800),
 next_run_at TIMESTAMPTZ NOT NULL CHECK(next_run_at>='0001-01-01T00:00:00Z' AND next_run_at<'10000-01-01T00:00:00Z'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(updated_at>=created_at)
);
CREATE INDEX job_schedules_due_idx ON job_schedules(next_run_at,id) WHERE status='ACTIVE';
ALTER TABLE jobs ADD COLUMN scheduled_at TIMESTAMPTZ,
 ADD COLUMN required_capabilities TEXT[] NOT NULL DEFAULT '{}' CHECK(flowforge_capabilities_valid(required_capabilities)),
 ADD COLUMN schedule_id UUID REFERENCES job_schedules(id),
 ADD COLUMN scheduled_for TIMESTAMPTZ,
 ADD CONSTRAINT jobs_schedule_pair CHECK((schedule_id IS NULL)=(scheduled_for IS NULL)),
 ADD CONSTRAINT jobs_scheduled_time CHECK(scheduled_at IS NULL OR (scheduled_at>='0001-01-01T00:00:00Z' AND scheduled_at<'10000-01-01T00:00:00Z')),
 ADD CONSTRAINT jobs_occurrence_time CHECK(scheduled_for IS NULL OR (scheduled_at IS NOT NULL AND scheduled_for=scheduled_at)),
 ADD CONSTRAINT jobs_schedule_occurrence UNIQUE(schedule_id,scheduled_for);
CREATE INDEX jobs_queued_due_priority_idx ON jobs(priority DESC,created_at,id,scheduled_at) WHERE status='QUEUED' AND attempt_count<max_attempts;
COMMENT ON COLUMN jobs.scheduled_at IS 'Initial PostgreSQL time eligibility; does not replace retry_at or execution timeout';
COMMENT ON TABLE job_schedules IS 'Fixed interval; one oldest due occurrence per bounded scan; missed middle intervals skipped';
