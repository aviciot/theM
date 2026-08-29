package agentgen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// MCPCaller calls an MCP tool via them-mcp-service. Nil means MCP is not configured.
type MCPCaller interface {
	// Call invokes a tool on the named MCP server for the given application.
	// Returns the JSON-encoded tool result or a non-nil error.
	Call(ctx context.Context, applicationID, mcpServerSlug, toolName string, args map[string]any) (json.RawMessage, error)
}

// PipelineVars holds variables scoped to one pipeline execution.
type PipelineVars map[string]any

// Interpreter executes a SkillSpec pipeline against an InvocationContext.
type Interpreter struct {
	httpClient       HTTPDoer
	llmFactory       LLMFactory
	mcpCaller        MCPCaller // nil when MCP_SERVICE_URL is not configured
	platformAPIKey   string    // fallback LLM key when no app key is set
	nextStepOverride string    // set by condition/branch steps; read and cleared by the Execute loop
}

// NewInterpreter creates an Interpreter.
func NewInterpreter(httpClient HTTPDoer, llmFactory LLMFactory, platformAPIKey string) *Interpreter {
	return &Interpreter{
		httpClient:     httpClient,
		llmFactory:     llmFactory,
		platformAPIKey: platformAPIKey,
	}
}

// WithMCPCaller sets the MCP caller used for mcp_call steps.
func (interp *Interpreter) WithMCPCaller(mc MCPCaller) *Interpreter {
	interp.mcpCaller = mc
	return interp
}

// clone returns a shallow copy of the Interpreter with a fresh nextStepOverride.
// Used by LocalExecutor so each branch goroutine has its own mutable state.
func (interp *Interpreter) clone() *Interpreter {
	cp := *interp
	cp.nextStepOverride = ""
	return &cp
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

	// ── Stage 6: scoped input resolution ────────────────────────────────────────
	// When the compiled spec carries declared Inputs/Outputs, enforce the contract:
	//   1. Build a scoped PipelineVars from only the declared input keys.
	//   2. Check Required inputs — absent → ErrContractViolation.
	//   3. Call Execute with the scoped map (nodes cannot read undeclared vars).
	//   4. Promote only declared output keys back to global state.
	//
	// Steps with empty Inputs and Outputs (stub nodes or specs compiled before
	// Stage 6) fall through with the full global vars, preserving backward
	// compatibility for any uncompiled or partially-compiled skills.
	//
	// Immutability contract: Execute functions MUST NOT mutate values retrieved
	// from scoped inputs (map[string]any / []any values are shared references).
	// Deep-copy is intentionally omitted — sequential execution makes concurrent
	// mutation impossible. This will need revisiting when StepParallel is implemented.
	if len(step.Inputs) > 0 || len(step.Outputs) > 0 {
		// Check required inputs before building the scoped map.
		for _, ref := range step.Inputs {
			if ref.Required {
				if _, present := vars[ref.Name]; !present {
					return &ErrContractViolation{
						StepID:  step.ID,
						VarName: ref.Name,
						Kind:    "missing_required_input",
					}
				}
			}
		}

		// Build scoped map: only declared input keys (absent optional vars are omitted).
		scopedVars := make(PipelineVars, len(step.Inputs))
		for _, ref := range step.Inputs {
			if v, present := vars[ref.Name]; present {
				scopedVars[ref.Name] = v
			}
		}

		if err := def.Execute(ctx, interp, ic, step, scopedVars, result); err != nil {
			return err
		}

		// Promote only declared output keys back to global state.
		for _, ref := range step.Outputs {
			if v, present := scopedVars[ref.Name]; present {
				vars[ref.Name] = v
			}
		}
		return nil
	}

	// Fallback: no compiled contract — pass global vars directly (legacy path).
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

	// Node-level override (set via RuntimeView) takes precedence over compiled values.
	providerName := cfg.Provider
	model := cfg.Model
	if ov, ok := ic.NodeLLMOverrides[step.ID]; ok && ov.Provider != "" {
		providerName = ov.Provider
		if ov.Model != "" {
			model = ov.Model
		}
	}
	if providerName == "" {
		providerName = "anthropic"
	}

	// API key: per-app key wins over platform fallback.
	apiKey := interp.platformAPIKey
	if appKey, ok := ic.AppAPIKey[providerName]; ok && appKey != "" {
		apiKey = appKey
	}

	if interp.llmFactory == nil {
		return fmt.Errorf("no LLM factory configured")
	}
	provider, err := interp.llmFactory.NewProvider(providerName, model, cfg.MaxTokens, apiKey)
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

// injectAuthParam injects paramVal into req using the specified inject mode.
// mode "" defaults to "header" (Authorization: Bearer).
func injectAuthParam(req *http.Request, mode, headerName, paramVal string) error {
	switch mode {
	case "header", "":
		req.Header.Set("Authorization", "Bearer "+paramVal)
	case "query":
		q := req.URL.Query()
		name := headerName
		if name == "" {
			name = "api_key"
		}
		q.Set(name, paramVal)
		req.URL.RawQuery = q.Encode()
	case "basic":
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(paramVal)))
	case "custom_header":
		if headerName == "" {
			return fmt.Errorf("inject_mode %q requires inject_header_name to be set", mode)
		}
		req.Header.Set(headerName, paramVal)
	default:
		return fmt.Errorf("unknown inject_mode %q", mode)
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
		if cfg.FormKey != "" {
			// Percent-encode the rendered body as a form value so characters like ":" and "["
			// in Overpass QL (and similar APIs) are not misinterpreted by upstream servers.
			bodyStr = cfg.FormKey + "=" + url.QueryEscape(bodyStr)
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
	// AppParamRef (app-global param) takes precedence over AppParamKey (per-binding param).
	if cfg.AppParamRef != "" {
		paramVal := ""
		if ic.AppGlobalParams != nil {
			paramVal = ic.AppGlobalParams[cfg.AppParamRef]
		}
		if paramVal != "" {
			if err := injectAuthParam(req, cfg.InjectMode, cfg.InjectHeaderName, paramVal); err != nil {
				return err
			}
		} else if cfg.InjectMode != "" {
			return fmt.Errorf("step requires app param %q (app_param_ref) but param is not set in app params", cfg.AppParamRef)
		}
		// InjectMode empty + no value: silently skip injection (param optional).
	} else if cfg.AppParamKey != "" {
		paramVal := ""
		if ic.AgentParams != nil {
			// Look up by composite key "{stepID}:{paramKey}" (new per-instance format).
			// Fall back to plain "{paramKey}" for agents published before this change.
			if v := ic.AgentParams[step.ID+":"+cfg.AppParamKey]; v != "" {
				paramVal = v
			} else {
				paramVal = ic.AgentParams[cfg.AppParamKey]
			}
		}
		if paramVal != "" {
			if err := injectAuthParam(req, cfg.InjectMode, cfg.InjectHeaderName, paramVal); err != nil {
				return err
			}
		} else if cfg.InjectMode != "" {
			return fmt.Errorf("step requires param %q for auth injection but param is not set in app binding", cfg.AppParamKey)
		}
		// InjectMode empty + no value: silently skip injection (param optional).
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
		body, _ := io.ReadAll(resp.Body)
		detail := strings.TrimSpace(string(body))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if detail != "" {
			return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, urlStr, detail)
		}
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

	if len(cfg.Functions) == 0 {
		return nil
	}

	// Cast vars to transform.Vars (same underlying type) and execute in-place.
	// When called via the scoped path (Stage 6), vars is already the scoped map;
	// the interpreter promotes only declared outputs back to global state after
	// this function returns. When called via the legacy path, vars is global and
	// all writes are immediately visible downstream — same behaviour as before.
	tvars := transform.Vars(vars)
	if _, err := transform.Execute(cfg.Functions, tvars); err != nil {
		return fmt.Errorf("transform function chain: %w", err)
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
