package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/agentgen"
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

	known := agentgen.KnownStepTypes()
	if len(infos) != len(known) {
		t.Errorf("expected %d node types, got %d", len(known), len(infos))
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
	if byType[agentgen.StepBranch].Executable {
		t.Error("branch node must report executable=false (stub)")
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
