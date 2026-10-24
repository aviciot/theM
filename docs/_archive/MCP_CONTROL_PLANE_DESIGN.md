# the-M as an MCP Control Plane

Status: PROPOSED — design doc, not started
Author: session 2026-09-19
Companion doc: `docs/LLM_GATEWAY_DESIGN.md` (the pattern this reuses)

## Why this doc exists

the-M already governs LLM traffic through the LLM Gateway (model allowlists,
budgets, and a policy pipeline designed to be protocol-agnostic — see
`LLM_GATEWAY_DESIGN.md` §12/§14). The explicit next step called out in that
doc is: **apply the same governance pattern to MCP traffic**, closing the
blind spot where tool calls are invisible to the-M.

Before designing that, this doc checks the MCP spec itself, because it
changed underneath us. The newest revision (2026-07-28) removed sessions
from the protocol entirely — which directly answers the "does MCP need to
be stateful, and do we need to support it" question raised earlier in this
project, and materially simplifies the gateway design.

---

## Part 1 — What changed in MCP 2026-07-28

This is the first major spec revision since 2025-11-25. The MCP maintainers
call it the largest revision since launch. Relevant changes, grouped by
what they mean for a governance proxy:

### 1a. Protocol-level sessions are gone

- The `initialize` / `notifications/initialized` handshake is **removed**.
  Every request now carries its protocol version and client capabilities
  in `_meta` (`io.modelcontextprotocol/protocolVersion`,
  `clientCapabilities`, `clientInfo`). No handshake round-trip before the
  real call.
- The `Mcp-Session-Id` header and protocol-level sessions are removed from
  Streamable HTTP. `tools/list`/`resources/list`/`prompts/list` no longer
  vary per-connection.
- Servers that need state across calls now use **server-minted handles**
  passed back as ordinary tool arguments — state becomes visible to the
  model and to anything inspecting the call, instead of hidden in
  transport/session plumbing.
- SSE stream resumability (`Last-Event-ID`) is removed. A broken stream
  loses the in-flight request; the client just re-issues it as a new
  request.

**This confirms our executor's "stateless per call" design (`executor.go`,
comment: "stateless executor design") is not a shortcut — it's now the
direction the protocol itself moved.** The old HTTP+SSE transport
(session-based) is formally deprecated in this revision. We do not need to
build session/connection pooling to be "correct" — the spec agrees with
stateless-per-call as the model going forward.

### 1b. New headers make gateway routing cheap

- Streamable HTTP POST requests now require `Mcp-Method` (the JSON-RPC
  method — `tools/call`, `resources/read`, etc.) and `Mcp-Name` (the
  operation target — tool name, prompt name, or resource URI).
- Optional `Mcp-Param-*` headers can carry selected argument values,
  one header per opted-in parameter.
- This means a gateway can **route, rate-limit, and apply coarse policy
  by reading headers alone** — no JSON body parsing needed for the common
  case (e.g., "block tool X for tenant Y" is a header check, not a body
  parse). Fine-grained policy (argument/content inspection, PII/injection
  scanning) still requires the body, but routing/allow-deny by tool name
  gets much cheaper.
- The spec explicitly notes the JSON-RPC body remains authoritative;
  a gateway must reject mismatched header/body pairs rather than trust
  headers blindly (relevant to §3 below — don't skip body validation for
  security-relevant decisions, only for cheap routing).

### 1c. Multi Round-Trip Requests (MRTR) replace server-initiated requests

- `roots/list`, `sampling/createMessage`, `elicitation/create` no longer
  work as server-initiated mid-call requests. Instead a server returns
  `resultType: "input_required"` with an `inputRequests` list; the client
  retries the *original* request with `inputResponses` attached.
- Every result now carries a `resultType` (`"complete"` or
  `"input_required"`). A gateway must pass through `input_required`
  results and their retries coherently — it cannot treat a call as "done"
  until it sees `resultType: "complete"`.

### 1d. Deprecations relevant to us

- **Roots, Sampling, Logging** features are deprecated (12-month window).
- **HTTP+SSE transport** (the old stateful one) is now formally Deprecated
  — new work should target Streamable HTTP only.
- **OAuth 2.0 Dynamic Client Registration** is deprecated in favor of
  Client ID Metadata Documents (still usable for back-compat).

### 1e. Authorization hardening (OAuth 2.1-based, tightened further)

- MCP server = OAuth resource server, MCP client = OAuth client, external
  IdP = authorization server (this split was introduced in the 2025-06
  revision; 2026-07-28 hardens it further).
- `iss` (issuer) validation per RFC 9207 is now required — closes an
  authorization-server "mix-up" attack class.
- Client credentials must be keyed by issuer; a client must not reuse
  credentials across different authorization servers.

### 1f. Caching hints

- `tools/list`, `resources/list`, etc. now return `ttlMs` and `cacheScope`
  (`"public"`/`"private"`). A gateway sitting in front of multiple callers
  can honor `cacheScope: "public"` to safely cache/share list responses
  tenant-wide, cutting repeated discovery calls.

---

## Part 2 — What this means for the-M's design

### 2a. Statefulness question — resolved

No, we do not need to build session pooling or persistent per-server
connections into the governance layer. The spec itself abandoned sessions.
Our existing `Executor.Execute` stateless-per-call model is already
aligned with where MCP is going. Any latency concern about "re-auth every
call" is an **OAuth token caching** problem (cache the bearer token /
credential per tenant+server, which we already do via
`them.app_mcp_credentials`), not a protocol-session problem — there is no
handshake left to amortize in the new spec.

Practical implication: keep `them-mcp-service` stateless per call, as it
is today. Do not build a session/connection pool. Do cache resolved
credentials and, once servers adopt `ttlMs`/`cacheScope`, cache
`tools/list` results tenant-wide where `cacheScope: "public"`.

### 2b. The control-plane shape (mirrors LLM Gateway)

```
Canvas agent / orchestrator ─┐
                              ├──▶ them-mcp-service (already the choke point)
Closed agent / external job ─┘         │
                                        ├─ 1. resolve tenant + credential (exists today)
                                        ├─ 2. policy check (NEW)
                                        ├─ 3. audit log the call (NEW)
                                        ├─ 4. forward to real MCP server
                                        ├─ 5. inspect/redact response (NEW)
                                        └─ 6. return to caller
```

**Clarification — one URL per tenant, not one URL per MCP server.** This
design does *not* propose exposing a separate managed endpoint per
registered MCP server (e.g. `/mcp/jira`, `/mcp/github`,
`/mcp/filesystem`). There is exactly **one** the-M-facing entry point per
tenant, matching the LLM Gateway's `POST /{tenant_slug}/llm/v1/chat/completions`
shape (one path per tenant; which provider/model to use is a body field,
not a different path per provider). For MCP, which server and which tool
to call is likewise carried *inside* the request — a body field today via
`them-mcp-service`'s `/internal/execute`, or the `Mcp-Method`/`Mcp-Name`
headers the 2026-07-28 spec now defines (§1b) — not selected by hitting a
different URL. Routing to the correct downstream MCP server happens
*inside* `them-mcp-service`, after tenant/policy checks, same as it does
today for internal callers in `executor.go`. See Part 3, which already
says "no per-MCP-server gateway process."

Two gaps to close, matching the earlier investigation:

**Gap 1 — canvas agents already route through them-mcp-service, but it
does nothing.** `executor.go`'s `validateTool` only checks tool-name
existence in a cached manifest (fails open if manifest is empty). No
allow/deny, no argument inspection, no audit log of name/arguments/result
— only a call-count metric.

**Gap 2 — closed agents bypass the-M entirely**, dialing MCP servers
directly with their own credentials. Same blind spot the LLM Gateway
closed for LLM calls (`LLM_GATEWAY_DESIGN.md` §12): "no identity, no
audit, no policy." Closing this means closed agents must be issued a
the-M-brokered MCP endpoint/credential instead of the real server's
URL+secret, exactly like closed agents already get a the-M-brokered LLM
endpoint.

### 2c. Reuse, don't rebuild, the LLM Gateway's policy pipeline

Per `LLM_GATEWAY_DESIGN.md`'s own stated design intent, the pipeline
schema (`them.middleware_defs`, `them.gateway_profiles` /
`gateway_profile_steps`) was deliberately built protocol-agnostic ("inspect
a payload, allow/redact/block") specifically so MCP governance could plug
into the same components instead of a parallel implementation. Concretely:

- Extend `gateway_policies`-equivalent config for MCP scope: tool-name
  allow/deny lists (per tenant, per MCP server), instead of (or alongside)
  model allowlists.
- Wire the dormant `guard_default` middleware def (already seeded in
  `db/013_agentic_middleware.sql`, PII + prompt-injection config, currently
  unused by any code) into the MCP call path as a profile step — this is
  the same engine `LLM_GATEWAY_DESIGN.md` Phase 3 already planned to wire
  for LLM text; wiring it once and applying it to both LLM and MCP
  payloads is the point of keeping it protocol-agnostic.
- Use the new `Mcp-Method`/`Mcp-Name` headers for the cheap first-pass
  filter (tool allow/deny by name — reject before touching the body), and
  reserve full JSON-body inspection (PII/injection scanning of arguments
  and results) for calls that pass the cheap filter. This keeps the common
  case fast.

### 2d. Audit logging (new, does not exist today)

Add a `them.mcp_call_log` (or extend `gateway_requests`-style table) that
records, per call: tenant, application, server, tool name, arguments
(redacted per policy), result summary, policy verdict (allow/block/redact),
timestamp. This is the piece explicitly called "missing" in the earlier
investigation — today only a call-count metric exists, no content trail.

### 2e. Closing the "closed agent" blind spot

Mirror the LLM Gateway's closed-agent pattern: issue closed agents **one**
the-M-fronted MCP URL per tenant (`them-mcp-service`'s already-internal
endpoint, exposed with a the-M-issued token scoped to that tenant) instead
of the raw MCP server URL + raw credential. This is a single shared
endpoint for every MCP server the tenant has registered — not one URL per
server (see the clarification in §2b) — with server/tool selected inside
the request body or headers. the-M holds/brokers the real credential
(reusing `them.app_mcp_credentials`) so the external script never sees it.
This requires no new protocol work — it's the same "become the middleman"
move already made for LLM traffic, applied to the MCP entry point.

### 2f. MRTR / input_required pass-through

Because MRTR replaces server-initiated requests with a retry pattern
carrying `resultType: "input_required"`, the gateway must treat these as
first-class pass-through, not as "call complete" — audit-log them as
in-progress, not finished, and don't apply completion-time policy checks
(e.g., budget/rate-limit decrement) until a `"complete"` result is seen.

---

## Part 3 — What we do NOT need to build

- No session/connection pooling (§2a).
- No SSE resumability handling — the new spec removed it; a dropped
  stream is just a fresh request/retry, nothing for the gateway to
  reconstruct.
- No changes to `them-mcp-service`'s "stateless executor" architecture —
  it was already aligned with where the spec landed.
- No per-MCP-server gateway process — one shared `them-mcp-service`
  fronting all registered servers remains correct; the new header-based
  routing (`Mcp-Method`/`Mcp-Name`) makes a single shared gateway even
  cheaper to operate at scale, not harder.

---

## Open items for a future planning session

- Decide whether tool-name allow/deny lives in a new MCP-scoped policy
  table or is added as fields on the existing `gateway_policies` row
  (schema decision, needs its own short design pass).
  bump `them.mcp_servers`/executor to accept and validate `Mcp-Method`/
  `Mcp-Name` headers once client SDKs commonly emit them (check current
  MCP SDK versions in use by registered servers before requiring them).
- Rollout order should follow the same wave discipline as the LLM
  Gateway and the folders plan: Wave 1 = audit logging only (visibility,
  no enforcement), Wave 2 = tool allow/deny enforcement, Wave 3 = PII/
  injection guard wiring, Wave 4 = close the closed-agent direct-access
  gap. Land each wave, test, commit, before starting the next.

## Sources

- [Key Changes — MCP spec 2026-07-28 changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog)
- [The 2026-07-28 Specification — MCP Blog](https://blog.modelcontextprotocol.io/posts/2026-07-28/)
- [The 2026-07-28 MCP Specification Release Candidate — MCP Blog](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- [MCP prepares to break with its stateful past — The Register](https://www.theregister.com/devops/2026/07/23/model-context-protocol-prepares-to-break-with-its-stateful-past/5276722)
- [Scaling AI Agent Infrastructure with MCP Stateless updates — Google Developers Blog](https://developers.googleblog.com/scaling-ai-agent-infrastructure-with-the-mcp-stateless-updates/)
- [The New MCP Headers Are a Gift to Gateways — Tigera](https://www.tigera.io/blog/the-new-mcp-headers-are-a-gift-to-gateways/)
- [MCP Goes Stateless — InfoQ](https://www.infoq.com/news/2026/08/mcp-stateless-gateway/)
- [MCP gateway: architecture and governance — Speakeasy](https://www.speakeasy.com/resources/mcp-gateway)
- [Diving Into the MCP Authorization Specification — Descope](https://www.descope.com/blog/post/mcp-auth-spec)
- [The biggest MCP spec update ships July 28 — WorkOS](https://workos.com/blog/mcp-2026-spec-agent-authentication)
