package agentgen

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// tmplKeywords is the set of Go template control keywords that are not variable names.
var tmplKeywords = map[string]bool{
	"if": true, "else": true, "end": true, "range": true, "with": true,
	"define": true, "block": true, "template": true, "call": true,
	"and": true, "or": true, "not": true, "html": true, "js": true,
	"urlquery": true, "print": true, "println": true, "printf": true,
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
	"len": true, "index": true, "slice": true,
}

// extractTmplVars extracts unique variable references from a Go template string.
// It handles two patterns:
//   - {{.varname}} or {{varname}} — simple standalone variable reference
//   - .varname inside any {{ ... }} block — e.g. {{if gt .score 5}}
//
// Go template control keywords (if, else, end, range, etc.) are excluded.
func extractTmplVars(tmpl string) []string {
	// Two regexes:
	// 1. standalone: {{.varname}} or {{varname}}
	reStandalone := regexp.MustCompile(`\{\{\.?(\w+)\}\}`)
	// 2. dot-prefixed inside any action: .varname within {{ ... }}
	reAction := regexp.MustCompile(`\{\{[^}]*\.(\w+)[^}]*\}\}`)

	seen := map[string]bool{}
	var out []string

	addVar := func(name string) {
		if tmplKeywords[name] {
			return
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}

	for _, m := range reStandalone.FindAllStringSubmatch(tmpl, -1) {
		addVar(m[1])
	}
	for _, m := range reAction.FindAllStringSubmatch(tmpl, -1) {
		addVar(m[1])
	}
	return out
}

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
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			// Input step reads from vars["input"] implicitly (how pipeline starts).
			return []VarRef{{Name: "input", Required: false}}
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c InputStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			bindVar := "input"
			if c.Bindings != nil {
				if v, ok := c.Bindings["text"]; ok && v != "" {
					bindVar = v
				}
			}
			return []VarRef{{Name: bindVar, Required: false}}
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
		// Static port: the prompt input. Var name is instance-specific (user_prompt template vars);
		// port ID "input" is the canonical handle used in canvas bindings.
		InputPorts:  []PortDef{{ID: "input", Label: "Prompt input", TypeHint: "text"}},
		// Static port: LLM response. Var name is instance-specific (output_var config field).
		OutputPorts: []PortDef{{ID: "output", Label: "LLM response", Required: true, TypeHint: "text"}},
		Validate:    nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execLLM(ctx, ic, step, vars)
		},
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c LLMStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			seen := map[string]bool{}
			var refs []VarRef
			// If user_prompt is empty the runtime falls back to vars["input"].
			if c.UserPrompt == "" {
				seen["input"] = true
				refs = append(refs, VarRef{Name: "input", Required: false})
			} else {
				// Extract {{.varname}} patterns from user_prompt and system_prompt.
				for _, v := range extractTmplVars(c.UserPrompt) {
					if !seen[v] {
						seen[v] = true
						refs = append(refs, VarRef{Name: v, Required: false})
					}
				}
			}
			for _, v := range extractTmplVars(c.SystemPrompt) {
				if !seen[v] {
					seen[v] = true
					refs = append(refs, VarRef{Name: v, Required: false})
				}
			}
			return refs
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c LLMStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			outVar := c.OutputVar
			if outVar == "" {
				outVar = "output"
			}
			// PortID "output" is the stable binding handle; Name is the instance var name.
			return []VarRef{{Name: outVar, Required: false, PortID: "output"}}
		},
	})

	RegisterNode(NodeDef{
		Type:        StepHTTP,
		Version:     1,
		Label:       "HTTP",
		Description: "Make an HTTP request to an external API. Supports GET/POST with template variables and optional app-level auth credential.",
		Emoji:       "🌐",
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		SingleInput: false,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		InputField:  "url_template",
		AppParams: []AppParamDecl{
			{
				Key:         "bearer_token",
				Label:       "Bearer Token",
				Description: "Injected as Authorization: Bearer <value>. Leave unset for no auth.",
				Type:        "secret",
				Required:    false,
			},
			{
				Key:         "api_key",
				Label:       "API Key",
				Description: "Generic API key. Use InjectMode on the step to control where it is injected (header/query/custom_header).",
				Type:        "secret",
				Required:    false,
			},
		},
		Validate: nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execHTTP(ctx, ic, step, vars)
		},
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c HTTPStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			seen := map[string]bool{}
			var refs []VarRef
			for _, v := range extractTmplVars(c.URLTemplate) {
				if !seen[v] {
					seen[v] = true
					refs = append(refs, VarRef{Name: v, Required: false})
				}
			}
			for _, v := range extractTmplVars(c.BodyTemplate) {
				if !seen[v] {
					seen[v] = true
					refs = append(refs, VarRef{Name: v, Required: false})
				}
			}
			return refs
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c HTTPStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			refs := []VarRef{{Name: "http_response", Required: false}}
			for _, ext := range c.Extractions {
				if ext.Var != "" {
					refs = append(refs, VarRef{Name: ext.Var, Required: false})
				}
			}
			return refs
		},
	})

	RegisterNode(NodeDef{
		Type:        StepTransform,
		Version:     1,
		Label:       "Transform",
		Description: "Evaluate expressions to extract, reshape, or compute new variables from existing pipeline data.",
		Emoji:       "⚙️",
		OutputArity: "multi",
		IsSource:    false,
		IsSink:      false,
		SingleInput: true,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 0},
		InputField:  "expression",
		Validate:    nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execTransform(step, vars)
		},
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c TransformStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			seen := map[string]bool{}
			var refs []VarRef
			for _, fn := range c.Functions {
				if fn.InputVar != "" && !seen[fn.InputVar] {
					seen[fn.InputVar] = true
					refs = append(refs, VarRef{Name: fn.InputVar, Required: false})
				}
			}
			return refs
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c TransformStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			seen := map[string]bool{}
			var refs []VarRef
			for _, fn := range c.Functions {
				if fn.OutputVar != "" && !seen[fn.OutputVar] {
					seen[fn.OutputVar] = true
					refs = append(refs, VarRef{Name: fn.OutputVar, Required: false})
				}
			}
			return refs
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
		// Static port: the value to send back. Var name is instance-specific (from_var config field).
		InputPorts: []PortDef{{ID: "from_var", Label: "Response value", Required: true, TypeHint: "any"}},
		Validate:   nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execResponse(step, vars, result)
		},
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c ResponseStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			fromVar := c.FromVar
			if fromVar == "" {
				fromVar = "output"
			}
			// PortID "from_var" is the stable binding handle; Name is the instance var name.
			return []VarRef{{Name: fromVar, Required: true, PortID: "from_var"}}
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			// Response is a sink — writes to ExecutionResult, not PipelineVars.
			return nil
		},
	})

	// ── Implemented branching types ──────────────────────────────────────────

	RegisterNode(NodeDef{
		Type:        StepBranch,
		Version:     1,
		Label:       "Branch",
		Description: "Route to one of two paths based on a Go template expression that evaluates to true or false.",
		Emoji:       "🔀",
		OutputArity: "multi",
		IsSource:    false,
		IsSink:      false,
		SingleInput: true,
		// Two outgoing edges: first = true path, second = false path.
		// TrueNext/FalseNext in config override edge order when set.
		Edges:    EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 2, MaxOut: 2},
		Validate: nil,
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return interp.execBranch(step, vars)
		},
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c BranchStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			var refs []VarRef
			for _, v := range extractTmplVars(c.Expression) {
				refs = append(refs, VarRef{Name: v, Required: false})
			}
			return refs
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			// Branch writes nothing to PipelineVars — only controls routing.
			return nil
		},
	})

	// ── Stub types (Execute: nil — not yet implemented) ──────────────────────

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
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c LoopConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			var refs []VarRef
			for _, v := range extractTmplVars(c.Condition) {
				refs = append(refs, VarRef{Name: v, Required: false})
			}
			return refs
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c LoopConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			if c.AccumVar != "" {
				return []VarRef{{Name: c.AccumVar, Required: false}}
			}
			return nil
		},
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
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			return nil
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c ParallelConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			if c.MergeVar != "" {
				return []VarRef{{Name: c.MergeVar, Required: false}}
			}
			return nil
		},
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
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c A2ACallStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			if c.InputVar != "" {
				return []VarRef{{Name: c.InputVar, Required: true}}
			}
			return nil
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c A2ACallStepConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			if c.OutputVar != "" {
				return []VarRef{{Name: c.OutputVar, Required: false}}
			}
			return nil
		},
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
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			return nil
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c HumanWaitConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			if c.ReplyVar != "" {
				return []VarRef{{Name: c.ReplyVar, Required: false}}
			}
			return nil
		},
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
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			return nil
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			return nil
		},
	})

	// ── MCP call ─────────────────────────────────────────────────────────────

	RegisterNode(NodeDef{
		Type:        StepMCPCall,
		Version:     1,
		Label:       "MCP Tool",
		Description: "Call a tool on an MCP server registered in the admin UI. Credential is resolved per-application.",
		Emoji:       "🔌",
		OutputArity: "single",
		IsSource:    false,
		IsSink:      false,
		SingleInput: true,
		Edges:       EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		Validate: func(step canvasStep) []Issue {
			var c MCPCallConfig
			if len(step.Config) > 0 {
				if err := json.Unmarshal(step.Config, &c); err != nil {
					return []Issue{{Code: "INVALID_CONFIG", Severity: "error", Message: "invalid mcp_call config: " + err.Error()}}
				}
			}
			var issues []Issue
			if c.MCPServerSlug == "" {
				issues = append(issues, Issue{Code: "INVALID_CONFIG", Severity: "error", Message: "mcp_server_slug is required"})
			}
			if c.ToolName == "" {
				issues = append(issues, Issue{Code: "INVALID_CONFIG", Severity: "error", Message: "tool_name is required"})
			}
			if c.OutputVar == "" {
				issues = append(issues, Issue{Code: "INVALID_CONFIG", Severity: "error", Message: "output_var is required"})
			}
			return issues
		},
		Execute: func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
			step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
			return execMCP(ctx, interp, ic, step, vars)
		},
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c MCPCallConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			// Extract template variable references from the args template.
			return varRefsFromTemplate(extractTmplVars(c.ArgsTemplate))
		},
		DeriveOutputs: func(cfg json.RawMessage) []VarRef {
			var c MCPCallConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			if c.OutputVar != "" {
				return []VarRef{{Name: c.OutputVar, Required: false}}
			}
			return nil
		},
	})

}

// varRefsFromTemplate converts a list of variable names into VarRef values.
func varRefsFromTemplate(names []string) []VarRef {
	if len(names) == 0 {
		return nil
	}
	refs := make([]VarRef, len(names))
	for i, n := range names {
		refs[i] = VarRef{Name: n, Required: false}
	}
	return refs
}

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
