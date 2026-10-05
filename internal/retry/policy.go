// Package retry implements bounded equal jitter without wall-clock decisions.
package retry

import (
	"fmt"
	"math/rand/v2"
	"time"
)

type Policy struct{ BaseDelay, MaxDelay time.Duration }

func DefaultPolicy() Policy { return Policy{time.Second, 30 * time.Second} }
func (p Policy) Validate() error {
	if p.BaseDelay < time.Second || p.BaseDelay > 60*time.Second || p.MaxDelay < p.BaseDelay || p.MaxDelay > 10*time.Minute {
		return fmt.Errorf("retry delay requires base 1..60 seconds and max base..600 seconds")
	}
	return nil
}

// Jitter is injectable. The default top-level rand/v2 source is concurrency safe.
// Custom sources must honor [0,n) and be concurrency safe when shared.
type Jitter func(int64) int64

func (p Policy) Delay(attempt int, jitter Jitter) (time.Duration, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if attempt < 1 {
		return 0, fmt.Errorf("attempt must be positive")
	}
	cap := p.BaseDelay
	for n := 1; n < attempt && cap < p.MaxDelay; n++ {
		if cap > p.MaxDelay/2 {
			cap = p.MaxDelay
		} else {
			cap *= 2
		}
	}
	cap = min(cap, p.MaxDelay)
	low := cap / 2
	if jitter == nil {
		jitter = rand.Int64N
	}
	width := int64(cap-low) + 1
	sample := jitter(width)
	if sample < 0 || sample >= width {
		return 0, fmt.Errorf("invalid jitter sample")
	}
	return low + time.Duration(sample), nil
}
