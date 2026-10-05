package handler

import (
	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/schedule"
	"github.com/Daniel-Cpz/FlowForge/internal/domain/worker"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strconv"
)

func (h *Jobs) Summary(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_INPUT", "summary accepts no query")
		return
	}
	v, err := h.service.DashboardSummary(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}
func pagination(r *http.Request) (int, string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, "", domain.ErrInvalidInput
	}
	for key, v := range q {
		if (key != "limit" && key != "cursor") || len(v) != 1 || v[0] == "" {
			return 0, "", domain.ErrInvalidInput
		}
	}
	limit := 20
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
	}
	if err != nil || limit < 1 || limit > 100 {
		return 0, "", domain.ErrInvalidInput
	}
	return limit, q.Get("cursor"), nil
}
func (h *Jobs) Workers(w http.ResponseWriter, r *http.Request) {
	limit, encoded, err := pagination(r)
	var after *uuid.UUID
	if err == nil && encoded != "" {
		id, e := uuid.Parse(encoded)
		if e != nil || id == uuid.Nil || id.String() != encoded {
			err = domain.ErrInvalidInput
		} else {
			after = &id
		}
	}
	if err != nil {
		writeError(w, 400, "INVALID_INPUT", "invalid pagination")
		return
	}
	values, next, err := h.service.Workers(r.Context(), limit, after)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var cursor *string
	if next != nil {
		s := next.String()
		cursor = &s
	}
	writeJSON(w, 200, struct {
		Workers []worker.Worker `json:"workers"`
		Next    *string         `json:"next_cursor"`
	}{values, cursor})
}
func (h *Jobs) Schedules(w http.ResponseWriter, r *http.Request) {
	limit, encoded, err := pagination(r)
	var after *domain.PageCursor
	if err == nil && encoded != "" {
		after, err = decodeCursor(encoded)
	}
	if err != nil {
		writeError(w, 400, "INVALID_INPUT", "invalid pagination")
		return
	}
	values, next, err := h.service.Schedules(r.Context(), limit, after)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var cursor *string
	if next != nil {
		s, e := encodeCursor(*next)
		if e != nil {
			h.fail(w, r, e)
			return
		}
		cursor = &s
	}
	writeJSON(w, 200, struct {
		Schedules []schedule.Schedule `json:"schedules"`
		Next      *string             `json:"next_cursor"`
	}{values, cursor})
}
