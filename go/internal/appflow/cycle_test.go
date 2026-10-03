package appflow

import (
	"encoding/json"
	"testing"
)

// hasValidationCode checks whether a specific error code appears in a slice of ValidationErrors.
func hasValidationCode(errs []ValidationError, code string) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

// AF-CY-01: evalBreakCondition — all ops and edge cases.
func TestEvalBreakCondition(t *testing.T) {
	vars := FlowVars{"done": "true", "count": "5", "empty": ""}

	tests := []struct {
		name    string
		varName string
		op      string
		wantVal string
		expect  bool
	}{
		{"eq match", "done", "eq", "true", true},
		{"eq no match", "done", "eq", "false", false},
		{"neq match", "done", "neq", "false", true},
		{"neq no match", "done", "neq", "true", false},
		{"truthy true", "done", "truthy", "", true},
		{"truthy false", "empty", "truthy", "", false},
		{"missing var", "missing", "eq", "x", false},
		{"empty varName", "", "eq", "true", false},
		{"default op is eq", "count", "", "5", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evalBreakCondition(vars, tt.varName, tt.op, tt.wantVal)
			if got != tt.expect {
				t.Errorf("evalBreakCondition(%q,%q,%q) = %v, want %v", tt.varName, tt.op, tt.wantVal, got, tt.expect)
			}
		})
	}
}

// AF-CY-02: Compile — cycle node with one LLM child produces outer graph with
// exactly 1 node (the cycle), child embedded in CycleConfig.BodyNodes.
func TestCycleNodeCompile(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"execution_backend": "temporal",
		"components": [
			{
				"instance_id": "cycle_1",
				"definition_ref": {"kind": "flow_control", "namespace": "builtin", "name": "cycle", "version": 1},
				"config": {"break_when_var": "done", "break_when_op": "eq", "break_when_val": "true", "max_iterations": 5}
			},
			{
				"instance_id": "llm_inner",
				"definition_ref": {"kind": "inline", "namespace": "builtin", "name": "llm", "version": 1},
				"parent_instance_id": "cycle_1",
				"config": {"system_prompt": "retry", "user_prompt": "{{.input}}"}
			}
		],
		"entry_points": [
			{"instance_id": "ep1", "slug": "main", "protocol": "ws", "root": "cycle_1"}
		],
		"connections": [
			{"source": "ep1", "target": "cycle_1", "type": "flow_control"}
		]
	}`)

	spec, err := Compile(raw, map[string]string{})
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if len(spec.EntryPoints) != 1 {
		t.Fatalf("expected 1 EP, got %d", len(spec.EntryPoints))
	}
	ep := spec.EntryPoints[0]
	if len(ep.Nodes) != 1 {
		t.Errorf("outer nodes = %d, want 1 (child must be inside CycleConfig)", len(ep.Nodes))
	}
	if ep.Nodes[0].Kind != "cycle" {
		t.Errorf("node kind = %q, want %q", ep.Nodes[0].Kind, "cycle")
	}
	var cfg CycleConfig
	if err := json.Unmarshal(ep.Nodes[0].Config, &cfg); err != nil {
		t.Fatalf("unmarshal CycleConfig: %v", err)
	}
	if cfg.BreakWhenVar != "done" {
		t.Errorf("BreakWhenVar = %q, want %q", cfg.BreakWhenVar, "done")
	}
	if cfg.MaxIterations != 5 {
		t.Errorf("MaxIterations = %d, want 5", cfg.MaxIterations)
	}
	if len(cfg.BodyNodes) != 1 {
		t.Errorf("BodyNodes = %d, want 1", len(cfg.BodyNodes))
	}
	if cfg.BodyNodes[0].ID != "llm_inner" {
		t.Errorf("body node ID = %q, want %q", cfg.BodyNodes[0].ID, "llm_inner")
	}
	if cfg.EntryNodeID != "llm_inner" {
		t.Errorf("EntryNodeID = %q, want %q", cfg.EntryNodeID, "llm_inner")
	}
}

// AF-CY-03: Compile — cycle with two chained children produces correct BodyEdges
// and EntryNodeID = the node with no incoming body edges.
func TestCycleNodeCompile_TwoChildren(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{
				"instance_id": "cycle_1",
				"definition_ref": {"kind": "flow_control", "namespace": "builtin", "name": "cycle", "version": 1},
				"config": {"break_when_var": "done", "break_when_op": "eq", "break_when_val": "true", "max_iterations": 3}
			},
			{
				"instance_id": "llm_a",
				"definition_ref": {"kind": "inline", "namespace": "builtin", "name": "llm", "version": 1},
				"parent_instance_id": "cycle_1",
				"config": {"system_prompt": "step a"}
			},
			{
				"instance_id": "llm_b",
				"definition_ref": {"kind": "inline", "namespace": "builtin", "name": "llm", "version": 1},
				"parent_instance_id": "cycle_1",
				"config": {"system_prompt": "step b"}
			}
		],
		"entry_points": [
			{"instance_id": "ep1", "slug": "main", "protocol": "ws", "root": "cycle_1"}
		],
		"connections": [
			{"source": "ep1",    "target": "cycle_1", "type": "flow_control"},
			{"source": "llm_a",  "target": "llm_b",   "type": "flow_control"}
		]
	}`)

	spec, err := Compile(raw, map[string]string{})
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	ep := spec.EntryPoints[0]
	if len(ep.Nodes) != 1 {
		t.Errorf("outer nodes = %d, want 1", len(ep.Nodes))
	}
	var cfg CycleConfig
	if err := json.Unmarshal(ep.Nodes[0].Config, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.BodyNodes) != 2 {
		t.Errorf("BodyNodes = %d, want 2", len(cfg.BodyNodes))
	}
	if len(cfg.BodyEdges) != 1 {
		t.Errorf("BodyEdges = %d, want 1", len(cfg.BodyEdges))
	}
	if cfg.EntryNodeID != "llm_a" {
		t.Errorf("EntryNodeID = %q, want %q", cfg.EntryNodeID, "llm_a")
	}
}

// AF-CY-04: Validate — error codes for invalid cycle configs.
func TestCycleValidation(t *testing.T) {
	makeCycleNode := func(cfg CycleConfig) AppFlowNode {
		b, _ := json.Marshal(cfg)
		return AppFlowNode{ID: "c1", Kind: "cycle", Config: b}
	}

	t.Run("no break var", func(t *testing.T) {
		spec := &AppFlowSpec{EntryPoints: []EPFlow{{
			Slug: "s", StartID: "c1",
			Nodes: []AppFlowNode{makeCycleNode(CycleConfig{
				MaxIterations: 5,
				BodyNodes:     []AppFlowNode{{ID: "n1", Kind: "llm"}},
			})},
		}}}
		errs := Validate(spec)
		if !hasValidationCode(errs, "cycle_no_break_condition") {
			t.Errorf("expected cycle_no_break_condition, got %v", errs)
		}
	})

	t.Run("max iterations zero", func(t *testing.T) {
		spec := &AppFlowSpec{EntryPoints: []EPFlow{{
			Slug: "s", StartID: "c1",
			Nodes: []AppFlowNode{makeCycleNode(CycleConfig{
				BreakWhenVar:  "done",
				MaxIterations: 0,
				BodyNodes:     []AppFlowNode{{ID: "n1", Kind: "llm"}},
			})},
		}}}
		errs := Validate(spec)
		if !hasValidationCode(errs, "cycle_invalid_max_iterations") {
			t.Errorf("expected cycle_invalid_max_iterations, got %v", errs)
		}
	})

	t.Run("max iterations over 100", func(t *testing.T) {
		spec := &AppFlowSpec{EntryPoints: []EPFlow{{
			Slug: "s", StartID: "c1",
			Nodes: []AppFlowNode{makeCycleNode(CycleConfig{
				BreakWhenVar:  "done",
				MaxIterations: 101,
				BodyNodes:     []AppFlowNode{{ID: "n1", Kind: "llm"}},
			})},
		}}}
		errs := Validate(spec)
		if !hasValidationCode(errs, "cycle_invalid_max_iterations") {
			t.Errorf("expected cycle_invalid_max_iterations, got %v", errs)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		spec := &AppFlowSpec{EntryPoints: []EPFlow{{
			Slug: "s", StartID: "c1",
			Nodes: []AppFlowNode{makeCycleNode(CycleConfig{
				BreakWhenVar:  "done",
				MaxIterations: 5,
			})},
		}}}
		errs := Validate(spec)
		if !hasValidationCode(errs, "cycle_empty_body") {
			t.Errorf("expected cycle_empty_body, got %v", errs)
		}
	})

	t.Run("valid cycle — no cycle error codes", func(t *testing.T) {
		spec := &AppFlowSpec{EntryPoints: []EPFlow{{
			Slug: "s", StartID: "c1",
			Nodes: []AppFlowNode{makeCycleNode(CycleConfig{
				BreakWhenVar:  "done",
				BreakWhenOp:   "eq",
				BreakWhenVal:  "true",
				MaxIterations: 10,
				EntryNodeID:   "n1",
				BodyNodes:     []AppFlowNode{{ID: "n1", Kind: "llm"}},
			})},
		}}}
		errs := Validate(spec)
		for _, e := range errs {
			if e.Code == "cycle_no_break_condition" || e.Code == "cycle_invalid_max_iterations" || e.Code == "cycle_empty_body" {
				t.Errorf("unexpected cycle error: %v", e)
			}
		}
	})
}
