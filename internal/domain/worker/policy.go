package worker

import (
	"errors"
	"time"
)

var ErrOffline = errors.New("worker is offline or registration rejected")

type LeasePolicy struct {
	LeaseDuration     time.Duration
	RenewInterval     time.Duration
	HeartbeatInterval time.Duration
	OfflineAfter      time.Duration
	RecoveryInterval  time.Duration
}

func DefaultLeasePolicy() LeasePolicy {
	return LeasePolicy{15 * time.Second, 5 * time.Second, 2 * time.Second, 10 * time.Second, time.Second}
}
func (p LeasePolicy) Validate() error {
	for _, bound := range []struct {
		value    time.Duration
		min, max int
	}{
		{p.LeaseDuration, 3, 300}, {p.RenewInterval, 1, 60}, {p.HeartbeatInterval, 1, 60}, {p.OfflineAfter, 3, 600}, {p.RecoveryInterval, 1, 30},
	} {
		if bound.value < time.Duration(bound.min)*time.Second || bound.value > time.Duration(bound.max)*time.Second || bound.value%time.Second != 0 {
			return errors.New("invalid worker lease policy bounds")
		}
	}
	if 2*p.RenewInterval >= p.LeaseDuration || 2*p.HeartbeatInterval >= p.OfflineAfter {
		return errors.New("lease and offline windows must exceed twice their update interval")
	}
	return nil
}
