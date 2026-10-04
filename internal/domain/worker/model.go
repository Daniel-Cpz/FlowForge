package worker

import (
	"github.com/google/uuid"
	"time"
)

// Model only: no registration, heartbeat, or persistence use case in Phase 0.
type Worker struct {
	ID            uuid.UUID  `json:"worker_id"`
	Hostname      string     `json:"hostname"`
	Status        Status     `json:"status"`
	Capabilities  []string   `json:"capabilities"`
	Concurrency   int        `json:"concurrency"`
	ActiveJobs    int        `json:"active_jobs"`
	LastHeartbeat *time.Time `json:"last_heartbeat"`
}
