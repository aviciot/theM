package agentgen

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aviciot/them/internal/nodedefs"
)

// ConfigFieldDoc, NodeExample, PortDef, EdgeRules and Meta are the portable
// node metadata types, now defined once in internal/nodedefs. Aliased here so
// existing agentgen code and callers keep compiling unchanged.
type ConfigFieldDoc = nodedefs.ConfigFieldDoc
type NodeExample = nodedefs.NodeExample
type PortDef = nodedefs.PortDef
type EdgeRules = nodedefs.EdgeRules
type Meta = nodedefs.Meta

// NodeDef is the central declaration for one canvas node type.
// It is the single source of truth for both runtime behaviour and
// the public canvas metadata exposed to the frontend via GET /api/v1/admin/node-types.
type NodeDef struct {
	// Meta is the portable node metadata (label, colors, ports, config field docs)
	// shared with any other canvas that adopts the node contract — see internal/nodedefs
	// and docs/NODE_REGISTRY_PLAN.md. Embedded so its JSON fields serialise inline,
	// exactly as if they were declared directly on NodeDef.
	nodedefs.Meta

	// ── Canvas-public fields (serialised and sent to the frontend) ────────────
	Type        StepType `json:"type"`
	Version     int      `json:"version"`      // schema version, default 1
	OutputArity string   `json:"output_arity"` // "single" | "multi" | "none"
	IsSource    bool     `json:"is_source"`    // valid pipeline start
	IsSink      bool     `json:"is_sink"`       // terminates the pipeline
	SingleInput bool     `json:"single_input"` // only one incoming edge allowed
	InputField  string   `json:"input_field,omitempty"` // config key used for auto-fill on connect
	// AcceptsDynamicInputs controls whether the user can drag a data-out port from
	// another node onto this node to create a named input port. False for routing-only
	// nodes (input, branch) that don't consume data vars directly.
	AcceptsDynamicInputs bool `json:"accepts_dynamic_inputs"`
	// DynamicOutputs is true when this node's output port names are derived from
	// its config at canvas-edit time rather than statically declared in OutputPorts.
	// True only for transform (functions[].output_var drives port names).
	DynamicOutputs bool `json:"dynamic_outputs"`

	// AppParams declares the runtime parameters this node type can consume.
	// Populated for HTTP, LLM, and A2A Call nodes; empty for all others.
	// The compiler aggregates these across all nodes into AgentSpec.RequiredParams.
	AppParams []AppParamDecl `json:"app_params,omitempty"`
	// DynamicOutputSource is a JSONPath-like expression that tells the frontend which
	// config field path drives dynamic output port names. Only meaningful when
	// DynamicOutputs=true. Format: "functions[].output_var" means iterate cfg.functions,
	// collect each item's output_var value. The frontend uses this generically without
	// per-type conditionals.
	DynamicOutputSource string `json:"dynamic_output_source,omitempty"`
	// Executable is NOT stored — computed from Execute != nil at serialisation time.

	// AllowedSuccessors lists step types that are valid next-hops from this node.
	// Empty means all types are allowed (no constraint beyond edge rules).
	AllowedSuccessors []StepType `json:"allowed_successors,omitempty"`

	// ── Execution policy fields ───────────────────────────────────────────────
	// DefaultPolicy is the baseline ExecutionPolicy for this node type.
	// The compiler uses it as the starting point before applying canvas overrides.
	DefaultPolicy ExecutionPolicy `json:"-"`
	// MaxPolicy caps any user canvas override. Fields with zero value are uncapped.
	MaxPolicy ExecutionPolicy `json:"-"`

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
	nodedefs.Meta

	Type                 StepType       `json:"type"`
	Version              int            `json:"version"`
	OutputArity          string         `json:"output_arity"`
	IsSource             bool           `json:"is_source"`
	IsSink               bool           `json:"is_sink"`
	SingleInput          bool           `json:"single_input"`
	AcceptsDynamicInputs bool           `json:"accepts_dynamic_inputs"`
	DynamicOutputs       bool           `json:"dynamic_outputs"`
	InputField           string         `json:"input_field,omitempty"`
	AppParams            []AppParamDecl `json:"app_params,omitempty"`
	DynamicOutputSource  string         `json:"dynamic_output_source,omitempty"`
	Executable           bool           `json:"executable"`

	// AllowedSuccessors — same as NodeDef, passed through for AI copilot use.
	AllowedSuccessors []StepType `json:"allowed_successors,omitempty"`

	// Execution policy metadata — exposed to the frontend for the Properties panel.
	DefaultPolicy ExecutionPolicy `json:"default_policy"`
	MaxPolicy     ExecutionPolicy `json:"max_policy"`
}

// ToInfo converts a NodeDef to its public API representation.
func (d *NodeDef) ToInfo() NodeTypeInfo {
	return NodeTypeInfo{
		Meta:                 d.Meta,
		Type:                 d.Type,
		Version:              d.Version,
		OutputArity:          d.OutputArity,
		IsSource:             d.IsSource,
		IsSink:               d.IsSink,
		SingleInput:          d.SingleInput,
		AcceptsDynamicInputs: d.AcceptsDynamicInputs,
		DynamicOutputs:       d.DynamicOutputs,
		InputField:           d.InputField,
		AppParams:            d.AppParams,
		DynamicOutputSource:  d.DynamicOutputSource,
		Executable:           d.Execute != nil,
		AllowedSuccessors:    d.AllowedSuccessors,
		DefaultPolicy:        d.DefaultPolicy,
		MaxPolicy:            d.MaxPolicy,
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
