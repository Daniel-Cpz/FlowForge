package job

import (
	"context"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/dashboard"
	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/google/uuid"
)

func (s *Service) DashboardSummary(ctx context.Context) (*dashboard.Summary, error) {
	r, ok := s.repo.(dashboard.Repository)
	if !ok {
		return nil, domain.ErrInvalidStoredData
	}
	return r.DashboardSummary(ctx)
}
func (s *Service) Workers(ctx context.Context, limit int, after *uuid.UUID) ([]worker.Worker, *uuid.UUID, error) {
	if limit < 1 || limit > 100 || (after != nil && *after == uuid.Nil) {
		return nil, nil, domain.ErrInvalidInput
	}
	r, ok := s.repo.(dashboard.Repository)
	if !ok {
		return nil, nil, domain.ErrInvalidStoredData
	}
	values, err := r.ListWorkers(ctx, limit+1, after)
	if err != nil {
		return nil, nil, err
	}
	if values == nil {
		values = []worker.Worker{}
	}
	var next *uuid.UUID
	if len(values) > limit {
		values = values[:limit]
		id := values[limit-1].ID
		next = &id
	}
	return values, next, nil
}
func (s *Service) Schedules(ctx context.Context, limit int, after *domain.PageCursor) ([]schedule.Schedule, *domain.PageCursor, error) {
	if limit < 1 || limit > 100 || (after != nil && !after.Valid()) {
		return nil, nil, domain.ErrInvalidInput
	}
	r, ok := s.repo.(dashboard.Repository)
	if !ok {
		return nil, nil, domain.ErrInvalidStoredData
	}
	values, err := r.ListSchedules(ctx, limit+1, after)
	if err != nil {
		return nil, nil, err
	}
	if values == nil {
		values = []schedule.Schedule{}
	}
	var next *domain.PageCursor
	if len(values) > limit {
		values = values[:limit]
		v := values[limit-1]
		next = &domain.PageCursor{CreatedAt: v.CreatedAt, ID: v.ID}
	}
	return values, next, nil
}
