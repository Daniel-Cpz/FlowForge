package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/google/uuid"
	"io"
	"net/http"
	"time"
)

func decodeScheduleInput(data []byte) (service.ScheduleInput, error) {
	var in service.ScheduleInput
	template := make(map[string]json.RawMessage)
	err := decodeObject(data, func(name string, raw json.RawMessage) error {
		switch name {
		case "interval_seconds":
			if bytes.Equal(raw, []byte("null")) {
				return errInvalidJSON
			}
			return json.Unmarshal(raw, &in.IntervalSeconds)
		case "next_run_at":
			if bytes.Equal(raw, []byte("null")) {
				return nil
			}
			var v string
			if err := decodeText(raw, &v); err != nil {
				return err
			}
			timestamp, err := time.Parse(time.RFC3339Nano, v)
			in.NextRunAt = &timestamp
			return err
		case "type", "payload", "priority", "max_attempts", "timeout", "required_capabilities":
			template[name] = raw
			return nil
		default:
			return errInvalidJSON
		}
	})
	if err != nil {
		return in, err
	}
	encoded, err := json.Marshal(template)
	if err != nil {
		return in, err
	}
	in.Template, err = decodeCreateInput(encoded)
	return in, err
}
func (h *Jobs) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.decodeError(w, err)
		return
	}
	in, err := decodeScheduleInput(body)
	if err != nil {
		h.decodeError(w, err)
		return
	}
	v, err := h.service.CreateSchedule(r.Context(), in)
	if err != nil {
		h.scheduleError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/schedules/"+v.ID.String())
	writeJSON(w, 201, v)
}
func (h *Jobs) GetSchedule(w http.ResponseWriter, r *http.Request)    { h.scheduleCommand(w, r, false) }
func (h *Jobs) CancelSchedule(w http.ResponseWriter, r *http.Request) { h.scheduleCommand(w, r, true) }
func (h *Jobs) scheduleCommand(w http.ResponseWriter, r *http.Request, cancel bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || len(r.PathValue("id")) != 36 || id == uuid.Nil {
		writeError(w, 400, "INVALID_ID", "invalid schedule UUID")
		return
	}
	if cancel {
		v, err := h.service.CancelSchedule(r.Context(), id)
		if err != nil {
			h.scheduleError(w, r, err)
			return
		}
		writeJSON(w, 200, v)
	} else {
		v, err := h.service.GetSchedule(r.Context(), id)
		if err != nil {
			h.scheduleError(w, r, err)
			return
		}
		writeJSON(w, 200, v)
	}
}
func (h *Jobs) scheduleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, 404, "SCHEDULE_NOT_FOUND", "schedule not found")
		return
	}
	if errors.Is(err, domain.ErrInvalidInput) {
		writeError(w, 400, "INVALID_INPUT", "invalid schedule input")
		return
	}
	h.fail(w, r, err)
}
