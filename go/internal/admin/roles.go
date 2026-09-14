package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// RolesHandler handles /admin/roles routes (tenant-scoped).
type RolesHandler struct {
	db *dal.DB
}

// NewRolesHandler creates a RolesHandler.
func NewRolesHandler(db *dal.DB) *RolesHandler {
	return &RolesHandler{db: db}
}

// Routes mounts role CRUD + grants + mappings.
func (h *RolesHandler) Routes(r chi.Router) {
	r.Get("/roles", h.ListRoles)
	r.Post("/roles", h.CreateRole)
	r.Get("/roles/{role_id}", h.GetRole)
	r.Put("/roles/{role_id}", h.UpdateRole)
	r.Delete("/roles/{role_id}", h.DeleteRole)

	r.Get("/roles/{role_id}/grants", h.ListGrants)
	r.Post("/roles/{role_id}/grants", h.AddGrant)
	r.Delete("/roles/{role_id}/grants/{grant_id}", h.DeleteGrant)

	r.Get("/roles/{role_id}/mappings", h.ListMappings)
	r.Post("/roles/{role_id}/mappings", h.AddMapping)
	r.Delete("/roles/{role_id}/mappings/{mapping_id}", h.DeleteMapping)
}

// ── JSON wire types ────────────────────────────────────────────────────────

type roleBody struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type roleRow struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type grantBody struct {
	ApplicationID string `json:"application_id"`
}

type grantRow struct {
	ID            string    `json:"id"`
	RoleID        string    `json:"role_id"`
	ApplicationID string    `json:"application_id"`
	CreatedAt     time.Time `json:"created_at"`
}

type mappingBody struct {
	Source string `json:"source"` // "jwt_claim" | "header"
	Field  string `json:"field"`
	Value  string `json:"value"`
}

type mappingRow struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Source    string    `json:"source"`
	Field     string    `json:"field"`
	Value     string    `json:"value"`
	RoleID    string    `json:"role_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ── Role CRUD ──────────────────────────────────────────────────────────────

func (h *RolesHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	rows, err := h.db.ListRoles(r.Context(), tenantID)
	if err != nil {
		writeError(w, 500, "failed to list roles")
		return
	}
	writeJSON(w, 200, rows)
}

func (h *RolesHandler) CreateRole(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	var body roleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Name == "" {
		writeError(w, 400, "name is required")
		return
	}
	row, err := h.db.CreateRole(r.Context(), tenantID, body.Name, body.DisplayName, body.Description)
	if err != nil {
		writeError(w, 500, "failed to create role")
		return
	}
	writeJSON(w, 201, row)
}

func (h *RolesHandler) GetRole(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	row, err := h.db.GetRole(r.Context(), tenantID, roleID)
	if err != nil {
		writeError(w, 404, "role not found")
		return
	}
	writeJSON(w, 200, row)
}

func (h *RolesHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	var body roleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	row, err := h.db.UpdateRole(r.Context(), tenantID, roleID, body.Name, body.DisplayName, body.Description)
	if err != nil {
		writeError(w, 500, "failed to update role")
		return
	}
	writeJSON(w, 200, row)
}

func (h *RolesHandler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	if err := h.db.DeleteRole(r.Context(), tenantID, roleID); err != nil {
		writeError(w, 500, "failed to delete role")
		return
	}
	w.WriteHeader(204)
}

// ── Grants ─────────────────────────────────────────────────────────────────

func (h *RolesHandler) ListGrants(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	rows, err := h.db.ListGrants(r.Context(), tenantID, roleID)
	if err != nil {
		writeError(w, 500, "failed to list grants")
		return
	}
	writeJSON(w, 200, rows)
}

func (h *RolesHandler) AddGrant(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	var body grantBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ApplicationID == "" {
		writeError(w, 400, "application_id is required")
		return
	}
	row, err := h.db.AddGrant(r.Context(), tenantID, roleID, body.ApplicationID)
	if err != nil {
		writeError(w, 500, "failed to add grant")
		return
	}
	writeJSON(w, 201, row)
}

func (h *RolesHandler) DeleteGrant(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	grantID := chi.URLParam(r, "grant_id")
	if err := h.db.DeleteGrant(r.Context(), tenantID, roleID, grantID); err != nil {
		writeError(w, 500, "failed to delete grant")
		return
	}
	w.WriteHeader(204)
}

// ── Mappings ───────────────────────────────────────────────────────────────

func (h *RolesHandler) ListMappings(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	rows, err := h.db.ListRoleMappings(r.Context(), tenantID, roleID)
	if err != nil {
		writeError(w, 500, "failed to list mappings")
		return
	}
	writeJSON(w, 200, rows)
}

func (h *RolesHandler) AddMapping(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	var body mappingBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Source != "jwt_claim" && body.Source != "header" {
		writeError(w, 400, "source must be jwt_claim or header")
		return
	}
	if body.Field == "" || body.Value == "" {
		writeError(w, 400, "field and value are required")
		return
	}
	row, err := h.db.AddRoleMapping(r.Context(), tenantID, roleID, body.Source, body.Field, body.Value)
	if err != nil {
		writeError(w, 500, "failed to add mapping")
		return
	}
	writeJSON(w, 201, row)
}

func (h *RolesHandler) DeleteMapping(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	roleID := chi.URLParam(r, "role_id")
	mappingID := chi.URLParam(r, "mapping_id")
	if err := h.db.DeleteRoleMapping(r.Context(), tenantID, roleID, mappingID); err != nil {
		writeError(w, 500, "failed to delete mapping")
		return
	}
	w.WriteHeader(204)
}
