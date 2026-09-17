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

	// AppFlowInvokeAgentActivityName is the registered name for the agent invocation activity.
	AppFlowInvokeAgentActivityName = "AppFlowInvokeAgentActivity"

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
	// TemporalCfg holds execution controls (timeouts, retry policy).
	// Nil = use hardcoded defaults (fail-open: never blocks a workflow).
	TemporalCfg *TemporalExecCfg `json:"temporal_cfg,omitempty"`
}

// TemporalExecCfg carries Temporal execution controls resolved at workflow submit time.
// Nil fields fall back to hardcoded defaults inside AppFlowWorkflow (fail-open).
type TemporalExecCfg struct {
	// WorkflowTimeoutS is set via StartWorkflowOptions.WorkflowRunTimeout by the caller.
	// Stored here for reference; not applied inside the workflow function.
	WorkflowTimeoutS *int `json:"workflow_timeout_s,omitempty"`
	// ActivityTimeoutS overrides the default StartToCloseTimeout for all activities.
	ActivityTimeoutS *int `json:"activity_timeout_s,omitempty"`
	// RetryMaxAttempts overrides the retry policy MaximumAttempts for all activities.
	RetryMaxAttempts *int `json:"retry_max_attempts,omitempty"`
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

	// Resolve execution controls (fail-open: hardcoded defaults when TemporalCfg is nil).
	actTimeout := appFlowActivityTimeout
	retryMax := int32(2)
	if input.TemporalCfg != nil {
		if input.TemporalCfg.ActivityTimeoutS != nil && *input.TemporalCfg.ActivityTimeoutS > 0 {
			actTimeout = time.Duration(*input.TemporalCfg.ActivityTimeoutS) * time.Second
		}
		if input.TemporalCfg.RetryMaxAttempts != nil && *input.TemporalCfg.RetryMaxAttempts >= 0 {
			retryMax = int32(*input.TemporalCfg.RetryMaxAttempts)
		}
	}

	// Walk nodes from start_id. Fork/Join enables parallel branches.
	ao := workflow.ActivityOptions{
		TaskQueue:           AppFlowTaskQueue,
		StartToCloseTimeout: actTimeout,
		RetryPolicy: &temporalerr.RetryPolicy{
			MaximumAttempts: retryMax,
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

		case "agent":
			// Call agent via A2A HTTP through InvokeAgentActivity.
			var agentOut AgentInvokeActivityOutput
			err := workflow.ExecuteActivity(ctx, AppFlowInvokeAgentActivityName, AgentInvokeActivityInput{
				RunID:         input.RunID,
				TenantID:      input.TenantID,
				ApplicationID: input.ApplicationID,
				NodeID:        node.ID,
				AgentID:       node.AgentID,
				UserMessage:   accumulated,
			}).Get(ctx, &agentOut)
			if err != nil {
				out.Status = "failed"
				retErr = fmt.Errorf("agent %q: %w", node.ID, err)
				return
			}
			// Accumulate the agent's response for downstream nodes.
			if agentOut.ResponseText != "" {
				accumulated = agentOut.ResponseText
			}
			currentID = firstEdgeTarget(outEdgesBySource[node.ID])
			continue

		case "fork":
			branches := outEdgesBySource[node.ID]
			branchResults := make([]string, len(branches))
			wg := workflow.NewWaitGroup(ctx)
			// Find the join node that all branches converge on.
			// Each branch walks until it hits a join node, then stops.
			joinID := findJoinNode(branches, nodeByID, outEdgesBySource)
			for i, branch := range branches {
				i, startNodeID := i, branch.Target
				wg.Add(1)
				workflow.Go(ctx, func(gCtx workflow.Context) {
					branchResult, branchErr := walkBranch(gCtx, startNodeID, joinID, nodeByID, outEdgesBySource, input, accumulated, ao, shortAO)
					if branchErr != nil {
						// Store error text as result; main goroutine will detect via out.Status.
						branchResults[i] = ""
					} else {
						branchResults[i] = branchResult
					}
					wg.Done()
				})
			}
			wg.Wait(ctx)
			// Merge branch results: concatenate non-empty results.
			merged := mergeBranchResults(branchResults)
			if merged != "" {
				accumulated = merged
			}
			// Continue from the join node's outgoing edge.
			if joinID != "" {
				currentID = firstEdgeTarget(outEdgesBySource[joinID])
			} else {
				currentID = ""
			}
			continue

		case "join":
			// Join nodes are consumed inside walkBranch; reaching one in the main
			// loop means a bare join with no preceding fork — treat as pass-through.
			currentID = firstEdgeTarget(outEdgesBySource[node.ID])
			continue

		case "orchestrator":
			// Orchestrator nodes act as pass-through routing containers in the app canvas.
			// The actual agent invocation happens at the agent leaf nodes.
			currentID = firstEdgeTarget(outEdgesBySource[node.ID])
			continue

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

// ── Helpers ───────────────────────────────────────────────────────────────────

// findJoinNode walks the outgoing branches of a fork to locate the first join node
// reachable from any branch. All branches in a well-formed canvas converge on the same join.
func findJoinNode(branches []AppFlowEdge, nodeByID map[string]*AppFlowNode, outEdges map[string][]AppFlowEdge) string {
	for _, branch := range branches {
		cur := branch.Target
		visited := make(map[string]bool)
		for cur != "" && !visited[cur] {
			visited[cur] = true
			n, ok := nodeByID[cur]
			if !ok {
				break
			}
			if n.Kind == "join" {
				return cur
			}
			cur = firstEdgeTarget(outEdges[cur])
		}
	}
	return ""
}

// walkBranch executes nodes along a single fork branch starting at startID,
// stopping when it reaches stopID (the join node) or a dead end.
// Returns the final accumulated text for this branch.
func walkBranch(
	ctx workflow.Context,
	startID, stopID string,
	nodeByID map[string]*AppFlowNode,
	outEdges map[string][]AppFlowEdge,
	input AppFlowWorkflowInput,
	initialMsg string,
	ao, shortAO workflow.ActivityOptions,
) (string, error) {
	accumulated := initialMsg
	curID := startID
	for curID != "" && curID != stopID {
		node, ok := nodeByID[curID]
		if !ok {
			return accumulated, fmt.Errorf("branch: node %q not found", curID)
		}
		switch node.Kind {
		case "agent":
			var agentOut AgentInvokeActivityOutput
			agentCtx := workflow.WithActivityOptions(ctx, ao)
			err := workflow.ExecuteActivity(agentCtx, AppFlowInvokeAgentActivityName, AgentInvokeActivityInput{
				RunID:         input.RunID,
				TenantID:      input.TenantID,
				ApplicationID: input.ApplicationID,
				NodeID:        node.ID,
				AgentID:       node.AgentID,
				UserMessage:   accumulated,
			}).Get(agentCtx, &agentOut)
			if err != nil {
				return accumulated, fmt.Errorf("branch agent %q: %w", node.ID, err)
			}
			if agentOut.ResponseText != "" {
				accumulated = agentOut.ResponseText
			}
			curID = firstEdgeTarget(outEdges[node.ID])
		case "orchestrator", "middleware":
			curID = firstEdgeTarget(outEdges[node.ID])
		case "join":
			// Reached join — stop this branch.
			return accumulated, nil
		default:
			return accumulated, fmt.Errorf("branch: unsupported node kind %q at %q", node.Kind, node.ID)
		}
	}
	return accumulated, nil
}

// mergeBranchResults concatenates non-empty branch results with a newline separator.
func mergeBranchResults(results []string) string {
	var parts []string
	for _, r := range results {
		if r != "" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, "\n")
}

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
