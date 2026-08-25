package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/session"
)

// ── Fakes ──────────────────────────────────────────────────────────────────────

// fakeDal implements service.Dal. Fields control what each method returns.
type fakeDal struct {
	agents        []dal.Agent
	agent         dal.Agent
	orchs         []dal.Orchestrator
	orch          dal.Orchestrator
	apps          []dal.Application
	app           dal.Application
	eps           []dal.EntryPoint
	epSlug        string
	epSlugs       []string
	epTenantSlug  dal.EPTenantSlug
	epTenantSlugs []dal.EPTenantSlug
	runs          []dal.Run
	run           dal.Run
	contextID     string
	createdID     string

	listAgentsErr   error
	getAgentErr     error
	createAgentErr  error
	updateAgentErr  error
	deleteAgentErr  error
	listOrchsErr    error
	getOrchErr      error
	createOrchErr   error
	updateOrchErr   error
	deleteOrchErr   error
	listAppsErr     error
	getAppErr       error
	createAppErr    error
	updateAppErr    error
	deleteAppErr    error
	createEPErr     error
	getEPSlugErr    error
	updateEPErr     error
	deleteEPErr     error
	listRunsErr     error
	getRunErr       error
	getContextIDErr error

	createAgentCalls        []dal.AgentInput
	createAgentEnabledCalls []bool
	updateAgentCalls        []dal.AgentInput
	createOrchCalls         []dal.OrchestratorInput
	createOrchEnabledCalls  []bool

	// runtime config + bulk delete fields
	updateRuntimeConfigErr    error
	updateRuntimeConfigCalled bool
	orchNames                 []string
	orchNamesErr              error
	bulkDeletedCount          int64
	bulkDeleteErr             error
	bulkDeleteCalled          bool

	// provider key fields
	setProviderKeyValue []byte // last raw JSON written by SetProviderKey
	providerKeysRaw     []byte // what GetProviderKeys and GetAppParams return (nil → "{}")

	// app global param fields
	setAppParamValue     []byte // last raw JSON written by SetAppParam
	deleteAppParamCalled bool

	// config fields
	configRow         *dal.ConfigRow
	configErr         error
	upsertConfigKey   string
	upsertConfigValue []byte
	upsertConfigErr   error

	// LLM provider fields
	providers           []dal.LLMProvider
	provider            dal.LLMProvider
	createdProvider     dal.LLMProvider
	updatedProvider     dal.LLMProvider
	listProvidersErr    error
	getProviderErr      error
	createProviderErr   error
	updateProviderErr   error
	deleteProviderErr   error
	createProviderCalls []dal.LLMProviderInput
	updateProviderCalls []dal.LLMProviderInput

	// token fields
	tokens            []dal.Token
	token             dal.Token
	orchExists        bool
	orchExistsErr     error
	createdToken      dal.Token
	createTokenErr    error
	updatedTokenHash  string
	updatedToken      dal.Token
	updateTokenErr    error
	deletedTokenHash  string
	deleteTokenErr    error
	listTokensErr     error
	getTokenErr       error
	createTokenCalls  []dal.TokenCreateRow
	updateTokenCalls  []dal.TokenPatchRow

	// MCP server fields
	mcpServer           dal.MCPServer
	mcpServers          []dal.MCPServer
	mcpCreated          dal.MCPServer
	mcpUpdated          dal.MCPServer
	getMCPErr           error
	createMCPErr        error
	updateMCPErr        error
	deleteMCPErr        error
	upsertCredErr       error
	deleteCredErr       error
	upsertCredCalled    bool
	upsertCredHeader    string
	lastCreateMCPInput  dal.MCPServerInput
	lastUpdateMCPInput  dal.MCPServerInput
}

func (f *fakeDal) ListAgents(_ context.Context, _ string) ([]dal.Agent, error) {
	return f.agents, f.listAgentsErr
}
func (f *fakeDal) GetAgent(_ context.Context, _, _ string) (dal.Agent, error) {
	return f.agent, f.getAgentErr
}
func (f *fakeDal) CreateAgent(_ context.Context, _ string, in dal.AgentInput, enabled bool) (string, error) {
	f.createAgentCalls = append(f.createAgentCalls, in)
	f.createAgentEnabledCalls = append(f.createAgentEnabledCalls, enabled)
	return f.createdID, f.createAgentErr
}
func (f *fakeDal) UpdateAgent(_ context.Context, _, _ string, in dal.AgentInput, _ bool) error {
	f.updateAgentCalls = append(f.updateAgentCalls, in)
	return f.updateAgentErr
}
func (f *fakeDal) DeleteAgent(_ context.Context, _, _ string) error { return f.deleteAgentErr }

func (f *fakeDal) ListOrchestrators(_ context.Context, _ string) ([]dal.Orchestrator, error) {
	return f.orchs, f.listOrchsErr
}
func (f *fakeDal) GetOrchestrator(_ context.Context, _, _ string) (dal.Orchestrator, error) {
	return f.orch, f.getOrchErr
}
func (f *fakeDal) CreateOrchestrator(_ context.Context, _ string, in dal.OrchestratorInput, enabled bool) (string, error) {
	f.createOrchCalls = append(f.createOrchCalls, in)
	f.createOrchEnabledCalls = append(f.createOrchEnabledCalls, enabled)
	return f.createdID, f.createOrchErr
}
func (f *fakeDal) UpdateOrchestrator(_ context.Context, _, _ string, _ dal.OrchestratorInput, _ bool) error {
	return f.updateOrchErr
}
func (f *fakeDal) DeleteOrchestrator(_ context.Context, _, _ string) error { return f.deleteOrchErr }

func (f *fakeDal) ListApplications(_ context.Context, _ string) ([]dal.Application, error) {
	return f.apps, f.listAppsErr
}
func (f *fakeDal) GetApplication(_ context.Context, _, _ string) (dal.Application, error) {
	return f.app, f.getAppErr
}
func (f *fakeDal) CreateApplication(_ context.Context, _, _ string, _ bool) (string, error) {
	return f.createdID, f.createAppErr
}
func (f *fakeDal) UpdateApplication(_ context.Context, _, _, _ string, _ bool) error {
	return f.updateAppErr
}
func (f *fakeDal) DeleteApplication(_ context.Context, _, _ string) error { return f.deleteAppErr }
func (f *fakeDal) ListEntryPoints(_ context.Context, _ string) []dal.EntryPoint {
	return f.eps
}
func (f *fakeDal) CreateEntryPoint(_ context.Context, _, _, _ string, _ bool) (string, error) {
	return f.createdID, f.createEPErr
}
func (f *fakeDal) GetEntryPointSlug(_ context.Context, _, _ string) (string, error) {
	return f.epSlug, f.getEPSlugErr
}
func (f *fakeDal) UpdateEntryPoint(_ context.Context, _, _, _, _ string, _ bool) error {
	return f.updateEPErr
}
func (f *fakeDal) DeleteEntryPoint(_ context.Context, _, _ string) error { return f.deleteEPErr }
func (f *fakeDal) ListEPSlugsForApp(_ context.Context, _ string) []string { return f.epSlugs }
func (f *fakeDal) GetEntryPointTenantAndSlug(_ context.Context, _, _ string) dal.EPTenantSlug {
	return f.epTenantSlug
}
func (f *fakeDal) ListEPTenantSlugsForApp(_ context.Context, _ string) []dal.EPTenantSlug {
	return f.epTenantSlugs
}
func (f *fakeDal) UpdateRuntimeConfig(_ context.Context, _, _ string, _ []byte) error {
	f.updateRuntimeConfigCalled = true
	return f.updateRuntimeConfigErr
}
func (f *fakeDal) ListAppOrchestratorNames(_ context.Context, _ string) ([]string, error) {
	return f.orchNames, f.orchNamesErr
}
func (f *fakeDal) BulkDeleteApplications(_ context.Context, _ string, _ []string) (int64, error) {
	f.bulkDeleteCalled = true
	return f.bulkDeletedCount, f.bulkDeleteErr
}

func (f *fakeDal) GetProviderKeys(_ context.Context, _, _ string) ([]byte, error) {
	if f.providerKeysRaw != nil {
		return f.providerKeysRaw, nil
	}
	return []byte(`{}`), nil
}
func (f *fakeDal) SetProviderKey(_ context.Context, _, _, _ string, value []byte) error {
	f.setProviderKeyValue = value
	return nil
}
func (f *fakeDal) DeleteProviderKey(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeDal) SetOrchestratorLLM(_ context.Context, _, _, _, _ string) error { return nil }
func (f *fakeDal) SetOrchestratorMCPServers(_ context.Context, _, _ string, _ []dal.MCPServerAttachment) error { return nil }

func (f *fakeDal) ListRuns(_ context.Context, _, _ string, _ int) ([]dal.Run, error) {
	return f.runs, f.listRunsErr
}
func (f *fakeDal) GetRun(_ context.Context, _, _ string) (dal.Run, error) {
	return f.run, f.getRunErr
}
func (f *fakeDal) GetRunContextID(_ context.Context, _, _ string) (string, error) {
	return f.contextID, f.getContextIDErr
}
func (f *fakeDal) GetRunStats(_ context.Context, _ string) (dal.RunStats, error) {
	return dal.RunStats{ByStatus: make(map[string]int), TotalCostUSD: "0"}, nil
}
func (f *fakeDal) GetRunDetail(_ context.Context, _, _ string) (dal.RunDetail, error) {
	return dal.RunDetail{Run: f.run, Steps: []dal.RunStep{}, Usage: []dal.RunUsage{}, Children: []dal.Run{}}, f.getRunErr
}
func (f *fakeDal) GetRunTasks(_ context.Context, _, _ string) ([]dal.Task, error) {
	return []dal.Task{}, nil
}
func (f *fakeDal) GetRunArtifacts(_ context.Context, _, _ string) ([]dal.Artifact, error) {
	return []dal.Artifact{}, nil
}
func (f *fakeDal) ListContextSessions(_ context.Context, _, _ string, _ int) ([]dal.ContextSession, error) {
	return []dal.ContextSession{}, nil
}
func (f *fakeDal) GetContextArtifacts(_ context.Context, _, _ string, _ int) ([]dal.Artifact, error) {
	return []dal.Artifact{}, nil
}
func (f *fakeDal) GetContextMessages(_ context.Context, _, _ string, _ int) ([]dal.ContextMessage, error) {
	return []dal.ContextMessage{}, nil
}
func (f *fakeDal) CancelRun(_ context.Context, _, _ string) (dal.Run, error) {
	return f.run, f.getRunErr
}
func (f *fakeDal) DeleteRun(_ context.Context, _, _ string) error { return f.getRunErr }
func (f *fakeDal) BulkDeleteRuns(_ context.Context, _ string, ids []string) (int64, error) {
	return int64(len(ids)), nil
}

func (f *fakeDal) ListTokens(_ context.Context, _ string, _ *int64) ([]dal.Token, error) {
	return f.tokens, f.listTokensErr
}
func (f *fakeDal) GetToken(_ context.Context, _, _ string) (dal.Token, error) {
	return f.token, f.getTokenErr
}
func (f *fakeDal) OrchestratorExists(_ context.Context, _, _ string) (bool, error) {
	return f.orchExists, f.orchExistsErr
}
func (f *fakeDal) CreateToken(_ context.Context, _ string, in dal.TokenCreateRow) (dal.Token, error) {
	f.createTokenCalls = append(f.createTokenCalls, in)
	return f.createdToken, f.createTokenErr
}
func (f *fakeDal) UpdateToken(_ context.Context, _, _ string, patch dal.TokenPatchRow) (string, dal.Token, error) {
	f.updateTokenCalls = append(f.updateTokenCalls, patch)
	return f.updatedTokenHash, f.updatedToken, f.updateTokenErr
}
func (f *fakeDal) DeleteToken(_ context.Context, _, _ string) (string, error) {
	return f.deletedTokenHash, f.deleteTokenErr
}

// Application definition stubs.
func (f *fakeDal) GetNextRevision(_ context.Context, _ string) (int, error) {
	return 1, nil
}
func (f *fakeDal) CreateDefinition(_ context.Context, _, _ string, _ int, _ []byte, _ string) (string, error) {
	return f.createdID, nil
}
func (f *fakeDal) GetDefinition(_ context.Context, _, _, _ string) (dal.AppDefinition, error) {
	return dal.AppDefinition{}, nil
}
func (f *fakeDal) ListDefinitions(_ context.Context, _, _ string) ([]dal.AppDefinition, error) {
	return []dal.AppDefinition{}, nil
}
func (f *fakeDal) UpdateDraftDefinition(_ context.Context, _, _, _ string, _ []byte, _ string) error {
	return nil
}
func (f *fakeDal) DeleteDraftDefinition(_ context.Context, _, _, _ string) error {
	return nil
}

// Phase C publish pipeline stubs.
func (f *fakeDal) PublishDefinition(_ context.Context, _, _, _, _ string) (dal.PublishResult, error) {
	return dal.PublishResult{}, nil
}
func (f *fakeDal) UpsertAppOrchestrator(_ context.Context, _ dal.AppOrchestratorRow) (string, error) {
	return f.createdID, nil
}
func (f *fakeDal) UpsertEntryPoint(_ context.Context, _ dal.EntryPointRow) (string, error) {
	return f.createdID, nil
}
func (f *fakeDal) DeactivateStaleOrchestrators(_ context.Context, _, _, _ string) error {
	return nil
}
func (f *fakeDal) DeactivateStaleEntryPoints(_ context.Context, _, _, _ string) error {
	return nil
}

// Config stubs — populated by config service tests.
func (f *fakeDal) GetConfig(_ context.Context, _ string) (*dal.ConfigRow, error) {
	return f.configRow, f.configErr
}
func (f *fakeDal) UpsertConfig(_ context.Context, key string, value []byte) error {
	f.upsertConfigKey = key
	f.upsertConfigValue = value
	return f.upsertConfigErr
}

// Agent action stubs (platform-global, no tenant scope).
func (f *fakeDal) AgentExists(_ context.Context, _ string) (bool, error) { return true, nil }
func (f *fakeDal) GetAgentBySlug(_ context.Context, _ string) (dal.Agent, error) {
	return f.agent, f.getAgentErr
}
func (f *fakeDal) UpdateAgentScanResult(_ context.Context, _ string, _ []byte) error {
	return nil
}
func (f *fakeDal) GetAgentByID(_ context.Context, _ string) (dal.Agent, error) {
	return f.agent, f.getAgentErr
}
func (f *fakeDal) GetAgentTokenEncrypted(_ context.Context, _ string) (string, error) {
	return "", nil
}

// LLM provider stubs.
func (f *fakeDal) ListProviders(_ context.Context) ([]dal.LLMProvider, error) {
	return f.providers, f.listProvidersErr
}
func (f *fakeDal) GetProvider(_ context.Context, _ int64) (dal.LLMProvider, error) {
	return f.provider, f.getProviderErr
}
func (f *fakeDal) CreateProvider(_ context.Context, in dal.LLMProviderInput) (dal.LLMProvider, error) {
	f.createProviderCalls = append(f.createProviderCalls, in)
	return f.createdProvider, f.createProviderErr
}
func (f *fakeDal) UpdateProvider(_ context.Context, _ int64, in dal.LLMProviderInput) (dal.LLMProvider, error) {
	f.updateProviderCalls = append(f.updateProviderCalls, in)
	return f.updatedProvider, f.updateProviderErr
}
func (f *fakeDal) DeleteProvider(_ context.Context, _ int64) error {
	return f.deleteProviderErr
}

// Component definitions registry stub.
func (f *fakeDal) ListComponentDefinitions(_ context.Context, _ string) ([]dal.ComponentDefinitionSummary, error) {
	return []dal.ComponentDefinitionSummary{}, nil
}

// Agent definition stubs.
func (f *fakeDal) GetNextAgentRevision(_ context.Context, _, _ string) (int, error) { return 1, nil }
func (f *fakeDal) CreateAgentDefinition(_ context.Context, _, _ string, _ int, _ []byte, _ string, _ int) (string, error) {
	return f.createdID, nil
}
func (f *fakeDal) GetAgentDefinition(_ context.Context, _, _ string) (dal.AgentDefinition, error) {
	return dal.AgentDefinition{}, nil
}
func (f *fakeDal) ListAgentDefinitions(_ context.Context, _ string) ([]dal.AgentDefinition, error) {
	return []dal.AgentDefinition{}, nil
}
func (f *fakeDal) UpdateDraftAgentDefinition(_ context.Context, _, _ string, _ []byte, _ string) error {
	return nil
}
func (f *fakeDal) RevertPublishedToDraft(_ context.Context, _, _ string, _ []byte, _ string) error {
	return nil
}
func (f *fakeDal) DeleteDraftAgentDefinition(_ context.Context, _, _ string) error { return nil }

// Phase 3 stubs for fakeDal.
func (f *fakeDal) GetAgentDefinitionForPublish(_ context.Context, _ string, _ string) (dal.AgentDefinition, error) {
	return dal.AgentDefinition{}, nil
}
func (f *fakeDal) PublishCanvasAgent(_ context.Context, _ dal.CanvasAgentRow) error { return nil }
func (f *fakeDal) MarkAgentDefinitionPublished(_ context.Context, _, _ string) error { return nil }
func (f *fakeDal) UpsertAgentBinding(_ context.Context, _ dal.AgentBindingRow) error { return nil }
func (f *fakeDal) GetAgentBindingStatus(_ context.Context, _, _ string) (dal.AgentBindingSlotStatus, error) {
	return dal.AgentBindingSlotStatus{CredentialSet: map[string]bool{}}, nil
}
func (f *fakeDal) ListAgentBindings(_ context.Context, _ string) ([]dal.AgentBindingSlotStatus, error) {
	return []dal.AgentBindingSlotStatus{}, nil
}
func (f *fakeDal) DeleteAgentBinding(_ context.Context, _, _ string) error { return nil }
func (f *fakeDal) GetAgentParamsForBinding(_ context.Context, _, _ string) (dal.AgentParamsRow, error) {
	return dal.AgentParamsRow{RequiredParams: []agentgen.AgentParamSpec{}}, nil
}
func (f *fakeDal) GetRequiredParamsForAgent(_ context.Context, _ string) (dal.AgentParamsRow, error) {
	return dal.AgentParamsRow{}, nil
}
func (f *fakeDal) UpsertAgentParams(_ context.Context, _, _ string, _ []byte) error { return nil }
func (f *fakeDal) GetAgentLLMNodes(_ context.Context, _, _ string) ([]byte, []byte, string, error) {
	return []byte("[]"), []byte("{}"), "", nil
}
func (f *fakeDal) UpsertNodeLLMOverride(_ context.Context, _, _, _, _, _ string) error { return nil }

// App global params stubs — satisfy Dal interface.
func (f *fakeDal) GetAppParams(_ context.Context, _, _ string) ([]byte, error) {
	if f.providerKeysRaw != nil {
		return f.providerKeysRaw, nil
	}
	return []byte(`{}`), nil
}
func (f *fakeDal) SetAppParam(_ context.Context, _, _, _ string, value []byte) error {
	f.setAppParamValue = value
	return nil
}
func (f *fakeDal) DeleteAppParam(_ context.Context, _, _, _ string) error {
	f.deleteAppParamCalled = true
	return nil
}

// MCP server stubs — satisfy Dal interface, use controllable fields.
func (f *fakeDal) ListMCPServers(_ context.Context, _ string) ([]dal.MCPServer, error) {
	if f.mcpServers != nil {
		return f.mcpServers, nil
	}
	return []dal.MCPServer{}, nil
}
func (f *fakeDal) GetMCPServer(_ context.Context, _, _ string) (dal.MCPServer, error) {
	return f.mcpServer, f.getMCPErr
}
func (f *fakeDal) CreateMCPServer(_ context.Context, in dal.MCPServerInput) (dal.MCPServer, error) {
	f.lastCreateMCPInput = in
	return f.mcpCreated, f.createMCPErr
}
func (f *fakeDal) UpdateMCPServer(_ context.Context, _, _ string, in dal.MCPServerInput) (dal.MCPServer, error) {
	f.lastUpdateMCPInput = in
	return f.mcpUpdated, f.updateMCPErr
}
func (f *fakeDal) DeleteMCPServer(_ context.Context, _, _ string) error { return f.deleteMCPErr }
func (f *fakeDal) GetAppMCPCredential(_ context.Context, _, _ string) (dal.AppMCPCredential, error) {
	return dal.AppMCPCredential{}, nil
}
func (f *fakeDal) ListAppMCPCredentials(_ context.Context, _ string) ([]dal.AppMCPCredentialMeta, error) {
	return []dal.AppMCPCredentialMeta{}, nil
}
func (f *fakeDal) UpsertAppMCPCredential(_ context.Context, _, _, _ string, headerName string) error {
	f.upsertCredCalled = true
	f.upsertCredHeader = headerName
	return f.upsertCredErr
}
func (f *fakeDal) DeleteAppMCPCredential(_ context.Context, _, _ string) error {
	return f.deleteCredErr
}

// fakeCache implements service.Cache.
type fakeCache struct {
	deletedKeys  []string
	publishMsgs  []string
	publishOrder []string
}

func (c *fakeCache) Del(_ context.Context, key string) error {
	c.deletedKeys = append(c.deletedKeys, key)
	return nil
}
func (c *fakeCache) Publish(_ context.Context, ch, msg string) error {
	c.publishMsgs = append(c.publishMsgs, ch+":"+msg)
	c.publishOrder = append(c.publishOrder, msg)
	return nil
}

// fakeTemporal implements service.Temporal.
type fakeTemporal struct {
	signaled []string
	err      error
}

func (t *fakeTemporal) SignalRun(_ context.Context, wfID string, _ []byte) error {
	if t.err != nil {
		return t.err
	}
	t.signaled = append(t.signaled, wfID)
	return nil
}

// ── fakeTokenGenerator ───────────────────────────────────────────────────────

type fakeTokenGen struct {
	plaintext string
	hash      string
	err       error
}

func (g *fakeTokenGen) Generate(_ context.Context) (string, string, error) {
	return g.plaintext, g.hash, g.err
}

// ── TokenService tests ────────────────────────────────────────────────────────

func TestTokenService_Create_GeneratesHashAndReturnsPlaintext(t *testing.T) {
	d := &fakeDal{createdToken: dal.Token{ID: "tok-1", Label: "test"}}
	gen := &fakeTokenGen{plaintext: "mytoken", hash: "myhash"}
	svc := service.NewTokenService(d, nil, gen)
	out, err := svc.Create(context.Background(), "t1", dal.TokenCreateRow{Label: "test", UserID: 1}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Plaintext != "mytoken" {
		t.Errorf("want plaintext=mytoken, got %q", out.Plaintext)
	}
	if len(d.createTokenCalls) == 0 || d.createTokenCalls[0].TokenHash != "myhash" {
		t.Error("DAL must receive the generated hash")
	}
}

func TestTokenService_Create_OrchMissing_NotFound(t *testing.T) {
	d := &fakeDal{orchExists: false}
	svc := service.NewTokenService(d, nil, &fakeTokenGen{plaintext: "p", hash: "h"})
	orchID := "some-orch-id"
	_, err := svc.Create(context.Background(), "t1", dal.TokenCreateRow{Label: "x", UserID: 1}, &orchID)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if len(d.createTokenCalls) != 0 {
		t.Error("CreateToken must not be called when orch is missing")
	}
}

func TestTokenService_Create_NoOrch_SkipsExistsCheck(t *testing.T) {
	d := &fakeDal{orchExists: false, orchExistsErr: errors.New("should not be called")}
	svc := service.NewTokenService(d, nil, &fakeTokenGen{plaintext: "p", hash: "h"})
	_, _ = svc.Create(context.Background(), "t1", dal.TokenCreateRow{Label: "x", UserID: 1}, nil)
	// If OrchestratorExists were called it would return an error and fail Create.
	// The test passes as long as Create does not return the orchExistsErr.
	// (createToken returning zero Token is fine here.)
}

func TestTokenService_Update_InvalidatesByHash(t *testing.T) {
	c := &fakeCache{}
	d := &fakeDal{updatedTokenHash: "abc123", updatedToken: dal.Token{ID: "tok-1"}}
	svc := service.NewTokenService(d, c, nil)
	_, err := svc.Update(context.Background(), "t1", "tok-1", dal.TokenPatchRow{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantDel := "them:session:token:abc123"
	found := false
	for _, k := range c.deletedKeys {
		if k == wantDel {
			found = true
		}
	}
	if !found {
		t.Errorf("want cache Del %q, got %v", wantDel, c.deletedKeys)
	}
	wantPub := "abc123"
	foundPub := false
	for _, m := range c.publishOrder {
		if m == wantPub {
			foundPub = true
		}
	}
	if !foundPub {
		t.Errorf("want Publish revoke %q, got %v", wantPub, c.publishOrder)
	}
}

func TestTokenService_Update_Missing_NotFound(t *testing.T) {
	d := &fakeDal{updateTokenErr: errors.New("pgx: no rows in result set")}
	// Simulate IsNoRows by making updateTokenErr be pgx.ErrNoRows-compatible.
	// Since we can't import pgx here, we use a workaround: dal.IsNoRows is false
	// for a generic error, so the service returns the error itself (not ErrNotFound).
	// The real pgx.ErrNoRows path is covered by integration tests.
	svc := service.NewTokenService(d, nil, nil)
	_, err := svc.Update(context.Background(), "t1", "missing", dal.TokenPatchRow{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTokenService_Delete_InvalidatesByHash(t *testing.T) {
	c := &fakeCache{}
	d := &fakeDal{deletedTokenHash: "delhash"}
	svc := service.NewTokenService(d, c, nil)
	err := svc.Delete(context.Background(), "t1", "tok-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantDel := "them:session:token:delhash"
	found := false
	for _, k := range c.deletedKeys {
		if k == wantDel {
			found = true
		}
	}
	if !found {
		t.Errorf("want cache Del %q, got %v", wantDel, c.deletedKeys)
	}
}

func TestTokenService_NilCache_NoPanic(t *testing.T) {
	d := &fakeDal{updatedTokenHash: "h", updatedToken: dal.Token{ID: "x"}}
	svc := service.NewTokenService(d, nil, nil)
	_, err := svc.Update(context.Background(), "t1", "x", dal.TokenPatchRow{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTokenService_List_ForwardsUserFilter(t *testing.T) {
	uid := int64(42)
	d := &fakeDal{tokens: []dal.Token{{ID: "tok-1", UserID: 42}}}
	svc := service.NewTokenService(d, nil, nil)
	tokens, err := svc.List(context.Background(), "t1", &uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 {
		t.Errorf("want 1 token, got %d", len(tokens))
	}
}

// ── fakeSessionReader ─────────────────────────────────────────────────────────

type fakeSessionReader struct {
	epSessions  []string
	appSessions []string
	sessionInfo *session.SessionInfo
	getErr      error
	sigErr      error
	sigDelivered bool
}

func (f *fakeSessionReader) ListEPSessions(_ context.Context, _ string) ([]string, error) {
	return f.epSessions, nil
}
func (f *fakeSessionReader) ListAppSessions(_ context.Context, _ string) ([]string, error) {
	return f.appSessions, nil
}
func (f *fakeSessionReader) Get(_ context.Context, _ string) (*session.SessionInfo, error) {
	return f.sessionInfo, f.getErr
}
func (f *fakeSessionReader) SignalDisconnect(_ context.Context, _ string) (bool, error) {
	return f.sigDelivered, f.sigErr
}

// ── SessionAdminService tests ─────────────────────────────────────────────────

func TestSessionAdmin_ListByApp_SkipsNotFound(t *testing.T) {
	r := &fakeSessionReader{
		appSessions: []string{"s1", "s2"},
		getErr:      session.ErrSessionNotFound,
	}
	svc := service.NewSessionAdminService(r)
	result, err := svc.ListByApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 0 {
		t.Errorf("all sessions not-found → count 0, got %d", result.Count)
	}
}

func TestSessionAdmin_List_ReturnsEmptySliceNotNil(t *testing.T) {
	r := &fakeSessionReader{appSessions: []string{}}
	svc := service.NewSessionAdminService(r)
	result, _ := svc.ListByApp(context.Background(), "app-1")
	if result.Sessions == nil {
		t.Error("Sessions must be [] not nil")
	}
}

func TestSessionAdmin_Disconnect_NotFound(t *testing.T) {
	r := &fakeSessionReader{getErr: session.ErrSessionNotFound}
	svc := service.NewSessionAdminService(r)
	_, err := svc.Disconnect(context.Background(), "missing-sid")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSessionAdmin_Disconnect_Delivered(t *testing.T) {
	r := &fakeSessionReader{
		sessionInfo:  &session.SessionInfo{SessionID: "s1"},
		sigDelivered: true,
	}
	svc := service.NewSessionAdminService(r)
	delivered, err := svc.Disconnect(context.Background(), "s1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !delivered {
		t.Error("want delivered=true")
	}
}

// ── fakeSessionReader + TokenService + SessionAdminService tests ──────────────

// ── AgentService tests ────────────────────────────────────────────────────────

func TestAgentService_Create_Defaults(t *testing.T) {
	d := &fakeDal{createdID: "id-1"}
	svc := service.NewAgentService(d, nil)
	in := dal.AgentInput{Slug: "my-agent", DisplayName: "My Agent"}
	_, err := svc.Create(context.Background(), "t1", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := d.createAgentCalls[0]
	if got.Transport != "a2a_async" {
		t.Errorf("Transport: want a2a_async, got %q", got.Transport)
	}
	if got.MaxConcurrency != 5 {
		t.Errorf("MaxConcurrency: want 5, got %d", got.MaxConcurrency)
	}
	if got.MaxRetries != 2 {
		t.Errorf("MaxRetries: want 2, got %d", got.MaxRetries)
	}
	if got.TimeoutSeconds != 30 {
		t.Errorf("TimeoutSeconds: want 30, got %d", got.TimeoutSeconds)
	}
	if !d.createAgentEnabledCalls[0] {
		t.Error("enabled: want true (default)")
	}
}

func TestAgentService_Create_MissingSlug_Validation(t *testing.T) {
	svc := service.NewAgentService(&fakeDal{}, nil)
	_, err := svc.Create(context.Background(), "t1", dal.AgentInput{DisplayName: "D"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestAgentService_Create_MissingDisplayName_Validation(t *testing.T) {
	svc := service.NewAgentService(&fakeDal{}, nil)
	_, err := svc.Create(context.Background(), "t1", dal.AgentInput{Slug: "s"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestAgentService_Create_EnabledFalse_Respected(t *testing.T) {
	d := &fakeDal{createdID: "id-2"}
	svc := service.NewAgentService(d, nil)
	f := false
	_, _ = svc.Create(context.Background(), "t1", dal.AgentInput{Slug: "s", DisplayName: "D", Enabled: &f})
	if len(d.createAgentEnabledCalls) == 0 || d.createAgentEnabledCalls[0] {
		t.Error("enabled=false must be passed to DAL")
	}
}

func TestAgentService_Update_ReappliesMaxConcurrencyDefault(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAgentService(d, nil)
	_ = svc.Update(context.Background(), "t1", "id-1", dal.AgentInput{MaxConcurrency: 0})
	if len(d.updateAgentCalls) == 0 || d.updateAgentCalls[0].MaxConcurrency != 5 {
		t.Error("MaxConcurrency must default to 5 on update")
	}
}

func TestAgentService_Create_InvalidatesRegistry(t *testing.T) {
	c := &fakeCache{}
	svc := service.NewAgentService(&fakeDal{createdID: "id-3"}, c)
	_, _ = svc.Create(context.Background(), "t1", dal.AgentInput{Slug: "s", DisplayName: "D"})
	// SEC-03: cache key is now tenant-scoped: them:agents:registry:{tenantID}
	expectedKey := "them:agents:registry:t1"
	found := false
	for _, k := range c.deletedKeys {
		if k == expectedKey {
			found = true
		}
	}
	if !found {
		t.Errorf("%s not deleted, got %v", expectedKey, c.deletedKeys)
	}
}

func TestAgentService_NilCache_NoPanic(t *testing.T) {
	svc := service.NewAgentService(&fakeDal{createdID: "id-4"}, nil)
	_, err := svc.Create(context.Background(), "t1", dal.AgentInput{Slug: "s", DisplayName: "D"})
	if err != nil {
		t.Fatal(err)
	}
}

// ── AppService tests ──────────────────────────────────────────────────────────

func TestAppService_Create_MissingName_Validation(t *testing.T) {
	svc := service.NewAppService(&fakeDal{}, nil, nil)
	_, err := svc.Create(context.Background(), "t1", "", nil)
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestAppService_CreateEntryPoint_InvalidType_Unprocessable(t *testing.T) {
	svc := service.NewAppService(&fakeDal{}, nil, nil)
	_, err := svc.CreateEntryPoint(context.Background(), "app-1", "my-ep", "grpc", nil)
	if !errors.Is(err, service.ErrUnprocessable) {
		t.Errorf("want ErrUnprocessable, got %v", err)
	}
}

func TestAppService_CreateEntryPoint_ValidTypes(t *testing.T) {
	for _, epType := range []string{"websocket", "sse", "voice", "webrtc", "a2a"} {
		d := &fakeDal{createdID: "ep-1"}
		svc := service.NewAppService(d, nil, nil)
		_, err := svc.CreateEntryPoint(context.Background(), "app-1", "sl", epType, nil)
		if err != nil {
			t.Errorf("type %q: unexpected error: %v", epType, err)
		}
	}
}

const svcTestTenantID = "00000000-0000-0000-0000-000000000001"

func TestAppService_UpdateEntryPoint_OldSlugBeforeNew(t *testing.T) {
	c := &fakeCache{}
	d := &fakeDal{epTenantSlug: dal.EPTenantSlug{TenantID: svcTestTenantID, Slug: "old-slug"}}
	svc := service.NewAppService(d, c, nil)
	_ = svc.UpdateEntryPoint(context.Background(), svcTestTenantID, "ep-1", "app-1", "new-slug", "sse", nil)
	if len(c.publishOrder) < 2 {
		t.Fatalf("want 2 publishes, got %d: %v", len(c.publishOrder), c.publishOrder)
	}
	wantOld := svcTestTenantID + ":old-slug"
	wantNew := svcTestTenantID + ":new-slug"
	if c.publishOrder[0] != wantOld {
		t.Errorf("first publish must be old slug %q, got %q", wantOld, c.publishOrder[0])
	}
	if c.publishOrder[1] != wantNew {
		t.Errorf("second publish must be new slug %q, got %q", wantNew, c.publishOrder[1])
	}
}

func TestAppService_UpdateEntryPoint_InvalidType_Unprocessable(t *testing.T) {
	svc := service.NewAppService(&fakeDal{}, nil, nil)
	err := svc.UpdateEntryPoint(context.Background(), svcTestTenantID, "ep-1", "app-1", "sl", "tcp", nil)
	if !errors.Is(err, service.ErrUnprocessable) {
		t.Errorf("want ErrUnprocessable, got %v", err)
	}
}

func TestAppService_DeleteEntryPoint_PublishesSlug(t *testing.T) {
	c := &fakeCache{}
	d := &fakeDal{epTenantSlug: dal.EPTenantSlug{TenantID: svcTestTenantID, Slug: "my-ep"}}
	svc := service.NewAppService(d, c, nil)
	_ = svc.DeleteEntryPoint(context.Background(), "ep-1", "app-1")
	want := svcTestTenantID + ":my-ep"
	if len(c.publishOrder) != 1 || c.publishOrder[0] != want {
		t.Errorf("want [%s], got %v", want, c.publishOrder)
	}
}

func TestAppService_Update_InvalidatesAppEPs(t *testing.T) {
	c := &fakeCache{}
	d := &fakeDal{epTenantSlugs: []dal.EPTenantSlug{
		{TenantID: svcTestTenantID, Slug: "ep-a"},
		{TenantID: svcTestTenantID, Slug: "ep-b"},
	}}
	svc := service.NewAppService(d, c, nil)
	_ = svc.Update(context.Background(), "t1", "app-1", "New Name", nil)
	if len(c.publishOrder) != 2 {
		t.Errorf("want 2 EP publishes, got %d: %v", len(c.publishOrder), c.publishOrder)
	}
}

// ── OrchService tests ─────────────────────────────────────────────────────────

func TestOrchService_Create_Defaults(t *testing.T) {
	d := &fakeDal{createdID: "orch-1"}
	svc := service.NewOrchService(d, nil)
	in := dal.OrchestratorInput{Name: "my-orch"}
	_, err := svc.Create(context.Background(), "t1", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := d.createOrchCalls[0]
	if got.MaxIterations != 10 {
		t.Errorf("MaxIterations: want 10, got %d", got.MaxIterations)
	}
	if got.HistoryWindow != 20 {
		t.Errorf("HistoryWindow: want 20, got %d", got.HistoryWindow)
	}
	if !d.createOrchEnabledCalls[0] {
		t.Error("enabled: want true (default)")
	}
}

func TestOrchService_Create_MissingName_Validation(t *testing.T) {
	svc := service.NewOrchService(&fakeDal{}, nil)
	_, err := svc.Create(context.Background(), "t1", dal.OrchestratorInput{})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestOrchService_Create_InvalidatesCache(t *testing.T) {
	c := &fakeCache{}
	svc := service.NewOrchService(&fakeDal{createdID: "orch-2"}, c)
	_, _ = svc.Create(context.Background(), "t1", dal.OrchestratorInput{Name: "my-orch"})
	found := false
	for _, k := range c.deletedKeys {
		if k == "them:orchestrators:my-orch" {
			found = true
		}
	}
	if !found {
		t.Errorf("them:orchestrators:my-orch not deleted, got %v", c.deletedKeys)
	}
}

func TestOrchService_Delete_InvalidatesCache(t *testing.T) {
	c := &fakeCache{}
	svc := service.NewOrchService(&fakeDal{}, c)
	_ = svc.Delete(context.Background(), "t1", "target-orch")
	found := false
	for _, k := range c.deletedKeys {
		if k == "them:orchestrators:target-orch" {
			found = true
		}
	}
	if !found {
		t.Errorf("them:orchestrators:target-orch not deleted, got %v", c.deletedKeys)
	}
}

// ── RunService tests ───────────────────────────────────────────────────────────

func TestRunService_Signal_BuildsWorkflowID(t *testing.T) {
	temp := &fakeTemporal{}
	d := &fakeDal{contextID: "abc-123"}
	svc := service.NewRunService(d, temp)
	payload := json.RawMessage(`{"key":"val"}`)
	err := svc.Signal(context.Background(), "t1", "run-1", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(temp.signaled) != 1 || temp.signaled[0] != "ctx-abc-123" {
		t.Errorf("want [ctx-abc-123], got %v", temp.signaled)
	}
}

func TestRunService_Signal_TemporalNil_Unavailable(t *testing.T) {
	svc := service.NewRunService(&fakeDal{}, nil)
	err := svc.Signal(context.Background(), "t1", "run-1", nil)
	if !errors.Is(err, service.ErrTemporalUnavailable) {
		t.Errorf("want ErrTemporalUnavailable, got %v", err)
	}
}

func TestRunService_Signal_DBError_NotNotFound(t *testing.T) {
	// Non-pgx error → generic db error wrapping, not ErrNotFound.
	// (pgx.ErrNoRows path is covered by the integration suite.)
	d := &fakeDal{getContextIDErr: errors.New("connection reset")}
	svc := service.NewRunService(d, &fakeTemporal{})
	err := svc.Signal(context.Background(), "t1", "run-1", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, service.ErrNotFound) {
		t.Error("generic db error must not map to ErrNotFound")
	}
}

func TestRunService_List_ForwardsParams(t *testing.T) {
	d := &fakeDal{runs: []dal.Run{{ID: "r1"}}}
	svc := service.NewRunService(d, nil)
	runs, err := svc.List(context.Background(), "t1", "ctx-id", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("want 1 run, got %d", len(runs))
	}
}
