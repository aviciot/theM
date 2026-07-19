// Package sse implements the Server-Sent Events entry point for orchestration.
//
// Route: GET /sse/orchestrate/{app_slug}/{entry_point_slug}
//
// The initial user message is passed as ?message=<text> (GET) or in the
// request body (POST). Bearer token authentication is read from the
// Authorization header or the ?token=<value> query parameter.
//
// Wire format:
//
//	data: {"type":"token","content":"..."}\n\n
//	data: {"type":"tool_call","name":"...","input":{}}\n\n
//	data: {"type":"done","run_id":"..."}\n\n
//	data: {"type":"error","message":"..."}\n\n
package sse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/session"
)

// newID generates a random 16-byte hex string suitable for session/run IDs.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Authenticator validates bearer tokens.
type Authenticator interface {
	Validate(ctx context.Context, token string) (*auth.TokenInfo, error)
}

// SessionStore manages session lifecycle.
type SessionStore interface {
	Register(ctx context.Context, info session.SessionInfo) error
	End(ctx context.Context, sessionID, epSlug, appID string) error
}

// Handler is the SSE orchestration handler.
type Handler struct {
	sessions      SessionStore
	recorder      *runrecorder.Recorder
	orch          *orchestrator.Orchestrator
	bus           event.Bus
	authenticator Authenticator
	instanceID    string
	logger        *slog.Logger
}

// NewHandler creates a Handler.
func NewHandler(
	sessions SessionStore,
	recorder *runrecorder.Recorder,
	orch *orchestrator.Orchestrator,
	bus event.Bus,
	authenticator Authenticator,
	instanceID string,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		sessions:      sessions,
		recorder:      recorder,
		orch:          orch,
		bus:           bus,
		authenticator: authenticator,
		instanceID:    instanceID,
		logger:        logger,
	}
}

// Routes returns an http.Handler that mounts the SSE orchestration routes.
// Accepts both GET (message as ?message=) and POST (message in JSON body).
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/orchestrate/{app_slug}/{entry_point_slug}", h.ServeHTTP)
	r.Post("/orchestrate/{app_slug}/{entry_point_slug}", h.ServeHTTP)
	return r
}

// ServeHTTP handles the SSE connection.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	appSlug := chi.URLParam(r, "app_slug")
	epSlug := chi.URLParam(r, "entry_point_slug")

	// ── 1. Authenticate ───────────────────────────────────────────────────────
	tokenInfo, ok := h.authenticate(r)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// ── 2. Extract user message ───────────────────────────────────────────────
	userText, err := h.extractMessage(r)
	if err != nil || userText == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"missing message"}`, http.StatusBadRequest)
		return
	}

	// ── 3. Set SSE headers ────────────────────────────────────────────────────
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, hasFlusher := w.(http.Flusher)

	// ── 4. Set up session / run IDs ───────────────────────────────────────────
	sessionID := newID()
	runID := newID()
	contextID := newID()

	// ── 5. Register session ───────────────────────────────────────────────────
	sessInfo := session.SessionInfo{
		SessionID:        sessionID,
		InstanceID:       h.instanceID,
		UserID:           tokenInfo.TokenID,
		OrchestratorName: appSlug,
		EPSlug:           epSlug,
		ContextID:        contextID,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := h.sessions.Register(r.Context(), sessInfo); err != nil {
		h.logger.Warn("sse: register session failed", "session_id", sessionID, "error", err)
	}
	defer func() {
		ctx := context.Background()
		_ = h.sessions.End(ctx, sessionID, epSlug, "")
	}()

	// ── 6. CRITICAL: Subscribe to event bus BEFORE starting the workflow ──────
	evCh, unsub := h.bus.Subscribe(r.Context(), contextID, 256)
	defer unsub()

	// ── 7. Create run record ──────────────────────────────────────────────────
	run := domain.Run{
		ID:             runID,
		ContextID:      contextID,
		EntryPointSlug: epSlug,
		Status:         domain.RunStatusRunning,
	}
	if err := h.recorder.CreateRun(r.Context(), run); err != nil {
		h.logger.Warn("sse: create run failed", "run_id", runID, "error", err)
	}

	// ── 8. Start orchestration in a goroutine ─────────────────────────────────
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	userMsg := domain.TextMessage(domain.RoleUser, userText)

	orchDone := make(chan struct{})
	go func() {
		defer close(orchDone)
		_, runErr := h.orch.Run(ctx, runID, contextID, userMsg, nil)
		if runErr != nil {
			h.logger.Warn("sse: orchestrator error", "run_id", runID, "error", runErr)
		}
	}()

	// ── 9. Stream events from bus as SSE ──────────────────────────────────────
	h.streamEvents(ctx, cancel, w, flusher, hasFlusher, evCh, orchDone)
}

// streamEvents forwards bus events to the SSE response until orchestration
// completes or the client disconnects.
func (h *Handler) streamEvents(
	ctx context.Context,
	cancel context.CancelFunc,
	w http.ResponseWriter,
	flusher http.Flusher,
	hasFlusher bool,
	evCh <-chan event.Event,
	orchDone <-chan struct{},
) {
	flush := func() {
		if hasFlusher {
			flusher.Flush()
		}
	}

	writeSSE := func(ev event.Event) bool {
		sseData, err := h.formatSSE(ev)
		if err != nil {
			return true // skip unknown event types
		}
		if _, writeErr := fmt.Fprint(w, sseData); writeErr != nil {
			cancel()
			return false
		}
		flush()
		return true
	}

	for {
		select {
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			if !writeSSE(ev) {
				return
			}
			if ev.Type == "done" || ev.Type == "error" {
				return
			}
		case <-orchDone:
			// Drain any buffered events before returning.
			for {
				select {
				case ev, ok := <-evCh:
					if !ok {
						return
					}
					_ = writeSSE(ev)
					if ev.Type == "done" || ev.Type == "error" {
						return
					}
				default:
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// formatSSE converts a bus event to an SSE "data: ...\n\n" line.
func (h *Handler) formatSSE(ev event.Event) (string, error) {
	var payload map[string]json.RawMessage
	if len(ev.Payload) > 0 {
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			return "", fmt.Errorf("sse: unmarshal payload: %w", err)
		}
	}

	var msg map[string]any
	switch ev.Type {
	case "token":
		var content string
		if raw, ok := payload["content"]; ok {
			_ = json.Unmarshal(raw, &content)
		}
		msg = map[string]any{"type": "token", "content": content}
	case "tool_call":
		var name string
		if raw, ok := payload["name"]; ok {
			_ = json.Unmarshal(raw, &name)
		}
		var input any
		if raw, ok := payload["input"]; ok {
			_ = json.Unmarshal(raw, &input)
		}
		msg = map[string]any{"type": "tool_call", "name": name, "input": input}
	case "done":
		var runID string
		if raw, ok := payload["run_id"]; ok {
			_ = json.Unmarshal(raw, &runID)
		}
		msg = map[string]any{"type": "done", "run_id": runID}
	case "error":
		var message string
		if raw, ok := payload["message"]; ok {
			_ = json.Unmarshal(raw, &message)
		}
		msg = map[string]any{"type": "error", "message": message}
	default:
		return "", fmt.Errorf("sse: unknown event type %q", ev.Type)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("sse: marshal event: %w", err)
	}
	return "data: " + string(data) + "\n\n", nil
}

// authenticate extracts and validates the bearer token.
// Checks Authorization header first, then ?token= query param.
func (h *Handler) authenticate(r *http.Request) (*auth.TokenInfo, bool) {
	var rawToken string

	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		rawToken = strings.TrimPrefix(hdr, "Bearer ")
	} else if t := r.URL.Query().Get("token"); t != "" {
		rawToken = t
	}

	if rawToken == "" {
		return nil, false
	}

	info, err := h.authenticator.Validate(r.Context(), rawToken)
	if err != nil {
		return nil, false
	}
	return info, true
}

// extractMessage reads the user message from ?message= query param (GET) or
// from the request body (POST).
func (h *Handler) extractMessage(r *http.Request) (string, error) {
	if msg := r.URL.Query().Get("message"); msg != "" {
		return msg, nil
	}
	if r.Method == http.MethodPost && r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB limit
		if err != nil {
			return "", fmt.Errorf("sse: read body: %w", err)
		}
		var p struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body, &p); err == nil && p.Message != "" {
			return p.Message, nil
		}
		if len(body) > 0 {
			return string(body), nil
		}
	}
	return "", nil
}
