package admin

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/aviciot/them/internal/agentgen"
)

// NodeTypesHandler serves GET /admin/node-types.
// Returns the public NodeTypeInfo for every registered canvas node type,
// sorted by type name for deterministic output.
// It is stateless and can be instantiated with NodeTypesHandler{}.
type NodeTypesHandler struct{}

func (h NodeTypesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	infos := agentgen.AllNodeTypeInfos()
	sort.Slice(infos, func(i, j int) bool {
		return string(infos[i].Type) < string(infos[j].Type)
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(infos)
}
