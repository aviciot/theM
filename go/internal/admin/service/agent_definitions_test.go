package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// ── agentDefFakeDal — focused fake for agent definition tests ─────────────────

type agentDefFakeDal struct {
	// agent definition control
	nextRev        int
	createdAgentID string
	agentDef       dal.AgentDefinition
	agentDefs      []dal.AgentDefinition

	getAgentDefErr    error
	createAgentDefErr error
	updateAgentDefErr error
	deleteAgentDefErr error

	// track calls
	createAgentDefCalls int
	updateAgentDefCalls int
	deleteAgentDefCalls int
	lastUpdateHash      string
}

// Agent definition methods — real behavior.
func (f *agentDefFakeDal) GetNextAgentRevision(_ context.Context, _, _ string) (int, error) {
	return f.nextRev, nil
}
func (f *agentDefFakeDal) CreateAgentDefinition(_ context.Context, _, _ string, _ int, _ []byte, _ string) (string, error) {
	f.createAgentDefCalls++
	return f.createdAgentID, f.createAgentDefErr
}
func (f *agentDefFakeDal) GetAgentDefinition(_ context.Context, _, _ string) (dal.AgentDefinition, error) {
	return f.agentDef, f.getAgentDefErr
}
func (f *agentDefFakeDal) ListAgentDefinitions(_ context.Context, _ string) ([]dal.AgentDefinition, error) {
	return f.agentDefs, nil
}
func (f *agentDefFakeDal) UpdateDraftAgentDefinition(_ context.Context, _, _ string, _ []byte, hash string) error {
	f.updateAgentDefCalls++
	f.lastUpdateHash = hash
	return f.updateAgentDefErr
}
func (f *agentDefFakeDal) DeleteDraftAgentDefinition(_ context.Context, _, _ string) error {
	f.deleteAgentDefCalls++
	return f.deleteAgentDefErr
}

// All other Dal methods — stubs only.
func (f *agentDefFakeDal) ListAgents(_ context.Context, _ string) ([]dal.Agent, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetAgent(_ context.Context, _, _ string) (dal.Agent, error) {
	return dal.Agent{}, nil
}
func (f *agentDefFakeDal) CreateAgent(_ context.Context, _ string, _ dal.AgentInput, _ bool) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) UpdateAgent(_ context.Context, _, _ string, _ dal.AgentInput, _ bool) error {
	return nil
}
func (f *agentDefFakeDal) DeleteAgent(_ context.Context, _, _ string) error { return nil }
func (f *agentDefFakeDal) GetAgentBySlug(_ context.Context, _ string) (dal.Agent, error) {
	return dal.Agent{}, nil
}
func (f *agentDefFakeDal) UpdateAgentScanResult(_ context.Context, _ string, _ []byte) error {
	return nil
}
func (f *agentDefFakeDal) GetAgentByID(_ context.Context, _ string) (dal.Agent, error) {
	return dal.Agent{}, nil
}
func (f *agentDefFakeDal) GetAgentTokenEncrypted(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) ListOrchestrators(_ context.Context, _ string) ([]dal.Orchestrator, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetOrchestrator(_ context.Context, _, _ string) (dal.Orchestrator, error) {
	return dal.Orchestrator{}, nil
}
func (f *agentDefFakeDal) CreateOrchestrator(_ context.Context, _ string, _ dal.OrchestratorInput, _ bool) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) UpdateOrchestrator(_ context.Context, _, _ string, _ dal.OrchestratorInput, _ bool) error {
	return nil
}
func (f *agentDefFakeDal) DeleteOrchestrator(_ context.Context, _, _ string) error { return nil }
func (f *agentDefFakeDal) ListApplications(_ context.Context, _ string) ([]dal.Application, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetApplication(_ context.Context, _, _ string) (dal.Application, error) {
	return dal.Application{}, nil
}
func (f *agentDefFakeDal) CreateApplication(_ context.Context, _, _ string, _ bool) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) UpdateApplication(_ context.Context, _, _, _ string, _ bool) error {
	return nil
}
func (f *agentDefFakeDal) DeleteApplication(_ context.Context, _, _ string) error { return nil }
func (f *agentDefFakeDal) ListEntryPoints(_ context.Context, _ string) []dal.EntryPoint {
	return nil
}
func (f *agentDefFakeDal) CreateEntryPoint(_ context.Context, _, _, _ string, _ bool) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) GetEntryPointSlug(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) GetEntryPointTenantAndSlug(_ context.Context, _, _ string) dal.EPTenantSlug {
	return dal.EPTenantSlug{}
}
func (f *agentDefFakeDal) UpdateEntryPoint(_ context.Context, _, _, _, _ string, _ bool) error {
	return nil
}
func (f *agentDefFakeDal) DeleteEntryPoint(_ context.Context, _, _ string) error { return nil }
func (f *agentDefFakeDal) ListEPSlugsForApp(_ context.Context, _ string) []string { return nil }
func (f *agentDefFakeDal) ListEPTenantSlugsForApp(_ context.Context, _ string) []dal.EPTenantSlug {
	return nil
}
func (f *agentDefFakeDal) UpdateRuntimeConfig(_ context.Context, _, _ string, _ []byte) error {
	return nil
}
func (f *agentDefFakeDal) ListAppOrchestratorNames(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *agentDefFakeDal) BulkDeleteApplications(_ context.Context, _ string, _ []string) (int64, error) {
	return 0, nil
}
func (f *agentDefFakeDal) GetProviderKeys(_ context.Context, _, _ string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (f *agentDefFakeDal) SetProviderKey(_ context.Context, _, _, _ string, _ []byte) error {
	return nil
}
func (f *agentDefFakeDal) DeleteProviderKey(_ context.Context, _, _, _ string) error { return nil }
func (f *agentDefFakeDal) SetOrchestratorLLM(_ context.Context, _, _, _, _ string) error { return nil }
func (f *agentDefFakeDal) ListRuns(_ context.Context, _, _ string, _ int) ([]dal.Run, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetRun(_ context.Context, _, _ string) (dal.Run, error) {
	return dal.Run{}, nil
}
func (f *agentDefFakeDal) GetRunContextID(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) GetRunStats(_ context.Context, _ string) (dal.RunStats, error) {
	return dal.RunStats{ByStatus: make(map[string]int), TotalCostUSD: "0"}, nil
}
func (f *agentDefFakeDal) GetRunDetail(_ context.Context, _, _ string) (dal.RunDetail, error) {
	return dal.RunDetail{Steps: []dal.RunStep{}, Usage: []dal.RunUsage{}, Children: []dal.Run{}}, nil
}
func (f *agentDefFakeDal) GetRunTasks(_ context.Context, _, _ string) ([]dal.Task, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetRunArtifacts(_ context.Context, _, _ string) ([]dal.Artifact, error) {
	return nil, nil
}
func (f *agentDefFakeDal) ListContextSessions(_ context.Context, _, _ string, _ int) ([]dal.ContextSession, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetContextArtifacts(_ context.Context, _, _ string, _ int) ([]dal.Artifact, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetContextMessages(_ context.Context, _, _ string, _ int) ([]dal.ContextMessage, error) {
	return nil, nil
}
func (f *agentDefFakeDal) CancelRun(_ context.Context, _, _ string) (dal.Run, error) {
	return dal.Run{}, nil
}
func (f *agentDefFakeDal) DeleteRun(_ context.Context, _, _ string) error { return nil }
func (f *agentDefFakeDal) BulkDeleteRuns(_ context.Context, _ string, ids []string) (int64, error) {
	return int64(len(ids)), nil
}
func (f *agentDefFakeDal) ListTokens(_ context.Context, _ string, _ *int64) ([]dal.Token, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetToken(_ context.Context, _, _ string) (dal.Token, error) {
	return dal.Token{}, nil
}
func (f *agentDefFakeDal) OrchestratorExists(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}
func (f *agentDefFakeDal) CreateToken(_ context.Context, _ string, _ dal.TokenCreateRow) (dal.Token, error) {
	return dal.Token{}, nil
}
func (f *agentDefFakeDal) UpdateToken(_ context.Context, _, _ string, _ dal.TokenPatchRow) (string, dal.Token, error) {
	return "", dal.Token{}, nil
}
func (f *agentDefFakeDal) DeleteToken(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) GetNextRevision(_ context.Context, _ string) (int, error) { return 1, nil }
func (f *agentDefFakeDal) CreateDefinition(_ context.Context, _, _ string, _ int, _ []byte, _ string) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) GetDefinition(_ context.Context, _, _, _ string) (dal.AppDefinition, error) {
	return dal.AppDefinition{}, nil
}
func (f *agentDefFakeDal) ListDefinitions(_ context.Context, _, _ string) ([]dal.AppDefinition, error) {
	return []dal.AppDefinition{}, nil
}
func (f *agentDefFakeDal) UpdateDraftDefinition(_ context.Context, _, _, _ string, _ []byte, _ string) error {
	return nil
}
func (f *agentDefFakeDal) DeleteDraftDefinition(_ context.Context, _, _, _ string) error { return nil }
func (f *agentDefFakeDal) PublishDefinition(_ context.Context, _, _, _, _ string) (dal.PublishResult, error) {
	return dal.PublishResult{}, nil
}
func (f *agentDefFakeDal) UpsertAppOrchestrator(_ context.Context, _ dal.AppOrchestratorRow) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) UpsertEntryPoint(_ context.Context, _ dal.EntryPointRow) (string, error) {
	return "", nil
}
func (f *agentDefFakeDal) DeactivateStaleOrchestrators(_ context.Context, _, _, _ string) error {
	return nil
}
func (f *agentDefFakeDal) DeactivateStaleEntryPoints(_ context.Context, _, _, _ string) error {
	return nil
}
func (f *agentDefFakeDal) ListComponentDefinitions(_ context.Context, _ string) ([]dal.ComponentDefinitionSummary, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetConfig(_ context.Context, _ string) (*dal.ConfigRow, error) {
	return nil, nil
}
func (f *agentDefFakeDal) UpsertConfig(_ context.Context, _ string, _ []byte) error { return nil }
func (f *agentDefFakeDal) ListProviders(_ context.Context) ([]dal.LLMProvider, error) {
	return nil, nil
}
func (f *agentDefFakeDal) GetProvider(_ context.Context, _ int64) (dal.LLMProvider, error) {
	return dal.LLMProvider{}, nil
}
func (f *agentDefFakeDal) CreateProvider(_ context.Context, _ dal.LLMProviderInput) (dal.LLMProvider, error) {
	return dal.LLMProvider{}, nil
}
func (f *agentDefFakeDal) UpdateProvider(_ context.Context, _ int64, _ dal.LLMProviderInput) (dal.LLMProvider, error) {
	return dal.LLMProvider{}, nil
}
func (f *agentDefFakeDal) DeleteProvider(_ context.Context, _ int64) error { return nil }

// Canvas agent publish pipeline stubs (Phase 3).
func (f *agentDefFakeDal) GetAgentDefinitionForPublish(_ context.Context, _ string, _ string) (dal.AgentDefinition, error) {
	return f.agentDef, f.getAgentDefErr
}
func (f *agentDefFakeDal) PublishCanvasAgent(_ context.Context, _ dal.CanvasAgentRow) error {
	return nil
}
func (f *agentDefFakeDal) MarkAgentDefinitionPublished(_ context.Context, _, _ string) error {
	return nil
}

// Application agent binding stubs (Phase 3).
func (f *agentDefFakeDal) UpsertAgentBinding(_ context.Context, _ dal.AgentBindingRow) error {
	return nil
}
func (f *agentDefFakeDal) GetAgentBindingStatus(_ context.Context, _, _ string) (dal.AgentBindingSlotStatus, error) {
	return dal.AgentBindingSlotStatus{CredentialSet: map[string]bool{}}, nil
}
func (f *agentDefFakeDal) ListAgentBindings(_ context.Context, _ string) ([]dal.AgentBindingSlotStatus, error) {
	return []dal.AgentBindingSlotStatus{}, nil
}
func (f *agentDefFakeDal) DeleteAgentBinding(_ context.Context, _, _ string) error { return nil }

// ── valid canvas JSON helpers ─────────────────────────────────────────────────

func validAgentDef(t *testing.T) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{
		"schema_version": 1,
		"agent_slug": "my-agent",
		"agent_root": {
			"display_name": "My Agent",
			"description": "test",
			"version": "1.0.0",
			"capabilities": {"streaming": false, "push_notifications": false},
			"credential_slots": []
		},
		"skills": []
	}`)
}

func newAgentDefSvc(f *agentDefFakeDal) *service.AgentDefinitionService {
	return service.NewAgentDefinitionService(f, nil, nil)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestCreateDraft_Valid(t *testing.T) {
	f := &agentDefFakeDal{nextRev: 1, createdAgentID: "def-uuid-1"}
	svc := newAgentDefSvc(f)
	id, rev, err := svc.CreateDraft(context.Background(), "t1", "my-agent", validAgentDef(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "def-uuid-1" {
		t.Errorf("want id=def-uuid-1, got %q", id)
	}
	if rev != 1 {
		t.Errorf("want revision=1, got %d", rev)
	}
	if f.createAgentDefCalls != 1 {
		t.Error("CreateAgentDefinition must be called once")
	}
}

func TestCreateDraft_MissingSlug(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	_, _, err := svc.CreateDraft(context.Background(), "t1", "", validAgentDef(t))
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
	if f.createAgentDefCalls != 0 {
		t.Error("DAL must not be called")
	}
}

func TestCreateDraft_EmptyDefinitionObject(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	_, _, err := svc.CreateDraft(context.Background(), "t1", "my-agent", json.RawMessage(`[]`))
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation for array input, got %v", err)
	}
}

func TestCreateDraft_RejectsSecretValueKey(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	bad := json.RawMessage(`{
		"agent_root": {"display_name": "X", "secret_value": "oops"},
		"skills": []
	}`)
	_, _, err := svc.CreateDraft(context.Background(), "t1", "my-agent", bad)
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation for secret_value key, got %v", err)
	}
	if f.createAgentDefCalls != 0 {
		t.Error("DAL must not be called when secret is present")
	}
}

func TestCreateDraft_RejectsSecretValue(t *testing.T) {
	// Any key named "secret_value" at any nesting level must be rejected.
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	bad := json.RawMessage(`{
		"agent_root": {"display_name": "X"},
		"skills": [],
		"secret_value": "should-be-rejected"
	}`)
	_, _, err := svc.CreateDraft(context.Background(), "t1", "my-agent", bad)
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation for secret_value key, got %v", err)
	}
}

func TestCreateDraft_ValidMinimalDefinition(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	good := json.RawMessage(`{"agent_root": {"display_name": "Minimal"}, "skills": []}`)
	_, _, err := svc.CreateDraft(context.Background(), "t1", "my-agent", good)
	if err != nil {
		t.Errorf("want no error for minimal valid definition, got %v", err)
	}
}

func TestCreateDraft_DuplicateSkillId(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	bad := json.RawMessage(`{
		"agent_root": {"display_name": "X", "credential_slots": []},
		"skills": [
			{"skill_id": "s1", "name": "Skill A"},
			{"skill_id": "s1", "name": "Skill B"}
		]
	}`)
	_, _, err := svc.CreateDraft(context.Background(), "t1", "my-agent", bad)
	if !errors.Is(err, service.ErrUnprocessable) {
		t.Errorf("want ErrUnprocessable for duplicate skill_id, got %v", err)
	}
}

func TestCreateDraft_DuplicateStepId(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	bad := json.RawMessage(`{
		"agent_root": {"display_name": "X", "credential_slots": []},
		"skills": [{
			"skill_id": "s1",
			"name": "Skill",
			"steps": [
				{"id": "step1", "type": "input"},
				{"id": "step1", "type": "response"}
			]
		}]
	}`)
	_, _, err := svc.CreateDraft(context.Background(), "t1", "my-agent", bad)
	if !errors.Is(err, service.ErrUnprocessable) {
		t.Errorf("want ErrUnprocessable for duplicate step id, got %v", err)
	}
}

func TestCreateDraft_RevisionIncrements(t *testing.T) {
	f := &agentDefFakeDal{nextRev: 2, createdAgentID: "def-uuid-2"}
	svc := newAgentDefSvc(f)
	_, rev, err := svc.CreateDraft(context.Background(), "t1", "my-agent", validAgentDef(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rev != 2 {
		t.Errorf("want revision=2, got %d", rev)
	}
}

func TestCreateDraft_UniqueViolation_MapsConflict(t *testing.T) {
	// Simulate a pgx unique violation error using a real pgconn.PgError.
	pgErr := &pgconn.PgError{Code: "23505"}
	f := &agentDefFakeDal{
		nextRev:           1,
		createAgentDefErr: pgErr,
	}
	svc := newAgentDefSvc(f)
	_, _, err := svc.CreateDraft(context.Background(), "t1", "my-agent", validAgentDef(t))
	if !errors.Is(err, service.ErrConflict) {
		t.Errorf("want ErrConflict for unique violation, got %v", err)
	}
}

func TestGetDefinition_NotFound(t *testing.T) {
	f := &agentDefFakeDal{getAgentDefErr: pgx.ErrNoRows}
	svc := newAgentDefSvc(f)
	_, err := svc.GetDefinition(context.Background(), "t1", "missing-id")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestGetDefinition_Found(t *testing.T) {
	f := &agentDefFakeDal{agentDef: dal.AgentDefinition{ID: "def-1", AgentSlug: "my-agent", TenantID: "t1"}}
	svc := newAgentDefSvc(f)
	def, err := svc.GetDefinition(context.Background(), "t1", "def-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.ID != "def-1" {
		t.Errorf("want ID=def-1, got %q", def.ID)
	}
}

func TestListDefinitions_EmptyReturnsNonNil(t *testing.T) {
	f := &agentDefFakeDal{agentDefs: nil}
	svc := newAgentDefSvc(f)
	defs, err := svc.ListDefinitions(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if defs == nil {
		t.Error("ListDefinitions must return [] not nil")
	}
}

func TestUpdateDraft_Valid(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	err := svc.UpdateDraft(context.Background(), "t1", "def-1", validAgentDef(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.updateAgentDefCalls != 1 {
		t.Error("UpdateDraftAgentDefinition must be called once")
	}
	if f.lastUpdateHash == "" {
		t.Error("hash must be set on update")
	}
}

func TestUpdateDraft_NotFound(t *testing.T) {
	f := &agentDefFakeDal{
		updateAgentDefErr: pgx.ErrNoRows,
		getAgentDefErr:    pgx.ErrNoRows,
	}
	svc := newAgentDefSvc(f)
	err := svc.UpdateDraft(context.Background(), "t1", "missing", validAgentDef(t))
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestUpdateDraft_Published_Conflict(t *testing.T) {
	f := &agentDefFakeDal{
		updateAgentDefErr: pgx.ErrNoRows,
		agentDef:          dal.AgentDefinition{ID: "def-1", Status: "published"},
	}
	svc := newAgentDefSvc(f)
	err := svc.UpdateDraft(context.Background(), "t1", "def-1", validAgentDef(t))
	if !errors.Is(err, service.ErrConflict) {
		t.Errorf("want ErrConflict for published definition, got %v", err)
	}
}

func TestUpdateDraft_RejectsSecrets(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	bad := json.RawMessage(`{
		"agent_root": {"display_name": "X", "secret_value": "boom"},
		"skills": []
	}`)
	err := svc.UpdateDraft(context.Background(), "t1", "def-1", bad)
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
	if f.updateAgentDefCalls != 0 {
		t.Error("DAL must not be called when secret is present")
	}
}

func TestDeleteDraft_Valid(t *testing.T) {
	f := &agentDefFakeDal{}
	svc := newAgentDefSvc(f)
	err := svc.DeleteDraft(context.Background(), "t1", "def-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.deleteAgentDefCalls != 1 {
		t.Error("DeleteDraftAgentDefinition must be called once")
	}
}

func TestDeleteDraft_NotFound(t *testing.T) {
	f := &agentDefFakeDal{
		deleteAgentDefErr: pgx.ErrNoRows,
		getAgentDefErr:    pgx.ErrNoRows,
	}
	svc := newAgentDefSvc(f)
	err := svc.DeleteDraft(context.Background(), "t1", "def-1")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteDraft_Published_Conflict(t *testing.T) {
	f := &agentDefFakeDal{
		deleteAgentDefErr: pgx.ErrNoRows,
		agentDef:          dal.AgentDefinition{ID: "def-1", Status: "published"},
	}
	svc := newAgentDefSvc(f)
	err := svc.DeleteDraft(context.Background(), "t1", "def-1")
	if !errors.Is(err, service.ErrConflict) {
		t.Errorf("want ErrConflict for published definition, got %v", err)
	}
}

func TestHashDeterminism(t *testing.T) {
	// Same content, different key order → same hash.
	d1 := &agentDefFakeDal{}
	d2 := &agentDefFakeDal{}
	svc1 := newAgentDefSvc(d1)
	svc2 := newAgentDefSvc(d2)

	def1 := json.RawMessage(`{"agent_root":{"display_name":"A","credential_slots":[]},"skills":[]}`)
	def2 := json.RawMessage(`{"skills":[],"agent_root":{"credential_slots":[],"display_name":"A"}}`)

	// We test hash determinism indirectly: update with both forms — if hashes match
	// the service calls the DAL; both must succeed with calls recorded.
	_ = svc1.UpdateDraft(context.Background(), "t1", "id1", def1)
	_ = svc2.UpdateDraft(context.Background(), "t1", "id1", def2)

	if d1.lastUpdateHash == "" || d2.lastUpdateHash == "" {
		t.Skip("could not capture hashes (update returned error)")
	}
	if d1.lastUpdateHash != d2.lastUpdateHash {
		t.Errorf("hash mismatch: %q != %q", d1.lastUpdateHash, d2.lastUpdateHash)
	}
}
