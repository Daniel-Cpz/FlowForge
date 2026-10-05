package job

import "errors"

var (
	ErrPriorityDeferred      = errors.New("higher ranked queued job exists")
	ErrCancellationRequested = errors.New("user cancellation requested")
	ErrControlConflict       = errors.New("job control conflict")
	ErrIdempotencyConflict   = errors.New("idempotency key request conflict")
	ErrNotFound              = errors.New("job not found")
	ErrInvalidInput          = errors.New("invalid job input")
	ErrInvalidTransition     = errors.New("invalid job state transition")
	ErrInvalidStoredData     = errors.New("invalid stored job data")
)
