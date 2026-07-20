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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/temporal"
)

// newID generates a random 16-byte hex string suitable for session/run IDs.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// tokenHash returns the lowercase hex SHA-256 of rawToken, matching the hash
// stored in them.access_tokens by the Python platform (same as auth.tokenHash).
func tokenHash(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return fmt.Sprintf("%x", h)
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

// TemporalClientExecutor starts a Temporal workflow execution.
// Using an interface (rather than the full client.Client) allows tests to inject
// a fake without depending on a live Temporal server.
type TemporalClientExecutor interface {
	ExecuteWorkflow(ctx context.Context, options temporalclient.StartWorkflowOptions, workflow interface{}, args ...interface{}) (temporalclient.WorkflowRun, error)
}

// Handler is the SSE orchestration handler.
type Handler struct {
	sessions        SessionStore
	gateStore       GateStore
	epLoader        EPConfigLoader
	recorder        *runrecorder.Recorder
	orch            *orchestrator.Orchestrator
	bus             event.Bus
	authenticator   Authenticator
	instanceID      string
	logger          *slog.Logger
	temporalClient  TemporalClientExecutor
	runStreamSub    runstream.Subscriber
	temporalEnabled bool
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

// WithTemporal attaches a Temporal client and run-stream subscriber. When
// temporalEnabled is true, incoming connections use the Temporal execution
// path instead of the Go-inline orchestrator.
// When WithTemporal is not called (e.g. in tests), temporalEnabled defaults to
// false and all connections use the inline path.
func (h *Handler) WithTemporal(tc TemporalClientExecutor, sub runstream.Subscriber, enabled bool) *Handler {
	h.temporalClient = tc
	h.runStreamSub = sub
	h.temporalEnabled = enabled
	return h
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
			w.Header().Set("Content-Type", "application/json")
			switch err {
			case gate.ErrCapExceeded:
				http.Error(w, `{"error":"session cap exceeded"}`, http.StatusServiceUnavailable)
			case gate.ErrRateLimited:
				http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
			case gate.ErrQueueFull:
				http.Error(w, `{"error":"queue full"}`, http.StatusServiceUnavailable)
			default:
				h.logger.Warn("sse: gate check failed", "error", err)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			}
			return
		}
		gateAdmitted = true
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
	if err := h.sessions.Register(r.Context(), sessInfo); err != nil {
		h.logger.Warn("sse: register session failed", "session_id", sessionID, "error", err)
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
			h.logger.Warn("sse: gate confirm failed", "session_id", sessionID, "error", err)
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

	// ── 9. CRITICAL: Subscribe to event bus BEFORE starting the workflow ──────
	evCh, unsub := h.bus.Subscribe(r.Context(), contextID, 256)
	defer unsub()

	// ── 10. Create run record ─────────────────────────────────────────────────
	run := domain.Run{
		ID:             runID,
		ContextID:      contextID,
		EntryPointSlug: epSlug,
		Status:         domain.RunStatusRunning,
	}
	if err := h.recorder.CreateRun(r.Context(), run); err != nil {
		h.logger.Warn("sse: create run failed", "run_id", runID, "error", err)
	}

	// ── 11. Start orchestration ───────────────────────────────────────────────
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	userMsg := domain.TextMessage(domain.RoleUser, userText)

	orchDone := make(chan struct{})

	if h.temporalEnabled && h.temporalClient != nil {
		// ── 11a. Temporal path — two-phase channel handshake ─────────────────
		//
		// Phase 1: subscribe to the context channel BEFORE starting the workflow.
		// Python's init_run activity publishes a "ready" event here that carries
		// the Python-generated run_id (workflow.uuid4() inside the Python workflow).
		// We must subscribe first to avoid missing this event.
		ctxEvCh, ctxErr := runstream.StreamContext(ctx, h.runStreamSub, contextID)
		if ctxErr != nil {
			h.logger.Warn("sse: runstream context-channel subscribe failed — falling back to inline",
				"run_id", runID, "context_id", contextID, "error", ctxErr)
			go func() {
				defer close(orchDone)
				_, runErr := h.orch.Run(ctx, runID, contextID, userMsg, nil)
				if runErr != nil {
					h.logger.Warn("sse: orchestrator error", "run_id", runID, "error", runErr)
				}
			}()
			h.streamEvents(ctx, cancel, w, flusher, hasFlusher, evCh, orchDone)
			return
		}

		// Build the minimal token payload from the resolved token.
		tokenPayload := map[string]any{"user_id": tokenInfo.TokenID}

		input := temporal.PythonOrchestrationInput{
			OrchestratorName: appSlug,
			UserMessage:      userText,
			UserID:           tokenInfo.TokenID,
			TokenPayload:     tokenPayload,
			SessionID:        sessionID,
			ContextID:        contextID,
			EntryPointSlug:   epSlug,
			HistoryWindow:    20,
		}

		wfOpts := temporalclient.StartWorkflowOptions{
			ID:        runID,
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
		h.logger.Info("sse: temporal workflow started",
			"run_id", runID,
			"workflow_id", wfRun.GetID(),
		)

		// Wait for workflow completion in background (drives orchDone).
		go func() {
			defer close(orchDone)
			if err := wfRun.Get(ctx, nil); err != nil {
				h.logger.Warn("sse: temporal workflow error", "run_id", runID, "error", err)
			}
		}()

		// Phase 2: wait for the "ready" event which carries the Python run_id.
		var pythonRunID string
		select {
		case ev, ok := <-ctxEvCh:
			if ok && ev.Type == "ready" {
				pythonRunID, _ = runstream.RunIDFromReady(ev)
			}
		case <-time.After(30 * time.Second):
			h.logger.Warn("sse: timed out waiting for ready event", "run_id", runID)
		case <-ctx.Done():
			_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"cancelled before workflow started\"}\n\n")
			if hasFlusher {
				flusher.Flush()
			}
			return
		}

		// Phase 3: subscribe to the token stream using the Python run_id.
		var rsEvCh <-chan event.Event
		if pythonRunID != "" {
			ch, rsErr := runstream.Stream(ctx, h.runStreamSub, pythonRunID)
			if rsErr != nil {
				h.logger.Warn("sse: runstream tokens-channel subscribe failed — falling back to inline",
					"run_id", runID, "python_run_id", pythonRunID, "error", rsErr)
				go func() {
					defer close(orchDone)
					_, runErr := h.orch.Run(ctx, runID, contextID, userMsg, nil)
					if runErr != nil {
						h.logger.Warn("sse: orchestrator error", "run_id", runID, "error", runErr)
					}
				}()
				h.streamEvents(ctx, cancel, w, flusher, hasFlusher, evCh, orchDone)
				return
			}
			rsEvCh = ch
		} else {
			// No ready event — stream nothing; orchDone closes when wfRun.Get returns.
			rsEvCh = make(chan event.Event)
		}

		// ── 12a. Stream Redis run events as SSE ───────────────────────────────
		h.streamEvents(ctx, cancel, w, flusher, hasFlusher, rsEvCh, orchDone)
	} else {
		// ── 11b. Go-inline path (permanent fallback) ──────────────────────────
		go func() {
			defer close(orchDone)
			_, runErr := h.orch.Run(ctx, runID, contextID, userMsg, nil)
			if runErr != nil {
				h.logger.Warn("sse: orchestrator error", "run_id", runID, "error", runErr)
			}
		}()

		// ── 12b. Stream in-process bus events as SSE ─────────────────────────
		h.streamEvents(ctx, cancel, w, flusher, hasFlusher, evCh, orchDone)
	}
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
