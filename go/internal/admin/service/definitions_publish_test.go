package service_test

// Phase C — application definition validate + publish tests.
//
// These tests exercise service.DefinitionService.ValidateDefinition and
// service.DefinitionService.PublishDefinition using in-process fakes —
// no PostgreSQL or Redis required.
//
// Coverage:
//   S1-43  ValidateDefinition — structural + registry errors
//   S1-44  PublishDefinition  — success path + error paths

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/registry"
)

// ── fakeRegistry ─────────────────────────────────────────────────────────────

// fakeRegistry implements service.RegistryResolver.
// It maps (namespace, name) → ComponentDefinition or error.
type fakeRegistry struct {
	defs map[string]*registry.ComponentDefinition // key: namespace+"/"+name
	err  error                                    // if set, returned for every lookup
}

func (r *fakeRegistry) resolve(ref registry.DefinitionRef) (*registry.ComponentDefinition, error) {
	if r.err != nil {
		return nil, r.err
	}
	key := ref.Namespace + "/" + ref.Name
	d, ok := r.defs[key]
	if !ok {
		return nil, registry.ErrNotFound
	}
	return d, nil
}

func (r *fakeRegistry) Resolve(ctx context.Context, tenantID string, ref registry.DefinitionRef, definitionID string) (*registry.ComponentDefinition, error) {
	return r.resolve(ref)
}

func (r *fakeRegistry) ResolveForPublish(ctx context.Context, tenantID string, ref registry.DefinitionRef, definitionID string) (*registry.ComponentDefinition, error) {
	def, err := r.resolve(ref)
	if err != nil {
		return nil, err
	}
	if def.Status == registry.StatusDeprecated {
		return nil, registry.ErrDeprecated
	}
	if !def.Enabled {
		return nil, registry.ErrDisabled
	}
	return def, nil
}

// ── publishFakeDal ────────────────────────────────────────────────────────────

// publishFakeDal is a specialised fakeDal for publish tests that tracks calls
// and supports returning controlled errors.
type publishFakeDal struct {
	// GetDefinition control
	defByID       map[string]dal.AppDefinition
	getDefErr     error

	// PublishDefinition control
	publishResult dal.PublishResult
	publishErr    error
	publishCalled bool

	// Upsert call tracking
	upsertOrchCalls []dal.AppOrchestratorRow
	upsertEPCalls   []dal.EntryPointRow
	upsertOrchErr   error
	upsertEPErr     error

	// Deactivate call tracking
	deactivateOrchCalled bool
	deactivateEPCalled   bool
	deactivateOrchErr    error
	deactivateEPErr      error

	// Shared ID returned by upserts
	upsertedOrchID string
	upsertedEPID   string
}

func newPublishFakeDal() *publishFakeDal {
	return &publishFakeDal{
		defByID:        make(map[string]dal.AppDefinition),
		upsertedOrchID: "orch-uuid-1",
		upsertedEPID:   "ep-uuid-1",
	}
}

// ── Dal interface implementation ──────────────────────────────────────────────

func (f *publishFakeDal) GetNextRevision(_ context.Context, _ string) (int, error)      { return 1, nil }
func (f *publishFakeDal) CreateDefinition(_ context.Context, _, _ string, _ int, _ []byte, _ string) (string, error) {
	return "def-id", nil
}
func (f *publishFakeDal) GetDefinition(_ context.Context, _, _, defID string) (dal.AppDefinition, error) {
	if f.getDefErr != nil {
		return dal.AppDefinition{}, f.getDefErr
	}
	def, ok := f.defByID[defID]
	if !ok {
		return dal.AppDefinition{}, pgx.ErrNoRows
	}
	return def, nil
}
func (f *publishFakeDal) ListDefinitions(_ context.Context, _, _ string) ([]dal.AppDefinition, error) {
	return []dal.AppDefinition{}, nil
}
func (f *publishFakeDal) UpdateDraftDefinition(_ context.Context, _, _, _ string, _ []byte, _ string) error {
	return nil
}
func (f *publishFakeDal) DeleteDraftDefinition(_ context.Context, _, _, _ string) error { return nil }

func (f *publishFakeDal) PublishDefinition(_ context.Context, _, _, _, _ string) (dal.PublishResult, error) {
	f.publishCalled = true
	return f.publishResult, f.publishErr
}
func (f *publishFakeDal) UpsertAppOrchestrator(_ context.Context, row dal.AppOrchestratorRow) (string, error) {
	f.upsertOrchCalls = append(f.upsertOrchCalls, row)
	return f.upsertedOrchID, f.upsertOrchErr
}
func (f *publishFakeDal) UpsertEntryPoint(_ context.Context, row dal.EntryPointRow) (string, error) {
	f.upsertEPCalls = append(f.upsertEPCalls, row)
	return f.upsertedEPID, f.upsertEPErr
}
func (f *publishFakeDal) DeactivateStaleOrchestrators(_ context.Context, _, _, _ string) error {
	f.deactivateOrchCalled = true
	return f.deactivateOrchErr
}
func (f *publishFakeDal) DeactivateStaleEntryPoints(_ context.Context, _, _, _ string) error {
	f.deactivateEPCalled = true
	return f.deactivateEPErr
}

// Remaining Dal interface methods — stubs only (not exercised in publish tests).
func (f *publishFakeDal) ListAgents(_ context.Context, _ string) ([]dal.Agent, error)  { return nil, nil }
func (f *publishFakeDal) GetAgent(_ context.Context, _, _ string) (dal.Agent, error)   { return dal.Agent{}, nil }
func (f *publishFakeDal) CreateAgent(_ context.Context, _ string, _ dal.AgentInput, _ bool) (string, error) { return "", nil }
func (f *publishFakeDal) UpdateAgent(_ context.Context, _, _ string, _ dal.AgentInput, _ bool) error { return nil }
func (f *publishFakeDal) DeleteAgent(_ context.Context, _, _ string) error              { return nil }
func (f *publishFakeDal) GetAgentBySlug(_ context.Context, _ string) (dal.Agent, error) { return dal.Agent{}, nil }
func (f *publishFakeDal) UpdateAgentScanResult(_ context.Context, _ string, _ []byte) error { return nil }
func (f *publishFakeDal) GetAgentByID(_ context.Context, _ string) (dal.Agent, error)  { return dal.Agent{}, nil }
func (f *publishFakeDal) GetAgentTokenEncrypted(_ context.Context, _ string) (string, error) { return "", nil }
func (f *publishFakeDal) ListOrchestrators(_ context.Context, _ string) ([]dal.Orchestrator, error) { return nil, nil }
func (f *publishFakeDal) GetOrchestrator(_ context.Context, _, _ string) (dal.Orchestrator, error) { return dal.Orchestrator{}, nil }
func (f *publishFakeDal) CreateOrchestrator(_ context.Context, _ string, _ dal.OrchestratorInput, _ bool) (string, error) { return "", nil }
func (f *publishFakeDal) UpdateOrchestrator(_ context.Context, _, _ string, _ dal.OrchestratorInput, _ bool) error { return nil }
func (f *publishFakeDal) DeleteOrchestrator(_ context.Context, _, _ string) error      { return nil }
func (f *publishFakeDal) ListApplications(_ context.Context, _ string) ([]dal.Application, error) { return nil, nil }
func (f *publishFakeDal) GetApplication(_ context.Context, _, _ string) (dal.Application, error) { return dal.Application{}, nil }
func (f *publishFakeDal) CreateApplication(_ context.Context, _, _ string, _ bool) (string, error) { return "", nil }
func (f *publishFakeDal) UpdateApplication(_ context.Context, _, _, _ string, _ bool) error { return nil }
func (f *publishFakeDal) DeleteApplication(_ context.Context, _, _ string) error       { return nil }
func (f *publishFakeDal) ListEntryPoints(_ context.Context, _ string) []dal.EntryPoint { return nil }
func (f *publishFakeDal) CreateEntryPoint(_ context.Context, _, _, _ string, _ bool) (string, error) { return "", nil }
func (f *publishFakeDal) GetEntryPointSlug(_ context.Context, _, _ string) (string, error) { return "", nil }
func (f *publishFakeDal) GetEntryPointTenantAndSlug(_ context.Context, _, _ string) dal.EPTenantSlug { return dal.EPTenantSlug{} }
func (f *publishFakeDal) UpdateEntryPoint(_ context.Context, _, _, _, _ string, _ bool) error { return nil }
func (f *publishFakeDal) DeleteEntryPoint(_ context.Context, _, _ string) error        { return nil }
func (f *publishFakeDal) ListEPSlugsForApp(_ context.Context, _ string) []string       { return nil }
func (f *publishFakeDal) ListEPTenantSlugsForApp(_ context.Context, _ string) []dal.EPTenantSlug { return nil }
func (f *publishFakeDal) UpdateRuntimeConfig(_ context.Context, _, _ string, _ []byte) error { return nil }
func (f *publishFakeDal) ListAppOrchestratorNames(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (f *publishFakeDal) BulkDeleteApplications(_ context.Context, _ string, _ []string) (int64, error) { return 0, nil }
func (f *publishFakeDal) GetProviderKeys(_ context.Context, _, _ string) ([]byte, error)        { return []byte(`{}`), nil }
func (f *publishFakeDal) SetProviderKey(_ context.Context, _, _, _ string, _ []byte) error      { return nil }
func (f *publishFakeDal) DeleteProviderKey(_ context.Context, _, _, _ string) error              { return nil }
func (f *publishFakeDal) SetOrchestratorLLM(_ context.Context, _, _, _, _ string) error          { return nil }
func (f *publishFakeDal) SetOrchestratorMCPServers(_ context.Context, _, _ string, _ []dal.MCPServerAttachment) error { return nil }
func (f *publishFakeDal) ListRuns(_ context.Context, _, _ string, _ int) ([]dal.Run, error) { return nil, nil }
func (f *publishFakeDal) GetRun(_ context.Context, _, _ string) (dal.Run, error)       { return dal.Run{}, nil }
func (f *publishFakeDal) GetRunContextID(_ context.Context, _, _ string) (string, error) { return "", nil }
func (f *publishFakeDal) GetRunStats(_ context.Context, _ string) (dal.RunStats, error) { return dal.RunStats{ByStatus: make(map[string]int), TotalCostUSD: "0"}, nil }
func (f *publishFakeDal) GetRunDetail(_ context.Context, _, _ string) (dal.RunDetail, error) { return dal.RunDetail{Steps: []dal.RunStep{}, Usage: []dal.RunUsage{}, Children: []dal.Run{}}, nil }
func (f *publishFakeDal) GetRunTasks(_ context.Context, _, _ string) ([]dal.Task, error)  { return nil, nil }
func (f *publishFakeDal) GetRunArtifacts(_ context.Context, _, _ string) ([]dal.Artifact, error) { return nil, nil }
func (f *publishFakeDal) ListContextSessions(_ context.Context, _, _ string, _ int) ([]dal.ContextSession, error) { return nil, nil }
func (f *publishFakeDal) GetContextArtifacts(_ context.Context, _, _ string, _ int) ([]dal.Artifact, error) { return nil, nil }
func (f *publishFakeDal) GetContextMessages(_ context.Context, _, _ string, _ int) ([]dal.ContextMessage, error) { return nil, nil }
func (f *publishFakeDal) CancelRun(_ context.Context, _, _ string) (dal.Run, error)    { return dal.Run{}, nil }
func (f *publishFakeDal) DeleteRun(_ context.Context, _, _ string) error               { return nil }
func (f *publishFakeDal) BulkDeleteRuns(_ context.Context, _ string, ids []string) (int64, error) { return int64(len(ids)), nil }
func (f *publishFakeDal) ListTokens(_ context.Context, _ string, _ *int64) ([]dal.Token, error) { return nil, nil }
func (f *publishFakeDal) GetToken(_ context.Context, _, _ string) (dal.Token, error)   { return dal.Token{}, nil }
func (f *publishFakeDal) OrchestratorExists(_ context.Context, _, _ string) (bool, error) { return false, nil }
func (f *publishFakeDal) CreateToken(_ context.Context, _ string, _ dal.TokenCreateRow) (dal.Token, error) { return dal.Token{}, nil }
func (f *publishFakeDal) UpdateToken(_ context.Context, _, _ string, _ dal.TokenPatchRow) (string, dal.Token, error) { return "", dal.Token{}, nil }
func (f *publishFakeDal) DeleteToken(_ context.Context, _, _ string) (string, error)   { return "", nil }
func (f *publishFakeDal) GetConfig(_ context.Context, _ string) (*dal.ConfigRow, error) { return nil, nil }
func (f *publishFakeDal) UpsertConfig(_ context.Context, _ string, _ []byte) error     { return nil }
func (f *publishFakeDal) ListProviders(_ context.Context) ([]dal.LLMProvider, error)   { return nil, nil }
func (f *publishFakeDal) GetProvider(_ context.Context, _ int64) (dal.LLMProvider, error) { return dal.LLMProvider{}, nil }
func (f *publishFakeDal) CreateProvider(_ context.Context, _ dal.LLMProviderInput) (dal.LLMProvider, error) { return dal.LLMProvider{}, nil }
func (f *publishFakeDal) UpdateProvider(_ context.Context, _ int64, _ dal.LLMProviderInput) (dal.LLMProvider, error) { return dal.LLMProvider{}, nil }
func (f *publishFakeDal) DeleteProvider(_ context.Context, _ int64) error              { return nil }
func (f *publishFakeDal) ListComponentDefinitions(_ context.Context, _ string) ([]dal.ComponentDefinitionSummary, error) {
	return []dal.ComponentDefinitionSummary{}, nil
}

// Agent definition stubs.
func (f *publishFakeDal) GetNextAgentRevision(_ context.Context, _, _ string) (int, error) { return 1, nil }
func (f *publishFakeDal) CreateAgentDefinition(_ context.Context, _, _ string, _ int, _ []byte, _ string, _ int) (string, error) {
	return "", nil
}
func (f *publishFakeDal) GetAgentDefinition(_ context.Context, _, _ string) (dal.AgentDefinition, error) {
	return dal.AgentDefinition{}, nil
}
func (f *publishFakeDal) ListAgentDefinitions(_ context.Context, _ string) ([]dal.AgentDefinition, error) {
	return []dal.AgentDefinition{}, nil
}
func (f *publishFakeDal) UpdateDraftAgentDefinition(_ context.Context, _, _ string, _ []byte, _ string) error {
	return nil
}
func (f *publishFakeDal) DeleteDraftAgentDefinition(_ context.Context, _, _ string) error { return nil }

// Phase 3 stubs.
func (f *publishFakeDal) GetAgentDefinitionForPublish(_ context.Context, _ string, _ string) (dal.AgentDefinition, error) {
	return dal.AgentDefinition{}, nil
}
func (f *publishFakeDal) PublishCanvasAgent(_ context.Context, _ dal.CanvasAgentRow) error {
	return nil
}
func (f *publishFakeDal) MarkAgentDefinitionPublished(_ context.Context, _, _ string) error {
	return nil
}
func (f *publishFakeDal) UpsertAgentBinding(_ context.Context, _ dal.AgentBindingRow) error {
	return nil
}
func (f *publishFakeDal) GetAgentBindingStatus(_ context.Context, _, _ string) (dal.AgentBindingSlotStatus, error) {
	return dal.AgentBindingSlotStatus{CredentialSet: map[string]bool{}}, nil
}
func (f *publishFakeDal) ListAgentBindings(_ context.Context, _ string) ([]dal.AgentBindingSlotStatus, error) {
	return []dal.AgentBindingSlotStatus{}, nil
}
func (f *publishFakeDal) DeleteAgentBinding(_ context.Context, _, _ string) error { return nil }
func (f *publishFakeDal) GetAgentParamsForBinding(_ context.Context, _, _ string) (dal.AgentParamsRow, error) {
	return dal.AgentParamsRow{RequiredParams: []agentgen.AgentParamSpec{}}, nil
}
func (f *publishFakeDal) GetRequiredParamsForAgent(_ context.Context, _ string) (dal.AgentParamsRow, error) {
	return dal.AgentParamsRow{}, nil
}
func (f *publishFakeDal) UpsertAgentParams(_ context.Context, _, _ string, _ []byte) error {
	return nil
}

// MCP server stubs for publishFakeDal.
func (f *publishFakeDal) ListMCPServers(_ context.Context, _ string) ([]dal.MCPServer, error) {
	return []dal.MCPServer{}, nil
}
func (f *publishFakeDal) GetMCPServer(_ context.Context, _, _ string) (dal.MCPServer, error) {
	return dal.MCPServer{}, nil
}
func (f *publishFakeDal) CreateMCPServer(_ context.Context, _ dal.MCPServerInput) (dal.MCPServer, error) {
	return dal.MCPServer{}, nil
}
func (f *publishFakeDal) UpdateMCPServer(_ context.Context, _, _ string, _ dal.MCPServerInput) (dal.MCPServer, error) {
	return dal.MCPServer{}, nil
}
func (f *publishFakeDal) DeleteMCPServer(_ context.Context, _, _ string) error { return nil }
func (f *publishFakeDal) GetAppMCPCredential(_ context.Context, _, _ string) (dal.AppMCPCredential, error) {
	return dal.AppMCPCredential{}, nil
}
func (f *publishFakeDal) ListAppMCPCredentials(_ context.Context, _ string) ([]dal.AppMCPCredentialMeta, error) {
	return []dal.AppMCPCredentialMeta{}, nil
}
func (f *publishFakeDal) UpsertAppMCPCredential(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (f *publishFakeDal) DeleteAppMCPCredential(_ context.Context, _, _ string) error { return nil }

// ── helpers ───────────────────────────────────────────────────────────────────

func orchRef() registry.DefinitionRef {
	return registry.DefinitionRef{Kind: registry.KindOrchestrator, Namespace: "builtin", Name: "standard-orchestrator", Version: 1}
}

func agentRef() registry.DefinitionRef {
	return registry.DefinitionRef{Kind: registry.KindAgent, Namespace: "builtin", Name: "echo-agent", Version: 1}
}

func makeOrchDef(id string) *registry.ComponentDefinition {
	return &registry.ComponentDefinition{
		ID: id, Kind: registry.KindOrchestrator, Namespace: "builtin",
		Name: "standard-orchestrator", Version: 1,
		Status: registry.StatusPublished, Enabled: true,
	}
}

func makeAgentDef(id string) *registry.ComponentDefinition {
	return &registry.ComponentDefinition{
		ID: id, Kind: registry.KindAgent, Namespace: "builtin",
		Name: "echo-agent", Version: 1,
		Status: registry.StatusPublished, Enabled: true,
	}
}

// minimalDraftDef builds a minimal valid definition JSON for a single orchestrator + entry point.
func minimalDraftDef() json.RawMessage {
	return json.RawMessage(`{
		"components": [
			{
				"instance_id": "orch_main",
				"name": "Main Orchestrator",
				"definition_ref": {"kind": "orchestrator", "namespace": "builtin", "name": "standard-orchestrator", "version": 1},
				"config": {"llm_provider": "anthropic", "llm_model": "claude-3-5-sonnet", "max_iterations": 10, "history_window": 20}
			}
		],
		"entry_points": [
			{
				"instance_id": "ep_main",
				"slug": "main",
				"protocol": "websocket",
				"root": "orch_main"
			}
		],
		"connections": []
	}`)
}

func addDraft(d *publishFakeDal, defID string, raw json.RawMessage) {
	d.defByID[defID] = dal.AppDefinition{
		ID:             defID,
		ApplicationID:  "app-1",
		TenantID:       "tenant-1",
		Revision:       1,
		Status:         "draft",
		Definition:     raw,
		DefinitionHash: "sha256:abc",
	}
}

// ── S1-43: ValidateDefinition tests ──────────────────────────────────────────

func TestValidateDefinition_ValidDef_ReturnsValidTrue(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-1", minimalDraftDef())

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-orch-1"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	report, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "def-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Valid {
		t.Errorf("want valid=true, got false; errors: %+v", report.Errors)
	}
	if len(report.Errors) != 0 {
		t.Errorf("want no errors, got %+v", report.Errors)
	}
}

func TestValidateDefinition_UnknownComponent_ReturnsNotFound(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-2", minimalDraftDef()) // references builtin/standard-orchestrator

	reg := &fakeRegistry{defs: map[string]*registry.ComponentDefinition{}} // empty → not found
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	report, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "def-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Error("want valid=false for unknown component")
	}
	found := false
	for _, ve := range report.Errors {
		if ve.Code == "component_not_found" {
			found = true
		}
	}
	if !found {
		t.Errorf("want code=component_not_found in errors: %+v", report.Errors)
	}
}

func TestValidateDefinition_DisabledComponent_ReturnsDisabled(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-3", minimalDraftDef())

	disabledDef := makeOrchDef("comp-disabled")
	disabledDef.Enabled = false
	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": disabledDef,
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	report, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "def-3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Error("want valid=false for disabled component")
	}
	found := false
	for _, ve := range report.Errors {
		if ve.Code == "component_disabled" {
			found = true
		}
	}
	if !found {
		t.Errorf("want code=component_disabled in errors: %+v", report.Errors)
	}
}

func TestValidateDefinition_DeprecatedComponent_ReturnsDeprecated(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-4", minimalDraftDef())

	deprecatedDef := makeOrchDef("comp-depr")
	deprecatedDef.Status = registry.StatusDeprecated
	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": deprecatedDef,
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	report, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "def-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Error("want valid=false for deprecated component")
	}
	found := false
	for _, ve := range report.Errors {
		if ve.Code == "component_deprecated" {
			found = true
		}
	}
	if !found {
		t.Errorf("want code=component_deprecated in errors: %+v", report.Errors)
	}
}

func TestValidateDefinition_DuplicateInstanceID_ReturnsError(t *testing.T) {
	// Duplicate instance_ids are caught by Phase B structural validation
	// (validateDefinition) and reported as code=structural_error in the report.
	d := newPublishFakeDal()
	raw := json.RawMessage(`{
		"components": [
			{"instance_id":"dup","name":"A","definition_ref":{"kind":"orchestrator","namespace":"builtin","name":"standard-orchestrator","version":1},"config":{}},
			{"instance_id":"dup","name":"B","definition_ref":{"kind":"orchestrator","namespace":"builtin","name":"standard-orchestrator","version":1},"config":{}}
		],
		"entry_points": [],
		"connections": []
	}`)
	addDraft(d, "def-5", raw)

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-1"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	report, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "def-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Error("want valid=false for duplicate instance_id")
	}
	// Phase B catches duplicates and returns structural_error or duplicate_instance_id.
	if len(report.Errors) == 0 {
		t.Error("want at least one error for duplicate instance_id")
	}
}

func TestValidateDefinition_DanglingConnection_ReturnsError(t *testing.T) {
	d := newPublishFakeDal()
	raw := json.RawMessage(`{
		"components": [
			{"instance_id":"orch_a","name":"A","definition_ref":{"kind":"orchestrator","namespace":"builtin","name":"standard-orchestrator","version":1},"config":{}}
		],
		"entry_points": [],
		"connections": [
			{"source":"orch_a","target":"nonexistent","type":"tool"}
		]
	}`)
	addDraft(d, "def-6", raw)

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-1"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	report, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "def-6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Error("want valid=false for dangling connection")
	}
	found := false
	for _, ve := range report.Errors {
		if ve.Code == "dangling_connection" {
			found = true
		}
	}
	if !found {
		t.Errorf("want code=dangling_connection in errors: %+v", report.Errors)
	}
}

func TestValidateDefinition_InvalidProtocol_ReturnsError(t *testing.T) {
	d := newPublishFakeDal()
	raw := json.RawMessage(`{
		"components": [],
		"entry_points": [
			{"instance_id":"ep_grpc","slug":"grpc-ep","protocol":"grpc","root":""}
		],
		"connections": []
	}`)
	addDraft(d, "def-7", raw)

	reg := &fakeRegistry{defs: map[string]*registry.ComponentDefinition{}}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	report, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "def-7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Error("want valid=false for invalid protocol")
	}
	found := false
	for _, ve := range report.Errors {
		if ve.Code == "invalid_protocol" {
			found = true
		}
	}
	if !found {
		t.Errorf("want code=invalid_protocol in errors: %+v", report.Errors)
	}
}

func TestValidateDefinition_EmbeddedSecret_ReturnsError(t *testing.T) {
	d := newPublishFakeDal()
	raw := json.RawMessage(`{
		"components": [],
		"entry_points": [],
		"connections": [],
		"secret_value": "oops"
	}`)
	addDraft(d, "def-8", raw)

	svc := service.NewDefinitionServiceWithRegistry(d, &fakeRegistry{defs: map[string]*registry.ComponentDefinition{}})

	report, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "def-8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Error("want valid=false for embedded secret")
	}
}

func TestValidateDefinition_DefinitionNotFound_ReturnsErrNotFound(t *testing.T) {
	d := newPublishFakeDal() // no definitions added
	svc := service.NewDefinitionServiceWithRegistry(d, &fakeRegistry{})

	_, err := svc.ValidateDefinition(context.Background(), "tenant-1", "app-1", "missing-def")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestValidateDefinition_WrongTenant_ReturnsErrNotFound(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-9", minimalDraftDef())

	svc := service.NewDefinitionServiceWithRegistry(d, &fakeRegistry{})

	// The fake returns pgx.ErrNoRows for wrong defIDs; simulate wrong tenant
	// by checking that the correct defID is still "found" for the right tenant
	// (tenant isolation is enforced at the DAL level — here we verify the service
	// delegates to GetDefinition which will miss on wrong tenant in real DB).
	// In unit tests, we verify the not-found path via getDefErr.
	d.getDefErr = pgx.ErrNoRows // simulates dal returning pgx.ErrNoRows
	_, err := svc.ValidateDefinition(context.Background(), "other-tenant", "app-1", "def-9")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound for wrong tenant, got %v", err)
	}
}

// ── S1-44: PublishDefinition tests ───────────────────────────────────────────

func TestPublishDefinition_Success_ReturnsPublishResult(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-pub-1", minimalDraftDef())
	d.publishResult = dal.PublishResult{
		DefinitionID:   "def-pub-1",
		Revision:       1,
		DefinitionHash: "sha256:abc",
	}

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-orch-1"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	result, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-pub-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.DefinitionID != "def-pub-1" {
		t.Errorf("DefinitionID: want def-pub-1, got %q", result.DefinitionID)
	}
	if result.Revision != 1 {
		t.Errorf("Revision: want 1, got %d", result.Revision)
	}
	if !d.publishCalled {
		t.Error("dal.PublishDefinition must be called")
	}
}

func TestPublishDefinition_PublishedDefinition_ReturnsConflict(t *testing.T) {
	d := newPublishFakeDal()
	d.defByID["def-already-pub"] = dal.AppDefinition{
		ID:            "def-already-pub",
		ApplicationID: "app-1",
		TenantID:      "tenant-1",
		Revision:      1,
		Status:        "published", // already published
		Definition:    minimalDraftDef(),
		DefinitionHash: "sha256:abc",
	}

	svc := service.NewDefinitionServiceWithRegistry(d, &fakeRegistry{})

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-already-pub")
	if !errors.Is(err, service.ErrConflict) {
		t.Errorf("want ErrConflict for already-published definition, got %v", err)
	}
	if d.publishCalled {
		t.Error("dal.PublishDefinition must NOT be called for already-published definition")
	}
}

func TestPublishDefinition_ValidationFails_ReturnsValidationError(t *testing.T) {
	d := newPublishFakeDal()
	// Definition references unknown component.
	addDraft(d, "def-invalid", minimalDraftDef())

	reg := &fakeRegistry{defs: map[string]*registry.ComponentDefinition{}} // empty → not found

	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-invalid")
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation when validation fails, got %v", err)
	}
	if d.publishCalled {
		t.Error("dal.PublishDefinition must NOT be called when validation fails")
	}
}

func TestPublishDefinition_OrchProjectionCreated(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-orch", minimalDraftDef())
	d.publishResult = dal.PublishResult{DefinitionID: "def-orch", Revision: 1, DefinitionHash: "sha256:abc"}

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-orch-uuid"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-orch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(d.upsertOrchCalls) != 1 {
		t.Fatalf("want 1 UpsertAppOrchestrator call, got %d", len(d.upsertOrchCalls))
	}
	orch := d.upsertOrchCalls[0]
	if orch.Name != "orch_main" {
		t.Errorf("Name: want orch_main, got %q", orch.Name)
	}
	if orch.InstanceID != "orch_main" {
		t.Errorf("InstanceID: want orch_main, got %q", orch.InstanceID)
	}
	if orch.SourceDefinitionID != "def-orch" {
		t.Errorf("SourceDefinitionID: want def-orch, got %q", orch.SourceDefinitionID)
	}
	if orch.ComponentDefinitionID == nil || *orch.ComponentDefinitionID != "comp-orch-uuid" {
		t.Errorf("ComponentDefinitionID: want comp-orch-uuid, got %v", orch.ComponentDefinitionID)
	}
	if orch.MaxIterations != 10 {
		t.Errorf("MaxIterations: want 10, got %d", orch.MaxIterations)
	}
	if orch.LLMProvider == nil || *orch.LLMProvider != "anthropic" {
		t.Errorf("LLMProvider: want anthropic, got %v", orch.LLMProvider)
	}
}

func TestPublishDefinition_EPProjectionCreated(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-ep", minimalDraftDef())
	d.publishResult = dal.PublishResult{DefinitionID: "def-ep", Revision: 1, DefinitionHash: "sha256:abc"}
	d.upsertedOrchID = "orch-uuid-from-upsert"

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-orch-uuid"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-ep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(d.upsertEPCalls) != 1 {
		t.Fatalf("want 1 UpsertEntryPoint call, got %d", len(d.upsertEPCalls))
	}
	ep := d.upsertEPCalls[0]
	if ep.Slug != "main" {
		t.Errorf("Slug: want main, got %q", ep.Slug)
	}
	if ep.EntryPointType != "websocket" {
		t.Errorf("EntryPointType: want websocket, got %q", ep.EntryPointType)
	}
	if ep.AppOrchestratorID == nil || *ep.AppOrchestratorID != "orch-uuid-from-upsert" {
		t.Errorf("AppOrchestratorID: want orch-uuid-from-upsert, got %v", ep.AppOrchestratorID)
	}
	if ep.SourceDefinitionID != "def-ep" {
		t.Errorf("SourceDefinitionID: want def-ep, got %q", ep.SourceDefinitionID)
	}
}

func TestPublishDefinition_StaleProjectionsDeactivated(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-stale", minimalDraftDef())
	d.publishResult = dal.PublishResult{DefinitionID: "def-stale", Revision: 1, DefinitionHash: "sha256:abc"}

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-1"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-stale")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !d.deactivateOrchCalled {
		t.Error("DeactivateStaleOrchestrators must be called")
	}
	if !d.deactivateEPCalled {
		t.Error("DeactivateStaleEntryPoints must be called")
	}
}

func TestPublishDefinition_ToolConnectionWiresAgentIDs(t *testing.T) {
	d := newPublishFakeDal()
	raw := json.RawMessage(`{
		"components": [
			{
				"instance_id": "orch_main",
				"name": "Main Orchestrator",
				"definition_ref": {"kind": "orchestrator", "namespace": "builtin", "name": "standard-orchestrator", "version": 1},
				"config": {}
			},
			{
				"instance_id": "agent_echo",
				"name": "Echo Agent",
				"definition_ref": {"kind": "agent", "namespace": "builtin", "name": "echo-agent", "version": 1},
				"config": {}
			}
		],
		"entry_points": [],
		"connections": [
			{"source": "orch_main", "target": "agent_echo", "type": "tool"}
		]
	}`)
	addDraft(d, "def-tool", raw)
	d.publishResult = dal.PublishResult{DefinitionID: "def-tool", Revision: 1, DefinitionHash: "sha256:abc"}

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-orch-uuid"),
			"builtin/echo-agent":            makeAgentDef("comp-agent-uuid"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(d.upsertOrchCalls) != 1 {
		t.Fatalf("want 1 orch upsert, got %d", len(d.upsertOrchCalls))
	}
	orch := d.upsertOrchCalls[0]
	if len(orch.AllowedAgentIDs) != 1 || orch.AllowedAgentIDs[0] != "comp-agent-uuid" {
		t.Errorf("AllowedAgentIDs: want [comp-agent-uuid], got %v", orch.AllowedAgentIDs)
	}
}

func TestPublishDefinition_DelegationConnectionSetsDelegatable(t *testing.T) {
	d := newPublishFakeDal()
	raw := json.RawMessage(`{
		"components": [
			{
				"instance_id": "orch_root",
				"name": "Root",
				"definition_ref": {"kind": "orchestrator", "namespace": "builtin", "name": "standard-orchestrator", "version": 1},
				"config": {}
			},
			{
				"instance_id": "orch_sub",
				"name": "Sub",
				"definition_ref": {"kind": "orchestrator", "namespace": "builtin", "name": "standard-orchestrator", "version": 1},
				"config": {}
			}
		],
		"entry_points": [],
		"connections": [
			{"source": "orch_root", "target": "orch_sub", "type": "delegation"}
		]
	}`)
	addDraft(d, "def-deleg", raw)
	d.publishResult = dal.PublishResult{DefinitionID: "def-deleg", Revision: 1, DefinitionHash: "sha256:abc"}

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-orch-1"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-deleg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(d.upsertOrchCalls) != 2 {
		t.Fatalf("want 2 orch upserts, got %d", len(d.upsertOrchCalls))
	}

	// orch_sub should be delegatable=true (it's the delegation target).
	var subRow *dal.AppOrchestratorRow
	for i, r := range d.upsertOrchCalls {
		if r.Name == "orch_sub" {
			subRow = &d.upsertOrchCalls[i]
		}
	}
	if subRow == nil {
		t.Fatal("orch_sub row not found in upsert calls")
	}
	if !subRow.Delegatable {
		t.Error("orch_sub: want delegatable=true (is a delegation target)")
	}
}

func TestPublishDefinition_UpsertOrchError_AbortsPipeline(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-uerr", minimalDraftDef())
	d.upsertOrchErr = errors.New("db error")

	reg := &fakeRegistry{
		defs: map[string]*registry.ComponentDefinition{
			"builtin/standard-orchestrator": makeOrchDef("comp-1"),
		},
	}
	svc := service.NewDefinitionServiceWithRegistry(d, reg)

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-uerr")
	if err == nil {
		t.Fatal("expected error from upsert failure")
	}
	if d.publishCalled {
		t.Error("dal.PublishDefinition must NOT be called after upsert error")
	}
}

func TestPublishDefinition_DefinitionNotFound_ReturnsErrNotFound(t *testing.T) {
	d := newPublishFakeDal() // no definitions added
	svc := service.NewDefinitionServiceWithRegistry(d, &fakeRegistry{})

	_, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "missing-def")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestPublishDefinition_WrongTenantBlocked(t *testing.T) {
	d := newPublishFakeDal()
	addDraft(d, "def-wt", minimalDraftDef())
	d.getDefErr = pgx.ErrNoRows // simulate DAL returning no-rows for wrong tenant

	svc := service.NewDefinitionServiceWithRegistry(d, &fakeRegistry{})

	_, err := svc.PublishDefinition(context.Background(), "other-tenant", "app-1", "def-wt")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound for wrong tenant, got %v", err)
	}
}

func TestPublishDefinition_NoRegistry_SkipsResolution(t *testing.T) {
	// When no registry is wired, components are not resolved but publish still works.
	d := newPublishFakeDal()
	addDraft(d, "def-noreg", minimalDraftDef())
	d.publishResult = dal.PublishResult{DefinitionID: "def-noreg", Revision: 1, DefinitionHash: "sha256:abc"}

	// Use NewDefinitionService (no registry).
	svc := service.NewDefinitionService(d)

	result, err := svc.PublishDefinition(context.Background(), "tenant-1", "app-1", "def-noreg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.DefinitionID != "def-noreg" {
		t.Errorf("unexpected result: %+v", result)
	}
	if !d.publishCalled {
		t.Error("dal.PublishDefinition must be called")
	}
}
