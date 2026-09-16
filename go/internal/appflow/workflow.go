package appflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	temporalerr "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/aviciot/them/internal/domain"
)

const (
	// AppFlowTaskQueue is the Temporal task queue polled by dag-worker for AppFlowWorkflow.
	AppFlowTaskQueue = "appflow-dag"

	// AppFlowWorkflowType is the registered workflow type name.
	AppFlowWorkflowType = "AppFlowWorkflow"

	// AppFlowExecuteRouterActivityName is the registered name for the Router activity.
	AppFlowExecuteRouterActivityName = "AppFlowExecuteRouterActivity"

	// AppFlowExecuteHILActivityName is the registered name for the HIL activity.
	AppFlowExecuteHILActivityName = "AppFlowExecuteHILActivity"

	// AppFlowFinalizeRunActivityName is the registered name for the finalize activity.
	AppFlowFinalizeRunActivityName = "AppFlowFinalizeRunActivity"

	// AppFlowSignalHILApproval is the signal name for HIL human approval.
	AppFlowSignalHILApproval = "hil_approval"

	appFlowActivityTimeout = 10 * time.Minute
	appFlowHILTimeout      = 24 * time.Hour // HIL can wait up to 24h before fallback
)

// WorkflowIDForRun returns a deterministic Temporal workflow ID for an AppFlowWorkflow
// given the tenant UUID and run UUID. Format: "appflow:{tenantID}:{runID}".
// Using run_id (unique per execution) allows the same context to execute multiple
// runs without conflicting on Temporal's unique workflow-ID constraint.
func WorkflowIDForRun(tenantID, runID string) string {
	return "appflow:" + tenantID + ":" + runID
}

// ── Workflow types ─────────────────────────────────────────────────────────────

// AppFlowWorkflowInput is the input to AppFlowWorkflow.
type AppFlowWorkflowInput struct {
	// RunID is the them.runs.id for this execution.
	RunID string `json:"run_id"`
	// TenantID is the tenant owning this run.
	TenantID string `json:"tenant_id"`
	// ApplicationID is the application being executed.
	ApplicationID string `json:"application_id"`
	// EntryPointSlug identifies which EP flow to execute.
	EntryPointSlug string `json:"entry_point_slug"`
	// Spec is the compiled AppFlowSpec.
	Spec *AppFlowSpec `json:"spec"`
	// UserMessage is the initial input from the caller.
	UserMessage string `json:"user_message"`
	// LLMProviderName is the provider name (e.g. "anthropic") for Router classification.
	// The activity resolves the API key from the DB at execution time (never stored in history).
	LLMProviderName string `json:"llm_provider_name,omitempty"`
	// LLMProvider is the provider name ("anthropic", "openai", etc.).
	LLMProvider string `json:"llm_provider,omitempty"`
	// LLMModel is the model to use for Router classification.
	LLMModel string `json:"llm_model,omitempty"`
	// UserID is the the-M user ID (0 when not a dashboard user).
	UserID int64 `json:"user_id,omitempty"`
	// ExternalUserID is the caller-supplied external user identifier.
	ExternalUserID string `json:"external_user_id,omitempty"`
}

// AppFlowWorkflowOutput is returned by AppFlowWorkflow on completion.
type AppFlowWorkflowOutput struct {
	FinalText string `json:"final_text"`
	Status    string `json:"status"` // "completed" | "failed" | "rejected"
}

// HILApprovalPayload is the signal payload for HIL approval/rejection.
type HILApprovalPayload struct {
	// Approved is true when the human approved, false when rejected.
	Approved bool `json:"approved"`
	// Comment is an optional human-provided note.
	Comment string `json:"comment,omitempty"`
}

// ── Activity input/output types ────────────────────────────────────────────────

// RouterActivityInput is the input to AppFlowExecuteRouterActivity.
type RouterActivityInput struct {
	RunID         string   `json:"run_id"`
	TenantID      string   `json:"tenant_id"`
	ApplicationID string   `json:"application_id"`
	NodeID        string   `json:"node_id"`
	UserMessage   string   `json:"user_message"`
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
	Status    string `json:"status"`              // "completed" | "failed" | "rejected"
	FinalText string `json:"final_text,omitempty"`
	ErrMsg    string `json:"err_msg,omitempty"`
}

// ── Workflow ──────────────────────────────────────────────────────────────────

// AppFlowWorkflow executes an application canvas AppFlowSpec as a Temporal workflow.
//
// Execution model per EPFlow:
//   - Walk nodes in topological order from start_id.
//   - Agent nodes: rejected with a non-retryable error (direct invocation not yet implemented).
//   - Router nodes: ExecuteRouterActivity classifies the user message → chooses an outgoing label.
//   - HIL nodes: ExecuteHILActivity persists an approval request; workflow pauses on signal.
//   - Missing outgoing edges on a router → non-retryable error.
//   - FinalizeRunActivity runs via defer on every exit path (including workflow cancellation)
//     using workflow.NewDisconnectedContext so it executes even when ctx is cancelled.
func AppFlowWorkflow(ctx workflow.Context, input AppFlowWorkflowInput) (out AppFlowWorkflowOutput, retErr error) {
	// Shared activity options for short-lived finalize/HIL activities.
	shortAO := workflow.ActivityOptions{
		TaskQueue:           AppFlowTaskQueue,
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporalerr.RetryPolicy{MaximumAttempts: 3},
	}

	// defer runs FinalizeRunActivity on a disconnected context so it executes on
	// every exit path: normal completion, error returns, and workflow cancellation.
	defer func() {
		status := out.Status
		if status == "" {
			status = "failed"
		}
		errMsg := ""
		if retErr != nil {
			errMsg = retErr.Error()
		}
		dCtx, cancel := workflow.NewDisconnectedContext(ctx)
		defer cancel()
		fCtx := workflow.WithActivityOptions(dCtx, shortAO)
		_ = workflow.ExecuteActivity(fCtx, AppFlowFinalizeRunActivityName, FinalizeRunActivityInput{
			RunID:     input.RunID,
			TenantID:  input.TenantID,
			Status:    status,
			FinalText: out.FinalText,
			ErrMsg:    errMsg,
		}).Get(fCtx, nil)
	}()

	if input.Spec == nil {
		out.Status = "failed"
		retErr = temporalerr.NewNonRetryableApplicationError(
			"AppFlowWorkflow: spec is nil",
			"InvalidInput", nil,
		)
		return
	}
	if input.TenantID == "" || input.ApplicationID == "" || input.RunID == "" {
		out.Status = "failed"
		retErr = temporalerr.NewNonRetryableApplicationError(
			"AppFlowWorkflow: TenantID, ApplicationID, and RunID must be non-empty",
			"InvalidInput", nil,
		)
		return
	}

	// Find the target EPFlow.
	var epFlow *EPFlow
	for i := range input.Spec.EntryPoints {
		if input.Spec.EntryPoints[i].Slug == input.EntryPointSlug {
			epFlow = &input.Spec.EntryPoints[i]
			break
		}
	}
	if epFlow == nil {
		out.Status = "failed"
		retErr = temporalerr.NewNonRetryableApplicationError(
			fmt.Sprintf("AppFlowWorkflow: entry point %q not found in spec", input.EntryPointSlug),
			"NotFound", nil,
		)
		return
	}

	// Build node index.
	nodeByID := make(map[string]*AppFlowNode, len(epFlow.Nodes))
	for i := range epFlow.Nodes {
		n := &epFlow.Nodes[i]
		nodeByID[n.ID] = n
	}

	// Build outgoing edges index.
	outEdgesBySource := make(map[string][]AppFlowEdge)
	for _, e := range epFlow.Edges {
		outEdgesBySource[e.Source] = append(outEdgesBySource[e.Source], e)
	}

	// Walk nodes sequentially from start_id.
	// Fan-out (parallel agent calls) is future work; for now we follow the first
	// outgoing edge for non-router nodes.
	ao := workflow.ActivityOptions{
		TaskQueue:           AppFlowTaskQueue,
		StartToCloseTimeout: appFlowActivityTimeout,
		RetryPolicy: &temporalerr.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	currentID := epFlow.StartID
	accumulated := input.UserMessage

	for currentID != "" {
		node, ok := nodeByID[currentID]
		if !ok {
			out.Status = "failed"
			retErr = fmt.Errorf("AppFlowWorkflow: node %q not found", currentID)
			return
		}

		switch node.Kind {
		case "router":
			// Parse router config.
			var cfg RouterConfig
			if len(node.Config) > 0 {
				_ = json.Unmarshal(node.Config, &cfg)
			}

			var routerOut RouterActivityOutput
			err := workflow.ExecuteActivity(ctx, AppFlowExecuteRouterActivityName, RouterActivityInput{
				RunID:            input.RunID,
				TenantID:         input.TenantID,
				ApplicationID:    input.ApplicationID,
				NodeID:           node.ID,
				UserMessage:      accumulated,
				Labels:           cfg.OutputLabels,
				ClassifierPrompt: cfg.ClassifierPrompt,
				LLMProviderName:  input.LLMProviderName,
				LLMProvider:      input.LLMProvider,
				LLMModel:         input.LLMModel,
			}).Get(ctx, &routerOut)
			if err != nil {
				out.Status = "failed"
				retErr = fmt.Errorf("router %q: %w", node.ID, err)
				return
			}

			// Find outgoing edge matching the chosen label.
			nextID := findEdgeByLabel(outEdgesBySource[node.ID], routerOut.ChosenLabel)
			if nextID == "" {
				// No matching edge — also try taking any edge as fallback if there's exactly one.
				edges := outEdgesBySource[node.ID]
				if len(edges) == 1 {
					nextID = edges[0].Target
				} else {
					out.Status = "failed"
					retErr = temporalerr.NewNonRetryableApplicationError(
						fmt.Sprintf("router %q: no outgoing edge matches label %q", node.ID, routerOut.ChosenLabel),
						"RouterNoMatch", nil,
					)
					return
				}
			}
			currentID = nextID
			continue

		case "hil":
			// Parse HIL config.
			var cfg HILConfig
			if len(node.Config) > 0 {
				_ = json.Unmarshal(node.Config, &cfg)
			}
			if cfg.FallbackAction == "" {
				cfg.FallbackAction = "reject"
			}
			if cfg.ApproverRole == "" {
				cfg.ApproverRole = "admin"
			}

			// HIL: pause workflow. The HIL activity persists the approval request
			// and returns immediately. Then the workflow waits for the signal.
			var hilOut HILActivityOutput

			hilCtx := workflow.WithActivityOptions(ctx, shortAO)
			err := workflow.ExecuteActivity(hilCtx, AppFlowExecuteHILActivityName, HILActivityInput{
				RunID:          input.RunID,
				TenantID:       input.TenantID,
				ApplicationID:  input.ApplicationID,
				NodeID:         node.ID,
				ApproverRole:   cfg.ApproverRole,
				Prompt:         cfg.Prompt,
				TimeoutSecs:    cfg.TimeoutSeconds,
				FallbackAction: cfg.FallbackAction,
			}).Get(hilCtx, &hilOut)
			if err != nil {
				out.Status = "failed"
				retErr = fmt.Errorf("hil %q: persist: %w", node.ID, err)
				return
			}

			// Wait for signal (approval or rejection).
			var approval HILApprovalPayload
			signalName := AppFlowSignalHILApproval + ":" + node.ID

			if cfg.TimeoutSeconds > 0 {
				timer := workflow.NewTimer(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
				sigCh := workflow.GetSignalChannel(ctx, signalName)
				workflow.NewSelector(ctx).
					AddFuture(timer, func(f workflow.Future) {
						// Timer fired — apply fallback.
						switch cfg.FallbackAction {
						case "approve":
							approval.Approved = true
							approval.Comment = "timeout-auto-approved"
						default:
							approval.Approved = false
							approval.Comment = "timeout-rejected"
						}
					}).
					AddReceive(sigCh, func(ch workflow.ReceiveChannel, more bool) {
						ch.Receive(ctx, &approval)
					}).
					Select(ctx)
			} else {
				// Wait indefinitely.
				workflow.GetSignalChannel(ctx, signalName).Receive(ctx, &approval)
			}

			if !approval.Approved {
				rejMsg := "HIL gate rejected: " + approval.Comment
				out = AppFlowWorkflowOutput{Status: "rejected", FinalText: rejMsg}
				return
			}

			// Approved — continue to next node.
			currentID = firstEdgeTarget(outEdgesBySource[node.ID])
			continue

		case "agent", "orchestrator":
			// Direct agent invocation within AppFlowWorkflow is not yet implemented.
			// Return a non-retryable error so the run fails explicitly rather than
			// silently producing a "completed" result with no agent output.
			out.Status = "failed"
			retErr = temporalerr.NewNonRetryableApplicationError(
				fmt.Sprintf("AppFlowWorkflow: agent/orchestrator node %q requires direct invocation which is not yet implemented; use OrchestrationWorkflow for agent execution", node.ID),
				"AgentInvocationNotImplemented", nil,
			)
			return

		case "middleware":
			// Middleware nodes affect agent calls but are not directly executed here.
			currentID = firstEdgeTarget(outEdgesBySource[node.ID])
			continue

		default:
			out.Status = "failed"
			retErr = temporalerr.NewNonRetryableApplicationError(
				fmt.Sprintf("AppFlowWorkflow: unknown node kind %q at %q", node.Kind, node.ID),
				"UnknownNodeKind", nil,
			)
			return
		}
	}

	out = AppFlowWorkflowOutput{Status: "completed", FinalText: accumulated}
	return
}

// ── Activity stubs ────────────────────────────────────────────────────────────

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

// ── Helpers ───────────────────────────────────────────────────────────────────

// findEdgeByLabel returns the Target of the first edge whose Label matches label
// (case-insensitive). Returns "" if no match.
func findEdgeByLabel(edges []AppFlowEdge, label string) string {
	for _, e := range edges {
		if strings.EqualFold(e.Label, label) {
			return e.Target
		}
	}
	return ""
}

// firstEdgeTarget returns the Target of the first edge, or "" if there are none.
func firstEdgeTarget(edges []AppFlowEdge) string {
	if len(edges) == 0 {
		return ""
	}
	return edges[0].Target
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
