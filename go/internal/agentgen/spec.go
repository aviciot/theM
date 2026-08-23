package agentgen

import "encoding/json"

// AgentSpec is the compiled, reusable form the runtime loads. Frozen at publish.
type AgentSpec struct {
	ID             string           `json:"id"`              // == agents.id == component_definitions.id
	DefinitionID   string           `json:"definition_id"`   // which agent_definitions revision this came from
	Slug           string           `json:"slug"`
	TenantID       string           `json:"tenant_id"`
	Card           CardSpec         `json:"card"`
	Skills         []SkillSpec      `json:"skills"`
	DefaultModel   string           `json:"default_model"`
	RequiredParams []AgentParamSpec `json:"required_params,omitempty"` // aggregated from all nodes at publish time
}

// AppParamDecl declares one runtime parameter that a node type can consume.
// Declared statically on NodeDef — identical for every instance of that node type.
// The per-instance config references a specific param by key via AppParamKey fields.
type AppParamDecl struct {
	Key          string `json:"key"`                     // identifier referenced in node instance configs
	Label        string `json:"label"`                   // human-readable label for the UI form
	Description  string `json:"description"`             // tooltip / help text
	Type         string `json:"type"`                    // "secret" | "string" | "url" | "int" | "bool"
	Required     bool   `json:"required"`
	DefaultValue string `json:"default_value,omitempty"`
}

// AgentParamSpec is the published, immutable form of one required parameter.
// Collected by the compiler from all AppParamDecl entries across all skills in an agent.
// Stored as part of AgentSpec.RequiredParams in agent_runtime_specs.spec JSONB.
type AgentParamSpec struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Description  string   `json:"description"`
	Type         string   `json:"type"`                    // "secret" | "string" | "url" | "int" | "bool"
	Required     bool     `json:"required"`
	DefaultValue string   `json:"default_value,omitempty"`
	UsedByNodes  []string `json:"used_by_nodes"` // step IDs that reference this key
}

type CardSpec struct {
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Version      string           `json:"version"`
	Icon         string           `json:"icon,omitempty"`
	Category     string           `json:"category,omitempty"`
	Capabilities CapabilitiesSpec `json:"capabilities"`
}

type CapabilitiesSpec struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"push_notifications"`
}

type SkillSpec struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Tags        []string   `json:"tags"`
	InputModes  []string   `json:"input_modes"`
	OutputModes []string   `json:"output_modes"`
	Steps       []StepSpec `json:"steps"` // topologically ordered by compiler
}

// StepSpec is one pipeline node, compiled from the canvas.
type StepSpec struct {
	ID       string          `json:"id"`
	Type     StepType        `json:"type"`
	Config   json.RawMessage `json:"config"`
	Next     []string        `json:"next"`               // step IDs to run after this one
	Branches []BranchArm     `json:"branches,omitempty"` // for branch/loop steps
}

type StepType string

const (
	StepInput     StepType = "input"
	StepLLM       StepType = "llm"
	StepHTTP      StepType = "http"
	StepTransform StepType = "transform"
	StepResponse  StepType = "response"
	StepBranch    StepType = "branch"
	StepLoop      StepType = "loop"
	StepParallel  StepType = "parallel"
	StepA2ACall   StepType = "a2a_call"
	StepHumanWait StepType = "human_wait"
	StepStreamOut StepType = "stream_out"
)

type BranchArm struct {
	Condition string   `json:"condition"`
	Next      []string `json:"next"`
}

// LLMStepConfig configures one LLM step.
type LLMStepConfig struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
	MaxTokens    int    `json:"max_tokens"`
	Effort       string `json:"effort,omitempty"`
	OutputVar    string `json:"output_var"`
	Stream       bool   `json:"stream"`
	// ModelOverrideParamKey, if set, names the AgentParamSpec.Key whose value overrides
	// the compiled model at runtime. The referenced param must be of type "string".
	ModelOverrideParamKey string `json:"model_override_param_key,omitempty"`
}

// HTTPStepConfig configures one HTTP tool step.
type HTTPStepConfig struct {
	Method         string            `json:"method"`
	URLTemplate    string            `json:"url_template"`
	Headers        map[string]string `json:"headers,omitempty"`
	BodyTemplate   string            `json:"body_template,omitempty"`
	Extractions    []JSONPathExtract  `json:"extractions"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	// AppParamKey names the AgentParamSpec.Key holding the auth credential to inject.
	// Empty means no auth injection.
	AppParamKey string `json:"app_param_key,omitempty"`
	// InjectMode controls how the credential is injected:
	// "header" (default) → Authorization: Bearer <value>
	// "query"            → ?<InjectHeaderName>=<value>
	// "basic"            → Authorization: Basic base64(<value>)
	// "custom_header"    → <InjectHeaderName>: <value>
	InjectMode string `json:"inject_mode,omitempty"`
	// InjectHeaderName is the header or query param name for "query" and "custom_header" modes.
	InjectHeaderName string `json:"inject_header_name,omitempty"`
}

type JSONPathExtract struct {
	Var      string `json:"var"`
	JSONPath string `json:"json_path"`
}

type TransformStepConfig struct {
	Expressions map[string]string `json:"expressions"` // output_var → Go template expression
}

type InputStepConfig struct {
	Bindings map[string]string `json:"bindings"` // part_type → variable_name
}

type ResponseStepConfig struct {
	FromVar   string `json:"from_var"`
	MediaType string `json:"media_type"`
}

type A2ACallStepConfig struct {
	Ref            DefinitionRef `json:"ref"`
	InputVar       string        `json:"input_var"`
	OutputVar      string        `json:"output_var"`
	TimeoutSeconds int           `json:"timeout_seconds"`
}

// DefinitionRef is a portable cross-environment reference to a component.
type DefinitionRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
}

type HumanWaitConfig struct {
	Prompt   string `json:"prompt"`
	ReplyVar string `json:"reply_var"`
}

type LoopConfig struct {
	BodySteps     []string `json:"body_steps"`
	Condition     string   `json:"condition"`
	MaxIterations int      `json:"max_iterations"`
	AccumVar      string   `json:"accum_var,omitempty"`
}

type ParallelConfig struct {
	Branches [][]string `json:"branches"`
	MergeVar string     `json:"merge_var"`
}

// BranchStepConfig configures a branch step.
// Expression is a Go template that must render to "true" or "false".
// TrueNext is the step ID when true; FalseNext when false.
type BranchStepConfig struct {
	Expression string `json:"expression"` // Go template; "true"/"false"
	TrueNext   string `json:"true_next"`  // step ID when true
	FalseNext  string `json:"false_next"` // step ID when false
}
