package redis

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestEventPublishingIsBoundedDuringOutage(t *testing.T) {
	c := NewClient("127.0.0.1:1", "", 0)
	defer c.Close()
	var log bytes.Buffer
	b := NewEvents(context.Background(), c, "test:ui", slog.New(slog.NewTextHandler(&log, nil)))
	start := time.Now()
	for range 1000 {
		b.Change("job.changed", uuid.New(), "QUEUED")
	}
	if time.Since(start) > time.Second || len(b.queue) > 128 {
		t.Fatal("business producer blocked")
	}
	// Joining guarantees the log writer has stopped before inspection.
	time.Sleep(300 * time.Millisecond)
	b.Close()
	b.Close()
	if !strings.Contains(log.String(), "ui_hint_publish_failed") || !strings.Contains(log.String(), "ui_hint_queue_full") {
		t.Fatal("missing sanitized degradation warnings")
	}
}
