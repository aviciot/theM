package admin

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/appflow"
)

// NodeTypesHandler serves GET /admin/node-types.
// Returns the public node metadata for every registered canvas node type —
// both the agentgen (agent builder) family and the appflow (app canvas)
// family — merged into one array with one shape, sorted by type name for
// deterministic output. See docs/NODE_REGISTRY_PLAN.md Phase 3.
// It is stateless and can be instantiated with NodeTypesHandler{}.
type NodeTypesHandler struct{}

func (h NodeTypesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	agentInfos := agentgen.AllNodeTypeInfos()
	appInfos := appflow.AllAppCanvasNodeInfos()

	out := make([]json.RawMessage, 0, len(agentInfos)+len(appInfos))
	for _, info := range agentInfos {
		b, err := json.Marshal(info)
		if err != nil {
			continue
		}
		out = append(out, withFamily(b, "agentgen"))
	}
	for _, info := range appInfos {
		b, err := json.Marshal(info)
		if err != nil {
			continue
		}
		out = append(out, withFamily(b, "appflow"))
	}
	sort.Slice(out, func(i, j int) bool {
		return nodeTypeKey(out[i]) < nodeTypeKey(out[j])
	})

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("["))
	for i, b := range out {
		if i > 0 {
			_, _ = w.Write([]byte(","))
		}
		_, _ = w.Write(b)
	}
	_, _ = w.Write([]byte("]"))
}

// nodeTypeKey extracts the "type" field from a marshalled node info for sort
// comparison, without re-parsing the full struct.
func nodeTypeKey(raw json.RawMessage) string {
	var v struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Type
}

// withFamily stamps a "family" field ("agentgen" or "appflow") onto an
// already-marshalled node info. Neither agentgen.NodeTypeInfo nor
// appflow.AppCanvasNodeInfo carries this field — the two packages are
// deliberately decoupled (see appflow/noderegistry.go's header comment) — but
// "type" is not a unique key across the merged array: agentgen's StepLLM and
// appflow's app-canvas llm node are both legitimately typed "llm" (see
// TestNodeTypesHandler_IncludesAppCanvasKinds). Consumers that index the
// response by bare "type" (frontend/src/lib/nodeRegistry.ts) need "family" to
// disambiguate. Injected here, at the merge boundary, rather than on either
// struct, so both packages stay free of this handler-level concern.
func withFamily(raw json.RawMessage, family string) json.RawMessage {
	out := make(json.RawMessage, 0, len(raw)+len(family)+12)
	out = append(out, raw[:len(raw)-1]...) // drop trailing '}'
	out = append(out, []byte(`,"family":"`)...)
	out = append(out, []byte(family)...)
	out = append(out, []byte(`"}`)...)
	return out
}
