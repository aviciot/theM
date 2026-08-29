package agentgen

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
// The SkillSpec.Steps slice must be topologically sorted. No cycle detection is
// done here — the compiler enforces acyclicity upstream.
func CompileExecutionPlan(skill *SkillSpec) *ExecutionPlan {
	if skill == nil || len(skill.Steps) == 0 {
		return &ExecutionPlan{SkillID: safeSkillID(skill)}
	}

	// Build step-type and predecessor maps.
	stepTypes := make(map[string]StepType, len(skill.Steps))
	for _, s := range skill.Steps {
		stepTypes[s.ID] = s.Type
	}

	// preds: targetID → []predecessorID
	preds := make(map[string][]string, len(skill.Steps))
	for _, s := range skill.Steps {
		if _, ok := preds[s.ID]; !ok {
			preds[s.ID] = nil
		}
		for _, next := range s.Next {
			preds[next] = append(preds[next], s.ID)
		}
	}

	// nextSet: stepID → set of direct successors (for branch-coverage check).
	nextSet := make(map[string]map[string]bool, len(skill.Steps))
	for _, s := range skill.Steps {
		set := make(map[string]bool, len(s.Next))
		for _, n := range s.Next {
			set[n] = true
		}
		nextSet[s.ID] = set
	}

	nodes := make([]*PlanNode, 0, len(skill.Steps))
	for _, s := range skill.Steps {
		node := &PlanNode{
			StepID:   s.ID,
			Type:     s.Type,
			Config:   s.Config,
			Next:     s.Next,
			Inputs:   s.Inputs,
			Outputs:  s.Outputs,
			Branches: s.Branches,
			JoinMode: JoinNone,
		}

		if predecessors := preds[s.ID]; len(predecessors) > 1 {
			node.JoinOf = predecessors
			node.JoinMode = classifyJoin(s.ID, predecessors, stepTypes, preds, nextSet)
		}

		nodes = append(nodes, node)
	}

	startID := ""
	if len(skill.Steps) > 0 {
		startID = skill.Steps[0].ID
	}

	return &ExecutionPlan{
		SkillID: skill.ID,
		StartID: startID,
		Nodes:   nodes,
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
