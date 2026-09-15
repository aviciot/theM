package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// MiddlewareWiringsHandler handles CRUD for middleware_wirings under an application.
// Routes mounted at /admin/applications/{id}/middleware-wirings.
type MiddlewareWiringsHandler struct {
	db    DBQuerier
	redis rueidis.Client // may be nil
}

// NewMiddlewareWiringsHandler creates a MiddlewareWiringsHandler.
func NewMiddlewareWiringsHandler(db DBQuerier, redis rueidis.Client) *MiddlewareWiringsHandler {
	return &MiddlewareWiringsHandler{db: db, redis: redis}
}

// MountOn attaches routes to r, sharing the /applications/{id} sub-tree.
func (h *MiddlewareWiringsHandler) MountOn(r chi.Router) {
	r.Get("/middleware-wirings", h.List)
	r.Post("/middleware-wirings", h.Create)
	r.Get("/middleware-wirings/{wiring_id}", h.Get)
	r.Put("/middleware-wirings/{wiring_id}", h.Update)
	r.Delete("/middleware-wirings/{wiring_id}", h.Delete)
}

// List handles GET /admin/applications/{id}/middleware-wirings.
func (h *MiddlewareWiringsHandler) List(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing application id")
		return
	}
	_ = tenantctx.MustTenantIDFromCtx(r.Context()) // ensures tenant context is set

	wirings, err := dal.ListMiddlewareWirings(r.Context(), h.db, appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, wirings)
}

// Get handles GET /admin/applications/{id}/middleware-wirings/{wiring_id}.
func (h *MiddlewareWiringsHandler) Get(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	wiringID := chi.URLParam(r, "wiring_id")
	_ = tenantctx.MustTenantIDFromCtx(r.Context())

	wiring, err := dal.GetMiddlewareWiring(r.Context(), h.db, appID, wiringID)
	if err != nil {
		writeError(w, http.StatusNotFound, "wiring not found")
		return
	}
	writeJSON(w, http.StatusOK, wiring)
}

// Create handles POST /admin/applications/{id}/middleware-wirings.
func (h *MiddlewareWiringsHandler) Create(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	_ = tenantctx.MustTenantIDFromCtx(r.Context())

	var in dal.MiddlewareWiringInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if in.DefSlug == "" {
		in.DefSlug = "file-guard" // default to file-guard if not specified
	}

	wiring, err := dal.CreateMiddlewareWiring(r.Context(), h.db, appID, in)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	h.invalidate(r, appID)
	writeJSON(w, http.StatusCreated, wiring)
}

// Update handles PUT /admin/applications/{id}/middleware-wirings/{wiring_id}.
func (h *MiddlewareWiringsHandler) Update(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	wiringID := chi.URLParam(r, "wiring_id")
	_ = tenantctx.MustTenantIDFromCtx(r.Context())

	var in dal.MiddlewareWiringInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	wiring, err := dal.UpdateMiddlewareWiring(r.Context(), h.db, appID, wiringID, in)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	h.invalidate(r, appID)
	writeJSON(w, http.StatusOK, wiring)
}

// Delete handles DELETE /admin/applications/{id}/middleware-wirings/{wiring_id}.
func (h *MiddlewareWiringsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	wiringID := chi.URLParam(r, "wiring_id")
	_ = tenantctx.MustTenantIDFromCtx(r.Context())

	if err := dal.DeleteMiddlewareWiring(r.Context(), h.db, appID, wiringID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	h.invalidate(r, appID)
	w.WriteHeader(http.StatusNoContent)
}

// invalidate publishes a Redis cache-invalidation event for the application's
// security config so the gateway's FileGate cache is evicted.
func (h *MiddlewareWiringsHandler) invalidate(r *http.Request, appID string) {
	if h.redis == nil {
		return
	}
	_ = h.redis.Do(r.Context(), h.redis.B().Publish().
		Channel("them:security_config:invalidated:"+appID).
		Message("1").Build()).Error()
}
