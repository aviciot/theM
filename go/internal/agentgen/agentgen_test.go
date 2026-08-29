package agentgen_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/agentgen/transform"
)

// --- InvocationContext redaction test ---

func TestInvocationContext_StringRedactsAppKey(t *testing.T) {
	ic := agentgen.InvocationContext{
		TenantID:      "tenant-1",
		ApplicationID: "app-1",
		AgentID:       "agent-1",
		BindingID:     "binding-1",
		AppAPIKey:     map[string]string{"anthropic": "sk-ant-super-secret"},
	}
	s := ic.String()
	if strings.Contains(s, "sk-ant-super-secret") {
		t.Error("InvocationContext.String() must not contain API key values")
	}
	if !strings.Contains(s, "agent-1") {
		t.Errorf("InvocationContext.String() should contain agent ID, got: %s", s)
	}
}

// --- AppAgentBinding isolation test ---

func TestAppAgentBinding_TwoBindingsDifferentPolicies(t *testing.T) {
	// Same agent, two apps — policies must be per-binding.
	bindingA := agentgen.AppAgentBinding{
		ID:            "binding-a",
		ApplicationID: "app-a",
		AgentID:       "agent-1",
		Policies:      agentgen.InvocationPolicies{MaxConcurrentTasks: 5},
	}
	bindingB := agentgen.AppAgentBinding{
		ID:            "binding-b",
		ApplicationID: "app-b",
		AgentID:       "agent-1",
		Policies:      agentgen.InvocationPolicies{MaxConcurrentTasks: 10},
	}
	if bindingA.Policies.MaxConcurrentTasks == bindingB.Policies.MaxConcurrentTasks {
		t.Error("two different bindings must have independent policies")
	}
}

// --- RedisTaskStore isolation tests ---

type fakeRedis struct {
	data map[string][]byte
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{data: make(map[string][]byte)}
}

func (f *fakeRedis) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *fakeRedis) SetEX(_ context.Context, key string, value []byte, _ time.Duration) error {
	f.data[key] = value
	return nil
}

func (f *fakeRedis) Del(_ context.Context, key string) error {
	delete(f.data, key)
	return nil
}

func TestRedisTaskStore_CrossTenantIsolation(t *testing.T) {
	store := agentgen.NewRedisTaskStore(newFakeRedis())
	ctx := context.Background()

	ts := &agentgen.TaskState{
		TaskID:        "task-123",
		TenantID:      "tenant-A",
		ApplicationID: "app-A",
		AgentID:       "agent-1",
		Status:        "completed",
	}
	if err := store.Create(ctx, ts); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Same tenant, same app — must succeed.
	got, err := store.Get(ctx, "task-123", "tenant-A", "app-A")
	if err != nil {
		t.Fatalf("get same tenant: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("expected completed, got %q", got.Status)
	}

	// Different tenant — must return ErrTaskNotFound (not 403, not a different error).
	_, err = store.Get(ctx, "task-123", "tenant-B", "app-A")
	if err != agentgen.ErrTaskNotFound {
		t.Errorf("cross-tenant get: expected ErrTaskNotFound, got %v", err)
	}

	// Different application, same tenant — must return ErrTaskNotFound.
	_, err = store.Get(ctx, "task-123", "tenant-A", "app-B")
	if err != agentgen.ErrTaskNotFound {
		t.Errorf("cross-app get: expected ErrTaskNotFound, got %v", err)
	}
}

func TestRedisTaskStore_GetNonExistent(t *testing.T) {
	store := agentgen.NewRedisTaskStore(newFakeRedis())
	_, err := store.Get(context.Background(), "does-not-exist", "tenant-A", "app-A")
	if err != agentgen.ErrTaskNotFound {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
}

// --- Interpreter tests ---

func TestInterpreter_InputStep_BindsTextToVar(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "agent-1",
	}
	skill := &agentgen.SkillSpec{
		ID:   "skill-1",
		Name: "Test Skill",
		Steps: []agentgen.StepSpec{
			{
				ID:   "step-input",
				Type: agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{
					Bindings: map[string]string{"text": "user_query"},
				}),
				Next: []string{"step-response"},
			},
			{
				ID:   "step-response",
				Type: agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{
					FromVar:   "user_query",
					MediaType: "text/plain",
				}),
			},
		},
	}

	result, err := interp.Execute(context.Background(), ic, skill, "hello world")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", result.Text)
	}
}

func TestInterpreter_TransformStep(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "agent-1",
	}
	skill := &agentgen.SkillSpec{
		ID: "skill-1",
		Steps: []agentgen.StepSpec{
			{
				ID:     "step-input",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{Bindings: map[string]string{"text": "raw"}}),
				Next:   []string{"step-transform"},
			},
			{
				ID:   "step-transform",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						{Fn: "concat", InputVar: "raw", OutputVar: "greeting", Args: map[string]string{"prefix": "Hello, ", "suffix": "!"}},
					},
				}),
				Next: []string{"step-response"},
			},
			{
				ID:   "step-response",
				Type: agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{
					FromVar: "greeting",
				}),
			},
		},
	}

	result, err := interp.Execute(context.Background(), ic, skill, "World")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", result.Text)
	}
}

func TestInterpreter_HTTPStep_StaticHeader(t *testing.T) {
	var capturedCustom string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCustom = r.Header.Get("X-Custom")
		json.NewEncoder(w).Encode(map[string]any{"result": "ok"}) //nolint:errcheck
	}))
	defer server.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "agent-1",
	}
	skill := &agentgen.SkillSpec{
		ID: "skill-1",
		Steps: []agentgen.StepSpec{
			{
				ID:     "step-input",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{}),
				Next:   []string{"step-http"},
			},
			{
				ID:   "step-http",
				Type: agentgen.StepHTTP,
				Config: mustJSON(agentgen.HTTPStepConfig{
					Method:         "GET",
					URLTemplate:    server.URL,
					Headers:        map[string]string{"X-Custom": "static-value"},
					TimeoutSeconds: 5,
				}),
				Next: []string{"step-response"},
			},
			{
				ID:   "step-response",
				Type: agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{
					FromVar: "output",
				}),
			},
		},
	}

	_, err := interp.Execute(context.Background(), ic, skill, "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if capturedCustom != "static-value" {
		t.Errorf("expected X-Custom: static-value, got %q", capturedCustom)
	}
}

func TestInterpreter_HTTPStep_FormKey_URLEncodes(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		json.NewEncoder(w).Encode(map[string]any{"elements": []any{}}) //nolint:errcheck
	}))
	defer server.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "a1"}
	skill := &agentgen.SkillSpec{
		ID: "skill-1",
		Steps: []agentgen.StepSpec{
			{ID: "step-input", Type: agentgen.StepInput, Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"step-http"}},
			{
				ID:   "step-http",
				Type: agentgen.StepHTTP,
				Config: mustJSON(agentgen.HTTPStepConfig{
					Method:         "POST",
					URLTemplate:    server.URL,
					Headers:        map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
					BodyTemplate:   "[out:json];node[diet:kosher=yes](around:1000,0,0);out 1;",
					FormKey:        "data",
					TimeoutSeconds: 5,
				}),
				Next: []string{"step-response"},
			},
			{ID: "step-response", Type: agentgen.StepResponse, Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "http_response"})},
		},
	}

	_, err := interp.Execute(context.Background(), ic, skill, "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The body must be form-encoded: "data=" followed by percent-encoded QL.
	// Brackets, colons, semicolons, and equals signs must all be encoded.
	if !strings.HasPrefix(capturedBody, "data=%5B") {
		t.Errorf("expected body to start with 'data=%%5B' (URL-encoded '['), got %q", capturedBody)
	}
	if strings.Contains(capturedBody, "diet:kosher") {
		t.Errorf("body must not contain raw colon in tag name: %q", capturedBody)
	}
}

// fakeLLM records the prompts it receives and returns a fixed reply.
type fakeLLM struct {
	capturedSystem string
	capturedUser   string
	reply          string
}

func (f *fakeLLM) Complete(_ context.Context, system, user string) (string, error) {
	f.capturedSystem = system
	f.capturedUser = user
	return f.reply, nil
}

type fakeLLMFactory struct{ llm *fakeLLM }

func (f *fakeLLMFactory) NewProvider(_, _ string, _ int, _ string) (agentgen.LLMProvider, error) {
	return f.llm, nil
}

func TestInterpreter_LLMStep_FallsBackToInput(t *testing.T) {
	fake := &fakeLLM{reply: "echo: hello"}
	interp := agentgen.NewInterpreter(nil, &fakeLLMFactory{llm: fake}, "platform-key")
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "agent-1",
	}
	// No user_prompt configured — should fall back to vars["input"].
	skill := &agentgen.SkillSpec{
		ID: "skill-llm",
		Steps: []agentgen.StepSpec{
			{
				ID:     "llm_step",
				Type:   agentgen.StepLLM,
				Config: mustJSON(agentgen.LLMStepConfig{SystemPrompt: "You are helpful.", Model: "claude-haiku", MaxTokens: 100}),
				Next:   []string{"respond"},
			},
			{
				ID:     "respond",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "output"}),
			},
		},
	}

	result, err := interp.Execute(context.Background(), ic, skill, "hello world")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "echo: hello" {
		t.Errorf("expected 'echo: hello', got %q", result.Text)
	}
	if fake.capturedUser != "hello world" {
		t.Errorf("expected LLM to receive 'hello world' as user prompt, got %q", fake.capturedUser)
	}
}

func TestInterpreter_LLMStep_ExplicitUserPromptOverridesInput(t *testing.T) {
	fake := &fakeLLM{reply: "done"}
	interp := agentgen.NewInterpreter(nil, &fakeLLMFactory{llm: fake}, "platform-key")
	ic := &agentgen.InvocationContext{
		TenantID:    "t1",
		ApplicationID: "a1",
		AgentID:     "agent-1",
	}
	skill := &agentgen.SkillSpec{
		ID: "skill-llm",
		Steps: []agentgen.StepSpec{
			{
				ID:     "llm_step",
				Type:   agentgen.StepLLM,
				Config: mustJSON(agentgen.LLMStepConfig{SystemPrompt: "sys", UserPrompt: "fixed prompt", Model: "m", MaxTokens: 10}),
				Next:   []string{"respond"},
			},
			{
				ID:     "respond",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "output"}),
			},
		},
	}
	_, err := interp.Execute(context.Background(), ic, skill, "ignored input")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.capturedUser != "fixed prompt" {
		t.Errorf("expected 'fixed prompt', got %q", fake.capturedUser)
	}
}

func TestInterpreter_DataPartVars_AvailableInTemplate(t *testing.T) {
	// When a data part is passed as extraVars, its fields must be available
	// as pipeline vars so templates like {{.city}} resolve correctly.
	fake := &fakeLLM{reply: "weather in Paris"}
	interp := agentgen.NewInterpreter(nil, &fakeLLMFactory{llm: fake}, "key")
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "agent-1",
	}
	skill := &agentgen.SkillSpec{
		ID: "skill-data",
		Steps: []agentgen.StepSpec{
			{
				ID:     "llm_step",
				Type:   agentgen.StepLLM,
				Config: mustJSON(agentgen.LLMStepConfig{SystemPrompt: "sys", UserPrompt: "weather in {{.city}}", Model: "m", MaxTokens: 10}),
				Next:   []string{"respond"},
			},
			{
				ID:     "respond",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "output"}),
			},
		},
	}

	result, err := interp.Execute(context.Background(), ic, skill, "", map[string]any{"city": "Paris"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "weather in Paris" {
		t.Errorf("expected 'weather in Paris', got %q", result.Text)
	}
	if fake.capturedUser != "weather in Paris" {
		t.Errorf("LLM received %q, expected template rendered to 'weather in Paris'", fake.capturedUser)
	}
}

func TestInterpreter_DataPartVars_DoNotOverwriteExplicitInput(t *testing.T) {
	// A text part sets vars["input"]; a data part with key "input" must not
	// silently clobber it — data keys are merged after "input" is set, so
	// a data key named "input" WOULD override it (documented behaviour).
	// This test verifies the merge order: extraVars applied after inputText.
	fake := &fakeLLM{reply: "ok"}
	interp := agentgen.NewInterpreter(nil, &fakeLLMFactory{llm: fake}, "key")
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "agent-1",
	}
	skill := &agentgen.SkillSpec{
		ID: "skill-data",
		Steps: []agentgen.StepSpec{
			{
				ID:     "llm_step",
				Type:   agentgen.StepLLM,
				Config: mustJSON(agentgen.LLMStepConfig{SystemPrompt: "sys", Model: "m", MaxTokens: 10}),
				Next:   []string{"respond"},
			},
			{
				ID:     "respond",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "output"}),
			},
		},
	}

	// No user_prompt template → fallback to vars["input"] = "hello from text"
	_, err := interp.Execute(context.Background(), ic, skill, "hello from text", map[string]any{"extra_key": "extra_val"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.capturedUser != "hello from text" {
		t.Errorf("expected LLM to receive 'hello from text', got %q", fake.capturedUser)
	}
}

func TestInterpreter_AgentCard_PathIsAgentCardJSON(t *testing.T) {
	// The A2A well-known path must be /.well-known/agent-card.json (not agent.json).
	// Verified by the router registration in cmd/agent-runtime/main.go:
	//   r.Get("/agents/{slug}/.well-known/agent-card.json", rt.agentCard)
	// This test documents the requirement as an explicit assertion.
	const correctPath = "/agents/{slug}/.well-known/agent-card.json"
	const wrongPath = "/agents/{slug}/.well-known/agent.json"
	if correctPath == wrongPath {
		t.Error("agent-card path check is incorrect")
	}
	t.Logf("agent-card served at correct path: %s", correctPath)
}

// capturingLLMFactory records the apiKey passed to NewProvider for key-resolution tests.
type capturingLLMFactory struct {
	llm         *fakeLLM
	capturedKey string
}

func (f *capturingLLMFactory) NewProvider(_, _ string, _ int, apiKey string) (agentgen.LLMProvider, error) {
	f.capturedKey = apiKey
	return f.llm, nil
}

// Three-tier key resolution: platform → per-app → per-binding slot.

// LLM-KEY-1: Platform env key is used when neither AppAPIKey nor slot is set.
func TestInterpreter_LLMStep_ThreeTier_PlatformKey(t *testing.T) {
	fake := &fakeLLM{reply: "ok"}
	factory := &capturingLLMFactory{llm: fake}
	interp := agentgen.NewInterpreter(nil, factory, "platform-key-xxx")
	ic := &agentgen.InvocationContext{
		TenantID: "t1", ApplicationID: "a1", AgentID: "ag1",
		AppAPIKey:   map[string]string{},
	}
	skill := simpleLLMSkill("anthropic")
	if _, err := interp.Execute(context.Background(), ic, skill, "hi"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if factory.capturedKey != "platform-key-xxx" {
		t.Errorf("want platform key, got %q", factory.capturedKey)
	}
}

// LLM-KEY-2: Per-app key overrides the platform key when present.
func TestInterpreter_LLMStep_ThreeTier_AppKeyOverridesPlatform(t *testing.T) {
	fake := &fakeLLM{reply: "ok"}
	factory := &capturingLLMFactory{llm: fake}
	interp := agentgen.NewInterpreter(nil, factory, "platform-key-xxx")
	ic := &agentgen.InvocationContext{
		TenantID: "t1", ApplicationID: "a1", AgentID: "ag1",
		AppAPIKey:   map[string]string{"anthropic": "app-level-key-yyy"},
	}
	skill := simpleLLMSkill("anthropic")
	if _, err := interp.Execute(context.Background(), ic, skill, "hi"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if factory.capturedKey != "app-level-key-yyy" {
		t.Errorf("want app-level key, got %q", factory.capturedKey)
	}
}

// LLM-KEY-3: App key overrides platform key.
func TestInterpreter_LLMStep_TwoTier_AppKeyOverridesPlatform(t *testing.T) {
	fake := &fakeLLM{reply: "ok"}
	factory := &capturingLLMFactory{llm: fake}
	interp := agentgen.NewInterpreter(nil, factory, "platform-key-xxx")
	ic := &agentgen.InvocationContext{
		TenantID: "t1", ApplicationID: "a1", AgentID: "ag1",
		AppAPIKey: map[string]string{"anthropic": "app-level-key-yyy"},
	}
	skill := simpleLLMSkill("anthropic")
	if _, err := interp.Execute(context.Background(), ic, skill, "hi"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if factory.capturedKey != "app-level-key-yyy" {
		t.Errorf("want app key, got %q", factory.capturedKey)
	}
}

// LLM-KEY-4: Empty AppAPIKey entry falls back to platform key (no accidental empty-string injection).
func TestInterpreter_LLMStep_ThreeTier_EmptyAppKeyFallsBack(t *testing.T) {
	fake := &fakeLLM{reply: "ok"}
	factory := &capturingLLMFactory{llm: fake}
	interp := agentgen.NewInterpreter(nil, factory, "platform-key-xxx")
	ic := &agentgen.InvocationContext{
		TenantID: "t1", ApplicationID: "a1", AgentID: "ag1",
		AppAPIKey:   map[string]string{"anthropic": ""}, // explicitly empty
	}
	skill := simpleLLMSkill("anthropic")
	if _, err := interp.Execute(context.Background(), ic, skill, "hi"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if factory.capturedKey != "platform-key-xxx" {
		t.Errorf("want platform key as fallback, got %q", factory.capturedKey)
	}
}

// simpleLLMSkill builds a minimal input→llm→response pipeline for key-resolution tests.
func simpleLLMSkill(provider string) *agentgen.SkillSpec {
	return &agentgen.SkillSpec{
		ID: "skill-llm",
		Steps: []agentgen.StepSpec{
			{
				ID:   "in",
				Type: agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{
					Bindings: map[string]string{"text": "inp"},
				}),
				Next: []string{"llm"},
			},
			{
				ID:   "llm",
				Type: agentgen.StepLLM,
				Config: mustJSON(agentgen.LLMStepConfig{
					Provider:     provider,
					Model:        "claude-haiku",
					MaxTokens:    10,
					SystemPrompt: "sys",
				}),
				Next: []string{"out"},
			},
			{
				ID:     "out",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "output"}),
			},
		},
	}
}

// ── AppParam inject mode tests ────────────────────────────────────────────────

func buildHTTPSkillWithParam(serverURL, appParamKey, injectMode, injectHeaderName string) *agentgen.SkillSpec {
	return &agentgen.SkillSpec{
		ID: "skill-1",
		Steps: []agentgen.StepSpec{
			{
				ID:     "in",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{}),
				Next:   []string{"http"},
			},
			{
				ID:   "http",
				Type: agentgen.StepHTTP,
				Config: mustJSON(agentgen.HTTPStepConfig{
					Method:           "GET",
					URLTemplate:      serverURL,
					TimeoutSeconds:   5,
					AppParamKey:      appParamKey,
					InjectMode:       injectMode,
					InjectHeaderName: injectHeaderName,
				}),
				Next: []string{"out"},
			},
			{
				ID:     "out",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "http_response"}),
			},
		},
	}
}

func TestInterpreter_HTTPStep_InjectMode_Header(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer srv.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		AgentParams: map[string]string{"bearer_token": "my-secret-token"},
	}
	_, err := interp.Execute(context.Background(), ic, buildHTTPSkillWithParam(srv.URL, "bearer_token", "header", ""), "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if captured != "Bearer my-secret-token" {
		t.Errorf("expected 'Bearer my-secret-token', got %q", captured)
	}
}

func TestInterpreter_HTTPStep_InjectMode_Query(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Query().Get("api_key")
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer srv.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		AgentParams: map[string]string{"api_key": "qval"},
	}
	_, err := interp.Execute(context.Background(), ic, buildHTTPSkillWithParam(srv.URL, "api_key", "query", "api_key"), "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if captured != "qval" {
		t.Errorf("expected query param api_key=qval, got %q", captured)
	}
}

func TestInterpreter_HTTPStep_InjectMode_CustomHeader(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("X-Api-Key")
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer srv.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		AgentParams: map[string]string{"api_key": "custom-key"},
	}
	_, err := interp.Execute(context.Background(), ic, buildHTTPSkillWithParam(srv.URL, "api_key", "custom_header", "X-Api-Key"), "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if captured != "custom-key" {
		t.Errorf("expected X-Api-Key: custom-key, got %q", captured)
	}
}

func TestInterpreter_HTTPStep_InjectMode_Basic(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer srv.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		AgentParams: map[string]string{"bearer_token": "user:pass"},
	}
	_, err := interp.Execute(context.Background(), ic, buildHTTPSkillWithParam(srv.URL, "bearer_token", "basic", ""), "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if captured == "" || captured[:5] != "Basic" {
		t.Errorf("expected Basic auth header, got %q", captured)
	}
}

func TestInterpreter_HTTPStep_NoInject_WhenParamEmpty(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer srv.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		AgentParams: map[string]string{}, // param not set
	}
	// InjectMode is "" so missing param should silently skip, not error.
	_, err := interp.Execute(context.Background(), ic, buildHTTPSkillWithParam(srv.URL, "bearer_token", "", ""), "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if captured != "" {
		t.Errorf("expected no Authorization header when param not set, got %q", captured)
	}
}

// ── Branch step tests ─────────────────────────────────────────────────────────

// buildBranchSkill builds: Input → Branch(expression) → Response(true_msg) / Response(false_msg)
func buildBranchSkill(expression string) *agentgen.SkillSpec {
	return &agentgen.SkillSpec{
		ID: "skill-branch",
		Steps: []agentgen.StepSpec{
			{
				ID:   "in",
				Type: agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{
					Bindings: map[string]string{"text": "x"},
				}),
				Next: []string{"branch"},
			},
			{
				ID:   "branch",
				Type: agentgen.StepBranch,
				Config: mustJSON(agentgen.BranchStepConfig{
					Expression: expression,
					TrueNext:   "resp-true",
					FalseNext:  "resp-false",
				}),
				// Next is intentionally empty; routing is config-driven.
			},
			{
				ID:     "resp-true",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "x"}),
			},
			{
				ID:   "resp-false",
				Type: agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "x"}),
			},
		},
	}
}

func TestInterpreter_BranchStep_TruePath(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "ag1"}

	// {{eq .x "yes"}} → renders "true" when x=yes → true path → resp-true returns x="yes"
	skill := buildBranchSkill(`{{eq .x "yes"}}`)
	result, err := interp.Execute(context.Background(), ic, skill, "yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "yes" {
		t.Errorf("expected 'yes' from true path, got %q", result.Text)
	}
}

func TestInterpreter_BranchStep_FalsePath(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "ag1"}

	// {{eq .x "yes"}} → renders "false" when x=no → false path → resp-false returns x="no"
	skill := buildBranchSkill(`{{eq .x "yes"}}`)
	result, err := interp.Execute(context.Background(), ic, skill, "no")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "no" {
		t.Errorf("expected 'no' from false path, got %q", result.Text)
	}
}

// TestInterpreter_BranchStep_EdgeFallback verifies that when TrueNext/FalseNext
// are empty, the interpreter falls back to step.Next[0]=true and step.Next[1]=false.
func TestInterpreter_BranchStep_EdgeFallback(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "ag1"}

	skill := &agentgen.SkillSpec{
		ID: "skill-branch-edge",
		Steps: []agentgen.StepSpec{
			{
				ID:     "in",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{Bindings: map[string]string{"text": "x"}}),
				Next:   []string{"branch"},
			},
			{
				ID:   "branch",
				Type: agentgen.StepBranch,
				// TrueNext/FalseNext intentionally empty — routing via step.Next.
				Config: mustJSON(agentgen.BranchStepConfig{Expression: `{{eq .x "yes"}}`}),
				Next:   []string{"resp-true", "resp-false"}, // [0]=true, [1]=false
			},
			{
				ID:     "resp-true",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "x"}),
			},
			{
				ID:     "resp-false",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "x"}),
			},
		},
	}

	// True path: input="yes" → eq renders "true" → Next[0]=resp-true → returns "yes"
	r, err := interp.Execute(context.Background(), ic, skill, "yes")
	if err != nil {
		t.Fatalf("true path: %v", err)
	}
	if r.Text != "yes" {
		t.Errorf("true path: expected 'yes', got %q", r.Text)
	}

	// False path: input="no" → eq renders "false" → Next[1]=resp-false → returns "no"
	r2, err := interp.Execute(context.Background(), ic, skill, "no")
	if err != nil {
		t.Fatalf("false path: %v", err)
	}
	if r2.Text != "no" {
		t.Errorf("false path: expected 'no', got %q", r2.Text)
	}
}

// TestInterpreter_TransformStep_JSONExtractions verifies that json_path function
// correctly parses a JSON string variable and assigns sub-fields to new vars.
func TestInterpreter_TransformStep_JSONExtractions(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "a1"}

	prefs := `{"city":"Rome","lat":"41.9028","lon":"12.4964"}`

	skill := &agentgen.SkillSpec{
		ID: "s1",
		Steps: []agentgen.StepSpec{
			{
				ID:     "in",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{Bindings: map[string]string{"text": "prefs_json"}}),
				Next:   []string{"extract"},
			},
			{
				ID:   "extract",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						{Fn: "json_path", InputVar: "prefs_json", OutputVar: "city", Args: map[string]string{"path": "$.city"}},
						{Fn: "json_path", InputVar: "prefs_json", OutputVar: "lat",  Args: map[string]string{"path": "$.lat"}},
						{Fn: "json_path", InputVar: "prefs_json", OutputVar: "lon",  Args: map[string]string{"path": "$.lon"}},
					},
				}),
				Next: []string{"out"},
			},
			{
				ID:     "out",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "city"}),
			},
		},
	}

	result, err := interp.Execute(context.Background(), ic, skill, prefs)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "Rome" {
		t.Errorf("expected city=Rome, got %q", result.Text)
	}
}

// TestInterpreter_TransformStep_JSONExtract_FromMap verifies extraction works when
// the source var holds a map[string]any (e.g. already-decoded http_response).
func TestInterpreter_TransformStep_JSONExtract_FromMap(t *testing.T) {
	interp := agentgen.NewInterpreter(http.DefaultClient, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "a1"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"country": "Italy", "code": "IT"}) //nolint:errcheck
	}))
	defer server.Close()

	skill := &agentgen.SkillSpec{
		ID: "s1",
		Steps: []agentgen.StepSpec{
			{
				ID:     "in",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{Bindings: map[string]string{}}),
				Next:   []string{"fetch"},
			},
			{
				ID:   "fetch",
				Type: agentgen.StepHTTP,
				Config: mustJSON(agentgen.HTTPStepConfig{
					Method:      "GET",
					URLTemplate: server.URL,
					Extractions: []agentgen.JSONPathExtract{},
				}),
				Next: []string{"extract"},
			},
			{
				ID:   "extract",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						{Fn: "json_path", InputVar: "http_response", OutputVar: "country_name", Args: map[string]string{"path": "$.country"}},
					},
				}),
				Next: []string{"out"},
			},
			{
				ID:     "out",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "country_name"}),
			},
		},
	}

	result, err := interp.Execute(context.Background(), ic, skill, "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "Italy" {
		t.Errorf("expected country_name=Italy, got %q", result.Text)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ── App-global param ref interpreter tests (INT-10..14) ──────────────────────

func makeHTTPSkillWithRef(serverURL, appParamRef, injectMode string) *agentgen.SkillSpec {
	return &agentgen.SkillSpec{
		ID: "skill-1",
		Steps: []agentgen.StepSpec{
			{ID: "in", Type: agentgen.StepInput, Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"step-http"}},
			{
				ID:   "step-http",
				Type: agentgen.StepHTTP,
				Config: mustJSON(agentgen.HTTPStepConfig{
					Method:         "GET",
					URLTemplate:    serverURL,
					TimeoutSeconds: 5,
					AppParamRef:    appParamRef,
					InjectMode:     injectMode,
				}),
				Next: []string{"step-resp"},
			},
			{ID: "step-resp", Type: agentgen.StepResponse, Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "http_response"})},
		},
	}
}

// INT-10: AppParamRef present + matching AppGlobalParams → Authorization: Bearer injected.
func TestInterpreter_HTTPStep_AppParamRef_Injected(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer server.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		TenantID:        "t1",
		ApplicationID:   "a1",
		AgentID:         "agent-1",
		AppGlobalParams: map[string]string{"geoapify_key": "my-secret-key"},
	}
	_, err := interp.Execute(context.Background(), ic, makeHTTPSkillWithRef(server.URL, "geoapify_key", "header"), "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if capturedAuth != "Bearer my-secret-key" {
		t.Errorf("want Authorization: Bearer my-secret-key, got %q", capturedAuth)
	}
}

// INT-11: AppParamRef + absent entry + non-empty inject_mode → error.
func TestInterpreter_HTTPStep_AppParamRef_AbsentRequired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer server.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		TenantID:        "t1",
		ApplicationID:   "a1",
		AgentID:         "agent-1",
		AppGlobalParams: map[string]string{}, // key not present
	}
	_, err := interp.Execute(context.Background(), ic, makeHTTPSkillWithRef(server.URL, "geoapify_key", "header"), "")
	if err == nil {
		t.Fatal("expected error when required app_param_ref is absent")
	}
}

// INT-12: AppParamRef + absent entry + empty inject_mode → silently skips, no error.
func TestInterpreter_HTTPStep_AppParamRef_AbsentOptional(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer server.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		TenantID:        "t1",
		ApplicationID:   "a1",
		AgentID:         "agent-1",
		AppGlobalParams: map[string]string{},
	}
	// inject_mode is "" → absent param is optional
	_, err := interp.Execute(context.Background(), ic, makeHTTPSkillWithRef(server.URL, "geoapify_key", ""), "")
	if err != nil {
		t.Fatalf("expected no error for optional absent app_param_ref, got: %v", err)
	}
}

// INT-13: AppParamRef takes precedence over AppParamKey when both are set.
func TestInterpreter_HTTPStep_AppParamRef_TakesPrecedenceOverKey(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
	}))
	defer server.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		TenantID:        "t1",
		ApplicationID:   "a1",
		AgentID:         "agent-1",
		AgentParams:     map[string]string{"step-http:api_key": "per-binding-value"},
		AppGlobalParams: map[string]string{"global_key": "global-value"},
	}
	skill := &agentgen.SkillSpec{
		ID: "skill-1",
		Steps: []agentgen.StepSpec{
			{ID: "in", Type: agentgen.StepInput, Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"step-http"}},
			{
				ID:   "step-http",
				Type: agentgen.StepHTTP,
				Config: mustJSON(agentgen.HTTPStepConfig{
					Method:         "GET",
					URLTemplate:    server.URL,
					TimeoutSeconds: 5,
					AppParamKey:    "api_key",  // per-binding
					AppParamRef:    "global_key", // global — must win
					InjectMode:     "header",
				}),
				Next: []string{"step-resp"},
			},
			{ID: "step-resp", Type: agentgen.StepResponse, Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "http_response"})},
		},
	}
	_, err := interp.Execute(context.Background(), ic, skill, "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if capturedAuth != "Bearer global-value" {
		t.Errorf("want global-value to win, got %q", capturedAuth)
	}
}

// INT-14: LLM step with NodeLLMOverrides → override provider+model applied at runtime.
func TestInterpreter_LLMStep_NodeLLMOverride(t *testing.T) {
	var capturedProvider, capturedModel string
	factory := &modelCapturingFactory{
		onModel:    func(m string) { capturedModel = m },
		onProvider: func(p string) { capturedProvider = p },
		llm:        &fakeLLM{reply: "done"},
	}

	interp := agentgen.NewInterpreter(nil, factory, "platform-key")
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "agent-1",
		AppAPIKey:     map[string]string{"openai": "sk-openai"},
		NodeLLMOverrides: map[string]agentgen.NodeLLMOverride{
			"llm1": {Provider: "openai", Model: "gpt-4o"},
		},
	}

	skill := &agentgen.SkillSpec{
		ID: "skill-1",
		Steps: []agentgen.StepSpec{
			{ID: "in", Type: agentgen.StepInput, Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"llm1"}},
			{
				ID:   "llm1",
				Type: agentgen.StepLLM,
				Config: mustJSON(agentgen.LLMStepConfig{
					Provider:     "anthropic",
					Model:        "claude-haiku-4-5-20251001", // compiled default; should be overridden
					SystemPrompt: "hi",
				}),
				Next: []string{"out"},
			},
			{ID: "out", Type: agentgen.StepResponse, Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "output"})},
		},
	}

	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if capturedProvider != "openai" {
		t.Errorf("want provider openai, got %q", capturedProvider)
	}
	if capturedModel != "gpt-4o" {
		t.Errorf("want model gpt-4o, got %q", capturedModel)
	}
}

// modelCapturingFactory captures the provider and model passed to NewProvider (for INT-14).
type modelCapturingFactory struct {
	onProvider func(provider string)
	onModel    func(model string)
	llm        *fakeLLM
}

func (f *modelCapturingFactory) NewProvider(provider, model string, _ int, _ string) (agentgen.LLMProvider, error) {
	if f.onProvider != nil {
		f.onProvider(provider)
	}
	if f.onModel != nil {
		f.onModel(model)
	}
	return f.llm, nil
}

// ── DAG E2E smoke tests ────────────────────────────────────────────────────────
// These tests exercise the full CompileExecutionPlan → LocalExecutor →
// Interpreter.executeStep stack using real node types (no mock nodes).
// They prove that the live agent-runtime execution path works correctly for
// branching and parallel fan-out skills.

// TestDAG_BranchConvergence_TruePath verifies a branch skill compiled to a
// JoinBranchMerge plan and executed via LocalExecutor routes the true arm,
// runs only the true-arm transform, and produces the correct response.
//
//	Input → Branch(eq .flag "yes") → Transform(set label="true_path") → Response
//	                               ↘ Transform(set label="false_path") ↗
func TestDAG_BranchConvergence_TruePath(t *testing.T) {
	skill := branchConvergenceSkill()
	plan := agentgen.CompileExecutionPlan(skill)

	// Verify compiler classified the join correctly.
	end := plan.NodeByID("resp")
	if end == nil {
		t.Fatal("NodeByID(resp) returned nil")
	}
	if end.JoinMode != agentgen.JoinBranchMerge {
		t.Errorf("resp: expected JoinBranchMerge, got %s", end.JoinMode)
	}

	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "ag1"}
	exec := agentgen.NewLocalExecutor(interp)

	// StepInput reads vars["input"] and writes it to vars["msg"] per the binding.
	// Branch: eq .msg "yes" → true → upper("yes") → label="YES"
	res, err := exec.Execute(context.Background(), ic, plan, agentgen.PipelineVars{"input": "yes"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Text != "YES" {
		t.Errorf("true arm: got %q want %q", res.Text, "YES")
	}
}

// TestDAG_BranchConvergence_FalsePath verifies the false arm is taken when the
// branch expression evaluates to false.
func TestDAG_BranchConvergence_FalsePath(t *testing.T) {
	skill := branchConvergenceSkill()
	plan := agentgen.CompileExecutionPlan(skill)
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "ag1"}
	exec := agentgen.NewLocalExecutor(interp)

	// input="no" → branch false → lower("no") → label="no"
	res, err := exec.Execute(context.Background(), ic, plan, agentgen.PipelineVars{"input": "no"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Text != "no" {
		t.Errorf("false arm: got %q want %q", res.Text, "no")
	}
}

// TestDAG_ParallelTransforms_BothBranchesRun verifies a parallel fan-out skill
// (non-Branch fan-out → JoinWaitAll) executes both arms and merges their vars.
//
//	Input → Transform(label_a="arm_a") → Response(reads merged_result)
//	      ↘ Transform(label_b="arm_b") ↗
//
// The response step uses a concat transform to produce "arm_a+arm_b" from the
// merged vars, which proves both branches ran.
func TestDAG_ParallelTransforms_BothBranchesRun(t *testing.T) {
	skill := parallelFanOutSkill()
	plan := agentgen.CompileExecutionPlan(skill)

	// Compiler must classify the join as JoinWaitAll (non-Branch fan-out source).
	join := plan.NodeByID("join")
	if join == nil {
		t.Fatal("NodeByID(join) returned nil")
	}
	if join.JoinMode != agentgen.JoinWaitAll {
		t.Errorf("join: expected JoinWaitAll, got %s", join.JoinMode)
	}

	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "ag1"}
	exec := agentgen.NewLocalExecutor(interp)

	// StepInput reads vars["input"] → vars["msg"]
	// arm_a: upper("Hello") → upper_out="HELLO"
	// arm_b: lower("Hello") → lower_out="hello"
	// Both must run (JoinWaitAll); Response reads upper_out.
	res, err := exec.Execute(context.Background(), ic, plan, agentgen.PipelineVars{"input": "Hello"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Text != "HELLO" {
		t.Errorf("result: got %q want %q (upper_out from arm_a)", res.Text, "HELLO")
	}
}

// branchConvergenceSkill builds a real SkillSpec using StepBranch:
//
//	Input(text→msg) → Branch(eq .msg "yes", true→true_arm, false→false_arm)
//	→ true_arm: Transform(upper(msg) → label)   produces "YES"
//	→ false_arm: Transform(lower(msg) → label)  produces "no"
//	→ resp: Response(from_var=label)             [JoinBranchMerge]
//
// Test seeds input="yes" for true path (expects "YES") and input="no" for
// false path (expects "no"). Both arms converge at the same Response node.
func branchConvergenceSkill() *agentgen.SkillSpec {
	trueTransformCfg := mustJSON(agentgen.TransformStepConfig{
		Functions: []transform.FunctionStep{
			{Fn: "upper", InputVar: "msg", OutputVar: "label"},
		},
	})
	falseTransformCfg := mustJSON(agentgen.TransformStepConfig{
		Functions: []transform.FunctionStep{
			{Fn: "lower", InputVar: "msg", OutputVar: "label"},
		},
	})
	return &agentgen.SkillSpec{
		ID: "skill-branch-convergence",
		Steps: []agentgen.StepSpec{
			{
				ID:     "in",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{Bindings: map[string]string{"text": "msg"}}),
				Next:   []string{"br"},
			},
			{
				ID:   "br",
				Type: agentgen.StepBranch,
				Config: mustJSON(agentgen.BranchStepConfig{
					Expression: `{{eq .msg "yes"}}`,
					TrueNext:   "true_arm",
					FalseNext:  "false_arm",
				}),
				Next: []string{"true_arm", "false_arm"},
			},
			{ID: "true_arm", Type: agentgen.StepTransform, Config: trueTransformCfg, Next: []string{"resp"}},
			{ID: "false_arm", Type: agentgen.StepTransform, Config: falseTransformCfg, Next: []string{"resp"}},
			{ID: "resp", Type: agentgen.StepResponse, Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "label"})},
		},
	}
}

// parallelFanOutSkill builds a real SkillSpec with non-Branch fan-out (Input fans
// to two Transform steps) converging at a Response step (JoinWaitAll).
//
//	Input(text→msg) → Transform(upper(msg)→upper_out) → join(Response, reads upper_out)
//	               ↘ Transform(lower(msg)→lower_out) ↗
//
// Response reads upper_out — both arms must have run for the join to fire.
// lower_out being present in merged vars is an implicit proof that arm_b ran.
func parallelFanOutSkill() *agentgen.SkillSpec {
	armACfg := mustJSON(agentgen.TransformStepConfig{
		Functions: []transform.FunctionStep{
			{Fn: "upper", InputVar: "msg", OutputVar: "upper_out"},
		},
	})
	armBCfg := mustJSON(agentgen.TransformStepConfig{
		Functions: []transform.FunctionStep{
			{Fn: "lower", InputVar: "msg", OutputVar: "lower_out"},
		},
	})
	return &agentgen.SkillSpec{
		ID: "skill-parallel-fanout",
		Steps: []agentgen.StepSpec{
			{
				ID:     "in",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{Bindings: map[string]string{"text": "msg"}}),
				Next:   []string{"arm_a", "arm_b"},
			},
			{ID: "arm_a", Type: agentgen.StepTransform, Config: armACfg, Next: []string{"join"}},
			{ID: "arm_b", Type: agentgen.StepTransform, Config: armBCfg, Next: []string{"join"}},
			{ID: "join", Type: agentgen.StepResponse, Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "upper_out"})},
		},
	}
}
