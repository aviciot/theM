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
