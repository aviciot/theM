// Package nodedefs holds the portable half of a canvas node type's description —
// the metadata shared by the agent builder (agentgen) and, eventually, the app
// canvas (appflow). It has no dependency on either runtime's interpreter.
//
// What stays out on purpose: execution (Execute/DeriveInputs/DeriveOutputs),
// StepType, AppParamDecl and ExecutionPolicy remain in agentgen — they are typed
// to its interpreter and compiler. See docs/NODE_REGISTRY_PLAN.md Phase 2.
package nodedefs

// ConfigFieldDoc documents one config JSON key for a node type.
// Used by the LLM prompt builder to explain what each field does.
type ConfigFieldDoc struct {
	Key         string `json:"key"`
	Type        string `json:"type"` // "string" | "int" | "bool" | "object" | "array"
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Example     string `json:"example,omitempty"`
}

// NodeExample is a short worked example for a node type, used in the LLM system prompt.
type NodeExample struct {
	Description string         `json:"description"`
	Config      map[string]any `json:"config"`
}

// PortDef declares one named data port on a node type.
// Port IDs are permanent stable identifiers — never rename after registration.
// InputPorts/OutputPorts are static (same for every instance). Dynamic ports
// (e.g. transform outputs from functions[].output_var) are derived per-instance
// by the owning runtime instead.
type PortDef struct {
	ID       string `json:"id"`                  // stable identifier used in canvas binding references
	Label    string `json:"label"`               // human-readable name shown in the canvas UX
	Required bool   `json:"required"`            // for inputs: must be wired; for outputs: always produced
	Multi    bool   `json:"multi,omitempty"`     // for inputs: accepts multiple bindings (fan-in)
	TypeHint string `json:"type_hint,omitempty"` // loose tag: "text" | "json" | "any" — informational only
	// Color overrides the node accent color for this specific port's handle.
	// Used for semantically distinct ports (e.g. branch true=green, false=red).
	// Empty means use the node's Color.
	Color string `json:"color,omitempty"`
	// MaxConnections caps how many edges may attach to this port. 0 = unlimited.
	MaxConnections int `json:"max_connections,omitempty"`
}

// EdgeRules declares the allowed incoming/outgoing edge counts for a node type.
// Zero means "no constraint". These are the single source of truth for both
// the backend graph validator and the frontend connection guard.
type EdgeRules struct {
	MinIn  int `json:"min_in"`  // minimum incoming edges required (0 = none required)
	MaxIn  int `json:"max_in"`  // maximum incoming edges allowed  (0 = unlimited)
	MinOut int `json:"min_out"` // minimum outgoing edges required (0 = none required)
	MaxOut int `json:"max_out"` // maximum outgoing edges allowed  (0 = unlimited)
}

// Meta is the portable description of one canvas node type — the metadata a
// frontend needs to render, label, connect and configure a node, independent
// of which runtime executes it.
type Meta struct {
	Label       string    `json:"label"`       // human-readable name shown in the builder
	Description string    `json:"description"` // short tooltip shown on palette hover
	Emoji       string    `json:"emoji"`        // icon character shown on the node card
	Color       string    `json:"color"`        // primary accent CSS hex color
	BgColor     string    `json:"bg_color"`     // card background CSS hex color
	Edges       EdgeRules `json:"edges"`        // data-driven in/out degree constraints

	// InputPorts declares the named data input ports for this node type.
	// Nil for types with dynamic inputs or no data inputs.
	InputPorts []PortDef `json:"input_ports,omitempty"`
	// OutputPorts declares the named data output ports for this node type.
	// Nil for types with dynamic outputs or no data outputs.
	OutputPorts []PortDef `json:"output_ports,omitempty"`
	// ControlOutputPorts declares named control-flow output ports for nodes that
	// have multiple named control exits (e.g. branch: true/false paths).
	// Empty means a single anonymous control output — the common case.
	ControlOutputPorts []PortDef `json:"control_output_ports,omitempty"`

	// ConfigFields documents each config JSON key, used to build LLM system prompts
	// and to render the properties panel.
	ConfigFields []ConfigFieldDoc `json:"config_fields,omitempty"`
	// UsageNotes is a paragraph of guidance for the LLM: when to choose this node,
	// common pitfalls, and relationship to other node types.
	UsageNotes string `json:"usage_notes,omitempty"`
	// Examples shows 1-2 worked config examples the LLM can use as templates.
	Examples []NodeExample `json:"examples,omitempty"`
}
