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
	"strings"
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

	// AppFlowFileGateActivityName is the registered name for the File Guard
	// scan activity (Phase 2 of docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md).
	AppFlowFileGateActivityName = "AppFlowFileGateActivity"

	// AppFlowFileGateWaitActivityName is the registered name for the
	// activity that blocks until File Guard's async scan reaches a real
	// terminal verdict (docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 0).
	AppFlowFileGateWaitActivityName = "AppFlowFileGateWaitActivity"

	// AppFlowTextGateActivityName is the registered name for the PII/
	// prompt-injection text guard activity (Phase 3 of
	// docs/APPFLOW_TEXT_GUARDS_PLAN.md).
	AppFlowTextGateActivityName = "AppFlowTextGateActivity"

	// AppFlowInlineLLMActivityName is the registered name for the inline LLM node activity.
	AppFlowInlineLLMActivityName = "AppFlowInlineLLMActivity"

	// AppFlowTraceNodeEventActivityName is the registered name for the live
	// node_start/node_done/node_error trace-publish activity (Phase 2 of
	// docs/APP_CANVAS_DEBUG_PLAN.md). Used by Condition/Fork/Join, which have
	// no activity of their own for their real logic.
	AppFlowTraceNodeEventActivityName = "AppFlowTraceNodeEventActivity"

	// AppFlowSignalHILApproval is the signal name for HIL human approval.
	AppFlowSignalHILApproval = "hil_approval"

	// AppFlowSignalUserInput is the prefix for wait_for_input signals.
	// Full signal name per node: AppFlowSignalUserInput + ":" + nodeID
	// so a flow with multiple wait points doesn't mix up replies.
	AppFlowSignalUserInput = "appflow_user_input"

	// AppFlowPendingWaitSetActivityName is the registered name for the
	// activity that writes them:wait:{runID} to Redis so the WS handler
	// knows to signal this workflow instead of ignoring subsequent messages.
	AppFlowPendingWaitSetActivityName = "AppFlowPendingWaitSetActivity"

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

	// fileGateWaitActivityTimeout bounds AppFlowFileGateWaitActivity's own
	// Temporal StartToCloseTimeout — deliberately longer than the
	// activity's internal fileGateWaitTimeout (60s, activities.go) so the
	// activity has room to return its own "timeout" result cleanly rather
	// than being killed by Temporal's outer timeout first (which would
	// surface as a real activity failure, not the handled fail-open path
	// this phase is built around).
	fileGateWaitActivityTimeout = 75 * time.Second
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

// guardStatusVarName builds the FlowVar name a condition node reads a
// guard's outcome from — docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 1.
// One var per (node, guard kind) pair, deliberately not combined across
// guards on the same node (Open Question 1: a node with both PII Guard and
// Prompt-Injection Guard enabled must let a condition node branch on EACH
// independently — e.g. route PII to HIL but reject a bad file outright —
// so collapsing them into one "anything flagged" value would lose exactly
// the distinction this feature exists to expose).
func guardStatusVarName(nodeID, defSlug string) string {
	return nodeID + "_" + defSlug + "_status"
}

// writeTextGuardVars parses a TextGateCheckOutput's Categories string
// (space-separated "defSlug:status" pairs, e.g. "pii_redact:flagged
// prompt_inject:clean" — see middleware.TextGate.Check's joinCategoryParts)
// into one FlowVar per guard that actually ran on this node, so a
// downstream condition node can branch on each guard independently.
// Guards that didn't run at all for this node (no wiring, or wrong
// direction for this phase) get no var — a condition expression checking
// for one must treat "unset" the same as "clean" (both mean "nothing to
// worry about"), matching renderFlowTemplate's own <no value> convention
// for an unset FlowVar.
func writeTextGuardVars(vars FlowVars, nodeID, categories string) {
	if categories == "" {
		return
	}
	for _, part := range strings.Fields(categories) {
		defSlug, status, ok := strings.Cut(part, ":")
		if !ok || defSlug == "" || status == "" {
			continue // malformed pair — skip rather than write a var with an empty key/value
		}
		vars[guardStatusVarName(nodeID, defSlug)] = status
	}
}

// writeGuardCategoryVars writes one FlowVar per (guard, category) pair
// found in details — docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 1.5.
// E.g. a pii_redact guard that matched "email" once and "phone" twice on
// node "agent1" writes agent1_pii_redact_email_status="flagged" and
// agent1_pii_redact_phone_status="flagged". A guard with Outcome=="clean"
// (nothing matched at all) writes no per-category vars — there is nothing
// to enumerate — but writeTextGuardVars's own per-guard status var (e.g.
// agent1_pii_redact_status="clean") already covers that case.
//
// Flat storage key, not real nested Go values, deliberately — FlowVars is
// (and stays) a flat map[string]string; the nicer dotted display form
// ({{pii_guard.email.status}}) a user sees/drags on the canvas is a
// presentation-layer translation the frontend applies over this flat key,
// not a different runtime representation (Phase 2's (a)/(b) decision,
// resolved as (b): additive, doesn't touch FlowVars' type or every
// existing vars[x]=y call site).
func writeGuardCategoryVars(vars FlowVars, nodeID string, details []GuardCategoryDetail) {
	for _, d := range details {
		for category := range d.Categories {
			vars[nodeID+"_"+d.DefSlug+"_"+category+"_status"] = "flagged"
		}
	}
}

// writeFileGuardVar sets the File Guard status var for nodeID — its own
// vocabulary (clean/infected/error/timeout/disabled/pending), deliberately
// NOT normalized into text guards' clean/flagged/blocked, since "infected"
// is a materially different, more severe outcome than "flagged" and a
// condition node needs to be able to tell them apart.
func writeFileGuardVar(vars FlowVars, nodeID, scanStatus string) {
	if scanStatus == "" {
		return
	}
	vars[guardStatusVarName(nodeID, "file_guard")] = scanStatus
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
			nextID, chosenLabel, confidence, rErr := execRouterNode(ctx, node, input, outEdgesBySource[node.ID], accumulated)
			if rErr != nil {
				out.Status = "failed"
				retErr = rErr
				return
			}
			// Write router result into flow vars so downstream nodes can use them.
			// router_label: the chosen intent label (e.g. "billing")
			// router_confidence: LLM's self-reported confidence (0.0–1.0)
			vars["router_label"] = chosenLabel
			vars[fmt.Sprintf("%s_label", node.ID)] = chosenLabel
			vars["router_confidence"] = fmt.Sprintf("%.4f", confidence)
			vars[fmt.Sprintf("%s_confidence", node.ID)] = fmt.Sprintf("%.4f", confidence)
			currentID = nextID
			continue

		case "hil":
			approved, comment, hErr := execHILNode(ctx, node, input, shortAO, vars)
			if hErr != nil {
				out.Status = "failed"
				retErr = hErr
				return
			}
			// Write approver comment and decision into flow vars.
			// hil_comment / {node_id}_comment — the approver's text (empty if none given)
			// hil_decision / {node_id}_decision — "approved" or "rejected"
			vars["hil_comment"] = comment
			vars[node.ID+"_comment"] = comment
			decision := "rejected"
			if approved {
				decision = "approved"
			}
			vars["hil_decision"] = decision
			vars[node.ID+"_decision"] = decision

			if !approved {
				// If the app wired a "rejected" outgoing edge, follow it so the flow
				// can compose a rejection response rather than hard-terminating.
				if rejID := findEdgeByLabel(outEdgesBySource[node.ID], "rejected"); rejID != "" {
					currentID = rejID
					continue
				}
				// No rejection branch wired — end the run.
				out = AppFlowWorkflowOutput{Status: "rejected", FinalText: "HIL gate rejected: " + comment}
				return
			}
			// Approved — follow the "approved" labeled edge if present, else the first edge.
			if appID := findEdgeByLabel(outEdgesBySource[node.ID], "approved"); appID != "" {
				currentID = appID
			} else {
				currentID = firstEdgeTarget(outEdgesBySource[node.ID])
			}
			continue

		case "agent":
			// guardNotes collects non-blocking Text Guard flags (mode=warn,
			// or mode=redact's own flagged status) from BOTH the input and
			// output phase checks below, so a single node_done re-trace at
			// the end can show everything that happened on this node — a
			// warn-mode flag on input must not get silently lost just
			// because it happened before InvokeAgentActivity's own trace.
			var guardNotes []string

			// Text Guards input-phase check (docs/APPFLOW_TEXT_GUARDS_PLAN.md
			// Phase 4's direction field): only a guard wired with
			// direction:"both" actually does anything here — TextGate.Check
			// itself decides that per-guard from the resolved config, this
			// call site doesn't need to know which guards are so configured.
			{
				var textIn TextGateCheckOutput
				textErr := workflow.ExecuteActivity(ctx, AppFlowTextGateActivityName, TextGateCheckInput{
					RunID:         input.RunID,
					ApplicationID: input.ApplicationID,
					NodeID:        node.ID,
					Text:          accumulated,
					Phase:         "input",
					Verbosity:     input.LogVerbosity,
				}).Get(ctx, &textIn)
				if textErr != nil {
					out.Status = "failed"
					retErr = fmt.Errorf("agent %q: text guard input check: %w", node.ID, textErr)
					return
				}
				if textIn.Blocked {
					out.Status = "failed"
					retErr = temporalerr.NewNonRetryableApplicationError(
						fmt.Sprintf("agent %q: text guard blocked input (%s)", node.ID, textIn.Categories),
						"TextGateBlocked", nil,
					)
					return
				}
				accumulated = textIn.Text
				writeTextGuardVars(vars, node.ID, textIn.Categories)
				writeGuardCategoryVars(vars, node.ID, textIn.GuardDetails)
				if strings.Contains(textIn.Categories, ":flagged") {
					guardNotes = append(guardNotes, "input: "+textIn.Categories)
				}
			}
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
			// Text Guards check (Phase 3 of docs/APPFLOW_TEXT_GUARDS_PLAN.md):
			// only when there's real text to guard — a file/data/raw-only
			// response has nothing for pii_redact/prompt_inject to scan.
			// Runs AFTER File Guard's own check below intentionally: PII in a
			// filename/URL is a different, much smaller surface than PII in a
			// full text response, and File Guard's own async job path is
			// independent of this synchronous one either way.
			if agentOut.ResponseText != "" {
				var textOut TextGateCheckOutput
				textErr := workflow.ExecuteActivity(ctx, AppFlowTextGateActivityName, TextGateCheckInput{
					RunID:         input.RunID,
					ApplicationID: input.ApplicationID,
					NodeID:        node.ID,
					Text:          agentOut.ResponseText,
					Phase:         "output",
					Verbosity:     input.LogVerbosity,
				}).Get(ctx, &textOut)
				if textErr != nil {
					out.Status = "failed"
					retErr = fmt.Errorf("agent %q: text guard check: %w", node.ID, textErr)
					return
				}
				if textOut.Blocked {
					out.Status = "failed"
					retErr = temporalerr.NewNonRetryableApplicationError(
						fmt.Sprintf("agent %q: text guard blocked response (%s)", node.ID, textOut.Categories),
						"TextGateBlocked", nil,
					)
					return
				}
				accumulated = textOut.Text
				writeTextGuardVars(vars, node.ID, textOut.Categories)
				writeGuardCategoryVars(vars, node.ID, textOut.GuardDetails)
				if strings.Contains(textOut.Categories, ":flagged") {
					guardNotes = append(guardNotes, "output: "+textOut.Categories)
				}
			}
			// A non-blocking flag (mode=warn, or mode=redact — both set
			// Categories without Blocked) must still be visible somewhere:
			// warn mode changes nothing else at all (no redaction, no
			// failure), so without this the guard would look like it did
			// nothing. Re-emits node_done with the FINAL text (already
			// includes InvokeAgentActivity's own trace) plus every guard note
			// collected across BOTH phases — never a bare replacement, since
			// persistTrace's node_done UPDATE overwrites `output` wholesale,
			// not append-only.
			if len(guardNotes) > 0 {
				traceNode(ctx, input.RunID, node.ID, node.Kind, "node_done",
					fmt.Sprintf("%s — Text Guard: %s", accumulated, strings.Join(guardNotes, ", ")), input.LogVerbosity)
			}
			// File Guard check (Phase 2 of docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md):
			// only when the agent actually returned a recognized file part —
			// a text-only response (the overwhelmingly common case today) never
			// even calls this activity. Scoped by this exact canvas node
			// instance (node.ID), not just the agent, so two boxes using the
			// same agent can carry independent File Guard configs. A blocked
			// file fails the run non-retryably; a scan-pending or
			// scanning-disabled result is traced but does not change
			// `accumulated` — the file's own URL/name are what a downstream
			// node would need, not something this phase changes yet.
			if agentOut.PartKind == "file" {
				var gateOut FileGateCheckOutput
				gateErr := workflow.ExecuteActivity(ctx, AppFlowFileGateActivityName, FileGateCheckInput{
					RunID:           input.RunID,
					TenantID:        input.TenantID,
					ApplicationID:   input.ApplicationID,
					NodeID:          node.ID,
					FileURL:         agentOut.FileURL,
					FileName:        agentOut.FileName,
					FileContentType: agentOut.FileContentType,
					Verbosity:       input.LogVerbosity,
				}).Get(ctx, &gateOut)
				if gateErr != nil {
					out.Status = "failed"
					retErr = fmt.Errorf("agent %q: file guard check: %w", node.ID, gateErr)
					return
				}
				// Wait for the REAL scan verdict (docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md
				// Phase 0) — only when a scan was actually enqueued
				// (ScanStatus=="pending"; "disabled" means no wiring/no
				// storage configured, nothing to wait for). Own
				// ActivityOptions: a longer StartToCloseTimeout than the
				// main ao (the activity itself blocks for up to
				// fileGateWaitTimeout=60s) and MaximumAttempts:1 — a
				// timed-out wait must fail once and let this workflow decide
				// fail-open, not have Temporal's default retry policy
				// silently re-run the wait up to 3 times, tripling the
				// user-visible delay.
				if gateOut.ScanStatus == "pending" {
					waitAO := workflow.ActivityOptions{
						TaskQueue:           activityTaskQueue,
						StartToCloseTimeout: fileGateWaitActivityTimeout,
						RetryPolicy:         &temporalerr.RetryPolicy{MaximumAttempts: 1},
					}
					waitCtx := workflow.WithActivityOptions(ctx, waitAO)
					var waitOut FileGateWaitOutput
					waitErr := workflow.ExecuteActivity(waitCtx, AppFlowFileGateWaitActivityName, FileGateWaitInput{
						RunID:      input.RunID,
						ArtifactID: gateOut.ArtifactID,
						NodeID:     node.ID,
						Verbosity:  input.LogVerbosity,
					}).Get(waitCtx, &waitOut)
					if waitErr != nil {
						out.Status = "failed"
						retErr = fmt.Errorf("agent %q: file guard wait: %w", node.ID, waitErr)
						return
					}
					writeFileGuardVar(vars, node.ID, waitOut.ScanStatus)
					if waitOut.ScanStatus == "infected" {
						out.Status = "failed"
						retErr = temporalerr.NewNonRetryableApplicationError(
							fmt.Sprintf("agent %q: file guard blocked file (threat=%s)", node.ID, waitOut.Threat),
							"FileGateBlocked", nil,
						)
						return
					}
					traceNode(ctx, input.RunID, node.ID, node.Kind, "node_done",
						fmt.Sprintf("%s — File Guard: %s", accumulated, waitOut.ScanStatus), input.LogVerbosity)
				} else {
					writeFileGuardVar(vars, node.ID, gateOut.ScanStatus)
				}
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

			// guardNotes collects non-blocking Text Guard flags from BOTH
			// phases — see the agent case's identical comment for why.
			var guardNotes []string

			// Text Guards input-phase check (docs/APPFLOW_TEXT_GUARDS_PLAN.md
			// Phase 4's direction field) — same "only a guard wired
			// direction:'both' does anything" contract as the agent case.
			// Checked against `accumulated` (the same value about to become
			// vars["input"] and be interpolated into the rendered prompt) —
			// the exact final rendered prompt only exists inside
			// InlineLLMActivity, this is the best available proxy for "the
			// text about to be sent," consistent with how the agent case
			// treats its own UserMessage input.
			{
				var textIn TextGateCheckOutput
				textErr := workflow.ExecuteActivity(ctx, AppFlowTextGateActivityName, TextGateCheckInput{
					RunID:         input.RunID,
					ApplicationID: input.ApplicationID,
					NodeID:        node.ID,
					Text:          accumulated,
					Phase:         "input",
					Verbosity:     input.LogVerbosity,
				}).Get(ctx, &textIn)
				if textErr != nil {
					out.Status = "failed"
					retErr = fmt.Errorf("llm %q: text guard input check: %w", node.ID, textErr)
					return
				}
				if textIn.Blocked {
					out.Status = "failed"
					retErr = temporalerr.NewNonRetryableApplicationError(
						fmt.Sprintf("llm %q: text guard blocked input (%s)", node.ID, textIn.Categories),
						"TextGateBlocked", nil,
					)
					return
				}
				accumulated = textIn.Text
				writeTextGuardVars(vars, node.ID, textIn.Categories)
				writeGuardCategoryVars(vars, node.ID, textIn.GuardDetails)
				if strings.Contains(textIn.Categories, ":flagged") {
					guardNotes = append(guardNotes, "input: "+textIn.Categories)
				}
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
			// Text Guards check (Phase 3 of docs/APPFLOW_TEXT_GUARDS_PLAN.md) —
			// same call as the agent case, run BEFORE the response is written
			// into vars/accumulated so a downstream node never sees
			// unredacted text.
			if llmOut.ResponseText != "" {
				var textOut TextGateCheckOutput
				textErr := workflow.ExecuteActivity(ctx, AppFlowTextGateActivityName, TextGateCheckInput{
					RunID:         input.RunID,
					ApplicationID: input.ApplicationID,
					NodeID:        node.ID,
					Text:          llmOut.ResponseText,
					Phase:         "output",
					Verbosity:     input.LogVerbosity,
				}).Get(ctx, &textOut)
				if textErr != nil {
					out.Status = "failed"
					retErr = fmt.Errorf("llm %q: text guard check: %w", node.ID, textErr)
					return
				}
				if textOut.Blocked {
					out.Status = "failed"
					retErr = temporalerr.NewNonRetryableApplicationError(
						fmt.Sprintf("llm %q: text guard blocked response (%s)", node.ID, textOut.Categories),
						"TextGateBlocked", nil,
					)
					return
				}
				llmOut.ResponseText = textOut.Text
				writeTextGuardVars(vars, node.ID, textOut.Categories)
				writeGuardCategoryVars(vars, node.ID, textOut.GuardDetails)
				if strings.Contains(textOut.Categories, ":flagged") {
					guardNotes = append(guardNotes, "output: "+textOut.Categories)
				}
			}
			// Same non-blocking-flag visibility fix as the agent case — see
			// its own comment for why this can't just append onto the
			// original node_done in place.
			if len(guardNotes) > 0 {
				traceNode(ctx, input.RunID, node.ID, node.Kind, "node_done",
					fmt.Sprintf("%s — Text Guard: %s", llmOut.ResponseText, strings.Join(guardNotes, ", ")), input.LogVerbosity)
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

		case "cycle":
			var cycleErr error
			accumulated, vars, cycleErr = execCycleNode(ctx, node, input, accumulated, vars, ao, shortAO, tick, mainLastSeenGen)
			if cycleErr != nil {
				// If a HIL inside the cycle was rejected with no body-level rejected
				// edge, follow the cycle node's own outer "rejected" edge if wired.
				if _, isRej := cycleErr.(cycleHILRejectedError); isRej {
					if rejID := findEdgeByLabel(outEdgesBySource[node.ID], "rejected"); rejID != "" {
						currentID = rejID
						continue
					}
					// No outer rejected edge — end the run as rejected.
					out = AppFlowWorkflowOutput{Status: "rejected", FinalText: cycleErr.Error()}
					return
				}
				out.Status = "failed"
				retErr = cycleErr
				return
			}
			currentID = firstEdgeTarget(outEdgesBySource[node.ID])
			continue

		case "wait_for_input":
			newAccumulated, newVars, wErr := execWaitForInputNode(ctx, node, input, accumulated, vars, shortAO)
			if wErr != nil {
				out.Status = "failed"
				retErr = wErr
				return
			}
			accumulated = newAccumulated
			vars = newVars
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
