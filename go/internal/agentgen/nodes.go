package agentgen

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// stdNonRetryable lists the Temporal error types that must never be retried,
// regardless of MaxAttempts. Set on every node; not user-overridable.
var stdNonRetryable = []string{"ContractViolation", "InvalidConfig", "PermissionDenied"}

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
		Type:                 StepInput,
		Version:              1,
		Label:                "Input",
		Description:          "Pipeline entry point. Receives the user message and exposes it as a variable for downstream steps.",
		Emoji:                "📥",
		OutputArity:          "single",
		IsSource:             true,
		IsSink:               false,
		SingleInput:          false,
		AcceptsDynamicInputs: false,
		DynamicOutputs:       false,
		Color:                "rgba(74,222,128,0.3)",
		BgColor:              "rgba(74,222,128,0.05)",
		Edges:                EdgeRules{MinIn: 0, MaxIn: 0, MinOut: 1, MaxOut: 0},
		ConfigFields: []ConfigFieldDoc{
			{Key: "bindings", Type: "object", Required: false, Description: "Map of port name to pipeline variable name. Use {\"text\": \"my_var\"} to rename the default input variable.", Example: "{\"text\": \"user_query\"}"},
		},
		UsageNotes: "Every skill must start with exactly one Input node. Its output variable (default: \"input\") flows to all downstream steps. You can rename it via bindings if clarity demands it.",
		Examples: []NodeExample{
			{Description: "Default input — exposes user message as 'input'", Config: map[string]any{"bindings": nil}},
			{Description: "Rename input to 'user_query'", Config: map[string]any{"bindings": map[string]any{"text": "user_query"}}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepBranch, StepMCPCall, StepA2ACall, StepResponse},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
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
		Type:                 StepLLM,
		Version:              1,
		Label:                "LLM",
		Description:          "Call a language model with a prompt. Stores the response in a named variable for use by later steps.",
		Emoji:                "🧠",
		OutputArity:          "single",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          true,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(208,188,255,0.6)",
		BgColor:              "rgba(87,27,193,0.1)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 0},
		InputField:  "user_prompt",
		// Static port: the prompt input. Var name is instance-specific (user_prompt template vars);
		// port ID "input" is the canonical handle used in canvas bindings.
		InputPorts:  []PortDef{{ID: "input", Label: "Prompt input", TypeHint: "text"}},
		// Static port: LLM response. Var name is instance-specific (output_var config field).
		OutputPorts: []PortDef{{ID: "output", Label: "LLM response", Required: true, TypeHint: "text"}},
		ConfigFields: []ConfigFieldDoc{
			{Key: "system_prompt", Type: "string", Required: false, Description: "System/persona prompt injected before the user message. Supports {{.varname}} template interpolation.", Example: "You are a helpful assistant. Context: {{.context}}"},
			{Key: "user_prompt", Type: "string", Required: false, Description: "The user-turn prompt. Supports {{.varname}} template interpolation. When empty, falls back to vars[\"input\"].", Example: "Summarize this: {{.input}}"},
			{Key: "output_var", Type: "string", Required: false, Description: "Pipeline variable name to store the LLM response text. Defaults to \"output\".", Example: "summary"},
			{Key: "model", Type: "string", Required: false, Description: "LLM model to use. Overrides the skill's default_model when set.", Example: "claude-sonnet-4-6"},
			{Key: "max_tokens", Type: "int", Required: false, Description: "Maximum number of tokens in the LLM response.", Example: "1024"},
		},
		UsageNotes: "Use an LLM node whenever you need natural language generation or reasoning. Always set output_var to a meaningful name so downstream steps can reference it unambiguously. Use {{.varname}} in system_prompt and user_prompt to inject pipeline variables. Chain multiple LLM nodes for multi-step reasoning (classify → generate → validate).",
		Examples: []NodeExample{
			{Description: "Summarise the user input", Config: map[string]any{"system_prompt": "You are a concise summarizer.", "user_prompt": "Summarise this text: {{.input}}", "output_var": "summary"}},
			{Description: "Classify sentiment", Config: map[string]any{"user_prompt": "Classify the sentiment of: {{.input}}. Reply with POSITIVE, NEGATIVE, or NEUTRAL only.", "output_var": "sentiment"}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepBranch, StepMCPCall, StepA2ACall, StepResponse, StepStreamOut},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 2, TimeoutSeconds: 300, InitialIntervalSeconds: 2.0, BackoffCoefficient: 2.0, MaxIntervalSeconds: 30, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 600},
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
		Type:                 StepHTTP,
		Version:              1,
		Label:                "HTTP",
		Description:          "Make an HTTP request to an external API. Supports GET/POST with template variables and optional app-level auth credential.",
		Emoji:                "🌐",
		OutputArity:          "single",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          false,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(20,184,166,0.5)",
		BgColor:              "rgba(20,184,166,0.07)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 0},
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
		ConfigFields: []ConfigFieldDoc{
			{Key: "url_template", Type: "string", Required: true, Description: "URL to call. Supports {{.varname}} template interpolation for dynamic path/query params.", Example: "https://api.example.com/search?q={{.query}}"},
			{Key: "method", Type: "string", Required: false, Description: "HTTP method: GET (default), POST, PUT, PATCH, DELETE.", Example: "POST"},
			{Key: "body_template", Type: "string", Required: false, Description: "Request body template (JSON string with {{.varname}} interpolation). Only used for POST/PUT/PATCH.", Example: "{\"text\": \"{{.input}}\"}"},
			{Key: "headers", Type: "object", Required: false, Description: "Static request headers as key-value pairs.", Example: "{\"Content-Type\": \"application/json\"}"},
			{Key: "timeout_seconds", Type: "int", Required: false, Description: "Request timeout in seconds. Defaults to 30.", Example: "10"},
			{Key: "extractions", Type: "array", Required: false, Description: "JSON path extractions from the response body. Each item has {\"path\": \"$.field\", \"var\": \"my_var\"}.", Example: "[{\"path\": \"$.result\", \"var\": \"api_result\"}]"},
			{Key: "inject_mode", Type: "string", Required: false, Description: "How to inject app_param credential: \"bearer\" (Authorization header), \"query\" (URL param), \"custom_header\" (X-API-Key header).", Example: "bearer"},
			{Key: "auth_param_key", Type: "string", Required: false, Description: "Which app_param key to use for auth injection: \"bearer_token\" or \"api_key\".", Example: "bearer_token"},
		},
		UsageNotes: "Use an HTTP node to call external REST APIs. The raw response body is stored in vars[\"http_response\"]. Use extractions to pull specific fields into named variables for downstream steps. For authenticated APIs, configure bearer_token or api_key as app_params — never hardcode secrets in config.",
		Examples: []NodeExample{
			{Description: "GET request with query param", Config: map[string]any{"url_template": "https://api.example.com/items?q={{.input}}", "method": "GET", "extractions": []map[string]any{{"path": "$.items[0].name", "var": "item_name"}}}},
			{Description: "POST with JSON body", Config: map[string]any{"url_template": "https://api.example.com/process", "method": "POST", "body_template": "{\"text\": \"{{.input}}\"}", "headers": map[string]any{"Content-Type": "application/json"}}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepBranch, StepMCPCall, StepA2ACall, StepResponse},
		// HTTP default is conservative (MaxAttempts=1) because the method (GET vs POST) is
		// only known at compile time. resolvePolicy upgrades GET to MaxAttempts=3.
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, InitialIntervalSeconds: 1.0, BackoffCoefficient: 2.0, MaxIntervalSeconds: 15, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 300},
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
		Type:                 StepTransform,
		Version:              1,
		Label:                "Transform",
		Description:          "Evaluate expressions to extract, reshape, or compute new variables from existing pipeline data.",
		Emoji:                "⚙️",
		OutputArity:          "multi",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          true,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       true,
		// DynamicOutputSource tells the frontend which config path drives output port names.
		// The frontend resolves "functions[].output_var" generically without per-type conditionals.
		DynamicOutputSource: "functions[].output_var",
		Color:                "rgba(99,102,241,0.5)",
		BgColor:              "rgba(99,102,241,0.1)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 0},
		InputField:  "expression",
		ConfigFields: []ConfigFieldDoc{
			{Key: "functions", Type: "array", Required: true, Description: "List of transform operations. Each item: {\"input_var\": \"source_var\", \"function\": \"fn_name\", \"args\": {...}, \"output_var\": \"dest_var\"}.", Example: "[{\"input_var\": \"http_response\", \"function\": \"json_extract\", \"args\": {\"path\": \"$.name\"}, \"output_var\": \"extracted_name\"}]"},
		},
		UsageNotes: "Use Transform nodes to reshape data between steps without an LLM call — extract JSON fields, split strings, trim whitespace, or format numbers. Available functions include: json_extract, split, join, trim, upper, lower, replace, slice, length. Chain Transform → LLM to pre-process data before sending it to the model.",
		Examples: []NodeExample{
			{Description: "Extract a field from JSON response", Config: map[string]any{"functions": []map[string]any{{"input_var": "http_response", "function": "json_extract", "args": map[string]any{"path": "$.data.name"}, "output_var": "company_name"}}}},
			{Description: "Trim and uppercase the input", Config: map[string]any{"functions": []map[string]any{{"input_var": "input", "function": "trim", "args": map[string]any{}, "output_var": "clean_input"}, {"input_var": "clean_input", "function": "upper", "args": map[string]any{}, "output_var": "upper_input"}}}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepBranch, StepMCPCall, StepA2ACall, StepResponse},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
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
		Type:                 StepResponse,
		Version:              1,
		Label:                "Response",
		Description:          "Pipeline sink. Sends a variable's value back to the caller as the final agent response.",
		Emoji:                "📤",
		OutputArity:          "none",
		IsSource:             false,
		IsSink:               true,
		SingleInput:          true,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(0,240,255,0.4)",
		BgColor:              "rgba(0,240,255,0.05)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 0},
		InputField:  "from_var",
		// Static port: the value to send back. Var name is instance-specific (from_var config field).
		InputPorts: []PortDef{{ID: "from_var", Label: "Response value", Required: true, TypeHint: "any"}},
		ConfigFields: []ConfigFieldDoc{
			{Key: "from_var", Type: "string", Required: true, Description: "Pipeline variable name to send as the response. Defaults to \"output\" when empty.", Example: "summary"},
		},
		UsageNotes: "Every skill must end with exactly one Response (or StreamOut) node. Set from_var to the variable that holds the final answer. The value is sent as-is — format it using a Transform or LLM step before wiring to Response if you need a specific output shape.",
		Examples: []NodeExample{
			{Description: "Return the LLM response", Config: map[string]any{"from_var": "output"}},
			{Description: "Return a named variable", Config: map[string]any{"from_var": "summary"}},
		},
		AllowedSuccessors: []StepType{},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
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
		Type:                 StepBranch,
		Version:              1,
		Label:                "Branch",
		Description:          "Route to one of two paths based on a Go template expression that evaluates to true or false.",
		Emoji:                "🔀",
		OutputArity:          "multi",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          true,
		AcceptsDynamicInputs: false,
		DynamicOutputs:       false,
		Color:                "rgba(249,115,22,0.5)",
		BgColor:              "rgba(249,115,22,0.07)",
		// Two outgoing edges: first = true path, second = false path.
		// TrueNext/FalseNext in config override edge order when set.
		Edges: EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 2, MaxOut: 2},
		// Named control-flow output ports — drives handle rendering generically in the frontend.
		// Handle IDs: "ctrl-out-true" and "ctrl-out-false".
		ControlOutputPorts: []PortDef{
			{ID: "true",  Label: "True path",  Color: "#4ade80", MaxConnections: 1},
			{ID: "false", Label: "False path", Color: "#f87171", MaxConnections: 1},
		},
		ConfigFields: []ConfigFieldDoc{
			{Key: "expression", Type: "string", Required: true, Description: "Go template expression that evaluates to true or false. Supports {{.varname}} interpolation and comparison operators.", Example: "{{eq .sentiment \"POSITIVE\"}}"},
			{Key: "true_next", Type: "string", Required: false, Description: "Step ID to route to when expression is true. Overrides the first outgoing edge.", Example: "positive_handler"},
			{Key: "false_next", Type: "string", Required: false, Description: "Step ID to route to when expression is false. Overrides the second outgoing edge.", Example: "fallback_handler"},
		},
		UsageNotes: "Use Branch for binary routing based on a pipeline variable. The expression is a Go template — use {{eq .var \"value\"}} for equality, {{gt .count 5}} for comparisons. true_next and false_next are optional; without them the first and second canvas edges are used for true and false paths respectively.",
		Examples: []NodeExample{
			{Description: "Route on sentiment classification", Config: map[string]any{"expression": "{{eq .sentiment \"POSITIVE\"}}", "true_next": "positive_step", "false_next": "negative_step"}},
			{Description: "Check if response is non-empty", Config: map[string]any{"expression": "{{gt (len .output) 0}}"}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepBranch, StepMCPCall, StepA2ACall, StepResponse},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
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
		Type:                 StepLoop,
		Version:              1,
		Label:                "Loop",
		Description:          "Iterate over a list, running the connected body pipeline once per item.",
		Emoji:                "🔁",
		OutputArity:          "single",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          false,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(245,158,11,0.3)",
		BgColor:              "rgba(245,158,11,0.05)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 1},
		ConfigFields: []ConfigFieldDoc{
			{Key: "items_var", Type: "string", Required: true, Description: "Pipeline variable name that holds the list to iterate over.", Example: "results"},
			{Key: "item_var", Type: "string", Required: false, Description: "Variable name for the current loop item within each iteration. Defaults to \"item\".", Example: "item"},
			{Key: "condition", Type: "string", Required: false, Description: "Optional Go template expression to filter items. Only items where this evaluates to true are processed.", Example: "{{gt .item.score 0.5}}"},
			{Key: "accum_var", Type: "string", Required: false, Description: "Variable name to accumulate loop outputs into a list.", Example: "processed_items"},
			{Key: "max_iterations", Type: "int", Required: false, Description: "Hard cap on iterations. Defaults to 100.", Example: "50"},
		},
		UsageNotes: "Iterates over items_var (must be []any). Each iteration runs the body steps with item_var injected. Results are accumulated into accum_var. Body steps are compiled into a sub-plan; they do not appear in the outer DAG.",
		Examples: []NodeExample{
			{Description: "Iterate over a list of results", Config: map[string]any{"items_var": "results", "item_var": "item", "accum_var": "processed", "max_iterations": 100}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepResponse},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
		Validate: func(step canvasStep) []Issue {
			var c LoopConfig
			if len(step.Config) > 0 {
				_ = json.Unmarshal(step.Config, &c)
			}
			var issues []Issue
			if c.ItemsVar == "" {
				issues = append(issues, Issue{Severity: "error", Code: "INVALID_CONFIG", NodeID: step.ID, Field: "items_var", Message: "items_var is required"})
			}
			if len(c.BodySteps) == 0 {
				issues = append(issues, Issue{Severity: "error", Code: "INVALID_CONFIG", NodeID: step.ID, Field: "body_steps", Message: "loop body is empty — connect steps from the loop-body output port"})
			}
			return issues
		},
		Execute: execLoop,
		DeriveInputs: func(cfg json.RawMessage) []VarRef {
			var c LoopConfig
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &c)
			}
			refs := []VarRef{}
			if c.ItemsVar != "" {
				refs = append(refs, VarRef{Name: c.ItemsVar, Required: true})
			}
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
		Type:                 StepParallel,
		Version:              1,
		Label:                "Parallel",
		Description:          "Fan out to multiple branches simultaneously and wait for all to complete before continuing.",
		Emoji:                "⚡",
		OutputArity:          "multi",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          false,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(208,188,255,0.6)",
		BgColor:              "rgba(87,27,193,0.1)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 2, MaxOut: 0},
		ConfigFields: []ConfigFieldDoc{
			{Key: "merge_var", Type: "string", Required: false, Description: "Variable name to collect all branch outputs into a list once all branches complete.", Example: "branch_results"},
		},
		UsageNotes: "Fans out to N downstream branches simultaneously and waits for all to finish before the next node runs. The downstream convergence node receives merged vars from all branches.",
		Examples: []NodeExample{
			{Description: "Fan out and collect results", Config: map[string]any{"merge_var": "all_results"}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepResponse},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
		Validate:    nil,
		Execute: func(_ context.Context, _ *Interpreter, _ *InvocationContext, _ *StepSpec, _ PipelineVars, _ *ExecutionResult) error {
			// StepParallel is a pure fan-out coordinator. LocalExecutor fans out
			// to all node.Next entries automatically when len(Next) > 1 — no work
			// is needed here. The downstream join node (JoinWaitAll) merges vars
			// once all branches complete.
			return nil
		},
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
		Type:                 StepA2ACall,
		Version:              1,
		Label:                "A2A Call",
		Description:          "Invoke an external A2A agent as a step in this pipeline. Passes variables and receives structured output. (stub)",
		Emoji:                "🤝",
		OutputArity:          "single",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          false,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(0,240,255,0.4)",
		BgColor:              "rgba(0,240,255,0.05)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 0},
		ConfigFields: []ConfigFieldDoc{
			{Key: "agent_url", Type: "string", Required: true, Description: "Base URL of the A2A agent to call (e.g. http://my-agent:9200).", Example: "http://vision-agent:9100"},
			{Key: "skill_id", Type: "string", Required: false, Description: "Specific skill ID to invoke on the target agent. Omit to use the agent's default skill.", Example: "analyze_image"},
			{Key: "input_var", Type: "string", Required: true, Description: "Pipeline variable name to send as the A2A task input.", Example: "user_query"},
			{Key: "output_var", Type: "string", Required: false, Description: "Pipeline variable name to store the A2A response. Defaults to \"a2a_response\".", Example: "agent_result"},
		},
		UsageNotes: "A2A Call is not yet fully implemented (stub). Use it to delegate a subtask to another A2A-compliant agent registered in the platform. For immediate use, prefer MCP Tool or HTTP nodes to call external services.",
		Examples: []NodeExample{
			{Description: "Call vision agent", Config: map[string]any{"agent_url": "http://vision-agent:9100", "skill_id": "describe_image", "input_var": "image_url", "output_var": "description"}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepBranch, StepMCPCall, StepResponse},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 2, TimeoutSeconds: 600},
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
		Type:                 StepHumanWait,
		Version:              1,
		Label:                "Human Wait",
		Description:          "Pause execution and wait for a human-in-the-loop approval or input before continuing. (stub)",
		Emoji:                "⏸️",
		OutputArity:          "single",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          false,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(74,222,128,0.3)",
		BgColor:              "rgba(74,222,128,0.05)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 0},
		ConfigFields: []ConfigFieldDoc{
			{Key: "prompt_text", Type: "string", Required: false, Description: "Message shown to the human operator while waiting for input.", Example: "Please review the draft and approve or reject it."},
			{Key: "reply_var", Type: "string", Required: false, Description: "Variable name to store the human's reply.", Example: "human_reply"},
			{Key: "timeout_seconds", Type: "int", Required: false, Description: "How long to wait before timing out. 0 means wait indefinitely.", Example: "300"},
		},
		UsageNotes: "Human Wait is not yet fully implemented (stub). It pauses the Temporal workflow and signals resumption via HITL. Only use in designs that explicitly require human-in-the-loop approval flow.",
		Examples: []NodeExample{
			{Description: "Wait for approval", Config: map[string]any{"prompt_text": "Approve this action?", "reply_var": "approval", "timeout_seconds": 3600}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepBranch, StepResponse},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
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
		Type:                 StepStreamOut,
		Version:              1,
		Label:                "Stream Out",
		Description:          "Pipeline sink. Streams incremental output tokens back to the caller as they are generated. (stub)",
		Emoji:                "📡",
		OutputArity:          "none",
		IsSource:             false,
		IsSink:               true,
		SingleInput:          false,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(0,240,255,0.4)",
		BgColor:              "rgba(0,240,255,0.05)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 0, MaxOut: 0},
		ConfigFields: []ConfigFieldDoc{
			{Key: "from_var", Type: "string", Required: true, Description: "Pipeline variable name to stream as output. The variable must hold the text to be streamed.", Example: "llm_output"},
		},
		UsageNotes: "StreamOut is not yet fully implemented (stub). When implemented, it will stream LLM tokens incrementally over SSE instead of returning a complete response. For now, use Response as the pipeline sink.",
		Examples: []NodeExample{
			{Description: "Stream the LLM output", Config: map[string]any{"from_var": "output"}},
		},
		AllowedSuccessors: []StepType{},
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300},
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
		Type:                 StepMCPCall,
		Version:              1,
		Label:                "MCP Tool",
		Description:          "Call a tool on an MCP server registered in the admin UI. Credential is resolved per-application.",
		Emoji:                "🔌",
		OutputArity:          "single",
		IsSource:             false,
		IsSink:               false,
		SingleInput:          true,
		AcceptsDynamicInputs: true,
		DynamicOutputs:       false,
		Color:                "rgba(99,102,241,0.5)",
		BgColor:              "rgba(99,102,241,0.1)",
		Edges:                EdgeRules{MinIn: 1, MaxIn: 1, MinOut: 1, MaxOut: 0},
		ConfigFields: []ConfigFieldDoc{
			{Key: "mcp_server_slug", Type: "string", Required: true, Description: "Slug of the MCP server as configured in Admin → MCP Servers.", Example: "github-mcp"},
			{Key: "tool_name", Type: "string", Required: true, Description: "Name of the tool to invoke on the MCP server.", Example: "list_issues"},
			{Key: "args_template", Type: "string", Required: false, Description: "JSON object Go template for the tool arguments. Supports {{.varname}} interpolation.", Example: "{\"repo\": \"{{.repo_name}}\", \"state\": \"open\"}"},
			{Key: "output_var", Type: "string", Required: true, Description: "Pipeline variable name to store the tool result JSON string.", Example: "issues_json"},
		},
		UsageNotes: "MCP Tool calls a registered MCP server's tool. The mcp_server_slug must match a server registered in Admin → MCP Servers. Credentials are resolved per-application — the application must have the MCP server bound to it. The result is stored as a raw JSON string; use a Transform step to extract specific fields.",
		Examples: []NodeExample{
			{Description: "List open GitHub issues", Config: map[string]any{"mcp_server_slug": "github-mcp", "tool_name": "list_issues", "args_template": "{\"repo\": \"{{.repo_name}}\", \"state\": \"open\"}", "output_var": "issues_json"}},
			{Description: "Search Slack messages", Config: map[string]any{"mcp_server_slug": "slack-mcp", "tool_name": "search_messages", "args_template": "{\"query\": \"{{.input}}\"}", "output_var": "slack_results"}},
		},
		AllowedSuccessors: []StepType{StepLLM, StepHTTP, StepTransform, StepBranch, StepMCPCall, StepResponse},
		// MCP default is conservative (MaxAttempts=1) because mutating vs read-only is
		// determined by tool_name at compile time. resolvePolicy upgrades read-only tools to 2.
		DefaultPolicy: ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, InitialIntervalSeconds: 1.0, BackoffCoefficient: 2.0, MaxIntervalSeconds: 15, NonRetryableErrors: stdNonRetryable},
		MaxPolicy:     ExecutionPolicy{MaxAttempts: 3, TimeoutSeconds: 300},
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

// execLoop is the Execute function for StepLoop (LocalExecutor path only).
//
// In the Temporal path, CanvasAgentWorkflow.runLoopNode handles loop iteration by
// scheduling each body step as its own ExecuteStepActivity. This function is only
// reached when ExecutionBackend == "local".
//
// It iterates over vars[cfg.ItemsVar] (must be []any), injects cfg.ItemVar per
// element, runs the compiled SubPlan body steps sequentially once per item using
// ExecNodeWithPolicy (so each body step gets its own retry/timeout/backoff), and
// accumulates only declared body Outputs keys into cfg.AccumVar ([]any).
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
		// No body configured — no-op.
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
		// items_var absent — treat as empty list.
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("loop step %q: %q must be a list, got %T", step.ID, cfg.ItemsVar, raw)
	}

	// Build a step index and collect declared output keys from the body sub-plan.
	bodyIdx := make(map[string]*PlanNode, len(step.SubPlan.Nodes))
	bodyOutputKeys := make(map[string]bool)
	for _, n := range step.SubPlan.Nodes {
		bodyIdx[n.StepID] = n
		for _, ref := range n.Outputs {
			bodyOutputKeys[ref.Name] = true
		}
	}

	// Sequential body semaphore: one slot — body steps run one at a time.
	bodySem := make(chan struct{}, 1)

	var accumulated []any

	for i, item := range items {
		if i >= maxIter {
			break
		}

		// Build per-iteration vars: start from global state, inject item.
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

		// Run each body step sequentially via ExecNodeWithPolicy so that each body
		// node gets its own retry/timeout/backoff from node.Policy.
		currentID := step.SubPlan.StartID
		for currentID != "" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			node, found := bodyIdx[currentID]
			if !found {
				return fmt.Errorf("loop step %q iteration %d: body step %q not found", step.ID, i, currentID)
			}
			bodySpec := planNodeToStepSpec(node)
			override, err := ExecNodeWithPolicy(ctx, ic, interp, bodySpec, node.Policy, iterVars, nil, bodySem)
			if err != nil {
				return fmt.Errorf("loop step %q iteration %d body step %q: %w", step.ID, i, currentID, err)
			}
			if override != "" {
				currentID = override
			} else if len(node.Next) > 0 {
				currentID = node.Next[0]
			} else {
				currentID = ""
			}
		}

		// Merge iteration outputs back into global vars (last iteration wins).
		for k, v := range iterVars {
			vars[k] = v
		}

		// Accumulate only declared body output keys for this iteration.
		// Falls back to all keys written during this iteration when no body node
		// declares Outputs (legacy/untyped body steps).
		if cfg.AccumVar != "" {
			snapshot := make(PipelineVars)
			if len(bodyOutputKeys) > 0 {
				for k := range bodyOutputKeys {
					if v, exists := iterVars[k]; exists {
						snapshot[k] = v
					}
				}
			} else {
				for k, v := range iterVars {
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
