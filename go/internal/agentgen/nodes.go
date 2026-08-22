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
		OutputArity: "single",
		IsSource:    true,
		IsSink:      false,
		Validate:    nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execInput(step, vars)
		},
	})

	RegisterNode(NodeDef{
		Type:        StepLLM,
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		Validate: func(step canvasStep, knownSlots map[string]bool) []CompileError {
			if len(step.Config) == 0 {
				return nil
			}
			var cfg LLMStepConfig
			if err := json.Unmarshal(step.Config, &cfg); err != nil {
				return nil
			}
			if cfg.ProviderKeySlot != "" && !knownSlots[cfg.ProviderKeySlot] {
				return []CompileError{{
					Code:    "UNDECLARED_SLOT",
					Message: fmt.Sprintf("LLM step references undeclared provider_key_slot %q", cfg.ProviderKeySlot),
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
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		Validate: func(step canvasStep, knownSlots map[string]bool) []CompileError {
			if len(step.Config) == 0 {
				return nil
			}
			var cfg HTTPStepConfig
			if err := json.Unmarshal(step.Config, &cfg); err != nil {
				return nil
			}
			if cfg.CredentialSlot != "" && !knownSlots[cfg.CredentialSlot] {
				return []CompileError{{
					Code:    "UNDECLARED_SLOT",
					Message: fmt.Sprintf("HTTP step references undeclared credential slot %q", cfg.CredentialSlot),
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
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		Validate:    nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execTransform(step, vars)
		},
	})

	RegisterNode(NodeDef{
		Type:        StepResponse,
		OutputArity: "none",
		IsSource:    false,
		IsSink:      true,
		Validate:    nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execResponse(step, vars, result)
		},
	})

	// ── Stub types (Execute: nil — not yet implemented) ──────────────────────

	RegisterNode(NodeDef{
		Type:        StepBranch,
		OutputArity: "multi",
		IsSource:    false,
		IsSink:      false,
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepLoop,
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepParallel,
		OutputArity: "multi",
		IsSource:    false,
		IsSink:      false,
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepA2ACall,
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepHumanWait,
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		Validate:    nil,
		Execute:     nil,
	})

	RegisterNode(NodeDef{
		Type:        StepStreamOut,
		OutputArity: "none",
		IsSource:    false,
		IsSink:      true,
		Validate:    nil,
		Execute:     nil,
	})
}
