package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/aviciot/them/internal/crypto"
)

// ExecuteRequest is the body of POST /internal/execute.
type ExecuteRequest struct {
	ApplicationID  string         `json:"application_id"`
	MCPServerSlug  string         `json:"mcp_server_slug"`
	ToolName       string         `json:"tool_name"`
	Arguments      map[string]any `json:"arguments"`
	TestCredential string         `json:"test_credential,omitempty"` // canvas test only
}

// ExecuteResponse is returned by POST /internal/execute.
type ExecuteResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Executor handles MCP tool calls from the orchestrator.
type Executor struct {
	dal       *DAL
	registry  *Registry
	secretKey []byte
}

// NewExecutor creates an Executor. secretKey is the Fernet key for credential decryption.
func NewExecutor(dal *DAL, registry *Registry, secretKey string) *Executor {
	return &Executor{
		dal:       dal,
		registry:  registry,
		secretKey: crypto.DeriveKey(secretKey),
	}
}

// Execute resolves credentials for the given application + MCP server slug,
// calls the tool, and returns the result.
func (e *Executor) Execute(ctx context.Context, req ExecuteRequest) ExecuteResponse {
	if req.ApplicationID == "" || req.MCPServerSlug == "" || req.ToolName == "" {
		return ExecuteResponse{Error: "application_id, mcp_server_slug, and tool_name are required"}
	}

	// 1. Resolve tenant from application.
	tenantID, err := e.dal.GetApplicationTenantID(ctx, req.ApplicationID)
	if err != nil {
		return ExecuteResponse{Error: fmt.Sprintf("application not found: %v", err)}
	}

	// 2. Resolve MCP server — tenant-scoped.
	server, err := e.dal.GetServerBySlugAndTenant(ctx, req.MCPServerSlug, tenantID)
	if err != nil {
		return ExecuteResponse{Error: fmt.Sprintf("mcp server %q not found for tenant: %v", req.MCPServerSlug, err)}
	}

	// 3. Resolve credential.
	authHeaderName, authValue, err := e.resolveCredential(ctx, req, server)
	if err != nil {
		return ExecuteResponse{Error: err.Error()}
	}

	// 4. Validate tool is in manifest (when manifest is non-empty).
	if err := e.validateTool(server, req.ToolName); err != nil {
		return ExecuteResponse{Error: err.Error()}
	}

	// 5. Call the MCP tool.
	client := NewClient(server.URL, authHeaderName, authValue)
	result, err := client.Call(ctx, req.ToolName, req.Arguments)
	if err != nil {
		return ExecuteResponse{Error: fmt.Sprintf("mcp call failed: %v", err)}
	}

	raw, _ := json.Marshal(result)
	return ExecuteResponse{Result: raw}
}

func (e *Executor) resolveCredential(ctx context.Context, req ExecuteRequest, server Server) (headerName, value string, err error) {
	if server.AuthType == "none" {
		return "", "", nil
	}

	// Canvas test credential takes precedence (never persisted).
	if req.TestCredential != "" {
		return authHeaderName(server), formatAuthValue(server.AuthType, req.TestCredential), nil
	}

	// Look up per-application credential.
	cred, err := e.dal.GetCredential(ctx, req.ApplicationID, server.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", fmt.Errorf(
				"no credential configured for application %s and mcp server %q — set one in Application settings",
				req.ApplicationID, server.Slug,
			)
		}
		return "", "", fmt.Errorf("credential lookup failed: %v", err)
	}

	if cred.CredentialEncrypted == "" {
		return "", "", fmt.Errorf("credential for mcp server %q is empty", server.Slug)
	}

	plaintext, err := crypto.DecryptStored(e.secretKey, cred.CredentialEncrypted)
	if err != nil {
		return "", "", fmt.Errorf("credential decrypt failed for mcp server %q: %v", server.Slug, err)
	}

	return cred.AuthHeaderName, formatAuthValue(server.AuthType, plaintext), nil
}

func authHeaderName(s Server) string {
	if s.AuthType == "header" {
		return "Authorization" // default; real value comes from app_mcp_credentials
	}
	return "Authorization"
}

func formatAuthValue(authType, credential string) string {
	switch authType {
	case "bearer":
		if !strings.HasPrefix(credential, "Bearer ") {
			return "Bearer " + credential
		}
		return credential
	case "header":
		// credential is the raw header value; header name comes from the DB row
		return credential
	default:
		return credential
	}
}

func (e *Executor) validateTool(server Server, toolName string) error {
	if len(server.ToolsManifest) == 0 || string(server.ToolsManifest) == "[]" {
		return nil // no manifest yet — allow, MCP server will reject if tool doesn't exist
	}
	var tools []Tool
	if err := json.Unmarshal(server.ToolsManifest, &tools); err != nil {
		return nil // can't parse manifest — allow through
	}
	for _, t := range tools {
		if t.Name == toolName {
			return nil
		}
	}
	return fmt.Errorf("tool %q not found in manifest for mcp server %q", toolName, server.Slug)
}
