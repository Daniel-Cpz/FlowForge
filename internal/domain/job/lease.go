package job

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RecoveredAttempt identifies the fenced execution closed by lease recovery.
type RecoveredAttempt struct {
	JobID         uuid.UUID
	WorkerID      uuid.UUID
	AttemptNumber int
	Status        Status
	RetryAt       *time.Time
}

var ErrLeaseLost = fmt.Errorf("%w: execution lease lost", ErrInvalidTransition)

// The caller must supply authoritative database time. This local predicate does
// not replace SQL conditional updates or grant execution authority by itself.
func (j *Job) OwnsLease(owner uuid.UUID, attempt int, now time.Time) bool {
	return j.Status == Running && owner != uuid.Nil && j.AssignedWorker != nil && *j.AssignedWorker == owner && j.AttemptCount == attempt && attempt > 0 && j.LeaseExpiry != nil && j.LeaseExpiry.After(now)
}

// Expiry recovery follows RUNNING -> FAILED -> RETRYING/DEAD_LETTER and the
// shared budget. The repository chooses configured jitter and persists DB time.
func (j *Job) RecoverExpired(now time.Time) error {
	if j.Status != Running || j.AssignedWorker == nil || *j.AssignedWorker == uuid.Nil || j.AttemptCount < 1 || j.LeaseExpiry == nil || j.LeaseExpiry.After(now) {
		return ErrLeaseLost
	}
	return j.Fail(Failure{Retryable, "lease_expired"}, now, time.Second)
}
