package appflow

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// InlineLLMConfig is the config stored on an inline LLM node
// (definition_ref.kind="inline", name="llm").
//
// Field names intentionally match agentgen.LLMStepConfig so the app canvas and
// the agent builder expose one vocabulary for the same concept.
type InlineLLMConfig struct {
	// Provider is the LLM provider slug ("anthropic", "openai", "groq", "ollama",
	// "vllm", "lmstudio", "mock"). Empty inherits the entry point's orchestrator
	// provider (AppFlowWorkflowInput.LLMProviderName).
	//
	// This is the first per-node LLM selection in the AppFlow path: today every
	// node in a flow shares the one bound orchestrator's provider/model, because
	// LLMProviderName/LLMModel are resolved once from app_orchestrators at submit
	// time (ws/handler.go:502). An inline node can now pick a cheap model for a
	// classification step and a strong one for generation in the same flow.
	Provider string `json:"provider,omitempty"`
	// Model is the model identifier. Empty inherits the EP orchestrator model,
	// then falls back to the activity's default.
	Model string `json:"model,omitempty"`
	// SystemPrompt supports {{.varname}} Go-template interpolation over flow vars.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// UserPrompt supports {{.varname}} interpolation. When empty, falls back to
	// vars["input"] (the accumulated upstream output).
	UserPrompt string `json:"user_prompt,omitempty"`
	// MaxTokens caps the response. 0 → activity default (1024).
	MaxTokens int `json:"max_tokens,omitempty"`
	// Temperature is the sampling temperature. nil → provider default.
	Temperature *float64 `json:"temperature,omitempty"`
	// OutputVar names the flow variable receiving the response. Empty → "output".
	OutputVar string `json:"output_var,omitempty"`
}

// InlineConditionConfig is the config stored on an inline Condition node
// (definition_ref.kind="inline", name="condition").
//
// Evaluation is deterministic and happens inside the workflow — no activity,
// no LLM call. Matches agentgen.BranchStepConfig semantics.
type InlineConditionConfig struct {
	// Expression is a Go template rendered against flow vars. The result is
	// truthy unless it is "", "false", or "0". A reference to an unset variable
	// renders as "" (see isTruthy), so an expression over a missing variable
	// takes the false branch rather than erroring.
	Expression string `json:"expression"`
}

// FlowVars carries named values between nodes in one AppFlow execution.
// Always contains "input" = the current accumulated upstream output.
type FlowVars map[string]string

// flowFuncs are the extra template functions available in inline node prompts
// and condition expressions.
//
// Go's text/template has no string-predicate builtins, so a condition like
// {{contains .output "error"}} fails to parse without this map. Every function
// here must be pure (no I/O, no clock, no randomness) to keep renderFlowTemplate
// safe to call from Temporal workflow code.
var flowFuncs = template.FuncMap{
	"contains":  strings.Contains,
	"hasPrefix": strings.HasPrefix,
	"hasSuffix": strings.HasSuffix,
	"lower":     strings.ToLower,
	"upper":     strings.ToUpper,
	"trim":      strings.TrimSpace,
}

// renderFlowTemplate executes a Go text/template over flow vars.
// Safe for workflow code: no I/O, no clock, no randomness, and FlowVars is
// map[string]string accessed only by explicit key.
//
// Modeled on agentgen.renderTemplate (internal/agentgen/interpreter.go:623) and
// deliberately duplicated rather than imported, so appflow does not depend on
// agentgen. Unlike that function, this one registers flowFuncs so condition
// expressions can use string predicates.
func renderFlowTemplate(tmpl string, vars FlowVars) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	t, err := template.New("").Funcs(flowFuncs).Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// isTruthy reports whether a rendered expression counts as true.
// False for "", "false", "0", and "<no value>".
//
// On "<no value>": because FlowVars is map[string]string, text/template's
// missingkey=zero substitutes the zero value of the map's value type — the
// empty string — so a missing variable renders as "" and never as the literal
// "<no value>". That sentinel only appears for map[string]any / struct data.
// It is kept in the guard list defensively: both map to false either way, and
// the check costs nothing if FlowVars ever widens to map[string]any.
//
// Safe for workflow code: no I/O, no clock, no randomness — pure string
// comparison. Modeled on agentgen.isTruthy (internal/agentgen/interpreter.go:459),
// duplicated rather than imported so appflow does not depend on agentgen.
func isTruthy(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && s != "false" && s != "0" && s != "<no value>"
}
