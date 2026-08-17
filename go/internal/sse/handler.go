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
//	runEvents: subscribe to run-stream BEFORE Start (bootstrap ordering invariant)
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

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/metrics"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/tenantctx"
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
	runStreamer   runstream.RedisStreamer
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

// WithRunStreamer attaches the Redis Streams reader used to deliver run events
// to the client. The Temporal client is held by the Lifecycle. When not
// attached, runEvents fails fast at connect time.
func (h *Handler) WithRunStreamer(rc runstream.RedisStreamer) *Handler {
	h.runStreamer = rc
	return h
}

// runEvents opens the event channel for a run by reading the run's Redis Stream
// (them:dash:run:{runID}:stream). lastEventID is the client's resume cursor.
func (h *Handler) runEvents(ctx context.Context, runID, lastEventID string) (<-chan event.Event, error) {
	return runstream.StreamFromRedis(ctx, h.runStreamer, runID, runstream.StreamerOptions{LastEventID: lastEventID})
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

	// ── 2. Extract raw token (Lifecycle.Admit owns all validation/enforcement) ─
	rawToken := h.extractRawToken(r)

	// ── 3. Resolve tenant identity for EP config lookup ──────────────────────
	// Tenant comes from the bearer token or JWT. For public EPs (no token),
	// use the bootstrap tenant UUID (single-tenant deployment safe).
	tenantID := tenantctx.BootstrapTenantID
	if rawToken != "" && h.authenticator != nil {
		if ti, err := h.authenticator.Validate(r.Context(), rawToken); err == nil && ti.TenantID != "" {
			tenantID = ti.TenantID
		}
	}

	// ── 4. Admit (auth → EPConfig → voice-check → access → gate → session → CreateRun) ──
	// All pre-Admit errors return clean HTTP responses. After Admit succeeds,
	// SSE headers are written and errors become SSE error events.
	admitReq := execution.ExecutionRequest{
		EPSlug:      epSlug,
		TenantID:    tenantID,
		RawToken:    rawToken,
		UserMessage: domain.TextMessage(domain.RoleUser, userText),
		InstanceID:  h.instanceID,
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

		h.lc.Release(handle)
	}()

	lastEventID := r.Header.Get("Last-Event-ID")

	// ── 7. Subscribe to run-stream ───────────────────────────────────────────
	// Subscribe BEFORE calling Start (ExecuteWorkflow) so no event emitted
	// between workflow launch and stream open can be lost (bootstrap ordering).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	rsEvCh, rsErr := h.runEvents(ctx, handle.RunID, lastEventID)
	if rsErr != nil {
		h.logger.Warn("sse: runstream subscribe failed", "run_id", handle.RunID, "error", rsErr)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"event stream unavailable\"}\n\n")
		if hasFlusher {
			flusher.Flush()
		}
		return
	}

	// ── 8. Resolve orchestrator name from EP binding (SEC-04) ────────────────
	// OrchestratorName comes from entry_points.app_orchestrator_id →
	// app_orchestrators.name, resolved by epconfig at admission time.
	// An unbound EP (OrchestratorName == "") is a configuration error.
	orchName := handle.EPConfig.OrchestratorName
	if orchName == "" {
		epSlug := chi.URLParam(r, "entry_point_slug")
		h.logger.Warn("sse: entry point has no orchestrator bound",
			"ep_slug", epSlug,
			"app_id", handle.EPConfig.AppID,
		)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"entry point has no orchestrator configured\"}\n\n")
		if hasFlusher {
			flusher.Flush()
		}
		return
	}

	// ── 9. Start Temporal workflow ────────────────────────────────────────────
	// Run-stream is already subscribed — no events can be lost.
	input := temporal.WorkflowInput{
		OrchestratorName:  orchName,
		AppOrchestratorID: handle.EPConfig.AppOrchestratorID,
		UserMessage:       domain.TextMessage(domain.RoleUser, userText),
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

	// Send ready — client needs run_id + context_id before stream events arrive.
	if readyJSON, err := json.Marshal(map[string]any{"type": "ready", "run_id": handle.RunID, "context_id": handle.ContextID}); err == nil {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", readyJSON)
		if hasFlusher {
			flusher.Flush()
		}
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

// extractRawToken reads the bearer token string from the Authorization header
// or ?token= param. It does NOT validate — Lifecycle.Admit owns all enforcement.
func (h *Handler) extractRawToken(r *http.Request) string {
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		return strings.TrimPrefix(hdr, "Bearer ")
	}
	return r.URL.Query().Get("token")
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
