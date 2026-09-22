package appflow

import (
	"encoding/json"
	"testing"

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
