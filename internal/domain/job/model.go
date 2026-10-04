package job

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Timeout is expressed in seconds, including in the HTTP representation.
type Job struct {
	ID             uuid.UUID       `json:"id"`
	Type           string          `json:"type"`
	Status         Status          `json:"status"`
	Priority       int             `json:"priority"`
	Payload        json.RawMessage `json:"payload"`
	Result         json.RawMessage `json:"result"`
	AttemptCount   int             `json:"attempt_count"`
	MaxAttempts    int             `json:"max_attempts"`
	Timeout        int             `json:"timeout"`
	IdempotencyKey *string         `json:"idempotency_key"`
	AssignedWorker *uuid.UUID      `json:"assigned_worker"`
	LeaseExpiry    *time.Time      `json:"lease_expiry"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
}

// Attempt is an execution record, not the durable logical job itself.
type Attempt struct {
	ID            uuid.UUID       `json:"id"`
	JobID         uuid.UUID       `json:"job_id"`
	WorkerID      uuid.UUID       `json:"worker_id"`
	AttemptNumber int             `json:"attempt_number"`
	Status        Status          `json:"status"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	Result        json.RawMessage `json:"result"`
	Error         *string         `json:"error"`
}
