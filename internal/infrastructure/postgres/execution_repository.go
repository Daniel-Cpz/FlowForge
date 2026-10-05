package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PendingDispatch also reconciles published but still QUEUED work. A Redis
// restart or lost pre-claim delivery therefore cannot silently strand a job.
// Due retry promotion restores this intent in its RETRYING -> QUEUED transaction.
func (r *JobRepository) PendingDispatch(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit < 1 || limit > 100 {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, `SELECT d.job_id FROM job_dispatch d JOIN jobs j ON j.id=d.job_id
	 WHERE j.status=$1 AND (d.published_at IS NULL OR d.published_at < CURRENT_TIMESTAMP - INTERVAL '30 seconds')
	 AND j.attempt_count<j.max_attempts
	 ORDER BY j.priority DESC, j.created_at ASC, j.id ASC LIMIT $2`, job.Queued, limit)
	if err != nil {
		return nil, fmt.Errorf("pending dispatch: %w", err)
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

func (r *JobRepository) MarkPublished(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := r.pool.Exec(ctx, `UPDATE job_dispatch SET published_at=clock_timestamp() WHERE job_id=$1`, id)
	if err != nil {
		return fmt.Errorf("mark dispatch: %w", err)
	}
	if res.RowsAffected() != 1 {
		return job.ErrNotFound
	}
	return nil
}

// Claim atomically wins QUEUED -> RUNNING and creates exactly one business
// attempt in the same transaction. No SELECT followed by unconditional UPDATE.
func (r *JobRepository) Claim(ctx context.Context, id, worker uuid.UUID) (*job.Job, error) {
	if id == uuid.Nil || worker == uuid.Nil || !job.CanTransition(job.Queued, job.Running) {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	// Serialize registry expiry with a new claim, without holding a database
	// transaction while executing or publishing. Worker rows precede job rows.
	var live int
	err = tx.QueryRow(ctx, `SELECT 1 FROM workers WHERE worker_id=$1
	 AND status IN ('ONLINE','IDLE','BUSY') AND last_heartbeat>clock_timestamp()-make_interval(secs=>$2)
	 FOR SHARE`, worker, r.leasePolicy.OfflineAfter.Seconds()).Scan(&live)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, job.ErrInvalidTransition
	}
	if err != nil {
		return nil, err
	}
	// Only the short scheduling decision is serialized, never execution. A
	// transaction-scoped lock avoids two snapshots skipping the same backlog.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(704621831)`); err != nil {
		return nil, err
	}
	var first uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM jobs WHERE status='QUEUED' AND attempt_count<max_attempts ORDER BY priority DESC,created_at,id LIMIT 1`).Scan(&first)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, job.ErrInvalidTransition
	}
	if err != nil {
		return nil, err
	}
	if first != id {
		var eligible bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=$1 AND status='QUEUED' AND attempt_count<max_attempts)`, id).Scan(&eligible); err != nil {
			return nil, err
		}
		if eligible {
			return nil, job.ErrPriorityDeferred
		}
		return nil, job.ErrInvalidTransition
	}
	j, err := scanJob(tx.QueryRow(ctx, `UPDATE jobs SET status=$2, assigned_worker=$3,
	 attempt_count=attempt_count+1, started_at=clock_timestamp(), finished_at=NULL, result=NULL,
	 lease_expiry=clock_timestamp()+make_interval(secs=>$5)
	 WHERE id=$1 AND status=$4 AND attempt_count<max_attempts
	 AND NOT EXISTS(SELECT 1 FROM jobs p WHERE p.status='QUEUED' AND p.attempt_count<p.max_attempts
	 AND (p.priority>jobs.priority OR (p.priority=jobs.priority AND (p.created_at,p.id)<(jobs.created_at,jobs.id))))
	 AND EXISTS(SELECT 1 FROM workers WHERE worker_id=$3 AND status IN ('ONLINE','IDLE','BUSY')
	 AND last_heartbeat>clock_timestamp()-make_interval(secs=>$6))
	 RETURNING `+columns, id, job.Running, worker, job.Queued, r.leasePolicy.LeaseDuration.Seconds(), r.leasePolicy.OfflineAfter.Seconds()))
	if errors.Is(err, job.ErrNotFound) {
		return nil, job.ErrInvalidTransition
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO job_attempts(id,job_id,worker_id,attempt_number,status,started_at)
	 VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), id, worker, j.AttemptCount, job.Running, j.StartedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return j, nil
}

// Finalize commits the exact Attempt and its durable outcome before any ACK.
func (r *JobRepository) Finalize(ctx context.Context, j *job.Job, status job.Status, result json.RawMessage, failures ...job.Failure) error {
	if j == nil || j.AssignedWorker == nil || j.Status != job.Running || (status != job.Succeeded && status != job.Failed && status != job.TimedOut && status != job.Cancelled) || !json.Valid(result) || len(failures) > 1 {
		return job.ErrInvalidInput
	}
	f := job.Failure{Class: job.Permanent, Code: "execution_failed"}
	if len(failures) == 1 {
		f = failures[0]
	}
	if (status == job.Failed || status == job.TimedOut) && !f.Valid() {
		return job.ErrInvalidInput
	}
	if status == job.Succeeded && len(failures) != 0 {
		return job.ErrInvalidInput
	}
	if status == job.TimedOut && (len(failures) != 1 || f.Class != job.Retryable || f.Code != "execution_timeout") {
		return job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	current, err := scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM jobs WHERE id=$1 AND status='RUNNING' AND assigned_worker=$2 AND attempt_count=$3 AND lease_expiry>clock_timestamp() FOR UPDATE`, j.ID, *j.AssignedWorker, j.AttemptCount))
	if errors.Is(err, job.ErrNotFound) {
		return job.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	stored, err := r.finish(ctx, tx, current, status, result, f, false)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	*j = *stored
	return nil
}

// finish is used by normal finalization and expired-lease recovery. Its caller
// holds the Job row lock. SQL rechecks the fence against current database time.
func (r *JobRepository) finish(ctx context.Context, tx pgx.Tx, current *job.Job, status job.Status, result json.RawMessage, f job.Failure, expired bool) (*job.Job, error) {
	owner := *current.AssignedWorker
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	// Row lock linearizes user intent against success/timeout/recovery. If the
	// request won, even a late executor success must settle as user cancellation.
	if current.CancelRequestedAt != nil {
		status = job.Cancelled
		result = json.RawMessage(`{"error":"user_cancelled"}`)
		f = job.Failure{Class: job.Permanent, Code: "user_cancelled"}
	} else if status == job.Cancelled {
		return nil, job.ErrControlConflict
	}
	target := *current
	delay := r.retryPolicy.BaseDelay
	var code *string
	if status == job.Failed || status == job.TimedOut {
		if f.Class == job.Retryable && current.AttemptCount < current.MaxAttempts {
			var err error
			delay, err = r.retryPolicy.Delay(current.AttemptCount, r.jitter)
			if err != nil {
				return nil, err
			}
		}
		if err := target.FailOutcome(status, f, now, delay); err != nil {
			return nil, err
		}
		code = &f.Code
	} else {
		if err := target.Transition(status); err != nil {
			return nil, err
		}
		target.LeaseExpiry = nil
		if status == job.Cancelled {
			code = &f.Code
		}
	}
	predicate := `lease_expiry>clock_timestamp()`
	if expired {
		predicate = `lease_expiry<=clock_timestamp()`
	}
	stored, err := scanJob(tx.QueryRow(ctx, `UPDATE jobs SET status=$4,result=$5,assigned_worker=$6,lease_expiry=NULL,
 retry_at=CASE WHEN $4='RETRYING' THEN clock_timestamp()+make_interval(secs=>$7) ELSE NULL END,
 finished_at=CASE WHEN $4='RETRYING' THEN NULL ELSE clock_timestamp() END
 WHERE id=$1 AND status='RUNNING' AND assigned_worker=$2 AND attempt_count=$3 AND `+predicate+` RETURNING `+columns,
		current.ID, owner, current.AttemptCount, target.Status, result, target.AssignedWorker, delay.Seconds()))
	if errors.Is(err, job.ErrNotFound) {
		return nil, job.ErrLeaseLost
	}
	if err != nil {
		return nil, err
	}
	// Attempt completion time is independent of the later retry/Job completion.
	res, err := tx.Exec(ctx, `UPDATE job_attempts SET status=$4,result=$5,error=$6,finished_at=COALESCE($7,clock_timestamp())
 WHERE job_id=$1 AND worker_id=$2 AND attempt_number=$3 AND status='RUNNING'`, current.ID, owner, current.AttemptCount, status, result, code, stored.FinishedAt)
	if err != nil {
		return nil, err
	}
	if res.RowsAffected() != 1 {
		return nil, job.ErrInvalidStoredData
	}
	return stored, nil
}
