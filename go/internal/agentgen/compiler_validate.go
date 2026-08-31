package agentgen

import (
	"encoding/json"
	"fmt"
	"sort"
)

// validateStructural parses the raw JSON and checks spec-level invariants:
// display_name present, no duplicate skill IDs, valid slug.
func validateStructural(agentID, tenantID, definitionID, agentSlug string, raw json.RawMessage) (*canvasDefinition, string, []Issue) {
	var issues []Issue

	var def canvasDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, "", []Issue{errorf("INVALID_JSON", "definition is not valid JSON: "+err.Error())}
	}

	if def.AgentRoot.DisplayName == "" {
		issues = append(issues, errorf("MISSING_FIELD", "agent_root.display_name is required"))
	}

	// Validate execution_backend when present.
	switch def.AgentRoot.ExecutionBackend {
	case "", "local", "temporal":
		// valid
	default:
		issues = append(issues, errorf("INVALID_FIELD", "agent_root.execution_backend must be one of: \"\", \"local\", \"temporal\""))
	}

	// Validate duplicate skill IDs.
	seenSkills := make(map[string]bool)
	for _, cs := range def.Skills {
		if cs.SkillID == "" {
			issues = append(issues, errorf("MISSING_FIELD", "skill_id is required"))
			continue
		}
		if seenSkills[cs.SkillID] {
			issues = append(issues, Issue{Severity: "error", Code: "DUPLICATE_SKILL", Message: "duplicate skill_id", SkillID: cs.SkillID})
		}
		seenSkills[cs.SkillID] = true
	}

	if hasErrors(issues) {
		return nil, "", issues
	}

	slug := sanitizeSlug(agentSlug)
	if !slugRe.MatchString(slug) {
		return nil, "", []Issue{errorf("INVALID_SLUG", "agent slug must match ^[a-z0-9_]{1,48}$")}
	}

	return &def, slug, issues
}

// validateNodes checks each step's type and per-type config rules.
// SkillID and NodeID are stamped on every returned issue.
func validateNodes(def *canvasDefinition) []Issue {
	var issues []Issue
	for _, cs := range def.Skills {
		seenSteps := make(map[string]bool)
		for _, step := range cs.Steps {
			if step.ID == "" {
				issues = append(issues, Issue{Severity: "error", Code: "MISSING_FIELD", Message: "step id is required", SkillID: cs.SkillID})
				continue
			}
			if seenSteps[step.ID] {
				issues = append(issues, Issue{Severity: "error", Code: "DUPLICATE_STEP", Message: "duplicate step id", SkillID: cs.SkillID, NodeID: step.ID})
				continue
			}
			seenSteps[step.ID] = true

			nd, ok := LookupNode(step.Type)
			if !ok {
				issues = append(issues, Issue{
					Severity: "error",
					Code:     "UNKNOWN_STEP_TYPE",
					Message:  "unknown step type: " + string(step.Type),
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
				continue
			}

			if nd.Validate != nil {
				for _, iss := range nd.Validate(step) {
					iss.SkillID = cs.SkillID
					iss.NodeID = step.ID
					issues = append(issues, iss)
				}
			}
		}
	}
	return issues
}

// validateGraph checks edge references and detects cycles.
// Runs per-skill; SkillID is stamped on every returned issue.
func validateGraph(def *canvasDefinition) ([]Issue, map[string][]StepSpec) {
	var issues []Issue
	compiled := make(map[string][]StepSpec, len(def.Skills))

	for _, cs := range def.Skills {
		seenSteps := make(map[string]bool)
		stepMap := make(map[string]canvasStep, len(cs.Steps))
		for _, step := range cs.Steps {
			if step.ID == "" {
				continue // already caught by validateNodes
			}
			if seenSteps[step.ID] {
				continue // already caught by validateNodes
			}
			seenSteps[step.ID] = true
			stepMap[step.ID] = step
		}

		// Dangling edge refs.
		for _, step := range cs.Steps {
			if !seenSteps[step.ID] {
				continue
			}
			for _, nextID := range step.Next {
				if !seenSteps[nextID] {
					issues = append(issues, Issue{
						Severity: "error",
						Code:     "DANGLING_NEXT",
						Message:  fmt.Sprintf("step %q has next ref to unknown step %q", step.ID, nextID),
						SkillID:  cs.SkillID,
						NodeID:   step.ID,
					})
				}
			}
			for _, arm := range step.Branches {
				for _, nextID := range arm.Next {
					if !seenSteps[nextID] {
						issues = append(issues, Issue{
							Severity: "error",
							Code:     "DANGLING_BRANCH",
							Message:  fmt.Sprintf("branch arm in step %q refs unknown step %q", step.ID, nextID),
							SkillID:  cs.SkillID,
							NodeID:   step.ID,
						})
					}
				}
			}
		}

		if hasErrors(issues) {
			continue // skip topo-sort if edges are broken
		}

		// Build in/out degree maps, then apply EdgeRules from the node registry.
		inDegree  := make(map[string]int, len(cs.Steps))
		outDegree := make(map[string]int, len(cs.Steps))
		for _, step := range cs.Steps {
			if !seenSteps[step.ID] {
				continue
			}
			if _, ok := inDegree[step.ID]; !ok {
				inDegree[step.ID] = 0
				outDegree[step.ID] = 0
			}
			for _, nextID := range step.Next {
				inDegree[nextID]++
				outDegree[step.ID]++
			}
			for _, arm := range step.Branches {
				for _, nextID := range arm.Next {
					inDegree[nextID]++
					outDegree[step.ID]++
				}
			}
		}

		// Apply EdgeRules declared on each node type — single source of truth.
		for _, step := range cs.Steps {
			if !seenSteps[step.ID] {
				continue
			}
			nd, ok := LookupNode(step.Type)
			if !ok {
				continue // unknown type already caught in validateNodes
			}
			r := nd.Edges
			in  := inDegree[step.ID]
			out := outDegree[step.ID]

			if r.MinIn > 0 && in < r.MinIn {
				issues = append(issues, Issue{
					Severity: "warning",
					Code:     "MISSING_INPUT_EDGE",
					Message:  fmt.Sprintf("%s node requires at least %d incoming edge(s), has %d", nd.Label, r.MinIn, in),
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
			}
			if r.MaxIn > 0 && in > r.MaxIn {
				issues = append(issues, Issue{
					Severity: "error",
					Code:     "TOO_MANY_INPUT_EDGES",
					Message:  fmt.Sprintf("%s node allows at most %d incoming edge(s), has %d", nd.Label, r.MaxIn, in),
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
			}
			if nd.IsSource && in > 0 {
				issues = append(issues, Issue{
					Severity: "error",
					Code:     "SOURCE_HAS_INPUT",
					Message:  fmt.Sprintf("%s is a source node and must not have incoming edges", nd.Label),
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
			}
			if r.MinOut > 0 && out < r.MinOut {
				issues = append(issues, Issue{
					Severity: "warning",
					Code:     "MISSING_OUTPUT_EDGE",
					Message:  fmt.Sprintf("%s node requires at least %d outgoing edge(s), has %d", nd.Label, r.MinOut, out),
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
			}
			if nd.IsSink && out > 0 {
				issues = append(issues, Issue{
					Severity: "error",
					Code:     "SINK_HAS_OUTPUT",
					Message:  fmt.Sprintf("%s is a sink node and must not have outgoing edges", nd.Label),
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
			} else if r.MaxOut > 0 && out > r.MaxOut {
				issues = append(issues, Issue{
					Severity: "error",
					Code:     "TOO_MANY_OUTPUT_EDGES",
					Message:  fmt.Sprintf("%s node allows at most %d outgoing edge(s), has %d", nd.Label, r.MaxOut, out),
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
			}
		}

		ordered, cycleErrs := topoSort(cs.SkillID, cs.Steps, stepMap)
		issues = append(issues, cycleErrs...)
		if len(cycleErrs) == 0 {
			// Resolve explicit bindings now that all steps in this skill are compiled.
			// topoSort produces heuristic-only VarRefs; this pass annotates steps that
			// have explicit canvas bindings with SourceStep/SourcePort.
			ordered = resolveBindings(cs.Steps, ordered)
			compiled[cs.SkillID] = ordered
		}
	}
	return issues, compiled
}

// collectAgentParams walks all steps across all skills, gathering AppParamDecl
// entries from each step's NodeDef and validating AppParamKey references.
// Returns the deduplicated, sorted list of AgentParamSpec and any issues found.
func collectAgentParams(def *canvasDefinition) ([]AgentParamSpec, []Issue) {
	var params []AgentParamSpec

	for _, cs := range def.Skills {
		for _, step := range cs.Steps {
			key := extractAppParamKey(step)
			if key == "" {
				continue
			}

			// Look up the param declaration from the node type to get label/description/type.
			nd, _ := LookupNode(step.Type)
			var decl *AppParamDecl
			for i := range nd.AppParams {
				if nd.AppParams[i].Key == key {
					decl = &nd.AppParams[i]
					break
				}
			}

			// Build the per-instance composite key: "{stepID}:{paramKey}"
			instanceKey := step.ID + ":" + key

			// Human label: "{step label or ID} — {param label}"
			nodeLabel := step.Label
			if nodeLabel == "" {
				nodeLabel = step.ID
			}
			paramLabel := key
			if decl != nil {
				paramLabel = decl.Label
			}
			label := nodeLabel + " — " + paramLabel

			spec := AgentParamSpec{
				Key:         instanceKey,
				Label:       label,
				Type:        "secret",
				Required:    false,
				UsedByNodes: []string{step.ID},
			}
			if decl != nil {
				spec.Type = decl.Type
				spec.Required = decl.Required
				spec.Description = decl.Description
				spec.DefaultValue = decl.DefaultValue
			}

			params = append(params, spec)
		}
	}

	// Stable ordering for deterministic spec serialization.
	sort.Slice(params, func(i, j int) bool { return params[i].Key < params[j].Key })
	return params, nil
}

// extractAppParamKey reads the AppParamKey from an HTTP step's config JSON.
// Returns empty string for non-HTTP steps or steps with no AppParamKey.
func extractAppParamKey(step canvasStep) string {
	if step.Type != StepHTTP {
		return ""
	}
	var cfg HTTPStepConfig
	if len(step.Config) > 0 && json.Unmarshal(step.Config, &cfg) == nil {
		return cfg.AppParamKey
	}
	return ""
}

// collectLLMNodes walks all steps across all skills and returns one AgentLLMNodeSpec
// per LLM step, recording its node ID, label, and compiled provider/model.
func collectLLMNodes(def *canvasDefinition) []AgentLLMNodeSpec {
	var nodes []AgentLLMNodeSpec
	for _, cs := range def.Skills {
		for _, step := range cs.Steps {
			if step.Type != StepLLM {
				continue
			}
			var cfg LLMStepConfig
			if len(step.Config) > 0 {
				_ = json.Unmarshal(step.Config, &cfg)
			}
			label := step.Label
			if label == "" {
				label = step.ID
			}
			nodes = append(nodes, AgentLLMNodeSpec{
				NodeID:           step.ID,
				Label:            label,
				CompiledProvider: cfg.Provider,
				CompiledModel:    cfg.Model,
			})
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return nodes
}

// validateExecutability checks that every step type has a registered Execute
// function. severity controls whether stub nodes are "warning" or "error".
func validateExecutability(def *canvasDefinition, severity string) []Issue {
	var issues []Issue
	for _, cs := range def.Skills {
		for _, step := range cs.Steps {
			nd, ok := LookupNode(step.Type)
			if !ok {
				continue // already caught by validateNodes
			}
			if nd.Execute == nil {
				issues = append(issues, Issue{
					Severity: severity,
					Code:     "NODE_NOT_EXECUTABLE",
					Message:  fmt.Sprintf("node type %q is not yet implemented", step.Type),
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
			}
		}
	}
	return issues
}

// validateHumanWaitBackend checks that any skill containing a human_wait step is
// configured with execution_backend="temporal". LocalExecutor cannot block a
// goroutine waiting for external human input — only Temporal can durably park
// a workflow and resume it on signal.
// severity controls whether violations are "warning" (Validate) or "error" (CompileForPublish).
func validateHumanWaitBackend(def *canvasDefinition, severity string) []Issue {
	if def.AgentRoot.ExecutionBackend == "temporal" {
		return nil
	}
	var issues []Issue
	for _, cs := range def.Skills {
		for _, step := range cs.Steps {
			if StepType(step.Type) == StepHumanWait {
				issues = append(issues, Issue{
					Severity: severity,
					Code:     "HUMAN_WAIT_REQUIRES_TEMPORAL",
					Message:  "human_wait steps require execution_backend=\"temporal\" — LocalExecutor cannot durably pause for human input",
					SkillID:  cs.SkillID,
					NodeID:   step.ID,
				})
			}
		}
	}
	return issues
}

// validateBindings checks each canvas step's explicit bindings for coherence.
// Emits BROKEN_BINDING (at the given severity) when a binding's from_step does
// not exist in the compiled skill, or when from_port is not a declared output
// port on that step.  Steps with no explicit bindings are silently skipped —
// they are handled by the heuristic DeriveInputs path and the UNRESOLVED_INPUT
// data-flow check.
func validateBindings(def *canvasDefinition, compiled map[string][]StepSpec, severity string) []Issue {
	var issues []Issue

	// Build a flat step-id → StepSpec lookup across all skills.
	specByID := make(map[string]StepSpec)
	for _, steps := range compiled {
		for _, s := range steps {
			specByID[s.ID] = s
		}
	}

	// outputPortSet returns the set of valid port identifiers on a step's outputs.
	outputPortSet := func(stepID string) map[string]bool {
		s, ok := specByID[stepID]
		if !ok {
			return nil
		}
		m := make(map[string]bool, len(s.Outputs)*2)
		for _, o := range s.Outputs {
			if o.PortID != "" {
				m[o.PortID] = true
			}
			m[o.Name] = true // also match by var name
		}
		return m
	}

	for _, cs := range def.Skills {
		for _, step := range cs.Steps {
			if len(step.Inputs) == 0 {
				continue // no explicit bindings — heuristic path, nothing to validate here
			}

			for portID, binding := range step.Inputs {
				if _, ok := specByID[binding.FromStep]; !ok {
					issues = append(issues, Issue{
						Severity: severity,
						Code:     "BROKEN_BINDING",
						Message:  fmt.Sprintf("step %q input port %q references unknown source step %q", step.ID, portID, binding.FromStep),
						SkillID:  cs.SkillID,
						NodeID:   step.ID,
						Field:    portID,
					})
					continue
				}

				ports := outputPortSet(binding.FromStep)
				if ports != nil && !ports[binding.FromPort] {
					issues = append(issues, Issue{
						Severity: severity,
						Code:     "BROKEN_BINDING",
						Message:  fmt.Sprintf("step %q input port %q: source step %q has no output port %q", step.ID, portID, binding.FromStep, binding.FromPort),
						SkillID:  cs.SkillID,
						NodeID:   step.ID,
						Field:    portID,
					})
				}
			}
		}
	}
	return issues
}

// validateDataFlow runs path-sensitive data-flow analysis on each compiled skill.
//
// "Path-sensitive" means a variable is considered guaranteed at step S only when
// every execution path from the pipeline source to S writes it. This correctly
// handles branch convergence: if the true-path writes x but the false-path does
// not, x is not guaranteed at the join point, so a Required read of x there is
// flagged. The analysis uses a standard available-definitions lattice:
//
//	guaranteed[step] = {"input"} ∪ step.Outputs
//	                   ∪ intersection(guaranteed[pred] for each predecessor of step)
//
// Steps with no predecessors (the source) start with only {"input"}.
// severity controls whether UNRESOLVED_INPUT issues are "error" (publish) or
// "warning" (validate).
func validateDataFlow(def *canvasDefinition, compiled map[string][]StepSpec, severity string) []Issue {
	var issues []Issue
	for _, cs := range def.Skills {
		steps, ok := compiled[cs.SkillID]
		if !ok {
			continue // graph had errors; skip data-flow check
		}

		// Build a predecessor map from the forward Next edges on compiled steps.
		// compiled[cs.SkillID] is already in topological execution order.
		preds := make(map[string][]string, len(steps))
		for i := range steps {
			preds[steps[i].ID] = nil // ensure every step has an entry
		}
		for i := range steps {
			s := &steps[i]
			for _, nxt := range s.Next {
				preds[nxt] = append(preds[nxt], s.ID)
			}
			for _, arm := range s.Branches {
				for _, nxt := range arm.Next {
					preds[nxt] = append(preds[nxt], s.ID)
				}
			}
		}

		// guaranteed[stepID] = set of var names guaranteed on every path to this step.
		guaranteed := make(map[string]map[string]bool, len(steps))

		for i := range steps {
			step := &steps[i]

			// Compute the intersection of all predecessors' guaranteed sets.
			// A step with no predecessors is the pipeline source — only "input" is guaranteed.
			var incoming map[string]bool
			for _, predID := range preds[step.ID] {
				g, ok := guaranteed[predID]
				if !ok {
					continue
				}
				if incoming == nil {
					// First predecessor: copy its set.
					incoming = make(map[string]bool, len(g))
					for v := range g {
						incoming[v] = true
					}
				} else {
					// Subsequent predecessors: intersect.
					for v := range incoming {
						if !g[v] {
							delete(incoming, v)
						}
					}
				}
			}
			if incoming == nil {
				incoming = map[string]bool{}
			}

			// "input" is always pre-seeded from the invocation context.
			incoming["input"] = true

			// Check this step's inputs against guaranteed vars from predecessors.
			for _, inp := range step.Inputs {
				if !incoming[inp.Name] {
					sev := "warning"
					if inp.Required {
						sev = severity
					}
					issues = append(issues, Issue{
						Severity: sev,
						Code:     "UNRESOLVED_INPUT",
						Message:  fmt.Sprintf("step %q reads %q but it is not guaranteed on all incoming paths", step.ID, inp.Name),
						SkillID:  cs.SkillID,
						NodeID:   step.ID,
						Field:    inp.Name,
					})
				}
			}

			// Build this step's guaranteed set: predecessor intersection ∪ own outputs.
			g := make(map[string]bool, len(incoming)+len(step.Outputs))
			for v := range incoming {
				g[v] = true
			}
			for _, out := range step.Outputs {
				g[out.Name] = true
			}
			guaranteed[step.ID] = g
		}
	}
	return issues
}
