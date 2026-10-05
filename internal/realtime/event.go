// Package realtime defines lossy invalidation hints, never business authority.
package realtime

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

type Hint struct {
	Status string `json:"status,omitempty"`
}
type Event struct {
	Version    int       `json:"version"`
	Event      string    `json:"event"`
	ResourceID uuid.UUID `json:"resource_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Hint       Hint      `json:"hint"`
}

func New(kind string, id uuid.UUID, status string) Event {
	return Event{1, kind, id, time.Now().UTC(), Hint{status}}
}
func (e Event) Valid() bool {
	if e.Version != 1 || e.OccurredAt.IsZero() || e.OccurredAt.Year() < 1 || e.OccurredAt.Year() > 9999 {
		return false
	}
	allowed := map[string]string{
		"job.changed":    " QUEUED RUNNING SUCCEEDED FAILED RETRYING DEAD_LETTER CANCELLED TIMED_OUT ",
		"worker.changed": " ONLINE IDLE BUSY DRAINING OFFLINE ", "schedule.changed": " ACTIVE CANCELLED ",
		"system.changed": " LIVE DEGRADED ",
	}
	statuses, ok := allowed[e.Event]
	if !ok || (e.Event != "system.changed" && e.ResourceID == uuid.Nil) {
		return false
	}
	if e.Hint.Status == "" {
		return true
	}
	for _, s := range strings.Fields(statuses) {
		if s == e.Hint.Status {
			return true
		}
	}
	return false
}
func Decode(data []byte) (Event, error) {
	var e Event
	if len(data) > 1024 {
		return e, errors.New("invalid event")
	}
	if err := json.Unmarshal(data, &e); err != nil || !e.Valid() {
		return Event{}, errors.New("invalid event")
	}
	return e, nil
}
