package execution

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeStore struct {
	j                           *job.Job
	getErr, claimErr, finishErr error
	claims, finishes            int
	liveCleanup                 bool
}

func (s *fakeStore) GetByID(context.Context, uuid.UUID) (*job.Job, error) { return s.j, s.getErr }
func (s *fakeStore) Claim(ctx context.Context, id, worker uuid.UUID) (*job.Job, error) {
	s.claims++
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	copy := *s.j
	copy.Status = job.Running
	copy.AssignedWorker = &worker
	return &copy, nil
}
func (s *fakeStore) Finalize(ctx context.Context, j *job.Job, status job.Status, result json.RawMessage) error {
	s.finishes++
	s.liveCleanup = ctx.Err() == nil
	return s.finishErr
}

type fakeQueue struct {
	msg                *job.Delivery
	receiveErr, ackErr error
	acks, reads        int
}

func (q *fakeQueue) Receive(ctx context.Context, c string) (*job.Delivery, error) {
	q.reads++
	return q.msg, q.receiveErr
}
func (q *fakeQueue) Ack(context.Context, string) error { q.acks++; return q.ackErr }

type fakeExecutor struct {
	count  int
	cancel context.CancelFunc
}

func (e *fakeExecutor) Execute(context.Context, *job.Job) Outcome {
	e.count++
	if e.cancel != nil {
		e.cancel()
	}
	return Outcome{job.Succeeded, json.RawMessage(`{}`)}
}
func TestWorkerBoundaries(t *testing.T) {
	for _, mode := range []string{"success", "terminal", "running", "missing", "malformed", "version", "claim_rejected", "get_error", "claim_error", "finish_error", "ack_error", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			id := uuid.New()
			s := &fakeStore{j: &job.Job{ID: id, Status: job.Queued}}
			q := &fakeQueue{}
			e := &fakeExecutor{}
			msg := &job.Delivery{MessageID: "1-0", Version: "1", JobID: id.String()}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "terminal":
				s.j.Status = job.Succeeded
			case "running":
				s.j.Status = job.Running
			case "missing":
				s.getErr = job.ErrNotFound
			case "malformed":
				msg.JobID = "bad"
			case "version":
				msg.Version = "2"
			case "claim_rejected":
				s.claimErr = job.ErrInvalidTransition
			case "get_error":
				s.getErr = errors.New("db")
			case "claim_error":
				s.claimErr = errors.New("db")
			case "finish_error":
				s.finishErr = errors.New("db")
			case "ack_error":
				q.ackErr = errors.New("redis")
			case "shutdown":
				e.cancel = cancel
			}
			_, err := New(s, q, e, slog.New(slog.NewTextHandler(io.Discard, nil))).Handle(ctx, msg)
			if mode == "get_error" || mode == "claim_error" || mode == "finish_error" {
				if err == nil || q.acks != 0 {
					t.Fatal("failure acknowledged", err, q.acks)
				}
				return
			}
			if mode == "ack_error" {
				if err == nil || s.finishes != 1 {
					t.Fatal(err)
				}
				return
			}
			if err != nil || q.acks != 1 {
				t.Fatal(err, q.acks)
			}
			if mode == "success" || mode == "shutdown" {
				if e.count != 1 || s.finishes != 1 || !s.liveCleanup {
					t.Fatal("execution/cleanup", s, e)
				}
			} else if e.count != 0 || s.finishes != 0 {
				t.Fatal("duplicate/poison executed")
			}
		})
	}
}
func TestWorkerRunBoundedFailureAndIdleCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := &fakeQueue{receiveErr: errors.New("unavailable")}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	if err := New(&fakeStore{}, q, &fakeExecutor{}, logger).Run(ctx); err != nil || q.reads != 1 {
		t.Fatal("tight retry", err, q.reads)
	}
}

func TestWorkerRunStopsAfterFinalizeFailure(t *testing.T) {
	id := uuid.New()
	s := &fakeStore{j: &job.Job{ID: id, Status: job.Queued}, finishErr: errors.New("raw driver secret")}
	q := &fakeQueue{msg: &job.Delivery{MessageID: "1-0", JobID: id.String(), Version: "1"}}
	e := &fakeExecutor{}
	err := New(s, q, e, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(t.Context())
	if err == nil || err.Error() != "execution persistence or acknowledgement failed" || q.acks != 0 || e.count != 1 || q.reads != 1 {
		t.Fatal("failed finalize was retried/executed/acked", err)
	}
}
