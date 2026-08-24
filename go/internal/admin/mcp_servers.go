package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/tenantctx"
)

// MCPServersHandler handles /api/v1/admin/mcp-servers CRUD routes.
// All mcp-server routes are tenant-scoped: each server belongs to exactly one tenant.
// Credential values are never returned — only credential_set bool.
type MCPServersHandler struct {
	svc           *service.MCPServerService
	mcpServiceURL string // base URL of them-mcp-service; empty → probe returns 503
}

// NewMCPServersHandler creates an MCPServersHandler.
// mcpServiceURL is the internal base URL of them-mcp-service (e.g. "http://them-mcp-service:8010").
// Pass empty string when the service is not deployed — the probe endpoint will return 503.
func NewMCPServersHandler(db DBQuerier, secretKey, mcpServiceURL string) *MCPServersHandler {
	return &MCPServersHandler{
		svc:           service.NewMCPServerService(dal.NewDB(db), secretKey),
		mcpServiceURL: mcpServiceURL,
	}
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
	out, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
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

	out, err := h.svc.Create(r.Context(), tenantID, body)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.Header().Set("Location", r.URL.Path+"/"+out.ID)
	writeJSON(w, http.StatusCreated, out)
}

// Get handles GET /api/v1/admin/mcp-servers/{id}
func (h *MCPServersHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	out, err := h.svc.Get(r.Context(), id, tenantID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
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

	out, err := h.svc.Update(r.Context(), id, tenantID, patch)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/admin/mcp-servers/{id}
func (h *MCPServersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.svc.Delete(r.Context(), id, tenantID); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCredentials handles GET /api/v1/admin/applications/{app_id}/mcp-credentials
func (h *MCPServersHandler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "app_id")
	out, err := h.svc.ListCredentials(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SetCredential handles PUT /api/v1/admin/applications/{app_id}/mcp-credentials/{server_id}
// The request body is NOT logged — it contains a plaintext credential.
func (h *MCPServersHandler) SetCredential(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "app_id")
	serverID := chi.URLParam(r, "server_id")

	var body service.MCPCredentialSet
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := h.svc.SetCredential(r.Context(), appID, serverID, body); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteCredential handles DELETE /api/v1/admin/applications/{app_id}/mcp-credentials/{server_id}
func (h *MCPServersHandler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "app_id")
	serverID := chi.URLParam(r, "server_id")

	if err := h.svc.DeleteCredential(r.Context(), appID, serverID); err != nil {
		if writeServiceError(w, err) {
			return
		}
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
