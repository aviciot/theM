// Package a2a implements a JSON-RPC 2.0 A2A (Agent-to-Agent) server that
// exposes an orchestrator as an A2A agent, using the official a2a-go/v2 SDK
// for 100% A2A v1.0 wire-format compliance.
//
// Routes:
//
//	POST /a2a/{app_slug}/{ep_slug}                         — JSON-RPC 2.0 endpoint
//	GET  /a2a/{app_slug}/{ep_slug}/.well-known/agent.json  — A2A agent card
//
// Execution pipeline (shared via internal/execution):
//
//	extractRawToken → pre-admit (auth/gate/session/run) → SDK dispatch →
//	executor: Lifecycle.Start → drain run-stream → yield SDK events
//
// TenantID and ApplicationID come from EPConfig only; never from the request.
package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/aviciot/them/internal/agentgen"
	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/dashboard"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/tenantctx"
	"github.com/aviciot/them/internal/transport"
)

// Authenticator validates bearer tokens. Implemented by auth.Cache.
type Authenticator = transport.Authenticator

// Server is the A2A JSON-RPC 2.0 server backed by the official a2a-go/v2 SDK.
type Server struct {
	lc            *execution.Lifecycle
	bus           event.Bus
	authenticator Authenticator
	instanceID    string
	logger        *slog.Logger
	runStreamer    runstream.RedisStreamer
	sessionPub    *dashboard.SessionPublisher // optional; nil → no Monitor events
	publicURL     string                      // externally-reachable base URL
	cardLoader    CardLoader                  // optional; nil → minimal fallback card
	taskStore     *agentgen.RedisA2ATaskStore // optional; nil → SDK in-memory store
}

// NewServer creates a Server backed by the shared execution Lifecycle.
func NewServer(
	lifecycle *execution.Lifecycle,
	bus event.Bus,
	authenticator Authenticator,
	instanceID string,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		lc:            lifecycle,
		bus:           bus,
		authenticator: authenticator,
		instanceID:    instanceID,
		logger:        logger,
	}
}

// WithRunStreamer attaches the Redis Streams reader for cross-process event delivery.
func (s *Server) WithRunStreamer(rc runstream.RedisStreamer) *Server {
	s.runStreamer = rc
	return s
}

// WithPublicURL sets the externally-reachable base URL used in the agent card.
func (s *Server) WithPublicURL(u string) *Server {
	s.publicURL = strings.TrimRight(u, "/")
	return s
}

// WithCardLoader attaches a CardLoader for serving synthesized agent cards.
func (s *Server) WithCardLoader(cl CardLoader) *Server {
	s.cardLoader = cl
	return s
}

// WithTaskStore attaches a Redis-backed task store for SDK task persistence.
// When nil (the default) the SDK uses an in-memory store.
func (s *Server) WithTaskStore(ts *agentgen.RedisA2ATaskStore) *Server {
	s.taskStore = ts
	return s
}

// WithSessionPublisher attaches a SessionPublisher so A2A sessions are visible
// in the dashboard Monitor view.
func (s *Server) WithSessionPublisher(pub *dashboard.SessionPublisher) *Server {
	s.sessionPub = pub
	return s
}

// Routes returns an http.Handler with A2A routes mounted.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/a2a/{app_slug}/{ep_slug}", s.handle)
	r.Get("/a2a/{app_slug}/{ep_slug}/.well-known/agent.json", s.handleAgentCard)
	return r
}

// handle is the A2A JSON-RPC endpoint.
//
// Strategy: buffer the body, quick-parse to extract message text and contextId
// for admission, run full admission (auth / EP config / gate / session / CreateRun),
// then restore the body and delegate to the SDK JSON-RPC handler which provides
// full method dispatch with A2A v1.0-compliant wire format.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	appSlug := chi.URLParam(r, "app_slug")
	epSlug := chi.URLParam(r, "ep_slug")
	rawToken := s.extractRawToken(r)

	// ── 1. Buffer body for double-reading ─────────────────────────────────────
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		writeHTTPError(w, nil, http.StatusBadRequest, "failed to read request body")
		return
	}
	// Restore body so the SDK can re-read it.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// ── 2. Quick-parse for admission inputs ───────────────────────────────────
	// We only need the method, message text, and optional contextId. Errors here are
	// best-effort; the SDK will re-parse and produce a proper parse error.
	var envelope struct {
		Method string `json:"method"`
		Params struct {
			Message struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
				ContextID string `json:"contextId"`
			} `json:"message"`
		} `json:"params"`
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(bodyBytes, &envelope)

	var userText string
	for _, part := range envelope.Params.Message.Parts {
		if part.Text != "" {
			userText = part.Text
			break
		}
	}
	contextID := envelope.Params.Message.ContextID
	rpcID := envelope.ID

	// ── 3. Resolve tenant from bearer token ───────────────────────────────────
	tenantID := tenantctx.BootstrapTenantID
	if rawToken != "" && s.authenticator != nil {
		if ti, err := s.authenticator.Validate(r.Context(), rawToken); err == nil && ti.TenantID != "" {
			tenantID = ti.TenantID
		}
	}

	// ── 4. Validate user text before admission ────────────────────────────────
	// Only enforce the text check for message-sending methods. For other methods
	// (GetTask, CancelTask, unknown), delegate to the SDK for proper error handling.
	isMessageMethod := envelope.Method == "SendMessage" || envelope.Method == "SendStreamingMessage"
	if isMessageMethod && userText == "" {
		writeHTTPError(w, rpcID, http.StatusOK, "no text content in message")
		return
	}

	// For non-message methods (GetTask, CancelTask, unknown methods, etc.),
	// skip pre-admission and delegate directly to the SDK for proper routing.
	// The SDK will return -32601 for unknown methods and handle known methods.
	if !isMessageMethod {
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		// Use a no-op executor — the SDK will route non-SendMessage/SendStreamingMessage
		// methods without calling the executor.
		noopExec := a2asrv.AgentExecutorFunc(func(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
			return func(yield func(a2a.Event, error) bool) {}
		})
		inner := a2asrv.NewHandler(noopExec, a2asrv.WithLogger(s.logger))
		a2asrv.NewJSONRPCHandler(inner).ServeHTTP(w, r)
		return
	}

	// ── 5. Pre-admit (auth → EPConfig → access → gate → session → CreateRun) ─
	// We use a placeholder UserMessage; Start() receives the real user message.
	// The run record is created here; Start() updates the WorkflowInput fields.
	admitReq := execution.ExecutionRequest{
		AppSlug:     appSlug,
		EPSlug:      epSlug,
		TenantID:    tenantID,
		RawToken:    rawToken,
		ContextID:   contextID,
		InstanceID:  s.instanceID,
		UserMessage: domain.TextMessage(domain.RoleUser, userText),
	}
	h, admitErr := s.lc.Admit(r.Context(), admitReq)
	if admitErr != nil {
		// Write HTTP-level error before the SDK has a chance to set headers.
		s.mapAdmitError(w, rpcID, admitErr)
		return
	}
	defer s.lc.Release(h)

	if s.sessionPub != nil {
		s.sessionPub.PublishSessionStart(r.Context(), h.SessionInfo())
		sid, appID := h.SessionID, h.EPConfig.AppID
		defer func() {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s.sessionPub.PublishSessionEnd(cleanCtx, sid, appID)
		}()
	}

	s.logger.Info("a2a: session admitted",
		"app_slug", appSlug,
		"app_id", h.EPConfig.AppID,
		"session_id", h.SessionID,
		"run_id", h.RunID,
	)

	// ── 6. Restore body and delegate to SDK ──────────────────────────────────
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	executor := s.orchExecutorFunc(h, userText)

	handlerOpts := []a2asrv.RequestHandlerOption{
		a2asrv.WithLogger(s.logger),
	}
	if s.taskStore != nil {
		handlerOpts = append(handlerOpts, a2asrv.WithTaskStore(s.taskStore))
	}

	inner := a2asrv.NewHandler(executor, handlerOpts...)
	a2asrv.NewJSONRPCHandler(inner).ServeHTTP(w, r)
}

// extractRawToken reads the bearer token from Authorization header or ?token= query param.
// It does NOT validate — Lifecycle.Admit owns enforcement.
func (s *Server) extractRawToken(r *http.Request) string {
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		return strings.TrimPrefix(hdr, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// mapAdmitError converts a *execution.AdmitError to an HTTP+JSON-RPC response.
// HTTP status codes communicate the failure class to A2A callers.
func (s *Server) mapAdmitError(w http.ResponseWriter, id json.RawMessage, err error) {
	ae, ok := err.(*execution.AdmitError)
	if !ok {
		writeHTTPError(w, id, http.StatusInternalServerError, "internal error")
		return
	}
	switch ae.Kind {
	case execution.AdmitErrNotFound:
		writeHTTPError(w, id, http.StatusNotFound, "entry point not found")
	case execution.AdmitErrUnauthorized:
		writeHTTPError(w, id, http.StatusUnauthorized, "unauthorized")
	case execution.AdmitErrForbidden:
		writeHTTPError(w, id, http.StatusForbidden, "access denied")
	case execution.AdmitErrCapExceeded:
		writeHTTPError(w, id, http.StatusTooManyRequests, "session cap exceeded")
	case execution.AdmitErrRateLimited:
		writeHTTPError(w, id, http.StatusTooManyRequests, "rate limited")
	case execution.AdmitErrQueueFull, execution.AdmitErrDBUnavailable:
		writeHTTPError(w, id, http.StatusServiceUnavailable, "service unavailable")
	default:
		writeHTTPError(w, id, http.StatusInternalServerError, "internal error")
	}
}

// ── Response helpers ──────────────────────────────────────────────────────────

// writeHTTPError writes an HTTP-level error with a JSON-RPC 2.0 error body.
func writeHTTPError(w http.ResponseWriter, id json.RawMessage, httpStatus int, message string) {
	type rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	type rpcResp struct {
		JSONRPC string          `json:"jsonrpc"`
		Error   *rpcErr         `json:"error,omitempty"`
		ID      json.RawMessage `json:"id"`
	}
	resp := rpcResp{
		JSONRPC: "2.0",
		Error:   &rpcErr{Code: -32603, Message: message},
		ID:      id,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(resp)
}
