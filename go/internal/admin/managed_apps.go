package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// ManagedAppsHandler handles managed-app catalog and tenant binding routes.
// Platform routes are mounted in the platform-global group (no AdminTenantMiddleware).
// Tenant routes are mounted in the tenant-scoped group (AdminTenantMiddleware applied).
type ManagedAppsHandler struct {
	db *dal.DB
}

// NewManagedAppsHandler creates a ManagedAppsHandler.
func NewManagedAppsHandler(db DBQuerier) *ManagedAppsHandler {
	return &ManagedAppsHandler{db: dal.NewDB(db)}
}

// PlatformRoutes mounts routes that operate on the platform-owned managed app catalog.
// These require super_admin but are NOT tenant-scoped (managed apps have no tenant_id).
func (h *ManagedAppsHandler) PlatformRoutes(r chi.Router) {
	r.Get("/managed-apps", h.List)
	r.Post("/managed-apps", h.Create)
	r.Get("/managed-apps/{id}", h.Get)
	r.Put("/managed-apps/{id}/params", h.PutParams)
}

// TenantRoutes mounts routes that operate on per-tenant bindings.
// These must be mounted inside AdminTenantMiddleware so TenantID is in context.
func (h *ManagedAppsHandler) TenantRoutes(r chi.Router) {
	r.Get("/managed-app-bindings", h.ListBindings)
	r.Put("/managed-app-bindings/{app_id}", h.UpsertBinding)
}

// List handles GET /admin/managed-apps.
func (h *ManagedAppsHandler) List(w http.ResponseWriter, r *http.Request) {
	apps, err := h.db.ListManagedApps(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

// Create handles POST /admin/managed-apps.
func (h *ManagedAppsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in dal.ManagedAppInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.TrimSpace(in.Slug)
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if in.Slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}

	app, err := h.db.CreateManagedApp(r.Context(), in)
	if dal.IsUniqueViolation(err) {
		writeError(w, http.StatusConflict, "managed app slug already exists")
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
	writeJSON(w, http.StatusCreated, app)
}

// Get handles GET /admin/managed-apps/{id}.
func (h *ManagedAppsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	detail, err := h.db.GetManagedApp(r.Context(), id)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "managed app not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// PutParams handles PUT /admin/managed-apps/{id}/params.
// Replaces the full parameter manifest for a managed app.
func (h *ManagedAppsHandler) PutParams(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	var params []dal.ManagedAppParamInput
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	for _, p := range params {
		if p.Key == "" || p.Label == "" || p.ParamType == "" {
			writeError(w, http.StatusBadRequest, "each param requires key, label, and param_type")
			return
		}
	}
	if err := h.db.UpsertManagedAppParams(r.Context(), id, params); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": len(params)})
}

// ListBindings handles GET /admin/managed-app-bindings.
// Returns bindings for the current tenant (from tenantctx).
func (h *ManagedAppsHandler) ListBindings(w http.ResponseWriter, r *http.Request) {
	tenantID, err := tenantctx.TenantIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "tenant identity required")
		return
	}
	bindings, err := h.db.ListBindingsForTenant(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, bindings)
}

// UpsertBinding handles PUT /admin/managed-app-bindings/{app_id}.
// Creates or updates the current tenant's binding to the specified managed app.
func (h *ManagedAppsHandler) UpsertBinding(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing app_id")
		return
	}
	tenantID, err := tenantctx.TenantIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "tenant identity required")
		return
	}
	var in dal.ManagedAppBindingInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(in.Config) == 0 {
		writeError(w, http.StatusBadRequest, "config is required")
		return
	}

	binding, err := h.db.UpsertBinding(r.Context(), appID, tenantID, in)
	if dal.IsForeignKeyViolation(err) {
		writeError(w, http.StatusNotFound, "managed app not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, binding)
}
