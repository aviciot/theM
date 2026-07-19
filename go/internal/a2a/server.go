// Package a2a implements a JSON-RPC 2.0 A2A (Agent-to-Agent) server that
// exposes an orchestrator as an A2A agent. This is the "orchestrator-as-agent"
// pattern: external callers can invoke this platform as if it were an A2A agent.
//
// Routes:
//
//	POST /a2a/{app_slug}            — JSON-RPC 2.0 endpoint
//	GET  /.well-known/agent.json    — A2A agent card
package a2a

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
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
	Status    rpcStatus     `json:"status"`
	Artifacts []rpcArtifact `json:"artifacts"`
}

type rpcStatus struct {
	State string `json:"state"`
}

type rpcArtifact struct {
	Parts []rpcPart `json:"parts"`
}

type rpcPart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// messageSendParams is the params object for the "message/send" method.
type messageSendParams struct {
	Message struct {
		Role      string    `json:"role"`
		Parts     []rpcPart `json:"parts"`
		MessageID string    `json:"messageId"`
	} `json:"message"`
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

// ── Server ────────────────────────────────────────────────────────────────────

// Server is the A2A JSON-RPC 2.0 server.
type Server struct {
	recorder *runrecorder.Recorder
	orch     *orchestrator.Orchestrator
	bus      event.Bus
	logger   *slog.Logger
}

// NewServer creates a Server.
func NewServer(
	recorder *runrecorder.Recorder,
	orch *orchestrator.Orchestrator,
	bus event.Bus,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		recorder: recorder,
		orch:     orch,
		bus:      bus,
		logger:   logger,
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
	// Parse request body.
	var req rpcRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		writeRPCError(w, nil, codeParseError, "parse error: "+err.Error())
		return
	}

	// Validate JSON-RPC version.
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

// handleMessageSend processes the "message/send" RPC method.
// It runs orchestration synchronously and returns the final response.
func (s *Server) handleMessageSend(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var params messageSendParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, codeParseError, "invalid params: "+err.Error())
			return
		}
	}

	// Extract text from the first text part.
	var userText string
	for _, part := range params.Message.Parts {
		if part.Kind == "text" {
			userText = part.Text
			break
		}
	}
	if userText == "" {
		writeRPCError(w, req.ID, codeInternalError, "no text content in message")
		return
	}

	// Generate IDs.
	runID := newID()
	contextID := newID()

	// Subscribe to event bus BEFORE starting orchestration.
	evCh, unsub := s.bus.Subscribe(r.Context(), contextID, 256)
	defer unsub()

	// Create run record.
	run := domain.Run{
		ID:        runID,
		ContextID: contextID,
		Status:    domain.RunStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := s.recorder.CreateRun(r.Context(), run); err != nil {
		s.logger.Warn("a2a: create run failed", "run_id", runID, "error", err)
	}

	// Run orchestration.
	ctx := r.Context()
	userMsg := domain.TextMessage(domain.RoleUser, userText)

	finalText, runErr := s.orch.Run(ctx, runID, contextID, userMsg, nil)

	// Drain any remaining events (best-effort, non-blocking).
	drainEvents(evCh)

	if runErr != nil {
		s.logger.Warn("a2a: orchestrator error", "run_id", runID, "error", runErr)
		writeRPCError(w, req.ID, codeInternalError, "internal error")
		return
	}

	result := rpcResult{
		Status: rpcStatus{State: "completed"},
		Artifacts: []rpcArtifact{
			{
				Parts: []rpcPart{
					{Kind: "text", Text: finalText},
				},
			},
		},
	}
	writeRPCResult(w, req.ID, result)
}

// drainEvents discards any buffered events from the channel without blocking.
func drainEvents(ch <-chan event.Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
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

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		Error:   &rpcError{Code: code, Message: message},
		ID:      id,
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

