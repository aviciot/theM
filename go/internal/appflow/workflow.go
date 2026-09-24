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

	// AppFlowDebugTaskQueue is the Temporal task queue polled by the isolated debug
	// worker pool (them-dag-worker-debug) for AppFlowWorkflow runs started with
	// debug=true. Same workflow/activity code as AppFlowTaskQueue — isolation is by
	// queue only, so debug traffic never competes with or risks production traffic.
	// See docs/APP_CANVAS_DEBUG_PLAN.md Phase 1.
	AppFlowDebugTaskQueue = "appflow-dag-debug"

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

	// AppFlowTraceNodeEventActivityName is the registered name for the live
	// node_start/node_done/node_error trace-publish activity (Phase 2 of
	// docs/APP_CANVAS_DEBUG_PLAN.md). Used by Condition/Fork/Join, which have
	// no activity of their own for their real logic.
	AppFlowTraceNodeEventActivityName = "AppFlowTraceNodeEventActivity"

	// AppFlowSignalHILApproval is the signal name for HIL human approval.
	AppFlowSignalHILApproval = "hil_approval"

	// AppFlowSignalStep is the signal name for the debug "Step" control
	// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6). One signal = one tick: every
	// currently-paused node (including every node in every currently-active
	// fork branch) advances by exactly one node, then pauses again. See
	// stepGate's doc comment for why this is a shared-counter design rather
	// than one signal per paused branch.
	AppFlowSignalStep = "appflow_step"

	appFlowActivityTimeout = 10 * time.Minute
	appFlowHILTimeout      = 24 * time.Hour // HIL can wait up to 24h before fallback

	// DebugRunMaxLifetime is the enforced ceiling on a debug run's total
	// wall-clock execution time (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md) — set as
	// this workflow's WorkflowRunTimeout for debug runs only (production runs
	// are unaffected; they use the app's own TemporalCfg.WorkflowTimeoutS or
	// no limit at all). Sized from the actual worst case, not a round number:
	//
	//   per-node worst case = appFlowActivityTimeout (10m) × retryMax (2)
	//                       + backoff (~10s, negligible) ≈ 20.2 min/node
	//   10-node canvas worst case ≈ 202 min ≈ 3h22m
	//   + a queue-wait allowance (~5 min, for Temporal scheduling latency
	//     under a busy debug worker pool — not separately measured, a
	//     deliberate buffer)
	//   → rounds up to 3h30m
	//
	// "10 nodes" is this package's own assumption for a realistic debug
	// canvas, not an enforced cap — nothing in appflow/validate.go limits
	// node count. A canvas with more sequential LLM nodes than that, where
	// EVERY node also exhausts every retry, could still be killed by this
	// timeout before finishing — an explicit, documented tradeoff, not an
	// oversight. HIL nodes are NOT counted here: a debug run parked at a HIL
	// gate consumes no LLM credential while waiting, so it doesn't factor
	// into the credential-retention math this bound exists to serve (see
	// debugcred.TTL, which is derived FROM this constant).
	//
	// docs/APPFLOW_RUNTIME_PARAMS_PLAN.md's own review corrected an earlier,
	// wrong assumption that appFlowActivityTimeout was 120s — it is 10m.
	DebugRunMaxLifetime = 3*time.Hour + 30*time.Minute
)

// WorkflowIDForRun returns a deterministic Temporal workflow ID for an AppFlowWorkflow
// given the tenant UUID and run UUID. Format: "appflow:{tenantID}:{runID}".
// Using run_id (unique per execution) allows the same context to execute multiple
// runs without conflicting on Temporal's unique workflow-ID constraint.
func WorkflowIDForRun(tenantID, runID string) string {
	return "appflow:" + tenantID + ":" + runID
}

// activityTaskQueueFor returns AppFlowDebugTaskQueue when debug is true, else
// AppFlowTaskQueue. Extracted as a pure function so the selection logic can be
// unit-tested without spinning up a Temporal workflow test environment.
func activityTaskQueueFor(debug bool) string {
	if debug {
		return AppFlowDebugTaskQueue
	}
	return AppFlowTaskQueue
}

// traceNode fires a node_start/node_done/node_error event via
// AppFlowTraceNodeEventActivity. Used only by Condition/Fork/Join, which do no
// I/O of their own (docs/APP_CANVAS_DEBUG_PLAN.md Phase 2). Always emitted,
// never persisted, never allowed to fail the node it describes — the .Get
// error is deliberately discarded, matching TraceNodeEventActivity's own
// never-fail contract.
func traceNode(ctx workflow.Context, runID, nodeID, kind, eventType, detail, verbosity string) {
	_ = workflow.ExecuteActivity(ctx, AppFlowTraceNodeEventActivityName, TraceEventInput{
		RunID:     runID,
		NodeID:    nodeID,
		Kind:      kind,
		EventType: eventType,
		Detail:    detail,
		Verbosity: verbosity,
	}).Get(ctx, nil)
}

// stepTick is workflow-local shared state for the debug Step control
// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6). Exactly one instance is created per
// AppFlowWorkflow execution when StepMode is true, and a pointer to it is
// threaded into every place a node dispatch can pause (the main loop and each
// fork branch in walkBranch).
//
// Design note — why a shared counter instead of one signal per paused node:
// a Temporal named signal channel is a FIFO mailbox, not a broadcast — one
// SignalWorkflow call wakes exactly one blocked Receive, never all of them.
// With N fork branches each blocked on their own Receive for the same signal
// name, a single Step click would only ever release one branch, silently
// stranding the rest. Instead, exactly one goroutine (startStepListener)
// owns the only Receive on AppFlowSignalStep; every paused caller instead
// blocks on workflow.Await watching this shared Gen counter, which IS
// broadcast to every blocked Await condition in the same dispatcher tick
// when it changes. One signal -> one Gen bump -> every waiter wakes together.
type stepTick struct {
	Gen int
}

// startStepListener starts the single goroutine that owns the Receive on
// AppFlowSignalStep and bumps tick.Gen once per signal. Must be started
// exactly once per workflow execution, before any stepGate call, only when
// StepMode is true. Runs for the lifetime of the workflow (no exit condition
// other than the workflow itself completing) — safe because workflow.Go
// goroutines are cooperatively scheduled and abandoned harmlessly on
// workflow completion, the same pattern any long-lived workflow-local
// listener uses.
func startStepListener(ctx workflow.Context, tick *stepTick) {
	workflow.Go(ctx, func(gCtx workflow.Context) {
		sigCh := workflow.GetSignalChannel(gCtx, AppFlowSignalStep)
		for {
			sigCh.Receive(gCtx, nil)
			tick.Gen++
		}
	})
}

// stepGate blocks the calling node dispatch until the next Step click when
// StepMode is active, then fires a node_paused trace event before blocking so
// the UI can distinguish "genuinely running" from "paused, waiting for Step."
// lastSeenGen is the caller's own cursor into tick.Gen — the main loop and
// each fork branch each keep their own, since they must each advance exactly
// one tick per Step click, not skip ahead if they happen to check late.
// No-op (returns immediately) when tick is nil, i.e. StepMode is false —
// this is the only call site cost Run-All debug sessions pay: one nil check.
func stepGate(ctx workflow.Context, tick *stepTick, lastSeenGen *int, input AppFlowWorkflowInput, runID, nodeID, kind string) {
	if tick == nil {
		return
	}
	traceNode(ctx, runID, nodeID, kind, "node_paused", "", input.LogVerbosity)
	seenAt := *lastSeenGen
	_ = workflow.Await(ctx, func() bool { return tick.Gen > seenAt })
	*lastSeenGen = tick.Gen
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
	// Debug routes this run's activities to AppFlowDebugTaskQueue instead of
	// AppFlowTaskQueue, so it executes on the isolated debug worker pool. The
	// workflow itself must also be started with TaskQueue: AppFlowDebugTaskQueue
	// (StartWorkflowOptions) for a debug worker to pick it up in the first place —
	// this field only controls the *activity* dispatch queue used inside the
	// workflow function once it's already running.
	Debug bool `json:"debug,omitempty"`
	// LogVerbosity is the resolved effective trace-persistence level for this
	// run: "off"|"status"|"full" (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4).
	// Resolved once by StartAppFlow from them.app_debug_config before the
	// workflow starts (fail-open default "status" on load error or no row).
	// When Debug is true, StartAppFlow always sets this to "full" regardless
	// of the app's configured setting, overriding whatever was loaded.
	LogVerbosity string `json:"log_verbosity,omitempty"`
	// StepMode pauses execution before every node's tick (main path AND every
	// active fork branch) and waits for an external AppFlowSignalStep signal
	// before proceeding (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6). Only
	// meaningful when Debug is true; Run-All debug sessions leave this false,
	// in which case stepGate is never called and behavior is identical to
	// today. Not persisted on the run row — purely an execution-time control.
	StepMode bool `json:"step_mode,omitempty"`
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
	// Activities dispatch to the debug queue when this run was started in debug
	// mode, so they land on the isolated debug worker pool rather than production
	// (see AppFlowWorkflowInput.Debug).
	activityTaskQueue := activityTaskQueueFor(input.Debug)

	// Shared activity options for short-lived finalize/HIL activities.
	shortAO := workflow.ActivityOptions{
		TaskQueue:           activityTaskQueue,
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
			Debug:     input.Debug,
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
		TaskQueue:           activityTaskQueue,
		StartToCloseTimeout: actTimeout,
		RetryPolicy: &temporalerr.RetryPolicy{
			MaximumAttempts: retryMax,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Debug Step control (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6) — only wired
	// up when StepMode is set; every other run pays a single nil check per
	// node (see stepGate). See stepTick's doc comment for why this is a
	// shared counter rather than a signal per paused branch.
	var tick *stepTick
	if input.StepMode {
		tick = &stepTick{}
		startStepListener(ctx, tick)
	}
	mainLastSeenGen := 0

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
		stepGate(ctx, tick, &mainLastSeenGen, input, input.RunID, node.ID, node.Kind)

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
				Verbosity:     input.LogVerbosity,
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
				Verbosity:     input.LogVerbosity,
				Debug:         input.Debug,
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
			traceNode(ctx, input.RunID, node.ID, node.Kind, "node_start", "", input.LogVerbosity)
			var cfg InlineConditionConfig
			if len(node.Config) > 0 {
				_ = json.Unmarshal(node.Config, &cfg)
			}
			vars["input"] = accumulated
			rendered, rErr := renderFlowTemplate(cfg.Expression, vars)
			if rErr != nil {
				traceNode(ctx, input.RunID, node.ID, node.Kind, "node_error", rErr.Error(), input.LogVerbosity)
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
				traceNode(ctx, input.RunID, node.ID, node.Kind, "node_error", fmt.Sprintf("no outgoing edge labelled %q", branch), input.LogVerbosity)
				out.Status = "failed"
				retErr = temporalerr.NewNonRetryableApplicationError(
					fmt.Sprintf("condition %q: no outgoing edge labelled %q", node.ID, branch),
					"ConditionNoMatch", nil,
				)
				return
			}
			traceNode(ctx, input.RunID, node.ID, node.Kind, "node_done", "branch="+branch, input.LogVerbosity)
			currentID = nextID
			continue

		case "fork":
			branches := outEdgesBySource[node.ID]
			traceNode(ctx, input.RunID, node.ID, node.Kind, "node_start", fmt.Sprintf("branches=%d", len(branches)), input.LogVerbosity)
			branchResults := make([]string, len(branches))
			wg := workflow.NewWaitGroup(ctx)
			// Find the join node that all branches converge on.
			// Each branch walks until it hits a join node, then stops.
			joinID := findJoinNode(branches, nodeByID, outEdgesBySource)
			for i, branch := range branches {
				i, startNodeID := i, branch.Target
				wg.Add(1)
				workflow.Go(ctx, func(gCtx workflow.Context) {
					branchResult, branchErr := walkBranch(gCtx, startNodeID, joinID, nodeByID, outEdgesBySource, input, accumulated, ao, shortAO, tick, mainLastSeenGen)
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
			// Sync the main loop's own step cursor up to whatever tick.Gen
			// reached while the branches ran (docs/APP_CANVAS_DEBUG_PLAN.md
			// Phase 6). Each branch advanced tick.Gen independently via its
			// own signals while converging on the join — without this sync,
			// mainLastSeenGen stays frozen at its pre-fork value, so the
			// very next stepGate call (for whatever follows the join) would
			// see tick.Gen already ahead of its stale cursor and run
			// immediately, for free, without a Step click of its own.
			if tick != nil {
				mainLastSeenGen = tick.Gen
			}
			// Merge branch results: concatenate non-empty results.
			merged := mergeBranchResults(branchResults)
			if merged != "" {
				accumulated = merged
			}
			traceNode(ctx, input.RunID, node.ID, node.Kind, "node_done", fmt.Sprintf("branches=%d", len(branches)), input.LogVerbosity)
			// The join node itself is never visited by walkBranch — each branch
			// stops as soon as it reaches joinID (see walkBranch's loop condition
			// in graph.go), so its trace fires here instead, once all branches
			// have converged.
			if joinNode, ok := nodeByID[joinID]; ok {
				traceNode(ctx, input.RunID, joinNode.ID, joinNode.Kind, "node_start", "", input.LogVerbosity)
				traceNode(ctx, input.RunID, joinNode.ID, joinNode.Kind, "node_done", "", input.LogVerbosity)
			}
			// Continue from the join node's outgoing edge.
			if joinID != "" {
				currentID = firstEdgeTarget(outEdgesBySource[joinID])
			} else {
				currentID = ""
			}
			continue

		case "join":
			traceNode(ctx, input.RunID, node.ID, node.Kind, "node_start", "", input.LogVerbosity)
			traceNode(ctx, input.RunID, node.ID, node.Kind, "node_done", "", input.LogVerbosity)
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
