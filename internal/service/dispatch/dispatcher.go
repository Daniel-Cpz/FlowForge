package dispatch

import (
	"context"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"log/slog"
	"time"
)

type Store interface {
	PendingDispatch(context.Context, int) ([]uuid.UUID, error)
	MarkPublished(context.Context, uuid.UUID) error
}
type Publisher interface {
	Publish(context.Context, uuid.UUID) error
}
type Dispatcher struct {
	store     Store
	publisher Publisher
	logger    *slog.Logger
}

func New(store Store, publisher Publisher, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{store, publisher, logger}
}
func (d *Dispatcher) Once(ctx context.Context) error {
	ids, err := d.store.PendingDispatch(ctx, 100)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		publishCtx := ctx
		if reader, ok := d.store.(interface {
			GetByID(context.Context, uuid.UUID) (*job.Job, error)
		}); ok {
			if j, e := reader.GetByID(ctx, id); e == nil {
				publishCtx = observability.Restore(ctx, j.TraceParent)
			}
		}
		publishCtx, span := observability.Start(publishCtx, "dispatch.publish", attribute.String("job.id", id.String()))
		err := d.publisher.Publish(publishCtx, id)
		span.End()
		if m := observability.MetricsFrom(ctx); m != nil {
			result := "success"
			if err != nil {
				result = "error"
			}
			m.Dispatch.WithLabelValues(result).Inc()
		}
		if err != nil {
			return err
		}
		if err := d.store.MarkPublished(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Even on failure/full batches wait one second; no tight retry loop.
func (d *Dispatcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := d.Once(ctx); err != nil && ctx.Err() == nil {
			d.logger.Warn("Dispatch cycle failed; durable intent retained", "event", "dispatch_failed")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
