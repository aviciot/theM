package service

import (
	"context"
	"encoding/json"

	"github.com/aviciot/them/internal/admin/dal"
)

// AgentDefinitionService owns the business logic for canvas agent definition CRUD
// and the publish pipeline (Phase 3). fernetKey is 32 bytes (AES-256); pass nil
// to disable credential encryption (tests only).
type AgentDefinitionService struct {
	dal        Dal
	cache      Cache
	fernetKey  []byte // 32-byte AES-GCM key derived from THE_M_SECRET_KEY
}

// NewAgentDefinitionService creates an AgentDefinitionService.
// cache and fernetKey may be nil (CRUD-only mode; publish will return an error if called).
func NewAgentDefinitionService(d Dal, cache Cache, fernetKey []byte) *AgentDefinitionService {
	return &AgentDefinitionService{dal: d, cache: cache, fernetKey: fernetKey}
}

// validateAgentDefinition performs structural validation on an agent definition
// canvas JSON. Returns ErrValidation for malformed input, ErrUnprocessable for
// semantic violations. Rejects any secret material — slot NAMES only.
func validateAgentDefinition(raw json.RawMessage) error {
	// Must be a JSON object.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return validation("definition must be a JSON object")
	}
	if top == nil {
		return validation("definition must be a JSON object")
	}

	// Reject any secret_value keys at any nesting level.
	if err := rejectSecretKeys(raw); err != nil {
		return err
	}

	// Validate agent_root.
	rootRaw, ok := top["agent_root"]
	if !ok {
		return validation("agent_root is required")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(rootRaw, &root); err != nil {
		return validation("agent_root must be a JSON object")
	}
	dnRaw, ok := root["display_name"]
	if !ok {
		return validation("agent_root.display_name is required")
	}
	var dn string
	if err := json.Unmarshal(dnRaw, &dn); err != nil || dn == "" {
		return validation("agent_root.display_name must be a non-empty string")
	}

	// Validate credential_slots if present.
	if slotsRaw, ok := root["credential_slots"]; ok {
		var slots []json.RawMessage
		if err := json.Unmarshal(slotsRaw, &slots); err != nil {
			return validation("agent_root.credential_slots must be an array")
		}
		seenSlots := make(map[string]struct{}, len(slots))
		for _, slotRaw := range slots {
			var slot map[string]json.RawMessage
			if err := json.Unmarshal(slotRaw, &slot); err != nil {
				return validation("each credential slot must be a JSON object")
			}
			// Reject slot with a value field.
			if _, hasVal := slot["value"]; hasVal {
				return validation("credential slot must not contain a value")
			}
			nameRaw, ok := slot["name"]
			if !ok {
				return validation("each credential slot must have a name")
			}
			var name string
			if err := json.Unmarshal(nameRaw, &name); err != nil || name == "" {
				return validation("credential slot name must be a non-empty string")
			}
			if _, dup := seenSlots[name]; dup {
				return unprocessable("duplicate credential slot name: " + name)
			}
			seenSlots[name] = struct{}{}
		}
	}

	// Validate skills if present.
	if skillsRaw, ok := top["skills"]; ok {
		var skills []json.RawMessage
		if err := json.Unmarshal(skillsRaw, &skills); err != nil {
			return validation("skills must be an array")
		}
		seenSkills := make(map[string]struct{}, len(skills))
		for _, skillRaw := range skills {
			var skill map[string]json.RawMessage
			if err := json.Unmarshal(skillRaw, &skill); err != nil {
				return validation("each skill must be a JSON object")
			}
			sidRaw, ok := skill["skill_id"]
			if !ok {
				return validation("each skill must have a skill_id")
			}
			var sid string
			if err := json.Unmarshal(sidRaw, &sid); err != nil || sid == "" {
				return validation("skill_id must be a non-empty string")
			}
			if _, dup := seenSkills[sid]; dup {
				return unprocessable("duplicate skill_id: " + sid)
			}
			seenSkills[sid] = struct{}{}

			// Validate steps if present.
			if stepsRaw, ok := skill["steps"]; ok {
				var steps []json.RawMessage
				if err := json.Unmarshal(stepsRaw, &steps); err != nil {
					return validation("skill steps must be an array")
				}
				seenSteps := make(map[string]struct{}, len(steps))
				for _, stepRaw := range steps {
					var step map[string]json.RawMessage
					if err := json.Unmarshal(stepRaw, &step); err != nil {
						return validation("each step must be a JSON object")
					}
					stepIDRaw, ok := step["id"]
					if !ok {
						return validation("each step must have an id")
					}
					var stepID string
					if err := json.Unmarshal(stepIDRaw, &stepID); err != nil || stepID == "" {
						return validation("step id must be a non-empty string")
					}
					stepTypeRaw, ok := step["type"]
					if !ok {
						return validation("each step must have a type")
					}
					var stepType string
					if err := json.Unmarshal(stepTypeRaw, &stepType); err != nil || stepType == "" {
						return validation("step type must be a non-empty string")
					}
					if _, dup := seenSteps[stepID]; dup {
						return unprocessable("duplicate step id in skill " + sid + ": " + stepID)
					}
					seenSteps[stepID] = struct{}{}
				}
			}
		}
	}

	return nil
}

// CreateDraft validates, gets next revision, hashes, and inserts a draft agent definition.
// Returns the new definition UUID and revision.
func (s *AgentDefinitionService) CreateDraft(ctx context.Context, tenantID, agentSlug string, defRaw json.RawMessage) (string, int, error) {
	if agentSlug == "" {
		return "", 0, validation("agent_slug is required")
	}
	if err := validateAgentDefinition(defRaw); err != nil {
		return "", 0, err
	}
	rev, err := s.dal.GetNextAgentRevision(ctx, tenantID, agentSlug)
	if err != nil {
		return "", 0, err
	}
	hash, err := hashDefinition(defRaw)
	if err != nil {
		return "", 0, validation("definition is not valid JSON")
	}
	id, err := s.dal.CreateAgentDefinition(ctx, tenantID, agentSlug, rev, []byte(defRaw), hash)
	if err != nil {
		if dal.IsUniqueViolation(err) {
			return "", 0, ErrConflict
		}
		return "", 0, err
	}
	return id, rev, nil
}

// GetDefinition fetches a single agent definition scoped to tenant.
func (s *AgentDefinitionService) GetDefinition(ctx context.Context, tenantID, id string) (dal.AgentDefinition, error) {
	def, err := s.dal.GetAgentDefinition(ctx, tenantID, id)
	if err != nil {
		if dal.IsNoRows(err) {
			return dal.AgentDefinition{}, ErrNotFound
		}
		return dal.AgentDefinition{}, err
	}
	return def, nil
}

// ListDefinitions returns all agent definitions for the tenant, newest first.
func (s *AgentDefinitionService) ListDefinitions(ctx context.Context, tenantID string) ([]dal.AgentDefinition, error) {
	defs, err := s.dal.ListAgentDefinitions(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if defs == nil {
		defs = []dal.AgentDefinition{}
	}
	return defs, nil
}

// UpdateDraft updates a draft agent definition in place.
func (s *AgentDefinitionService) UpdateDraft(ctx context.Context, tenantID, id string, defRaw json.RawMessage) error {
	if err := validateAgentDefinition(defRaw); err != nil {
		return err
	}
	hash, err := hashDefinition(defRaw)
	if err != nil {
		return validation("definition is not valid JSON")
	}
	err = s.dal.UpdateDraftAgentDefinition(ctx, tenantID, id, []byte(defRaw), hash)
	if err == nil {
		return nil
	}
	if !dal.IsNoRows(err) {
		return err
	}
	// 0 rows — distinguish not-found vs published.
	existing, getErr := s.dal.GetAgentDefinition(ctx, tenantID, id)
	if getErr != nil {
		return ErrNotFound
	}
	if existing.Status == "published" {
		return ErrConflict
	}
	return ErrNotFound
}

// DeleteDraft hard-deletes a draft agent definition.
func (s *AgentDefinitionService) DeleteDraft(ctx context.Context, tenantID, id string) error {
	err := s.dal.DeleteDraftAgentDefinition(ctx, tenantID, id)
	if err == nil {
		return nil
	}
	if !dal.IsNoRows(err) {
		return err
	}
	// 0 rows — distinguish not-found vs published.
	existing, getErr := s.dal.GetAgentDefinition(ctx, tenantID, id)
	if getErr != nil {
		return ErrNotFound
	}
	if existing.Status == "published" {
		return ErrConflict
	}
	return ErrNotFound
}
