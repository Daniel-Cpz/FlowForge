package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *JobRepository) RegisterWorker(ctx context.Context, id uuid.UUID, concurrency int) error {
	if id == uuid.Nil || concurrency < 1 || concurrency > 32 {
		return job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := r.pool.Exec(ctx, `INSERT INTO workers(worker_id,status,last_heartbeat,concurrency,active_jobs)
 VALUES($1,'ONLINE',clock_timestamp(),$2,0) ON CONFLICT(worker_id) DO NOTHING`, id, concurrency)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return worker.ErrOffline
	}
	return nil
}
func (r *JobRepository) Heartbeat(ctx context.Context, id uuid.UUID, active int) error {
	ctx, cancel := context.WithTimeout(ctx, min(3*time.Second, r.leasePolicy.HeartbeatInterval))
	defer cancel()
	result, err := r.pool.Exec(ctx, `UPDATE workers SET status=CASE WHEN $2=0 THEN 'IDLE' ELSE 'BUSY' END,
 last_heartbeat=clock_timestamp(),active_jobs=$2 WHERE worker_id=$1 AND status IN ('ONLINE','IDLE','BUSY')
 AND last_heartbeat>clock_timestamp()-make_interval(secs=>$3)`, id, active, r.leasePolicy.OfflineAfter.Seconds())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return worker.ErrOffline
	}
	return nil
}
func (r *JobRepository) MarkDraining(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := r.pool.Exec(ctx, `UPDATE workers SET status='DRAINING',last_heartbeat=clock_timestamp() WHERE worker_id=$1 AND status<>'OFFLINE'`, id)
	if err == nil && result.RowsAffected() != 1 {
		return worker.ErrOffline
	}
	return err
}
func (r *JobRepository) StopWorker(ctx context.Context, id uuid.UUID, reason string) error {
	if reason != "graceful_shutdown" && reason != "fatal_error" {
		return job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := r.pool.Exec(ctx, `UPDATE workers SET status='OFFLINE',active_jobs=0,stopped_at=clock_timestamp(),offline_reason=$2
 WHERE worker_id=$1 AND status<>'OFFLINE'`, id, reason)
	if err == nil && result.RowsAffected() != 1 {
		return worker.ErrOffline
	}
	return err
}
func (r *JobRepository) Renew(ctx context.Context, j *job.Job) (time.Time, error) {
	if j == nil || j.AssignedWorker == nil || j.AttemptCount < 1 {
		return time.Time{}, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, min(3*time.Second, r.leasePolicy.RenewInterval))
	defer cancel()
	var expiry time.Time
	err := r.pool.QueryRow(ctx, `WITH live_worker AS (SELECT worker_id FROM workers
 WHERE worker_id=$2 AND status IN ('ONLINE','IDLE','BUSY')
 AND last_heartbeat>clock_timestamp()-make_interval(secs=>$5) FOR SHARE)
 UPDATE jobs SET lease_expiry=clock_timestamp()+make_interval(secs=>$4)
 WHERE id=$1 AND status='RUNNING' AND assigned_worker=$2 AND attempt_count=$3 AND lease_expiry>clock_timestamp()
 AND EXISTS(SELECT 1 FROM live_worker) RETURNING lease_expiry`,
		j.ID, *j.AssignedWorker, j.AttemptCount, r.leasePolicy.LeaseDuration.Seconds(), r.leasePolicy.OfflineAfter.Seconds()).Scan(&expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, job.ErrLeaseLost
	}
	return expiry.UTC(), err
}
func (r *JobRepository) DetectOffline(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit < 1 || limit > 100 {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, `WITH expired AS (SELECT worker_id FROM workers WHERE status<>'OFFLINE'
 AND last_heartbeat<=clock_timestamp()-make_interval(secs=>$1) ORDER BY last_heartbeat,worker_id LIMIT $2 FOR UPDATE SKIP LOCKED)
 UPDATE workers w SET status='OFFLINE',offline_reason='heartbeat_expired',stopped_at=clock_timestamp()
 FROM expired e WHERE w.worker_id=e.worker_id AND w.status<>'OFFLINE'
 AND w.last_heartbeat<=clock_timestamp()-make_interval(secs=>$1) RETURNING w.worker_id`, r.leasePolicy.OfflineAfter.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Recovery and dispatch intent are one transaction. Row locks and conditional
// owner/attempt/expiry predicates fence concurrent reapers and late execution.
// A crash attempt ends FAILED/lease_expired; this is not business-failure retry.
func (r *JobRepository) RecoverExpired(ctx context.Context, limit int) ([]job.RecoveredAttempt, error) {
	if limit < 1 || limit > 100 {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, `SELECT id,assigned_worker,attempt_count,lease_expiry,clock_timestamp() FROM jobs
 WHERE status='RUNNING' AND lease_expiry<=clock_timestamp() ORDER BY lease_expiry,id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id      uuid.UUID
		owner   *uuid.UUID
		attempt int
		expiry  *time.Time
		now     time.Time
	}
	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.owner, &c.attempt, &c.expiry, &c.now); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	recovered := make([]job.RecoveredAttempt, 0, len(candidates))
	for _, c := range candidates {
		j := job.Job{ID: c.id, Status: job.Running, AssignedWorker: c.owner, AttemptCount: c.attempt, LeaseExpiry: c.expiry}
		if err := j.RecoverExpired(c.now); err != nil {
			return nil, job.ErrInvalidStoredData
		}
		// Closing the exact old Attempt is mandatory. Corrupt history rolls back
		// the entire bounded batch rather than silently inventing recovery evidence.
		result, err := tx.Exec(ctx, `UPDATE job_attempts SET status='FAILED',error='lease_expired',
   result='{"error":"lease_expired"}'::jsonb,finished_at=clock_timestamp()
   WHERE job_id=$1 AND worker_id=$2 AND attempt_number=$3 AND status='RUNNING'`, c.id, *c.owner, c.attempt)
		if err != nil {
			return nil, err
		}
		if result.RowsAffected() != 1 {
			return nil, job.ErrInvalidStoredData
		}
		result, err = tx.Exec(ctx, `UPDATE jobs SET status='QUEUED',assigned_worker=NULL,lease_expiry=NULL,
   started_at=NULL,finished_at=NULL,result=NULL WHERE id=$1 AND status='RUNNING' AND assigned_worker=$2
   AND attempt_count=$3 AND lease_expiry=$4 AND lease_expiry<=clock_timestamp()`, c.id, *c.owner, c.attempt, c.expiry)
		if err != nil {
			return nil, err
		}
		if result.RowsAffected() != 1 {
			return nil, job.ErrLeaseLost
		}
		if _, err := tx.Exec(ctx, `INSERT INTO job_dispatch(job_id) VALUES($1) ON CONFLICT(job_id) DO UPDATE SET published_at=NULL`, c.id); err != nil {
			return nil, err
		}
		recovered = append(recovered, job.RecoveredAttempt{JobID: c.id, WorkerID: *c.owner, AttemptNumber: c.attempt})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return recovered, nil
}
