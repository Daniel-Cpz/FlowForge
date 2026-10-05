package job

import (
	"context"
	"github.com/google/uuid"
)

type Repository interface {
	Create(context.Context, *Job) (CreateDisposition, error)
	GetByID(context.Context, uuid.UUID) (*Job, error)
	// List returns at most limit rows strictly after the optional boundary.
	List(context.Context, int, *PageCursor) ([]Job, error)
}

type CreateDisposition string

const (
	Created  CreateDisposition = "created"
	Replayed CreateDisposition = "replayed"
)
