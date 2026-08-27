package agentgen_test

// interpreter_contracts_test.go — Stage 6 runtime contract enforcement tests.
//
// Tests are grouped as CONT-1..CONT-12 and cover:
//   - Scoped input resolution: nodes receive only declared Inputs
//   - Output-only promotion: undeclared writes are dropped
//   - ErrContractViolation for missing required inputs
//   - Fan-out: two downstream steps both reading the same upstream output
//   - Legacy fallback: steps without compiled Inputs/Outputs use full global vars
//   - Branch step with scoped inputs routes correctly
//   - Canonical transform output derivation via functions[].output_var

import (
	"context"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/agentgen/transform"
)

// ic returns a minimal InvocationContext for contract tests.
func icCtx() *agentgen.InvocationContext {
	return &agentgen.InvocationContext{TenantID: "t1", ApplicationID: "a1", AgentID: "ag1"}
}

// inputStep builds a standard StepInput step with fully compiled Inputs/Outputs.
// The input step always reads vars["input"] (declared by DeriveInputs) and writes
// to the binding var.
func inputStep(id, bindVar, next string) agentgen.StepSpec {
	return agentgen.StepSpec{
		ID:   id,
		Type: agentgen.StepInput,
		Config: mustJSON(agentgen.InputStepConfig{
			Bindings: map[string]string{"text": bindVar},
		}),
		Next:    []string{next},
		Inputs:  []agentgen.VarRef{{Name: "input", Required: false}},
		Outputs: []agentgen.VarRef{{Name: bindVar, Required: false}},
	}
}

// responseStep builds a StepResponse with compiled Inputs (the fromVar).
func responseStep(id, fromVar string) agentgen.StepSpec {
	return agentgen.StepSpec{
		ID:     id,
		Type:   agentgen.StepResponse,
		Config: mustJSON(agentgen.ResponseStepConfig{FromVar: fromVar}),
		Inputs: []agentgen.VarRef{{Name: fromVar, Required: true}},
	}
}

// ── CONT-1: end-to-end pipeline with compiled contracts ───────────────────────

// CONT-1: A fully compiled pipeline (all steps have Inputs/Outputs) produces
// the correct result — scoped enforcement does not break normal execution.
func TestInterpreter_Contract_EndToEnd(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-contract",
		Steps: []agentgen.StepSpec{
			inputStep("in", "raw", "xform"),
			{
				ID:   "xform",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						{Fn: "upper", InputVar: "raw", OutputVar: "upper"},
					},
				}),
				Next:    []string{"out"},
				Inputs:  []agentgen.VarRef{{Name: "raw", Required: true}},
				Outputs: []agentgen.VarRef{{Name: "upper", Required: false}},
			},
			responseStep("out", "upper"),
		},
	}

	result, err := interp.Execute(context.Background(), icCtx(), skill, "hello")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "HELLO" {
		t.Errorf("expected HELLO, got %q", result.Text)
	}
}

// ── CONT-2: scoped input — undeclared global var not visible to node ───────────

// CONT-2: A node with declared Inputs must not be able to read a pipeline var
// that is present globally but not in its declared inputs.
// The transform step only declares "raw" as input; "secret_var" is in the global
// pipeline (injected as extraVars) but must be absent from the scoped map.
func TestInterpreter_Contract_ScopedInput_UndeclaredVarNotVisible(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-scoped",
		Steps: []agentgen.StepSpec{
			inputStep("in", "raw", "xform"),
			{
				ID:   "xform",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						// Reads "raw" only; "secret_var" is globally present but undeclared.
						{Fn: "upper", InputVar: "raw", OutputVar: "out_var"},
					},
				}),
				Next:    []string{"out"},
				Inputs:  []agentgen.VarRef{{Name: "raw", Required: true}},
				Outputs: []agentgen.VarRef{{Name: "out_var", Required: false}},
			},
			responseStep("out", "out_var"),
		},
	}

	result, err := interp.Execute(context.Background(), icCtx(), skill, "world",
		map[string]any{"secret_var": "CLASSIFIED"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The transform saw only "raw" → "WORLD"; "secret_var" was not visible.
	if result.Text != "WORLD" {
		t.Errorf("expected WORLD, got %q", result.Text)
	}
}

// ── CONT-3: output-only promotion — undeclared write is dropped ───────────────

// CONT-3: A node that writes to a key not in its declared Outputs must not
// pollute the global PipelineVars. "side_effect" is produced by the transform
// but not declared as an Output — it must be absent from global state.
func TestInterpreter_Contract_OutputPromotion_UndeclaredWriteDropped(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-output-gate",
		Steps: []agentgen.StepSpec{
			inputStep("in", "raw", "xform"),
			{
				ID:   "xform",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						{Fn: "upper", InputVar: "raw", OutputVar: "result"},
						// "side_effect" is written by the transform but NOT declared as Output.
						{Fn: "upper", InputVar: "raw", OutputVar: "side_effect"},
					},
				}),
				Next:   []string{"read-side"},
				Inputs: []agentgen.VarRef{{Name: "raw", Required: true}},
				// Only "result" declared — "side_effect" must be dropped.
				Outputs: []agentgen.VarRef{{Name: "result", Required: false}},
			},
			{
				// Reads "side_effect" — must get "" because it was not promoted.
				ID:     "read-side",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "side_effect"}),
				Inputs: []agentgen.VarRef{{Name: "side_effect", Required: false}},
			},
		},
	}

	result, err := interp.Execute(context.Background(), icCtx(), skill, "hello")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// "side_effect" was not promoted; response sees "" (absent var → empty string).
	if result.Text != "" {
		t.Errorf("expected empty (side_effect dropped), got %q", result.Text)
	}
}

// ── CONT-4: ErrContractViolation — missing required input ─────────────────────

// CONT-4: A step with a Required input absent from global state at execution
// time must return ErrContractViolation with Kind="missing_required_input".
func TestInterpreter_Contract_MissingRequiredInput_Error(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-required",
		Steps: []agentgen.StepSpec{
			inputStep("in", "raw", "out"),
			{
				// Declares "must_exist" as Required but it is never written upstream.
				ID:     "out",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "raw"}),
				Inputs: []agentgen.VarRef{{Name: "must_exist", Required: true}},
			},
		},
	}

	_, err := interp.Execute(context.Background(), icCtx(), skill, "hello")
	if err == nil {
		t.Fatal("expected ErrContractViolation, got nil")
	}

	var cv *agentgen.ErrContractViolation
	if !errors.As(err, &cv) {
		t.Fatalf("expected *ErrContractViolation, got %T: %v", err, err)
	}
	if cv.Kind != "missing_required_input" {
		t.Errorf("expected kind missing_required_input, got %q", cv.Kind)
	}
	if cv.VarName != "must_exist" {
		t.Errorf("expected VarName must_exist, got %q", cv.VarName)
	}
	if cv.StepID != "out" {
		t.Errorf("expected StepID out, got %q", cv.StepID)
	}
}

// ── CONT-5: optional missing input is not an error ────────────────────────────

// CONT-5: A step with a non-Required input that is absent must not error.
func TestInterpreter_Contract_MissingOptionalInput_NoError(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-optional",
		Steps: []agentgen.StepSpec{
			inputStep("in", "raw", "out"),
			{
				// "maybe_present" is optional and absent — must not error.
				ID:     "out",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "raw"}),
				Inputs: []agentgen.VarRef{
					{Name: "raw", Required: true},
					{Name: "maybe_present", Required: false},
				},
			},
		},
	}

	result, err := interp.Execute(context.Background(), icCtx(), skill, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "hello" {
		t.Errorf("expected hello, got %q", result.Text)
	}
}

// ── CONT-6: fan-out — two downstream steps read same upstream output ───────────

// CONT-6: When two downstream steps both declare a var written by an upstream
// step, both must receive its value independently via their scoped copies.
func TestInterpreter_Contract_FanOut_TwoStepsReadSameVar(t *testing.T) {
	fake := &fakeLLM{reply: "LLM response"}
	interp := agentgen.NewInterpreter(nil, &fakeLLMFactory{llm: fake}, "key")

	// Input → LLM(writes "llm_out") → Transform(reads "llm_out", writes "final") → Response
	skill := &agentgen.SkillSpec{
		ID: "skill-fanout",
		Steps: []agentgen.StepSpec{
			inputStep("in", "query", "llm"),
			{
				ID:   "llm",
				Type: agentgen.StepLLM,
				Config: mustJSON(agentgen.LLMStepConfig{
					Provider: "anthropic", Model: "m", MaxTokens: 10,
					UserPrompt: "{{.query}}", OutputVar: "llm_out",
				}),
				Next:    []string{"xform"},
				Inputs:  []agentgen.VarRef{{Name: "query", Required: true}},
				Outputs: []agentgen.VarRef{{Name: "llm_out", Required: false}},
			},
			{
				ID:   "xform",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						{Fn: "upper", InputVar: "llm_out", OutputVar: "final"},
					},
				}),
				Next:    []string{"out"},
				Inputs:  []agentgen.VarRef{{Name: "llm_out", Required: true}},
				Outputs: []agentgen.VarRef{{Name: "final", Required: false}},
			},
			responseStep("out", "final"),
		},
	}

	result, err := interp.Execute(context.Background(), icCtx(), skill, "ping")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "LLM RESPONSE" {
		t.Errorf("expected LLM RESPONSE, got %q", result.Text)
	}
}

// ── CONT-7: legacy fallback — steps without compiled contracts ────────────────

// CONT-7: Steps with no Inputs/Outputs (legacy or stub path) must execute
// with full global vars and continue to work correctly.
func TestInterpreter_Contract_LegacyFallback_NoContractPassesGlobalVars(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	// Deliberately omit Inputs/Outputs — simulates a spec compiled before Stage 6.
	skill := &agentgen.SkillSpec{
		ID: "skill-legacy",
		Steps: []agentgen.StepSpec{
			{
				ID:     "in",
				Type:   agentgen.StepInput,
				Config: mustJSON(agentgen.InputStepConfig{Bindings: map[string]string{"text": "msg"}}),
				Next:   []string{"out"},
				// No Inputs/Outputs declared — legacy path.
			},
			{
				ID:     "out",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "msg"}),
				// No Inputs declared — legacy path.
			},
		},
	}

	result, err := interp.Execute(context.Background(), icCtx(), skill, "legacy works")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "legacy works" {
		t.Errorf("expected 'legacy works', got %q", result.Text)
	}
}

// ── CONT-8: ErrContractViolation is unwrappable via errors.As ─────────────────

// CONT-8: The error returned for a contract violation must be unwrappable with
// errors.As(*ErrContractViolation) even when wrapped by the interpreter's step
// error context (fmt.Errorf("step %q: %w", ...)).
func TestInterpreter_Contract_ErrorIsUnwrappable(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-unwrap",
		Steps: []agentgen.StepSpec{
			inputStep("in", "x", "out"),
			{
				ID:     "out",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "x"}),
				Inputs: []agentgen.VarRef{{Name: "never_written", Required: true}},
			},
		},
	}

	_, err := interp.Execute(context.Background(), icCtx(), skill, "")
	if err == nil {
		t.Fatal("expected error")
	}

	var cv *agentgen.ErrContractViolation
	if !errors.As(err, &cv) {
		t.Fatalf("error must be unwrappable to *ErrContractViolation; got %T: %v", err, err)
	}
}

// ── CONT-9: transform declared outputs match functions[].output_var ────────────

// CONT-9: Transform step outputs derive from functions[].output_var only.
// No "exposed_vars" or other parallel mechanism is needed or allowed.
func TestInterpreter_Contract_Transform_OutputsFromFunctionOutputVar(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-transform-outputs",
		Steps: []agentgen.StepSpec{
			inputStep("in", "src", "xform"),
			{
				ID:   "xform",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						{Fn: "upper", InputVar: "src", OutputVar: "a"},
						{Fn: "concat", InputVar: "src", OutputVar: "b",
							Args: map[string]string{"prefix": "pre_", "suffix": ""}},
					},
				}),
				Next:   []string{"out"},
				Inputs: []agentgen.VarRef{{Name: "src", Required: true}},
				// Outputs match functions[].output_var — canonical source of truth.
				Outputs: []agentgen.VarRef{
					{Name: "a", Required: false},
					{Name: "b", Required: false},
				},
			},
			responseStep("out", "a"),
		},
	}

	result, err := interp.Execute(context.Background(), icCtx(), skill, "world")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "WORLD" {
		t.Errorf("expected WORLD, got %q", result.Text)
	}
}

// ── CONT-10: branch step with compiled contract routes correctly ───────────────

// CONT-10: Branch step with declared Inputs uses the scoped path and still
// routes correctly via nextStepOverride.
func TestInterpreter_Contract_BranchStep_ScopedInputRoutes(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-branch-scoped",
		Steps: []agentgen.StepSpec{
			inputStep("in", "x", "branch"),
			{
				ID:   "branch",
				Type: agentgen.StepBranch,
				Config: mustJSON(agentgen.BranchStepConfig{
					Expression: `{{eq .x "yes"}}`,
					TrueNext:   "resp-true",
					FalseNext:  "resp-false",
				}),
				// Branch reads "x"; writes nothing to PipelineVars (routes via nextStepOverride).
				Inputs: []agentgen.VarRef{{Name: "x", Required: true}},
			},
			{
				ID:     "resp-true",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "x"}),
				Inputs: []agentgen.VarRef{{Name: "x", Required: true}},
			},
			{
				ID:     "resp-false",
				Type:   agentgen.StepResponse,
				Config: mustJSON(agentgen.ResponseStepConfig{FromVar: "x"}),
				Inputs: []agentgen.VarRef{{Name: "x", Required: true}},
			},
		},
	}

	r, err := interp.Execute(context.Background(), icCtx(), skill, "yes")
	if err != nil {
		t.Fatalf("execute (true path): %v", err)
	}
	if r.Text != "yes" {
		t.Errorf("true path: expected yes, got %q", r.Text)
	}

	r2, err := interp.Execute(context.Background(), icCtx(), skill, "no")
	if err != nil {
		t.Fatalf("execute (false path): %v", err)
	}
	if r2.Text != "no" {
		t.Errorf("false path: expected no, got %q", r2.Text)
	}
}

// ── CONT-11: scoped vars rebuilt from current global state per step ────────────

// CONT-11: The scoped vars for each step are built from the current global state
// at the moment that step runs — outputs promoted by step N are available to step N+1.
func TestInterpreter_Contract_ScopedRebuildPerStep(t *testing.T) {
	interp := agentgen.NewInterpreter(nil, nil, "")

	skill := &agentgen.SkillSpec{
		ID: "skill-rebuild",
		Steps: []agentgen.StepSpec{
			inputStep("in", "a", "xform"),
			{
				ID:   "xform",
				Type: agentgen.StepTransform,
				Config: mustJSON(agentgen.TransformStepConfig{
					Functions: []transform.FunctionStep{
						{Fn: "upper", InputVar: "a", OutputVar: "b"},
					},
				}),
				Next:    []string{"out"},
				Inputs:  []agentgen.VarRef{{Name: "a", Required: true}},
				Outputs: []agentgen.VarRef{{Name: "b", Required: false}},
			},
			responseStep("out", "b"),
		},
	}

	result, err := interp.Execute(context.Background(), icCtx(), skill, "world")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text != "WORLD" {
		t.Errorf("expected WORLD, got %q", result.Text)
	}
}

// ── CONT-12: ErrContractViolation.Error() is readable ────────────────────────

// CONT-12: ErrContractViolation.Error() must produce a readable string containing
// the step ID, var name, and kind.
func TestErrContractViolation_ErrorString(t *testing.T) {
	cv := &agentgen.ErrContractViolation{
		StepID:  "my-step",
		VarName: "my-var",
		Kind:    "missing_required_input",
	}
	s := cv.Error()
	for _, sub := range []string{"my-step", "my-var", "missing_required_input"} {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Error() %q must contain %q", s, sub)
		}
	}
}
