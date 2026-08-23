package agentgen_test

import (
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

func compileOK(t *testing.T, raw string) *agentgen.AgentSpec {
	t.Helper()
	spec, errs := agentgen.Compile("agent-1", "tenant-1", "def-1", "my_agent", json.RawMessage(raw))
	for _, e := range errs {
		if e.Severity == "error" {
			t.Fatalf("expected no errors, got: %v", errs)
		}
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	return spec
}

func compileFail(t *testing.T, raw string) []agentgen.Issue {
	t.Helper()
	spec, issues := agentgen.Compile("agent-1", "tenant-1", "def-1", "my_agent", json.RawMessage(raw))
	hasErr := false
	for _, iss := range issues {
		if iss.Severity == "error" {
			hasErr = true
			break
		}
	}
	if !hasErr {
		t.Fatalf("expected compile errors, got spec: %+v", spec)
	}
	return issues
}

func hasCode(issues []agentgen.Issue, code string) bool {
	for _, iss := range issues {
		if iss.Code == code {
			return true
		}
	}
	return false
}

func hasSeverity(issues []agentgen.Issue, code, severity string) bool {
	for _, iss := range issues {
		if iss.Code == code && iss.Severity == severity {
			return true
		}
	}
	return false
}

// validateIssues calls Validate and returns all issues (including warnings).
func validateIssues(t *testing.T, raw string) []agentgen.Issue {
	t.Helper()
	_, issues := agentgen.Validate("agent-1", "tenant-1", "def-1", "my_agent", json.RawMessage(raw))
	return issues
}

// publishFail calls CompileForPublish and expects at least one error.
func publishFail(t *testing.T, raw string) []agentgen.Issue {
	t.Helper()
	spec, issues := agentgen.CompileForPublish("agent-1", "tenant-1", "def-1", "my_agent", json.RawMessage(raw))
	hasErr := false
	for _, iss := range issues {
		if iss.Severity == "error" {
			hasErr = true
			break
		}
	}
	if !hasErr {
		t.Fatalf("expected publish errors, got spec: %+v", spec)
	}
	return issues
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

// ── BuildValidator severity tests ─────────────────────────────────────────────

const stubGraph = `{
	"agent_root": {"display_name": "X"},
	"skills": [{
		"skill_id": "s1",
		"steps": [{"id": "step1", "type": "branch"}]
	}]
}`

// TestValidate_StubNodeIsWarning verifies that Validate() surfaces stub nodes
// as warnings, not errors — the user should be able to save a draft that uses
// unimplemented node types.
func TestValidate_StubNodeIsWarning(t *testing.T) {
	issues := validateIssues(t, stubGraph)
	if !hasSeverity(issues, "NODE_NOT_EXECUTABLE", "warning") {
		t.Errorf("Validate: expected NODE_NOT_EXECUTABLE as warning, got: %v", issues)
	}
	for _, iss := range issues {
		if iss.Code == "NODE_NOT_EXECUTABLE" && iss.Severity == "error" {
			t.Errorf("Validate: NODE_NOT_EXECUTABLE must not be an error at validate time")
		}
	}
}

// TestValidate_StubNodeDoesNotBlockSpec verifies that Validate() returns a
// non-nil spec even when stub nodes are present.
func TestValidate_StubNodeDoesNotBlockSpec(t *testing.T) {
	spec, _ := agentgen.Validate("agent-1", "tenant-1", "def-1", "my_agent", json.RawMessage(stubGraph))
	if spec == nil {
		t.Error("Validate: expected non-nil spec for a valid graph with stub nodes")
	}
}

// TestCompileForPublish_StubNodeIsError verifies that CompileForPublish() treats
// stub nodes as hard errors — publishing an agent with unimplemented nodes must fail.
func TestCompileForPublish_StubNodeIsError(t *testing.T) {
	issues := publishFail(t, stubGraph)
	if !hasSeverity(issues, "NODE_NOT_EXECUTABLE", "error") {
		t.Errorf("CompileForPublish: expected NODE_NOT_EXECUTABLE as error, got: %v", issues)
	}
}

// TestCompileForPublish_ImplementedOnlySucceeds verifies that a graph with only
// implemented node types publishes cleanly.
func TestCompileForPublish_ImplementedOnlySucceeds(t *testing.T) {
	spec, issues := agentgen.CompileForPublish("agent-1", "tenant-1", "def-1", "my_agent",
		json.RawMessage(`{
			"agent_root": {"display_name": "X"},
			"skills": [{"skill_id": "s1", "steps": [
				{"id": "in", "type": "input", "next": ["out"]},
				{"id": "out", "type": "response", "config": {"from_var": "x"}}
			]}]
		}`))
	for _, iss := range issues {
		if iss.Severity == "error" {
			t.Errorf("CompileForPublish: unexpected error: %v", iss)
		}
	}
	if spec == nil {
		t.Error("CompileForPublish: expected non-nil spec for implemented-only graph")
	}
}

// TestIssue_StructuredFields verifies that issues returned by validateNodes
// carry populated SkillID and NodeID fields.
func TestIssue_StructuredFields(t *testing.T) {
	_, issues := agentgen.Validate("a", "t", "d", "slug", json.RawMessage(`{
		"agent_root": {"display_name": "X", "credential_slots": []},
		"skills": [{"skill_id": "skill_abc", "steps": [{
			"id": "node_xyz",
			"type": "llm",
			"config": {"provider": "anthropic", "provider_key_slot": "missing"}
		}]}]
	}`))
	var found *agentgen.Issue
	for i, iss := range issues {
		if iss.Code == "UNDECLARED_SLOT" {
			found = &issues[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected UNDECLARED_SLOT issue")
	}
	if found.SkillID != "skill_abc" {
		t.Errorf("SkillID: want %q, got %q", "skill_abc", found.SkillID)
	}
	if found.NodeID != "node_xyz" {
		t.Errorf("NodeID: want %q, got %q", "node_xyz", found.NodeID)
	}
	if found.Field != "provider_key_slot" {
		t.Errorf("Field: want %q, got %q", "provider_key_slot", found.Field)
	}
}
