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
	// LLMNodes lists every inline LLM node across all entry points, with its
	// canvas-compiled provider/model. Mirrors agentgen's AgentLLMNodeSpec so the
	// Runtime screen can show/override provider+model without a re-publish.
	LLMNodes []AppFlowLLMNodeSpec `json:"llm_nodes,omitempty"`
	// HTTPNodes lists every HTTP node across all entry points so the Runtime
	// screen can display and manage their credentials (bearer_token, api_key).
	HTTPNodes []AppFlowHTTPNodeSpec `json:"http_nodes,omitempty"`
}

// AppFlowLLMNodeSpec describes one inline LLM node in a compiled app flow.
type AppFlowLLMNodeSpec struct {
	NodeID           string `json:"node_id"`
	CompiledProvider string `json:"compiled_provider"`
	CompiledModel    string `json:"compiled_model"`
}

// AppFlowHTTPNodeSpec describes one HTTP node in a compiled app flow.
// Credentials are not stored here — they live in app_flow_http_params.
type AppFlowHTTPNodeSpec struct {
	NodeID      string `json:"node_id"`
	Method      string `json:"method,omitempty"`
	URLTemplate string `json:"url_template,omitempty"`
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
	InstanceID       string          `json:"instance_id"`
	Name             string          `json:"name,omitempty"`
	DefinitionRef    defRef          `json:"definition_ref"`
	DefinitionID     string          `json:"definition_id,omitempty"`
	Config           json.RawMessage `json:"config,omitempty"`
	ParentInstanceID string          `json:"parent_instance_id,omitempty"`
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
	// Provider and Model mirror InlineLLMConfig so the Runtime screen can set
	// the LLM credential for the router node just like inline LLM nodes.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// CycleConfig is the configuration stored in a Cycle node's config JSON.
// The Cycle body is embedded as BodyNodes/BodyEdges so the outer graph
// treats the Cycle as one opaque node.
type CycleConfig struct {
	BreakWhenVar  string        `json:"break_when_var"`
	BreakWhenOp   string        `json:"break_when_op"` // "eq"|"neq"|"truthy"
	BreakWhenVal  string        `json:"break_when_val"`
	MaxIterations int           `json:"max_iterations"`
	EntryNodeID   string        `json:"entry_node_id"`
	BodyNodes     []AppFlowNode `json:"body_nodes"`
	BodyEdges     []AppFlowEdge `json:"body_edges"`
}

// WaitForInputConfig is the configuration stored in a Wait-for-Input node's config JSON.
// The node pauses the Temporal workflow until the user sends another WS/SSE message,
// then stores that message in OutputVar and continues.
type WaitForInputConfig struct {
	// Prompt is sent to the user while the workflow is paused (rendered against flow vars).
	// When empty no prompt message is emitted before waiting.
	Prompt string `json:"prompt,omitempty"`
	// OutputVar names the flow variable that receives the user's reply. Default: "input".
	OutputVar string `json:"output_var,omitempty"`
	// TimeoutSeconds: 0 = wait indefinitely (default for user-facing waits).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// HTTPNodeConfig is the configuration stored in an HTTP node's config JSON.
// Stored inside application_definitions.definition — travels with export/import
// automatically. Credentials (bearer_token, api_key) are NOT stored here;
// they live in app_flow_http_params, resolved by the activity at execution time.
type HTTPNodeConfig struct {
	// Method is the HTTP method: GET, POST, PUT, PATCH, DELETE.
	Method string `json:"method,omitempty"`
	// URLTemplate is a Go template rendered against flow vars, e.g. "https://api.example.com/orders/{{.order_id}}".
	URLTemplate string `json:"url_template,omitempty"`
	// Headers are static key→value pairs added to every request.
	Headers map[string]string `json:"headers,omitempty"`
	// BodyTemplate is a Go template rendered against flow vars for the request body.
	BodyTemplate string `json:"body_template,omitempty"`
	// Extractions maps response JSON paths to flow variable names.
	// e.g. [{var:"order_status", path:"$.status"}]
	Extractions []HTTPExtraction `json:"extractions,omitempty"`
	// TimeoutSeconds: 0 = 30s default, max 300.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// HTTPExtraction defines one JSON-path → flow-variable mapping.
type HTTPExtraction struct {
	Var  string `json:"var"`  // flow variable to write the extracted value into
	Path string `json:"path"` // dot-separated JSON path, e.g. "$.data.id"
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
		LLMNodes:         collectLLMNodes(epFlows),
		HTTPNodes:        collectHTTPNodes(epFlows),
	}, nil
}

// LLMOverride is a stored provider+model override for one inline LLM node.
type LLMOverride struct {
	Provider string
	Model    string
}

// ApplyLLMOverrides rewrites the Provider/Model fields inside each inline LLM
// node's Config for every node ID present in overrides. The workflow always
// reads Config, so this is the single choke point where a stored runtime
// override takes precedence over the canvas-compiled value — no workflow code
// needs to know overrides exist.
func ApplyLLMOverrides(spec *AppFlowSpec, overrides map[string]LLMOverride) {
	if spec == nil || len(overrides) == 0 {
		return
	}
	for i := range spec.EntryPoints {
		nodes := spec.EntryPoints[i].Nodes
		for j := range nodes {
			ov, ok := overrides[nodes[j].ID]
			if !ok {
				continue
			}
			switch nodes[j].Kind {
			case "llm":
				var cfg InlineLLMConfig
				if len(nodes[j].Config) > 0 {
					_ = json.Unmarshal(nodes[j].Config, &cfg)
				}
				cfg.Provider = ov.Provider
				cfg.Model = ov.Model
				if b, err := json.Marshal(cfg); err == nil {
					nodes[j].Config = b
				}
			case "router":
				var cfg RouterConfig
				if len(nodes[j].Config) > 0 {
					_ = json.Unmarshal(nodes[j].Config, &cfg)
				}
				cfg.Provider = ov.Provider
				cfg.Model = ov.Model
				if b, err := json.Marshal(cfg); err == nil {
					nodes[j].Config = b
				}
			}
		}
	}
}

// collectLLMNodes walks all entry points and returns one AppFlowLLMNodeSpec per
// inline LLM node, recording its node ID and compiled provider/model.
// Mirrors agentgen.collectLLMNodes for the app canvas.
func collectLLMNodes(epFlows []EPFlow) []AppFlowLLMNodeSpec {
	var nodes []AppFlowLLMNodeSpec
	for _, epf := range epFlows {
		for _, n := range epf.Nodes {
			switch n.Kind {
			case "llm":
				var cfg InlineLLMConfig
				if len(n.Config) > 0 {
					_ = json.Unmarshal(n.Config, &cfg)
				}
				nodes = append(nodes, AppFlowLLMNodeSpec{
					NodeID:           n.ID,
					CompiledProvider: cfg.Provider,
					CompiledModel:    cfg.Model,
				})
			case "router":
				var cfg RouterConfig
				if len(n.Config) > 0 {
					_ = json.Unmarshal(n.Config, &cfg)
				}
				nodes = append(nodes, AppFlowLLMNodeSpec{
					NodeID:           n.ID,
					CompiledProvider: cfg.Provider,
					CompiledModel:    cfg.Model,
				})
			}
		}
	}
	return nodes
}

// collectHTTPNodes walks all entry points and returns one AppFlowHTTPNodeSpec
// per HTTP node, recording its node ID, method, and URL template (no credentials).
func collectHTTPNodes(epFlows []EPFlow) []AppFlowHTTPNodeSpec {
	var nodes []AppFlowHTTPNodeSpec
	for _, epf := range epFlows {
		for _, n := range epf.Nodes {
			if n.Kind != "http" {
				continue
			}
			var cfg HTTPNodeConfig
			if len(n.Config) > 0 {
				_ = json.Unmarshal(n.Config, &cfg)
			}
			nodes = append(nodes, AppFlowHTTPNodeSpec{
				NodeID:      n.ID,
				Method:      cfg.Method,
				URLTemplate: cfg.URLTemplate,
			})
		}
	}
	return nodes
}

// compileEP compiles one entry point into an EPFlow.
func compileEP(ep epInst, compByID map[string]*compInst, outEdges map[string][]connDef, agentByInstanceID map[string]string) (EPFlow, error) {
	// Walk reachable nodes from the entry point via BFS.
	// The entry point connects to an orchestrator (root) or directly to an agent/router.
	// We collect all reachable component IDs and their edges.

	// Index child components (nodes nested inside a Cycle) so they are excluded
	// from the outer BFS — they will be compiled into the CycleConfig body instead.
	childNodes := make(map[string]bool)
	for i := range compByID {
		if compByID[i].ParentInstanceID != "" {
			childNodes[compByID[i].InstanceID] = true
		}
	}

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
		// Add only component nodes (not the EP itself, not cycle children).
		if cur != ep.InstanceID && !childNodes[cur] {
			nodeIDs = append(nodeIDs, cur)
		}
		for _, conn := range outEdges[cur] {
			if !visited[conn.Target] && !childNodes[conn.Target] {
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

	// For each cycle node, compile its children into the embedded CycleConfig body.
	for i := range nodes {
		if nodes[i].Kind != "cycle" {
			continue
		}
		cycleInstID := nodes[i].ID
		if err := compileCycleBody(&nodes[i], cycleInstID, compByID, outEdges, agentByInstanceID); err != nil {
			return EPFlow{}, fmt.Errorf("cycle %q: %w", cycleInstID, err)
		}
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
		return c.DefinitionRef.Name == "router" || c.DefinitionRef.Name == "hil"
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
		case "http":
			node.Kind = "http"
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
		case "cycle":
			node.Kind = "cycle"
		case "wait_for_input":
			node.Kind = "wait_for_input"
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

// compileCycleBody populates the CycleConfig.BodyNodes, BodyEdges, and EntryNodeID
// for the given cycle node by collecting all compInst children (ParentInstanceID == cycleInstID).
func compileCycleBody(cycleNode *AppFlowNode, cycleInstID string, compByID map[string]*compInst, outEdges map[string][]connDef, agentByInstanceID map[string]string) error {
	// Parse existing config (BreakWhenVar/Op/Val/MaxIterations from canvas).
	var cfg CycleConfig
	if len(cycleNode.Config) > 0 {
		_ = json.Unmarshal(cycleNode.Config, &cfg)
	}

	// Collect child instance IDs.
	var childIDs []string
	childSet := make(map[string]bool)
	for id, c := range compByID {
		if c.ParentInstanceID == cycleInstID {
			childIDs = append(childIDs, id)
			childSet[id] = true
		}
	}

	// Compile child nodes.
	bodyNodes := make([]AppFlowNode, 0, len(childIDs))
	for _, id := range childIDs {
		c := compByID[id]
		n, err := compileNode(c, agentByInstanceID)
		if err != nil {
			return fmt.Errorf("body node %q: %w", id, err)
		}
		bodyNodes = append(bodyNodes, n)
	}

	// Collect edges between children only.
	var bodyEdges []AppFlowEdge
	for _, id := range childIDs {
		for _, conn := range outEdges[id] {
			if childSet[conn.Target] {
				edge := AppFlowEdge{Source: conn.Source, Target: conn.Target}
				if src, ok := compByID[conn.Source]; ok && isLabelRoutingSource(src) {
					edge.Label = conn.edgeLabel()
				}
				bodyEdges = append(bodyEdges, edge)
			}
		}
	}

	// Determine entry node: prefer the explicit entry_node_id stored by the canvas
	// (set when user wires the cycle-in handle); fall back to auto-detect for
	// apps saved before the IN handle existed.
	if cfg.EntryNodeID == "" || !childSet[cfg.EntryNodeID] {
		inCount := make(map[string]int, len(childIDs))
		for _, e := range bodyEdges {
			inCount[e.Target]++
		}
		for _, id := range childIDs {
			if inCount[id] == 0 {
				cfg.EntryNodeID = id
				break
			}
		}
	}

	cfg.BodyNodes = bodyNodes
	cfg.BodyEdges = bodyEdges

	b, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal cycle config: %w", err)
	}
	cycleNode.Config = b
	return nil
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

