package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// MCPServerOut is the HTTP response shape for MCP server endpoints.
// Credential values are NEVER included — only credential_set bools.
type MCPServerOut struct {
	ID                  string          `json:"id"`
	TenantID            string          `json:"tenant_id"`
	Name                string          `json:"name"`
	Slug                string          `json:"slug"`
	Description         string          `json:"description"`
	Transport           string          `json:"transport"`
	URL                 string          `json:"url"`
	AuthType            string          `json:"auth_type"`
	HealthStatus        string          `json:"health_status"`
	LastCheckedAt       *string         `json:"last_checked_at"`
	LastError           string          `json:"last_error,omitempty"`
	ToolsManifest       json.RawMessage `json:"tools_manifest"`
	ToolsCount          int             `json:"tools_count"`
	Capabilities        json.RawMessage `json:"capabilities"`
	Enabled             bool            `json:"enabled"`
	ProbeCredentialSet  bool            `json:"probe_credential_set"`
	CreatedAt           string          `json:"created_at"`
	UpdatedAt           string          `json:"updated_at"`
}

// MCPServerCreate is the request body for POST /admin/mcp-servers.
type MCPServerCreate struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Transport   string `json:"transport"`
	URL         string `json:"url"`
	AuthType    string `json:"auth_type"`
	Enabled     *bool  `json:"enabled"`
	ProbeToken  string `json:"probe_token,omitempty"` // plaintext; encrypted before storage
}

// MCPServerPatch is the PATCH request body. Nil pointer = field absent.
type MCPServerPatch struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Description *string `json:"description"`
	Transport   *string `json:"transport"`
	URL         *string `json:"url"`
	AuthType    *string `json:"auth_type"`
	Enabled     *bool   `json:"enabled"`
	ProbeToken  *string `json:"probe_token"` // nil = unchanged; "" = clear; non-empty = update
}

// MCPCredentialSet is the request body for PUT .../mcp-credentials/{server_id}.
type MCPCredentialSet struct {
	Credential     string `json:"credential"`       // plaintext; never logged
	AuthHeaderName string `json:"auth_header_name"` // default: Authorization
}

// MCPServerService owns business logic for MCP server CRUD and credential management.
type MCPServerService struct {
	dal       Dal
	fernetKey []byte
}

// NewMCPServerService creates an MCPServerService.
func NewMCPServerService(d Dal, secretKey string) *MCPServerService {
	return &MCPServerService{
		dal:       d,
		fernetKey: crypto.DeriveKey(secretKey),
	}
}

// NewMCPServerServiceFromFernet creates an MCPServerService with a pre-derived fernet key.
// Use this when the key has already been derived at startup to avoid re-deriving per request.
func NewMCPServerServiceFromFernet(d Dal, fernetKey []byte) *MCPServerService {
	return &MCPServerService{dal: d, fernetKey: fernetKey}
}

var validMCPTransports = map[string]bool{"http": true, "sse": true, "streamable-http": true}
var validMCPAuthTypes = map[string]bool{"none": true, "bearer": true, "header": true, "oauth2": true}

// List returns all MCP servers for the tenant.
func (s *MCPServerService) List(ctx context.Context, tenantID string) ([]MCPServerOut, error) {
	rows, err := s.dal.ListMCPServers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]MCPServerOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, toMCPServerOut(r))
	}
	return out, nil
}

// Get returns one MCP server by ID.
func (s *MCPServerService) Get(ctx context.Context, id, tenantID string) (MCPServerOut, error) {
	row, err := s.dal.GetMCPServer(ctx, id, tenantID)
	if err != nil {
		if dal.IsNoRows(err) {
			return MCPServerOut{}, ErrNotFound
		}
		return MCPServerOut{}, err
	}
	return toMCPServerOut(row), nil
}

// Create validates and inserts a new MCP server.
func (s *MCPServerService) Create(ctx context.Context, tenantID string, body MCPServerCreate) (MCPServerOut, error) {
	if body.Name == "" {
		return MCPServerOut{}, validation("name is required")
	}
	if body.Slug == "" {
		return MCPServerOut{}, validation("slug is required")
	}
	transport := body.Transport
	if transport == "" {
		transport = "http"
	}
	if !validMCPTransports[transport] {
		return MCPServerOut{}, unprocessable("transport must be one of: http, sse, stdio")
	}
	authType := body.AuthType
	if authType == "" {
		authType = "none"
	}
	if !validMCPAuthTypes[authType] {
		return MCPServerOut{}, unprocessable("auth_type must be one of: none, bearer, header, oauth2")
	}

	var probeEnc *string
	if body.ProbeToken != "" {
		enc, err := crypto.EncryptStored(s.fernetKey, body.ProbeToken)
		if err != nil {
			return MCPServerOut{}, errors.New("failed to encrypt probe token")
		}
		probeEnc = &enc
	}
	in := dal.MCPServerInput{
		TenantID:                tenantID,
		Name:                    body.Name,
		Slug:                    body.Slug,
		Description:             body.Description,
		Transport:               transport,
		URL:                     body.URL,
		AuthType:                authType,
		Enabled:                 enabledOrDefault(body.Enabled),
		ProbeCredentialEncrypted: probeEnc,
	}
	row, err := s.dal.CreateMCPServer(ctx, in)
	if err != nil {
		if dal.IsUniqueViolation(err) {
			return MCPServerOut{}, ErrConflict
		}
		return MCPServerOut{}, err
	}
	return toMCPServerOut(row), nil
}

// Update applies a PATCH using fetch-then-modify semantics.
func (s *MCPServerService) Update(ctx context.Context, id, tenantID string, patch MCPServerPatch) (MCPServerOut, error) {
	row, err := s.dal.GetMCPServer(ctx, id, tenantID)
	if err != nil {
		if dal.IsNoRows(err) {
			return MCPServerOut{}, ErrNotFound
		}
		return MCPServerOut{}, err
	}

	if patch.Name != nil {
		row.Name = *patch.Name
	}
	if patch.Slug != nil {
		row.Slug = *patch.Slug
	}
	if patch.Description != nil {
		row.Description = *patch.Description
	}
	if patch.Transport != nil {
		if !validMCPTransports[*patch.Transport] {
			return MCPServerOut{}, unprocessable("transport must be one of: http, sse, stdio")
		}
		row.Transport = *patch.Transport
	}
	if patch.URL != nil {
		row.URL = *patch.URL
	}
	if patch.AuthType != nil {
		if !validMCPAuthTypes[*patch.AuthType] {
			return MCPServerOut{}, unprocessable("auth_type must be one of: none, bearer, header, oauth2")
		}
		row.AuthType = *patch.AuthType
	}
	if patch.Enabled != nil {
		row.Enabled = *patch.Enabled
	}

	// Encrypt probe token if present in patch (nil = no change, "" = clear, non-empty = update).
	var probeEnc *string
	if patch.ProbeToken != nil {
		if *patch.ProbeToken == "" {
			empty := ""
			probeEnc = &empty // clear
		} else {
			enc, err := crypto.EncryptStored(s.fernetKey, *patch.ProbeToken)
			if err != nil {
				return MCPServerOut{}, errors.New("failed to encrypt probe token")
			}
			probeEnc = &enc
		}
	}

	updated, err := s.dal.UpdateMCPServer(ctx, id, tenantID, dal.MCPServerInput{
		TenantID:                row.TenantID,
		Name:                    row.Name,
		Slug:                    row.Slug,
		Description:             row.Description,
		Transport:               row.Transport,
		URL:                     row.URL,
		AuthType:                row.AuthType,
		Enabled:                 row.Enabled,
		ProbeCredentialEncrypted: probeEnc,
	})
	if err != nil {
		if dal.IsNoRows(err) {
			return MCPServerOut{}, ErrNotFound
		}
		if dal.IsUniqueViolation(err) {
			return MCPServerOut{}, ErrConflict
		}
		return MCPServerOut{}, err
	}
	return toMCPServerOut(updated), nil
}

// Delete hard-deletes an MCP server.
func (s *MCPServerService) Delete(ctx context.Context, id, tenantID string) error {
	err := s.dal.DeleteMCPServer(ctx, id, tenantID)
	if err != nil {
		if dal.IsNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// SetCredential encrypts and upserts a per-application credential.
func (s *MCPServerService) SetCredential(ctx context.Context, applicationID, serverID string, body MCPCredentialSet) error {
	if body.Credential == "" {
		return validation("credential is required")
	}
	headerName := body.AuthHeaderName
	if headerName == "" {
		headerName = "Authorization"
	}
	enc, err := crypto.EncryptStored(s.fernetKey, body.Credential)
	if err != nil {
		return errors.New("failed to encrypt credential")
	}
	return s.dal.UpsertAppMCPCredential(ctx, applicationID, serverID, enc, headerName)
}

// DeleteCredential removes a per-application credential.
func (s *MCPServerService) DeleteCredential(ctx context.Context, applicationID, serverID string) error {
	err := s.dal.DeleteAppMCPCredential(ctx, applicationID, serverID)
	if err != nil {
		if dal.IsNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// ListCredentials returns credential metadata (no plaintext) for an application.
func (s *MCPServerService) ListCredentials(ctx context.Context, applicationID string) ([]dal.AppMCPCredentialMeta, error) {
	return s.dal.ListAppMCPCredentials(ctx, applicationID)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func toMCPServerOut(r dal.MCPServer) MCPServerOut {
	var lastChecked *string
	if r.LastCheckedAt != nil {
		s := r.LastCheckedAt.UTC().Format("2006-01-02T15:04:05Z")
		lastChecked = &s
	}
	toolsCount := 0
	if len(r.ToolsManifest) > 0 && string(r.ToolsManifest) != "[]" {
		var tools []json.RawMessage
		if json.Unmarshal(r.ToolsManifest, &tools) == nil {
			toolsCount = len(tools)
		}
	}
	tm := r.ToolsManifest
	if len(tm) == 0 {
		tm = json.RawMessage("[]")
	}
	caps := r.Capabilities
	if len(caps) == 0 {
		caps = json.RawMessage("{}")
	}
	return MCPServerOut{
		ID:                 r.ID,
		TenantID:           r.TenantID,
		Name:               r.Name,
		Slug:               r.Slug,
		Description:        r.Description,
		Transport:          r.Transport,
		URL:                r.URL,
		AuthType:           r.AuthType,
		HealthStatus:       r.HealthStatus,
		LastCheckedAt:      lastChecked,
		LastError:          r.LastError,
		ToolsManifest:      tm,
		ToolsCount:         toolsCount,
		Capabilities:       caps,
		Enabled:            r.Enabled,
		ProbeCredentialSet: r.ProbeCredentialSet,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}
