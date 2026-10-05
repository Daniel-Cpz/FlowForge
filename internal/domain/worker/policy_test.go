package worker

import (
	"testing"
	"time"
)

func TestLeasePolicyBoundsAndSafetyMargins(t *testing.T) {
	p := DefaultLeasePolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*LeasePolicy){
		func(p *LeasePolicy) { p.LeaseDuration = 2 * time.Second }, func(p *LeasePolicy) { p.LeaseDuration = 301 * time.Second },
		func(p *LeasePolicy) { p.RenewInterval = 0 }, func(p *LeasePolicy) { p.RenewInterval = 8 * time.Second },
		func(p *LeasePolicy) { p.HeartbeatInterval = 5 * time.Second }, func(p *LeasePolicy) { p.OfflineAfter = 601 * time.Second },
		func(p *LeasePolicy) { p.RecoveryInterval = 31 * time.Second }, func(p *LeasePolicy) { p.LeaseDuration += time.Millisecond },
	} {
		copy := p
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatal("unsafe policy accepted", copy)
		}
	}
	p = LeasePolicy{3 * time.Second, time.Second, time.Second, 4 * time.Second, time.Second}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}
