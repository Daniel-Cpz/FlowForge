package job

import (
	"context"
	"github.com/google/uuid"
)

type Repository interface {
	Create(context.Context, *Job) error
	GetByID(context.Context, uuid.UUID) (*Job, error)
	List(context.Context, int, int) ([]Job, error)
}
