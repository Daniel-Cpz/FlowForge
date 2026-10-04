package dispatch

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	ids                 []uuid.UUID
	marks               int
	pendingErr, markErr error
	reads               int
}

func (s *fakeStore) PendingDispatch(ctx context.Context, n int) ([]uuid.UUID, error) {
	s.reads++
	if n != 100 {
		panic("unbounded batch")
	}
	return s.ids, s.pendingErr
}
func (s *fakeStore) MarkPublished(context.Context, uuid.UUID) error { s.marks++; return s.markErr }

type fakePublisher struct {
	calls int
	err   error
}

func (p *fakePublisher) Publish(context.Context, uuid.UUID) error { p.calls++; return p.err }
func TestPublicationBoundaries(t *testing.T) {
	secret := errors.New("password=must-not-log")
	for _, mode := range []string{"ok", "pending", "publish", "marker"} {
		t.Run(mode, func(t *testing.T) {
			s := &fakeStore{ids: []uuid.UUID{uuid.New(), uuid.New()}}
			p := &fakePublisher{}
			switch mode {
			case "pending":
				s.pendingErr = secret
			case "publish":
				p.err = secret
			case "marker":
				s.markErr = secret
			}
			d := New(s, p, slog.New(slog.NewTextHandler(io.Discard, nil)))
			err := d.Once(t.Context())
			if mode == "ok" && (err != nil || p.calls != 2 || s.marks != 2) {
				t.Fatal(err, p.calls, s.marks)
			}
			if mode == "pending" && (err == nil || p.calls != 0 || s.marks != 0) {
				t.Fatal("DB failure published work")
			}
			if mode == "publish" && (err == nil || s.marks != 0) {
				t.Fatal("publication failure marked success")
			}
			if mode == "marker" && (err == nil || p.calls != 1 || s.marks != 1) {
				t.Fatal("marker boundary")
			}
		})
	}
}
func TestRunWaitsAndSanitizes(t *testing.T) {
	var logs strings.Builder
	s := &fakeStore{pendingErr: errors.New("password=secret raw driver")}
	p := &fakePublisher{}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	New(s, p, slog.New(slog.NewJSONHandler(&logs, nil))).Run(ctx)
	if s.reads != 1 || strings.Contains(logs.String(), "password") || !strings.Contains(logs.String(), "dispatch_failed") {
		t.Fatal(s.reads, logs.String())
	}
}
