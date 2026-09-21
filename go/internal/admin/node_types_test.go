package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/appflow"
)

func TestNodeTypesHandler_ReturnsAllTypes(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/node-types", nil)
	admin.NodeTypesHandler{}.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var infos []agentgen.NodeTypeInfo
	if err := json.Unmarshal(w.Body.Bytes(), &infos); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// The endpoint merges the agentgen (agent builder) family with the appflow
	// (app canvas) family — see docs/NODE_REGISTRY_PLAN.md Phase 3.
	wantTotal := len(agentgen.KnownStepTypes()) + len(appflow.AllAppCanvasNodeInfos())
	if len(infos) != wantTotal {
		t.Errorf("expected %d node types (agentgen + appflow), got %d", wantTotal, len(infos))
	}

	for _, info := range infos {
		if info.Type == "" {
			t.Error("node type entry has empty Type")
		}
		if info.Label == "" {
			t.Errorf("node type %q has empty Label", info.Type)
		}
		if info.Version < 1 {
			t.Errorf("node type %q has Version < 1", info.Type)
		}
		if info.OutputArity != "single" && info.OutputArity != "multi" && info.OutputArity != "none" {
			t.Errorf("node type %q has invalid OutputArity %q", info.Type, info.OutputArity)
		}
	}
}

// TestNodeTypesHandler_IncludesAppCanvasKinds verifies the 6 appflow node
// kinds (llm, condition, router, hil, fork, join) are present in the merged
// /admin/node-types response, in the same JSON shape as agentgen entries.
func TestNodeTypesHandler_IncludesAppCanvasKinds(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/node-types", nil)
	admin.NodeTypesHandler{}.ServeHTTP(w, r)

	var infos []agentgen.NodeTypeInfo
	if err := json.Unmarshal(w.Body.Bytes(), &infos); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	byType := make(map[string]agentgen.NodeTypeInfo, len(infos))
	for _, info := range infos {
		byType[string(info.Type)] = info
	}

	wantKinds := []string{"router", "hil", "fork", "join", "condition"}
	for _, kind := range wantKinds {
		info, ok := byType[kind]
		if !ok {
			t.Errorf("expected app-canvas kind %q in merged response, not found", kind)
			continue
		}
		if info.Label == "" {
			t.Errorf("app-canvas kind %q has empty Label", kind)
		}
		if !info.Executable {
			t.Errorf("app-canvas kind %q should report executable=true", kind)
		}
	}

	// "llm" is shared by name with agentgen's own StepLLM — both must be present
	// (agentgen's agent-builder llm step, and appflow's app-canvas llm node),
	// so there must be at least 2 entries typed "llm" in the merged array.
	llmCount := 0
	for _, info := range infos {
		if string(info.Type) == "llm" {
			llmCount++
		}
	}
	if llmCount != 2 {
		t.Errorf("expected exactly 2 entries typed %q (agentgen + appflow), got %d", "llm", llmCount)
	}

	// condition's true/false control ports must survive the merge.
	cond := byType["condition"]
	if len(cond.ControlOutputPorts) != 2 {
		t.Errorf("condition: expected 2 control_output_ports, got %d", len(cond.ControlOutputPorts))
	}
}

// TestNodeTypesHandler_FamilyDisambiguatesDuplicateType verifies every merged
// entry carries a "family" tag ("agentgen" or "appflow"), and that the two
// entries typed "llm" have different families — the frontend's node-type
// cache is keyed by bare "type" (see nodeRegistry.ts), so without "family" a
// consumer cannot tell agentgen's StepLLM apart from appflow's app-canvas
// llm node when both are loaded.
func TestNodeTypesHandler_FamilyDisambiguatesDuplicateType(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/node-types", nil)
	admin.NodeTypesHandler{}.ServeHTTP(w, r)

	var raw []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	llmFamilies := make(map[string]bool)
	for _, entry := range raw {
		family, _ := entry["family"].(string)
		if family != "agentgen" && family != "appflow" {
			t.Errorf("node type %q: expected family agentgen|appflow, got %q", entry["type"], family)
		}
		if entry["type"] == "llm" {
			llmFamilies[family] = true
		}
	}
	if len(llmFamilies) != 2 {
		t.Errorf("expected the 2 \"llm\" entries to have 2 distinct families, got %v", llmFamilies)
	}
}

func TestNodeTypesHandler_ExecutableComputedNotStored(t *testing.T) {
	infos := agentgen.AllNodeTypeInfos()
	byType := make(map[agentgen.StepType]agentgen.NodeTypeInfo, len(infos))
	for _, info := range infos {
		byType[info.Type] = info
	}

	if !byType[agentgen.StepInput].Executable {
		t.Error("input node must report executable=true")
	}
	if !byType[agentgen.StepResponse].Executable {
		t.Error("response node must report executable=true")
	}
	if !byType[agentgen.StepBranch].Executable {
		t.Error("branch node must report executable=true")
	}
	if !byType[agentgen.StepLoop].Executable {
		t.Error("loop node must report executable=true (implemented in Phase 5-A)")
	}
}

func TestNodeTypesHandler_VersionDefaultsToOne(t *testing.T) {
	for _, info := range agentgen.AllNodeTypeInfos() {
		if info.Version != 1 {
			t.Errorf("node type %q: expected version=1, got %d", info.Type, info.Version)
		}
	}
}

func TestNodeTypesHandler_SortedOutput(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/node-types", nil)
	admin.NodeTypesHandler{}.ServeHTTP(w, r)

	var infos []agentgen.NodeTypeInfo
	_ = json.Unmarshal(w.Body.Bytes(), &infos)

	for i := 1; i < len(infos); i++ {
		if string(infos[i].Type) < string(infos[i-1].Type) {
			t.Errorf("output not sorted: %q before %q", infos[i-1].Type, infos[i].Type)
		}
	}
}
