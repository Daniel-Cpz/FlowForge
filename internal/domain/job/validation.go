package job

import (
	"encoding/json"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/capability"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Validate checks values, not execution policy or state-transition permissions.
// Services use it before persistence; repositories use it after reading data.
func (j *Job) Validate() error {
	if !capability.Canonical(j.RequiredCapabilities) || (j.ScheduleID == nil) != (j.ScheduledFor == nil) || (j.ScheduleID != nil && *j.ScheduleID == uuid.Nil) {
		return ErrInvalidInput
	}
	if j.ID == uuid.Nil || !j.Status.Valid() || !validTime(j.CreatedAt) ||
		!validText(j.Type, 128) || j.Priority < 0 || j.Priority > 100 ||
		j.AttemptCount < 0 || j.AttemptCount > j.MaxAttempts || j.MaxAttempts < 1 || j.MaxAttempts > 100 ||
		j.Timeout < 1 || j.Timeout > 86400 || !json.Valid(j.Payload) ||
		(len(j.Result) != 0 && !json.Valid(j.Result)) {
		return ErrInvalidInput
	}
	if j.IdempotencyKey != nil && !validText(*j.IdempotencyKey, 255) {
		return ErrInvalidInput
	}
	if j.AssignedWorker != nil && *j.AssignedWorker == uuid.Nil {
		return ErrInvalidInput
	}
	if (j.Status == Retrying) != (j.RetryAt != nil) || (j.Status == Retrying && (j.AssignedWorker != nil || j.LeaseExpiry != nil || j.AttemptCount >= j.MaxAttempts)) {
		return ErrInvalidInput
	}
	if j.CancelRequestedAt != nil && j.Status != Running && j.Status != Cancelled {
		return ErrInvalidInput
	}
	for _, timestamp := range []*time.Time{j.LeaseExpiry, j.RetryAt, j.StartedAt, j.FinishedAt, j.CancelRequestedAt, j.ScheduledAt, j.ScheduledFor} {
		if timestamp != nil && !validTime(*timestamp) {
			return ErrInvalidInput
		}
	}
	if j.StartedAt != nil && j.FinishedAt != nil && j.FinishedAt.Before(*j.StartedAt) {
		return ErrInvalidInput
	}
	return nil
}

func validText(s string, max int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" &&
		utf8.RuneCountInString(s) <= max && !strings.ContainsRune(s, '\x00')
}

func validTime(t time.Time) bool {
	return !t.IsZero() && t.UTC().Year() >= 1 && t.UTC().Year() <= 9999
}
