package job

import "errors"

var (
	ErrIdempotencyConflict = errors.New("idempotency key request conflict")
	ErrNotFound            = errors.New("job not found")
	ErrInvalidInput        = errors.New("invalid job input")
	ErrInvalidTransition   = errors.New("invalid job state transition")
	ErrInvalidStoredData   = errors.New("invalid stored job data")
)
