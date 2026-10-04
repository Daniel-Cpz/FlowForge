package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
)

// PendingDispatch also reconciles published but still QUEUED work. A Redis
// restart or lost pre-claim delivery therefore cannot silently strand a job.
// This does not reclaim RUNNING work or introduce worker leases.
func (r *JobRepository) PendingDispatch(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit < 1 || limit > 100 {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, `SELECT d.job_id FROM job_dispatch d JOIN jobs j ON j.id=d.job_id
	 WHERE j.status=$1 AND (d.published_at IS NULL OR d.published_at < CURRENT_TIMESTAMP - INTERVAL '30 seconds')
	 ORDER BY d.published_at NULLS FIRST, d.created_at, d.job_id LIMIT $2`, job.Queued, limit)
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
	j, err := scanJob(tx.QueryRow(ctx, `UPDATE jobs SET status=$2, assigned_worker=$3,
	 attempt_count=attempt_count+1, started_at=clock_timestamp(), finished_at=NULL, result=NULL
	 WHERE id=$1 AND status=$4 AND attempt_count < max_attempts RETURNING `+columns, id, job.Running, worker, job.Queued))
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

// Finalize requires the owner AND attempt number and commits both records.
func (r *JobRepository) Finalize(ctx context.Context, j *job.Job, status job.Status, result json.RawMessage) error {
	if j == nil || j.AssignedWorker == nil || j.Status != job.Running ||
		(status != job.Succeeded && status != job.Failed) || !job.CanTransition(j.Status, status) || !json.Valid(result) {
		return job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	stored, err := scanJob(tx.QueryRow(ctx, `UPDATE jobs SET status=$2, result=$3, finished_at=clock_timestamp()
	 WHERE id=$1 AND status=$4 AND assigned_worker=$5 AND attempt_count=$6 RETURNING `+columns,
		j.ID, status, result, job.Running, *j.AssignedWorker, j.AttemptCount))
	if errors.Is(err, job.ErrNotFound) {
		return job.ErrInvalidTransition
	}
	if err != nil {
		return err
	}
	res, err := tx.Exec(ctx, `UPDATE job_attempts SET status=$4, result=$5, finished_at=$6
	 WHERE job_id=$1 AND worker_id=$2 AND attempt_number=$3 AND status=$7`,
		j.ID, *j.AssignedWorker, j.AttemptCount, status, result, stored.FinishedAt, job.Running)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return job.ErrInvalidStoredData
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	*j = *stored
	return nil
}
