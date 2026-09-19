package appflow

import "testing"

// TestAllAppCanvasNodeInfos_ReturnsSixKinds verifies the registry declares
// exactly the 6 app-canvas node kinds the compiler/workflow/validate switches
// dispatch on, each with a non-empty label and color, and that mutating the
// returned slice does not affect the shared static registry.
func TestAllAppCanvasNodeInfos_ReturnsSixKinds(t *testing.T) {
	infos := AllAppCanvasNodeInfos()
	if len(infos) != 6 {
		t.Fatalf("expected 6 app-canvas node kinds, got %d", len(infos))
	}

	wantTypes := map[string]bool{
		"llm": false, "condition": false, "router": false,
		"hil": false, "fork": false, "join": false,
	}
	for _, info := range infos {
		if _, ok := wantTypes[info.Type]; !ok {
			t.Errorf("unexpected node kind %q", info.Type)
			continue
		}
		wantTypes[info.Type] = true
		if info.Label == "" {
			t.Errorf("kind %q: empty Label", info.Type)
		}
		if info.Color == "" {
			t.Errorf("kind %q: empty Color", info.Type)
		}
		if !info.Executable {
			t.Errorf("kind %q: expected Executable=true", info.Type)
		}
	}
	for typ, found := range wantTypes {
		if !found {
			t.Errorf("expected kind %q not present in registry", typ)
		}
	}
}

// TestAllAppCanvasNodeInfos_ConditionHasTrueFalsePorts verifies condition's
// two control-flow output ports match the edge labels validate.go and
// workflow.go require ("true"/"false", case-insensitive elsewhere).
func TestAllAppCanvasNodeInfos_ConditionHasTrueFalsePorts(t *testing.T) {
	for _, info := range AllAppCanvasNodeInfos() {
		if info.Type != "condition" {
			continue
		}
		if len(info.ControlOutputPorts) != 2 {
			t.Fatalf("condition: expected 2 control_output_ports, got %d", len(info.ControlOutputPorts))
		}
		ids := map[string]bool{}
		for _, p := range info.ControlOutputPorts {
			ids[p.ID] = true
		}
		if !ids["true"] || !ids["false"] {
			t.Errorf("condition: expected control_output_ports with IDs true+false, got %v", ids)
		}
		if info.Edges.MinOut != 2 || info.Edges.MaxOut != 2 {
			t.Errorf("condition: expected exactly-2 outgoing edge rule, got min=%d max=%d", info.Edges.MinOut, info.Edges.MaxOut)
		}
		return
	}
	t.Fatal("condition kind not found in registry")
}

// TestAllAppCanvasNodeInfos_ForkJoinDegreeRules verifies fork requires ≥2
// outgoing and join requires ≥2 incoming, matching validate.go's structural
// rules (fork_insufficient_branches / join_insufficient_branches).
func TestAllAppCanvasNodeInfos_ForkJoinDegreeRules(t *testing.T) {
	byType := make(map[string]AppCanvasNodeInfo)
	for _, info := range AllAppCanvasNodeInfos() {
		byType[info.Type] = info
	}

	fork, ok := byType["fork"]
	if !ok {
		t.Fatal("fork kind not found")
	}
	if fork.Edges.MinOut < 2 {
		t.Errorf("fork: expected min_out >= 2, got %d", fork.Edges.MinOut)
	}

	join, ok := byType["join"]
	if !ok {
		t.Fatal("join kind not found")
	}
	if join.Edges.MinIn < 2 {
		t.Errorf("join: expected min_in >= 2, got %d", join.Edges.MinIn)
	}
}

// TestAllAppCanvasNodeInfos_ReturnsCopyNotSharedSlice verifies mutating the
// returned slice/struct does not corrupt the shared static registry — the
// same defensive-copy guarantee agentgen.AllNodeTypeInfos gives its callers.
func TestAllAppCanvasNodeInfos_ReturnsCopyNotSharedSlice(t *testing.T) {
	first := AllAppCanvasNodeInfos()
	first[0].Label = "MUTATED"

	second := AllAppCanvasNodeInfos()
	if second[0].Label == "MUTATED" {
		t.Fatal("mutating the returned slice affected the shared registry")
	}
}
