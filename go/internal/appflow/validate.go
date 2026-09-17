// Structural validation of a compiled AppFlowSpec.
//
// Validate is intentionally pure — no DB access, no I/O — so it is unit-testable
// without a database and safe to call from both the publish path and run start.
// That purity is why provider-key availability is NOT validated here: key
// resolution is a three-tier DB lookup (app provider_keys → tenant
// llm_providers → platform default) owned by the worker at activity time.
package appflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidationError is a structured compilation/validation error.
type ValidationError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	InstanceID string `json:"instance_id,omitempty"`
}

func (e ValidationError) Error() string {
	if e.InstanceID != "" {
		return fmt.Sprintf("[%s] %s (node: %s)", e.Code, e.Message, e.InstanceID)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Validate runs structural validation on an AppFlowSpec.
// Returns a list of errors (blocking) and warnings (advisory).
func Validate(spec *AppFlowSpec) []ValidationError {
	if spec == nil {
		return []ValidationError{{Code: "nil_spec", Message: "spec is nil"}}
	}

	var errs []ValidationError

	for _, ep := range spec.EntryPoints {
		if ep.StartID == "" && len(ep.Nodes) > 0 {
			errs = append(errs, ValidationError{
				Code:    "no_start_node",
				Message: fmt.Sprintf("entry point %q has nodes but no start_id", ep.Slug),
			})
		}

		// Index nodes.
		nodeByID := make(map[string]*AppFlowNode, len(ep.Nodes))
		for i := range ep.Nodes {
			nodeByID[ep.Nodes[i].ID] = &ep.Nodes[i]
		}

		// Count outgoing and incoming edges per node.
		outCount := make(map[string]int)
		inCount := make(map[string]int)
		for _, e := range ep.Edges {
			outCount[e.Source]++
			inCount[e.Target]++
		}

		for _, n := range ep.Nodes {
			if n.Kind == "router" && outCount[n.ID] == 0 {
				errs = append(errs, ValidationError{
					Code:       "router_no_edges",
					Message:    "router node has no outgoing edges",
					InstanceID: n.ID,
				})
			}
			if n.Kind == "fork" && outCount[n.ID] < 2 {
				errs = append(errs, ValidationError{
					Code:       "fork_insufficient_branches",
					Message:    fmt.Sprintf("fork node has %d outgoing edge(s); need ≥2", outCount[n.ID]),
					InstanceID: n.ID,
				})
			}
			if n.Kind == "join" && inCount[n.ID] < 2 {
				errs = append(errs, ValidationError{
					Code:       "join_insufficient_branches",
					Message:    fmt.Sprintf("join node has %d incoming edge(s); need ≥2", inCount[n.ID]),
					InstanceID: n.ID,
				})
			}
			if n.Kind == "agent" && n.AgentID == "" {
				errs = append(errs, ValidationError{
					Code:       "unresolved_agent",
					Message:    "agent node has no resolved agent_id",
					InstanceID: n.ID,
				})
			}
			if n.Kind == "condition" {
				var cfg InlineConditionConfig
				if len(n.Config) > 0 {
					_ = json.Unmarshal(n.Config, &cfg)
				}
				if strings.TrimSpace(cfg.Expression) == "" {
					errs = append(errs, ValidationError{
						Code:       "condition_no_expression",
						Message:    "condition node has no expression configured",
						InstanceID: n.ID,
					})
				}
				if outCount[n.ID] != 2 {
					errs = append(errs, ValidationError{
						Code:       "condition_edge_count",
						Message:    fmt.Sprintf("condition node has %d outgoing edge(s); need exactly 2 (true/false)", outCount[n.ID]),
						InstanceID: n.ID,
					})
				} else if !hasTrueFalseLabels(ep.Edges, n.ID) {
					errs = append(errs, ValidationError{
						Code:       "condition_missing_labels",
						Message:    "condition node outgoing edges must be labelled \"true\" and \"false\"",
						InstanceID: n.ID,
					})
				}
			}
			if n.Kind == "llm" {
				var cfg InlineLLMConfig
				if len(n.Config) > 0 {
					_ = json.Unmarshal(n.Config, &cfg)
				}
				if strings.TrimSpace(cfg.SystemPrompt) == "" && strings.TrimSpace(cfg.UserPrompt) == "" {
					errs = append(errs, ValidationError{
						Code:       "llm_no_prompt",
						Message:    "llm node needs a system_prompt or user_prompt",
						InstanceID: n.ID,
					})
				}
				if outCount[n.ID] == 0 && inCount[n.ID] == 0 {
					errs = append(errs, ValidationError{
						Code:       "llm_orphan",
						Message:    "llm node is not connected to the flow",
						InstanceID: n.ID,
					})
				}
			}
			if n.Kind == "inline" {
				errs = append(errs, ValidationError{
					Code:       "unknown_inline_node",
					Message:    "unrecognised inline node type — re-add it from the canvas palette",
					InstanceID: n.ID,
				})
			}
		}
	}

	return errs
}

// hasTrueFalseLabels reports whether nodeID's outgoing edges in edges carry
// both a "true" label and a "false" label, matched case-insensitively (mirrors
// findEdgeByLabel's use of strings.EqualFold in workflow.go).
func hasTrueFalseLabels(edges []AppFlowEdge, nodeID string) bool {
	hasTrue, hasFalse := false, false
	for _, e := range edges {
		if e.Source != nodeID {
			continue
		}
		if strings.EqualFold(e.Label, "true") {
			hasTrue = true
		}
		if strings.EqualFold(e.Label, "false") {
			hasFalse = true
		}
	}
	return hasTrue && hasFalse
}
