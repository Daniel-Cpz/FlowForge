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
}

var ErrLeaseLost = fmt.Errorf("%w: execution lease lost", ErrInvalidTransition)

// The caller must supply authoritative database time. This local predicate does
// not replace SQL conditional updates or grant execution authority by itself.
func (j *Job) OwnsLease(owner uuid.UUID, attempt int, now time.Time) bool {
	return j.Status == Running && owner != uuid.Nil && j.AssignedWorker != nil && *j.AssignedWorker == owner && j.AttemptCount == attempt && attempt > 0 && j.LeaseExpiry != nil && j.LeaseExpiry.After(now)
}

// Recovery is a separate, expiry-guarded transition. Ordinary Transition still
// rejects RUNNING -> QUEUED; business FAILED jobs are never recovered by this path.
func (j *Job) RecoverExpired(now time.Time) error {
	if j.Status != Running || j.AssignedWorker == nil || *j.AssignedWorker == uuid.Nil || j.AttemptCount < 1 || j.LeaseExpiry == nil || j.LeaseExpiry.After(now) {
		return ErrLeaseLost
	}
	j.Status = Queued
	j.AssignedWorker = nil
	j.LeaseExpiry = nil
	j.StartedAt = nil
	j.FinishedAt = nil
	j.Result = nil
	return nil
}
