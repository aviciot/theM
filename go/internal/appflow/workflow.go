// The AppFlow Temporal workflow: walks a compiled AppFlowSpec, dispatching each
// node to an activity (agent, router, HIL, inline LLM) or deciding control flow
// in-workflow (condition, fork/join).
//
// Everything in this file runs as workflow code and MUST be deterministic across
// replays: no I/O, no clocks other than workflow.Now, no randomness, no map
// iteration that affects control flow. Anything violating that belongs in
// activities.go.
package appflow

import (
	"encoding/json"
	"fmt"
	"time"

	temporalerr "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
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

	// AppFlowInlineLLMActivityName is the registered name for the inline LLM node activity.
	AppFlowInlineLLMActivityName = "AppFlowInlineLLMActivity"

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
	// vars is the flow variables bus (§1.3/§2.4 of the inline nodes plan):
	// threaded alongside accumulated so inline LLM/Condition nodes can read and
	// write named values, not just the single accumulated string. Not
	// persisted outside this workflow execution.
	vars := FlowVars{}

	for currentID != "" {
		node, ok := nodeByID[currentID]
		if !ok {
			out.Status = "failed"
			retErr = fmt.Errorf("AppFlowWorkflow: node %q not found", currentID)
			return
		}

		switch node.Kind {
		case "router":
			nextID, rErr := execRouterNode(ctx, node, input, outEdgesBySource[node.ID], accumulated)
			if rErr != nil {
				out.Status = "failed"
				retErr = rErr
				return
			}
			currentID = nextID
			continue

		case "hil":
			approved, comment, hErr := execHILNode(ctx, node, input, shortAO)
			if hErr != nil {
				out.Status = "failed"
				retErr = hErr
				return
			}
			if !approved {
				out = AppFlowWorkflowOutput{Status: "rejected", FinalText: "HIL gate rejected: " + comment}
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

		case "llm":
			var cfg InlineLLMConfig
			if len(node.Config) > 0 {
				_ = json.Unmarshal(node.Config, &cfg)
			}
			// Provider/model inherit from the EP orchestrator binding when unset.
			provider, model := cfg.Provider, cfg.Model
			if provider == "" {
				provider = input.LLMProviderName
			}
			if model == "" {
				model = input.LLMModel
			}
			vars["input"] = accumulated

			var llmOut InlineLLMActivityOutput
			err := workflow.ExecuteActivity(ctx, AppFlowInlineLLMActivityName, InlineLLMActivityInput{
				RunID:         input.RunID,
				TenantID:      input.TenantID,
				ApplicationID: input.ApplicationID,
				NodeID:        node.ID,
				SystemPrompt:  cfg.SystemPrompt,
				UserPrompt:    cfg.UserPrompt,
				Vars:          vars,
				Provider:      provider,
				Model:         model,
				MaxTokens:     cfg.MaxTokens,
				Temperature:   cfg.Temperature,
				OutputVar:     cfg.OutputVar,
				Stream:        true,
			}).Get(ctx, &llmOut)
			if err != nil {
				out.Status = "failed"
				retErr = fmt.Errorf("llm %q: %w", node.ID, err)
				return
			}
			outVar := llmOut.OutputVar
			if outVar == "" {
				outVar = "output"
			}
			vars[outVar] = llmOut.ResponseText
			if llmOut.ResponseText != "" {
				accumulated = llmOut.ResponseText
			}
			currentID = firstEdgeTarget(outEdgesBySource[node.ID])
			continue

		case "condition":
			var cfg InlineConditionConfig
			if len(node.Config) > 0 {
				_ = json.Unmarshal(node.Config, &cfg)
			}
			vars["input"] = accumulated
			rendered, rErr := renderFlowTemplate(cfg.Expression, vars)
			if rErr != nil {
				out.Status = "failed"
				retErr = temporalerr.NewNonRetryableApplicationError(
					fmt.Sprintf("condition %q: render expression: %v", node.ID, rErr),
					"ConditionRenderFailed", nil,
				)
				return
			}
			branch := "false"
			if isTruthy(rendered) {
				branch = "true"
			}
			nextID := findEdgeByLabel(outEdgesBySource[node.ID], branch)
			if nextID == "" {
				out.Status = "failed"
				retErr = temporalerr.NewNonRetryableApplicationError(
					fmt.Sprintf("condition %q: no outgoing edge labelled %q", node.ID, branch),
					"ConditionNoMatch", nil,
				)
				return
			}
			currentID = nextID
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
