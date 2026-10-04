package execution

import (
	"context"
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"strings"
	"testing"
	"time"
)

func TestSleepContract(t *testing.T) {
	for _, payload := range []string{`null`, `[]`, `{}`, `{"duration_ms":null}`, `{"duration_ms":-1}`, `{"duration_ms":10001}`, `{"duration_ms":1.0}`, `{"duration_ms":1e0}`, `{"duration_ms":"1"}`, `{"duration_ms":true}`, `{"Duration_ms":0}`, `{"duration_ms":0,"other":1}`, `{"duration_ms":0} {}`, `bad`} {
		t.Run(payload, func(t *testing.T) {
			out := (Sleep{}).Execute(t.Context(), &job.Job{Type: "SLEEP", Payload: json.RawMessage(payload)})
			if out.Status != job.Failed || !strings.Contains(string(out.Result), "invalid_sleep_payload") {
				t.Fatal(out)
			}
		})
	}
	for _, duration := range []string{"0", "1"} {
		out := (Sleep{}).Execute(t.Context(), &job.Job{Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":` + duration + `}`)})
		if out.Status != job.Succeeded || !strings.Contains(string(out.Result), "slept") {
			t.Fatal(out)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out := (Sleep{}).Execute(ctx, &job.Job{Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":10000}`)})
	if out.Status != job.Failed || !strings.Contains(string(out.Result), "execution_cancelled") {
		t.Fatal(out)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	out = (Sleep{}).Execute(ctx, &job.Job{Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":10000}`)})
	if out.Status != job.Failed || time.Since(started) > time.Second {
		t.Fatal("uncancellable sleep", out)
	}
	out = (Sleep{}).Execute(t.Context(), &job.Job{Type: "sleep", Payload: json.RawMessage(`{}`)})
	if !strings.Contains(string(out.Result), "unsupported_job_type") {
		t.Fatal(out)
	}
}
