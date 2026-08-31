package agentgen

import (
	"encoding/json"
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

	issues = append(issues, validateHumanWaitBackend(def, "warning")...)

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

	issues = append(issues, validateHumanWaitBackend(def, "error")...)
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

