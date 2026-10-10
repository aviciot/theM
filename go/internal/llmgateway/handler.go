package llmgateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/tenantctx"
)

// DALWriter abstracts the WriteRequest and client-lookup operations so tests
// can inject a fake without a live database pool.
type DALWriter interface {
	WriteRequest(ctx context.Context, r RequestRecord) error
	// ClientIDForHash returns the gateway_clients.id for a token hash, or "".
	ClientIDForHash(ctx context.Context, tokenHash string) string
	// LoadClientApp resolves the app canvas app linked to the client, or nil.
	LoadClientApp(ctx context.Context, tokenHash string) (*ClientApp, error)
}

// Handler implements POST /{tenant_slug}/llm/v1/chat/completions.
//
// Auth: BearerTenantMiddleware must run upstream — the handler reads TokenInfo
// and tenantID from context. The {tenant_slug} in the path is validated to
// match the token's tenant.
type Handler struct {
	svc      *Service
	dal      DALWriter
	resolver tenantctx.SlugResolver // may be nil (slug check skipped in tests)
	log      *slog.Logger

	// Optional — set via WithAppFlow to enable profile-app dispatch.
	bus event.Bus
	lc  *execution.Lifecycle
}

// NewHandler creates a Handler. resolver may be nil (slug validation skipped).
func NewHandler(svc *Service, dal DALWriter, resolver tenantctx.SlugResolver, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, dal: dal, resolver: resolver, log: log}
}

// WithAppFlow attaches the dependencies needed for profile-app (AppFlow) dispatch.
// When set and a client has an app_id, the gateway starts an AppFlowWorkflow
// instead of calling the LLM directly. The Lifecycle already holds the epLoader.
func (h *Handler) WithAppFlow(lc *execution.Lifecycle, bus event.Bus) *Handler {
	h.lc = lc
	h.bus = bus
	return h
}

// Routes returns the chi sub-router for gateway routes. Use for tests that
// want to exercise the full chi routing (slug extraction via URLParam).
// Production wiring uses ChatCompletionsHandler() instead.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/{tenant_slug}/llm/v1/chat/completions", h.chatCompletions)
	return r
}

// ChatCompletionsHandler returns the bare HTTP handler for
// POST /{tenant_slug}/llm/v1/chat/completions. The caller registers this on
// the server router at the exact path so chi resolves it before the /*
// catch-all (see server.MountGateway). BearerTenantMiddleware must wrap it.
func (h *Handler) ChatCompletionsHandler() http.HandlerFunc {
	return h.chatCompletions
}

func (h *Handler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// ── 1. Extract auth ────────────────────────────────────────────────────────
	tokenInfo, ok := auth.TokenInfoFromCtx(r.Context())
	if !ok {
		writeGatewayError(w, http.StatusUnauthorized, "unauthorized", "missing token info")
		return
	}
	tenantID, err := tenantctx.TenantIDFromCtx(r.Context())
	if err != nil {
		writeGatewayError(w, http.StatusForbidden, "forbidden", "token has no tenant")
		return
	}

	// ── 2. Validate path slug matches token tenant ──────────────────────────────
	slug := chi.URLParam(r, "tenant_slug")
	if slug != "" && h.resolver != nil {
		resolvedTenant, rerr := h.resolver.ResolveSlug(r.Context(), slug)
		if rerr != nil || resolvedTenant == "" {
			writeGatewayError(w, http.StatusNotFound, "not_found", "unknown tenant")
			return
		}
		if resolvedTenant != tenantID {
			writeGatewayError(w, http.StatusForbidden, "forbidden", "tenant mismatch")
			return
		}
	}

	// ── 3. Decode request ──────────────────────────────────────────────────────
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "invalid_request", "cannot decode body")
		return
	}
	if req.Model == "" {
		writeGatewayError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}

	// ── 4. Resolve gateway client by token hash ───────────────────────────────
	rawBearer := r.Header.Get("Authorization")
	if len(rawBearer) > 7 {
		rawBearer = rawBearer[7:] // strip "Bearer "
	} else {
		rawBearer = ""
	}
	sum := sha256.Sum256([]byte(rawBearer))
	tokenHashHex := fmt.Sprintf("%x", sum)

	clientID := h.dal.ClientIDForHash(r.Context(), tokenHashHex)

	rec := RequestRecord{
		TenantID:       tenantID,
		ClientID:       clientID,
		TokenHash:      tokenHashHex,
		ModelRequested: req.Model,
		Status:         "ok",
		Streamed:       req.Stream,
	}
	_ = tokenInfo

	// ── 5. If client has a profile app — dispatch via AppFlow ────────────────
	if h.lc != nil {
		ca, caErr := h.dal.LoadClientApp(r.Context(), tokenHashHex)
		if caErr == nil && ca != nil {
			if req.Stream {
				h.dispatchAppFlowStream(w, r, start, req, &rec, tenantID, ca)
			} else {
				h.dispatchAppFlowSync(w, r, start, req, &rec, tenantID, ca)
			}
			return
		}
	}

	if req.Stream {
		h.handleStream(w, r, start, req, &rec)
	} else {
		h.handleSync(w, r, start, req, &rec)
	}
}

// dispatchAppFlowSync runs the request through an App Canvas AppFlow workflow
// (non-streaming). Blocks until the workflow completes, then returns the result
// in OpenAI chat.completion JSON format.
func (h *Handler) dispatchAppFlowSync(w http.ResponseWriter, r *http.Request, start time.Time, req ChatRequest, rec *RequestRecord, tenantID string, ca *ClientApp) {
	defer func() {
		rec.LatencyMS = int(time.Since(start).Milliseconds())
		if werr := h.dal.WriteRequest(r.Context(), *rec); werr != nil {
			h.log.Warn("llmgateway: write_request (appflow sync) failed", "err", werr)
		}
	}()

	handle, wfRun, err := h.admitAndStartAppFlow(r.Context(), tenantID, ca, req)
	if err != nil {
		rec.Status = "error"
		rec.HTTPStatus = http.StatusBadGateway
		writeGatewayError(w, http.StatusBadGateway, "provider_error", "profile workflow failed to start")
		return
	}
	defer h.lc.Release(handle)

	var output appflow.AppFlowWorkflowOutput
	if wfErr := wfRun.Get(r.Context(), &output); wfErr != nil {
		rec.Status = "error"
		rec.HTTPStatus = http.StatusBadGateway
		writeGatewayError(w, http.StatusBadGateway, "provider_error", "profile workflow error")
		return
	}
	if output.Status == "failed" || output.Status == "rejected" {
		rec.Status = "error"
		rec.HTTPStatus = http.StatusBadGateway
		writeGatewayError(w, http.StatusBadGateway, "provider_error", "profile workflow returned "+output.Status)
		return
	}

	rec.HTTPStatus = http.StatusOK
	resp := ChatResponse{
		ID:      fmt.Sprintf("gw-af-%d", start.Unix()),
		Object:  "chat.completion",
		Created: start.Unix(),
		Model:   req.Model,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: output.FinalText},
			FinishReason: "stop",
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// dispatchAppFlowStream runs the request through an App Canvas AppFlow workflow,
// streaming tokens back in OpenAI SSE format (data: {...}\n\n / data: [DONE]\n\n).
func (h *Handler) dispatchAppFlowStream(w http.ResponseWriter, r *http.Request, start time.Time, req ChatRequest, rec *RequestRecord, tenantID string, ca *ClientApp) {
	defer func() {
		rec.LatencyMS = int(time.Since(start).Milliseconds())
		if werr := h.dal.WriteRequest(r.Context(), *rec); werr != nil {
			h.log.Warn("llmgateway: write_request (appflow stream) failed", "err", werr)
		}
	}()

	// Subscribe to event bus BEFORE starting workflow (ordering invariant).
	runID := uuid.New().String()
	evCh, termCh, unsub := h.bus.Subscribe(r.Context(), runID, 64)
	defer unsub()

	handle, wfRun, err := h.admitAndStartAppFlow(r.Context(), tenantID, ca, req)
	if err != nil {
		rec.Status = "error"
		rec.HTTPStatus = http.StatusBadGateway
		writeGatewayError(w, http.StatusBadGateway, "provider_error", "profile workflow failed to start")
		return
	}
	defer h.lc.Release(handle)
	_ = wfRun // streaming reads from event bus, not wfRun.Get

	rec.HTTPStatus = http.StatusOK
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	ttfbSet := false

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-termCh:
			if !ok {
				return
			}
			if ev.Type == "error" {
				rec.Status = "error"
				_, _ = fmt.Fprintf(w, "data: {\"error\":{\"message\":\"workflow error\"}}\n\n")
			} else {
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			}
			if canFlush {
				flusher.Flush()
			}
			return
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			switch ev.Type {
			case "done", "error":
				if ev.Type == "error" {
					rec.Status = "error"
					_, _ = fmt.Fprintf(w, "data: {\"error\":{\"message\":\"workflow error\"}}\n\n")
				} else {
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				}
				if canFlush {
					flusher.Flush()
				}
				return
			case "token":
				if !ttfbSet {
					rec.TTFBMs = int(time.Since(start).Milliseconds())
					ttfbSet = true
				}
				// Wrap payload as OpenAI streaming delta.
				var tokenText string
				_ = json.Unmarshal(ev.Payload, &tokenText)
				chunk := openAIStreamChunk(req.Model, tokenText, start)
				data, _ := json.Marshal(chunk)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				if canFlush {
					flusher.Flush()
				}
			}
		}
	}
}

// admitAndStartAppFlow resolves the EPConfig for the profile app, creates a run
// handle via AdmitDebug (no gate/session slot — gateway runs are internal),
// compiles the AppFlowSpec, and starts the Temporal workflow.
// Subscribe to the event bus BEFORE calling this for streaming paths.
func (h *Handler) admitAndStartAppFlow(ctx context.Context, tenantID string, ca *ClientApp, req ChatRequest) (*execution.ExecutionHandle, temporalclient.WorkflowRun, error) {
	handle, admitErr := h.lc.AdmitDebug(ctx, tenantID, ca.AppSlug, ca.EPSlug, 0)
	if admitErr != nil {
		return nil, nil, fmt.Errorf("llmgateway: admit debug: %w", admitErr)
	}

	defJSON := handle.EPConfig.ActiveDefinitionJSON
	if len(defJSON) == 0 {
		h.lc.Release(handle)
		return nil, nil, fmt.Errorf("llmgateway: no active definition on profile app")
	}

	agentByInstanceID, err := appflow.ResolveAgentByInstanceID(defJSON)
	if err != nil {
		h.lc.Release(handle)
		return nil, nil, fmt.Errorf("llmgateway: resolve agents: %w", err)
	}

	spec, err := appflow.Compile(defJSON, agentByInstanceID)
	if err != nil {
		h.lc.Release(handle)
		return nil, nil, fmt.Errorf("llmgateway: compile: %w", err)
	}

	// Find the gateway EPFlow.
	var epFlow *appflow.EPFlow
	for i := range spec.EntryPoints {
		if spec.EntryPoints[i].Slug == ca.EPSlug {
			epFlow = &spec.EntryPoints[i]
			break
		}
	}
	if epFlow == nil {
		h.lc.Release(handle)
		return nil, nil, fmt.Errorf("llmgateway: no EPFlow for slug %q", ca.EPSlug)
	}

	llmCfg := appflow.ParseLLMConfig(appflow.LLMOrchConfig{
		Provider: handle.EPConfig.OrchestratorLLMProvider,
		Model:    handle.EPConfig.OrchestratorLLMModel,
	})

	// Extract the last user message as the prompt.
	userMsg := lastUserMessage(req.Messages)

	input := appflow.AppFlowWorkflowInput{
		Spec:            &appflow.AppFlowSpec{ExecutionBackend: spec.ExecutionBackend, EntryPoints: []appflow.EPFlow{*epFlow}},
		UserMessage:     userMsg,
		LLMProviderName: llmCfg.ProviderName,
		LLMProvider:     llmCfg.ProviderName,
		LLMModel:        llmCfg.Model,
	}

	wfRun, startErr := h.lc.StartAppFlow(ctx, handle, input, false)
	if startErr != nil {
		h.lc.Release(handle)
		return nil, nil, fmt.Errorf("llmgateway: start appflow: %w", startErr)
	}
	return handle, wfRun, nil
}

// lastUserMessage returns the text of the last message with role "user", or
// the concatenation of all messages if none is found.
func lastUserMessage(msgs []ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content
	}
	return ""
}

// openAIStreamChunk wraps a token string in an OpenAI streaming response chunk.
func openAIStreamChunk(model, token string, start time.Time) map[string]any {
	return map[string]any{
		"id":      fmt.Sprintf("gw-af-%d", start.Unix()),
		"object":  "chat.completion.chunk",
		"created": start.Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{"role": "assistant", "content": token},
		}},
	}
}

func (h *Handler) handleSync(w http.ResponseWriter, r *http.Request, start time.Time, req ChatRequest, rec *RequestRecord) {
	defer func() {
		rec.LatencyMS = int(time.Since(start).Milliseconds())
		if werr := h.dal.WriteRequest(r.Context(), *rec); werr != nil {
			if h.log != nil {
				h.log.Warn("llmgateway: write_request failed", "err", werr)
			}
		}
	}()

	text, cr, callErr := h.svc.Call(r.Context(), rec.TenantID, req)
	if callErr != nil {
		rec.Status = callStatus(callErr)
		rec.HTTPStatus = gatewayHTTPStatus(callErr)
		writeGatewayError(w, rec.HTTPStatus, gatewayErrType(callErr), callErr.Error())
		return
	}

	rec.Provider = cr.Provider
	rec.ModelServed = cr.ModelServed
	rec.TokensIn = cr.TokensIn
	rec.TokensOut = cr.TokensOut
	rec.CostUSD = cr.CostUSD
	rec.HTTPStatus = http.StatusOK

	resp := ChatResponse{
		ID:      fmt.Sprintf("gw-%d", start.Unix()),
		Object:  "chat.completion",
		Created: start.Unix(),
		Model:   req.Model,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
		Usage: ChatUsage{
			PromptTokens:     cr.TokensIn,
			CompletionTokens: cr.TokensOut,
			TotalTokens:      cr.TokensIn + cr.TokensOut,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request, start time.Time, req ChatRequest, rec *RequestRecord) {
	defer func() {
		rec.LatencyMS = int(time.Since(start).Milliseconds())
		if werr := h.dal.WriteRequest(r.Context(), *rec); werr != nil {
			if h.log != nil {
				h.log.Warn("llmgateway: write_request (stream) failed", "err", werr)
			}
		}
	}()

	sr, _, streamErr := h.svc.Stream(r.Context(), rec.TenantID, req)
	if streamErr != nil {
		rec.Status = callStatus(streamErr)
		rec.HTTPStatus = gatewayHTTPStatus(streamErr)
		writeGatewayError(w, rec.HTTPStatus, gatewayErrType(streamErr), streamErr.Error())
		return
	}

	rec.Provider = sr.Provider
	rec.ModelServed = sr.ModelServed
	rec.HTTPStatus = http.StatusOK

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	for ev := range sr.Events {
		if ev.Err != nil {
			rec.Status = "error"
			_, _ = fmt.Fprintf(w, "data: {\"error\":{\"message\":%q}}\n\n", ev.Err.Error())
			if canFlush {
				flusher.Flush()
			}
			return
		}
		if ev.Done {
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			if canFlush {
				flusher.Flush()
			}
			return
		}
		if ev.Usage != nil {
			rec.TokensIn = ev.Usage.InputTokens
			rec.TokensOut = ev.Usage.OutputTokens
			rec.CostUSD = estimateCost(rec.Provider, req.Model, ev.Usage.InputTokens, ev.Usage.OutputTokens)
		}
		if ev.TTFB > 0 && rec.TTFBMs == 0 {
			rec.TTFBMs = ev.TTFB
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", ev.JSON)
		if canFlush {
			flusher.Flush()
		}
	}
}

// ── Error helpers ─────────────────────────────────────────────────────────────

type gatewayError struct {
	Error gatewayErrorBody `json:"error"`
}

type gatewayErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func writeGatewayError(w http.ResponseWriter, status int, errType, msg string) {
	safeMsg := sanitizeErrorMsg(msg)
	body := gatewayError{Error: gatewayErrorBody{Type: errType, Message: safeMsg}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func sanitizeErrorMsg(msg string) string {
	if strings.Contains(msg, "enc:") || strings.Contains(msg, "sk-") {
		return "provider configuration error"
	}
	return msg
}

// callStatus maps a service error to the status string stored in gateway_requests.
func callStatus(err error) string {
	switch {
	case errors.Is(err, ErrModelBlocked):
		return "blocked"
	case errors.Is(err, ErrQuotaExceeded), errors.Is(err, ErrBudgetExceeded):
		return "rate_limited"
	default:
		return "error"
	}
}

// gatewayErrType returns the JSON error.type string for the client response.
func gatewayErrType(err error) string {
	switch {
	case errors.Is(err, ErrModelBlocked):
		return "model_not_permitted"
	case errors.Is(err, ErrBudgetExceeded):
		return "budget_exceeded"
	case errors.Is(err, ErrQuotaExceeded):
		return "rate_limit_exceeded"
	case errors.Is(err, ErrNoProvider):
		return "provider_not_configured"
	default:
		return "provider_error"
	}
}

func gatewayHTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrQuotaExceeded), errors.Is(err, ErrBudgetExceeded):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrModelBlocked):
		return http.StatusForbidden
	case errors.Is(err, ErrNoProvider):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusBadGateway
	}
}
