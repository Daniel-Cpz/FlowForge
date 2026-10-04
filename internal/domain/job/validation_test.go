package job

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateJob(t *testing.T) {
	valid := Job{ID: uuid.New(), Type: "x", Status: Queued, Payload: json.RawMessage(`null`), MaxAttempts: 3,
		Timeout: 300, CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, corrupt := range map[string]func(*Job){
		"nil_id":                     func(j *Job) { j.ID = uuid.Nil },
		"unknown_status":             func(j *Job) { j.Status = "CORRUPT" },
		"zero_created_at":            func(j *Job) { j.CreatedAt = time.Time{} },
		"unrepresentable_created_at": func(j *Job) { j.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"priority":                   func(j *Job) { j.Priority = -1 },
		"attempt_count":              func(j *Job) { j.AttemptCount = -1 },
		"max_attempts":               func(j *Job) { j.MaxAttempts = 0 },
		"timeout":                    func(j *Job) { j.Timeout = 0 },
		"blank_type":                 func(j *Job) { j.Type = " " },
		"payload":                    func(j *Job) { j.Payload = nil },
		"result":                     func(j *Job) { j.Result = json.RawMessage(`{`) },
		"blank_key":                  func(j *Job) { key := " "; j.IdempotencyKey = &key },
		"nil_worker_id":              func(j *Job) { id := uuid.Nil; j.AssignedWorker = &id },
		"zero_optional_time":         func(j *Job) { timestamp := time.Time{}; j.FinishedAt = &timestamp },
		"backwards_time": func(j *Job) {
			start, end := j.CreatedAt, j.CreatedAt.Add(-time.Second)
			j.StartedAt, j.FinishedAt = &start, &end
		},
	} {
		t.Run(name, func(t *testing.T) {
			j := valid
			corrupt(&j)
			if !errors.Is(j.Validate(), ErrInvalidInput) {
				t.Fatal("invalid domain data accepted")
			}
		})
	}
}
