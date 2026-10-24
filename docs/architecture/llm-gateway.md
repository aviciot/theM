---
title: LLM Gateway, Governance & Provider Resolution
type: architecture
status: current
owns:
  - go/internal/llmgateway/
  - go/internal/governance/
  - go/internal/llmresolve/
  - go/internal/llm/
  - go/internal/admin/gateway.go
  - go/internal/admin/policy_cache.go
last_verified: 2026-10-24   # code read + live read-only catalog queries; no gateway request sent
---

# LLM Gateway, Governance & Provider Resolution

## Purpose
`POST /{tenant_slug}/llm/v1/chat/completions` lets "closed" callers (scripts, cron jobs, services) use an
LLM through the-M with an OpenAI-shaped API. The gateway authenticates the caller, applies governance
(model allow-list, aliases, token cap, monthly USD budget), resolves the tenant's provider key, calls the
model (or runs a canvas app), records usage, and returns an OpenAI-format answer.

## How it works

```
client ─ Authorization: Bearer <gateway token>  [X-User-JWT: <dashboard JWT>, optional]
  │  Traefik /{tenant}/llm → bridge catch-all → server.MountGateway
  ▼
OptionalJWTMiddleware  (valid X-User-JWT → claims in ctx; invalid → ignored)
GatewayClientMiddleware (sha256(token) → gateway_clients row → client_id, tenant_id;
                         config_status must be "valid", else 403)
  ▼
Handler.chatCompletions
  1 path slug must resolve to the token's tenant (403 on mismatch)
  2 decode body (model required)
  3 per-user rate / daily-cost limits if X-User-JWT present (cost from gateway_requests; fail-open)
  4 client has app_id + enabled `gateway` EP?  ── yes ─► profile-app path (AppFlow)
                                             └─ no ──► direct path
```

### Direct path (`service.go`)
`checkPolicy` → `checkQuota` → `ResolverFactory.Build` → provider `Stream` (always streamed internally; sync mode drains it):
1. Policy: load + resolve client policy; apply alias (`requested → resolved`), check `ModelAllowed(resolved)` (403 `model_not_permitted`), monthly budget vs tenant spend (429). DB errors fail open.
2. Quota: tenant `api_requests_per_minute` (`quota.CheckAPIRPM`), fail-open on Redis error.
3. Token cap: `max_tokens = min(request, policy cap)`; default 4096.
4. Provider: `modelToProvider(model)` by prefix (`claude-`→anthropic, `gpt-|o1-|o3-`→openai, `llama|gemma|mixtral`→groq, ollama names, `mock-`→mock; unknown → 422). Key via `llmresolve.ResolveProvider`: app key → tenant `llm_providers` → tenant `llm_provider_keys`. **Platform keys are never a fallback**; tenants pay with their own keys. `base_url` from the tenant row selects OpenAI-compatible servers.
5. Cost: `estimateCost` (static rate card). Usage is written in a `defer`.

### Profile-app path (`handler.go` `dispatchAppFlow*`)
Runs an App Canvas flow instead of calling a provider directly: `LoadPolicy` (error → 500, fails closed), monthly budget check, `AdmitDebug` (real run row, **no gate/session/quota**), compile the app's `gateway` entry point, `StartAppFlow` (non-debug) with `GatewayMessages` (full chat history) and `PolicyOverrideLLM{provider, model, max_tokens}` which forces **all** LLM nodes to that model. It blocks on the workflow result; "streaming" is the whole answer as one SSE chunk + `[DONE]`. Failed/rejected workflow → 502.

### Governance (`governance/`, `llmgateway/policy_gov.go`)
Policy doc (`governance_policies.doc`, schema_version 1): `limits.models.allow` (trailing `*` prefix match) and `aliases`, `limits.per_request.max_tokens`, `limits.budgets[]` (metric usd|tokens|requests, period minute..month, `on_exceed`), `warn_at`, `logging{level, sample_rate, capture_*, retention_days}`. Scopes: tenant, application, client, user (`profile` is treated as client scope). `Resolve` merges: limits = every scope applies, tightest wins; logging = most specific wins per field. `loadPolicyFromDB` loads the tenant row, the client's `governance_profile_id` row and the client's own row. **What runtime enforces today:** model allow-lists (all scopes), aliases, max tokens, and one budget — monthly USD with `block`. Everything else in the doc is stored but not enforced (see gaps). Cached in Redis `them:gateway:policy:v2:{clientID}` for 15 min; admin policy writes evict it (`admin/policy_cache.go`).

### Recording
`DAL.WriteRequest` inserts `them.gateway_requests` (Admin pool) and/or emits a `gateway_request` event, by `THE_M_EVENTS_GATEWAY` (`postgres` default, `dual`; `clickhouse` is blocked at startup — see `events-pipeline.md`). At logging level `debug`/`audit` the request/response text is also stored in `gateway_request_bodies`. Monthly spend = `SUM(cost_usd)` of the tenant's rows this calendar month.

### Admin API (`admin/gateway.go`, tenant-admin scoped)
`/gateway/clients` CRUD (token returned once, only the hash is stored), `/gateway/profiles`, `/gateway/policy` (tenant default), `/gateway/policy-profiles` CRUD, `/gateway/requests` listing.

### Provider layer (`internal/llm/`)
`llm.Provider.Stream(ctx, messages, tools, opts)` returns a channel of `text_delta | tool_calls | stop | error` events with usage. Implementations: Anthropic, OpenAI-compatible (also Groq/Ollama/vLLM via base URL), Mock. Cancelling the context cancels the HTTP call.

## Key invariants
- Tenant comes from the gateway token's row, never from the URL (the slug is only cross-checked).
- No secret (provider key, ciphertext) may reach a client: `sanitizeErrorMsg` replaces errors containing `enc:` / `sk-`.
- Policy always wins over the app/caller: model alias + allow-list + token cap are applied before any provider call (direct path).
- The Admin pool is used for gateway writes; isolation relies on explicit `tenant_id` columns/filters, not RLS.
- Tenants use their own provider keys; the platform key is not used on their behalf.

## Failure modes
| What fails | Effect |
|---|---|
| Policy DB error (direct path) | Fail-open: request proceeds without policy |
| Policy DB error (profile-app path) | 500 `policy_error` |
| Spend query error | Budget check skipped (fail-open) |
| Redis down | Policy cache miss (DB used); API-RPM check skipped |
| No key for provider | 422 `provider_not_configured` |
| Provider/workflow error | 502 `provider_error` |
| `gateway_requests` insert fails | Logged only; the caller still gets the answer; usage is not recorded |

## Known gaps (found while writing, 2026-10-24; code + live catalog; no gateway request was sent)
See `docs/plans/code-findings-backlog.md` F-42…F-55. Highlights:
1. **Schema mismatch:** `WriteRequest` inserts `user_id`, but the live `them.gateway_requests` has no such column (migration 125 not applied). On this database every usage write would fail.
2. **Profile-app path skips governance:** no model allow-list check, no API-RPM quota, no tenant run quotas or concurrency gate (it uses `AdmitDebug`). The caller's `model` string picks the provider.
3. **Usage lost on client disconnect:** the deferred `WriteRequest` uses `r.Context()`, which is already cancelled when the client goes away.
4. **Cost is a static rate card** with stale IDs (e.g. `claude-opus-5`); unknown Anthropic models are billed at Sonnet rates. DB `model_pricing` (`llmresolve`) is ignored, so budgets are inaccurate.
5. **Budget semantics:** one monthly USD budget only; it sums the whole tenant's spend (not per client); check-then-spend race; other budget metrics, `warn_at`, `sample_rate`, `capture_bodies`, `retention_days` are not enforced; `retain_until` is never purged.
6. **SSRF:** a tenant admin can set any `base_url` on a provider; the bridge/workers will call it. No validation.
7. OpenAI compatibility: `system` messages are rewritten to `user`; array content parts, `tools`, `n`, `stop`, `top_p` are unsupported (400 or silently dropped); no request-size cap.
8. `X-User-JWT` tenant is not checked against the client's tenant.
9. `gateway_clients` has no expiry/revoked/enabled flag; `last_seen` is never updated; no throttling on bad tokens.

## Where to change things
| To… | Edit |
|---|---|
| Add an enforced policy field | `governance/policy.go` (+ `Validate`), `llmgateway/policy_gov.go` (`policyFromRows`), enforcement in `service.go` **and** `handler.go` profile-app path |
| Add a provider / model prefix | `llm/`, `service.go` `modelToProvider` + `ResolverFactory.Build` |
| Change pricing | `service.go` `estimateCost` (should use `llmresolve` pricing) |
| Change key precedence | `llmresolve/llmresolve.go` |
| Add admin endpoints | `admin/gateway.go` (+ `dal/gateway*.go`, `policy_cache.go` for cache eviction) |

Tests: `go test ./internal/llmgateway/... ./internal/governance/... ./internal/llm/... ./internal/llmresolve/...` (+ integration for `llmresolve`); governance/admin: `./internal/admin/...`. Index in `go/TEST_INDEX.md`.

## Related
- `run-lifecycle.md`, `app-canvas-appflow.md` — `StartAppFlow` and `PolicyOverrideLLM`
- `events-pipeline.md` — `gateway_request` events and dual mode
- `multi-tenancy-rls.md`, `auth-and-roles.md`
- Old design notes: `docs/_archive/LLM_GATEWAY_DESIGN.md`, `GOVERNANCE_POLICY_DESIGN.md` (history only)
