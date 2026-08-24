package agentgen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/aviciot/them/internal/agentgen/transform"
)

// HTTPDoer is the interface for making outbound HTTP calls (injectable for tests).
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// LLMProvider is the interface for calling an LLM from a pipeline step.
type LLMProvider interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// LLMFactory creates a provider for a given model and apiKey.
type LLMFactory interface {
	NewProvider(provider, model string, maxTokens int, apiKey string) (LLMProvider, error)
}

// PipelineVars holds variables scoped to one pipeline execution.
type PipelineVars map[string]any

// Interpreter executes a SkillSpec pipeline against an InvocationContext.
type Interpreter struct {
	httpClient       HTTPDoer
	llmFactory       LLMFactory
	platformAPIKey   string // fallback LLM key when no app key is set
	nextStepOverride string // set by condition/branch steps; read and cleared by the Execute loop
}

// NewInterpreter creates an Interpreter.
func NewInterpreter(httpClient HTTPDoer, llmFactory LLMFactory, platformAPIKey string) *Interpreter {
	return &Interpreter{
		httpClient:     httpClient,
		llmFactory:     llmFactory,
		platformAPIKey: platformAPIKey,
	}
}

// ExecutionResult is the output of a successful pipeline execution.
type ExecutionResult struct {
	Text      string
	MediaType string
}

// Execute runs the pipeline for the given skill.
// extraVars are merged into the initial pipeline vars after "input" is set;
// they are used to expose structured data parts (application/json) as named vars.
func (interp *Interpreter) Execute(ctx context.Context, ic *InvocationContext, skill *SkillSpec, inputText string, extraVars ...map[string]any) (*ExecutionResult, error) {
	vars := PipelineVars{"input": inputText}
	for _, ev := range extraVars {
		for k, v := range ev {
			vars[k] = v
		}
	}
	result := &ExecutionResult{MediaType: "text/plain"}

	stepIdx := make(map[string]*StepSpec, len(skill.Steps))
	for i := range skill.Steps {
		stepIdx[skill.Steps[i].ID] = &skill.Steps[i]
	}

	var startID string
	for _, s := range skill.Steps {
		if s.Type == StepInput {
			startID = s.ID
			break
		}
	}
	if startID == "" && len(skill.Steps) > 0 {
		startID = skill.Steps[0].ID
	}
	if startID == "" {
		return nil, fmt.Errorf("skill %q has no steps", skill.ID)
	}

	visited := make(map[string]bool)
	currentID := startID
	for currentID != "" {
		if visited[currentID] {
			return nil, fmt.Errorf("pipeline cycle detected at step %q", currentID)
		}
		visited[currentID] = true

		step, ok := stepIdx[currentID]
		if !ok {
			return nil, fmt.Errorf("step %q not found in skill %q", currentID, skill.ID)
		}

		interp.nextStepOverride = ""
		if err := interp.executeStep(ctx, ic, step, vars, result); err != nil {
			return nil, fmt.Errorf("step %q (%s): %w", step.ID, step.Type, err)
		}
		override := interp.nextStepOverride
		interp.nextStepOverride = ""

		if override != "" {
			currentID = override
		} else if len(step.Next) > 0 {
			currentID = step.Next[0]
		} else {
			break
		}
	}

	return result, nil
}

func (interp *Interpreter) executeStep(ctx context.Context, ic *InvocationContext, step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
	def, ok := LookupNode(step.Type)
	if !ok {
		return fmt.Errorf("unknown step type %q", step.Type)
	}
	if def.Execute == nil {
		return fmt.Errorf("step type %q not yet implemented", step.Type)
	}
	return def.Execute(ctx, interp, ic, step, vars, result)
}

func (interp *Interpreter) execInput(step *StepSpec, vars PipelineVars) error {
	var cfg InputStepConfig
	if err := json.Unmarshal(step.Config, &cfg); err != nil {
		return fmt.Errorf("parse input config: %w", err)
	}
	if varName, ok := cfg.Bindings["text"]; ok && varName != "" {
		if inputText, ok := vars["input"]; ok {
			vars[varName] = inputText
		}
	}
	return nil
}

func (interp *Interpreter) execLLM(ctx context.Context, ic *InvocationContext, step *StepSpec, vars PipelineVars) error {
	var cfg LLMStepConfig
	if err := json.Unmarshal(step.Config, &cfg); err != nil {
		return fmt.Errorf("parse llm config: %w", err)
	}

	// Two-tier key resolution (most specific wins):
	// 1. per-app key from applications.provider_keys (ic.AppAPIKey)
	// 2. platform ANTHROPIC_API_KEY env var (interp.platformAPIKey)
	providerName := cfg.Provider
	if providerName == "" {
		providerName = "anthropic"
	}
	apiKey := interp.platformAPIKey
	if appKey, ok := ic.AppAPIKey[providerName]; ok && appKey != "" {
		apiKey = appKey
	}

	// Agent param model override: if set, prefer the runtime param over the compiled model.
	model := cfg.Model
	if cfg.ModelOverrideParamKey != "" && ic.AgentParams != nil {
		if override := ic.AgentParams[cfg.ModelOverrideParamKey]; override != "" {
			model = override
		}
	}

	if interp.llmFactory == nil {
		return fmt.Errorf("no LLM factory configured")
	}
	provider, err := interp.llmFactory.NewProvider(cfg.Provider, model, cfg.MaxTokens, apiKey)
	if err != nil {
		return fmt.Errorf("create LLM provider: %w", err)
	}

	systemPrompt, err := renderTemplate(cfg.SystemPrompt, vars)
	if err != nil {
		return fmt.Errorf("render system prompt: %w", err)
	}
	userPrompt, err := renderTemplate(cfg.UserPrompt, vars)
	if err != nil {
		return fmt.Errorf("render user prompt: %w", err)
	}
	// When no user_prompt template is set, fall back to the skill's input text.
	if userPrompt == "" {
		if inputVal, ok := vars["input"]; ok {
			userPrompt = fmt.Sprintf("%v", inputVal)
		}
	}

	out, err := provider.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return fmt.Errorf("LLM complete: %w", err)
	}

	if cfg.OutputVar != "" {
		vars[cfg.OutputVar] = out
	} else {
		vars["output"] = out
	}
	return nil
}

func (interp *Interpreter) execHTTP(ctx context.Context, ic *InvocationContext, step *StepSpec, vars PipelineVars) error {
	var cfg HTTPStepConfig
	if err := json.Unmarshal(step.Config, &cfg); err != nil {
		return fmt.Errorf("parse http config: %w", err)
	}

	urlStr, err := renderTemplate(cfg.URLTemplate, vars)
	if err != nil {
		return fmt.Errorf("render URL: %w", err)
	}

	var bodyReader *bytes.Reader
	if cfg.BodyTemplate != "" {
		bodyStr, err := renderTemplate(cfg.BodyTemplate, vars)
		if err != nil {
			return fmt.Errorf("render body: %w", err)
		}
		bodyReader = bytes.NewReader([]byte(bodyStr))
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, cfg.Method, urlStr, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	// App param auth injection — runs after static headers so it can override them.
	if cfg.AppParamKey != "" {
		paramVal := ""
		if ic.AgentParams != nil {
			paramVal = ic.AgentParams[cfg.AppParamKey]
		}
		if paramVal == "" {
			if cfg.InjectMode != "" {
				return fmt.Errorf("step requires param %q for auth injection but param is not set in app binding", cfg.AppParamKey)
			}
			// InjectMode empty + no value: silently skip injection (param optional).
		} else {
			switch cfg.InjectMode {
			case "header", "":
				req.Header.Set("Authorization", "Bearer "+paramVal)
			case "query":
				q := req.URL.Query()
				name := cfg.InjectHeaderName
				if name == "" {
					name = "api_key"
				}
				q.Set(name, paramVal)
				req.URL.RawQuery = q.Encode()
			case "basic":
				req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(paramVal)))
			case "custom_header":
				if cfg.InjectHeaderName == "" {
					return fmt.Errorf("inject_mode %q requires inject_header_name to be set", cfg.InjectMode)
				}
				req.Header.Set(cfg.InjectHeaderName, paramVal)
			default:
				return fmt.Errorf("unknown inject_mode %q", cfg.InjectMode)
			}
		}
	}

	if interp.httpClient == nil {
		return fmt.Errorf("no HTTP client configured")
	}
	resp, err := interp.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, urlStr)
	}

	var respBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err == nil {
		for _, ext := range cfg.Extractions {
			if val := extractJSONPath(respBody, ext.JSONPath); val != "" {
				vars[ext.Var] = val
			}
		}
		vars["http_response"] = respBody
	}
	return nil
}

func (interp *Interpreter) execTransform(step *StepSpec, vars PipelineVars) error {
	var cfg TransformStepConfig
	if err := json.Unmarshal(step.Config, &cfg); err != nil {
		return fmt.Errorf("parse transform config: %w", err)
	}

	// Phase 1: Go template expressions (legacy, kept for backward compat).
	for outputVar, expr := range cfg.Expressions {
		val, err := renderTemplate(expr, vars)
		if err != nil {
			return fmt.Errorf("transform expression for %q: %w", outputVar, err)
		}
		vars[outputVar] = val
	}

	// Phase 2: JSON path extractions (legacy, kept for backward compat).
	for _, ext := range cfg.Extractions {
		raw, ok := vars[ext.FromVar]
		if !ok {
			continue
		}
		var parsed map[string]any
		switch v := raw.(type) {
		case map[string]any:
			parsed = v
		case string:
			if err := json.Unmarshal([]byte(v), &parsed); err != nil {
				continue // silently skip unparseable
			}
		default:
			continue
		}
		if val := extractJSONPath(parsed, ext.JSONPath); val != "" {
			vars[ext.Var] = val
		}
	}

	// Phase 3: function chain — runs after expressions and extractions.
	if len(cfg.Functions) > 0 {
		tvars := make(transform.Vars, len(vars))
		for k, v := range vars {
			tvars[k] = v
		}
		_, err := transform.Execute(cfg.Functions, tvars)
		if err != nil {
			return fmt.Errorf("transform function chain: %w", err)
		}
		// Write outputs back to pipeline vars.
		for k, v := range tvars {
			vars[k] = v
		}
	}

	return nil
}

// isTruthy returns true when s represents a non-empty, non-false value.
func isTruthy(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && s != "false" && s != "0" && s != "<no value>"
}

func (interp *Interpreter) execBranch(step *StepSpec, vars PipelineVars) error {
	var cfg BranchStepConfig
	if err := json.Unmarshal(step.Config, &cfg); err != nil {
		return fmt.Errorf("parse branch config: %w", err)
	}
	rendered, err := renderTemplate(cfg.Expression, vars)
	if err != nil {
		return fmt.Errorf("render branch expression: %w", err)
	}

	// Resolve true/false targets: explicit config fields take priority over
	// edge order. Edge order fallback: Next[0]=true path, Next[1]=false path.
	trueNext := cfg.TrueNext
	falseNext := cfg.FalseNext
	if trueNext == "" && len(step.Next) > 0 {
		trueNext = step.Next[0]
	}
	if falseNext == "" && len(step.Next) > 1 {
		falseNext = step.Next[1]
	}

	if isTruthy(rendered) {
		interp.nextStepOverride = trueNext
	} else {
		interp.nextStepOverride = falseNext
	}
	return nil
}

func (interp *Interpreter) execResponse(step *StepSpec, vars PipelineVars, result *ExecutionResult) error {
	var cfg ResponseStepConfig
	if err := json.Unmarshal(step.Config, &cfg); err != nil {
		return fmt.Errorf("parse response config: %w", err)
	}
	fromVar := cfg.FromVar
	if fromVar == "" {
		fromVar = "output"
	}
	val, ok := vars[fromVar]
	if !ok {
		val = ""
	}
	result.Text = fmt.Sprintf("%v", val)
	result.MediaType = cfg.MediaType
	if result.MediaType == "" {
		result.MediaType = "text/plain"
	}
	return nil
}

// renderTemplate executes a Go text/template string over pipeline vars.
func renderTemplate(tmpl string, vars PipelineVars) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	t, err := template.New("").Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// extractJSONPath does simple dot-separated path extraction from a JSON map.
// Not a full JSONPath implementation — Phase 4 can upgrade to a real library.
func extractJSONPath(obj map[string]any, path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "$."), ".")
	var cur any = obj
	for _, part := range parts {
		if part == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[part]
		if !ok {
			return ""
		}
	}
	if cur == nil {
		return ""
	}
	return fmt.Sprintf("%v", cur)
}
