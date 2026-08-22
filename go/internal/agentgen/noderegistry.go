package agentgen

import "context"

// NodeDef is the central declaration for one canvas node type.
// It describes the node's runtime properties and wires together compile-time
// validation (Validate) with runtime execution (Execute).
type NodeDef struct {
	Type        StepType
	OutputArity string // "single" | "multi" | "none"
	IsSource    bool   // valid pipeline start (only input)
	IsSink      bool   // terminates pipeline (response, stream_out)
	// Validate checks per-type config constraints at compile time.
	// Returns CompileErrors (nil/empty = valid).
	Validate func(step canvasStep, knownSlots map[string]bool) []CompileError
	// Execute runs the step at runtime. nil = not yet implemented.
	Execute func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
		step *StepSpec, vars PipelineVars, result *ExecutionResult) error
}

var nodeRegistry = map[StepType]*NodeDef{}

// RegisterNode adds a NodeDef to the registry. Typically called from init().
func RegisterNode(def NodeDef) {
	nodeRegistry[def.Type] = &def
}

// LookupNode returns the NodeDef for a given StepType, or (nil, false) if not registered.
func LookupNode(t StepType) (*NodeDef, bool) {
	d, ok := nodeRegistry[t]
	return d, ok
}

// KnownStepTypes returns all registered step type names. Used by tests and tooling.
func KnownStepTypes() []StepType {
	out := make([]StepType, 0, len(nodeRegistry))
	for t := range nodeRegistry {
		out = append(out, t)
	}
	return out
}
