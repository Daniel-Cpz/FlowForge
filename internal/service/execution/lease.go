package execution

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
)

// LeaseStore separates execution authority from notification transport. All
// operations are bounded; implementations use database time for validity.
type LeaseStore interface {
	RegisterWorker(context.Context, uuid.UUID, int) error
	Heartbeat(context.Context, uuid.UUID, int) error
	MarkDraining(context.Context, uuid.UUID) error
	StopWorker(context.Context, uuid.UUID, string) error
	Renew(context.Context, *job.Job) (time.Time, error)
	DetectOffline(context.Context, int) ([]uuid.UUID, error)
	RecoverExpired(context.Context, int) ([]job.RecoveredAttempt, error)
	PromoteRetries(context.Context, int) ([]uuid.UUID, error)
}

func (w *Worker) ensureRegistered(ctx context.Context) error {
	if w.leases == nil {
		return nil
	}
	w.registration.Lock()
	defer w.registration.Unlock()
	if w.registered {
		return nil
	}
	var err error
	if registry, ok := w.leases.(interface {
		RegisterWorkerCapabilities(context.Context, uuid.UUID, int, []string) error
	}); ok {
		err = registry.RegisterWorkerCapabilities(ctx, w.id, w.concurrency, w.capabilities)
	} else if len(w.capabilities) > 0 {
		return job.ErrInvalidInput
	} else {
		err = w.leases.RegisterWorker(ctx, w.id, w.concurrency)
	}
	if err != nil {
		return err
	}
	w.registered = true
	w.logger.Info("Worker registered", "event", "worker_registered", "capabilities", w.capabilities)
	return nil
}

func (w *Worker) heartbeat(ctx context.Context) error {
	defer w.logger.Info("Heartbeat stopped", "event", "heartbeat_stopped")
	ticker := time.NewTicker(w.policy.HeartbeatInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		active := int(w.active.Load())
		if err := w.leases.Heartbeat(ctx, w.id, active); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger.Error("Heartbeat failed", "event", "heartbeat_failed")
			return errors.New("worker heartbeat failed")
		}
		w.logger.Info("Heartbeat persisted", "event", "heartbeat", "active_jobs", active)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

func (w *Worker) recovery(ctx context.Context) error {
	defer w.logger.Info("Recovery stopped", "event", "recovery_stopped")
	ticker := time.NewTicker(w.policy.RecoveryInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		recovered, err := w.leases.RecoverExpired(ctx, 100)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger.Warn("Recovery scan failed", "event", "recovery_failed")
		} else {
			for _, r := range recovered {
				logger := w.logger.With("job_id", r.JobID, "expired_worker_id", r.WorkerID, "attempt_number", r.AttemptNumber)
				logger.Warn("Execution lease expired", "event", "lease_expired")
				logger.Info("Expired attempt settled", "event", "recovery_settled", "status", r.Status, "retry_at", r.RetryAt)
			}
		}
		if scheduler, ok := w.store.(interface {
			MaterializeDue(context.Context, int) ([]uuid.UUID, error)
		}); ok {
			ids, scanErr := scheduler.MaterializeDue(ctx, 100)
			if scanErr != nil && ctx.Err() == nil {
				w.logger.Warn("Schedule materialization failed", "event", "schedule_scan_failed")
			} else if len(ids) > 0 {
				w.logger.Info("Schedule occurrences created", "event", "schedule_materialized", "count", len(ids))
			}
		}
		promoted, err := w.leases.PromoteRetries(ctx, 100)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger.Warn("Retry promotion failed", "event", "retry_promotion_failed")
		} else {
			for _, id := range promoted {
				w.logger.Info("Due retry queued", "event", "retry_queued", "job_id", id)
			}
		}
		offline, err := w.leases.DetectOffline(ctx, 100)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger.Warn("Offline scan failed", "event", "offline_scan_failed")
		} else {
			for _, id := range offline {
				w.logger.Info("Worker heartbeat expired", "event", "worker_offline", "offline_worker_id", id, "reason", "heartbeat_expired")
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

// A failed/ambiguous renewal cancels local execution and retains the delivery.
// The deferred join also runs if an Executor panics. The claimed Job is never
// mutated by the renew goroutine; PostgreSQL owns its current expiry.
func (w *Worker) execute(ctx context.Context, j *job.Job, logger *slog.Logger) (out Outcome, err error) {
	// The deadline starts immediately before Execute, never while queued.
	timed, endTimeout := context.WithTimeout(ctx, time.Duration(j.Timeout)*time.Second)
	defer endTimeout()
	executionCtx, cancel := context.WithCancelCause(timed)
	done := make(chan error, 1)
	if w.leases == nil {
		done <- nil
	} else {
		go func() {
			var failure error
			defer func() {
				if recover() != nil {
					failure = errors.New("lease renewal panicked")
				}
				if failure != nil {
					cancel(failure)
				}
				done <- failure
			}()
			ticker := time.NewTicker(w.policy.RenewInterval)
			defer ticker.Stop()
			for {
				select {
				case <-executionCtx.Done():
					return
				case <-ticker.C:
					expiry, renewalErr := w.leases.Renew(executionCtx, j)
					if errors.Is(renewalErr, job.ErrCancellationRequested) {
						cancel(job.ErrCancellationRequested)
						logger.Info("User cancellation observed", "event", "cancellation_observed")
						return
					}
					if renewalErr != nil {
						if executionCtx.Err() != nil {
							return
						}
						logger.Error("Lease renewal failed", "event", "lease_renew_failed")
						failure = errors.New("execution lease renewal failed")
						return
					}
					logger.Info("Execution lease renewed", "event", "lease_renewed", "lease_expiry", expiry)
				}
			}
		}()
	}
	defer func() {
		cancel(context.Canceled)
		if renewalErr := <-done; renewalErr != nil {
			err = renewalErr
		}
	}()
	out = w.executor.Execute(executionCtx, j)
	switch cause := context.Cause(executionCtx); {
	case errors.Is(cause, job.ErrCancellationRequested):
		out = Outcome{Status: job.Cancelled, Result: []byte(`{"error":"user_cancelled"}`)}
	case errors.Is(cause, context.DeadlineExceeded) && ctx.Err() == nil:
		out = Outcome{Status: job.TimedOut, Result: []byte(`{"error":"execution_timeout"}`), Failure: job.Failure{Class: job.Retryable, Code: "execution_timeout"}}
	}
	return out, nil
}
