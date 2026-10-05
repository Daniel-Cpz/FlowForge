package postgres

import (
	"context"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
	"time"
)

var _ job.ControlRepository = (*JobRepository)(nil)

func (r *JobRepository) Cancel(ctx context.Context, id uuid.UUID) (*job.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	current, err := scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM jobs WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	var stored *job.Job
	switch current.Status {
	case job.Cancelled:
		stored = current
	case job.Running:
		stored, err = scanJob(tx.QueryRow(ctx, `UPDATE jobs SET cancel_requested_at=COALESCE(cancel_requested_at,clock_timestamp()) WHERE id=$1 RETURNING `+columns, id))
	case job.Queued, job.Retrying:
		if err = current.Transition(job.Cancelled); err != nil {
			return nil, err
		}
		stored, err = scanJob(tx.QueryRow(ctx, `UPDATE jobs SET status='CANCELLED',cancel_requested_at=clock_timestamp(),retry_at=NULL,lease_expiry=NULL,assigned_worker=NULL,finished_at=clock_timestamp(),result='{"error":"user_cancelled"}'::jsonb WHERE id=$1 RETURNING `+columns, id))
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM job_dispatch WHERE job_id=$1`, id)
		}
	default:
		return nil, job.ErrControlConflict
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return stored, nil
}

func (r *JobRepository) RedriveDeadLetter(ctx context.Context, id uuid.UUID) (*job.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	current, err := scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM jobs WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if current.Status != job.DeadLetter || current.MaxAttempts >= 100 {
		return nil, job.ErrControlConflict
	}
	// The ordinary DEAD_LETTER graph remains terminal; preserve all history.
	stored, err := scanJob(tx.QueryRow(ctx, `UPDATE jobs SET status='QUEUED',max_attempts=max_attempts+1,assigned_worker=NULL,lease_expiry=NULL,retry_at=NULL,cancel_requested_at=NULL,started_at=NULL,finished_at=NULL,result=NULL WHERE id=$1 AND status='DEAD_LETTER' AND max_attempts<100 RETURNING `+columns, id))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO job_dispatch(job_id) VALUES($1) ON CONFLICT(job_id) DO UPDATE SET published_at=NULL`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return stored, nil
}

// Cursor order deliberately matches the existing creation-time Job cursor.
func (r *JobRepository) ListDeadLetter(ctx context.Context, limit int, after *job.PageCursor) ([]job.Job, error) {
	if limit < 1 || limit > 101 || (after != nil && !after.Valid()) {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := `SELECT ` + columns + ` FROM jobs WHERE status='DEAD_LETTER' ORDER BY created_at DESC,id DESC LIMIT $1`
	args := []any{limit}
	if after != nil {
		query = `SELECT ` + columns + ` FROM jobs WHERE status='DEAD_LETTER' AND (created_at,id)<($2,$3) ORDER BY created_at DESC,id DESC LIMIT $1`
		args = append(args, after.CreatedAt, after.ID)
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]job.Job, 0)
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *j)
	}
	return jobs, rows.Err()
}

func (r *JobRepository) Attempts(ctx context.Context, id uuid.UUID) ([]job.Attempt, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Max budget is 100, so returning full immutable history is bounded.
	if _, err := r.GetByID(ctx, id); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id,job_id,worker_id,attempt_number,status,started_at,finished_at,result,error FROM job_attempts WHERE job_id=$1 ORDER BY attempt_number LIMIT 101`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := make([]job.Attempt, 0)
	for rows.Next() {
		var a job.Attempt
		if err := rows.Scan(&a.ID, &a.JobID, &a.WorkerID, &a.AttemptNumber, &a.Status, &a.StartedAt, &a.FinishedAt, &a.Result, &a.Error); err != nil {
			return nil, err
		}
		a.StartedAt = a.StartedAt.UTC()
		if a.FinishedAt != nil {
			at := a.FinishedAt.UTC()
			a.FinishedAt = &at
		}
		// Only stable application codes leave the process; raw legacy error text is
		// redacted. Results are existing client-visible Job result data.
		if a.Error != nil && !(job.Failure{Class: job.Permanent, Code: *a.Error}).Valid() {
			redacted := "execution_error"
			a.Error = &redacted
		}
		if a.AttemptNumber != len(attempts)+1 || len(attempts) >= 100 || !a.Status.Valid() {
			return nil, job.ErrInvalidStoredData
		}
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}
