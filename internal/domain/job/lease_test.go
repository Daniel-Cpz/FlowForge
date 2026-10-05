package job

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestLeaseOwnershipAndExpiryRecovery(t *testing.T) {
	now := time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC)
	owner := uuid.New()
	expiry := now.Add(time.Second)
	original := Job{ID: uuid.New(), Status: Running, AssignedWorker: &owner, AttemptCount: 2, MaxAttempts: 3, LeaseExpiry: &expiry}
	if !original.OwnsLease(owner, 2, now) || original.OwnsLease(uuid.New(), 2, now) || original.OwnsLease(owner, 1, now) || original.OwnsLease(owner, 2, expiry) {
		t.Fatal("invalid lease authority")
	}
	for _, status := range []Status{Queued, Succeeded, Failed} {
		j := original
		j.Status = status
		if j.OwnsLease(owner, 2, now) || j.RecoverExpired(expiry) == nil {
			t.Fatal("non-running recovered")
		}
	}
	for _, mode := range []string{"valid", "no_owner", "nil_owner", "no_attempt", "no_expiry"} {
		j := original
		switch mode {
		case "no_owner":
			j.AssignedWorker = nil
		case "nil_owner":
			id := uuid.Nil
			j.AssignedWorker = &id
		case "no_attempt":
			j.AttemptCount = 0
		case "no_expiry":
			j.LeaseExpiry = nil
		}
		if j.RecoverExpired(now) == nil {
			t.Fatal("unexpired/corrupt recovered", mode)
		}
		if mode != "valid" && j.RecoverExpired(expiry) == nil {
			t.Fatal("corrupt recovered", mode)
		}
	}
	j := original
	if j.Transition(Queued) == nil {
		t.Fatal("ordinary graph allows recovery")
	}
	if err := j.RecoverExpired(expiry); err != nil {
		t.Fatal(err)
	}
	if j.Status != Retrying || j.AttemptCount != 2 || j.AssignedWorker != nil || j.LeaseExpiry != nil || j.RetryAt == nil {
		t.Fatal("recovery lost history or retained execution metadata")
	}
	if j.RecoverExpired(expiry) == nil {
		t.Fatal("recovered twice")
	}
}
