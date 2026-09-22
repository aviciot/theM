package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// LogVerbosityHandler handles the per-app AppFlow trace log-verbosity route
// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4).
type LogVerbosityHandler struct {
	svc *service.ConfigService
}

// NewLogVerbosityHandler creates a LogVerbosityHandler.
func NewLogVerbosityHandler(db DBQuerier) *LogVerbosityHandler {
	return &LogVerbosityHandler{svc: service.NewConfigService(dal.NewDB(db))}
}

// AppRoutes mounts GET and PUT for the per-app log-verbosity setting.
// Must be registered under a RequireTenantAdmin group with {id} = application UUID.
func (h *LogVerbosityHandler) AppRoutes(r chi.Router) {
	r.Get("/log-verbosity", h.GetApp)
	r.Put("/log-verbosity", h.PutApp)
}

type logVerbosityBody struct {
	LogVerbosity string `json:"log_verbosity"`
}

// GetApp handles GET /api/v1/admin/applications/{id}/log-verbosity.
func (h *LogVerbosityHandler) GetApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	v, err := h.svc.GetLogVerbosity(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, logVerbosityBody{LogVerbosity: v})
}

// PutApp handles PUT /api/v1/admin/applications/{id}/log-verbosity.
func (h *LogVerbosityHandler) PutApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	var body logVerbosityBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	v, err := h.svc.PutLogVerbosity(r.Context(), appID, body.LogVerbosity)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, logVerbosityBody{LogVerbosity: v})
}
