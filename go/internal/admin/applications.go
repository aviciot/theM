package admin

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/tenantctx"
)

// newMultipartWriter wraps multipart.NewWriter so callers can use it by value.
func newMultipartWriter(buf *bytes.Buffer) *multipart.Writer { return multipart.NewWriter(buf) }

// ApplicationsHandler handles /api/v1/admin/applications routes.
type ApplicationsHandler struct {
	svc       *service.AppService
	dal       *dal.DB
	fernetKey []byte
}

// NewApplicationsHandler creates an ApplicationsHandler.
// fernetKey is the AES-GCM key used to encrypt/decrypt provider_keys at rest.
func NewApplicationsHandler(db DBQuerier, cache CacheInvalidator, fernetKey []byte) *ApplicationsHandler {
	return &ApplicationsHandler{
		svc:       service.NewAppService(dal.NewDB(db), cache, fernetKey),
		dal:       dal.NewDB(db),
		fernetKey: fernetKey,
	}
}

// Svc returns the underlying AppService so callers (e.g. the voice handler)
// can reuse it without constructing a second service instance.
func (h *ApplicationsHandler) Svc() *service.AppService { return h.svc }

// RuntimeConfigInput mirrors Python's AppRuntimeConfig schema.
type RuntimeConfigInput = service.AppRuntimeConfig

// BulkDeleteInput is the request body for POST /bulk-delete.
type BulkDeleteInput struct {
	AppIDs []string `json:"app_ids"`
}

// Routes mounts application and entry point CRUD endpoints.
// bindings is optional; when non-nil its routes are mounted under /applications/{id}
// so they share the same chi sub-tree and don't shadow the flat /{id} routes.
func (h *ApplicationsHandler) Routes(r chi.Router, bindings ...BindingRouter) {
	r.Get("/applications", h.List)
	r.Post("/applications", h.Create)
	r.Post("/applications/bulk-delete", h.BulkDelete) // must come BEFORE /{id}
	r.Route("/applications/{id}", func(app chi.Router) {
		app.Get("/", h.Get)
		app.Put("/", h.Update)
		app.Patch("/", h.Update) // Python frontend sends PATCH; accept both
		app.Delete("/", h.Delete)
		app.Put("/runtime", h.PutRuntime)
		app.Get("/provider-keys", h.GetProviderKeys)
		app.Put("/provider-keys/{provider}", h.SetProviderKey)
		app.Delete("/provider-keys/{provider}", h.DeleteProviderKey)
		app.Post("/test-llm", h.TestLLM)
		app.Get("/app-params", h.GetAppParams)
		app.Put("/app-params/{name}", h.SetAppParam)
		app.Delete("/app-params/{name}", h.DeleteAppParam)
		app.Patch("/orchestrators/{orch_id}/llm", h.PatchOrchestratorLLM)
		app.Patch("/orchestrators/{orch_id}/voice", h.PatchOrchestratorVoice)
		app.Post("/orchestrators/{orch_id}/test-voice", h.TestOrchestratorVoice)
		app.Post("/orchestrators/{orch_id}/test-tts", h.TestOrchestratorTTS)
		app.Patch("/orchestrators/{orch_id}/mcp-servers", h.PatchOrchestratorMCPServers)
		app.Get("/entry-points", h.ListEntryPoints)
		app.Patch("/entry-points/{ep_id}/summarizer", h.PatchEntryPointSummarizer)
		app.Patch("/entry-points/{ep_id}/llm", h.PatchEntryPointLLM)
		app.Post("/entry-points", h.CreateEntryPoint)
		app.Put("/entry-points/{ep_id}", h.UpdateEntryPoint)
		app.Patch("/entry-points/{ep_id}", h.UpdateEntryPoint) // Python sends PATCH
		app.Delete("/entry-points/{ep_id}", h.DeleteEntryPoint)
		app.Post("/entry-points/{ep_id}/discover", h.DiscoverEP)
		for _, b := range bindings {
			b.MountOn(app)
		}
	})
}

// BindingRouter is implemented by any handler that mounts sub-routes under /applications/{id}.
type BindingRouter interface {
	MountOn(r chi.Router)
}

// List handles GET /api/v1/admin/applications.
func (h *ApplicationsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	apps, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

// Create handles POST /api/v1/admin/applications.
func (h *ApplicationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input ApplicationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id, err := h.svc.Create(r.Context(), tenantID, input.Name, input.Slug, input.Enabled)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "create application: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/applications/%s", id))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// Get handles GET /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	a, err := h.svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// Update handles PUT/PATCH /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input ApplicationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.Update(r.Context(), tenantID, id, input.Name, input.Slug, input.Enabled); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "update application: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete application: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// ListEntryPoints handles GET /api/v1/admin/applications/{id}/entry-points.
func (h *ApplicationsHandler) ListEntryPoints(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	eps := h.svc.ListEntryPoints(r.Context(), appID)
	writeJSON(w, http.StatusOK, eps)
}

// CreateEntryPoint handles POST /api/v1/admin/applications/{id}/entry-points.
func (h *ApplicationsHandler) CreateEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input EntryPointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	epID, err := h.svc.CreateEntryPoint(r.Context(), appID, input.Slug, input.EntryPointType, input.Enabled)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "create entry point: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/applications/%s/entry-points/%s", appID, epID))
	writeJSON(w, http.StatusCreated, map[string]any{"id": epID})
}

// UpdateEntryPoint handles PUT/PATCH /api/v1/admin/applications/{id}/entry-points/{ep_id}.
func (h *ApplicationsHandler) UpdateEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if appID == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	var input EntryPointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.UpdateEntryPoint(r.Context(), tenantID, epID, appID, input.Slug, input.EntryPointType, input.Enabled); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "update entry point: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "updated": true})
}

// DeleteEntryPoint handles DELETE /api/v1/admin/applications/{id}/entry-points/{ep_id}.
func (h *ApplicationsHandler) DeleteEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if appID == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	if err := h.svc.DeleteEntryPoint(r.Context(), epID, appID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete entry point: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "deleted": true})
}

// PutRuntime handles PUT /api/v1/admin/applications/{id}/runtime.
func (h *ApplicationsHandler) PutRuntime(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input RuntimeConfigInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	cfg, err := h.svc.PutRuntime(r.Context(), tenantID, id, input)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "update runtime config")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// GetProviderKeys handles GET /api/v1/admin/applications/{id}/provider-keys.
// Returns key-set status per provider — never the plaintext key.
func (h *ApplicationsHandler) GetProviderKeys(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	keys, err := h.svc.GetProviderKeys(r.Context(), tenantID, id)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "get provider keys")
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

// SetProviderKey handles PUT /api/v1/admin/applications/{id}/provider-keys/{provider}.
// Body: {"key": "<plaintext api key>"}
func (h *ApplicationsHandler) SetProviderKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	provider := chi.URLParam(r, "provider")
	if id == "" || provider == "" {
		writeError(w, http.StatusBadRequest, "invalid application id or provider")
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.SetProviderKey(r.Context(), tenantID, id, provider, body.Key); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "set provider key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "updated": true})
}

// DeleteProviderKey handles DELETE /api/v1/admin/applications/{id}/provider-keys/{provider}.
func (h *ApplicationsHandler) DeleteProviderKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	provider := chi.URLParam(r, "provider")
	if id == "" || provider == "" {
		writeError(w, http.StatusBadRequest, "invalid application id or provider")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.DeleteProviderKey(r.Context(), tenantID, id, provider); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "delete provider key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "deleted": true})
}

// GetAppParams handles GET /api/v1/admin/applications/{id}/app-params.
// Returns name, type, is_set, value_hint per param. Never returns ciphertext or plaintext secrets.
func (h *ApplicationsHandler) GetAppParams(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	params, err := h.svc.GetAppParams(r.Context(), tenantID, id)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "get app params")
		return
	}
	writeJSON(w, http.StatusOK, params)
}

// SetAppParam handles PUT /api/v1/admin/applications/{id}/app-params/{name}.
// Body: {"value": "<plaintext>", "type": "secret"|"string"|"url"|"int"|"bool"}
func (h *ApplicationsHandler) SetAppParam(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	if id == "" || name == "" {
		writeError(w, http.StatusBadRequest, "invalid application id or param name")
		return
	}
	var body service.AppGlobalParamUpsertInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.SetAppParam(r.Context(), tenantID, id, name, body); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "set app param")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "updated": true})
}

// DeleteAppParam handles DELETE /api/v1/admin/applications/{id}/app-params/{name}.
func (h *ApplicationsHandler) DeleteAppParam(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	if id == "" || name == "" {
		writeError(w, http.StatusBadRequest, "invalid application id or param name")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.DeleteAppParam(r.Context(), tenantID, id, name); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "delete app param")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true})
}

// TestLLM handles POST /api/v1/admin/applications/{id}/test-llm.
// Body: {"provider": "anthropic", "model": "claude-haiku-4-5-20251001"}
// Reads the stored key from provider_keys, fires a minimal probe request, returns latency.
func (h *ApplicationsHandler) TestLLM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Provider == "" || body.Model == "" {
		writeError(w, http.StatusBadRequest, "provider and model are required")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	raw, err := h.svc.GetProviderKeys(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	// raw is []ProviderKeyOut — we need the plaintext key; call DAL directly via svc
	apiKey, err := h.svc.GetPlaintextProviderKey(r.Context(), tenantID, id, body.Provider)
	if err != nil || apiKey == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "No API key stored for " + body.Provider + " — save one in Runtime settings first"})
		return
	}
	_ = raw // used above only for not-found check

	start := time.Now()
	ok, testErr := probeLLM(r.Context(), body.Provider, body.Model, apiKey)
	latency := time.Since(start).Milliseconds()

	if ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latency_ms": latency})
	} else {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": testErr})
	}
}

// PatchOrchestratorLLM handles PATCH /api/v1/admin/applications/{id}/orchestrators/{orch_id}/llm.
// Body: {"provider": "anthropic", "model": "claude-haiku-4-5-20251001"}
// Validates that the provider has a key stored on the app, then updates the orchestrator row.
func (h *ApplicationsHandler) PatchOrchestratorLLM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	orchID := chi.URLParam(r, "orch_id")
	if id == "" || orchID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or orchestrator id")
		return
	}

	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.SetOrchestratorLLM(r.Context(), tenantID, id, orchID, body.Provider, body.Model); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           orchID,
		"app_id":       id,
		"llm_provider": body.Provider,
		"llm_model":    body.Model,
	})
}

// PatchEntryPointSummarizer handles PATCH /api/v1/admin/applications/{id}/entry-points/{ep_id}/summarizer.
// Body: {"memory_enabled":true,"summarize_every_n_calls":10,"memory_raw_fallback_n":3,"summarizer_provider":"anthropic","summarizer_model":"claude-haiku-4-5-20251001"}
func (h *ApplicationsHandler) PatchEntryPointSummarizer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if id == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	var body struct {
		MemoryEnabled        bool    `json:"memory_enabled"`
		SummarizeEveryNCalls int     `json:"summarize_every_n_calls"`
		MemoryRawFallbackN   int     `json:"memory_raw_fallback_n"`
		SummarizerProvider   *string `json:"summarizer_provider"`
		SummarizerModel      *string `json:"summarizer_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.SummarizeEveryNCalls == 0 {
		body.SummarizeEveryNCalls = 10
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.SetEntryPointSummarizer(r.Context(), tenantID, id, epID,
		body.MemoryEnabled, body.SummarizeEveryNCalls, body.MemoryRawFallbackN,
		body.SummarizerProvider, body.SummarizerModel,
	); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                      epID,
		"app_id":                  id,
		"memory_enabled":          body.MemoryEnabled,
		"summarize_every_n_calls": body.SummarizeEveryNCalls,
		"memory_raw_fallback_n":   body.MemoryRawFallbackN,
		"summarizer_provider":     body.SummarizerProvider,
		"summarizer_model":        body.SummarizerModel,
	})
}

// PatchEntryPointLLM handles PATCH /api/v1/admin/applications/{id}/entry-points/{ep_id}/llm.
// Body: {"llm_provider":"anthropic","llm_model":"claude-haiku-4-5-20251001"}
func (h *ApplicationsHandler) PatchEntryPointLLM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if id == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	var body struct {
		LLMProvider *string `json:"llm_provider"`
		LLMModel    *string `json:"llm_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.SetEntryPointLLM(r.Context(), tenantID, id, epID, body.LLMProvider, body.LLMModel); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           epID,
		"app_id":       id,
		"llm_provider": body.LLMProvider,
		"llm_model":    body.LLMModel,
	})
}

// PatchOrchestratorMCPServers handles PATCH /api/v1/admin/applications/{id}/orchestrators/{orch_id}/mcp-servers.
// Body: {"mcp_servers": [{"slug": "smoke-mcp", "tools": []}, ...]}
// An empty array clears all attached servers.
func (h *ApplicationsHandler) PatchOrchestratorMCPServers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	orchID := chi.URLParam(r, "orch_id")
	if id == "" || orchID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or orchestrator id")
		return
	}

	var body struct {
		MCPServers []dal.MCPServerAttachment `json:"mcp_servers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.SetOrchestratorMCPServers(r.Context(), tenantID, id, orchID, body.MCPServers); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          orchID,
		"app_id":      id,
		"mcp_servers": body.MCPServers,
	})
}

// probeLLM fires a minimal single-message request to validate the provider key.
func probeLLM(ctx context.Context, provider, model, apiKey string) (bool, string) {
	return probeLLMWithBase(ctx, provider, model, apiKey, "")
}

// probeLLMWithBase is like probeLLM but accepts an optional baseURL for
// OpenAI-compatible providers with custom endpoints.
func probeLLMWithBase(ctx context.Context, provider, model, apiKey, baseURL string) (bool, string) {
	switch provider {
	case "anthropic":
		return probeAnthropic(ctx, model, apiKey)
	case "openai":
		if baseURL != "" {
			return probeOpenAICompat(ctx, model, apiKey, baseURL)
		}
		return probeOpenAI(ctx, model, apiKey)
	case "groq":
		return probeOpenAICompat(ctx, model, apiKey, "https://api.groq.com/openai/v1/chat/completions")
	case "gemini":
		return probeGemini(ctx, model, apiKey)
	default:
		if baseURL != "" {
			return probeOpenAICompat(ctx, model, apiKey, baseURL)
		}
		return false, "unsupported provider: " + provider
	}
}

func probeAnthropic(ctx context.Context, model, apiKey string) (bool, string) {
	payload := map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	return doProbe(req)
}

func probeOpenAI(ctx context.Context, model, apiKey string) (bool, string) {
	return probeOpenAICompat(ctx, model, apiKey, "https://api.openai.com/v1/chat/completions")
}

func probeOpenAICompat(ctx context.Context, model, apiKey, url string) (bool, string) {
	payload := map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return doProbe(req)
}

func probeGemini(ctx context.Context, model, apiKey string) (bool, string) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	payload := map[string]any{
		"contents": []map[string]any{{"parts": []map[string]any{{"text": "hi"}}}},
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return doProbe(req)
}

func doProbe(req *http.Request) (bool, string) {
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return true, ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
}

// PatchOrchestratorVoice handles PATCH /api/v1/admin/applications/{id}/orchestrators/{orch_id}/voice.
// Body: {"stt_provider":"openai","stt_model":"whisper-1","tts_provider":"openai","tts_voice":"alloy","voice_enabled":true,"tts_enabled":true}
// Sets voice/STT/TTS configuration on the app_orchestrators row.
func (h *ApplicationsHandler) PatchOrchestratorVoice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	orchID := chi.URLParam(r, "orch_id")
	if id == "" || orchID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or orchestrator id")
		return
	}
	var body struct {
		STTProvider  string `json:"stt_provider"`
		STTModel     string `json:"stt_model"`
		TTSProvider  string `json:"tts_provider"`
		TTSVoice     string `json:"tts_voice"`
		VoiceEnabled bool   `json:"voice_enabled"`
		TTSEnabled   bool   `json:"tts_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.SetOrchestratorVoice(r.Context(), tenantID, id, orchID, service.VoiceConfig{
		STTProvider:  body.STTProvider,
		STTModel:     body.STTModel,
		TTSProvider:  body.TTSProvider,
		TTSVoice:     body.TTSVoice,
		VoiceEnabled: body.VoiceEnabled,
		TTSEnabled:   body.TTSEnabled,
	}); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            orchID,
		"app_id":        id,
		"stt_provider":  body.STTProvider,
		"stt_model":     body.STTModel,
		"tts_provider":  body.TTSProvider,
		"tts_voice":     body.TTSVoice,
		"voice_enabled": body.VoiceEnabled,
		"tts_enabled":   body.TTSEnabled,
	})
}

// TestOrchestratorVoice handles POST /api/v1/admin/applications/{id}/orchestrators/{orch_id}/test-voice.
// Body: {"provider":"openai","model":"whisper-1"}
// Sends a tiny silent WAV to the STT API to verify the key works.
func (h *ApplicationsHandler) TestOrchestratorVoice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	orchID := chi.URLParam(r, "orch_id")
	if id == "" || orchID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or orchestrator id")
		return
	}
	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	apiKey, err := h.svc.GetPlaintextProviderKey(r.Context(), tenantID, id, body.Provider)
	if err != nil || apiKey == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "no API key stored for provider " + body.Provider})
		return
	}
	ok, errStr := probeSTT(r.Context(), body.Provider, body.Model, apiKey)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "error": errStr})
}

// TestOrchestratorTTS handles POST /api/v1/admin/applications/{id}/orchestrators/{orch_id}/test-tts.
// Body: {"provider":"openai","voice":"alloy"}
// Sends a minimal TTS request to verify the key works.
func (h *ApplicationsHandler) TestOrchestratorTTS(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	orchID := chi.URLParam(r, "orch_id")
	if id == "" || orchID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or orchestrator id")
		return
	}
	var body struct {
		Provider string `json:"provider"`
		Voice    string `json:"voice"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	apiKey, err := h.svc.GetPlaintextProviderKey(r.Context(), tenantID, id, body.Provider)
	if err != nil || apiKey == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "no API key stored for provider " + body.Provider})
		return
	}
	ok, errStr := probeTTS(r.Context(), body.Provider, body.Voice, apiKey)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "error": errStr})
}

// probeSTT sends a minimal audio request to validate the STT API key.
// Builds a 0.2-second silent WAV (16-bit mono 44100 Hz) — OpenAI and Groq
// both reject clips shorter than 0.1 s, so we need actual audio samples.
func probeSTT(ctx context.Context, provider, model, apiKey string) (bool, string) {
	silentWAV := buildSilentWAV(44100, 1, 16, 200*time.Millisecond)
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := probeSTTCall(ctx2, provider, model, apiKey, silentWAV)
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

// buildSilentWAV returns a valid PCM WAV file with the requested duration of silence.
func buildSilentWAV(sampleRate, channels, bitsPerSample int, dur time.Duration) []byte {
	numSamples := int(float64(sampleRate) * dur.Seconds())
	dataSize := numSamples * channels * (bitsPerSample / 8)
	// Total file = 44-byte header + dataSize
	buf := make([]byte, 44+dataSize)
	le := binary.LittleEndian
	copy(buf[0:4], "RIFF")
	le.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	le.PutUint32(buf[16:20], 16) // fmt chunk size
	le.PutUint16(buf[20:22], 1)  // PCM
	le.PutUint16(buf[22:24], uint16(channels))
	le.PutUint32(buf[24:28], uint32(sampleRate))
	byteRate := sampleRate * channels * bitsPerSample / 8
	le.PutUint32(buf[28:32], uint32(byteRate))
	le.PutUint16(buf[32:34], uint16(channels*bitsPerSample/8))
	le.PutUint16(buf[34:36], uint16(bitsPerSample))
	copy(buf[36:40], "data")
	le.PutUint32(buf[40:44], uint32(dataSize))
	// remaining bytes are zero (silence)
	return buf
}

func probeSTTCall(ctx context.Context, provider, model, apiKey string, audio []byte) (string, error) {
	var (
		url   string
		mname string
	)
	switch provider {
	case "openai":
		url = "https://api.openai.com/v1/audio/transcriptions"
		mname = model
		if mname == "" {
			mname = "whisper-1"
		}
	case "groq":
		url = "https://api.groq.com/openai/v1/audio/transcriptions"
		mname = model
		if mname == "" {
			mname = "whisper-large-v3"
		}
	default:
		return "", fmt.Errorf("unsupported STT provider: %s", provider)
	}

	var buf bytes.Buffer
	mw := newMultipartForm(&buf, audio, "probe.wav", mname)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw)
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return "", nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
}

// newMultipartForm builds a multipart form for the audio file and returns the Content-Type.
func newMultipartForm(buf *bytes.Buffer, audio []byte, filename, model string) string {
	mw := newMultipartWriter(buf)
	if fw, err := mw.CreateFormFile("file", filename); err == nil {
		_, _ = fw.Write(audio)
	}
	_ = mw.WriteField("model", model)
	mw.Close()
	return mw.FormDataContentType()
}

// probeTTS sends a minimal TTS request to validate the key.
func probeTTS(ctx context.Context, provider, voice, apiKey string) (bool, string) {
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var w bytes.Buffer
	_, err := probeVoiceTTS(ctx2, &w, provider, voice, apiKey)
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

func probeVoiceTTS(ctx context.Context, w *bytes.Buffer, provider, voice, apiKey string) (string, error) {
	switch provider {
	case "openai":
		payload, _ := json.Marshal(map[string]string{
			"model": "tts-1", "input": "test", "voice": voice, "response_format": "mp3",
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/audio/speech", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
		}
		_, _ = io.Copy(w, resp.Body)
		return "audio/mpeg", nil
	case "elevenlabs":
		if voice == "" {
			return "", fmt.Errorf("voice ID required for ElevenLabs")
		}
		payload, _ := json.Marshal(map[string]any{"text": "test", "model_id": "eleven_monolingual_v1"})
		url := "https://api.elevenlabs.io/v1/text-to-speech/" + voice + "/stream"
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		req.Header.Set("xi-api-key", apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
		}
		_, _ = io.Copy(w, resp.Body)
		return "audio/mpeg", nil
	default:
		return "", fmt.Errorf("unsupported TTS provider: %s", provider)
	}
}

// BulkDelete handles POST /api/v1/admin/applications/bulk-delete.
func (h *ApplicationsHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	var input BulkDeleteInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	deleted, err := h.svc.BulkDelete(r.Context(), tenantID, input.AppIDs)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "bulk delete applications")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}
