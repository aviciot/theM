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
//
// Gate contract (internal/gate):
//
//	Gate.Check() → session.Register() → Gate.Confirm()
//	On Register failure: Gate.Rollback()
//	On session end: session.End() + Gate.Release()
package sse

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
	"github.com/google/uuid"
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

// newID generates a UUID v4 string.
// UUID format is required because Go passes run_id, session_id, and context_id to
// the Python Temporal worker which converts them via uuid.UUID(); a plain hex string
// would raise ValueError there.
func newID() string {
	return uuid.New().String()
}

// tokenHash is a package-local alias for transport.TokenHash for backward
// compatibility with the sse package's internal call sites.
var tokenHash = transport.TokenHash

// Authenticator validates bearer tokens.
// Sourced from internal/transport.
type Authenticator = transport.Authenticator

// SessionStore manages session lifecycle.
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

// Handler is the SSE orchestration handler.
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
// SSE connection goes through Gate.Check → session.Register → Gate.Confirm.
func (h *Handler) WithGate(g GateStore) *Handler {
	h.gateStore = g
	return h
}

// WithEPConfig attaches an EP config loader that resolves entry-point and
// application runtime configuration (session limits, rate limits, access mode,
// block-lists) on every inbound connection. When present, a disabled or
// inaccessible EP is rejected before SSE headers are written.
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

// Routes returns an http.Handler that mounts the SSE orchestration routes.
// Accepts both GET (message as ?message=) and POST (message in JSON body).
// Also registers /apps/{slug}/sse as an alias for the app entry-point path.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/orchestrate/{app_slug}/{entry_point_slug}", h.ServeHTTP)
	r.Post("/orchestrate/{app_slug}/{entry_point_slug}", h.ServeHTTP)
	return r
}

// AppsSSERoute returns an http.Handler for /{slug}/sse (relative path).
// Mount at /apps so the full external path is /apps/{slug}/sse.
// It remaps the {slug} chi param to {entry_point_slug} so the shared
// ServeHTTP can call chi.URLParam(r, "entry_point_slug") uniformly.
func (h *Handler) AppsSSERoute() http.Handler {
	r := chi.NewRouter()
	r.Get("/{slug}/sse", func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("app_slug", slug)
		rctx.URLParams.Add("entry_point_slug", slug)
		h.ServeHTTP(w, r)
	})
	r.Post("/{slug}/sse", func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("app_slug", slug)
		rctx.URLParams.Add("entry_point_slug", slug)
		h.ServeHTTP(w, r)
	})
	return r
}

// ServeHTTP handles the SSE connection.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	appSlug := chi.URLParam(r, "app_slug")
	epSlug := chi.URLParam(r, "entry_point_slug")

	// Track active SSE connection for the lifetime of this request.
	metrics.ActiveSSEConnections.Inc()
	defer metrics.ActiveSSEConnections.Dec()

	// ── 1. Attempt token extraction (non-enforcing at this point) ────────────
	// Whether auth is required depends on the EP's access_policy. We resolve
	// the EP config first, then enforce auth if mode == "token".
	tokenInfo, rawToken, authed := h.tryAuthenticate(r)

	// ── 2. Extract user message ───────────────────────────────────────────────
	userText, err := h.extractMessage(r)
	if err != nil || userText == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"missing message"}`, http.StatusBadRequest)
		return
	}

	// ── 3. Resolve EP + App runtime configuration ─────────────────────────────
	// Fail-closed: DB unavailable → 503, EP/App disabled → 403.
	// Must run before SSE headers are written so we can still return HTTP errors.
	var resolvedCfg *epconfig.EPConfig
	if h.epLoader != nil {
		var loadErr error
		resolvedCfg, loadErr = h.epLoader.Load(r.Context(), epSlug)
		if loadErr != nil {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case errors.Is(loadErr, epconfig.ErrNotFound):
				http.Error(w, `{"error":"entry point not found"}`, http.StatusNotFound)
			case errors.Is(loadErr, epconfig.ErrDBUnavailable):
				h.logger.Warn("sse: epconfig db unavailable", "ep_slug", epSlug, "error", loadErr)
				http.Error(w, `{"error":"service unavailable"}`, http.StatusServiceUnavailable)
			default:
				h.logger.Warn("sse: epconfig load failed", "ep_slug", epSlug, "error", loadErr)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			}
			return
		}

		isPublic := resolvedCfg.AccessMode == epconfig.AccessModePublic

		// Enforce authentication for token-mode EPs.
		if !isPublic && !authed {
			w.Header().Set("Content-Type", "application/json")
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
			w.Header().Set("Content-Type", "application/json")
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
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
	}

	// Ensure tokenInfo is non-nil for the rest of the handler.
	if tokenInfo == nil {
		tokenInfo = &auth.TokenInfo{}
	}

	// ── 3b. Reject voice EPs — not yet implemented ───────────────────────────
	// Voice EPs require STT/TTS providers, audio framing, and interruption
	// handling that are not implemented in the SSE text-orchestration path.
	// Return 501 before any session or gate state is allocated.
	if resolvedCfg != nil && resolvedCfg.EPType == "voice" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"voice entry points are not yet implemented"}`, http.StatusNotImplemented)
		return
	}

	// ── 4. Gate.Check ─────────────────────────────────────────────────────────
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
			w.Header().Set("Content-Type", "application/json")
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
				h.logger.Warn("sse: gate check failed", "ep_slug", epSlug, "error", err)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			}
			return
		}
		gateAdmitted = true
		metrics.GateAdmissions.WithLabelValues(epTypeLabel(resolvedCfg)).Inc()
	}

	// ── 5. Set SSE headers ────────────────────────────────────────────────────
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, hasFlusher := w.(http.Flusher)

	// ── 6. Set up run / context IDs ───────────────────────────────────────────
	runID := newID()
	contextID := newID()

	// ── 7. Register session ───────────────────────────────────────────────────
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
		h.logger.Warn("sse: register session failed",
			"ep_slug", epSlug,
			"app_id", sessInfo.AppID,
			"error", err)
		if gateAdmitted {
			_ = h.gateStore.Rollback(context.Background(), gateCfg)
		}
		// SSE headers already sent; write an error event.
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"session registration failed\"}\n\n")
		if hasFlusher {
			flusher.Flush()
		}
		return
	}

	// ── 8. Gate.Confirm ───────────────────────────────────────────────────────
	if gateAdmitted {
		if err := h.gateStore.Confirm(r.Context(), gateCfg); err != nil {
			h.logger.Warn("sse: gate confirm failed",
				"ep_slug", epSlug,
				"app_id", sessInfo.AppID,
				"error", err)
			// Non-fatal: session hash is registered; shadow TTL (10s) provides safety net.
		}
	}

	appID := ""
	if resolvedCfg != nil {
		appID = resolvedCfg.AppID
	}

	// Track active session count and record session lifecycle metrics.
	epType := epTypeLabel(resolvedCfg)
	metrics.ActiveSessions.WithLabelValues(epType).Inc()
	metrics.SessionsStarted.WithLabelValues(epType, "admitted").Inc()

	h.logger.Info("sse: session started",
		"ep_slug", epSlug,
		"app_id", appID,
		"tenant_id", sessInfo.TenantID,
		"session_id", sessionID,
	)

	defer func() {
		metrics.ActiveSessions.WithLabelValues(epType).Dec()
		metrics.SessionsEnded.WithLabelValues(epType, "client_disconnect").Inc()

		h.logger.Info("sse: session ended",
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

	// ── 9. Subscribe to event bus (kept for future in-process events) ────────
	// After R-2B, the SSE handler streams from the Redis run-stream (rsEvCh),
	// not from evCh/termCh. The subscription is a no-op for the Temporal path.
	_, _, unsub := h.bus.Subscribe(r.Context(), contextID, 256)
	defer unsub()

	// ── 10. Create run record ─────────────────────────────────────────────────
	// events_transport is decided by RUN_EVENTS_MODE at run-creation time and is
	// stable for the run's lifetime (Phase 11c-B).
	eventsTransport := h.eventsTransportForNewRun()
	run := domain.Run{
		ID:              runID,
		ContextID:       contextID,
		EntryPointSlug:  epSlug,
		Status:          domain.RunStatusRunning,
		EventsTransport: eventsTransport,
	}
	if err := h.recorder.CreateRun(r.Context(), run); err != nil {
		h.logger.Warn("sse: create run failed", "run_id", runID, "error", err)
	}

	// SSE resume cursor: the standard Last-Event-ID header set by EventSource on
	// reconnect. "" when absent → full replay / start from beginning.
	lastEventID := r.Header.Get("Last-Event-ID")

	// ── 11. Start orchestration (Temporal — unconditional after R-2B) ────────
	// When temporalClient is nil (Temporal not configured), return 503 immediately.
	// There is NO inline fallback — Temporal is the only execution path.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if h.temporalClient == nil {
		h.logger.Warn("sse: temporal client unavailable — rejecting request", "run_id", runID)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"orchestration service unavailable\"}\n\n")
		if hasFlusher {
			flusher.Flush()
		}
		_ = h.recorder.UpdateStatus(r.Context(), runID, domain.RunStatusFailed)
		return
	}

	// Subscribe to the run-stream BEFORE starting the workflow so no event
	// is missed (critical bootstrap ordering).
	rsEvCh, rsErr := h.runEvents(ctx, runID, eventsTransport, lastEventID)
	if rsErr != nil {
		h.logger.Warn("sse: runstream subscribe failed", "run_id", runID, "error", rsErr)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"event stream unavailable\"}\n\n")
		if hasFlusher {
			flusher.Flush()
		}
		_ = h.recorder.UpdateStatus(r.Context(), runID, domain.RunStatusFailed)
		return
	}

	tokenPayload := map[string]any{"user_id": tokenInfo.TokenID}

	input := temporal.PythonOrchestrationInput{
		OrchestratorName: appSlug,
		UserMessage:      userText,
		UserID:           tokenInfo.TokenID,
		TokenPayload:     tokenPayload,
		SessionID:        sessionID,
		ContextID:        contextID,
		RunID:            runID,
		EntryPointSlug:   epSlug,
		HistoryWindow:    20,
	}

	wfOpts := temporalclient.StartWorkflowOptions{
		// Python OrchestrationWorkflow registers itself as "ctx-{contextID}".
		// Go must use the same scheme so HITL signals reach the correct workflow.
		ID:        "ctx-" + contextID,
		TaskQueue: temporal.TaskQueue,
	}

	wfRun, wfErr := h.temporalClient.ExecuteWorkflow(ctx, wfOpts, temporal.WorkflowType, input)
	if wfErr != nil {
		h.logger.Warn("sse: start temporal workflow failed", "run_id", runID, "error", wfErr)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"failed to start workflow\"}\n\n")
		if hasFlusher {
			flusher.Flush()
		}
		return
	}
	h.logger.Info("sse: temporal workflow started", "run_id", runID, "workflow_id", wfRun.GetID())

	orchDone := make(chan struct{})

	// Wait for workflow completion in background (drives orchDone).
	go func() {
		defer close(orchDone)
		if err := wfRun.Get(ctx, nil); err != nil {
			h.logger.Warn("sse: temporal workflow error", "run_id", runID, "error", err)
		}
	}()

	// ── 12. Stream Redis run events as SSE ────────────────────────────────────
	// Redis run-stream events arrive on rsEvCh (not the in-process bus),
	// so termCh is not used here — pass nil to signal no termCh available.
	h.streamEvents(ctx, cancel, w, flusher, hasFlusher, rsEvCh, nil, orchDone)
}

// streamEvents forwards bus events to the SSE response until orchestration
// completes or the client disconnects.
// termCh is the dedicated terminal-event channel (capacity 1) from the in-process
// bus (R-0 L-1 fix). Pass nil when using the Redis run-stream path.
func (h *Handler) streamEvents(
	ctx context.Context,
	cancel context.CancelFunc,
	w http.ResponseWriter,
	flusher http.Flusher,
	hasFlusher bool,
	evCh <-chan event.Event,
	termCh <-chan event.Event,
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
		case ev, ok := <-termCh:
			// Terminal event via dedicated channel (R-0 L-1 fix).
			// termCh may be nil when using the Redis run-stream path; a nil channel
			// blocks forever in a select (never selected), which is the correct behaviour.
			if !ok {
				return
			}
			_ = writeSSE(ev)
			return
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
				case ev, ok := <-termCh:
					if !ok {
						return
					}
					_ = writeSSE(ev)
					return
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
	case "replay_unavailable":
		// Emitted by StreamFromRedis when last_event_id was trimmed by MAXLEN.
		// Forward so the client can display a notice (Phase 11c-B).
		var reason string
		if raw, ok := payload["reason"]; ok {
			_ = json.Unmarshal(raw, &reason)
		}
		var runID string
		if raw, ok := payload["run_id"]; ok {
			_ = json.Unmarshal(raw, &runID)
		}
		msg = map[string]any{"type": "replay_unavailable", "reason": reason, "run_id": runID}
	default:
		return "", fmt.Errorf("sse: unknown event type %q", ev.Type)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("sse: marshal event: %w", err)
	}
	return "data: " + string(data) + "\n\n", nil
}

// tryAuthenticate extracts and validates the bearer token.
// Checks Authorization header first, then ?token= query param.
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

// extractMessage reads the user message from ?message= query param (GET) or
// from the request body (POST).
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
