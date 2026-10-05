package execution

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
)

type cancellationClaimStore struct {
	*poolStore
	cancel context.CancelFunc
}

func (s *cancellationClaimStore) Claim(ctx context.Context, id, owner uuid.UUID) (*job.Job, error) {
	j, err := s.poolStore.Claim(ctx, id, owner)
	s.cancel()
	return j, err
}

type cancellationReceiveQueue struct {
	*poolQueue
	started chan struct{}
	msg     *job.Delivery
}

func (q *cancellationReceiveQueue) Receive(ctx context.Context, _ string) (*job.Delivery, error) {
	close(q.started)
	<-ctx.Done()
	return q.msg, nil
}
func TestPoolCancellationInFlightClaim(t *testing.T) {
	ctx, cancel := boundedTest(t)
	defer cancel()
	s, q, _ := poolFixture(1)
	for _, j := range s.jobs {
		j.Type = "SLEEP"
		j.Payload = json.RawMessage(`{"duration_ms":10000}`)
	}
	w := poolWorker(t, &cancellationClaimStore{s, cancel}, q, Sleep{}, 1)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, j := range s.jobs {
		if j.Status != job.Failed || j.AttemptCount != 1 || !strings.Contains(string(j.Result), "execution_cancelled") {
			t.Fatal("committed in-flight claim abandoned", j)
		}
	}
	if len(q.acks) != 1 {
		t.Fatal("cleanup not acknowledged")
	}
}
func TestPoolCancellationInFlightReceive(t *testing.T) {
	ctx, cancel := boundedTest(t)
	defer cancel()
	s, q, e := poolFixture(1)
	started := make(chan struct{})
	wrapper := &cancellationReceiveQueue{q, started, <-q.messages}
	done := make(chan error, 1)
	go func() { done <- poolWorker(t, s, wrapper, e, 1).Run(ctx) }()
	take(t, ctx, started)
	cancel()
	if err := take(t, t.Context(), done); err != nil {
		t.Fatal(err)
	}
	if s.claims != 0 || len(q.acks) != 0 || e.calls != 0 {
		t.Fatal("unclaimed canceled delivery marked complete")
	}
}
