package httptransport

import (
	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
	"github.com/Daniel-Cpz/FlowForge/internal/transport/http/handler"
	"log/slog"
	"net/http"
)

func NewRouter(service *service.Service, logger *slog.Logger, checks ...handler.Check) http.Handler {
	mux := http.NewServeMux()
	jobs := handler.NewJobs(service, logger)
	mux.HandleFunc("GET /health", handler.Health)
	mux.HandleFunc("GET /ready", handler.Ready(checks...))
	mux.HandleFunc("POST /api/v1/jobs", jobs.Create)
	mux.HandleFunc("GET /api/v1/jobs", jobs.List)
	mux.HandleFunc("GET /api/v1/jobs/{id}", jobs.Get)
	mux.HandleFunc("GET /api/v1/dead-letter", jobs.DeadLetter)
	mux.HandleFunc("GET /api/v1/jobs/{id}/attempts", jobs.Attempts)
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", jobs.Cancel)
	mux.HandleFunc("POST /api/v1/jobs/{id}/retry", jobs.Redrive)
	mux.HandleFunc("POST /api/v1/schedules", jobs.CreateSchedule)
	mux.HandleFunc("GET /api/v1/schedules/{id}", jobs.GetSchedule)
	mux.HandleFunc("POST /api/v1/schedules/{id}/cancel", jobs.CancelSchedule)
	for path, allow := range map[string]string{"/api/v1/schedules": "POST", "/api/v1/schedules/{id}": "GET, HEAD", "/api/v1/schedules/{id}/cancel": "POST"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { handler.RoutingError(w, r, true, allow) })
	}
	for path, allow := range map[string]string{"/health": "GET, HEAD", "/ready": "GET, HEAD", "/api/v1/jobs": "GET, HEAD, POST", "/api/v1/jobs/{id}": "GET, HEAD", "/api/v1/dead-letter": "GET, HEAD", "/api/v1/jobs/{id}/attempts": "GET, HEAD", "/api/v1/jobs/{id}/cancel": "POST", "/api/v1/jobs/{id}/retry": "POST"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { handler.RoutingError(w, r, true, allow) })
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { handler.RoutingError(w, r, false, "") })
	return mux
}
