package job

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestTimeoutOutcomeUsesGraphAndBudget(t *testing.T) {
	for _, budget := range []int{1, 2} {
		j := Job{Status: Running, AttemptCount: 1, MaxAttempts: budget}
		now := time.Now().UTC()
		if err := j.FailOutcome(TimedOut, Failure{Class: Retryable, Code: "execution_timeout"}, now, time.Second); err != nil {
			t.Fatal(err)
		}
		want := DeadLetter
		if budget == 2 {
			want = Retrying
		}
		if j.Status != want || (j.RetryAt != nil) != (budget == 2) {
			t.Fatal(j)
		}
	}
	if TimedOut.Terminal() || CanTransition(DeadLetter, Queued) {
		t.Fatal("outcome/admin graph contradiction")
	}
	owner := uuid.New()
	now := time.Now().UTC()
	expired := now.Add(-time.Second)
	j := Job{Status: Running, AssignedWorker: &owner, LeaseExpiry: &expired, CancelRequestedAt: &now, AttemptCount: 1, MaxAttempts: 3}
	if err := j.RecoverExpired(now); err != nil || j.Status != Cancelled || j.RetryAt != nil {
		t.Fatal(j, err)
	}
}
