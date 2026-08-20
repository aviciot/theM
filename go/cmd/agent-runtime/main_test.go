package main

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/aviciot/them/internal/agentgen"
)

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

// TestSpecCache_IsolatedKeys verifies that different keys don't collide.
func TestSpecCache_IsolatedKeys(t *testing.T) {
	c := &specCache{entries: make(map[string]*cachedSpec)}

	s1 := &agentgen.AgentSpec{Slug: "agent-one"}
	s2 := &agentgen.AgentSpec{Slug: "agent-two"}
	c.set("id-1", s1)
	c.set("id-2", s2)

	got1 := c.get("id-1")
	got2 := c.get("id-2")
	if got1 == nil || got1.Slug != "agent-one" {
		t.Errorf("id-1: want agent-one, got %v", got1)
	}
	if got2 == nil || got2.Slug != "agent-two" {
		t.Errorf("id-2: want agent-two, got %v", got2)
	}
}
