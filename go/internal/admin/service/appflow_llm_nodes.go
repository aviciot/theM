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

// AppFlowHTTPNodeStatus describes one HTTP node on an app canvas and its
// stored credential status (never the plaintext value).
type AppFlowHTTPNodeStatus struct {
	ApplicationID string `json:"application_id"`
	NodeID        string `json:"node_id"`
	Method        string `json:"method,omitempty"`
	URLTemplate   string `json:"url_template,omitempty"`
	// Params lists the credential/param slots with set status (no values).
	Params []AppFlowHTTPParamStatus `json:"params"`
}

// AppFlowHTTPParamStatus describes one stored param key for an HTTP node.
type AppFlowHTTPParamStatus struct {
	ParamKey         string `json:"param_key"`
	IsSet            bool   `json:"is_set"`
	InjectMode       string `json:"inject_mode"`
	InjectHeaderName string `json:"inject_header_name,omitempty"`
}

// GetAppFlowHTTPNodes returns the HTTP nodes from the application's active
// definition merged with stored credential status.
func (s *AppService) GetAppFlowHTTPNodes(ctx context.Context, applicationID string) ([]AppFlowHTTPNodeStatus, error) {
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

	stored, err := s.dal.ListAppFlowHTTPParams(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	paramsByNode := make(map[string][]dal.AppFlowHTTPParam)
	for _, p := range stored {
		paramsByNode[p.NodeID] = append(paramsByNode[p.NodeID], p)
	}

	result := make([]AppFlowHTTPNodeStatus, 0, len(spec.HTTPNodes))
	for _, n := range spec.HTTPNodes {
		st := AppFlowHTTPNodeStatus{
			ApplicationID: applicationID,
			NodeID:        n.NodeID,
			Method:        n.Method,
			URLTemplate:   n.URLTemplate,
		}
		// Report status for the two standard params; extend if more are added.
		for _, key := range []string{"bearer_token", "api_key"} {
			ps := AppFlowHTTPParamStatus{ParamKey: key, InjectMode: "header"}
			for _, p := range paramsByNode[n.NodeID] {
				if p.ParamKey == key {
					ps.IsSet = p.ValueEncrypted != nil && *p.ValueEncrypted != ""
					ps.InjectMode = p.InjectMode
					if p.InjectHeaderName != nil {
						ps.InjectHeaderName = *p.InjectHeaderName
					}
					break
				}
			}
			st.Params = append(st.Params, ps)
		}
		result = append(result, st)
	}
	return result, nil
}

// PutAppFlowHTTPParam stores a credential/param for one HTTP node.
func (s *AppService) PutAppFlowHTTPParam(ctx context.Context, applicationID, nodeID, paramKey, value, injectMode, injectHeaderName string) error {
	if nodeID == "" || paramKey == "" {
		return validation("node_id and param_key are required")
	}
	if injectMode == "" {
		injectMode = "header"
	}
	return s.dal.UpsertAppFlowHTTPParam(ctx, applicationID, nodeID, paramKey, value, injectMode, injectHeaderName)
}
