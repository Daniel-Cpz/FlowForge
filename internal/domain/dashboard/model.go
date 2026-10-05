// Package dashboard contains read models, never scheduling authority.
package dashboard

import (
	"context"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/google/uuid"
)

type Summary struct {
	Jobs       map[string]int64 `json:"jobs"`
	QueueDepth int64            `json:"queue_depth"`
	Workers    map[string]int64 `json:"workers"`
	ActiveJobs int64            `json:"active_jobs"`
	Schedules  map[string]int64 `json:"schedules"`
}
type Repository interface {
	DashboardSummary(context.Context) (*Summary, error)
	ListWorkers(context.Context, int, *uuid.UUID) ([]worker.Worker, error)
	ListSchedules(context.Context, int, *job.PageCursor) ([]schedule.Schedule, error)
}
