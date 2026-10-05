package job

import "time"

// Failure classification is an explicit executor contract, never driver text.
type FailureClass string

const (
	Permanent FailureClass = "permanent"
	Retryable FailureClass = "retryable"
)

type Failure struct {
	Class FailureClass
	Code  string
}

func (f Failure) Valid() bool {
	if f.Class != Permanent && f.Class != Retryable {
		return false
	}
	// Bounded, stable application codes; raw errors cannot become stored policy.
	if len(f.Code) < 1 || len(f.Code) > 64 {
		return false
	}
	for _, c := range f.Code {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// Fail applies the ordinary graph in order, retaining the Attempt separately.
// now must be database time. Only the repository can persist this decision.
func (j *Job) Fail(f Failure, now time.Time, delay time.Duration) error {
	if !f.Valid() || j.Status != Running || j.AttemptCount < 1 || j.AttemptCount > j.MaxAttempts || !validTime(now) || delay <= 0 {
		return ErrInvalidInput
	}
	copy := *j
	if err := copy.Transition(Failed); err != nil {
		return err
	}
	target := DeadLetter
	if f.Class == Retryable && copy.AttemptCount < copy.MaxAttempts {
		target = Retrying
	}
	if err := copy.Transition(target); err != nil {
		return err
	}
	copy.AssignedWorker, copy.LeaseExpiry, copy.RetryAt = nil, nil, nil
	if target == Retrying {
		at := now.Add(delay)
		copy.RetryAt = &at
		copy.FinishedAt = nil
	} else {
		copy.FinishedAt = &now
	}
	*j = copy
	return nil
}
