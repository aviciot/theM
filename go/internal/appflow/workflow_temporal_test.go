package appflow

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	temporalactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// mustJSON marshals v to json.RawMessage, panicking on error — test-only
// helper for building AppFlowNode.Config literals inline.
func mustJSON(v interface{}) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// docs/APP_CANVAS_DEBUG_PLAN.md Phase 2 — full-workflow-execution coverage for
// the traceNode call sites added to AppFlowWorkflow's main loop (workflow.go)
// and walkBranch (graph.go). The activity-level tests in workflow_test.go
// cover Router/HIL/Agent/Inline LLM's inline trace emission; this suite
// exercises the paths that have no activity of their own — Condition, Fork,
// and Join — via a real Temporal workflow test environment, since traceNode
// dispatches through workflow.ExecuteActivity and can't be called directly
// from a plain unit test.
type AppFlowTraceWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env       *testsuite.TestWorkflowEnvironment
	acts      *AppFlowActivities
	streamPub *fakeStreamPub
}

func (s *AppFlowTraceWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.streamPub = &fakeStreamPub{}
	s.acts = &AppFlowActivities{
		StreamPub:     s.streamPub,
		StatusUpdater: &fakeStatusUpdater{},
	}
	s.env.RegisterWorkflow(AppFlowWorkflow)
	s.env.RegisterActivityWithOptions(s.acts.ExecuteRouterActivity, temporalactivity.RegisterOptions{Name: AppFlowExecuteRouterActivityName})
	s.env.RegisterActivityWithOptions(s.acts.ExecuteHILActivity, temporalactivity.RegisterOptions{Name: AppFlowExecuteHILActivityName})
	s.env.RegisterActivityWithOptions(s.acts.FinalizeRunActivity, temporalactivity.RegisterOptions{Name: AppFlowFinalizeRunActivityName})
	s.env.RegisterActivityWithOptions(s.acts.InvokeAgentActivity, temporalactivity.RegisterOptions{Name: AppFlowInvokeAgentActivityName})
	s.env.RegisterActivityWithOptions(s.acts.InlineLLMActivity, temporalactivity.RegisterOptions{Name: AppFlowInlineLLMActivityName})
	s.env.RegisterActivityWithOptions(s.acts.TraceNodeEventActivity, temporalactivity.RegisterOptions{Name: AppFlowTraceNodeEventActivityName})
}

func (s *AppFlowTraceWorkflowTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func TestAppFlowTraceWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(AppFlowTraceWorkflowTestSuite))
}

// singleConditionSpec builds a minimal one-EP spec: condition node routes to
// a terminal pass-through "end" node via its "true" edge. The target must be
// a real node ID, not empty — an empty Target is indistinguishable from "no
// matching edge" in findEdgeByLabel and would wrongly take the error path.
func singleConditionSpec(expression string) *AppFlowSpec {
	return &AppFlowSpec{
		EntryPoints: []EPFlow{
			{
				Slug:    "test",
				StartID: "cond1",
				Nodes: []AppFlowNode{
					{ID: "cond1", Kind: "condition", Config: mustJSON(InlineConditionConfig{Expression: expression})},
					{ID: "end", Kind: "orchestrator"},
				},
				Edges: []AppFlowEdge{
					{Source: "cond1", Target: "end", Label: "true"},
				},
			},
		},
	}
}

// AF-TR-W01: a Condition node emits node_start then node_done with
// detail="branch=true" when its expression is truthy.
func (s *AppFlowTraceWorkflowTestSuite) TestConditionNode_EmitsStartAndDoneTrace() {
	input := AppFlowWorkflowInput{
		RunID:          "run-w-1",
		TenantID:       "tenant-1",
		ApplicationID:  "app-1",
		EntryPointSlug: "test",
		Spec:           singleConditionSpec("true"),
		UserMessage:    "hi",
	}
	s.env.ExecuteWorkflow(AppFlowWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())

	starts := tracePayloadsOfType(s.T(), s.streamPub, "node_start")
	dones := tracePayloadsOfType(s.T(), s.streamPub, "node_done")
	s.Require().Len(starts, 1)
	s.Require().Len(dones, 1)
	s.Equal("condition", starts[0]["kind"])
	s.Equal("cond1", starts[0]["node_id"])
	s.Equal("branch=true", dones[0]["detail"])
}

// AF-TR-W02: a Condition node with no matching outgoing edge emits node_error,
// not node_done, before the workflow fails.
func (s *AppFlowTraceWorkflowTestSuite) TestConditionNode_NoMatchingEdge_EmitsErrorTrace() {
	spec := singleConditionSpec("false") // renders "false" branch, but only "true" edge exists
	input := AppFlowWorkflowInput{
		RunID:          "run-w-2",
		TenantID:       "tenant-1",
		ApplicationID:  "app-1",
		EntryPointSlug: "test",
		Spec:           spec,
		UserMessage:    "hi",
	}
	s.env.ExecuteWorkflow(AppFlowWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())

	errs := tracePayloadsOfType(s.T(), s.streamPub, "node_error")
	dones := tracePayloadsOfType(s.T(), s.streamPub, "node_done")
	s.Require().Len(errs, 1)
	s.Len(dones, 0)
}

// forkJoinSpec builds a fork into 2 single-node branches converging on a join.
func forkJoinSpec() *AppFlowSpec {
	return &AppFlowSpec{
		EntryPoints: []EPFlow{
			{
				Slug:    "test",
				StartID: "fork1",
				Nodes: []AppFlowNode{
					{ID: "fork1", Kind: "fork"},
					{ID: "condA", Kind: "condition", Config: mustJSON(InlineConditionConfig{Expression: "true"})},
					{ID: "condB", Kind: "condition", Config: mustJSON(InlineConditionConfig{Expression: "true"})},
					{ID: "join1", Kind: "join"},
				},
				Edges: []AppFlowEdge{
					{Source: "fork1", Target: "condA"},
					{Source: "fork1", Target: "condB"},
					{Source: "condA", Target: "join1", Label: "true"},
					{Source: "condB", Target: "join1", Label: "true"},
				},
			},
		},
	}
}

// AF-TR-W03: Fork emits node_start(detail="branches=2")/node_done, and each
// branch's Condition node (walked via graph.go's walkBranch) emits its own
// start/done, and Join emits start/done — proving traceNode fires correctly
// from both workflow.go's main loop and graph.go's branch walker.
func (s *AppFlowTraceWorkflowTestSuite) TestForkJoin_EmitsTraceForAllNodes() {
	input := AppFlowWorkflowInput{
		RunID:          "run-w-3",
		TenantID:       "tenant-1",
		ApplicationID:  "app-1",
		EntryPointSlug: "test",
		Spec:           forkJoinSpec(),
		UserMessage:    "hi",
	}
	s.env.ExecuteWorkflow(AppFlowWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	starts := tracePayloadsOfType(s.T(), s.streamPub, "node_start")
	dones := tracePayloadsOfType(s.T(), s.streamPub, "node_done")

	nodeIDs := func(events []map[string]interface{}) map[string]int {
		out := map[string]int{}
		for _, e := range events {
			out[e["node_id"].(string)]++
		}
		return out
	}
	startCounts := nodeIDs(starts)
	doneCounts := nodeIDs(dones)

	for _, id := range []string{"fork1", "condA", "condB", "join1"} {
		s.Equalf(1, startCounts[id], "node_start count for %q", id)
		s.Equalf(1, doneCounts[id], "node_done count for %q", id)
	}

	var forkDone map[string]interface{}
	for _, d := range dones {
		if d["node_id"] == "fork1" {
			forkDone = d
		}
	}
	s.Require().NotNil(forkDone)
	s.Equal("branches=2", forkDone["detail"])
}

// ── Phase 6 — Step controls (docs/APP_CANVAS_DEBUG_PLAN.md) ─────────────────

// twoConditionChainSpec builds a linear 2-node chain (cond1 -> cond2 -> end)
// with no fork — used to prove sequential stepGate pausing: one
// AppFlowSignalStep click advances exactly one node, not more.
func twoConditionChainSpec() *AppFlowSpec {
	return &AppFlowSpec{
		EntryPoints: []EPFlow{
			{
				Slug:    "test",
				StartID: "cond1",
				Nodes: []AppFlowNode{
					{ID: "cond1", Kind: "condition", Config: mustJSON(InlineConditionConfig{Expression: "true"})},
					{ID: "cond2", Kind: "condition", Config: mustJSON(InlineConditionConfig{Expression: "true"})},
					{ID: "end", Kind: "orchestrator"},
				},
				Edges: []AppFlowEdge{
					{Source: "cond1", Target: "cond2", Label: "true"},
					{Source: "cond2", Target: "end", Label: "true"},
				},
			},
		},
	}
}

// AF-STEP-01: with StepMode=true and zero Step signals sent, the workflow
// never completes within the test environment's mocked clock — it must be
// genuinely blocked on the first node's stepGate, not running straight
// through like a Run-All session would.
func (s *AppFlowTraceWorkflowTestSuite) TestStepMode_NoSignalSent_WorkflowNeverCompletes() {
	input := AppFlowWorkflowInput{
		RunID:          "run-step-1",
		TenantID:       "tenant-1",
		ApplicationID:  "app-1",
		EntryPointSlug: "test",
		Spec:           twoConditionChainSpec(),
		UserMessage:    "hi",
		StepMode:       true,
	}
	s.env.ExecuteWorkflow(AppFlowWorkflow, input)
	// The test environment's own internal "don't hang forever" background
	// timer (an SDK-internal default, unrelated to anything this package
	// sets) eventually fires when nothing else is scheduled, which the SDK
	// then reports as a completed-with-error workflow — this is the test
	// environment's mechanism for detecting "nothing will ever happen
	// again," not evidence the workflow made real progress. The meaningful
	// assertion is what DID or did NOT execute before that point: cond1 must
	// have paused and never actually run.
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError(), "must never reach a real completion — only the test env's own stuck-workflow detector")

	starts := tracePayloadsOfType(s.T(), s.streamPub, "node_start")
	paused := tracePayloadsOfType(s.T(), s.streamPub, "node_paused")
	s.Len(starts, 0, "node_start must not fire until the node is actually released")
	s.Require().Len(paused, 1)
	s.Equal("cond1", paused[0]["node_id"])
}

// AF-STEP-02: exactly 2 Step signals release cond1 then cond2 in order, one
// per tick — proving stepGate's per-caller lastSeenGen advances by exactly
// one tick per signal, not more, on a purely sequential (non-fork) chain.
func (s *AppFlowTraceWorkflowTestSuite) TestStepMode_TwoSignals_AdvancesOneNodeAtATime() {
	input := AppFlowWorkflowInput{
		RunID:          "run-step-2",
		TenantID:       "tenant-1",
		ApplicationID:  "app-1",
		EntryPointSlug: "test",
		Spec:           twoConditionChainSpec(),
		UserMessage:    "hi",
		StepMode:       true,
	}

	s.env.RegisterDelayedCallback(func() {
		// First signal releases cond1 only — cond2 must not have started yet.
		s.env.SignalWorkflow(AppFlowSignalStep, nil)
	}, time.Second)
	s.env.RegisterDelayedCallback(func() {
		dones := tracePayloadsOfType(s.T(), s.streamPub, "node_done")
		s.Require().Len(dones, 1, "cond1 must have finished before cond2 is released")
		s.Equal("cond1", dones[0]["node_id"])
		s.env.SignalWorkflow(AppFlowSignalStep, nil)
	}, 2*time.Second)
	s.env.RegisterDelayedCallback(func() {
		// The chain's 3rd node ("end", an orchestrator pass-through) also
		// pauses on its own stepGate call — every node kind pauses in Step
		// mode, not just the ones with interesting business logic. A 3rd
		// signal releases it so the workflow can actually complete.
		dones := tracePayloadsOfType(s.T(), s.streamPub, "node_done")
		s.Require().Len(dones, 2, "cond1 and cond2 must both have finished before end is released")
		s.env.SignalWorkflow(AppFlowSignalStep, nil)
	}, 3*time.Second)

	s.env.ExecuteWorkflow(AppFlowWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	dones := tracePayloadsOfType(s.T(), s.streamPub, "node_done")
	doneIDs := make([]string, len(dones))
	for i, d := range dones {
		doneIDs[i] = d["node_id"].(string)
	}
	s.Equal([]string{"cond1", "cond2"}, doneIDs, "both nodes must have run, in order, one per signal")
}

// AF-STEP-03: lockstep fan-out — with a fork into 2 branches, exactly ONE
// Step signal (sent once both branches are paused at their first node)
// releases BOTH branches simultaneously. This is the core Phase 6 guarantee:
// a single signal, via the shared tick.Gen counter + workflow.Await, wakes
// every currently-paused node across every active branch, not just one of
// them (a naive "block on Receive per branch" design would only release one
// branch per signal — see stepTick's doc comment in workflow.go).
func (s *AppFlowTraceWorkflowTestSuite) TestStepMode_ForkedBranches_OneSignalReleasesBothInLockstep() {
	input := AppFlowWorkflowInput{
		RunID:          "run-step-3",
		TenantID:       "tenant-1",
		ApplicationID:  "app-1",
		EntryPointSlug: "test",
		Spec:           forkJoinSpec(),
		UserMessage:    "hi",
		StepMode:       true,
	}

	s.env.RegisterDelayedCallback(func() {
		// fork1 itself has no stepGate call of its own (it's dispatched
		// in-line by the main loop's stepGate before the switch, then
		// immediately spawns branches) — one signal to release fork1.
		s.env.SignalWorkflow(AppFlowSignalStep, nil)
	}, time.Second)
	s.env.RegisterDelayedCallback(func() {
		// Both condA and condB must now be paused at their own first tick —
		// neither has emitted node_done yet.
		paused := tracePayloadsOfType(s.T(), s.streamPub, "node_paused")
		pausedIDs := map[string]bool{}
		for _, p := range paused {
			pausedIDs[p["node_id"].(string)] = true
		}
		s.True(pausedIDs["condA"], "condA must be paused before the second signal")
		s.True(pausedIDs["condB"], "condB must be paused before the second signal")
		dones := tracePayloadsOfType(s.T(), s.streamPub, "node_done")
		for _, d := range dones {
			s.NotEqual("condA", d["node_id"], "condA must not have run yet")
			s.NotEqual("condB", d["node_id"], "condB must not have run yet")
		}
		// The ONE signal under test: releases both branches at once.
		s.env.SignalWorkflow(AppFlowSignalStep, nil)
	}, 2*time.Second)

	s.env.ExecuteWorkflow(AppFlowWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	dones := tracePayloadsOfType(s.T(), s.streamPub, "node_done")
	doneIDs := map[string]bool{}
	for _, d := range dones {
		doneIDs[d["node_id"].(string)] = true
	}
	s.True(doneIDs["condA"], "condA must have completed")
	s.True(doneIDs["condB"], "condB must have completed")
}
