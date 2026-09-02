package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// LLMProvidersHandler handles /api/v1/admin/llm-providers CRUD routes.
// These 5 routes are a platform-global control-plane API (no tenant parameter).
// RequireSuperAdmin is applied by BuildRouter before these routes are mounted.
type LLMProvidersHandler struct {
	svc *service.LLMProviderService
}

// NewLLMProvidersHandler creates an LLMProvidersHandler.
// secretKey is THE_M_SECRET_KEY from config; it must not be empty (validated at startup).
func NewLLMProvidersHandler(db DBQuerier, secretKey string) *LLMProvidersHandler {
	svc := service.NewLLMProviderService(dal.NewDB(db), secretKey)
	return &LLMProvidersHandler{svc: svc}
}

// Routes mounts the LLM provider CRUD endpoints on r.
// All routes require super_admin (enforced by the admin router group).
func (h *LLMProvidersHandler) Routes(r chi.Router) {
	r.Get("/llm-providers", h.List)
	r.Post("/llm-providers", h.Create)
	r.Get("/llm-providers/{id}", h.Get)
	r.Patch("/llm-providers/{id}", h.Update)
	r.Delete("/llm-providers/{id}", h.Delete)
}

// List handles GET /api/v1/admin/llm-providers
func (h *LLMProvidersHandler) List(w http.ResponseWriter, r *http.Request) {
	providers, err := h.svc.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

// Create handles POST /api/v1/admin/llm-providers
// The request body is NOT logged — it contains a plaintext api_key.
func (h *LLMProvidersHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body service.LLMProviderCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	out, err := h.svc.Create(r.Context(), body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.Header().Set("Location", r.URL.Path+"/"+strconv.FormatInt(out.ID, 10))
	writeJSON(w, http.StatusCreated, out)
}

// Get handles GET /api/v1/admin/llm-providers/{id}
func (h *LLMProvidersHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProviderID(w, r)
	if !ok {
		return
	}

	out, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Update handles PATCH /api/v1/admin/llm-providers/{id}
// The request body is NOT logged — it may contain a plaintext api_key.
// PATCH uses json.RawMessage to detect whether api_key was present in the body
// (including explicit null), because absence and null are semantically distinct:
// absent → keep current key; null or "" → clear the key; non-empty → rotate.
func (h *LLMProvidersHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProviderID(w, r)
	if !ok {
		return
	}

	// Decode into a raw map first to detect field presence.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var patch service.LLMProviderPatch

	if v, ok := raw["display_name"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			writeError(w, http.StatusBadRequest, "invalid display_name")
			return
		}
		patch.DisplayName = &s
	}

	if v, ok := raw["api_key"]; ok {
		// api_key was present in the JSON (may be null, "", or a real key).
		patch.APIKeyPresent = true
		var s *string
		if err := json.Unmarshal(v, &s); err != nil {
			writeError(w, http.StatusBadRequest, "invalid api_key")
			return
		}
		patch.APIKey = s
	}

	if v, ok := raw["base_url"]; ok {
		var s *string
		if err := json.Unmarshal(v, &s); err != nil {
			writeError(w, http.StatusBadRequest, "invalid base_url")
			return
		}
		patch.BaseURL = &s
	}

	if v, ok := raw["default_model"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			writeError(w, http.StatusBadRequest, "invalid default_model")
			return
		}
		patch.DefaultModel = &s
	}

	if v, ok := raw["model_pricing"]; ok {
		var m map[string]any
		if err := json.Unmarshal(v, &m); err != nil {
			writeError(w, http.StatusBadRequest, "invalid model_pricing")
			return
		}
		patch.ModelPricing = m
	}

	if v, ok := raw["enabled"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			writeError(w, http.StatusBadRequest, "invalid enabled")
			return
		}
		patch.Enabled = &b
	}

	out, err := h.svc.Update(r.Context(), id, patch)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/admin/llm-providers/{id}
func (h *LLMProvidersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProviderID(w, r)
	if !ok {
		return
	}

	err := h.svc.Delete(r.Context(), id)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TenantProviderRoutes mounts the per-tenant LLM provider override endpoints
// under a router that already has {id} = tenantID in the path context.
// Called by BuildRouter inside the platform-global group (no AdminTenantMiddleware).
func (h *LLMProvidersHandler) TenantProviderRoutes(r chi.Router) {
	r.Get("/tenants/{id}/llm-providers", h.ListForTenant)
	r.Put("/tenants/{id}/llm-providers/{name}", h.UpsertForTenant)
}

// ListForTenant handles GET /admin/tenants/{id}/llm-providers.
// Returns the merged view: tenant overrides + platform defaults not overridden.
func (h *LLMProvidersHandler) ListForTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")
	if tenantID == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	providers, err := h.svc.ListForTenant(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

// UpsertForTenant handles PUT /admin/tenants/{id}/llm-providers/{name}.
// Creates or replaces a tenant-scoped override for the named platform provider.
func (h *LLMProvidersHandler) UpsertForTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	if tenantID == "" || name == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id or provider name")
		return
	}

	var body service.LLMProviderCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	out, err := h.svc.UpsertForTenant(r.Context(), tenantID, name, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// parseProviderID extracts and validates the {id} path parameter.
// Writes a 400 error and returns false on failure.
func parseProviderID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid provider id")
		return 0, false
	}
	return id, true
}
