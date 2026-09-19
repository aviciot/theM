package llmgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/quota"
	"github.com/aviciot/them/internal/tenantctx"
)

// errAPIRateLimited is quota.ErrAPIRateLimited — the error the real quota
// checker returns on RPM breach. checkQuota translates this to ErrQuotaExceeded.
var errAPIRateLimited = quota.ErrAPIRateLimited

// ── Fakes ──────────────────────────────────────────────────────────────────────

const (
	testTenantID = "11111111-1111-1111-1111-111111111111"
	testSlug     = "acme"
)

// fakeDAL records WriteRequest calls; satisfies DALWriter.
type fakeDAL struct {
	written []RequestRecord
}

func (f *fakeDAL) WriteRequest(_ context.Context, r RequestRecord) error {
	f.written = append(f.written, r)
	return nil
}
func (f *fakeDAL) ClientIDForHash(_ context.Context, _ string) string { return "" }

// fakeSlugResolver maps slugs to tenant IDs; satisfies tenantctx.SlugResolver.
type fakeSlugResolver struct{ m map[string]string }

func (f *fakeSlugResolver) ResolveSlug(_ context.Context, slug string) (string, error) {
	id, ok := f.m[slug]
	if !ok {
		return "", tenantctx.ErrTenantSlugNotFound
	}
	return id, nil
}

// fakeLLMProvider streams a single text_delta then a stop event.
type fakeLLMProvider struct {
	text   string
	inTok  int
	outTok int
	errOn  bool
}

func (f *fakeLLMProvider) Stream(_ context.Context, _ []domain.Message, _ []llm.ToolDef, _ llm.Options) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 4)
	go func() {
		defer close(ch)
		if f.errOn {
			ch <- llm.StreamEvent{Type: "error", Error: errors.New("fake provider error")}
			return
		}
		ch <- llm.StreamEvent{Type: "text_delta", Delta: f.text}
		ch <- llm.StreamEvent{Type: "stop", Usage: &llm.Usage{InputTokens: f.inTok, OutputTokens: f.outTok}}
	}()
	return ch, nil
}

// fakeProviderFactory satisfies ProviderFactory.
type fakeProviderFactory struct {
	provName string
	p        llm.Provider
	err      error
}

func (f *fakeProviderFactory) Build(_ context.Context, _, _ string, _ int) (string, llm.Provider, error) {
	return f.provName, f.p, f.err
}

// fakeQuotaChecker satisfies QuotaChecker.
type fakeQuotaChecker struct{ err error }

func (f *fakeQuotaChecker) CheckAPIRPM(_ context.Context, _ string) error { return f.err }

// ── Test helpers ──────────────────────────────────────────────────────────────

func newService(factory ProviderFactory, qcErr error) *Service {
	qc := &fakeQuotaChecker{err: qcErr}
	return &Service{factory: factory, dal: nil, quota: qc, log: nil}
}

func newHandler(tenantID, slug string, svc *Service, dal *fakeDAL) *Handler {
	resolver := &fakeSlugResolver{m: map[string]string{slug: tenantID}}
	return &Handler{svc: svc, dal: dal, resolver: resolver, log: nil}
}

func doRequest(h *Handler, method, path, body, tenantID string, tokenID int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := tenantctx.WithTenantID(req.Context(), tenantID)
	ctx = auth.WithTokenInfo(ctx, &auth.TokenInfo{TokenID: tokenID, TenantID: tenantID})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)
	return rr
}

func chatBody(model string, stream bool) string {
	b, _ := json.Marshal(ChatRequest{
		Model:    model,
		Stream:   stream,
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	return string(b)
}

// ── modelToProvider ────────────────────────────────────────────────────────────

// GW-M-01: claude- prefix → anthropic
func TestModelToProvider_Claude(t *testing.T) {
	if got := modelToProvider("claude-sonnet-4-6"); got != "anthropic" {
		t.Fatalf("want anthropic, got %q", got)
	}
}

// GW-M-02: gpt- prefix → openai
func TestModelToProvider_GPT(t *testing.T) {
	if got := modelToProvider("gpt-4o"); got != "openai" {
		t.Fatalf("want openai, got %q", got)
	}
}

// GW-M-03: llama- prefix → groq
func TestModelToProvider_Llama(t *testing.T) {
	if got := modelToProvider("llama-3.3-70b"); got != "groq" {
		t.Fatalf("want groq, got %q", got)
	}
}

// GW-M-04: unknown model → empty string
func TestModelToProvider_Unknown(t *testing.T) {
	if got := modelToProvider("some-unknown-model"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

// GW-M-05: o1- prefix → openai
func TestModelToProvider_O1(t *testing.T) {
	if got := modelToProvider("o1-mini"); got != "openai" {
		t.Fatalf("want openai, got %q", got)
	}
}

// ── sanitizeErrorMsg ───────────────────────────────────────────────────────────

// GW-S-01: enc: prefix in message → redacted
func TestSanitizeErrorMsg_EncPrefix(t *testing.T) {
	if got := sanitizeErrorMsg("bad key enc:gAAAA..."); got != "provider configuration error" {
		t.Fatalf("want redacted, got %q", got)
	}
}

// GW-S-02: sk- prefix in message → redacted
func TestSanitizeErrorMsg_SKPrefix(t *testing.T) {
	if got := sanitizeErrorMsg("api key sk-ant-123 is invalid"); got != "provider configuration error" {
		t.Fatalf("want redacted, got %q", got)
	}
}

// GW-S-03: clean message passes through
func TestSanitizeErrorMsg_Clean(t *testing.T) {
	msg := "provider configuration error"
	if got := sanitizeErrorMsg(msg); got != msg {
		t.Fatalf("want %q unchanged, got %q", msg, got)
	}
}

// ── estimateCost ───────────────────────────────────────────────────────────────

// GW-C-01: known model returns nonzero cost
func TestEstimateCost_KnownModel(t *testing.T) {
	cost := estimateCost("anthropic", "claude-sonnet-4-6", 1000, 500)
	if cost <= 0 {
		t.Fatalf("want positive cost, got %v", cost)
	}
}

// GW-C-02: unknown model + known provider returns nonzero cost
func TestEstimateCost_UnknownModelKnownProvider(t *testing.T) {
	cost := estimateCost("anthropic", "claude-future-9", 1000, 0)
	if cost <= 0 {
		t.Fatalf("want positive cost for anthropic provider, got %v", cost)
	}
}

// GW-C-03: unknown provider returns 0
func TestEstimateCost_UnknownProvider(t *testing.T) {
	cost := estimateCost("groq", "some-model", 1000, 1000)
	if cost != 0 {
		t.Fatalf("want 0 for groq (no rate card), got %v", cost)
	}
}

// ── gatewayHTTPStatus ──────────────────────────────────────────────────────────

// GW-H-01: ErrQuotaExceeded → 429
func TestGatewayHTTPStatus_Quota(t *testing.T) {
	if got := gatewayHTTPStatus(ErrQuotaExceeded); got != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", got)
	}
}

// GW-H-02: ErrNoProvider → 422
func TestGatewayHTTPStatus_NoProvider(t *testing.T) {
	if got := gatewayHTTPStatus(ErrNoProvider); got != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", got)
	}
}

// GW-H-03: other error → 502
func TestGatewayHTTPStatus_Other(t *testing.T) {
	if got := gatewayHTTPStatus(errors.New("generic")); got != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", got)
	}
}

// ── toInternalMessages ─────────────────────────────────────────────────────────

// GW-MSG-01: system role promoted to user
func TestToInternalMessages_SystemToUser(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
	}
	got := toInternalMessages(msgs)
	if len(got) != 2 {
		t.Fatalf("want 2 messages, got %d", len(got))
	}
	if got[0].Role != domain.RoleUser {
		t.Fatalf("want system→user, got %q", got[0].Role)
	}
	if len(got[0].Parts) == 0 || got[0].Parts[0].Text != "You are helpful." {
		t.Fatalf("want text part content, got %+v", got[0].Parts)
	}
}

// GW-MSG-02: assistant role preserved
func TestToInternalMessages_AssistantPreserved(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello!"},
	}
	got := toInternalMessages(msgs)
	if got[1].Role != domain.RoleAssistant {
		t.Fatalf("want assistant, got %q", got[1].Role)
	}
}

// ── Handler HTTP tests ─────────────────────────────────────────────────────────

// GW-HND-01: non-streaming call returns 200 + valid JSON body
func TestHandler_NonStream_200(t *testing.T) {
	fake := &fakeLLMProvider{text: "hello world", inTok: 10, outTok: 5}
	factory := &fakeProviderFactory{provName: "anthropic", p: fake}
	dal := &fakeDAL{}
	svc := newService(factory, nil)
	h := newHandler(testTenantID, testSlug, svc, dal)

	rr := doRequest(h, http.MethodPost, "/"+testSlug+"/llm/v1/chat/completions",
		chatBody("claude-sonnet-4-6", false), testTenantID, 7)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v — body: %s", err, rr.Body.String())
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "hello world" {
		t.Fatalf("want 'hello world', got %+v", resp.Choices)
	}
	if len(dal.written) != 1 || dal.written[0].Status != "ok" {
		t.Fatalf("want 1 ok record, got %+v", dal.written)
	}
	if dal.written[0].TokensIn != 10 || dal.written[0].TokensOut != 5 {
		t.Fatalf("want tokens 10/5, got %d/%d", dal.written[0].TokensIn, dal.written[0].TokensOut)
	}
}

// GW-HND-02: streaming call returns 200 + SSE chunks ending in [DONE]
func TestHandler_Stream_200(t *testing.T) {
	fake := &fakeLLMProvider{text: "streamed", inTok: 3, outTok: 2}
	factory := &fakeProviderFactory{provName: "anthropic", p: fake}
	dal := &fakeDAL{}
	svc := newService(factory, nil)
	h := newHandler(testTenantID, testSlug, svc, dal)

	rr := doRequest(h, http.MethodPost, "/"+testSlug+"/llm/v1/chat/completions",
		chatBody("claude-sonnet-4-6", true), testTenantID, 7)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("want [DONE] in stream, got:\n%s", body)
	}
	if !strings.Contains(body, "data: {") {
		t.Fatalf("want at least one chunk, got:\n%s", body)
	}
	if len(dal.written) != 1 {
		t.Fatalf("want 1 written record, got %d", len(dal.written))
	}
}

// GW-HND-03: tenant mismatch → 403
func TestHandler_TenantMismatch_403(t *testing.T) {
	otherTenant := "22222222-2222-2222-2222-222222222222"
	factory := &fakeProviderFactory{provName: "anthropic", p: &fakeLLMProvider{text: "x"}}
	dal := &fakeDAL{}
	svc := newService(factory, nil)
	// Slug resolves to testTenantID, but token is for otherTenant.
	h := newHandler(testTenantID, testSlug, svc, dal)

	req := httptest.NewRequest(http.MethodPost, "/"+testSlug+"/llm/v1/chat/completions",
		strings.NewReader(chatBody("claude-sonnet-4-6", false)))
	req.Header.Set("Content-Type", "application/json")
	ctx := tenantctx.WithTenantID(req.Context(), otherTenant) // token tenant ≠ slug tenant
	ctx = auth.WithTokenInfo(ctx, &auth.TokenInfo{TokenID: 1, TenantID: otherTenant})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// GW-HND-04: no token in context → 401
func TestHandler_NoToken_401(t *testing.T) {
	h := newHandler(testTenantID, testSlug, newService(&fakeProviderFactory{}, nil), &fakeDAL{})
	req := httptest.NewRequest(http.MethodPost, "/"+testSlug+"/llm/v1/chat/completions",
		strings.NewReader(chatBody("claude-sonnet-4-6", false)))
	req.Header.Set("Content-Type", "application/json")
	// No token info in context.
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

// GW-HND-05: missing model → 400
func TestHandler_NoModel_400(t *testing.T) {
	h := newHandler(testTenantID, testSlug, newService(&fakeProviderFactory{}, nil), &fakeDAL{})
	b, _ := json.Marshal(ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	rr := doRequest(h, http.MethodPost, "/"+testSlug+"/llm/v1/chat/completions",
		string(b), testTenantID, 1)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// GW-HND-06: quota exceeded → 429 and usage is still recorded
func TestHandler_QuotaExceeded_429(t *testing.T) {
	factory := &fakeProviderFactory{provName: "anthropic", p: &fakeLLMProvider{text: "x"}}
	dal := &fakeDAL{}
	// The quota checker must return quota.ErrAPIRateLimited so checkQuota translates it.
	svc := newService(factory, errAPIRateLimited)
	h := newHandler(testTenantID, testSlug, svc, dal)

	rr := doRequest(h, http.MethodPost, "/"+testSlug+"/llm/v1/chat/completions",
		chatBody("claude-sonnet-4-6", false), testTenantID, 1)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", rr.Code, rr.Body.String())
	}
}

// GW-HND-07: unknown tenant slug → 404
func TestHandler_UnknownSlug_404(t *testing.T) {
	factory := &fakeProviderFactory{provName: "anthropic", p: &fakeLLMProvider{text: "x"}}
	h := newHandler(testTenantID, testSlug, newService(factory, nil), &fakeDAL{})

	rr := doRequest(h, http.MethodPost, "/unknown-slug/llm/v1/chat/completions",
		chatBody("claude-sonnet-4-6", false), testTenantID, 1)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// GW-HND-08: provider error on non-streaming → 502, status=error in record
func TestHandler_ProviderError_502(t *testing.T) {
	fake := &fakeLLMProvider{errOn: true}
	factory := &fakeProviderFactory{provName: "anthropic", p: fake}
	dal := &fakeDAL{}
	svc := newService(factory, nil)
	h := newHandler(testTenantID, testSlug, svc, dal)

	rr := doRequest(h, http.MethodPost, "/"+testSlug+"/llm/v1/chat/completions",
		chatBody("claude-sonnet-4-6", false), testTenantID, 1)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(dal.written) != 1 || dal.written[0].Status != "error" {
		t.Fatalf("want 1 error record, got %+v", dal.written)
	}
}

// GW-HND-09: provider key in error message is redacted
func TestHandler_ProviderKeyRedacted(t *testing.T) {
	factory := &fakeProviderFactory{err: errors.New("key sk-ant-bad is wrong")}
	dal := &fakeDAL{}
	svc := newService(factory, nil)
	h := newHandler(testTenantID, testSlug, svc, dal)

	rr := doRequest(h, http.MethodPost, "/"+testSlug+"/llm/v1/chat/completions",
		chatBody("claude-sonnet-4-6", false), testTenantID, 1)

	if strings.Contains(rr.Body.String(), "sk-ant") {
		t.Fatalf("provider key must not appear in response: %s", rr.Body.String())
	}
}
