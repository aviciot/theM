package agentgen_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

func strContains(s, sub string) bool { return strings.Contains(s, sub) }

// stubA2ACaller is a test double for A2ACaller.
type stubA2ACaller struct {
	lastTenantID      string
	lastAppID         string
	lastSlug          string
	lastInput         json.RawMessage
	lastDepth         int
	returnJSON        string
	returnErr         error
}

func (s *stubA2ACaller) Call(_ context.Context, tenantID, appID, slug string, input json.RawMessage, depth int) (json.RawMessage, error) {
	s.lastTenantID = tenantID
	s.lastAppID = appID
	s.lastSlug = slug
	s.lastInput = input
	s.lastDepth = depth
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return json.RawMessage(s.returnJSON), nil
}

var _ agentgen.A2ACaller = (*stubA2ACaller)(nil)

// buildA2ASkill builds a minimal SkillSpec with input→a2a_call→response.
func buildA2ASkill(targetSlug, inputVar, outputVar string) *agentgen.SkillSpec {
	cfgMap := map[string]any{
		"agent_slug": targetSlug,
		"input_var":  inputVar,
		"output_var": outputVar,
	}
	cfg, _ := json.Marshal(cfgMap)
	outVar := outputVar
	if outVar == "" {
		outVar = "a2a_response"
	}
	return &agentgen.SkillSpec{
		ID:   "sk1",
		Name: "test",
		Steps: []agentgen.StepSpec{
			{ID: "in",  Type: agentgen.StepInput,   Config: json.RawMessage(`{}`),                  Next: []string{"a2a1"}},
			{ID: "a2a1", Type: agentgen.StepA2ACall, Config: json.RawMessage(cfg),                  Next: []string{"out"}},
			{ID: "out", Type: agentgen.StepResponse, Config: json.RawMessage(`{"from_var":"` + outVar + `"}`), Next: nil},
		},
	}
}

// A2A-1: a2a_call node is registered and has Execute set.
func TestA2A_NodeRegistered(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepA2ACall)
	if !ok {
		t.Fatal("StepA2ACall not registered")
	}
	if def.Execute == nil {
		t.Error("StepA2ACall.Execute must be non-nil after Phase 5-C")
	}
	if def.Label == "" {
		t.Error("StepA2ACall.Label must not be empty")
	}
}

// A2A-2: Validate rejects missing required fields (agent_slug, input_var).
func TestA2A_Validate_MissingFields(t *testing.T) {
	_, issues := agentgen.Validate("a", "t", "d", "slug", json.RawMessage(`{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",   "type": "input",    "config": {}, "next": ["a1"]},
				{"id": "a1",   "type": "a2a_call", "config": {}, "next": ["out"]},
				{"id": "out",  "type": "response", "config": {"from_var": "r"}}
			]
		}]
	}`))
	hasSlug, hasInput := false, false
	for _, iss := range issues {
		if iss.Severity != "error" {
			continue
		}
		if strContains(iss.Message, "agent_slug") {
			hasSlug = true
		}
		if strContains(iss.Message, "input_var") {
			hasInput = true
		}
	}
	if !hasSlug {
		t.Error("expected error about agent_slug")
	}
	if !hasInput {
		t.Error("expected error about input_var")
	}
}

// A2A-3: Validate accepts a complete config with no required-field errors.
func TestA2A_Validate_ValidConfig(t *testing.T) {
	_, issues := agentgen.Validate("a", "t", "d", "slug", json.RawMessage(`{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",  "type": "input",    "config": {}, "next": ["a1"]},
				{"id": "a1",  "type": "a2a_call", "config": {"agent_slug":"vision","input_var":"q","output_var":"r"}, "next": ["out"]},
				{"id": "out", "type": "response", "config": {"from_var": "r"}}
			]
		}]
	}`))
	for _, iss := range issues {
		if iss.Severity == "error" && iss.NodeID == "a1" {
			t.Errorf("unexpected error on valid a2a_call config: %+v", iss)
		}
	}
}

// A2A-4: Execute without A2ACaller returns an error.
func TestA2A_Execute_NoCaller(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "app1"}
	skill := buildA2ASkill("target", "input", "out")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err == nil {
		t.Fatal("expected error when A2ACaller is nil")
	}
}

// A2A-5: Execute calls A2ACaller with correct tenant/app/slug and stores result.
func TestA2A_Execute_CallsCallerAndSetsVar(t *testing.T) {
	stub := &stubA2ACaller{returnJSON: `"summary text"`}
	interp := agentgen.NewInterpreter(nil, nil, "").WithA2ACaller(stub)
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "app-123"}
	skill := buildA2ASkill("vision-agent", "input", "vision_out")
	result, err := interp.Execute(context.Background(), ic, skill, "describe this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.lastSlug != "vision-agent" {
		t.Errorf("slug: want vision-agent, got %q", stub.lastSlug)
	}
	if stub.lastTenantID != "t1" {
		t.Errorf("tenantID: want t1, got %q", stub.lastTenantID)
	}
	if stub.lastAppID != "app-123" {
		t.Errorf("appID: want app-123, got %q", stub.lastAppID)
	}
	if result.Text != "summary text" {
		t.Errorf("result text: want 'summary text', got %q", result.Text)
	}
}

// A2A-6: Execute propagates A2ACaller errors.
func TestA2A_Execute_CallerError(t *testing.T) {
	stub := &stubA2ACaller{returnErr: fmt.Errorf("agent unreachable")}
	interp := agentgen.NewInterpreter(nil, nil, "").WithA2ACaller(stub)
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "app1"}
	skill := buildA2ASkill("target", "input", "out")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err == nil {
		t.Fatal("expected error propagated from A2ACaller")
	}
	if !strContains(err.Error(), "agent unreachable") {
		t.Errorf("expected original error in message, got: %v", err)
	}
}

// A2A-7: depth propagation — caller receives the IC's A2ACallDepth.
func TestA2A_Execute_PropagatesDepth(t *testing.T) {
	stub := &stubA2ACaller{returnJSON: `"ok"`}
	interp := agentgen.NewInterpreter(nil, nil, "").WithA2ACaller(stub)
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "app1", A2ACallDepth: 2}
	skill := buildA2ASkill("target", "input", "out")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.lastDepth != 2 {
		t.Errorf("depth: want 2, got %d", stub.lastDepth)
	}
}

// A2A-8: self-call rejection — agent_slug == caller's own slug is rejected.
func TestA2A_Execute_SelfCallRejected(t *testing.T) {
	stub := &stubA2ACaller{returnJSON: `"ok"`}
	interp := agentgen.NewInterpreter(nil, nil, "").WithA2ACaller(stub)
	spec := &agentgen.AgentSpec{Slug: "my-agent"}
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "app1", Spec: spec}
	skill := buildA2ASkill("my-agent", "input", "out") // same slug as caller
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err == nil {
		t.Fatal("expected self-call to be rejected")
	}
	if !strContains(err.Error(), "self-invocation") {
		t.Errorf("expected self-invocation message, got: %v", err)
	}
	if stub.lastSlug != "" {
		t.Error("A2ACaller.Call must not be invoked for self-call")
	}
}

// A2A-9: depth cap — A2ACallDepth >= MaxA2ACallDepth is rejected by the caller.
func TestA2A_MaxDepthEnforced(t *testing.T) {
	errDepth := fmt.Errorf("maximum nesting depth 3 reached")
	stub := &stubA2ACaller{returnErr: errDepth}
	interp := agentgen.NewInterpreter(nil, nil, "").WithA2ACaller(stub)
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "app1", A2ACallDepth: agentgen.MaxA2ACallDepth}
	skill := buildA2ASkill("other", "input", "out")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err == nil {
		t.Fatal("expected depth cap error")
	}
}

// A2A-10: HumanWait in a non-temporal agent emits a warning during Validate
// and a fatal error set (NODE_NOT_EXECUTABLE) during CompileForPublish.
// The HUMAN_WAIT_REQUIRES_TEMPORAL rule is enforced at Validate time as a warning
// so authors see it before attempting publish.
func TestA2A_HumanWait_LocalBackend_Rejected(t *testing.T) {
	const def = `{
		"agent_root": {"display_name": "X", "execution_backend": "local"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",  "type": "input",      "config": {}, "next": ["hw1"]},
				{"id": "hw1", "type": "human_wait",  "config": {"prompt":"Approve?","reply_var":"reply"}, "next": ["out"]},
				{"id": "out", "type": "response",    "config": {"from_var": "reply"}}
			]
		}]
	}`
	// Validate emits the HUMAN_WAIT_REQUIRES_TEMPORAL warning (human_wait is a stub,
	// so executability is also a warning at this stage).
	_, issues := agentgen.Validate("a", "t", "d", "slug", json.RawMessage(def))
	found := false
	for _, iss := range issues {
		if iss.Code == "HUMAN_WAIT_REQUIRES_TEMPORAL" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected HUMAN_WAIT_REQUIRES_TEMPORAL warning from Validate, got: %v", issues)
	}

	// CompileForPublish must also reject this graph (NODE_NOT_EXECUTABLE for human_wait
	// fires before HUMAN_WAIT_REQUIRES_TEMPORAL, both block publish).
	spec, pubIssues := agentgen.CompileForPublish("a", "t", "d", "slug", json.RawMessage(def))
	if spec != nil {
		t.Error("CompileForPublish must return nil spec when graph has human_wait in non-temporal agent")
	}
	hasBlockingError := false
	for _, iss := range pubIssues {
		if iss.Severity == "error" {
			hasBlockingError = true
		}
	}
	if !hasBlockingError {
		t.Errorf("CompileForPublish must emit at least one error for human_wait+local, got: %v", pubIssues)
	}
}

// A2A-11: HumanWait in a temporal agent compiles cleanly.
func TestA2A_HumanWait_TemporalBackend_OK(t *testing.T) {
	const def = `{
		"agent_root": {"display_name": "X", "execution_backend": "temporal"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",  "type": "input",      "config": {}, "next": ["hw1"]},
				{"id": "hw1", "type": "human_wait",  "config": {"prompt":"Approve?","reply_var":"reply"}, "next": ["out"]},
				{"id": "out", "type": "response",    "config": {"from_var": "reply"}}
			]
		}]
	}`
	_, issues := agentgen.Validate("a", "t", "d", "slug", json.RawMessage(def))
	for _, iss := range issues {
		if iss.Code == "HUMAN_WAIT_REQUIRES_TEMPORAL" {
			t.Errorf("unexpected HUMAN_WAIT_REQUIRES_TEMPORAL for temporal backend: %v", iss)
		}
	}
}

// A2A-12: HTTPA2ACaller calls the real endpoint and parses the response.
func TestA2A_HTTPA2ACaller_Integration(t *testing.T) {
	// Minimal A2A JSON-RPC response with a task result containing one text artifact.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify required headers.
		if r.Header.Get("X-Them-Tenant-Id") != "tenant-1" {
			http.Error(w, "missing tenant header", http.StatusBadRequest)
			return
		}
		if r.Header.Get("X-Them-A2A-Depth") != "1" {
			http.Error(w, "wrong depth header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"x","result":{"status":{"state":"completed","message":{"role":"ROLE_AGENT","parts":[{"kind":"text","text":"agent output"}]}},"artifacts":[]}}`)
	}))
	defer srv.Close()

	// Stub resolver returns the test server URL with no auth token.
	resolver := &stubEndpointResolver{url: srv.URL}
	caller := agentgen.NewHTTPA2ACaller(resolver, nil)

	result, err := caller.Call(context.Background(), "tenant-1", "app-1", "target-agent", json.RawMessage(`"hello"`), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var text string
	if err := json.Unmarshal(result, &text); err != nil {
		t.Fatalf("expected JSON string result, got %s: %v", result, err)
	}
	if text != "agent output" {
		t.Errorf("result: want 'agent output', got %q", text)
	}
}

// A2A-13: DeriveOutputs defaults output_var to "a2a_response" when not set.
func TestA2A_DeriveOutputs_DefaultVar(t *testing.T) {
	def, _ := agentgen.LookupNode(agentgen.StepA2ACall)
	refs := def.DeriveOutputs(json.RawMessage(`{"agent_slug":"s","input_var":"q"}`))
	if len(refs) != 1 {
		t.Fatalf("expected 1 output ref, got %d", len(refs))
	}
	if refs[0].Name != "a2a_response" {
		t.Errorf("default output_var: want 'a2a_response', got %q", refs[0].Name)
	}
}

// stubEndpointResolver implements agentgen.AgentEndpointResolver for tests.
type stubEndpointResolver struct {
	url       string
	authToken string
	err       error
}

func (s *stubEndpointResolver) ResolveEndpoint(_ context.Context, _, _ string) (string, string, error) {
	return s.url, s.authToken, s.err
}

var _ agentgen.AgentEndpointResolver = (*stubEndpointResolver)(nil)
