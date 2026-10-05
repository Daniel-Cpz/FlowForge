package job

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestFailureClassificationBudgetAndGraph(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	owner := uuid.New()
	expiry := now.Add(time.Second)
	for _, tc := range []struct {
		class        FailureClass
		attempt, max int
		want         Status
	}{{Retryable, 1, 2, Retrying}, {Retryable, 2, 2, DeadLetter}, {Permanent, 1, 3, DeadLetter}} {
		j := Job{Status: Running, AttemptCount: tc.attempt, MaxAttempts: tc.max, AssignedWorker: &owner, LeaseExpiry: &expiry}
		if err := j.Fail(Failure{tc.class, "controlled_failure"}, now, time.Second); err != nil {
			t.Fatal(err)
		}
		if j.Status != tc.want || j.AttemptCount != tc.attempt || j.AssignedWorker != nil || j.LeaseExpiry != nil || (j.RetryAt != nil) != (tc.want == Retrying) {
			t.Fatal(j)
		}
		if j.Fail(Failure{Retryable, "x"}, now, time.Second) == nil {
			t.Fatal("double finalize")
		}
	}
	for _, f := range []Failure{{"unknown", "x"}, {Retryable, "driver error details"}, {Permanent, ""}} {
		if f.Valid() {
			t.Fatal("raw/invalid failure accepted")
		}
	}
}
