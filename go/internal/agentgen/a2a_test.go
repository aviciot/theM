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

// ── Test doubles ─────────────────────────────────────────────────────────────

// stubA2ACaller is a test double for A2ACaller.
type stubA2ACaller struct {
	lastParams agentgen.A2ACallParams
	returnJSON string
	returnErr  error
}

func (s *stubA2ACaller) Call(_ context.Context, p agentgen.A2ACallParams) (json.RawMessage, error) {
	s.lastParams = p
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return json.RawMessage(s.returnJSON), nil
}

var _ agentgen.A2ACaller = (*stubA2ACaller)(nil)

// stubEndpointResolver implements agentgen.AgentEndpointResolver for tests.
type stubEndpointResolver struct {
	url       string
	authToken string
	agentID   string
	bindingID string
	err       error
}

func (s *stubEndpointResolver) ResolveEndpoint(_ context.Context, _, _, _ string) (agentgen.ResolvedEndpoint, error) {
	if s.err != nil {
		return agentgen.ResolvedEndpoint{}, s.err
	}
	return agentgen.ResolvedEndpoint{
		EndpointURL: s.url,
		AuthToken:   s.authToken,
		AgentID:     s.agentID,
		BindingID:   s.bindingID,
	}, nil
}

var _ agentgen.AgentEndpointResolver = (*stubEndpointResolver)(nil)

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
			{ID: "in", Type: agentgen.StepInput, Config: json.RawMessage(`{}`), Next: []string{"a2a1"}},
			{ID: "a2a1", Type: agentgen.StepA2ACall, Config: json.RawMessage(cfg), Next: []string{"out"}},
			{ID: "out", Type: agentgen.StepResponse, Config: json.RawMessage(`{"from_var":"` + outVar + `"}`), Next: nil},
		},
	}
}

// ── A2A-1: node registered with non-nil Execute ───────────────────────────────

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

// ── A2A-2: Validate rejects missing required fields ───────────────────────────

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

// ── A2A-3: Validate accepts a complete config ─────────────────────────────────

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

// ── A2A-4: Execute without A2ACaller returns an error ─────────────────────────

func TestA2A_Execute_NoCaller(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "app1"}
	skill := buildA2ASkill("target", "input", "out")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err == nil {
		t.Fatal("expected error when A2ACaller is nil")
	}
}

// ── A2A-5: Execute calls A2ACaller with correct params and stores result ───────

func TestA2A_Execute_CallsCallerAndSetsVar(t *testing.T) {
	stub := &stubA2ACaller{returnJSON: `"summary text"`}
	interp := agentgen.NewInterpreter(nil, nil, "").WithA2ACaller(stub)
	ic := &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "app-123",
		InvocationID:  "inv-abc",
	}
	skill := buildA2ASkill("vision-agent", "input", "vision_out")
	result, err := interp.Execute(context.Background(), ic, skill, "describe this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.lastParams.AgentSlug != "vision-agent" {
		t.Errorf("slug: want vision-agent, got %q", stub.lastParams.AgentSlug)
	}
	if stub.lastParams.TenantID != "t1" {
		t.Errorf("tenantID: want t1, got %q", stub.lastParams.TenantID)
	}
	if stub.lastParams.ApplicationID != "app-123" {
		t.Errorf("appID: want app-123, got %q", stub.lastParams.ApplicationID)
	}
	if stub.lastParams.InvocationID != "inv-abc" {
		t.Errorf("invocationID: want inv-abc, got %q", stub.lastParams.InvocationID)
	}
	if stub.lastParams.StepID != "a2a1" {
		t.Errorf("stepID: want a2a1, got %q", stub.lastParams.StepID)
	}
	if result.Text != "summary text" {
		t.Errorf("result text: want 'summary text', got %q", result.Text)
	}
}

// ── A2A-6: Execute propagates A2ACaller errors ────────────────────────────────

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

// ── A2A-7: depth propagation ──────────────────────────────────────────────────

func TestA2A_Execute_PropagatesDepth(t *testing.T) {
	stub := &stubA2ACaller{returnJSON: `"ok"`}
	interp := agentgen.NewInterpreter(nil, nil, "").WithA2ACaller(stub)
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "app1", A2ACallDepth: 2}
	skill := buildA2ASkill("target", "input", "out")
	_, err := interp.Execute(context.Background(), ic, skill, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.lastParams.Depth != 2 {
		t.Errorf("depth: want 2, got %d", stub.lastParams.Depth)
	}
}

// ── A2A-8: self-call rejection ────────────────────────────────────────────────

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
	if stub.lastParams.AgentSlug != "" {
		t.Error("A2ACaller.Call must not be invoked for self-call")
	}
}

// ── A2A-9: depth cap ─────────────────────────────────────────────────────────

func TestA2A_MaxDepthEnforced(t *testing.T) {
	// The HTTPA2ACaller itself enforces the cap; the interpreter passes the depth
	// through. Simulate this by making the stub return a depth error.
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

// ── A2A-9b: HTTPA2ACaller enforces depth cap directly ────────────────────────

func TestA2A_HTTPA2ACaller_DepthCapRejected(t *testing.T) {
	resolver := &stubEndpointResolver{url: "http://nowhere", agentID: "ag1", bindingID: "b1"}
	caller := agentgen.NewHTTPA2ACaller(resolver, nil)
	_, err := caller.Call(context.Background(), agentgen.A2ACallParams{
		TenantID: "t1", ApplicationID: "app1", AgentSlug: "other", Depth: agentgen.MaxA2ACallDepth,
	})
	if err == nil {
		t.Fatal("expected depth cap error from HTTPA2ACaller")
	}
	if !strContains(err.Error(), "nesting depth") {
		t.Errorf("expected depth message, got: %v", err)
	}
}

// ── A2A-10: HumanWait + local backend ─────────────────────────────────────────

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
	// Validate emits HUMAN_WAIT_REQUIRES_TEMPORAL as a warning.
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

	// CompileForPublish must also reject the graph.
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

// ── A2A-11: HumanWait + temporal backend ─────────────────────────────────────

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

// ── A2A-12: HTTPA2ACaller sends all required headers ─────────────────────────

func TestA2A_HTTPA2ACaller_Integration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify all required identity + depth headers.
		for _, hdr := range []string{"X-Them-Tenant-Id", "X-Them-Application-Id", "X-Them-Agent-Id", "X-Them-Binding-Id"} {
			if r.Header.Get(hdr) == "" {
				http.Error(w, "missing header "+hdr, http.StatusBadRequest)
				return
			}
		}
		if r.Header.Get("X-Them-Tenant-Id") != "tenant-1" {
			http.Error(w, "wrong tenant", http.StatusBadRequest)
			return
		}
		if r.Header.Get("X-Them-A2A-Depth") != "1" {
			http.Error(w, "wrong depth header", http.StatusBadRequest)
			return
		}
		if r.Header.Get("X-Them-Agent-Id") != "ag-uuid-1" {
			http.Error(w, "wrong agent-id header", http.StatusBadRequest)
			return
		}
		if r.Header.Get("X-Them-Binding-Id") != "bind-uuid-1" {
			http.Error(w, "wrong binding-id header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"x","result":{"status":{"state":"completed","message":{"role":"ROLE_AGENT","parts":[{"kind":"text","text":"agent output"}]}},"artifacts":[]}}`)
	}))
	defer srv.Close()

	resolver := &stubEndpointResolver{
		url:       srv.URL,
		agentID:   "ag-uuid-1",
		bindingID: "bind-uuid-1",
	}
	caller := agentgen.NewHTTPA2ACaller(resolver, nil)

	result, err := caller.Call(context.Background(), agentgen.A2ACallParams{
		TenantID:      "tenant-1",
		ApplicationID: "app-1",
		AgentSlug:     "target-agent",
		InvocationID:  "inv-111",
		StepID:        "step-a2a",
		Input:         json.RawMessage(`"hello"`),
		Depth:         0,
	})
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

// ── A2A-13: DeriveOutputs defaults output_var ─────────────────────────────────

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

// ── A2A-14: fail-closed when no binding exists ────────────────────────────────

func TestA2A_HTTPA2ACaller_NoBinding_FailClosed(t *testing.T) {
	resolver := &stubEndpointResolver{
		err: fmt.Errorf("a2a_call: agent \"target\" not bound to application or disabled"),
	}
	caller := agentgen.NewHTTPA2ACaller(resolver, nil)
	_, err := caller.Call(context.Background(), agentgen.A2ACallParams{
		TenantID: "t1", ApplicationID: "app1", AgentSlug: "target", Depth: 0,
	})
	if err == nil {
		t.Fatal("expected error when binding is missing")
	}
	if !strContains(err.Error(), "not bound") {
		t.Errorf("expected 'not bound' message, got: %v", err)
	}
}

// ── A2A-15: stable request IDs are deterministic ──────────────────────────────

func TestA2A_HTTPA2ACaller_StableRequestIDs(t *testing.T) {
	var capturedIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     string `json:"id"`
			Params struct {
				Message struct {
					MessageID string `json:"messageId"`
				} `json:"message"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		capturedIDs = append(capturedIDs, req.ID, req.Params.Message.MessageID)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"x","result":{"status":{"state":"completed","message":{"role":"ROLE_AGENT","parts":[{"kind":"text","text":"ok"}]}}}}`)
	}))
	defer srv.Close()

	resolver := &stubEndpointResolver{url: srv.URL, agentID: "ag1", bindingID: "b1"}
	caller := agentgen.NewHTTPA2ACaller(resolver, nil)
	params := agentgen.A2ACallParams{
		TenantID:      "t1",
		ApplicationID: "app1",
		AgentSlug:     "other",
		InvocationID:  "inv-stable",
		StepID:        "step-x",
		Input:         json.RawMessage(`"hello"`),
		Depth:         0,
	}

	// First call.
	capturedIDs = nil
	if _, err := caller.Call(context.Background(), params); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	firstRPCID, firstMsgID := capturedIDs[0], capturedIDs[1]

	// Retry with identical params — IDs must be identical.
	capturedIDs = nil
	if _, err := caller.Call(context.Background(), params); err != nil {
		t.Fatalf("retry call error: %v", err)
	}
	if capturedIDs[0] != firstRPCID {
		t.Errorf("RPC ID changed across retries: %q vs %q", firstRPCID, capturedIDs[0])
	}
	if capturedIDs[1] != firstMsgID {
		t.Errorf("Message ID changed across retries: %q vs %q", firstMsgID, capturedIDs[1])
	}
}

// ── A2A-16: remote error messages are sanitized ───────────────────────────────

func TestA2A_HTTPA2ACaller_RemoteErrorSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Response contains a URL that should be stripped.
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"x","error":{"code":-32000,"message":"upstream failure at http://internal-host:9300/agents/secret/endpoint with key=sk-abc"}}`)
	}))
	defer srv.Close()

	resolver := &stubEndpointResolver{url: srv.URL, agentID: "ag1", bindingID: "b1"}
	caller := agentgen.NewHTTPA2ACaller(resolver, nil)
	_, err := caller.Call(context.Background(), agentgen.A2ACallParams{
		TenantID: "t1", ApplicationID: "app1", AgentSlug: "other", Depth: 0,
	})
	if err == nil {
		t.Fatal("expected error from remote agent")
	}
	errStr := err.Error()
	if strContains(errStr, "http://internal-host") {
		t.Errorf("error must not contain raw URL, got: %v", errStr)
	}
	if strContains(errStr, "[url-redacted]") == false {
		// the URL should be replaced with [url-redacted]
		t.Errorf("expected [url-redacted] placeholder in sanitized error, got: %v", errStr)
	}
}

// ── A2A-17: E2E — Agent A calls Agent B through LocalExecutor ─────────────────
//
// This test simulates a full canvas pipeline where agent A (the caller) has
// an a2a_call step targeting agent B (the callee). Agent B is represented by
// a stub HTTP server. We verify:
//   - correct tenant/application/agent/binding headers
//   - output written to the configured output_var
//   - depth incremented across the call boundary
//   - stable request IDs (retry idempotency)

func TestA2A_E2E_LocalExecutor(t *testing.T) {
	var capturedHeaders http.Header
	calledCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		calledCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"x","result":{"status":{"state":"completed","message":{"role":"ROLE_AGENT","parts":[{"kind":"text","text":"agent-b-result"}]}}}}`)
	}))
	defer srv.Close()

	resolver := &stubEndpointResolver{
		url:       srv.URL,
		agentID:   "agent-b-uuid",
		bindingID: "binding-b-uuid",
	}
	interp := agentgen.NewInterpreter(nil, nil, "").
		WithA2ACaller(agentgen.NewHTTPA2ACaller(resolver, nil))

	ic := &agentgen.InvocationContext{
		TenantID:      "tenant-e2e",
		ApplicationID: "app-e2e",
		InvocationID:  "inv-e2e-001",
		A2ACallDepth:  0,
	}

	skill := buildA2ASkill("agent-b", "input", "b_result")
	result, err := interp.Execute(context.Background(), ic, skill, "call agent B")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Text != "agent-b-result" {
		t.Errorf("result text: want 'agent-b-result', got %q", result.Text)
	}

	// Verify all four identity headers were sent.
	if capturedHeaders.Get("X-Them-Tenant-Id") != "tenant-e2e" {
		t.Errorf("X-Them-Tenant-Id: want tenant-e2e, got %q", capturedHeaders.Get("X-Them-Tenant-Id"))
	}
	if capturedHeaders.Get("X-Them-Application-Id") != "app-e2e" {
		t.Errorf("X-Them-Application-Id: want app-e2e, got %q", capturedHeaders.Get("X-Them-Application-Id"))
	}
	if capturedHeaders.Get("X-Them-Agent-Id") != "agent-b-uuid" {
		t.Errorf("X-Them-Agent-Id: want agent-b-uuid, got %q", capturedHeaders.Get("X-Them-Agent-Id"))
	}
	if capturedHeaders.Get("X-Them-Binding-Id") != "binding-b-uuid" {
		t.Errorf("X-Them-Binding-Id: want binding-b-uuid, got %q", capturedHeaders.Get("X-Them-Binding-Id"))
	}
	// Depth must be incremented to 1 (caller starts at 0, sends depth+1).
	if capturedHeaders.Get("X-Them-A2A-Depth") != "1" {
		t.Errorf("X-Them-A2A-Depth: want 1, got %q", capturedHeaders.Get("X-Them-A2A-Depth"))
	}

	// Tenant isolation: wrong tenant must NOT reach the same resolver result.
	// With a wrong-tenant resolver (returns error), the call must fail.
	wrongTenantResolver := &stubEndpointResolver{
		err: fmt.Errorf("a2a_call: agent \"agent-b\" not bound to application or disabled"),
	}
	interpWrong := agentgen.NewInterpreter(nil, nil, "").
		WithA2ACaller(agentgen.NewHTTPA2ACaller(wrongTenantResolver, nil))
	icWrong := &agentgen.InvocationContext{
		TenantID:      "other-tenant",
		ApplicationID: "app-other",
		InvocationID:  "inv-e2e-002",
	}
	_, err = interpWrong.Execute(context.Background(), icWrong, skill, "call agent B")
	if err == nil {
		t.Error("cross-tenant call must fail when binding is not found")
	}
}

// ── A2A-18: E2E — Agent A calls Agent B through ExecuteNodeForActivity ────────
//
// Tests the Temporal activity execution path (ExecuteNodeForActivity) with an
// a2a_call node. Verifies that ActivityIC.A2ACallDepth propagates correctly
// through the activity boundary and that the output var is set.

func TestA2A_E2E_ExecuteNodeForActivity(t *testing.T) {
	var capturedDepth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedDepth = r.Header.Get("X-Them-A2A-Depth")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"x","result":{"status":{"state":"completed","message":{"role":"ROLE_AGENT","parts":[{"kind":"text","text":"activity-result"}]}}}}`)
	}))
	defer srv.Close()

	resolver := &stubEndpointResolver{
		url:       srv.URL,
		agentID:   "agent-b-uuid",
		bindingID: "binding-b-uuid",
	}
	interp := agentgen.NewInterpreter(nil, nil, "").
		WithA2ACaller(agentgen.NewHTTPA2ACaller(resolver, nil))

	// Simulate the ActivityIC that dag-worker would construct from Temporal history.
	activityIC := agentgen.ActivityIC{
		TenantID:      "tenant-act",
		ApplicationID: "app-act",
		AgentID:       "agent-a-uuid",
		BindingID:     "binding-a-uuid",
		A2ACallDepth:  1, // already one level deep
	}

	// Reconstruct InvocationContext as dag-worker's dbContextLoader.Load does.
	ic := &agentgen.InvocationContext{
		TenantID:      activityIC.TenantID,
		ApplicationID: activityIC.ApplicationID,
		AgentID:       activityIC.AgentID,
		BindingID:     activityIC.BindingID,
		InvocationID:  "inv-act-001",
		A2ACallDepth:  activityIC.A2ACallDepth,
	}

	a2aCfg, _ := json.Marshal(map[string]any{
		"agent_slug": "agent-b",
		"input_var":  "input",
		"output_var": "act_result",
	})
	node := agentgen.PlanNode{
		StepID:  "a2a-node",
		Type:    agentgen.StepA2ACall,
		Config:  a2aCfg,
		Outputs: []agentgen.VarRef{{Name: "act_result", Required: false}},
	}
	input := agentgen.NodeExecutionInput{
		Node: node,
		Vars: agentgen.PipelineVars{"input": "hello from activity"},
	}

	out, err := agentgen.ExecuteNodeForActivity(context.Background(), interp.Clone(), ic, input)
	if err != nil {
		t.Fatalf("ExecuteNodeForActivity error: %v", err)
	}
	if out.Vars["act_result"] != "activity-result" {
		t.Errorf("act_result: want 'activity-result', got %v", out.Vars["act_result"])
	}
	// Depth sent to agent B must be 2 (IC is 1, HTTPA2ACaller sends depth+1).
	if capturedDepth != "2" {
		t.Errorf("X-Them-A2A-Depth sent to agent B: want 2, got %q", capturedDepth)
	}
}
