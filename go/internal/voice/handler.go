package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/temporal"
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
	loader   ConfigLoader
	keys     KeyResolver
	auth     Authenticator
	lc       *execution.Lifecycle
	bus      event.Bus
	tenantID string // bootstrap tenant ID (single-tenant deployment)
	logger   *slog.Logger
}

// NewHandler constructs a voice Handler.
func NewHandler(
	loader ConfigLoader,
	keys KeyResolver,
	auth Authenticator,
	lc *execution.Lifecycle,
	bus event.Bus,
	bootstrapTenantID string,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		loader:   loader,
		keys:     keys,
		auth:     auth,
		lc:       lc,
		bus:      bus,
		tenantID: bootstrapTenantID,
		logger:   logger,
	}
}

// Routes returns an http.Handler mounting all three voice endpoints.
// Mount at /apps so full paths are /apps/{slug}/voice/*.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/{slug}/voice/chat", h.Chat)
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

	// ── 4. Lifecycle.Admit — same admission pipeline as WS/SSE ───────────────
	rawToken := extractRawToken(r)
	admitReq := execution.ExecutionRequest{
		EPSlug:   slug,
		TenantID: h.tenantID,
		RawToken: rawToken,
		UserMessage: domain.Message{
			Role:  "user",
			Parts: []domain.ContentPart{{Type: "text", Text: transcript}},
		},
	}
	handle, admitErr := h.lc.Admit(ctx, admitReq)
	if admitErr != nil {
		writeAdmitError(w, admitErr)
		return
	}
	defer h.lc.Release(handle)

	// ── 5. Subscribe to event bus BEFORE Start (ordering invariant) ───────────
	evCh, termCh, unsub := h.bus.Subscribe(ctx, handle.ContextID, 64)
	defer unsub()

	// ── 6. Lifecycle.Start → orchestrator ─────────────────────────────────────
	orchName := ""
	if handle.EPConfig != nil {
		orchName = handle.EPConfig.OrchestratorName
	}
	input := temporal.WorkflowInput{
		OrchestratorName:  orchName,
		AppOrchestratorID: handle.EPConfig.AppOrchestratorID,
		EntryPointID:      handle.EPConfig.EPID,
		UserMessage:       admitReq.UserMessage,
	}
	if _, err := h.lc.Start(ctx, handle, input); err != nil {
		h.logger.Warn("voice: lifecycle start failed", "ep_slug", slug, "run_id", handle.RunID, "error", err)
		http.Error(w, `{"error":"orchestrator failed to start"}`, http.StatusInternalServerError)
		return
	}

	// ── 7. Collect LLM reply from event bus ───────────────────────────────────
	replyText := collectReply(ctx, evCh, termCh)
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

// collectReply drains the event bus until a terminal event or context cancellation,
// concatenating all "token" event text payloads into the LLM reply.
func collectReply(ctx context.Context, evCh, termCh <-chan event.Event) string {
	var sb strings.Builder
	for {
		select {
		case <-ctx.Done():
			return sb.String()
		case ev, ok := <-termCh:
			if !ok {
				return sb.String()
			}
			_ = ev
			return sb.String()
		case ev, ok := <-evCh:
			if !ok {
				return sb.String()
			}
			if ev.Type == "token" {
				var tok struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(ev.Payload, &tok) == nil {
					sb.WriteString(tok.Text)
				}
			}
		}
	}
}

// writeAdmitError maps *execution.AdmitError to the appropriate HTTP status.
func writeAdmitError(w http.ResponseWriter, err error) {
	var ae *execution.AdmitError
	if !errors.As(err, &ae) {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	http.Error(w, `{"error":"`+ae.Error()+`"}`, ae.HTTPStatus)
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
