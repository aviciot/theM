// Package a2a implements a JSON-RPC 2.0 A2A (Agent-to-Agent) server that
// exposes an orchestrator as an A2A agent. This is the "orchestrator-as-agent"
// pattern: external callers can invoke this platform as if it were an A2A agent.
//
// Routes:
//
//	POST /a2a/{app_slug}            — JSON-RPC 2.0 endpoint
//	GET  /.well-known/agent.json    — A2A agent card
//
// R-4e: inbound A2A now runs the same pipeline as WS/SSE:
//
//	tryAuthenticate → EPConfig load (from app_slug) → CheckAccess →
//	gate.Check → session.Register → gate.Confirm →
//	recorder.CreateRun → ExecuteWorkflow → block → result →
//	session.End + gate.Release → write A2A JSON-RPC response
//
// TenantID and ApplicationID come from EPConfig only; never from the request.
package a2a

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/transport"
)

// JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// newID generates a random 16-byte hex string suitable for IDs.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

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
		Role      string          `json:"role"`
		Parts     []rpcIncomingPart `json:"parts"`
		MessageID string          `json:"messageId"`
		ContextID string          `json:"contextId"`
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

// ── Dependencies interfaces ───────────────────────────────────────────────────

// Authenticator validates bearer tokens. Implemented by auth.Cache.
type Authenticator = transport.Authenticator

// EPConfigLoader resolves entry point and application runtime config.
type EPConfigLoader = transport.EPConfigLoader

// GateStore performs admission control. Implemented by gate.Gate.
type GateStore = transport.GateStore

// SessionStore manages session lifecycle in Redis. Implemented by session.Store.
type SessionStore = transport.SessionStore

// TemporalClientExecutor starts a Temporal workflow execution.
type TemporalClientExecutor = transport.TemporalClientExecutor

// ── Server ────────────────────────────────────────────────────────────────────

// Server is the A2A JSON-RPC 2.0 server.
type Server struct {
	recorder      *runrecorder.Recorder
	bus           event.Bus
	authenticator Authenticator
	epLoader      EPConfigLoader
	gateStore     GateStore
	sessions      SessionStore
	temporalCli   TemporalClientExecutor
	instanceID    string
	logger        *slog.Logger
}

// NewServer creates a Server with the full R-4e execution pipeline.
// recorder and bus are required. All others must be non-nil for production use;
// tests may pass nil to exercise partial paths.
func NewServer(
	recorder *runrecorder.Recorder,
	bus event.Bus,
	authenticator Authenticator,
	epLoader EPConfigLoader,
	gateStore GateStore,
	sessions SessionStore,
	temporalCli TemporalClientExecutor,
	instanceID string,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		recorder:      recorder,
		bus:           bus,
		authenticator: authenticator,
		epLoader:      epLoader,
		gateStore:     gateStore,
		sessions:      sessions,
		temporalCli:   temporalCli,
		instanceID:    instanceID,
		logger:        logger,
	}
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
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	card := agentCard{
		Name:        "the-M Orchestrator",
		Description: "AI orchestration platform",
		URL:         fmt.Sprintf("http://%s/a2a/{app_slug}", host),
		Version:     "1.0",
		Capabilities: agentCardCapability{
			Streaming: false,
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
	default:
		writeRPCError(w, req.ID, codeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleMessageSend processes the "message/send" RPC method using the full
// R-4e execution pipeline: auth → EPConfig → access → gate → session → run →
// Temporal → result. TenantID and ApplicationID come only from EPConfig.
func (s *Server) handleMessageSend(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	ctx := r.Context()
	appSlug := chi.URLParam(r, "app_slug")

	// ── 1. Non-enforcing token extraction ─────────────────────────────────────
	// Whether a token is required depends on EPConfig.AccessMode, resolved next.
	tokenInfo, rawToken, authed := s.tryAuthenticate(r)

	// ── 2. Resolve EPConfig from app_slug (trusted server-side binding) ───────
	// TenantID and ApplicationID come exclusively from this resolution;
	// they are never taken from the request payload, headers, or query params.
	if s.epLoader == nil {
		s.logger.Warn("a2a: eploader not configured", "app_slug", appSlug)
		writeRPCError(w, req.ID, codeInternalError, "internal error")
		return
	}
	resolvedCfg, loadErr := s.epLoader.Load(ctx, appSlug)
	if loadErr != nil {
		switch {
		case errors.Is(loadErr, epconfig.ErrNotFound):
			writeHTTPError(w, req.ID, http.StatusNotFound, "entry point not found")
		case errors.Is(loadErr, epconfig.ErrDBUnavailable):
			s.logger.Warn("a2a: epconfig db unavailable", "app_slug", appSlug, "error", loadErr)
			writeHTTPError(w, req.ID, http.StatusServiceUnavailable, "service unavailable")
		default:
			s.logger.Warn("a2a: epconfig load failed", "app_slug", appSlug, "error", loadErr)
			writeHTTPError(w, req.ID, http.StatusInternalServerError, "internal error")
		}
		return
	}

	// ── 3. Access mode enforcement ────────────────────────────────────────────
	isPublic := resolvedCfg.AccessMode == epconfig.AccessModePublic
	if !isPublic && !authed {
		writeHTTPError(w, req.ID, http.StatusUnauthorized, "unauthorized")
		return
	}

	// ── 4. CheckAccess — disabled / block-list checks ─────────────────────────
	th := ""
	if rawToken != "" {
		th = transport.TokenHash(rawToken)
	}
	var userID int64
	if tokenInfo != nil {
		userID = tokenInfo.TokenID
	}
	if accessErr := epconfig.CheckAccess(resolvedCfg, th, userID); accessErr != nil {
		switch {
		case errors.Is(accessErr, epconfig.ErrDisabled):
			writeHTTPError(w, req.ID, http.StatusForbidden, "entry point disabled")
		default:
			writeHTTPError(w, req.ID, http.StatusForbidden, "access denied")
		}
		return
	}

	// Ensure tokenInfo is non-nil for the rest of the handler.
	if tokenInfo == nil {
		tokenInfo = &auth.TokenInfo{}
	}

	// ── 5. Parse message params ───────────────────────────────────────────────
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

	// ── 6. Generate IDs ───────────────────────────────────────────────────────
	runID := newID()
	contextID := params.Message.ContextID
	if contextID == "" {
		contextID = newID()
	}
	sessionID := newID()

	// Capture appID and tenantID from EPConfig — never from request.
	appID := resolvedCfg.AppID
	tenantID := resolvedCfg.TenantID

	// ── 7. Gate.Check ─────────────────────────────────────────────────────────
	var gateCfg gate.Config
	var gateAdmitted bool

	if s.gateStore != nil {
		gateCfg = gate.Config{
			EPSlug:           appSlug,
			AppID:            appID,
			TokenHash:        th,
			SessionID:        sessionID,
			EPMaxConcurrent:  resolvedCfg.EPMaxConcurrent,
			AppMaxConcurrent: resolvedCfg.AppMaxConcurrent,
			RateLimitRPM:     resolvedCfg.RateLimitRPM,
			QueueTimeout:     resolvedCfg.QueueTimeout,
		}
		if _, err := s.gateStore.Check(ctx, gateCfg); err != nil {
			switch err {
			case gate.ErrCapExceeded:
				writeHTTPError(w, req.ID, http.StatusTooManyRequests, "session cap exceeded")
			case gate.ErrRateLimited:
				writeHTTPError(w, req.ID, http.StatusTooManyRequests, "rate limited")
			case gate.ErrQueueFull:
				writeHTTPError(w, req.ID, http.StatusServiceUnavailable, "queue full")
			default:
				s.logger.Warn("a2a: gate check failed", "app_slug", appSlug, "error", err)
				writeHTTPError(w, req.ID, http.StatusInternalServerError, "internal error")
			}
			return
		}
		gateAdmitted = true
	}

	// ── 8. Register session in Redis ──────────────────────────────────────────
	sessInfo := session.SessionInfo{
		SessionID:        sessionID,
		InstanceID:       s.instanceID,
		UserID:           tokenInfo.TokenID,
		OrchestratorName: appSlug,
		EPSlug:           appSlug,
		AppID:            appID,
		TenantID:         tenantID,
		ContextID:        contextID,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if s.sessions != nil {
		if err := s.sessions.Register(ctx, sessInfo); err != nil {
			s.logger.Warn("a2a: register session failed",
				"app_slug", appSlug,
				"app_id", appID,
				"error", err)
			if gateAdmitted {
				_ = s.gateStore.Rollback(context.Background(), gateCfg)
			}
			writeHTTPError(w, req.ID, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// ── 9. Gate.Confirm ───────────────────────────────────────────────────────
	if gateAdmitted {
		if err := s.gateStore.Confirm(ctx, gateCfg); err != nil {
			s.logger.Warn("a2a: gate confirm failed", "app_slug", appSlug, "error", err)
			// Non-fatal: shadow TTL (10s) provides safety net.
		}
	}

	// Cleanup: always release gate and end session on exit.
	defer func() {
		bgCtx := context.Background()
		if s.sessions != nil {
			_ = s.sessions.End(bgCtx, sessionID, appSlug, appID)
		}
		if gateAdmitted {
			_ = s.gateStore.Release(bgCtx, gateCfg)
		}
	}()

	s.logger.Info("a2a: session started",
		"app_slug", appSlug,
		"app_id", appID,
		"tenant_id", tenantID,
		"session_id", sessionID,
		"run_id", runID,
	)

	// ── 10. Subscribe to event bus BEFORE creating run and starting workflow ──
	_, _, unsub := s.bus.Subscribe(ctx, contextID, 256)
	defer unsub()

	// ── 11. Create run record ─────────────────────────────────────────────────
	// TenantID and ApplicationID from EPConfig; entry_point_slug from app_slug.
	run := domain.Run{
		ID:             runID,
		ContextID:      contextID,
		TenantID:       tenantID,
		ApplicationID:  appID,
		EntryPointSlug: appSlug,
		Status:         domain.RunStatusRunning,
		StartedAt:      time.Now().UTC(),
	}
	if err := s.recorder.CreateRun(ctx, run); err != nil {
		s.logger.Warn("a2a: create run failed", "run_id", runID, "error", err)
		// Non-fatal: run record failure should not block execution.
	}

	// ── 12. Start Temporal workflow ───────────────────────────────────────────
	if s.temporalCli == nil {
		s.logger.Warn("a2a: temporal client not configured", "app_slug", appSlug)
		writeHTTPError(w, req.ID, http.StatusServiceUnavailable, "orchestration service unavailable")
		return
	}

	userMsg := domain.TextMessage(domain.RoleUser, userText)

	// OrchestratorName uses appSlug, matching the WS/SSE convention.
	// TenantID and ApplicationID propagate from EPConfig — never from client.
	input := temporal.WorkflowInput{
		RunID:            runID,
		ContextID:        contextID,
		TenantID:         tenantID,
		ApplicationID:    appID,
		EntryPointSlug:   appSlug,
		OrchestratorName: appSlug,
		UserMessage:      userMsg,
	}

	wfOpts := temporalclient.StartWorkflowOptions{
		ID:        "ctx-" + contextID,
		TaskQueue: temporal.GoTaskQueue,
	}

	wfRun, wfErr := s.temporalCli.ExecuteWorkflow(ctx, wfOpts, temporal.WorkflowType, input)
	if wfErr != nil {
		s.logger.Warn("a2a: start temporal workflow failed", "run_id", runID, "error", wfErr)
		writeHTTPError(w, req.ID, http.StatusInternalServerError, "internal error")
		return
	}

	s.logger.Info("a2a: temporal workflow started",
		"app_slug", appSlug,
		"run_id", runID,
		"workflow_id", wfRun.GetID(),
	)

	// ── 13. Block until workflow completes ────────────────────────────────────
	var wfResult temporal.WorkflowResult
	if err := wfRun.Get(ctx, &wfResult); err != nil {
		s.logger.Warn("a2a: temporal workflow error", "run_id", runID, "error", err)
		writeHTTPError(w, req.ID, http.StatusInternalServerError, "internal error")
		return
	}

	// ── 14. Map result to A2A response ────────────────────────────────────────
	if wfResult.Status == domain.RunStatusFailed {
		writeHTTPError(w, req.ID, http.StatusInternalServerError, "internal error")
		return
	}

	result := rpcResult{
		TaskID: runID,
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
		"run_id", runID,
		"session_id", sessionID,
	)
}

// tryAuthenticate extracts and validates the bearer token from the request.
// Returns (tokenInfo, rawToken, ok). ok=false means no valid token was found.
// The caller enforces auth requirements based on EPConfig.AccessMode.
func (s *Server) tryAuthenticate(r *http.Request) (*auth.TokenInfo, string, bool) {
	if s.authenticator == nil {
		return nil, "", false
	}

	var rawToken string
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		rawToken = strings.TrimPrefix(hdr, "Bearer ")
	} else if t := r.URL.Query().Get("token"); t != "" {
		rawToken = t
	}
	if rawToken == "" {
		return nil, "", false
	}

	info, err := s.authenticator.Validate(r.Context(), rawToken)
	if err != nil {
		return nil, "", false
	}
	return info, rawToken, true
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
