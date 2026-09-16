package appflow

import (
	"encoding/json"
	"testing"
)

// AF-01: Compile a minimal doc with one EP, one agent node.
func TestCompile_MinimalDoc(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{
				"instance_id": "agent_1",
				"definition_ref": {"kind":"agent","namespace":"default","name":"echo","version":1},
				"definition_id": "agent-uuid-1"
			}
		],
		"entry_points": [
			{"instance_id":"ep_ws_1","slug":"chat","protocol":"websocket","root":"agent_1"}
		],
		"connections": []
	}`)

	spec, err := Compile(raw, map[string]string{"agent_1": "agent-uuid-1"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(spec.EntryPoints) != 1 {
		t.Fatalf("want 1 entry point, got %d", len(spec.EntryPoints))
	}
	ep := spec.EntryPoints[0]
	if ep.Slug != "chat" {
		t.Errorf("slug: want %q, got %q", "chat", ep.Slug)
	}
	if ep.Protocol != "websocket" {
		t.Errorf("protocol: want websocket, got %q", ep.Protocol)
	}
	if ep.StartID != "agent_1" {
		t.Errorf("start_id: want %q, got %q", "agent_1", ep.StartID)
	}
	if len(ep.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(ep.Nodes))
	}
	if ep.Nodes[0].Kind != "agent" {
		t.Errorf("node kind: want agent, got %q", ep.Nodes[0].Kind)
	}
	if ep.Nodes[0].AgentID != "agent-uuid-1" {
		t.Errorf("agent_id: want %q, got %q", "agent-uuid-1", ep.Nodes[0].AgentID)
	}
}

// AF-02: Compile a doc with Router and HIL nodes.
func TestCompile_RouterAndHIL(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{
				"instance_id": "fc_router_1",
				"definition_ref": {"kind":"flow_control","namespace":"builtin","name":"router","version":1},
				"config": {"node_type":"router","display_name":"Router","classifier_prompt":"classify","output_labels":["left","right"]}
			},
			{
				"instance_id": "fc_hil_1",
				"definition_ref": {"kind":"flow_control","namespace":"builtin","name":"hil","version":1},
				"config": {"node_type":"hil","display_name":"HIL","approver_role":"admin","timeout_seconds":300,"fallback_action":"reject"}
			},
			{
				"instance_id": "agent_left",
				"definition_ref": {"kind":"agent","namespace":"default","name":"agent-left","version":1},
				"definition_id": "agent-left-uuid"
			},
			{
				"instance_id": "agent_right",
				"definition_ref": {"kind":"agent","namespace":"default","name":"agent-right","version":1},
				"definition_id": "agent-right-uuid"
			}
		],
		"entry_points": [
			{"instance_id":"ep_ws_1","slug":"chat","protocol":"websocket","root":"fc_router_1"}
		],
		"connections": [
			{"source":"ep_ws_1","target":"fc_router_1","type":"flow_control"},
			{"source":"fc_router_1","target":"agent_left","type":"flow_control"},
			{"source":"fc_router_1","target":"fc_hil_1","type":"flow_control"},
			{"source":"fc_hil_1","target":"agent_right","type":"flow_control"}
		]
	}`)

	agents := map[string]string{
		"agent_left":  "agent-left-uuid",
		"agent_right": "agent-right-uuid",
	}
	spec, err := Compile(raw, agents)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(spec.EntryPoints) != 1 {
		t.Fatalf("want 1 entry point, got %d", len(spec.EntryPoints))
	}
	ep := spec.EntryPoints[0]
	if ep.StartID != "fc_router_1" {
		t.Errorf("start_id: want fc_router_1, got %q", ep.StartID)
	}

	kindByID := make(map[string]string)
	for _, n := range ep.Nodes {
		kindByID[n.ID] = n.Kind
	}
	if kindByID["fc_router_1"] != "router" {
		t.Errorf("router node kind: got %q", kindByID["fc_router_1"])
	}
	if kindByID["fc_hil_1"] != "hil" {
		t.Errorf("hil node kind: got %q", kindByID["fc_hil_1"])
	}
	if kindByID["agent_left"] != "agent" {
		t.Errorf("agent_left kind: got %q", kindByID["agent_left"])
	}
	if kindByID["agent_right"] != "agent" {
		t.Errorf("agent_right kind: got %q", kindByID["agent_right"])
	}
}

// AF-03: Compile rejects schema_version != 2.
func TestCompile_WrongSchemaVersion(t *testing.T) {
	raw := json.RawMessage(`{"schema_version":1,"components":[],"entry_points":[],"connections":[]}`)
	_, err := Compile(raw, nil)
	if err == nil {
		t.Fatal("expected error for schema_version 1")
	}
}

// AF-04: Compile propagates execution_backend from doc.
func TestCompile_ExecutionBackend(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"execution_backend": "temporal",
		"components": [],
		"entry_points": [],
		"connections": []
	}`)
	spec, err := Compile(raw, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if spec.ExecutionBackend != "temporal" {
		t.Errorf("execution_backend: want temporal, got %q", spec.ExecutionBackend)
	}
}

// AF-05: Validate returns an error for a router with no outgoing edges.
func TestValidate_RouterNoEdges(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{
			{
				Slug:    "chat",
				StartID: "fc_router_1",
				Nodes: []AppFlowNode{
					{ID: "fc_router_1", Kind: "router"},
				},
				Edges: nil, // no edges
			},
		},
	}
	errs := Validate(spec)
	if len(errs) == 0 {
		t.Fatal("expected validation error for router with no edges")
	}
	found := false
	for _, e := range errs {
		if e.Code == "router_no_edges" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected router_no_edges error, got: %+v", errs)
	}
}

// AF-06: Validate returns no errors for a valid spec.
func TestValidate_ValidSpec(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{
			{
				Slug:    "chat",
				StartID: "agent_1",
				Nodes: []AppFlowNode{
					{ID: "agent_1", Kind: "agent", AgentID: "uuid-1"},
				},
				Edges: nil,
			},
		},
	}
	errs := Validate(spec)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %+v", errs)
	}
}

// AF-07: defaultRouterPrompt contains all labels.
func TestDefaultRouterPrompt(t *testing.T) {
	labels := []string{"billing", "support", "technical"}
	prompt := defaultRouterPrompt(labels)
	for _, l := range labels {
		if !contains(prompt, l) {
			t.Errorf("prompt missing label %q: %s", l, prompt)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// AF-08: findEdgeByLabel case-insensitive match.
func TestFindEdgeByLabel(t *testing.T) {
	edges := []AppFlowEdge{
		{Source: "r1", Target: "a1", Label: "Billing"},
		{Source: "r1", Target: "a2", Label: "Support"},
	}
	if got := findEdgeByLabel(edges, "billing"); got != "a1" {
		t.Errorf("want a1, got %q", got)
	}
	if got := findEdgeByLabel(edges, "SUPPORT"); got != "a2" {
		t.Errorf("want a2, got %q", got)
	}
	if got := findEdgeByLabel(edges, "unknown"); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}
