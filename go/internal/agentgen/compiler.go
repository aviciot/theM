package agentgen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// CompileError describes one compile-time problem in an agent definition.
type CompileError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Context narrows the location: skill ID, step ID, or slot name.
	Context string `json:"context,omitempty"`
}

func (e CompileError) Error() string {
	if e.Context != "" {
		return fmt.Sprintf("[%s] %s (context: %s)", e.Code, e.Message, e.Context)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}


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
	AgentRoot  agentRoot         `json:"agent_root"`
	Skills     []canvasSkill     `json:"skills"`
}

type agentRoot struct {
	DisplayName     string               `json:"display_name"`
	Description     string               `json:"description"`
	Version         string               `json:"version"`
	Icon            string               `json:"icon"`
	Category        string               `json:"category"`
	DefaultModel    string               `json:"default_model"`
	Capabilities    CapabilitiesSpec     `json:"capabilities"`
	CredentialSlots []canvasCredSlot     `json:"credential_slots"`
}

type canvasCredSlot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type canvasSkill struct {
	SkillID     string        `json:"skill_id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Tags        []string      `json:"tags"`
	InputModes  []string      `json:"input_modes"`
	OutputModes []string      `json:"output_modes"`
	Steps       []canvasStep  `json:"steps"`
}

type canvasStep struct {
	ID       string          `json:"id"`
	Type     StepType        `json:"type"`
	Config   json.RawMessage `json:"config"`
	Next     []string        `json:"next"`
	Branches []BranchArm     `json:"branches"`
}

// Compile transforms a raw canvas JSONB definition into a validated AgentSpec.
// Returns (spec, nil) on success; (nil, errors) when there are validation failures.
// agentID, tenantID, definitionID are injected into the spec (not in the canvas JSON).
func Compile(agentID, tenantID, definitionID, agentSlug string, raw json.RawMessage) (*AgentSpec, []CompileError) {
	var errs []CompileError

	var def canvasDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, []CompileError{{Code: "INVALID_JSON", Message: "definition is not valid JSON: " + err.Error()}}
	}

	if def.AgentRoot.DisplayName == "" {
		errs = append(errs, CompileError{Code: "MISSING_FIELD", Message: "agent_root.display_name is required"})
	}

	// Validate and collect credential slot names for cross-referencing.
	seenSlots := make(map[string]bool)
	credSlots := make([]CredentialSlotSpec, 0, len(def.AgentRoot.CredentialSlots))
	for _, slot := range def.AgentRoot.CredentialSlots {
		if slot.Name == "" {
			errs = append(errs, CompileError{Code: "MISSING_FIELD", Message: "credential slot name must be non-empty"})
			continue
		}
		if seenSlots[slot.Name] {
			errs = append(errs, CompileError{Code: "DUPLICATE_SLOT", Message: "duplicate credential slot", Context: slot.Name})
			continue
		}
		seenSlots[slot.Name] = true
		credSlots = append(credSlots, CredentialSlotSpec{
			Name:        slot.Name,
			Description: slot.Description,
			Required:    slot.Required,
		})
	}

	// Compile each skill.
	seenSkills := make(map[string]bool)
	compiledSkills := make([]SkillSpec, 0, len(def.Skills))
	for _, cs := range def.Skills {
		if cs.SkillID == "" {
			errs = append(errs, CompileError{Code: "MISSING_FIELD", Message: "skill_id is required"})
			continue
		}
		if seenSkills[cs.SkillID] {
			errs = append(errs, CompileError{Code: "DUPLICATE_SKILL", Message: "duplicate skill_id", Context: cs.SkillID})
			continue
		}
		seenSkills[cs.SkillID] = true

		skillErrs, compiledSteps := compileSkillSteps(cs, seenSlots)
		errs = append(errs, skillErrs...)

		compiledSkills = append(compiledSkills, SkillSpec{
			ID:          cs.SkillID,
			Name:        cs.Name,
			Description: cs.Description,
			Tags:        cs.Tags,
			InputModes:  cs.InputModes,
			OutputModes: cs.OutputModes,
			Steps:       compiledSteps,
		})
	}

	if len(errs) > 0 {
		return nil, errs
	}

	slug := sanitizeSlug(agentSlug)
	if !slugRe.MatchString(slug) {
		return nil, []CompileError{{Code: "INVALID_SLUG", Message: "agent slug must match ^[a-z0-9_]{1,48}$", Context: slug}}
	}

	// Compute spec hash from canonical JSON.
	specHash := computeSpecHash(agentID, tenantID, definitionID, slug)

	version := def.AgentRoot.Version
	if version == "" {
		version = "1.0.0"
	}

	spec := &AgentSpec{
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
		Skills: compiledSkills,
	}
	_ = specHash
	return spec, nil
}

// compileSkillSteps validates steps within one skill and returns ordered StepSpecs.
func compileSkillSteps(cs canvasSkill, knownSlots map[string]bool) ([]CompileError, []StepSpec) {
	var errs []CompileError
	seenSteps := make(map[string]bool)
	stepMap := make(map[string]canvasStep, len(cs.Steps))

	for _, step := range cs.Steps {
		if step.ID == "" {
			errs = append(errs, CompileError{Code: "MISSING_FIELD", Message: "step id is required", Context: cs.SkillID})
			continue
		}
		if seenSteps[step.ID] {
			errs = append(errs, CompileError{Code: "DUPLICATE_STEP", Message: "duplicate step id", Context: cs.SkillID + ":" + step.ID})
			continue
		}
		seenSteps[step.ID] = true
		stepMap[step.ID] = step

		if _, ok := LookupNode(step.Type); !ok {
			errs = append(errs, CompileError{Code: "UNKNOWN_STEP_TYPE", Message: "unknown step type: " + string(step.Type), Context: cs.SkillID + ":" + step.ID})
		}

		// Delegate per-type config validation to the node registry.
		if def, ok := LookupNode(step.Type); ok && def.Validate != nil {
			for _, ve := range def.Validate(step, knownSlots) {
				ve.Context = cs.SkillID + ":" + step.ID
				errs = append(errs, ve)
			}
		}
	}

	// Validate Next references (dangling edges).
	for _, step := range cs.Steps {
		for _, nextID := range step.Next {
			if !seenSteps[nextID] {
				errs = append(errs, CompileError{
					Code:    "DANGLING_NEXT",
					Message: fmt.Sprintf("step %q has next ref to unknown step %q", step.ID, nextID),
					Context: cs.SkillID,
				})
			}
		}
		for _, arm := range step.Branches {
			for _, nextID := range arm.Next {
				if !seenSteps[nextID] {
					errs = append(errs, CompileError{
						Code:    "DANGLING_BRANCH",
						Message: fmt.Sprintf("branch arm in step %q refs unknown step %q", step.ID, nextID),
						Context: cs.SkillID,
					})
				}
			}
		}
	}

	if len(errs) > 0 {
		return errs, nil
	}

	// DFS cycle detection + topological order.
	ordered, cycleErrs := topoSort(cs.SkillID, cs.Steps, stepMap)
	if len(cycleErrs) > 0 {
		return cycleErrs, nil
	}

	return nil, ordered
}

// topoSort performs DFS-based topological sort and cycle detection.
func topoSort(skillID string, steps []canvasStep, stepMap map[string]canvasStep) ([]StepSpec, []CompileError) {
	const (
		white = 0 // unvisited
		grey  = 1 // in current DFS path
		black = 2 // done
	)
	color := make(map[string]int, len(steps))
	var result []StepSpec
	var errs []CompileError

	var dfs func(id string)
	dfs = func(id string) {
		if color[id] == black {
			return
		}
		if color[id] == grey {
			errs = append(errs, CompileError{
				Code:    "CYCLE_DETECTED",
				Message: fmt.Sprintf("cycle detected in skill %q at step %q", skillID, id),
				Context: skillID,
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

	// Visit all steps to handle disconnected sub-graphs.
	for _, step := range steps {
		if color[step.ID] == white {
			dfs(step.ID)
		}
	}

	if len(errs) > 0 {
		return nil, errs
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
