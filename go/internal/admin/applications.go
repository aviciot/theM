package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/tenantctx"
)

// ApplicationsHandler handles /api/v1/admin/applications routes.
type ApplicationsHandler struct {
	// legacySvc is used by Svc() to provide a shared AppService for callers
	// that don't have a per-request TenantTx (e.g. voiceAppsSvc in main.go).
	legacySvc *service.AppService
	// legacyDAL is used by action endpoints (ep_discover) that perform
	// cross-EP admin-pool reads not scoped to a single TenantTx.
	legacyDAL *dal.DB
	pools     *db.Pools
	cache     CacheInvalidator
	audit     *AuditWriter
	fernetKey []byte
}

// NewApplicationsHandler creates an ApplicationsHandler.
// When pools is non-nil each request uses a TenantTx (RLS-ready path).
// When pools is nil (e.g. for Svc()-only callers), openSvc falls back to legacySvc.
// fernetKey is the AES-GCM key used to encrypt/decrypt provider_keys at rest.
func NewApplicationsHandler(legacyDB DBQuerier, pools *db.Pools, cache CacheInvalidator, fernetKey []byte, audit *AuditWriter) *ApplicationsHandler {
	d := dal.NewDB(legacyDB)
	return &ApplicationsHandler{
		legacySvc: service.NewAppService(d, cache, fernetKey),
		legacyDAL: d,
		pools:     pools,
		cache:     cache,
		audit:     audit,
		fernetKey: fernetKey,
	}
}

func (h *ApplicationsHandler) openSvc(ctx context.Context, tenantID string) (svc *service.AppService, commit func(context.Context) error, rollback func(), err error) {
	if h.pools == nil {
		return h.legacySvc, func(_ context.Context) error { return nil }, func() {}, nil
	}
	tenantUUID, uuidErr := uuid.Parse(tenantID)
	if uuidErr != nil {
		return nil, nil, nil, uuidErr
	}
	tx, txErr := h.pools.BeginTenantTx(ctx, tenantUUID)
	if txErr != nil {
		return nil, nil, nil, txErr
	}
	rb := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}
	return service.NewAppService(dal.NewDBFromTenantQuerier(tx), h.cache, h.fernetKey), tx.Commit, rb, nil
}

// Svc returns the underlying AppService so callers (e.g. agent bindings handler)
// can reuse it without constructing a second service instance.
func (h *ApplicationsHandler) Svc() *service.AppService { return h.legacySvc }

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
		app.Post("/entry-points", h.CreateEntryPoint)
		app.Route("/entry-points/{ep_id}", func(ep chi.Router) {
			ep.Put("/", h.UpdateEntryPoint)
			ep.Patch("/", h.UpdateEntryPoint) // Python sends PATCH
			ep.Delete("/", h.DeleteEntryPoint)
			ep.Patch("/enabled", h.PatchEntryPointEnabled)
			ep.Patch("/summarizer", h.PatchEntryPointSummarizer)
			ep.Patch("/llm", h.PatchEntryPointLLM)
			ep.Post("/discover", h.DiscoverEP)
		})
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
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	apps, err := svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, apps)
}

// Create handles POST /api/v1/admin/applications.
func (h *ApplicationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input ApplicationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	id, err := svc.Create(r.Context(), tenantID, input.Name, input.Slug, input.Enabled)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.audit.Write(r.Context(), dal.AuditEntry{
		TenantID: tenantID, UserID: userIDPtr(r),
		Action: "app.create", EntityType: "app", EntityID: id, Actor: actorFromRequest(r),
	})
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
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	a, err := svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	_ = commit(r.Context())
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
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.Update(r.Context(), tenantID, id, input.Name, input.Slug, input.Enabled); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.audit.Write(r.Context(), dal.AuditEntry{
		TenantID: tenantID, UserID: userIDPtr(r),
		Action: "app.update", EntityType: "app", EntityID: id, Actor: actorFromRequest(r),
		Changes: changesOf(input),
	})
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
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.audit.Write(r.Context(), dal.AuditEntry{
		TenantID: tenantID, UserID: userIDPtr(r),
		Action: "app.delete", EntityType: "app", EntityID: id, Actor: actorFromRequest(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// ListEntryPoints handles GET /api/v1/admin/applications/{id}/entry-points.
func (h *ApplicationsHandler) ListEntryPoints(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	eps := svc.ListEntryPoints(r.Context(), appID)
	_ = commit(r.Context())
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
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	epID, err := svc.CreateEntryPoint(r.Context(), appID, input.Slug, input.EntryPointType, input.Enabled)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
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
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.UpdateEntryPoint(r.Context(), tenantID, epID, appID, input.Slug, input.EntryPointType, input.Enabled); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
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

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.DeleteEntryPoint(r.Context(), epID, appID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "deleted": true})
}

// PatchEntryPointEnabled handles PATCH /api/v1/admin/applications/{id}/entry-points/{ep_id}/enabled.
// Only updates the enabled column — slug and entry_point_type are untouched.
// Body: {"enabled": true|false}
func (h *ApplicationsHandler) PatchEntryPointEnabled(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if appID == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.SetEntryPointEnabled(r.Context(), tenantID, appID, epID, body.Enabled); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "enabled": body.Enabled})
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
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	cfg, err := svc.PutRuntime(r.Context(), tenantID, id, input)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
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
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	keys, err := svc.GetProviderKeys(r.Context(), tenantID, id)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, keys)
}

// SetProviderKey handles PUT /api/v1/admin/applications/{id}/provider-keys/{provider}.
// Body: {"key": "<plaintext api key>", "base_url": "<optional endpoint URL>"}
// For local providers (ollama/vllm/lmstudio) key may be omitted; base_url is stored
// in them.llm_providers as a tenant-scoped row.
func (h *ApplicationsHandler) SetProviderKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	provider := chi.URLParam(r, "provider")
	if id == "" || provider == "" {
		writeError(w, http.StatusBadRequest, "invalid application id or provider")
		return
	}
	var body struct {
		Key     string `json:"key"`
		BaseURL string `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.SetProviderKey(r.Context(), tenantID, id, provider, body.Key, body.BaseURL); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
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
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.DeleteProviderKey(r.Context(), tenantID, id, provider); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
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
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	params, err := svc.GetAppParams(r.Context(), tenantID, id)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	_ = commit(r.Context())
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
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.SetAppParam(r.Context(), tenantID, id, name, body); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
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
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.DeleteAppParam(r.Context(), tenantID, id, name); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true})
}

// BulkDelete handles POST /api/v1/admin/applications/bulk-delete.
// PatchManaged handles PATCH /api/v1/admin/applications/{id}/managed.
// Toggles the app_type between 'tenant' and 'managed'. RequireSuperAdmin.
// Body: {"is_managed": bool}
func (h *ApplicationsHandler) PatchManaged(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	var body struct {
		IsManaged bool `json:"is_managed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := dal.SetManagedFlag(r.Context(), h.legacyDAL.Querier(), appID, body.IsManaged); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"is_managed": body.IsManaged})
}

func (h *ApplicationsHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	var input BulkDeleteInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	deleted, err := svc.BulkDelete(r.Context(), tenantID, input.AppIDs)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}
