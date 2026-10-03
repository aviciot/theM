package appflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// AF-WFI-01: wait_for_input node compiles from a flow_control/wait_for_input
// component with the correct kind and WaitForInputConfig.
func TestWaitForInputCompile(t *testing.T) {
	cfg := WaitForInputConfig{
		Prompt:    "Please re-enter the amount.",
		OutputVar: "user_reply",
	}
	cfgRaw, _ := json.Marshal(cfg)

	docRaw, _ := json.Marshal(map[string]interface{}{
		"schema_version": 2,
		"components": []map[string]interface{}{
			{
				"instance_id": "wfi1",
				"definition_ref": map[string]interface{}{
					"kind": "flow_control",
					"name": "wait_for_input",
				},
				"config": json.RawMessage(cfgRaw),
			},
		},
		"entry_points": []map[string]interface{}{
			{
				"instance_id": "ep1",
				"slug":        "chat",
				"protocol":    "ws",
				"root":        "wfi1",
			},
		},
		"connections": []map[string]interface{}{},
	})

	spec, err := Compile(docRaw, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(spec.EntryPoints) == 0 {
		t.Fatal("no entry points compiled")
	}
	ep := spec.EntryPoints[0]
	if len(ep.Nodes) == 0 {
		t.Fatal("no nodes compiled")
	}
	node := ep.Nodes[0]
	if node.Kind != "wait_for_input" {
		t.Errorf("expected kind=wait_for_input, got %q", node.Kind)
	}

	var got WaitForInputConfig
	if err := json.Unmarshal(node.Config, &got); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if got.Prompt != cfg.Prompt {
		t.Errorf("Prompt: want %q got %q", cfg.Prompt, got.Prompt)
	}
	if got.OutputVar != cfg.OutputVar {
		t.Errorf("OutputVar: want %q got %q", cfg.OutputVar, got.OutputVar)
	}
}

// AF-WFI-02: wait_for_input validation — output_var with whitespace fails.
func TestWaitForInputValidation_InvalidOutputVar(t *testing.T) {
	cfg := WaitForInputConfig{OutputVar: "bad var"}
	cfgRaw, _ := json.Marshal(cfg)

	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{
			{
				Slug:    "chat",
				StartID: "wfi1",
				Nodes: []AppFlowNode{
					{ID: "wfi1", Kind: "wait_for_input", Config: cfgRaw},
				},
				Edges: []AppFlowEdge{
					{Source: "wfi1", Target: "wfi2"},
				},
			},
		},
	}

	errs := Validate(spec)
	if !hasValidationCode(errs, "wait_for_input_invalid_output_var") {
		t.Errorf("expected wait_for_input_invalid_output_var, got %v", errs)
	}
}

// AF-WFI-03: wait_for_input validation — missing outgoing edge.
func TestWaitForInputValidation_NoOutgoingEdge(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{
			{
				Slug:    "chat",
				StartID: "wfi1",
				Nodes: []AppFlowNode{
					{ID: "wfi1", Kind: "wait_for_input"},
				},
				Edges: []AppFlowEdge{},
			},
		},
	}

	errs := Validate(spec)
	if !hasValidationCode(errs, "wait_for_input_no_outgoing_edge") {
		t.Errorf("expected wait_for_input_no_outgoing_edge, got %v", errs)
	}
}

// AF-WFI-04: PendingWaitKey returns the correct Redis key format.
func TestPendingWaitKey(t *testing.T) {
	runID := "abc-123"
	want := "them:wait:abc-123"
	got := PendingWaitKey(runID)
	if got != want {
		t.Errorf("PendingWaitKey(%q) = %q, want %q", runID, got, want)
	}
}

// AF-WFI-05: AppFlowSignalUserInput constant has the expected prefix.
func TestAppFlowSignalUserInputConstant(t *testing.T) {
	const nodeID = "node42"
	sigName := AppFlowSignalUserInput + ":" + nodeID
	if !strings.HasPrefix(sigName, "appflow_user_input:") {
		t.Errorf("unexpected signal name format: %q", sigName)
	}
	if !strings.HasSuffix(sigName, ":"+nodeID) {
		t.Errorf("signal name must end with :%s, got %q", nodeID, sigName)
	}
}

// AF-WFI-06: valid wait_for_input config (no output_var) passes validation.
func TestWaitForInputValidation_Valid(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{
			{
				Slug:    "chat",
				StartID: "wfi1",
				Nodes: []AppFlowNode{
					{ID: "wfi1", Kind: "wait_for_input"},
				},
				Edges: []AppFlowEdge{
					{Source: "wfi1", Target: "wfi2"},
				},
			},
		},
	}

	errs := Validate(spec)
	for _, e := range errs {
		if e.InstanceID == "wfi1" {
			t.Errorf("unexpected validation error for wfi1: %v", e)
		}
	}
}

