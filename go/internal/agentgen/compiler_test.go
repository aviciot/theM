package agentgen_test

import (
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

func compileOK(t *testing.T, raw string) *agentgen.AgentSpec {
	t.Helper()
	spec, errs := agentgen.Compile("agent-1", "tenant-1", "def-1", "my_agent", json.RawMessage(raw))
	if len(errs) > 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	return spec
}

func compileFail(t *testing.T, raw string) []agentgen.CompileError {
	t.Helper()
	spec, errs := agentgen.Compile("agent-1", "tenant-1", "def-1", "my_agent", json.RawMessage(raw))
	if len(errs) == 0 {
		t.Fatalf("expected compile errors, got spec: %+v", spec)
	}
	return errs
}

func hasCode(errs []agentgen.CompileError, code string) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

func TestCompile_EmptyDefinition(t *testing.T) {
	errs := compileFail(t, `{}`)
	if !hasCode(errs, "MISSING_FIELD") {
		t.Errorf("expected MISSING_FIELD for empty definition, got: %v", errs)
	}
}

func TestCompile_InvalidJSON(t *testing.T) {
	_, errs := agentgen.Compile("a", "t", "d", "slug", json.RawMessage(`not json`))
	if !hasCode(errs, "INVALID_JSON") {
		t.Errorf("expected INVALID_JSON, got: %v", errs)
	}
}

func TestCompile_MinimalValid(t *testing.T) {
	spec := compileOK(t, `{
		"agent_root": {"display_name": "My Agent"},
		"skills": []
	}`)
	if spec.Card.Name != "My Agent" {
		t.Errorf("expected card name 'My Agent', got %q", spec.Card.Name)
	}
	if spec.ID != "agent-1" {
		t.Errorf("expected ID 'agent-1', got %q", spec.ID)
	}
	if spec.DefinitionID != "def-1" {
		t.Errorf("expected DefinitionID 'def-1', got %q", spec.DefinitionID)
	}
}

func TestCompile_DefaultVersionFallback(t *testing.T) {
	spec := compileOK(t, `{"agent_root": {"display_name": "X"}, "skills": []}`)
	if spec.Card.Version != "1.0.0" {
		t.Errorf("expected default version 1.0.0, got %q", spec.Card.Version)
	}
}

func TestCompile_SlugSanitized(t *testing.T) {
	_, errs := agentgen.Compile("a", "t", "d", "my-agent", json.RawMessage(`{"agent_root":{"display_name":"X"},"skills":[]}`))
	// hyphens → underscores → "my_agent" — valid
	if len(errs) > 0 {
		t.Errorf("slug with hyphens should be sanitized to underscores, got errors: %v", errs)
	}
}

func TestCompile_DuplicateSlotName(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {
			"display_name": "X",
			"credential_slots": [
				{"name": "api_key", "required": true},
				{"name": "api_key", "required": false}
			]
		},
		"skills": []
	}`)
	if !hasCode(errs, "DUPLICATE_SLOT") {
		t.Errorf("expected DUPLICATE_SLOT, got: %v", errs)
	}
}

func TestCompile_DuplicateSkillID(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X"},
		"skills": [
			{"skill_id": "s1", "name": "A", "steps": []},
			{"skill_id": "s1", "name": "B", "steps": []}
		]
	}`)
	if !hasCode(errs, "DUPLICATE_SKILL") {
		t.Errorf("expected DUPLICATE_SKILL, got: %v", errs)
	}
}

func TestCompile_DuplicateStepID(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "step1", "type": "input"},
				{"id": "step1", "type": "response"}
			]
		}]
	}`)
	if !hasCode(errs, "DUPLICATE_STEP") {
		t.Errorf("expected DUPLICATE_STEP, got: %v", errs)
	}
}

func TestCompile_UnknownStepType(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [{"id": "s", "type": "banana"}]
		}]
	}`)
	if !hasCode(errs, "UNKNOWN_STEP_TYPE") {
		t.Errorf("expected UNKNOWN_STEP_TYPE, got: %v", errs)
	}
}

func TestCompile_UndeclaredHTTPCredentialSlot(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X", "credential_slots": []},
		"skills": [{
			"skill_id": "s1",
			"steps": [{
				"id": "step1",
				"type": "http",
				"config": {"method": "GET", "url_template": "http://x", "credential_slot": "missing_slot"}
			}]
		}]
	}`)
	if !hasCode(errs, "UNDECLARED_SLOT") {
		t.Errorf("expected UNDECLARED_SLOT, got: %v", errs)
	}
}

func TestCompile_UndeclaredLLMProviderKeySlot(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X", "credential_slots": []},
		"skills": [{
			"skill_id": "s1",
			"steps": [{
				"id": "step1",
				"type": "llm",
				"config": {"provider": "anthropic", "provider_key_slot": "missing_key"}
			}]
		}]
	}`)
	if !hasCode(errs, "UNDECLARED_SLOT") {
		t.Errorf("expected UNDECLARED_SLOT, got: %v", errs)
	}
}

func TestCompile_DanglingNextRef(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [{"id": "step1", "type": "input", "next": ["ghost"]}]
		}]
	}`)
	if !hasCode(errs, "DANGLING_NEXT") {
		t.Errorf("expected DANGLING_NEXT, got: %v", errs)
	}
}

func TestCompile_CycleDetected(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "stepA", "type": "transform", "next": ["stepB"]},
				{"id": "stepB", "type": "transform", "next": ["stepA"]}
			]
		}]
	}`)
	if !hasCode(errs, "CYCLE_DETECTED") {
		t.Errorf("expected CYCLE_DETECTED, got: %v", errs)
	}
}

func TestCompile_ValidCredentialSlotRef(t *testing.T) {
	spec := compileOK(t, `{
		"agent_root": {
			"display_name": "X",
			"credential_slots": [{"name": "my_key", "required": true}]
		},
		"skills": [{
			"skill_id": "s1",
			"steps": [{
				"id": "step1",
				"type": "http",
				"config": {"method": "GET", "url_template": "http://x", "credential_slot": "my_key"}
			}]
		}]
	}`)
	if len(spec.CredentialSlots) != 1 || spec.CredentialSlots[0].Name != "my_key" {
		t.Errorf("expected credential slot my_key, got: %v", spec.CredentialSlots)
	}
}

func TestCompile_TopologicalOrder(t *testing.T) {
	// input → transform → response (linear chain)
	spec := compileOK(t, `{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "step-response", "type": "response", "config": {"from_var": "x"}},
				{"id": "step-input", "type": "input", "config": {}, "next": ["step-transform"]},
				{"id": "step-transform", "type": "transform", "config": {"expressions": {}}, "next": ["step-response"]}
			]
		}]
	}`)
	if len(spec.Skills) != 1 {
		t.Fatalf("expected 1 skill")
	}
	steps := spec.Skills[0].Steps
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	// input must come before transform, transform before response
	pos := make(map[string]int)
	for i, s := range steps {
		pos[s.ID] = i
	}
	if pos["step-input"] >= pos["step-transform"] {
		t.Errorf("step-input must come before step-transform, positions: %v", pos)
	}
	if pos["step-transform"] >= pos["step-response"] {
		t.Errorf("step-transform must come before step-response, positions: %v", pos)
	}
}
