package agentgen

// CompileExecutionPlan converts a SkillSpec into an ExecutionPlan.
//
// It performs a single pass over the already-topologically-ordered steps,
// building a predecessor-count map to detect join points (inDegree > 1).
// Any node reached by more than one predecessor gets JoinMode: wait_all and
// a populated JoinOf list.
//
// The SkillSpec.Steps slice must be topologically sorted (the compiler guarantees
// this). No cycle detection is done here — the compiler enforces acyclicity.
func CompileExecutionPlan(skill *SkillSpec) *ExecutionPlan {
	if skill == nil || len(skill.Steps) == 0 {
		return &ExecutionPlan{SkillID: safeSkillID(skill)}
	}

	// Build predecessor map: stepID → []predecessorID
	preds := make(map[string][]string, len(skill.Steps))
	for _, s := range skill.Steps {
		if _, ok := preds[s.ID]; !ok {
			preds[s.ID] = nil
		}
		for _, next := range s.Next {
			preds[next] = append(preds[next], s.ID)
		}
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
			node.JoinMode = JoinWaitAll
			node.JoinOf = predecessors
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
