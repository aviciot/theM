package agentgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"
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
	httpClient     HTTPDoer
	llmFactory     LLMFactory
	platformAPIKey string // fallback LLM key when no slot is bound
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

		if err := interp.executeStep(ctx, ic, step, vars, result); err != nil {
			return nil, fmt.Errorf("step %q (%s): %w", step.ID, step.Type, err)
		}

		if len(step.Next) > 0 {
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

	// Three-tier key resolution (most specific wins):
	// 1. per-binding slot override (cfg.ProviderKeySlot → ic.Credentials)
	// 2. per-app key from applications.provider_keys (ic.AppAPIKey)
	// 3. platform ANTHROPIC_API_KEY env var (interp.platformAPIKey)
	providerName := cfg.Provider
	if providerName == "" {
		providerName = "anthropic"
	}
	apiKey := interp.platformAPIKey
	if appKey, ok := ic.AppAPIKey[providerName]; ok && appKey != "" {
		apiKey = appKey
	}
	if cfg.ProviderKeySlot != "" {
		if slotVal, ok := ic.Credentials[cfg.ProviderKeySlot]; ok && slotVal != "" {
			apiKey = slotVal
		}
	}

	if interp.llmFactory == nil {
		return fmt.Errorf("no LLM factory configured")
	}
	provider, err := interp.llmFactory.NewProvider(cfg.Provider, cfg.Model, cfg.MaxTokens, apiKey)
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

	if cfg.CredentialSlot != "" {
		cred, ok := ic.Credentials[cfg.CredentialSlot]
		if !ok {
			return fmt.Errorf("credential slot %q not found in invocation context", cfg.CredentialSlot)
		}
		switch cfg.CredentialInject.Mode {
		case "query":
			q := req.URL.Query()
			q.Set(cfg.CredentialInject.QueryParam, cred)
			req.URL.RawQuery = q.Encode()
		case "basic":
			req.SetBasicAuth(cred, "")
		default: // "header" or empty
			headerName := cfg.CredentialInject.HeaderName
			if headerName == "" {
				headerName = "Authorization"
			}
			value := cfg.CredentialInject.ValueTemplate
			if value == "" {
				value = "Bearer {credential}"
			}
			value = strings.ReplaceAll(value, "{credential}", cred)
			req.Header.Set(headerName, value)
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
	for outputVar, expr := range cfg.Expressions {
		val, err := renderTemplate(expr, vars)
		if err != nil {
			return fmt.Errorf("transform expression for %q: %w", outputVar, err)
		}
		vars[outputVar] = val
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
