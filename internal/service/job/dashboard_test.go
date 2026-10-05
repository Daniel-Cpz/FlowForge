package job

import (
	"context"
	"errors"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/dashboard"
	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/google/uuid"
	"testing"
	"time"
)

type readFake struct {
	domain.Repository
	limit   int
	failure error
}

func (r *readFake) DashboardSummary(context.Context) (*dashboard.Summary, error) {
	return &dashboard.Summary{QueueDepth: 5}, r.failure
}
func (r *readFake) ListWorkers(_ context.Context, n int, _ *uuid.UUID) ([]worker.Worker, error) {
	r.limit = n
	return []worker.Worker{{ID: uuid.New()}, {ID: uuid.New()}}, r.failure
}
func (r *readFake) ListSchedules(_ context.Context, n int, _ *domain.PageCursor) ([]schedule.Schedule, error) {
	r.limit = n
	return []schedule.Schedule{{ID: uuid.New(), CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}, {ID: uuid.New()}}, r.failure
}
func TestDashboardServiceBoundsAndErrors(t *testing.T) {
	r := &readFake{}
	s := New(r)
	ctx := t.Context()
	v, err := s.DashboardSummary(ctx)
	if err != nil || v.QueueDepth != 5 {
		t.Fatal(v, err)
	}
	values, next, err := s.Workers(ctx, 1, nil)
	if err != nil || len(values) != 1 || next == nil || r.limit != 2 {
		t.Fatal(values, next, err)
	}
	schedules, boundary, err := s.Schedules(ctx, 1, nil)
	if err != nil || len(schedules) != 1 || boundary == nil || r.limit != 2 {
		t.Fatal(err)
	}
	for _, n := range []int{0, 101} {
		if _, _, err := s.Workers(ctx, n, nil); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal(err)
		}
		if _, _, err := s.Schedules(ctx, n, nil); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	r.failure = errors.New("unavailable")
	if _, err := s.DashboardSummary(ctx); err == nil {
		t.Fatal("read failure hidden")
	}
	if _, _, err := s.Workers(ctx, 20, nil); err == nil {
		t.Fatal("worker failure hidden")
	}
	if _, _, err := s.Schedules(ctx, 20, nil); err == nil {
		t.Fatal("schedule failure hidden")
	}
}
