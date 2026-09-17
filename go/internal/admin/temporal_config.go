package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// TemporalConfigHandler handles platform-level and per-app Temporal execution config routes.
type TemporalConfigHandler struct {
	svc *service.ConfigService
}

// NewTemporalConfigHandler creates a TemporalConfigHandler.
func NewTemporalConfigHandler(db DBQuerier) *TemporalConfigHandler {
	return &TemporalConfigHandler{svc: service.NewConfigService(dal.NewDB(db))}
}

// PlatformRoutes mounts GET and PUT for the platform-level Temporal config.
// Must be registered under a RequireSuperAdmin group.
func (h *TemporalConfigHandler) PlatformRoutes(r chi.Router) {
	r.Get("/temporal-config", h.GetPlatform)
	r.Put("/temporal-config", h.PutPlatform)
}

// AppRoutes mounts GET and PUT for per-app Temporal config.
// Must be registered under a RequireTenantAdmin group with {id} = application UUID.
func (h *TemporalConfigHandler) AppRoutes(r chi.Router) {
	r.Get("/temporal-config", h.GetApp)
	r.Put("/temporal-config", h.PutApp)
}

// GetPlatform handles GET /api/v1/admin/temporal-config.
func (h *TemporalConfigHandler) GetPlatform(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.GetTemporalPlatformConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// PutPlatform handles PUT /api/v1/admin/temporal-config.
func (h *TemporalConfigHandler) PutPlatform(w http.ResponseWriter, r *http.Request) {
	var body dal.TemporalConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	out, err := h.svc.PutTemporalPlatformConfig(r.Context(), body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GetApp handles GET /api/v1/admin/applications/{id}/temporal-config.
// Returns the effective merged config (app override → platform → hardcoded defaults).
func (h *TemporalConfigHandler) GetApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	cfg, err := h.svc.GetTemporalEffectiveConfig(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// PutApp handles PUT /api/v1/admin/applications/{id}/temporal-config.
// Stores per-app overrides and returns the effective merged config.
func (h *TemporalConfigHandler) PutApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	var body dal.TemporalConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	out, err := h.svc.PutTemporalAppConfig(r.Context(), appID, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
