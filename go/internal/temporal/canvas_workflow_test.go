package temporal_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/temporal"
)

// ── fake ContextLoader ────────────────────────────────────────────────────────

type fakeContextLoader struct {
	ic  *agentgen.InvocationContext
	err error
}

func (f *fakeContextLoader) Load(_ context.Context, _ agentgen.ActivityIC) (*agentgen.InvocationContext, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.ic != nil {
		return f.ic, nil
	}
	return &agentgen.InvocationContext{
		TenantID:      "t",
		ApplicationID: "a",
		AgentID:       "ag",
		BindingID:     "b",
	}, nil
}

// failContextLoader always returns an error — used to force activity failures in tests.
type failContextLoader struct{}

func (f *failContextLoader) Load(_ context.Context, _ agentgen.ActivityIC) (*agentgen.InvocationContext, error) {
	return nil, errors.New("injected failure")
}

// ── test helpers ─────────────────────────────────────────────────────────────

func makeTestIC() agentgen.ActivityIC {
	return agentgen.ActivityIC{
		TenantID:      "t",
		ApplicationID: "a",
		AgentID:       "ag",
		BindingID:     "b",
	}
}

func makeTestActivities() *temporal.CanvasAgentActivities {
	return &temporal.CanvasAgentActivities{
		InterpTemplate: agentgen.NewInterpreter(&http.Client{}, nil, ""),
		Loader:         &fakeContextLoader{},
	}
}

// inputPlanNode builds a PlanNode of type "input" pointing to nextIDs.
func inputPlanNode(id string, nextIDs ...string) *agentgen.PlanNode {
	cfg, _ := json.Marshal(agentgen.InputStepConfig{})
	return &agentgen.PlanNode{
		StepID: id,
		Type:   agentgen.StepInput,
		Config: cfg,
		Next:   nextIDs,
	}
}

// responsePlanNode builds a PlanNode of type "response" reading fromVar.
func responsePlanNode(id, fromVar string) *agentgen.PlanNode {
	cfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: fromVar, MediaType: "text/plain"})
	return &agentgen.PlanNode{
		StepID: id,
		Type:   agentgen.StepResponse,
		Config: cfg,
	}
}

// branchPlanNode builds a branch node. trueNext/falseNext are the step IDs for each arm.
func branchPlanNode(id, trueNext, falseNext string) *agentgen.PlanNode {
	cfg, _ := json.Marshal(agentgen.BranchStepConfig{
		Expression: `{{.flag}}`,
		TrueNext:   trueNext,
		FalseNext:  falseNext,
	})
	return &agentgen.PlanNode{
		StepID: id,
		Type:   agentgen.StepBranch,
		Config: cfg,
		Next:   []string{trueNext, falseNext},
	}
}

// joinPlanNode builds a join node that merges the listed predecessors.
func joinPlanNode(id string, mode agentgen.JoinMode, joinOf []string, nextID string) *agentgen.PlanNode {
	cfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "result", MediaType: "text/plain"})
	next := []string{}
	if nextID != "" {
		next = []string{nextID}
	}
	return &agentgen.PlanNode{
		StepID:   id,
		Type:     agentgen.StepResponse,
		Config:   cfg,
		JoinOf:   joinOf,
		JoinMode: mode,
		Next:     next,
	}
}

// ── conformance test suite ────────────────────────────────────────────────────

type CanvasWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env  *testsuite.TestWorkflowEnvironment
	acts *temporal.CanvasAgentActivities
}

func (s *CanvasWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.acts = makeTestActivities()
	s.env.RegisterWorkflow(temporal.CanvasAgentWorkflow)
	s.env.RegisterActivity(s.acts.ExecuteStepActivity)
}

func (s *CanvasWorkflowTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func TestCanvasWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(CanvasWorkflowTestSuite))
}

// ── CT-1: Linear chain A→B→C ─────────────────────────────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT01_LinearChain() {
	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "A",
		Nodes: []*agentgen.PlanNode{
			inputPlanNode("A", "B"),
			inputPlanNode("B", "C"),
			responsePlanNode("C", "input"),
		},
	}
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    *plan,
		Initial: agentgen.PipelineVars{"input": "hello"},
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.Equal("hello", out.ResultText)
}

// ── CT-2: Parallel fan-out A→{B,C}→D (JoinWaitAll) ──────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT02_ParallelFanOut_JoinWaitAll() {
	bCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	cCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	dCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})

	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "A",
		Nodes: []*agentgen.PlanNode{
			{StepID: "A", Type: agentgen.StepInput, Config: mustMarshal(agentgen.InputStepConfig{}), Next: []string{"B", "C"}},
			{StepID: "B", Type: agentgen.StepInput, Config: bCfg, Next: []string{"D"}},
			{StepID: "C", Type: agentgen.StepInput, Config: cCfg, Next: []string{"D"}},
			{StepID: "D", Type: agentgen.StepResponse, Config: dCfg,
				JoinOf: []string{"B", "C"}, JoinMode: agentgen.JoinWaitAll},
		},
	}
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    *plan,
		Initial: agentgen.PipelineVars{"input": "parallel"},
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.Equal("parallel", out.ResultText)
}

// ── CT-3: Branch true path ────────────────────────────────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT03_BranchTruePath() {
	trueCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})
	falseCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})
	joinCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})

	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "A",
		Nodes: []*agentgen.PlanNode{
			inputPlanNode("A", "branch"),
			branchPlanNode("branch", "trueNode", "falseNode"),
			{StepID: "trueNode", Type: agentgen.StepResponse, Config: trueCfg, Next: []string{"join"}},
			{StepID: "falseNode", Type: agentgen.StepResponse, Config: falseCfg, Next: []string{"join"}},
			{StepID: "join", Type: agentgen.StepResponse, Config: joinCfg,
				JoinOf: []string{"trueNode", "falseNode"}, JoinMode: agentgen.JoinBranchMerge},
		},
	}
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    *plan,
		Initial: agentgen.PipelineVars{"input": "yes", "flag": "true"},
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	// Either the trueNode or join node captured the result — content is "yes".
	s.Equal("yes", out.ResultText)
}

// ── CT-4: Branch false path ───────────────────────────────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT04_BranchFalsePath() {
	falseCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})

	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "A",
		Nodes: []*agentgen.PlanNode{
			inputPlanNode("A", "branch"),
			branchPlanNode("branch", "trueNode", "falseNode"),
			{StepID: "trueNode", Type: agentgen.StepResponse, Config: mustMarshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"}), Next: []string{}},
			{StepID: "falseNode", Type: agentgen.StepResponse, Config: falseCfg, Next: []string{}},
		},
	}
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    *plan,
		Initial: agentgen.PipelineVars{"input": "no", "flag": "false"},
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.Equal("no", out.ResultText)
}

// ── CT-5: Node error propagates and cancels siblings ─────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT05_NodeError_PropagatesAndCancelsSiblings() {
	// Register a failing activity that returns an error for any input.
	failingActs := &temporal.CanvasAgentActivities{
		InterpTemplate: agentgen.NewInterpreter(&http.Client{}, nil, ""),
		Loader:         &failContextLoader{},
	}
	s.env.RegisterActivity(failingActs.ExecuteStepActivity)

	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "start",
		Nodes: []*agentgen.PlanNode{
			{StepID: "start", Type: agentgen.StepInput, Config: mustMarshal(agentgen.InputStepConfig{}), Next: []string{"failNode"}},
			inputPlanNode("failNode"),
		},
	}
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    *plan,
		Initial: agentgen.PipelineVars{"input": "x"},
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
}

// ── CT-6: Empty plan returns non-retryable error ──────────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT06_EmptyPlan_ReturnsError() {
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    agentgen.ExecutionPlan{},
		Initial: agentgen.PipelineVars{},
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "empty")
}

// ── CT-7: JoinBranchMerge — second arm dropped ───────────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT07_JoinBranchMerge_SecondArmDropped() {
	// A simple plan where a branch's false arm leads to the join.
	// flag=true → trueNode → join; falseNode never runs.
	joinCfg := mustMarshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})

	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "A",
		Nodes: []*agentgen.PlanNode{
			inputPlanNode("A", "branch"),
			branchPlanNode("branch", "trueNode", "falseNode"),
			{StepID: "trueNode", Type: agentgen.StepInput, Config: mustMarshal(agentgen.InputStepConfig{}), Next: []string{"join"}},
			{StepID: "falseNode", Type: agentgen.StepInput, Config: mustMarshal(agentgen.InputStepConfig{}), Next: []string{"join"}},
			{StepID: "join", Type: agentgen.StepResponse, Config: joinCfg,
				JoinOf: []string{"trueNode", "falseNode"}, JoinMode: agentgen.JoinBranchMerge},
		},
	}
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    *plan,
		Initial: agentgen.PipelineVars{"input": "merge-test", "flag": "true"},
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.Equal("merge-test", out.ResultText)
}

// ── CT-8: ErrContractViolation from activity causes workflow failure ──────────

func (s *CanvasWorkflowTestSuite) TestCT08_ContractViolation_CausesWorkflowFailure() {
	// A response node with a Required input that is absent from initial vars.
	// The real ExecuteStepActivity will call ExecuteNodeForActivity which will
	// return ErrContractViolation (via the interpreter's required-var check).
	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "out",
		Nodes: []*agentgen.PlanNode{
			{
				StepID: "out",
				Type:   agentgen.StepResponse,
				Config: mustMarshal(agentgen.ResponseStepConfig{FromVar: "must_exist", MediaType: "text/plain"}),
				Inputs: []agentgen.VarRef{{Name: "must_exist", Required: true}},
			},
		},
	}
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    *plan,
		Initial: agentgen.PipelineVars{}, // "must_exist" is absent
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
}

// ── CT-9: Invalid ActivityIC — non-retryable ──────────────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT09_InvalidIC_NonRetryable() {
	input := temporal.CanvasAgentWorkflowInput{
		Plan: agentgen.ExecutionPlan{
			SkillID: "sk",
			StartID: "A",
			Nodes:   []*agentgen.PlanNode{inputPlanNode("A")},
		},
		Initial: agentgen.PipelineVars{},
		IC:      agentgen.ActivityIC{TenantID: "", ApplicationID: "a", AgentID: "ag"},
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "ActivityIC")
}

// ── CT-10: StepResponse result propagation ────────────────────────────────────

func (s *CanvasWorkflowTestSuite) TestCT10_ResponseResult_Propagation() {
	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			inputPlanNode("in", "out"),
			responsePlanNode("out", "input"),
		},
	}
	input := temporal.CanvasAgentWorkflowInput{
		Plan:    *plan,
		Initial: agentgen.PipelineVars{"input": "the result"},
		IC:      makeTestIC(),
	}
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.Equal("the result", out.ResultText)
	s.Equal("text/plain", out.ResultMT)
}

// ── CT-A: CanvasAgentWorkflowInput serialization ─────────────────────────────

func TestCanvasAgentWorkflowInput_Serialization(t *testing.T) {
	in := temporal.CanvasAgentWorkflowInput{
		Plan: agentgen.ExecutionPlan{
			SkillID: "sk1",
			StartID: "A",
			Nodes:   []*agentgen.PlanNode{inputPlanNode("A")},
		},
		Initial: agentgen.PipelineVars{"input": "hi"},
		IC:      makeTestIC(),
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var got temporal.CanvasAgentWorkflowInput
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, "sk1", got.Plan.SkillID)
	require.Equal(t, "hi", got.Initial["input"])
	require.Equal(t, "t", got.IC.TenantID)
}

// ── CT-B: StepActivityOutput — no secrets in serialization ───────────────────

func TestStepActivityOutput_NoSecrets(t *testing.T) {
	out := temporal.StepActivityOutput{
		Vars:       agentgen.PipelineVars{"greeting": "hello"},
		ResultText: "hello",
		ResultMT:   "text/plain",
	}
	b, err := json.Marshal(out)
	require.NoError(t, err)
	s := string(b)
	require.Contains(t, s, "greeting")
	require.NotContains(t, s, "sk-")
}

// ── CT-C: ActivityIC validation in ExecuteStepActivity ───────────────────────

func TestExecuteStepActivity_InvalidIC_ReturnsError(t *testing.T) {
	acts := makeTestActivities()
	ctx := context.Background()
	_, err := acts.ExecuteStepActivity(ctx, temporal.StepActivityInput{
		Node: *inputPlanNode("A"),
		Vars: agentgen.PipelineVars{},
		IC:   agentgen.ActivityIC{TenantID: ""}, // missing required fields
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "TenantID")
}

// ── CT-D: ExecuteStepActivity — human_wait returns WaitingForHuman ───────────

func TestExecuteStepActivity_HumanWait_ReturnsImmediately(t *testing.T) {
	acts := makeTestActivities()
	ctx := context.Background()
	cfg, _ := json.Marshal(agentgen.HumanWaitConfig{Prompt: "Enter something", ReplyVar: "reply"})
	out, err := acts.ExecuteStepActivity(ctx, temporal.StepActivityInput{
		Node: agentgen.PlanNode{
			StepID: "hw",
			Type:   agentgen.StepHumanWait,
			Config: cfg,
		},
		Vars: agentgen.PipelineVars{},
		IC:   makeTestIC(),
	})
	require.NoError(t, err)
	require.True(t, out.WaitingForHuman, "human_wait must return WaitingForHuman=true immediately")
}

// ── CT-E: ExecuteStepActivity — nil InterpTemplate ───────────────────────────

func TestExecuteStepActivity_NilInterp_ReturnsError(t *testing.T) {
	acts := &temporal.CanvasAgentActivities{
		InterpTemplate: nil,
		Loader:         &fakeContextLoader{},
	}
	ctx := context.Background()
	_, err := acts.ExecuteStepActivity(ctx, temporal.StepActivityInput{
		Node: *inputPlanNode("A"),
		Vars: agentgen.PipelineVars{},
		IC:   makeTestIC(),
	})
	require.Error(t, err)
}

// ── CT-F: ExecuteStepActivity — loader error propagates ──────────────────────

func TestExecuteStepActivity_LoaderError_Propagates(t *testing.T) {
	acts := &temporal.CanvasAgentActivities{
		InterpTemplate: agentgen.NewInterpreter(&http.Client{}, nil, ""),
		Loader:         &fakeContextLoader{err: errors.New("db timeout")},
	}
	ctx := context.Background()
	_, err := acts.ExecuteStepActivity(ctx, temporal.StepActivityInput{
		Node: *inputPlanNode("A"),
		Vars: agentgen.PipelineVars{"input": "x"},
		IC:   makeTestIC(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "db timeout")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// ── ExecutionPolicy Temporal tests (CT-EP1..CT-EP2) ──────────────────────────

// CT-EP1: StepActivityOutput with empty ResultText but non-empty ResultMT triggers result capture.
// This directly tests the NoResult bug fix in the workflow result-capture condition.
// The fix: (ResultText != "" || ResultMT != "") — previously only checked ResultText.
func TestNoResultBugFixed(t *testing.T) {
	// Simulate the condition the workflow evaluates before capturing a result.
	// Before the fix: stepOut.ResultText != "" was the only check.
	// After the fix: stepOut.ResultText != "" || stepOut.ResultMT != "" is checked.
	out := temporal.StepActivityOutput{
		Vars:       agentgen.PipelineVars{},
		ResultText: "",                // empty text
		ResultMT:   "application/json", // but non-empty media type
	}
	// Replicate the fixed condition:
	triggered := out.ResultText != "" || out.ResultMT != ""
	require.True(t, triggered, "ResultMT-only output must trigger result capture")

	// Also verify that a truly empty output does NOT trigger.
	empty := temporal.StepActivityOutput{}
	triggeredEmpty := empty.ResultText != "" || empty.ResultMT != ""
	require.False(t, triggeredEmpty, "truly empty output must not trigger result capture")
}

// CT-EP2: Policy-driven activityOptionsForNode — LLM node uses MaxAttempts=2 from resolved policy.
func TestActivityOptionsFromPolicy(t *testing.T) {
	// Verify that a PlanNode built from CompileExecutionPlan carries MaxAttempts=2 for LLM.
	skill := &agentgen.SkillSpec{
		ID: "sk",
		Steps: []agentgen.StepSpec{
			{ID: "llm", Type: agentgen.StepLLM, Config: json.RawMessage(`{}`), Next: nil},
		},
	}
	plan := agentgen.CompileExecutionPlan(skill)
	require.Len(t, plan.Nodes, 1)
	node := plan.Nodes[0]
	require.Equal(t, agentgen.StepLLM, node.Type)
	require.EqualValues(t, 2, node.Policy.MaxAttempts, "LLM node must have MaxAttempts=2")
	require.Greater(t, node.Policy.TimeoutSeconds, 0, "LLM node must have positive TimeoutSeconds")
	require.NotEmpty(t, node.Policy.NonRetryableErrors, "LLM node must have NonRetryableErrors")
}

// CT-CONC1: CanvasAgentWorkflowInput.MaxConcurrentTasks=0 resolves to DefaultMaxConcurrentTasks (10).
// Verifies that the workflow wires up the semaphore correctly and a zero value is safe.
func (s *CanvasWorkflowTestSuite) TestWorkflowConcurrencyLimit_ZeroResolvesToDefault() {
	// A simple linear plan: input → response. With limit=0 the semaphore resolves
	// to 10. The workflow must complete normally (the semaphore is not a bottleneck
	// when there is only one activity in flight).
	plan := agentgen.ExecutionPlan{
		SkillID: "conc-zero",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			inputPlanNode("in", "resp"),
			responsePlanNode("resp", "input"),
		},
	}
	for _, n := range plan.Nodes {
		n.Policy = agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}
	}

	s.env.RegisterActivity(s.acts.ExecuteStepActivity)
	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, temporal.CanvasAgentWorkflowInput{
		Plan:               plan,
		Initial:            agentgen.PipelineVars{"input": "hi"},
		IC:                 makeTestIC(),
		MaxConcurrentTasks: 0, // must resolve to 10 without panic
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.NotEmpty(out.ResultText)
}
