package execution

import (
	"context"
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"testing"
)

func TestSleepFailureClassification(t *testing.T) {
	for _, tc := range []struct{ typ, payload, code string }{{"OTHER", `{}`, "unsupported_job_type"}, {"SLEEP", `{}`, "invalid_sleep_payload"}, {"SLEEP", `{"duration_ms":-1}`, "invalid_sleep_payload"}} {
		out := (Sleep{}).Execute(t.Context(), &job.Job{Type: tc.typ, Payload: json.RawMessage(tc.payload)})
		if out.Status != job.Failed || out.Failure.Class != job.Permanent || out.Failure.Code != tc.code {
			t.Fatal(out)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out := (Sleep{}).Execute(ctx, &job.Job{Type: "SLEEP", Payload: json.RawMessage(`{"duration_ms":0}`)})
	if out.Failure.Class != job.Retryable || out.Failure.Code != "execution_cancelled" {
		t.Fatal(out)
	}
}
