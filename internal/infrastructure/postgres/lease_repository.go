package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/capability"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *JobRepository) RegisterWorker(ctx context.Context, id uuid.UUID, concurrency int) error {
	return r.RegisterWorkerCapabilities(ctx, id, concurrency, nil)
}
func (r *JobRepository) RegisterWorkerCapabilities(ctx context.Context, id uuid.UUID, concurrency int, values []string) error {
	capabilities, err := capability.Normalize(values)
	if err != nil {
		return job.ErrInvalidInput
	}
	if id == uuid.Nil || concurrency < 1 || concurrency > 32 {
		return job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := r.pool.Exec(ctx, `INSERT INTO workers(worker_id,status,last_heartbeat,concurrency,active_jobs,capabilities)
 VALUES($1,'ONLINE',clock_timestamp(),$2,0,$3) ON CONFLICT(worker_id) DO NOTHING`, id, concurrency, capabilities)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return worker.ErrOffline
	}
	r.changed("worker.changed", id, "ONLINE")
	return nil
}
func (r *JobRepository) Heartbeat(ctx context.Context, id uuid.UUID, active int) error {
	ctx, cancel := context.WithTimeout(ctx, min(3*time.Second, r.leasePolicy.HeartbeatInterval))
	defer cancel()
	var changed bool
	err := r.pool.QueryRow(ctx, `WITH previous AS (SELECT worker_id,status,active_jobs FROM workers WHERE worker_id=$1
 AND status IN ('ONLINE','IDLE','BUSY') AND last_heartbeat>clock_timestamp()-make_interval(secs=>$3) FOR UPDATE)
 UPDATE workers w SET status=CASE WHEN $2=0 THEN 'IDLE' ELSE 'BUSY' END,last_heartbeat=clock_timestamp(),active_jobs=$2
 FROM previous p WHERE w.worker_id=p.worker_id AND w.status IN ('ONLINE','IDLE','BUSY')
 AND w.last_heartbeat>clock_timestamp()-make_interval(secs=>$3)
 RETURNING p.active_jobs<>$2 OR p.status<>CASE WHEN $2=0 THEN 'IDLE' ELSE 'BUSY' END`, id, active, r.leasePolicy.OfflineAfter.Seconds()).Scan(&changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return worker.ErrOffline
	}
	if err != nil {
		return err
	}
	if changed {
		status := "IDLE"
		if active > 0 {
			status = "BUSY"
		}
		r.changed("worker.changed", id, status)
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
	if err == nil {
		r.changed("worker.changed", id, "DRAINING")
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
	if err == nil {
		r.changed("worker.changed", id, "OFFLINE")
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
	var requested *time.Time
	err := r.pool.QueryRow(ctx, `WITH live_worker AS (SELECT worker_id FROM workers
 WHERE worker_id=$2 AND status IN ('ONLINE','IDLE','BUSY')
 AND last_heartbeat>clock_timestamp()-make_interval(secs=>$5) FOR SHARE)
 UPDATE jobs SET lease_expiry=clock_timestamp()+make_interval(secs=>$4)
 WHERE id=$1 AND status='RUNNING' AND assigned_worker=$2 AND attempt_count=$3 AND lease_expiry>clock_timestamp()
 AND EXISTS(SELECT 1 FROM live_worker) RETURNING lease_expiry,cancel_requested_at`,
		j.ID, *j.AssignedWorker, j.AttemptCount, r.leasePolicy.LeaseDuration.Seconds(), r.leasePolicy.OfflineAfter.Seconds()).Scan(&expiry, &requested)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, job.ErrLeaseLost
	}
	if err == nil && requested != nil {
		return expiry.UTC(), job.ErrCancellationRequested
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
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		r.changed("worker.changed", id, "OFFLINE")
	}
	return ids, nil
}

// Recovery closes expired Attempts through the same budget and backoff policy.
// No Redis access or waiting occurs while row locks are held.
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
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM jobs WHERE status='RUNNING' AND lease_expiry<=clock_timestamp() ORDER BY lease_expiry,id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	candidates := make([]*job.Job, 0, limit)
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	recovered := make([]job.RecoveredAttempt, 0, len(candidates))
	for _, j := range candidates {
		if j.AssignedWorker == nil || j.AttemptCount < 1 {
			return nil, job.ErrInvalidStoredData
		}
		stored, err := r.finish(ctx, tx, j, job.Failed, json.RawMessage(`{"error":"lease_expired"}`), job.Failure{Class: job.Retryable, Code: "lease_expired"}, true)
		if err != nil {
			return nil, err
		}
		recovered = append(recovered, job.RecoveredAttempt{JobID: j.ID, WorkerID: *j.AssignedWorker, AttemptNumber: j.AttemptCount, Status: stored.Status, RetryAt: stored.RetryAt})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, v := range recovered {
		r.changed("job.changed", v.JobID, string(v.Status))
	}
	return recovered, nil
}

// PromoteRetries atomically makes due retries dispatchable. Every process may
// run this bounded scan; SKIP LOCKED prevents duplicate effective transitions.
func (r *JobRepository) PromoteRetries(ctx context.Context, limit int) ([]uuid.UUID, error) {
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
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM jobs WHERE status='RETRYING' AND retry_at<=clock_timestamp() AND attempt_count<max_attempts ORDER BY retry_at,id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	candidates := make([]*job.Job, 0, limit)
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(candidates))
	for _, j := range candidates {
		if err := j.Transition(job.Queued); err != nil {
			return nil, err
		}
		res, err := tx.Exec(ctx, `UPDATE jobs SET status='QUEUED',retry_at=NULL,started_at=NULL,finished_at=NULL,result=NULL WHERE id=$1 AND status='RETRYING' AND retry_at<=clock_timestamp() AND attempt_count<max_attempts`, j.ID)
		if err != nil {
			return nil, err
		}
		if res.RowsAffected() != 1 {
			return nil, job.ErrInvalidTransition
		}
		if _, err := tx.Exec(ctx, `INSERT INTO job_dispatch(job_id) VALUES($1) ON CONFLICT(job_id) DO UPDATE SET published_at=NULL`, j.ID); err != nil {
			return nil, err
		}
		ids = append(ids, j.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, id := range ids {
		r.changed("job.changed", id, "QUEUED")
	}
	return ids, nil
}
