package agentgen

import (
	"context"
	"encoding/json"
	"fmt"
)

// ConfigFieldDoc documents one config JSON key for a node type.
// Used by the LLM prompt builder to explain what each field does.
type ConfigFieldDoc struct {
	Key         string `json:"key"`
	Type        string `json:"type"`         // "string" | "int" | "bool" | "object" | "array"
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
// InputPorts/OutputPorts on NodeDef are static (same for every instance).
// Dynamic ports (e.g. transform outputs from functions[].output_var) are derived
// per-instance via DeriveInputs/DeriveOutputs instead.
type PortDef struct {
	ID       string `json:"id"`                 // stable identifier used in canvas binding references
	Label    string `json:"label"`              // human-readable name shown in the canvas UX
	Required bool   `json:"required"`           // for inputs: must be wired; for outputs: always produced
	Multi    bool   `json:"multi,omitempty"`    // for inputs: accepts multiple bindings (fan-in)
	TypeHint string `json:"type_hint,omitempty"` // loose tag: "text" | "json" | "any" — informational only
}

// EdgeRules declares the allowed incoming/outgoing edge counts for a node type.
// Zero means "no constraint". These are the single source of truth for both
// the backend graph validator and the frontend connection guard.
type EdgeRules struct {
	MinIn  int // minimum incoming edges required (0 = none required)
	MaxIn  int // maximum incoming edges allowed  (0 = unlimited)
	MinOut int // minimum outgoing edges required (0 = none required)
	MaxOut int // maximum outgoing edges allowed  (0 = unlimited)
}

// NodeDef is the central declaration for one canvas node type.
// It is the single source of truth for both runtime behaviour and
// the public canvas metadata exposed to the frontend via GET /api/v1/admin/node-types.
type NodeDef struct {
	// ── Canvas-public fields (serialised and sent to the frontend) ────────────
	Type        StepType       `json:"type"`
	Version     int            `json:"version"`      // schema version, default 1
	Label       string         `json:"label"`        // human-readable name shown in the builder
	Description string         `json:"description"`  // short tooltip shown on palette hover
	Emoji       string         `json:"emoji"`        // icon character shown on the node card
	OutputArity string         `json:"output_arity"` // "single" | "multi" | "none"
	IsSource    bool           `json:"is_source"`    // valid pipeline start
	IsSink      bool           `json:"is_sink"`      // terminates the pipeline
	SingleInput bool           `json:"single_input"` // only one incoming edge allowed
	Edges       EdgeRules      `json:"edges"`        // data-driven in/out degree constraints
	InputField  string         `json:"input_field,omitempty"` // config key used for auto-fill on connect
	// AppParams declares the runtime parameters this node type can consume.
	// Populated for HTTP, LLM, and A2A Call nodes; empty for all others.
	// The compiler aggregates these across all nodes into AgentSpec.RequiredParams.
	AppParams []AppParamDecl `json:"app_params,omitempty"`
	// InputPorts declares the named data input ports for this node type.
	// Nil for types with dynamic inputs (transform) or no data inputs (input step).
	// Used by the frontend to render port sockets and by the compiler to resolve explicit bindings.
	InputPorts []PortDef `json:"input_ports,omitempty"`
	// OutputPorts declares the named data output ports for this node type.
	// Nil for types with dynamic outputs (transform, http extractions) or no data outputs (response, branch).
	OutputPorts []PortDef `json:"output_ports,omitempty"`
	// Executable is NOT stored — computed from Execute != nil at serialisation time.

	// ── LLM knowledge fields (serialised, used by AI copilot) ────────────────
	// ConfigFields documents each config JSON key, used to build LLM system prompts.
	ConfigFields []ConfigFieldDoc `json:"config_fields,omitempty"`
	// UsageNotes is a paragraph of guidance for the LLM: when to choose this node,
	// common pitfalls, and relationship to other node types.
	UsageNotes string `json:"usage_notes,omitempty"`
	// Examples shows 1-2 worked config examples the LLM can use as templates.
	Examples []NodeExample `json:"examples,omitempty"`
	// AllowedSuccessors lists step types that are valid next-hops from this node.
	// Empty means all types are allowed (no constraint beyond edge rules).
	AllowedSuccessors []StepType `json:"allowed_successors,omitempty"`

	// ── Runtime-only fields (not serialised) ─────────────────────────────────
	// Validate checks per-type config constraints at compile time.
	Validate func(step canvasStep) []Issue `json:"-"`
	// Execute runs the step. nil means the type is not yet implemented.
	Execute func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
		step *StepSpec, vars PipelineVars, result *ExecutionResult) error `json:"-"`
	// DeriveInputs returns the variables this step instance reads from PipelineVars.
	// Called by the compiler with the step's raw config JSON.
	// nil means "no static derivation for this type" — treated as empty inputs.
	DeriveInputs func(cfg json.RawMessage) []VarRef `json:"-"`
	// DeriveOutputs returns the variables this step instance writes to PipelineVars.
	// Called by the compiler with the step's raw config JSON.
	// nil means "no static derivation for this type" — treated as empty outputs.
	DeriveOutputs func(cfg json.RawMessage) []VarRef `json:"-"`
}

// NodeTypeInfo is the JSON-serialisable view of a NodeDef sent to the frontend.
// Executable is derived here so NodeDef itself never stores duplicated state.
type NodeTypeInfo struct {
	Type        StepType       `json:"type"`
	Version     int            `json:"version"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Emoji       string         `json:"emoji"`
	OutputArity string         `json:"output_arity"`
	IsSource    bool           `json:"is_source"`
	IsSink      bool           `json:"is_sink"`
	SingleInput bool           `json:"single_input"`
	Edges       EdgeRules      `json:"edges"`
	InputField  string         `json:"input_field,omitempty"`
	AppParams   []AppParamDecl `json:"app_params,omitempty"`
	InputPorts  []PortDef      `json:"input_ports,omitempty"`
	OutputPorts []PortDef      `json:"output_ports,omitempty"`
	Executable  bool           `json:"executable"`

	// LLM knowledge fields — same as NodeDef, passed through for AI copilot use.
	ConfigFields      []ConfigFieldDoc `json:"config_fields,omitempty"`
	UsageNotes        string           `json:"usage_notes,omitempty"`
	Examples          []NodeExample    `json:"examples,omitempty"`
	AllowedSuccessors []StepType       `json:"allowed_successors,omitempty"`
}

// ToInfo converts a NodeDef to its public API representation.
func (d *NodeDef) ToInfo() NodeTypeInfo {
	return NodeTypeInfo{
		Type:              d.Type,
		Version:           d.Version,
		Label:             d.Label,
		Description:       d.Description,
		Emoji:             d.Emoji,
		OutputArity:       d.OutputArity,
		IsSource:          d.IsSource,
		IsSink:            d.IsSink,
		SingleInput:       d.SingleInput,
		Edges:             d.Edges,
		InputField:        d.InputField,
		AppParams:         d.AppParams,
		InputPorts:        d.InputPorts,
		OutputPorts:       d.OutputPorts,
		Executable:        d.Execute != nil,
		ConfigFields:      d.ConfigFields,
		UsageNotes:        d.UsageNotes,
		Examples:          d.Examples,
		AllowedSuccessors: d.AllowedSuccessors,
	}
}

var nodeRegistry = map[StepType]*NodeDef{}

// RegisterNode adds a NodeDef to the registry. Called from nodes.go init().
func RegisterNode(def NodeDef) {
	nodeRegistry[def.Type] = &def
}

// LookupNode returns the NodeDef for a StepType, or (nil, false) if not registered.
func LookupNode(t StepType) (*NodeDef, bool) {
	d, ok := nodeRegistry[t]
	return d, ok
}

// KnownStepTypes returns all registered step type names.
func KnownStepTypes() []StepType {
	out := make([]StepType, 0, len(nodeRegistry))
	for t := range nodeRegistry {
		out = append(out, t)
	}
	return out
}

// AllNodeTypeInfos returns the public API representation of every registered node type.
func AllNodeTypeInfos() []NodeTypeInfo {
	out := make([]NodeTypeInfo, 0, len(nodeRegistry))
	for _, def := range nodeRegistry {
		out = append(out, def.ToInfo())
	}
	return out
}

// ValidateDefinitionJSON validates raw agent definition JSON using the compiler.
// Uses synthetic IDs so callers need not supply real DB identifiers.
// Returns the slice of issues (may be empty) and a non-nil error only when the JSON
// cannot be parsed at all (structural failure — no *AgentSpec returned by Validate).
func ValidateDefinitionJSON(raw []byte) ([]Issue, error) {
	spec, issues := Validate("gen", "gen", "gen", "gen_agent", raw)
	if spec == nil && len(issues) == 0 {
		return nil, fmt.Errorf("definition JSON could not be parsed")
	}
	return issues, nil
}
