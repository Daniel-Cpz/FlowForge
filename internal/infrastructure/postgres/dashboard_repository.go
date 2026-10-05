package postgres

import (
	"context"
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/dashboard"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/google/uuid"
	"time"
)

var _ dashboard.Repository = (*JobRepository)(nil)

func (r *JobRepository) DashboardSummary(ctx context.Context) (*dashboard.Summary, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var data []byte
	// A single PostgreSQL statement gives one MVCC snapshot across all counts.
	err := r.pool.QueryRow(ctx, `SELECT json_build_object(
 'jobs',COALESCE((SELECT json_object_agg(status,n) FROM (SELECT status,count(*) n FROM jobs GROUP BY status) c),'{}'::json),
 'queue_depth',(SELECT count(*) FROM jobs WHERE status='QUEUED' AND attempt_count<max_attempts AND (scheduled_at IS NULL OR scheduled_at<=statement_timestamp())),
 'workers',COALESCE((SELECT json_object_agg(status,n) FROM (SELECT status,count(*) n FROM workers GROUP BY status) c),'{}'::json),
 'active_jobs',COALESCE((SELECT sum(active_jobs) FROM workers WHERE status<>'OFFLINE'),0),
 'schedules',COALESCE((SELECT json_object_agg(status,n) FROM (SELECT status,count(*) n FROM job_schedules GROUP BY status) c),'{}'::json))`).Scan(&data)
	if err != nil {
		return nil, err
	}
	v := &dashboard.Summary{}
	if err = json.Unmarshal(data, v); err != nil {
		return nil, err
	}
	for _, s := range []string{"QUEUED", "RUNNING", "SUCCEEDED", "FAILED", "RETRYING", "DEAD_LETTER", "CANCELLED", "TIMED_OUT"} {
		if _, ok := v.Jobs[s]; !ok {
			v.Jobs[s] = 0
		}
	}
	for _, s := range []string{"ONLINE", "IDLE", "BUSY", "DRAINING", "OFFLINE"} {
		if _, ok := v.Workers[s]; !ok {
			v.Workers[s] = 0
		}
	}
	for _, s := range []string{"ACTIVE", "CANCELLED"} {
		if _, ok := v.Schedules[s]; !ok {
			v.Schedules[s] = 0
		}
	}
	return v, nil
}

// Immutable worker UUID order avoids heartbeat updates moving pagination boundaries.
func (r *JobRepository) ListWorkers(ctx context.Context, limit int, after *uuid.UUID) ([]worker.Worker, error) {
	if limit < 1 || limit > 101 || (after != nil && *after == uuid.Nil) {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, `SELECT worker_id,status,capabilities,concurrency,active_jobs,last_heartbeat FROM workers WHERE ($2::uuid IS NULL OR worker_id<$2) ORDER BY worker_id DESC LIMIT $1`, limit, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]worker.Worker, 0)
	for rows.Next() {
		var v worker.Worker
		if err := rows.Scan(&v.ID, &v.Status, &v.Capabilities, &v.Concurrency, &v.ActiveJobs, &v.LastHeartbeat); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}
func (r *JobRepository) ListSchedules(ctx context.Context, limit int, after *job.PageCursor) ([]schedule.Schedule, error) {
	if limit < 1 || limit > 101 || (after != nil && !after.Valid()) {
		return nil, job.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var timestamp any
	var id any
	if after != nil {
		timestamp = after.CreatedAt
		id = after.ID
	}
	rows, err := r.pool.Query(ctx, `SELECT `+scheduleColumns+` FROM job_schedules WHERE ($2::timestamptz IS NULL OR (created_at,id)<($2,$3::uuid)) ORDER BY created_at DESC,id DESC LIMIT $1`, limit, timestamp, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]schedule.Schedule, 0)
	for rows.Next() {
		v, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, *v)
	}
	return values, rows.Err()
}
