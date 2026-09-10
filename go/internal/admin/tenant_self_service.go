package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// TenantSelfServiceHandler handles /tenant/ routes for tenant admins.
// All routes read the tenant ID from JWT claims (via tenantctx) — never from URL params.
type TenantSelfServiceHandler struct {
	db    *dal.DB
	audit *AuditWriter
	redis rueidis.Client // nil when Redis unavailable; debug reads return 404
}

// NewTenantSelfServiceHandler creates a TenantSelfServiceHandler.
// idpKey is the AES-256 encryption key for IdP client_secret; nil disables encryption.
// rc is the Redis client for OIDC debug reads; nil skips debug reads.
func NewTenantSelfServiceHandler(db DBQuerier, audit *AuditWriter, idpKey []byte, rc rueidis.Client) *TenantSelfServiceHandler {
	return &TenantSelfServiceHandler{db: dal.NewDB(db).WithIDPKey(idpKey), audit: audit, redis: rc}
}

// Routes mounts the self-service endpoints.
func (h *TenantSelfServiceHandler) Routes(r chi.Router) {
	r.Get("/tenant/settings", h.GetSettings)
	r.Patch("/tenant/settings", h.PatchSettings)
	r.Get("/tenant/quota", h.GetQuota)
	r.Get("/tenant/members", h.GetMyMembers)
	r.Patch("/tenant/members/{user_id}", h.PatchMyMember)
	r.Get("/tenant/group-mappings", h.ListMyGroupMappings)
	r.Put("/tenant/group-mappings", h.UpsertMyGroupMapping)
	r.Delete("/tenant/group-mappings/{mapping_id}", h.DeleteMyGroupMapping)
	r.Get("/tenant/oidc-debug", h.GetOIDCDebug)
	r.Get("/tenant/runtime-idp", h.GetRuntimeIDP)
	r.Put("/tenant/runtime-idp", h.PutRuntimeIDP)
	r.Delete("/tenant/runtime-idp", h.DeleteRuntimeIDP)
}

// GetSettings handles GET /api/v1/tenant/settings.
// Returns the caller's own tenant info including IDP config fields (secret blanked).
// Tenant ID comes from JWT via tenantctx.
func (h *TenantSelfServiceHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	tenant, err := h.db.GetTenantDetail(r.Context(), tenantID)
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

// GetMyMembers handles GET /api/v1/tenant/members.
// Returns membership list for the caller's own tenant — tenant ID from JWT via tenantctx.
// Requires admin or super_admin membership role (enforced by RequireTenantAdmin middleware).
func (h *TenantSelfServiceHandler) GetMyMembers(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	members, err := h.db.ListMembers(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, members)
}

// PatchMyMember handles PATCH /api/v1/tenant/members/{user_id}.
// Allows tenant admins to update the membership role of any member in their own tenant.
// Only role changes are accepted (viewer | member | admin).
func (h *TenantSelfServiceHandler) PatchMyMember(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	userIDStr := chi.URLParam(r, "user_id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	switch in.Role {
	case "viewer", "member", "admin":
	default:
		writeError(w, http.StatusBadRequest, "role must be viewer, member, or admin")
		return
	}
	if err := h.db.UpdateMemberRole(r.Context(), tenantID, userID, in.Role); dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "membership not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// ListMyGroupMappings handles GET /api/v1/tenant/group-mappings.
// Returns all group mappings for the caller's own tenant.
func (h *TenantSelfServiceHandler) ListMyGroupMappings(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	mappings, err := h.db.ListGroupMappings(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, mappings)
}

// UpsertMyGroupMapping handles PUT /api/v1/tenant/group-mappings.
// Creates or updates a group mapping for the caller's own tenant.
// super_admin role is rejected — OIDC mappings cannot grant platform super_admin.
func (h *TenantSelfServiceHandler) UpsertMyGroupMapping(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	var in dal.GroupMappingInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.GroupClaim == "" {
		writeError(w, http.StatusBadRequest, "group_claim is required")
		return
	}
	switch in.Role {
	case "admin", "member", "viewer":
	default:
		writeError(w, http.StatusBadRequest, "role must be admin, member, or viewer")
		return
	}
	m, err := h.db.UpsertGroupMapping(r.Context(), tenantID, in)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// DeleteMyGroupMapping handles DELETE /api/v1/tenant/group-mappings/{mapping_id}.
// Removes a group mapping by ID — tenant isolation enforced at DB layer (tenant_id filter).
func (h *TenantSelfServiceHandler) DeleteMyGroupMapping(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	mappingID := chi.URLParam(r, "mapping_id")
	if mappingID == "" {
		writeError(w, http.StatusBadRequest, "mapping_id is required")
		return
	}
	if err := h.db.DeleteGroupMapping(r.Context(), tenantID, mappingID); dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetOIDCDebug handles GET /api/v1/tenant/oidc-debug?email={email}.
// Returns the most recent SSO login debug record for the given email in the caller's tenant.
// Only tenant admins may call this — viewers get 403 via RequireTenantAdmin middleware.
// Returns 404 when no record exists (key expired or login never occurred).
func (h *TenantSelfServiceHandler) GetOIDCDebug(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if email == "" {
		writeError(w, http.StatusBadRequest, "email query parameter is required")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if h.redis == nil {
		writeError(w, http.StatusServiceUnavailable, "debug log unavailable")
		return
	}
	rec, err := h.readOIDCDebug(r.Context(), tenantID, email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "debug read error")
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "no debug record found")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// oidcDebugKey returns the Redis key for an OIDC debug record.
// Must match the key format used in authserver.oidcDebugKey.
func oidcDebugKey(tenantID, email string) string {
	return "them:oidc:last_login:" + tenantID + ":" + email
}

// readOIDCDebug reads the stored OIDC debug record from Redis for (tenantID, email).
// Returns nil when no record exists (key expired or never written).
func (h *TenantSelfServiceHandler) readOIDCDebug(ctx context.Context, tenantID, email string) (map[string]any, error) {
	key := oidcDebugKey(tenantID, email)
	cmd := h.redis.B().Get().Key(key).Build()
	res := h.redis.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}
		return nil, err
	}
	raw, err := res.AsBytes()
	if err != nil {
		return nil, err
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return rec, nil
}
