// Package ws implements the WebSocket entry point for orchestration.
//
// Route: GET /ws/orchestrate/{app_slug}/{entry_point_slug}
//
// Wire protocol (matches Python platform):
//
//	Client -> Server: {"type":"message","content":"user text"}
//	Server -> Client: {"type":"token","content":"..."}
//	                  {"type":"tool_call","name":"...","input":{}}
//	                  {"type":"tool_result","name":"...","output":{}}
//	                  {"type":"done","run_id":"..."}
//	                  {"type":"error","message":"..."}
//
// Gate contract (internal/gate):
//
//	Gate.Check() → session.Register() → Gate.Confirm()
//	On Register failure: Gate.Rollback()
//	On session end: session.End() + Gate.Release()
package ws

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/session"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	CheckOrigin:      func(_ *http.Request) bool { return true },
}

// clientMsg is the message shape received from the WebSocket client.
type clientMsg struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// serverMsg is the message shape sent to the WebSocket client.
type serverMsg struct {
	Type    string          `json:"type"`
	Content string          `json:"content,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
	RunID   string          `json:"run_id,omitempty"`
	Message string          `json:"message,omitempty"`
}

// Authenticator validates bearer tokens and returns auth claims.
// Implemented by auth.Cache.
type Authenticator interface {
	Validate(ctx context.Context, token string) (*auth.TokenInfo, error)
}

// SessionStore manages WebSocket session lifecycle in Redis.
type SessionStore interface {
	Register(ctx context.Context, info session.SessionInfo) error
	End(ctx context.Context, sessionID, epSlug, appID string) error
}

// GateStore performs admission control for incoming sessions.
// Implemented by gate.Gate.
type GateStore interface {
	Check(ctx context.Context, cfg gate.Config) (gate.Result, error)
	Confirm(ctx context.Context, cfg gate.Config) error
	Rollback(ctx context.Context, cfg gate.Config) error
	Release(ctx context.Context, cfg gate.Config) error
}

// EPConfigLoader resolves Entry Point and Application runtime config.
// Implemented by epconfig.Loader.
type EPConfigLoader interface {
	Load(ctx context.Context, epSlug string) (*epconfig.EPConfig, error)
}

// Handler is the WebSocket orchestration handler.
type Handler struct {
	sessions      SessionStore
	gateStore     GateStore
	epLoader      EPConfigLoader
	recorder      *runrecorder.Recorder
	orch          *orchestrator.Orchestrator
	bus           event.Bus
	authenticator Authenticator
	instanceID    string
	logger        *slog.Logger
}

// NewHandler creates a Handler. gateStore may be nil (gate check is skipped),
// which is useful in tests that do not exercise admission control.
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

// WithGate attaches an admission gate to the handler. Must be called before
// the handler starts serving requests. When a gate is present every inbound
// WebSocket connection goes through Gate.Check → session.Register → Gate.Confirm.
func (h *Handler) WithGate(g GateStore) *Handler {
	h.gateStore = g
	return h
}

// WithEPConfig attaches an EP config loader that resolves entry-point and
// application runtime configuration (session limits, rate limits, access mode,
// block-lists) on every inbound connection. When present, a disabled or
// inaccessible EP is rejected before the WS upgrade.
func (h *Handler) WithEPConfig(l EPConfigLoader) *Handler {
	h.epLoader = l
	return h
}

// Routes returns an http.Handler that mounts the WS orchestration route.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/orchestrate/{app_slug}/{entry_point_slug}", h.ServeHTTP)
	return r
}

// ServeHTTP upgrades the connection and drives the orchestration session.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	appSlug := chi.URLParam(r, "app_slug")
	epSlug := chi.URLParam(r, "entry_point_slug")

	// ── 1. Authenticate ───────────────────────────────────────────────────────
	tokenInfo, rawToken, ok := h.authenticate(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// ── 2. Resolve EP + App runtime configuration ─────────────────────────────
	// Fail-closed: DB unavailable → 503, EP/App disabled → 403.
	var resolvedCfg *epconfig.EPConfig
	if h.epLoader != nil {
		var loadErr error
		resolvedCfg, loadErr = h.epLoader.Load(r.Context(), epSlug)
		if loadErr != nil {
			switch {
			case errors.Is(loadErr, epconfig.ErrNotFound):
				http.Error(w, `{"error":"entry point not found"}`, http.StatusNotFound)
			case errors.Is(loadErr, epconfig.ErrDBUnavailable):
				h.logger.Warn("ws: epconfig db unavailable", "ep_slug", epSlug, "error", loadErr)
				http.Error(w, `{"error":"service unavailable"}`, http.StatusServiceUnavailable)
			default:
				h.logger.Warn("ws: epconfig load failed", "ep_slug", epSlug, "error", loadErr)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			}
			return
		}
		// Enforce enabled + block-list checks. tokenHash is "" for public EPs.
		th := tokenHash(rawToken)
		if resolvedCfg.AccessMode == epconfig.AccessModePublic {
			th = ""
		}
		if accessErr := epconfig.CheckAccess(resolvedCfg, th, tokenInfo.TokenID); accessErr != nil {
			switch {
			case errors.Is(accessErr, epconfig.ErrDisabled):
				http.Error(w, `{"error":"entry point disabled"}`, http.StatusForbidden)
			case errors.Is(accessErr, epconfig.ErrBlocked):
				http.Error(w, `{"error":"access denied"}`, http.StatusForbidden)
			default:
				http.Error(w, `{"error":"access denied"}`, http.StatusForbidden)
			}
			return
		}
	}

	// ── 3. Gate.Check ─────────────────────────────────────────────────────────
	sessionID := newID()
	var gateCfg gate.Config
	var gateAdmitted bool

	if h.gateStore != nil {
		gateCfg = gate.Config{
			EPSlug:    epSlug,
			TokenHash: tokenHash(rawToken),
			SessionID: sessionID,
		}
		if resolvedCfg != nil {
			gateCfg.AppID = resolvedCfg.AppID
			gateCfg.EPMaxConcurrent = resolvedCfg.EPMaxConcurrent
			gateCfg.AppMaxConcurrent = resolvedCfg.AppMaxConcurrent
			gateCfg.RateLimitRPM = resolvedCfg.RateLimitRPM
			gateCfg.QueueTimeout = resolvedCfg.QueueTimeout
		}
		if _, err := h.gateStore.Check(r.Context(), gateCfg); err != nil {
			switch err {
			case gate.ErrCapExceeded:
				http.Error(w, `{"error":"session cap exceeded"}`, http.StatusServiceUnavailable)
			case gate.ErrRateLimited:
				http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
			case gate.ErrQueueFull:
				http.Error(w, `{"error":"queue full"}`, http.StatusServiceUnavailable)
			default:
				h.logger.Warn("ws: gate check failed", "error", err)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			}
			return
		}
		gateAdmitted = true
	}

	// ── 4. Upgrade to WebSocket ───────────────────────────────────────────────
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("ws: upgrade failed", "error", err)
		if gateAdmitted {
			_ = h.gateStore.Rollback(context.Background(), gateCfg)
		}
		return
	}
	defer conn.Close()

	// ── 5. Set up run / context IDs ───────────────────────────────────────────
	runID := newID()
	contextID := newID()

	// ── 6. Register session in Redis ──────────────────────────────────────────
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
		h.logger.Warn("ws: register session failed", "session_id", sessionID, "error", err)
		if gateAdmitted {
			_ = h.gateStore.Rollback(context.Background(), gateCfg)
		}
		h.writeError(conn, "session registration failed")
		return
	}

	// ── 7. Gate.Confirm ───────────────────────────────────────────────────────
	if gateAdmitted {
		if err := h.gateStore.Confirm(r.Context(), gateCfg); err != nil {
			h.logger.Warn("ws: gate confirm failed", "session_id", sessionID, "error", err)
			// Non-fatal: session hash is registered; shadow TTL (10s) provides safety net.
		}
	}

	defer func() {
		ctx := context.Background()
		_ = h.sessions.End(ctx, sessionID, epSlug, "")
		if gateAdmitted {
			_ = h.gateStore.Release(ctx, gateCfg)
		}
	}()

	// ── 8. CRITICAL: Subscribe to event bus BEFORE starting the workflow ──────
	// (from 09-domain-events.md §3 — the ready bootstrap handshake)
	evCh, unsub := h.bus.Subscribe(r.Context(), contextID, 256)
	defer unsub()

	// ── 9. Create run record in DB ────────────────────────────────────────────
	run := domain.Run{
		ID:             runID,
		ContextID:      contextID,
		EntryPointSlug: epSlug,
		Status:         domain.RunStatusRunning,
	}
	if err := h.recorder.CreateRun(r.Context(), run); err != nil {
		h.logger.Warn("ws: create run failed", "run_id", runID, "error", err)
	}

	// ── 10. Wait for first client message ────────────────────────────────────
	userMsg, err := h.readClientMessage(conn)
	if err != nil {
		h.writeError(conn, "failed to read message: "+err.Error())
		return
	}

	// ── 11. Start orchestration in a goroutine ───────────────────────────────
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	orchDone := make(chan struct{})
	go func() {
		defer close(orchDone)
		_, runErr := h.orch.Run(ctx, runID, contextID, userMsg, nil)
		if runErr != nil {
			h.logger.Warn("ws: orchestrator error", "run_id", runID, "error", runErr)
		}
	}()

	// ── 12. Stream events from bus → client; handle disconnect ───────────────
	h.streamEvents(ctx, cancel, conn, evCh, orchDone)
}

// authenticate extracts and validates the bearer token from the request.
// Returns (tokenInfo, rawToken, ok).
func (h *Handler) authenticate(r *http.Request) (*auth.TokenInfo, string, bool) {
	var rawToken string

	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		rawToken = strings.TrimPrefix(hdr, "Bearer ")
	} else if t := r.URL.Query().Get("token"); t != "" {
		rawToken = t
	}

	if rawToken == "" {
		return nil, "", false
	}

	info, err := h.authenticator.Validate(r.Context(), rawToken)
	if err != nil {
		return nil, "", false
	}
	return info, rawToken, true
}

// readClientMessage reads the first message from the WebSocket client.
func (h *Handler) readClientMessage(conn *websocket.Conn) (domain.Message, error) {
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		return domain.Message{}, fmt.Errorf("ws: read: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})

	var cm clientMsg
	if err := json.Unmarshal(msgBytes, &cm); err != nil {
		return domain.Message{}, fmt.Errorf("ws: decode: %w", err)
	}
	return domain.TextMessage(domain.RoleUser, cm.Content), nil
}

// streamEvents forwards bus events to the WebSocket client until orchestration
// completes or the client disconnects.
func (h *Handler) streamEvents(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, evCh <-chan event.Event, orchDone <-chan struct{}) {
	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			if err := h.writeEvent(conn, ev); err != nil {
				cancel()
				return
			}
			if ev.Type == "done" || ev.Type == "error" {
				return
			}
		case <-orchDone:
			// Drain any buffered events (e.g., the "done" event published just
			// before orchDone closed) before returning.
			for {
				select {
				case ev, ok := <-evCh:
					if !ok {
						return
					}
					_ = h.writeEvent(conn, ev)
					if ev.Type == "done" || ev.Type == "error" {
						return
					}
				default:
					return
				}
			}
		case <-clientGone:
			cancel()
			return
		case <-ctx.Done():
			return
		}
	}
}

// writeEvent marshals a bus event and sends it over the WebSocket.
// Payload is json.RawMessage; extract fields by unmarshalling into a map.
func (h *Handler) writeEvent(conn *websocket.Conn, ev event.Event) error {
	var payload map[string]json.RawMessage
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &payload)
	}

	var msg serverMsg
	switch ev.Type {
	case "token":
		var content string
		if raw, ok := payload["content"]; ok {
			_ = json.Unmarshal(raw, &content)
		}
		msg = serverMsg{Type: "token", Content: content}
	case "tool_call":
		var name string
		if raw, ok := payload["name"]; ok {
			_ = json.Unmarshal(raw, &name)
		}
		msg = serverMsg{Type: "tool_call", Name: name, Input: payload["input"]}
	case "tool_result":
		var name string
		if raw, ok := payload["name"]; ok {
			_ = json.Unmarshal(raw, &name)
		}
		msg = serverMsg{Type: "tool_result", Name: name, Output: payload["output"]}
	case "done":
		var runID string
		if raw, ok := payload["run_id"]; ok {
			_ = json.Unmarshal(raw, &runID)
		}
		msg = serverMsg{Type: "done", RunID: runID}
	case "error":
		var message string
		if raw, ok := payload["message"]; ok {
			_ = json.Unmarshal(raw, &message)
		}
		msg = serverMsg{Type: "error", Message: message}
	default:
		return nil
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("ws: marshal event: %w", err)
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func (h *Handler) writeError(conn *websocket.Conn, msg string) {
	data, _ := json.Marshal(serverMsg{Type: "error", Message: msg})
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

// tokenHash returns the lowercase hex SHA-256 of rawToken, matching the hash
// stored in them.access_tokens by the Python platform (same as auth.tokenHash).
func tokenHash(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return fmt.Sprintf("%x", h)
}
