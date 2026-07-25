package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// MonitoringConfigHandler handles /admin/monitoring-config routes.
type MonitoringConfigHandler struct {
	svc *service.ConfigService
}

// NewMonitoringConfigHandler creates a MonitoringConfigHandler.
func NewMonitoringConfigHandler(db DBQuerier) *MonitoringConfigHandler {
	return &MonitoringConfigHandler{svc: service.NewConfigService(dal.NewDB(db))}
}

// Routes mounts GET and PUT for monitoring-config.
func (h *MonitoringConfigHandler) Routes(r chi.Router) {
	r.Get("/monitoring-config", h.Get)
	r.Put("/monitoring-config", h.Put)
}

// Get handles GET /api/v1/admin/monitoring-config.
func (h *MonitoringConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.GetMonitoring(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// Put handles PUT /api/v1/admin/monitoring-config.
func (h *MonitoringConfigHandler) Put(w http.ResponseWriter, r *http.Request) {
	var body service.MonitoringConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	out, err := h.svc.PutMonitoring(r.Context(), body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
