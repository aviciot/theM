package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/tenantctx"
)

// inMemoryHITLRedis is a minimal TaskStoreRedis for canvas_tasks tests.
type inMemoryHITLRedis struct {
	data map[string][]byte
}

func newInMemoryHITLRedis() *inMemoryHITLRedis {
	return &inMemoryHITLRedis{data: make(map[string][]byte)}
}
func (m *inMemoryHITLRedis) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := m.data[key]
	return v, ok, nil
}
func (m *inMemoryHITLRedis) SetEX(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.data[key] = value
	return nil
}
func (m *inMemoryHITLRedis) Del(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

// stubCanvasSignalerCT records SignalCanvasStep calls.
type stubCanvasSignalerCT struct {
	called     bool
	workflowID string
	runID      string
	signalName string
	payload    agentgen.PipelineVars
	err        error
}

func (s *stubCanvasSignalerCT) SignalCanvasStep(_ context.Context, workflowID, runID, signalName string, payload agentgen.PipelineVars) error {
	s.called = true
	s.workflowID = workflowID
	s.runID = runID
	s.signalName = signalName
	s.payload = payload
	return s.err
}

// buildCanvasTasksRequest creates an HTTP request with the task_id chi param and tenant context.
func buildCanvasTasksRequest(method, taskID, body, tenantID string) *http.Request {
	req := httptest.NewRequest(method, "/admin/canvas-tasks/"+taskID+"/signal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("task_id", taskID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	return req.WithContext(ctx)
}

// ── CSIG-1: signal delivers to the correct workflow when state is waiting ─────

func TestCanvasTasksHandler_Signal_Success(t *testing.T) {
	redis := newInMemoryHITLRedis()
	store := agentgen.NewHITLStore(redis)

	taskID := "csig-task-1"
	_ = store.Store(context.Background(), taskID, "wf-csig", "run-csig", "tenant-1", "hw1")
	_ = store.UpdateWaitToken(context.Background(), taskID, "tok-abc", "hw1")

	sig := &stubCanvasSignalerCT{}
	h := NewCanvasTasksHandler(store, sig)

	req := buildCanvasTasksRequest(http.MethodPost, taskID, `{"wait_token":"tok-abc","payload":{"approval":"yes"}}`, "tenant-1")
	rr := httptest.NewRecorder()
	h.signal(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !sig.called {
		t.Error("SignalCanvasStep must be called")
	}
	if sig.workflowID != "wf-csig" {
		t.Errorf("workflowID: want wf-csig, got %q", sig.workflowID)
	}
	approval, _ := sig.payload["approval"].(string)
	if approval != "yes" {
		t.Errorf("payload approval: want yes, got %q", approval)
	}
}

// ── CSIG-2: 404 when task handle not found ────────────────────────────────────

func TestCanvasTasksHandler_Signal_NotFound(t *testing.T) {
	store := agentgen.NewHITLStore(newInMemoryHITLRedis())
	sig := &stubCanvasSignalerCT{}
	h := NewCanvasTasksHandler(store, sig)

	req := buildCanvasTasksRequest(http.MethodPost, "no-such-task", `{"payload":{}}`, "tenant-1")
	rr := httptest.NewRecorder()
	h.signal(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
	if sig.called {
		t.Error("SignalCanvasStep must NOT be called when handle is missing")
	}
}

// ── CSIG-3: 403 when tenant does not own the task ─────────────────────────────

func TestCanvasTasksHandler_Signal_CrossTenant(t *testing.T) {
	store := agentgen.NewHITLStore(newInMemoryHITLRedis())
	taskID := "csig-task-3"
	_ = store.Store(context.Background(), taskID, "wf", "run", "tenant-owner", "hw1")
	_ = store.UpdateWaitToken(context.Background(), taskID, "tok-x", "hw1")

	sig := &stubCanvasSignalerCT{}
	h := NewCanvasTasksHandler(store, sig)

	req := buildCanvasTasksRequest(http.MethodPost, taskID, `{"wait_token":"tok-x","payload":{}}`, "tenant-attacker")
	rr := httptest.NewRecorder()
	h.signal(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
	if sig.called {
		t.Error("SignalCanvasStep must NOT be called for cross-tenant request")
	}
}

// ── CSIG-4: 409 when wrong wait_token is presented ────────────────────────────

func TestCanvasTasksHandler_Signal_WrongToken(t *testing.T) {
	store := agentgen.NewHITLStore(newInMemoryHITLRedis())
	taskID := "csig-task-4"
	_ = store.Store(context.Background(), taskID, "wf", "run", "tenant-1", "hw1")
	_ = store.UpdateWaitToken(context.Background(), taskID, "real-token", "hw1")

	sig := &stubCanvasSignalerCT{}
	h := NewCanvasTasksHandler(store, sig)

	req := buildCanvasTasksRequest(http.MethodPost, taskID, `{"wait_token":"wrong-token","payload":{}}`, "tenant-1")
	rr := httptest.NewRecorder()
	h.signal(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", rr.Code)
	}
	if sig.called {
		t.Error("SignalCanvasStep must NOT be called with wrong token")
	}
}
