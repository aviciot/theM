package agentgen

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

// execMCP is the Execute function for StepMCPCall.
func execMCP(ctx context.Context, interp *Interpreter, ic *InvocationContext, step *StepSpec, vars PipelineVars) error {
	if interp.mcpCaller == nil {
		return fmt.Errorf("mcp_call step %q: MCP service not configured (MCP_SERVICE_URL is unset)", step.ID)
	}

	var cfg MCPCallConfig
	if err := json.Unmarshal(step.Config, &cfg); err != nil {
		return fmt.Errorf("mcp_call step %q: invalid config: %w", step.ID, err)
	}
	if cfg.MCPServerSlug == "" || cfg.ToolName == "" {
		return fmt.Errorf("mcp_call step %q: mcp_server_slug and tool_name are required", step.ID)
	}

	// Render the args template against current pipeline vars.
	args, err := renderMCPArgs(cfg.ArgsTemplate, vars)
	if err != nil {
		return fmt.Errorf("mcp_call step %q: args_template render failed: %w", step.ID, err)
	}

	result, err := interp.mcpCaller.Call(ctx, ic.ApplicationID, cfg.MCPServerSlug, cfg.ToolName, args)
	if err != nil {
		return fmt.Errorf("mcp_call step %q (%s/%s): %w", step.ID, cfg.MCPServerSlug, cfg.ToolName, err)
	}

	if cfg.OutputVar != "" {
		// Store the raw JSON as a string so downstream template steps can reference it.
		vars[cfg.OutputVar] = string(result)
	}
	return nil
}

// execLoop is the Execute function for StepLoop (LocalExecutor path only).
//
// In the Temporal path, CanvasAgentWorkflow.runLoopNode handles loop iteration by
// scheduling each body step as its own ExecuteStepActivity. This function is only
// reached when ExecutionBackend == "local".
//
// It runs the compiled SubPlan body through a fresh LocalExecutor per iteration so
// that Parallel/Join and Branch patterns inside the body work correctly. Each
// iteration gets an isolated copy of vars with the current item injected; only
// declared body output keys are merged back and accumulated.
//
// cfg.Condition is an optional Go template; items that do not render to "true" are skipped.
// cfg.MaxIterations caps the number of iterations (default 100).
func execLoop(ctx context.Context, interp *Interpreter, ic *InvocationContext, step *StepSpec, vars PipelineVars, _ *ExecutionResult) error {
	var cfg LoopConfig
	if len(step.Config) > 0 {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return fmt.Errorf("loop step %q: invalid config: %w", step.ID, err)
		}
	}
	if cfg.ItemsVar == "" {
		return fmt.Errorf("loop step %q: items_var is required", step.ID)
	}
	if step.SubPlan == nil || len(step.SubPlan.Nodes) == 0 {
		return nil
	}

	itemVar := cfg.ItemVar
	if itemVar == "" {
		itemVar = "item"
	}
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 100
	}

	raw, ok := vars[cfg.ItemsVar]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("loop step %q: %q must be a list, got %T", step.ID, cfg.ItemsVar, raw)
	}

	// Collect declared output keys across ALL body nodes.
	bodyOutputKeys := make(map[string]bool)
	for _, n := range step.SubPlan.Nodes {
		for _, ref := range n.Outputs {
			bodyOutputKeys[ref.Name] = true
		}
	}
	// Only enforce declared outputs when there are items to iterate; an empty list
	// is a no-op and the body's Outputs don't matter.
	if len(items) > 0 && len(bodyOutputKeys) == 0 {
		return fmt.Errorf("loop step %q: body steps must declare at least one output (add Outputs to body nodes)", step.ID)
	}

	var accumulated []any

	for i, item := range items {
		if i >= maxIter {
			break
		}

		// Per-iteration isolated vars: include outer state so body steps can read
		// upstream variables, but changes are scoped to this iteration.
		iterVars := deepCopyVars(vars)
		iterVars[itemVar] = item

		// Apply optional condition filter.
		if cfg.Condition != "" {
			tmpl, err := template.New("loop_cond").Option("missingkey=zero").Parse(cfg.Condition)
			if err != nil {
				return fmt.Errorf("loop step %q: condition parse error: %w", step.ID, err)
			}
			var buf strings.Builder
			if err := tmpl.Execute(&buf, iterVars); err != nil {
				return fmt.Errorf("loop step %q: condition execute error: %w", step.ID, err)
			}
			if strings.TrimSpace(buf.String()) != "true" {
				continue
			}
		}

		// Run the body sub-plan through a fresh LocalExecutor so that Branch, Parallel,
		// and Join nodes inside the body work correctly. The body plan has no Response
		// node, so Execute returns ErrNoResult — we ignore that sentinel and read
		// outputs from iterVars after the executor has mutated them via the shared vars map.
		//
		// We pass iterVars as initial; the executor deep-copies it internally, but all
		// writes from executeStep flow back through the shared vars reference that the
		// executor carries. After Execute, iterVars still holds the pre-execution values,
		// so we reconstruct the post-execution state by running the body through a
		// wrapper that writes back to iterVars.
		//
		// Implementation: re-use execLoopBody which runs the sub-plan inline with a
		// body-scoped LocalExecutor and returns the post-execution vars.
		postVars, err := execLoopBody(ctx, interp, ic, step.SubPlan, iterVars)
		if err != nil {
			return fmt.Errorf("loop step %q iteration %d: %w", step.ID, i, err)
		}

		// Merge only declared body output keys back into outer vars (last iteration wins).
		for k := range bodyOutputKeys {
			if v, exists := postVars[k]; exists {
				vars[k] = v
			}
		}

		// Accumulate declared body outputs for this iteration.
		if cfg.AccumVar != "" {
			snapshot := make(PipelineVars, len(bodyOutputKeys))
			for k := range bodyOutputKeys {
				if v, exists := postVars[k]; exists {
					snapshot[k] = v
				}
			}
			accumulated = append(accumulated, snapshot)
		}
	}

	if cfg.AccumVar != "" {
		vars[cfg.AccumVar] = accumulated
	}
	return nil
}

// execLoopBody runs a body sub-plan and returns the post-execution PipelineVars.
// Body plans have no Response node, so we use LocalExecutor.ExecuteBody which
// captures terminal-branch vars instead of requiring a Result.
func execLoopBody(ctx context.Context, interp *Interpreter, ic *InvocationContext, plan *ExecutionPlan, initial PipelineVars) (PipelineVars, error) {
	exec := NewLocalExecutor(interp.clone())
	return exec.ExecuteBody(ctx, ic, plan, initial)
}

// renderMCPArgs renders the args_template (a JSON object Go template) against
// pipeline vars and parses it into a map. Empty template → empty args map.
func renderMCPArgs(tmplStr string, vars PipelineVars) (map[string]any, error) {
	if strings.TrimSpace(tmplStr) == "" {
		return map[string]any{}, nil
	}
	tmpl, err := template.New("mcp_args").Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		return nil, fmt.Errorf("args must be a JSON object, got: %w", err)
	}
	return out, nil
}
