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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/metrics"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/temporal"
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
	Type    string          `json:"type"`
	Content string          `json:"content,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
	RunID   string          `json:"run_id,omitempty"`
	Message string          `json:"message,omitempty"`
}

// Authenticator validates bearer tokens and returns auth claims.
// Implemented by auth.Cache. Sourced from internal/transport.
type Authenticator = transport.Authenticator

// SessionStore manages WebSocket session lifecycle in Redis.
// Sourced from internal/transport.
type SessionStore = transport.SessionStore

// GateStore performs admission control for incoming sessions.
// Implemented by gate.Gate. Sourced from internal/transport.
type GateStore = transport.GateStore

// EPConfigLoader resolves Entry Point and Application runtime config.
// Implemented by epconfig.Loader. Sourced from internal/transport.
type EPConfigLoader = transport.EPConfigLoader

// TemporalClientExecutor starts a Temporal workflow execution.
// Using an interface (rather than the full client.Client) allows tests to inject
// a fake without depending on a live Temporal server. Sourced from internal/transport.
type TemporalClientExecutor = transport.TemporalClientExecutor

// Handler is the WebSocket orchestration handler.
type Handler struct {
	sessions       SessionStore
	gateStore      GateStore
	epLoader       EPConfigLoader
	recorder       *runrecorder.Recorder
	orch           *orchestrator.Orchestrator
	bus            event.Bus
	authenticator  Authenticator
	instanceID     string
	logger         *slog.Logger
	temporalClient TemporalClientExecutor
	runStreamSub   runstream.Subscriber
	dispatcher     *runstream.Dispatcher
	runEventsMode  config.RunEventsMode
	// temporalEnabled was removed in R-2B: Temporal is now the unconditional
	// execution path. When temporalClient is nil, the handler returns 503.
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

// WithTemporal attaches a Temporal client and run-stream subscriber. Temporal is
// now the unconditional execution path (R-2B). The `enabled` parameter is kept for
// call-site compatibility but is ignored — use of Temporal is determined solely by
// whether tc is non-nil. When tc is nil, the handler returns 503.
func (h *Handler) WithTemporal(tc TemporalClientExecutor, sub runstream.Subscriber, enabled bool) *Handler {
	h.temporalClient = tc
	h.runStreamSub = sub
	_ = enabled // ignored: Temporal is unconditional — see R-2B
	return h
}

// WithRunEvents attaches the run-event dispatcher and the active RUN_EVENTS_MODE
// (Phase 11c-B). The dispatcher chooses Pub/Sub or Redis Streams per run based on
// mode and the run's events_transport value. Call after WithTemporal in main.go.
// When not called, the handler falls back to the Pub/Sub subscriber directly.
func (h *Handler) WithRunEvents(d *runstream.Dispatcher, mode config.RunEventsMode) *Handler {
	h.dispatcher = d
	h.runEventsMode = mode
	return h
}

// eventsTransportForNewRun returns the events_transport value this handler
// stamps on a new run row: "streams" in dual/streams mode, else "pubsub".
func (h *Handler) eventsTransportForNewRun() string {
	if h.runEventsMode == config.RunEventsModeDual || h.runEventsMode == config.RunEventsModeStreams {
		return "streams"
	}
	return "pubsub"
}

// runEvents opens the event channel for a run, using the dispatcher when wired
// (Phase 11c-B) or the legacy Pub/Sub Stream otherwise.
func (h *Handler) runEvents(ctx context.Context, runID, eventsTransport, lastEventID string) (<-chan event.Event, error) {
	if h.dispatcher != nil {
		return h.dispatcher.Stream(ctx, runID, eventsTransport, lastEventID)
	}
	return runstream.Stream(ctx, h.runStreamSub, runID)
}

// Routes returns an http.Handler that mounts the WS orchestration route.
// Also registers /apps/{slug}/ws as an alias for the app entry-point path,
// mapping the {slug} param to {entry_point_slug} so ServeHTTP can use it.
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

// ServeHTTP upgrades the connection and drives the orchestration session.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	appSlug := chi.URLParam(r, "app_slug")
	epSlug := chi.URLParam(r, "entry_point_slug")

	// Track active WS connection for the lifetime of this request.
	metrics.ActiveWSConnections.Inc()
	defer metrics.ActiveWSConnections.Dec()

	// ── 1. Attempt token extraction (non-enforcing at this point) ────────────
	// Whether auth is required depends on the EP's access_policy. We resolve
	// the EP config first, then enforce auth if mode == "token".
	tokenInfo, rawToken, authed := h.tryAuthenticate(r)

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

		isPublic := resolvedCfg.AccessMode == epconfig.AccessModePublic

		// Enforce authentication for token-mode EPs.
		if !isPublic && !authed {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		// Enforce enabled + block-list checks. tokenHash is "" for public EPs.
		th := tokenHash(rawToken)
		if isPublic {
			th = ""
		}
		var userID int64
		if tokenInfo != nil {
			userID = tokenInfo.TokenID
		}
		if accessErr := epconfig.CheckAccess(resolvedCfg, th, userID); accessErr != nil {
			switch {
			case errors.Is(accessErr, epconfig.ErrDisabled):
				http.Error(w, `{"error":"entry point disabled"}`, http.StatusForbidden)
			default:
				http.Error(w, `{"error":"access denied"}`, http.StatusForbidden)
			}
			return
		}
	} else {
		// No EP config loader wired — fall back to mandatory token auth.
		if !authed {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
	}

	// Ensure tokenInfo is non-nil for the rest of the handler.
	if tokenInfo == nil {
		tokenInfo = &auth.TokenInfo{}
	}

	// ── 2b. Reject voice EPs — not yet implemented ───────────────────────────
	// Voice EPs require STT/TTS providers, audio framing, and interruption
	// handling that are not implemented in the WS text-orchestration path.
	// Return 501 before any session or gate state is allocated.
	if resolvedCfg != nil && resolvedCfg.EPType == "voice" {
		http.Error(w, `{"error":"voice entry points are not yet implemented"}`, http.StatusNotImplemented)
		return
	}

	// ── 3. Gate.Check ─────────────────────────────────────────────────────────
	sessionID := newID()
	var gateCfg gate.Config
	var gateAdmitted bool

	// Compute gate token hash: "" for anonymous sessions so that rlKey() in
	// gate.go returns "" and skips per-token rate limiting for public EPs.
	// sha256("") is NOT empty string, so we must not pass tokenHash(rawToken)
	// when rawToken is "".
	gateTokenHash := ""
	if rawToken != "" {
		gateTokenHash = tokenHash(rawToken)
	}

	if h.gateStore != nil {
		gateCfg = gate.Config{
			EPSlug:    epSlug,
			TokenHash: gateTokenHash,
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
			epType := epTypeLabel(resolvedCfg)
			switch err {
			case gate.ErrCapExceeded:
				metrics.GateRejections.WithLabelValues(epType, "cap_exceeded").Inc()
				metrics.SessionsStarted.WithLabelValues(epType, "rejected").Inc()
				http.Error(w, `{"error":"session cap exceeded"}`, http.StatusServiceUnavailable)
			case gate.ErrRateLimited:
				metrics.GateRejections.WithLabelValues(epType, "rate_limited").Inc()
				metrics.SessionsStarted.WithLabelValues(epType, "rejected").Inc()
				http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
			case gate.ErrQueueFull:
				metrics.GateRejections.WithLabelValues(epType, "queue_full").Inc()
				metrics.SessionsStarted.WithLabelValues(epType, "rejected").Inc()
				http.Error(w, `{"error":"queue full"}`, http.StatusServiceUnavailable)
			default:
				h.logger.Warn("ws: gate check failed", "ep_slug", epSlug, "error", err)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			}
			return
		}
		gateAdmitted = true
		metrics.GateAdmissions.WithLabelValues(epTypeLabel(resolvedCfg)).Inc()
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
	// R-0 T-2 / OD-2: populate AppID and TenantID from resolved EP config so the
	// five-element RuntimeIdentity is fully materialised in the Redis session Hash.
	if resolvedCfg != nil {
		sessInfo.AppID = resolvedCfg.AppID
		sessInfo.TenantID = resolvedCfg.TenantID
	}
	if err := h.sessions.Register(r.Context(), sessInfo); err != nil {
		h.logger.Warn("ws: register session failed",
			"ep_slug", epSlug,
			"app_id", sessInfo.AppID,
			"error", err)
		if gateAdmitted {
			_ = h.gateStore.Rollback(context.Background(), gateCfg)
		}
		h.writeError(conn, "session registration failed")
		return
	}

	// ── 7. Gate.Confirm ───────────────────────────────────────────────────────
	if gateAdmitted {
		if err := h.gateStore.Confirm(r.Context(), gateCfg); err != nil {
			h.logger.Warn("ws: gate confirm failed",
				"ep_slug", epSlug,
				"app_id", sessInfo.AppID,
				"error", err)
			// Non-fatal: session hash is registered; shadow TTL (10s) provides safety net.
		}
	}

	// Capture appID for use in defer (resolvedCfg may be nil in tests without EP loader).
	appID := ""
	if resolvedCfg != nil {
		appID = resolvedCfg.AppID
	}

	// Track active session count and record session lifecycle metrics.
	epType := epTypeLabel(resolvedCfg)
	metrics.ActiveSessions.WithLabelValues(epType).Inc()
	metrics.SessionsStarted.WithLabelValues(epType, "admitted").Inc()

	h.logger.Info("ws: session started",
		"ep_slug", epSlug,
		"app_id", appID,
		"tenant_id", sessInfo.TenantID,
		"session_id", sessionID,
	)

	defer func() {
		metrics.ActiveSessions.WithLabelValues(epType).Dec()
		metrics.SessionsEnded.WithLabelValues(epType, "client_disconnect").Inc()

		h.logger.Info("ws: session ended",
			"ep_slug", epSlug,
			"app_id", appID,
			"tenant_id", sessInfo.TenantID,
			"session_id", sessionID,
		)

		ctx := context.Background()
		_ = h.sessions.End(ctx, sessionID, epSlug, appID)
		if gateAdmitted {
			_ = h.gateStore.Release(ctx, gateCfg)
		}
	}()

	// ── 8. Subscribe to event bus (kept for future in-process events) ────────
	// The in-process bus subscription is retained so any internal components
	// (e.g. future direct-Go orchestration for testing) can still publish events.
	// After R-2B, the WS handler streams from the Redis run-stream (rsEvCh),
	// not from evCh/termCh. The subscription is a no-op for the Temporal path.
	_, _, unsub := h.bus.Subscribe(r.Context(), contextID, 256)
	defer unsub()

	// ── 9. Create run record in DB ────────────────────────────────────────────
	// events_transport is decided by RUN_EVENTS_MODE at run-creation time and is
	// stable for the run's lifetime (Phase 11c-B). The dispatcher reads it to
	// pick Pub/Sub or Streams.
	// TenantID and ApplicationID come from resolvedCfg (R-4d); never from the client.
	eventsTransport := h.eventsTransportForNewRun()
	run := domain.Run{
		ID:              runID,
		ContextID:       contextID,
		EntryPointSlug:  epSlug,
		Status:          domain.RunStatusRunning,
		EventsTransport: eventsTransport,
	}
	if resolvedCfg != nil {
		run.TenantID = resolvedCfg.TenantID
		run.ApplicationID = resolvedCfg.AppID
	}
	if err := h.recorder.CreateRun(r.Context(), run); err != nil {
		h.logger.Warn("ws: create run failed", "run_id", runID, "error", err)
	}

	// ── 10. Wait for first client message ────────────────────────────────────
	userMsg, lastEventID, err := h.readClientMessage(conn)
	if err != nil {
		h.writeError(conn, "failed to read message: "+err.Error())
		return
	}

	// ── 11. Start orchestration (Temporal — unconditional after R-2B) ────────
	// When temporalClient is nil (Temporal not configured), return 503 immediately.
	// There is NO inline fallback — Temporal is the only execution path.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if h.temporalClient == nil {
		h.logger.Warn("ws: temporal client unavailable — rejecting request", "run_id", runID)
		h.writeError(conn, "orchestration service unavailable")
		_ = h.recorder.UpdateStatus(r.Context(), runID, domain.RunStatusFailed)
		return
	}

	// Subscribe to the run-stream BEFORE starting the workflow so no event
	// is missed (critical bootstrap ordering — bus.Subscribe already done above).
	rsEvCh, rsErr := h.runEvents(ctx, runID, eventsTransport, lastEventID)
	if rsErr != nil {
		h.logger.Warn("ws: runstream subscribe failed", "run_id", runID, "error", rsErr)
		h.writeError(conn, "event stream unavailable")
		_ = h.recorder.UpdateStatus(r.Context(), runID, domain.RunStatusFailed)
		return
	}

	// R-2C: send WorkflowInput (typed) to GoTaskQueue so the Go Worker can
	// deserialize it correctly. PythonOrchestrationInput was for the Python
	// worker's "them-orchestration" queue; the Go Worker expects WorkflowInput.
	// R-4d: TenantID and ApplicationID propagated from resolvedCfg — never from
	// client request data.
	input := temporal.WorkflowInput{
		RunID:            runID,
		ContextID:        contextID,
		EntryPointSlug:   epSlug,
		OrchestratorName: appSlug,
		UserMessage:      userMsg,
	}
	if resolvedCfg != nil {
		input.TenantID = resolvedCfg.TenantID
		input.ApplicationID = resolvedCfg.AppID
	}

	wfOpts := temporalclient.StartWorkflowOptions{
		// Python OrchestrationWorkflow registers itself as "ctx-{contextID}".
		// Go must use the same scheme so HITL signals reach the correct workflow.
		// R-2C: Bridge sends workflows to GoTaskQueue ("them-orchestration-go");
		// the dedicated Go Worker polls that queue. Python worker continues to
		// poll TaskQueue ("them-orchestration") independently.
		ID:        "ctx-" + contextID,
		TaskQueue: temporal.GoTaskQueue,
	}

	wfRun, wfErr := h.temporalClient.ExecuteWorkflow(ctx, wfOpts, temporal.WorkflowType, input)
	if wfErr != nil {
		h.logger.Warn("ws: start temporal workflow failed", "run_id", runID, "error", wfErr)
		h.writeError(conn, "failed to start workflow")
		return
	}
	h.logger.Info("ws: temporal workflow started",
		"ep_slug", epSlug,
		"app_id", appID,
		"session_id", sessionID,
		"run_id", runID,
		"workflow_id", wfRun.GetID(),
	)

	orchDone := make(chan struct{})

	// Wait for workflow completion in background (drives orchDone).
	go func() {
		defer close(orchDone)
		if err := wfRun.Get(ctx, nil); err != nil {
			h.logger.Warn("ws: temporal workflow error", "run_id", runID, "error", err)
		}
	}()

	// ── 12. Stream Redis run events to client ─────────────────────────────────
	// Redis run-stream events arrive on rsEvCh (not the in-process bus),
	// so termCh is not used here — pass nil to signal no termCh available.
	h.streamEvents(ctx, cancel, conn, rsEvCh, nil, orchDone)
}

// textContent extracts the concatenated text from all "text" parts of a message.
// Bridges domain.Message to the string expected by PythonOrchestrationInput.UserMessage.
func textContent(msg domain.Message) string {
	return msg.Text()
}

// tryAuthenticate extracts and validates the bearer token from the request.
// Returns (tokenInfo, rawToken, ok). ok=false means no valid token was found;
// the caller decides whether to reject the request based on the EP's access mode.
func (h *Handler) tryAuthenticate(r *http.Request) (*auth.TokenInfo, string, bool) {
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

// tokenHash is a package-local alias for transport.TokenHash for backward
// compatibility with the ws package's internal call sites.
var tokenHash = transport.TokenHash

// epTypeLabel returns the Prometheus ep_type label for a resolved EPConfig.
// When cfg is nil (no EP loader wired — tests), returns "unknown".
// Values are low-cardinality: websocket, sse, voice, a2a, unknown.
func epTypeLabel(cfg *epconfig.EPConfig) string {
	if cfg == nil {
		return "unknown"
	}
	switch cfg.EPType {
	case "websocket", "sse", "voice", "a2a":
		return cfg.EPType
	default:
		return "unknown"
	}
}
