package worker

import (
	"github.com/google/uuid"
	"time"
)

// Registry persists liveness, concurrency, active count and immutable capabilities.
// Hostname remains reserved; capability changes require a fresh process identity.
type Worker struct {
	ID            uuid.UUID  `json:"worker_id"`
	Hostname      string     `json:"hostname"`
	Status        Status     `json:"status"`
	Capabilities  []string   `json:"capabilities"`
	Concurrency   int        `json:"concurrency"`
	ActiveJobs    int        `json:"active_jobs"`
	LastHeartbeat *time.Time `json:"last_heartbeat"`
}
