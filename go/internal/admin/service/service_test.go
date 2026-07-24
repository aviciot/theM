package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
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
}

func (f *fakeDal) ListAgents(_ context.Context) ([]dal.Agent, error) {
	return f.agents, f.listAgentsErr
}
func (f *fakeDal) GetAgent(_ context.Context, _ string) (dal.Agent, error) {
	return f.agent, f.getAgentErr
}
func (f *fakeDal) CreateAgent(_ context.Context, in dal.AgentInput, enabled bool) (string, error) {
	f.createAgentCalls = append(f.createAgentCalls, in)
	f.createAgentEnabledCalls = append(f.createAgentEnabledCalls, enabled)
	return f.createdID, f.createAgentErr
}
func (f *fakeDal) UpdateAgent(_ context.Context, _ string, in dal.AgentInput, _ bool) error {
	f.updateAgentCalls = append(f.updateAgentCalls, in)
	return f.updateAgentErr
}
func (f *fakeDal) DeleteAgent(_ context.Context, _ string) error { return f.deleteAgentErr }

func (f *fakeDal) ListOrchestrators(_ context.Context) ([]dal.Orchestrator, error) {
	return f.orchs, f.listOrchsErr
}
func (f *fakeDal) GetOrchestrator(_ context.Context, _ string) (dal.Orchestrator, error) {
	return f.orch, f.getOrchErr
}
func (f *fakeDal) CreateOrchestrator(_ context.Context, in dal.OrchestratorInput, enabled bool) (string, error) {
	f.createOrchCalls = append(f.createOrchCalls, in)
	f.createOrchEnabledCalls = append(f.createOrchEnabledCalls, enabled)
	return f.createdID, f.createOrchErr
}
func (f *fakeDal) UpdateOrchestrator(_ context.Context, _ string, _ dal.OrchestratorInput, _ bool) error {
	return f.updateOrchErr
}
func (f *fakeDal) DeleteOrchestrator(_ context.Context, _ string) error { return f.deleteOrchErr }

func (f *fakeDal) ListApplications(_ context.Context) ([]dal.Application, error) {
	return f.apps, f.listAppsErr
}
func (f *fakeDal) GetApplication(_ context.Context, _ string) (dal.Application, error) {
	return f.app, f.getAppErr
}
func (f *fakeDal) CreateApplication(_ context.Context, _ string, _ bool) (string, error) {
	return f.createdID, f.createAppErr
}
func (f *fakeDal) UpdateApplication(_ context.Context, _, _ string, _ bool) error {
	return f.updateAppErr
}
func (f *fakeDal) DeleteApplication(_ context.Context, _ string) error { return f.deleteAppErr }
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

func (f *fakeDal) ListRuns(_ context.Context, _ string, _ int) ([]dal.Run, error) {
	return f.runs, f.listRunsErr
}
func (f *fakeDal) GetRun(_ context.Context, _ string) (dal.Run, error) {
	return f.run, f.getRunErr
}
func (f *fakeDal) GetRunContextID(_ context.Context, _ string) (string, error) {
	return f.contextID, f.getContextIDErr
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

// ── AgentService tests ────────────────────────────────────────────────────────

func TestAgentService_Create_Defaults(t *testing.T) {
	d := &fakeDal{createdID: "id-1"}
	svc := service.NewAgentService(d, nil)
	in := dal.AgentInput{Slug: "my-agent", DisplayName: "My Agent"}
	_, err := svc.Create(context.Background(), in)
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
	_, err := svc.Create(context.Background(), dal.AgentInput{DisplayName: "D"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestAgentService_Create_MissingDisplayName_Validation(t *testing.T) {
	svc := service.NewAgentService(&fakeDal{}, nil)
	_, err := svc.Create(context.Background(), dal.AgentInput{Slug: "s"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestAgentService_Create_EnabledFalse_Respected(t *testing.T) {
	d := &fakeDal{createdID: "id-2"}
	svc := service.NewAgentService(d, nil)
	f := false
	_, _ = svc.Create(context.Background(), dal.AgentInput{Slug: "s", DisplayName: "D", Enabled: &f})
	if len(d.createAgentEnabledCalls) == 0 || d.createAgentEnabledCalls[0] {
		t.Error("enabled=false must be passed to DAL")
	}
}

func TestAgentService_Update_ReappliesMaxConcurrencyDefault(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAgentService(d, nil)
	_ = svc.Update(context.Background(), "id-1", dal.AgentInput{MaxConcurrency: 0})
	if len(d.updateAgentCalls) == 0 || d.updateAgentCalls[0].MaxConcurrency != 5 {
		t.Error("MaxConcurrency must default to 5 on update")
	}
}

func TestAgentService_Create_InvalidatesRegistry(t *testing.T) {
	c := &fakeCache{}
	svc := service.NewAgentService(&fakeDal{createdID: "id-3"}, c)
	_, _ = svc.Create(context.Background(), dal.AgentInput{Slug: "s", DisplayName: "D"})
	found := false
	for _, k := range c.deletedKeys {
		if k == "them:agents:registry" {
			found = true
		}
	}
	if !found {
		t.Errorf("them:agents:registry not deleted, got %v", c.deletedKeys)
	}
}

func TestAgentService_NilCache_NoPanic(t *testing.T) {
	svc := service.NewAgentService(&fakeDal{createdID: "id-4"}, nil)
	_, err := svc.Create(context.Background(), dal.AgentInput{Slug: "s", DisplayName: "D"})
	if err != nil {
		t.Fatal(err)
	}
}

// ── OrchService tests ─────────────────────────────────────────────────────────

func TestOrchService_Create_Defaults(t *testing.T) {
	d := &fakeDal{createdID: "orch-1"}
	svc := service.NewOrchService(d, nil)
	in := dal.OrchestratorInput{Name: "my-orch"}
	_, err := svc.Create(context.Background(), in)
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
	_, err := svc.Create(context.Background(), dal.OrchestratorInput{})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestOrchService_Create_InvalidatesCache(t *testing.T) {
	c := &fakeCache{}
	svc := service.NewOrchService(&fakeDal{createdID: "orch-2"}, c)
	_, _ = svc.Create(context.Background(), dal.OrchestratorInput{Name: "my-orch"})
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
	_ = svc.Delete(context.Background(), "target-orch")
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
	err := svc.Signal(context.Background(), "run-1", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(temp.signaled) != 1 || temp.signaled[0] != "ctx-abc-123" {
		t.Errorf("want [ctx-abc-123], got %v", temp.signaled)
	}
}

func TestRunService_Signal_TemporalNil_Unavailable(t *testing.T) {
	svc := service.NewRunService(&fakeDal{}, nil)
	err := svc.Signal(context.Background(), "run-1", nil)
	if !errors.Is(err, service.ErrTemporalUnavailable) {
		t.Errorf("want ErrTemporalUnavailable, got %v", err)
	}
}

func TestRunService_Signal_DBError_NotNotFound(t *testing.T) {
	// Non-pgx error → generic db error wrapping, not ErrNotFound.
	// (pgx.ErrNoRows path is covered by the integration suite.)
	d := &fakeDal{getContextIDErr: errors.New("connection reset")}
	svc := service.NewRunService(d, &fakeTemporal{})
	err := svc.Signal(context.Background(), "run-1", nil)
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
	runs, err := svc.List(context.Background(), "ctx-id", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("want 1 run, got %d", len(runs))
	}
}
