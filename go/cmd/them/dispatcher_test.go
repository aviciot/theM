package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubHandler records that it was called and responds with the given status code.
type stubHandler struct {
	called bool
	status int
}

func (s *stubHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.called = true
	w.WriteHeader(s.status)
}

func TestAppsDispatcher_WSPath(t *testing.T) {
	ws := &stubHandler{status: http.StatusSwitchingProtocols}
	sse := &stubHandler{status: http.StatusOK}
	d := appsDispatcher(ws, sse)

	req := httptest.NewRequest(http.MethodGet, "/myapp/ws", nil)
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, req)

	if !ws.called {
		t.Error("expected WS handler to be called for /myapp/ws")
	}
	if sse.called {
		t.Error("SSE handler must not be called for /myapp/ws")
	}
}

func TestAppsDispatcher_SSEPath_GET(t *testing.T) {
	ws := &stubHandler{status: http.StatusSwitchingProtocols}
	sse := &stubHandler{status: http.StatusOK}
	d := appsDispatcher(ws, sse)

	req := httptest.NewRequest(http.MethodGet, "/myapp/sse", nil)
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, req)

	if !sse.called {
		t.Error("expected SSE handler to be called for GET /myapp/sse")
	}
	if ws.called {
		t.Error("WS handler must not be called for GET /myapp/sse")
	}
}

func TestAppsDispatcher_SSEPath_POST(t *testing.T) {
	ws := &stubHandler{status: http.StatusSwitchingProtocols}
	sse := &stubHandler{status: http.StatusOK}
	d := appsDispatcher(ws, sse)

	req := httptest.NewRequest(http.MethodPost, "/myapp/sse", nil)
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, req)

	if !sse.called {
		t.Error("expected SSE handler to be called for POST /myapp/sse")
	}
	if ws.called {
		t.Error("WS handler must not be called for POST /myapp/sse")
	}
}

func TestAppsDispatcher_UnknownPath_Returns404(t *testing.T) {
	ws := &stubHandler{status: http.StatusSwitchingProtocols}
	sse := &stubHandler{status: http.StatusOK}
	d := appsDispatcher(ws, sse)

	for _, path := range []string{"/myapp/grpc", "/myapp/", "/myapp", "/"} {
		ws.called = false
		sse.called = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		d.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: expected 404, got %d", path, rec.Code)
		}
		if ws.called || sse.called {
			t.Errorf("path %q: neither handler should be called for unknown path", path)
		}
	}
}

// TestAppsDispatcher_UnsupportedMethod_WS verifies that an unsupported method on
// a /ws path is forwarded to the WS sub-handler (which returns 405 via chi).
func TestAppsDispatcher_UnsupportedMethod_WS(t *testing.T) {
	// The real AppsWSRoute only registers GET. Use a stub that returns 405
	// to simulate chi's method-not-allowed response.
	ws := &stubHandler{status: http.StatusMethodNotAllowed}
	sse := &stubHandler{status: http.StatusOK}
	d := appsDispatcher(ws, sse)

	req := httptest.NewRequest(http.MethodPost, "/myapp/ws", nil)
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, req)

	if !ws.called {
		t.Error("expected WS handler to be called for POST /myapp/ws (method enforcement is chi's job)")
	}
	if sse.called {
		t.Error("SSE handler must not be called for POST /myapp/ws")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 from WS stub, got %d", rec.Code)
	}
}
