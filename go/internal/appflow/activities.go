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
}

// AgentInvokeActivityOutput is returned by AppFlowInvokeAgentActivity.
type AgentInvokeActivityOutput struct {
	// ResponseText is the agent's plain-text reply.
	ResponseText string `json:"response_text"`
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

// ExecuteRouterActivity calls an LLM to classify the user message and returns
// the matching intent label from the router's output_labels list.
func (a *AppFlowActivities) ExecuteRouterActivity(ctx context.Context, input RouterActivityInput) (RouterActivityOutput, error) {
	if len(input.Labels) == 0 {
		return RouterActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"router has no output_labels configured", "NoLabels", nil,
		)
	}
	if a.LLMCaller == nil {
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
		return RouterActivityOutput{}, fmt.Errorf("router classify: %w", err)
	}

	// Validate the returned label is in the allowed set.
	for _, l := range input.Labels {
		if strings.EqualFold(l, label) {
			return RouterActivityOutput{ChosenLabel: l}, nil
		}
	}

	// LLM returned an unknown label — fail explicitly so the caller can react.
	return RouterActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
		fmt.Sprintf("router: LLM returned unknown label %q (valid: %v)", label, input.Labels),
		"RouterUnknownLabel", nil,
	)
}

// ExecuteHILActivity persists an HIL approval request to them.hil_approvals and
// returns immediately. The workflow then pauses waiting for the hil_approval signal.
// This activity is idempotent — a duplicate (same run_id + node_id) is silently ignored.
func (a *AppFlowActivities) ExecuteHILActivity(ctx context.Context, input HILActivityInput) (HILActivityOutput, error) {
	if a.DB == nil {
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
		return HILActivityOutput{}, fmt.Errorf("hil: insert approval request: %w", err)
	}
	return HILActivityOutput{}, nil
}

// InvokeAgentActivity calls an agent by its DB UUID via A2A HTTP.
// The agent endpoint is resolved by the AgentInvoker (which queries DB for the
// agent record and performs the HTTP call). Returns the agent's text response.
func (a *AppFlowActivities) InvokeAgentActivity(ctx context.Context, input AgentInvokeActivityInput) (AgentInvokeActivityOutput, error) {
	if a.AgentInvoker == nil {
		return AgentInvokeActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"appflow: no AgentInvoker configured on AppFlowActivities", "NoAgentInvoker", nil,
		)
	}
	if input.AgentID == "" {
		return AgentInvokeActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			fmt.Sprintf("appflow: node %q has empty agent_id (re-publish the application canvas to stamp IDs)", input.NodeID),
			"EmptyAgentID", nil,
		)
	}
	text, err := a.AgentInvoker.InvokeByID(ctx, input.TenantID, input.ApplicationID, input.AgentID, input.UserMessage)
	if err != nil {
		return AgentInvokeActivityOutput{}, fmt.Errorf("appflow: invoke agent %s: %w", input.AgentID, err)
	}
	return AgentInvokeActivityOutput{ResponseText: text}, nil
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
