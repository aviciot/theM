package agentgen

import "encoding/json"

// AgentSpec is the compiled, reusable form the runtime loads. Frozen at publish.
// NO secret values and NO env-var names — only credential slot NAMES.
type AgentSpec struct {
	ID           string      `json:"id"`            // == agents.id == component_definitions.id
	DefinitionID string      `json:"definition_id"` // which agent_definitions revision this came from
	Slug         string      `json:"slug"`
	TenantID     string      `json:"tenant_id"`
	Card         CardSpec    `json:"card"`
	Skills       []SkillSpec `json:"skills"`
	// CredentialSlots is the contract every binding must satisfy. Names only.
	CredentialSlots []CredentialSlotSpec `json:"credential_slots"`
	DefaultModel    string               `json:"default_model"`
}

type CredentialSlotSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
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

// LLMStepConfig configures one LLM step. Credential handling is slot-name-only.
type LLMStepConfig struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
	MaxTokens    int    `json:"max_tokens"`
	Effort       string `json:"effort,omitempty"`
	OutputVar    string `json:"output_var"`
	Stream       bool   `json:"stream"`
	// ProviderKeySlot: if set, the LLM API key is resolved from the application
	// binding's credential slot of this name. If empty, falls back to platform key.
	// The key value is NEVER in the spec.
	ProviderKeySlot string `json:"provider_key_slot,omitempty"`
}

// HTTPStepConfig configures one HTTP tool step. Credential handling is slot-name-only.
type HTTPStepConfig struct {
	Method      string `json:"method"`
	URLTemplate string `json:"url_template"`
	// Headers holds NON-SECRET static headers only (Accept, Content-Type, etc.).
	Headers          map[string]string `json:"headers,omitempty"`
	BodyTemplate     string            `json:"body_template,omitempty"`
	Extractions      []JSONPathExtract `json:"extractions"`
	// CredentialSlot names the slot whose decrypted value is injected at runtime.
	// This is the ONLY way credentials enter an HTTP step.
	CredentialSlot   string           `json:"credential_slot,omitempty"`
	CredentialInject CredentialInject `json:"credential_inject,omitempty"`
	TimeoutSeconds   int              `json:"timeout_seconds"`
}

// CredentialInject describes how the resolved slot value is applied to the request.
type CredentialInject struct {
	// Mode: "header" (default) | "query" | "basic"
	Mode          string `json:"mode"`
	HeaderName    string `json:"header_name,omitempty"`
	ValueTemplate string `json:"value_template,omitempty"` // e.g. "Bearer {credential}"
	QueryParam    string `json:"query_param,omitempty"`
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
