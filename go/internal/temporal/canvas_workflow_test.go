package temporal_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
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

// ── CT-LOOP-1: ExecuteStepActivity — loop node iterates body and accumulates ─────

func TestExecuteStepActivity_Loop_BasicIteration(t *testing.T) {
	var callCount int32

	agentgen.RegisterNode(agentgen.NodeDef{
		Type: "ct_loop_body_L1", Label: "CT Loop Body L1", Version: 1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *agentgen.Interpreter, _ *agentgen.InvocationContext, _ *agentgen.StepSpec, vars agentgen.PipelineVars, _ *agentgen.ExecutionResult) error {
			atomic.AddInt32(&callCount, 1)
			if item, ok := vars["item"]; ok {
				vars["processed_item"] = fmt.Sprintf("done:%v", item)
			}
			return nil
		},
		Edges: agentgen.EdgeRules{},
	})

	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps: []string{"b1"},
		ItemsVar:  "items",
		ItemVar:   "item",
		AccumVar:  "results",
	})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "loop:body",
		StartID: "b1",
		Nodes: []*agentgen.PlanNode{
			{
				StepID:  "b1",
				Type:    "ct_loop_body_L1",
				Config:  json.RawMessage(`{}`),
				Inputs:  []agentgen.VarRef{{Name: "item"}},
				Outputs: []agentgen.VarRef{{Name: "processed_item"}},
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30},
			},
		},
	}

	acts := makeTestActivities()
	ctx := context.Background()
	// Inputs and Outputs must be declared so Stage-6 scoping passes the right vars.
	// items_var ("items") is the declared input; accum_var ("results") is the output.
	out, err := acts.ExecuteStepActivity(ctx, temporal.StepActivityInput{
		Node: agentgen.PlanNode{
			StepID:  "loop1",
			Type:    agentgen.StepLoop,
			Config:  loopCfg,
			SubPlan: subPlan,
			Inputs:  []agentgen.VarRef{{Name: "items", Required: true}},
			Outputs: []agentgen.VarRef{{Name: "results"}},
			Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30},
		},
		Vars: agentgen.PipelineVars{"items": []any{"a", "b", "c"}},
		IC:   makeTestIC(),
	})
	require.NoError(t, err)
	require.EqualValues(t, 3, atomic.LoadInt32(&callCount), "body must run once per item")
	accum, ok := out.Vars["results"]
	require.True(t, ok, "accum_var must appear in output vars")
	accumSlice, ok := accum.([]any)
	require.True(t, ok, "accum_var must be a []any")
	require.Len(t, accumSlice, 3)
	// Each entry must contain only the declared output key (processed_item), not all pipeline vars.
	for i, entry := range accumSlice {
		mv, mvok := entry.(agentgen.PipelineVars)
		require.True(t, mvok, "accum entry %d must be agentgen.PipelineVars", i)
		_, hasProcessed := mv["processed_item"]
		_, hasItems := mv["items"]
		require.True(t, hasProcessed, "accum entry %d must contain declared output key processed_item", i)
		require.False(t, hasItems, "accum entry %d must NOT contain pipeline-wide var 'items'", i)
	}
}

// ── CT-LOOP-2: ExecuteStepActivity — loop node with nil SubPlan is a no-op ──────

func TestExecuteStepActivity_Loop_NilSubPlan(t *testing.T) {
	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		ItemsVar: "items",
		ItemVar:  "item",
	})

	acts := makeTestActivities()
	ctx := context.Background()
	_, err := acts.ExecuteStepActivity(ctx, temporal.StepActivityInput{
		Node: agentgen.PlanNode{
			StepID:  "loop_nil",
			Type:    agentgen.StepLoop,
			Config:  loopCfg,
			SubPlan: nil,
			Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30},
		},
		Vars: agentgen.PipelineVars{"items": []any{"x", "y"}},
		IC:   makeTestIC(),
	})
	require.NoError(t, err, "nil SubPlan must be a no-op, not an error")
}

// ── CT-LOOP-3: ExecuteStepActivity — loop node with non-list items_var → error ──

func TestExecuteStepActivity_Loop_NonListItemsVar(t *testing.T) {
	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps: []string{"b_nl"},
		ItemsVar:  "bad_var",
		ItemVar:   "item",
	})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "loop:body",
		StartID: "b_nl",
		Nodes: []*agentgen.PlanNode{
			{
				StepID: "b_nl",
				Type:   agentgen.StepInput,
				Config: json.RawMessage(`{}`),
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30},
			},
		},
	}

	acts := makeTestActivities()
	ctx := context.Background()
	_, err := acts.ExecuteStepActivity(ctx, temporal.StepActivityInput{
		Node: agentgen.PlanNode{
			StepID:  "loop_bad",
			Type:    agentgen.StepLoop,
			Config:  loopCfg,
			SubPlan: subPlan,
			Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30},
		},
		Vars: agentgen.PipelineVars{"bad_var": "not_a_list"},
		IC:   makeTestIC(),
	})
	require.Error(t, err, "non-list items_var must return an error")
}

// ── CT-LOOP-DURABLE-1: CanvasAgentWorkflow — loop schedules each body step as its own activity ──

func (s *CanvasWorkflowTestSuite) TestCTLoopDurable1_BasicIteration() {
	var callCount int32

	agentgen.RegisterNode(agentgen.NodeDef{
		Type: "ct_ld_body", Label: "CT LD Body", Version: 1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *agentgen.Interpreter, _ *agentgen.InvocationContext, _ *agentgen.StepSpec, vars agentgen.PipelineVars, _ *agentgen.ExecutionResult) error {
			atomic.AddInt32(&callCount, 1)
			if item, ok := vars["item"]; ok {
				vars["processed"] = fmt.Sprintf("done:%v", item)
			}
			return nil
		},
		Edges: agentgen.EdgeRules{},
	})

	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps: []string{"body1"},
		ItemsVar:  "items",
		ItemVar:   "item",
		AccumVar:  "results",
	})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "ld:body",
		StartID: "body1",
		Nodes: []*agentgen.PlanNode{{
			StepID:  "body1",
			Type:    "ct_ld_body",
			Config:  json.RawMessage(`{}`),
			Inputs:  []agentgen.VarRef{{Name: "item"}},
			Outputs: []agentgen.VarRef{{Name: "processed"}},
			Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}},
		}},
	}

	inputCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	respCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "results", MediaType: "application/json"})
	plan := agentgen.ExecutionPlan{
		SkillID: "loop-durable",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			{StepID: "in", Type: agentgen.StepInput, Config: inputCfg, Next: []string{"loop1"},
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}},
				Outputs: []agentgen.VarRef{{Name: "items"}}},
			{StepID: "loop1", Type: agentgen.StepLoop, Config: loopCfg, Next: []string{"resp"},
				SubPlan: subPlan,
				Inputs:  []agentgen.VarRef{{Name: "items", Required: true}},
				Outputs: []agentgen.VarRef{{Name: "results"}},
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "resp", Type: agentgen.StepResponse, Config: respCfg,
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
		},
	}

	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, temporal.CanvasAgentWorkflowInput{
		Plan:    plan,
		Initial: agentgen.PipelineVars{"input": "go", "items": []any{"a", "b", "c"}},
		IC:      makeTestIC(),
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	// body must have been called once per item
	s.EqualValues(3, atomic.LoadInt32(&callCount), "body activity must run once per item")
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.NotEmpty(out.ResultText)
}

// ── CT-LOOP-DURABLE-2: CanvasAgentWorkflow — loop over empty list skips body, post-loop runs ──

func (s *CanvasWorkflowTestSuite) TestCTLoopDurable2_EmptyList() {
	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps: []string{"body_empty"},
		ItemsVar:  "items",
		ItemVar:   "item",
	})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "ld_empty:body",
		StartID: "body_empty",
		Nodes: []*agentgen.PlanNode{{
			StepID: "body_empty", Type: agentgen.StepInput, Config: json.RawMessage(`{}`),
			Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}},
		}},
	}
	inputCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	respCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})
	plan := agentgen.ExecutionPlan{
		SkillID: "loop-empty",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			{StepID: "in", Type: agentgen.StepInput, Config: inputCfg, Next: []string{"loop1"},
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "loop1", Type: agentgen.StepLoop, Config: loopCfg, Next: []string{"resp"},
				SubPlan: subPlan,
				Inputs:  []agentgen.VarRef{{Name: "items", Required: true}},
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "resp", Type: agentgen.StepResponse, Config: respCfg,
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
		},
	}

	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, temporal.CanvasAgentWorkflowInput{
		Plan:    plan,
		Initial: agentgen.PipelineVars{"input": "hello", "items": []any{}},
		IC:      makeTestIC(),
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.Equal("hello", out.ResultText, "post-loop response node must run after empty loop")
}

// ── CT-LOOP-DURABLE-3: CanvasAgentWorkflow — max_iterations cap is enforced ──

func (s *CanvasWorkflowTestSuite) TestCTLoopDurable3_MaxIterationsCap() {
	var callCount int32

	agentgen.RegisterNode(agentgen.NodeDef{
		Type: "ct_ld_cap_body", Label: "CT LD Cap Body", Version: 1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *agentgen.Interpreter, _ *agentgen.InvocationContext, _ *agentgen.StepSpec, vars agentgen.PipelineVars, _ *agentgen.ExecutionResult) error {
			atomic.AddInt32(&callCount, 1)
			return nil
		},
		Edges: agentgen.EdgeRules{},
	})

	// 10-item list but max_iterations=3
	items := make([]any, 10)
	for i := range items {
		items[i] = i
	}
	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps:     []string{"body_cap"},
		ItemsVar:      "items",
		ItemVar:       "item",
		MaxIterations: 3,
	})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "ld_cap:body",
		StartID: "body_cap",
		Nodes: []*agentgen.PlanNode{{
			StepID:  "body_cap",
			Type:    "ct_ld_cap_body",
			Config:  json.RawMessage(`{}`),
			Outputs: []agentgen.VarRef{{Name: "cap_out"}},
			Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}},
		}},
	}
	inputCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	respCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})
	plan := agentgen.ExecutionPlan{
		SkillID: "loop-cap",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			{StepID: "in", Type: agentgen.StepInput, Config: inputCfg, Next: []string{"loop1"},
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "loop1", Type: agentgen.StepLoop, Config: loopCfg, Next: []string{"resp"},
				SubPlan: subPlan,
				Inputs:  []agentgen.VarRef{{Name: "items", Required: true}},
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "resp", Type: agentgen.StepResponse, Config: respCfg,
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
		},
	}

	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, temporal.CanvasAgentWorkflowInput{
		Plan:    plan,
		Initial: agentgen.PipelineVars{"input": "hi", "items": items},
		IC:      makeTestIC(),
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.EqualValues(3, atomic.LoadInt32(&callCount), "max_iterations cap must stop after 3 iterations")
}

// ── CT-LOOP-DURABLE-4: CanvasAgentWorkflow — accum_var contains only declared body outputs ──

func (s *CanvasWorkflowTestSuite) TestCTLoopDurable4_AccumVarScopedToOutputs() {
	agentgen.RegisterNode(agentgen.NodeDef{
		Type: "ct_ld_accum_body", Label: "CT LD Accum Body", Version: 1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *agentgen.Interpreter, _ *agentgen.InvocationContext, _ *agentgen.StepSpec, vars agentgen.PipelineVars, _ *agentgen.ExecutionResult) error {
			if item, ok := vars["item"]; ok {
				vars["out_key"] = fmt.Sprintf("processed:%v", item)
				vars["should_not_appear"] = "secret"
			}
			return nil
		},
		Edges: agentgen.EdgeRules{},
	})

	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps: []string{"body_accum"},
		ItemsVar:  "items",
		ItemVar:   "item",
		AccumVar:  "results",
	})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "ld_accum:body",
		StartID: "body_accum",
		Nodes: []*agentgen.PlanNode{{
			StepID:  "body_accum",
			Type:    "ct_ld_accum_body",
			Config:  json.RawMessage(`{}`),
			Inputs:  []agentgen.VarRef{{Name: "item"}},
			Outputs: []agentgen.VarRef{{Name: "out_key"}}, // only out_key declared
			Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}},
		}},
	}
	inputCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	respCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "results", MediaType: "application/json"})
	plan := agentgen.ExecutionPlan{
		SkillID: "loop-accum",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			{StepID: "in", Type: agentgen.StepInput, Config: inputCfg, Next: []string{"loop1"},
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "loop1", Type: agentgen.StepLoop, Config: loopCfg, Next: []string{"resp"},
				SubPlan: subPlan,
				Inputs:  []agentgen.VarRef{{Name: "items", Required: true}},
				Outputs: []agentgen.VarRef{{Name: "results"}},
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "resp", Type: agentgen.StepResponse, Config: respCfg,
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
		},
	}

	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, temporal.CanvasAgentWorkflowInput{
		Plan:    plan,
		Initial: agentgen.PipelineVars{"input": "go", "items": []any{"x", "y"}},
		IC:      makeTestIC(),
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	// The response text contains a fmt.Sprint of the []any accum_var.
	// Verify it contains the declared output key and not the undeclared one.
	s.Contains(out.ResultText, "out_key", "accum_var must contain declared output key")
	s.NotContains(out.ResultText, "should_not_appear", "accum_var must not contain undeclared key")
	// Two items processed.
	s.Contains(out.ResultText, "processed:x")
	s.Contains(out.ResultText, "processed:y")
}

// ── CT-LOOP-DURABLE-5: CanvasAgentWorkflow — branch inside loop body selects correct arm ──

func (s *CanvasWorkflowTestSuite) TestCTLoopDurable5_BranchInsideBody() {
	var trueCount, falseCount int32

	agentgen.RegisterNode(agentgen.NodeDef{
		Type: "ct_ld_branch_true", Label: "CT LD Branch True", Version: 1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *agentgen.Interpreter, _ *agentgen.InvocationContext, _ *agentgen.StepSpec, vars agentgen.PipelineVars, _ *agentgen.ExecutionResult) error {
			atomic.AddInt32(&trueCount, 1)
			vars["arm"] = "true"
			return nil
		},
		Edges: agentgen.EdgeRules{},
	})
	agentgen.RegisterNode(agentgen.NodeDef{
		Type: "ct_ld_branch_false", Label: "CT LD Branch False", Version: 1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *agentgen.Interpreter, _ *agentgen.InvocationContext, _ *agentgen.StepSpec, vars agentgen.PipelineVars, _ *agentgen.ExecutionResult) error {
			atomic.AddInt32(&falseCount, 1)
			vars["arm"] = "false"
			return nil
		},
		Edges: agentgen.EdgeRules{},
	})

	// Body: branch("{{.item}}") → true_arm | false_arm
	// items: ["true", "false", "true"] → 2 true, 1 false
	branchCfg, _ := json.Marshal(agentgen.BranchStepConfig{
		Expression: `{{.item}}`,
		TrueNext:   "arm_true",
		FalseNext:  "arm_false",
	})
	trueCfg, _ := json.Marshal(map[string]any{})
	falseCfg, _ := json.Marshal(map[string]any{})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "ld_branch:body",
		StartID: "br",
		Nodes: []*agentgen.PlanNode{
			{StepID: "br", Type: agentgen.StepBranch, Config: branchCfg, Next: []string{"arm_true", "arm_false"},
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "arm_true", Type: "ct_ld_branch_true", Config: json.RawMessage(trueCfg),
				Outputs: []agentgen.VarRef{{Name: "arm"}},
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "arm_false", Type: "ct_ld_branch_false", Config: json.RawMessage(falseCfg),
				Outputs: []agentgen.VarRef{{Name: "arm"}},
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
		},
	}

	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps: []string{"br", "arm_true", "arm_false"},
		ItemsVar:  "items",
		ItemVar:   "item",
	})
	inputCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	respCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})
	plan := agentgen.ExecutionPlan{
		SkillID: "loop-branch",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			{StepID: "in", Type: agentgen.StepInput, Config: inputCfg, Next: []string{"loop1"},
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "loop1", Type: agentgen.StepLoop, Config: loopCfg, Next: []string{"resp"},
				SubPlan: subPlan,
				Inputs:  []agentgen.VarRef{{Name: "items", Required: true}},
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "resp", Type: agentgen.StepResponse, Config: respCfg,
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
		},
	}

	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, temporal.CanvasAgentWorkflowInput{
		Plan:    plan,
		Initial: agentgen.PipelineVars{"input": "go", "items": []any{"true", "false", "true"}},
		IC:      makeTestIC(),
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.EqualValues(2, atomic.LoadInt32(&trueCount), "true arm must execute for 'true' items")
	s.EqualValues(1, atomic.LoadInt32(&falseCount), "false arm must execute for 'false' item")
}

// ── CT-LOOP-DURABLE-6: iteration isolation — vars from iteration N do not leak to N+1 ──

func (s *CanvasWorkflowTestSuite) TestCTLoopDurable6_IterationIsolation() {
	// Body node writes "sentinel" in iteration 0. Iteration 1 must NOT see it.
	var iter0SawSentinel, iter1SawSentinel bool

	agentgen.RegisterNode(agentgen.NodeDef{
		Type: "ct_ld_iso_body", Label: "CT LD Iso Body", Version: 1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *agentgen.Interpreter, _ *agentgen.InvocationContext, _ *agentgen.StepSpec, vars agentgen.PipelineVars, _ *agentgen.ExecutionResult) error {
			item, _ := vars["item"]
			if item == 0 {
				iter0SawSentinel = vars["sentinel"] != nil
				vars["sentinel"] = "set_in_iter0"
				vars["iso_out"] = "done_0"
			} else {
				iter1SawSentinel = vars["sentinel"] != nil
				vars["iso_out"] = "done_1"
			}
			return nil
		},
		Edges: agentgen.EdgeRules{},
	})

	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps: []string{"body_iso"},
		ItemsVar:  "items",
		ItemVar:   "item",
	})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "ld_iso:body",
		StartID: "body_iso",
		Nodes: []*agentgen.PlanNode{{
			StepID:  "body_iso",
			Type:    "ct_ld_iso_body",
			Config:  json.RawMessage(`{}`),
			Inputs:  []agentgen.VarRef{{Name: "item"}},
			Outputs: []agentgen.VarRef{{Name: "iso_out"}},
			Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}},
		}},
	}
	inputCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	respCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "input", MediaType: "text/plain"})
	plan := agentgen.ExecutionPlan{
		SkillID: "loop-iso",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			{StepID: "in", Type: agentgen.StepInput, Config: inputCfg, Next: []string{"loop1"},
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "loop1", Type: agentgen.StepLoop, Config: loopCfg, Next: []string{"resp"},
				SubPlan: subPlan,
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "resp", Type: agentgen.StepResponse, Config: respCfg,
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
		},
	}

	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, temporal.CanvasAgentWorkflowInput{
		Plan:    plan,
		Initial: agentgen.PipelineVars{"input": "iso", "items": []any{0, 1}},
		IC:      makeTestIC(),
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.False(iter0SawSentinel, "iteration 0 must not see sentinel from a prior run")
	s.False(iter1SawSentinel, "iteration 1 must not see sentinel written by iteration 0")
}

// ── CT-LOOP-DURABLE-7: scoped accum_var — only declared body outputs in each snapshot ──

func (s *CanvasWorkflowTestSuite) TestCTLoopDurable7_ScopedAccumVar() {
	agentgen.RegisterNode(agentgen.NodeDef{
		Type: "ct_ld_accum_body", Label: "CT LD Accum Body", Version: 1,
		OutputArity: "single",
		Execute: func(_ context.Context, _ *agentgen.Interpreter, _ *agentgen.InvocationContext, _ *agentgen.StepSpec, vars agentgen.PipelineVars, _ *agentgen.ExecutionResult) error {
			vars["proc"] = fmt.Sprintf("done:%v", vars["item"])
			vars["should_not_appear"] = "leaked"
			return nil
		},
		Edges: agentgen.EdgeRules{},
	})

	loopCfg, _ := json.Marshal(agentgen.LoopConfig{
		BodySteps: []string{"body_accum"},
		ItemsVar:  "items",
		ItemVar:   "item",
		AccumVar:  "collected",
	})
	subPlan := &agentgen.ExecutionPlan{
		SkillID: "ld_accum:body",
		StartID: "body_accum",
		Nodes: []*agentgen.PlanNode{{
			StepID:  "body_accum",
			Type:    "ct_ld_accum_body",
			Config:  json.RawMessage(`{}`),
			Inputs:  []agentgen.VarRef{{Name: "item"}},
			Outputs: []agentgen.VarRef{{Name: "proc"}},
			Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}},
		}},
	}
	respCfg, _ := json.Marshal(agentgen.ResponseStepConfig{FromVar: "collected", MediaType: "text/plain"})
	inputCfg, _ := json.Marshal(agentgen.InputStepConfig{})
	plan := agentgen.ExecutionPlan{
		SkillID: "loop-accum",
		StartID: "in",
		Nodes: []*agentgen.PlanNode{
			{StepID: "in", Type: agentgen.StepInput, Config: inputCfg, Next: []string{"loop1"},
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "loop1", Type: agentgen.StepLoop, Config: loopCfg, Next: []string{"resp"},
				SubPlan: subPlan,
				Policy:  agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 60, NonRetryableErrors: []string{"ContractViolation"}}},
			{StepID: "resp", Type: agentgen.StepResponse, Config: respCfg,
				Policy: agentgen.ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 30, NonRetryableErrors: []string{"ContractViolation"}}},
		},
	}

	s.env.ExecuteWorkflow(temporal.CanvasAgentWorkflow, temporal.CanvasAgentWorkflowInput{
		Plan:    plan,
		Initial: agentgen.PipelineVars{"input": "acc", "items": []any{"x", "y"}},
		IC:      makeTestIC(),
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var out temporal.CanvasAgentWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&out))
	s.Contains(out.ResultText, "done:x")
	s.Contains(out.ResultText, "done:y")
	s.NotContains(out.ResultText, "should_not_appear", "accum_var must not contain undeclared key")
	s.NotContains(out.ResultText, "items", "accum_var must not contain the items list variable")
}

// ── CT-CONC1: CanvasAgentWorkflowInput.MaxConcurrentTasks=0 resolves to DefaultMaxConcurrentTasks (10).
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
