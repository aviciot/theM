# LLM Gateway — Design
# Status: DESIGN — not yet implemented
# Last updated: 2026-09-17

---

## 1. Background — why we need this

### The problem

the-M today governs agents it **hosts**. An agent is registered in `them.agents`, it has a non-null `endpoint_url`, and the-M calls it over A2A. Registration and invocability are the same thing: if the-M cannot call it, it cannot be registered.

That covers agents built *for* the-M. It does not cover what organizations actually have.

In a real enterprise, most AI is **closed**:

- a nightly cron job that scores transactions for fraud
- a Python script that parses invoices from a mailbox
- an internal service that summarizes support tickets
- a team's LangChain prototype that quietly went to production

These agents run on their own schedule, execute their own business logic, and expose no endpoint. Nobody can call them — they call out. There are typically hundreds of them, owned by different teams, built in different frameworks, using different models.

For the organization this creates a governance blind spot that grows every quarter:

- **Nobody knows what exists.** There is no inventory of which AI is running in production.
- **Nobody knows the cost.** Spend appears as one provider invoice with no attribution to team or use case.
- **Provider keys are sprayed everywhere.** The same API key is pasted into forty repos, CI configs and `.env` files. Rotating it means finding all forty.
- **No policy applies.** Nothing prevents an agent from sending card numbers or PII to a public model, or from using a model the organization has not approved.
- **No audit trail.** When someone asks "which AI touched customer data last month", there is no answer.
- **A bug is expensive.** A runaway loop in a cron job burns budget until the invoice arrives.

### Why "just migrate them into the-M" does not work

The obvious answer is to have each team rebuild their agent as a the-M agent. In practice this fails:

- It requires every team to rewrite working code for no benefit they can see.
- It requires the-M to be able to *call* the agent, but a scheduled job has no caller — it is the caller.
- Adoption is gated on hundreds of separate teams prioritizing a migration.

A control plane that only governs what was built for it will only ever govern a small fraction of an organization's AI. That is the strategic problem.

### The insight

Closed agents differ in language, framework, schedule and purpose. But they share exactly one thing:

> **Every one of them calls an LLM.**

That is the single wire the-M can insert itself into without touching the agent's business logic.

```
Before:   Closed agent ──────────────────▶ Anthropic / OpenAI / Groq / Ollama
After:    Closed agent ──▶ the-M Gateway ──▶ Anthropic / OpenAI / Groq / Ollama
```

The agent changes one line of configuration — the LLM base URL and the key it presents. No rewrite, no new SDK, no redeployment of logic.

In exchange the-M gains, for an agent it does not run and did not build:

| Capability | What it gives the organization |
|---|---|
| Identity | which agent made this call |
| Model usage | which models are actually in use, by whom |
| Tokens & cost | spend attributed per agent, per team, per model |
| Latency & failures | operational visibility |
| Audit | a queryable record of every LLM interaction |
| Quotas & rate limits | per-tenant and per-agent budget ceilings |
| Model restrictions | approved models only; block or route others |
| Key custody | provider keys held centrally, never given to agents |
| Data protection | PII filtering, file scanning, prompt-injection checks |

### Why this is the right strategic bet

Adoption is nearly free for the agent owner — a config change, not a project. That inverts the migration problem: instead of hundreds of teams doing work for the-M, the-M provides something they want (a working LLM endpoint, no key to manage) and governance comes along for free.

Once traffic flows through the-M, the-M becomes the **choke point** for enterprise AI. Every subsequent capability — caching, failover, budgets, policy, data protection — becomes a feature that can be added centrally with no change to any agent.

This is Priority 2 ("Gateway + universal connectivity") in `docs/THE-M_ENTERPRISE_AI_OPERATING_SYSTEM.md` §4.2 and §8, and it is the initial commercial wedge described in §17 ("Enterprise Agent Gateway & Registry"). §7 states the goal directly: *"Build the parts that make the-M valuable even when the agent was created somewhere else."*

### Why it is cheap to build here

Most of the required infrastructure already exists in this codebase:

| Need | Already exists |
|---|---|
| Client authentication | `internal/auth` — opaque bearer tokens, 3-tier cache, revocation |
| Tenant isolation | RLS + `BeginTenantTx` + `app.tenant_id` GUC |
| Rate limits & quotas | `internal/quota` — all five limits implemented |
| Encrypted provider keys | `them.llm_providers.api_key_encrypted` + `internal/crypto` (Fernet) |
| Provider clients | `internal/llm` — Anthropic + OpenAI-compatible (covers Groq/Ollama/vLLM/LM Studio) |
| Key precedence (tenant → platform) | `internal/temporal/workerconfig/loader.go` |
| Usage recording | `internal/runrecorder` |
| Guard components | `them.middleware_defs` — PII/injection guard, File Guard, cache already seeded |
| File interception | `middleware.FileGate.InterceptInline` |
| Horizontal scaling | stateless bridge + Traefik LB + proven second replica |
| UI patterns | canvas guard nodes + properties panel; nav slot already reserved |

The gateway is mostly **assembly and one new ingress**, not new subsystems.

---

## 2. Scope of this document

This document covers the design only. It is not yet implemented.

Open decisions are marked **DECISION NEEDED** and must be settled before the corresponding phase begins.

---

## 3. How we enable it

One new endpoint on `them-go-bridge`:

```
POST /{tenant_slug}/llm/v1/chat/completions
```

Auth is the existing THE-M access token — `Authorization: Bearer <them token>`. No new auth system.

Onboarding for a closed agent is a config change, not a code change:

```python
from openai import OpenAI
client = OpenAI(
    base_url="https://the-m/acme/llm/v1",
    api_key="<THE-M token>",       # not the provider key
)
client.chat.completions.create(model="claude-sonnet-4-6", messages=[...])
```

**Why the OpenAI request shape is the front door:** it is the de-facto standard. `go/internal/llm/openai.go` already serves `openai`, `groq`, `ollama`, `vllm` and `lmstudio` from that one format. Most agents and every major framework (LangChain, LlamaIndex, Semantic Kernel) can already speak it.

Anthropic has its own shape. Phase 5 (§14) adds `POST /{tenant}/llm/v1/messages` for teams using the Anthropic SDK directly, so they also onboard without a rewrite.

Multi-turn conversations need no session: LLM APIs are stateless, so the agent resends the full message history on each turn (it already does this today when calling the provider directly). Streaming holds a connection open for one answer only, then closes. The gateway therefore stores nothing between requests — see §10.

---

## 4. Providers and models

The front-door format is independent of the destination. An agent sends OpenAI-shaped JSON naming any model; the gateway translates and calls the right provider.

| `model` sent | Provider used | Existing code |
|---|---|---|
| `claude-sonnet-4-6` | Anthropic | `llm/anthropic.go` |
| `gpt-4o` | OpenAI | `llm/openai.go` |
| `llama-3.3-70b` | Groq | `llm/openai.go` |
| `llama3` | Ollama (local) | `llm/openai.go` |
| any vLLM / LM Studio model | self-hosted | `llm/openai.go` |

Resolution order for a model name:
1. Tenant's own `them.llm_providers` row (`tenant_id = <tenant>`) — BYO key / private endpoint.
2. Tenant's allowed-model policy — reject with `403` if the model is not permitted.
3. Platform default row (`tenant_id IS NULL`) only if the tenant policy allows falling back.

This precedence already exists in `go/internal/temporal/workerconfig/loader.go` (`loadTenantProviderKey` → `lookupLLMProviderKey`). It is currently buried in the Temporal worker. **Extract it into a new `internal/llmresolve` package** returning `ResolvedLLM{Provider, Model, APIKey, BaseURL}`, and have the worker, the DAG worker, the agent runtime and the gateway all call it. `docs/LLM_API_KEY_ARCHITECTURE.md` already proposed exactly this; it was never built.

Model aliasing lives here too: map `"fast"` → a concrete model per tenant, so policy can be changed centrally without redeploying agents.

---

## 5. Protocols — streaming, SSE, HTTP

Three cases must all work, because real SDKs use all three.

**Non-streaming** (`stream: false`) — the common case for cron jobs. One JSON response. The internal `llm.Provider` interface is **stream-only** (`Stream(...) (<-chan StreamEvent, error)`), so the gateway drains the channel and assembles one response body. `cmd/agent-runtime/llm.go` already does this via its `Complete` adapter — reuse that pattern rather than reinventing it.

**Streaming via SSE** (`stream: true`) — the gateway must re-emit provider events in OpenAI's SSE shape (`data: {...}` chunks, terminating `data: [DONE]`). Anthropic's native event names must be translated into OpenAI chunk deltas. `llm/anthropic.go` and `llm/openai.go` already parse both inbound formats; the new work is the **outbound** encoder.

**Cancellation** must propagate: client disconnect → `ctx` cancel → upstream HTTP cancel. The providers already honour `ctx` (`sendEvent` selects on `ctx.Done()`), so this is wiring, not new logic. Usage must still be recorded on a cancelled call — record in a `defer`, the same pattern `appflow/workflow.go` uses for `FinalizeRunActivity`.

Not in scope now: WebSocket ingress, and Anthropic-native SSE out (arrives with `/v1/messages` in Phase 2).

---

## 6. Reusing the existing policy and quota layer

This is the strongest argument for the gateway: **the control layer already exists.** `quota.Enforcer.Check(ctx, tenantID, Quota)` in `go/internal/quota/enforcer.go` already implements every limit the gateway needs.

| Control | Status |
|---|---|
| `api_requests_per_minute` | exists — Redis `rl:them:{tenant}:api:{minute}` |
| `monthly_llm_tokens` | exists — needs the bug fix in §12 |
| allowed models | new, per tenant |
| audit | exists — `admin.AuditWriter.Write` |
| tenant isolation | exists — RLS + `BeginTenantTx` |

Call `Check` with only the two relevant fields populated; leave the run-oriented fields `nil` (nil means "no limit"). Reuse the `tenantQuotaAdapter` already wired at `cmd/them/main.go:524`.

Per-token limiting (one noisy agent must not exhaust the tenant's budget) uses the existing `ratelimit.Limiter.CheckToken(ctx, tenantID, tokenHash, limit)`.

Deliberately **not** in slice 1: PII/secret scanning and prompt-injection filtering. Both sit on the hot path of every call and need their own false-positive tuning. The `middleware` package (File Guard) is the right home for them later.

---

## 7. Tenant separation

No new mechanism. The gateway follows the existing rules exactly:

- Tenant identity comes from the **token**, resolved from trusted DB state (`auth.TokenInfo.TenantID`) — never from a header or the request body. This is a hard rule stated in `tenantctx`'s package doc.
- Use `auth.BearerTenantMiddleware`, which already rejects tokens with an empty tenant (`403`).
- The `{tenant_slug}` in the path is resolved via the cached `tenantctx.PgxSlugResolver` and **must match the token's tenant**, else `404`. A mismatch must never be allowed to widen access.
- Writes go through `pools.BeginTenantTx(ctx, tenantID)`, which sets the transaction-local `app.tenant_id` GUC so RLS applies.

Reads of gateway history are RLS-scoped, so tenant A can never see tenant B's prompts, models or spend.

---

## 8. Storing provider keys safely

Two modes, per tenant. Both matter.

**Managed keys (the governance win).** The real provider key is stored Fernet-encrypted in `them.llm_providers.api_key_encrypted` (`enc:` prefix, `internal/crypto/fernet.go`, key derived from `secrets.local`). Agents authenticate with a THE-M token and **never see the provider key**. Keys can then be rotated centrally, and revoking one agent means revoking its token — not rotating a key shared by forty teams.

**BYO key (the on-ramp).** The tenant registers their own key, or a local endpoint with no key at all. Same encrypted column. This lets a team onboard in observe-only mode before handing over key custody.

Local/self-hosted models (Ollama, vLLM, LM Studio) need no key — `openai.go` already skips the `Authorization` header when the key is empty or `"ollama"`, and `base_url` points at the private endpoint, so **no traffic leaves the network**. This is the answer for PCI/PII-sensitive workloads.

Rules that must hold:
- Keys are decrypted only in memory, at call time, and never logged or returned by any admin API (`service/llm_providers.go` already masks them to `abcd...wxyz`).
- Platform-default keys must **not** silently serve tenant traffic — `loader.go:412` deliberately enforces this. Preserve it.
- **Fix `decryptValue` (`loader.go:505`)**, which currently returns the ciphertext verbatim when no Fernet key is configured. The gateway would forward the literal string `enc:gAAAA...` upstream as a bearer token. It must fail loudly instead.

---

## 9. Per-agent policy — components, profiles, assignment

Different agents need different treatment: one logs everything, one scans files, one strips PII. Policy is therefore **per agent**, not per tenant.

### 9.0 Roles vs policy — not the same axis

These are routinely conflated, so state it plainly:

> **Roles govern who may administer. Policy governs what runs on traffic.**

|  | **Roles (RBAC)** | **Policy (profiles)** |
|---|---|---|
| Subject | a **human** in the dashboard | an **agent's traffic** |
| Question answered | "may Sarah change this budget?" | "should this request be PII-scanned?" |
| Enforced at | the admin API, per dashboard request | the gateway hot path, per LLM call |
| Carried by | user JWT (`tenant_id` + role) | the client's assigned profile |
| Status | ✅ exists today | ❌ new build |

The clearest test: **a PII filter is not a permission.** No role grants "may send card numbers to
Anthropic" — that is a property of the traffic, decided per agent. Conversely no profile decides
whether Sarah may edit it — that is her role. They intersect at exactly one point: **a role decides
who may assign a profile.**

A third axis is commonly folded into these two and should not be:

- **Role** — who may *configure* (human, dashboard, user JWT)
- **Policy** — what runs on *traffic* (agent, hot path, profile)
- **Token** — which *agent* is calling (identity, quota attribution, revocation)

The token is the agent's identity, which is why revoking one agent means revoking its token rather
than rotating a provider key shared by forty teams (§8) — and why the revocation window in §16.2
matters more here than it does for dashboard sessions.

**Existing tenant roles are reused unchanged.** The gateway adds no new auth system and needs no new
role tier: `super_admin` → platform settings and cross-tenant observability; `admin` → create
clients, issue tokens, assign profiles, set budgets, view spend; `member` → view own clients and
request log; `viewer` → read-only. Tenant isolation is §7.

Three pieces:

**Components** — the catalogue of available checks. `them.middleware_defs` already exists and is already seeded with `guard_default` (PII + Prompt Injection), `file-guard`, and `cache_default`. Reuse it; add new defs (e.g. `audit-log`, §9a) as rows, not code.

**Profiles** (new) — a named, ordered bundle of components, each with its own config:
```
"Payments-Strict" → PII Guard → File Guard → Audit Log {tool_calls: true}
"Debug-Support"   → Audit Log {request_text: true, response_text: true}   (7-day retention)
"Internal-Basic"  → Audit Log {metadata: true}
"Dev-Sandbox"     → (none; metering only)
```

**Assignment** — each gateway client points at one profile. With 1000 agents, per-agent wiring drifts and is unauditable; a profile is defined once, assigned many times, and changing it updates every agent on it at once. Allow a per-agent override for exceptions, but the profile is the default path.

Runtime:
```
Agent ─▶ [PII Filter] ─▶ LLM ─▶ [File Guard] ─▶ Agent
              │                      │
         mask/block            scan/quarantine
```
No profile = plain pass-through with metering only.

Order is semantically load-bearing: PII filter **before** the LLM means the card number never reaches the provider; after would be pointless. The data model must preserve order (`position`, as `middleware_wirings` already does).

### 9a. Configurable logging (the `audit-log` component)

What gets logged must be **configurable per agent**, not hardcoded. Sensitivity and value differ sharply between the things the gateway can see, so they must be independently toggleable rather than bundled into one "log everything" switch.

A new `audit-log` def in `middleware_defs`, with its `config` JSONB carrying the toggles:

| Toggle | Content | Risk | Value |
|---|---|---|---|
| `metadata` | client, model, tokens, cost, latency, status | none | billing, ops |
| `tool_calls` | tool name + arguments the model requested | **low** | **high** — security review |
| `tool_results` | results the agent fed back | medium | debugging |
| `request_text` | the prompt | **high** — customer data, card numbers | debugging only |
| `response_text` | the completion | high | debugging only |
| `files` | uploaded file metadata / bytes | medium | pairs with File Guard |

Defaults: `metadata` on, everything else off.

**`tool_calls` is the cheapest high-value audit available and should be independently toggleable.** A record that the model requested `transfer_funds(amount=50000)` is exactly what a security team wants, and it typically contains far less regulated data than the prompt that produced it. Bundling it inside full-text logging would force teams to accept the highest-risk capture in order to get the highest-value signal.

Text and file bodies write to `gateway_request_bodies` (§12) and inherit its encryption, separate read permission and retention window. Tool calls and metadata are low-risk enough to live on `gateway_requests` directly.

### 9b. What the gateway can and cannot observe

| Observable | Visible? | Notes |
|---|---|---|
| Text sent to the LLM | yes | full request body |
| Text returned | yes | including streamed deltas |
| Tool calls requested by the model | yes | name + arguments, parsed from the response |
| Tool results | yes | as replayed by the agent on the next turn |
| Files sent **to** the LLM | yes | base64 images/documents in the request — File Guard can scan these |
| Files the LLM merely *names* | text only | LLMs return text, not files. If the model emits a URL and the agent fetches it, that fetch does not pass through the gateway |
| Actual tool execution | **no** | the agent calls the tool directly; see §12 |

The practical consequence: the gateway is a complete record of the **conversation**, and a complete record of **what the model asked for** — but not of what the agent then did. Full execution coverage requires the MCP gateway (§12).

New table needed because `middleware_wirings.agent_id` references `them.agents`, and closed agents are not rows there:
```sql
them.gateway_profiles(id, tenant_id, name, enabled)
them.gateway_profile_steps(profile_id, def_id, position, config)
-- client→profile assignment carried on the gateway client record
```

**Reuse `FileGate.InterceptInline(ctx, in, data)`** (`internal/middleware/gate.go:129`) as-is for the file case — it already takes raw bytes and returns allow/block, and already quarantines via `middleware_jobs`.

### Execution: in-process, not Temporal

Run these inline as ordinary function calls. Temporal is for durable long-running work; a PII regex is ~0.1ms and a Temporal round-trip is ~20–50ms plus a DB write, so routing every LLM call through it would be 100×+ slower and would make the gateway fail when Temporal is down.

| Check | Where |
|---|---|
| PII filter, logging, model allowlist, cache | in-process, inline |
| Virus scan of a file | enqueue (`middleware_jobs`) — never block the request |

This is the split File Guard already implements (`quarantineAndEnqueue`). Keep it.

---

## 10. Scaling approach

The gateway is **stateless** — no sessions, nothing held between requests (LLM APIs are stateless; the agent resends full history each turn). Scaling is therefore purely horizontal.

```
                    ┌─ bridge 1 ─┐
Agents ──▶ Traefik ─┼─ bridge 2 ─┼──▶ providers
                    └─ bridge n ─┘
```

Already in place: Traefik load balancing on `them-go-bridge-svc`, a proven second replica (`them-go-bridge-2` in `docker-compose.hetzner.yml`), health checks, and Redis-based shared counters in `quota`.

Growth path:

| Stage | Action |
|---|---|
| Start | gateway inside `them-go-bridge`, 2 replicas |
| More load | add replicas |
| Gateway starves admin/WS traffic | split to own container, scale independently |
| Very high volume | batch writes, consider PG read replica |

**Four rules that make this work. These are design constraints, not optimizations:**

1. **Never hold a DB connection across the LLM call.** This is the single most important rule.
   ```
   WRONG: open tx → call LLM (30s, connection idle) → write usage → commit
   RIGHT: read policy (Redis) → call LLM (no connection) → open tx → write usage → commit (~2ms)
   ```
   Done wrong, each replica caps at pool-size concurrent streams (~25) and adding replicas barely helps. Done right, the same pool serves thousands, because the work is network wait.
2. **Cache policy and resolved keys in Redis**, so the hot path does zero DB reads.
3. **Cap concurrent streams per tenant** — reuse the existing `gate` admission pattern; excess returns 429.
4. **Keep the code in `internal/llmgateway`**, free of bridge-specific coupling, so splitting it into `cmd/llm-gateway` later is a compose change plus a small `main.go` — the pattern `dag-worker` and `mcp-service` already follow.

**Gap to close:** the pgx pool size is not currently configurable (it uses the pgx default). Make `MaxConns` an env setting before load-testing.

At high volume Postgres write throughput — one usage row per call — becomes the next limit, so batch usage inserts rather than writing synchronously per request.

---

## 11. UI placement

Add **LLM Gateway** to the Build group in `frontend/src/components/Sidebar.tsx`, directly under Agents — Agents are agents THE-M hosts, the Gateway covers agents it does not, so they belong in the same category.

The nav slot is already reserved (commented out) at `frontend/src/app/admin/services/page.tsx:233`. Do **not** put it under `/admin/services` (Security Scans) — that page is for THE-M's own internal services and the gateway would be buried there.

Four tabs:

| Tab | Contents |
|---|---|
| **Clients** | agent list — label, last seen, requests today, spend, error rate. Click → assign profile. Creating a client issues the token and shows the copy-paste snippet, so users think in agents, not tokens. |
| **Profiles** | build the ordered bundles (see §9) |
| **Requests** | call log — time, client, model, tokens, cost, latency, status |
| **Settings** | allowed models, aliases, budget, provider keys |

**Profile editor — reuse the canvas.** A profile is a straight chain, which the existing canvas draws naturally:
```
[Request] → [PII Filter] → [LLM] → [File Guard] → [Response]
```
`MiddlewareNode` (`components/CanvasNodes.tsx:201`) and `CanvasNodePropertiesPanel` already exist, and users already know the interaction from App Canvas. A canvas also makes step order visible, which a checkbox list cannot.

Two constraints: restrict the palette to **guard nodes only** with fixed Request/LLM/Response endpoints and no branching — otherwise users will drop an agent node into a profile and expect it to execute. And since the underlying data is just an ordered list either way, **ship a simple ordered list first and swap the canvas in later** (Phase 3) so slice 1 is not blocked on canvas work.

---

## 12. Known gaps and risks

**Pre-existing metering bugs — FIXED in Phase 0 (commit `ed3620dc`, 2026-09-19).** The gateway's whole value is trustworthy numbers, and these three bugs would have made them wrong on day one:

1. ~~`SumMonthlyTokens` (`admin/dal/runs.go:175`) filters on `runs.created_at` — that column does not exist (the column is `started_at`).~~ Fixed: now filters on `started_at`. Regression test `go/TEST_INDEX.md` S2-10.
2. ~~The OpenAI-compatible path never sends `stream_options: {"include_usage": true}`.~~ Fixed: sent on every streamed request. Test S1-116.
3. ~~Cost comes from a hardcoded Claude-only map that defaults every unknown model to Sonnet pricing.~~ Fixed: cost now reads `them.llm_providers.model_pricing` via `internal/llmresolve.PricingTable` + `orchestrator.CostEstimator`, loaded once per run alongside the existing API-key lookup; falls back to the old hardcoded table only when DB pricing has no entry for the model. Tests S1-117, S1-118, S2-11.

`internal/llmresolve` was also extracted from `workerconfig/loader.go` in the same commit, per Phase 0 below — see `docs/CURRENT.md` "Phase 0 — COMPLETE" for the full change list, including two bugs found and fixed while unifying the duplicated key-resolution code (a missing tenant_id filter, and a missing "plain:" test-mode prefix case), and the deliberately-deferred `cmd/agent-runtime` gap.

**Where usage is stored — DECIDED: new gateway tables.** `run_usage.run_id` is `NOT NULL` → `runs.orchestrator_id` is `NOT NULL` → `orchestrators`. A cron job has no orchestrator and no run, so gateway usage cannot be written to the existing tables as-is.

Rejected alternatives: fabricating a synthetic orchestrator and a `runs` row per call would let the existing run-history UI work for free, but floods run history with thousands of single-call entries and drains the meaning of "run". Making `orchestrator_id` nullable and adding a `source` column touches the hottest table in the schema and forces every existing query to filter on `source`.

Why the existing agent tables cannot be reused either: all four (`agents`, `agent_definitions`, `agent_runtime_specs`, `app_agent_bindings`) assume the-M **calls** the agent — `agents.endpoint_url` is `NOT NULL` and the transport CHECK allows only outbound A2A. A closed agent has no callable URL.

The difference is not missing data, it is a different shape. For a hosted agent the-M runs the whole execution and can record a story (start → steps → end). For a closed agent the-M sees exactly one LLM call fly past, with no knowledge of what the job did before or after, or whether two calls belong to the same job execution. `runs` demands `orchestrator_id`, `session_id`, `goal` and `iterations` — all of which would have to be fabricated.

```
Hosted agent  → runs + run_steps + run_usage   (the-M runs it)
Closed agent  → gateway_requests               (the-M only observes it)
```

### Tables

**New — 5:**

| Table | Purpose |
|---|---|
| `gateway_requests` | one row per LLM call — client, provider, model, tokens, cost, latency, status. Drives the Requests screen, spend reports and budget totals. |
| `gateway_clients` | the closed-agent registry — label, owning token, assigned profile, last seen. The inventory the-M does not have today. |
| `gateway_profiles` | named policy bundles ("Payments-Strict", "Internal-Basic") |
| `gateway_profile_steps` | the ordered components in each profile (`def_id`, `position`, `config`) |
| `gateway_request_bodies` | opt-in encrypted prompt/completion text, separate so the metering path never touches it and retention deletes are cheap |

```sql
-- db/099_llm_gateway.sql
them.gateway_requests(
  id, tenant_id NOT NULL, client_id, token_id,
  provider, model_requested, model_served,
  status, http_status, error_code,
  tokens_in, tokens_out, cost_usd,
  latency_ms, ttfb_ms, streamed, created_at)
```
Plus per-tenant policy: `them.gateway_policies(tenant_id, allowed_models, model_aliases, max_tokens_per_request, monthly_budget_usd)`.

**Reused as-is — 4:** `access_tokens` (client authentication), `llm_providers` (keys + `model_pricing`), `middleware_defs` (the available components), `tenant_quotas` (limits).

Use the RLS boilerplate from `db/092_tenant_runtime_config.sql` verbatim on every new table — `ENABLE` + `FORCE` RLS, a `tenant_isolation` policy `TO them_app`, and explicit GRANTs to **both** `them_app` and `them_admin` (BYPASSRLS does not grant table privileges). Then teach `SumMonthlyTokens` to sum runs **and** gateway usage, so one tenant budget covers both kinds of traffic.

**Prompt/response retention — DECIDED: text capture is in scope, as an opt-in toggle.**

The gateway sees every prompt from every closed agent. Capturing the text is wanted (for debugging bad answers and for replay, §13), so the schema must support it from the start — encryption and retention cannot be bolted on later.

Three levels, with only the first on by default:

| Level | Stored | Default |
|---|---|---|
| Metadata | who, model, tokens, cost, latency, status | **on** |
| Redacted | text with emails/cards/secrets masked | opt-in |
| Full text | raw prompt + completion | opt-in per client |

Requirements for the full-text level, because in a payments context these bodies will contain card numbers and customer data:

- **Per-client toggle**, not per-tenant — turn it on for the one agent being debugged, not everything.
- **Encrypted at rest** — reuse `internal/crypto` (Fernet), same pattern as `api_key_encrypted`.
- **Retention window with automatic deletion** — default 7 days. A retention job must exist before the feature ships; without it "temporary debugging" becomes a permanent regulated-data store.
- **Separate read permission** — viewing bodies must be a distinct grant from viewing metadata, and every read should be audited.
- **Stored in a separate table** (`gateway_request_bodies`, keyed on request id) rather than as columns on `gateway_requests`, so the hot metering path never reads or writes them, and so deletion is a cheap partition/range delete.

Metadata alone still answers the questions that get asked most — who spends the most, what is slow, what is failing, what it costs — so the default stays metadata-only.

**Tool-call visibility is partial — know the limit.** When a closed agent uses tools (MCP or otherwise), the gateway sees the model's *intent* but not the execution:

```
Agent ──▶ Gateway ──▶ LLM
                       └─▶ "call get_balance(acct=123)"   ← VISIBLE (in the response)
Agent ◀── Gateway ◀────┘
   │
   └──▶ MCP server: get_balance(123)                       ← NOT VISIBLE (agent calls direct)
   │
   └──▶ Gateway ──▶ LLM (with the tool result)             ← result text VISIBLE next turn
```

Visible: the requested tool name and arguments, and whatever result the agent feeds back on the following turn. Not visible: whether the tool actually ran, what it really returned, or any tool the agent invokes on its own without the model asking.

So the gateway can **observe and alert** on tool intent — log every tool an agent's model requested, flag a request for a payment or data-export tool, detect an injection attempt trying to trigger a dangerous call — but it **cannot verify or block** execution, because it is not in that path.

Closing that gap requires the-M to sit in the MCP path as well (see below). The two together give: LLM gateway = what the model wanted; MCP gateway = what actually happened.

**Future: the same pattern for MCP (not this slice).** Closed agents also call MCP servers directly, with the same blind spot — no identity, no audit, no policy, and credentials held by each team. The gateway pattern applies unchanged: put the-M in the path and gain the same governance.

Design implication for now: **keep the policy pipeline (§9) protocol-agnostic.** The step interface should be "inspect a payload, allow/redact/block" rather than anything LLM-shaped, so MCP interception reuses the same components, profiles, and UI rather than needing a parallel implementation. `middleware_defs` is already generic in this way.

**Other gaps worth flagging:**
- **Failure semantics.** Provider 429/5xx must map to sane gateway responses, with upstream `Retry-After` preserved. Decide whether the gateway retries or the agent does (recommend: agent does, gateway stays thin and predictable).
- **Timeouts.** `httpTimeout` is 10 minutes — far too long for a gateway. Needs its own shorter, configurable budget.
- **Provider failover.** Named in the vision doc (§4.2). Not slice 1, but the resolver should be shaped so a fallback provider can be added without redesign.
- **"Closed agent" is a new concept.** `them.agents` requires a non-null `endpoint_url` and a CHECK-constrained outbound transport, so an agent THE-M cannot call is currently unrepresentable. Registration and invocability are the same thing today. Gateway clients therefore need their own identity (a labelled token), and `agentregistry` must never offer them as LLM tools.
- **`gemini`** passes provider validation but has no implementation — it will fail at runtime if a tenant selects it.
- **OpenAI parallel tool calls are broken** (`openai.go:245` hardcodes index 0), so concurrent tool calls merge into one corrupt call. Affects any agent using parallel tools through the gateway.

---

## 13. High-value features this unlocks

All of these are **additional profile components**. If the gateway is built as an ordered pipeline from the start, each is a new box on the canvas rather than a re-architecture — which is the main argument for the pipeline shape even though slice 1 only meters.

| Feature | Value |
|---|---|
| **Semantic cache** | Repeated/similar question → return stored answer. Zero provider cost, instant. Typically 30–50% of agent traffic is repetitive. `cache_default` is already seeded. |
| **Failover** | Provider returns 529/5xx → retry on another provider automatically. The agent never notices. Named in vision doc §4.2. |
| **Budget kill-switch** | Per-agent monthly USD cap; block or downgrade at the limit. Today a runaway loop is discovered via the invoice. |
| **Cost-based model downgrade** | Simple prompts → Haiku, complex → Sonnet. Same outcomes, a fraction of the cost. |
| **Prompt-injection detection** | Blocks "ignore your instructions" attacks. `guard_default` already covers this. |
| **Shadow / A-B testing** | Mirror a % of traffic to another model and compare cost/quality. Answers "should we switch?" with data. |
| **Local-only enforcement** | Certain agents restricted to Ollama/vLLM — data physically cannot leave the network. A real compliance control for PCI/PII. |
| **Replay** | Re-run a stored request against a new model or prompt. Depends on the text-retention decision. |

Strongest first two: **cache** (saves money on day one, demos well) and **failover** (the thing that makes an org comfortable routing all traffic through THE-M).

---

## 14. Recommended approach

Ship the choke point first, then policy. Each phase is independently useful.

**Phase 0 — foundation (no new surface). ✅ COMPLETE (commit `ed3620dc`, 2026-09-19).** Extracted `internal/llmresolve` from `workerconfig/loader.go` (and unified `cmd/dag-worker`'s duplicate `resolveKey` behind it); fixed the three metering bugs in §12; fixed `decryptValue`/`DecryptValue` fail-loud. Every existing LLM path now meters correctly. `go test ./...` — 0 failures (S1 1254, S2 59; see `go/TEST_INDEX.md` S1-116..118, S2-10, S2-11). Full change list and two additional bugs found during the unification (missing tenant_id filter on app-level key lookup; missing `"plain:"` test-mode prefix handling) are in `docs/CURRENT.md` → "Phase 0 — COMPLETE". One gap deliberately deferred: `cmd/agent-runtime` keeps its own narrower, un-unified key-resolution path — see the same section for why and what breaks (tenant-level-only keys aren't found by agent-runtime).

**Phase 1 — gateway, observe + meter.** Next task on this track. Migration `db/099`. New `internal/llmgateway` (handler → service → DAL). `POST /{tenant}/llm/v1/chat/completions`, streaming and non-streaming. Bearer auth, tenant match, quota via the existing `Enforcer`, usage recorded in a `defer`, audit on rejections. Traefik router `PathRegexp(^/[^/]+/llm(/|$))` at priority 120, and a Go route mounted **before** `MountApps` — its `Handle("/*")` catch-all already caused the A2A outage in commit `7e9b7b1`.

Phase 1 must ship as an **ordered pipeline with zero steps configured**, not as a straight-through proxy — so later phases add components instead of restructuring. Obey the four scaling rules in §10 from the first commit, especially rule 1 (no DB connection held across the LLM call).

**Phase 2 — governance.** `gateway_policies`: allowed models, aliases, per-request token ceiling, monthly USD budget. `403` on blocked model.

**Phase 3 — profiles + UI.** `gateway_profiles` / `gateway_profile_steps` (§9). Wire the first components: PII Guard (`guard_default`), File Guard (`InterceptInline`), and `audit-log` with the low-risk toggles only (`metadata`, `tool_calls` — §9a). Admin UI with the four tabs (§11) — ordered-list profile editor first, canvas editor after.

**Phase 4 — text capture.** Enables the remaining `audit-log` toggles (`request_text`, `response_text`, `tool_results`, `files`). `gateway_request_bodies` with Fernet encryption, per-client toggle, separate read permission, and the retention job. The retention job ships **in the same phase** as the capture — not after.

**Phase 5 — cache + failover** (§13). The two highest-value components.

**Phase 6 — Anthropic native ingress** (`/v1/messages`), if real teams need it.

**Later, separate feature — MCP gateway.** Same pattern applied to MCP traffic (§12), which also closes the tool-execution blind spot. Not part of this work, but the pipeline must stay protocol-agnostic so it can reuse the same components and profiles.

Per `CLAUDE.md`, one focused subsystem per session: **each phase is its own session.**

---

## 15. Verification

- `cd go && go test ./...` — zero failures; add `S1-113` (gateway) and `S1-114` (resolver) rows to `go/TEST_INDEX.md` in the same commit.
- Unit: model resolution precedence, blocked model → 403, token→tenant mismatch → 404, quota exceeded → 429, usage recorded on success / error / client-cancel, SSE chunk shape, non-streaming assembly.
- RLS: extend `go/internal/db/rls_integration_test.go` — tenant A must not read tenant B's `gateway_requests`.
- Live E2E (`scripts/tests/test_41_llm_gateway.py`): point the real `openai` Python SDK at the gateway and assert, for **Anthropic, Groq and Ollama** targets, that a non-streaming call returns a valid body, a streaming call yields ordered chunks ending in `[DONE]`, and that **non-zero** tokens and cost land in `gateway_requests` for each.
- Negative: revoked token → 401; blocked model → 403; exceeded RPM → 429; provider 429 → `Retry-After` preserved.
- Confirm the provider key never appears in logs, in any admin API response, or in `gateway_requests`.

---

## 16. Availability, failure modes and bypass

**Why this section exists.** §10 answers "how does this scale". It does not answer "what happens
when it breaks". Those are different questions, and the second one becomes existential the moment
the gateway carries real traffic: routing 200 closed agents through the-M converts it from a tool
some teams use into **org-critical infrastructure**. Every agent's ability to function now depends
on the-M being up. That is an obligation this design must discharge explicitly, not discover in
the first incident.

Two questions drive the whole section:

1. What happens to the organization when the-M is restarted or fails?
2. Can a team keep working if the-M is unavailable — deliberately, not by accident?

### 16.1 Restart and rolling deploys

The gateway is **stateless** (§10): no sessions, nothing held between requests, because LLM APIs
are stateless and the agent resends full history each turn. That property is what makes restarts
survivable.

With Traefik in front and ≥2 replicas, deploy one replica at a time and in-flight requests drain
on the replica being replaced while the other serves traffic. **A normal deploy is therefore
zero-downtime and requires no agent-side change.** This is already achievable with the existing
`them-go-bridge` / `them-go-bridge-2` setup — it has simply never been stated as a requirement.

Two constraints make it real rather than theoretical:

- **Minimum 2 replicas is a production requirement for the gateway, not a tuning option.**
  Single-replica is acceptable for `them-go-bridge` serving dashboard traffic; it is not
  acceptable once closed agents depend on it.
- **Graceful shutdown must drain, not cut.** A streaming LLM response can be 30s+. `internal/server`
  already implements graceful shutdown; verify its timeout exceeds the longest expected stream, or
  deploys will sever live responses and surface to agent owners as random failures.

### 16.2 Redis — what actually depends on it

Redis is commonly assumed to be "just cache / UI data". For the gateway path that is wrong, but it
is also not uniformly fatal. It splits three ways, and each needs a different answer.

| Concern | Redis role | On Redis failure | Verdict |
|---|---|---|---|
| **Token auth** | L2 cache between L1 and Postgres | Falls through to DB | ✅ Already correct |
| **Quotas / rate limits** | Sole store (INCR counters) | No fallback exists | ⚠️ Needs a decision |
| **Token revocation** | Pub/sub L1 eviction | Best-effort; missed | 🔴 Real exposure |

**Auth already degrades correctly.** `Cache.Validate` walks `L1 (in-process) → L2 (Redis) → DB`,
and the L2 read is guarded by `err == nil && found` (`internal/auth/token_cache.go:154`), so a
Redis error simply falls through to Postgres. Authentication keeps working, slower. No change
needed — but do not "optimize" this into a hard dependency later.

**Quotas are Redis-only.** `runs_per_minute`, `api_requests_per_minute` and `monthly_runs` are
Redis INCR counters (`internal/quota/enforcer.go`) with no DB fallback. `Enforcer.Check`
deliberately returns the error as-is so the **caller** decides. The gateway must therefore choose,
and the choice is:

> **Fail open on policy, fail closed on auth.**

Cannot read a quota counter → allow the call, emit a metric and an audit entry. Cannot verify a
token → reject. Rationale: a Redis hiccup must never take down 200 production agents, and must
never become an authentication bypass. The asymmetry is the point.

Fail-open must be **observable**: a counter (`gateway_quota_failopen_total`) and an audit entry per
occurrence, otherwise a silent Redis outage becomes a silent unmetered-spend window.

**Token revocation is the sharp edge.** `Cache.Revoke` deletes the L2 key and publishes the hash on
`them:token:revoked` so every pod evicts its L1 entry. Both steps are best-effort and only log a
warning on failure (`token_cache.go:196-215`). With `l1TTL = 300s`, **a revoked token can remain
valid for up to 5 minutes on any pod that missed the message.**

This is tolerable for dashboard sessions. It is more significant here, because in the gateway a
token *is* an agent's identity — revocation is how you cut off a compromised or runaway agent. The
window is bounded and small, but it must be a known, accepted property rather than a surprise.
Options, in increasing cost: document and accept; shorten `l1TTL` for gateway tokens specifically;
or check a revocation generation counter on the hot path. **Decide before Phase 1 ships**, and
record the decision here.

### 16.3 Postgres — the problem is connections, not uptime

The database risk for the gateway is not availability. It is **connection exhaustion**, and it is
caused by a coding pattern rather than by load:

```
WRONG: open tx → call LLM (2–30s, connection idle) → write usage → commit
RIGHT: read policy (Redis) → call LLM (no connection held) → open tx → write usage → commit (~2ms)
```

This is rule 1 of §10, restated here because it is an *availability* property, not only a
throughput one. Held across the call, each replica saturates at roughly pool-size concurrent
streams (~25) and adding replicas barely helps; released, the same pool serves thousands, because
the work is network wait.

**PgBouncer does not fix this.** It multiplexes many client connections onto fewer server
connections; it does not stop application code from pinning a connection for 30 seconds. Used
against the wrong pattern it relocates the queue rather than removing it. Fix the pattern first;
adopt PgBouncer afterwards for headroom. If adopted: use **transaction pooling** mode, and note
that pgx prepared-statement caching needs a compatible statement-cache mode.

**Open gap (from §10):** `MaxConns` is not configurable — the pgx default applies. Make it an env
setting before any load test, or the test measures the default rather than the design.

### 16.4 Bypass — deliberate, not accidental

Because the-M becomes a hard dependency for agents it does not own, there must be a defined way to
take it out of the path. The alternative is not "no bypass" — it is an undocumented, panicked
bypass invented during an incident.

**Layer 1 — per-client BYO key.** §8 already defines BYO-key mode as an onboarding on-ramp. It is
equally the per-agent escape hatch: an agent configured with its own provider key and the upstream
URL is one config change away from running without the-M. Same two values that onboarded it.

**Layer 2 — break-glass redirect.** Point the gateway hostname at the provider (DNS or proxy rule)
so every agent recovers at once without touching 200 individual configs. Governance is lost for the
duration; the business keeps running.

**The governing principle: make bypass loud, not hard.**

A bypass easy enough to save you in an outage is, by construction, easy enough to evade governance
with. Do not resolve that by making it difficult — that only guarantees it gets done badly under
pressure. Resolve it by making it **visible**: break-glass raises an alert, is written to the audit
log, and appears in the Clients tab as a per-client state (e.g. `bypassed since <time>`). An
operator can always tell how much traffic is currently ungoverned.

**Tell teams the escape hatch exists during onboarding.** It is not a weakness to hide; it is a
large part of why 200 teams agree to route through you in the first place. "You can always flip it
back" is what makes the config change feel safe.

### 16.5 Consequences for phasing

**HA belongs in Phase 1, not a later hardening pass.** Retrofitting availability onto a component
that 200 agents already depend on is materially harder than designing it in: the fail-open/closed
split, the no-connection-held rule and the bypass path all shape the code from the first commit.

Concretely, Phase 1 must ship with: ≥2 replicas, the fail-open-on-policy decision implemented and
instrumented, the revocation-window decision recorded, drain-aware shutdown verified against a long
stream, and Layer 1 bypass documented in the onboarding snippet.

### 16.6 Status of this section

Derived from a design review, not from a built system. Verified against code at the time of
writing: the `L1 → L2 → DB` auth fallback and its `err == nil` guard, the Redis-only quota
counters, the best-effort revocation publish, and `l1TTL = 300s`. The rest is design intent and
must be re-checked when Phase 1 is implemented.
