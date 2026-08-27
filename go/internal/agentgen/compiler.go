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
	DisplayName  string           `json:"display_name"`
	Description  string           `json:"description"`
	Version      string           `json:"version"`
	Icon         string           `json:"icon"`
	Category     string           `json:"category"`
	DefaultModel string           `json:"default_model"`
	Capabilities CapabilitiesSpec `json:"capabilities"`
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

type canvasStep struct {
	ID       string          `json:"id"`
	Label    string          `json:"label"`
	Type     StepType        `json:"type"`
	Config   json.RawMessage `json:"config"`
	Next     []string        `json:"next"`
	Branches []BranchArm     `json:"branches"`
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
		ID:             agentID,
		DefinitionID:   definitionID,
		Slug:           slug,
		TenantID:       tenantID,
		DefaultModel:   def.AgentRoot.DefaultModel,
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

// deriveStepVars calls the NodeDef DeriveInputs/DeriveOutputs hooks for a step
// and returns the result. Returns empty slices when hooks are nil.
func deriveStepVars(step canvasStep) (inputs, outputs []VarRef) {
	nd, ok := LookupNode(step.Type)
	if !ok {
		return nil, nil
	}
	if nd.DeriveInputs != nil {
		inputs = nd.DeriveInputs(step.Config)
	}
	if nd.DeriveOutputs != nil {
		outputs = nd.DeriveOutputs(step.Config)
	}
	return inputs, outputs
}

// validateDataFlow runs data-flow analysis on the topo-sorted compiled steps for each skill.
// It walks steps in execution order, accumulates a map of known writers, and checks that
// each step's Required inputs have a reachable upstream writer.
// severity controls whether UNRESOLVED_INPUT issues are "error" (publish) or "warning" (validate).
func validateDataFlow(def *canvasDefinition, compiled map[string][]StepSpec, severity string) []Issue {
	var issues []Issue
	for _, cs := range def.Skills {
		steps, ok := compiled[cs.SkillID]
		if !ok {
			continue // graph had errors; skip data-flow check
		}
		// writers maps varName → stepID of last writer seen so far in topo order.
		// Pre-seed "input" — it is always available at pipeline start from the
		// invocation context (user message). Every pipeline implicitly has this var.
		writers := map[string]string{"input": "__invocation__"}
		for _, step := range steps {
			// Check this step's inputs against known writers.
			for _, inp := range step.Inputs {
				if _, written := writers[inp.Name]; !written {
					sev := "warning"
					if inp.Required {
						sev = severity
					}
					issues = append(issues, Issue{
						Severity: sev,
						Code:     "UNRESOLVED_INPUT",
						Message:  fmt.Sprintf("step %q reads %q but no upstream step writes it", step.ID, inp.Name),
						SkillID:  cs.SkillID,
						NodeID:   step.ID,
						Field:    inp.Name,
					})
				}
			}
			// Register this step's outputs as known writers.
			for _, out := range step.Outputs {
				writers[out.Name] = step.ID
			}
		}
	}
	return issues
}

// topoSort performs DFS-based topological sort and cycle detection.
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
		ins, outs := deriveStepVars(step)
		result = append(result, StepSpec{
			ID:       step.ID,
			Type:     step.Type,
			Config:   step.Config,
			Next:     step.Next,
			Branches: step.Branches,
			Inputs:   ins,
			Outputs:  outs,
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
