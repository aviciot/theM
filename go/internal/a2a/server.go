// Package a2a implements a JSON-RPC 2.0 A2A (Agent-to-Agent) server that
// exposes an orchestrator as an A2A agent. This is the "orchestrator-as-agent"
// pattern: external callers can invoke this platform as if it were an A2A agent.
//
// Routes:
//
//	POST /a2a/{app_slug}            — JSON-RPC 2.0 endpoint
//	GET  /.well-known/agent.json    — A2A agent card
//
// Supported A2A methods:
//
//	message/send   — blocking: waits for workflow completion, returns full result.
//	message/stream — streaming: responds with text/event-stream, emitting A2A
//	                 streaming events as the workflow runs, then a final
//	                 task-status-update (completed/failed).
//
// Execution pipeline (shared via internal/execution):
//
//	tryAuthenticate → Lifecycle.Admit (EPConfig, auth, access, gate, session, run) →
//	bus.Subscribe → Lifecycle.Start (ExecuteWorkflow) →
//	stream/block on bus events → defer Lifecycle.Release
//
// TenantID and ApplicationID come from EPConfig only; never from the request.
package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/tenantctx"
	"github.com/aviciot/them/internal/transport"
)

// JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// ── JSON-RPC wire types ───────────────────────────────────────────────────────

// rpcRequest is the JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id"` // string, number, or null
}

// rpcResponse is the JSON-RPC 2.0 response envelope.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  *rpcResult      `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type rpcResult struct {
	TaskID    string        `json:"taskId"`
	Status    rpcStatus     `json:"status"`
	Artifacts []rpcArtifact `json:"artifacts"`
}

type rpcStatus struct {
	State string `json:"state"`
}

type rpcArtifact struct {
	Parts []rpcTextPart `json:"parts"`
}

// rpcTextPart is a text-only part. A2A spec uses field presence as the
// discriminator — there is no "kind" field. The discriminator is "text".
type rpcTextPart struct {
	Text string `json:"text"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// messageSendParams is the params object for the "message/send" method.
type messageSendParams struct {
	Message struct {
		Role      string            `json:"role"`
		Parts     []rpcIncomingPart `json:"parts"`
		MessageID string            `json:"messageId"`
		ContextID string            `json:"contextId"`
	} `json:"message"`
}

// rpcIncomingPart handles both the spec-correct {"text":"..."} and the
// legacy {"kind":"text","text":"..."} wire formats from callers.
type rpcIncomingPart struct {
	Kind string `json:"kind"` // legacy; ignored for identity
	Text string `json:"text"`
}

// ── Agent card ────────────────────────────────────────────────────────────────

// agentCard is the A2A well-known agent card.
type agentCard struct {
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	URL          string              `json:"url"`
	Version      string              `json:"version"`
	Capabilities agentCardCapability `json:"capabilities"`
}

type agentCardCapability struct {
	Streaming bool `json:"streaming"`
}

// streamEvent is a single A2A streaming event frame sent over SSE.
// The spec uses method "stream/event" with a params.event discriminated by "kind".
type streamEvent struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  streamEventParam `json:"params"`
}

type streamEventParam struct {
	Event streamEventBody `json:"event"`
}

type streamEventBody struct {
	Kind      string           `json:"kind"`
	TaskID    string           `json:"taskId,omitempty"`
	ContextID string           `json:"contextId,omitempty"`
	Role      string           `json:"role,omitempty"`
	Parts     []rpcTextPart    `json:"parts,omitempty"`
	Status    *rpcStreamStatus `json:"status,omitempty"`
}

type rpcStreamStatus struct {
	State string `json:"state"`
}

// ── Dependency interfaces ─────────────────────────────────────────────────────

// Authenticator validates bearer tokens. Implemented by auth.Cache.
type Authenticator = transport.Authenticator

// ── Server ────────────────────────────────────────────────────────────────────

// Server is the A2A JSON-RPC 2.0 server.
type Server struct {
	lc            *execution.Lifecycle
	bus           event.Bus
	authenticator Authenticator
	instanceID    string
	logger        *slog.Logger
	runStreamer    runstream.RedisStreamer
	publicURL     string // externally-reachable base URL, e.g. "https://example.com"
}

// NewServer creates a Server backed by the shared execution Lifecycle.
// lifecycle, bus, authenticator and logger are required.
// instanceID identifies this pod/replica in session records.
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

// WithRunStreamer attaches the Redis Streams reader used to deliver run events
// to the streaming client. Without it, message/stream falls back to the
// in-process bus (only works when the orchestrator runs in the same process).
func (s *Server) WithRunStreamer(rc runstream.RedisStreamer) *Server {
	s.runStreamer = rc
	return s
}

// WithPublicURL sets the externally-reachable base URL used in the agent card
// (e.g. "https://example.com"). When empty (the default), the handler derives
// the base URL from the incoming request's scheme and Host header.
func (s *Server) WithPublicURL(u string) *Server {
	s.publicURL = strings.TrimRight(u, "/")
	return s
}

// Routes returns an http.Handler with A2A routes mounted.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/a2a/{app_slug}", s.handleRPC)
	r.Get("/.well-known/agent.json", s.handleAgentCard)
	return r
}

// handleAgentCard serves GET /.well-known/agent.json.
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	base := s.publicURL
	if base == "" {
		// Derive from the request: honour X-Forwarded-Proto set by Traefik.
		scheme := r.Header.Get("X-Forwarded-Proto")
		if scheme == "" {
			scheme = "http"
		}
		host := r.Host
		if host == "" {
			host = "localhost"
		}
		base = scheme + "://" + host
	}
	card := agentCard{
		Name:        "the-M Orchestrator",
		Description: "AI orchestration platform",
		URL:         fmt.Sprintf("%s/a2a/{app_slug}", base),
		Version:     "1.0",
		Capabilities: agentCardCapability{
			Streaming: true,
		},
	}
	writeJSON(w, http.StatusOK, card)
}

// handleRPC handles POST /a2a/{app_slug}.
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		writeRPCError(w, nil, codeParseError, "parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, codeParseError, "invalid jsonrpc version")
		return
	}

	switch req.Method {
	case "message/send":
		s.handleMessageSend(w, r, req)
	case "message/stream":
		s.handleMessageStream(w, r, req)
	default:
		writeRPCError(w, req.ID, codeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleMessageSend processes the "message/send" RPC method using the shared
// execution Lifecycle: Admit → bus.Subscribe → Start → wfRun.Get → respond.
// TenantID and ApplicationID come only from EPConfig (inside Lifecycle.Admit).
func (s *Server) handleMessageSend(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	ctx := r.Context()
	appSlug := chi.URLParam(r, "app_slug")

	// ── 1. Extract raw token (Lifecycle.Admit owns all validation/enforcement) ─
	rawToken := s.extractRawToken(r)

	// ── 2. Parse A2A message params ───────────────────────────────────────────
	var params messageSendParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, codeParseError, "invalid params")
			return
		}
	}

	var userText string
	for _, part := range params.Message.Parts {
		if part.Text != "" {
			userText = part.Text
			break
		}
	}
	if userText == "" {
		writeRPCError(w, req.ID, codeInternalError, "no text content in message")
		return
	}

	// ── 3. Resolve tenant identity for EP config lookup ──────────────────────
	// Tenant comes from the bearer token. For public EPs (no token), use the
	// bootstrap tenant UUID (single-tenant deployment safe).
	tenantID := tenantctx.BootstrapTenantID
	if rawToken != "" && s.authenticator != nil {
		if ti, err := s.authenticator.Validate(ctx, rawToken); err == nil && ti.TenantID != "" {
			tenantID = ti.TenantID
		}
	}

	// ── 4. Admit: auth → EPConfig → access → gate → session → CreateRun ──────
	admitReq := execution.ExecutionRequest{
		EPSlug:      appSlug,
		TenantID:    tenantID,
		RawToken:    rawToken,
		ContextID:   params.Message.ContextID, // caller-supplied multi-turn ID; empty → generated
		InstanceID:  s.instanceID,
		UserMessage: domain.TextMessage(domain.RoleUser, userText),
	}
	h, err := s.lc.Admit(ctx, admitReq)
	if err != nil {
		s.mapAdmitError(w, req.ID, err)
		return
	}
	defer s.lc.Release(h)

	s.logger.Info("a2a: session started",
		"app_slug", appSlug,
		"app_id", h.EPConfig.AppID,
		"tenant_id", h.EPConfig.TenantID,
		"session_id", h.SessionID,
		"run_id", h.RunID,
	)

	// ── 4. Subscribe BEFORE Start (bootstrap ordering invariant) ──────────────
	_, _, unsub := s.bus.Subscribe(ctx, h.ContextID, 256)
	defer unsub()

	// ── 5. Resolve orchestrator name from EP binding (SEC-04) ────────────────
	// OrchestratorName comes from entry_points.app_orchestrator_id →
	// app_orchestrators.name, resolved by epconfig at admission time.
	// An unbound EP is a configuration error — we do not fall back to appSlug.
	orchName := h.EPConfig.OrchestratorName
	if orchName == "" {
		s.logger.Warn("a2a: entry point has no orchestrator bound",
			"app_slug", appSlug,
			"app_id", h.EPConfig.AppID,
		)
		writeHTTPError(w, req.ID, http.StatusServiceUnavailable, "entry point has no orchestrator configured")
		return
	}

	// ── 6. Start Temporal workflow ────────────────────────────────────────────
	// RunID, ContextID, TenantID, ApplicationID, EntryPointSlug are set by Start
	// from the handle — caller-supplied values in input are overwritten.
	input := temporal.WorkflowInput{
		OrchestratorName:  orchName,
		AppOrchestratorID: h.EPConfig.AppOrchestratorID,
		UserMessage:       domain.TextMessage(domain.RoleUser, userText),
	}
	wfRun, startErr := s.lc.Start(ctx, h, input)
	if startErr != nil {
		s.logger.Warn("a2a: start workflow failed", "run_id", h.RunID, "error", startErr)
		writeHTTPError(w, req.ID, http.StatusServiceUnavailable, "orchestration service unavailable")
		return
	}

	s.logger.Info("a2a: temporal workflow started",
		"app_slug", appSlug,
		"run_id", h.RunID,
		"workflow_id", wfRun.GetID(),
	)

	// ── 6. Block until workflow completes ─────────────────────────────────────
	var wfResult temporal.WorkflowResult
	if err := wfRun.Get(ctx, &wfResult); err != nil {
		s.logger.Warn("a2a: temporal workflow error", "run_id", h.RunID, "error", err)
		writeHTTPError(w, req.ID, http.StatusInternalServerError, "internal error")
		return
	}

	// ── 7. Map result to A2A response ─────────────────────────────────────────
	if wfResult.Status == domain.RunStatusFailed {
		writeHTTPError(w, req.ID, http.StatusInternalServerError, "internal error")
		return
	}

	result := rpcResult{
		TaskID: h.RunID,
		Status: rpcStatus{State: "completed"},
		Artifacts: []rpcArtifact{
			{
				Parts: []rpcTextPart{
					{Text: wfResult.FinalText},
				},
			},
		},
	}
	writeRPCResult(w, req.ID, result)

	s.logger.Info("a2a: session completed",
		"app_slug", appSlug,
		"run_id", h.RunID,
		"session_id", h.SessionID,
	)
}

// handleMessageStream processes the "message/stream" RPC method.
// It follows the same Admit→Subscribe→Start pipeline as handleMessageSend but
// responds with text/event-stream (SSE) and emits incremental A2A events from
// the in-process event bus. The final event is always a task-status-update
// (completed or failed). The HTTP connection is held open until the workflow
// terminates or the client disconnects.
func (s *Server) handleMessageStream(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	ctx := r.Context()
	appSlug := chi.URLParam(r, "app_slug")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPCError(w, req.ID, codeInternalError, "streaming not supported by server")
		return
	}

	rawToken := s.extractRawToken(r)

	var params messageSendParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, codeParseError, "invalid params")
			return
		}
	}

	var userText string
	for _, part := range params.Message.Parts {
		if part.Text != "" {
			userText = part.Text
			break
		}
	}
	if userText == "" {
		writeRPCError(w, req.ID, codeInternalError, "no text content in message")
		return
	}

	tenantID := tenantctx.BootstrapTenantID
	if rawToken != "" && s.authenticator != nil {
		if ti, err := s.authenticator.Validate(ctx, rawToken); err == nil && ti.TenantID != "" {
			tenantID = ti.TenantID
		}
	}

	admitReq := execution.ExecutionRequest{
		EPSlug:      appSlug,
		TenantID:    tenantID,
		RawToken:    rawToken,
		ContextID:   params.Message.ContextID,
		InstanceID:  s.instanceID,
		UserMessage: domain.TextMessage(domain.RoleUser, userText),
	}
	h, err := s.lc.Admit(ctx, admitReq)
	if err != nil {
		s.mapAdmitError(w, req.ID, err)
		return
	}
	defer s.lc.Release(h)

	// All pre-stream errors above return clean HTTP responses.
	// After writing SSE headers, all errors become SSE terminal events.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "close")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeSSE := func(ev streamEvent) bool {
		data, err := json.Marshal(ev)
		if err != nil {
			return false
		}
		_, werr := fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return werr == nil
	}

	sendStatus := func(state string) {
		writeSSE(streamEvent{ //nolint:errcheck
			JSONRPC: "2.0",
			Method:  "stream/event",
			Params: streamEventParam{Event: streamEventBody{
				Kind:   "task-status-update",
				TaskID: h.RunID,
				Status: &rpcStreamStatus{State: state},
			}},
		})
	}

	orchName := h.EPConfig.OrchestratorName
	if orchName == "" {
		s.logger.Warn("a2a stream: entry point has no orchestrator bound",
			"app_slug", appSlug, "app_id", h.EPConfig.AppID)
		sendStatus("failed")
		return
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Subscribe to run events. Prefer the Redis run-stream (cross-process, used by
	// the SSE handler) because the Temporal activity runs in them-go-worker, a
	// separate process whose in-process bus events never reach this handler.
	// Fall back to the in-process bus only when no runStreamer is configured (tests).
	var evCh <-chan event.Event
	if s.runStreamer != nil {
		ch, rsErr := runstream.StreamFromRedis(streamCtx, s.runStreamer, h.RunID, runstream.StreamerOptions{})
		if rsErr != nil {
			s.logger.Warn("a2a stream: runstream subscribe failed", "run_id", h.RunID, "error", rsErr)
			sendStatus("failed")
			return
		}
		evCh = ch
	} else {
		// In-process bus fallback (unit tests / same-process orchestrator).
		busEvCh, busTermCh, unsub := s.bus.Subscribe(streamCtx, h.ContextID, 256)
		defer unsub()
		merged := make(chan event.Event, 256)
		go func() {
			defer close(merged)
			for {
				select {
				case ev, ok := <-busEvCh:
					if !ok {
						return
					}
					merged <- ev
				case ev, ok := <-busTermCh:
					if !ok {
						return
					}
					merged <- ev
				case <-streamCtx.Done():
					return
				}
			}
		}()
		evCh = merged
	}

	input := temporal.WorkflowInput{
		OrchestratorName:  orchName,
		AppOrchestratorID: h.EPConfig.AppOrchestratorID,
		UserMessage:       domain.TextMessage(domain.RoleUser, userText),
	}
	wfRun, startErr := s.lc.Start(ctx, h, input)
	if startErr != nil {
		s.logger.Warn("a2a stream: start workflow failed", "run_id", h.RunID, "error", startErr)
		sendStatus("failed")
		return
	}

	s.logger.Info("a2a stream: workflow started",
		"app_slug", appSlug, "run_id", h.RunID, "workflow_id", wfRun.GetID())

	// Emit run-started so clients can capture run_id/context_id for dashboard subscription.
	writeSSE(streamEvent{ //nolint:errcheck
		JSONRPC: "2.0",
		Method:  "stream/event",
		Params: streamEventParam{Event: streamEventBody{
			Kind:      "run-started",
			TaskID:    h.RunID,
			ContextID: h.ContextID,
		}},
	})

	// Reap the workflow in the background to release Temporal resources.
	// The terminal signal comes from the run-stream "done"/"error" event.
	go func() {
		if err := wfRun.Get(streamCtx, nil); err != nil {
			s.logger.Warn("a2a stream: workflow error", "run_id", h.RunID, "error", err)
		}
	}()

	// Drain run-stream events, translating them to A2A SSE frames.
	for {
		select {
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			switch ev.Type {
			case "token":
				var content string
				var p map[string]json.RawMessage
				if json.Unmarshal(ev.Payload, &p) == nil {
					json.Unmarshal(p["content"], &content) //nolint:errcheck
				}
				if !writeSSE(streamEvent{
					JSONRPC: "2.0",
					Method:  "stream/event",
					Params: streamEventParam{Event: streamEventBody{
						Kind:  "message-delta",
						Role:  "assistant",
						Parts: []rpcTextPart{{Text: content}},
					}},
				}) {
					return
				}
			case "done":
				sendStatus("completed")
				return
			case "error":
				sendStatus("failed")
				return
			}
		case <-streamCtx.Done():
			return
		}
	}
}

// extractRawToken reads the bearer token string from the Authorization header
// or ?token= query param. It does NOT validate — Lifecycle.Admit owns enforcement.
func (s *Server) extractRawToken(r *http.Request) string {
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		return strings.TrimPrefix(hdr, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// mapAdmitError converts a *execution.AdmitError to an A2A HTTP+JSON-RPC response.
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

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result rpcResult) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		Result:  &result,
		ID:      id,
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeRPCError writes a JSON-RPC error as HTTP 200 (protocol-level error).
func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		Error:   &rpcError{Code: code, Message: message},
		ID:      id,
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeHTTPError writes an HTTP-level error with a JSON-RPC error body.
// Used for auth, access, gate, and infrastructure failures where the HTTP
// status code communicates the failure class to the caller.
func writeHTTPError(w http.ResponseWriter, id json.RawMessage, httpStatus int, message string) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		Error:   &rpcError{Code: codeInternalError, Message: message},
		ID:      id,
	}
	writeJSON(w, httpStatus, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
