package agentgen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
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
	DisplayName     string           `json:"display_name"`
	Description     string           `json:"description"`
	Version         string           `json:"version"`
	Icon            string           `json:"icon"`
	Category        string           `json:"category"`
	DefaultModel    string           `json:"default_model"`
	Capabilities    CapabilitiesSpec `json:"capabilities"`
	CredentialSlots []canvasCredSlot `json:"credential_slots"`
}

type canvasCredSlot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
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
// display_name present, no duplicate slots, no duplicate skill IDs, valid slug.
// Returns the parsed definition and any structural issues.
func validateStructural(agentID, tenantID, definitionID, agentSlug string, raw json.RawMessage) (*canvasDefinition, []CredentialSlotSpec, string, []Issue) {
	var issues []Issue

	var def canvasDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, nil, "", []Issue{errorf("INVALID_JSON", "definition is not valid JSON: "+err.Error())}
	}

	if def.AgentRoot.DisplayName == "" {
		issues = append(issues, errorf("MISSING_FIELD", "agent_root.display_name is required"))
	}

	// Collect and dedup credential slot names for cross-referencing.
	seenSlots := make(map[string]bool)
	credSlots := make([]CredentialSlotSpec, 0, len(def.AgentRoot.CredentialSlots))
	for _, slot := range def.AgentRoot.CredentialSlots {
		if slot.Name == "" {
			issues = append(issues, errorf("MISSING_FIELD", "credential slot name must be non-empty"))
			continue
		}
		if seenSlots[slot.Name] {
			issues = append(issues, Issue{Severity: "error", Code: "DUPLICATE_SLOT", Message: "duplicate credential slot", Field: slot.Name})
			continue
		}
		seenSlots[slot.Name] = true
		credSlots = append(credSlots, CredentialSlotSpec{
			Name:        slot.Name,
			Description: slot.Description,
			Required:    slot.Required,
		})
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
		return nil, nil, "", issues
	}

	slug := sanitizeSlug(agentSlug)
	if !slugRe.MatchString(slug) {
		return nil, nil, "", []Issue{errorf("INVALID_SLUG", "agent slug must match ^[a-z0-9_]{1,48}$")}
	}

	return &def, credSlots, slug, issues
}

// ── Stage 2: node validation ─────────────────────────────────────────────────

// validateNodes checks each step's type and per-type config rules.
// SkillID and NodeID are stamped on every returned issue.
func validateNodes(def *canvasDefinition, knownSlots map[string]bool) []Issue {
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
				for _, iss := range nd.Validate(step, knownSlots) {
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

		// Build in-degree map to find roots (steps with no incoming edges).
		inDegree := make(map[string]int, len(cs.Steps))
		for _, step := range cs.Steps {
			if !seenSteps[step.ID] {
				continue
			}
			if _, ok := inDegree[step.ID]; !ok {
				inDegree[step.ID] = 0
			}
			for _, nextID := range step.Next {
				inDegree[nextID]++
			}
			for _, arm := range step.Branches {
				for _, nextID := range arm.Next {
					inDegree[nextID]++
				}
			}
		}

		// Collect root steps (in-degree 0). A valid skill has exactly one root
		// (the source/input step). Multiple roots means disconnected subgraphs.
		var roots []string
		for id, deg := range inDegree {
			if deg == 0 {
				roots = append(roots, id)
			}
		}
		if len(roots) > 1 {
			for _, rootID := range roots {
				issues = append(issues, Issue{
					Severity: "warning",
					Code:     "DISCONNECTED_NODE",
					Message:  fmt.Sprintf("step %q is not connected to the pipeline", rootID),
					SkillID:  cs.SkillID,
					NodeID:   rootID,
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

// Validate runs structural + node + graph checks and returns all issues.
// Stub nodes (Execute == nil) emit warnings — the user is still building.
// Returns a non-nil *AgentSpec when parsing succeeds, even if issues exist,
// so callers can inspect the parsed graph. Returns nil only on structural failure.
func Validate(agentID, tenantID, definitionID, agentSlug string, raw json.RawMessage) (*AgentSpec, []Issue) {
	def, credSlots, slug, issues := validateStructural(agentID, tenantID, definitionID, agentSlug, raw)
	if hasErrors(issues) {
		return nil, issues
	}

	knownSlots := make(map[string]bool, len(credSlots))
	for _, s := range credSlots {
		knownSlots[s.Name] = true
	}

	// Collect node and graph issues without early-exit so the canvas sees
	// all problems at once.
	issues = append(issues, validateNodes(def, knownSlots)...)
	graphIssues, compiled := validateGraph(def)
	issues = append(issues, graphIssues...)
	issues = append(issues, validateExecutability(def, "warning")...)

	spec := buildSpec(agentID, tenantID, definitionID, slug, def, credSlots, compiled)
	return spec, issues
}

// CompileForPublish runs all four stages. Stub nodes emit errors (not warnings).
// Returns nil spec + issues if any error-severity issue exists.
// Used exclusively by the publish handler.
func CompileForPublish(agentID, tenantID, definitionID, agentSlug string, raw json.RawMessage) (*AgentSpec, []Issue) {
	def, credSlots, slug, issues := validateStructural(agentID, tenantID, definitionID, agentSlug, raw)
	if hasErrors(issues) {
		return nil, issues
	}

	knownSlots := make(map[string]bool, len(credSlots))
	for _, s := range credSlots {
		knownSlots[s.Name] = true
	}

	issues = append(issues, validateNodes(def, knownSlots)...)
	if hasErrors(issues) {
		return nil, issues
	}

	graphIssues, compiled := validateGraph(def)
	issues = append(issues, graphIssues...)
	if hasErrors(issues) {
		return nil, issues
	}

	issues = append(issues, validateExecutability(def, "error")...)
	if hasErrors(issues) {
		return nil, issues
	}

	spec := buildSpec(agentID, tenantID, definitionID, slug, def, credSlots, compiled)
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
func buildSpec(agentID, tenantID, definitionID, slug string, def *canvasDefinition, credSlots []CredentialSlotSpec, compiled map[string][]StepSpec) *AgentSpec {
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

	return &AgentSpec{
		ID:              agentID,
		DefinitionID:    definitionID,
		Slug:            slug,
		TenantID:        tenantID,
		DefaultModel:    def.AgentRoot.DefaultModel,
		CredentialSlots: credSlots,
		Card: CardSpec{
			Name:         def.AgentRoot.DisplayName,
			Description:  def.AgentRoot.Description,
			Version:      version,
			Icon:         def.AgentRoot.Icon,
			Category:     def.AgentRoot.Category,
			Capabilities: def.AgentRoot.Capabilities,
		},
		Skills: skills,
	}
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
		result = append(result, StepSpec{
			ID:       step.ID,
			Type:     step.Type,
			Config:   step.Config,
			Next:     step.Next,
			Branches: step.Branches,
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
