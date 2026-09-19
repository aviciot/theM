package llmgateway

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

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/tenantctx"
)

// DALWriter abstracts the WriteRequest and client-lookup operations so tests
// can inject a fake without a live database pool.
type DALWriter interface {
	WriteRequest(ctx context.Context, r RequestRecord) error
	// ClientIDForHash returns the gateway_clients.id for a token hash, or "".
	ClientIDForHash(ctx context.Context, tokenHash string) string
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
}

// NewHandler creates a Handler. resolver may be nil (slug validation skipped).
func NewHandler(svc *Service, dal DALWriter, resolver tenantctx.SlugResolver, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, dal: dal, resolver: resolver, log: log}
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

	// ── 4. Look up gateway client by token hash (best-effort; Phase 3 UI) ───────
	// TokenInfo.TokenID is actually the user_id from access_tokens, not a hash.
	// The hash is computed upstream in the auth cache but not surfaced here.
	// For Phase 1 we record requests without correlating to a named client row;
	// Phase 3 will expose a token_hash in TokenInfo (or look up by tenant+token).
	clientID := "" // populated by Phase 3 admin UI when the agent registers

	rec := RequestRecord{
		TenantID:       tenantID,
		ClientID:       clientID,
		ModelRequested: req.Model,
		Status:         "ok",
		Streamed:       req.Stream,
	}
	_ = tokenInfo // TokenID is user_id; kept in scope for future extension

	if req.Stream {
		h.handleStream(w, r, start, req, &rec)
	} else {
		h.handleSync(w, r, start, req, &rec)
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
