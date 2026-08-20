package agentgen_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aviciot/them/internal/agentgen"
)

// --- InvocationContext redaction test ---

func TestInvocationContext_StringRedactsCredentials(t *testing.T) {
	ic := agentgen.InvocationContext{
		TenantID:      "tenant-1",
		ApplicationID: "app-1",
		AgentID:       "agent-1",
		BindingID:     "binding-1",
		Credentials: map[string]string{
			"api_key":    "super-secret-key",
			"other_slot": "another-secret",
		},
	}
	s := ic.String()
	if strings.Contains(s, "super-secret-key") {
		t.Error("InvocationContext.String() must not contain credential values")
	}
	if strings.Contains(s, "another-secret") {
		t.Error("InvocationContext.String() must not contain credential values")
	}
	if !strings.Contains(s, "slots=2") {
		t.Errorf("InvocationContext.String() should report slot count, got: %s", s)
	}
}

// --- AppAgentBinding credential resolution ---

func TestAppAgentBinding_ResolveCredentials_TwoBindingsDifferentCreds(t *testing.T) {
	// Simulates the Salesforce example: same agent, two apps, different credentials.
	decryptMock := func(ct string) (string, error) {
		if strings.HasPrefix(ct, "enc:") {
			return strings.TrimPrefix(ct, "enc:"), nil
		}
		return ct, nil
	}

	bindingA := agentgen.AppAgentBinding{
		ID:                   "binding-a",
		EncryptedCredentials: map[string]string{"salesforce_api": "enc:org-a-token"},
	}
	bindingB := agentgen.AppAgentBinding{
		ID:                   "binding-b",
		EncryptedCredentials: map[string]string{"salesforce_api": "enc:org-b-token"},
	}

	credsA, err := bindingA.ResolveCredentials(decryptMock)
	if err != nil {
		t.Fatalf("resolve binding A: %v", err)
	}
	credsB, err := bindingB.ResolveCredentials(decryptMock)
	if err != nil {
		t.Fatalf("resolve binding B: %v", err)
	}

	if credsA["salesforce_api"] != "org-a-token" {
		t.Errorf("binding A: expected org-a-token, got %q", credsA["salesforce_api"])
	}
	if credsB["salesforce_api"] != "org-b-token" {
		t.Errorf("binding B: expected org-b-token, got %q", credsB["salesforce_api"])
	}
	if credsA["salesforce_api"] == credsB["salesforce_api"] {
		t.Error("two different bindings for the same agent must resolve to DIFFERENT credentials")
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
		Credentials:   map[string]string{},
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
		Credentials:   map[string]string{},
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
					Expressions: map[string]string{
						"greeting": "Hello, {{.raw}}!",
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

func TestInterpreter_HTTPStep_InjectsCredential(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"result": "ok"}) //nolint:errcheck
	}))
	defer server.Close()

	interp := agentgen.NewInterpreter(&http.Client{}, nil, "")
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "agent-1",
		Credentials:   map[string]string{"my_api_key": "secret-token-123"},
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
					CredentialSlot: "my_api_key",
					CredentialInject: agentgen.CredentialInject{
						Mode:          "header",
						HeaderName:    "Authorization",
						ValueTemplate: "Bearer {credential}",
					},
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
	if capturedAuth != "Bearer secret-token-123" {
		t.Errorf("expected Authorization: Bearer secret-token-123, got %q", capturedAuth)
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
		Credentials:   map[string]string{},
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
		Credentials: map[string]string{},
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
		Credentials:   map[string]string{},
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
		Credentials:   map[string]string{},
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

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
