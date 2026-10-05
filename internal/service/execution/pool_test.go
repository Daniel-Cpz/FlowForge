package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
)

type poolStore struct {
	mu               sync.Mutex
	jobs             map[uuid.UUID]*job.Job
	claims, finishes int
	failJob          uuid.UUID
	finishStarted    chan uuid.UUID
	blockFinish      bool
}

func (s *poolStore) GetByID(_ context.Context, id uuid.UUID) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[id]; j != nil {
		copy := *j
		return &copy, nil
	}
	return nil, job.ErrNotFound
}
func (s *poolStore) Claim(_ context.Context, id, worker uuid.UUID) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	if j.Status != job.Queued {
		return nil, job.ErrInvalidTransition
	}
	s.claims++
	j.Status = job.Running
	j.AssignedWorker = &worker
	j.AttemptCount++
	copy := *j
	return &copy, nil
}
func (s *poolStore) Finalize(ctx context.Context, j *job.Job, status job.Status, result json.RawMessage) error {
	if s.finishStarted != nil {
		select {
		case s.finishStarted <- j.ID:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.blockFinish {
		<-ctx.Done()
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.ID == s.failJob {
		return errors.New("driver secret")
	}
	stored := s.jobs[j.ID]
	if stored.Status != job.Running || *stored.AssignedWorker != *j.AssignedWorker || stored.AttemptCount != j.AttemptCount {
		return job.ErrInvalidTransition
	}
	stored.Status = status
	stored.Result = result
	s.finishes++
	return nil
}

type poolQueue struct {
	messages     chan *job.Delivery
	mu           sync.Mutex
	reads        int
	acks         map[string]bool
	deliveryJobs map[string]uuid.UUID
	consumers    map[string]bool
	ackErr       bool
	received     chan struct{}
}

func (q *poolQueue) Receive(ctx context.Context, consumer string) (*job.Delivery, error) {
	q.mu.Lock()
	q.reads++
	q.consumers[consumer] = true
	q.mu.Unlock()
	if q.received != nil {
		select {
		case q.received <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	select {
	case msg := <-q.messages:
		q.mu.Lock()
		q.deliveryJobs[msg.MessageID] = uuid.MustParse(msg.JobID)
		q.mu.Unlock()
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (q *poolQueue) Ack(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ackErr {
		return errors.New("redis secret")
	}
	q.acks[id] = true
	return nil
}

type barrierExecutor struct {
	entered            chan uuid.UUID
	release            chan struct{}
	mu                 sync.Mutex
	active, max, calls int
	panicJob           uuid.UUID
}

func (e *barrierExecutor) Execute(ctx context.Context, j *job.Job) Outcome {
	e.mu.Lock()
	e.calls++
	e.active++
	if e.active > e.max {
		e.max = e.active
	}
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.active--; e.mu.Unlock() }()
	select {
	case e.entered <- j.ID:
	case <-ctx.Done():
		return Outcome{job.Failed, json.RawMessage(`{"error":"execution_cancelled"}`)}
	}
	if j.ID == e.panicJob {
		panic("payload secret")
	}
	select {
	case <-e.release:
		return Outcome{job.Succeeded, json.RawMessage(`{}`)}
	case <-ctx.Done():
		return Outcome{job.Failed, json.RawMessage(`{"error":"execution_cancelled"}`)}
	}
}
func poolFixture(count int) (*poolStore, *poolQueue, *barrierExecutor) {
	s := &poolStore{jobs: map[uuid.UUID]*job.Job{}}
	q := &poolQueue{messages: make(chan *job.Delivery, count*2), acks: map[string]bool{}, deliveryJobs: map[string]uuid.UUID{}, consumers: map[string]bool{}}
	e := &barrierExecutor{entered: make(chan uuid.UUID, count), release: make(chan struct{}, count)}
	for n := 0; n < count; n++ {
		id := uuid.New()
		s.jobs[id] = &job.Job{ID: id, Status: job.Queued}
		q.messages <- &job.Delivery{MessageID: fmt.Sprint(n), JobID: id.String(), Version: "1"}
	}
	return s, q, e
}
func boundedTest(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), 10*time.Second)
}
func take[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case v := <-ch:
		return v
	case <-ctx.Done():
		t.Fatal("barrier timed out")
		var zero T
		return zero
	}
}
func poolWorker(t *testing.T, s Store, q Queue, e Executor, c int) *Worker {
	t.Helper()
	w, err := NewWithConcurrency(s, q, e, slog.New(slog.NewTextHandler(io.Discard, nil)), c)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestPoolBoundedSaturationAndRelease(t *testing.T) {
	for _, c := range []int{1, 2, 4} {
		t.Run(fmt.Sprint(c), func(t *testing.T) {
			ctx, cancel := boundedTest(t)
			defer cancel()
			s, q, e := poolFixture(c + 1)
			w := poolWorker(t, s, q, e, c)
			done := make(chan error, 1)
			go func() { done <- w.Run(ctx) }()
			for range c {
				take(t, ctx, e.entered)
			}
			// All slots are inside the executor barrier: no further Receive/Claim can run.
			q.mu.Lock()
			reads := q.reads
			consumers := len(q.consumers)
			q.mu.Unlock()
			s.mu.Lock()
			claims := s.claims
			s.mu.Unlock()
			if reads != c || claims != c || consumers != c || w.active.Load() != int64(c) {
				t.Fatal("unbounded prefetch", reads, claims, consumers, w.active.Load())
			}
			e.release <- struct{}{}
			take(t, ctx, e.entered)
			cancel()
			if err := take(t, t.Context(), done); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			maximum, calls, active := e.max, e.calls, e.active
			e.mu.Unlock()
			if maximum != c || calls != c+1 || active != 0 || w.active.Load() != 0 {
				t.Fatal(maximum, calls, active)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, j := range s.jobs {
				if j.Status != job.Succeeded && j.Status != job.Failed {
					t.Fatal("not drained", j.Status)
				}
				if j.AttemptCount != 1 || *j.AssignedWorker != w.id {
					t.Fatal("owner/attempt")
				}
			}
		})
	}
}
func TestTwoPoolsDuplicateJobOnlyOneExecution(t *testing.T) {
	ctx, cancel := boundedTest(t)
	defer cancel()
	s, q, e := poolFixture(1)
	original := <-q.messages
	q.messages = make(chan *job.Delivery, 8)
	q.messages <- original
	for n := 1; n < 8; n++ {
		for id := range s.jobs {
			q.messages <- &job.Delivery{MessageID: fmt.Sprint(n), JobID: id.String(), Version: "1"}
		}
	}
	w1, w2 := poolWorker(t, s, q, e, 2), poolWorker(t, s, q, e, 2)
	done := make(chan error, 2)
	go func() { done <- w1.Run(ctx) }()
	go func() { done <- w2.Run(ctx) }()
	take(t, ctx, e.entered)
	// Original execution stays gated; duplicates may only ACK their own delivery.

	cancel()
	for range 2 {
		if err := take(t, t.Context(), done); err != nil {
			t.Fatal(err)
		}
	}
	e.mu.Lock()
	calls := e.calls
	e.mu.Unlock()
	s.mu.Lock()
	claims := s.claims
	j := s.jobs[uuid.MustParse(original.JobID)]
	s.mu.Unlock()
	if calls != 1 || claims != 1 || j.AttemptCount != 1 || (*j.AssignedWorker != w1.id && *j.AssignedWorker != w2.id) {
		t.Fatal("duplicate execution", calls, claims, j)
	}
}

func TestPoolFatalCancelsPeersAndJoinsDispatcher(t *testing.T) {
	for _, mode := range []string{"finalize", "ack", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := boundedTest(t)
			defer cancel()
			s, q, e := poolFixture(2)
			w := poolWorker(t, s, q, e, 2)
			var logs bytes.Buffer
			w.logger = slog.New(slog.NewJSONHandler(&logs, nil)).With("worker_id", w.id, "configured_concurrency", 2)
			dispatcherDone := make(chan struct{})
			done := make(chan error, 1)
			if mode == "ack" {
				q.ackErr = true
			}
			// Panic identity is immutable before any goroutine starts.
			if mode == "panic" {
				for id := range s.jobs {
					e.panicJob = id
					break
				}
			}
			go func() {
				done <- w.RunWithDispatcher(ctx, func(ctx context.Context) { defer close(dispatcherDone); <-ctx.Done() })
			}()
			first := take(t, ctx, e.entered)
			if mode != "panic" {
				take(t, ctx, e.entered)
				if mode == "finalize" {
					s.mu.Lock()
					s.failJob = first
					s.mu.Unlock()
				}
				e.release <- struct{}{}
				e.release <- struct{}{}
			}
			err := take(t, ctx, done)
			if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(logs.String(), "secret") {
				t.Fatal("unsafe fatal", err)
			}
			select {
			case <-dispatcherDone:
			default:
				t.Fatal("dispatcher not joined")
			}
			if w.active.Load() != 0 {
				t.Fatal("peers still running")
			}
			q.mu.Lock()
			defer q.mu.Unlock()
			if mode == "ack" && len(q.acks) != 0 {
				t.Fatal("failed ACK reported successful", q.acks)
			}
			if mode == "finalize" {
				for messageID, acked := range q.acks {
					if acked && q.deliveryJobs[messageID] == s.failJob {
						t.Fatal("failed finalize ACKed")
					}
				}
				for id, j := range s.jobs {
					if id == s.failJob && j.Status != job.Running {
						t.Fatal("failed finalize changed state")
					}
				}
			}
			if mode == "ack" {
				for _, j := range s.jobs {
					if j.Status != job.Succeeded && j.Status != job.Failed {
						t.Fatal("ACK failure undid terminal persistence")
					}
					if j.AttemptCount != 1 {
						t.Fatal("ACK failure reexecuted")
					}
				}
			}
		})
	}
}
func TestPoolSharedCleanupDeadline(t *testing.T) {
	ctx, cancel := boundedTest(t)
	defer cancel()
	s, q, e := poolFixture(4)
	s.blockFinish = true
	s.finishStarted = make(chan uuid.UUID, 4)
	done := make(chan error, 1)
	go func() { done <- poolWorker(t, s, q, e, 4).Run(ctx) }()
	for range 4 {
		take(t, ctx, e.entered)
	}
	cancel()
	// All cleanups must start concurrently, not after previous cleanup timeout.
	finishCtx, finishCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer finishCancel()
	for range 4 {
		take(t, finishCtx, s.finishStarted)
	}
	joinCtx, joinCancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer joinCancel()
	if err := take(t, joinCtx, done); err == nil {
		t.Fatal("failed persistence reported successful")
	}
}
func TestPoolIdleAndCanceledReceive(t *testing.T) {
	ctx, cancel := boundedTest(t)
	defer cancel()
	s, q, e := poolFixture(0)
	q.received = make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() { done <- poolWorker(t, s, q, e, 4).Run(ctx) }()
	for range 4 {
		take(t, ctx, q.received)
	}
	cancel()
	if err := take(t, t.Context(), done); err != nil {
		t.Fatal(err)
	}
	if w, err := NewWithConcurrency(s, q, e, slog.Default(), 0); err == nil || w != nil {
		t.Fatal("constructor accepted invalid bound")
	}
}
