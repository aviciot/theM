package agentgen

import (
	"context"
	"encoding/json"
	"fmt"
)

func init() {
	// ── Implemented types ────────────────────────────────────────────────────

	RegisterNode(NodeDef{
		Type:        StepInput,
		Version:     1,
		Label:       "Input",
		Description: "Pipeline entry point. Receives the user message and exposes it as a variable for downstream steps.",
		Emoji:       "📥",
		OutputArity: "single",
		IsSource:    true,
		IsSink:      false,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 1, MaxOut: 0},
		Validate:    nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execInput(step, vars)
		},
	})

	RegisterNode(NodeDef{
		Type:        StepLLM,
		Version:     1,
		Label:       "LLM",
		Description: "Call a language model with a prompt. Stores the response in a named variable for use by later steps.",
		Emoji:       "🧠",
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		SingleInput: true,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		InputField:  "user_prompt",
		Validate: func(step canvasStep, knownSlots map[string]bool) []Issue {
			if len(step.Config) == 0 {
				return nil
			}
			var cfg LLMStepConfig
			if err := json.Unmarshal(step.Config, &cfg); err != nil {
				return nil
			}
			if cfg.ProviderKeySlot != "" && !knownSlots[cfg.ProviderKeySlot] {
				return []Issue{{
					Severity: "error",
					Code:     "UNDECLARED_SLOT",
					Message:  fmt.Sprintf("LLM step references undeclared provider_key_slot %q", cfg.ProviderKeySlot),
					Field:    "provider_key_slot",
				}}
			}
			return nil
		},
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execLLM(ctx, ic, step, vars)
		},
	})

	RegisterNode(NodeDef{
		Type:        StepHTTP,
		Version:     1,
		Label:       "HTTP",
		Description: "Make an HTTP request to an external API. Supports GET/POST with template variables and optional credential slot.",
		Emoji:       "🌐",
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		InputField:  "url_template",
		Validate: func(step canvasStep, knownSlots map[string]bool) []Issue {
			if len(step.Config) == 0 {
				return nil
			}
			var cfg HTTPStepConfig
			if err := json.Unmarshal(step.Config, &cfg); err != nil {
				return nil
			}
			if cfg.CredentialSlot != "" && !knownSlots[cfg.CredentialSlot] {
				return []Issue{{
					Severity: "error",
					Code:     "UNDECLARED_SLOT",
					Message:  fmt.Sprintf("HTTP step references undeclared credential slot %q", cfg.CredentialSlot),
					Field:    "credential_slot",
				}}
			}
			return nil
		},
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execHTTP(ctx, ic, step, vars)
		},
	})

	RegisterNode(NodeDef{
		Type:        StepTransform,
		Version:     1,
		Label:       "Transform",
		Description: "Evaluate expressions to extract, reshape, or compute new variables from existing pipeline data.",
		Emoji:       "⚙️",
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		SingleInput: true,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		InputField:  "expression",
		Validate:    nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execTransform(step, vars)
		},
	})

	RegisterNode(NodeDef{
		Type:        StepResponse,
		Version:     1,
		Label:       "Response",
		Description: "Pipeline sink. Sends a variable's value back to the caller as the final agent response.",
		Emoji:       "📤",
		OutputArity: "none",
		IsSource:    false,
		IsSink:      true,
		SingleInput: true,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 0},
		InputField:  "from_var",
		Validate:    nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execResponse(step, vars, result)
		},
	})

	// ── Stub types (Execute: nil — not yet implemented) ──────────────────────

	RegisterNode(NodeDef{
		Type:        StepBranch,
		Version:     1,
		Label:       "Branch",
		Description: "Conditional routing. Evaluates expressions and directs flow to one of multiple output paths. (stub)",
		Emoji:       "🔀",
		OutputArity: "multi",
		IsSource:    false,
		IsSink:      false,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 2, MaxOut: 0},
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepLoop,
		Version:     1,
		Label:       "Loop",
		Description: "Iterate over a list, running the connected pipeline once per item. (stub)",
		Emoji:       "🔁",
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepParallel,
		Version:     1,
		Label:       "Parallel",
		Description: "Fan out to multiple branches simultaneously and wait for all to complete before continuing. (stub)",
		Emoji:       "⚡",
		OutputArity: "multi",
		IsSource:    false,
		IsSink:      false,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 2, MaxOut: 0},
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepA2ACall,
		Version:     1,
		Label:       "A2A Call",
		Description: "Invoke an external A2A agent as a step in this pipeline. Passes variables and receives structured output. (stub)",
		Emoji:       "🤝",
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepHumanWait,
		Version:     1,
		Label:       "Human Wait",
		Description: "Pause execution and wait for a human-in-the-loop approval or input before continuing. (stub)",
		Emoji:       "⏸️",
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepStreamOut,
		Version:     1,
		Label:       "Stream Out",
		Description: "Pipeline sink. Streams incremental output tokens back to the caller as they are generated. (stub)",
		Emoji:       "📡",
		OutputArity: "none",
		IsSource:    false,
		IsSink:      true,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 0},
		Validate:    nil,
		Execute:     nil,
	})
}
