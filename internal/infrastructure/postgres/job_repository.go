package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type JobRepository struct{ pool *pgxpool.Pool }

var _ job.Repository = (*JobRepository)(nil)

func NewJobRepository(pool *pgxpool.Pool) *JobRepository { return &JobRepository{pool: pool} }

const columns = `id, type, status, priority, payload, result, attempt_count, max_attempts,
 timeout, idempotency_key, assigned_worker, lease_expiry, created_at, started_at, finished_at`

func (r *JobRepository) Create(ctx context.Context, j *job.Job) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.pool.Exec(ctx, `INSERT INTO jobs (`+columns+`) VALUES
	 ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		j.ID, j.Type, j.Status, j.Priority, j.Payload, j.Result, j.AttemptCount, j.MaxAttempts,
		j.Timeout, j.IdempotencyKey, j.AssignedWorker, j.LeaseExpiry, j.CreatedAt, j.StartedAt, j.FinishedAt)
	if err != nil {
		// PostgreSQL JSONB cannot represent Unicode NUL or unpaired surrogates.
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && (pgErr.SQLState() == "22P02" || pgErr.SQLState() == "22P05") {
			return job.ErrInvalidInput
		}
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

func scanJob(row pgx.Row) (*job.Job, error) {
	var j job.Job
	err := row.Scan(&j.ID, &j.Type, &j.Status, &j.Priority, &j.Payload, &j.Result, &j.AttemptCount,
		&j.MaxAttempts, &j.Timeout, &j.IdempotencyKey, &j.AssignedWorker, &j.LeaseExpiry, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, job.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan job: %w", err)
	}
	j.CreatedAt = j.CreatedAt.UTC()
	for _, t := range []*time.Time{j.LeaseExpiry, j.StartedAt, j.FinishedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	return &j, nil
}

func (r *JobRepository) GetByID(ctx context.Context, id uuid.UUID) (*job.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return scanJob(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM jobs WHERE id=$1`, id))
}

func (r *JobRepository) List(ctx context.Context, limit, offset int) ([]job.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, `SELECT `+columns+` FROM jobs ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
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
