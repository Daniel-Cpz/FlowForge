package job

import "errors"

var (
	ErrNotFound          = errors.New("job not found")
	ErrInvalidInput      = errors.New("invalid job input")
	ErrInvalidTransition = errors.New("invalid job state transition")
)
