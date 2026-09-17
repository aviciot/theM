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

// AF-10: unresolved agent (missing from agentByInstanceID) produces empty AgentID caught by Validate.
func TestCompile_UnresolvedAgent_CaughtByValidate(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{"instance_id":"a1","definition_ref":{"kind":"agent","namespace":"custom","name":"my-agent","version":1},"definition_id":"some-def-id"}
		],
		"entry_points":[{"instance_id":"ep1","slug":"main","protocol":"websocket","root":"a1"}],
		"connections":[]
	}`)
	// Empty map — agent not resolved.
	spec, err := Compile(raw, map[string]string{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(spec.EntryPoints[0].Nodes) == 0 {
		t.Fatal("expected 1 node")
	}
	agentNode := spec.EntryPoints[0].Nodes[0]
	if agentNode.AgentID != "" {
		t.Errorf("want empty AgentID when unresolved, got %q (DefinitionID fallback must be removed)", agentNode.AgentID)
	}
	errs := Validate(spec)
	foundUnresolved := false
	for _, e := range errs {
		if e.Code == "unresolved_agent" {
			foundUnresolved = true
		}
	}
	if !foundUnresolved {
		t.Errorf("expected unresolved_agent error from Validate, got: %+v", errs)
	}
}

// AF-09: edge label is preserved through compilation when conn.Label is set.
func TestCompile_EdgeLabelPreserved(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{"instance_id":"r1","definition_ref":{"kind":"flow_control","namespace":"builtin","name":"router","version":1},"config":{"node_type":"router","output_labels":["billing","support"]}},
			{"instance_id":"a1","definition_ref":{"kind":"agent","namespace":"custom","name":"billing-agent","version":1}},
			{"instance_id":"a2","definition_ref":{"kind":"agent","namespace":"custom","name":"support-agent","version":1}}
		],
		"entry_points":[{"instance_id":"ep1","slug":"main","protocol":"websocket","root":"r1"}],
		"connections":[
			{"source":"r1","target":"a1","type":"flow_control","label":"billing"},
			{"source":"r1","target":"a2","type":"flow_control","label":"support"}
		]
	}`)
	agentMap := map[string]string{"a1": "uuid-billing", "a2": "uuid-support"}
	spec, err := Compile(raw, agentMap)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ep := spec.EntryPoints[0]
	labelByTarget := make(map[string]string)
	for _, e := range ep.Edges {
		labelByTarget[e.Target] = e.Label
	}
	if labelByTarget["a1"] != "billing" {
		t.Errorf("want edge to a1 label=billing, got %q", labelByTarget["a1"])
	}
	if labelByTarget["a2"] != "support" {
		t.Errorf("want edge to a2 label=support, got %q", labelByTarget["a2"])
	}
}

// AF-C-01: ResolveAgentByInstanceID reads _resolved_agent_ids from the definition JSON.
func TestResolveAgentByInstanceID_ReadsServerStampedMap(t *testing.T) {
	defJSON := []byte(`{
		"schema_version": 2,
		"_resolved_agent_ids": {
			"agent_1": "server-uuid-1",
			"agent_2": "server-uuid-2"
		},
		"components": [
			{"instance_id":"agent_1","definition_ref":{"kind":"agent","namespace":"default","name":"echo","version":1},"definition_id":"client-uuid-IGNORED"}
		]
	}`)
	m, err := ResolveAgentByInstanceID(defJSON)
	if err != nil {
		t.Fatalf("ResolveAgentByInstanceID: %v", err)
	}
	if m["agent_1"] != "server-uuid-1" {
		t.Errorf("want server-uuid-1, got %q — client definition_id must be ignored", m["agent_1"])
	}
	if m["agent_2"] != "server-uuid-2" {
		t.Errorf("want server-uuid-2, got %q", m["agent_2"])
	}
}

// AF-C-02: ResolveAgentByInstanceID returns empty map for old definition without _resolved_agent_ids.
func TestResolveAgentByInstanceID_MissingMap_ReturnsEmpty(t *testing.T) {
	defJSON := []byte(`{
		"schema_version": 2,
		"components": [
			{"instance_id":"agent_1","definition_ref":{"kind":"agent","namespace":"default","name":"echo","version":1},"definition_id":"uuid-1"}
		]
	}`)
	m, err := ResolveAgentByInstanceID(defJSON)
	if err != nil {
		t.Fatalf("ResolveAgentByInstanceID: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("want empty map for old definition, got %v", m)
	}
}

// AF-C-03: ParseLLMConfig uses orchestrator provider; defaults to "anthropic" when empty.
func TestParseLLMConfig_UsesOrchProvider(t *testing.T) {
	cfg := ParseLLMConfig(LLMOrchConfig{Provider: "openai", Model: "gpt-4o"})
	if cfg.ProviderName != "openai" {
		t.Errorf("want openai, got %q", cfg.ProviderName)
	}
	if cfg.Model != "gpt-4o" {
		t.Errorf("want gpt-4o, got %q", cfg.Model)
	}
}

func TestParseLLMConfig_DefaultsToAnthropic(t *testing.T) {
	cfg := ParseLLMConfig(LLMOrchConfig{})
	if cfg.ProviderName != "anthropic" {
		t.Errorf("want anthropic default, got %q", cfg.ProviderName)
	}
}

// AF-11: Compile recognises fork and join node kinds.
func TestCompile_ForkJoinNodes(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{"instance_id":"fork_1","definition_ref":{"kind":"flow_control","namespace":"builtin","name":"fork","version":1},"config":{}},
			{"instance_id":"agent_a","definition_ref":{"kind":"agent","namespace":"default","name":"agent-a","version":1}},
			{"instance_id":"agent_b","definition_ref":{"kind":"agent","namespace":"default","name":"agent-b","version":1}},
			{"instance_id":"join_1","definition_ref":{"kind":"flow_control","namespace":"builtin","name":"join","version":1},"config":{}}
		],
		"entry_points":[{"instance_id":"ep1","slug":"main","protocol":"websocket","root":"fork_1"}],
		"connections":[
			{"source":"ep1","target":"fork_1","type":"flow_control"},
			{"source":"fork_1","target":"agent_a","type":"flow_control"},
			{"source":"fork_1","target":"agent_b","type":"flow_control"},
			{"source":"agent_a","target":"join_1","type":"flow_control"},
			{"source":"agent_b","target":"join_1","type":"flow_control"}
		]
	}`)
	agents := map[string]string{"agent_a": "uuid-a", "agent_b": "uuid-b"}
	spec, err := Compile(raw, agents)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ep := spec.EntryPoints[0]
	kindByID := make(map[string]string)
	for _, n := range ep.Nodes {
		kindByID[n.ID] = n.Kind
	}
	if kindByID["fork_1"] != "fork" {
		t.Errorf("fork node kind: got %q, want fork", kindByID["fork_1"])
	}
	if kindByID["join_1"] != "join" {
		t.Errorf("join node kind: got %q, want join", kindByID["join_1"])
	}
	if kindByID["agent_a"] != "agent" {
		t.Errorf("agent_a kind: got %q", kindByID["agent_a"])
	}
	if kindByID["agent_b"] != "agent" {
		t.Errorf("agent_b kind: got %q", kindByID["agent_b"])
	}
}

// AF-12: Validate rejects fork with <2 outgoing edges.
func TestValidate_ForkInsufficientBranches(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "fork_1",
			Nodes: []AppFlowNode{
				{ID: "fork_1", Kind: "fork"},
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
			},
			Edges: []AppFlowEdge{
				{Source: "fork_1", Target: "agent_a"},
			},
		}},
	}
	errs := Validate(spec)
	found := false
	for _, e := range errs {
		if e.Code == "fork_insufficient_branches" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fork_insufficient_branches, got: %+v", errs)
	}
}

// AF-13: Validate rejects join with <2 incoming edges.
func TestValidate_JoinInsufficientBranches(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "agent_a",
			Nodes: []AppFlowNode{
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
				{ID: "join_1", Kind: "join"},
			},
			Edges: []AppFlowEdge{
				{Source: "agent_a", Target: "join_1"},
			},
		}},
	}
	errs := Validate(spec)
	found := false
	for _, e := range errs {
		if e.Code == "join_insufficient_branches" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected join_insufficient_branches, got: %+v", errs)
	}
}

// AF-14: Validate passes for a well-formed fork/join topology.
func TestValidate_ForkJoinValidTopology(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "fork_1",
			Nodes: []AppFlowNode{
				{ID: "fork_1", Kind: "fork"},
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
				{ID: "agent_b", Kind: "agent", AgentID: "uuid-b"},
				{ID: "join_1", Kind: "join"},
			},
			Edges: []AppFlowEdge{
				{Source: "fork_1", Target: "agent_a"},
				{Source: "fork_1", Target: "agent_b"},
				{Source: "agent_a", Target: "join_1"},
				{Source: "agent_b", Target: "join_1"},
			},
		}},
	}
	errs := Validate(spec)
	if len(errs) != 0 {
		t.Errorf("expected no validation errors, got: %+v", errs)
	}
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

// AF-15: Compile maps definition_ref.kind="inline"/name="llm" to Kind="llm", config preserved verbatim.
func TestCompile_InlineLLMNode(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{
				"instance_id": "inline_llm_1",
				"definition_ref": {"kind":"inline","namespace":"builtin","name":"llm","version":1},
				"config": {"node_type":"llm","display_name":"Summarize","system_prompt":"You are concise.","user_prompt":"{{.input}}","output_var":"summary"}
			}
		],
		"entry_points": [
			{"instance_id":"ep1","slug":"main","protocol":"websocket","root":"inline_llm_1"}
		],
		"connections": []
	}`)
	spec, err := Compile(raw, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ep := spec.EntryPoints[0]
	if len(ep.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(ep.Nodes))
	}
	n := ep.Nodes[0]
	if n.Kind != "llm" {
		t.Errorf("kind: want llm, got %q", n.Kind)
	}
	var cfg InlineLLMConfig
	if err := json.Unmarshal(n.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.SystemPrompt != "You are concise." {
		t.Errorf("system_prompt: got %q", cfg.SystemPrompt)
	}
	if cfg.UserPrompt != "{{.input}}" {
		t.Errorf("user_prompt: got %q", cfg.UserPrompt)
	}
	if cfg.OutputVar != "summary" {
		t.Errorf("output_var: got %q", cfg.OutputVar)
	}
}

// AF-16: Compile maps definition_ref.kind="inline"/name="condition" to Kind="condition".
func TestCompile_InlineConditionNode(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{
				"instance_id": "inline_condition_1",
				"definition_ref": {"kind":"inline","namespace":"builtin","name":"condition","version":1},
				"config": {"node_type":"condition","expression":"{{eq .summary \"APPROVED\"}}"}
			}
		],
		"entry_points": [
			{"instance_id":"ep1","slug":"main","protocol":"websocket","root":"inline_condition_1"}
		],
		"connections": []
	}`)
	spec, err := Compile(raw, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ep := spec.EntryPoints[0]
	if len(ep.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(ep.Nodes))
	}
	if ep.Nodes[0].Kind != "condition" {
		t.Errorf("kind: want condition, got %q", ep.Nodes[0].Kind)
	}
}

// AF-17: an unknown inline name (e.g. a typo like "lmm") compiles to Kind="inline",
// not the raw name — so Validate can report unknown_inline_node instead of the
// workflow dying on an UnknownNodeKind at run time.
func TestCompile_InlineUnknownName(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{
				"instance_id": "inline_bad_1",
				"definition_ref": {"kind":"inline","namespace":"builtin","name":"lmm","version":1},
				"config": {}
			}
		],
		"entry_points": [
			{"instance_id":"ep1","slug":"main","protocol":"websocket","root":"inline_bad_1"}
		],
		"connections": []
	}`)
	spec, err := Compile(raw, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := spec.EntryPoints[0].Nodes[0].Kind; got != "inline" {
		t.Errorf("kind: want inline, got %q", got)
	}
}

// AF-18: true/false edge labels on a condition node's outgoing connections
// survive compilation into AppFlowEdge.Label.
func TestCompile_ConditionEdgeLabels(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version": 2,
		"components": [
			{"instance_id":"cond_1","definition_ref":{"kind":"inline","namespace":"builtin","name":"condition","version":1},"config":{"expression":"{{.input}}"}},
			{"instance_id":"agent_yes","definition_ref":{"kind":"agent","namespace":"default","name":"agent-yes","version":1}},
			{"instance_id":"agent_no","definition_ref":{"kind":"agent","namespace":"default","name":"agent-no","version":1}}
		],
		"entry_points":[{"instance_id":"ep1","slug":"main","protocol":"websocket","root":"cond_1"}],
		"connections":[
			{"source":"cond_1","target":"agent_yes","type":"flow_control","label":"true"},
			{"source":"cond_1","target":"agent_no","type":"flow_control","label":"false"}
		]
	}`)
	agents := map[string]string{"agent_yes": "uuid-yes", "agent_no": "uuid-no"}
	spec, err := Compile(raw, agents)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ep := spec.EntryPoints[0]
	labelByTarget := make(map[string]string)
	for _, e := range ep.Edges {
		labelByTarget[e.Target] = e.Label
	}
	if labelByTarget["agent_yes"] != "true" {
		t.Errorf("want edge to agent_yes label=true, got %q", labelByTarget["agent_yes"])
	}
	if labelByTarget["agent_no"] != "false" {
		t.Errorf("want edge to agent_no label=false, got %q", labelByTarget["agent_no"])
	}
}

// AF-19: Validate rejects a condition node with no expression configured.
func TestValidate_ConditionNoExpression(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "cond_1",
			Nodes: []AppFlowNode{
				{ID: "cond_1", Kind: "condition", Config: json.RawMessage(`{"expression":""}`)},
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
				{ID: "agent_b", Kind: "agent", AgentID: "uuid-b"},
			},
			Edges: []AppFlowEdge{
				{Source: "cond_1", Target: "agent_a", Label: "true"},
				{Source: "cond_1", Target: "agent_b", Label: "false"},
			},
		}},
	}
	errs := Validate(spec)
	found := false
	for _, e := range errs {
		if e.Code == "condition_no_expression" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected condition_no_expression, got: %+v", errs)
	}
}

// AF-20: Validate rejects a condition node whose outgoing edge count is not
// exactly 2 — both the 1-edge and the 3-edge case must produce condition_edge_count.
func TestValidate_ConditionEdgeCount(t *testing.T) {
	hasCode := func(errs []ValidationError, code string) bool {
		for _, e := range errs {
			if e.Code == code {
				return true
			}
		}
		return false
	}

	// 1 outgoing edge.
	specOne := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "cond_1",
			Nodes: []AppFlowNode{
				{ID: "cond_1", Kind: "condition", Config: json.RawMessage(`{"expression":"{{.input}}"}`)},
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
			},
			Edges: []AppFlowEdge{
				{Source: "cond_1", Target: "agent_a", Label: "true"},
			},
		}},
	}
	errsOne := Validate(specOne)
	if !hasCode(errsOne, "condition_edge_count") {
		t.Errorf("1-edge case: expected condition_edge_count, got: %+v", errsOne)
	}

	// 3 outgoing edges.
	specThree := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "cond_1",
			Nodes: []AppFlowNode{
				{ID: "cond_1", Kind: "condition", Config: json.RawMessage(`{"expression":"{{.input}}"}`)},
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
				{ID: "agent_b", Kind: "agent", AgentID: "uuid-b"},
				{ID: "agent_c", Kind: "agent", AgentID: "uuid-c"},
			},
			Edges: []AppFlowEdge{
				{Source: "cond_1", Target: "agent_a", Label: "true"},
				{Source: "cond_1", Target: "agent_b", Label: "false"},
				{Source: "cond_1", Target: "agent_c", Label: "extra"},
			},
		}},
	}
	errsThree := Validate(specThree)
	if !hasCode(errsThree, "condition_edge_count") {
		t.Errorf("3-edge case: expected condition_edge_count, got: %+v", errsThree)
	}
}

// AF-21: Validate rejects a condition node with exactly 2 outgoing edges when
// they are not labelled true/false (here: yes/no).
func TestValidate_ConditionMissingLabels(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "cond_1",
			Nodes: []AppFlowNode{
				{ID: "cond_1", Kind: "condition", Config: json.RawMessage(`{"expression":"{{.input}}"}`)},
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
				{ID: "agent_b", Kind: "agent", AgentID: "uuid-b"},
			},
			Edges: []AppFlowEdge{
				{Source: "cond_1", Target: "agent_a", Label: "yes"},
				{Source: "cond_1", Target: "agent_b", Label: "no"},
			},
		}},
	}
	errs := Validate(spec)
	found := false
	for _, e := range errs {
		if e.Code == "condition_missing_labels" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected condition_missing_labels, got: %+v", errs)
	}
}

// AF-22: Validate rejects an llm node with neither system_prompt nor user_prompt
// set, and passes when only system_prompt is set.
func TestValidate_LLMNoPrompt(t *testing.T) {
	hasCode := func(errs []ValidationError, code string) bool {
		for _, e := range errs {
			if e.Code == code {
				return true
			}
		}
		return false
	}

	specNoPrompt := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "llm_1",
			Nodes: []AppFlowNode{
				{ID: "llm_1", Kind: "llm", Config: json.RawMessage(`{}`)},
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
			},
			Edges: []AppFlowEdge{
				{Source: "llm_1", Target: "agent_a"},
			},
		}},
	}
	errs := Validate(specNoPrompt)
	if !hasCode(errs, "llm_no_prompt") {
		t.Errorf("expected llm_no_prompt, got: %+v", errs)
	}

	specSystemOnly := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "llm_1",
			Nodes: []AppFlowNode{
				{ID: "llm_1", Kind: "llm", Config: json.RawMessage(`{"system_prompt":"You are concise."}`)},
				{ID: "agent_a", Kind: "agent", AgentID: "uuid-a"},
			},
			Edges: []AppFlowEdge{
				{Source: "llm_1", Target: "agent_a"},
			},
		}},
	}
	errsSystemOnly := Validate(specSystemOnly)
	if hasCode(errsSystemOnly, "llm_no_prompt") {
		t.Errorf("system_prompt alone should pass llm_no_prompt, got: %+v", errsSystemOnly)
	}
}

// AF-23: Validate rejects an llm node with no incoming or outgoing edges (orphan).
func TestValidate_LLMOrphan(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "llm_1",
			Nodes: []AppFlowNode{
				{ID: "llm_1", Kind: "llm", Config: json.RawMessage(`{"user_prompt":"{{.input}}"}`)},
			},
			Edges: nil,
		}},
	}
	errs := Validate(spec)
	found := false
	for _, e := range errs {
		if e.Code == "llm_orphan" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected llm_orphan, got: %+v", errs)
	}
}

// AF-24: Validate reports unknown_inline_node for a node compiled with Kind="inline"
// (an unrecognised inline name).
func TestValidate_UnknownInlineNode(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "inline_bad_1",
			Nodes: []AppFlowNode{
				{ID: "inline_bad_1", Kind: "inline"},
			},
			Edges: nil,
		}},
	}
	errs := Validate(spec)
	found := false
	for _, e := range errs {
		if e.Code == "unknown_inline_node" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown_inline_node, got: %+v", errs)
	}
}

// AF-25: a well-formed LLM -> Condition -> 2 agents topology produces zero
// validation errors.
func TestValidate_InlineValidTopology(t *testing.T) {
	spec := &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "main",
			StartID: "llm_1",
			Nodes: []AppFlowNode{
				{ID: "llm_1", Kind: "llm", Config: json.RawMessage(`{"user_prompt":"{{.input}}","output_var":"summary"}`)},
				{ID: "cond_1", Kind: "condition", Config: json.RawMessage(`{"expression":"{{eq .summary \"APPROVED\"}}"}`)},
				{ID: "agent_yes", Kind: "agent", AgentID: "uuid-yes"},
				{ID: "agent_no", Kind: "agent", AgentID: "uuid-no"},
			},
			Edges: []AppFlowEdge{
				{Source: "llm_1", Target: "cond_1"},
				{Source: "cond_1", Target: "agent_yes", Label: "true"},
				{Source: "cond_1", Target: "agent_no", Label: "false"},
			},
		}},
	}
	errs := Validate(spec)
	if len(errs) != 0 {
		t.Errorf("expected no validation errors, got: %+v", errs)
	}
}
