package appflow

import "testing"

// TestAllAppCanvasNodeInfos_ReturnsSixKinds verifies the registry declares
// the core app-canvas node kinds the compiler/workflow/validate switches
// dispatch on, each with a non-empty label and color, and that mutating the
// returned slice does not affect the shared static registry.
func TestAllAppCanvasNodeInfos_ReturnsSixKinds(t *testing.T) {
	infos := AllAppCanvasNodeInfos()
	if len(infos) != 8 {
		t.Fatalf("expected 8 app-canvas node kinds, got %d", len(infos))
	}

	wantTypes := map[string]bool{
		"llm": false, "condition": false, "router": false,
		"hil": false, "fork": false, "join": false, "wait_for_input": false,
		"http": false,
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

// TestAllAppCanvasNodeInfos_LLMHasOutputPort verifies llm and http each declare
// exactly one named output port ("output"). Control-flow-only nodes (condition,
// fork, join, etc.) have no data output port; only data-producing nodes get an
// OutputPorts entry.
func TestAllAppCanvasNodeInfos_LLMHasOutputPort(t *testing.T) {
	// Kinds that must declare exactly one "output" OutputPort.
	dataKinds := map[string]bool{"llm": true, "http": true}
	infos := AllAppCanvasNodeInfos()
	for _, info := range infos {
		if !dataKinds[info.Type] {
			if len(info.OutputPorts) != 0 {
				t.Errorf("kind %q: expected no OutputPorts, got %+v", info.Type, info.OutputPorts)
			}
			continue
		}
		if len(info.OutputPorts) != 1 {
			t.Fatalf("%s: expected exactly 1 OutputPorts entry, got %d", info.Type, len(info.OutputPorts))
		}
		p := info.OutputPorts[0]
		if p.ID != "output" {
			t.Errorf("%s: expected OutputPorts[0].ID == \"output\", got %q", info.Type, p.ID)
		}
		if p.Label == "" {
			t.Errorf("%s: OutputPorts[0].Label must not be empty", info.Type)
		}
		if !info.AcceptsDynamicInputs {
			t.Errorf("%s: expected AcceptsDynamicInputs=true", info.Type)
		}
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

// TestAllAppCanvasNodeInfos_LLMDeclaresCredentialRuntimeParam verifies the
// llm and router kinds each declare a required "llm_credential"-typed runtime
// param (the debug panel provider/model/key picker), and http declares
// "secret"-typed params (bearer_token, api_key). No other kind may have any.
func TestAllAppCanvasNodeInfos_LLMDeclaresCredentialRuntimeParam(t *testing.T) {
	infos := AllAppCanvasNodeInfos()
	for _, info := range infos {
		switch info.Type {
		case "llm", "router":
			if len(info.RuntimeParams) != 1 {
				t.Fatalf("%s: expected exactly 1 RuntimeParams entry, got %d", info.Type, len(info.RuntimeParams))
			}
			p := info.RuntimeParams[0]
			if p.Key == "" {
				t.Errorf("%s: RuntimeParams[0].Key must not be empty", info.Type)
			}
			if p.Type != "llm_credential" {
				t.Errorf("%s: expected Type=\"llm_credential\", got %q", info.Type, p.Type)
			}
			if !p.Required {
				t.Errorf("%s: expected Required=true", info.Type)
			}
		case "http":
			if len(info.RuntimeParams) == 0 {
				t.Errorf("http: expected at least 1 RuntimeParams entry (bearer_token / api_key), got none")
			}
			for _, p := range info.RuntimeParams {
				if p.Type != "secret" {
					t.Errorf("http: RuntimeParam %q: expected Type=\"secret\", got %q", p.Key, p.Type)
				}
			}
		default:
			if len(info.RuntimeParams) != 0 {
				t.Errorf("kind %q: expected no RuntimeParams, got %+v", info.Type, info.RuntimeParams)
			}
		}
	}
}

// TestRouterDynamicControlOutputSource verifies the router declares
// DynamicControlOutputSource="output_labels" so the frontend can render one
// spread handle per label without hardcoding the router type.
func TestRouterDynamicControlOutputSource(t *testing.T) {
	for _, info := range AllAppCanvasNodeInfos() {
		if info.Type != "router" {
			continue
		}
		if info.DynamicControlOutputSource != "output_labels" {
			t.Errorf("router: expected DynamicControlOutputSource=\"output_labels\", got %q", info.DynamicControlOutputSource)
		}
		return
	}
	t.Fatal("router kind not found in AllAppCanvasNodeInfos")
}
