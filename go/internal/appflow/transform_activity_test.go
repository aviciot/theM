package appflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/agentgen/transform"
)

// TestTransformActivity_BasicChain verifies a two-step chain (strip_fences →
// json_path) writes the correct output vars and returns a populated TraceJSON.
func TestTransformActivity_BasicChain(t *testing.T) {
	acts := &AppFlowActivities{}

	input := TransformActivityInput{
		RunID:  "run-1",
		NodeID: "node-1",
		Functions: []transform.FunctionStep{
			{Fn: "strip_fences", InputVar: "raw", OutputVar: "clean", Args: map[string]string{}},
			{Fn: "json_path", InputVar: "clean", OutputVar: "city", Args: map[string]string{"path": "$.city"}},
		},
		Vars:      FlowVars{"raw": "```json\n{\"city\":\"Rome\"}\n```"},
		Verbosity: "debug",
	}

	out, err := acts.TransformActivity(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.UpdatedVars["clean"] != `{"city":"Rome"}` {
		t.Errorf("clean: got %q", out.UpdatedVars["clean"])
	}
	if out.UpdatedVars["city"] != "Rome" {
		t.Errorf("city: got %q", out.UpdatedVars["city"])
	}
	if out.TraceJSON == "" {
		t.Error("TraceJSON must not be empty")
	}
	var trace transform.TraceResult
	if err := json.Unmarshal([]byte(out.TraceJSON), &trace); err != nil {
		t.Fatalf("TraceJSON is not valid JSON: %v", err)
	}
	if len(trace.Steps) != 2 {
		t.Errorf("expected 2 trace steps, got %d", len(trace.Steps))
	}
	for i, s := range trace.Steps {
		if !s.OK {
			t.Errorf("step %d (%s) reported failure: %s", i, s.Fn, s.Error)
		}
	}
}

// TestTransformActivity_EmptyFunctions verifies an empty chain returns
// no vars and an empty trace.
func TestTransformActivity_EmptyFunctions(t *testing.T) {
	acts := &AppFlowActivities{}
	out, err := acts.TransformActivity(context.Background(), TransformActivityInput{
		RunID: "run-2", NodeID: "node-2",
		Vars: FlowVars{"output": "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.UpdatedVars) != 0 {
		t.Errorf("expected no vars, got %v", out.UpdatedVars)
	}
}

// TestTransformActivity_UnknownFunction verifies that an unknown function name
// causes an error and the TraceJSON still contains the failing step record.
func TestTransformActivity_UnknownFunction(t *testing.T) {
	acts := &AppFlowActivities{}
	out, err := acts.TransformActivity(context.Background(), TransformActivityInput{
		RunID: "run-3", NodeID: "node-3",
		Functions: []transform.FunctionStep{
			{Fn: "does_not_exist", InputVar: "output", OutputVar: "x"},
		},
		Vars: FlowVars{"output": "hello"},
	})
	if err == nil {
		t.Fatal("expected error for unknown function, got nil")
	}
	if out.TraceJSON == "" {
		t.Error("TraceJSON must be populated even on error")
	}
}

// TestTransformActivity_PartialVarsOnError verifies that vars written by
// successful steps before a failure are returned in UpdatedVars.
func TestTransformActivity_PartialVarsOnError(t *testing.T) {
	acts := &AppFlowActivities{}
	out, err := acts.TransformActivity(context.Background(), TransformActivityInput{
		RunID: "run-4", NodeID: "node-4",
		Functions: []transform.FunctionStep{
			{Fn: "upper", InputVar: "input", OutputVar: "upped"},
			{Fn: "json_path", InputVar: "upped", OutputVar: "field", Args: map[string]string{"path": "$.x"}},
		},
		Vars: FlowVars{"input": "hello"},
	})
	if err == nil {
		t.Fatal("expected error (json_path on non-JSON), got nil")
	}
	if out.UpdatedVars["upped"] != "HELLO" {
		t.Errorf("partial result: upped=%q", out.UpdatedVars["upped"])
	}
}

// TestTransformNodeConfig_Unmarshal verifies the config struct correctly
// deserialises a JSON config as stored in the app definition.
func TestTransformNodeConfig_Unmarshal(t *testing.T) {
	raw := `{"functions":[{"fn":"strip_fences","input_var":"output","output_var":"clean"}]}`
	var cfg TransformNodeConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Functions) != 1 || cfg.Functions[0].Fn != "strip_fences" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}
