// Package appflow compiles an application canvas definition into an executable
// AppFlowSpec — the application-level analogue of agentgen.AgentSpec.
//
// Where agentgen.Compile operates within a single agent (steps → AgentSpec),
// appflow.Compile operates at the application level (agents → AppFlowSpec).
// Agents and inline nodes are the units of execution; middleware, Router,
// Condition, HIL, and Fork/Join nodes govern how control flows between them.
//
// File layout:
//   compiler.go — doc types + Compile (definition JSON → AppFlowSpec)
//   validate.go — Validate (AppFlowSpec → []ValidationError)
//   inline.go   — inline node config types + workflow-safe template helpers
//   workflow.go — the Temporal workflow and its activities
package appflow

import (
	"encoding/json"
	"fmt"
)

// AppFlowSpec is the compiled, reusable execution plan for an application canvas.
// Compiled on demand at run start from application_definitions.definition — it is
// not persisted anywhere; the spec is recompiled from the raw definition on every
// connection.
type AppFlowSpec struct {
	// ExecutionBackend selects the runtime.
	// "" or "local" → in-process goroutine loop.
	// "temporal" → AppFlowWorkflow via Temporal.
	ExecutionBackend string    `json:"execution_backend,omitempty"`
	EntryPoints      []EPFlow  `json:"entry_points"`
}

// EPFlow is the compiled flow for one entry point.
type EPFlow struct {
	Slug     string        `json:"slug"`
	Protocol string        `json:"protocol"`
	Nodes    []AppFlowNode `json:"nodes"`
	Edges    []AppFlowEdge `json:"edges"`
	StartID  string        `json:"start_id"` // first node ID after the entry point
}

// AppFlowNode represents one node in the application flow graph.
type AppFlowNode struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`      // "agent" | "middleware" | "router" | "hil" | "fork" | "join" | "llm" | "condition" | "inline"
	AgentID  string          `json:"agent_id,omitempty"`  // for kind=agent: resolved agents.id
	Config   json.RawMessage `json:"config,omitempty"`
}

// AppFlowEdge is a directed connection between two AppFlowNodes.
// Label is set on router outgoing edges to identify the intent branch.
type AppFlowEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Label  string `json:"label,omitempty"` // intent label for router outgoing edges
}

// ── Doc types (match AppDefinitionDoc wire format from the frontend) ──────────

// appDoc is the top-level shape of an application definition document (schema_version 2).
type appDoc struct {
	SchemaVersion int             `json:"schema_version"`
	Name          string          `json:"name,omitempty"`
	// ExecutionBackend is persisted in the doc when the canvas toolbar toggle is set.
	ExecutionBackend string        `json:"execution_backend,omitempty"`
	Components       []compInst    `json:"components"`
	EntryPoints      []epInst      `json:"entry_points"`
	Connections      []connDef     `json:"connections"`
}

type compInst struct {
	InstanceID   string          `json:"instance_id"`
	Name         string          `json:"name,omitempty"`
	DefinitionRef defRef         `json:"definition_ref"`
	DefinitionID  string         `json:"definition_id,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
}

type defRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
}

type epInst struct {
	InstanceID string          `json:"instance_id"`
	Slug       string          `json:"slug"`
	Protocol   string          `json:"protocol"`
	Root       string          `json:"root,omitempty"` // orchestrator instance_id connected to this EP
	Config     json.RawMessage `json:"config,omitempty"`
}

type connDef struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // "entry"|"delegation"|"tool"|"middleware"|"flow_control"
	Label  string `json:"label,omitempty"` // intent label on router outgoing edges
}

// RouterConfig is the configuration stored in a Router node's config JSON.
type RouterConfig struct {
	// ClassifierPrompt is the system prompt for the LLM intent classifier.
	// When empty, the router uses a default prompt.
	ClassifierPrompt string `json:"classifier_prompt,omitempty"`
	// OutputLabels defines the ordered list of intent labels the router can route to.
	// Each label must match the label on one outgoing flow_control edge.
	OutputLabels []string `json:"output_labels,omitempty"`
}

// HILConfig is the configuration stored in a HIL node's config JSON.
type HILConfig struct {
	// ApproverRole is the minimum RBAC role required to approve. Default: "admin".
	ApproverRole string `json:"approver_role,omitempty"`
	// TimeoutSeconds is how long to wait for approval before applying FallbackAction.
	// 0 or absent means wait indefinitely.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// FallbackAction is what to do when timeout expires.
	// "reject" (default) | "approve" | "abort".
	FallbackAction string `json:"fallback_action,omitempty"`
	// Prompt is the message shown to the human approver.
	Prompt string `json:"prompt,omitempty"`
}

// ── Compiler ──────────────────────────────────────────────────────────────────

// Compile takes a raw application definition JSON (schema_version 2) and
// produces an AppFlowSpec. agentByInstanceID maps component instance_id to the
// resolved agents.id UUID (caller must resolve from DB).
//
// The caller must supply agentByInstanceID so Compile does not need DB access.
// This keeps the compiler pure (testable without a DB connection).
func Compile(raw json.RawMessage, agentByInstanceID map[string]string) (*AppFlowSpec, error) {
	var doc appDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("appflow: unmarshal doc: %w", err)
	}
	if doc.SchemaVersion != 2 {
		return nil, fmt.Errorf("appflow: unsupported schema_version %d (want 2)", doc.SchemaVersion)
	}

	// Index components by instance_id.
	compByID := make(map[string]*compInst, len(doc.Components))
	for i := range doc.Components {
		c := &doc.Components[i]
		compByID[c.InstanceID] = c
	}

	// Build adjacency list from connections.
	outEdges := make(map[string][]connDef)
	for _, conn := range doc.Connections {
		outEdges[conn.Source] = append(outEdges[conn.Source], conn)
	}

	// Compile one EPFlow per entry point.
	epFlows := make([]EPFlow, 0, len(doc.EntryPoints))
	for _, ep := range doc.EntryPoints {
		epf, err := compileEP(ep, compByID, outEdges, agentByInstanceID)
		if err != nil {
			return nil, fmt.Errorf("appflow: entry point %q: %w", ep.Slug, err)
		}
		epFlows = append(epFlows, epf)
	}

	return &AppFlowSpec{
		ExecutionBackend: doc.ExecutionBackend,
		EntryPoints:      epFlows,
	}, nil
}

// compileEP compiles one entry point into an EPFlow.
func compileEP(ep epInst, compByID map[string]*compInst, outEdges map[string][]connDef, agentByInstanceID map[string]string) (EPFlow, error) {
	// Walk reachable nodes from the entry point via BFS.
	// The entry point connects to an orchestrator (root) or directly to an agent/router.
	// We collect all reachable component IDs and their edges.

	visited := make(map[string]bool)
	var nodeIDs []string // ordered by BFS discovery

	// Seed BFS from the EP and from ep.Root (the ep.Root connection is stored
	// directly on the EP instance rather than in the connections list).
	seeds := []string{ep.InstanceID}
	if ep.Root != "" {
		seeds = append(seeds, ep.Root)
	}
	queue := seeds
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		// Add only component nodes (not the EP itself).
		if cur != ep.InstanceID {
			nodeIDs = append(nodeIDs, cur)
		}
		for _, conn := range outEdges[cur] {
			if !visited[conn.Target] {
				queue = append(queue, conn.Target)
			}
		}
	}

	// Determine start_id: first non-EP node reachable from the EP.
	startID := ""
	for _, conn := range outEdges[ep.InstanceID] {
		if conn.Target != ep.InstanceID {
			startID = conn.Target
			break
		}
	}
	// Also check ep.Root (set by canvasToDoc for the EP→orch connection).
	if startID == "" && ep.Root != "" {
		startID = ep.Root
	}

	// Build AppFlowNodes for each reachable component.
	nodes := make([]AppFlowNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		c, ok := compByID[id]
		if !ok {
			continue
		}
		node, err := compileNode(c, agentByInstanceID)
		if err != nil {
			return EPFlow{}, fmt.Errorf("node %q: %w", id, err)
		}
		nodes = append(nodes, node)
	}

	// Build AppFlowEdges from connections between component nodes.
	var edges []AppFlowEdge
	for _, conn := range ep.connsBetween(nodeIDs, outEdges) {
		edge := AppFlowEdge{Source: conn.Source, Target: conn.Target}
		// Attach label from router config if available.
		if src, ok := compByID[conn.Source]; ok && isLabelRoutingSource(src) {
			edge.Label = conn.edgeLabel()
		}
		edges = append(edges, edge)
	}

	return EPFlow{
		Slug:     ep.Slug,
		Protocol: ep.Protocol,
		Nodes:    nodes,
		Edges:    edges,
		StartID:  startID,
	}, nil
}

// connsBetween returns all connections between nodes in nodeIDs (not from the EP itself).
func (ep epInst) connsBetween(nodeIDs []string, outEdges map[string][]connDef) []connDef {
	set := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		set[id] = true
	}
	var result []connDef
	for _, id := range nodeIDs {
		for _, conn := range outEdges[id] {
			if set[conn.Target] {
				result = append(result, conn)
			}
		}
	}
	return result
}

// edgeLabel returns the intent label from a router outgoing edge.
// The label is stored in conn.Label (set by the frontend when the user assigns
// an output_label to the edge via the Router properties panel).
func (c connDef) edgeLabel() string {
	return c.Label
}

// isLabelRoutingSource reports whether a component's outgoing edges carry
// routing labels: flow_control/router (intent labels) or inline/condition
// (true/false).
func isLabelRoutingSource(c *compInst) bool {
	switch c.DefinitionRef.Kind {
	case "flow_control":
		return c.DefinitionRef.Name == "router"
	case "inline":
		return c.DefinitionRef.Name == "condition"
	}
	return false
}

// compileNode converts a component instance to an AppFlowNode.
func compileNode(c *compInst, agentByInstanceID map[string]string) (AppFlowNode, error) {
	node := AppFlowNode{
		ID:     c.InstanceID,
		Config: c.Config,
	}

	switch c.DefinitionRef.Kind {
	case "agent":
		node.Kind = "agent"
		// agentByInstanceID must be populated by the caller from DB (tenant-scoped).
		// If the instance is not in the map, AgentID stays empty and Validate will
		// catch it as unresolved_agent — no fallback to DefinitionID to prevent
		// cross-tenant leakage.
		node.AgentID = agentByInstanceID[c.InstanceID]
	case "middleware":
		node.Kind = "middleware"
	case "inline":
		switch c.DefinitionRef.Name {
		case "llm":
			node.Kind = "llm"
		case "condition":
			node.Kind = "condition"
		default:
			// Unknown inline name: keep the kind so Validate reports it as
			// unknown_inline_node rather than the workflow failing at run time.
			node.Kind = "inline"
		}
	case "flow_control":
		switch c.DefinitionRef.Name {
		case "router":
			node.Kind = "router"
		case "hil":
			node.Kind = "hil"
		case "fork":
			node.Kind = "fork"
		case "join":
			node.Kind = "join"
		default:
			node.Kind = "flow_control"
		}
	case "orchestrator":
		// Orchestrators in the app canvas are execution containers.
		// Treat them as pass-through nodes for now.
		node.Kind = "orchestrator"
	default:
		node.Kind = c.DefinitionRef.Kind
	}

	return node, nil
}

// ── Definition helpers ────────────────────────────────────────────────────────

// ResolveAgentByInstanceID reads the server-stamped _resolved_agent_ids map from
// an application definition JSON. This map is written at publish time by
// PublishDefinition (service/publish.go) using the registry's tenant-scoped UUID
// for each agent component, so it is never influenced by client-supplied data.
//
// Old definitions (published before this stamp was added) return an empty map,
// which will cause Validate to emit unresolved_agent errors — prompting a re-publish.
func ResolveAgentByInstanceID(defJSON []byte) (map[string]string, error) {
	var doc struct {
		ResolvedAgentIDs map[string]string `json:"_resolved_agent_ids,omitempty"`
	}
	if err := json.Unmarshal(defJSON, &doc); err != nil {
		return nil, fmt.Errorf("appflow: parse definition for agent map: %w", err)
	}
	if doc.ResolvedAgentIDs == nil {
		return map[string]string{}, nil
	}
	return doc.ResolvedAgentIDs, nil
}

// LLMConfig holds the LLM provider and model for Router classification.
type LLMConfig struct {
	// ProviderName is the provider slug (e.g. "anthropic", "openai").
	// Defaults to "anthropic" when not configured.
	ProviderName string
	// Model is the model identifier.
	// When empty, the Router activity defaults to its own model constant.
	Model string
}

// LLMOrchConfig is the LLM configuration sourced from the bound app_orchestrators row.
// Populated from EPConfig (which reads ao.llm_provider / ao.llm_model at query time).
type LLMOrchConfig struct {
	Provider string
	Model    string
}

// ParseLLMConfig builds an LLMConfig from the orchestrator binding resolved for this
// entry point. The canvas stores llm_provider/llm_model on the app_orchestrators row
// (set at publish time from the orchestrator component config), not on the definition
// document root. Falls back to "anthropic" so the Router can always attempt DB key
// resolution rather than failing immediately with a blank provider.
func ParseLLMConfig(orch LLMOrchConfig) LLMConfig {
	provider := orch.Provider
	if provider == "" {
		provider = "anthropic"
	}
	return LLMConfig{ProviderName: provider, Model: orch.Model}
}

