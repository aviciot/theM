package mcp

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aviciot/them/internal/health"
)

// NewRouter builds the chi router for them-mcp-service.
//
// Routes:
//
//	GET  /health/live                 — liveness (always 200)
//	GET  /health/ready                — readiness (probes DB + Redis)
//	POST /internal/probe/{server_id}  — on-demand health + discovery probe
//	POST /internal/execute            — tool call (called by them-go-bridge)
func NewRouter(
	healthHandler *health.Handler,
	supervisor *Supervisor,
	executor *Executor,
	version string,
) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	r.Get("/health/live", healthHandler.Live)
	r.Get("/health/ready", healthHandler.Ready)

	// Internal API — only reachable on them-network, never via Traefik.
	r.Route("/internal", func(r chi.Router) {
		r.Post("/probe/{server_id}", makeProbeHandler(supervisor))
		r.Post("/execute", makeExecuteHandler(executor))
	})

	return r
}

// ProbeResponse is the JSON body returned by POST /internal/probe/{server_id}.
type ProbeResponse struct {
	HealthStatus string `json:"health_status"`
	ToolsCount   int    `json:"tools_count"`
	LastError    string `json:"last_error,omitempty"`
}

func makeProbeHandler(supervisor *Supervisor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := chi.URLParam(r, "server_id")
		if serverID == "" {
			writeError(w, http.StatusBadRequest, "server_id is required")
			return
		}

		updated, err := supervisor.ProbeNow(r.Context(), serverID)
		if err != nil {
			writeError(w, http.StatusNotFound, "server not found: "+err.Error())
			return
		}

		var tools []Tool
		_ = json.Unmarshal(updated.ToolsManifest, &tools)

		writeJSON(w, http.StatusOK, ProbeResponse{
			HealthStatus: updated.HealthStatus,
			ToolsCount:   len(tools),
			LastError:    updated.LastError,
		})
	}
}

func makeExecuteHandler(executor *Executor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}

		resp := executor.Execute(r.Context(), req)
		if resp.Error != "" {
			writeJSON(w, http.StatusUnprocessableEntity, resp)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
