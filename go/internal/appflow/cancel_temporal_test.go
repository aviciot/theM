package appflow

import (
	"time"

	temporalactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// waitForInputSpec: wait1 (wait_for_input) -> end. The flow blocks on wait1 until a
// user reply signal arrives.
func waitForInputSpec() *AppFlowSpec {
	return &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "test",
			StartID: "wait1",
			Nodes: []AppFlowNode{
				{ID: "wait1", Kind: "wait_for_input", Config: mustJSON(WaitForInputConfig{OutputVar: "reply"})},
				{ID: "end", Kind: "orchestrator"},
			},
			Edges: []AppFlowEdge{{Source: "wait1", Target: "end"}},
		}},
	}
}

func (s *AppFlowTraceWorkflowTestSuite) waitInput(runID string) AppFlowWorkflowInput {
	s.env.RegisterActivityWithOptions(s.acts.PendingWaitSetActivity, temporalactivity.RegisterOptions{Name: AppFlowPendingWaitSetActivityName})
	return AppFlowWorkflowInput{
		RunID: runID, TenantID: "tenant-1", ApplicationID: "app-1",
		EntryPointSlug: "test", Spec: waitForInputSpec(), UserMessage: "hi",
	}
}

// F-134: cancelling a run that is waiting for user input must end the workflow as
// cancelled (not hang) and still run FinalizeRunActivity (status failed).
func (s *AppFlowTraceWorkflowTestSuite) TestWaitForInput_CancelEndsWorkflowAndFinalizes() {
	input := s.waitInput("run-cancel-1")
	s.env.RegisterDelayedCallback(func() { s.env.CancelWorkflow() }, 5*time.Second)

	s.env.ExecuteWorkflow(AppFlowWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.True(temporal.IsCanceledError(err), "workflow must end as canceled, got: %v", err)

	calls := s.acts.StatusUpdater.(*fakeStatusUpdater).calls
	s.Require().Len(calls, 1, "finalize must run on cancellation")
	s.Equal("run-cancel-1", calls[0].runID)
	s.Equal("failed", string(calls[0].status))
}

// Regression: a user reply still releases the wait node and the run completes.
func (s *AppFlowTraceWorkflowTestSuite) TestWaitForInput_ReplyStillCompletes() {
	input := s.waitInput("run-reply-1")
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(AppFlowSignalUserInput+":wait1", "50")
	}, 5*time.Second)

	s.env.ExecuteWorkflow(AppFlowWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	calls := s.acts.StatusUpdater.(*fakeStatusUpdater).calls
	s.Require().Len(calls, 1)
	s.Equal("completed", string(calls[0].status))
}
