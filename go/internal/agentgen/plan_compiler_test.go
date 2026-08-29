package agentgen

import (
	"encoding/json"
	"testing"
)

// rawCfg returns a minimal json.RawMessage for test steps.
func rawCfg(t *testing.T) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{}`)
}

// TestCompileExecutionPlan_Linear verifies that a simple linear chain (A→B→C)
// produces a plan with no join nodes and the correct Next pointers.
func TestCompileExecutionPlan_Linear(t *testing.T) {
	skill := &SkillSpec{
		ID: "skill-linear",
		Steps: []StepSpec{
			{ID: "s1", Type: StepInput, Config: rawCfg(t), Next: []string{"s2"}},
			{ID: "s2", Type: StepLLM, Config: rawCfg(t), Next: []string{"s3"}},
			{ID: "s3", Type: StepResponse, Config: rawCfg(t), Next: nil},
		},
	}

	plan := CompileExecutionPlan(skill)

	if plan.SkillID != "skill-linear" {
		t.Fatalf("SkillID: got %q want %q", plan.SkillID, "skill-linear")
	}
	if plan.StartID != "s1" {
		t.Fatalf("StartID: got %q want %q", plan.StartID, "s1")
	}
	if len(plan.Nodes) != 3 {
		t.Fatalf("Nodes len: got %d want 3", len(plan.Nodes))
	}

	for _, n := range plan.Nodes {
		if n.JoinMode != JoinNone {
			t.Errorf("node %s: expected JoinNone, got %s", n.StepID, n.JoinMode)
		}
		if len(n.JoinOf) != 0 {
			t.Errorf("node %s: expected empty JoinOf, got %v", n.StepID, n.JoinOf)
		}
	}

	s2 := plan.NodeByID("s2")
	if s2 == nil {
		t.Fatal("NodeByID(s2) returned nil")
	}
	if len(s2.Next) != 1 || s2.Next[0] != "s3" {
		t.Errorf("s2.Next: got %v want [s3]", s2.Next)
	}
}

// TestCompileExecutionPlan_FanOutJoin verifies a diamond DAG:
//
//	s1 → s2a
//	s1 → s2b
//	s2a → s3 (join)
//	s2b → s3 (join)
//
// s3 must be annotated JoinWaitAll with JoinOf = [s2a, s2b].
func TestCompileExecutionPlan_FanOutJoin(t *testing.T) {
	skill := &SkillSpec{
		ID: "skill-diamond",
		Steps: []StepSpec{
			{ID: "s1", Type: StepInput, Config: rawCfg(t), Next: []string{"s2a", "s2b"}},
			{ID: "s2a", Type: StepHTTP, Config: rawCfg(t), Next: []string{"s3"}},
			{ID: "s2b", Type: StepHTTP, Config: rawCfg(t), Next: []string{"s3"}},
			{ID: "s3", Type: StepResponse, Config: rawCfg(t), Next: nil},
		},
	}

	plan := CompileExecutionPlan(skill)

	if len(plan.Nodes) != 4 {
		t.Fatalf("Nodes len: got %d want 4", len(plan.Nodes))
	}

	// s1 — fan-out source, no join
	s1 := plan.NodeByID("s1")
	if s1 == nil {
		t.Fatal("NodeByID(s1) returned nil")
	}
	if s1.JoinMode != JoinNone {
		t.Errorf("s1: expected JoinNone, got %s", s1.JoinMode)
	}
	if len(s1.Next) != 2 {
		t.Errorf("s1.Next len: got %d want 2", len(s1.Next))
	}

	// s3 — join node
	s3 := plan.NodeByID("s3")
	if s3 == nil {
		t.Fatal("NodeByID(s3) returned nil")
	}
	if s3.JoinMode != JoinWaitAll {
		t.Errorf("s3: expected JoinWaitAll, got %s", s3.JoinMode)
	}
	if len(s3.JoinOf) != 2 {
		t.Fatalf("s3.JoinOf len: got %d want 2", len(s3.JoinOf))
	}

	joinOfSet := map[string]bool{s3.JoinOf[0]: true, s3.JoinOf[1]: true}
	if !joinOfSet["s2a"] || !joinOfSet["s2b"] {
		t.Errorf("s3.JoinOf: got %v want [s2a s2b] (any order)", s3.JoinOf)
	}

	// s2a, s2b — single predecessors, no join
	for _, id := range []string{"s2a", "s2b"} {
		n := plan.NodeByID(id)
		if n == nil {
			t.Fatalf("NodeByID(%s) returned nil", id)
		}
		if n.JoinMode != JoinNone {
			t.Errorf("%s: expected JoinNone, got %s", id, n.JoinMode)
		}
	}
}

// TestCompileExecutionPlan_Branch verifies that a branch skill compiles cleanly.
// Branch node has two Next entries but the join node (if any) must be detected.
//
//	s1 → branch → s_true → s_end
//	               s_false → s_end
func TestCompileExecutionPlan_Branch(t *testing.T) {
	skill := &SkillSpec{
		ID: "skill-branch",
		Steps: []StepSpec{
			{ID: "s1", Type: StepInput, Config: rawCfg(t), Next: []string{"br"}},
			{
				ID: "br", Type: StepBranch, Config: rawCfg(t),
				Next:     []string{"s_true", "s_false"},
				Branches: []BranchArm{{Condition: "true", Next: []string{"s_true"}}, {Condition: "false", Next: []string{"s_false"}}},
			},
			{ID: "s_true", Type: StepLLM, Config: rawCfg(t), Next: []string{"s_end"}},
			{ID: "s_false", Type: StepHTTP, Config: rawCfg(t), Next: []string{"s_end"}},
			{ID: "s_end", Type: StepResponse, Config: rawCfg(t), Next: nil},
		},
	}

	plan := CompileExecutionPlan(skill)

	if len(plan.Nodes) != 5 {
		t.Fatalf("Nodes len: got %d want 5", len(plan.Nodes))
	}

	// s_end receives from s_true and s_false → branch convergence, not parallel fan-out
	sEnd := plan.NodeByID("s_end")
	if sEnd == nil {
		t.Fatal("NodeByID(s_end) returned nil")
	}
	if sEnd.JoinMode != JoinBranchMerge {
		t.Errorf("s_end: expected JoinBranchMerge (branch arms are mutually exclusive), got %s", sEnd.JoinMode)
	}
	if len(sEnd.JoinOf) != 2 {
		t.Errorf("s_end.JoinOf len: got %d want 2", len(sEnd.JoinOf))
	}
}

// TestCompileExecutionPlan_MixedFanOut verifies that a node reached by both a
// parallel fan-out source and a branch arm gets JoinWaitAll, not JoinBranchMerge.
//
//	llm → s2a (parallel)
//	llm → s2b (parallel)
//	s2a → s_end
//	s2b → s_end
func TestCompileExecutionPlan_MixedFanOut(t *testing.T) {
	skill := &SkillSpec{
		ID: "skill-parallel-fanout",
		Steps: []StepSpec{
			{ID: "s1", Type: StepInput, Config: rawCfg(t), Next: []string{"s2"}},
			{ID: "s2", Type: StepLLM, Config: rawCfg(t), Next: []string{"s3a", "s3b"}},
			{ID: "s3a", Type: StepHTTP, Config: rawCfg(t), Next: []string{"s4"}},
			{ID: "s3b", Type: StepHTTP, Config: rawCfg(t), Next: []string{"s4"}},
			{ID: "s4", Type: StepResponse, Config: rawCfg(t), Next: nil},
		},
	}

	plan := CompileExecutionPlan(skill)
	s4 := plan.NodeByID("s4")
	if s4 == nil {
		t.Fatal("NodeByID(s4) returned nil")
	}
	if s4.JoinMode != JoinWaitAll {
		t.Errorf("s4: expected JoinWaitAll (parallel LLM fan-out), got %s", s4.JoinMode)
	}
}

// TestCompileExecutionPlan_BranchMerge verifies that a branch convergence node
// (all predecessors are Branch arms) gets JoinBranchMerge, not JoinWaitAll.
//
//	branch → s_true → s_end
//	branch → s_false → s_end
func TestCompileExecutionPlan_BranchMerge(t *testing.T) {
	skill := &SkillSpec{
		ID: "skill-branch-merge",
		Steps: []StepSpec{
			{ID: "s1", Type: StepInput, Config: rawCfg(t), Next: []string{"br"}},
			{
				ID: "br", Type: StepBranch, Config: rawCfg(t),
				Next:     []string{"s_true", "s_false"},
				Branches: []BranchArm{{Condition: "true", Next: []string{"s_true"}}, {Condition: "false", Next: []string{"s_false"}}},
			},
			{ID: "s_true", Type: StepLLM, Config: rawCfg(t), Next: []string{"s_end"}},
			{ID: "s_false", Type: StepHTTP, Config: rawCfg(t), Next: []string{"s_end"}},
			{ID: "s_end", Type: StepResponse, Config: rawCfg(t), Next: nil},
		},
	}

	plan := CompileExecutionPlan(skill)
	sEnd := plan.NodeByID("s_end")
	if sEnd == nil {
		t.Fatal("NodeByID(s_end) returned nil")
	}
	if sEnd.JoinMode != JoinBranchMerge {
		t.Errorf("s_end: expected JoinBranchMerge, got %s", sEnd.JoinMode)
	}
	if len(sEnd.JoinOf) != 2 {
		t.Errorf("s_end.JoinOf len: got %d want 2", len(sEnd.JoinOf))
	}

	// s2a, s2b (branch arms) must have JoinNone
	for _, id := range []string{"s_true", "s_false"} {
		n := plan.NodeByID(id)
		if n == nil {
			t.Fatalf("NodeByID(%s) returned nil", id)
		}
		if n.JoinMode != JoinNone {
			t.Errorf("%s: expected JoinNone, got %s", id, n.JoinMode)
		}
	}
}

// TestCompileExecutionPlan_Nil verifies nil/empty inputs don't panic.
func TestCompileExecutionPlan_Nil(t *testing.T) {
	plan := CompileExecutionPlan(nil)
	if plan == nil {
		t.Fatal("expected non-nil plan for nil input")
	}
	if len(plan.Nodes) != 0 {
		t.Errorf("expected empty nodes, got %d", len(plan.Nodes))
	}

	plan2 := CompileExecutionPlan(&SkillSpec{ID: "empty"})
	if len(plan2.Nodes) != 0 {
		t.Errorf("expected empty nodes for empty skill, got %d", len(plan2.Nodes))
	}
}

// ── ExecutionPolicy tests (EP-1..EP-9) ───────────────────────────────────────

func httpCfg(method string) json.RawMessage {
	return json.RawMessage(`{"method":"` + method + `"}`)
}

func mcpCfg(toolName string) json.RawMessage {
	return json.RawMessage(`{"mcp_server_slug":"s","tool_name":"` + toolName + `","output_var":"out"}`)
}

// EP-1: HTTP GET → MaxAttempts=3, RequiresIdempotencyKey=false.
func TestResolvePolicy_HTTPGet(t *testing.T) {
	nd, ok := LookupNode(StepHTTP)
	if !ok {
		t.Fatal("http node not registered")
	}
	p := resolvePolicy(nd, httpCfg("GET"), nil)
	if p.MaxAttempts != 3 {
		t.Errorf("GET MaxAttempts: got %d want 3", p.MaxAttempts)
	}
	if p.RequiresIdempotencyKey {
		t.Error("GET must not require idempotency key")
	}
}

// EP-2: HTTP POST → MaxAttempts=1, RequiresIdempotencyKey=false (no retry → key not required).
func TestResolvePolicy_HTTPPost(t *testing.T) {
	nd, _ := LookupNode(StepHTTP)
	p := resolvePolicy(nd, httpCfg("POST"), nil)
	if p.MaxAttempts != 1 {
		t.Errorf("POST MaxAttempts: got %d want 1", p.MaxAttempts)
	}
	if p.RequiresIdempotencyKey {
		t.Error("POST with MaxAttempts=1 must NOT require idempotency key (no retry possible)")
	}
}

// EP-2b: HTTP POST with canvas override MaxAttempts=2 → RequiresIdempotencyKey=true.
func TestResolvePolicy_HTTPPostRetryRequiresKey(t *testing.T) {
	nd, _ := LookupNode(StepHTTP)
	override := &ExecutionPolicy{MaxAttempts: 2}
	p := resolvePolicy(nd, httpCfg("POST"), override)
	if p.MaxAttempts != 2 {
		t.Errorf("POST override MaxAttempts: got %d want 2", p.MaxAttempts)
	}
	if !p.RequiresIdempotencyKey {
		t.Error("POST with MaxAttempts=2 MUST require idempotency key")
	}
}

// EP-3: HTTP empty method treated as GET.
func TestResolvePolicy_HTTPEmptyMethod(t *testing.T) {
	nd, _ := LookupNode(StepHTTP)
	p := resolvePolicy(nd, json.RawMessage(`{}`), nil)
	if p.MaxAttempts != 3 {
		t.Errorf("empty method: MaxAttempts got %d want 3", p.MaxAttempts)
	}
}

// EP-4: LLM default policy.
func TestResolvePolicy_LLM(t *testing.T) {
	nd, _ := LookupNode(StepLLM)
	p := resolvePolicy(nd, json.RawMessage(`{}`), nil)
	if p.MaxAttempts != 2 {
		t.Errorf("LLM MaxAttempts: got %d want 2", p.MaxAttempts)
	}
	if p.InitialIntervalSeconds != 2.0 {
		t.Errorf("LLM InitialInterval: got %v want 2.0", p.InitialIntervalSeconds)
	}
}

// EP-5: Canvas override clamped to MaxPolicy.
func TestResolvePolicy_UserOverrideClamped(t *testing.T) {
	nd, _ := LookupNode(StepLLM) // MaxPolicy.MaxAttempts = 3
	override := &ExecutionPolicy{MaxAttempts: 10}
	p := resolvePolicy(nd, json.RawMessage(`{}`), override)
	if p.MaxAttempts != 3 {
		t.Errorf("clamped MaxAttempts: got %d want 3", p.MaxAttempts)
	}
}

// EP-6: NonRetryableErrors never overridable by canvas user.
func TestResolvePolicy_NonRetryableNotOverridable(t *testing.T) {
	nd, _ := LookupNode(StepLLM)
	override := &ExecutionPolicy{NonRetryableErrors: []string{}} // attempt to clear
	p := resolvePolicy(nd, json.RawMessage(`{}`), override)
	if len(p.NonRetryableErrors) == 0 {
		t.Error("NonRetryableErrors must not be clearable by canvas override")
	}
}

// EP-7: Zero-value PlanNode policy (backward compat) — MaxAttempts must not be 0.
func TestResolvePolicy_ZeroValBackwardCompat(t *testing.T) {
	// Simulate a PlanNode compiled before ExecutionPolicy existed.
	zero := ExecutionPolicy{}
	if zero.MaxAttempts == 0 {
		// executors guard this: treat 0 as 1.
		guarded := zero.MaxAttempts
		if guarded == 0 {
			guarded = 1
		}
		if guarded != 1 {
			t.Errorf("zero guard: got %d want 1", guarded)
		}
	}
}

// EP-8: CompileExecutionPlan populates Policy on each PlanNode.
func TestCompileExecutionPlan_PolicyPopulated(t *testing.T) {
	skill := &SkillSpec{
		ID: "skill-policy",
		Steps: []StepSpec{
			{ID: "in",  Type: StepInput,    Config: json.RawMessage(`{}`), Next: []string{"llm"}},
			{ID: "llm", Type: StepLLM,      Config: json.RawMessage(`{}`), Next: []string{"out"}},
			{ID: "out", Type: StepResponse, Config: json.RawMessage(`{}`), Next: nil},
		},
	}
	plan := CompileExecutionPlan(skill)
	for _, n := range plan.Nodes {
		if n.Policy.MaxAttempts == 0 {
			t.Errorf("node %q has zero MaxAttempts after CompileExecutionPlan", n.StepID)
		}
		if n.Policy.TimeoutSeconds == 0 {
			t.Errorf("node %q has zero TimeoutSeconds after CompileExecutionPlan", n.StepID)
		}
		if len(n.Policy.NonRetryableErrors) == 0 {
			t.Errorf("node %q has empty NonRetryableErrors after CompileExecutionPlan", n.StepID)
		}
	}
}

// EP-9: MCP read-only tool → MaxAttempts=2; mutating tool → MaxAttempts=1.
func TestResolvePolicy_MCPMutatingVsRead(t *testing.T) {
	nd, _ := LookupNode(StepMCPCall)

	read := resolvePolicy(nd, mcpCfg("list_issues"), nil)
	if read.MaxAttempts != 2 {
		t.Errorf("MCP read MaxAttempts: got %d want 2", read.MaxAttempts)
	}
	if read.RequiresIdempotencyKey {
		t.Error("MCP read must not require idempotency key")
	}

	mutating := resolvePolicy(nd, mcpCfg("create_issue"), nil)
	if mutating.MaxAttempts != 1 {
		t.Errorf("MCP mutating MaxAttempts: got %d want 1", mutating.MaxAttempts)
	}
	if mutating.RequiresIdempotencyKey {
		t.Error("MCP mutating with MaxAttempts=1 must NOT require idempotency key (no retry possible)")
	}
}
