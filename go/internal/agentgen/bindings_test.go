package agentgen_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

// stepsCanvas builds a minimal valid canvas JSON from multiple raw step JSON objects.
func stepsCanvas(steps ...string) string {
	stepsJSON := "[" + strings.Join(steps, ",") + "]"
	return `{
		"agent_root": {"display_name": "Binding Test Agent"},
		"skills": [{"skill_id": "s1", "name": "skill", "steps": ` + stepsJSON + `}]
	}`
}

// BND-1: no explicit bindings → compiles without BROKEN_BINDING issues.
func TestBindings_NoExplicitBindings_Clean(t *testing.T) {
	raw := stepsCanvas(
		`{"id":"in1","type":"input","config":{"bindings":{"text":"raw"}},"next":["resp1"]}`,
		`{"id":"resp1","type":"response","config":{"from_var":"raw"},"next":[]}`,
	)
	spec := compileOK(t, raw)
	if spec == nil {
		t.Fatal("expected spec")
	}
	// No BROKEN_BINDING issues.
	for _, s := range spec.Skills {
		for _, step := range s.Steps {
			if step.ID == "in1" && len(step.Outputs) == 0 {
				t.Errorf("in1 should have outputs derived")
			}
		}
	}
}

// BND-2: explicit binding from a valid step and port → SourceStep/SourcePort populated.
func TestBindings_ExplicitBinding_PopulatesSourceFields(t *testing.T) {
	raw := stepsCanvas(
		`{"id":"in1","type":"input","config":{"bindings":{"text":"raw"}},"next":["llm1"]}`,
		`{
			"id":"llm1","type":"llm",
			"config":{"provider":"anthropic","model":"claude-haiku-4-5-20251001","output_var":"summary","user_prompt":"summarize {{.raw}}"},
			"next":["resp1"]
		}`,
		`{
			"id":"resp1","type":"response",
			"config":{"from_var":"summary"},
			"next":[],
			"inputs":{"from_var":{"from_step":"llm1","from_port":"output"}}
		}`,
	)
	spec := compileOK(t, raw)

	var respStep *agentgen.StepSpec
	for _, sk := range spec.Skills {
		for i := range sk.Steps {
			if sk.Steps[i].ID == "resp1" {
				s := sk.Steps[i]
				respStep = &s
			}
		}
	}
	if respStep == nil {
		t.Fatal("resp1 step not found in compiled spec")
	}
	if len(respStep.Inputs) == 0 {
		t.Fatal("resp1 should have inputs")
	}

	var bound *agentgen.VarRef
	for i := range respStep.Inputs {
		if respStep.Inputs[i].SourceStep == "llm1" {
			ref := respStep.Inputs[i]
			bound = &ref
			break
		}
	}
	if bound == nil {
		t.Fatalf("expected a VarRef with SourceStep=llm1 in resp1 inputs, got: %+v", respStep.Inputs)
	}
	if bound.SourcePort != "output" {
		t.Errorf("expected SourcePort=output, got %q", bound.SourcePort)
	}
	if bound.Name == "" {
		t.Error("resolved VarRef.Name should not be empty")
	}
}

// BND-3: BROKEN_BINDING when from_step does not exist — warning on Validate.
func TestBindings_BrokenBinding_UnknownSourceStep_Validate(t *testing.T) {
	raw := stepsCanvas(
		`{"id":"in1","type":"input","config":{"bindings":{"text":"raw"}},"next":["resp1"]}`,
		`{
			"id":"resp1","type":"response",
			"config":{"from_var":"raw"},
			"next":[],
			"inputs":{"from_var":{"from_step":"nonexistent","from_port":"output"}}
		}`,
	)
	issues := validateIssues(t, raw)
	if !hasSeverity(issues, "BROKEN_BINDING", "warning") {
		t.Errorf("expected BROKEN_BINDING warning, got: %v", issues)
	}
}

// BND-4: BROKEN_BINDING when from_step does not exist — error on CompileForPublish.
func TestBindings_BrokenBinding_UnknownSourceStep_Publish(t *testing.T) {
	raw := stepsCanvas(
		`{"id":"in1","type":"input","config":{"bindings":{"text":"raw"}},"next":["resp1"]}`,
		`{
			"id":"resp1","type":"response",
			"config":{"from_var":"raw"},
			"next":[],
			"inputs":{"from_var":{"from_step":"nonexistent","from_port":"output"}}
		}`,
	)
	issues := publishFail(t, raw)
	if !hasSeverity(issues, "BROKEN_BINDING", "error") {
		t.Errorf("expected BROKEN_BINDING error on publish, got: %v", issues)
	}
}

// BND-5: BROKEN_BINDING when from_port does not exist on source step — warning on Validate.
func TestBindings_BrokenBinding_UnknownSourcePort_Validate(t *testing.T) {
	raw := stepsCanvas(
		`{"id":"in1","type":"input","config":{"bindings":{"text":"raw"}},"next":["llm1"]}`,
		`{
			"id":"llm1","type":"llm",
			"config":{"provider":"anthropic","model":"claude-haiku-4-5-20251001","output_var":"summary","user_prompt":"{{.raw}}"},
			"next":["resp1"]
		}`,
		`{
			"id":"resp1","type":"response",
			"config":{"from_var":"summary"},
			"next":[],
			"inputs":{"from_var":{"from_step":"llm1","from_port":"does_not_exist"}}
		}`,
	)
	issues := validateIssues(t, raw)
	if !hasSeverity(issues, "BROKEN_BINDING", "warning") {
		t.Errorf("expected BROKEN_BINDING warning for unknown port, got: %v", issues)
	}
}

// BND-6: BROKEN_BINDING when from_port does not exist on source — error on CompileForPublish.
func TestBindings_BrokenBinding_UnknownSourcePort_Publish(t *testing.T) {
	raw := stepsCanvas(
		`{"id":"in1","type":"input","config":{"bindings":{"text":"raw"}},"next":["llm1"]}`,
		`{
			"id":"llm1","type":"llm",
			"config":{"provider":"anthropic","model":"claude-haiku-4-5-20251001","output_var":"summary","user_prompt":"{{.raw}}"},
			"next":["resp1"]
		}`,
		`{
			"id":"resp1","type":"response",
			"config":{"from_var":"summary"},
			"next":[],
			"inputs":{"from_var":{"from_step":"llm1","from_port":"does_not_exist"}}
		}`,
	)
	issues := publishFail(t, raw)
	if !hasSeverity(issues, "BROKEN_BINDING", "error") {
		t.Errorf("expected BROKEN_BINDING error on publish, got: %v", issues)
	}
}

// BND-7: PortDef present on LLM node type via AllNodeTypeInfos.
func TestBindings_LLMNodeHasStaticPorts(t *testing.T) {
	infos := agentgen.AllNodeTypeInfos()
	var llm *agentgen.NodeTypeInfo
	for i := range infos {
		if infos[i].Type == agentgen.StepLLM {
			llm = &infos[i]
			break
		}
	}
	if llm == nil {
		t.Fatal("LLM node not registered")
	}
	if len(llm.InputPorts) == 0 {
		t.Error("LLM node should have InputPorts declared")
	}
	if len(llm.OutputPorts) == 0 {
		t.Error("LLM node should have OutputPorts declared")
	}

	// Output port ID must be "output" (stable identifier).
	found := false
	for _, p := range llm.OutputPorts {
		if p.ID == "output" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("LLM node output ports should include id='output', got: %+v", llm.OutputPorts)
	}
}

// BND-8: Response node has InputPorts, no OutputPorts (sink).
func TestBindings_ResponseNodeHasInputPortNoOutputPort(t *testing.T) {
	infos := agentgen.AllNodeTypeInfos()
	var resp *agentgen.NodeTypeInfo
	for i := range infos {
		if infos[i].Type == agentgen.StepResponse {
			resp = &infos[i]
			break
		}
	}
	if resp == nil {
		t.Fatal("Response node not registered")
	}
	if len(resp.InputPorts) == 0 {
		t.Error("Response node should have InputPorts declared")
	}
	if len(resp.OutputPorts) != 0 {
		t.Errorf("Response node (sink) should have no OutputPorts, got: %+v", resp.OutputPorts)
	}
}

// BND-9: VarRef round-trips PortID/SourceStep/SourcePort through JSON.
func TestBindings_VarRefJSONRoundTrip(t *testing.T) {
	ref := agentgen.VarRef{
		Name:       "summary",
		Required:   true,
		PortID:     "from_var",
		SourceStep: "llm1",
		SourcePort: "output",
	}
	b, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	var got agentgen.VarRef
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != ref {
		t.Errorf("round-trip mismatch: want %+v, got %+v", ref, got)
	}
}

// BND-10: existing canvas JSON with no "inputs" field compiles without error (backward compat).
func TestBindings_NoInputsField_BackwardCompat(t *testing.T) {
	// Classic canvas step shape — no "inputs" field.
	raw := `{
		"agent_root": {"display_name": "Old Agent"},
		"skills": [{
			"skill_id": "s1", "name": "s",
			"steps": [
				{"id":"in1","type":"input","config":{"bindings":{"text":"raw"}},"next":["llm1"]},
				{"id":"llm1","type":"llm","config":{"provider":"anthropic","model":"claude-haiku-4-5-20251001","output_var":"out","user_prompt":"{{.raw}}"},"next":["resp1"]},
				{"id":"resp1","type":"response","config":{"from_var":"out"},"next":[]}
			]
		}]
	}`
	spec := compileOK(t, raw)
	if len(spec.Skills) == 0 || len(spec.Skills[0].Steps) == 0 {
		t.Fatal("expected compiled skill with steps")
	}
}
