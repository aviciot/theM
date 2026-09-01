package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/middleware"
	"github.com/aviciot/them/internal/tenantctx"
)

// SecurityConfigHandler handles GET/PUT /admin/applications/{id}/security-config.
type SecurityConfigHandler struct {
	db    DBQuerier
	redis rueidis.Client // may be nil — no-op when absent
}

// NewSecurityConfigHandler creates a SecurityConfigHandler.
func NewSecurityConfigHandler(db DBQuerier, redis rueidis.Client) *SecurityConfigHandler {
	return &SecurityConfigHandler{db: db, redis: redis}
}

// Routes mounts GET and PUT under the applications sub-tree.
// Must be called with a router that already has {id} in scope (the application ID).
func (h *SecurityConfigHandler) Routes(r chi.Router) {
	r.Get("/applications/{id}/security-config", h.Get)
	r.Put("/applications/{id}/security-config", h.Put)
}

// Get handles GET /admin/applications/{id}/security-config.
func (h *SecurityConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	raw, err := h.loadRaw(r, tenantID, appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, raw)
}

// Put handles PUT /admin/applications/{id}/security-config.
func (h *SecurityConfigHandler) Put(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	var cfg middleware.SecurityConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	cfg = middleware.MergeDefaults(cfg)
	if err := middleware.Validate(cfg); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal error")
		return
	}

	const q = `
UPDATE them.applications
SET    security_config = $3::jsonb, updated_at = now()
WHERE  id = $1::uuid AND tenant_id = $2::uuid`
	if err := h.db.Exec(r.Context(), q, appID, tenantID, b); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Invalidate gateway security config caches for this application.
	if h.redis != nil {
		_ = h.redis.Do(r.Context(), h.redis.B().Publish().
			Channel("them:security_config:invalidated:"+appID).
			Message("1").Build()).Error()
	}

	writeJSON(w, http.StatusOK, cfg)
}

// loadRaw loads and merges the security config for the application.
func (h *SecurityConfigHandler) loadRaw(r *http.Request, tenantID, appID string) (middleware.SecurityConfig, error) {
	const q = `
SELECT COALESCE(security_config, '{}')
FROM   them.applications
WHERE  id = $1::uuid AND tenant_id = $2::uuid`
	var raw []byte
	if err := h.db.QueryRow(r.Context(), q, appID, tenantID).Scan(&raw); err != nil {
		return middleware.DefaultSecurityConfig(), nil
	}
	var cfg middleware.SecurityConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return middleware.DefaultSecurityConfig(), nil
	}
	return middleware.MergeDefaults(cfg), nil
}
