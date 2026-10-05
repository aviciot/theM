package appflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aviciot/them/internal/agentgen/transform"
	"go.temporal.io/sdk/workflow"
)

const AppFlowTransformActivityName = "AppFlowTransformActivity"

// TransformNodeConfig is the canvas-side config for a transform node.
// Functions mirrors transform.FunctionStep exactly — reusing the agentgen
// type keeps the JSON wire format and the executor in sync with zero duplication.
type TransformNodeConfig struct {
	Functions []transform.FunctionStep `json:"functions,omitempty"`
}

// TransformActivityInput carries everything the activity needs.
// Vars is a snapshot of the current FlowVars at the point the node runs.
// No credential is needed — transform is pure in-process string manipulation.
type TransformActivityInput struct {
	RunID     string                   `json:"run_id"`
	NodeID    string                   `json:"node_id"`
	Functions []transform.FunctionStep `json:"functions"`
	Vars      FlowVars                 `json:"vars"`
	Verbosity string                   `json:"verbosity"`
}

// TransformActivityOutput carries the mutated vars and the per-step trace.
// Trace is a JSON-encoded transform.TraceResult — the debug inspector renders
// it as a structured card (same pattern as HTTPActivityOutput's TraceJSON).
type TransformActivityOutput struct {
	// UpdatedVars contains only the keys written by the function chain
	// (i.e. the output_var of each step). The workflow merges these back.
	UpdatedVars map[string]string `json:"updated_vars"`
	// TraceJSON is a JSON-encoded transform.TraceResult emitted into the
	// node_done detail field when verbosity is debug or verbose.
	TraceJSON string `json:"trace_json"`
}

// execTransformNode runs a transform node inline in the workflow.
// Transform is pure in-memory string manipulation — no I/O, typically
// microseconds — so we execute it as a short-timeout side-effect activity
// rather than an inline workflow.Go call, preserving Temporal replay safety.
func execTransformNode(
	ctx workflow.Context,
	node *AppFlowNode,
	input AppFlowWorkflowInput,
	vars FlowVars,
	shortAO workflow.ActivityOptions,
) (FlowVars, error) {
	var cfg TransformNodeConfig
	if len(node.Config) > 0 {
		_ = json.Unmarshal(node.Config, &cfg)
	}

	if len(cfg.Functions) == 0 {
		traceNode(ctx, input.RunID, node.ID, "transform", "node_done", "no functions configured", input.LogVerbosity)
		return vars, nil
	}

	traceNode(ctx, input.RunID, node.ID, "transform", "node_start",
		fmt.Sprintf("steps=%d", len(cfg.Functions)), input.LogVerbosity)

	var out TransformActivityOutput
	actCtx := workflow.WithActivityOptions(ctx, shortAO)
	err := workflow.ExecuteActivity(actCtx, AppFlowTransformActivityName, TransformActivityInput{
		RunID:     input.RunID,
		NodeID:    node.ID,
		Functions: cfg.Functions,
		Vars:      vars,
		Verbosity: input.LogVerbosity,
	}).Get(actCtx, &out)
	if err != nil {
		traceNode(ctx, input.RunID, node.ID, "transform", "node_error", err.Error(), input.LogVerbosity)
		return vars, fmt.Errorf("transform %q: %w", node.ID, err)
	}

	for k, v := range out.UpdatedVars {
		vars[k] = v
	}
	traceNode(ctx, input.RunID, node.ID, "transform", "node_done", out.TraceJSON, input.LogVerbosity)
	return vars, nil
}

// TransformActivity is the Temporal activity implementation.
// It is registered on the dag-worker in cmd/dag-worker/main.go.
func (a *AppFlowActivities) TransformActivity(ctx context.Context, input TransformActivityInput) (TransformActivityOutput, error) {
	// Build a Vars map from the snapshot — transform.Execute mutates it in place.
	tvars := make(transform.Vars, len(input.Vars))
	for k, v := range input.Vars {
		tvars[k] = v
	}

	trace, err := transform.Execute(input.Functions, tvars)

	// Collect only the keys the chain wrote (output_var of each step).
	written := make(map[string]string, len(input.Functions))
	for _, step := range input.Functions {
		if v, ok := tvars[step.OutputVar]; ok {
			switch s := v.(type) {
			case string:
				written[step.OutputVar] = s
			default:
				if b, merr := json.Marshal(v); merr == nil {
					written[step.OutputVar] = string(b)
				}
			}
		}
	}

	// Always encode the trace so the debug inspector can render it.
	traceJSON := ""
	if trace != nil {
		if b, jerr := json.Marshal(trace); jerr == nil {
			traceJSON = string(b)
		}
	}

	if err != nil {
		// Return what was written before the error so partial results are visible.
		return TransformActivityOutput{UpdatedVars: written, TraceJSON: traceJSON},
			fmt.Errorf("transform node %q: %w", input.NodeID, err)
	}
	return TransformActivityOutput{UpdatedVars: written, TraceJSON: traceJSON}, nil
}
