package agentgen_test

import (
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/agentgen"
)

// allStepTypes lists every StepType constant declared in spec.go.
var allStepTypes = []agentgen.StepType{
	agentgen.StepInput,
	agentgen.StepLLM,
	agentgen.StepHTTP,
	agentgen.StepTransform,
	agentgen.StepResponse,
	agentgen.StepBranch,
	agentgen.StepLoop,
	agentgen.StepParallel,
	agentgen.StepA2ACall,
	agentgen.StepHumanWait,
	agentgen.StepStreamOut,
}

// TestNodeRegistry_AllTypesRegistered verifies that every StepType constant has
// a corresponding NodeDef in the registry.
func TestNodeRegistry_AllTypesRegistered(t *testing.T) {
	for _, st := range allStepTypes {
		_, ok := agentgen.LookupNode(st)
		if !ok {
			t.Errorf("StepType %q is not registered in the node registry", st)
		}
	}
}

// TestNodeRegistry_KnownStepTypesCount verifies KnownStepTypes returns all 11 types.
func TestNodeRegistry_KnownStepTypesCount(t *testing.T) {
	known := agentgen.KnownStepTypes()
	if len(known) != 11 {
		t.Errorf("expected 11 registered node types, got %d: %v", len(known), known)
	}
}

// TestNodeRegistry_InputProperties verifies the input node's metadata.
func TestNodeRegistry_InputProperties(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepInput)
	if !ok {
		t.Fatal("StepInput not registered")
	}
	if !def.IsSource {
		t.Error("input node must have IsSource=true")
	}
	if def.IsSink {
		t.Error("input node must have IsSink=false")
	}
	if def.OutputArity != "single" {
		t.Errorf("input node OutputArity: want %q, got %q", "single", def.OutputArity)
	}
	if def.Execute == nil {
		t.Error("input node Execute must be non-nil (implemented)")
	}
	if def.Label == "" {
		t.Error("input node must have a non-empty Label")
	}
	if def.Version < 1 {
		t.Errorf("input node Version must be >= 1, got %d", def.Version)
	}
}

// TestNodeRegistry_ToInfo verifies that ToInfo correctly derives Executable from Execute.
func TestNodeRegistry_ToInfo(t *testing.T) {
	inputDef, _ := agentgen.LookupNode(agentgen.StepInput)
	info := inputDef.ToInfo()
	if !info.Executable {
		t.Error("ToInfo: input node must have Executable=true")
	}
	if info.Label != inputDef.Label {
		t.Errorf("ToInfo: Label mismatch: got %q, want %q", info.Label, inputDef.Label)
	}
	if info.Version != inputDef.Version {
		t.Errorf("ToInfo: Version mismatch: got %d, want %d", info.Version, inputDef.Version)
	}

	branchDef, _ := agentgen.LookupNode(agentgen.StepBranch)
	branchInfo := branchDef.ToInfo()
	if branchInfo.Executable {
		t.Error("ToInfo: branch node must have Executable=false (Execute is nil)")
	}
}

// TestNodeRegistry_AllNodesHaveLabelAndVersion verifies every registered node has required canvas metadata.
func TestNodeRegistry_AllNodesHaveLabelAndVersion(t *testing.T) {
	for _, info := range agentgen.AllNodeTypeInfos() {
		if info.Label == "" {
			t.Errorf("node type %q has empty Label", info.Type)
		}
		if info.Version < 1 {
			t.Errorf("node type %q has Version < 1 (%d)", info.Type, info.Version)
		}
		if info.OutputArity != "single" && info.OutputArity != "multi" && info.OutputArity != "none" {
			t.Errorf("node type %q has invalid OutputArity %q", info.Type, info.OutputArity)
		}
	}
}

// TestNodeRegistry_ResponseProperties verifies the response node's metadata.
func TestNodeRegistry_ResponseProperties(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepResponse)
	if !ok {
		t.Fatal("StepResponse not registered")
	}
	if def.IsSource {
		t.Error("response node must have IsSource=false")
	}
	if !def.IsSink {
		t.Error("response node must have IsSink=true")
	}
	if def.OutputArity != "none" {
		t.Errorf("response node OutputArity: want %q, got %q", "none", def.OutputArity)
	}
	if def.Execute == nil {
		t.Error("response node Execute must be non-nil (implemented)")
	}
}

// TestNodeRegistry_BranchOutputArity verifies branch is multi-output.
func TestNodeRegistry_BranchOutputArity(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepBranch)
	if !ok {
		t.Fatal("StepBranch not registered")
	}
	if def.OutputArity != "multi" {
		t.Errorf("branch OutputArity: want %q, got %q", "multi", def.OutputArity)
	}
	if def.Execute != nil {
		t.Error("branch Execute must be nil (stub — not yet implemented)")
	}
}

// TestNodeRegistry_StreamOutIsSink verifies stream_out terminates the pipeline.
func TestNodeRegistry_StreamOutIsSink(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepStreamOut)
	if !ok {
		t.Fatal("StepStreamOut not registered")
	}
	if !def.IsSink {
		t.Error("stream_out must have IsSink=true")
	}
	if def.OutputArity != "none" {
		t.Errorf("stream_out OutputArity: want %q, got %q", "none", def.OutputArity)
	}
}

// TestNodeRegistry_UnknownTypeReturnsFalse verifies LookupNode returns ok=false
// for types that are not in the registry.
func TestNodeRegistry_UnknownTypeReturnsFalse(t *testing.T) {
	_, ok := agentgen.LookupNode(agentgen.StepType("banana"))
	if ok {
		t.Error("LookupNode must return ok=false for an unregistered step type")
	}
}

// TestNodeRegistry_CompilerRejectsUnknownStepType is an integration test that
// verifies the compiler uses the registry to reject unknown step types, consistent
// with the existing TestCompile_UnknownStepType pattern in compiler_test.go.
func TestNodeRegistry_CompilerRejectsUnknownStepType(t *testing.T) {
	errs := compileFail(t, `{
		"agent_root": {"display_name": "X"},
		"skills": [{
			"skill_id": "s1",
			"steps": [{"id": "s", "type": "not_a_real_type"}]
		}]
	}`)
	if !hasCode(errs, "UNKNOWN_STEP_TYPE") {
		t.Errorf("expected UNKNOWN_STEP_TYPE from registry-based compiler, got: %v", errs)
	}
}

// TestNodeRegistry_ImplementedTypesHaveNonNilExecute verifies that the five
// implemented node types all have an Execute function set.
func TestNodeRegistry_ImplementedTypesHaveNonNilExecute(t *testing.T) {
	implemented := []agentgen.StepType{
		agentgen.StepInput,
		agentgen.StepLLM,
		agentgen.StepHTTP,
		agentgen.StepTransform,
		agentgen.StepResponse,
	}
	for _, st := range implemented {
		def, ok := agentgen.LookupNode(st)
		if !ok {
			t.Errorf("step type %q not registered", st)
			continue
		}
		if def.Execute == nil {
			t.Errorf("step type %q expected Execute != nil (implemented)", st)
		}
	}
}

// TestNodeRegistry_StubTypesHaveNilExecute verifies that stub node types have
// Execute=nil, signalling they are not yet implemented.
func TestNodeRegistry_StubTypesHaveNilExecute(t *testing.T) {
	stubs := []agentgen.StepType{
		agentgen.StepBranch,
		agentgen.StepLoop,
		agentgen.StepParallel,
		agentgen.StepA2ACall,
		agentgen.StepHumanWait,
		agentgen.StepStreamOut,
	}
	for _, st := range stubs {
		def, ok := agentgen.LookupNode(st)
		if !ok {
			t.Errorf("step type %q not registered", st)
			continue
		}
		if def.Execute != nil {
			t.Errorf("stub step type %q expected Execute == nil (not yet implemented)", st)
		}
	}
}

// TestNodeRegistry_LLMValidate_UndeclaredSlot verifies that the LLM node's
// Validate function catches an undeclared provider_key_slot.
func TestNodeRegistry_LLMValidate_UndeclaredSlot(t *testing.T) {
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
		t.Errorf("LLM node Validate: expected UNDECLARED_SLOT, got: %v", errs)
	}
}

// TestNodeRegistry_HTTPValidate_UndeclaredSlot verifies that the HTTP node's
// Validate function catches an undeclared credential_slot.
func TestNodeRegistry_HTTPValidate_UndeclaredSlot(t *testing.T) {
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
		t.Errorf("HTTP node Validate: expected UNDECLARED_SLOT, got: %v", errs)
	}
}

// TestNodeRegistry_ParallelOutputArity verifies parallel is multi-output.
func TestNodeRegistry_ParallelOutputArity(t *testing.T) {
	def, ok := agentgen.LookupNode(agentgen.StepParallel)
	if !ok {
		t.Fatal("StepParallel not registered")
	}
	if def.OutputArity != "multi" {
		t.Errorf("parallel OutputArity: want %q, got %q", "multi", def.OutputArity)
	}
}

// Verify compileFail and hasCode helpers work correctly (smoke test for test helpers).
func TestNodeRegistry_Helpers_Smoke(t *testing.T) {
	spec, errs := agentgen.Compile("a", "t", "d", "slug", json.RawMessage(`{"agent_root":{"display_name":"X"},"skills":[]}`))
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
}
