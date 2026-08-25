package service_test

// MCP server service unit tests — no DB or Redis required.
//
// Coverage:
//   S1-MCP-01  Create — missing name → ErrValidation
//   S1-MCP-02  Create — missing slug → ErrValidation
//   S1-MCP-03  Create — invalid transport → ErrUnprocessable
//   S1-MCP-04  Create — invalid auth_type → ErrUnprocessable
//   S1-MCP-05  Create — defaults applied (transport=http, auth_type=none, enabled=true)
//   S1-MCP-06  Update — not found → ErrNotFound
//   S1-MCP-07  Update — applies patch fields, preserves unset fields
//   S1-MCP-08  Delete — not found → ErrNotFound
//   S1-MCP-09  SetCredential — empty credential → ErrValidation
//   S1-MCP-10  SetCredential — encrypts and upserts
//   S1-MCP-11  SetCredential — empty auth_header_name defaults to "Authorization"
//   S1-MCP-12  Create — probe_token encrypted before DAL write
//   S1-MCP-13  Create — no probe_token → DAL receives nil (no column write)
//   S1-MCP-14  Update — probe_token="" → DAL receives pointer-to-"" (clear)
//   S1-MCP-15  Update — probe_token absent → DAL receives nil (no change)

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

const mcpTestSecretKey = "mcp-service-test-secret-do-not-use"

func newMCPSvc(d *fakeDal) *service.MCPServerService {
	return service.NewMCPServerService(d, mcpTestSecretKey)
}

// ── Create validation ─────────────────────────────────────────────────────────

func TestMCPServerService_Create_MissingName(t *testing.T) {
	svc := newMCPSvc(&fakeDal{})
	_, err := svc.Create(context.Background(), "tenant-1", service.MCPServerCreate{Slug: "my-server"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestMCPServerService_Create_MissingSlug(t *testing.T) {
	svc := newMCPSvc(&fakeDal{})
	_, err := svc.Create(context.Background(), "tenant-1", service.MCPServerCreate{Name: "My Server"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestMCPServerService_Create_InvalidTransport(t *testing.T) {
	svc := newMCPSvc(&fakeDal{})
	_, err := svc.Create(context.Background(), "tenant-1", service.MCPServerCreate{
		Name:      "My Server",
		Slug:      "my-server",
		Transport: "grpc", // invalid
	})
	if !errors.Is(err, service.ErrUnprocessable) {
		t.Errorf("want ErrUnprocessable, got %v", err)
	}
}

func TestMCPServerService_Create_InvalidAuthType(t *testing.T) {
	svc := newMCPSvc(&fakeDal{})
	_, err := svc.Create(context.Background(), "tenant-1", service.MCPServerCreate{
		Name:     "My Server",
		Slug:     "my-server",
		AuthType: "api_key", // invalid
	})
	if !errors.Is(err, service.ErrUnprocessable) {
		t.Errorf("want ErrUnprocessable, got %v", err)
	}
}

func TestMCPServerService_Create_DefaultsApplied(t *testing.T) {
	created := dal.MCPServer{
		ID:           "srv-1",
		TenantID:     "tenant-1",
		Name:         "My Server",
		Slug:         "my-server",
		Transport:    "http",
		AuthType:     "none",
		HealthStatus: "unknown",
		Enabled:      true,
	}
	d := &fakeDal{mcpCreated: created}
	svc := newMCPSvc(d)

	out, err := svc.Create(context.Background(), "tenant-1", service.MCPServerCreate{
		Name: "My Server",
		Slug: "my-server",
		// Transport and AuthType omitted — should default
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Transport != "http" {
		t.Errorf("want transport=http, got %q", out.Transport)
	}
	if out.AuthType != "none" {
		t.Errorf("want auth_type=none, got %q", out.AuthType)
	}
	if !out.Enabled {
		t.Error("want enabled=true by default")
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestMCPServerService_Update_NotFound(t *testing.T) {
	d := &fakeDal{getMCPErr: pgx.ErrNoRows}
	svc := newMCPSvc(d)
	_, err := svc.Update(context.Background(), "srv-1", "tenant-1", service.MCPServerPatch{})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestMCPServerService_Update_AppliesPatch(t *testing.T) {
	enabled := true
	original := dal.MCPServer{
		ID:        "srv-1",
		TenantID:  "t1",
		Name:      "Old Name",
		Slug:      "old-slug",
		Transport: "http",
		AuthType:  "none",
		Enabled:   true,
	}
	updated := dal.MCPServer{
		ID:        "srv-1",
		TenantID:  "t1",
		Name:      "New Name",
		Slug:      "old-slug", // unchanged
		Transport: "sse",
		AuthType:  "none",
		Enabled:   true,
	}
	d := &fakeDal{mcpServer: original, mcpUpdated: updated}
	svc := newMCPSvc(d)

	newName := "New Name"
	newTransport := "sse"
	out, err := svc.Update(context.Background(), "srv-1", "t1", service.MCPServerPatch{
		Name:      &newName,
		Transport: &newTransport,
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Name != "New Name" {
		t.Errorf("want name=New Name, got %q", out.Name)
	}
	if out.Transport != "sse" {
		t.Errorf("want transport=sse, got %q", out.Transport)
	}
	if out.Slug != "old-slug" {
		t.Errorf("slug should be unchanged, got %q", out.Slug)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestMCPServerService_Delete_NotFound(t *testing.T) {
	d := &fakeDal{deleteMCPErr: pgx.ErrNoRows}
	svc := newMCPSvc(d)
	err := svc.Delete(context.Background(), "srv-1", "tenant-1")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// ── Credential management ─────────────────────────────────────────────────────

func TestMCPServerService_SetCredential_Empty(t *testing.T) {
	svc := newMCPSvc(&fakeDal{})
	err := svc.SetCredential(context.Background(), "app-1", "srv-1", service.MCPCredentialSet{
		Credential: "", // empty
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestMCPServerService_SetCredential_EncryptsAndUpserts(t *testing.T) {
	d := &fakeDal{}
	svc := newMCPSvc(d)
	err := svc.SetCredential(context.Background(), "app-1", "srv-1", service.MCPCredentialSet{
		Credential:     "my-secret-token",
		AuthHeaderName: "Authorization",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.upsertCredCalled {
		t.Error("want UpsertAppMCPCredential to be called")
	}
}

// ── Probe token ───────────────────────────────────────────────────────────────

func TestMCPServerService_Create_ProbeTokenEncrypted(t *testing.T) {
	// S1-MCP-12: Create with probe_token → DAL receives non-empty encrypted value.
	d := &fakeDal{mcpCreated: dal.MCPServer{ID: "srv-1", Name: "S", Slug: "s", AuthType: "bearer", Transport: "http", HealthStatus: "unknown", Enabled: true}}
	svc := newMCPSvc(d)
	_, err := svc.Create(context.Background(), "tenant-1", service.MCPServerCreate{
		Name:       "S",
		Slug:       "s",
		AuthType:   "bearer",
		ProbeToken: "my-probe-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.lastCreateMCPInput.ProbeCredentialEncrypted == nil {
		t.Fatal("want ProbeCredentialEncrypted to be set in DAL input")
	}
	if *d.lastCreateMCPInput.ProbeCredentialEncrypted == "" {
		t.Error("want non-empty encrypted probe credential in DAL input")
	}
	if *d.lastCreateMCPInput.ProbeCredentialEncrypted == "my-probe-token" {
		t.Error("probe token must be encrypted, not stored as plaintext")
	}
}

func TestMCPServerService_Create_NoProbeToken_NilInput(t *testing.T) {
	// S1-MCP-13: Create without probe_token → DAL receives nil (no column write).
	d := &fakeDal{mcpCreated: dal.MCPServer{ID: "srv-1", Name: "S", Slug: "s", Transport: "http", HealthStatus: "unknown", Enabled: true}}
	svc := newMCPSvc(d)
	_, err := svc.Create(context.Background(), "tenant-1", service.MCPServerCreate{Name: "S", Slug: "s"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.lastCreateMCPInput.ProbeCredentialEncrypted != nil {
		t.Error("want ProbeCredentialEncrypted nil when no probe_token provided")
	}
}

func TestMCPServerService_Update_ProbeTokenCleared(t *testing.T) {
	// S1-MCP-14: PATCH with probe_token="" → DAL receives pointer to "" (clear).
	original := dal.MCPServer{ID: "srv-1", TenantID: "t1", Name: "S", Slug: "s", Transport: "http", AuthType: "bearer", Enabled: true}
	d := &fakeDal{mcpServer: original, mcpUpdated: original}
	svc := newMCPSvc(d)
	empty := ""
	_, err := svc.Update(context.Background(), "srv-1", "t1", service.MCPServerPatch{ProbeToken: &empty})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.lastUpdateMCPInput.ProbeCredentialEncrypted == nil {
		t.Fatal("want ProbeCredentialEncrypted to be non-nil (clear signal)")
	}
	if *d.lastUpdateMCPInput.ProbeCredentialEncrypted != "" {
		t.Errorf("want empty string for clear, got %q", *d.lastUpdateMCPInput.ProbeCredentialEncrypted)
	}
}

func TestMCPServerService_Update_ProbeTokenAbsent_NilInput(t *testing.T) {
	// S1-MCP-15: PATCH without probe_token field → DAL receives nil (no change).
	original := dal.MCPServer{ID: "srv-1", TenantID: "t1", Name: "S", Slug: "s", Transport: "http", AuthType: "bearer", Enabled: true}
	d := &fakeDal{mcpServer: original, mcpUpdated: original}
	svc := newMCPSvc(d)
	newName := "New Name"
	_, err := svc.Update(context.Background(), "srv-1", "t1", service.MCPServerPatch{Name: &newName})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.lastUpdateMCPInput.ProbeCredentialEncrypted != nil {
		t.Error("want ProbeCredentialEncrypted nil when probe_token not in patch")
	}
}

func TestMCPServerService_SetCredential_DefaultsHeaderName(t *testing.T) {
	d := &fakeDal{}
	svc := newMCPSvc(d)
	err := svc.SetCredential(context.Background(), "app-1", "srv-1", service.MCPCredentialSet{
		Credential:     "my-secret-token",
		AuthHeaderName: "", // empty — should default to "Authorization"
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.upsertCredHeader != "Authorization" {
		t.Errorf("want header=Authorization, got %q", d.upsertCredHeader)
	}
}
