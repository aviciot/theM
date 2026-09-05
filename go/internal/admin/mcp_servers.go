package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/tenantctx"
)

// MCPServersHandler handles /api/v1/admin/mcp-servers CRUD routes.
// All mcp-server routes are tenant-scoped: each server belongs to exactly one tenant.
// Credential values are never returned — only credential_set bool.
// Every request opens a per-request TenantTx (RLS-enforced path).
type MCPServersHandler struct {
	pools         *db.Pools
	fernetKey     []byte
	mcpServiceURL string // base URL of them-mcp-service; empty → probe returns 503
	audit         *AuditWriter
	testQuerier   DBQuerier // non-nil only in tests — bypasses pools
}

// NewMCPServersHandler creates an MCPServersHandler.
// mcpServiceURL is the internal base URL of them-mcp-service (e.g. "http://them-mcp-service:8010").
// Pass empty string when the service is not deployed — the probe endpoint will return 503.
func NewMCPServersHandler(pools *db.Pools, secretKey, mcpServiceURL string, audit *AuditWriter) *MCPServersHandler {
	return &MCPServersHandler{
		pools:         pools,
		fernetKey:     crypto.DeriveKey(secretKey),
		mcpServiceURL: mcpServiceURL,
		audit:         audit,
	}
}

// NewMCPServersHandlerForTest creates an MCPServersHandler backed by a fake DBQuerier.
// Pools is nil — openSvc uses testQuerier directly with a no-op commit/rollback.
// A throwaway fernet key is derived so encryption paths can exercise without real secrets.
// Use only in tests.
func NewMCPServersHandlerForTest(q DBQuerier, audit *AuditWriter) *MCPServersHandler {
	return &MCPServersHandler{testQuerier: q, fernetKey: crypto.DeriveKey("test-only-key"), audit: audit}
}

func (h *MCPServersHandler) openSvc(ctx context.Context, tenantID string) (svc *service.MCPServerService, commit func(context.Context) error, cancel func(), err error) {
	if h.testQuerier != nil {
		d := dal.NewDB(h.testQuerier)
		noop := func(context.Context) error { return nil }
		return service.NewMCPServerServiceFromFernet(d, h.fernetKey), noop, func() {}, nil
	}
	tenantUUID, uuidErr := uuid.Parse(tenantID)
	if uuidErr != nil {
		return nil, nil, nil, uuidErr
	}
	tx, txErr := h.pools.BeginTenantTx(ctx, tenantUUID)
	if txErr != nil {
		return nil, nil, nil, txErr
	}
	rollback := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		tx.Rollback(cleanupCtx)
	}
	d := dal.NewDBFromTenantQuerier(tx)
	svc = service.NewMCPServerServiceFromFernet(d, h.fernetKey)
	return svc, tx.Commit, rollback, nil
}

// Routes mounts all MCP server endpoints on r.
//
//	GET    /mcp-servers                                              — list (tenant-scoped)
//	POST   /mcp-servers                                              — create
//	GET    /mcp-servers/{id}                                         — get single
//	PATCH  /mcp-servers/{id}                                         — update
//	DELETE /mcp-servers/{id}                                         — delete
//	POST   /mcp-servers/{id}/probe                                   — on-demand probe (proxied to mcp-service)
//	GET    /applications/{app_id}/mcp-credentials                    — list (meta only, no plaintext)
//	PUT    /applications/{app_id}/mcp-credentials/{server_id}        — set/update credential
//	DELETE /applications/{app_id}/mcp-credentials/{server_id}        — remove credential
func (h *MCPServersHandler) Routes(r chi.Router) {
	r.Get("/mcp-servers", h.List)
	r.Post("/mcp-servers", h.Create)
	r.Get("/mcp-servers/{id}", h.Get)
	r.Patch("/mcp-servers/{id}", h.Update)
	r.Delete("/mcp-servers/{id}", h.Delete)
	r.Post("/mcp-servers/{id}/probe", h.Probe)

	r.Get("/applications/{app_id}/mcp-credentials", h.ListCredentials)
	r.Put("/applications/{app_id}/mcp-credentials/{server_id}", h.SetCredential)
	r.Delete("/applications/{app_id}/mcp-credentials/{server_id}", h.DeleteCredential)
}

// List handles GET /api/v1/admin/mcp-servers
func (h *MCPServersHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	out, err := svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Create handles POST /api/v1/admin/mcp-servers
func (h *MCPServersHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	var body service.MCPServerCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	out, err := svc.Create(r.Context(), tenantID, body)
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
		Action: "mcp_server.create", EntityType: "mcp_server", EntityID: out.ID, Actor: actorFromRequest(r),
	})
	w.Header().Set("Location", r.URL.Path+"/"+out.ID)
	writeJSON(w, http.StatusCreated, out)
}

// Get handles GET /api/v1/admin/mcp-servers/{id}
func (h *MCPServersHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	out, err := svc.Get(r.Context(), id, tenantID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// Read-only: rollback is fine; commit is a no-op for reads but closes cleanly.
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, out)
}

// Update handles PATCH /api/v1/admin/mcp-servers/{id}
func (h *MCPServersHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	var patch service.MCPServerPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	out, err := svc.Update(r.Context(), id, tenantID, patch)
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
	changes := changesOf(patch)
	// Redact probe_token — it must never be logged. Record a sentinel instead.
	if patch.ProbeToken != nil {
		if changes == nil {
			changes = map[string]any{}
		}
		delete(changes, "probe_token")
		if *patch.ProbeToken == "" {
			changes["probe_token_changed"] = "cleared"
		} else {
			changes["probe_token_changed"] = true
		}
	}
	h.audit.Write(r.Context(), dal.AuditEntry{
		TenantID: tenantID, UserID: userIDPtr(r),
		Action: "mcp_server.update", EntityType: "mcp_server", EntityID: id, Actor: actorFromRequest(r),
		Changes: changes,
	})
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/admin/mcp-servers/{id}
func (h *MCPServersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.Delete(r.Context(), id, tenantID); err != nil {
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
		Action: "mcp_server.delete", EntityType: "mcp_server", EntityID: id, Actor: actorFromRequest(r),
	})
	w.WriteHeader(http.StatusNoContent)
}

// ListCredentials handles GET /api/v1/admin/applications/{app_id}/mcp-credentials
// This uses the tenant context from the request (not app_id) since credentials
// are scoped to a tenant's application.
func (h *MCPServersHandler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	appID := chi.URLParam(r, "app_id")

	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	out, err := svc.ListCredentials(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, out)
}

// SetCredential handles PUT /api/v1/admin/applications/{app_id}/mcp-credentials/{server_id}
// The request body is NOT logged — it contains a plaintext credential.
func (h *MCPServersHandler) SetCredential(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	appID := chi.URLParam(r, "app_id")
	serverID := chi.URLParam(r, "server_id")

	var body service.MCPCredentialSet
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.SetCredential(r.Context(), appID, serverID, body); err != nil {
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
	w.WriteHeader(http.StatusNoContent)
}

// DeleteCredential handles DELETE /api/v1/admin/applications/{app_id}/mcp-credentials/{server_id}
func (h *MCPServersHandler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	appID := chi.URLParam(r, "app_id")
	serverID := chi.URLParam(r, "server_id")

	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.DeleteCredential(r.Context(), appID, serverID); err != nil {
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
	w.WriteHeader(http.StatusNoContent)
}

// Probe handles POST /api/v1/admin/mcp-servers/{id}/probe.
// It proxies the request to them-mcp-service POST /internal/probe/{id} and streams
// the response back. Returns 503 when MCP_SERVICE_URL is not configured.
func (h *MCPServersHandler) Probe(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if h.mcpServiceURL == "" {
		writeError(w, http.StatusServiceUnavailable, "MCP service not configured (MCP_SERVICE_URL not set)")
		return
	}

	target := fmt.Sprintf("%s/internal/probe/%s", h.mcpServiceURL, id)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, http.NoBody)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "MCP service unreachable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
