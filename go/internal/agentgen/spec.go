package agentgen

import (
	"encoding/json"

	"github.com/aviciot/them/internal/agentgen/transform"
)

// AgentSpec is the compiled, reusable form the runtime loads. Frozen at publish.
type AgentSpec struct {
	ID             string             `json:"id"`            // == agents.id == component_definitions.id
	DefinitionID   string             `json:"definition_id"` // which agent_definitions revision this came from
	Slug           string             `json:"slug"`
	TenantID       string             `json:"tenant_id"`
	Card           CardSpec           `json:"card"`
	Skills         []SkillSpec        `json:"skills"`
	DefaultModel   string             `json:"default_model"`
	RequiredParams []AgentParamSpec   `json:"required_params,omitempty"` // per-binding params (HTTP api_key / bearer_token)
	LLMNodes       []AgentLLMNodeSpec `json:"llm_nodes,omitempty"`       // LLM nodes whose provider+model can be overridden at runtime
}

// AgentLLMNodeSpec describes one LLM node in a published agent.
// The runtime uses this list to populate NodeLLMOverrides from app_agent_bindings.config_overrides.
type AgentLLMNodeSpec struct {
	NodeID          string `json:"node_id"`          // step ID in the canvas
	Label           string `json:"label"`            // human-readable label (node label or step ID)
	CompiledProvider string `json:"compiled_provider"` // provider baked in at publish
	CompiledModel   string `json:"compiled_model"`   // model baked in at publish
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

// VarRef describes one variable a step reads from or writes to PipelineVars.
// Computed by NodeDef.DeriveInputs / DeriveOutputs from instance config.
// Stored in StepSpec after compilation. Not present in canvas JSON (only in compiled AgentSpec).
//
// Stage 6 runtime enforcement: the interpreter resolves step.Inputs into a scoped
// PipelineVars before calling Execute, so nodes can only read declared inputs.
// After Execute, only declared step.Outputs are promoted back to global state.
// Required=true: a missing upstream writer is a publish error AND a runtime
// ErrContractViolation. Required=false: missing at compile time is a warning;
// missing at runtime is silently treated as zero/absent (same as today's template
// "missingkey=zero" behaviour).
//
// Immutability contract (sequential enforcement only): Execute functions MUST NOT
// mutate values retrieved from their scoped input vars. Values of type map[string]any
// or []any are shared references — deep-copy is not done automatically because the
// sequential execution model makes concurrent mutation impossible today. When
// StepParallel is implemented, deep-copy will be required at that boundary.
type VarRef struct {
	Name     string `json:"name"`                // PipelineVars key
	Required bool   `json:"required"`            // missing upstream writer → error (true) vs warning (false)
	PortID     string `json:"port_id,omitempty"`   // port this var was derived from (empty for heuristic path)
	SourceStep string `json:"source_step,omitempty"` // step that produces this value (from explicit binding)
	SourcePort string `json:"source_port,omitempty"` // output port on the source step
}

// ErrContractViolation is returned by the interpreter at runtime when a step's
// declared data-flow contract is violated. Kind is one of:
//   - "missing_required_input": step.Inputs contains a Required var that is absent
//     from global PipelineVars at the moment the step is about to execute.
type ErrContractViolation struct {
	StepID  string
	VarName string
	Kind    string // "missing_required_input"
}

func (e *ErrContractViolation) Error() string {
	return "contract violation at step " + e.StepID + ": " + e.Kind + " (var: " + e.VarName + ")"
}

// StepSpec is one pipeline node, compiled from the canvas.
type StepSpec struct {
	ID       string          `json:"id"`
	Type     StepType        `json:"type"`
	Config   json.RawMessage `json:"config"`
	Next     []string        `json:"next"`               // step IDs to run after this one
	Branches []BranchArm     `json:"branches,omitempty"` // for branch/loop steps
	// Inputs and Outputs are derived by the compiler from NodeDef.DeriveInputs/DeriveOutputs.
	// Absent from canvas JSON; present only in compiled AgentSpec persisted to agent_runtime_specs.
	//
	// Runtime enforcement (Stage 6): the interpreter resolves Inputs into a scoped
	// PipelineVars before calling Execute (nodes cannot read undeclared vars), and
	// promotes only Outputs back to global state after Execute returns (undeclared
	// writes are dropped). Required Inputs that are absent at runtime produce
	// ErrContractViolation. Steps with empty Inputs/Outputs (stub or uncompiled
	// specs) pass through with the full global vars, preserving backward compatibility.
	Inputs  []VarRef `json:"inputs,omitempty"`
	Outputs []VarRef `json:"outputs,omitempty"`
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
	StepMCPCall   StepType = "mcp_call"
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
	// AppParamRef names an app-global param (from AppGlobalParams) whose value provides
	// the auth credential. Takes precedence over AppParamKey when both are set.
	AppParamRef string `json:"app_param_ref,omitempty"`
	// InjectMode controls how the credential is injected:
	// "header" (default) → Authorization: Bearer <value>
	// "query"            → ?<InjectHeaderName>=<value>
	// "basic"            → Authorization: Basic base64(<value>)
	// "custom_header"    → <InjectHeaderName>: <value>
	InjectMode string `json:"inject_mode,omitempty"`
	// InjectHeaderName is the header or query param name for "query" and "custom_header" modes.
	InjectHeaderName string `json:"inject_header_name,omitempty"`
	// FormKey, when set, treats body_template as a raw form value. The rendered template is
	// percent-encoded and sent as "{FormKey}={encoded}" with Content-Type application/x-www-form-urlencoded.
	// This is required when the raw body contains characters like ":" or "[" that Apache/nginx
	// reject if left unencoded in a form POST.
	FormKey string `json:"form_key,omitempty"`
}

type JSONPathExtract struct {
	Var      string `json:"var"`
	JSONPath string `json:"json_path"`
}

type TransformStepConfig struct {
	Functions []transform.FunctionStep `json:"functions,omitempty"`
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

// ── ExecutionPlan ─────────────────────────────────────────────────────────────
// ExecutionPlan is compiled from a SkillSpec by CompileExecutionPlan.
// It is a richer, executor-ready representation of the DAG with join annotations.
// Stored alongside AgentSpec; the same plan is executed by LocalExecutor or
// TemporalExecutor without modification.

type JoinMode string

const (
	JoinNone    JoinMode = "none"     // standard node — no join
	JoinWaitAll JoinMode = "wait_all" // block until ALL predecessor branches arrive
	JoinWaitAny JoinMode = "wait_any" // reserved; not implemented in Phase 1
)

// ExecutionPlan is the compiled, executor-ready form of one skill's DAG.
type ExecutionPlan struct {
	SkillID string      `json:"skill_id"`
	StartID string      `json:"start_id"` // ID of the entry step (type: input or first step)
	Nodes   []*PlanNode `json:"nodes"`
}

// PlanNode is one node in the ExecutionPlan.
type PlanNode struct {
	StepID   string          `json:"step_id"`
	Type     StepType        `json:"type"`
	Config   json.RawMessage `json:"config"`
	Next     []string        `json:"next"`              // len>1 = fan-out point
	JoinOf   []string        `json:"join_of,omitempty"` // predecessor IDs that must all arrive
	JoinMode JoinMode        `json:"join_mode"`
	Inputs   []VarRef        `json:"inputs,omitempty"`
	Outputs  []VarRef        `json:"outputs,omitempty"`
	Branches []BranchArm     `json:"branches,omitempty"`
}

// MCPCallConfig configures an mcp_call canvas step.
type MCPCallConfig struct {
	MCPServerSlug string `json:"mcp_server_slug"` // slug of the MCP server registered in the admin UI
	ToolName      string `json:"tool_name"`        // tool to invoke (must be in the server's manifest)
	ArgsTemplate  string `json:"args_template"`    // JSON Go template for the args object
	OutputVar     string `json:"output_var"`       // pipeline var to write the tool result into
}
