package service

import (
	"context"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/appflow"
)

// AppFlowLLMNodeStatus describes one inline LLM node on an app canvas and its
// current runtime override. Mirrors AgentLLMNodeStatus for the agent builder.
type AppFlowLLMNodeStatus struct {
	ApplicationID    string `json:"application_id"`
	NodeID           string `json:"node_id"`
	CompiledProvider string `json:"compiled_provider"`
	CompiledModel    string `json:"compiled_model"`
	OverrideProvider string `json:"override_provider,omitempty"`
	OverrideModel    string `json:"override_model,omitempty"`
}

// GetAppFlowLLMNodes returns the inline LLM nodes from the application's active
// definition merged with any stored runtime overrides.
func (s *AppService) GetAppFlowLLMNodes(ctx context.Context, applicationID string) ([]AppFlowLLMNodeStatus, error) {
	defJSON, err := s.dal.GetActiveDefinitionJSON(ctx, applicationID)
	if err != nil {
		return nil, ErrNotFound
	}

	agentByInstanceID, err := appflow.ResolveAgentByInstanceID(defJSON)
	if err != nil {
		return nil, err
	}
	spec, err := appflow.Compile(defJSON, agentByInstanceID)
	if err != nil {
		return nil, err
	}

	overrides, err := s.dal.ListAppFlowLLMOverrides(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	overrideByNode := make(map[string]dal.AppFlowLLMOverride, len(overrides))
	for _, o := range overrides {
		overrideByNode[o.NodeID] = o
	}

	result := make([]AppFlowLLMNodeStatus, 0, len(spec.LLMNodes))
	for _, n := range spec.LLMNodes {
		st := AppFlowLLMNodeStatus{
			ApplicationID:    applicationID,
			NodeID:           n.NodeID,
			CompiledProvider: n.CompiledProvider,
			CompiledModel:    n.CompiledModel,
		}
		if ov, ok := overrideByNode[n.NodeID]; ok {
			st.OverrideProvider = ov.Provider
			st.OverrideModel = ov.Model
		}
		result = append(result, st)
	}
	return result, nil
}

// PutAppFlowLLMOverride saves a provider+model override for one inline LLM node
// on an app canvas. provider and model must both be non-empty.
func (s *AppService) PutAppFlowLLMOverride(ctx context.Context, applicationID, nodeID, provider, model string) error {
	if provider == "" || model == "" {
		return validation("provider and model are required")
	}
	return s.dal.UpsertAppFlowLLMOverride(ctx, applicationID, nodeID, provider, model)
}
