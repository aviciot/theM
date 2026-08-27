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
	d := appsDispatcher(ws, sse, nil)

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
	d := appsDispatcher(ws, sse, nil)

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
	d := appsDispatcher(ws, sse, nil)

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
	d := appsDispatcher(ws, sse, nil)

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

// TestAppsDispatcher_VoicePath routes /slug/voice/transcribe and /slug/voice/tts to voiceApps.
func TestAppsDispatcher_VoicePath(t *testing.T) {
	ws := &stubHandler{status: http.StatusSwitchingProtocols}
	sse := &stubHandler{status: http.StatusOK}
	voiceH := &stubHandler{status: http.StatusOK}
	d := appsDispatcher(ws, sse, voiceH)

	for _, path := range []string{"/myapp/voice/transcribe", "/myapp/voice/tts"} {
		ws.called, sse.called, voiceH.called = false, false, false
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		d.ServeHTTP(rec, req)
		if !voiceH.called {
			t.Errorf("path %q: expected voice handler to be called", path)
		}
		if ws.called || sse.called {
			t.Errorf("path %q: WS/SSE must not be called for voice path", path)
		}
	}
}

// TestAppsDispatcher_VoiceNilHandler means voice paths return 404 when voiceApps is nil.
func TestAppsDispatcher_VoiceNilHandler(t *testing.T) {
	ws := &stubHandler{status: http.StatusSwitchingProtocols}
	sse := &stubHandler{status: http.StatusOK}
	d := appsDispatcher(ws, sse, nil)

	req := httptest.NewRequest(http.MethodPost, "/myapp/voice/transcribe", nil)
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for voice path with nil handler, got %d", rec.Code)
	}
}

// TestAppsDispatcher_UnsupportedMethod_WS verifies that an unsupported method on
// a /ws path is forwarded to the WS sub-handler (which returns 405 via chi).
func TestAppsDispatcher_UnsupportedMethod_WS(t *testing.T) {
	// The real AppsWSRoute only registers GET. Use a stub that returns 405
	// to simulate chi's method-not-allowed response.
	ws := &stubHandler{status: http.StatusMethodNotAllowed}
	sse := &stubHandler{status: http.StatusOK}
	d := appsDispatcher(ws, sse, nil)

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
