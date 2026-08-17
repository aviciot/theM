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
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/metrics"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/tenantctx"
	"github.com/aviciot/them/internal/transport"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	CheckOrigin:      func(_ *http.Request) bool { return true },
}

// clientMsg is the message shape received from the WebSocket client.
// LastEventID, when present on the first message, is the stream resume cursor
// used by the Redis Streams transport (Phase 11c-B) to replay missed events.
type clientMsg struct {
	Type        string `json:"type"`
	Content     string `json:"content"`
	LastEventID string `json:"last_event_id,omitempty"`
}

// serverMsg is the message shape sent to the WebSocket client.
type serverMsg struct {
	Type      string          `json:"type"`
	Content   string          `json:"content,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	RunID     string          `json:"run_id,omitempty"`
	ContextID string          `json:"context_id,omitempty"`
	Message   string          `json:"message,omitempty"`
}

// Authenticator validates bearer tokens and returns auth claims.
// Implemented by auth.Cache. Sourced from internal/transport.
type Authenticator = transport.Authenticator

// SessionStore manages WebSocket session lifecycle in Redis.
// Sourced from internal/transport.
type SessionStore = transport.SessionStore

// GateStore performs admission control for incoming sessions.
// Sourced from internal/transport.
type GateStore = transport.GateStore

// EPConfigLoader resolves Entry Point and Application runtime config.
// Sourced from internal/transport.
type EPConfigLoader = transport.EPConfigLoader

// TemporalClientExecutor starts a Temporal workflow execution.
// Sourced from internal/transport.
type TemporalClientExecutor = transport.TemporalClientExecutor

// Handler is the WebSocket orchestration handler.
//
// After migration to execution.Lifecycle the handler retains only:
//   - WebSocket upgrade (gorilla/websocket)
//   - Frame parsing/writing (readClientMessage, streamEvents, writeEvent, writeError)
//   - WS-specific error mapping (writeError sends a WS text frame, not HTTP)
//   - Metrics (ActiveWSConnections, ActiveSessions, SessionsStarted)
//
// Auth, EPConfig, gate, session, CreateRun, and ExecuteWorkflow identity
// enforcement all live in execution.Lifecycle.Admit/Start/Release.
type Handler struct {
	lc            *execution.Lifecycle
	bus           event.Bus
	authenticator Authenticator
	instanceID    string
	logger        *slog.Logger
	runStreamer   runstream.RedisStreamer
}

// NewHandler creates a Handler. All admission/session/gate logic is delegated
// to lc. The handler retains only upgrade, frame I/O and metrics.
func NewHandler(
	lc *execution.Lifecycle,
	bus event.Bus,
	authenticator Authenticator,
	instanceID string,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		lc:            lc,
		bus:           bus,
		authenticator: authenticator,
		instanceID:    instanceID,
		logger:        logger,
	}
}

// WithRunStreamer attaches the Redis Streams reader used to deliver run events
// to the client. The Temporal client itself is held by the shared
// execution.Lifecycle. When not attached, runEvents fails fast at connect time.
func (h *Handler) WithRunStreamer(rc runstream.RedisStreamer) *Handler {
	h.runStreamer = rc
	return h
}

// runEvents opens the event channel for a run by reading the run's Redis Stream
// (them:dash:run:{runID}:stream). lastEventID is the client's resume cursor.
func (h *Handler) runEvents(ctx context.Context, runID, lastEventID string) (<-chan event.Event, error) {
	return runstream.StreamFromRedis(ctx, h.runStreamer, runID, runstream.StreamerOptions{LastEventID: lastEventID})
}

// Routes returns an http.Handler that mounts the WS orchestration route.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/orchestrate/{app_slug}/{entry_point_slug}", h.ServeHTTP)
	return r
}

// AppsWSRoute returns an http.Handler for /{slug}/ws (relative path).
// Mount at /apps so the full external path is /apps/{slug}/ws.
// It remaps the {slug} chi param to {entry_point_slug} so the shared
// ServeHTTP can call chi.URLParam(r, "entry_point_slug") uniformly.
func (h *Handler) AppsWSRoute() http.Handler {
	r := chi.NewRouter()
	r.Get("/{slug}/ws", func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("app_slug", slug)
		rctx.URLParams.Add("entry_point_slug", slug)
		h.ServeHTTP(w, r)
	})
	return r
}

// ServeHTTP runs the full admission pipeline via Lifecycle.Admit BEFORE upgrading
// to WebSocket. This means all pre-Admit errors (auth, EPConfig, gate, session)
// are returned as clean HTTP responses. After Admit succeeds the connection is
// upgraded; if the upgrade fails, Release cleans up gate/session state with a
// bounded 5-second timeout.
//
// After a successful upgrade:
//  1. bus.Subscribe (bootstrap ordering — before Start to avoid missed events)
//  2. readClientMessage (first user message, 30s deadline)
//  3. Lifecycle.Start → ExecuteWorkflow
//  4. streamEvents → forward run events to client until done or disconnect
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	epSlug := chi.URLParam(r, "entry_point_slug")

	metrics.ActiveWSConnections.Inc()
	defer metrics.ActiveWSConnections.Dec()

	// ── 1. Extract raw token (Lifecycle.Admit owns all validation/enforcement) ─
	rawToken := h.extractRawToken(r)

	// ── 2. Resolve tenant identity for EP config lookup ──────────────────────
	// Tenant comes from the bearer token (validated by auth.Cache) or JWT claims.
	// For public EPs (no token), use the bootstrap tenant UUID. This is correct
	// for the current single-tenant deployment. Multi-tenant public EPs require
	// Wave 10 hostname/path-prefix routing to supply the tenantID.
	// The tokenInfo validation happens inside Lifecycle.Admit; we only need the
	// TenantID here to scope the EP lookup. For token EPs we pre-validate now.
	tenantID := tenantctx.BootstrapTenantID
	if rawToken != "" && h.authenticator != nil {
		if ti, err := h.authenticator.Validate(r.Context(), rawToken); err == nil && ti.TenantID != "" {
			tenantID = ti.TenantID
		}
	}

	// ── 3. Lifecycle.Admit — full pipeline before upgrade ────────────────────
	// All pre-upgrade errors are clean HTTP responses (not WS close frames).
	// The voice EP check (→ 501) is inside Lifecycle, so no separate check needed.
	admitReq := execution.ExecutionRequest{
		EPSlug:     epSlug,
		TenantID:   tenantID,
		RawToken:   rawToken,
		InstanceID: h.instanceID,
	}
	handle, admitErr := h.lc.Admit(r.Context(), admitReq)
	if admitErr != nil {
		var ae *execution.AdmitError
		if errors.As(admitErr, &ae) {
			if ae.Kind == execution.AdmitErrNotImplemented {
				http.Error(w, `{"error":"voice entry points are not yet implemented"}`, ae.HTTPStatus)
			} else {
				http.Error(w, `{"error":"`+ae.Error()+`"}`, ae.HTTPStatus)
			}
		} else {
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		}
		return
	}

	epType := epTypeLabel(handle)
	metrics.GateAdmissions.WithLabelValues(epType).Inc()
	metrics.ActiveSessions.WithLabelValues(epType).Inc()
	metrics.SessionsStarted.WithLabelValues(epType, "admitted").Inc()

	// ── 3. Upgrade to WebSocket ───────────────────────────────────────────────
	// Admit has run the full pipeline (gate check + session.Register + CreateRun).
	// upgrader.Upgrade writes its own HTTP error on failure; we only need to
	// Release to clean up gate/session/run state.
	conn, upgradeErr := upgrader.Upgrade(w, r, nil)
	if upgradeErr != nil {
		h.logger.Warn("ws: upgrade failed", "ep_slug", epSlug, "error", upgradeErr)
		// Release derives its own bounded 5s context; gorilla has already written
		// its own HTTP error, so we only need cleanup here.
		h.lc.Release(handle)
		metrics.ActiveSessions.WithLabelValues(epType).Dec()
		metrics.SessionsEnded.WithLabelValues(epType, "upgrade_failed").Inc()
		return
	}
	defer conn.Close()

	h.logger.Info("ws: session started",
		"ep_slug", epSlug,
		"app_id", handle.EPConfig.AppID,
		"tenant_id", handle.EPConfig.TenantID,
		"session_id", handle.SessionID,
		"run_id", handle.RunID,
	)

	defer func() {
		metrics.ActiveSessions.WithLabelValues(epType).Dec()
		metrics.SessionsEnded.WithLabelValues(epType, "client_disconnect").Inc()

		h.logger.Info("ws: session ended",
			"ep_slug", epSlug,
			"app_id", handle.EPConfig.AppID,
			"tenant_id", handle.EPConfig.TenantID,
			"session_id", handle.SessionID,
		)
		h.lc.Release(handle)
	}()

	// ── 5. Wait for first client message ─────────────────────────────────────
	userMsg, lastEventID, msgErr := h.readClientMessage(conn)
	if msgErr != nil {
		h.writeError(conn, "failed to read message")
		return
	}

	// ── 6. Subscribe to run-stream BEFORE Lifecycle.Start ─────────────────────
	// This ensures no event emitted by the workflow immediately after start is
	// missed (bootstrap ordering invariant — same pattern as SSE and A2A).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	rsEvCh, rsErr := h.runEvents(ctx, handle.RunID, lastEventID)
	if rsErr != nil {
		h.logger.Warn("ws: runstream subscribe failed", "run_id", handle.RunID, "error", rsErr)
		h.writeError(conn, "event stream unavailable")
		return
	}

	// ── 7. Resolve orchestrator name from EP binding (SEC-04) ────────────────
	// OrchestratorName is resolved from entry_points.app_orchestrator_id →
	// app_orchestrators.name — never from the URL slug.
	// An unbound EP (app_orchestrator_id IS NULL) is a configuration error:
	// return a clear error rather than silently mis-routing.
	orchName := handle.EPConfig.OrchestratorName
	if orchName == "" {
		h.logger.Warn("ws: entry point has no orchestrator bound",
			"ep_slug", epSlug,
			"app_id", handle.EPConfig.AppID,
		)
		h.writeError(conn, "entry point has no orchestrator configured")
		return
	}

	// ── 8. Lifecycle.Start → ExecuteWorkflow ─────────────────────────────────
	// Identity fields (RunID, ContextID, TenantID, ApplicationID, EPSlug) are
	// overwritten inside Start from the handle — never from client-supplied data.
	input := temporal.WorkflowInput{
		OrchestratorName:  orchName,
		AppOrchestratorID: handle.EPConfig.AppOrchestratorID,
		UserMessage:       userMsg,
	}
	wfRun, startErr := h.lc.Start(ctx, handle, input)
	if startErr != nil {
		h.logger.Warn("ws: start lifecycle failed", "run_id", handle.RunID)
		h.writeError(conn, "failed to start workflow")
		return
	}
	h.logger.Info("ws: temporal workflow started",
		"ep_slug", epSlug,
		"session_id", handle.SessionID,
		"run_id", handle.RunID,
		"workflow_id", wfRun.GetID(),
	)

	// Send ready — client needs run_id + context_id to open the dashboard WS
	// and display the thinking bubble before any stream events arrive.
	if ready, err := json.Marshal(serverMsg{Type: "ready", RunID: handle.RunID, ContextID: handle.ContextID}); err == nil {
		_ = conn.WriteMessage(websocket.TextMessage, ready)
	}

	// ── 9. Ping/pong keepalive ───────────────────────────────────────────────
	// Send WS pings every 15 s to prevent Traefik / proxies from dropping the
	// idle TCP connection before the Temporal worker starts producing events.
	const pingInterval = 15 * time.Second
	const pongDeadline = 5 * time.Second
	// Initial read deadline: first ping arrives within pingInterval; pong handler
	// resets the deadline on each round-trip.
	_ = conn.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))
	})
	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = conn.SetWriteDeadline(time.Now().Add(pongDeadline))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
				_ = conn.SetWriteDeadline(time.Time{})
			}
		}
	}()

	// Monitor workflow completion on a detached context so a transport timeout
	// does not cancel the wfRun.Get call before the workflow actually finishes.
	orchDone := make(chan struct{})
	go func() {
		defer close(orchDone)
		// Use background context: we still want to know when the workflow finishes
		// even if the WS transport is momentarily slow. Cancel is handled by the
		// main ctx below when the client disconnects.
		if err := wfRun.Get(context.Background(), nil); err != nil {
			if ctx.Err() == nil {
				h.logger.Warn("ws: temporal workflow error", "run_id", handle.RunID, "error", err)
			}
		}
	}()

	// ── 10. Stream run events to client ──────────────────────────────────────
	h.streamEvents(ctx, cancel, conn, rsEvCh, nil, orchDone)
}

// extractRawToken extracts the bearer token string from the Authorization header
// or ?token query param. It does NOT validate the token — Lifecycle.Admit owns
// all token validation and enforcement per EP access policy.
func (h *Handler) extractRawToken(r *http.Request) string {
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		return strings.TrimPrefix(hdr, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// readClientMessage reads the first message from the WebSocket client. It
// returns the user message and the optional stream resume cursor
// (last_event_id) supplied by reconnecting clients — "" when absent.
func (h *Handler) readClientMessage(conn *websocket.Conn) (domain.Message, string, error) {
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		return domain.Message{}, "", fmt.Errorf("ws: read: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})

	var cm clientMsg
	if err := json.Unmarshal(msgBytes, &cm); err != nil {
		return domain.Message{}, "", fmt.Errorf("ws: decode: %w", err)
	}
	return domain.TextMessage(domain.RoleUser, cm.Content), cm.LastEventID, nil
}

// streamEvents forwards bus events to the WebSocket client until orchestration
// completes or the client disconnects.
// termCh is the dedicated terminal-event channel (capacity 1) from the in-process
// bus (R-0 L-1 fix). Pass nil when using the Redis run-stream path (termCh not needed
// there because the stream itself guarantees terminal event delivery).
func (h *Handler) streamEvents(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, evCh <-chan event.Event, termCh <-chan event.Event, orchDone <-chan struct{}) {
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
		case ev, ok := <-termCh:
			// Terminal event via dedicated channel (R-0 L-1 fix).
			// termCh may be nil when using the Redis run-stream path; a nil channel
			// blocks forever in a select (never selected), which is the correct behaviour.
			if !ok {
				return
			}
			_ = h.writeEvent(conn, ev)
			return
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
				case ev, ok := <-termCh:
					if !ok {
						return
					}
					_ = h.writeEvent(conn, ev)
					return
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
	case "replay_unavailable":
		// Emitted by StreamFromRedis when last_event_id was trimmed by MAXLEN.
		// Forward the raw payload so the client can display a notice (Phase 11c-B).
		var message string
		if raw, ok := payload["reason"]; ok {
			_ = json.Unmarshal(raw, &message)
		}
		msg = serverMsg{Type: "replay_unavailable", Message: message}
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

// epTypeLabel returns the Prometheus ep_type label from an ExecutionHandle.
// The EP type comes from the EPConfig resolved by Lifecycle.Admit.
func epTypeLabel(h *execution.ExecutionHandle) string {
	if h == nil || h.EPConfig == nil {
		return "unknown"
	}
	switch h.EPConfig.EPType {
	case "websocket", "sse", "voice", "a2a":
		return h.EPConfig.EPType
	default:
		return "unknown"
	}
}
