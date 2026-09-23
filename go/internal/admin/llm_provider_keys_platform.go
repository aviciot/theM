package admin

// Platform-owned mirror of llm_provider_keys.go's tenant-scoped routes,
// scoped to providers with tenant_id IS NULL (added db/108) — lets
// super_admin save multiple named keys per platform provider, exactly like a
// tenant can for their own providers. Split into its own file once
// llm_provider_keys.go passed ~500 lines with both sets of handlers together.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// PlatformRoutes mounts the same key sub-resource routes for platform-owned
// providers under the platformGlobal group (RequireSuperAdmin already applied
// by the caller). Mirrors TenantScopedRoutes exactly — same shapes, same
// semantics — just scoped to platform rows instead of the caller's own tenant.
func (h *LLMProviderKeysHandler) PlatformRoutes(r chi.Router) {
	r.Get("/llm-providers/{name}/keys", h.PlatformList)
	r.Post("/llm-providers/{name}/keys", h.PlatformCreate)
	r.Patch("/llm-providers/{name}/keys/{keyID}", h.PlatformUpdate)
	r.Delete("/llm-providers/{name}/keys/{keyID}", h.PlatformDelete)
	r.Post("/llm-providers/{name}/keys/{keyID}/default", h.PlatformSetDefault)
	r.Post("/llm-providers/{name}/keys/{keyID}/test", h.PlatformTest)
	r.Get("/llm-providers/{name}/models", h.PlatformListModels)
}

// resolvePlatformProvider resolves the {name} path param to the platform-default
// them.llm_providers row (tenant_id IS NULL).
func (h *LLMProviderKeysHandler) resolvePlatformProvider(w http.ResponseWriter, r *http.Request) (dal.LLMProvider, bool) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing provider name")
		return dal.LLMProvider{}, false
	}
	row, err := h.providers.GetPlatformProviderRow(r.Context(), name)
	if err != nil {
		if writeServiceError(w, err) {
			return dal.LLMProvider{}, false
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return dal.LLMProvider{}, false
	}
	return row, true
}

// PlatformList handles GET /admin/llm-providers/{name}/keys
func (h *LLMProviderKeysHandler) PlatformList(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.resolvePlatformProvider(w, r)
	if !ok {
		return
	}
	out, err := h.keys.List(r.Context(), provider.ID, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PlatformCreate handles POST /admin/llm-providers/{name}/keys
// The request body is NOT logged — it contains a plaintext api_key.
func (h *LLMProviderKeysHandler) PlatformCreate(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.resolvePlatformProvider(w, r)
	if !ok {
		return
	}

	var body service.LLMProviderKeyCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	out, err := h.keys.Create(r.Context(), provider.ID, nil, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// PlatformUpdate handles PATCH /admin/llm-providers/{name}/keys/{keyID}
// The request body is NOT logged — it may contain a plaintext api_key.
func (h *LLMProviderKeysHandler) PlatformUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolvePlatformProvider(w, r); !ok {
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

	out, err := h.keys.Update(r.Context(), id, nil, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PlatformDelete handles DELETE /admin/llm-providers/{name}/keys/{keyID}
func (h *LLMProviderKeysHandler) PlatformDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolvePlatformProvider(w, r); !ok {
		return
	}
	id, ok := parseKeyID(w, r)
	if !ok {
		return
	}

	if err := h.keys.Delete(r.Context(), id, nil); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PlatformSetDefault handles POST /admin/llm-providers/{name}/keys/{keyID}/default
func (h *LLMProviderKeysHandler) PlatformSetDefault(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolvePlatformProvider(w, r); !ok {
		return
	}
	id, ok := parseKeyID(w, r)
	if !ok {
		return
	}

	out, err := h.keys.SetDefault(r.Context(), id, nil)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PlatformTest handles POST /admin/llm-providers/{name}/keys/{keyID}/test
func (h *LLMProviderKeysHandler) PlatformTest(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.resolvePlatformProvider(w, r)
	if !ok {
		return
	}
	id, ok := parseKeyID(w, r)
	if !ok {
		return
	}

	apiKey, err := h.keys.ResolveDecrypted(r.Context(), id, nil)
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
	if recErr := h.keys.RecordTestResult(r.Context(), id, nil, ok2); recErr != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := map[string]any{"ok": ok2}
	if !ok2 {
		resp["error"] = testErr
	}
	writeJSON(w, http.StatusOK, resp)
}

// PlatformListModels handles GET /admin/llm-providers/{name}/models?key_id=...
func (h *LLMProviderKeysHandler) PlatformListModels(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.resolvePlatformProvider(w, r)
	if !ok {
		return
	}

	keyIDRaw := r.URL.Query().Get("key_id")
	keyID, err := strconv.ParseInt(keyIDRaw, 10, 64)
	if err != nil || keyID <= 0 {
		writeError(w, http.StatusBadRequest, "missing or invalid key_id")
		return
	}

	apiKey, err := h.keys.ResolveDecrypted(r.Context(), keyID, nil)
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
