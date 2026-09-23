// AppFlow Temporal activity implementations and their dependencies.
//
// Activities run in the worker process (them-dag-worker), NOT in workflow code,
// so they may do I/O: DB queries, HTTP calls, Redis writes. Anything that is
// non-deterministic or needs a secret belongs here rather than in workflow.go.
//
// Secrets invariant: no API key, token, or credential may appear in an activity
// INPUT type — activity inputs are persisted in Temporal workflow history.
// Activities resolve credentials from the DB at execution time instead.
package appflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	temporalerr "go.temporal.io/sdk/temporal"

	"github.com/aviciot/them/internal/domain"
)

// ── Activity input/output types ────────────────────────────────────────────────

// RouterActivityInput is the input to AppFlowExecuteRouterActivity.
type RouterActivityInput struct {
	RunID         string `json:"run_id"`
	TenantID      string `json:"tenant_id"`
	ApplicationID string `json:"application_id"`
	NodeID        string `json:"node_id"`
	UserMessage   string `json:"user_message"`
	// Labels are the valid output labels the router can choose.
	Labels []string `json:"labels"`
	// ClassifierPrompt overrides the default classification prompt.
	ClassifierPrompt string `json:"classifier_prompt,omitempty"`
	// LLM config. API key resolved by activity from DB using LLMProviderName + TenantID + ApplicationID.
	LLMProviderName string `json:"llm_provider_name,omitempty"`
	LLMProvider     string `json:"llm_provider,omitempty"`
	LLMModel        string `json:"llm_model,omitempty"`
	// Verbosity is the resolved effective log-verbosity for this run
	// ("off"|"status"|"full"). See TraceEventInput.Verbosity.
	Verbosity string `json:"verbosity,omitempty"`
}

// RouterActivityOutput is returned by AppFlowExecuteRouterActivity.
type RouterActivityOutput struct {
	// ChosenLabel is the intent label chosen by the classifier.
	ChosenLabel string `json:"chosen_label"`
}

// HILActivityInput is the input to AppFlowExecuteHILActivity.
type HILActivityInput struct {
	RunID          string `json:"run_id"`
	TenantID       string `json:"tenant_id"`
	ApplicationID  string `json:"application_id"`
	NodeID         string `json:"node_id"`
	ApproverRole   string `json:"approver_role,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	TimeoutSecs    int    `json:"timeout_seconds,omitempty"`
	FallbackAction string `json:"fallback_action,omitempty"` // "reject"|"approve"|"abort"
	// Verbosity is the resolved effective log-verbosity for this run
	// ("off"|"status"|"full"). See TraceEventInput.Verbosity.
	Verbosity string `json:"verbosity,omitempty"`
}

// HILActivityOutput is returned by AppFlowExecuteHILActivity.
type HILActivityOutput struct {
	// Approved is true when a human approved the HIL gate.
	Approved bool   `json:"approved"`
	Comment  string `json:"comment,omitempty"`
}

// FinalizeRunActivityInput is the input to AppFlowFinalizeRunActivity.
type FinalizeRunActivityInput struct {
	RunID     string `json:"run_id"`
	TenantID  string `json:"tenant_id"`
	Status    string `json:"status"` // "completed" | "failed" | "rejected"
	FinalText string `json:"final_text,omitempty"`
	ErrMsg    string `json:"err_msg,omitempty"`
}

// AgentInvokeActivityInput is the input to AppFlowInvokeAgentActivity.
type AgentInvokeActivityInput struct {
	RunID         string `json:"run_id"`
	TenantID      string `json:"tenant_id"`
	ApplicationID string `json:"application_id"`
	NodeID        string `json:"node_id"`
	// AgentID is the UUID from agents.id (server-stamped in _resolved_agent_ids).
	AgentID     string `json:"agent_id"`
	UserMessage string `json:"user_message"`
	// Verbosity is the resolved effective log-verbosity for this run
	// ("off"|"status"|"full"). See TraceEventInput.Verbosity.
	Verbosity string `json:"verbosity,omitempty"`
}

// AgentInvokeActivityOutput is returned by AppFlowInvokeAgentActivity.
type AgentInvokeActivityOutput struct {
	// ResponseText is the agent's plain-text reply.
	ResponseText string `json:"response_text"`
}

// InlineLLMActivityInput is the input to AppFlowInlineLLMActivity.
// No API key is ever present — the activity resolves it from the DB at
// execution time so it never enters Temporal workflow history.
type InlineLLMActivityInput struct {
	RunID         string `json:"run_id"`
	TenantID      string `json:"tenant_id"`
	ApplicationID string `json:"application_id"`
	NodeID        string `json:"node_id"`

	// Rendered prompts. Templates are rendered in the ACTIVITY, not the
	// workflow — text/template is not deterministic-safe for workflow code.
	SystemPrompt string   `json:"system_prompt,omitempty"`
	UserPrompt   string   `json:"user_prompt,omitempty"`
	Vars         FlowVars `json:"vars,omitempty"`

	Provider    string   `json:"provider,omitempty"`
	Model       string   `json:"model,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`

	// OutputVar names the flow variable the workflow should set with the
	// response text. Echoed back in the output so the workflow does not have
	// to re-parse node config.
	OutputVar string `json:"output_var,omitempty"`

	// Stream, when true, publishes token events to the run's Redis stream.
	Stream bool `json:"stream,omitempty"`
	// Verbosity is the resolved effective log-verbosity for this run
	// ("off"|"status"|"full"). See TraceEventInput.Verbosity.
	Verbosity string `json:"verbosity,omitempty"`
	// Debug marks this run as a debug session — a non-secret flag, safe in
	// Temporal history (see AF-WF-14 and docs/APPFLOW_RUNTIME_PARAMS_PLAN.md).
	// When true, the credential resolver checks the debugcred.Store override
	// for (TenantID, RunID, NodeID) first; when absent for a debug run that
	// declared a required llm_credential param, resolution must fail loudly
	// rather than silently falling through to normal (tenant/platform) key
	// resolution — Debug is what lets the resolver distinguish "this is a
	// production call, no override was ever possible" from "this is a debug
	// call whose override has gone missing."
	Debug bool `json:"debug,omitempty"`
}

// InlineLLMActivityOutput is returned by AppFlowInlineLLMActivity.
type InlineLLMActivityOutput struct {
	// ResponseText is the model's reply.
	ResponseText string `json:"response_text"`
	// OutputVar is echoed back so the workflow knows which var to set without
	// re-parsing node config.
	OutputVar string `json:"output_var"`
}

// TraceEventInput is the input to AppFlowTraceActivity — a live, unconditional
// per-node progress event (docs/APP_CANVAS_DEBUG_PLAN.md Phase 2). Emitted for
// every run, debug or not; not persisted anywhere (Phase 3's job). Kept
// deliberately small: an event type, which node, and a short kind-specific
// Detail string — never full prompts/inputs/outputs.
type TraceEventInput struct {
	RunID  string `json:"run_id"`
	NodeID string `json:"node_id"`
	Kind   string `json:"kind"`
	// EventType is "node_start" | "node_done" | "node_error".
	EventType string `json:"event_type"`
	// Detail is a short, node-kind-specific summary (e.g. a condition's chosen
	// branch, a router's chosen label, a fork's branch count). Empty for
	// node_start. Never a full prompt, response, or other large/sensitive text.
	Detail string `json:"detail,omitempty"`
	// Verbosity is the resolved effective log-verbosity for this run
	// ("off"|"status"|"full") — see AppFlowWorkflowInput.LogVerbosity
	// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4). Governs how much persistTrace
	// writes to them.run_steps; the live Redis publish is unaffected.
	Verbosity string `json:"verbosity,omitempty"`
}

// ── Activity dependencies and implementations ─────────────────────────────────

// AppFlowActivities holds dependencies for AppFlow Temporal activities.
type AppFlowActivities struct {
	// LLMCaller is used by the Router activity to classify intent.
	LLMCaller RouterLLMCaller
	// DB is used by the HIL activity to persist approval requests.
	DB *pgxpool.Pool
	// StatusUpdater updates run DB status on completion. May be nil (no-op).
	StatusUpdater RunStatusUpdater
	// StreamPub publishes events to the run's Redis Stream. May be nil (no-op).
	StreamPub StreamPublisher
	// AgentInvoker calls the agent identified by AgentID via A2A HTTP.
	AgentInvoker AgentInvoker
	// InlineLLM is used by the inline LLM node activity to call an LLM directly
	// (as opposed to LLMCaller, which is shaped for Router label classification).
	InlineLLM InlineLLMCaller
}

// AgentInvoker calls a specific agent by its DB UUID via A2A.
// Implemented by the dag-worker's agentA2ACaller.
type AgentInvoker interface {
	InvokeByID(ctx context.Context, tenantID, applicationID, agentID, userMessage string) (string, error)
}

// RunStatusUpdater updates a run's terminal status in the DB.
// Implemented by *runrecorder.Recorder. Nil = no-op.
type RunStatusUpdater interface {
	UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus, errMsg string) error
}

// StreamPublisher writes a JSON event string to the run's Redis Stream key.
// Implemented by the dag-worker's redisStreamPublisher. Nil = no-op.
type StreamPublisher interface {
	XAdd(ctx context.Context, key string, fields map[string]interface{}) error
}

// RouterLLMCaller is the interface the Router activity uses to call an LLM.
// The implementation resolves the API key from DB using providerName + tenantID + applicationID
// so the key is never stored in Temporal workflow history.
type RouterLLMCaller interface {
	ClassifyIntent(ctx context.Context, userMessage, systemPrompt string, labels []string, providerName, model, tenantID, applicationID string) (string, error)
}

// InlineLLMCaller is the interface the inline LLM activity uses. Deliberately
// separate from RouterLLMCaller, which is shaped specifically for label
// classification. The implementation resolves the API key from the DB using
// ProviderName + TenantID + ApplicationID, so keys never reach workflow history.
type InlineLLMCaller interface {
	Complete(ctx context.Context, req InlineLLMRequest) (string, error)
}

// InlineLLMRequest is the request passed to InlineLLMCaller.Complete.
type InlineLLMRequest struct {
	SystemPrompt  string
	UserPrompt    string
	ProviderName  string
	Model         string
	MaxTokens     int
	Temperature   *float64
	TenantID      string
	ApplicationID string
	// RunID/NodeID identify which debug credential override (if any) applies
	// — see debugcred.Store and Debug below. IDs, not secrets: safe to have
	// reached this point via InlineLLMActivityInput, which AF-WF-14 already
	// permits ID-shaped fields on.
	RunID  string
	NodeID string
	// Debug — see InlineLLMActivityInput.Debug's doc comment for why this
	// flag exists and what "fail loudly, not silently" means for the
	// resolver that consumes it.
	Debug bool
}

// ExecuteRouterActivity calls an LLM to classify the user message and returns
// the matching intent label from the router's output_labels list.
func (a *AppFlowActivities) ExecuteRouterActivity(ctx context.Context, input RouterActivityInput) (RouterActivityOutput, error) {
	a.emitTrace(ctx, input.RunID, input.NodeID, "router", "node_start", "", input.Verbosity)

	if len(input.Labels) == 0 {
		a.emitTrace(ctx, input.RunID, input.NodeID, "router", "node_error", "no output_labels configured", input.Verbosity)
		return RouterActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"router has no output_labels configured", "NoLabels", nil,
		)
	}
	if a.LLMCaller == nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "router", "node_error", "no LLM caller configured", input.Verbosity)
		return RouterActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"router: no LLM caller configured on this worker", "NoLLMCaller", nil,
		)
	}

	prompt := input.ClassifierPrompt
	if prompt == "" {
		prompt = defaultRouterPrompt(input.Labels)
	}

	label, err := a.LLMCaller.ClassifyIntent(ctx, input.UserMessage, prompt, input.Labels, input.LLMProviderName, input.LLMModel, input.TenantID, input.ApplicationID)
	if err != nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "router", "node_error", err.Error(), input.Verbosity)
		return RouterActivityOutput{}, fmt.Errorf("router classify: %w", err)
	}

	// Validate the returned label is in the allowed set.
	for _, l := range input.Labels {
		if strings.EqualFold(l, label) {
			a.emitTrace(ctx, input.RunID, input.NodeID, "router", "node_done", "label="+l, input.Verbosity)
			return RouterActivityOutput{ChosenLabel: l}, nil
		}
	}

	// LLM returned an unknown label — fail explicitly so the caller can react.
	a.emitTrace(ctx, input.RunID, input.NodeID, "router", "node_error", fmt.Sprintf("unknown label %q", label), input.Verbosity)
	return RouterActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
		fmt.Sprintf("router: LLM returned unknown label %q (valid: %v)", label, input.Labels),
		"RouterUnknownLabel", nil,
	)
}

// ExecuteHILActivity persists an HIL approval request to them.hil_approvals and
// returns immediately. The workflow then pauses waiting for the hil_approval signal.
// This activity is idempotent — a duplicate (same run_id + node_id) is silently ignored.
// Only publishes node_start here — the "done" event (approved/rejected) fires
// from execHILNode in nodes.go once the approval signal or timeout resolves,
// since that decision happens in workflow code, after this activity returns.
func (a *AppFlowActivities) ExecuteHILActivity(ctx context.Context, input HILActivityInput) (HILActivityOutput, error) {
	a.emitTrace(ctx, input.RunID, input.NodeID, "hil", "node_start", "", input.Verbosity)

	if a.DB == nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "hil", "node_error", "no DB configured", input.Verbosity)
		return HILActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"hil: no DB configured on AppFlowActivities", "NoDB", nil,
		)
	}
	_, err := a.DB.Exec(ctx,
		`INSERT INTO them.hil_approvals
			(tenant_id, application_id, run_id, node_id, approver_role, prompt, fallback_action)
		 SELECT $1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7
		  WHERE NOT EXISTS (
		    SELECT 1 FROM them.hil_approvals
		     WHERE run_id = $3::uuid AND node_id = $4
		  )`,
		input.TenantID, input.ApplicationID, input.RunID,
		input.NodeID, input.ApproverRole, input.Prompt, input.FallbackAction,
	)
	if err != nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "hil", "node_error", "insert approval request failed", input.Verbosity)
		return HILActivityOutput{}, fmt.Errorf("hil: insert approval request: %w", err)
	}
	return HILActivityOutput{}, nil
}

// InvokeAgentActivity calls an agent by its DB UUID via A2A HTTP.
// The agent endpoint is resolved by the AgentInvoker (which queries DB for the
// agent record and performs the HTTP call). Returns the agent's text response.
func (a *AppFlowActivities) InvokeAgentActivity(ctx context.Context, input AgentInvokeActivityInput) (AgentInvokeActivityOutput, error) {
	a.emitTrace(ctx, input.RunID, input.NodeID, "agent", "node_start", "", input.Verbosity)

	if a.AgentInvoker == nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "agent", "node_error", "no AgentInvoker configured", input.Verbosity)
		return AgentInvokeActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"appflow: no AgentInvoker configured on AppFlowActivities", "NoAgentInvoker", nil,
		)
	}
	if input.AgentID == "" {
		a.emitTrace(ctx, input.RunID, input.NodeID, "agent", "node_error", "empty agent_id", input.Verbosity)
		return AgentInvokeActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			fmt.Sprintf("appflow: node %q has empty agent_id (re-publish the application canvas to stamp IDs)", input.NodeID),
			"EmptyAgentID", nil,
		)
	}
	text, err := a.AgentInvoker.InvokeByID(ctx, input.TenantID, input.ApplicationID, input.AgentID, input.UserMessage)
	if err != nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "agent", "node_error", err.Error(), input.Verbosity)
		return AgentInvokeActivityOutput{}, fmt.Errorf("appflow: invoke agent %s: %w", input.AgentID, err)
	}
	a.emitTrace(ctx, input.RunID, input.NodeID, "agent", "node_done", "", input.Verbosity)
	return AgentInvokeActivityOutput{ResponseText: text}, nil
}

// InlineLLMActivity renders an inline LLM node's prompts and calls the
// configured LLM provider directly (as opposed to Router's label
// classification). Templates are rendered here, not in the workflow, because
// text/template execution is not deterministic-safe workflow code.
func (a *AppFlowActivities) InlineLLMActivity(ctx context.Context, input InlineLLMActivityInput) (InlineLLMActivityOutput, error) {
	a.emitTrace(ctx, input.RunID, input.NodeID, "llm", "node_start", "", input.Verbosity)

	if a.InlineLLM == nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "llm", "node_error", "no InlineLLM caller configured", input.Verbosity)
		return InlineLLMActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"inline llm: no InlineLLM caller configured on this worker", "NoInlineLLMCaller", nil,
		)
	}

	systemPrompt, err := renderFlowTemplate(input.SystemPrompt, input.Vars)
	if err != nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "llm", "node_error", "render system_prompt failed", input.Verbosity)
		return InlineLLMActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			fmt.Sprintf("inline llm %q: render system_prompt: %v", input.NodeID, err),
			"InlineLLMRenderFailed", nil,
		)
	}
	userPrompt, err := renderFlowTemplate(input.UserPrompt, input.Vars)
	if err != nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "llm", "node_error", "render user_prompt failed", input.Verbosity)
		return InlineLLMActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			fmt.Sprintf("inline llm %q: render user_prompt: %v", input.NodeID, err),
			"InlineLLMRenderFailed", nil,
		)
	}

	// Mirrors agentgen execLLM (internal/agentgen/interpreter.go:277-282): an
	// empty rendered user prompt falls back to the accumulated upstream input.
	if userPrompt == "" {
		userPrompt = input.Vars["input"]
	}

	maxTokens := input.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	responseText, err := a.InlineLLM.Complete(ctx, InlineLLMRequest{
		SystemPrompt:  systemPrompt,
		UserPrompt:    userPrompt,
		ProviderName:  input.Provider,
		Model:         input.Model,
		MaxTokens:     maxTokens,
		Temperature:   input.Temperature,
		TenantID:      input.TenantID,
		ApplicationID: input.ApplicationID,
		RunID:         input.RunID,
		NodeID:        input.NodeID,
		Debug:         input.Debug,
	})
	if err != nil {
		a.emitTrace(ctx, input.RunID, input.NodeID, "llm", "node_error", err.Error(), input.Verbosity)
		return InlineLLMActivityOutput{}, fmt.Errorf("inline llm %q: %w", input.NodeID, err)
	}
	a.emitTrace(ctx, input.RunID, input.NodeID, "llm", "node_done", "", input.Verbosity)

	if input.Stream && a.StreamPub != nil && responseText != "" {
		key := fmt.Sprintf("them:dash:run:%s:stream", input.RunID)
		payload := map[string]interface{}{
			"type":    "token",
			"content": responseText,
			"run_id":  input.RunID,
		}
		raw, _ := json.Marshal(payload)
		// A stream-publish failure must NOT fail the activity: the LLM call
		// already succeeded and is not idempotent, so retrying the whole
		// activity to fix a Redis hiccup would re-call (and re-bill) the LLM.
		// This differs from FinalizeRunActivity, which does return XAdd errors
		// — that activity IS idempotent (safe to retry), this one is not.
		_ = a.StreamPub.XAdd(ctx, key, map[string]interface{}{"data": string(raw)})
	}

	return InlineLLMActivityOutput{
		ResponseText: responseText,
		OutputVar:    input.OutputVar,
	}, nil
}

// TraceNodeEventActivity publishes a live node_start/node_done/node_error event
// to the run's Redis Stream (docs/APP_CANVAS_DEBUG_PLAN.md Phase 2). It exists
// so Condition/Fork/Join — which execute entirely in workflow code and do no
// I/O for their real logic — have a determinism-safe way to report what they
// did. Router/HIL/Agent/Inline LLM emit the same events inline from their own
// activities instead of calling this one, since they already do I/O there.
//
// Never fails the caller: a tracing publish failure must not affect node
// execution or trigger a retry (same rule as InlineLLMActivity's token
// streaming — see the comment on that XAdd call).
func (a *AppFlowActivities) TraceNodeEventActivity(ctx context.Context, input TraceEventInput) error {
	a.emitTrace(ctx, input.RunID, input.NodeID, input.Kind, input.EventType, input.Detail, input.Verbosity)
	return nil
}

// emitTrace publishes one node_start/node_done/node_error event and persists
// it to them.run_steps (docs/APP_CANVAS_DEBUG_PLAN.md Phase 3 — durable
// storage; Phase 2 added only the live Redis publish below). Shared by
// TraceNodeEventActivity and the Router/HIL/Agent/Inline LLM activities, which
// call it inline since they already do I/O in this activity. Never returns an
// error — tracing must never affect node execution (Phase 2's rule, extended
// to the DB write here for the same reason).
func (a *AppFlowActivities) emitTrace(ctx context.Context, runID, nodeID, kind, eventType, detail, verbosity string) {
	a.persistTrace(ctx, runID, nodeID, kind, eventType, detail, verbosity)

	if a.StreamPub == nil {
		return
	}
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	payload := map[string]interface{}{
		"type":    eventType,
		"run_id":  runID,
		"node_id": nodeID,
		"kind":    kind,
	}
	if detail != "" {
		payload["detail"] = detail
	}
	raw, _ := json.Marshal(payload)
	_ = a.StreamPub.XAdd(ctx, key, map[string]interface{}{"data": string(raw)})
}

// persistTrace upserts a them.run_steps row for one AppFlow DAG node
// execution, keyed on (run_id, node_id) (unique partial index added by
// db/103_run_steps_appflow_trace.sql). node_start inserts a "running" row;
// node_done/node_error update that same row to its terminal status, mirroring
// the insert-then-update lifecycle orchestrator-mode steps already use.
// Iteration is always 0 (not-applicable sentinel — a DAG node has no loop
// iteration). No-op if a.DB is nil (matches every other activity's nil-DB
// guard); errors are logged-and-discarded, never surfaced to the caller,
// since a.DB is the BYPASSRLS Admin pool and this must never fail the node
// execution it describes.
//
// verbosity gates how much is written (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4):
//   - "off": no write at all — the live Redis publish in emitTrace is
//     unaffected, only durable persistence is skipped.
//   - "status": the row is written/updated, but output/error detail is never
//     stored (NULL) — same shape whether the node succeeded or failed.
//   - "full" or empty (fail-open default when a caller predates this field,
//     e.g. an in-flight workflow started before this phase deployed):
//     today's behavior — output/error detail included.
func (a *AppFlowActivities) persistTrace(ctx context.Context, runID, nodeID, kind, eventType, detail, verbosity string) {
	if a.DB == nil || nodeID == "" || verbosity == "off" {
		return
	}
	switch eventType {
	case "node_start":
		const q = `
			INSERT INTO them.run_steps (run_id, iteration, node_id, node_kind, status, started_at)
			VALUES ($1::uuid, 0, $2, $3, 'running', now())
			ON CONFLICT (run_id, node_id) WHERE node_id IS NOT NULL
			DO UPDATE SET node_kind = EXCLUDED.node_kind, status = 'running', started_at = now(),
				ended_at = NULL, error = NULL`
		_, _ = a.DB.Exec(ctx, q, runID, nodeID, kind)
	case "node_done", "node_error":
		status := "completed"
		if eventType == "node_error" {
			status = "failed"
		}
		const q = `
			UPDATE them.run_steps
			SET status = $3, output = NULLIF($4, ''), error = NULLIF($5, ''),
				ended_at = now(),
				latency_ms = EXTRACT(EPOCH FROM (now() - started_at))::integer * 1000
			WHERE run_id = $1::uuid AND node_id = $2`
		var output, errMsg string
		if verbosity != "status" {
			if eventType == "node_error" {
				errMsg = detail
			} else {
				output = detail
			}
		}
		_, _ = a.DB.Exec(ctx, q, runID, nodeID, status, output, errMsg)
	}
}

// FinalizeRunActivity updates the run's DB status and publishes a terminal event
// to the Redis Stream so connected WS/SSE clients receive the "done" or "error" frame.
// This activity is idempotent — safe to retry.
func (a *AppFlowActivities) FinalizeRunActivity(ctx context.Context, input FinalizeRunActivityInput) error {
	var domainStatus domain.RunStatus
	var evType string
	switch input.Status {
	case "completed":
		domainStatus = domain.RunCompleted
		evType = "done"
	case "rejected":
		domainStatus = domain.RunFailed
		evType = "error"
	default:
		domainStatus = domain.RunFailed
		evType = "error"
	}

	errMsg := input.ErrMsg
	if input.Status == "rejected" && errMsg == "" {
		errMsg = "rejected by HIL gate"
	}

	if a.StatusUpdater != nil {
		if err := a.StatusUpdater.UpdateRunStatus(ctx, input.RunID, domainStatus, errMsg); err != nil {
			return fmt.Errorf("finalize: update run status: %w", err)
		}
	}

	if a.StreamPub != nil {
		key := fmt.Sprintf("them:dash:run:%s:stream", input.RunID)
		var payload map[string]interface{}
		if evType == "done" {
			payload = map[string]interface{}{
				"type":       "done",
				"run_id":     input.RunID,
				"final_text": input.FinalText,
			}
		} else {
			payload = map[string]interface{}{
				"type":    "error",
				"run_id":  input.RunID,
				"message": errMsg,
			}
		}
		raw, _ := json.Marshal(payload)
		if err := a.StreamPub.XAdd(ctx, key, map[string]interface{}{"data": string(raw)}); err != nil {
			return fmt.Errorf("finalize: publish stream event: %w", err)
		}
	}
	return nil
}

// defaultRouterPrompt generates a default system prompt for the router classifier.
func defaultRouterPrompt(labels []string) string {
	quoted := make([]string, len(labels))
	for i, l := range labels {
		quoted[i] = fmt.Sprintf("%q", l)
	}
	return fmt.Sprintf(
		"You are an intent classifier. Based on the user message, choose exactly one of these labels: %s.\n"+
			"Reply with only the label, nothing else.",
		strings.Join(quoted, ", "),
	)
}
