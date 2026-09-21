package admin

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/nodedefs"
)

// NodeTypesHandler serves GET /admin/node-types.
// Returns the public node metadata for every registered canvas node type —
// the agentgen (agent builder) family, the appflow (app canvas) family, and
// the middleware (DB-defined guards, e.g. File Guard) family — merged into
// one array with one shape, sorted by type name for deterministic output.
// See docs/NODE_REGISTRY_PLAN.md Phases 3-5.
//
// Unlike the agentgen/appflow families (static Go registries), middleware
// defs live in them.middleware_defs and require a DB read — this is the
// first version of this handler with a dependency; use NewNodeTypesHandler.
type NodeTypesHandler struct {
	db DBQuerier
}

// NewNodeTypesHandler constructs a NodeTypesHandler. db may be nil in tests
// that only exercise the agentgen/appflow families — a nil db degrades to
// "no middleware entries" rather than panicking (fail-open, matches this
// package's general convention for optional dependencies).
func NewNodeTypesHandler(db DBQuerier) NodeTypesHandler {
	return NodeTypesHandler{db: db}
}

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
	if h.db != nil {
		if mwDefs, err := dal.ListMiddlewareDefs(r.Context(), h.db); err == nil {
			for _, d := range mwDefs {
				b, err := json.Marshal(middlewareNodeInfo(d))
				if err != nil {
					continue
				}
				out = append(out, withFamily(b, "middleware"))
			}
		}
		// DB error: fail open, same as the rest of this endpoint's error handling —
		// a middleware read failure must not take down agentgen/appflow entries.
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

// middlewareNodeInfo shapes one them.middleware_defs row into the same JSON
// shape agentgen/appflow entries use (appflow.AppCanvasNodeInfo — chosen over
// a new type since middleware nodes are, like appflow's, non-executable-by-a-
// Go-function metadata records, not interpreter-bound step definitions).
// Executable is always false: "middleware" is a pass-through no-op in the
// appflow workflow today (internal/appflow/workflow.go's "case middleware") —
// see docs/NODE_REGISTRY_PLAN.md Phase 5's explicit "does not change what
// runs" scope note. A row with no seeded edges/config_fields (nil JSON
// columns) still appears, just with zero-value EdgeRules and no config
// fields — matches this table's existing nullable-column, fail-open pattern.
func middlewareNodeInfo(d dal.MiddlewareDefSummary) appflow.AppCanvasNodeInfo {
	meta := nodedefs.Meta{
		Label:       d.DisplayName,
		Description: d.Description,
		Emoji:       d.Emoji,
		Color:       d.Color,
		BgColor:     d.BgColor,
	}
	if len(d.Edges) > 0 {
		_ = json.Unmarshal(d.Edges, &meta.Edges)
	}
	if len(d.InputPorts) > 0 {
		_ = json.Unmarshal(d.InputPorts, &meta.InputPorts)
	}
	if len(d.OutputPorts) > 0 {
		_ = json.Unmarshal(d.OutputPorts, &meta.OutputPorts)
	}
	if len(d.ConfigFields) > 0 {
		_ = json.Unmarshal(d.ConfigFields, &meta.ConfigFields)
	}
	return appflow.AppCanvasNodeInfo{
		Meta:        meta,
		Type:        d.Slug,
		Version:     1,
		OutputArity: "single",
		Executable:  false,
	}
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
