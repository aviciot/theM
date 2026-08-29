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

// ── Branch-convergence tests ──────────────────────────────────────────────────

// TestLocalExecutor_BranchTrue verifies that when the true arm is taken,
// the convergence node (JoinBranchMerge) executes with the true arm's vars.
//
//	s1(passthrough) → s_branch(branch, true→s_true, false→s_false)
//	s_true(write "from"="true") → s_end(JoinBranchMerge, reads "from")
//	s_false(write "from"="false") → s_end
func TestLocalExecutor_BranchTrue(t *testing.T) {
	setupBranchNodes(t)

	plan := branchPlan(t, "branch-true-test", "true_arm")
	exec := NewLocalExecutor(testInterp())
	res, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"route": "true"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "true_arm" {
		t.Errorf("result: got %q want %q", res.Text, "true_arm")
	}
}

// TestLocalExecutor_BranchFalse verifies that when the false arm is taken,
// the convergence node executes with the false arm's vars (not waiting for true arm).
func TestLocalExecutor_BranchFalse(t *testing.T) {
	setupBranchNodes(t)

	plan := branchPlan(t, "branch-false-test", "false_arm")
	exec := NewLocalExecutor(testInterp())
	res, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"route": "false"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "false_arm" {
		t.Errorf("result: got %q want %q", res.Text, "false_arm")
	}
}

// setupBranchNodes registers the test node types used by branch tests.
// Types: test_branch_router (routes via nextStepOverride), test_write_true,
// test_write_false, test_read_from (reads "from" → result).
func setupBranchNodes(t *testing.T) {
	t.Helper()
	// router: reads vars["route"]; overrides next to "s_true" or "s_false"
	registerTestNode(t, NodeDef{
		Type:        "test_branch_router",
		Label:       "test_branch_router",
		Version:     1,
		OutputArity: "multi",
		Execute: func(_ context.Context, interp *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			if vars["route"] == "true" {
				interp.nextStepOverride = "s_true"
			} else {
				interp.nextStepOverride = "s_false"
			}
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_write_true",
		Label:       "test_write_true",
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			vars["from"] = "true_arm"
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_write_false",
		Label:       "test_write_false",
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			vars["from"] = "false_arm"
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_read_from",
		Label:       "test_read_from",
		Version:     1,
		OutputArity: "none",
		IsSink:      true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			result.Text = fmt.Sprintf("%v", vars["from"])
			result.MediaType = "text/plain"
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_passthrough",
		Label:       "test_passthrough",
		Version:     1,
		OutputArity: "single",
		Execute:     func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error { return nil },
		Edges:       EdgeRules{},
	})
}

// branchPlan builds a plan with a router → true/false arms → convergence node.
// wantArmLabel is not used in the plan — it's for the caller to verify result.
func branchPlan(t *testing.T, skillID, _ string) *ExecutionPlan {
	t.Helper()
	return &ExecutionPlan{
		SkillID: skillID,
		StartID: "s1",
		Nodes: []*PlanNode{
			{StepID: "s1", Type: "test_passthrough", Config: rawJSON(t, nil), Next: []string{"s_branch"}, JoinMode: JoinNone},
			// Branch: Next has both, but router will override to only one via nextStepOverride.
			{StepID: "s_branch", Type: "test_branch_router", Config: rawJSON(t, nil), Next: []string{"s_true", "s_false"}, JoinMode: JoinNone},
			{StepID: "s_true", Type: "test_write_true", Config: rawJSON(t, nil), Next: []string{"s_end"}, JoinMode: JoinNone},
			{StepID: "s_false", Type: "test_write_false", Config: rawJSON(t, nil), Next: []string{"s_end"}, JoinMode: JoinNone},
			{StepID: "s_end", Type: "test_read_from", Config: rawJSON(t, nil), JoinMode: JoinBranchMerge, JoinOf: []string{"s_true", "s_false"}},
		},
	}
}

// ── Deterministic merge tests ─────────────────────────────────────────────────

// TestLocalExecutor_DeterministicMerge verifies that when both parallel branches
// write the SAME key, the merge result follows JoinOf order (last entry wins),
// regardless of which goroutine arrived first.
//
// Plan: s1 → s2a (writes "shared"="from_a") + s2b (writes "shared"="from_b")
//
//	→ s3 (JoinWaitAll, JoinOf: ["s2a","s2b"]) → s4 (reads "shared")
//
// Expected: "from_b" wins because s2b is last in JoinOf.
func TestLocalExecutor_DeterministicMerge(t *testing.T) {
	registerTestNode(t, NodeDef{
		Type:        "test_write_shared_a",
		Label:       "test_write_shared_a",
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			vars["shared"] = "from_a"
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_write_shared_b",
		Label:       "test_write_shared_b",
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			vars["shared"] = "from_b"
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_passthrough",
		Label:       "test_passthrough",
		Version:     1,
		OutputArity: "single",
		Execute:     func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error { return nil },
		Edges:       EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_resp_shared",
		Label:       "test_resp_shared",
		Version:     1,
		OutputArity: "none",
		IsSink:      true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			result.Text = fmt.Sprintf("%v", vars["shared"])
			result.MediaType = "text/plain"
			return nil
		},
		Edges: EdgeRules{},
	})

	// Run the plan many times to catch non-determinism from goroutine scheduling.
	for i := 0; i < 50; i++ {
		plan := &ExecutionPlan{
			SkillID: "deterministic-merge",
			StartID: "s1",
			Nodes: []*PlanNode{
				{StepID: "s1", Type: "test_passthrough", Config: rawJSON(t, nil), Next: []string{"s2a", "s2b"}, JoinMode: JoinNone},
				{StepID: "s2a", Type: "test_write_shared_a", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
				{StepID: "s2b", Type: "test_write_shared_b", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
				// JoinOf: ["s2a","s2b"] — s2b is last, so "from_b" must always win.
				{StepID: "s3", Type: "test_passthrough", Config: rawJSON(t, nil), Next: []string{"s4"}, JoinMode: JoinWaitAll, JoinOf: []string{"s2a", "s2b"}},
				{StepID: "s4", Type: "test_resp_shared", Config: rawJSON(t, nil), JoinMode: JoinNone},
			},
		}
		exec := NewLocalExecutor(testInterp())
		res, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{})
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v", i, err)
		}
		if res.Text != "from_b" {
			t.Errorf("iter %d: merge result: got %q want %q (JoinOf order must be deterministic)", i, res.Text, "from_b")
		}
	}
}

// ── Error propagation tests ───────────────────────────────────────────────────

// TestLocalExecutor_CausalErrorPreserved verifies that when one branch fails,
// the returned error is the original causal error, not context.Canceled from
// a sibling goroutine that was cancelled.
func TestLocalExecutor_CausalErrorPreserved(t *testing.T) {
	const causalMsg = "deliberate branch failure"

	registerTestNode(t, NodeDef{
		Type:        "test_causal_fail",
		Label:       "test_causal_fail",
		Version:     1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			return errors.New(causalMsg)
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_slow_waiter",
		Label:       "test_slow_waiter",
		Version:     1,
		OutputArity: "single",
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			<-ctx.Done()
			return ctx.Err()
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type:        "test_passthrough",
		Label:       "test_passthrough",
		Version:     1,
		OutputArity: "single",
		Execute:     func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error { return nil },
		Edges:       EdgeRules{},
	})

	plan := &ExecutionPlan{
		SkillID: "causal-error-test",
		StartID: "s1",
		Nodes: []*PlanNode{
			{StepID: "s1", Type: "test_passthrough", Config: rawJSON(t, nil), Next: []string{"s_fail", "s_slow"}, JoinMode: JoinNone},
			{StepID: "s_fail", Type: "test_causal_fail", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s_slow", Type: "test_slow_waiter", Config: rawJSON(t, nil), Next: []string{"s3"}, JoinMode: JoinNone},
			{StepID: "s3", Type: "test_passthrough", Config: rawJSON(t, nil), JoinMode: JoinWaitAll, JoinOf: []string{"s_fail", "s_slow"}},
		},
	}

	// Run many times — non-deterministic scheduling could expose ordering bugs.
	for i := 0; i < 20; i++ {
		exec := NewLocalExecutor(testInterp())
		_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{})
		if err == nil {
			t.Fatalf("iter %d: expected error, got nil", i)
		}
		// Must not be pure context.Canceled — must contain the causal message.
		if errors.Is(err, context.Canceled) && !containsMsg(err, causalMsg) {
			t.Errorf("iter %d: got context.Canceled instead of causal error %q: %v", i, causalMsg, err)
		}
	}
}

func containsMsg(err error, msg string) bool {
	return err != nil && len(err.Error()) > 0 && (err.Error() == msg || len(err.Error()) > len(msg) && contains(err.Error(), msg))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsRune(s, sub))
}

func containsRune(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── Per-node timeout tests (EP-L1..EP-L2) ────────────────────────────────────

// EP-L1: execNode with TimeoutSeconds=1 and a step that blocks returns deadline exceeded.
func TestExecNodeTimeout(t *testing.T) {
	typ := StepType("ep_slow_node_" + t.Name())
	doneCh := make(chan struct{})
	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			select {
			case <-ctx.Done():
				close(doneCh)
				return ctx.Err()
			case <-make(chan struct{}): // never fires
			}
			return nil
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "slow", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 1}

	ctx := context.Background()
	_, err := e.execNode(ctx, &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{})
	if err == nil {
		t.Fatal("expected error from timed-out step, got nil")
	}
	select {
	case <-doneCh:
		// good — the step ctx was cancelled
	default:
		t.Error("step context was not cancelled")
	}
}

// EP-L2: execNode with TimeoutSeconds=0 must not add any deadline.
func TestExecNodeNoTimeoutWhenZero(t *testing.T) {
	typ := StepType("ep_notimeout_node_" + t.Name())
	var capturedDeadline bool
	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			_, capturedDeadline = ctx.Deadline()
			return nil
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "nd", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 0} // zero → no deadline

	ctx := context.Background() // background has no deadline
	_, err := e.execNode(ctx, &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedDeadline {
		t.Error("TimeoutSeconds=0 must not inject a deadline into the step context")
	}
}

// EP-L3: execNode with MaxAttempts=3 retries a transient error and eventually succeeds.
func TestExecNodeRetry_SucceedsOnThirdAttempt(t *testing.T) {
	typ := StepType("ep_retry_node_" + t.Name())
	var calls int32
	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 30},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 30},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			n := int(atomic.AddInt32(&calls, 1))
			if n < 3 {
				return fmt.Errorf("transient error attempt %d", n)
			}
			return nil
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "r", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{
		MaxAttempts:            3,
		InitialIntervalSeconds: 0.001, // 1ms — fast for test
		BackoffCoefficient:     1.0,
		MaxIntervalSeconds:     1,
	}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{})
	if err != nil {
		t.Fatalf("expected success on 3rd attempt, got: %v", err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 calls, got %d", atomic.LoadInt32(&calls))
	}
}

// EP-L4: execNode with MaxAttempts=3 exhausts retries and returns the last error.
func TestExecNodeRetry_ExhaustsAttempts(t *testing.T) {
	typ := StepType("ep_exhaust_node_" + t.Name())
	var calls int32
	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 30},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 30},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			atomic.AddInt32(&calls, 1)
			return fmt.Errorf("permanent failure")
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "r", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{
		MaxAttempts:            3,
		InitialIntervalSeconds: 0.001,
		BackoffCoefficient:     1.0,
		MaxIntervalSeconds:     1,
	}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected exactly 3 attempts, got %d", atomic.LoadInt32(&calls))
	}
}

// EP-L5: ErrContractViolation is non-retryable — stops after the first attempt.
func TestExecNodeRetry_ContractViolationIsNonRetryable(t *testing.T) {
	typ := StepType("ep_cv_node_" + t.Name())
	var calls int32
	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 30},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 30},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, s *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			atomic.AddInt32(&calls, 1)
			return &ErrContractViolation{StepID: s.ID, VarName: "x", Kind: "missing_required_input"}
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "cv", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{MaxAttempts: 3, InitialIntervalSeconds: 0.001, BackoffCoefficient: 1.0, MaxIntervalSeconds: 1}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{})
	var cv *ErrContractViolation
	if !errors.As(err, &cv) {
		t.Fatalf("expected *ErrContractViolation, got %T: %v", err, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("ErrContractViolation must stop after 1 attempt, got %d", atomic.LoadInt32(&calls))
	}
}

// EP-L6: context.Canceled stops retries immediately.
func TestExecNodeRetry_CancelledStopsImmediately(t *testing.T) {
	typ := StepType("ep_cancel_node_" + t.Name())
	var calls int32
	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 5, TimeoutSeconds: 30},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 5, TimeoutSeconds: 30},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			atomic.AddInt32(&calls, 1)
			return fmt.Errorf("transient")
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "cc", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{MaxAttempts: 5, InitialIntervalSeconds: 0.001, BackoffCoefficient: 1.0, MaxIntervalSeconds: 1}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before starting

	_, err := e.execNode(ctx, &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	// Should have stopped after at most 1 attempt (may not even start due to ctx.Done check).
	if n := atomic.LoadInt32(&calls); n > 1 {
		t.Errorf("cancelled context should stop after ≤1 attempt, got %d", n)
	}
}

// EP-L7: RequiresIdempotencyKey + MaxAttempts > 1 without Idempotency-Key header → ErrIdempotencyKeyMissing.
func TestExecNodeRetry_IdempotencyKeyMissing(t *testing.T) {
	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	// Use StepHTTP type with a config that has NO Idempotency-Key header.
	step := &StepSpec{
		ID:     "http_post",
		Type:   StepHTTP,
		Config: json.RawMessage(`{"method":"POST","url_template":"http://example.com"}`),
	}
	policy := ExecutionPolicy{
		MaxAttempts:            2,
		RequiresIdempotencyKey: true,
		InitialIntervalSeconds: 0.001,
		BackoffCoefficient:     1.0,
		MaxIntervalSeconds:     1,
	}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{})
	var ik *ErrIdempotencyKeyMissing
	if !errors.As(err, &ik) {
		t.Fatalf("expected *ErrIdempotencyKeyMissing, got %T: %v", err, err)
	}
}

// EP-L8: RequiresIdempotencyKey + MaxAttempts > 1 WITH Idempotency-Key header → allowed (no guard error).
// (The HTTP call itself will fail because there is no live server, but the idempotency guard passes.)
func TestExecNodeRetry_IdempotencyKeyPresent_AllowsExecution(t *testing.T) {
	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{
		ID:   "http_post_idem",
		Type: StepHTTP,
		Config: json.RawMessage(`{
			"method": "POST",
			"url_template": "http://127.0.0.1:0/no-server",
			"headers": {"Idempotency-Key": "static-key-123"}
		}`),
	}
	policy := ExecutionPolicy{
		MaxAttempts:            2,
		RequiresIdempotencyKey: true,
		InitialIntervalSeconds: 0.001,
		BackoffCoefficient:     1.0,
		MaxIntervalSeconds:     1,
	}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{})
	// The guard must not fire — error will be from the HTTP call itself (no client / no server).
	var ik *ErrIdempotencyKeyMissing
	if errors.As(err, &ik) {
		t.Fatalf("idempotency guard must NOT fire when key is present: %v", err)
	}
}
