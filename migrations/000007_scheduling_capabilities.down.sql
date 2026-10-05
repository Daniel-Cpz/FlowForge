DROP INDEX jobs_queued_due_priority_idx;
ALTER TABLE jobs DROP CONSTRAINT jobs_schedule_occurrence, DROP CONSTRAINT jobs_schedule_pair,
 DROP CONSTRAINT jobs_scheduled_time, DROP CONSTRAINT jobs_occurrence_time,
 DROP COLUMN scheduled_at, DROP COLUMN required_capabilities, DROP COLUMN schedule_id, DROP COLUMN scheduled_for;
DROP TABLE job_schedules;
DROP TRIGGER workers_capabilities_fixed ON workers;
DROP FUNCTION flowforge_worker_capabilities_fixed();
ALTER TABLE workers DROP COLUMN capabilities;
DROP FUNCTION flowforge_capabilities_valid(TEXT[]);
