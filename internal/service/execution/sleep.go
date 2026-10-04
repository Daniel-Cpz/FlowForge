package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"io"
	"time"
)

type Outcome struct {
	Status job.Status
	Result json.RawMessage
}
type Executor interface {
	Execute(context.Context, *job.Job) Outcome
}
type Sleep struct{}

func failure(code string) Outcome {
	data, _ := json.Marshal(map[string]string{"error": code})
	return Outcome{job.Failed, data}
}

// SLEEP accepts exactly one duration_ms integer field, 0..10000 inclusive.
// PostgreSQL already canonicalizes nested duplicate JSON keys at API creation.
func (Sleep) Execute(ctx context.Context, j *job.Job) Outcome {
	if j.Type != "SLEEP" {
		return failure("unsupported_job_type")
	}
	var payload map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(j.Payload))
	if dec.Decode(&payload) != nil || len(payload) != 1 {
		return failure("invalid_sleep_payload")
	}
	var duration *int
	if json.Unmarshal(payload["duration_ms"], &duration) != nil || duration == nil || *duration < 0 || *duration > 10000 {
		return failure("invalid_sleep_payload")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return failure("invalid_sleep_payload")
	}
	if ctx.Err() != nil {
		return failure("execution_cancelled")
	}
	timer := time.NewTimer(time.Duration(*duration) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return failure("execution_cancelled")
	case <-timer.C:
	}
	if ctx.Err() != nil {
		return failure("execution_cancelled")
	}
	result, _ := json.Marshal(map[string]any{"outcome": "slept", "duration_ms": *duration})
	return Outcome{job.Succeeded, result}
}
