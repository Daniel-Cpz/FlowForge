package job

import (
	"context"
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/capability"
	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/google/uuid"
	"strings"
	"time"
)

type ScheduleInput struct {
	Template        CreateInput
	IntervalSeconds int
	NextRunAt       *time.Time
}

func (s *Service) CreateSchedule(ctx context.Context, in ScheduleInput) (*schedule.Schedule, error) {
	repo, ok := s.repo.(schedule.Repository)
	if !ok {
		return nil, domain.ErrInvalidStoredData
	}
	caps, err := capability.Normalize(in.Template.RequiredCapabilities)
	if err != nil || len(in.Template.Payload) > 1<<20 || in.Template.IdempotencyKey != nil || in.Template.ScheduledAt != nil {
		return nil, domain.ErrInvalidInput
	}
	maxAttempts, timeout := 3, 300
	if in.Template.MaxAttempts != nil {
		maxAttempts = *in.Template.MaxAttempts
	}
	if in.Template.Timeout != nil {
		timeout = *in.Template.Timeout
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	v := &schedule.Schedule{ID: uuid.New(), Status: "ACTIVE", Type: strings.TrimSpace(in.Template.Type), Payload: append(json.RawMessage(nil), in.Template.Payload...), Priority: in.Template.Priority, MaxAttempts: maxAttempts, Timeout: timeout, RequiredCapabilities: caps, IntervalSeconds: in.IntervalSeconds, NextRunAt: now, CreatedAt: now, UpdatedAt: now}
	if in.NextRunAt != nil {
		v.NextRunAt = in.NextRunAt.UTC().Truncate(time.Microsecond)
	}
	v.StartImmediately = in.NextRunAt == nil
	if err := v.Validate(); err != nil {
		return nil, err
	}
	// An omitted first run is resolved from PostgreSQL time, not API time.
	if err := repo.CreateSchedule(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}
func (s *Service) GetSchedule(ctx context.Context, id uuid.UUID) (*schedule.Schedule, error) {
	repo, ok := s.repo.(schedule.Repository)
	if !ok {
		return nil, domain.ErrInvalidStoredData
	}
	return repo.GetSchedule(ctx, id)
}
func (s *Service) CancelSchedule(ctx context.Context, id uuid.UUID) (*schedule.Schedule, error) {
	repo, ok := s.repo.(schedule.Repository)
	if !ok {
		return nil, domain.ErrInvalidStoredData
	}
	return repo.CancelSchedule(ctx, id)
}
