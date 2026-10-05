package schedule

import (
	"context"
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
	"time"
)

type Schedule struct {
	StartImmediately     bool            `json:"-"`
	ID                   uuid.UUID       `json:"id"`
	Status               string          `json:"status"`
	Type                 string          `json:"type"`
	Payload              json.RawMessage `json:"payload"`
	Priority             int             `json:"priority"`
	MaxAttempts          int             `json:"max_attempts"`
	Timeout              int             `json:"timeout"`
	RequiredCapabilities []string        `json:"required_capabilities"`
	IntervalSeconds      int             `json:"interval_seconds"`
	NextRunAt            time.Time       `json:"next_run_at"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

func (s *Schedule) Validate() error {
	if s == nil || s.ID == uuid.Nil || (s.Status != "ACTIVE" && s.Status != "CANCELLED") || s.IntervalSeconds < 1 || s.IntervalSeconds > 604800 || s.NextRunAt.IsZero() || s.NextRunAt.UTC().Year() < 1 || s.NextRunAt.UTC().Year() > 9999 || s.UpdatedAt.Before(s.CreatedAt) {
		return job.ErrInvalidInput
	}
	template := job.Job{ID: s.ID, Type: s.Type, Payload: s.Payload, Priority: s.Priority, MaxAttempts: s.MaxAttempts, Timeout: s.Timeout, RequiredCapabilities: s.RequiredCapabilities, Status: job.Queued, CreatedAt: s.CreatedAt}
	return template.Validate()
}

type Repository interface {
	CreateSchedule(context.Context, *Schedule) error
	GetSchedule(context.Context, uuid.UUID) (*Schedule, error)
	CancelSchedule(context.Context, uuid.UUID) (*Schedule, error)
}
