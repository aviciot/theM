package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// TenantSelfServiceHandler handles /tenant/ routes for tenant admins.
// All routes read the tenant ID from JWT claims (via tenantctx) — never from URL params.
type TenantSelfServiceHandler struct {
	db    *dal.DB
	audit *AuditWriter
}

// NewTenantSelfServiceHandler creates a TenantSelfServiceHandler.
func NewTenantSelfServiceHandler(db DBQuerier, audit *AuditWriter) *TenantSelfServiceHandler {
	return &TenantSelfServiceHandler{db: dal.NewDB(db), audit: audit}
}

// Routes mounts the self-service endpoints.
func (h *TenantSelfServiceHandler) Routes(r chi.Router) {
	r.Get("/tenant/settings", h.GetSettings)
	r.Patch("/tenant/settings", h.PatchSettings)
	r.Get("/tenant/quota", h.GetQuota)
}

// GetSettings handles GET /api/v1/tenant/settings.
// Returns the caller's own tenant info — tenant ID comes from JWT via tenantctx.
func (h *TenantSelfServiceHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	tenant, err := h.db.GetTenant(r.Context(), tenantID)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, tenant)
}

// PatchSettings handles PATCH /api/v1/tenant/settings.
// Allows tenant admins to update display_name, email_domain, and IDP config.
// Slug and enabled flag are enforced read-only — only super_admin can change those.
func (h *TenantSelfServiceHandler) PatchSettings(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	var in dal.TenantPatch
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Drop slug and enabled — self-service cannot change these.
	in.Enabled = nil
	detail, err := h.db.PatchTenant(r.Context(), tenantID, in)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	changes := changesOf(in)
	if in.SetIDP && in.IDPConfig != nil && in.IDPConfig.ClientSecret != "" {
		if changes == nil {
			changes = map[string]any{}
		}
		if idpMap, ok := changes["idp_config"].(map[string]any); ok {
			delete(idpMap, "client_secret")
		}
		changes["client_secret_changed"] = true
	}
	h.audit.Write(r.Context(), dal.AuditEntry{
		TenantID: tenantID, UserID: userIDPtr(r),
		Action: "tenant.self_patch", EntityType: "tenant", EntityID: tenantID, Actor: actorFromRequest(r),
		Changes: changes,
	})
	writeJSON(w, http.StatusOK, detail)
}

// GetQuota handles GET /api/v1/tenant/quota.
// Returns the caller's own tenant quota — tenant ID comes from JWT via tenantctx.
func (h *TenantSelfServiceHandler) GetQuota(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	q, err := h.db.GetQuota(r.Context(), tenantID)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "quota not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, q)
}
