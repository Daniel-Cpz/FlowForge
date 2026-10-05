package postgres

import (
	"context"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"log/slog"
	"time"
)

// Telemetry reads use a separate, two-connection pool in the composition root.
// A read failure drops measurement, never changes the committed outcome.
func (r *JobRepository) recordAttempt(ctx context.Context, j *job.Job, defaultOutcome string) {
	m := observability.MetricsFrom(ctx)
	if m == nil {
		return
	}
	ctx, span := observability.Start(observability.Restore(ctx, j.TraceParent), "attempt.committed", attribute.String("job.id", j.ID.String()), attribute.Int("attempt.number", j.AttemptCount))
	defer span.End()
	// Retain the caller's cancellation/batch deadline. A recovery batch must not
	// accumulate a fresh one-second wait per Attempt after its three-second budget.
	read, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	pool := r.telemetryPool
	if pool == nil {
		pool = r.pool
	}
	var status string
	var code *string
	var duration float64
	var owner string
	err := pool.QueryRow(read, `SELECT status,error,extract(epoch FROM (finished_at-started_at)),worker_id::text FROM job_attempts WHERE job_id=$1 AND attempt_number=$2 AND finished_at IS NOT NULL`, j.ID, j.AttemptCount).Scan(&status, &code, &duration, &owner)
	if err != nil {
		return
	}
	outcome := defaultOutcome
	switch {
	case status == "SUCCEEDED":
		outcome = "succeeded"
	case status == "CANCELLED":
		outcome = "cancelled"
	case status == "TIMED_OUT":
		outcome = "timed_out"
	case code != nil && *code == "lease_expired":
		outcome = "lease_expired"
	case j.Status == job.Retrying:
		outcome = "retryable_failed"
	}
	// Retryable exhaustion is also retryable_failed; the attempt error encodes its
	// cause, independent from the Job's remaining budget.
	if status == "FAILED" && defaultOutcome == "retryable_failed" {
		outcome = "retryable_failed"
	}
	m.Attempt(outcome, duration, j.Status == job.Retrying)
	span.SetAttributes(attribute.String("attempt.outcome", outcome), attribute.String("job.status", string(j.Status)))
	span.SetAttributes(attribute.String("worker.id", owner))
	observability.Correlated(ctx, slog.Default()).Info("Committed Attempt measured", "event", "attempt_committed", "job_id", j.ID, "worker_id", owner, "attempt_number", j.AttemptCount, "outcome", outcome)
}

func (r *JobRepository) observed(ctx context.Context, j *job.Job, name string) {
	ctx, span := observability.Start(observability.Restore(ctx, j.TraceParent), name, attribute.String("job.id", j.ID.String()), attribute.Int("attempt.number", j.AttemptCount), attribute.String("job.status", string(j.Status)))
	defer span.End()
	observability.Correlated(ctx, slog.Default()).Info("Durable Job change observed", "event", name, "job_id", j.ID, "attempt_number", j.AttemptCount, "status", j.Status)
}
