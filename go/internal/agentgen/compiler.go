package agentgen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Issue is a structured validation finding returned by the BuildValidator.
// Severity: "error" blocks save/publish; "warning" is advisory only.
// SkillID/NodeID/Field are empty at higher scopes (spec-level, skill-level, node-level).
type Issue struct {
	Severity string `json:"severity"`          // "error" | "warning"
	Code     string `json:"code"`
	Message  string `json:"message"`
	SkillID  string `json:"skill_id,omitempty"`
	NodeID   string `json:"node_id,omitempty"`
	Field    string `json:"field,omitempty"`
}

func (e Issue) Error() string {
	parts := "[" + e.Severity + ":" + e.Code + "] " + e.Message
	if e.SkillID != "" {
		parts += " (skill: " + e.SkillID
		if e.NodeID != "" {
			parts += ", node: " + e.NodeID
		}
		parts += ")"
	}
	return parts
}

// CompileError is a backward-compatible alias for Issue.
// Deprecated: use Issue directly.
type CompileError = Issue

var slugRe = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)

// sanitizeSlug converts an agent slug to the valid format:
// lowercase, hyphens → underscores, truncated to 48 chars.
func sanitizeSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "_")
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

// canvasDefinition is the top-level shape of an agent definition canvas JSON.
type canvasDefinition struct {
	AgentRoot agentRoot     `json:"agent_root"`
	Skills    []canvasSkill `json:"skills"`
}

type agentRoot struct {
	DisplayName      string           `json:"display_name"`
	Description      string           `json:"description"`
	Version          string           `json:"version"`
	Icon             string           `json:"icon"`
	Category         string           `json:"category"`
	DefaultModel     string           `json:"default_model"`
	Capabilities     CapabilitiesSpec `json:"capabilities"`
	// ExecutionBackend selects the DAG execution engine.
	// Valid values: "" (default, same as "local"), "local", "temporal".
	// Copied verbatim into AgentSpec.ExecutionBackend at compile time.
	ExecutionBackend string           `json:"execution_backend,omitempty"`
}

type canvasSkill struct {
	SkillID     string       `json:"skill_id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Tags        []string     `json:"tags"`
	InputModes  []string     `json:"input_modes"`
	OutputModes []string     `json:"output_modes"`
	Steps       []canvasStep `json:"steps"`
}

// Binding is an explicit data binding declared on a canvas step's input port.
// FromStep is the step ID that produces the value; FromPort is the output port ID on that step.
type Binding struct {
	FromStep string `json:"from_step"`
	FromPort string `json:"from_port"`
}

type canvasStep struct {
	ID       string             `json:"id"`
	Label    string             `json:"label"`
	Type     StepType           `json:"type"`
	Config   json.RawMessage    `json:"config"`
	Next     []string           `json:"next"`
	Branches []BranchArm        `json:"branches"`
	// Inputs holds explicit data bindings: port ID → {from_step, from_port}.
	// Absent means no explicit bindings for this step (heuristic DeriveInputs used instead).
	Inputs   map[string]Binding `json:"inputs,omitempty"`
	// Policy is an optional per-node execution policy override from the canvas.
	// Absent means use the NodeDef default. The compiler clamps it to NodeDef.MaxPolicy.
	Policy   *ExecutionPolicy   `json:"policy,omitempty"`
}

// hasErrors reports whether any issue in the slice has severity "error".
func hasErrors(issues []Issue) bool {
	for _, iss := range issues {
		if iss.Severity == "error" {
			return true
		}
	}
	return false
}

// errorf constructs an error-severity Issue at spec level.
func errorf(code, msg string) Issue {
	return Issue{Severity: "error", Code: code, Message: msg}
}

// ── Stage 1: structural ──────────────────────────────────────────────────────

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

// ── Stage 2: node validation ─────────────────────────────────────────────────

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

// ── Stage 3: graph validation ────────────────────────────────────────────────

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

// ── Stage 3.5: AppParam collection and validation ────────────────────────────

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

// ── Stage 4: executability ───────────────────────────────────────────────────

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

// ── Public API ───────────────────────────────────────────────────────────────

// Validate runs structural + node + graph + param checks and returns all issues.
// Stub nodes (Execute == nil) emit warnings — the user is still building.
// Returns a non-nil *AgentSpec when parsing succeeds, even if issues exist,
// so callers can inspect the parsed graph. Returns nil only on structural failure.
func Validate(agentID, tenantID, definitionID, agentSlug string, raw json.RawMessage) (*AgentSpec, []Issue) {
	def, slug, issues := validateStructural(agentID, tenantID, definitionID, agentSlug, raw)
	if hasErrors(issues) {
		return nil, issues
	}

	issues = append(issues, validateNodes(def)...)
	graphIssues, compiled := validateGraph(def)
	issues = append(issues, graphIssues...)

	params, paramIssues := collectAgentParams(def)
	issues = append(issues, paramIssues...)

	llmNodes := collectLLMNodes(def)

	issues = append(issues, validateExecutability(def, "warning")...)

	issues = append(issues, validateDataFlow(def, compiled, "warning")...)

	issues = append(issues, validateBindings(def, compiled, "warning")...)

	spec := buildSpec(agentID, tenantID, definitionID, slug, def, compiled, params, llmNodes)
	return spec, issues
}

// CompileForPublish runs all stages. Stub nodes emit errors (not warnings).
// Returns nil spec + issues if any error-severity issue exists.
// Used exclusively by the publish handler.
func CompileForPublish(agentID, tenantID, definitionID, agentSlug string, raw json.RawMessage) (*AgentSpec, []Issue) {
	def, slug, issues := validateStructural(agentID, tenantID, definitionID, agentSlug, raw)
	if hasErrors(issues) {
		return nil, issues
	}

	issues = append(issues, validateNodes(def)...)
	if hasErrors(issues) {
		return nil, issues
	}

	graphIssues, compiled := validateGraph(def)
	issues = append(issues, graphIssues...)
	if hasErrors(issues) {
		return nil, issues
	}

	params, paramIssues := collectAgentParams(def)
	issues = append(issues, paramIssues...)
	if hasErrors(issues) {
		return nil, issues
	}

	llmNodes := collectLLMNodes(def)

	issues = append(issues, validateExecutability(def, "error")...)
	if hasErrors(issues) {
		return nil, issues
	}

	issues = append(issues, validateDataFlow(def, compiled, "error")...)
	if hasErrors(issues) {
		return nil, issues
	}

	issues = append(issues, validateBindings(def, compiled, "error")...)
	if hasErrors(issues) {
		return nil, issues
	}

	spec := buildSpec(agentID, tenantID, definitionID, slug, def, compiled, params, llmNodes)
	return spec, issues
}

// Compile is a backward-compatible alias for CompileForPublish.
// Deprecated: call Validate or CompileForPublish directly.
func Compile(agentID, tenantID, definitionID, agentSlug string, raw json.RawMessage) (*AgentSpec, []Issue) {
	return CompileForPublish(agentID, tenantID, definitionID, agentSlug, raw)
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// buildSpec assembles the AgentSpec from parsed components. Called after all
// validation stages pass.
func buildSpec(agentID, tenantID, definitionID, slug string, def *canvasDefinition, compiled map[string][]StepSpec, params []AgentParamSpec, llmNodes []AgentLLMNodeSpec) *AgentSpec {
	specHash := computeSpecHash(agentID, tenantID, definitionID, slug)
	_ = specHash

	version := def.AgentRoot.Version
	if version == "" {
		version = "1.0.0"
	}

	skills := make([]SkillSpec, 0, len(def.Skills))
	for _, cs := range def.Skills {
		steps := compiled[cs.SkillID]
		if steps == nil {
			steps = []StepSpec{}
		}
		skills = append(skills, SkillSpec{
			ID:          cs.SkillID,
			Name:        cs.Name,
			Description: cs.Description,
			Tags:        cs.Tags,
			InputModes:  cs.InputModes,
			OutputModes: cs.OutputModes,
			Steps:       steps,
		})
	}

	var requiredParams []AgentParamSpec
	if len(params) > 0 {
		requiredParams = params
	}

	var llmNodeList []AgentLLMNodeSpec
	if len(llmNodes) > 0 {
		llmNodeList = llmNodes
	}

	return &AgentSpec{
		ID:               agentID,
		DefinitionID:     definitionID,
		Slug:             slug,
		TenantID:         tenantID,
		DefaultModel:     def.AgentRoot.DefaultModel,
		ExecutionBackend: def.AgentRoot.ExecutionBackend,
		Card: CardSpec{
			Name:         def.AgentRoot.DisplayName,
			Description:  def.AgentRoot.Description,
			Version:      version,
			Icon:         def.AgentRoot.Icon,
			Category:     def.AgentRoot.Category,
			Capabilities: def.AgentRoot.Capabilities,
		},
		Skills:         skills,
		RequiredParams: requiredParams,
		LLMNodes:       llmNodeList,
	}
}

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

// topoSort performs DFS-based topological sort and cycle detection.
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
