package handler

import (
	"testing"
	"time"
)

func TestSchedulingEnvelope(t *testing.T) {
	for _, body := range []string{`{"type":"SLEEP","payload":{},"scheduled_at":null,"required_capabilities":null}`, `{"type":"SLEEP","payload":{},"scheduled_at":"2026-10-05T17:00:00+11:00","required_capabilities":["CPU","cpu"]}`} {
		in, err := decodeCreateInput([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if in.ScheduledAt != nil && !in.ScheduledAt.Equal(time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)) {
			t.Fatal(in.ScheduledAt)
		}
	}
	for _, body := range []string{`{"scheduled_at":5}`, `{"scheduled_at":"tomorrow"}`, `{"scheduled_at":"2026-10-05"}`, `{"required_capabilities":"cpu"}`, `{"scheduled_at":null,"scheduled_at":null}`, `{"schedule_id":"ignored"}`} {
		if _, err := decodeCreateInput([]byte(body)); err == nil {
			t.Fatal("accepted", body)
		}
	}
	for _, body := range []string{`{"interval_seconds":null}`, `{"interval_seconds":1.1}`, `{"interval_seconds":1,"interval_seconds":2}`, `{"interval_seconds":1,"idempotency_key":"key"}`, `{"interval_seconds":1,"scheduled_at":null}`} {
		if _, err := decodeScheduleInput([]byte(body)); err == nil {
			t.Fatal("accepted", body)
		}
	}
}
