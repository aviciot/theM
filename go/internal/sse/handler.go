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
// Execution pipeline (via execution.Lifecycle):
//
//	Admit: auth → EPConfig → voice-check → access → gate → session → CreateRun
//	[SSE headers written AFTER Admit succeeds — all pre-Admit errors are clean HTTP responses]
//	Start: ExecuteWorkflow on GoTaskQueue
//	Release: session.End + gate.Release (always, via defer)
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

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/metrics"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/transport"
)

// Authenticator validates bearer tokens.
// Sourced from internal/transport.
type Authenticator = transport.Authenticator

// Handler is the SSE orchestration handler.
type Handler struct {
	lc            *execution.Lifecycle
	recorder      *runrecorder.Recorder
	bus           event.Bus
	authenticator Authenticator
	instanceID    string
	logger        *slog.Logger
	runStreamSub  runstream.Subscriber
	dispatcher    *runstream.Dispatcher
	runEventsMode config.RunEventsMode
}

// NewHandler creates a Handler. The lifecycle owns auth, EPConfig, gate, session,
// and CreateRun. recorder is retained for UpdateStatus calls after workflow
// completion.
func NewHandler(
	lc *execution.Lifecycle,
	recorder *runrecorder.Recorder,
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
		recorder:      recorder,
		bus:           bus,
		authenticator: authenticator,
		instanceID:    instanceID,
		logger:        logger,
	}
}

// WithTemporal attaches a run-stream subscriber. The Temporal client is held by
// the Lifecycle; this method keeps the caller-side TemporalClientExecutor
// reference only as a nil-check sentinel (kept for API compatibility with WS
// handler wiring in main.go).
func (h *Handler) WithTemporal(tc transport.TemporalClientExecutor, sub runstream.Subscriber, _ bool) *Handler {
	h.runStreamSub = sub
	return h
}

// WithRunEvents attaches the run-event dispatcher and the active RUN_EVENTS_MODE.
// The dispatcher chooses Pub/Sub or Redis Streams per run based on mode and the
// run's events_transport value.
func (h *Handler) WithRunEvents(d *runstream.Dispatcher, mode config.RunEventsMode) *Handler {
	h.dispatcher = d
	h.runEventsMode = mode
	return h
}

// runEvents opens the event channel for a run using the dispatcher (when wired)
// or the legacy Pub/Sub subscriber.
func (h *Handler) runEvents(ctx context.Context, runID, eventsTransport, lastEventID string) (<-chan event.Event, error) {
	if h.dispatcher != nil {
		return h.dispatcher.Stream(ctx, runID, eventsTransport, lastEventID)
	}
	return runstream.Stream(ctx, h.runStreamSub, runID)
}

// Routes returns an http.Handler that mounts the SSE orchestration routes.
// Accepts both GET (message as ?message=) and POST (message in JSON body).
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/orchestrate/{app_slug}/{entry_point_slug}", h.ServeHTTP)
	r.Post("/orchestrate/{app_slug}/{entry_point_slug}", h.ServeHTTP)
	return r
}

// AppsSSERoute returns an http.Handler for /{slug}/sse (relative path).
// Mount at /apps so the full external path is /apps/{slug}/sse.
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
	epSlug := chi.URLParam(r, "entry_point_slug")

	metrics.ActiveSSEConnections.Inc()
	defer metrics.ActiveSSEConnections.Dec()

	// ── 1. Extract user message ───────────────────────────────────────────────
	userText, err := h.extractMessage(r)
	if err != nil || userText == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"missing message"}`, http.StatusBadRequest)
		return
	}

	// ── 2. Extract bearer token (non-enforcing — Lifecycle decides per EP policy) ──
	rawToken, tokenInfo := h.extractToken(r)

	// ── 3. Admit (auth → EPConfig → voice-check → access → gate → session → CreateRun) ──
	// All pre-Admit errors return clean HTTP responses. After Admit succeeds,
	// SSE headers are written and errors become SSE error events.
	admitReq := execution.ExecutionRequest{
		EPSlug:        epSlug,
		RawToken:      rawToken,
		TokenInfo:     tokenInfo,
		UserMessage:   domain.TextMessage(domain.RoleUser, userText),
		RunEventsMode: h.runEventsMode,
		InstanceID:    h.instanceID,
	}
	handle, admitErr := h.lc.Admit(r.Context(), admitReq)
	if admitErr != nil {
		w.Header().Set("Content-Type", "application/json")
		var ae *execution.AdmitError
		if errors.As(admitErr, &ae) {
			if ae.Kind == execution.AdmitErrNotImplemented {
				http.Error(w, `{"error":"voice entry points are not yet implemented"}`, http.StatusNotImplemented)
			} else {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, ae.Error()), ae.HTTPStatus)
			}
		} else {
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		}
		return
	}

	// ── 4. Write SSE headers AFTER Admit succeeds ─────────────────────────────
	// All error paths before this point return clean HTTP errors.
	// After this point, errors are delivered as SSE data events.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, hasFlusher := w.(http.Flusher)

	// ── 5. Metrics and deferred cleanup ──────────────────────────────────────
	epType := epTypeLabel(handle.EPConfig)
	metrics.ActiveSessions.WithLabelValues(epType).Inc()
	metrics.SessionsStarted.WithLabelValues(epType, "admitted").Inc()
	metrics.GateAdmissions.WithLabelValues(epType).Inc()

	h.logger.Info("sse: session started",
		"ep_slug", epSlug,
		"app_id", handle.EPConfig.AppID,
		"tenant_id", handle.EPConfig.TenantID,
		"session_id", handle.SessionID,
		"run_id", handle.RunID,
	)

	defer func() {
		metrics.ActiveSessions.WithLabelValues(epType).Dec()
		metrics.SessionsEnded.WithLabelValues(epType, "client_disconnect").Inc()

		h.logger.Info("sse: session ended",
			"ep_slug", epSlug,
			"app_id", handle.EPConfig.AppID,
			"tenant_id", handle.EPConfig.TenantID,
			"session_id", handle.SessionID,
		)

		h.lc.Release(context.Background(), handle)
	}()

	// ── 6. Subscribe to event bus (kept for future in-process events) ────────
	// After Temporal migration, SSE streams from the Redis run-stream (rsEvCh).
	// The in-process bus subscription is a no-op for the Temporal path.
	_, _, unsub := h.bus.Subscribe(r.Context(), handle.ContextID, 256)
	defer unsub()

	lastEventID := r.Header.Get("Last-Event-ID")

	// ── 7. Start Temporal workflow ────────────────────────────────────────────
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	appSlug := chi.URLParam(r, "app_slug")
	input := temporal.WorkflowInput{
		OrchestratorName: appSlug,
		UserMessage:      domain.TextMessage(domain.RoleUser, userText),
	}
	// Identity fields (RunID, ContextID, TenantID, ApplicationID, EntryPointSlug) are
	// overwritten by Lifecycle.Start from the handle — caller values are ignored.
	wfRun, startErr := h.lc.Start(ctx, handle, input)
	if startErr != nil {
		h.logger.Warn("sse: start temporal workflow failed", "run_id", handle.RunID, "error", startErr)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"failed to start workflow\"}\n\n")
		if hasFlusher {
			flusher.Flush()
		}
		return
	}
	h.logger.Info("sse: temporal workflow started", "run_id", handle.RunID, "workflow_id", wfRun.GetID())

	// ── 8. Subscribe to run-stream ────────────────────────────────────────────
	// Subscribe BEFORE starting the goroutine that waits for orchDone so no
	// event between workflow start and stream open can be lost.
	rsEvCh, rsErr := h.runEvents(ctx, handle.RunID, handle.EventsTransport, lastEventID)
	if rsErr != nil {
		h.logger.Warn("sse: runstream subscribe failed", "run_id", handle.RunID, "error", rsErr)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"event stream unavailable\"}\n\n")
		if hasFlusher {
			flusher.Flush()
		}
		return
	}

	orchDone := make(chan struct{})
	go func() {
		defer close(orchDone)
		if err := wfRun.Get(ctx, nil); err != nil {
			h.logger.Warn("sse: temporal workflow error", "run_id", handle.RunID, "error", err)
		}
	}()

	// ── 9. Stream Redis run events as SSE ─────────────────────────────────────
	h.streamEvents(ctx, cancel, w, flusher, hasFlusher, rsEvCh, nil, orchDone)
}

// streamEvents forwards run-stream events to the SSE response until orchestration
// completes or the client disconnects.
// termCh is the terminal-event channel from the in-process bus. Pass nil when
// using the Redis run-stream path (a nil channel never fires in a select).
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
			// termCh is nil for the Redis run-stream path — a nil channel never fires.
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

// extractToken reads the bearer token from Authorization header or ?token= param,
// then validates it. Returns (rawToken, tokenInfo). tokenInfo is nil if no valid
// token was found; Lifecycle.Admit decides enforcement per EP access policy.
func (h *Handler) extractToken(r *http.Request) (string, *auth.TokenInfo) {
	var rawToken string
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		rawToken = strings.TrimPrefix(hdr, "Bearer ")
	} else if t := r.URL.Query().Get("token"); t != "" {
		rawToken = t
	}
	if rawToken == "" || h.authenticator == nil {
		return rawToken, nil
	}
	info, err := h.authenticator.Validate(r.Context(), rawToken)
	if err != nil {
		return rawToken, nil
	}
	return rawToken, info
}

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
