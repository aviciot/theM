package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

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

	// For provider key lookup
	AppID    string
	TenantID string
}

// ConfigLoader resolves voice configuration for a voice entry point by slug.
// The implementation queries entry_points → app_orchestrators joined together.
type ConfigLoader interface {
	LoadVoiceConfig(ctx context.Context, tenantID, epSlug string) (*EPVoiceConfig, error)
}

// KeyResolver decrypts and returns the plaintext provider API key for an application.
type KeyResolver interface {
	GetPlaintextProviderKey(ctx context.Context, tenantID, appID, provider string) (string, error)
}

// Authenticator validates bearer tokens.
type Authenticator = transport.Authenticator

// Handler serves POST /apps/{slug}/voice/transcribe and POST /apps/{slug}/voice/tts.
type Handler struct {
	loader    ConfigLoader
	keys      KeyResolver
	auth      Authenticator
	tenantID  string // bootstrap tenant ID (single-tenant deployment)
	logger    *slog.Logger
}

// NewHandler constructs a voice Handler.
func NewHandler(
	loader ConfigLoader,
	keys KeyResolver,
	auth Authenticator,
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
		tenantID: bootstrapTenantID,
		logger:   logger,
	}
}

// Routes returns an http.Handler mounting the two voice endpoints.
// Mount at /apps so full paths are /apps/{slug}/voice/transcribe and /apps/{slug}/voice/tts.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/{slug}/voice/transcribe", h.Transcribe)
	r.Post("/{slug}/voice/tts", h.TTS)
	return r
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
		// Headers already sent — can't write a clean error response.
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

	// If there's a token, try to extract the tenant from it.
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

	// Auth enforcement
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

	// Resolve provider and check config
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
