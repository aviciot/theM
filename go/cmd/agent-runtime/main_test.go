package main

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/temporal"
)

// buildTestDSN constructs a postgres DSN from environment variables for live DB tests.
func buildTestDSN(t *testing.T) string {
	t.Helper()
	host := os.Getenv("DATABASE_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("DATABASE_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("DATABASE_USER")
	if user == "" {
		user = "them"
	}
	password := os.Getenv("DATABASE_PASSWORD")
	name := os.Getenv("DATABASE_NAME")
	if name == "" {
		name = "them"
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s", user, password, host, port, name)
}

// TestSpecCache_MissAndHit verifies TTL-based eviction and cache hit.
func TestSpecCache_MissAndHit(t *testing.T) {
	c := &specCache{entries: make(map[string]*cachedSpec)}

	// Cold miss.
	if got := c.get("abc"); got != nil {
		t.Fatal("expected nil on cold miss")
	}

	spec := &agentgen.AgentSpec{Slug: "test-slug"}
	c.set("abc", spec)

	// Hot hit.
	if got := c.get("abc"); got == nil {
		t.Fatal("expected spec after set")
	}

	// Expired entry returns nil.
	c.mu.Lock()
	c.entries["abc"].expiresAt = time.Now().Add(-1 * time.Second)
	c.mu.Unlock()
	if got := c.get("abc"); got != nil {
		t.Fatal("expected nil after TTL expiry")
	}
}

// TestBuildSDKAgentCard_SupportedInterfacesAndModes verifies that buildSDKAgentCard
// emits a SupportedInterfaces entry (SDK v2.5 replacement for the deprecated URL field)
// and populates per-skill InputModes/OutputModes.
func TestBuildSDKAgentCard_SupportedInterfacesAndModes(t *testing.T) {
	spec := &agentgen.AgentSpec{
		Slug: "my-agent",
		Card: agentgen.CardSpec{
			Name:        "My Agent",
			Description: "Test agent",
			Version:     "1.0",
			Capabilities: agentgen.CapabilitiesSpec{
				Streaming: true,
			},
		},
		Skills: []agentgen.SkillSpec{
			{ID: "sk-1", Name: "Skill One", Description: "does stuff"},
		},
	}

	card := buildSDKAgentCard(spec)

	if len(card.SupportedInterfaces) == 0 {
		t.Fatal("expected at least one SupportedInterface")
	}
	iface := card.SupportedInterfaces[0]
	if !strings.Contains(iface.URL, "my-agent") {
		t.Errorf("SupportedInterface URL should contain slug, got %q", iface.URL)
	}
	if iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("expected JSONRPC binding, got %q", iface.ProtocolBinding)
	}

	if len(card.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(card.Skills))
	}
	sk := card.Skills[0]
	if len(sk.InputModes) == 0 {
		t.Error("skill InputModes should be populated")
	}
	if len(sk.OutputModes) == 0 {
		t.Error("skill OutputModes should be populated")
	}

	if !card.Capabilities.Streaming {
		t.Error("Capabilities.Streaming should be true")
	}
}

// TestBuildSDKAgentCard_StaticHandler verifies that NewStaticAgentCardHandler wrapping
// the SDK card returns HTTP 200 with JSON content-type.
func TestBuildSDKAgentCard_StaticHandler(t *testing.T) {
	spec := &agentgen.AgentSpec{
		Slug: "echo",
		Card: agentgen.CardSpec{Name: "Echo", Description: "echo", Version: "1.0"},
		Skills: []agentgen.SkillSpec{
			{ID: "echo-sk", Name: "Echo Skill"},
		},
	}
	card := buildSDKAgentCard(spec)
	h := a2asrv.NewStaticAgentCardHandler(card)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content-type, got %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Echo") {
		t.Errorf("response body should contain agent name, got %q", body)
	}
}

// TestExecuteSkill_SDKEventSequence verifies that executeSkill emits the expected
// A2A event sequence: Submitted → Working → Artifact → Completed.
// It uses a stub Runtime with a mock interpreter that returns a fixed result.
func TestExecuteSkill_SDKEventSequence(t *testing.T) {
	rt := &Runtime{
		interp: agentgen.NewInterpreter(
			&http.Client{},
			&stubLLMFactory{reply: "hello world"},
			"",
		),
	}

	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "app1",
		AgentID:       "agent1",
		Spec: &agentgen.AgentSpec{
			Skills: []agentgen.SkillSpec{
				{
					ID:   "sk1",
					Name: "Skill One",
					Steps: []agentgen.StepSpec{
						// Step IDs must be non-empty; the interpreter uses them to index the pipeline.
						{ID: "step-1", Type: agentgen.StepResponse, Config: []byte(`{}`)},
					},
				},
			},
		},
	}

	execCtx := &a2asrv.ExecutorContext{
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
	}

	seq := rt.executeSkill(context.Background(), ic, execCtx)

	var events []a2a.Event
	for evt, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, evt)
	}

	// Expect at least: Submitted, Working, Artifact, Completed (4 events).
	if len(events) < 4 {
		t.Fatalf("expected ≥4 events, got %d", len(events))
	}

	if _, ok := events[0].(*a2a.Task); !ok {
		t.Errorf("event[0] should be *a2a.Task (Submitted), got %T", events[0])
	}
	checkStatusEvent(t, events[1], a2a.TaskStateWorking, "event[1]")
	if _, ok := events[len(events)-2].(*a2a.TaskArtifactUpdateEvent); !ok {
		t.Errorf("penultimate event should be artifact, got %T", events[len(events)-2])
	}
	checkStatusEvent(t, events[len(events)-1], a2a.TaskStateCompleted, "last event")
}

// TestExecuteSkill_NoSkills_EmitsFailed verifies that an agent with no skills emits
// Submitted → Working → Failed (no panic, clean error path).
func TestExecuteSkill_NoSkills_EmitsFailed(t *testing.T) {
	rt := &Runtime{
		interp: agentgen.NewInterpreter(&http.Client{}, &stubLLMFactory{}, ""),
	}
	ic := &agentgen.InvocationContext{
		Spec: &agentgen.AgentSpec{Skills: []agentgen.SkillSpec{}},
	}
	execCtx := &a2asrv.ExecutorContext{
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
	}

	seq := rt.executeSkill(context.Background(), ic, execCtx)

	var events []a2a.Event
	for evt, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error from iterator: %v", err)
		}
		events = append(events, evt)
	}

	if len(events) < 3 {
		t.Fatalf("expected ≥3 events, got %d", len(events))
	}
	checkStatusEvent(t, events[len(events)-1], a2a.TaskStateFailed, "last event")
}

// TestExecuteSkill_StoredTask_NoSubmitted verifies that when StoredTask is non-nil
// (i.e. a continuation), the executor skips the Submitted event.
func TestExecuteSkill_StoredTask_NoSubmitted(t *testing.T) {
	rt := &Runtime{
		interp: agentgen.NewInterpreter(&http.Client{}, &stubLLMFactory{reply: "ok"}, ""),
	}
	ic := &agentgen.InvocationContext{
		Spec: &agentgen.AgentSpec{
			Skills: []agentgen.SkillSpec{
				{
					ID: "sk1",
					Steps: []agentgen.StepSpec{
						{ID: "step-1", Type: agentgen.StepResponse, Config: []byte(`{}`)},
					},
				},
			},
		},
	}
	taskID := a2a.NewTaskID()
	execCtx := &a2asrv.ExecutorContext{
		TaskID:     taskID,
		ContextID:  a2a.NewContextID(),
		Message:    a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue")),
		StoredTask: &a2a.Task{ID: taskID, ContextID: "ctx1"},
	}

	seq := rt.executeSkill(context.Background(), ic, execCtx)

	var events []a2a.Event
	for evt, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, evt)
	}

	// First event must NOT be a *a2a.Task (Submitted) since StoredTask is set.
	if _, ok := events[0].(*a2a.Task); ok {
		t.Error("expected no Submitted event when StoredTask is non-nil")
	}
	checkStatusEvent(t, events[0], a2a.TaskStateWorking, "event[0]")
}

// TestJSONRPCHandler_MethodNotFound verifies that NewJSONRPCHandler rejects unknown methods.
func TestJSONRPCHandler_MethodNotFound(t *testing.T) {
	executor := a2asrv.AgentExecutorFunc(func(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {}
	})
	handler := a2asrv.NewHandler(executor)
	h := a2asrv.NewJSONRPCHandler(handler)

	body := `{"jsonrpc":"2.0","id":1,"method":"unknown/method","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("JSONRPC errors should use 200, got %d", rr.Code)
	}
	resp := rr.Body.String()
	if !strings.Contains(resp, "error") {
		t.Errorf("expected error in response, got %q", resp)
	}
}

func checkStatusEvent(t *testing.T, evt a2a.Event, wantState a2a.TaskState, label string) {
	t.Helper()
	su, ok := evt.(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Errorf("%s: expected *TaskStatusUpdateEvent, got %T", label, evt)
		return
	}
	if su.Status.State != wantState {
		t.Errorf("%s: expected state %q, got %q", label, wantState, su.Status.State)
	}
}

// stubLLMFactory is a test double for agentgen.LLMFactory.
type stubLLMFactory struct{ reply string }

func (f *stubLLMFactory) NewProvider(_, _ string, _ int, _ string) (agentgen.LLMProvider, error) {
	return &stubLLMProvider{reply: f.reply}, nil
}

// stubLLMProvider is a test double for agentgen.LLMProvider.
type stubLLMProvider struct{ reply string }

func (p *stubLLMProvider) Complete(_ context.Context, _, _ string) (string, error) {
	return p.reply, nil
}

// TestExecuteSkill_SkillSelectionByID verifies that when Message.Metadata contains
// "skill_id", executeSkill runs the matching skill rather than always picking Skills[0].
func TestExecuteSkill_SkillSelectionByID(t *testing.T) {
	rt := &Runtime{
		interp: agentgen.NewInterpreter(&http.Client{}, &stubLLMFactory{reply: "from sk2"}, ""),
	}
	ic := &agentgen.InvocationContext{
		Spec: &agentgen.AgentSpec{
			Skills: []agentgen.SkillSpec{
				{ID: "sk1", Steps: []agentgen.StepSpec{{ID: "s1", Type: agentgen.StepResponse, Config: []byte(`{"from_var":"output"}`)}}},
				{ID: "sk2", Steps: []agentgen.StepSpec{{ID: "s2", Type: agentgen.StepResponse, Config: []byte(`{"from_var":"output"}`)}}},
			},
		},
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))
	msg.Metadata = map[string]any{"skill_id": "sk2"}
	execCtx := &a2asrv.ExecutorContext{
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
		Message:   msg,
	}

	var events []a2a.Event
	for evt, err := range rt.executeSkill(context.Background(), ic, execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, evt)
	}
	// Must complete (not fail) — wrong skill would run a different pipeline but still succeed.
	checkStatusEvent(t, events[len(events)-1], a2a.TaskStateCompleted, "last event")
}

// TestExecuteSkill_SkillSelectionByID_NotFound verifies that an unknown skill_id emits Failed.
func TestExecuteSkill_SkillSelectionByID_NotFound(t *testing.T) {
	rt := &Runtime{
		interp: agentgen.NewInterpreter(&http.Client{}, &stubLLMFactory{}, ""),
	}
	ic := &agentgen.InvocationContext{
		Spec: &agentgen.AgentSpec{
			Skills: []agentgen.SkillSpec{
				{ID: "sk1", Steps: []agentgen.StepSpec{{ID: "s1", Type: agentgen.StepResponse, Config: []byte(`{}`)}}},
			},
		},
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))
	msg.Metadata = map[string]any{"skill_id": "does-not-exist"}
	execCtx := &a2asrv.ExecutorContext{
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
		Message:   msg,
	}

	var events []a2a.Event
	for evt, err := range rt.executeSkill(context.Background(), ic, execCtx) {
		if err != nil {
			t.Fatalf("unexpected error from iterator: %v", err)
		}
		events = append(events, evt)
	}
	checkStatusEvent(t, events[len(events)-1], a2a.TaskStateFailed, "last event")
}

// TestExecuteSkill_PolicyAllowedSkillIDs_Denied verifies that when AllowedSkillIDs
// is set in the binding policy, executing a non-listed skill emits Failed.
func TestExecuteSkill_PolicyAllowedSkillIDs_Denied(t *testing.T) {
	rt := &Runtime{
		interp: agentgen.NewInterpreter(&http.Client{}, &stubLLMFactory{}, ""),
	}
	ic := &agentgen.InvocationContext{
		Spec: &agentgen.AgentSpec{
			Skills: []agentgen.SkillSpec{
				{ID: "sk1", Steps: []agentgen.StepSpec{{ID: "s1", Type: agentgen.StepResponse, Config: []byte(`{}`)}}},
			},
		},
		Policies: agentgen.InvocationPolicies{
			AllowedSkillIDs: []string{"sk-other"}, // sk1 is NOT allowed
		},
	}
	execCtx := &a2asrv.ExecutorContext{
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
	}

	var events []a2a.Event
	for evt, err := range rt.executeSkill(context.Background(), ic, execCtx) {
		if err != nil {
			t.Fatalf("unexpected error from iterator: %v", err)
		}
		events = append(events, evt)
	}
	checkStatusEvent(t, events[len(events)-1], a2a.TaskStateFailed, "last event")
}

// TestExecuteSkill_PolicyAllowedSkillIDs_Permitted verifies that when the requested
// skill IS in AllowedSkillIDs, execution succeeds normally.
func TestExecuteSkill_PolicyAllowedSkillIDs_Permitted(t *testing.T) {
	rt := &Runtime{
		interp: agentgen.NewInterpreter(&http.Client{}, &stubLLMFactory{reply: "ok"}, ""),
	}
	ic := &agentgen.InvocationContext{
		Spec: &agentgen.AgentSpec{
			Skills: []agentgen.SkillSpec{
				{ID: "sk1", Steps: []agentgen.StepSpec{{ID: "s1", Type: agentgen.StepResponse, Config: []byte(`{}`)}}},
			},
		},
		Policies: agentgen.InvocationPolicies{
			AllowedSkillIDs: []string{"sk1"}, // sk1 IS allowed
		},
	}
	execCtx := &a2asrv.ExecutorContext{
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
	}

	var events []a2a.Event
	for evt, err := range rt.executeSkill(context.Background(), ic, execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, evt)
	}
	checkStatusEvent(t, events[len(events)-1], a2a.TaskStateCompleted, "last event")
}

// TestSpecCache_IsolatedKeys verifies that different keys don't collide.
func TestSpecCache_IsolatedKeys(t *testing.T) {
	c := &specCache{entries: make(map[string]*cachedSpec)}

	s1 := &agentgen.AgentSpec{Slug: "agent-one"}
	s2 := &agentgen.AgentSpec{Slug: "agent-two"}
	c.set(specCacheKey("tenant-1", "id-1"), s1)
	c.set(specCacheKey("tenant-1", "id-2"), s2)

	got1 := c.get(specCacheKey("tenant-1", "id-1"))
	got2 := c.get(specCacheKey("tenant-1", "id-2"))
	if got1 == nil || got1.Slug != "agent-one" {
		t.Errorf("id-1: want agent-one, got %v", got1)
	}
	if got2 == nil || got2.Slug != "agent-two" {
		t.Errorf("id-2: want agent-two, got %v", got2)
	}
}

// TestSpecCacheKey_TenantIsolation verifies that the same agentID under different
// tenants produces distinct cache keys, preventing cross-tenant spec poisoning.
func TestSpecCacheKey_TenantIsolation(t *testing.T) {
	const agentID = "00000000-0000-0000-0000-000000000001"
	k1 := specCacheKey("tenant-a", agentID)
	k2 := specCacheKey("tenant-b", agentID)
	if k1 == k2 {
		t.Errorf("expected distinct keys for different tenants, both got %q", k1)
	}
}

// TestExecuteSkill_InvocationIDFromTaskID verifies that executeSkill stamps
// ic.InvocationID from execCtx.TaskID before executing, so Temporal receives a
// stable workflow ID derived from the A2A task ID rather than a new UUID per call.
func TestExecuteSkill_InvocationIDFromTaskID(t *testing.T) {
	rt := &Runtime{
		interp: agentgen.NewInterpreter(
			&http.Client{},
			&stubLLMFactory{reply: "ok"},
			"",
		),
	}

	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "app1",
		AgentID:       "agent1",
		Spec: &agentgen.AgentSpec{
			Skills: []agentgen.SkillSpec{
				{
					ID:   "sk1",
					Name: "Skill One",
					Steps: []agentgen.StepSpec{
						{ID: "step-1", Type: agentgen.StepResponse, Config: []byte(`{}`)},
					},
				},
			},
		},
	}

	taskID := a2a.NewTaskID()
	execCtx := &a2asrv.ExecutorContext{
		TaskID:    taskID,
		ContextID: a2a.NewContextID(),
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
	}

	// Drain the iterator to trigger executeSkill.
	for range rt.executeSkill(context.Background(), ic, execCtx) {
	}

	if ic.InvocationID != string(taskID) {
		t.Errorf("InvocationID: want %q (TaskID), got %q", string(taskID), ic.InvocationID)
	}
}

// TestLoadBinding_SQLTenantScope verifies that both loadBinding query paths carry a
// tenant_id predicate and — critically — that the bindingID path also enforces
// application_id and agent_id so a cross-application/cross-agent binding lookup within
// the same tenant is rejected at the DB level.
func TestLoadBinding_SQLTenantScope(t *testing.T) {
	const expectedJoin = "JOIN them.applications a ON a.id = b.application_id"
	const expectedTenantParam4 = "a.tenant_id = $4::uuid"
	const expectedAppID = "b.application_id = $2::uuid"
	const expectedAgentID = "b.agent_id = $3::uuid"
	const expectedTenantNoID = "a.tenant_id = $3::uuid"

	// bindingID path — must enforce all 4 IDs: bindingID + appID + agentID + tenantID.
	// The old query only had b.id + a.tenant_id, which allowed cross-app/cross-agent
	// binding reads within the same tenant.
	queryWithID := `SELECT b.id, b.application_id, b.agent_id, b.definition_id,
		          b.credential_bindings, b.config_overrides, b.policies,
		          COALESCE(b.agent_params, '{}')
		          FROM them.app_agent_bindings b
		          JOIN them.applications a ON a.id = b.application_id
		          WHERE b.id = $1::uuid
		            AND b.application_id = $2::uuid
		            AND b.agent_id = $3::uuid
		            AND a.tenant_id = $4::uuid`
	if !strings.Contains(queryWithID, expectedJoin) {
		t.Errorf("bindingID path missing JOIN: %q", queryWithID)
	}
	if !strings.Contains(queryWithID, expectedAppID) {
		t.Errorf("bindingID path missing application_id predicate: %q", queryWithID)
	}
	if !strings.Contains(queryWithID, expectedAgentID) {
		t.Errorf("bindingID path missing agent_id predicate: %q", queryWithID)
	}
	if !strings.Contains(queryWithID, expectedTenantParam4) {
		t.Errorf("bindingID path missing a.tenant_id = $4::uuid predicate: %q", queryWithID)
	}

	// appID+agentID path (no bindingID) — unchanged; already has all 3 predicates.
	queryNoID := `SELECT b.id, b.application_id, b.agent_id, b.definition_id,
		          b.credential_bindings, b.config_overrides, b.policies,
		          COALESCE(b.agent_params, '{}')
		          FROM them.app_agent_bindings b
		          JOIN them.applications a ON a.id = b.application_id
		          WHERE b.application_id = $1::uuid AND b.agent_id = $2::uuid AND a.tenant_id = $3::uuid`
	if !strings.Contains(queryNoID, expectedJoin) {
		t.Errorf("no-bindingID path missing JOIN: %q", queryNoID)
	}
	if !strings.Contains(queryNoID, expectedTenantNoID) {
		t.Errorf("no-bindingID path missing a.tenant_id = $3::uuid: %q", queryNoID)
	}
}

// TestLoadBinding_CrossAgentRejection verifies that a binding that exists in the DB
// but belongs to a different agent (same tenant, same app) is rejected when the caller
// supplies the wrong agentID. This test runs against a live Postgres database and is
// gated by THEM_AGENT_RUNTIME_E2E=true.
func TestLoadBinding_CrossAgentRejection(t *testing.T) {
	if os.Getenv("THEM_AGENT_RUNTIME_E2E") != "true" {
		t.Skip("THEM_AGENT_RUNTIME_E2E not set — skipping live DB cross-agent rejection test")
	}
	dsn := buildTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	rt := &Runtime{pool: pool, logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}

	const (
		tenantID  = "00000000-0000-0000-0000-000000000001"
		appID     = "00000000-0000-0000-0000-000000000002"
		agentID   = "00000000-0000-0000-0000-000000000003"
		bindingID = "fa6ae508-412b-46e4-8da1-34441825c6c2" // real binding for agentID above
	)

	// Happy path: correct tenant + app + agent + binding → succeeds.
	_, _, err = rt.loadBinding(ctx, tenantID, appID, agentID, bindingID)
	if err != nil {
		t.Fatalf("correct 4-ID lookup failed: %v", err)
	}

	// Cross-agent: same binding UUID + same tenant + same app, but wrong agentID → rejected.
	wrongAgentID := "00000000-0000-0000-0000-000000000099"
	_, _, err = rt.loadBinding(ctx, tenantID, appID, wrongAgentID, bindingID)
	if err == nil {
		t.Fatal("cross-agent binding lookup must be rejected but was not")
	}
	t.Logf("cross-agent correctly rejected: %v", err)

	// Cross-application: same binding UUID + same tenant + wrong appID → rejected.
	wrongAppID := "00000000-0000-0000-0000-000000000098"
	_, _, err = rt.loadBinding(ctx, tenantID, wrongAppID, agentID, bindingID)
	if err == nil {
		t.Fatal("cross-application binding lookup must be rejected but was not")
	}
	t.Logf("cross-application correctly rejected: %v", err)
}

// TestLoadAppAPIKey_SQLTenantScope verifies that the provider_keys query includes tenant_id.
func TestLoadAppAPIKey_SQLTenantScope(t *testing.T) {
	const query = `SELECT COALESCE(provider_keys, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`
	if !strings.Contains(query, "tenant_id = $2::uuid") {
		t.Errorf("loadAppAPIKey query missing tenant_id predicate: %q", query)
	}
}

// TestLoadAppGlobalParams_SQLTenantScope verifies that the app_params query includes tenant_id.
func TestLoadAppGlobalParams_SQLTenantScope(t *testing.T) {
	const query = `SELECT COALESCE(app_params, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`
	if !strings.Contains(query, "tenant_id = $2::uuid") {
		t.Errorf("loadAppGlobalParams query missing tenant_id predicate: %q", query)
	}
}

// ── decodeAppGlobalParams unit tests (RT-20..22) ───────────────────────────────

// RT-20: secret entry with "plain:" prefix (test mode) → plaintext returned.
func TestDecodeAppGlobalParams_SecretPlainPrefix(t *testing.T) {
	raw := []byte(`{"my_key":{"ct":"plain:supersecret","hint":"cret"}}`)
	out := decodeAppGlobalParams(raw, nil, "app-1")
	if got := out["my_key"]; got != "supersecret" {
		t.Errorf("expected 'supersecret', got %q", got)
	}
}

// RT-21: non-secret plain string entry → returned verbatim.
func TestDecodeAppGlobalParams_PlainString(t *testing.T) {
	raw := []byte(`{"city":"Tel Aviv","score":"42"}`)
	out := decodeAppGlobalParams(raw, nil, "app-1")
	if got := out["city"]; got != "Tel Aviv" {
		t.Errorf("city: expected 'Tel Aviv', got %q", got)
	}
	if got := out["score"]; got != "42" {
		t.Errorf("score: expected '42', got %q", got)
	}
}

// RT-22: DB error (empty raw / bad JSON) → empty map returned, no panic.
func TestDecodeAppGlobalParams_BadJSON(t *testing.T) {
	out := decodeAppGlobalParams([]byte("not-json"), nil, "app-1")
	if out == nil {
		t.Fatal("expected non-nil empty map on bad JSON")
	}
	if len(out) != 0 {
		t.Errorf("expected empty map on bad JSON, got %v", out)
	}
}

// RT-23: empty JSONB '{}' → empty map with no error.
func TestDecodeAppGlobalParams_EmptyObject(t *testing.T) {
	out := decodeAppGlobalParams([]byte(`{}`), nil, "app-1")
	if len(out) != 0 {
		t.Errorf("expected empty map, got %v", out)
	}
}

// RT-24: mixed secret + plain in same blob → both decoded correctly.
func TestDecodeAppGlobalParams_MixedEntries(t *testing.T) {
	raw := []byte(`{"api_key":{"ct":"plain:my-api-key","hint":"_key"},"target":"prod"}`)
	out := decodeAppGlobalParams(raw, nil, "app-1")
	if got := out["api_key"]; got != "my-api-key" {
		t.Errorf("api_key: expected 'my-api-key', got %q", got)
	}
	if got := out["target"]; got != "prod" {
		t.Errorf("target: expected 'prod', got %q", got)
	}
}

// ── HITL async tests ──────────────────────────────────────────────────────────

// stubHITLRedis is a trivial in-memory TaskStoreRedis for hitlStore tests.
type stubHITLRedis struct {
	data map[string][]byte
}

func newStubHITLRedis() *stubHITLRedis {
	return &stubHITLRedis{data: make(map[string][]byte)}
}

func (m *stubHITLRedis) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := m.data[key]
	return v, ok, nil
}

func (m *stubHITLRedis) SetEX(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.data[key] = value
	return nil
}

func (m *stubHITLRedis) Del(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

// stubCanvasSubmitter captures the Submit call and returns a fixed SubmitResult.
type stubCanvasSubmitter struct {
	called bool
	result temporal.SubmitResult
	err    error
}

func (s *stubCanvasSubmitter) Submit(_ context.Context, _ *agentgen.InvocationContext, _ *agentgen.ExecutionPlan, _ agentgen.PipelineVars) (temporal.SubmitResult, error) {
	s.called = true
	return s.result, s.err
}

// stubCanvasSignaler captures SignalCanvasStep calls.
type stubCanvasSignaler struct {
	called     bool
	workflowID string
	runID      string
	signalName string
	payload    agentgen.PipelineVars
	err        error
}

func (s *stubCanvasSignaler) SignalCanvasStep(_ context.Context, workflowID, runID, signalName string, payload agentgen.PipelineVars) error {
	s.called = true
	s.workflowID = workflowID
	s.runID = runID
	s.signalName = signalName
	s.payload = payload
	return s.err
}

// makeHITLSpec builds an AgentSpec with a single human_wait skill.
func makeHITLSpec() *agentgen.AgentSpec {
	return &agentgen.AgentSpec{
		ExecutionBackend: "temporal",
		Skills: []agentgen.SkillSpec{
			{
				ID:   "hitl-skill",
				Name: "HITL Skill",
				Steps: []agentgen.StepSpec{
					{ID: "hw1", Type: agentgen.StepHumanWait, Config: []byte(`{"prompt":"approve?","reply_var":"approval"}`)},
				},
			},
		},
	}
}

// RT-HITL-1: executeSkill for a HITL Temporal plan returns TaskStateWorking
// immediately without blocking on workflow completion.
// InputRequired is emitted later by HITLRequestHandler.SubscribeToTask when the
// workflow query reports state==waiting.
func TestExecuteSkill_HITL_ReturnsWorking(t *testing.T) {
	submitter := &stubCanvasSubmitter{result: temporal.SubmitResult{WorkflowID: "wf-1", RunID: "run-1"}}
	stubRedis := newStubHITLRedis()
	rt := &Runtime{
		interp:          agentgen.NewInterpreter(&http.Client{}, &stubLLMFactory{}, ""),
		hitlStore:       agentgen.NewHITLStore(stubRedis),
		canvasSubmitter: submitter,
	}

	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "app1",
		AgentID:       "agent1",
		Spec:          makeHITLSpec(),
	}
	execCtx := &a2asrv.ExecutorContext{
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("please approve")),
	}

	var events []a2a.Event
	for evt, err := range rt.executeSkill(context.Background(), ic, execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, evt)
	}

	if !submitter.called {
		t.Error("Submit must be called for HITL plan")
	}

	// Last event must be Working (async — not yet at human_wait node).
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	su, ok := last.(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Fatalf("last event must be *a2a.TaskStatusUpdateEvent, got %T", last)
	}
	if su.Status.State != a2a.TaskStateWorking {
		t.Errorf("last state: want working, got %v", su.Status.State)
	}
}

// RT-HITL-2: executeSkill stores the workflow handle in HITLStore after Submit.
func TestExecuteSkill_HITL_StoresHandle(t *testing.T) {
	submitter := &stubCanvasSubmitter{result: temporal.SubmitResult{WorkflowID: "wf-store", RunID: "run-store"}}
	stubRedis := newStubHITLRedis()
	rt := &Runtime{
		interp:          agentgen.NewInterpreter(&http.Client{}, &stubLLMFactory{}, ""),
		hitlStore:       agentgen.NewHITLStore(stubRedis),
		canvasSubmitter: submitter,
	}
	ic := &agentgen.InvocationContext{
		Spec: makeHITLSpec(),
	}
	taskID := a2a.NewTaskID()
	execCtx := &a2asrv.ExecutorContext{
		TaskID:    taskID,
		ContextID: a2a.NewContextID(),
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
	}

	for range rt.executeSkill(context.Background(), ic, execCtx) {
	}

	h, err := rt.hitlStore.Get(context.Background(), string(taskID))
	if err != nil {
		t.Fatalf("hitlStore.Get: %v", err)
	}
	if h.WorkflowID != "wf-store" {
		t.Errorf("WorkflowID: want wf-store, got %q", h.WorkflowID)
	}
	if h.StepID != "hw1" {
		t.Errorf("StepID: want hw1 (first human_wait node), got %q", h.StepID)
	}
}

// stubCanvasCanceler records CancelWorkflow calls.
type stubCanvasCanceler struct {
	called     bool
	workflowID string
	runID      string
	err        error
}

func (s *stubCanvasCanceler) CancelWorkflow(_ context.Context, workflowID, runID string) error {
	s.called = true
	s.workflowID = workflowID
	s.runID = runID
	return s.err
}

// stubCanvasAwaiter records AwaitResult calls and returns a fixed result.
type stubCanvasAwaiter struct {
	called bool
	result *agentgen.ExecutionResult
	err    error
}

func (s *stubCanvasAwaiter) AwaitResult(_ context.Context, _, _ string) (*agentgen.ExecutionResult, error) {
	s.called = true
	return s.result, s.err
}

// stubCanvasHITLQuerier records QueryHITLStatus calls.
type stubCanvasHITLQuerier struct {
	status temporal.HITLQueryStatus
	err    error
}

func (s *stubCanvasHITLQuerier) QueryHITLStatus(_ context.Context, _, _ string) (temporal.HITLQueryStatus, error) {
	return s.status, s.err
}

// stubSDKHandler is a minimal a2asrv.RequestHandler that records delegated calls.
type stubSDKHandler struct {
	cancelTaskCalled bool
	getTaskCalled    bool
	subscribeCalled  bool
}

func (s *stubSDKHandler) GetTask(_ context.Context, _ *a2a.GetTaskRequest) (*a2a.Task, error) {
	s.getTaskCalled = true
	return &a2a.Task{}, nil
}
func (s *stubSDKHandler) ListTasks(_ context.Context, _ *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return &a2a.ListTasksResponse{Tasks: []*a2a.Task{}}, nil
}
func (s *stubSDKHandler) CancelTask(_ context.Context, _ *a2a.CancelTaskRequest) (*a2a.Task, error) {
	s.cancelTaskCalled = true
	return &a2a.Task{}, nil
}
func (s *stubSDKHandler) SendMessage(_ context.Context, _ *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	return nil, nil
}
func (s *stubSDKHandler) SubscribeToTask(_ context.Context, _ *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	s.subscribeCalled = true
	return func(yield func(a2a.Event, error) bool) {}
}
func (s *stubSDKHandler) SendStreamingMessage(_ context.Context, _ *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {}
}
func (s *stubSDKHandler) GetTaskPushConfig(_ context.Context, _ *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return nil, nil
}
func (s *stubSDKHandler) ListTaskPushConfigs(_ context.Context, _ *a2a.ListTaskPushConfigRequest) (*a2a.ListTaskPushConfigResponse, error) {
	return &a2a.ListTaskPushConfigResponse{}, nil
}
func (s *stubSDKHandler) CreateTaskPushConfig(_ context.Context, cfg *a2a.PushConfig) (*a2a.PushConfig, error) {
	return cfg, nil
}
func (s *stubSDKHandler) DeleteTaskPushConfig(_ context.Context, _ *a2a.DeleteTaskPushConfigRequest) error {
	return nil
}
func (s *stubSDKHandler) GetExtendedAgentCard(_ context.Context, _ *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return nil, nil
}

// RT-HITL-3 (renamed): HITLRequestHandler.CancelTask cancels the workflow and marks done.
func TestHITLRequestHandler_CancelTask_CancelsWorkflow(t *testing.T) {
	store := agentgen.NewHITLStore(newStubHITLRedis())
	taskID := a2a.TaskID("task-cancel-1")
	_ = store.Store(context.Background(), string(taskID), "wf-cancel", "run-cancel", "t1", "hw1")

	canceler := &stubCanvasCanceler{}
	inner := &stubSDKHandler{}
	h := &HITLRequestHandler{
		inner:     inner,
		hitlStore: store,
		canceler:  canceler,
		logger:    slog.Default(),
	}

	_, err := h.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: taskID})
	if err != nil {
		t.Fatalf("CancelTask: unexpected error: %v", err)
	}
	if !canceler.called {
		t.Error("CancelWorkflow must be called for HITL task")
	}
	if canceler.workflowID != "wf-cancel" {
		t.Errorf("workflowID: want wf-cancel, got %q", canceler.workflowID)
	}
	if !inner.cancelTaskCalled {
		t.Error("inner handler must be called")
	}
	// Handle must be marked done (removed from store).
	if _, err := store.Get(context.Background(), string(taskID)); err == nil {
		t.Error("handle must be removed after CancelTask")
	}
}

// RT-HITL-4 (renamed): HITLRequestHandler.CancelTask for a non-HITL task delegates without cancelling Temporal.
func TestHITLRequestHandler_CancelTask_NonHITL_Delegates(t *testing.T) {
	canceler := &stubCanvasCanceler{}
	inner := &stubSDKHandler{}
	h := &HITLRequestHandler{
		inner:     inner,
		hitlStore: agentgen.NewHITLStore(newStubHITLRedis()), // empty store
		canceler:  canceler,
		logger:    slog.Default(),
	}

	_, _ = h.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: "no-such-task"})
	if canceler.called {
		t.Error("CancelWorkflow must NOT be called for non-HITL task")
	}
	if !inner.cancelTaskCalled {
		t.Error("inner handler must be called")
	}
}

// RT-HITL-5 (renamed): HITLRequestHandler.SubscribeToTask for a non-HITL task delegates to inner handler.
func TestHITLRequestHandler_SubscribeToTask_NonHITL_Delegates(t *testing.T) {
	querier := &stubCanvasHITLQuerier{}
	inner := &stubSDKHandler{}
	h := &HITLRequestHandler{
		inner:     inner,
		hitlStore: agentgen.NewHITLStore(newStubHITLRedis()), // empty store
		querier:   querier,
		logger:    slog.Default(),
	}

	seq := h.SubscribeToTask(context.Background(), &a2a.SubscribeToTaskRequest{ID: "no-hitl-task"})
	for range seq {
	}
	if !inner.subscribeCalled {
		t.Error("inner SubscribeToTask must be called for non-HITL task")
	}
}
