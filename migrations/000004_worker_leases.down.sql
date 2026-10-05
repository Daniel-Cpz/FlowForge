DROP INDEX jobs_expired_lease_idx;
DROP TABLE workers;
-- Historical job/attempt data is retained. Downgrade does not promise that an
-- older binary understands lease recovery; stop all processes before rollback.
