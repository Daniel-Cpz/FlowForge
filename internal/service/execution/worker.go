package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/capability"
	"github.com/Daniel-Cpz/FlowForge/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	domainworker "github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/google/uuid"
)

type Store interface {
	GetByID(context.Context, uuid.UUID) (*job.Job, error)
	Claim(context.Context, uuid.UUID, uuid.UUID) (*job.Job, error)
	Finalize(context.Context, *job.Job, job.Status, json.RawMessage, ...job.Failure) error
}
type Queue interface {
	Receive(context.Context, string) (*job.Delivery, error)
	Ack(context.Context, string) error
}

// Store and Queue implementations must support concurrent calls. Sleep is
// stateless; slog is concurrency safe. Each claimed Job belongs to one slot.
type Worker struct {
	capabilities []string
	store        Store
	queue        Queue
	executor     Executor
	logger       *slog.Logger
	id           uuid.UUID
	concurrency  int
	active       atomic.Int64
	leases       LeaseStore
	policy       domainworker.LeasePolicy
	registration sync.Mutex
	registered   bool
}

func New(store Store, queue Queue, executor Executor, logger *slog.Logger) *Worker {
	w, _ := NewWithConcurrency(store, queue, executor, logger, 1)
	return w
}
func NewWithConcurrency(store Store, queue Queue, executor Executor, logger *slog.Logger, concurrency int) (*Worker, error) {
	return NewWithLeasePolicy(store, queue, executor, logger, concurrency, domainworker.DefaultLeasePolicy())
}
func NewWithLeasePolicy(store Store, queue Queue, executor Executor, logger *slog.Logger, concurrency int, policy domainworker.LeasePolicy, values ...string) (*Worker, error) {
	capabilities, err := capability.Normalize(values)
	if err != nil {
		return nil, err
	}
	if concurrency < 1 || concurrency > 32 {
		return nil, errors.New("worker concurrency must be in 1..32")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	id := uuid.New()
	w := &Worker{store: store, queue: queue, executor: executor, logger: logger.With("worker_id", id, "configured_concurrency", concurrency), id: id, concurrency: concurrency, policy: policy}
	w.leases, _ = store.(LeaseStore)
	w.capabilities = capabilities
	return w, nil
}

// Handle processes one delivery for serial diagnostic/test callers. It registers
// and renews execution, but does not start process heartbeat/recovery loops.
// Production callers use Run, which owns liveness and all concurrent slots.
func (w *Worker) Handle(ctx context.Context, msg *job.Delivery) (bool, error) {
	if err := w.ensureRegistered(ctx); err != nil {
		return false, err
	}
	return w.handle(ctx, context.WithoutCancel(ctx), msg, w.logger)
}
func (w *Worker) handle(ctx, cleanup context.Context, msg *job.Delivery, logger *slog.Logger) (claimed bool, err error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	id, parseErr := uuid.Parse(msg.JobID)
	if msg.Malformed || msg.Version != "1" || parseErr != nil || id == uuid.Nil || id.String() != msg.JobID {
		logger.Warn("Invalid queue message discarded", "event", "invalid_delivery")
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	j, err := w.store.GetByID(ctx, id)
	if errors.Is(err, job.ErrNotFound) {
		logger.Warn("Queue message references missing job", "event", "missing_job")
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	if err != nil {
		return false, err
	}
	if j.Status != job.Queued {
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	ctx, attemptSpan := observability.Start(observability.Restore(ctx, j.TraceParent), "queue.receive", attribute.String("job.id", id.String()), attribute.String("worker.id", w.id.String()))
	defer attemptSpan.End()
	j, err = w.store.Claim(ctx, id, w.id)
	// Give concurrent higher-ranked claims a short bounded opportunity to
	// commit before deferring this delivery to normal reconciliation.
	for n := 0; n < 3 && errors.Is(err, job.ErrPriorityDeferred); n++ {
		if !wait(ctx) {
			return false, ctx.Err()
		}
		j, err = w.store.Claim(ctx, id, w.id)
	}
	if errors.Is(err, job.ErrPriorityDeferred) {
		logger.Info("Delivery deferred by priority", "event", "priority_deferred", "job_id", id)
		// ACK only the notification; QUEUED intent remains and is reconciled at
		// the existing bounded 30s cadence, without a tight redispatch loop.
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	if errors.Is(err, job.ErrInvalidTransition) {
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	if err != nil {
		return false, err
	}
	active := w.active.Add(1)
	defer func() { n := w.active.Add(-1); observability.MetricsFrom(ctx).Worker(n, int64(w.concurrency)) }()
	observability.MetricsFrom(ctx).Worker(active, int64(w.concurrency))
	logger = observability.Correlated(ctx, logger).With("job_id", id, "attempt_number", j.AttemptCount)
	attemptSpan.SetAttributes(attribute.Int("attempt.number", j.AttemptCount))
	logger.Info("Job claimed", "event", "job_claimed", "active_jobs", active)
	if w.leases != nil {
		logger.Info("Execution lease acquired", "event", "lease_acquired", "lease_expiry", j.LeaseExpiry)
	}
	// A Claim may have committed just as cancellation arrived. Execute observes
	// cancellation and persists its outcome; an ambiguous error is never retried
	// as business execution. A panic leaves the claimed delivery pending.
	out, err := w.execute(ctx, j, logger)
	if err != nil {
		return true, err
	}
	// Every finish is bounded even if shutdown arrives during Finalize. In Run,
	// cleanup is canceled at the single process deadline five seconds after drain.
	finishCtx, cancel := context.WithTimeout(observability.WithMetrics(observability.Restore(cleanup, j.TraceParent), observability.MetricsFrom(ctx)), 5*time.Second)
	defer cancel()
	var failure []job.Failure
	if out.Status == job.Failed || out.Status == job.TimedOut {
		failure = append(failure, out.Failure)
	}
	if err := w.store.Finalize(finishCtx, j, out.Status, out.Result, failure...); err != nil {
		if errors.Is(err, job.ErrLeaseLost) {
			logger.Warn("Stale execution rejected", "event", "stale_finalize_rejected")
		}
		return true, err
	}
	logger.Info("Job execution persisted", "event", "job_finished", "status", j.Status, "retry_at", j.RetryAt, "failure_class", out.Failure.Class, "active_jobs", w.active.Load())
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
func (w *Worker) slot(ctx, cleanup context.Context, slot int) (err error) {
	// Never include the recovered value or stack, which may contain job secrets.
	defer func() {
		if recover() != nil {
			err = errors.New("worker slot panicked")
		}
	}()
	consumer := fmt.Sprintf("%s:%d", w.id, slot)
	logger := w.logger.With("slot", slot, "consumer", consumer)
	logger.Info("Execution slot started", "event", "slot_started")
	defer logger.Info("Execution slot stopped", "event", "slot_stopped")
	for ctx.Err() == nil {
		msg, err := w.queue.Receive(ctx, consumer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Warn("Queue receive failed", "event", "receive_failed")
			if !wait(ctx) {
				return nil
			}
			continue
		}
		if msg == nil {
			continue
		}
		for {
			claimed, err := w.handle(ctx, cleanup, msg, logger)
			if err == nil {
				break
			}
			if claimed {
				return errors.New("execution persistence or acknowledgement failed")
			}
			if ctx.Err() != nil {
				return nil
			}
			logger.Warn("Delivery failed before execution", "event", "delivery_failed")
			if !wait(ctx) {
				return nil
			}
		}
	}
	return nil
}

func (w *Worker) ActiveJobs() int64 { return w.active.Load() }

func (w *Worker) Run(ctx context.Context) error { return w.RunWithDispatcher(ctx, nil) }

// RunWithDispatcher supervises exactly C slots and at most one process
// dispatcher. The dispatcher must honor cancellation. Run is single-use.
// Run returns only after every goroutine is joined, so the composition root can
// close shared clients. Errors are sanitized; business FAILED outcomes continue.
func (w *Worker) RunWithDispatcher(ctx context.Context, dispatcher func(context.Context)) error {
	if ctx.Err() != nil {
		return nil
	}
	if err := w.ensureRegistered(ctx); err != nil {
		return errors.New("worker registration failed")
	}
	workCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	cleanup, endCleanup := context.WithCancel(context.WithoutCancel(ctx))
	defer endCleanup()
	var once sync.Once
	var deadline *time.Timer
	drain := func() {
		once.Do(func() {
			w.logger.Info("Worker draining", "event", "worker_draining", "active_jobs", w.active.Load())
			deadline = time.AfterFunc(5*time.Second, endCleanup)
			stop()
			// Cancel every execution before attempting a registry write, so an
			// unavailable database cannot delay heartbeat fail-fast cancellation.
			if w.leases != nil {
				if err := w.leases.MarkDraining(cleanup, w.id); err != nil {
					w.logger.Warn("Draining registration failed", "event", "worker_draining_failed")
				}
			}
		})
	}
	observability.MetricsFrom(ctx).Worker(0, int64(w.concurrency))
	w.logger.Info("Worker started", "event", "worker_started")
	if ctx.Err() != nil {
		drain()
	}
	finished := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			drain()
		case <-finished:
		}
	}()
	// Buffer covers every possible producer, including a dispatcher panic.
	results := make(chan error, w.concurrency+3)
	var wg sync.WaitGroup
	launch := func(run func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := func() (err error) {
				defer func() {
					if recover() != nil {
						err = errors.New("worker dispatcher panicked")
					}
				}()
				return run()
			}()
			if err != nil {
				w.logger.Error("Worker pool failed", "event", "pool_failed")
				drain()
			}
			results <- err
		}()
	}
	if w.leases != nil {
		launch(func() error { return w.heartbeat(workCtx) })
		launch(func() error { return w.recovery(workCtx) })
	}
	if dispatcher != nil {
		launch(func() error {
			defer w.logger.Info("Dispatcher stopped", "event", "dispatcher_stopped")
			dispatcher(workCtx)
			return nil
		})
	}
	for slot := 0; slot < w.concurrency; slot++ {
		launch(func() error { return w.slot(workCtx, cleanup, slot) })
	}
	wg.Wait()
	close(finished)
	<-watchDone
	if deadline != nil {
		deadline.Stop()
	}
	close(results)
	var failure error
	for err := range results {
		if err != nil && failure == nil {
			failure = err
		}
	}
	if w.leases != nil {
		reason := "graceful_shutdown"
		if failure != nil {
			reason = "fatal_error"
		}
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		if err := w.leases.StopWorker(stopCtx, w.id, reason); err != nil {
			w.logger.Warn("Worker stop registration failed", "event", "worker_stop_failed")
			if failure == nil {
				failure = errors.New("worker stop registration failed")
			}
		} else {
			w.logger.Info("Worker offline", "event", "worker_offline", "reason", reason)
		}
		cancel()
	}
	w.logger.Info("Worker stopped", "event", "worker_stopped", "active_jobs", w.active.Load())
	return failure
}
