package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/tenantctx"
)

// LLMProviderKeysHandler handles named-API-key sub-resource routes for a tenant's
// own LLM providers: /admin/my/llm-providers/{name}/keys...
//
// Every route resolves {name} to the caller's own tenant-scoped them.llm_providers
// row first — a tenant can only manage keys on a provider row it owns. There is no
// platform-key fallback anywhere in this file (hard rule, see docs/TENANT_LLM_PROVIDERS_PLAN.md).
type LLMProviderKeysHandler struct {
	keys      *service.LLMProviderKeyService
	providers *service.LLMProviderService
}

// NewLLMProviderKeysHandler creates an LLMProviderKeysHandler.
func NewLLMProviderKeysHandler(db DBQuerier, secretKey string) *LLMProviderKeysHandler {
	d := dal.NewDB(db)
	return &LLMProviderKeysHandler{
		keys:      service.NewLLMProviderKeyService(d, secretKey),
		providers: service.NewLLMProviderService(d, secretKey),
	}
}

// TenantScopedRoutes mounts the key sub-resource routes under the tenantScoped group
// (RequireTenantAdmin + AdminTenantMiddleware already applied by the caller).
func (h *LLMProviderKeysHandler) TenantScopedRoutes(r chi.Router) {
	r.Get("/my/llm-providers/{name}/keys", h.List)
	r.Post("/my/llm-providers/{name}/keys", h.Create)
	r.Patch("/my/llm-providers/{name}/keys/{keyID}", h.Update)
	r.Delete("/my/llm-providers/{name}/keys/{keyID}", h.Delete)
	r.Post("/my/llm-providers/{name}/keys/{keyID}/default", h.SetDefault)
	r.Post("/my/llm-providers/{name}/keys/{keyID}/test", h.Test)
	r.Get("/my/llm-providers/{name}/models", h.ListModels)
}

// resolveOwnProvider resolves the {name} path param to the caller's own tenant-scoped
// them.llm_providers row. Writes a 404 and returns ok=false when the tenant has no
// row for that provider name yet (the tenant must PUT /my/llm-providers/{name} first,
// e.g. to enable the provider, before it can hold any named keys).
func (h *LLMProviderKeysHandler) resolveOwnProvider(w http.ResponseWriter, r *http.Request, tenantID string) (dal.LLMProvider, bool) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing provider name")
		return dal.LLMProvider{}, false
	}
	row, err := h.providers.GetOwnProviderRow(r.Context(), name, tenantID)
	if err != nil {
		if writeServiceError(w, err) {
			return dal.LLMProvider{}, false
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return dal.LLMProvider{}, false
	}
	return row, true
}

func parseKeyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "keyID")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return 0, false
	}
	return id, true
}

// List handles GET /admin/my/llm-providers/{name}/keys
func (h *LLMProviderKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	provider, ok := h.resolveOwnProvider(w, r, tenantID)
	if !ok {
		return
	}
	out, err := h.keys.List(r.Context(), provider.ID, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Create handles POST /admin/my/llm-providers/{name}/keys
// The request body is NOT logged — it contains a plaintext api_key.
func (h *LLMProviderKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	provider, ok := h.resolveOwnProvider(w, r, tenantID)
	if !ok {
		return
	}

	var body service.LLMProviderKeyCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	out, err := h.keys.Create(r.Context(), provider.ID, tenantID, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// Update handles PATCH /admin/my/llm-providers/{name}/keys/{keyID}
// The request body is NOT logged — it may contain a plaintext api_key.
func (h *LLMProviderKeysHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if _, ok := h.resolveOwnProvider(w, r, tenantID); !ok {
		return
	}
	id, ok := parseKeyID(w, r)
	if !ok {
		return
	}

	var body service.LLMProviderKeyPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	out, err := h.keys.Update(r.Context(), id, tenantID, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /admin/my/llm-providers/{name}/keys/{keyID}
func (h *LLMProviderKeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if _, ok := h.resolveOwnProvider(w, r, tenantID); !ok {
		return
	}
	id, ok := parseKeyID(w, r)
	if !ok {
		return
	}

	if err := h.keys.Delete(r.Context(), id, tenantID); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetDefault handles POST /admin/my/llm-providers/{name}/keys/{keyID}/default
func (h *LLMProviderKeysHandler) SetDefault(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if _, ok := h.resolveOwnProvider(w, r, tenantID); !ok {
		return
	}
	id, ok := parseKeyID(w, r)
	if !ok {
		return
	}

	out, err := h.keys.SetDefault(r.Context(), id, tenantID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Test handles POST /admin/my/llm-providers/{name}/keys/{keyID}/test
// Fires a minimal real probe call against the provider using this specific key's
// decrypted secret, records the outcome, and returns it. No platform-key fallback:
// if decryption or lookup fails, the call fails — it never substitutes another key.
func (h *LLMProviderKeysHandler) Test(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	provider, ok := h.resolveOwnProvider(w, r, tenantID)
	if !ok {
		return
	}
	id, ok := parseKeyID(w, r)
	if !ok {
		return
	}

	apiKey, err := h.keys.ResolveDecrypted(r.Context(), id, tenantID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	model := provider.DefaultModel
	var baseURL string
	if provider.BaseURL != nil {
		baseURL = *provider.BaseURL
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	ok2, testErr := probeLLMWithBase(ctx, provider.Name, model, apiKey, baseURL)
	if recErr := h.keys.RecordTestResult(r.Context(), id, tenantID, ok2); recErr != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := map[string]any{"ok": ok2}
	if !ok2 {
		resp["error"] = testErr
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListModels handles GET /admin/my/llm-providers/{name}/models?key_id=...
// Refreshes the live model list from the provider's real API using the given key.
func (h *LLMProviderKeysHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	provider, ok := h.resolveOwnProvider(w, r, tenantID)
	if !ok {
		return
	}

	keyIDRaw := r.URL.Query().Get("key_id")
	keyID, err := strconv.ParseInt(keyIDRaw, 10, 64)
	if err != nil || keyID <= 0 {
		writeError(w, http.StatusBadRequest, "missing or invalid key_id")
		return
	}

	apiKey, err := h.keys.ResolveDecrypted(r.Context(), keyID, tenantID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var baseURL string
	if provider.BaseURL != nil {
		baseURL = *provider.BaseURL
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	models, errMsg := listAvailableModels(ctx, provider.Name, apiKey, baseURL)
	if errMsg != "" {
		writeError(w, http.StatusBadGateway, errMsg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}
