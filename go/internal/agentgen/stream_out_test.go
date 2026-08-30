package agentgen_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

func soSkill(id string, steps []agentgen.StepSpec) *agentgen.SkillSpec {
	return &agentgen.SkillSpec{ID: id, Steps: steps}
}

// SO-1: stream_out reads from_var and sets result.Text.
func TestStreamOut_ReadsFromVar(t *testing.T) {
	steps := []agentgen.StepSpec{
		{ID: "in",  Type: agentgen.StepInput,     Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"out"}},
		{ID: "out", Type: agentgen.StepStreamOut, Config: mustJSON(agentgen.StreamOutStepConfig{FromVar: "llm_out"})},
	}
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1"}
	result, err := interp.Execute(context.Background(), ic, soSkill("s1", steps), "", map[string]any{"llm_out": "hello stream"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "hello stream" {
		t.Errorf("result.Text: want %q, got %q", "hello stream", result.Text)
	}
}

// SO-2: stream_out defaults to text/plain when media_type is not set.
func TestStreamOut_DefaultMediaType(t *testing.T) {
	steps := []agentgen.StepSpec{
		{ID: "in",   Type: agentgen.StepInput,     Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"sink"}},
		{ID: "sink", Type: agentgen.StepStreamOut, Config: mustJSON(agentgen.StreamOutStepConfig{FromVar: "out"})},
	}
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1"}
	result, err := interp.Execute(context.Background(), ic, soSkill("s1", steps), "", map[string]any{"out": "data"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MediaType != "text/plain" {
		t.Errorf("MediaType: want %q, got %q", "text/plain", result.MediaType)
	}
}

// SO-3: stream_out honours an explicit media_type field.
func TestStreamOut_ExplicitMediaType(t *testing.T) {
	steps := []agentgen.StepSpec{
		{ID: "in",   Type: agentgen.StepInput,     Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"sink"}},
		{ID: "sink", Type: agentgen.StepStreamOut, Config: mustJSON(agentgen.StreamOutStepConfig{FromVar: "out", MediaType: "text/markdown"})},
	}
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1"}
	result, err := interp.Execute(context.Background(), ic, soSkill("s1", steps), "", map[string]any{"out": "# heading"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MediaType != "text/markdown" {
		t.Errorf("MediaType: want %q, got %q", "text/markdown", result.MediaType)
	}
}

// SO-4: stream_out returns empty string when from_var is not present in vars.
func TestStreamOut_MissingVar_EmptyResult(t *testing.T) {
	steps := []agentgen.StepSpec{
		{ID: "in",   Type: agentgen.StepInput,     Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"sink"}},
		{ID: "sink", Type: agentgen.StepStreamOut, Config: mustJSON(agentgen.StreamOutStepConfig{FromVar: "nonexistent"})},
	}
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1"}
	result, err := interp.Execute(context.Background(), ic, soSkill("s1", steps), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "" {
		t.Errorf("result.Text: want empty, got %q", result.Text)
	}
}

// SO-5: stream_out defaults from_var to "output" when from_var is empty.
func TestStreamOut_DefaultFromVar(t *testing.T) {
	steps := []agentgen.StepSpec{
		{ID: "in",   Type: agentgen.StepInput,     Config: mustJSON(agentgen.InputStepConfig{}), Next: []string{"sink"}},
		{ID: "sink", Type: agentgen.StepStreamOut, Config: mustJSON(agentgen.StreamOutStepConfig{})},
	}
	interp := agentgen.NewInterpreter(nil, nil, "")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1"}
	result, err := interp.Execute(context.Background(), ic, soSkill("s1", steps), "", map[string]any{"output": "default text"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "default text" {
		t.Errorf("result.Text: want %q, got %q", "default text", result.Text)
	}
}

// SO-6: Validate emits STREAM_OUT_MISSING_FROM_VAR when from_var is absent.
func TestStreamOut_Validate_MissingFromVar(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",  "type": "input",      "config": {}, "next": ["out"]},
				{"id": "out", "type": "stream_out",  "config": {}}
			]
		}]
	}`)
	if !hasCode(errs, "STREAM_OUT_MISSING_FROM_VAR") {
		t.Errorf("expected STREAM_OUT_MISSING_FROM_VAR, got: %v", errs)
	}
}

// SO-7: Validate accepts stream_out when from_var is set.
func TestStreamOut_Validate_Valid(t *testing.T) {
	raw := json.RawMessage(`{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [
				{"id": "in",  "type": "input",      "config": {}, "next": ["out"]},
				{"id": "out", "type": "stream_out",  "config": {"from_var": "output"}}
			]
		}]
	}`)
	spec, errs := agentgen.Validate("a", "t", "d", "test_agent", raw)
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	for _, iss := range errs {
		if iss.Severity == "error" {
			t.Errorf("unexpected compile error: %+v", iss)
		}
	}
}

// SO-8: DeriveInputs declares from_var as required input.
func TestStreamOut_DeriveInputs(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepStreamOut)
	if !ok {
		t.Fatal("StepStreamOut not registered")
	}
	inputs := def.DeriveInputs(mustJSON(agentgen.StreamOutStepConfig{FromVar: "llm_out"}))
	if len(inputs) != 1 {
		t.Fatalf("DeriveInputs: want 1 input, got %d", len(inputs))
	}
	if inputs[0].Name != "llm_out" {
		t.Errorf("DeriveInputs[0].Name: want %q, got %q", "llm_out", inputs[0].Name)
	}
	if !inputs[0].Required {
		t.Error("DeriveInputs[0].Required: want true")
	}
}

// SO-9: DeriveInputs falls back to "output" when from_var is not configured.
func TestStreamOut_DeriveInputs_DefaultVar(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepStreamOut)
	if !ok {
		t.Fatal("StepStreamOut not registered")
	}
	inputs := def.DeriveInputs(mustJSON(agentgen.StreamOutStepConfig{}))
	if len(inputs) != 1 {
		t.Fatalf("DeriveInputs: want 1 input, got %d", len(inputs))
	}
	if inputs[0].Name != "output" {
		t.Errorf("DeriveInputs default: want %q, got %q", "output", inputs[0].Name)
	}
}

// SO-10: Full pipeline — LLM stub → stream_out produces result.Text from LLM output.
func TestStreamOut_FullPipeline(t *testing.T) {
	fake := &fakeLLM{reply: "the answer is 42"}
	steps := []agentgen.StepSpec{
		{ID: "in",  Type: agentgen.StepInput,     Config: mustJSON(agentgen.InputStepConfig{}),
			Next: []string{"llm"}},
		{ID: "llm", Type: agentgen.StepLLM,       Config: mustJSON(agentgen.LLMStepConfig{
			Provider: "mock", Model: "x", MaxTokens: 1, SystemPrompt: "sys"}),
			Next: []string{"out"}},
		{ID: "out", Type: agentgen.StepStreamOut, Config: mustJSON(agentgen.StreamOutStepConfig{FromVar: "output"})},
	}
	interp := agentgen.NewInterpreter(nil, &fakeLLMFactory{llm: fake}, "platform-key")
	ic := &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "ag1"}
	result, err := interp.Execute(context.Background(), ic, soSkill("s1", steps), "what?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "the answer is 42" {
		t.Errorf("result.Text: want %q, got %q", "the answer is 42", result.Text)
	}
	if result.MediaType != "text/plain" {
		t.Errorf("MediaType: want %q, got %q", "text/plain", result.MediaType)
	}
}
