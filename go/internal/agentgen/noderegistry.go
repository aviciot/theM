package agentgen

import "context"

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
	Type        StepType  `json:"type"`
	Version     int       `json:"version"`      // schema version, default 1
	Label       string    `json:"label"`        // human-readable name shown in the builder
	Description string    `json:"description"`  // short tooltip shown on palette hover
	Emoji       string    `json:"emoji"`        // icon character shown on the node card
	OutputArity string    `json:"output_arity"` // "single" | "multi" | "none"
	IsSource    bool      `json:"is_source"`    // valid pipeline start
	IsSink      bool      `json:"is_sink"`      // terminates the pipeline
	SingleInput bool      `json:"single_input"` // only one incoming edge allowed
	Edges       EdgeRules `json:"edges"`        // data-driven in/out degree constraints
	InputField  string    `json:"input_field,omitempty"` // config key used for auto-fill on connect
	// Executable is NOT stored — computed from Execute != nil at serialisation time.

	// ── Runtime-only fields (not serialised) ─────────────────────────────────
	// Validate checks per-type config constraints at compile time.
	Validate func(step canvasStep) []Issue `json:"-"`
	// Execute runs the step. nil means the type is not yet implemented.
	Execute func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
		step *StepSpec, vars PipelineVars, result *ExecutionResult) error `json:"-"`
}

// NodeTypeInfo is the JSON-serialisable view of a NodeDef sent to the frontend.
// Executable is derived here so NodeDef itself never stores duplicated state.
type NodeTypeInfo struct {
	Type        StepType  `json:"type"`
	Version     int       `json:"version"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	Emoji       string    `json:"emoji"`
	OutputArity string    `json:"output_arity"`
	IsSource    bool      `json:"is_source"`
	IsSink      bool      `json:"is_sink"`
	SingleInput bool      `json:"single_input"`
	Edges       EdgeRules `json:"edges"`
	InputField  string    `json:"input_field,omitempty"`
	Executable  bool      `json:"executable"`
}

// ToInfo converts a NodeDef to its public API representation.
func (d *NodeDef) ToInfo() NodeTypeInfo {
	return NodeTypeInfo{
		Type:        d.Type,
		Version:     d.Version,
		Label:       d.Label,
		Description: d.Description,
		Emoji:       d.Emoji,
		OutputArity: d.OutputArity,
		IsSource:    d.IsSource,
		IsSink:      d.IsSink,
		SingleInput: d.SingleInput,
		Edges:       d.Edges,
		InputField:  d.InputField,
		Executable:  d.Execute != nil,
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
