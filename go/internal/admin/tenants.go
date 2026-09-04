package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
)

// TenantsHandler handles /admin/tenants routes.
// All routes require super_admin (enforced by the outer router group).
// Tenants are platform-global — no AdminTenantMiddleware is applied here.
type TenantsHandler struct {
	db *dal.DB
}

// NewTenantsHandler creates a TenantsHandler.
func NewTenantsHandler(db DBQuerier) *TenantsHandler {
	return &TenantsHandler{db: dal.NewDB(db)}
}

// Routes mounts the tenant CRUD + quota + member + group-mapping endpoints.
func (h *TenantsHandler) Routes(r chi.Router) {
	r.Get("/tenants", h.List)
	r.Post("/tenants", h.Create)
	r.Get("/tenants/{id}", h.Get)
	r.Patch("/tenants/{id}", h.Patch)
	r.Get("/tenants/{id}/quota", h.GetQuota)
	r.Put("/tenants/{id}/quota", h.UpsertQuota)
	r.Get("/tenants/{id}/members", h.ListMembers)
	r.Post("/tenants/{id}/members", h.AddMember)
	r.Get("/tenants/{id}/group-mappings", h.ListGroupMappings)
	r.Put("/tenants/{id}/group-mappings", h.UpsertGroupMapping)
	r.Delete("/tenants/{id}/group-mappings/{mapping_id}", h.DeleteGroupMapping)
}

// List handles GET /api/v1/admin/tenants.
func (h *TenantsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.db.ListTenants(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// Get handles GET /api/v1/admin/tenants/{id}.
func (h *TenantsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	tenant, err := h.db.GetTenant(r.Context(), id)
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

// Create handles POST /api/v1/admin/tenants.
func (h *TenantsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in dal.TenantInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in.Slug = strings.TrimSpace(in.Slug)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.Slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	if in.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	tenant, err := h.db.CreateTenant(r.Context(), in)
	if dal.IsUniqueViolation(err) {
		writeError(w, http.StatusConflict, "tenant slug already exists")
		return
	}
	if dal.IsCheckViolation(err) {
		writeError(w, http.StatusBadRequest, "slug must match ^[a-z0-9_-]{1,64}$")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, tenant)
}

// Patch handles PATCH /api/v1/admin/tenants/{id}.
func (h *TenantsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	var in dal.TenantPatch
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	detail, err := h.db.PatchTenant(r.Context(), id, in)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// GetQuota handles GET /api/v1/admin/tenants/{id}/quota.
// Returns 404 when no quota row has been provisioned for the tenant.
func (h *TenantsHandler) GetQuota(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	q, err := h.db.GetQuota(r.Context(), id)
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

// UpsertQuota handles PUT /api/v1/admin/tenants/{id}/quota.
// Creates the quota row when absent; replaces all fields when present.
func (h *TenantsHandler) UpsertQuota(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	var in dal.TenantQuota
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	validPlans := map[string]bool{"trial": true, "starter": true, "pro": true, "enterprise": true}
	if !validPlans[in.Plan] {
		writeError(w, http.StatusBadRequest, "plan must be one of trial, starter, pro, enterprise")
		return
	}
	in.TenantID = id
	q, err := h.db.UpsertQuota(r.Context(), in)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// ListMembers handles GET /api/v1/admin/tenants/{id}/members.
func (h *TenantsHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	members, err := h.db.ListMembers(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, members)
}

// AddMember handles POST /api/v1/admin/tenants/{id}/members.
// Adds a user to the tenant with the given role.
// Enforces the max_users quota: fails-open when no quota row exists or on DB error.
func (h *TenantsHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	var in dal.TenantMemberInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.UserID == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if in.Role == "" {
		writeError(w, http.StatusBadRequest, "role is required")
		return
	}
	// Quota check: fail-open on missing quota row or DB errors.
	if q, err := h.db.GetQuota(r.Context(), id); err == nil && q.MaxUsers != nil {
		if count, err := h.db.CountTenantMembers(r.Context(), id); err == nil && count >= *q.MaxUsers {
			writeError(w, http.StatusTooManyRequests, "max_users quota exceeded")
			return
		}
	}
	m, err := h.db.AddMember(r.Context(), id, in)
	if dal.IsUniqueViolation(err) {
		writeError(w, http.StatusConflict, "user is already a member of this tenant")
		return
	}
	if dal.IsForeignKeyViolation(err) {
		writeError(w, http.StatusBadRequest, "user_id does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// ListGroupMappings handles GET /api/v1/admin/tenants/{id}/group-mappings.
func (h *TenantsHandler) ListGroupMappings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	mappings, err := h.db.ListGroupMappings(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, mappings)
}

// UpsertGroupMapping handles PUT /api/v1/admin/tenants/{id}/group-mappings.
// Creates or updates the mapping for the given group_claim within this tenant.
func (h *TenantsHandler) UpsertGroupMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	var in dal.GroupMappingInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in.GroupClaim = strings.TrimSpace(in.GroupClaim)
	if in.GroupClaim == "" {
		writeError(w, http.StatusBadRequest, "group_claim is required")
		return
	}
	validRoles := map[string]bool{"viewer": true, "member": true, "admin": true, "super_admin": true}
	if !validRoles[in.Role] {
		writeError(w, http.StatusBadRequest, "role must be one of viewer, member, admin, super_admin")
		return
	}
	m, err := h.db.UpsertGroupMapping(r.Context(), id, in)
	if dal.IsForeignKeyViolation(err) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// DeleteGroupMapping handles DELETE /api/v1/admin/tenants/{id}/group-mappings/{mapping_id}.
func (h *TenantsHandler) DeleteGroupMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mappingID := chi.URLParam(r, "mapping_id")
	if id == "" || mappingID == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id or mapping id")
		return
	}
	err := h.db.DeleteGroupMapping(r.Context(), id, mappingID)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "group mapping not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
