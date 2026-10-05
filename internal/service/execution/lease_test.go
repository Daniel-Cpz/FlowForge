package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleStore struct {
	*poolStore
	heartbeats, renewals, recoveries, registered, stopped atomic.Int64
	heartbeatFailed, renewFailed, recoveryFailed          atomic.Bool
	renewPanic                                            bool
	renewed                                               chan struct{}
	reason                                                string // read only after Run joins
}

func TestStaleFinalizeLogsRejectionWithoutACK(t *testing.T) {
	id := uuid.New()
	s := &fakeStore{j: &job.Job{ID: id, Status: job.Queued}, finishErr: job.ErrLeaseLost}
	q := &fakeQueue{}
	var logs bytes.Buffer
	w := New(s, q, &fakeExecutor{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	claimed, err := w.Handle(t.Context(), &job.Delivery{JobID: id.String(), Version: "1", MessageID: "1-0"})
	if !claimed || !errors.Is(err, job.ErrLeaseLost) || q.acks != 0 {
		t.Fatal("stale finalization ACKed", err)
	}
	var event map[string]any
	found := false
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event["event"] == "stale_finalize_rejected" {
			found = true
			if event["job_id"] != id.String() || event["worker_id"] == nil || event["attempt_number"] == nil {
				t.Fatal("missing fencing trace")
			}
		}
	}
	if !found {
		t.Fatal("missing stale rejection log")
	}
}

func (s *lifecycleStore) RegisterWorker(context.Context, uuid.UUID, int) error {
	s.registered.Add(1)
	return nil
}
func (s *lifecycleStore) Heartbeat(context.Context, uuid.UUID, int) error {
	s.heartbeats.Add(1)
	if s.heartbeatFailed.Load() {
		return errors.New("driver secret")
	}
	return nil
}
func (s *lifecycleStore) MarkDraining(context.Context, uuid.UUID) error { return nil }
func (s *lifecycleStore) StopWorker(_ context.Context, _ uuid.UUID, reason string) error {
	s.reason = reason
	s.stopped.Add(1)
	return nil
}
func (s *lifecycleStore) Renew(ctx context.Context, _ *job.Job) (time.Time, error) {
	s.renewals.Add(1)
	if s.renewPanic {
		panic("driver secret")
	}
	if s.renewFailed.Load() {
		return time.Time{}, errors.New("driver secret")
	}
	select {
	case s.renewed <- struct{}{}:
	case <-ctx.Done():
		return time.Time{}, ctx.Err()
	}
	return time.Now().Add(time.Minute), nil
}
func (s *lifecycleStore) DetectOffline(context.Context, int) ([]uuid.UUID, error) { return nil, nil }
func (s *lifecycleStore) RecoverExpired(context.Context, int) ([]job.RecoveredAttempt, error) {
	s.recoveries.Add(1)
	if s.recoveryFailed.Load() {
		return nil, errors.New("driver secret")
	}
	return nil, nil
}

func TestLeaseLoopsRenewalFailureGracefulAndPanicJoin(t *testing.T) {
	for _, mode := range []string{"graceful", "renew_failure", "heartbeat_failure", "recovery_failure", "executor_panic", "renew_panic"} {
		t.Run(mode, func(t *testing.T) {
			base, q, e := poolFixture(1)
			s := &lifecycleStore{poolStore: base, renewed: make(chan struct{}, 100), renewPanic: mode == "renew_panic"}
			if mode == "executor_panic" {
				for id := range base.jobs {
					e.panicJob = id
				}
			}
			ctx, cancel := boundedTest(t)
			defer cancel()
			var logs bytes.Buffer
			w, err := NewWithConcurrency(s, q, e, slog.New(slog.NewJSONHandler(&logs, nil)), 1)
			if err != nil {
				t.Fatal(err)
			}
			// Accelerated deterministic unit timers only. Production config rejects
			// sub-second periods; the public constructor still validates the defaults.
			w.policy.RenewInterval = 10 * time.Millisecond
			w.policy.HeartbeatInterval = 10 * time.Millisecond
			w.policy.RecoveryInterval = 10 * time.Millisecond
			done := make(chan error, 1)
			go func() { done <- w.Run(ctx) }()
			take(t, ctx, e.entered)
			switch mode {
			case "executor_panic", "renew_panic":
			default:
				take(t, ctx, s.renewed)
				switch mode {
				case "renew_failure":
					s.renewFailed.Store(true)
				case "heartbeat_failure":
					s.heartbeatFailed.Store(true)
				case "recovery_failure":
					s.recoveryFailed.Store(true)
					time.Sleep(35 * time.Millisecond)
					cancel()
				case "graceful":
					cancel()
				}
			}
			join, stopJoin := context.WithTimeout(t.Context(), 3*time.Second)
			defer stopJoin()
			err = take(t, join, done)
			healthy := mode == "graceful" || mode == "recovery_failure"
			if (err == nil) != healthy {
				t.Fatal("incorrect lifecycle outcome", err)
			}
			if s.registered.Load() != 1 || s.stopped.Load() != 1 || w.active.Load() != 0 {
				t.Fatal("registration or join count")
			}
			if healthy && s.reason != "graceful_shutdown" || !healthy && s.reason != "fatal_error" {
				t.Fatal("stop reason", s.reason)
			}
			if mode == "renew_failure" || mode == "renew_panic" {
				base.mu.Lock()
				finishes := base.finishes
				base.mu.Unlock()
				q.mu.Lock()
				acks := len(q.acks)
				q.mu.Unlock()
				if finishes != 0 || acks != 0 {
					t.Fatal("uncertain renewal finalized/ACKed")
				}
			}
			beforeHB, beforeRenew, beforeRecovery := s.heartbeats.Load(), s.renewals.Load(), s.recoveries.Load()
			time.Sleep(25 * time.Millisecond)
			if beforeHB != s.heartbeats.Load() || beforeRenew != s.renewals.Load() || beforeRecovery != s.recoveries.Load() {
				t.Fatal("background operations continued after join")
			}
			if strings.Contains(logs.String(), "driver secret") {
				t.Fatal("raw error leaked")
			}
			for _, event := range []string{"heartbeat_stopped", "recovery_stopped", "worker_stopped"} {
				if !strings.Contains(logs.String(), event) {
					t.Fatal("missing join evidence", event)
				}
			}
			if mode == "recovery_failure" && !strings.Contains(logs.String(), "recovery_failed") {
				t.Fatal("reaper outage hidden")
			}
		})
	}
}

func (s *lifecycleStore) PromoteRetries(ctx context.Context, limit int) ([]uuid.UUID, error) {
	return nil, ctx.Err()
}
