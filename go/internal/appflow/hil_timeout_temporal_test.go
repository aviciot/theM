package appflow

import (
	"time"

	"github.com/stretchr/testify/mock"
	temporalactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// hilTimeoutSpec: hil1 (30 s timeout, fallback per arg) -> end on "approved"; no "rejected" edge,
// so a rejection/timeout-reject ends the run as "rejected".
func hilTimeoutSpec(fallback string) *AppFlowSpec {
	return &AppFlowSpec{
		EntryPoints: []EPFlow{{
			Slug:    "test",
			StartID: "hil1",
			Nodes: []AppFlowNode{
				{ID: "hil1", Kind: "hil", Config: mustJSON(HILConfig{TimeoutSeconds: 30, FallbackAction: fallback})},
				{ID: "end", Kind: "orchestrator"},
			},
			Edges: []AppFlowEdge{{Source: "hil1", Target: "end", Label: "approved"}},
		}},
	}
}

// hilEnv mocks the HIL persist activity and records calls to the mark-timed-out activity.
func (s *AppFlowTraceWorkflowTestSuite) hilEnv(runID, fallback string) (AppFlowWorkflowInput, *[]HILTimedOutInput) {
	var marked []HILTimedOutInput
	s.env.RegisterActivityWithOptions(s.acts.MarkHILTimedOutActivity, temporalactivity.RegisterOptions{Name: AppFlowMarkHILTimedOutActivityName})
	s.env.OnActivity(AppFlowExecuteHILActivityName, mock.Anything, mock.Anything).Return(HILActivityOutput{}, nil)
	s.env.OnActivity(AppFlowMarkHILTimedOutActivityName, mock.Anything, mock.Anything).Return(nil).Maybe().
		Run(func(args mock.Arguments) { marked = append(marked, args.Get(1).(HILTimedOutInput)) })
	return AppFlowWorkflowInput{
		RunID: runID, TenantID: "tenant-1", ApplicationID: "app-1",
		EntryPointSlug: "test", Spec: hilTimeoutSpec(fallback), UserMessage: "hi",
	}, &marked
}

// F-124: when the HIL timer fires, the pending approval is marked timed_out (once, for this node).
func (s *AppFlowTraceWorkflowTestSuite) TestHIL_TimerFires_MarksApprovalTimedOut() {
	input, marked := s.hilEnv("run-hil-1", "reject")

	s.env.ExecuteWorkflow(AppFlowWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Require().Len(*marked, 1)
	s.Equal(HILTimedOutInput{RunID: "run-hil-1", NodeID: "hil1", Comment: "timeout-rejected"}, (*marked)[0])
	calls := s.acts.StatusUpdater.(*fakeStatusUpdater).calls
	s.Require().Len(calls, 1)
	s.Equal("failed", string(calls[0].status), "reject fallback ends the run (rejected -> failed)")
}

// The approve fallback also marks the approval timed_out, and the run continues.
func (s *AppFlowTraceWorkflowTestSuite) TestHIL_TimerFires_ApproveFallbackContinues() {
	input, marked := s.hilEnv("run-hil-2", "approve")

	s.env.ExecuteWorkflow(AppFlowWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Require().Len(*marked, 1)
	s.Equal("timeout-auto-approved", (*marked)[0].Comment)
	s.Equal("completed", string(s.acts.StatusUpdater.(*fakeStatusUpdater).calls[0].status))
}

// A human decision before the timer wins: the approval is NOT marked timed_out.
func (s *AppFlowTraceWorkflowTestSuite) TestHIL_SignalBeforeTimer_DoesNotMarkTimedOut() {
	input, marked := s.hilEnv("run-hil-3", "reject")
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(AppFlowSignalHILApproval+":hil1", HILApprovalPayload{Approved: true, Comment: "ok"})
	}, 5*time.Second)

	s.env.ExecuteWorkflow(AppFlowWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Empty(*marked)
	s.Equal("completed", string(s.acts.StatusUpdater.(*fakeStatusUpdater).calls[0].status))
}

// Cancelling while the timer runs ends the workflow as canceled — not a "timeout" and not marked.
func (s *AppFlowTraceWorkflowTestSuite) TestHIL_CancelDuringTimer_NotATimeout() {
	input, marked := s.hilEnv("run-hil-4", "approve")
	s.env.RegisterDelayedCallback(func() { s.env.CancelWorkflow() }, 5*time.Second)

	s.env.ExecuteWorkflow(AppFlowWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.True(temporal.IsCanceledError(s.env.GetWorkflowError()), "got: %v", s.env.GetWorkflowError())
	s.Empty(*marked)
	s.Equal("failed", string(s.acts.StatusUpdater.(*fakeStatusUpdater).calls[0].status))
}
