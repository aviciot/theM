package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appFlowLLMTestDefJSON is a minimal schema_version 2 definition with one
// inline LLM node, used to exercise GetAppFlowLLMNodes end-to-end through the
// handler → service → appflow.Compile path.
const appFlowLLMTestDefJSON = `{
	"schema_version": 2,
	"components": [
		{
			"instance_id": "inline_llm_1",
			"definition_ref": {"kind":"inline","namespace":"builtin","name":"llm","version":1},
			"config": {"node_type":"llm","provider":"anthropic","model":"claude-haiku-4-5-20251001"}
		}
	],
	"entry_points": [
		{"instance_id":"ep1","slug":"main","protocol":"websocket","root":"inline_llm_1"}
	],
	"connections": []
}`

// AFN-H1: GET /applications/{id}/flow-llm-nodes returns the compiled inline LLM
// node with no override when none is stored.
func TestGetAppFlowLLMNodes_Handler_200_NoOverride(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(appFlowLLMTestDefJSON)}
	w := serveAppsQuerier(t, db, nil, http.MethodGet, "/applications/app-uuid/flow-llm-nodes", nil)
	require.Equal(t, http.StatusOK, w.Code)

	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "inline_llm_1", out[0]["node_id"])
	assert.Equal(t, "anthropic", out[0]["compiled_provider"])
	assert.Equal(t, "claude-haiku-4-5-20251001", out[0]["compiled_model"])
	assert.Nil(t, out[0]["override_provider"])
}

// AFN-H2: PUT /applications/{id}/flow-llm-nodes/{node_id} with an empty provider
// returns 400 (service.ErrValidation).
func TestPutAppFlowLLMOverride_Handler_400_EmptyProvider(t *testing.T) {
	db := &fakeDB{}
	body, _ := json.Marshal(map[string]any{"provider": "", "model": "claude-haiku-4-5-20251001"})
	w := serveApps(t, db, nil, http.MethodPut, "/applications/app-uuid/flow-llm-nodes/inline_llm_1", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// AFN-H3: PUT /applications/{id}/flow-llm-nodes/{node_id} with valid provider+model
// returns 200 and echoes node_id + updated:true.
func TestPutAppFlowLLMOverride_Handler_200(t *testing.T) {
	db := &fakeDB{}
	body, _ := json.Marshal(map[string]any{"provider": "groq", "model": "llama-3.3-70b"})
	w := serveApps(t, db, nil, http.MethodPut, "/applications/app-uuid/flow-llm-nodes/inline_llm_1", body)
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "inline_llm_1", out["node_id"])
	assert.Equal(t, true, out["updated"])
}
