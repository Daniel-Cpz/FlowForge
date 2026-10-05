package job

import "fmt"

func CanTransition(from, to Status) bool {
	switch from {
	case Queued:
		return to == Running || to == Cancelled
	case Running:
		return to == Succeeded || to == Failed || to == TimedOut || to == Cancelled
	case Failed, TimedOut:
		return to == Retrying || to == DeadLetter
	case Retrying:
		return to == Queued || to == Cancelled
	default:
		return false
	}
}

// Transition validates the state graph only. Execution metadata and persistence
// are coordinated atomically by repository claim/finalize use cases.
func (j *Job) Transition(to Status) error {
	if !CanTransition(j.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, j.Status, to)
	}
	j.Status = to
	return nil
}
