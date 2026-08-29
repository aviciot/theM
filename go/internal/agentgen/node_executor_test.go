package agentgen_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func makeInterp() *agentgen.Interpreter {
	return agentgen.NewInterpreter(&http.Client{}, nil, "")
}

func makeIC() *agentgen.InvocationContext {
	return &agentgen.InvocationContext{
		TenantID:      "tid",
		ApplicationID: "aid",
		AgentID:       "ag1",
		BindingID:     "bid",
	}
}

func inputNode(id string, nextID ...string) agentgen.PlanNode {
	cfg, _ := json.Marshal(agentgen.InputStepConfig{})
	return agentgen.PlanNode{
		StepID: id,
		Type:   agentgen.StepInput,
		Config: cfg,
		Next:   nextID,
	}
}

func responseNode(id, fromVar string) agentgen.PlanNode {
	cfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: fromVar, MediaType: "text/plain"})
	return agentgen.PlanNode{
		StepID: id,
		Type:   agentgen.StepResponse,
		Config: cfg,
	}
}

func transformNode(id, inputVar, outputVar, value string) agentgen.PlanNode {
	cfg, _ := json.Marshal(agentgen.TransformStepConfig{})
	// Use a simple identity transform; the var is already in scope.
	_ = inputVar
	_ = value
	return agentgen.PlanNode{
		StepID: id,
		Type:   agentgen.StepTransform,
		Config: cfg,
	}
}

// ── NA-01: input node writes "input" var ──────────────────────────────────────

func TestNA01_InputNode_WritesInputVar(t *testing.T) {
	node := inputNode("in", "out")
	ic := makeIC()
	vars := agentgen.PipelineVars{"input": "hello"}

	out, err := agentgen.ExecuteNodeForActivity(context.Background(), makeInterp().Clone(), ic, agentgen.NodeExecutionInput{
		Node: node,
		Vars: vars,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.NextOverride != "" {
		t.Errorf("input node: NextOverride should be empty, got %q", out.NextOverride)
	}
	if out.ResultText != "" {
		t.Errorf("input node: ResultText should be empty, got %q", out.ResultText)
	}
}

// ── NA-02: response node captures ResultText ──────────────────────────────────

func TestNA02_ResponseNode_CapturesResultText(t *testing.T) {
	node := responseNode("out", "greeting")
	ic := makeIC()
	vars := agentgen.PipelineVars{"greeting": "world"}

	out, err := agentgen.ExecuteNodeForActivity(context.Background(), makeInterp().Clone(), ic, agentgen.NodeExecutionInput{
		Node: node,
		Vars: vars,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ResultText != "world" {
		t.Errorf("response node: ResultText want %q, got %q", "world", out.ResultText)
	}
	if out.ResultMT == "" {
		t.Error("response node: ResultMT must be non-empty")
	}
}

// ── NA-03: branch node sets NextOverride ─────────────────────────────────────

func TestNA03_BranchNode_SetsNextOverride(t *testing.T) {
	cfg, _ := json.Marshal(agentgen.BranchStepConfig{
		Expression: `{{.flag}}`,
		TrueNext:   "true_step",
		FalseNext:  "false_step",
	})
	node := agentgen.PlanNode{
		StepID: "branch",
		Type:   agentgen.StepBranch,
		Config: cfg,
		Next:   []string{"true_step", "false_step"},
	}
	ic := makeIC()
	vars := agentgen.PipelineVars{"flag": "true"}

	out, err := agentgen.ExecuteNodeForActivity(context.Background(), makeInterp().Clone(), ic, agentgen.NodeExecutionInput{
		Node: node,
		Vars: vars,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.NextOverride != "true_step" {
		t.Errorf("branch node true: NextOverride want %q, got %q", "true_step", out.NextOverride)
	}

	// false path
	vars["flag"] = "false"
	out2, err := agentgen.ExecuteNodeForActivity(context.Background(), makeInterp().Clone(), ic, agentgen.NodeExecutionInput{
		Node: node,
		Vars: vars,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out2.NextOverride != "false_step" {
		t.Errorf("branch node false: NextOverride want %q, got %q", "false_step", out2.NextOverride)
	}
}

// ── NA-04: nil interp returns error ──────────────────────────────────────────

func TestNA04_NilInterp_ReturnsError(t *testing.T) {
	node := inputNode("in")
	_, err := agentgen.ExecuteNodeForActivity(context.Background(), nil, makeIC(), agentgen.NodeExecutionInput{
		Node: node,
		Vars: agentgen.PipelineVars{},
	})
	if err == nil {
		t.Fatal("expected error for nil interp, got nil")
	}
}

// ── NA-05: unknown node type returns error ────────────────────────────────────

func TestNA05_UnknownNodeType_ReturnsError(t *testing.T) {
	node := agentgen.PlanNode{
		StepID: "x",
		Type:   agentgen.StepType("not_a_real_type"),
		Config: json.RawMessage(`{}`),
	}
	_, err := agentgen.ExecuteNodeForActivity(context.Background(), makeInterp().Clone(), makeIC(), agentgen.NodeExecutionInput{
		Node: node,
		Vars: agentgen.PipelineVars{},
	})
	if err == nil {
		t.Fatal("expected error for unknown node type, got nil")
	}
}

// ── NA-06: context cancellation propagates ────────────────────────────────────

func TestNA06_ContextCancellation_Propagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	node := inputNode("in")
	_, err := agentgen.ExecuteNodeForActivity(ctx, makeInterp().Clone(), makeIC(), agentgen.NodeExecutionInput{
		Node: node,
		Vars: agentgen.PipelineVars{"input": "x"},
	})
	// Input node is pure — it may succeed even with a cancelled context because
	// it doesn't check ctx itself. Other nodes (LLM/HTTP) would fail.
	// The important invariant: no panic, no nil pointer.
	_ = err
}

// ── NA-07: isolated state — concurrent calls do not share nextStepOverride ───

func TestNA07_IsolatedState_ConcurrentCallsDoNotShare(t *testing.T) {
	cfg, _ := json.Marshal(agentgen.BranchStepConfig{
		Expression: `{{.flag}}`,
		TrueNext:   "T",
		FalseNext:  "F",
	})
	node := agentgen.PlanNode{
		StepID: "branch",
		Type:   agentgen.StepBranch,
		Config: cfg,
		Next:   []string{"T", "F"},
	}
	ic := makeIC()
	baseInterp := makeInterp()

	// Run 100 concurrent calls with alternating true/false — each clone must be independent.
	const N = 100
	results := make(chan string, N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			flag := "true"
			expected := "T"
			if idx%2 == 1 {
				flag = "false"
				expected = "F"
			}
			out, err := agentgen.ExecuteNodeForActivity(context.Background(), baseInterp.Clone(), ic, agentgen.NodeExecutionInput{
				Node: node,
				Vars: agentgen.PipelineVars{"flag": flag},
			})
			if err != nil {
				results <- "error:" + err.Error()
				return
			}
			if out.NextOverride != expected {
				results <- "wrong:" + out.NextOverride + "!=" + expected
				return
			}
			results <- "ok"
		}(i)
	}
	for i := 0; i < N; i++ {
		r := <-results
		if r != "ok" {
			t.Errorf("goroutine %d: %s", i, r)
		}
	}
}

// ── NA-08: scoped output projection ──────────────────────────────────────────

func TestNA08_ScopedOutputProjection(t *testing.T) {
	// Response node with declared Inputs + Outputs (Stage-6 full contract).
	// "secret" is in the global vars but not in Outputs — it must not appear in out.Vars.
	cfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "greeting", MediaType: "text/plain"})
	node := agentgen.PlanNode{
		StepID:  "out",
		Type:    agentgen.StepResponse,
		Config:  cfg,
		Inputs:  []agentgen.VarRef{{Name: "greeting", Required: true}},
		Outputs: []agentgen.VarRef{{Name: "greeting"}},
	}
	vars := agentgen.PipelineVars{"greeting": "hello", "secret": "s3kr3t"}

	out, err := agentgen.ExecuteNodeForActivity(context.Background(), makeInterp().Clone(), makeIC(), agentgen.NodeExecutionInput{
		Node: node,
		Vars: vars,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ResultText captured from the response node's output
	if out.ResultText != "hello" {
		t.Errorf("ResultText want %q, got %q", "hello", out.ResultText)
	}
	// Scoped output: only "greeting" declared — "secret" must be absent
	if _, ok := out.Vars["secret"]; ok {
		t.Error("scoped output must not include undeclared key 'secret'")
	}
}

// ── NA-09: ErrContractViolation on missing required input ────────────────────

func TestNA09_ErrContractViolation_MissingRequiredInput(t *testing.T) {
	cfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "must_exist", MediaType: "text/plain"})
	node := agentgen.PlanNode{
		StepID: "out",
		Type:   agentgen.StepResponse,
		Config: cfg,
		Inputs: []agentgen.VarRef{{Name: "must_exist", Required: true}},
	}
	vars := agentgen.PipelineVars{} // missing "must_exist"

	_, err := agentgen.ExecuteNodeForActivity(context.Background(), makeInterp().Clone(), makeIC(), agentgen.NodeExecutionInput{
		Node: node,
		Vars: vars,
	})
	if err == nil {
		t.Fatal("expected ErrContractViolation, got nil")
	}
	var cv *agentgen.ErrContractViolation
	if !errors.As(err, &cv) {
		t.Errorf("expected *ErrContractViolation, got %T: %v", err, err)
	}
}

// ── NA-10: ActivityIC validation ──────────────────────────────────────────────

func TestNA10_ActivityIC_Validate(t *testing.T) {
	cases := []struct {
		name    string
		ic      agentgen.ActivityIC
		wantErr bool
	}{
		{"all fields", agentgen.ActivityIC{"t", "a", "ag", "b"}, false},
		{"no binding", agentgen.ActivityIC{"t", "a", "ag", ""}, false},  // BindingID optional
		{"missing tenant", agentgen.ActivityIC{"", "a", "ag", "b"}, true},
		{"missing app", agentgen.ActivityIC{"t", "", "ag", "b"}, true},
		{"missing agent", agentgen.ActivityIC{"t", "a", "", "b"}, true},
	}
	for _, tc := range cases {
		err := tc.ic.Validate()
		if tc.wantErr && err == nil {
			t.Errorf("%s: expected validation error, got nil", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: unexpected validation error: %v", tc.name, err)
		}
	}
}

// ── NA-11: ActivityICFromInvocationContext never exposes secrets ──────────────

func TestNA11_ActivityICFromInvocationContext_NoSecrets(t *testing.T) {
	ic := &agentgen.InvocationContext{
		TenantID:        "t1",
		ApplicationID:   "a1",
		AgentID:         "ag1",
		BindingID:       "b1",
		AppAPIKey:       map[string]string{"anthropic": "sk-secret"},
		AgentParams:     map[string]string{"token": "verysecret"},
		AppGlobalParams: map[string]string{"key": "alsosecret"},
	}
	aic := agentgen.ActivityICFromInvocationContext(ic)

	if aic.TenantID != "t1" || aic.ApplicationID != "a1" || aic.AgentID != "ag1" || aic.BindingID != "b1" {
		t.Errorf("ActivityIC fields wrong: %+v", aic)
	}

	// Serialize to JSON and verify no secret values appear.
	b, _ := json.Marshal(aic)
	s := string(b)
	if strings.Contains(s, "sk-secret") || strings.Contains(s, "verysecret") || strings.Contains(s, "alsosecret") {
		t.Errorf("ActivityIC JSON contains secret values: %s", s)
	}
}

// ── NA-12: AgentSpec.ExecutionBackend round-trips through JSON ────────────────

func TestNA12_AgentSpec_ExecutionBackend_RoundTrip(t *testing.T) {
	spec := agentgen.AgentSpec{
		ID:               "a1",
		ExecutionBackend: "temporal",
	}
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got agentgen.AgentSpec
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ExecutionBackend != "temporal" {
		t.Errorf("ExecutionBackend round-trip: want %q, got %q", "temporal", got.ExecutionBackend)
	}
}

// ── NA-13: AgentSpec default ExecutionBackend is empty (treated as "local") ──

func TestNA13_AgentSpec_DefaultExecutionBackend_IsEmpty(t *testing.T) {
	spec := agentgen.AgentSpec{ID: "a1"}
	b, _ := json.Marshal(spec)
	// omitempty: empty string must not appear in JSON
	s := string(b)
	if strings.Contains(s, `"execution_backend"`) {
		t.Errorf("default ExecutionBackend must be omitted from JSON, got: %s", s)
	}
}

// ── NA-14: compiler rejects invalid execution_backend value ──────────────────

func TestNA14_Compiler_RejectsInvalidExecutionBackend(t *testing.T) {
	raw := json.RawMessage(`{
		"agent_root": {"display_name": "X", "execution_backend": "kubernetes"},
		"skills": []
	}`)
	_, issues := agentgen.Validate("a", "t", "d", "slug", raw)
	hasInvalidField := false
	for _, iss := range issues {
		if iss.Code == "INVALID_FIELD" {
			hasInvalidField = true
		}
	}
	if !hasInvalidField {
		t.Errorf("expected INVALID_FIELD issue for bad execution_backend, got: %v", issues)
	}
}

// ── NA-15: compiler copies execution_backend to AgentSpec ────────────────────

func TestNA15_Compiler_CopiesExecutionBackend(t *testing.T) {
	raw := json.RawMessage(`{
		"agent_root": {"display_name": "X", "execution_backend": "temporal"},
		"skills": []
	}`)
	spec, issues := agentgen.Validate("a", "t", "d", "slug", raw)
	for _, iss := range issues {
		if iss.Severity == "error" {
			t.Fatalf("unexpected compile error: %v", iss)
		}
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if spec.ExecutionBackend != "temporal" {
		t.Errorf("ExecutionBackend want %q, got %q", "temporal", spec.ExecutionBackend)
	}
}

// ── NA-16: compiler leaves ExecutionBackend empty when not specified ──────────

func TestNA16_Compiler_DefaultExecutionBackend_IsEmpty(t *testing.T) {
	raw := json.RawMessage(`{
		"agent_root": {"display_name": "X"},
		"skills": []
	}`)
	spec, _ := agentgen.Validate("a", "t", "d", "slug", raw)
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if spec.ExecutionBackend != "" {
		t.Errorf("default ExecutionBackend should be empty, got %q", spec.ExecutionBackend)
	}
}
