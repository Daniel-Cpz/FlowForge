package job

import (
	"time"

	"github.com/google/uuid"
)

// PageCursor is the exclusive boundary in (created_at DESC, id DESC) order.
// Encoding this boundary for HTTP belongs to transport, not persistence.
type PageCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

func (c PageCursor) Valid() bool {
	return validTime(c.CreatedAt) && c.ID != uuid.Nil &&
		c.CreatedAt.Equal(c.CreatedAt.Truncate(time.Microsecond))
}

type Page struct {
	Jobs       []Job
	NextCursor *PageCursor
}
