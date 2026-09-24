package admin

// AppFlowDebugPresetsHandler lets a user save/load/delete their own named
// presets (entry point, test message, step mode, per-node LLM overrides) for
// the AppFlow debug panel (docs/APP_CANVAS_DEBUG_PLAN.md). Presets are
// personal — scoped to (tenant, user, application) — never shared with other
// users, even within the same tenant/app. See
// docs/UNIFIED_ROLE_GOVERNANCE_DESIGN.md for why this is deliberately not
// folded into the tenant Role model.

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/tenantctx"
)

// AppFlowDebugPresetsHandler owns the HTTP layer for debug-panel presets.
type AppFlowDebugPresetsHandler struct {
	svc *service.AppFlowDebugPresetService
}

// NewAppFlowDebugPresetsHandler creates an AppFlowDebugPresetsHandler.
func NewAppFlowDebugPresetsHandler(db DBQuerier, secretKey string) *AppFlowDebugPresetsHandler {
	return &AppFlowDebugPresetsHandler{
		svc: service.NewAppFlowDebugPresetService(dal.NewDB(db), secretKey),
	}
}

// AppRoutes mounts the preset routes under the tenant-scoped
// /applications/{id} group (RequireTenantAdmin + AdminTenantMiddleware
// already applied by the caller, same as AppFlowDebugHandler.AppRoutes).
func (h *AppFlowDebugPresetsHandler) AppRoutes(r chi.Router) {
	r.Get("/debug/presets", h.List)
	r.Post("/debug/presets", h.Save)
	r.Delete("/debug/presets/{preset_id}", h.Delete)
}

func userIDFromCtx(r *http.Request) int64 {
	if claims, ok := auth.ClaimsFromCtx(r.Context()); ok {
		return claims.UserID
	}
	return 0
}

// List handles GET /admin/applications/{id}/debug/presets.
func (h *AppFlowDebugPresetsHandler) List(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(appID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	userID := userIDFromCtx(r)

	out, err := h.svc.List(r.Context(), tenantID, userID, appID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Save handles POST /admin/applications/{id}/debug/presets — creates or
// replaces (by name) a preset for the caller. The request body is NOT
// logged — it may contain plaintext api_key values in llm_overrides.
func (h *AppFlowDebugPresetsHandler) Save(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(appID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	userID := userIDFromCtx(r)

	var body service.AppFlowDebugPresetIn
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	out, err := h.svc.Save(r.Context(), tenantID, userID, appID, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /admin/applications/{id}/debug/presets/{preset_id}.
func (h *AppFlowDebugPresetsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	presetID := chi.URLParam(r, "preset_id")
	if _, err := uuid.Parse(presetID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid preset id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	userID := userIDFromCtx(r)

	if err := h.svc.Delete(r.Context(), tenantID, userID, presetID); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
