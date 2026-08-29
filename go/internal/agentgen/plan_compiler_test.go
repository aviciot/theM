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

	// s_end receives from s_true and s_false → join
	sEnd := plan.NodeByID("s_end")
	if sEnd == nil {
		t.Fatal("NodeByID(s_end) returned nil")
	}
	if sEnd.JoinMode != JoinWaitAll {
		t.Errorf("s_end: expected JoinWaitAll, got %s", sEnd.JoinMode)
	}
	if len(sEnd.JoinOf) != 2 {
		t.Errorf("s_end.JoinOf len: got %d want 2", len(sEnd.JoinOf))
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
