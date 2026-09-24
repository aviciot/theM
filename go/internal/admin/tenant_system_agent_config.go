package admin

// TenantSystemAgentConfigHandler handles the tenant self-service route for
// choosing general/custom mode on a system-agent role (classifier,
// card_synthesizer) — see docs/TENANT_LLM_PROVIDERS_PLAN.md step 5.
//
// Mounted under /admin/my/system-agents/{role}/config, tenant self-service
// only (mirrors the /admin/my/llm-providers naming pattern). When a tenant has
// no row here, resolveSystemAgentRole falls back to the bootstrap tenant's own
// config for the same role (Platform-as-Tenant Phase 2,
// docs/PLATFORM_AS_TENANT_PLAN.md) — there is no separate platform-global
// system-agents screen/route anymore.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/tenantctx"
)

// TenantSystemAgentConfigHandler owns the HTTP layer for tenant system-agent
// role config.
type TenantSystemAgentConfigHandler struct {
	svc *service.TenantSystemAgentConfigService
}

// NewTenantSystemAgentConfigHandler creates a TenantSystemAgentConfigHandler.
func NewTenantSystemAgentConfigHandler(db DBQuerier, secretKey string) *TenantSystemAgentConfigHandler {
	return &TenantSystemAgentConfigHandler{
		svc: service.NewTenantSystemAgentConfigService(dal.NewDB(db), secretKey),
	}
}

// TenantScopedRoutes mounts the config routes under the tenantScoped group
// (RequireTenantAdmin + AdminTenantMiddleware already applied by the caller).
func (h *TenantSystemAgentConfigHandler) TenantScopedRoutes(r chi.Router) {
	r.Get("/my/system-agents/{role}/config", h.Get)
	r.Put("/my/system-agents/{role}/config", h.Put)
	r.Post("/my/system-agents/{role}/test-llm", h.Test)
}

// Get handles GET /admin/my/system-agents/{role}/config.
func (h *TenantSystemAgentConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	role := chi.URLParam(r, "role")

	out, err := h.svc.Get(r.Context(), tenantID, role)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Put handles PUT /admin/my/system-agents/{role}/config.
// The request body is NOT logged — it may contain a plaintext custom_api_key.
func (h *TenantSystemAgentConfigHandler) Put(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	role := chi.URLParam(r, "role")

	var body service.TenantSystemAgentConfigIn
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	out, err := h.svc.Upsert(r.Context(), tenantID, role, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Test handles POST /admin/my/system-agents/{role}/test-llm — probes a
// provider/model/key for the tenant's own custom-mode config on role. Any
// field omitted from the body falls back to the tenant's own stored custom_*
// value, never the platform-global config (there is no tenant-scoped
// equivalent of the platform's /admin/system-agents/{role}/test-llm route).
// The request body is NOT logged — it may contain a plaintext api_key.
func (h *TenantSystemAgentConfigHandler) Test(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	role := chi.URLParam(r, "role")

	var body service.TenantSystemAgentConfigTest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	provider, model, apiKey, baseURL, err := h.svc.ResolveCustomTestInputs(r.Context(), tenantID, role, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	ok, testErr := probeLLMWithBase(ctx, provider, model, apiKey, baseURL)
	resp := map[string]any{"ok": ok}
	if !ok {
		resp["error"] = testErr
	}
	writeJSON(w, http.StatusOK, resp)
}
