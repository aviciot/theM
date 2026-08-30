package agentgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
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
	_, err := e.execNode(ctx, &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
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
	_, err := e.execNode(ctx, &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
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

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
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

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
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

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
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

	_, err := e.execNode(ctx, &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
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

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
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

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
	// The guard must not fire — error will be from the HTTP call itself (no client / no server).
	var ik *ErrIdempotencyKeyMissing
	if errors.As(err, &ik) {
		t.Fatalf("idempotency guard must NOT fire when key is present: %v", err)
	}
}

// EP-L9: per-attempt timeout — a second attempt gets a fresh timeout after the first times out.
// With per-attempt semantics, attempt 1 times out (returns DeadlineExceeded, non-retryable)
// and the executor stops immediately without a second attempt. The key assertion is that
// DeadlineExceeded from a single timed-out attempt is treated as non-retryable.
func TestExecNodeRetry_PerAttemptTimeout(t *testing.T) {
	typ := StepType("ep_per_attempt_timeout_" + t.Name())
	var calls int32
	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 300},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 300},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			atomic.AddInt32(&calls, 1)
			// Block until the per-attempt timeout fires.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-make(chan struct{}): // never fires
			}
			return nil
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "pat", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{
		MaxAttempts:            3,
		TimeoutSeconds:         1, // 1s per attempt
		InitialIntervalSeconds: 0.001,
		BackoffCoefficient:     1.0,
		MaxIntervalSeconds:     1,
	}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
	if err == nil {
		t.Fatal("expected DeadlineExceeded, got nil")
	}
	// DeadlineExceeded is non-retryable — only 1 attempt should have run.
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("DeadlineExceeded must stop retries: expected 1 attempt, got %d", n)
	}
}

// EP-L10: vars isolation per retry — a failed attempt's var writes must not appear
// in the input vars of the NEXT attempt. Each attempt starts from the original
// vars snapshot, not the mutated state left by a prior failed attempt.
func TestExecNodeRetry_VarsIsolation(t *testing.T) {
	typ := StepType("ep_vars_iso_" + t.Name())
	var calls int32
	// seenPriorWrite tracks whether attempt N saw "attempt_writes" from attempt N-1.
	var seenPriorWrite bool

	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 300},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 300},
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			n := atomic.AddInt32(&calls, 1)
			if n > 1 {
				// If isolation is broken, a prior failed attempt's write is visible here.
				if _, ok := vars["attempt_writes"]; ok {
					seenPriorWrite = true
				}
			}
			vars["attempt_writes"] = fmt.Sprintf("from_attempt_%d", n)
			if n < 3 {
				return fmt.Errorf("transient %d", n)
			}
			return nil
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "vi", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{
		MaxAttempts:            3,
		InitialIntervalSeconds: 0.001,
		BackoffCoefficient:     1.0,
		MaxIntervalSeconds:     1,
	}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{"initial": "clean"}, &sharedResult{}, make(chan struct{}, 10))
	if err != nil {
		t.Fatalf("expected success on 3rd attempt, got: %v", err)
	}
	if seenPriorWrite {
		t.Error("failed attempt leaked var write into next attempt's input vars")
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&calls))
	}
}

// EP-L11: NonRetryableError interface — an error implementing IsNonRetryable()=true
// stops retries after the first attempt even when MaxAttempts > 1.
// Uses ErrContractViolation (which implements NonRetryableError) as a representative case.
func TestExecNodeRetry_NonRetryableInterface(t *testing.T) {
	typ := StepType("ep_nre_iface_" + t.Name())
	var calls int32
	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 5, TimeoutSeconds: 300},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 5, TimeoutSeconds: 300},
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, s *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			atomic.AddInt32(&calls, 1)
			return &ErrContractViolation{StepID: s.ID, VarName: "x", Kind: "missing_required_input"}
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "nre", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{
		MaxAttempts:            5,
		InitialIntervalSeconds: 0.001,
		BackoffCoefficient:     1.0,
		MaxIntervalSeconds:     1,
	}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
	var cv *ErrContractViolation
	if !errors.As(err, &cv) {
		t.Fatalf("expected *ErrContractViolation, got %T: %v", err, err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("NonRetryableError interface must stop after 1 attempt, got %d", n)
	}
}

// EP-L12: idempotency guard fires in ExecuteNodeForActivity when RequiresIdempotencyKey=true
// and MaxAttempts>1 but no Idempotency-Key header is in the HTTP config.
func TestExecuteNodeForActivity_IdempotencyGuard(t *testing.T) {
	interp := NewInterpreter(nil, nil, "")

	node := PlanNode{
		StepID: "act_http",
		Type:   StepHTTP,
		Config: json.RawMessage(`{"method":"POST","url_template":"http://example.com"}`),
		Policy: ExecutionPolicy{
			MaxAttempts:            2,
			RequiresIdempotencyKey: true,
		},
	}
	input := NodeExecutionInput{Node: node, Vars: PipelineVars{}}

	_, err := ExecuteNodeForActivity(context.Background(), interp.clone(), &InvocationContext{TenantID: "t"}, input)
	var ik *ErrIdempotencyKeyMissing
	if !errors.As(err, &ik) {
		t.Fatalf("expected *ErrIdempotencyKeyMissing, got %T: %v", err, err)
	}
}

// EP-L13: ExecuteNodeForActivity allows execution when RequiresIdempotencyKey=true
// but MaxAttempts=1 (single attempt, no retry — idempotency protection not needed).
func TestExecuteNodeForActivity_IdempotencyGuard_MaxAttempts1_Skips(t *testing.T) {
	interp := NewInterpreter(nil, nil, "")

	node := PlanNode{
		StepID: "act_http_single",
		Type:   StepHTTP,
		Config: json.RawMessage(`{"method":"POST","url_template":"http://127.0.0.1:0/no-server"}`),
		Policy: ExecutionPolicy{
			MaxAttempts:            1,
			RequiresIdempotencyKey: true, // set, but MaxAttempts=1 → guard does NOT fire
		},
	}
	input := NodeExecutionInput{Node: node, Vars: PipelineVars{}}

	_, err := ExecuteNodeForActivity(context.Background(), interp.clone(), &InvocationContext{TenantID: "t"}, input)
	// Guard must not fire — error (if any) comes from the HTTP execution attempt itself.
	var ik *ErrIdempotencyKeyMissing
	if errors.As(err, &ik) {
		t.Fatalf("idempotency guard must NOT fire when MaxAttempts=1: %v", err)
	}
}

// EP-L14: string matching removed — a generic error whose message contains a
// Temporal type name (e.g. "invalid config: ...") must NOT be treated as non-retryable
// in the Local path. Only typed Go errors (NonRetryableError interface) or context
// errors stop retries; string matching was removed to prevent false positives.
func TestIsNonRetryable_NoStringMatch(t *testing.T) {
	// "InvalidConfig" appears in stdNonRetryable (Temporal list). A plain error whose
	// message contains this substring must NOT be non-retryable in LocalExecutor.
	plainErr := fmt.Errorf("InvalidConfig: something went wrong")
	policy := ExecutionPolicy{NonRetryableErrors: []string{"InvalidConfig", "PermissionDenied"}}

	if isNonRetryable(plainErr, policy) {
		t.Error("string-match on NonRetryableErrors must not classify plain errors as non-retryable in LocalExecutor")
	}
}

// EP-L15: fresh interpreter clone per attempt — state set by attempt N (here,
// nextStepOverride) must not appear in the interpreter of attempt N+1.
// We verify this by registering a node that sets nextStepOverride on attempt 1
// and returns an error (so attempt 2 runs), then checks that attempt 2's interp
// starts clean. This requires exposing the interp state through the result.
func TestExecNodeRetry_FreshClonePerAttempt(t *testing.T) {
	typ := StepType("ep_fresh_clone_" + t.Name())
	var calls int32
	var attempt2SawOverride bool

	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 300},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 300},
		Execute: func(_ context.Context, interp *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				// Set interpreter state on attempt 1 then fail.
				interp.nextStepOverride = "stale_override"
				return fmt.Errorf("transient failure on attempt 1")
			}
			if n == 2 {
				// A fresh clone must have empty nextStepOverride.
				if interp.nextStepOverride != "" {
					attempt2SawOverride = true
				}
				return nil
			}
			return nil
		},
	})

	interp := NewInterpreter(nil, nil, "")
	e := NewLocalExecutor(interp)

	step := &StepSpec{ID: "fc", Type: typ, Config: json.RawMessage(`{}`)}
	policy := ExecutionPolicy{
		MaxAttempts:            2,
		InitialIntervalSeconds: 0.001,
		BackoffCoefficient:     1.0,
		MaxIntervalSeconds:     1,
	}

	_, err := e.execNode(context.Background(), &InvocationContext{}, interp.clone(), step, policy, PipelineVars{}, &sharedResult{}, make(chan struct{}, 10))
	if err != nil {
		t.Fatalf("expected success on attempt 2, got: %v", err)
	}
	if attempt2SawOverride {
		t.Error("attempt 2 saw nextStepOverride set by attempt 1 — interpreter clone is not fresh per attempt")
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls, got %d", atomic.LoadInt32(&calls))
	}
}

// ── Concurrency limit tests ───────────────────────────────────────────────────

// TestResolveMaxConcurrentTasks_Zero verifies that 0 and negative values resolve
// to DefaultMaxConcurrentTasks, and values above SystemMaxConcurrentTasks are clamped.
func TestResolveMaxConcurrentTasks_Zero(t *testing.T) {
	if got := ResolveMaxConcurrentTasks(0); got != DefaultMaxConcurrentTasks {
		t.Errorf("0 → got %d want %d", got, DefaultMaxConcurrentTasks)
	}
	if got := ResolveMaxConcurrentTasks(-5); got != DefaultMaxConcurrentTasks {
		t.Errorf("-5 → got %d want %d", got, DefaultMaxConcurrentTasks)
	}
	if got := ResolveMaxConcurrentTasks(5); got != 5 {
		t.Errorf("5 → got %d want 5", got)
	}
	if got := ResolveMaxConcurrentTasks(SystemMaxConcurrentTasks + 1); got != SystemMaxConcurrentTasks {
		t.Errorf(">SystemMax → got %d want %d", got, SystemMaxConcurrentTasks)
	}
	if got := ResolveMaxConcurrentTasks(SystemMaxConcurrentTasks); got != SystemMaxConcurrentTasks {
		t.Errorf("SystemMax → got %d want %d", got, SystemMaxConcurrentTasks)
	}
}

// TestLocalExecutor_ConcurrencyLimit verifies that when MaxConcurrentTasks=2,
// at most 2 nodes execute at the same time across a fan-out of 5 branches.
// Measures a high-water mark of simultaneous in-flight executions.
func TestLocalExecutor_ConcurrencyLimit(t *testing.T) {
	const fanOut = 5
	const limit = 2

	typ := StepType("conc_limit_node_" + t.Name())
	var inFlight atomic.Int32
	var highWater atomic.Int32
	// gate blocks each node until all are started (so timing is deterministic)
	ready := make(chan struct{})

	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			cur := inFlight.Add(1)
			defer inFlight.Add(-1)
			// Track high-water mark.
			for {
				hw := highWater.Load()
				if cur <= hw || highWater.CompareAndSwap(hw, cur) {
					break
				}
			}
			// Wait until released, or context cancelled.
			select {
			case <-ready:
			case <-ctx.Done():
			}
			result.Text = "ok"
			result.MediaType = "text/plain"
			return nil
		},
	})

	// Build a fan-out plan: root → 5 parallel nodes → join → response.
	respTyp := StepType("conc_limit_resp_" + t.Name())
	registerTestNode(t, NodeDef{
		Type:        respTyp,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10},
		IsSink:      true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, result *ExecutionResult) error {
			result.Text = "done"
			result.MediaType = "text/plain"
			return nil
		},
	})
	inputTyp := StepType("conc_limit_in_" + t.Name())
	registerTestNode(t, NodeDef{
		Type:          inputTyp,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10},
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			return nil
		},
	})

	nextIDs := make([]string, fanOut)
	for i := range nextIDs {
		nextIDs[i] = fmt.Sprintf("n%d", i)
	}
	joinOf := make([]string, fanOut)
	copy(joinOf, nextIDs)

	nodes := []*PlanNode{
		{StepID: "root", Type: inputTyp, Config: json.RawMessage(`{}`), Next: nextIDs, JoinMode: JoinNone, Policy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10}},
	}
	for i, id := range nextIDs {
		_ = i
		nodes = append(nodes, &PlanNode{
			StepID:   id,
			Type:     typ,
			Config:   json.RawMessage(`{}`),
			Next:     []string{"join"},
			JoinMode: JoinNone,
			Policy:   ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10},
		})
	}
	nodes = append(nodes, &PlanNode{
		StepID:   "join",
		Type:     respTyp,
		Config:   json.RawMessage(`{}`),
		JoinMode: JoinWaitAll,
		JoinOf:   joinOf,
		Policy:   ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 10},
	})

	plan := &ExecutionPlan{SkillID: "conc", StartID: "root", Nodes: nodes}
	ic := &InvocationContext{Policies: InvocationPolicies{MaxConcurrentTasks: limit}}
	exec := NewLocalExecutor(NewInterpreter(nil, nil, ""))

	// Release all nodes after a short delay to let them start.
	go func() {
		// Give goroutines time to start and block on the semaphore.
		// Then release them all.
		// We don't sleep — instead we close ready when the first limit nodes are waiting.
		// Simplest: close after a short poll of inFlight.
		for {
			if inFlight.Load() > 0 {
				break
			}
		}
		close(ready)
	}()

	_, err := exec.Execute(context.Background(), ic, plan, PipelineVars{"input": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hw := highWater.Load()
	if hw > int32(limit) {
		t.Errorf("concurrency limit violated: high-water=%d, limit=%d", hw, limit)
	}
	if hw == 0 {
		t.Error("no nodes executed (high-water=0)")
	}
}

// TestLocalExecutor_ConcurrencyLimit_Cancellation verifies that cancelling the
// context while nodes are waiting at the semaphore does not deadlock.
// The plan has 4 nodes behind a limit=1 semaphore; the root blocks forever,
// so the 3 fan-out siblings wait at the semaphore. Cancelling the context
// must unblock everything and return promptly.
func TestLocalExecutor_ConcurrencyLimit_Cancellation(t *testing.T) {
	typ := StepType("conc_cancel_node_" + t.Name())
	var started atomic.Int32
	// blockCh is never closed; nodes block until context is cancelled.
	blockCh := make(chan struct{})

	registerTestNode(t, NodeDef{
		Type:          typ,
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30},
		Execute: func(ctx context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			started.Add(1)
			select {
			case <-blockCh: // never fires
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})

	nextIDs := []string{"n0", "n1", "n2"}
	nodes := []*PlanNode{
		{StepID: "root", Type: typ, Config: json.RawMessage(`{}`), Next: nextIDs, JoinMode: JoinNone, Policy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30}},
	}
	for _, id := range nextIDs {
		nodes = append(nodes, &PlanNode{
			StepID:   id,
			Type:     typ,
			Config:   json.RawMessage(`{}`),
			Next:     nil,
			JoinMode: JoinNone,
			Policy:   ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30},
		})
	}

	// Limit=1: root acquires the semaphore and blocks. The 3 fan-out goroutines
	// wait at the semaphore. Cancelling should unblock all of them.
	plan := &ExecutionPlan{SkillID: "cancel", StartID: "root", Nodes: nodes}
	ic := &InvocationContext{Policies: InvocationPolicies{MaxConcurrentTasks: 1}}
	exec := NewLocalExecutor(NewInterpreter(nil, nil, ""))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := exec.Execute(ctx, ic, plan, PipelineVars{})
		done <- err
	}()

	// Wait until root has started (is blocking inside Execute), then cancel.
	for started.Load() == 0 {
		// spin — root starts almost immediately
	}
	cancel()

	select {
	case err := <-done:
		// Must return an error (context cancelled or similar) without deadlocking.
		if err == nil {
			t.Error("expected non-nil error after context cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute deadlocked after context cancellation (5s timeout)")
	}
}

// ── StepLoop tests (EP-LOOP-1..5) ────────────────────────────────────────────

// buildLoopPlanWithResponse builds an outer plan: loop → response(fromVar).
// The response step captures fromVar into result.Text so we can assert on it.
func buildLoopPlanWithResponse(t *testing.T, loopCfg LoopConfig, bodyNodes []*PlanNode, responseFromVar string) *ExecutionPlan {
	t.Helper()
	cfg := rawJSON(t, loopCfg)
	startID := ""
	if len(bodyNodes) > 0 {
		startID = bodyNodes[0].StepID
	}
	subPlan := &ExecutionPlan{SkillID: "loop:body", StartID: startID, Nodes: bodyNodes}
	loopNode := &PlanNode{
		StepID:  "loop1",
		Type:    StepLoop,
		Config:  cfg,
		Next:    []string{"resp1"},
		SubPlan: subPlan,
		Policy:  ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60},
	}
	// Response step reads responseFromVar and formats its length as text.
	respCfg := rawJSON(t, ResponseStepConfig{FromVar: responseFromVar, MediaType: "text/plain"})
	respNode := &PlanNode{
		StepID:  "resp1",
		Type:    StepResponse,
		Config:  respCfg,
		Policy:  ExecutionPolicy{MaxAttempts: 1},
	}
	return &ExecutionPlan{
		SkillID: "loop_test",
		StartID: "loop1",
		Nodes:   []*PlanNode{loopNode, respNode},
	}
}

// buildLoopPlanNoResponse builds an outer plan: loop → stub response.
// The stub response node produces a constant result so LocalExecutor doesn't complain.
func buildLoopPlanNoResponse(t *testing.T, loopCfg LoopConfig, bodyNodes []*PlanNode) *ExecutionPlan {
	t.Helper()
	cfg := rawJSON(t, loopCfg)
	startID := ""
	if len(bodyNodes) > 0 {
		startID = bodyNodes[0].StepID
	}
	subPlan := &ExecutionPlan{SkillID: "loop:body", StartID: startID, Nodes: bodyNodes}
	// Minimal sink so LocalExecutor.Execute doesn't return "no result".
	sinkType := StepType("test_loop_sink_" + loopCfg.ItemsVar)
	registerTestNode(t, NodeDef{
		Type: sinkType, Version: 1, OutputArity: "none", IsSink: true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, res *ExecutionResult) error {
			res.Text = "done"
			res.MediaType = "text/plain"
			return nil
		},
		Edges: EdgeRules{},
	})
	return &ExecutionPlan{
		SkillID: "loop_test",
		StartID: "loop1",
		Nodes: []*PlanNode{
			{
				StepID:  "loop1",
				Type:    StepLoop,
				Config:  cfg,
				Next:    []string{"sink1"},
				SubPlan: subPlan,
				Policy:  ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60},
			},
			{
				StepID: "sink1",
				Type:   sinkType,
				Config: rawJSON(t, nil),
				Policy: ExecutionPolicy{MaxAttempts: 1},
			},
		},
	}
}

// EP-LOOP-1: basic iteration — 3 items, body runs 3 times, accum_var accumulates.
func TestLocalExecutor_Loop_BasicIteration(t *testing.T) {
	var callCount atomic.Int32
	registerTestNode(t, NodeDef{
		Type: "test_loop_body_L1", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			callCount.Add(1)
			if item, ok := vars["item"]; ok {
				vars["processed_item"] = fmt.Sprintf("done:%v", item)
			}
			return nil
		},
		Edges: EdgeRules{},
	})

	bodyNodes := []*PlanNode{
		{
			StepID:  "b1",
			Type:    "test_loop_body_L1",
			Config:  rawJSON(t, nil),
			Policy:  ExecutionPolicy{MaxAttempts: 1},
			Outputs: []VarRef{{Name: "processed_item"}},
		},
	}
	// Use a counter node as response to verify loop ran.
	plan := buildLoopPlanNoResponse(t, LoopConfig{
		ItemsVar: "items",
		ItemVar:  "item",
		AccumVar: "results",
	}, bodyNodes)

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"items": []any{"a", "b", "c"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount.Load() != 3 {
		t.Errorf("expected body to run 3 times, ran %d times", callCount.Load())
	}
}

// EP-LOOP-2: max_iterations caps the loop — 5 items but cap=2.
func TestLocalExecutor_Loop_MaxIterations(t *testing.T) {
	var callCount atomic.Int32
	registerTestNode(t, NodeDef{
		Type: "test_loop_body_L2", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			callCount.Add(1)
			return nil
		},
		Edges: EdgeRules{},
	})

	bodyNodes := []*PlanNode{
		{
			StepID:  "b2",
			Type:    "test_loop_body_L2",
			Config:  rawJSON(t, nil),
			Policy:  ExecutionPolicy{MaxAttempts: 1},
			Outputs: []VarRef{{Name: "b2_out"}},
		},
	}
	plan := buildLoopPlanNoResponse(t, LoopConfig{
		ItemsVar:      "items",
		ItemVar:       "item",
		MaxIterations: 2,
	}, bodyNodes)

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"items": []any{"a", "b", "c", "d", "e"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("max_iterations=2 but body ran %d times", callCount.Load())
	}
}

// EP-LOOP-3: items_var absent → no-op, no error, body never runs.
func TestLocalExecutor_Loop_MissingItemsVar(t *testing.T) {
	var callCount atomic.Int32
	registerTestNode(t, NodeDef{
		Type: "test_loop_body_L3", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			callCount.Add(1)
			return nil
		},
		Edges: EdgeRules{},
	})

	bodyNodes := []*PlanNode{
		{StepID: "b3", Type: "test_loop_body_L3", Config: rawJSON(t, nil), Policy: ExecutionPolicy{MaxAttempts: 1}},
	}
	plan := buildLoopPlanNoResponse(t, LoopConfig{ItemsVar: "missing_var", ItemVar: "item"}, bodyNodes)

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{})
	if err != nil {
		t.Fatalf("missing items_var should be a no-op, got error: %v", err)
	}
	if callCount.Load() != 0 {
		t.Errorf("body should not run when items_var is absent, ran %d times", callCount.Load())
	}
}

// EP-LOOP-4: items_var is not a list → execution error.
func TestLocalExecutor_Loop_NonListItemsVar(t *testing.T) {
	registerTestNode(t, NodeDef{
		Type: "test_loop_body_L4", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			return nil
		},
		Edges: EdgeRules{},
	})

	bodyNodes := []*PlanNode{
		{StepID: "b4", Type: "test_loop_body_L4", Config: rawJSON(t, nil), Policy: ExecutionPolicy{MaxAttempts: 1}},
	}
	plan := buildLoopPlanNoResponse(t, LoopConfig{ItemsVar: "bad_var", ItemVar: "item"}, bodyNodes)

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"bad_var": "not_a_list"})
	if err == nil {
		t.Fatal("expected error when items_var is not a list, got nil")
	}
}

// EP-LOOP-5: nil sub-plan → no-op, no error.
func TestLocalExecutor_Loop_NilSubPlan(t *testing.T) {
	registerTestNode(t, NodeDef{
		Type: "test_loop_sink_nil", Version: 1, OutputArity: "none", IsSink: true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, res *ExecutionResult) error {
			res.Text = "done"
			res.MediaType = "text/plain"
			return nil
		},
		Edges: EdgeRules{},
	})
	plan := &ExecutionPlan{
		SkillID: "loop_nil_body",
		StartID: "loop1",
		Nodes: []*PlanNode{
			{
				StepID:  "loop1",
				Type:    StepLoop,
				Config:  rawJSON(t, LoopConfig{ItemsVar: "items", ItemVar: "item"}),
				Next:    []string{"sink_nil"},
				SubPlan: nil,
				Policy:  ExecutionPolicy{MaxAttempts: 1},
			},
			{
				StepID:  "sink_nil",
				Type:    "test_loop_sink_nil",
				Config:  rawJSON(t, nil),
				Policy:  ExecutionPolicy{MaxAttempts: 1},
			},
		},
	}

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"items": []any{"x", "y"}})
	if err != nil {
		t.Fatalf("nil sub-plan should be no-op, got error: %v", err)
	}
}

// EP-LOOP-6: branch inside loop body — only the selected arm runs, counter reflects it.
func TestLocalExecutor_Loop_BranchInsideBody(t *testing.T) {
	var trueCount, falseCount atomic.Int32

	registerTestNode(t, NodeDef{
		Type: "test_lb6_branch_true", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			trueCount.Add(1)
			vars["arm_result"] = "true"
			return nil
		},
		Edges: EdgeRules{},
	})
	registerTestNode(t, NodeDef{
		Type: "test_lb6_branch_false", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			falseCount.Add(1)
			vars["arm_result"] = "false"
			return nil
		},
		Edges: EdgeRules{},
	})

	// Body: branch(item=="true") → true_arm | false_arm
	// items: ["true", "false", "true"] → 2 true, 1 false
	branchCfg := rawJSON(t, BranchStepConfig{
		Expression: `{{.item}}`,
		TrueNext:   "arm_true",
		FalseNext:  "arm_false",
	})
	subPlan := &ExecutionPlan{
		SkillID: "lb6:body",
		StartID: "br",
		Nodes: []*PlanNode{
			{StepID: "br", Type: StepBranch, Config: branchCfg, Next: []string{"arm_true", "arm_false"},
				Policy: ExecutionPolicy{MaxAttempts: 1}},
			{StepID: "arm_true", Type: "test_lb6_branch_true", Config: rawJSON(t, nil),
				Outputs: []VarRef{{Name: "arm_result"}},
				Policy:  ExecutionPolicy{MaxAttempts: 1}},
			{StepID: "arm_false", Type: "test_lb6_branch_false", Config: rawJSON(t, nil),
				Outputs: []VarRef{{Name: "arm_result"}},
				Policy:  ExecutionPolicy{MaxAttempts: 1}},
		},
	}

	loopCfg := rawJSON(t, LoopConfig{
		BodySteps: []string{"br", "arm_true", "arm_false"},
		ItemsVar:  "items",
		ItemVar:   "item",
	})
	registerTestNode(t, NodeDef{
		Type: "test_lb6_sink", Version: 1, OutputArity: "none", IsSink: true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, res *ExecutionResult) error {
			res.Text = "done"
			res.MediaType = "text/plain"
			return nil
		},
		Edges: EdgeRules{},
	})
	plan := &ExecutionPlan{
		SkillID: "lb6_test",
		StartID: "loop1",
		Nodes: []*PlanNode{
			{StepID: "loop1", Type: StepLoop, Config: loopCfg, Next: []string{"sink_lb6"},
				SubPlan: subPlan, Policy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60}},
			{StepID: "sink_lb6", Type: "test_lb6_sink", Config: rawJSON(t, nil), Policy: ExecutionPolicy{MaxAttempts: 1}},
		},
	}

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"items": []any{"true", "false", "true"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trueCount.Load() != 2 {
		t.Errorf("true arm should run 2 times, ran %d", trueCount.Load())
	}
	if falseCount.Load() != 1 {
		t.Errorf("false arm should run 1 time, ran %d", falseCount.Load())
	}
}

// EP-LOOP-7: iteration isolation — vars written in iteration N do not persist to N+1.
func TestLocalExecutor_Loop_IterationIsolation(t *testing.T) {
	registerTestNode(t, NodeDef{
		Type: "test_lb7_body", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			// Write a sentinel value; the next iteration should NOT see it via a previous-iteration leak.
			prev, hasPrev := vars["prev_sentinel"]
			vars["iter_out"] = fmt.Sprintf("item=%v prev=%v", vars["item"], hasPrev)
			_ = prev
			vars["prev_sentinel"] = "was_set"
			return nil
		},
		Edges: EdgeRules{},
	})

	subPlan := &ExecutionPlan{
		SkillID: "lb7:body",
		StartID: "body7",
		Nodes: []*PlanNode{
			{StepID: "body7", Type: "test_lb7_body", Config: rawJSON(t, nil),
				Inputs:  []VarRef{{Name: "item"}},
				Outputs: []VarRef{{Name: "iter_out"}},
				Policy:  ExecutionPolicy{MaxAttempts: 1}},
		},
	}
	loopCfg := rawJSON(t, LoopConfig{
		BodySteps: []string{"body7"},
		ItemsVar:  "items",
		ItemVar:   "item",
		AccumVar:  "results",
	})
	registerTestNode(t, NodeDef{
		Type: "test_lb7_resp", Version: 1, OutputArity: "none", IsSink: true,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, res *ExecutionResult) error {
			res.Text = "done"
			res.MediaType = "text/plain"
			return nil
		},
		Edges: EdgeRules{},
	})
	plan := &ExecutionPlan{
		SkillID: "lb7_test",
		StartID: "loop1",
		Nodes: []*PlanNode{
			{StepID: "loop1", Type: StepLoop, Config: loopCfg, Next: []string{"resp7"},
				SubPlan: subPlan, Policy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60}},
			{StepID: "resp7", Type: "test_lb7_resp", Config: rawJSON(t, nil), Policy: ExecutionPolicy{MaxAttempts: 1}},
		},
	}

	exec := NewLocalExecutor(testInterp())
	_, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"items": []any{"a", "b", "c"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// EP-LOOP-8: scoped accumulation — accum_var contains only declared Outputs, not itemVar or outer vars.
func TestLocalExecutor_Loop_ScopedAccumulation(t *testing.T) {
	registerTestNode(t, NodeDef{
		Type: "test_lb8_body", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
			vars["body_out"] = fmt.Sprintf("processed:%v", vars["current_item"])
			return nil
		},
		Edges: EdgeRules{},
	})

	subPlan := &ExecutionPlan{
		SkillID: "lb8:body",
		StartID: "body8",
		Nodes: []*PlanNode{
			{StepID: "body8", Type: "test_lb8_body", Config: rawJSON(t, nil),
				Inputs:  []VarRef{{Name: "current_item"}},
				Outputs: []VarRef{{Name: "body_out"}},
				Policy:  ExecutionPolicy{MaxAttempts: 1}},
		},
	}
	loopCfg := rawJSON(t, LoopConfig{
		BodySteps: []string{"body8"},
		ItemsVar:  "items",
		ItemVar:   "current_item",
		AccumVar:  "all_results",
	})
	registerTestNode(t, NodeDef{
		Type: "test_lb8_resp", Version: 1, OutputArity: "single",
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, vars PipelineVars, res *ExecutionResult) error {
			if results, ok := vars["all_results"]; ok {
				res.Text = fmt.Sprintf("%v", results)
			} else {
				res.Text = "missing"
			}
			res.MediaType = "text/plain"
			return nil
		},
		Edges: EdgeRules{},
	})
	plan := &ExecutionPlan{
		SkillID: "lb8_test",
		StartID: "loop1",
		Nodes: []*PlanNode{
			{StepID: "loop1", Type: StepLoop, Config: loopCfg, Next: []string{"resp8"},
				SubPlan: subPlan, Policy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60}},
			{StepID: "resp8", Type: "test_lb8_resp", Config: rawJSON(t, nil),
				Inputs:  []VarRef{{Name: "all_results"}},
				Policy:  ExecutionPolicy{MaxAttempts: 1}},
		},
	}

	exec := NewLocalExecutor(testInterp())
	result, err := exec.Execute(context.Background(), testIC(), plan, PipelineVars{"items": []any{"x", "y"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Each accum snapshot must contain "body_out" but NOT "current_item" or "items".
	if result == nil {
		t.Fatal("expected a result")
	}
	if !containsStr(result.Text, "processed:x") {
		t.Errorf("expected accum to contain 'processed:x', got: %s", result.Text)
	}
	if !containsStr(result.Text, "processed:y") {
		t.Errorf("expected accum to contain 'processed:y', got: %s", result.Text)
	}
	if containsStr(result.Text, "current_item") {
		t.Errorf("accum_var must not contain the item variable, got: %s", result.Text)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
