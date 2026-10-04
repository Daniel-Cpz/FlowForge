package execution

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
	"log/slog"
	"time"
)

type Store interface {
	GetByID(context.Context, uuid.UUID) (*job.Job, error)
	Claim(context.Context, uuid.UUID, uuid.UUID) (*job.Job, error)
	Finalize(context.Context, *job.Job, job.Status, json.RawMessage) error
}
type Queue interface {
	Receive(context.Context, string) (*job.Delivery, error)
	Ack(context.Context, string) error
}
type Worker struct {
	store    Store
	queue    Queue
	executor Executor
	logger   *slog.Logger
	id       uuid.UUID
}

func New(store Store, queue Queue, executor Executor, logger *slog.Logger) *Worker {
	return &Worker{store, queue, executor, logger, uuid.New()}
}

// Handle owns one delivered message until it is resolved. Infrastructure errors
// before claim are retried by Run; errors AFTER claim stop Run without ACK.
// Retrying a failed finalize via a RUNNING duplicate would silently hide failure.
func (w *Worker) Handle(ctx context.Context, msg *job.Delivery) (claimed bool, err error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	id, parseErr := uuid.Parse(msg.JobID)
	if msg.Malformed || msg.Version != "1" || parseErr != nil || id == uuid.Nil || id.String() != msg.JobID {
		w.logger.Warn("Invalid queue message discarded", "event", "invalid_delivery")
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	j, err := w.store.GetByID(ctx, id)
	if errors.Is(err, job.ErrNotFound) {
		w.logger.Warn("Queue message references missing job", "event", "missing_job")
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	if err != nil {
		return false, err
	}
	if j.Status != job.Queued {
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	j, err = w.store.Claim(ctx, id, w.id)
	if errors.Is(err, job.ErrInvalidTransition) {
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	if err != nil {
		return false, err
	}
	out := w.executor.Execute(ctx, j)
	// Shutdown interrupts SLEEP, then gives its FAILED result/ACK a bounded
	// independent cleanup window. If DB is unavailable leave RUNNING + pending.
	finishCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		finishCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	}
	defer cancel()
	if err := w.store.Finalize(finishCtx, j, out.Status, out.Result); err != nil {
		return true, err
	}
	w.logger.Info("Job execution persisted", "event", "job_finished", "job_id", id, "status", out.Status)
	return true, w.queue.Ack(finishCtx, msg.MessageID)
}

func wait(ctx context.Context) bool {
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (w *Worker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		msg, err := w.queue.Receive(ctx, w.id.String())
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger.Warn("Queue receive failed", "event", "receive_failed")
			if !wait(ctx) {
				return nil
			}
			continue
		}
		if msg == nil {
			continue
		}
		for {
			claimed, err := w.Handle(ctx, msg)
			if err == nil {
				break
			}
			if claimed {
				return errors.New("execution persistence or acknowledgement failed")
			}
			if ctx.Err() != nil {
				return nil
			}
			w.logger.Warn("Delivery failed before execution", "event", "delivery_failed")
			if !wait(ctx) {
				return nil
			}
		}
	}
	return nil
}
