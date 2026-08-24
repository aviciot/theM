package service_test

// R-4c1 tenant isolation tests
//
// Each entity type (agents, orchestrators, applications, runs, tokens) is
// verified against four contracts:
//   TC-OWN   own record succeeds
//   TC-OTHER other tenant cannot read/update/delete
//   TC-SLUG  same slug/name allowed across tenants
//   TC-DUP   duplicate inside same tenant returns conflict (via DAL unique violation)

import (
	"context"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/agentgen"
)

// ── Tenant-aware fake DAL ─────────────────────────────────────────────────────
//
// isolationFakeDal stores tenant-keyed records and enforces tenant scoping.
// It is independent from fakeDal in service_test.go to keep it self-contained.

const (
	tenantAlpha = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tenantBravo = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// isoRecord is one stored item in the isolation fake.
type isoRecord struct {
	tenantID string
	id       string
	slug     string // agents only
	name     string // orchestrators, applications
}

type isolationFakeDal struct {
	agents []isoRecord
	orchs  []isoRecord
	apps   []isoRecord
	runs   []isoRecord
	tokens []isoRecord

	// per-call captures
	lastCreatedTenant string
	lastCreatedSlug   string
}

// helpers
func (f *isolationFakeDal) recordsForTenant(recs []isoRecord, tid string) []isoRecord {
	var out []isoRecord
	for _, r := range recs {
		if r.tenantID == tid {
			out = append(out, r)
		}
	}
	return out
}

func (f *isolationFakeDal) findByIDAndTenant(recs []isoRecord, tid, id string) (isoRecord, bool) {
	for _, r := range recs {
		if r.tenantID == tid && r.id == id {
			return r, true
		}
	}
	return isoRecord{}, false
}

func (f *isolationFakeDal) findByNameAndTenant(recs []isoRecord, tid, name string) (isoRecord, bool) {
	for _, r := range recs {
		if r.tenantID == tid && r.name == name {
			return r, true
		}
	}
	return isoRecord{}, false
}

func (f *isolationFakeDal) slugExistsForTenant(recs []isoRecord, tid, slug string) bool {
	for _, r := range recs {
		if r.tenantID == tid && r.slug == slug {
			return true
		}
	}
	return false
}

// ── Agent methods ─────────────────────────────────────────────────────────────

func (f *isolationFakeDal) ListAgents(_ context.Context, tenantID string) ([]dal.Agent, error) {
	recs := f.recordsForTenant(f.agents, tenantID)
	out := make([]dal.Agent, len(recs))
	for i, r := range recs {
		out[i] = dal.Agent{ID: r.id, Slug: r.slug}
	}
	return out, nil
}
func (f *isolationFakeDal) GetAgent(_ context.Context, tenantID, id string) (dal.Agent, error) {
	r, ok := f.findByIDAndTenant(f.agents, tenantID, id)
	if !ok {
		return dal.Agent{}, errors.New("not found")
	}
	return dal.Agent{ID: r.id, Slug: r.slug}, nil
}
func (f *isolationFakeDal) CreateAgent(_ context.Context, tenantID string, in dal.AgentInput, _ bool) (string, error) {
	if f.slugExistsForTenant(f.agents, tenantID, in.Slug) {
		return "", &pgxUniqueViolation{}
	}
	id := tenantID + "/" + in.Slug
	f.agents = append(f.agents, isoRecord{tenantID: tenantID, id: id, slug: in.Slug})
	f.lastCreatedTenant = tenantID
	f.lastCreatedSlug = in.Slug
	return id, nil
}
func (f *isolationFakeDal) UpdateAgent(_ context.Context, tenantID, id string, _ dal.AgentInput, _ bool) error {
	_, ok := f.findByIDAndTenant(f.agents, tenantID, id)
	if !ok {
		return errors.New("not found")
	}
	return nil
}
func (f *isolationFakeDal) DeleteAgent(_ context.Context, tenantID, id string) error {
	_, ok := f.findByIDAndTenant(f.agents, tenantID, id)
	if !ok {
		return errors.New("not found")
	}
	return nil
}

// ── Orchestrator methods ──────────────────────────────────────────────────────

func (f *isolationFakeDal) ListOrchestrators(_ context.Context, tenantID string) ([]dal.Orchestrator, error) {
	recs := f.recordsForTenant(f.orchs, tenantID)
	out := make([]dal.Orchestrator, len(recs))
	for i, r := range recs {
		out[i] = dal.Orchestrator{ID: r.id, Name: r.name}
	}
	return out, nil
}
func (f *isolationFakeDal) GetOrchestrator(_ context.Context, tenantID, name string) (dal.Orchestrator, error) {
	r, ok := f.findByNameAndTenant(f.orchs, tenantID, name)
	if !ok {
		return dal.Orchestrator{}, errors.New("not found")
	}
	return dal.Orchestrator{ID: r.id, Name: r.name}, nil
}
func (f *isolationFakeDal) CreateOrchestrator(_ context.Context, tenantID string, in dal.OrchestratorInput, _ bool) (string, error) {
	_, alreadyExists := f.findByNameAndTenant(f.orchs, tenantID, in.Name)
	if alreadyExists {
		return "", &pgxUniqueViolation{}
	}
	id := tenantID + "/" + in.Name
	f.orchs = append(f.orchs, isoRecord{tenantID: tenantID, id: id, name: in.Name})
	return id, nil
}
func (f *isolationFakeDal) UpdateOrchestrator(_ context.Context, tenantID, name string, _ dal.OrchestratorInput, _ bool) error {
	_, ok := f.findByNameAndTenant(f.orchs, tenantID, name)
	if !ok {
		return errors.New("not found")
	}
	return nil
}
func (f *isolationFakeDal) DeleteOrchestrator(_ context.Context, tenantID, name string) error {
	_, ok := f.findByNameAndTenant(f.orchs, tenantID, name)
	if !ok {
		return errors.New("not found")
	}
	return nil
}

// ── Application methods ───────────────────────────────────────────────────────

func (f *isolationFakeDal) ListApplications(_ context.Context, tenantID string) ([]dal.Application, error) {
	recs := f.recordsForTenant(f.apps, tenantID)
	out := make([]dal.Application, len(recs))
	for i, r := range recs {
		out[i] = dal.Application{ID: r.id, Name: r.name}
	}
	return out, nil
}
func (f *isolationFakeDal) GetApplication(_ context.Context, tenantID, id string) (dal.Application, error) {
	r, ok := f.findByIDAndTenant(f.apps, tenantID, id)
	if !ok {
		return dal.Application{}, errors.New("not found")
	}
	return dal.Application{ID: r.id, Name: r.name}, nil
}
func (f *isolationFakeDal) CreateApplication(_ context.Context, tenantID, name string, _ bool) (string, error) {
	id := tenantID + "/" + name
	f.apps = append(f.apps, isoRecord{tenantID: tenantID, id: id, name: name})
	return id, nil
}
func (f *isolationFakeDal) UpdateApplication(_ context.Context, tenantID, id, _ string, _ bool) error {
	_, ok := f.findByIDAndTenant(f.apps, tenantID, id)
	if !ok {
		return errors.New("not found")
	}
	return nil
}
func (f *isolationFakeDal) DeleteApplication(_ context.Context, tenantID, id string) error {
	_, ok := f.findByIDAndTenant(f.apps, tenantID, id)
	if !ok {
		return errors.New("not found")
	}
	return nil
}

// ── Entry point stubs (scoped through app, no tenant param needed in DAL) ─────

func (f *isolationFakeDal) ListEntryPoints(_ context.Context, _ string) []dal.EntryPoint { return nil }
func (f *isolationFakeDal) CreateEntryPoint(_ context.Context, _, _, _ string, _ bool) (string, error) {
	return "ep-id", nil
}
func (f *isolationFakeDal) GetEntryPointSlug(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (f *isolationFakeDal) UpdateEntryPoint(_ context.Context, _, _, _, _ string, _ bool) error {
	return nil
}
func (f *isolationFakeDal) DeleteEntryPoint(_ context.Context, _, _ string) error { return nil }
func (f *isolationFakeDal) ListEPSlugsForApp(_ context.Context, _ string) []string { return nil }
func (f *isolationFakeDal) GetEntryPointTenantAndSlug(_ context.Context, _, _ string) dal.EPTenantSlug {
	return dal.EPTenantSlug{}
}
func (f *isolationFakeDal) ListEPTenantSlugsForApp(_ context.Context, _ string) []dal.EPTenantSlug {
	return nil
}

// ── Run methods ───────────────────────────────────────────────────────────────

func (f *isolationFakeDal) ListRuns(_ context.Context, tenantID, _ string, _ int) ([]dal.Run, error) {
	recs := f.recordsForTenant(f.runs, tenantID)
	out := make([]dal.Run, len(recs))
	for i, r := range recs {
		out[i] = dal.Run{ID: r.id}
	}
	return out, nil
}
func (f *isolationFakeDal) GetRun(_ context.Context, tenantID, runID string) (dal.Run, error) {
	r, ok := f.findByIDAndTenant(f.runs, tenantID, runID)
	if !ok {
		return dal.Run{}, errors.New("not found")
	}
	return dal.Run{ID: r.id}, nil
}
func (f *isolationFakeDal) GetRunContextID(_ context.Context, tenantID, runID string) (string, error) {
	_, ok := f.findByIDAndTenant(f.runs, tenantID, runID)
	if !ok {
		return "", errors.New("not found")
	}
	return "ctx-" + runID, nil
}
func (f *isolationFakeDal) GetRunStats(_ context.Context, _ string) (dal.RunStats, error) {
	return dal.RunStats{ByStatus: make(map[string]int), TotalCostUSD: "0"}, nil
}
func (f *isolationFakeDal) GetRunDetail(_ context.Context, tenantID, runID string) (dal.RunDetail, error) {
	r, ok := f.findByIDAndTenant(f.runs, tenantID, runID)
	if !ok {
		return dal.RunDetail{}, errors.New("not found")
	}
	return dal.RunDetail{
		Run:      dal.Run{ID: r.id},
		Steps:    []dal.RunStep{},
		Usage:    []dal.RunUsage{},
		Children: []dal.Run{},
	}, nil
}
func (f *isolationFakeDal) GetRunTasks(_ context.Context, tenantID, runID string) ([]dal.Task, error) {
	_, ok := f.findByIDAndTenant(f.runs, tenantID, runID)
	if !ok {
		return nil, errors.New("not found")
	}
	return []dal.Task{}, nil
}
func (f *isolationFakeDal) GetRunArtifacts(_ context.Context, tenantID, runID string) ([]dal.Artifact, error) {
	_, ok := f.findByIDAndTenant(f.runs, tenantID, runID)
	if !ok {
		return nil, errors.New("not found")
	}
	return []dal.Artifact{}, nil
}
func (f *isolationFakeDal) ListContextSessions(_ context.Context, _, _ string, _ int) ([]dal.ContextSession, error) {
	return []dal.ContextSession{}, nil
}
func (f *isolationFakeDal) GetContextArtifacts(_ context.Context, _, _ string, _ int) ([]dal.Artifact, error) {
	return []dal.Artifact{}, nil
}
func (f *isolationFakeDal) GetContextMessages(_ context.Context, tenantID, contextID string, _ int) ([]dal.ContextMessage, error) {
	// context messages are scoped to tenant via tenantID param; fake enforces it using runs keyed by contextID
	for _, r := range f.runs {
		if r.tenantID == tenantID && r.id == contextID {
			return []dal.ContextMessage{}, nil
		}
	}
	return nil, errors.New("not found")
}

// ── Token methods ─────────────────────────────────────────────────────────────

func (f *isolationFakeDal) ListTokens(_ context.Context, tenantID string, _ *int64) ([]dal.Token, error) {
	recs := f.recordsForTenant(f.tokens, tenantID)
	out := make([]dal.Token, len(recs))
	for i, r := range recs {
		out[i] = dal.Token{ID: r.id}
	}
	return out, nil
}
func (f *isolationFakeDal) GetToken(_ context.Context, tenantID, id string) (dal.Token, error) {
	r, ok := f.findByIDAndTenant(f.tokens, tenantID, id)
	if !ok {
		return dal.Token{}, errors.New("not found")
	}
	return dal.Token{ID: r.id}, nil
}
func (f *isolationFakeDal) OrchestratorExists(_ context.Context, tenantID, orchID string) (bool, error) {
	_, ok := f.findByIDAndTenant(f.orchs, tenantID, orchID)
	return ok, nil
}
func (f *isolationFakeDal) CreateToken(_ context.Context, tenantID string, in dal.TokenCreateRow) (dal.Token, error) {
	id := tenantID + "/" + in.Label
	f.tokens = append(f.tokens, isoRecord{tenantID: tenantID, id: id})
	return dal.Token{ID: id, TokenHash: in.TokenHash}, nil
}
func (f *isolationFakeDal) UpdateToken(_ context.Context, tenantID, id string, _ dal.TokenPatchRow) (string, dal.Token, error) {
	r, ok := f.findByIDAndTenant(f.tokens, tenantID, id)
	if !ok {
		return "", dal.Token{}, errors.New("not found")
	}
	return "hash-" + id, dal.Token{ID: r.id}, nil
}
func (f *isolationFakeDal) DeleteToken(_ context.Context, tenantID, id string) (string, error) {
	r, ok := f.findByIDAndTenant(f.tokens, tenantID, id)
	if !ok {
		return "", errors.New("not found")
	}
	return "hash-" + r.id, nil
}

// ── Platform-global stubs ─────────────────────────────────────────────────────

func (f *isolationFakeDal) GetConfig(_ context.Context, _ string) (*dal.ConfigRow, error) {
	return nil, nil
}
func (f *isolationFakeDal) UpsertConfig(_ context.Context, _ string, _ []byte) error { return nil }
func (f *isolationFakeDal) ListProviders(_ context.Context) ([]dal.LLMProvider, error) { return nil, nil }
func (f *isolationFakeDal) GetProvider(_ context.Context, _ int64) (dal.LLMProvider, error) {
	return dal.LLMProvider{}, nil
}
func (f *isolationFakeDal) CreateProvider(_ context.Context, _ dal.LLMProviderInput) (dal.LLMProvider, error) {
	return dal.LLMProvider{}, nil
}
func (f *isolationFakeDal) UpdateProvider(_ context.Context, _ int64, _ dal.LLMProviderInput) (dal.LLMProvider, error) {
	return dal.LLMProvider{}, nil
}
func (f *isolationFakeDal) DeleteProvider(_ context.Context, _ int64) error { return nil }

// Runtime config + bulk delete stubs — no isolation-specific behavior needed.
func (f *isolationFakeDal) UpdateRuntimeConfig(_ context.Context, _, _ string, _ []byte) error {
	return nil
}
func (f *isolationFakeDal) ListAppOrchestratorNames(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *isolationFakeDal) BulkDeleteApplications(_ context.Context, _ string, _ []string) (int64, error) {
	return 0, nil
}
func (f *isolationFakeDal) GetProviderKeys(_ context.Context, _, _ string) ([]byte, error)       { return []byte(`{}`), nil }
func (f *isolationFakeDal) SetProviderKey(_ context.Context, _, _, _ string, _ []byte) error     { return nil }
func (f *isolationFakeDal) DeleteProviderKey(_ context.Context, _, _, _ string) error            { return nil }
func (f *isolationFakeDal) SetOrchestratorLLM(_ context.Context, _, _, _, _ string) error        { return nil }
func (f *isolationFakeDal) CancelRun(_ context.Context, _, _ string) (dal.Run, error) {
	return dal.Run{}, nil
}
func (f *isolationFakeDal) DeleteRun(_ context.Context, _, _ string) error { return nil }
func (f *isolationFakeDal) BulkDeleteRuns(_ context.Context, _ string, ids []string) (int64, error) {
	return int64(len(ids)), nil
}

// Agent action stubs — platform-global, no tenant scope.
func (f *isolationFakeDal) GetAgentBySlug(_ context.Context, _ string) (dal.Agent, error) {
	return dal.Agent{}, nil
}
func (f *isolationFakeDal) UpdateAgentScanResult(_ context.Context, _ string, _ []byte) error {
	return nil
}
func (f *isolationFakeDal) GetAgentByID(_ context.Context, _ string) (dal.Agent, error) {
	return dal.Agent{}, nil
}
func (f *isolationFakeDal) GetAgentTokenEncrypted(_ context.Context, _ string) (string, error) {
	return "", nil
}

// Application definition stubs (no business logic needed for isolation tests).
func (f *isolationFakeDal) GetNextRevision(_ context.Context, _ string) (int, error) {
	return 1, nil
}
func (f *isolationFakeDal) CreateDefinition(_ context.Context, _, _ string, _ int, _ []byte, _ string) (string, error) {
	return "", nil
}
func (f *isolationFakeDal) GetDefinition(_ context.Context, _, _, _ string) (dal.AppDefinition, error) {
	return dal.AppDefinition{}, nil
}
func (f *isolationFakeDal) ListDefinitions(_ context.Context, _, _ string) ([]dal.AppDefinition, error) {
	return []dal.AppDefinition{}, nil
}
func (f *isolationFakeDal) UpdateDraftDefinition(_ context.Context, _, _, _ string, _ []byte, _ string) error {
	return nil
}
func (f *isolationFakeDal) DeleteDraftDefinition(_ context.Context, _, _, _ string) error {
	return nil
}

// Phase C publish pipeline stubs.
func (f *isolationFakeDal) PublishDefinition(_ context.Context, _, _, _, _ string) (dal.PublishResult, error) {
	return dal.PublishResult{}, nil
}
func (f *isolationFakeDal) UpsertAppOrchestrator(_ context.Context, _ dal.AppOrchestratorRow) (string, error) {
	return "orch-id", nil
}
func (f *isolationFakeDal) UpsertEntryPoint(_ context.Context, _ dal.EntryPointRow) (string, error) {
	return "ep-id", nil
}
func (f *isolationFakeDal) DeactivateStaleOrchestrators(_ context.Context, _, _, _ string) error {
	return nil
}
func (f *isolationFakeDal) DeactivateStaleEntryPoints(_ context.Context, _, _, _ string) error {
	return nil
}
func (f *isolationFakeDal) ListComponentDefinitions(_ context.Context, _ string) ([]dal.ComponentDefinitionSummary, error) {
	return []dal.ComponentDefinitionSummary{}, nil
}

// Agent definition stubs (no isolation-specific behavior needed for current tests).
func (f *isolationFakeDal) GetNextAgentRevision(_ context.Context, _, _ string) (int, error) { return 1, nil }
func (f *isolationFakeDal) CreateAgentDefinition(_ context.Context, _, _ string, _ int, _ []byte, _ string, _ int) (string, error) {
	return "", nil
}
func (f *isolationFakeDal) GetAgentDefinition(_ context.Context, _, _ string) (dal.AgentDefinition, error) {
	return dal.AgentDefinition{}, nil
}
func (f *isolationFakeDal) ListAgentDefinitions(_ context.Context, _ string) ([]dal.AgentDefinition, error) {
	return []dal.AgentDefinition{}, nil
}
func (f *isolationFakeDal) UpdateDraftAgentDefinition(_ context.Context, _, _ string, _ []byte, _ string) error {
	return nil
}
func (f *isolationFakeDal) DeleteDraftAgentDefinition(_ context.Context, _, _ string) error { return nil }

// Phase 3 stubs.
func (f *isolationFakeDal) GetAgentDefinitionForPublish(_ context.Context, _ string, _ string) (dal.AgentDefinition, error) {
	return dal.AgentDefinition{}, nil
}
func (f *isolationFakeDal) PublishCanvasAgent(_ context.Context, _ dal.CanvasAgentRow) error {
	return nil
}
func (f *isolationFakeDal) MarkAgentDefinitionPublished(_ context.Context, _, _ string) error {
	return nil
}
func (f *isolationFakeDal) UpsertAgentBinding(_ context.Context, _ dal.AgentBindingRow) error {
	return nil
}
func (f *isolationFakeDal) GetAgentBindingStatus(_ context.Context, _, _ string) (dal.AgentBindingSlotStatus, error) {
	return dal.AgentBindingSlotStatus{CredentialSet: map[string]bool{}}, nil
}
func (f *isolationFakeDal) ListAgentBindings(_ context.Context, _ string) ([]dal.AgentBindingSlotStatus, error) {
	return []dal.AgentBindingSlotStatus{}, nil
}
func (f *isolationFakeDal) DeleteAgentBinding(_ context.Context, _, _ string) error { return nil }
func (f *isolationFakeDal) GetAgentParamsForBinding(_ context.Context, _, _ string) (dal.AgentParamsRow, error) {
	return dal.AgentParamsRow{RequiredParams: []agentgen.AgentParamSpec{}}, nil
}
func (f *isolationFakeDal) UpsertAgentParams(_ context.Context, _, _ string, _ []byte) error {
	return nil
}

// ── pgxUniqueViolation stub ───────────────────────────────────────────────────
//
// dal.IsUniqueViolation checks for pgconn.PgError with Code "23505".
// We can't import pgconn in this test package, so we simulate it differently:
// the isolationFakeDal returns this error; the service layer currently does NOT
// call dal.IsUniqueViolation for agents/orchs — it propagates the raw error.
// The "duplicate same tenant → conflict" tests therefore verify that the service
// returns a non-nil error (conflict detection is tested at the DAL level via
// real Postgres in integration tests). The cross-tenant same-slug test verifies
// that a second call to the OTHER tenant's Create does NOT return an error.
type pgxUniqueViolation struct{}

func (e *pgxUniqueViolation) Error() string { return "ERROR: duplicate key value (SQLSTATE 23505)" }

// ── AgentService isolation tests ──────────────────────────────────────────────

// TC-OWN: own record succeeds (agent)
func TestAgentService_TenantIsolation_OwnRecordSucceeds(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAgentService(f, nil)
	ctx := context.Background()

	id, err := svc.Create(ctx, tenantAlpha, dal.AgentInput{Slug: "my-agent", DisplayName: "My Agent"})
	if err != nil {
		t.Fatalf("Create(alpha): unexpected error: %v", err)
	}

	got, err := svc.Get(ctx, tenantAlpha, id)
	if err != nil {
		t.Fatalf("Get(alpha, %s): unexpected error: %v", id, err)
	}
	if got.Slug != "my-agent" {
		t.Errorf("Get: want slug=my-agent, got %q", got.Slug)
	}
}

// TC-OTHER: other tenant cannot read agent
func TestAgentService_TenantIsolation_OtherTenantCannotRead(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAgentService(f, nil)
	ctx := context.Background()

	id, err := svc.Create(ctx, tenantAlpha, dal.AgentInput{Slug: "secret-agent", DisplayName: "Secret"})
	if err != nil {
		t.Fatalf("Create(alpha): %v", err)
	}

	_, err = svc.Get(ctx, tenantBravo, id)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(bravo, alpha's id): want ErrNotFound, got %v", err)
	}
}

// TC-OTHER: other tenant cannot update agent
func TestAgentService_TenantIsolation_OtherTenantCannotUpdate(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAgentService(f, nil)
	ctx := context.Background()

	id, _ := svc.Create(ctx, tenantAlpha, dal.AgentInput{Slug: "upd-agent", DisplayName: "Upd"})

	// Bravo tenant's Update calls UpdateAgent with tenantBravo, which returns not-found.
	err := svc.Update(ctx, tenantBravo, id, dal.AgentInput{DisplayName: "Hijacked"})
	if err == nil {
		t.Error("Update(bravo, alpha's id): expected error, got nil")
	}
}

// TC-OTHER: other tenant cannot delete agent
func TestAgentService_TenantIsolation_OtherTenantCannotDelete(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAgentService(f, nil)
	ctx := context.Background()

	id, _ := svc.Create(ctx, tenantAlpha, dal.AgentInput{Slug: "del-agent", DisplayName: "Del"})

	err := svc.Delete(ctx, tenantBravo, id)
	if err == nil {
		t.Error("Delete(bravo, alpha's id): expected error, got nil")
	}
}

// TC-SLUG: same slug allowed across tenants (agent)
func TestAgentService_TenantIsolation_SameSlugAcrossTenantsAllowed(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAgentService(f, nil)
	ctx := context.Background()

	_, err := svc.Create(ctx, tenantAlpha, dal.AgentInput{Slug: "shared-slug", DisplayName: "Alpha"})
	if err != nil {
		t.Fatalf("Create(alpha): %v", err)
	}

	_, err = svc.Create(ctx, tenantBravo, dal.AgentInput{Slug: "shared-slug", DisplayName: "Bravo"})
	if err != nil {
		t.Errorf("Create(bravo, same slug): expected success, got %v", err)
	}
}

// TC-DUP: duplicate slug inside same tenant returns error (agent)
func TestAgentService_TenantIsolation_DuplicateSlugSameTenantReturnsError(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAgentService(f, nil)
	ctx := context.Background()

	_, err := svc.Create(ctx, tenantAlpha, dal.AgentInput{Slug: "dup-slug", DisplayName: "First"})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}

	_, err = svc.Create(ctx, tenantAlpha, dal.AgentInput{Slug: "dup-slug", DisplayName: "Second"})
	if err == nil {
		t.Error("Create duplicate slug same tenant: expected error, got nil")
	}
}

// ── AgentService List isolation ───────────────────────────────────────────────

// TC-OWN/LIST: List only returns own tenant's agents
func TestAgentService_TenantIsolation_ListReturnsOwnTenantOnly(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAgentService(f, nil)
	ctx := context.Background()

	_, _ = svc.Create(ctx, tenantAlpha, dal.AgentInput{Slug: "alpha-agent", DisplayName: "A"})
	_, _ = svc.Create(ctx, tenantBravo, dal.AgentInput{Slug: "bravo-agent", DisplayName: "B"})

	alphaAgents, err := svc.List(ctx, tenantAlpha)
	if err != nil {
		t.Fatal(err)
	}
	if len(alphaAgents) != 1 || alphaAgents[0].Slug != "alpha-agent" {
		t.Errorf("List(alpha): want [alpha-agent], got %+v", alphaAgents)
	}

	bravoAgents, _ := svc.List(ctx, tenantBravo)
	if len(bravoAgents) != 1 || bravoAgents[0].Slug != "bravo-agent" {
		t.Errorf("List(bravo): want [bravo-agent], got %+v", bravoAgents)
	}
}

// ── OrchService isolation tests ───────────────────────────────────────────────

// TC-OWN: own record succeeds (orchestrator)
func TestOrchService_TenantIsolation_OwnRecordSucceeds(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewOrchService(f, nil)
	ctx := context.Background()

	_, err := svc.Create(ctx, tenantAlpha, dal.OrchestratorInput{Name: "my-orch", LLMProvider: "anthropic", LLMModel: "claude"})
	if err != nil {
		t.Fatalf("Create(alpha): %v", err)
	}

	got, err := svc.Get(ctx, tenantAlpha, "my-orch")
	if err != nil {
		t.Fatalf("Get(alpha): %v", err)
	}
	if got.Name != "my-orch" {
		t.Errorf("Get: want name=my-orch, got %q", got.Name)
	}
}

// TC-OTHER: other tenant cannot read orchestrator
func TestOrchService_TenantIsolation_OtherTenantCannotRead(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewOrchService(f, nil)
	ctx := context.Background()

	_, _ = svc.Create(ctx, tenantAlpha, dal.OrchestratorInput{Name: "secret-orch", LLMProvider: "p", LLMModel: "m"})

	_, err := svc.Get(ctx, tenantBravo, "secret-orch")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(bravo, alpha's orch): want ErrNotFound, got %v", err)
	}
}

// TC-SLUG: same name allowed across tenants (orchestrator)
func TestOrchService_TenantIsolation_SameNameAcrossTenantsAllowed(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewOrchService(f, nil)
	ctx := context.Background()

	_, err := svc.Create(ctx, tenantAlpha, dal.OrchestratorInput{Name: "shared-orch", LLMProvider: "p", LLMModel: "m"})
	if err != nil {
		t.Fatalf("Create(alpha): %v", err)
	}

	_, err = svc.Create(ctx, tenantBravo, dal.OrchestratorInput{Name: "shared-orch", LLMProvider: "p", LLMModel: "m"})
	if err != nil {
		t.Errorf("Create(bravo, same name): expected success, got %v", err)
	}
}

// TC-DUP: duplicate name inside same tenant returns error (orchestrator)
func TestOrchService_TenantIsolation_DuplicateNameSameTenantReturnsError(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewOrchService(f, nil)
	ctx := context.Background()

	_, _ = svc.Create(ctx, tenantAlpha, dal.OrchestratorInput{Name: "dup-orch", LLMProvider: "p", LLMModel: "m"})
	_, err := svc.Create(ctx, tenantAlpha, dal.OrchestratorInput{Name: "dup-orch", LLMProvider: "p", LLMModel: "m"})
	if err == nil {
		t.Error("Create duplicate name same tenant: expected error, got nil")
	}
}

// ── AppService isolation tests ────────────────────────────────────────────────

// TC-OWN: own record succeeds (application)
func TestAppService_TenantIsolation_OwnRecordSucceeds(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAppService(f, nil, nil)
	ctx := context.Background()

	id, err := svc.Create(ctx, tenantAlpha, "my-app", nil)
	if err != nil {
		t.Fatalf("Create(alpha): %v", err)
	}

	got, err := svc.Get(ctx, tenantAlpha, id)
	if err != nil {
		t.Fatalf("Get(alpha, %s): %v", id, err)
	}
	if got.Name != "my-app" {
		t.Errorf("Get: want name=my-app, got %q", got.Name)
	}
}

// TC-OTHER: other tenant cannot read application
func TestAppService_TenantIsolation_OtherTenantCannotRead(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAppService(f, nil, nil)
	ctx := context.Background()

	id, _ := svc.Create(ctx, tenantAlpha, "secret-app", nil)

	_, err := svc.Get(ctx, tenantBravo, id)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(bravo, alpha's app): want ErrNotFound, got %v", err)
	}
}

// TC-SLUG: same name allowed across tenants (application)
func TestAppService_TenantIsolation_SameNameAcrossTenantsAllowed(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewAppService(f, nil, nil)
	ctx := context.Background()

	_, err := svc.Create(ctx, tenantAlpha, "shared-app", nil)
	if err != nil {
		t.Fatalf("Create(alpha): %v", err)
	}
	_, err = svc.Create(ctx, tenantBravo, "shared-app", nil)
	if err != nil {
		t.Errorf("Create(bravo, same name): expected success, got %v", err)
	}
}

// ── RunService isolation tests ────────────────────────────────────────────────

// TC-OWN: own record succeeds (run)
func TestRunService_TenantIsolation_OwnRecordSucceeds(t *testing.T) {
	f := &isolationFakeDal{}
	f.runs = append(f.runs, isoRecord{tenantID: tenantAlpha, id: "run-alpha-1"})
	svc := service.NewRunService(f, nil)
	ctx := context.Background()

	got, err := svc.Get(ctx, tenantAlpha, "run-alpha-1")
	if err != nil {
		t.Fatalf("Get(alpha): %v", err)
	}
	if got.ID != "run-alpha-1" {
		t.Errorf("Get: want id=run-alpha-1, got %q", got.ID)
	}
}

// TC-OTHER: other tenant cannot read run
func TestRunService_TenantIsolation_OtherTenantCannotRead(t *testing.T) {
	f := &isolationFakeDal{}
	f.runs = append(f.runs, isoRecord{tenantID: tenantAlpha, id: "run-alpha-2"})
	svc := service.NewRunService(f, nil)
	ctx := context.Background()

	_, err := svc.Get(ctx, tenantBravo, "run-alpha-2")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(bravo, alpha's run): want ErrNotFound, got %v", err)
	}
}

// TC-OWN/LIST: List only returns own tenant's runs
func TestRunService_TenantIsolation_ListReturnsOwnTenantOnly(t *testing.T) {
	f := &isolationFakeDal{}
	f.runs = append(f.runs,
		isoRecord{tenantID: tenantAlpha, id: "run-a"},
		isoRecord{tenantID: tenantBravo, id: "run-b"},
	)
	svc := service.NewRunService(f, nil)
	ctx := context.Background()

	alphaRuns, err := svc.List(ctx, tenantAlpha, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(alphaRuns) != 1 || alphaRuns[0].ID != "run-a" {
		t.Errorf("List(alpha): want [run-a], got %+v", alphaRuns)
	}

	bravoRuns, _ := svc.List(ctx, tenantBravo, "", 50)
	if len(bravoRuns) != 1 || bravoRuns[0].ID != "run-b" {
		t.Errorf("List(bravo): want [run-b], got %+v", bravoRuns)
	}
}

// ── TokenService isolation tests ──────────────────────────────────────────────

// TC-OWN: own record succeeds (token)
func TestTokenService_TenantIsolation_OwnRecordSucceeds(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewTokenService(f, nil, &fakeTokenGen{plaintext: "pt", hash: "h1"})
	ctx := context.Background()

	out, err := svc.Create(ctx, tenantAlpha, dal.TokenCreateRow{Label: "tok-a", UserID: 1}, nil)
	if err != nil {
		t.Fatalf("Create(alpha): %v", err)
	}

	got, err := svc.Get(ctx, tenantAlpha, out.ID)
	if err != nil {
		t.Fatalf("Get(alpha): %v", err)
	}
	if got.ID != out.ID {
		t.Errorf("Get: want id=%s, got %q", out.ID, got.ID)
	}
}

// TC-OTHER: other tenant cannot read token
func TestTokenService_TenantIsolation_OtherTenantCannotRead(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewTokenService(f, nil, &fakeTokenGen{plaintext: "pt", hash: "h2"})
	ctx := context.Background()

	out, _ := svc.Create(ctx, tenantAlpha, dal.TokenCreateRow{Label: "tok-b", UserID: 1}, nil)

	_, err := svc.Get(ctx, tenantBravo, out.ID)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(bravo, alpha's token): want ErrNotFound, got %v", err)
	}
}

// TC-OTHER: other tenant cannot delete token
func TestTokenService_TenantIsolation_OtherTenantCannotDelete(t *testing.T) {
	f := &isolationFakeDal{}
	svc := service.NewTokenService(f, nil, &fakeTokenGen{plaintext: "pt", hash: "h3"})
	ctx := context.Background()

	out, _ := svc.Create(ctx, tenantAlpha, dal.TokenCreateRow{Label: "tok-c", UserID: 1}, nil)

	err := svc.Delete(ctx, tenantBravo, out.ID)
	// isolationFakeDal.DeleteToken returns not-found (not pgx.ErrNoRows), so service propagates it.
	// The important thing is that the delete did NOT succeed.
	if err == nil {
		t.Error("Delete(bravo, alpha's token): expected error, got nil")
	}
}

// ── RunService sub-resource isolation tests ───────────────────────────────────

// TC-OTHER: other tenant cannot read run tasks
func TestRunService_TenantIsolation_GetRunTasks_WrongTenant(t *testing.T) {
	f := &isolationFakeDal{}
	f.runs = append(f.runs, isoRecord{tenantID: tenantAlpha, id: "run-alpha-tasks"})
	svc := service.NewRunService(f, nil)
	ctx := context.Background()

	_, err := svc.GetTasks(ctx, tenantBravo, "run-alpha-tasks")
	if err == nil {
		t.Error("GetTasks(bravo, alpha's run): expected error, got nil")
	}
}

// TC-OTHER: other tenant cannot read run artifacts
func TestRunService_TenantIsolation_GetRunArtifacts_WrongTenant(t *testing.T) {
	f := &isolationFakeDal{}
	f.runs = append(f.runs, isoRecord{tenantID: tenantAlpha, id: "run-alpha-arts"})
	svc := service.NewRunService(f, nil)
	ctx := context.Background()

	_, err := svc.GetArtifacts(ctx, tenantBravo, "run-alpha-arts")
	if err == nil {
		t.Error("GetArtifacts(bravo, alpha's run): expected error, got nil")
	}
}

// TC-OTHER: other tenant cannot read context messages
func TestRunService_TenantIsolation_GetContextMessages_WrongTenant(t *testing.T) {
	f := &isolationFakeDal{}
	// store a "context" by run id so the fake can look it up
	f.runs = append(f.runs, isoRecord{tenantID: tenantAlpha, id: "ctx-alpha-1"})
	svc := service.NewRunService(f, nil)
	ctx := context.Background()

	_, err := svc.GetContextMessages(ctx, tenantBravo, "ctx-alpha-1", 10)
	if err == nil {
		t.Error("GetContextMessages(bravo, alpha's context): expected error, got nil")
	}
}

// TC-OWN/LIST: List only returns own tenant's tokens
func TestTokenService_TenantIsolation_ListReturnsOwnTenantOnly(t *testing.T) {
	f := &isolationFakeDal{}
	gen := &fakeTokenGen{plaintext: "pt", hash: "hx"}
	svc := service.NewTokenService(f, nil, gen)
	ctx := context.Background()

	gen.hash = "ha"
	_, _ = svc.Create(ctx, tenantAlpha, dal.TokenCreateRow{Label: "tok-alpha", UserID: 1}, nil)
	gen.hash = "hb"
	_, _ = svc.Create(ctx, tenantBravo, dal.TokenCreateRow{Label: "tok-bravo", UserID: 2}, nil)

	alphaToks, err := svc.List(ctx, tenantAlpha, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(alphaToks) != 1 {
		t.Errorf("List(alpha): want 1 token, got %d", len(alphaToks))
	}

	bravoToks, _ := svc.List(ctx, tenantBravo, nil)
	if len(bravoToks) != 1 {
		t.Errorf("List(bravo): want 1 token, got %d", len(bravoToks))
	}
}
