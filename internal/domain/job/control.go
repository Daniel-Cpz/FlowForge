package job

import (
	"context"
	"github.com/google/uuid"
)

// ControlRepository keeps user commands separate from ordinary graph changes.
// Redrive is an explicit administrative exception, never a normal transition.
type ControlRepository interface {
	Cancel(context.Context, uuid.UUID) (*Job, error)
	RedriveDeadLetter(context.Context, uuid.UUID) (*Job, error)
	ListDeadLetter(context.Context, int, *PageCursor) ([]Job, error)
	Attempts(context.Context, uuid.UUID) ([]Attempt, error)
}
