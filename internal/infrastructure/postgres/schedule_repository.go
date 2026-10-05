package postgres

import (
	"context"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

const scheduleColumns = `id,status,type,payload,priority,max_attempts,timeout,required_capabilities,interval_seconds,next_run_at,created_at,updated_at`

var _ schedule.Repository = (*JobRepository)(nil)

func scanSchedule(row pgx.Row) (*schedule.Schedule, error) {
	var v schedule.Schedule
	err := row.Scan(&v.ID, &v.Status, &v.Type, &v.Payload, &v.Priority, &v.MaxAttempts, &v.Timeout, &v.RequiredCapabilities, &v.IntervalSeconds, &v.NextRunAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, job.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.NextRunAt = v.NextRunAt.UTC()
	v.CreatedAt = v.CreatedAt.UTC()
	v.UpdatedAt = v.UpdatedAt.UTC()
	if err := v.Validate(); err != nil {
		return nil, job.ErrInvalidStoredData
	}
	return &v, nil
}

func (r *JobRepository) CreateSchedule(ctx context.Context, v *schedule.Schedule) error {
	if err := v.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if v.RequiredCapabilities == nil {
		v.RequiredCapabilities = []string{}
	}
	var first *time.Time
	if !v.StartImmediately {
		first = &v.NextRunAt
	}
	stored, err := scanSchedule(r.pool.QueryRow(ctx, `INSERT INTO job_schedules (`+scheduleColumns+`) VALUES($1,'ACTIVE',$2,$3,$4,$5,$6,$7,$8,COALESCE($9::timestamptz,clock_timestamp()),clock_timestamp(),clock_timestamp()) RETURNING `+scheduleColumns, v.ID, v.Type, v.Payload, v.Priority, v.MaxAttempts, v.Timeout, v.RequiredCapabilities, v.IntervalSeconds, first))
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && (pgErr.SQLState() == "22P02" || pgErr.SQLState() == "22P05" || pgErr.SQLState() == "22003") {
			return job.ErrInvalidInput
		}
		return err
	}
	*v = *stored
	return nil
}
func (r *JobRepository) GetSchedule(ctx context.Context, id uuid.UUID) (*schedule.Schedule, error) {
	if id == uuid.Nil {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return scanSchedule(r.pool.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM job_schedules WHERE id=$1`, id))
}
func (r *JobRepository) CancelSchedule(ctx context.Context, id uuid.UUID) (*schedule.Schedule, error) {
	if id == uuid.Nil {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	v, err := scanSchedule(tx.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM job_schedules WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if v.Status == "ACTIVE" {
		v, err = scanSchedule(tx.QueryRow(ctx, `UPDATE job_schedules SET status='CANCELLED',updated_at=clock_timestamp() WHERE id=$1 AND status='ACTIVE' RETURNING `+scheduleColumns, id))
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// One bounded transaction commits Jobs, dispatch intents and schedule advancement.
// Row locks serialize cancellation; SKIP LOCKED permits independent schedulers.
func (r *JobRepository) MaterializeDue(ctx context.Context, limit int) ([]uuid.UUID, error) {
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
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+scheduleColumns+` FROM job_schedules WHERE status='ACTIVE' AND next_run_at<=$1 ORDER BY next_run_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	candidates := make([]*schedule.Schedule, 0, limit)
	for rows.Next() {
		v, err := scanSchedule(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(candidates))
	for _, v := range candidates {
		id := uuid.New()
		res, err := tx.Exec(ctx, `INSERT INTO jobs(id,type,status,payload,priority,max_attempts,submission_max_attempts,timeout,required_capabilities,scheduled_at,schedule_id,scheduled_for,created_at)
  VALUES($1,$2,'QUEUED',$3,$4,$5,$5,$6,$7,$8,$9,$8,clock_timestamp()) ON CONFLICT(schedule_id,scheduled_for) DO NOTHING`, id, v.Type, v.Payload, v.Priority, v.MaxAttempts, v.Timeout, v.RequiredCapabilities, v.NextRunAt, v.ID)
		if err != nil {
			return nil, err
		}
		if res.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx, `INSERT INTO job_dispatch(job_id) VALUES($1)`, id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		// Preserve the original phase of the interval; skip all middle missed runs.
		if _, err := tx.Exec(ctx, `UPDATE job_schedules SET next_run_at=next_run_at+make_interval(secs=>interval_seconds*(floor(extract(epoch FROM (clock_timestamp()-next_run_at))/interval_seconds)+1)),updated_at=clock_timestamp() WHERE id=$1 AND status='ACTIVE'`, v.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}
