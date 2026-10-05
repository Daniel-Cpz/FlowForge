package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/Daniel-Cpz/FlowForge/internal/retry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type JobRepository struct {
	telemetryPool *pgxpool.Pool
	observe       func(string, uuid.UUID, string)
	pool          *pgxpool.Pool
	leasePolicy   worker.LeasePolicy
	retryPolicy   retry.Policy
	jitter        retry.Jitter
}

func (r *JobRepository) WithTelemetryPool(pool *pgxpool.Pool) *JobRepository {
	r.telemetryPool = pool
	return r
}

// ObserveChanges is configured once by the composition root before concurrent use.
// The callback must be nonblocking; it never participates in the transaction.
func (r *JobRepository) ObserveChanges(fn func(string, uuid.UUID, string)) *JobRepository {
	r.observe = fn
	return r
}
func (r *JobRepository) changed(kind string, id uuid.UUID, status string) {
	if r.observe != nil {
		r.observe(kind, id, status)
	}
}

var _ job.Repository = (*JobRepository)(nil)

func NewJobRepository(pool *pgxpool.Pool) *JobRepository {
	return &JobRepository{pool: pool, leasePolicy: worker.DefaultLeasePolicy(), retryPolicy: retry.DefaultPolicy()}
}

func NewJobRepositoryWithPolicy(pool *pgxpool.Pool, policy worker.LeasePolicy) (*JobRepository, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return NewJobRepositoryWithPolicies(pool, policy, retry.DefaultPolicy(), nil)
}

func NewJobRepositoryWithPolicies(pool *pgxpool.Pool, lease worker.LeasePolicy, backoff retry.Policy, jitter retry.Jitter) (*JobRepository, error) {
	if err := lease.Validate(); err != nil {
		return nil, err
	}
	if err := backoff.Validate(); err != nil {
		return nil, err
	}
	return &JobRepository{pool: pool, leasePolicy: lease, retryPolicy: backoff, jitter: jitter}, nil
}

const columns = `id, type, status, priority, payload, result, attempt_count, max_attempts,
 timeout, idempotency_key, assigned_worker, lease_expiry, created_at, started_at, finished_at, retry_at, cancel_requested_at,scheduled_at,required_capabilities,schedule_id,scheduled_for,traceparent`

func (r *JobRepository) Create(ctx context.Context, j *job.Job) (job.CreateDisposition, error) {
	ctx, span := observability.Start(ctx, "job.create", attribute.String("job.id", j.ID.String()))
	defer span.End()
	j.TraceParent = observability.TraceParent(ctx)
	if err := j.Validate(); err != nil {
		return "", err
	}
	if j.RequiredCapabilities == nil {
		j.RequiredCapabilities = []string{}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin create: %w", err)
	}
	defer rollback(tx)
	stored, err := scanJob(tx.QueryRow(ctx, `INSERT INTO jobs (`+columns+`,submission_max_attempts) VALUES
	 ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$8)
 ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING RETURNING `+columns,
		j.ID, j.Type, j.Status, j.Priority, j.Payload, j.Result, j.AttemptCount, j.MaxAttempts,
		j.Timeout, j.IdempotencyKey, j.AssignedWorker, j.LeaseExpiry, j.CreatedAt, j.StartedAt, j.FinishedAt, j.RetryAt, j.CancelRequestedAt, j.ScheduledAt, j.RequiredCapabilities, j.ScheduleID, j.ScheduledFor, j.TraceParent))
	disposition := job.Created
	if errors.Is(err, job.ErrNotFound) && j.IdempotencyKey != nil {
		// A new READ COMMITTED statement sees the committed winning insert after
		// uniqueness arbitration. Compare JSONB inside PostgreSQL, not float64 JSON.
		stored, err = scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM jobs WHERE idempotency_key=$1 FOR SHARE`, *j.IdempotencyKey))
		if err == nil {
			var same bool
			err = tx.QueryRow(ctx, `SELECT type=$2 AND payload=$3::jsonb AND priority=$4 AND submission_max_attempts=$5 AND timeout=$6 AND scheduled_at IS NOT DISTINCT FROM $7::timestamptz AND required_capabilities=$8::text[] FROM jobs WHERE id=$1`, stored.ID, j.Type, j.Payload, j.Priority, j.MaxAttempts, j.Timeout, j.ScheduledAt, j.RequiredCapabilities).Scan(&same)
			if err == nil && !same {
				if m := observability.MetricsFrom(ctx); m != nil {
					m.Submitted.WithLabelValues("conflict").Inc()
				}
				return "", job.ErrIdempotencyConflict
			}
		}
		disposition = job.Replayed
	}
	if err != nil {
		// JSONB rejects Unicode NUL, unpaired surrogates and numeric overflow.
		// All non-JSON values have already passed domain bounds validation.
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && (pgErr.SQLState() == "22P02" || pgErr.SQLState() == "22P05" || pgErr.SQLState() == "22003") {
			return "", job.ErrInvalidInput
		}
		return "", fmt.Errorf("create job: %w", err)
	}
	if disposition == job.Created {
		if _, err := tx.Exec(ctx, `INSERT INTO job_dispatch(job_id) VALUES($1)`, j.ID); err != nil {
			return "", fmt.Errorf("create dispatch intent: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit create: %w", err)
	}
	*j = *stored
	span.SetAttributes(attribute.String("job.id", j.ID.String()), attribute.String("job.status", string(j.Status)), attribute.String("create.disposition", string(disposition)))
	if disposition == job.Created {
		r.changed("job.changed", j.ID, string(j.Status))
	}
	if m := observability.MetricsFrom(ctx); m != nil {
		m.Submitted.WithLabelValues(string(disposition)).Inc()
	}
	return disposition, nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func scanJob(row pgx.Row) (*job.Job, error) {
	var j job.Job
	err := row.Scan(&j.ID, &j.Type, &j.Status, &j.Priority, &j.Payload, &j.Result, &j.AttemptCount,
		&j.MaxAttempts, &j.Timeout, &j.IdempotencyKey, &j.AssignedWorker, &j.LeaseExpiry, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.RetryAt, &j.CancelRequestedAt, &j.ScheduledAt, &j.RequiredCapabilities, &j.ScheduleID, &j.ScheduledFor, &j.TraceParent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, job.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan job: %w", err)
	}
	j.CreatedAt = j.CreatedAt.UTC()
	for _, t := range []*time.Time{j.LeaseExpiry, j.RetryAt, j.StartedAt, j.FinishedAt, j.CancelRequestedAt, j.ScheduledAt, j.ScheduledFor} {
		if t != nil {
			*t = t.UTC()
		}
	}
	if err := j.Validate(); err != nil {
		return nil, job.ErrInvalidStoredData
	}
	return &j, nil
}

func (r *JobRepository) GetByID(ctx context.Context, id uuid.UUID) (*job.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return scanJob(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM jobs WHERE id=$1`, id))
}

func (r *JobRepository) List(ctx context.Context, limit int, after *job.PageCursor) ([]job.Job, error) {
	// Service requests up to 100 + one lookahead row. Bound direct callers too.
	if limit < 1 || limit > 101 || (after != nil && !after.Valid()) {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := `SELECT ` + columns + ` FROM jobs ORDER BY created_at DESC, id DESC LIMIT $1`
	args := []any{limit}
	if after != nil {
		query = `SELECT ` + columns + ` FROM jobs WHERE (created_at, id) < ($2, $3) ORDER BY created_at DESC, id DESC LIMIT $1`
		args = append(args, after.CreatedAt, after.ID)
	}
	rows, err := r.pool.Query(ctx, query, args...)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate jobs: %w", err)
	}
	return jobs, nil
}
