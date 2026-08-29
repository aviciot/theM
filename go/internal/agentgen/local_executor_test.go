package agentgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

// Silence "imported and not used" for errors in the cancellation test.
var _ = errors.New

// ── Test node helpers ─────────────────────────────────────────────────────────

// registerTestNode registers def and schedules cleanup so the type is removed
// after the test finishes. This prevents pollution of the global registry that
// would break TestNodeRegistry_KnownStepTypesCount.
func registerTestNode(t *testing.T, def NodeDef) {
	t.Helper()
	RegisterNode(def)
	t.Cleanup(func() { delete(nodeRegistry, def.Type) })
}

// registerEchoNode registers a step type that copies one var key to another.
func registerEchoNode(t *testing.T, typ StepType, sourceKey, destKey string) {
	t.Helper()
	registerTestNode(t, NodeDef{
		Type:        typ,
		Label:       string(typ),
		Version:     1,
		OutputArity: "single",
		Execute:     makeEchoExecute(sourceKey, destKey),
		Edges:       EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 0, MaxOut: 0},
	})
}

func makeEchoExecute(sourceKey, destKey string) func(context.Context, *Interpreter, *InvocationContext, *StepSpec, PipelineVars, *ExecutionResult) error {
	return func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
		if v, ok := vars[sourceKey]; ok {
			vars[destKey] = v
		}
		return nil
	}
}

// registerCounterNode registers a step type that atomically increments a counter
// and writes its index as vars[destKey]. Used to verify both branches run.
func registerCounterNode(t *testing.T, typ StepType, counter *atomic.Int32, destKey string) {
	t.Helper()
	registerTestNode(t, NodeDef{
		Type:        typ,
		Label:       string(typ),
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			idx := counter.Add(1)
			vars[destKey] = fmt.Sprintf("%s-%d", typ, idx)
			return nil
		},
		Edges: EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 0, MaxOut: 0},
	})
}

// registerErrorNode registers a step type that always returns an error.
func registerErrorNode(t *testing.T, typ StepType, msg string) {
	t.Helper()
	registerTestNode(t, NodeDef{
		Type:        typ,
		Label:       string(typ),
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			return errors.New(msg)
		},
		Edges: EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 0, MaxOut: 0},
	})
}

// registerResponseNode registers a minimal response step that captures vars[fromKey].
func registerResponseNode(t *testing.T, typ StepType, fromKey string) {
	t.Helper()
	registerTestNode(t, NodeDef{
		Type:        typ,
		Label:       string(typ),
		Version:     1,
		OutputArity: "none",
		IsSink:      true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			if v, ok := vars[fromKey]; ok {
				result.Text = fmt.Sprintf("%v", v)
				result.MediaType = "text/plain"
			}
			return nil
		},
		Edges: EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 0, MaxOut: 0},
	})
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("rawJSON: %v", err)
	}
	return b
}

func testInterp() *Interpreter {
	return NewInterpreter(nil, nil, "")
}

func testIC() *InvocationContext {
	return &InvocationContext{TenantID: "test"}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestLocalExecutor_Linear verifies a simple A→B→C chain executes in order.
func TestLocalExecutor_Linear(t *testing.T) {
	registerEchoNode(t, "test_echo_a", "input", "step_a_out")
	registerEchoNode(t, "test_echo_b", "step_a_out", "step_b_out")
	registerResponseNode(t, "test_resp_lin", "step_b_out")

	plan := &ExecutionPlan{
		SkillID: "lin",
		StartID: "s1",
		Nodes: []*PlanNode{
			{StepID: "s1", Type: "test_echo_a", Config: rawJSON(t, nil), Next: []string{"s2"}, JoinMode: JoinNone},
			{StepID: "s2", Type: "test_echo_b", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s3", Type: "test_resp_lin", Config: rawJSON(t, nil), JoinMode: JoinNone},
		},
	}

	exec := NewLocalExecutor(testInterp())
	res, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"input": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "hello" {
		t.Errorf("result text: got %q want %q", res.Text, "hello")
	}
}

// TestLocalExecutor_FanOut verifies that both branches of a fan-out execute.
// s1 → s2a (counter) + s2b (counter) → s3 (join → response)
func TestLocalExecutor_FanOut(t *testing.T) {
	var counter atomic.Int32
	registerEchoNode(t, "test_echo_a", "input", "step_a_out") // s1 — passthrough
	registerCounterNode(t, "test_fanout_a", &counter, "branch_a")
	registerCounterNode(t, "test_fanout_b", &counter, "branch_b")
	registerResponseNode(t, "test_resp_fo", "input")

	plan := &ExecutionPlan{
		SkillID: "fanout",
		StartID: "s1",
		Nodes: []*PlanNode{
			{StepID: "s1", Type: "test_echo_a", Config: rawJSON(t, nil), Next: []string{"s2a", "s2b"}, JoinMode: JoinNone},
			{StepID: "s2a", Type: "test_fanout_a", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s2b", Type: "test_fanout_b", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s3", Type: "test_resp_fo", Config: rawJSON(t, nil), JoinMode: JoinWaitAll, JoinOf: []string{"s2a", "s2b"}},
		},
	}

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"input": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both branches must have run — counter should be 2.
	if got := counter.Load(); got != 2 {
		t.Errorf("expected both branches to run (counter=2), got %d", got)
	}
}

// TestLocalExecutor_Join_WaitAll verifies that vars from both branches are
// merged at the join node and subsequent steps can read them.
func TestLocalExecutor_Join_WaitAll(t *testing.T) {
	// s1 fans out to s2a and s2b.
	// s2a writes vars["from_a"] = "value_a"
	// s2b writes vars["from_b"] = "value_b"
	// s3 (join) reads both; s4 response captures "from_a".
	registerTestNode(t, NodeDef{
		Type:        "test_write_a",
		Label:       "test_write_a",
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			vars["from_a"] = "value_a"
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_write_b",
		Label:       "test_write_b",
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			vars["from_b"] = "value_b"
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_passthrough",
		Label:       "test_passthrough",
		Version:     1,
		OutputArity: "single",
		Execute:     func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error { return nil },
		Edges:       EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_resp_join",
		Label:       "test_resp_join",
		Version:     1,
		OutputArity: "none",
		IsSink:      true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			a, _ := vars["from_a"].(string)
			b, _ := vars["from_b"].(string)
			result.Text = a + "+" + b
			result.MediaType = "text/plain"
			return nil
		},
		Edges: EdgeRules{},
	})

	plan := &ExecutionPlan{
		SkillID: "join-test",
		StartID: "s1",
		Nodes: []*PlanNode{
			{StepID: "s1", Type: "test_passthrough", Config: rawJSON(t, nil), Next: []string{"s2a", "s2b"}, JoinMode: JoinNone},
			{StepID: "s2a", Type: "test_write_a", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s2b", Type: "test_write_b", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s3", Type: "test_passthrough", Config: rawJSON(t, nil), Next: []string{"s4"}, JoinMode: JoinWaitAll, JoinOf: []string{"s2a", "s2b"}},
			{StepID: "s4", Type: "test_resp_join", Config: rawJSON(t, nil), JoinMode: JoinNone},
		},
	}

	exec := NewLocalExecutor(testInterp())
	res, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"input": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "value_a+value_b" {
		t.Errorf("merged result: got %q want %q", res.Text, "value_a+value_b")
	}
}

// TestLocalExecutor_JoinFailure_CancelsOtherBranch verifies that when one branch
// errors, the other branch is cancelled and the error is returned.
func TestLocalExecutor_JoinFailure_CancelsOtherBranch(t *testing.T) {
	registerErrorNode(t, "test_fail_node", "branch failed")
	registerTestNode(t, NodeDef{
		Type:        "test_passthrough",
		Label:       "test_passthrough",
		Version:     1,
		OutputArity: "single",
		Execute:     func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error { return nil },
		Edges:       EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_slow_node",
		Label:       "test_slow_node",
		Version:     1,
		OutputArity: "single",
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Edges: EdgeRules{},
	})

	plan := &ExecutionPlan{
		SkillID: "fail-test",
		StartID: "s1",
		Nodes: []*PlanNode{
			{StepID: "s1", Type: "test_passthrough", Config: rawJSON(t, nil), Next: []string{"s_fail", "s_slow"}, JoinMode: JoinNone},
			{StepID: "s_fail", Type: "test_fail_node", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s_slow", Type: "test_slow_node", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s3", Type: "test_passthrough", Config: rawJSON(t, nil), JoinMode: JoinWaitAll, JoinOf: []string{"s_fail", "s_slow"}},
		},
	}

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"input": ""})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errors.New("branch failed")) && err.Error() == "" {
		// just verify we got an error — message check
	}
	t.Logf("got expected error: %v", err)
}

// TestDeepCopyVars_NestedMap verifies that deep copy isolates nested maps.
func TestDeepCopyVars_NestedMap(t *testing.T) {
	original := PipelineVars{
		"flat": "hello",
		"nested": map[string]any{
			"a": "1",
			"inner": map[string]any{
				"x": "deep",
			},
		},
		"list": []any{"one", "two", map[string]any{"k": "v"}},
	}

	copied := deepCopyVars(original)

	// Mutate the copy — original must not be affected.
	copied["flat"] = "changed"
	copied["nested"].(map[string]any)["a"] = "mutated"
	copied["nested"].(map[string]any)["inner"].(map[string]any)["x"] = "mutated-deep"
	copied["list"].([]any)[0] = "mutated-list"
	copied["list"].([]any)[2].(map[string]any)["k"] = "mutated-map-in-list"

	if original["flat"] != "hello" {
		t.Errorf("flat key mutated in original")
	}
	orig := original["nested"].(map[string]any)
	if orig["a"] != "1" {
		t.Errorf("nested[a] mutated in original")
	}
	if orig["inner"].(map[string]any)["x"] != "deep" {
		t.Errorf("nested[inner][x] mutated in original")
	}
	origList := original["list"].([]any)
	if origList[0] != "one" {
		t.Errorf("list[0] mutated in original")
	}
	if origList[2].(map[string]any)["k"] != "v" {
		t.Errorf("list[2][k] mutated in original")
	}
}

// TestLocalExecutor_Nil verifies nil/empty plan returns an error cleanly.
func TestLocalExecutor_Nil(t *testing.T) {
	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), nil, PipelineVars{})
	if err == nil {
		t.Fatal("expected error for nil plan")
	}
	_, err2 := exec.Execute(context.Background(), testIC(), &ExecutionPlan{}, PipelineVars{})
	if err2 == nil {
		t.Fatal("expected error for empty plan")
	}
}
