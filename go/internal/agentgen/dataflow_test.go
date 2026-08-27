package agentgen_test

// dataflow_test.go — Tests for data-flow derivation (DeriveInputs/DeriveOutputs)
// and Stage 5 validateDataFlow (UNRESOLVED_INPUT issues).

import (
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// minimalCanvas wraps steps into a valid single-skill canvas JSON.
// stepsJSON is a JSON array of step objects.
func minimalCanvas(stepsJSON string) string {
	return `{
		"agent_root": {"display_name": "Test"},
		"skills": [{
			"skill_id": "s1",
			"name": "Skill",
			"steps": ` + stepsJSON + `
		}]
	}`
}

// findStep returns the StepSpec with the given ID from the first skill,
// or fails the test if not found.
func findStep(t *testing.T, spec *agentgen.AgentSpec, id string) agentgen.StepSpec {
	t.Helper()
	for _, skill := range spec.Skills {
		for _, step := range skill.Steps {
			if step.ID == id {
				return step
			}
		}
	}
	t.Fatalf("step %q not found in compiled spec", id)
	return agentgen.StepSpec{}
}

// hasVarRef checks whether a VarRef with the given name exists in a slice.
func hasVarRef(refs []agentgen.VarRef, name string) bool {
	for _, r := range refs {
		if r.Name == name {
			return true
		}
	}
	return false
}

// varRefRequired returns the Required field for the first VarRef with the given name,
// or false if not found.
func varRefRequired(refs []agentgen.VarRef, name string) bool {
	for _, r := range refs {
		if r.Name == name {
			return r.Required
		}
	}
	return false
}

// ── DeriveInputs / DeriveOutputs per node type ────────────────────────────────

// DF-01: input node outputs the default "input" var when no bindings set.
func TestDataFlow_Input_DefaultOutput(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input",    "config": {}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "input"}}
	]`))
	step := findStep(t, spec, "in")
	if !hasVarRef(step.Outputs, "input") {
		t.Errorf("input node: expected output var 'input', got %+v", step.Outputs)
	}
}

// DF-02: input node outputs the binding name when bindings.text is set.
func TestDataFlow_Input_BindingOutput(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input",    "config": {"bindings": {"text": "user_text"}}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "user_text"}}
	]`))
	step := findStep(t, spec, "in")
	if !hasVarRef(step.Outputs, "user_text") {
		t.Errorf("input node: expected output var 'user_text', got %+v", step.Outputs)
	}
	if hasVarRef(step.Outputs, "input") {
		t.Errorf("input node: should not output 'input' when binding is set to 'user_text'")
	}
}

// DF-03: input node DeriveInputs includes "input" (implicit invocation var).
func TestDataFlow_Input_ImplicitInput(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input",    "config": {}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "input"}}
	]`))
	step := findStep(t, spec, "in")
	if !hasVarRef(step.Inputs, "input") {
		t.Errorf("input node: expected input var 'input' in Inputs, got %+v", step.Inputs)
	}
	if varRefRequired(step.Inputs, "input") {
		t.Errorf("input node: 'input' should not be Required (invocation always provides it)")
	}
}

// DF-04: llm node inputs include template vars from user_prompt.
func TestDataFlow_LLM_UserPromptTemplateVars(t *testing.T) {
	// Pipeline: in → xf (writes user_query, lang) → llm (reads user_query, lang) → out
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",   "type": "input", "config": {}, "next": ["xf"]},
		{"id": "xf",   "type": "transform",
		 "config": {"functions": [
		   {"name": "to_string", "input_var": "input", "output_var": "user_query"},
		   {"name": "to_string", "input_var": "input", "output_var": "lang"}
		 ]}, "next": ["llm"]},
		{"id": "llm",  "type": "llm",
		 "config": {"user_prompt": "Summarize {{.user_query}} in {{.lang}}", "output_var": "summary"},
		 "next": ["out"]},
		{"id": "out",  "type": "response", "config": {"from_var": "summary"}}
	]`))
	step := findStep(t, spec, "llm")
	if !hasVarRef(step.Inputs, "user_query") {
		t.Errorf("llm: expected 'user_query' in Inputs, got %+v", step.Inputs)
	}
	if !hasVarRef(step.Inputs, "lang") {
		t.Errorf("llm: expected 'lang' in Inputs, got %+v", step.Inputs)
	}
}

// DF-05: llm node includes "input" in Inputs when user_prompt is empty.
func TestDataFlow_LLM_EmptyUserPromptIncludesInput(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",  "config": {"output_var": "output"}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "output"}}
	]`))
	step := findStep(t, spec, "llm")
	if !hasVarRef(step.Inputs, "input") {
		t.Errorf("llm (empty user_prompt): expected 'input' in Inputs, got %+v", step.Inputs)
	}
}

// DF-06: llm node does NOT include "input" when user_prompt is set.
func TestDataFlow_LLM_SetUserPromptExcludesInput(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",  "config": {"user_prompt": "Hello world", "output_var": "output"}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "output"}}
	]`))
	step := findStep(t, spec, "llm")
	if hasVarRef(step.Inputs, "input") {
		t.Errorf("llm (set user_prompt): should NOT include 'input' in Inputs, got %+v", step.Inputs)
	}
}

// DF-07: llm node outputs default to "output" when output_var not set.
func TestDataFlow_LLM_DefaultOutput(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",  "config": {}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "output"}}
	]`))
	step := findStep(t, spec, "llm")
	if !hasVarRef(step.Outputs, "output") {
		t.Errorf("llm (no output_var): expected output 'output', got %+v", step.Outputs)
	}
}

// DF-08: llm node outputs the configured output_var.
func TestDataFlow_LLM_ConfiguredOutput(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",  "config": {"output_var": "recommendation"}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "recommendation"}}
	]`))
	step := findStep(t, spec, "llm")
	if !hasVarRef(step.Outputs, "recommendation") {
		t.Errorf("llm: expected output 'recommendation', got %+v", step.Outputs)
	}
	if hasVarRef(step.Outputs, "output") {
		t.Errorf("llm: should not output 'output' when output_var is 'recommendation'")
	}
}

// DF-09: http node inputs = template vars from url_template + body_template.
func TestDataFlow_HTTP_TemplateVarInputs(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {"bindings": {"text": "city"}}, "next": ["http"]},
		{"id": "http","type": "http",
		 "config": {"method": "GET", "url_template": "http://api/{{.city}}/data", "body_template": "q={{.city}}"},
		 "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "http_response"}}
	]`))
	step := findStep(t, spec, "http")
	if !hasVarRef(step.Inputs, "city") {
		t.Errorf("http: expected 'city' in Inputs from url_template, got %+v", step.Inputs)
	}
	// body_template also uses {{.city}} — deduplicated
	count := 0
	for _, r := range step.Inputs {
		if r.Name == "city" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("http: 'city' should appear exactly once in Inputs (dedup), got count=%d", count)
	}
}

// DF-10: http node always outputs "http_response".
func TestDataFlow_HTTP_AlwaysOutputsHTTPResponse(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["http"]},
		{"id": "http","type": "http", "config": {"method": "GET", "url_template": "http://x"}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "http_response"}}
	]`))
	step := findStep(t, spec, "http")
	if !hasVarRef(step.Outputs, "http_response") {
		t.Errorf("http: expected 'http_response' in Outputs, got %+v", step.Outputs)
	}
}

// DF-11: http node outputs extraction vars.
func TestDataFlow_HTTP_ExtractionOutputs(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["http"]},
		{"id": "http","type": "http",
		 "config": {"method": "GET", "url_template": "http://x",
		            "extractions": [{"var": "temp", "json_path": "$.temp"}, {"var": "wind", "json_path": "$.wind"}]},
		 "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "http_response"}}
	]`))
	step := findStep(t, spec, "http")
	if !hasVarRef(step.Outputs, "temp") {
		t.Errorf("http: expected extraction var 'temp' in Outputs, got %+v", step.Outputs)
	}
	if !hasVarRef(step.Outputs, "wind") {
		t.Errorf("http: expected extraction var 'wind' in Outputs, got %+v", step.Outputs)
	}
}

// DF-12: transform node inputs = unique input_vars from functions; outputs = unique output_vars.
func TestDataFlow_Transform_InputsAndOutputs(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in", "type": "input", "config": {}, "next": ["xf"]},
		{"id": "xf", "type": "transform",
		 "config": {"functions": [
		   {"name": "to_string", "input_var": "input", "output_var": "a"},
		   {"name": "to_string", "input_var": "input", "output_var": "b"}
		 ]}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "a"}}
	]`))
	step := findStep(t, spec, "xf")
	// "input" should appear once (dedup)
	count := 0
	for _, r := range step.Inputs {
		if r.Name == "input" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("transform: 'input' should appear once in Inputs (dedup), got %d", count)
	}
	if !hasVarRef(step.Outputs, "a") {
		t.Errorf("transform: expected 'a' in Outputs, got %+v", step.Outputs)
	}
	if !hasVarRef(step.Outputs, "b") {
		t.Errorf("transform: expected 'b' in Outputs, got %+v", step.Outputs)
	}
}

// DF-13: response node inputs = [{Name: from_var, Required: true}]; outputs = [].
func TestDataFlow_Response_InputRequired(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",  "config": {"output_var": "answer"}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "answer"}}
	]`))
	step := findStep(t, spec, "out")
	if !hasVarRef(step.Inputs, "answer") {
		t.Errorf("response: expected 'answer' in Inputs, got %+v", step.Inputs)
	}
	if !varRefRequired(step.Inputs, "answer") {
		t.Errorf("response: 'answer' input should be Required=true, got %+v", step.Inputs)
	}
	if len(step.Outputs) != 0 {
		t.Errorf("response: Outputs should be empty (sink), got %+v", step.Outputs)
	}
}

// DF-14: response node defaults from_var to "output" when not set.
func TestDataFlow_Response_DefaultFromVar(t *testing.T) {
	spec := compileOK(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",  "config": {}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {}}
	]`))
	step := findStep(t, spec, "out")
	if !hasVarRef(step.Inputs, "output") {
		t.Errorf("response (no from_var): expected default input 'output', got %+v", step.Inputs)
	}
}

// DF-15: branch node inputs = template vars from expression; outputs = [].
func TestDataFlow_Branch_ExpressionVars(t *testing.T) {
	// Branch needs 2 outgoing edges; use two response nodes.
	raw := `{
		"agent_root": {"display_name": "Test"},
		"skills": [{"skill_id": "s1", "name": "S", "steps": [
			{"id": "in",   "type": "input", "config": {"bindings": {"text": "score"}}, "next": ["br"]},
			{"id": "br",   "type": "branch",
			 "config": {"expression": "{{if gt .score 5}}true{{else}}false{{end}}", "true_next": "r1", "false_next": "r2"},
			 "next": ["r1", "r2"]},
			{"id": "r1",   "type": "response", "config": {"from_var": "score"}},
			{"id": "r2",   "type": "response", "config": {"from_var": "score"}}
		]}]
	}`
	spec := compileOK(t, raw)
	step := findStep(t, spec, "br")
	if !hasVarRef(step.Inputs, "score") {
		t.Errorf("branch: expected 'score' in Inputs from expression, got %+v", step.Inputs)
	}
	if len(step.Outputs) != 0 {
		t.Errorf("branch: Outputs should be empty, got %+v", step.Outputs)
	}
}

// DF-16: loop stub outputs accum_var when set.
func TestDataFlow_Loop_AccumVar(t *testing.T) {
	// Loop is a stub so compileOK (= CompileForPublish) will fail; use Validate.
	// Loop connects to "out" (no self-cycle — loop body is implicit in config).
	raw := `{
		"agent_root": {"display_name": "Test"},
		"skills": [{"skill_id": "s1", "name": "S", "steps": [
			{"id": "in",  "type": "input",    "config": {}, "next": ["lp"]},
			{"id": "lp",  "type": "loop",
			 "config": {"accum_var": "results", "condition": "{{.done}}", "max_iterations": 5},
			 "next": ["out"]},
			{"id": "out", "type": "response", "config": {"from_var": "results"}}
		]}]
	}`
	spec, issues := agentgen.Validate("a", "t", "d", "test_agent", json.RawMessage(raw))
	if spec == nil {
		t.Fatalf("Validate returned nil spec for loop canvas, issues: %v", issues)
	}
	step := findStep(t, spec, "lp")
	if !hasVarRef(step.Outputs, "results") {
		t.Errorf("loop: expected 'results' in Outputs, got %+v", step.Outputs)
	}
	if !hasVarRef(step.Inputs, "done") {
		t.Errorf("loop: expected 'done' in Inputs from condition, got %+v", step.Inputs)
	}
}

// DF-17: parallel stub outputs merge_var when set.
func TestDataFlow_Parallel_MergeVar(t *testing.T) {
	spec, _ := agentgen.Validate("a", "t", "d", "test_agent", json.RawMessage(`{
		"agent_root": {"display_name": "Test"},
		"skills": [{"skill_id": "s1", "name": "S", "steps": [
			{"id": "in", "type": "input",    "config": {}, "next": ["pl"]},
			{"id": "pl", "type": "parallel",
			 "config": {"merge_var": "merged"},
			 "next": ["r1", "r2"]},
			{"id": "r1", "type": "response", "config": {"from_var": "merged"}},
			{"id": "r2", "type": "response", "config": {"from_var": "merged"}}
		]}]
	}`))
	if spec == nil {
		t.Fatal("Validate returned nil spec")
	}
	step := findStep(t, spec, "pl")
	if !hasVarRef(step.Outputs, "merged") {
		t.Errorf("parallel: expected 'merged' in Outputs, got %+v", step.Outputs)
	}
}

// DF-18: a2a_call stub inputs = [{Name: input_var, Required: true}]; outputs = [{Name: output_var}].
func TestDataFlow_A2ACall_InputsAndOutputs(t *testing.T) {
	spec, _ := agentgen.Validate("a", "t", "d", "test_agent", json.RawMessage(minimalCanvas(`[
		{"id": "in",   "type": "input",    "config": {}, "next": ["a2a"]},
		{"id": "a2a",  "type": "a2a_call",
		 "config": {"input_var": "prompt", "output_var": "result"},
		 "next": ["out"]},
		{"id": "out",  "type": "response", "config": {"from_var": "result"}}
	]`)))
	if spec == nil {
		t.Fatal("Validate returned nil spec")
	}
	step := findStep(t, spec, "a2a")
	if !hasVarRef(step.Inputs, "prompt") {
		t.Errorf("a2a_call: expected 'prompt' in Inputs, got %+v", step.Inputs)
	}
	if !varRefRequired(step.Inputs, "prompt") {
		t.Errorf("a2a_call: 'prompt' input should be Required=true")
	}
	if !hasVarRef(step.Outputs, "result") {
		t.Errorf("a2a_call: expected 'result' in Outputs, got %+v", step.Outputs)
	}
}

// DF-19: human_wait stub outputs reply_var when set.
func TestDataFlow_HumanWait_ReplyVar(t *testing.T) {
	spec, _ := agentgen.Validate("a", "t", "d", "test_agent", json.RawMessage(minimalCanvas(`[
		{"id": "in",  "type": "input",      "config": {}, "next": ["hw"]},
		{"id": "hw",  "type": "human_wait", "config": {"reply_var": "approval"}, "next": ["out"]},
		{"id": "out", "type": "response",   "config": {"from_var": "approval"}}
	]`)))
	if spec == nil {
		t.Fatal("Validate returned nil spec")
	}
	step := findStep(t, spec, "hw")
	if !hasVarRef(step.Outputs, "approval") {
		t.Errorf("human_wait: expected 'approval' in Outputs, got %+v", step.Outputs)
	}
}

// ─── Stage 5 — validateDataFlow (UNRESOLVED_INPUT) ───────────────────────────

// DF-20: response reads a var written by LLM → no UNRESOLVED_INPUT.
func TestDataFlow_Stage5_ResolvedFlow(t *testing.T) {
	issues := validateIssues(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",  "config": {"output_var": "recommendation"}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "recommendation"}}
	]`))
	for _, iss := range issues {
		if iss.Code == "UNRESOLVED_INPUT" {
			t.Errorf("Stage5: expected no UNRESOLVED_INPUT for resolved flow, got: %v", iss)
		}
	}
}

// DF-21: response reads a var that no step writes → UNRESOLVED_INPUT error at publish.
func TestDataFlow_Stage5_MissingResponseVar_PublishFails(t *testing.T) {
	// "missing_var" is never written
	issues := publishFail(t, minimalCanvas(`[
		{"id": "in",  "type": "input",    "config": {}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "missing_var"}}
	]`))
	if !hasCode(issues, "UNRESOLVED_INPUT") {
		t.Errorf("Stage5: expected UNRESOLVED_INPUT for response reading unwritten var, got: %v", issues)
	}
	if !hasSeverity(issues, "UNRESOLVED_INPUT", "error") {
		t.Errorf("Stage5: UNRESOLVED_INPUT for Required input should be 'error' at publish, got: %v", issues)
	}
}

// DF-22: response reads a var that no step writes → UNRESOLVED_INPUT is warning at validate.
func TestDataFlow_Stage5_MissingResponseVar_ValidateWarns(t *testing.T) {
	issues := validateIssues(t, minimalCanvas(`[
		{"id": "in",  "type": "input",    "config": {}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "missing_var"}}
	]`))
	if !hasCode(issues, "UNRESOLVED_INPUT") {
		t.Errorf("Stage5: expected UNRESOLVED_INPUT warning at validate, got: %v", issues)
	}
	// At validate time it should be a warning (not an error) for Required inputs
	// because the LLM response.from_var Required=true maps severity=validate→"warning".
	if !hasSeverity(issues, "UNRESOLVED_INPUT", "warning") {
		t.Errorf("Stage5: UNRESOLVED_INPUT at validate should be 'warning', got: %v", issues)
	}
}

// DF-23: LLM reads {{.user_query}} but input writes "input" → UNRESOLVED_INPUT warning
// (because LLM template vars are Required=false → always warning regardless of stage).
func TestDataFlow_Stage5_LLMTemplateVarUnresolved(t *testing.T) {
	issues := validateIssues(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",
		 "config": {"user_prompt": "Summarize {{.user_query}}", "output_var": "output"},
		 "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "output"}}
	]`))
	if !hasCode(issues, "UNRESOLVED_INPUT") {
		t.Errorf("Stage5: expected UNRESOLVED_INPUT for unresolved LLM template var, got: %v", issues)
	}
	// Required=false → always "warning" regardless of validate vs publish.
	if !hasSeverity(issues, "UNRESOLVED_INPUT", "warning") {
		t.Errorf("Stage5: unresolved non-required template var should be 'warning'")
	}
}

// DF-24: pipeline with input → LLM reading {{.input}} → response: no UNRESOLVED_INPUT.
// The "input" var is always pre-seeded from the invocation context.
func TestDataFlow_Stage5_InputVarAlwaysAvailable(t *testing.T) {
	issues := validateIssues(t, minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",
		 "config": {"user_prompt": "Process: {{.input}}", "output_var": "output"},
		 "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "output"}}
	]`))
	for _, iss := range issues {
		if iss.Code == "UNRESOLVED_INPUT" && iss.Field == "input" {
			t.Errorf("Stage5: 'input' should always be available (pre-seeded), got UNRESOLVED_INPUT: %v", iss)
		}
	}
}

// DF-26: a fully correct pipeline (input → LLM → response) passes both validate and publish.
func TestDataFlow_Stage5_FullPipeline_NoIssues(t *testing.T) {
	canvas := minimalCanvas(`[
		{"id": "in",  "type": "input", "config": {}, "next": ["llm"]},
		{"id": "llm", "type": "llm",  "config": {"user_prompt": "{{.input}}", "output_var": "output"}, "next": ["out"]},
		{"id": "out", "type": "response", "config": {"from_var": "output"}}
	]`)
	// Validate: no errors expected.
	_, issues := agentgen.Validate("a", "t", "d", "full_pipeline", json.RawMessage(canvas))
	for _, iss := range issues {
		if iss.Severity == "error" {
			t.Errorf("full pipeline validate: unexpected error: %v", iss)
		}
		if iss.Code == "UNRESOLVED_INPUT" {
			t.Errorf("full pipeline validate: unexpected UNRESOLVED_INPUT: %v", iss)
		}
	}
	// Publish: should succeed.
	spec, pubIssues := agentgen.CompileForPublish("a", "t", "d", "full_pipeline", json.RawMessage(canvas))
	for _, iss := range pubIssues {
		if iss.Severity == "error" {
			t.Errorf("full pipeline publish: unexpected error: %v", iss)
		}
	}
	if spec == nil {
		t.Error("full pipeline publish: expected non-nil spec")
	}
}
