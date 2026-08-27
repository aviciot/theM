package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/temporal/workerconfig"
	"github.com/aviciot/them/internal/transport"
)

// EPVoiceConfig is the resolved voice configuration for a voice entry point.
// Loaded from app_orchestrators joined via entry_points.app_orchestrator_id.
type EPVoiceConfig struct {
	// Access policy
	AccessMode string // "public" | "token"
	EPEnabled  bool
	AppEnabled bool

	// STT
	STTProvider string // "openai" | "groq"
	STTModel    string // e.g. "whisper-1"

	// TTS
	TTSProvider string // "openai" | "elevenlabs"
	TTSVoice    string // voice name (openai) or voice ID (elevenlabs)
	TTSModel    string // model for openai TTS (e.g. "tts-1")

	// Orchestrator (for voice/chat pipeline)
	OrchestratorID string // app_orchestrators.id — empty when no orch connected

	// For provider key lookup
	AppID    string
	TenantID string
}

// ConfigLoader resolves voice configuration for a voice entry point by slug.
type ConfigLoader interface {
	LoadVoiceConfig(ctx context.Context, tenantID, epSlug string) (*EPVoiceConfig, error)
}

// KeyResolver decrypts and returns the plaintext provider API key for an application.
type KeyResolver interface {
	GetPlaintextProviderKey(ctx context.Context, tenantID, appID, provider string) (string, error)
}

// Authenticator validates bearer tokens.
type Authenticator = transport.Authenticator

// Handler serves voice endpoints:
//   - POST /apps/{slug}/voice/chat       — full pipeline: audio → STT → LLM → TTS → audio
//   - POST /apps/{slug}/voice/transcribe — standalone STT (audio → text)
//   - POST /apps/{slug}/voice/tts        — standalone TTS (text → audio)
type Handler struct {
	loader    ConfigLoader
	keys      KeyResolver
	auth      Authenticator
	runLoader workerconfig.Loader
	recorder  *runrecorder.Recorder
	bus       event.Bus
	tenantID  string // bootstrap tenant ID (single-tenant deployment)
	logger    *slog.Logger
}

// NewHandler constructs a voice Handler.
func NewHandler(
	loader ConfigLoader,
	keys KeyResolver,
	auth Authenticator,
	runLoader workerconfig.Loader,
	recorder *runrecorder.Recorder,
	bus event.Bus,
	bootstrapTenantID string,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		loader:    loader,
		keys:      keys,
		auth:      auth,
		runLoader: runLoader,
		recorder:  recorder,
		bus:       bus,
		tenantID:  bootstrapTenantID,
		logger:    logger,
	}
}

// Routes returns an http.Handler mounting all three voice endpoints.
// Mount at /apps so full paths are /apps/{slug}/voice/*.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/{slug}/voice/chat", h.Chat)
	r.Post("/{slug}/voice/stream", h.Stream)
	r.Post("/{slug}/voice/transcribe", h.Transcribe)
	r.Post("/{slug}/voice/tts", h.TTS)
	return r
}

// Chat handles POST /apps/{slug}/voice/chat.
// Full pipeline: audio → STT → orchestrator (LLM) → TTS → audio stream.
// The voice EP must be connected to an orchestrator in the canvas.
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	const chatTimeout = 60 * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), chatTimeout)
	defer cancel()

	slug := chi.URLParam(r, "slug")

	// ── 1. Parse audio from multipart ────────────────────────────────────────
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}
	f, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, `{"error":"audio field required"}`, http.StatusBadRequest)
		return
	}
	defer f.Close()
	audioBytes, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, `{"error":"read error"}`, http.StatusInternalServerError)
		return
	}

	// ── 2. Load voice config + resolve STT key ────────────────────────────────
	cfg, sttKey, err := h.resolveAndAuth(r, slug, "stt")
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if cfg.OrchestratorID == "" {
		http.Error(w, `{"error":"voice entry point has no orchestrator connected — connect it in the canvas"}`, http.StatusBadRequest)
		return
	}

	// ── 3. STT — transcribe audio to text ────────────────────────────────────
	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/webm"
	}
	transcript, err := Transcribe(ctx, cfg.STTProvider, cfg.STTModel, sttKey, audioBytes, header.Filename, ct)
	if err != nil {
		h.logger.Warn("voice: transcribe failed", "ep_slug", slug, "error", err)
		http.Error(w, `{"error":"transcription failed"}`, http.StatusBadGateway)
		return
	}
	if transcript == "" {
		http.Error(w, `{"error":"empty transcript — no speech detected"}`, http.StatusBadRequest)
		return
	}

	// ── 4. Load orchestrator config + build Go orchestrator (no Temporal) ───────
	runCfg, cfgErr := h.runLoader.LoadRunConfig(ctx, cfg.OrchestratorID, cfg.AppID, "")
	if cfgErr != nil {
		h.logger.Warn("voice: load run config failed", "ep_slug", slug, "orch_id", cfg.OrchestratorID, "error", cfgErr)
		http.Error(w, `{"error":"orchestrator configuration unavailable"}`, http.StatusInternalServerError)
		return
	}

	provider, provErr := h.buildProvider(runCfg)
	if provErr != nil {
		h.logger.Warn("voice: build LLM provider failed", "ep_slug", slug, "provider", runCfg.LLMProvider, "error", provErr)
		http.Error(w, `{"error":"LLM provider not configured — set an API key in Runtime settings"}`, http.StatusBadRequest)
		return
	}

	// ── 5. Create run record ─────────────────────────────────────────────────
	runID := uuid.New().String()
	contextID := uuid.New().String()
	if h.recorder != nil {
		run := domain.Run{
			ID:             runID,
			TenantID:       h.tenantID,
			EntryPointSlug: slug,
			Goal:           transcript,
			Status:         domain.RunRunning,
			StartedAt:      time.Now().UTC(),
		}
		if err := h.recorder.CreateRun(ctx, run); err != nil {
			h.logger.Warn("voice: create run failed (non-fatal)", "ep_slug", slug, "error", err)
		}
	}

	// ── 6. Run Go orchestrator directly ──────────────────────────────────────
	userMsg := domain.Message{
		Role:  domain.RoleUser,
		Parts: []domain.ContentPart{{Type: "text", Text: transcript}},
	}
	orch := orchestrator.New(runCfg.OrchestratorConfig, provider, nil, h.recorder, h.bus, h.logger)
	replyText, orchErr := orch.Run(ctx, runID, contextID, userMsg, nil,
		orchestrator.RunContext{TenantID: h.tenantID, ApplicationID: cfg.AppID},
	)
	if orchErr != nil {
		h.logger.Warn("voice: orchestrator run failed", "ep_slug", slug, "run_id", runID, "error", orchErr)
		http.Error(w, `{"error":"orchestrator error"}`, http.StatusBadGateway)
		return
	}
	if replyText == "" {
		http.Error(w, `{"error":"empty reply from orchestrator"}`, http.StatusBadGateway)
		return
	}

	// ── 8. Resolve TTS key ────────────────────────────────────────────────────
	_, ttsKey, err := h.resolveAndAuth(r, slug, "tts")
	if err != nil {
		http.Error(w, `{"error":"TTS not configured — set TTS provider in Runtime settings"}`, http.StatusBadRequest)
		return
	}

	// ── 9. TTS — stream audio reply ───────────────────────────────────────────
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Transcript", transcript)
	w.Header().Set("X-Reply", replyText)
	if _, err := StreamTTS(ctx, w, cfg.TTSProvider, cfg.TTSVoice, cfg.TTSModel, ttsKey, replyText); err != nil {
		h.logger.Warn("voice: tts stream failed", "ep_slug", slug, "error", err)
	}
}

// Stream handles POST /apps/{slug}/voice/stream.
// Streaming pipeline: audio → STT → SSE stream (transcript + LLM tokens) → done.
// The caller is expected to separately call /voice/tts with the full reply text
// to play back audio, enabling the user to read the reply as it streams in.
//
// SSE event format:
//
//	data: {"type":"transcript","text":"..."}\n\n
//	data: {"type":"token","content":"..."}\n\n   (one per LLM token)
//	data: {"type":"done","text":"..."}\n\n        (full reply text)
//	data: {"type":"error","message":"..."}\n\n
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	const streamTimeout = 90 * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), streamTimeout)
	defer cancel()

	slug := chi.URLParam(r, "slug")

	// ── 1. Parse audio ────────────────────────────────────────────────────────
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}
	f, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, `{"error":"audio field required"}`, http.StatusBadRequest)
		return
	}
	defer f.Close()
	audioBytes, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, `{"error":"read error"}`, http.StatusInternalServerError)
		return
	}

	// ── 2. Load voice config + auth ───────────────────────────────────────────
	cfg, sttKey, err := h.resolveAndAuth(r, slug, "stt")
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if cfg.OrchestratorID == "" {
		http.Error(w, `{"error":"voice entry point has no orchestrator connected"}`, http.StatusBadRequest)
		return
	}

	// ── 3. STT ────────────────────────────────────────────────────────────────
	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/webm"
	}
	transcript, err := Transcribe(ctx, cfg.STTProvider, cfg.STTModel, sttKey, audioBytes, header.Filename, ct)
	if err != nil {
		h.logger.Warn("voice/stream: transcribe failed", "ep_slug", slug, "error", err)
		http.Error(w, `{"error":"transcription failed"}`, http.StatusBadGateway)
		return
	}
	if transcript == "" {
		http.Error(w, `{"error":"empty transcript — no speech detected"}`, http.StatusBadRequest)
		return
	}

	// ── 4. Load orchestrator config + build provider ──────────────────────────
	runCfg, cfgErr := h.runLoader.LoadRunConfig(ctx, cfg.OrchestratorID, cfg.AppID, "")
	if cfgErr != nil {
		h.logger.Warn("voice/stream: load run config failed", "ep_slug", slug, "error", cfgErr)
		http.Error(w, `{"error":"orchestrator configuration unavailable"}`, http.StatusInternalServerError)
		return
	}
	provider, provErr := h.buildProvider(runCfg)
	if provErr != nil {
		http.Error(w, `{"error":"LLM provider not configured — set an API key in Runtime settings"}`, http.StatusBadRequest)
		return
	}

	// ── 5. Open SSE stream (all pre-stream errors must happen before this) ────
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx/traefik buffering
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	sseWrite := func(eventType, jsonPayload string) {
		fmt.Fprintf(w, "data: %s\n\n", jsonPayload)
		flusher.Flush()
	}
	sseErr := func(msg string) {
		b, _ := json.Marshal(map[string]string{"type": "error", "message": msg})
		sseWrite("error", string(b))
	}

	// ── 6. Emit transcript immediately ────────────────────────────────────────
	if b, err := json.Marshal(map[string]string{"type": "transcript", "text": transcript}); err == nil {
		sseWrite("transcript", string(b))
	}

	// ── 7. Subscribe to bus BEFORE starting orchestrator ─────────────────────
	runID := uuid.New().String()
	contextID := uuid.New().String()
	evCh, termCh, unsub := h.bus.Subscribe(ctx, contextID, 256)
	defer unsub()

	// ── 8. Record run (non-fatal) ─────────────────────────────────────────────
	if h.recorder != nil {
		run := domain.Run{
			ID:             runID,
			TenantID:       h.tenantID,
			EntryPointSlug: slug,
			Goal:           transcript,
			Status:         domain.RunRunning,
			StartedAt:      time.Now().UTC(),
		}
		if err := h.recorder.CreateRun(ctx, run); err != nil {
			h.logger.Warn("voice/stream: create run failed (non-fatal)", "ep_slug", slug, "error", err)
		}
	}

	// ── 9. Run orchestrator in background goroutine ───────────────────────────
	userMsg := domain.Message{
		Role:  domain.RoleUser,
		Parts: []domain.ContentPart{{Type: "text", Text: transcript}},
	}
	go func() {
		orch := orchestrator.New(runCfg.OrchestratorConfig, provider, nil, h.recorder, h.bus, h.logger)
		_, _ = orch.Run(ctx, runID, contextID, userMsg, nil,
			orchestrator.RunContext{TenantID: h.tenantID, ApplicationID: cfg.AppID},
		)
	}()

	// ── 10. Forward bus events to SSE stream ──────────────────────────────────
	var fullReply strings.Builder
	for {
		select {
		case <-ctx.Done():
			sseErr("request timeout")
			return
		case ev, ok := <-termCh:
			if !ok {
				return
			}
			if ev.Type == "error" {
				var p map[string]string
				_ = json.Unmarshal(ev.Payload, &p)
				sseErr(p["message"])
				return
			}
			// "done" — emit final event with full reply text
			if b, err := json.Marshal(map[string]string{"type": "done", "text": fullReply.String()}); err == nil {
				sseWrite("done", string(b))
			}
			return
		case ev, ok := <-evCh:
			if !ok {
				continue
			}
			if ev.Type != "token" {
				continue
			}
			var p map[string]string
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				continue
			}
			tok := p["content"]
			if tok == "" {
				continue
			}
			fullReply.WriteString(tok)
			if b, err := json.Marshal(map[string]string{"type": "token", "content": tok}); err == nil {
				sseWrite("token", string(b))
			}
		}
	}
}

// buildProvider constructs the LLM provider for a run from its loaded config.
func (h *Handler) buildProvider(cfg workerconfig.RunConfig) (llm.Provider, error) {
	if cfg.LLMAPIKey == "" {
		return nil, fmt.Errorf("no API key configured for provider %q", cfg.LLMProvider)
	}
	switch cfg.LLMProvider {
	case "anthropic", "":
		return llm.NewAnthropicProvider(cfg.LLMAPIKey, cfg.OrchestratorConfig.Model, 0), nil
	default:
		return nil, fmt.Errorf("provider %q is not yet supported for voice", cfg.LLMProvider)
	}
}

// Transcribe handles POST /apps/{slug}/voice/transcribe.
// Accepts multipart/form-data with an "audio" file field.
// Returns JSON: {"text":"...","provider":"...","model":"..."}
func (h *Handler) Transcribe(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	cfg, apiKey, err := h.resolveAndAuth(r, slug, "stt")
	if err != nil {
		h.writeErr(w, err)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}
	f, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, `{"error":"audio field required"}`, http.StatusBadRequest)
		return
	}
	defer f.Close()

	audioBytes, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, `{"error":"read error"}`, http.StatusInternalServerError)
		return
	}

	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/webm"
	}

	text, err := Transcribe(r.Context(), cfg.STTProvider, cfg.STTModel, apiKey, audioBytes, header.Filename, ct)
	if err != nil {
		h.logger.Warn("voice: transcribe failed", "ep_slug", slug, "error", err)
		http.Error(w, `{"error":"transcription failed"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"text":     text,
		"provider": cfg.STTProvider,
		"model":    cfg.STTModel,
	})
}

// TTS handles POST /apps/{slug}/voice/tts.
// Accepts JSON: {"text":"..."}
// Streams audio/mpeg back to the client.
func (h *Handler) TTS(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	cfg, apiKey, err := h.resolveAndAuth(r, slug, "tts")
	if err != nil {
		h.writeErr(w, err)
		return
	}

	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		http.Error(w, `{"error":"text is required"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")

	mimeType, err := StreamTTS(r.Context(), w, cfg.TTSProvider, cfg.TTSVoice, cfg.TTSModel, apiKey, body.Text)
	if err != nil {
		h.logger.Warn("voice: tts failed", "ep_slug", slug, "error", err)
		return
	}
	_ = mimeType
}

// ──────────────────────────────────────────────────────────────────────────────
// shared resolution
// ──────────────────────────────────────────────────────────────────────────────

type voiceErr struct {
	status  int
	message string
}

func (e *voiceErr) Error() string { return e.message }

func (h *Handler) resolveAndAuth(r *http.Request, slug, mode string) (*EPVoiceConfig, string, error) {
	tenantID := h.tenantID
	rawToken := extractRawToken(r)

	if rawToken != "" && h.auth != nil {
		if ti, err := h.auth.Validate(r.Context(), rawToken); err == nil && ti.TenantID != "" {
			tenantID = ti.TenantID
		}
	}

	cfg, err := h.loader.LoadVoiceConfig(r.Context(), tenantID, slug)
	if err != nil {
		return nil, "", &voiceErr{http.StatusNotFound, "entry point not found"}
	}
	if !cfg.EPEnabled || !cfg.AppEnabled {
		return nil, "", &voiceErr{http.StatusForbidden, "entry point disabled"}
	}

	if cfg.AccessMode == "token" {
		if rawToken == "" {
			return nil, "", &voiceErr{http.StatusUnauthorized, "authorization required"}
		}
		if h.auth != nil {
			if _, err := h.auth.Validate(r.Context(), rawToken); err != nil {
				return nil, "", &voiceErr{http.StatusUnauthorized, "invalid token"}
			}
		}
	}

	var provider string
	switch mode {
	case "stt":
		if cfg.STTProvider == "" {
			return nil, "", &voiceErr{http.StatusBadRequest, "voice entry point has no STT provider configured"}
		}
		provider = cfg.STTProvider
	case "tts":
		if cfg.TTSProvider == "" {
			return nil, "", &voiceErr{http.StatusBadRequest, "voice entry point has no TTS provider configured"}
		}
		provider = cfg.TTSProvider
	}

	apiKey, err := h.keys.GetPlaintextProviderKey(r.Context(), tenantID, cfg.AppID, provider)
	if err != nil || apiKey == "" {
		return nil, "", &voiceErr{http.StatusBadRequest, fmt.Sprintf("no API key stored for provider %q — save one in Runtime settings", provider)}
	}

	return cfg, apiKey, nil
}

func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	if ve, ok := err.(*voiceErr); ok {
		http.Error(w, `{"error":"`+ve.message+`"}`, ve.status)
		return
	}
	http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
}

func extractRawToken(r *http.Request) string {
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		return strings.TrimPrefix(hdr, "Bearer ")
	}
	return r.URL.Query().Get("token")
}
