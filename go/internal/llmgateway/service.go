package llmgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/llmresolve"
	"github.com/aviciot/them/internal/quota"
)

// ── Request / response types (OpenAI wire format) ────────────────────────────

// ChatRequest is the OpenAI /v1/chat/completions request body.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
}

// ChatMessage is a single message in the conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse is the non-streaming response body.
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
}

// ChatChoice is one completion choice.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage holds token counts in the OpenAI response shape.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// SSEChunk is one streamed SSE delta chunk.
type SSEChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []SSEChoice `json:"choices"`
}

// SSEChoice is the delta inside a streaming chunk.
type SSEChoice struct {
	Index        int      `json:"index"`
	Delta        SSEDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

// SSEDelta carries the incremental content.
type SSEDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ── Interfaces ────────────────────────────────────────────────────────────────

// QuotaChecker checks the per-tenant api_requests_per_minute limit.
// Nil interface = no limit enforced (tests and when quota is not wired).
type QuotaChecker interface {
	CheckAPIRPM(ctx context.Context, tenantID string) error
}

// ProviderFactory builds an llm.Provider for a given model and tenant.
// The production implementation uses llmresolve; tests inject a fake.
type ProviderFactory interface {
	// Build resolves the provider name and constructs an llm.Provider.
	// Returns the resolved provider name alongside the provider.
	Build(ctx context.Context, tenantID, model string, maxTokens int) (providerName string, p llm.Provider, err error)
}

// PolicyEnforcer loads gateway_policies for a tenant and sums monthly spend.
// Nil interface = no policy enforcement (tests and when not wired).
type PolicyEnforcer interface {
	LoadPolicy(ctx context.Context, tenantID string) (*Policy, error)
	SumMonthlySpend(ctx context.Context, tenantID string) (float64, error)
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service orchestrates a single LLM gateway call:
//  1. Check gateway_policies: model allowlist, monthly budget (Phase 2).
//  2. Check api_requests_per_minute quota (fail-open on Redis error — §16.2).
//  3. Resolve provider and key via ProviderFactory.
//  4. Build the llm.Provider and stream/drain.
//  5. Return tokens and cost to the handler for deferred WriteRequest.
type Service struct {
	factory ProviderFactory
	dal     *DAL
	quota   QuotaChecker
	policy  PolicyEnforcer
	log     *slog.Logger
}

// NewService creates a Service. quotaChecker and policyEnforcer may be nil.
func NewService(factory ProviderFactory, dal *DAL, qc QuotaChecker, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{factory: factory, dal: dal, quota: qc, log: log}
}

// WithPolicyEnforcer attaches a PolicyEnforcer to enable Phase 2 governance.
func (s *Service) WithPolicyEnforcer(pe PolicyEnforcer) *Service {
	s.policy = pe
	return s
}

// CallResult carries the usage and cost from a completed (or cancelled) call.
type CallResult struct {
	Provider    string
	ModelServed string
	TokensIn    int
	TokensOut   int
	CostUSD     float64
	TTFBMs      int // 0 for non-streaming
}

// ErrQuotaExceeded is returned when the api_requests_per_minute limit is hit.
var ErrQuotaExceeded = errors.New("llmgateway: quota exceeded")

// ErrNoProvider is returned when no provider/key can be resolved for the model.
var ErrNoProvider = errors.New("llmgateway: no provider configured for model")

// ErrModelBlocked is returned when the resolved model is not in the tenant's
// allowed_models list. The caller returns HTTP 403.
var ErrModelBlocked = errors.New("llmgateway: model not permitted by policy")

// ErrBudgetExceeded is returned when the tenant's monthly_budget_usd is
// exhausted. The caller returns HTTP 429.
var ErrBudgetExceeded = errors.New("llmgateway: monthly budget exceeded")

// Call resolves the provider, checks policy and quota, calls the LLM
// (non-streaming), and returns the assembled text response along with
// usage/cost. The CallResult.ModelServed is the resolved (post-alias) model.
func (s *Service) Call(ctx context.Context, tenantID string, req ChatRequest) (string, CallResult, error) {
	resolvedModel, tokenCap, perr := s.checkPolicy(ctx, tenantID, req.Model)
	if perr != nil {
		return "", CallResult{}, perr
	}

	if err := s.checkQuota(ctx, tenantID); err != nil {
		return "", CallResult{}, ErrQuotaExceeded
	}

	effectiveMaxTokens := applyTokenCap(req.MaxTokens, tokenCap)

	provName, prov, err := s.factory.Build(ctx, tenantID, resolvedModel, effectiveMaxTokens)
	if err != nil {
		return "", CallResult{}, err
	}

	domainMsgs := toInternalMessages(req.Messages)
	opts := llm.Options{Model: resolvedModel, MaxTokens: effectiveMaxTokens}
	if req.Temperature != nil {
		opts.Temperature = *req.Temperature
	}

	ch, err := prov.Stream(ctx, domainMsgs, nil, opts)
	if err != nil {
		return "", CallResult{}, fmt.Errorf("llmgateway: stream start: %w", err)
	}

	var sb strings.Builder
	var usage llm.Usage
	for ev := range ch {
		switch ev.Type {
		case "text_delta":
			sb.WriteString(ev.Delta)
		case "stop":
			if ev.Usage != nil {
				usage = *ev.Usage
			}
		case "error":
			return "", CallResult{}, fmt.Errorf("llmgateway: provider error: %w", ev.Error)
		}
	}

	cr := CallResult{
		Provider:    provName,
		ModelServed: resolvedModel,
		TokensIn:    usage.InputTokens,
		TokensOut:   usage.OutputTokens,
		CostUSD:     estimateCost(provName, resolvedModel, usage.InputTokens, usage.OutputTokens),
	}
	return sb.String(), cr, nil
}

// StreamResult is returned by Stream so the handler can emit SSE chunks and
// record usage in a defer.
type StreamResult struct {
	Provider    string
	ModelServed string
	// Events is a channel of StreamEvent. The handler drains it and emits SSE.
	Events <-chan GatewayEvent
}

// GatewayEvent carries either a JSON-encoded SSE chunk or a sentinel.
type GatewayEvent struct {
	// JSON is the raw JSON for one SSEChunk (not prefixed with "data: ").
	// Empty when Done==true or Err!=nil.
	JSON []byte
	// Done signals the terminal [DONE] marker.
	Done bool
	// Err carries a provider or pipeline error.
	Err error
	// Usage is populated on the final event before Done, for cost recording.
	Usage *llm.Usage
	// TTFB is set (ms) on the first content event.
	TTFB int
}

// Stream resolves the provider, checks policy and quota, and returns a
// StreamResult whose Events channel the handler reads and re-emits as SSE.
func (s *Service) Stream(ctx context.Context, tenantID string, req ChatRequest) (StreamResult, CallResult, error) {
	resolvedModel, tokenCap, perr := s.checkPolicy(ctx, tenantID, req.Model)
	if perr != nil {
		return StreamResult{}, CallResult{}, perr
	}

	if err := s.checkQuota(ctx, tenantID); err != nil {
		return StreamResult{}, CallResult{}, ErrQuotaExceeded
	}

	effectiveMaxTokens := applyTokenCap(req.MaxTokens, tokenCap)

	provName, prov, err := s.factory.Build(ctx, tenantID, resolvedModel, effectiveMaxTokens)
	if err != nil {
		return StreamResult{}, CallResult{}, err
	}

	domainMsgs := toInternalMessages(req.Messages)
	opts := llm.Options{Model: resolvedModel, MaxTokens: effectiveMaxTokens}
	if req.Temperature != nil {
		opts.Temperature = *req.Temperature
	}

	provCh, err := prov.Stream(ctx, domainMsgs, nil, opts)
	if err != nil {
		return StreamResult{}, CallResult{}, fmt.Errorf("llmgateway: stream start: %w", err)
	}

	out := make(chan GatewayEvent, 64)
	callRes := CallResult{Provider: provName, ModelServed: resolvedModel}

	go func() {
		defer close(out)
		start := time.Now()
		ttfbSet := false
		now := time.Now().Unix()
		callID := fmt.Sprintf("gw-%d", now)

		for ev := range provCh {
			switch ev.Type {
			case "text_delta":
				ttfb := 0
				if !ttfbSet {
					ttfbSet = true
					ttfb = int(time.Since(start).Milliseconds())
					callRes.TTFBMs = ttfb
				}
				chunk := SSEChunk{
					ID:      callID,
					Object:  "chat.completion.chunk",
					Created: now,
					Model:   req.Model,
					Choices: []SSEChoice{{
						Index: 0,
						Delta: SSEDelta{Content: ev.Delta},
					}},
				}
				b, merr := json.Marshal(chunk)
				if merr != nil {
					out <- GatewayEvent{Err: merr}
					return
				}
				out <- GatewayEvent{JSON: b, TTFB: ttfb}

			case "stop":
				var u llm.Usage
				if ev.Usage != nil {
					u = *ev.Usage
					callRes.TokensIn = u.InputTokens
					callRes.TokensOut = u.OutputTokens
					callRes.CostUSD = estimateCost(provName, resolvedModel, u.InputTokens, u.OutputTokens)
				}
				finReason := "stop"
				if ev.StopReason != "" {
					finReason = ev.StopReason
				}
				chunk := SSEChunk{
					ID:      callID,
					Object:  "chat.completion.chunk",
					Created: now,
					Model:   req.Model,
					Choices: []SSEChoice{{
						Index:        0,
						Delta:        SSEDelta{},
						FinishReason: &finReason,
					}},
				}
				b, merr := json.Marshal(chunk)
				if merr == nil {
					out <- GatewayEvent{JSON: b, Usage: &u}
				}

			case "error":
				out <- GatewayEvent{Err: ev.Error}
				return
			}
		}
		out <- GatewayEvent{Done: true}
	}()

	return StreamResult{Provider: provName, ModelServed: resolvedModel, Events: out}, callRes, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// checkPolicy enforces gateway_policies for the tenant:
//  1. Applies model aliases (alias → concrete model).
//  2. Checks the resolved model against allowed_models (if non-empty).
//  3. Checks monthly spend against monthly_budget_usd (if > 0, fail-open on error).
//
// Returns the resolved model string (possibly aliased), the per-request token
// ceiling (0 = no cap), and any blocking error.
// Returns the original model unchanged when no policy is configured.
func (s *Service) checkPolicy(ctx context.Context, tenantID, requestedModel string) (resolvedModel string, tokenCap int, err error) {
	if s.policy == nil {
		return requestedModel, 0, nil
	}

	pol, lerr := s.policy.LoadPolicy(ctx, tenantID)
	if lerr != nil {
		// Fail-open on DB error — same principle as quota (§16.2).
		if s.log != nil {
			s.log.Warn("llmgateway: policy load error — fail-open", "err", lerr)
		}
		return requestedModel, 0, nil
	}
	if pol == nil {
		// No policy row → allow everything.
		return requestedModel, 0, nil
	}

	// Step 1: alias resolution (before allowlist check — §14 Phase 2).
	resolved := requestedModel
	if alias, ok := pol.ModelAliases[requestedModel]; ok && alias != "" {
		resolved = alias
	}

	// Step 2: allowlist check.
	if len(pol.AllowedModels) > 0 {
		allowed := false
		for _, m := range pol.AllowedModels {
			if m == resolved {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", 0, ErrModelBlocked
		}
	}

	// Step 3: monthly budget check (fail-open on DB error).
	if pol.MonthlyBudgetUSD > 0 {
		spent, serr := s.policy.SumMonthlySpend(ctx, tenantID)
		if serr != nil {
			if s.log != nil {
				s.log.Warn("llmgateway: monthly spend check error — fail-open", "err", serr)
			}
		} else if spent >= pol.MonthlyBudgetUSD {
			return "", 0, ErrBudgetExceeded
		}
	}

	return resolved, pol.MaxTokensPerRequest, nil
}

func (s *Service) checkQuota(ctx context.Context, tenantID string) error {
	if s.quota == nil {
		return nil
	}
	err := s.quota.CheckAPIRPM(ctx, tenantID)
	if errors.Is(err, quota.ErrAPIRateLimited) {
		return ErrQuotaExceeded
	}
	// Any other error (Redis down, etc.) → fail-open per §16.2.
	if err != nil && s.log != nil {
		s.log.Warn("llmgateway: quota check error — fail-open", "err", err)
	}
	return nil
}

// applyTokenCap clamps requested max_tokens to the policy ceiling.
// If either value is 0 (meaning "no limit / not set"), the other is used.
// If both are 0 the caller's default (4096) will be applied in Build.
func applyTokenCap(requested, cap int) int {
	if cap <= 0 {
		return requested // no policy ceiling
	}
	if requested <= 0 || requested > cap {
		return cap
	}
	return requested
}

// toInternalMessages converts the OpenAI ChatMessage list to domain.Message slice.
// System messages are promoted to "user" role.
func toInternalMessages(msgs []ChatMessage) []domain.Message {
	out := make([]domain.Message, 0, len(msgs))
	for _, m := range msgs {
		role := m.Role
		if role == "system" {
			role = domain.RoleUser
		}
		out = append(out, domain.Message{
			Role:  role,
			Parts: []domain.ContentPart{{Type: "text", Text: m.Content}},
		})
	}
	return out
}

// estimateCost returns USD cost for in+out tokens using a static rate card.
// Phase 2 will source pricing from them.llm_providers.model_pricing via Resolved.
func estimateCost(provider, model string, in, out int) float64 {
	type rate struct{ in, out float64 }
	rates := map[string]rate{
		"claude-sonnet-4-6":         {3.0 / 1e6, 15.0 / 1e6},
		"claude-haiku-4-5-20251001": {0.25 / 1e6, 1.25 / 1e6},
		"claude-opus-5":             {15.0 / 1e6, 75.0 / 1e6},
		"gpt-4o":                    {5.0 / 1e6, 15.0 / 1e6},
		"gpt-4o-mini":               {0.15 / 1e6, 0.6 / 1e6},
	}
	if r, ok := rates[model]; ok {
		return r.in*float64(in) + r.out*float64(out)
	}
	switch provider {
	case "anthropic":
		return (3.0/1e6)*float64(in) + (15.0/1e6)*float64(out)
	case "openai":
		return (5.0/1e6)*float64(in) + (15.0/1e6)*float64(out)
	}
	return 0
}

// modelToProvider infers the LLM provider name from the model string.
// Returns "" for unknown models (caller returns ErrNoProvider).
func modelToProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o1-"), strings.HasPrefix(model, "o3-"):
		return "openai"
	case strings.HasPrefix(model, "llama-"), strings.HasPrefix(model, "llama3"), strings.HasPrefix(model, "gemma"), strings.HasPrefix(model, "mixtral"):
		return "groq"
	case strings.HasPrefix(model, "ollama/") || isOllamaModel(model):
		return "ollama"
	default:
		return ""
	}
}

func isOllamaModel(model string) bool {
	for _, m := range []string{"llama3", "mistral", "phi3", "qwen", "deepseek", "codellama"} {
		if strings.HasPrefix(model, m) {
			return true
		}
	}
	return false
}

// ── Production ProviderFactory ─────────────────────────────────────────────────

// ResolverFactory is the production ProviderFactory backed by llmresolve.Resolver.
type ResolverFactory struct {
	resolver *llmresolve.Resolver
}

// NewResolverFactory creates a ResolverFactory.
func NewResolverFactory(r *llmresolve.Resolver) *ResolverFactory {
	return &ResolverFactory{resolver: r}
}

// Build resolves the provider and key, then constructs the llm.Provider.
func (f *ResolverFactory) Build(ctx context.Context, tenantID, model string, maxTokens int) (string, llm.Provider, error) {
	providerName := modelToProvider(model)
	if providerName == "" {
		return "", nil, fmt.Errorf("%w: %q", ErrNoProvider, model)
	}

	resolved, err := f.resolver.ResolveProvider(ctx, "", tenantID, providerName)
	if err != nil {
		return "", nil, fmt.Errorf("llmgateway: resolve provider %q: %w", providerName, err)
	}
	if resolved.Key == "" && providerName != "ollama" {
		return "", nil, fmt.Errorf("%w: no key configured for %q in tenant %s", ErrNoProvider, providerName, tenantID)
	}

	if maxTokens <= 0 {
		maxTokens = 4096
	}

	var prov llm.Provider
	switch providerName {
	case "anthropic":
		prov = llm.NewAnthropicProvider(resolved.Key, model, maxTokens)
	default:
		prov = llm.NewOpenAIProvider(resolved.Key, model, resolved.BaseURL, maxTokens)
	}

	return providerName, prov, nil
}
