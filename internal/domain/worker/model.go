package worker

import (
	"github.com/google/uuid"
	"time"
)

// Registry persists liveness, concurrency and active count in Phase 4.
// Hostname/capabilities remain reserved; capability scheduling is not implemented.
type Worker struct {
	ID            uuid.UUID  `json:"worker_id"`
	Hostname      string     `json:"hostname"`
	Status        Status     `json:"status"`
	Capabilities  []string   `json:"capabilities"`
	Concurrency   int        `json:"concurrency"`
	ActiveJobs    int        `json:"active_jobs"`
	LastHeartbeat *time.Time `json:"last_heartbeat"`
}
