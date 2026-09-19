// Node metadata for the app canvas's 6 flow-control/inline node kinds — the
// portable half of their description (label, colors, edge rules, config field
// docs), shared via internal/nodedefs so /admin/node-types can return them
// alongside the agentgen node family. See docs/NODE_REGISTRY_PLAN.md Phase 3.
//
// Unlike agentgen, appflow has no interpreter-bound NodeDef — Kind is a bare
// string switched on directly in compiler.go/workflow.go/validate.go/graph.go.
// This registry only carries nodedefs.Meta; there is no Execute/DeriveInputs
// analogue here because appflow's execution lives in the Temporal workflow
// dispatch loop, not a per-kind function pointer.
package appflow

import "github.com/aviciot/them/internal/nodedefs"

// AppCanvasNodeInfo is the JSON-serialisable view of one appflow node kind.
// Field shape matches agentgen.NodeTypeInfo exactly (same field names/JSON
// tags) so the two families merge into one array with one shape, per
// docs/NODE_REGISTRY_PLAN.md's "one endpoint, one shape" goal. Fields with no
// structural meaning for a given kind (e.g. output_arity for a pure control
// node) take a conservative default — see per-kind comments below.
type AppCanvasNodeInfo struct {
	nodedefs.Meta
	Type                 string `json:"type"`
	Version              int    `json:"version"`
	OutputArity          string `json:"output_arity"`
	IsSource             bool   `json:"is_source"`
	IsSink               bool   `json:"is_sink"`
	SingleInput          bool   `json:"single_input"`
	AcceptsDynamicInputs bool   `json:"accepts_dynamic_inputs"`
	DynamicOutputs       bool   `json:"dynamic_outputs"`
	Executable           bool   `json:"executable"`
}

// appCanvasNodeRegistry is the static, ordered list of the 6 app-canvas node
// kinds. Order is fixed here; AllAppCanvasNodeInfos returns a copy so callers
// cannot mutate the shared metadata.
var appCanvasNodeRegistry = []AppCanvasNodeInfo{
	{
		Type:    "llm",
		Version: 1,
		Meta: nodedefs.Meta{
			Label:       "LLM",
			Description: "Call a language model inline in the app flow. Provider and model are configured in the Runtime screen; prompt and output_var stay on the canvas.",
			Emoji:       "🧠",
			Color:       "#d0bcff",
			Edges:       nodedefs.EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 0, MaxOut: 0},
			ConfigFields: []nodedefs.ConfigFieldDoc{
				{Key: "system_prompt", Type: "string", Required: false, Description: "System/persona prompt. Supports {{.varname}} interpolation over flow vars.", Example: "You are a concise summarizer."},
				{Key: "user_prompt", Type: "string", Required: false, Description: "User-turn prompt. Supports {{.varname}} interpolation. Falls back to vars[\"input\"] when empty.", Example: "Summarise this: {{.input}}"},
				{Key: "output_var", Type: "string", Required: false, Description: "Flow variable name to store the response. Defaults to \"output\".", Example: "summary"},
				{Key: "max_tokens", Type: "int", Required: false, Description: "Maximum response tokens. 0 uses the activity default (1024).", Example: "1024"},
				{Key: "temperature", Type: "number", Required: false, Description: "Sampling temperature. Stored but not yet applied by the LLM call — see docs/NODE_REGISTRY_PLAN.md known gaps.", Example: "0.7"},
			},
			UsageNotes: "Provider/model are set once per app in the Runtime screen (GET|PUT /admin/applications/{id}/flow-llm-nodes), not on the canvas node — changing the model does not require a re-publish.",
		},
		OutputArity:          "single",
		AcceptsDynamicInputs: true,
		Executable:           true,
	},
	{
		Type:    "condition",
		Version: 1,
		Meta: nodedefs.Meta{
			Label:       "Condition",
			Description: "Route to one of two paths based on a Go template expression evaluated against flow vars.",
			Emoji:       "⑂",
			Color:       "#f97316",
			Edges:       nodedefs.EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 2, MaxOut: 2},
			ControlOutputPorts: []nodedefs.PortDef{
				{ID: "true", Label: "True path", Color: "#4ade80", MaxConnections: 1},
				{ID: "false", Label: "False path", Color: "#f87171", MaxConnections: 1},
			},
			ConfigFields: []nodedefs.ConfigFieldDoc{
				{Key: "expression", Type: "string", Required: true, Description: "Go template expression over flow vars. Truthy unless it renders \"\", \"false\", or \"0\".", Example: "{{eq .sentiment \"POSITIVE\"}}"},
			},
			UsageNotes: "Must have exactly two outgoing edges, labelled \"true\" and \"false\" (case-insensitive). A missing/unset variable in the expression renders as \"\" — takes the false branch, not an error.",
		},
		OutputArity: "multi",
		Executable:  true,
	},
	{
		Type:    "router",
		Version: 1,
		Meta: nodedefs.Meta{
			Label:       "Router",
			Description: "Classify the accumulated input into one of N intent labels using an LLM, then route to the matching outgoing edge.",
			Emoji:       "🔀",
			Color:       "#06b6d4",
			Edges:       nodedefs.EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 1, MaxOut: 0},
			ConfigFields: []nodedefs.ConfigFieldDoc{
				{Key: "output_labels", Type: "array", Required: true, Description: "Ordered list of intent labels the router can choose. Each must match the label on one outgoing edge.", Example: "[\"billing\", \"support\", \"other\"]"},
				{Key: "classifier_prompt", Type: "string", Required: false, Description: "System prompt for the LLM intent classifier. A default prompt is used when empty.", Example: "Classify the user's intent."},
			},
			UsageNotes: "Best-effort classifier: if the model returns a label with no matching edge and the router has exactly one outgoing edge, that edge is taken rather than failing the run. With multiple outgoing edges and no match, the run fails non-retryably.",
		},
		OutputArity: "multi",
		Executable:  true,
	},
	{
		Type:    "hil",
		Version: 1,
		Meta: nodedefs.Meta{
			Label:       "Human-in-Loop",
			Description: "Pause the flow and wait for a human approval or rejection before continuing.",
			Emoji:       "✋",
			Color:       "#a855f7",
			Edges:       nodedefs.EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 0, MaxOut: 0},
			ConfigFields: []nodedefs.ConfigFieldDoc{
				{Key: "approver_role", Type: "string", Required: false, Description: "Minimum RBAC role required to approve. Defaults to \"admin\".", Example: "admin"},
				{Key: "prompt", Type: "string", Required: false, Description: "Message shown to the human approver.", Example: "Approve this refund request?"},
				{Key: "timeout_seconds", Type: "int", Required: false, Description: "How long to wait before applying fallback_action. 0 or absent waits indefinitely.", Example: "3600"},
				{Key: "fallback_action", Type: "string", Required: false, Description: "What happens on timeout: \"reject\" (default), \"approve\", or \"abort\".", Example: "reject"},
			},
			UsageNotes: "A rejection ends the run with status \"rejected\" rather than routing onward — HIL has no false-path edge like Condition.",
		},
		OutputArity: "single",
		Executable:  true,
	},
	{
		Type:    "fork",
		Version: 1,
		Meta: nodedefs.Meta{
			Label:       "Fork",
			Description: "Fan out to multiple branches simultaneously. Branches run concurrently until they reach a Join.",
			Emoji:       "⑂",
			Color:       "#f59e0b",
			Edges:       nodedefs.EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 2, MaxOut: 0},
			UsageNotes:  "Requires at least 2 outgoing edges. Each branch runs to completion independently; results are merged at the paired Join node.",
		},
		OutputArity: "multi",
		Executable:  true,
	},
	{
		Type:    "join",
		Version: 1,
		Meta: nodedefs.Meta{
			Label:       "Join",
			Description: "Wait for all fanned-out branches from a paired Fork to complete, then merge their results and continue.",
			Emoji:       "⊕",
			Color:       "#10b981",
			Edges:       nodedefs.EdgeRules{MinIn: 2, MaxIn: 0, MinOut: 0, MaxOut: 0},
			UsageNotes:  "Requires at least 2 incoming edges. Branch results are merged with a newline separator before the flow continues past the join.",
		},
		OutputArity: "single",
		SingleInput: false,
		Executable:  true,
	},
}

// AllAppCanvasNodeInfos returns the public API representation of the 6
// app-canvas node kinds (llm, condition, router, hil, fork, join). Returns a
// fresh slice copy so callers cannot mutate the shared static registry.
func AllAppCanvasNodeInfos() []AppCanvasNodeInfo {
	out := make([]AppCanvasNodeInfo, len(appCanvasNodeRegistry))
	copy(out, appCanvasNodeRegistry)
	return out
}
