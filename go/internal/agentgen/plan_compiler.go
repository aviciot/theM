package agentgen

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaxLoopHistoryBudget is the maximum number of Temporal history events a loop
// body may generate (body steps × max_iterations). ValidateLoopBodies returns an
// error when the resolved cap would exceed this limit.
const MaxLoopHistoryBudget = 5000

// ValidateLoopBodies validates all loop nodes in skill before compilation.
//
// Checks:
//   - Every step ID in body_steps exists in the skill's step list.
//   - len(body_steps) × effective_max_iterations ≤ MaxLoopHistoryBudget.
//
// Call this before CompileExecutionPlan at publish / invocation time.
func ValidateLoopBodies(skill *SkillSpec) error {
	if skill == nil {
		return nil
	}
	stepByID := make(map[string]bool, len(skill.Steps))
	for _, s := range skill.Steps {
		stepByID[s.ID] = true
	}
	for _, s := range skill.Steps {
		if s.Type != StepLoop {
			continue
		}
		var lc LoopConfig
		if len(s.Config) > 0 {
			_ = json.Unmarshal(s.Config, &lc)
		}
		for _, bID := range lc.BodySteps {
			if !stepByID[bID] {
				return fmt.Errorf("loop step %q: body_steps references unknown step %q", s.ID, bID)
			}
		}
		maxIter := lc.MaxIterations
		if maxIter <= 0 {
			maxIter = 100
		}
		if budget := len(lc.BodySteps) * maxIter; budget > MaxLoopHistoryBudget {
			return fmt.Errorf("loop step %q: body_steps(%d) × max_iterations(%d) = %d exceeds MaxLoopHistoryBudget(%d)",
				s.ID, len(lc.BodySteps), maxIter, budget, MaxLoopHistoryBudget)
		}
	}
	return nil
}

// resolvePolicy computes the final ExecutionPolicy for a PlanNode.
// Resolution order: NodeDef.DefaultPolicy → method/mutation upgrade → canvas override (clamped to MaxPolicy).
// NonRetryableErrors is always taken from the NodeDef and is not user-overridable.
func resolvePolicy(nd *NodeDef, cfg json.RawMessage, override *ExecutionPolicy) ExecutionPolicy {
	p := nd.DefaultPolicy

	// HTTP: upgrade GET to 3 attempts; POST/PUT/PATCH/DELETE stay at 1.
	// RequiresIdempotencyKey is only meaningful when MaxAttempts > 1 (i.e. retries can occur).
	if nd.Type == StepHTTP {
		method := extractHTTPMethod(cfg)
		if method == "" || strings.EqualFold(method, "GET") {
			p.MaxAttempts = 3
			p.RequiresIdempotencyKey = false
		} else {
			// Mutating HTTP: single attempt by default — no idempotency key needed.
			// If a canvas override raises MaxAttempts > 1, the idempotency requirement kicks in (see below).
			p.MaxAttempts = 1
			p.RequiresIdempotencyKey = false
		}
	}

	// MCP: read-only tools → 2 attempts; mutating → 1 (same reasoning as HTTP above).
	if nd.Type == StepMCPCall {
		if isMutatingMCPTool(cfg) {
			p.MaxAttempts = 1
			p.RequiresIdempotencyKey = false
		} else {
			p.MaxAttempts = 2
		}
	}

	// Apply canvas override, clamped to MaxPolicy.
	if override != nil {
		if override.MaxAttempts > 0 {
			capped := override.MaxAttempts
			if nd.MaxPolicy.MaxAttempts > 0 && capped > nd.MaxPolicy.MaxAttempts {
				capped = nd.MaxPolicy.MaxAttempts
			}
			p.MaxAttempts = capped
		}
		if override.TimeoutSeconds > 0 {
			capped := override.TimeoutSeconds
			if nd.MaxPolicy.TimeoutSeconds > 0 && capped > nd.MaxPolicy.TimeoutSeconds {
				capped = nd.MaxPolicy.TimeoutSeconds
			}
			p.TimeoutSeconds = capped
		}
		if override.InitialIntervalSeconds > 0 {
			p.InitialIntervalSeconds = override.InitialIntervalSeconds
		}
		if override.BackoffCoefficient > 0 {
			p.BackoffCoefficient = override.BackoffCoefficient
		}
		if override.MaxIntervalSeconds > 0 {
			p.MaxIntervalSeconds = override.MaxIntervalSeconds
		}
	}

	// NonRetryableErrors is always canonical — never user-overridable.
	p.NonRetryableErrors = nd.DefaultPolicy.NonRetryableErrors

	// Guard: zero MaxAttempts means "not set"; treat as 1.
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 1
	}

	// Hard-clamp: mutating MCP tools are always limited to MaxAttempts=1 regardless of any
	// canvas override. No real idempotency metadata exists for MCP tools today, so allowing
	// retries would risk double-spend on state-changing operations without any safety net.
	if nd.Type == StepMCPCall && isMutatingMCPTool(cfg) {
		p.MaxAttempts = 1
	}

	// RequiresIdempotencyKey: set when MaxAttempts > 1 AND the node type is mutating by nature.
	// HTTP nodes whose method is NOT GET are inherently mutating.
	// MCP nodes with a mutating tool name are inherently mutating.
	// This is re-evaluated AFTER clamping so the override's MaxAttempts takes effect first.
	if nd.Type == StepHTTP {
		method := extractHTTPMethod(cfg)
		if method != "" && !strings.EqualFold(method, "GET") && p.MaxAttempts > 1 {
			p.RequiresIdempotencyKey = true
		}
	}
	if nd.Type == StepMCPCall {
		if isMutatingMCPTool(cfg) && p.MaxAttempts > 1 {
			p.RequiresIdempotencyKey = true
		}
	}
	// Canvas override can also explicitly request it (upgrade only).
	if override != nil && override.RequiresIdempotencyKey {
		p.RequiresIdempotencyKey = true
	}

	return p
}

// extractHTTPMethod reads the "method" key from an HTTP step config JSON.
// Returns "" when unset (caller treats "" as GET).
func extractHTTPMethod(cfg json.RawMessage) string {
	if len(cfg) == 0 {
		return ""
	}
	var c struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(c.Method))
}

// isMutatingMCPTool returns true when the tool_name suggests a state-changing operation.
func isMutatingMCPTool(cfg json.RawMessage) bool {
	if len(cfg) == 0 {
		return false
	}
	var c struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		return false
	}
	name := strings.ToLower(c.ToolName)
	for _, keyword := range []string{"create", "update", "delete", "set", "write", "post", "put", "patch", "remove", "insert", "add", "push"} {
		if strings.Contains(name, keyword) {
			return true
		}
	}
	return false
}

// CompileExecutionPlan converts a SkillSpec into an ExecutionPlan.
//
// Join semantics:
//   - JoinWaitAll: node reached by >1 predecessors where the fan-out was from a
//     true parallel source (non-Branch step). ALL predecessor branches always
//     execute — must wait for all.
//   - JoinBranchMerge: node reached by >1 predecessors where ALL those predecessors
//     are direct children of a single Branch step (true/false arms). Only ONE arm
//     executes — first arrival continues.
//
// Detection rule for JoinBranchMerge: for a join node J with predecessors P1..Pn,
// check whether there exists a single Branch step B such that every Pi has B as
// its only predecessor AND B's Next list covers exactly {P1..Pn}. If yes →
// JoinBranchMerge; otherwise → JoinWaitAll.
//
// Loop nodes: LoopConfig.BodySteps are extracted into PlanNode.SubPlan and removed
// from the outer plan. The outer plan sees the loop as a single opaque node; its
// Next points to the first post-loop step (the step after the last body step).
//
// The SkillSpec.Steps slice must be topologically sorted. No cycle detection is
// done here — the compiler enforces acyclicity upstream.
func CompileExecutionPlan(skill *SkillSpec) *ExecutionPlan {
	if skill == nil || len(skill.Steps) == 0 {
		return &ExecutionPlan{SkillID: safeSkillID(skill)}
	}

	// Collect body step IDs from all loop nodes so they can be excluded from the
	// outer plan and compiled into per-loop SubPlans instead.
	bodyStepIDs := make(map[string]bool)
	stepByID := make(map[string]StepSpec, len(skill.Steps))
	for _, s := range skill.Steps {
		stepByID[s.ID] = s
		if s.Type == StepLoop {
			var lc LoopConfig
			if len(s.Config) > 0 {
				_ = json.Unmarshal(s.Config, &lc)
			}
			for _, bID := range lc.BodySteps {
				bodyStepIDs[bID] = true
			}
		}
	}

	// Outer steps: exclude body steps that belong to a loop's SubPlan.
	outerSteps := make([]StepSpec, 0, len(skill.Steps))
	for _, s := range skill.Steps {
		if !bodyStepIDs[s.ID] {
			outerSteps = append(outerSteps, s)
		}
	}

	// Build step-type and predecessor maps from outer steps only.
	stepTypes := make(map[string]StepType, len(outerSteps))
	for _, s := range outerSteps {
		stepTypes[s.ID] = s.Type
	}

	// preds: targetID → []predecessorID
	preds := make(map[string][]string, len(outerSteps))
	for _, s := range outerSteps {
		if _, ok := preds[s.ID]; !ok {
			preds[s.ID] = nil
		}
		for _, next := range s.Next {
			preds[next] = append(preds[next], s.ID)
		}
	}

	// nextSet: stepID → set of direct successors (for branch-coverage check).
	nextSet := make(map[string]map[string]bool, len(outerSteps))
	for _, s := range outerSteps {
		set := make(map[string]bool, len(s.Next))
		for _, n := range s.Next {
			set[n] = true
		}
		nextSet[s.ID] = set
	}

	nodes := make([]*PlanNode, 0, len(outerSteps))
	for _, s := range outerSteps {
		// Resolve the execution policy from the NodeDef defaults + optional canvas override.
		policy := ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable}
		if nd, ok := LookupNode(s.Type); ok {
			policy = resolvePolicy(nd, s.Config, s.PolicyOverride)
		}

		// For loop nodes, remap Next to skip over body steps and point to the
		// first post-loop step (the step that follows the last body step).
		outerNext := s.Next
		if s.Type == StepLoop {
			outerNext = resolveLoopOuterNext(s, stepByID, bodyStepIDs)
		}

		node := &PlanNode{
			StepID:   s.ID,
			Type:     s.Type,
			Config:   s.Config,
			Next:     outerNext,
			Inputs:   s.Inputs,
			Outputs:  s.Outputs,
			Branches: s.Branches,
			JoinMode: JoinNone,
			Policy:   policy,
		}

		// Loop nodes: compile BodySteps into SubPlan.
		if s.Type == StepLoop {
			node.SubPlan = compileLoopBodyPlan(s, stepByID)
		}

		if predecessors := preds[s.ID]; len(predecessors) > 1 {
			node.JoinOf = predecessors
			node.JoinMode = classifyJoin(s.ID, predecessors, stepTypes, preds, nextSet)
		}

		nodes = append(nodes, node)
	}

	startID := ""
	if len(outerSteps) > 0 {
		startID = outerSteps[0].ID
	}

	return &ExecutionPlan{
		SkillID: skill.ID,
		StartID: startID,
		Nodes:   nodes,
	}
}

// resolveLoopOuterNext finds the first post-loop step: the step that follows the last body step
// but is NOT itself a body step. This becomes the loop node's Next in the outer plan.
func resolveLoopOuterNext(loopStep StepSpec, stepByID map[string]StepSpec, bodyStepIDs map[string]bool) []string {
	var lc LoopConfig
	if len(loopStep.Config) > 0 {
		_ = json.Unmarshal(loopStep.Config, &lc)
	}
	if len(lc.BodySteps) == 0 {
		return loopStep.Next
	}
	// Walk the last body step's Next to find the first non-body successor.
	lastBodyID := lc.BodySteps[len(lc.BodySteps)-1]
	if last, ok := stepByID[lastBodyID]; ok {
		for _, nextID := range last.Next {
			if !bodyStepIDs[nextID] {
				return []string{nextID}
			}
		}
	}
	return nil
}

// compileLoopBodyPlan builds an ExecutionPlan for the body steps declared in a loop node's config.
// Body steps form a linear sub-plan; the last body step's Next is cleared (the loop executor
// controls when iteration ends, not the DAG walker).
func compileLoopBodyPlan(loopStep StepSpec, stepByID map[string]StepSpec) *ExecutionPlan {
	var lc LoopConfig
	if len(loopStep.Config) > 0 {
		_ = json.Unmarshal(loopStep.Config, &lc)
	}
	if len(lc.BodySteps) == 0 {
		return &ExecutionPlan{SkillID: loopStep.ID + ":body"}
	}

	bodyNodes := make([]*PlanNode, 0, len(lc.BodySteps))
	for i, bID := range lc.BodySteps {
		s, ok := stepByID[bID]
		if !ok {
			continue
		}
		policy := ExecutionPolicy{MaxAttempts: 1, TimeoutSeconds: 300, NonRetryableErrors: stdNonRetryable}
		if nd, ok2 := LookupNode(s.Type); ok2 {
			policy = resolvePolicy(nd, s.Config, s.PolicyOverride)
		}
		// The last body step must not jump out of the sub-plan; clear its Next.
		next := s.Next
		if i == len(lc.BodySteps)-1 {
			next = nil
		}
		bodyNodes = append(bodyNodes, &PlanNode{
			StepID:   s.ID,
			Type:     s.Type,
			Config:   s.Config,
			Next:     next,
			Inputs:   s.Inputs,
			Outputs:  s.Outputs,
			Branches: s.Branches,
			JoinMode: JoinNone,
			Policy:   policy,
		})
	}

	startID := ""
	if len(bodyNodes) > 0 {
		startID = bodyNodes[0].StepID
	}
	return &ExecutionPlan{
		SkillID: loopStep.ID + ":body",
		StartID: startID,
		Nodes:   bodyNodes,
	}
}

// classifyJoin determines whether a join node should be JoinWaitAll or
// JoinBranchMerge by checking whether all predecessors are direct arm-children
// of a single Branch step.
//
// JoinBranchMerge requires ALL of:
//  1. Every predecessor Pi has exactly one parent, and that parent is a Branch step.
//  2. All those Branch parents are the same step B.
//  3. B's full Next set equals exactly {P1..Pn} (B fans to this join's predecessors only).
//
// If any condition fails → JoinWaitAll.
func classifyJoin(
	joinID string,
	predecessors []string,
	stepTypes map[string]StepType,
	preds map[string][]string,
	nextSet map[string]map[string]bool,
) JoinMode {
	var commonBranch string

	for _, predID := range predecessors {
		predParents := preds[predID]
		// Condition 1: predecessor must have exactly one parent.
		if len(predParents) != 1 {
			return JoinWaitAll
		}
		parentID := predParents[0]
		// Condition 1b: that parent must be a Branch step.
		if stepTypes[parentID] != StepBranch {
			return JoinWaitAll
		}
		// Condition 2: all predecessors share the same Branch parent.
		if commonBranch == "" {
			commonBranch = parentID
		} else if commonBranch != parentID {
			return JoinWaitAll
		}
	}

	if commonBranch == "" {
		return JoinWaitAll
	}

	// Condition 3: the Branch step's full Next set must equal the predecessors set.
	// This ensures we're not looking at a partial coverage (e.g. branch fans to 3
	// arms but only 2 lead to this join).
	predSet := make(map[string]bool, len(predecessors))
	for _, p := range predecessors {
		predSet[p] = true
	}
	branchNext := nextSet[commonBranch]
	if len(branchNext) != len(predSet) {
		return JoinWaitAll
	}
	for arm := range branchNext {
		if !predSet[arm] {
			return JoinWaitAll
		}
	}

	return JoinBranchMerge
}

// NodeByID returns the PlanNode with the given step ID, or nil.
func (p *ExecutionPlan) NodeByID(id string) *PlanNode {
	for _, n := range p.Nodes {
		if n.StepID == id {
			return n
		}
	}
	return nil
}

func safeSkillID(skill *SkillSpec) string {
	if skill == nil {
		return ""
	}
	return skill.ID
}
