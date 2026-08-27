package voice_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/voice"
)

// ──────────────────────────────────────────────────────────────────────────────
// Fakes
// ──────────────────────────────────────────────────────────────────────────────

type fakeLoader struct {
	cfg *voice.EPVoiceConfig
	err error
}

func (f *fakeLoader) LoadVoiceConfig(_ context.Context, _, _ string) (*voice.EPVoiceConfig, error) {
	return f.cfg, f.err
}

type fakeKeyResolver struct {
	key string
	err error
}

func (f *fakeKeyResolver) GetPlaintextProviderKey(_ context.Context, _, _, _ string) (string, error) {
	return f.key, f.err
}

type fakeAuth struct {
	info *auth.TokenInfo
	err  error
}

func (f *fakeAuth) Validate(_ context.Context, _ string) (*auth.TokenInfo, error) {
	return f.info, f.err
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func buildHandler(cfg *voice.EPVoiceConfig, key string, authn voice.Authenticator) http.Handler {
	loader := &fakeLoader{cfg: cfg}
	keys := &fakeKeyResolver{key: key}
	h := voice.NewHandler(loader, keys, authn, "00000000-0000-0000-0000-000000000001", nil)
	return h.Routes()
}

func buildMultipart(t *testing.T, audioData []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("audio", "test.webm")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(audioData)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

// ──────────────────────────────────────────────────────────────────────────────
// Transcribe endpoint tests
// ──────────────────────────────────────────────────────────────────────────────

// 1. EP not found → 404.
func TestTranscribe_EPNotFound(t *testing.T) {
	h := voice.NewHandler(&fakeLoader{err: io.EOF}, &fakeKeyResolver{}, nil, "tenant-1", nil)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	buf, ct := buildMultipart(t, []byte("audio"))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/my-voice-ep/voice/transcribe", buf)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// 2. Token EP without Bearer → 401.
func TestTranscribe_TokenEPNoAuth(t *testing.T) {
	cfg := &voice.EPVoiceConfig{
		AccessMode:  "token",
		EPEnabled:   true,
		AppEnabled:  true,
		STTProvider: "openai",
		AppID:       "app-1",
	}
	h := voice.NewHandler(&fakeLoader{cfg: cfg}, &fakeKeyResolver{key: "sk-test"}, nil, "tenant-1", nil)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	buf, ct := buildMultipart(t, []byte("audio"))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/my-ep/voice/transcribe", buf)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// 3. EP disabled → 403.
func TestTranscribe_EPDisabled(t *testing.T) {
	cfg := &voice.EPVoiceConfig{
		AccessMode:  "public",
		EPEnabled:   false, // disabled
		AppEnabled:  true,
		STTProvider: "openai",
		AppID:       "app-1",
	}
	h := voice.NewHandler(&fakeLoader{cfg: cfg}, &fakeKeyResolver{key: "sk-test"}, nil, "tenant-1", nil)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	buf, ct := buildMultipart(t, []byte("audio"))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/my-ep/voice/transcribe", buf)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

// 4. No STT provider configured → 400.
func TestTranscribe_NoSTTProvider(t *testing.T) {
	cfg := &voice.EPVoiceConfig{
		AccessMode: "public",
		EPEnabled:  true,
		AppEnabled: true,
		AppID:      "app-1",
		// STTProvider is empty
	}
	h := voice.NewHandler(&fakeLoader{cfg: cfg}, &fakeKeyResolver{key: "sk-test"}, nil, "tenant-1", nil)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	buf, ct := buildMultipart(t, []byte("audio"))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/my-ep/voice/transcribe", buf)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// 5. No API key stored → 400.
func TestTranscribe_NoAPIKey(t *testing.T) {
	cfg := &voice.EPVoiceConfig{
		AccessMode:  "public",
		EPEnabled:   true,
		AppEnabled:  true,
		STTProvider: "openai",
		AppID:       "app-1",
	}
	h := voice.NewHandler(&fakeLoader{cfg: cfg}, &fakeKeyResolver{key: ""}, nil, "tenant-1", nil)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	buf, ct := buildMultipart(t, []byte("audio"))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/my-ep/voice/transcribe", buf)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TTS endpoint tests
// ──────────────────────────────────────────────────────────────────────────────

// 6. TTS without text body → 400.
func TestTTS_MissingText(t *testing.T) {
	cfg := &voice.EPVoiceConfig{
		AccessMode:  "public",
		EPEnabled:   true,
		AppEnabled:  true,
		TTSProvider: "openai",
		TTSVoice:    "alloy",
		AppID:       "app-1",
	}
	h := voice.NewHandler(&fakeLoader{cfg: cfg}, &fakeKeyResolver{key: "sk-test"}, nil, "tenant-1", nil)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"text": ""})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/my-ep/voice/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing text, got %d", resp.StatusCode)
	}
}

// 7. TTS token EP without auth → 401.
func TestTTS_TokenEPNoAuth(t *testing.T) {
	cfg := &voice.EPVoiceConfig{
		AccessMode:  "token",
		EPEnabled:   true,
		AppEnabled:  true,
		TTSProvider: "openai",
		AppID:       "app-1",
	}
	h := voice.NewHandler(&fakeLoader{cfg: cfg}, &fakeKeyResolver{key: "sk-test"}, nil, "tenant-1", nil)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"text": "hello"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/my-ep/voice/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}
