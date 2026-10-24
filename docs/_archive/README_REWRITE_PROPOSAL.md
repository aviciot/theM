# README Rewrite Proposal — the-M
# Created: 2026-10-10
# Status: PROPOSAL — structure for approval, README not yet changed

Evidence: code survey of `go/`, `db/`, `frontend/` plus `docs/CURRENT.md` and the design docs (2026-10-10).
Rule used: **README claims only what code ships.** Designed-but-unbuilt items go in a "Roadmap" section, clearly labelled.

---

## 1. Why the current README is stale

The README sells a generic "Enterprise AI Control Plane" (registry, BYOC, air-gap, marketplace). It under-weights what is actually built and differentiated:

| Built and differentiated (under-represented) | Where in code |
|---|---|
| **LLM Gateway for closed agents** (point `base_url` at the-M; auth, audit, metering, budgets, governance via an App Canvas "profile" flow) | `go/internal/llmgateway/`, migrations 100/120 |
| **Multi-tenancy** with Postgres FORCE RLS (~40 tables), quotas/plans, per-tenant LLM keys, tenant provisioning | `go/internal/db`, `quota`, `tenantctx` |
| **Governance guards** in flows: PII redaction, prompt-injection, ClamAV file scan with quarantine | `go/internal/middleware/`, `cmd/middleware-worker` |
| **SSO / RBAC**: per-tenant OIDC IdP, IdP-group to role mapping, runtime external-JWT at entry points, Org Roles to App grants | `go/internal/authserver`, `auth/external_jwt.go`, `roles` |
| **App Canvas** (visual AppFlow: llm/condition/router/fork/join/cycle/hil/http/transform/guards) + debug mode + full export/import + Deploy to Tenant | `go/internal/appflow`, `admin/app_deploy` |
| **Voice/WebRTC, A2A v1.0 server+client, HIL approvals** | `internal/voice`, `internal/a2a` |

## 2. README claims the code does not support (fix or move to Roadmap)

| README says | Reality |
|---|---|
| MCP "tool access policy" / limit tools by role | Only a **per-LLM-node tool allow-list** exists. No per-role exposure, deny list, per-tool limits, or the-M-as-MCP-server. Designed in `MCP_CONTROL_PLANE_DESIGN.md` (PROPOSED). |
| "Policy engine / PCI data may not route to public providers" | No policy engine. Gateway has model allow-list, max-tokens, monthly USD budget, RPM quota. |
| Auth "roles, teams, permissions", Keycloak | No Teams (AUTH.md: not migrated). Keycloak is only a demo IdP; code is generic OIDC/JWKS. Say "any OIDC IdP (Entra, Okta, Keycloak)". |
| "Immutable audit of every significant action" | Audit covers create/update/delete for agents, apps, MCP servers, tenants only. Not tokens, roles, gateway, LLM keys, deploy. |
| Distributed traces / cost attribution per team | Per-run/app/tenant cost and run-step traces; no OpenTelemetry, no per-team attribution. |
| "Any REST endpoint connects immediately", LangGraph/CrewAI | A2A is the agent transport (`a2a_async`, `canvas_a2a`). No generic REST adapter. |
| BYOC / on-prem / air-gap / hybrid split | Architecture intent; only docker-compose manifests exist. Label as "deployment model (design)". |
| Asset registry with owner/env/cost | Agent/MCP/component registries exist; no env/cost fields. |
| Roadmap: Copilot, Evaluation, Marketplace | No matching code. |
| "Auth: bcrypt, JWT HS256" | Also OIDC code flow + PKCE, RS256 external JWT. |
| "Per-app rate limiting" | Per-app/entry-point concurrency caps + per-token RPM in the gate; generic limiter not wired. |

Doc hygiene found along the way:
- `docs/LLM_GATEWAY_DESIGN.md` still says "not yet implemented" (it is live). `docs/INDEX.md` repeats this.
- `docs/CURRENT.md` contains a **live gateway test bearer token in plain text**. Scrub and rotate. (Needs your go-ahead; I did not touch it.)
- `docs/STATUS.md`, `THE-M_CONCEPT.md` have drifted too.

## 3. Real gaps to be honest about (and good "Next" items)

- Gateway is **chat-completions only**: no tools/function calling, multimodal, embeddings, `/models`; cost uses a hardcoded rate table; AppFlow-dispatched calls log 0 tokens; body capture is schema-only.
- MCP: supervisor/registry/credentials are solid, but no stdio/SSE client, no OAuth2 auth, no role-based tool policy, no MCP proxy endpoint for external clients.
- Three unconnected permission systems (Org Roles, gateway clients, MCP) — see `UNIFIED_ROLE_GOVERNANCE_DESIGN.md`.
- PII is 4-category regex; prompt guard is regex, not model-based; no DLP.
- No providers for Bedrock / Azure OpenAI / Vertex natively (OpenAI-compatible endpoints work).

## 4. Narrative and status tags (approved 2026-10-10)

Story: *every organization is about to run hundreds of AIs and needs an operating system for them.*
The OS gives AI four things: **Doors** (LLM Gateway, MCP Gateway, agent endpoints), **Permissions**
(SSO, roles, tenant isolation, per-caller model/tool access), **Safety** (guards, audit, budgets),
**Visibility** (trace, tokens, cost per run).

Every feature carries a tag so the vision stays attractive and honest:
**Live** = shipped and used, **Preview** = works but narrow or not polished, **Roadmap** = designed, not built.
Deployment models (BYOC, on-prem, air-gap) are tagged Roadmap. A "real situations" table
("cron job leaks card numbers to a public model" → what the-M does) carries the pitch.

## 5. Proposed README structure

Target ~250 lines. Move deep architecture to `docs/ARCHITECTURE_OVERVIEW.md`, deployment to `docs/DEPLOYMENT_MODELS.md`.

1. **Hero** — name, one-line: *"Governed gateway and control plane for enterprise AI: LLMs, agents and MCP tools, multi-tenant."*
2. **The problem** (short, 5 bullets): shadow AI, keys sprayed in repos, no audit, no per-role control, no cost attribution.
3. **What the-M is** — one diagram: closed apps / agents / MCP clients → the-M (Gateway · Governance · Orchestration · Observability) → LLM providers / MCP servers / A2A agents.
4. **Three entry doors** (the core pitch, each with "how you connect" in 3 lines):
   - **LLM Gateway** — for closed apps: change `base_url`, get auth, audit, budgets, model allow-list, profile flows.
   - **MCP Gateway** — registry, health, per-app credentials, per-node tool allow-list. *(Role-based tool policy: Roadmap.)*
   - **Agent Platform** — App Canvas, A2A, WS/SSE/WebRTC/voice, durable Temporal runtime, HIL.
5. **Governance & Security** — guards (PII, prompt-injection, AV), SSO/RBAC, org roles, audit log, credential encryption, RLS isolation.
6. **Multi-tenancy** — tenants, quotas/plans, per-tenant IdP and LLM keys, Deploy to Tenant, export/import.
7. **Observability** — run traces per node, tokens/cost, live monitor, Prometheus, debug mode.
8. **Who it solves what for** — table: Platform/Security/FinOps/App teams/SaaS vendors/Regulated orgs → problem → the-M feature.
9. **Feature status matrix** — every capability as `Shipped / Partial / Designed`, linking to the design doc. This is the honesty anchor and replaces vague "Capabilities Summary".
10. **Architecture at a glance** — containers, planes (short; link out).
11. **Roadmap** — MCP role-policy, unified role governance, gateway tools/embeddings, policy engine, OTel, deployment hardening.
12. **Quick start**, **Docs index**, **License**.

## 6. Suggested next steps

1. You approve/adjust section 4.
2. Write the new README + `docs/FEATURES.md` (status matrix with code paths).
3. Fix stale docs (INDEX.md, LLM_GATEWAY_DESIGN status) and scrub the token.
