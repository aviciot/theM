package agentgen

import (
	"crypto/sha256"
	"fmt"
)

// deriveStepVars computes the input and output VarRefs for a compiled step.
//
// When the canvas step has explicit bindings (step.Inputs non-empty), each bound
// port is resolved to a VarRef by looking up the source step's output port in the
// compiled skill and reading the corresponding VarRef.Name.  SourceStep/SourcePort
// are populated for downstream validation.  Unbound ports fall back to
// DeriveInputs heuristics — the same behaviour as before explicit bindings.
//
// When step.Inputs is empty (no explicit bindings), DeriveInputs/DeriveOutputs
// are called directly, identical to the pre-binding path.
func deriveStepVars(step canvasStep, compiledSteps map[string]StepSpec) (inputs, outputs []VarRef) {
	nd, ok := LookupNode(step.Type)
	if !ok {
		return nil, nil
	}

	// Outputs are always derived from DeriveOutputs — bindings do not change what a step writes.
	if nd.DeriveOutputs != nil {
		outputs = nd.DeriveOutputs(step.Config)
	}

	// Inputs: explicit bindings take precedence over heuristic derivation per port.
	if len(step.Inputs) == 0 {
		// No explicit bindings — use heuristic (legacy path).
		if nd.DeriveInputs != nil {
			inputs = nd.DeriveInputs(step.Config)
		}
		return inputs, outputs
	}

	// Explicit bindings present: resolve each bound port to a VarRef.
	// Start with heuristic derivation to get the base set, then annotate
	// with binding metadata for ports that have an explicit binding.
	var heuristic []VarRef
	if nd.DeriveInputs != nil {
		heuristic = nd.DeriveInputs(step.Config)
	}

	// Build a lookup from heuristic VarRef by name so we can annotate.
	heuristicByName := make(map[string]VarRef, len(heuristic))
	for _, r := range heuristic {
		heuristicByName[r.Name] = r
	}

	// For each explicitly bound input port, resolve the var name from the source step's outputs.
	bound := make(map[string]bool, len(step.Inputs)) // track which heuristic vars got explicit bindings
	for portID, binding := range step.Inputs {
		srcSpec, srcOK := compiledSteps[binding.FromStep]
		if !srcOK {
			// Source step not (yet) compiled — emit a placeholder; validateBindings will report BROKEN_BINDING.
			inputs = append(inputs, VarRef{
				PortID:     portID,
				SourceStep: binding.FromStep,
				SourcePort: binding.FromPort,
			})
			continue
		}

		// Find the named output port's VarRef on the source step.
		var resolved *VarRef
		for _, outRef := range srcSpec.Outputs {
			if outRef.PortID == binding.FromPort || outRef.Name == binding.FromPort {
				resolved = &outRef
				break
			}
		}
		if resolved == nil {
			// Port not found — emit placeholder; validateBindings will report BROKEN_BINDING.
			inputs = append(inputs, VarRef{
				PortID:     portID,
				SourceStep: binding.FromStep,
				SourcePort: binding.FromPort,
			})
			continue
		}

		bound[resolved.Name] = true
		inputs = append(inputs, VarRef{
			Name:       resolved.Name,
			Required:   resolved.Required,
			PortID:     portID,
			SourceStep: binding.FromStep,
			SourcePort: binding.FromPort,
		})
	}

	// Any heuristic-derived vars that were NOT covered by an explicit binding
	// are kept as unbound heuristic inputs (they may be referenced in templates).
	for _, r := range heuristic {
		if !bound[r.Name] {
			inputs = append(inputs, r)
		}
	}

	return inputs, outputs
}

// resolveBindings does a second pass over a topologically-ordered skill to annotate
// steps that have explicit canvas bindings (cs.Inputs non-empty). Steps without
// bindings are returned unchanged — their heuristic VarRefs from topoSort are kept.
func resolveBindings(canvasSteps []canvasStep, compiled []StepSpec) []StepSpec {
	canvasByID := make(map[string]canvasStep, len(canvasSteps))
	for _, cs := range canvasSteps {
		canvasByID[cs.ID] = cs
	}
	specByID := make(map[string]StepSpec, len(compiled))
	for _, s := range compiled {
		specByID[s.ID] = s
	}

	result := make([]StepSpec, len(compiled))
	for i, spec := range compiled {
		cs, ok := canvasByID[spec.ID]
		if !ok || len(cs.Inputs) == 0 {
			result[i] = spec
			continue
		}
		ins, _ := deriveStepVars(cs, specByID)
		spec.Inputs = ins
		result[i] = spec
	}
	return result
}

// topoSort performs DFS-based topological sort and cycle detection.
// VarRefs are derived using DeriveInputs/DeriveOutputs heuristics only.
// Explicit binding resolution happens in a subsequent resolveBindings pass.
func topoSort(skillID string, steps []canvasStep, stepMap map[string]canvasStep) ([]StepSpec, []Issue) {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int, len(steps))
	var result []StepSpec
	var issues []Issue

	var dfs func(id string)
	dfs = func(id string) {
		if color[id] == black {
			return
		}
		if color[id] == grey {
			issues = append(issues, Issue{
				Severity: "error",
				Code:     "CYCLE_DETECTED",
				Message:  fmt.Sprintf("cycle detected in skill %q at step %q", skillID, id),
				SkillID:  skillID,
				NodeID:   id,
			})
			return
		}
		color[id] = grey
		step := stepMap[id]
		for _, nextID := range step.Next {
			dfs(nextID)
		}
		for _, arm := range step.Branches {
			for _, nextID := range arm.Next {
				dfs(nextID)
			}
		}
		color[id] = black
		// Heuristic-only derivation; explicit-binding resolution happens in resolveBindings.
		ins, outs := deriveStepVars(step, nil)
		result = append(result, StepSpec{
			ID:             step.ID,
			Type:           step.Type,
			Config:         step.Config,
			Next:           step.Next,
			Branches:       step.Branches,
			Inputs:         ins,
			Outputs:        outs,
			PolicyOverride: step.Policy,
		})
	}

	for _, step := range steps {
		if color[step.ID] == white {
			dfs(step.ID)
		}
	}

	if len(issues) > 0 {
		return nil, issues
	}

	// DFS post-order is reverse topological — reverse to get execution order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

func computeSpecHash(agentID, tenantID, definitionID, slug string) string {
	h := sha256.Sum256([]byte(agentID + "|" + tenantID + "|" + definitionID + "|" + slug))
	return fmt.Sprintf("%x", h)
}
