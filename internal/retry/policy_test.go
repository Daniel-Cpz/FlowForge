package retry

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestEqualJitterBoundsAndSaturation(t *testing.T) {
	p := DefaultPolicy()
	for _, tc := range []struct {
		n   int
		cap time.Duration
	}{{1, time.Second}, {2, 2 * time.Second}, {5, 16 * time.Second}, {6, 30 * time.Second}, {math.MaxInt, 30 * time.Second}} {
		low, e := p.Delay(tc.n, func(int64) int64 { return 0 })
		if e != nil || low != tc.cap/2 {
			t.Fatal(tc, low, e)
		}
		high, e := p.Delay(tc.n, func(n int64) int64 { return n - 1 })
		if e != nil || high != tc.cap {
			t.Fatal(tc, high, e)
		}
	}
	for _, n := range []int{0, -1} {
		if _, e := p.Delay(n, nil); e == nil {
			t.Fatal("invalid attempt")
		}
	}
	for _, p := range []Policy{{0, time.Second}, {time.Second, 500 * time.Millisecond}, {61 * time.Second, 90 * time.Second}, {time.Second, 601 * time.Second}} {
		if p.Validate() == nil {
			t.Fatal("invalid policy")
		}
	}
	for _, sample := range []int64{-1, math.MaxInt64} {
		if _, e := p.Delay(1, func(int64) int64 { return sample }); e == nil {
			t.Fatal("invalid source")
		}
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				d, e := p.Delay(100, nil)
				if e != nil || d < 15*time.Second || d > 30*time.Second {
					t.Error("production jitter bounds")
				}
			}
		}()
	}
	wg.Wait()
}
