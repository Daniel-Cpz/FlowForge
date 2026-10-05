package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/google/uuid"
)

type Jobs struct {
	service *service.Service
	logger  *slog.Logger
}

func NewJobs(s *service.Service, logger *slog.Logger) *Jobs { return &Jobs{service: s, logger: logger} }

func writeJSON(w http.ResponseWriter, status int, data any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		slog.Error("response encoding failed", "event", "response_encoding_failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		slog.Debug("response write failed", "event", "response_write_failed")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (h *Jobs) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrControlConflict):
		writeError(w, 409, "JOB_CONTROL_CONFLICT", "job cannot accept this command")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		writeError(w, 409, "IDEMPOTENCY_CONFLICT", "idempotency key belongs to a different request")
	case errors.Is(err, domain.ErrInvalidInput):
		writeError(w, 400, "INVALID_INPUT", "invalid job input")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, 404, "JOB_NOT_FOUND", "job not found")
	default:
		// Driver errors may contain connection details or user data; log classification only.
		h.logger.ErrorContext(r.Context(), "job request failed", "event", "job_request_failed", "method", r.Method)
		writeError(w, 500, "INTERNAL_ERROR", "internal server error")
	}
}

func (h *Jobs) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	// MaxBytesReader bounds allocations even when Content-Length is absent.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.decodeError(w, err)
		return
	}
	in, err := decodeCreateInput(body)
	if err != nil {
		h.decodeError(w, err)
		return
	}
	j, disposition, err := h.service.CreateWithDisposition(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+j.ID.String())
	status := http.StatusCreated
	if disposition == domain.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, j)
}

func (h *Jobs) decodeError(w http.ResponseWriter, err error) {
	var sizeErr *http.MaxBytesError
	if errors.As(err, &sizeErr) {
		writeError(w, 413, "BODY_TOO_LARGE", "request body exceeds 1 MiB")
		return
	}
	writeError(w, 400, "INVALID_JSON", "expected one valid JSON object with exact, unique fields")
}

func (h *Jobs) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || len(r.PathValue("id")) != 36 {
		writeError(w, 400, "INVALID_ID", "invalid job UUID")
		return
	}
	j, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, 200, j)
}

func (h *Jobs) List(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, false)
}
func (h *Jobs) DeadLetter(w http.ResponseWriter, r *http.Request) { h.list(w, r, true) }
func (h *Jobs) list(w http.ResponseWriter, r *http.Request, dead bool) {
	limit := 20
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, 400, "INVALID_INPUT", "invalid pagination")
		return
	}
	for key, values := range query {
		if key == "cursor" && (len(values) != 1 || values[0] == "" || len(values[0]) > maxCursorLength) {
			writeError(w, 400, "INVALID_CURSOR", "invalid cursor")
			return
		}
		if (key != "limit" && key != "cursor") || len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			writeError(w, 400, "INVALID_INPUT", "invalid pagination")
			return
		}
	}
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
	}
	if err != nil {
		writeError(w, 400, "INVALID_INPUT", "invalid pagination")
		return
	}
	var after *domain.PageCursor
	if query.Has("cursor") {
		after, err = decodeCursor(query.Get("cursor"))
		if err != nil {
			writeError(w, 400, "INVALID_CURSOR", "invalid cursor")
			return
		}
	}
	var page *domain.Page
	if dead {
		page, err = h.service.ListDeadLetter(r.Context(), limit, after)
	} else {
		page, err = h.service.List(r.Context(), limit, after)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var next *string
	if page.NextCursor != nil {
		encoded, err := encodeCursor(*page.NextCursor)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		next = &encoded
	}
	writeJSON(w, 200, struct {
		Jobs       []domain.Job `json:"jobs"`
		NextCursor *string      `json:"next_cursor"`
	}{Jobs: page.Jobs, NextCursor: next})
}

func controlID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || len(r.PathValue("id")) != 36 || id == uuid.Nil {
		writeError(w, 400, "INVALID_ID", "invalid job UUID")
		return uuid.Nil, false
	}
	return id, true
}
func (h *Jobs) Cancel(w http.ResponseWriter, r *http.Request)  { h.command(w, r, false) }
func (h *Jobs) Redrive(w http.ResponseWriter, r *http.Request) { h.command(w, r, true) }
func (h *Jobs) command(w http.ResponseWriter, r *http.Request, redrive bool) {
	id, ok := controlID(w, r)
	if !ok {
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) != 0 || r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_INPUT", "command requires no body or query")
		return
	}
	var j *domain.Job
	event := "job_cancel_requested"
	if redrive {
		j, err = h.service.Redrive(r.Context(), id)
		event = "job_redriven"
	} else {
		j, err = h.service.Cancel(r.Context(), id)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.logger.InfoContext(r.Context(), "Job control persisted", "event", event, "job_id", id, "status", j.Status, "attempt_count", j.AttemptCount, "max_attempts", j.MaxAttempts)
	writeJSON(w, 200, j)
}
func (h *Jobs) Attempts(w http.ResponseWriter, r *http.Request) {
	id, ok := controlID(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_INPUT", "attempts does not accept query parameters")
		return
	}
	attempts, err := h.service.Attempts(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, 200, struct {
		Attempts []domain.Attempt `json:"attempts"`
	}{Attempts: attempts})
}

// RoutingError keeps unknown routes and unsupported methods in the same envelope.
func RoutingError(w http.ResponseWriter, r *http.Request, knownPath bool, allow string) {
	if knownPath {
		w.Header().Set("Allow", allow)
		writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	writeError(w, 404, "NOT_FOUND", "route not found")
}
