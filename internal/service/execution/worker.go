package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
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

// Store and Queue implementations must support concurrent calls. Sleep is
// stateless; slog is concurrency safe. Each claimed Job belongs to one slot.
type Worker struct {
	store       Store
	queue       Queue
	executor    Executor
	logger      *slog.Logger
	id          uuid.UUID
	concurrency int
	active      atomic.Int64
}

func New(store Store, queue Queue, executor Executor, logger *slog.Logger) *Worker {
	w, _ := NewWithConcurrency(store, queue, executor, logger, 1)
	return w
}
func NewWithConcurrency(store Store, queue Queue, executor Executor, logger *slog.Logger, concurrency int) (*Worker, error) {
	if concurrency < 1 || concurrency > 32 {
		return nil, errors.New("worker concurrency must be in 1..32")
	}
	id := uuid.New()
	return &Worker{store: store, queue: queue, executor: executor, logger: logger.With("worker_id", id, "configured_concurrency", concurrency), id: id, concurrency: concurrency}, nil
}

// Handle processes one delivery, for callers that own their own serial lifecycle.
// Run additionally limits held deliveries and supervises all concurrent slots.
func (w *Worker) Handle(ctx context.Context, msg *job.Delivery) (bool, error) {
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
	j, err = w.store.Claim(ctx, id, w.id)
	if errors.Is(err, job.ErrInvalidTransition) {
		return false, w.queue.Ack(ctx, msg.MessageID)
	}
	if err != nil {
		return false, err
	}
	active := w.active.Add(1)
	defer w.active.Add(-1)
	logger = logger.With("job_id", id, "attempt_number", j.AttemptCount)
	logger.Info("Job claimed", "event", "job_claimed", "active_jobs", active)
	// A Claim may have committed just as cancellation arrived. Execute observes
	// cancellation and persists its outcome; an ambiguous error is never retried
	// as business execution. A panic leaves the claimed delivery pending.
	out := w.executor.Execute(ctx, j)
	// Every finish is bounded even if shutdown arrives during Finalize. In Run,
	// cleanup is canceled at the single process deadline five seconds after drain.
	finishCtx, cancel := context.WithTimeout(cleanup, 5*time.Second)
	defer cancel()
	if err := w.store.Finalize(finishCtx, j, out.Status, out.Result); err != nil {
		return true, err
	}
	logger.Info("Job execution persisted", "event", "job_finished", "status", out.Status, "active_jobs", w.active.Load())
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

func (w *Worker) Run(ctx context.Context) error { return w.RunWithDispatcher(ctx, nil) }

// RunWithDispatcher supervises exactly C slots and at most one process
// dispatcher. The dispatcher must honor cancellation. Run is single-use.
// Run returns only after every goroutine is joined, so the composition root can
// close shared clients. Errors are sanitized; business FAILED outcomes continue.
func (w *Worker) RunWithDispatcher(ctx context.Context, dispatcher func(context.Context)) error {
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
		})
	}
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
	results := make(chan error, w.concurrency+1)
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
	w.logger.Info("Worker stopped", "event", "worker_stopped", "active_jobs", w.active.Load())
	return failure
}
