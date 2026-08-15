# Route Ownership Inventory — THEM Python → Go Migration
# Generated: 2026-08-02
# Updated: 2026-08-15 — real Python-OFF UI audit with Go profile active; runs auth mismatch + routing capture bug documented
# Scope: All externally exposed routes. Source of truth: router registrations + Traefik config.
# Do NOT trust this document alone — verify against live logs before any cutover.

---

## How to Read This Table

- **Live Owner**: which service actually handles the request through Traefik today
- **Go impl**: handler exists in `go/internal/` — does NOT mean it is live
- **Migration status**:
  - `complete` — Go handler exists AND Traefik routes traffic to Go
  - `impl-not-cut-over` — Go handler exists, Traefik still sends traffic to Python
  - `partial` — Some methods cut over, others are not
  - `not-started` — Python only, no Go handler
  - `legacy` — should be deprecated instead of migrated

---

## Route Counts Summary

**Current counts (2026-08-15):**

| Category | Count |
|---|---|
| **Total externally exposed routes** | **73** |
| Currently owned by Go (live via Traefik) | 32 |
| Currently owned by Python (live via Traefik) | 41 |
| Implemented in Go but NOT yet cut over | 2 (A2A routes — no Traefik labels) |
| Legacy / deprecation candidates | 4 |

**2026-08-15 update (initial):** Agent actions (discover, test, security-scan) migrated to Go (+3). Go count: 29 → 32. Python auth replaced by Go (`them-auth-go`).

**2026-08-15 update (real Python-OFF audit with Go profile active):**
- Go binary was stale (built 2026-07-28); rebuilt to include Wave 8 handlers.
- `/api/v1/runs` and `/api/v1/runs/{id}`: marked `broken` — Go uses `BearerTenantMiddleware` but admin UI sends session JWT → 401 for all admin dashboard users.
- `/api/v1/runs/stats` and `/api/v1/runs/contexts`: marked `broken` — caught by Go Traefik rule, Go returns 404. Python never handles them.
- **Next task: fix runs auth mismatch** — add JWT super_admin path to Go runs endpoints (same pattern as admin routes). This unblocks the entire Runs section of the admin UI.

---

## Domain: Health

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/health` | ✓ | ✗ | Python (p=90) | not-started | `app/routers/health.py:12` |
| GET | `/health/live` | ✓ | ✓ | **Go** (p=130) | complete | `go/internal/server/server.go:86` |
| GET | `/health/ready` | ✓ | ✓ | **Go** (p=130) | complete | `go/internal/server/server.go:87` |
| GET | `/metrics` | ✗ | ✓ | **Go** (p=130)* | complete | `go/internal/server/server.go:91` |
| GET | `/go-health/*` | ✗ | ✓ | **Go** (p=120, rewrite) | legacy | Traefik rewrite rule only |

**Notes:**
- `/health` (bare) is Python-only and will remain so until all Python routes are gone. No migration needed in isolation — deprecate with Python.
- `/metrics` is Go-only (Prometheus scrape endpoint). Python has no equivalent.
- `/go-health/*` is a backward-compat rewrite alias for `/health/*` in Go. No Python equivalent. Remove when scripts are updated.

---

## Domain: Admin — Agents

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/agents` | ✓ | ✓ | **Go** (p=110) | complete | `go/internal/admin/agents.go:27` |
| POST | `/api/v1/admin/agents` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/agents.go:28` |
| GET | `/api/v1/admin/agents/{id}` | ✓ | ✓ | **Go** (p=110) | complete | `go/internal/admin/agents.go:29` |
| PUT | `/api/v1/admin/agents/{id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/agents.go:30` |
| PATCH | `/api/v1/admin/agents/{id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/agents.go:31` |
| DELETE | `/api/v1/admin/agents/{id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/agents.go:32` |
| POST | `/api/v1/admin/agents/{id}/test` | ✓ | ✓ | **Go** (p=116) | complete | `go/internal/admin/agents.go` |
| POST | `/api/v1/admin/agents/discover` | ✓ | ✓ | **Go** (p=116) | complete | `go/internal/admin/agents.go` |
| POST | `/api/v1/admin/agents/{id}/security-scan` | ✓ | ✓ | **Go** (p=116) | complete | `go/internal/admin/agents.go` |

**✓ FIXED — Traefik UUID regex corrected:**
`them-go-agents-update` rule updated from `[0-9]+` to `[^/]+`.
Agent IDs are UUIDs; `[0-9]+` never matched. PUT/PATCH/DELETE agent writes now correctly route to Go (p=115).
Fix applied in `docker-compose.traefik.yml`. Same fix applied to applications (see Applications domain).
Orchestrators write rule was already correct (`[^/]+`).

---

## Domain: Admin — Orchestrators

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/orchestrators` | ✓ | ✓ | **Go** (p=110) | complete | `go/internal/admin/orchestrators.go:27` |
| POST | `/api/v1/admin/orchestrators` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/orchestrators.go:28` |
| GET | `/api/v1/admin/orchestrators/{name}` | ✓ | ✓ | **Go** (p=110) | complete | `go/internal/admin/orchestrators.go:29` |
| PUT | `/api/v1/admin/orchestrators/{name}` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/orchestrators.go:30` |
| PATCH | `/api/v1/admin/orchestrators/{name}` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/orchestrators.go:31` |
| DELETE | `/api/v1/admin/orchestrators/{name}` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/orchestrators.go:32` |
| POST | `/api/v1/admin/orchestrators/{id}/test-llm` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_orchestrators.py:349` |
| POST | `/api/v1/admin/orchestrators/{id}/test-voice` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_orchestrators.py:373` |
| POST | `/api/v1/admin/orchestrators/{id}/test-tts` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_orchestrators.py:407` |

**Notes:**
- Orchestrators write regex uses `[^/]+` (matches name strings with hyphens) — correct.
- `test-llm`, `test-voice`, `test-tts` are LLM provider connectivity tests. They call external LLM APIs. Not migrated — depend on LLM provider integration that isn't in Go yet.

---

## Domain: Admin — Applications

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/applications` | ✓ | ✓ | **Go** (p=110) | complete | `go/internal/admin/applications.go:27` |
| POST | `/api/v1/admin/applications` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/applications.go:28` |
| GET | `/api/v1/admin/applications/{id}` | ✓ | ✓ | **Go** (p=110) | complete | `go/internal/admin/applications.go:29` |
| PUT | `/api/v1/admin/applications/{id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/applications.go:30` |
| PATCH | `/api/v1/admin/applications/{id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/applications.go:31` |
| DELETE | `/api/v1/admin/applications/{id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/applications.go:32` |
| POST | `/api/v1/admin/applications/{id}/entry-points` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/applications.go:34` |
| PUT | `/api/v1/admin/applications/{id}/entry-points/{ep_id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/applications.go:35` |
| PATCH | `/api/v1/admin/applications/{id}/entry-points/{ep_id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/applications.go:36` |
| DELETE | `/api/v1/admin/applications/{id}/entry-points/{ep_id}` | ✓ | ✓ | **Go**⚠ (p=115) | complete* | `go/internal/admin/applications.go:37` |
| GET | `/api/v1/admin/applications/{id}/export` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_applications.py:806` |
| POST | `/api/v1/admin/applications/import` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_applications.py:827` |
| PUT | `/api/v1/admin/applications/{id}/restore` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_applications.py:866` |
| POST | `/api/v1/admin/applications/bulk-delete` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/applications.go:37` |
| PUT | `/api/v1/admin/applications/{id}/runtime` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/applications.go:43` |
| PUT | `/api/v1/admin/applications/{id}/middleware-wirings` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_applications.py:973` |
| POST | `/api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-llm` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_applications.py:1021` |
| POST | `/api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-voice` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_applications.py:1054` |
| POST | `/api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-tts` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_applications.py:1095` |

**✓ FIXED — Traefik UUID regex corrected:**
`them-go-apps-update` and `them-go-eps-writes` rules updated from `[0-9]+` to `[^/]+`.
Application IDs and entry point IDs are UUIDs; `[0-9]+` never matched.
PUT/PATCH/DELETE application writes and all entry-point writes now correctly route to Go (p=115).
Fix applied in `docker-compose.traefik.yml`.

**Notes on Python-only routes:**
- `export` / `import` / `restore`: portable JSON snapshot for app cloning. Uses `app_compiler.py` (`export_graph`, `compile_graph`, `validate_graph`). These functions must be ported to Go before migration.
- `bulk-delete`: deletes up to 200 apps at once. Flushes Redis orchestrator cache (`_flush_orch_caches`). Requires Redis write access in Go.
- `runtime`: writes `application.runtime_config` JSONB. Flushes Redis EP config cache. Simple write — no crypto.
- `middleware-wirings`: replaces all middleware wiring rows for an app. Flushes `them:mw:chain:{app_id}:*`. Middleware feature is not yet in Go at all.
- `test-llm`, `test-voice`, `test-tts`: live LLM/STT/TTS connectivity tests. Low priority — test endpoints only.

---

## Domain: Admin — Tokens

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/tokens` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/tokens.go:28` |
| POST | `/api/v1/admin/tokens` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/tokens.go:29` |
| GET | `/api/v1/admin/tokens/{token_id}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/tokens.go:30` |
| PATCH | `/api/v1/admin/tokens/{token_id}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/tokens.go:31` |
| DELETE | `/api/v1/admin/tokens/{token_id}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/tokens.go:32` |

---

## Domain: Admin — Sessions

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/sessions` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/sessions.go:23` |
| POST | `/api/v1/admin/sessions/{id}/disconnect` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/sessions.go:24` |

---

## Domain: Admin — LLM Providers

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/llm-providers` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/llm_providers.go:31` |
| POST | `/api/v1/admin/llm-providers` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/llm_providers.go:32` |
| GET | `/api/v1/admin/llm-providers/{id}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/llm_providers.go:33` |
| PATCH | `/api/v1/admin/llm-providers/{id}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/llm_providers.go:34` |
| DELETE | `/api/v1/admin/llm-providers/{id}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/llm_providers.go:35` |
| GET | `/api/v1/admin/llm-providers/routing/config` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/llm_routing.go:27` |
| PUT | `/api/v1/admin/llm-providers/routing/config` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/llm_routing.go:28` |

**Note:** The Traefik rule for full provider CRUD excludes the routing/config path:
`PathPrefix(/llm-providers) && !Path(/llm-providers/routing/config)` — correct, no conflict.

---

## Domain: Admin — Monitoring Config

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/monitoring-config` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/monitoring.go:25` |
| PUT | `/api/v1/admin/monitoring-config` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/admin/monitoring.go:26` |

---

## Domain: Admin — Middleware Definitions

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/middleware-defs` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_middleware.py:74` |
| GET | `/api/v1/admin/middleware-defs/{def_id}` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_middleware.py:80` |
| POST | `/api/v1/admin/middleware-defs` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_middleware.py:85` |
| PATCH | `/api/v1/admin/middleware-defs/{def_id}` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_middleware.py:106` |
| DELETE | `/api/v1/admin/middleware-defs/{def_id}` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_middleware.py:124` |

**Notes:** Middleware definitions CRUD is a simple config table. No crypto, no Redis. Low traffic. Not in any wave plan. Blocked only by wave prioritization, not by technical dependencies.

---

## Domain: Admin — System Agents

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/admin/system-agents` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_system_agents.py:123` |
| PUT | `/api/v1/admin/system-agents` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_system_agents.py:129` |
| POST | `/api/v1/admin/system-agents/{role}/test-llm` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/admin_system_agents.py:166` |

**Notes:** System agents configure built-in roles (summarizer, etc.). Low priority; test endpoint requires LLM Go client.

---

## Domain: Runs

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/api/v1/runs` | ✓ | ✓ | **Go** (p=110) | **complete** | `go/internal/admin/runs.go:30` |
| GET | `/api/v1/runs/{run_id}` | ✓ | ✓ | **Go** (p=110) | **complete** | `go/internal/admin/runs.go:35` |
| POST | `/api/v1/runs/{run_id}/signal` | ✓ | ✓ | **Go** (p=115) | complete | `go/internal/admin/runs.go:47` |
| GET | `/api/v1/runs/stats` | ✓ | ✓ | **Go** (p=110) | **complete** | `go/internal/admin/runs.go:32` |
| GET | `/api/v1/runs/contexts` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/runs.py:187` |
| POST | `/api/v1/runs/bulk-delete` | ✓ | ✓ | **Go** (p=116) | **complete** | `go/internal/admin/runs.go:33` |
| DELETE | `/api/v1/runs/{run_id}` | ✓ | ✓ | **Go** (p=114) | **complete** | `go/internal/admin/runs.go:36` |
| PATCH | `/api/v1/runs/{run_id}/cancel` | ✓ | ✓ | **Go** (p=116) | **complete** | `go/internal/admin/runs.go:34` |
| GET | `/api/v1/runs/{run_id}/tasks` | ✓ | ✓ | **Go** (p=115) | **complete** | `go/internal/admin/runs.go:44` |
| GET | `/api/v1/runs/{run_id}/artifacts` | ✓ | ✓ | **Go** (p=115) | **complete** | `go/internal/admin/runs.go:45` |
| GET | `/api/v1/runs/context/{context_id}/artifacts` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/runs.py:513` |
| GET | `/api/v1/runs/{run_id}/artifacts/{artifact_id}` | ✗ | ✓ | **Go** (direct) | complete | `go/internal/artifacts/handler.go:68` |

**Notes:**
- **Runs READ + WRITE: fully Go-owned as of a6b9953 (2026-08-15). Live Python-OFF verified.**
- `/runs/contexts`: list distinct context sessions. No Go handler; falls through to Python.
- `/runs/context/{context_id}/artifacts`: cross-run artifact lookup. No Go handler; not used by admin UI.
- `/runs/{run_id}/cancel`: DB-only status update (matches Python behavior — no Temporal signal). Temporal cancellation uses the separate `POST /runs/{id}/signal` endpoint.
- `/runs/{run_id}/artifacts/{artifact_id}`: single artifact download — Go-only (mounted at direct path before admin router).

**✓ ROUTING RESOLVED — Runs domain fully migrated (a6b9953, 2026-08-15):**
- Static routes (`/runs/stats`, `/runs/bulk-delete`) registered before `/{run_id}` in chi router — no wildcard shadowing.
- `them-go-admin-reads` Traefik GET rule captures `/runs` and `/runs/{run_id}` (Go handles both).
- Wave 2d (`them-go-runs-signal`): PATCH `/{id}/signal` → Go.
- Wave 2e (`them-go-runs-sub`): GET `/{id}/tasks`, `/{id}/artifacts` → Go.
- Wave 2f (`them-go-runs-cancel`, `them-go-runs-delete`, `them-go-runs-bulk-delete`): PATCH cancel, DELETE, POST bulk-delete → Go.
- All auth mismatch issues were resolved in the Runs READ slice (cf953cf): JWT + RequireSuperAdmin + AdminTenantMiddleware.
- `/runs/contexts` and `/runs/context/{ctx}/artifacts` remain on Python (not used by admin UI; low priority).

---

## Domain: WebSocket — Orchestration

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET (WS upgrade) | `/ws/orchestrate/{app_slug}/{ep_slug}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/ws/handler.go:154` |
| GET (WS upgrade) | `/ws/orchestrate/{name}` | ✓ | ✗ | Python (p=100) | legacy | `app/routers/ws_orchestrator.py:76` |
| GET (WS upgrade) | `/apps/{slug}/ws` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/ws/handler.go:164` |

**Notes:**
- `/ws/orchestrate/{name}` (one-segment) is the legacy playground path. The Go two-segment path `/{app_slug}/{ep_slug}` is the current standard. The legacy path is intentionally kept in Python and is a **deprecation candidate** once the frontend fully uses the two-segment form.

---

## Domain: SSE — Orchestration

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/sse/orchestrate/{app_slug}/{ep_slug}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/sse/handler.go:121` |
| POST | `/sse/orchestrate/{app_slug}/{ep_slug}` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/sse/handler.go:122` |
| GET | `/apps/{slug}/sse` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/sse/handler.go:130` |
| POST | `/apps/{slug}/sse` | ✓ | ✓ | **Go** (p=120) | complete | `go/internal/sse/handler.go:137` |

---

## Domain: Apps — Public Entry Point API

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/apps` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/apps.py:182` |
| GET | `/apps/{slug}` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/apps.py:208` |
| POST | `/apps/{slug}` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/apps.py:240` |
| GET | `/apps/{slug}/tasks/{task_id}` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/apps.py:361` |
| POST | `/apps/{slug}/voice/transcribe` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/apps.py:762` |
| POST | `/apps/{slug}/voice/tts` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/apps.py:812` |

**Notes:**
- `GET /apps` and `GET /apps/{slug}` return public app catalogue info. Simple reads, no auth.
- `POST /apps/{slug}` is the REST fire-and-forget entry point (non-streaming).
- `GET /apps/{slug}/tasks/{task_id}` is the task polling endpoint (pair with REST POST).
- `/voice/transcribe` and `/voice/tts` are voice pipeline endpoints. Not a Wave priority.

---

## Domain: WebRTC

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/apps/{slug}/webrtc/token` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/webrtc.py:94` |

**Notes:** WebRTC token endpoint serves Livekit/WebRTC room tokens for voice EPs. No Go equivalent. No wave plan. Low priority — voice EP is experimental.

---

## Domain: A2A

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET | `/.well-known/agent-card.json` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/a2a_server.py:95` |
| POST | `/a2a` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/a2a_server.py:552` |
| POST | `/a2a/push/{task_id}` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/a2a_server.py:648` |
| POST | `/a2a/{app_slug}` | ✗ | ✓ | Go (direct 8002 only)† | impl-not-cut-over | `go/internal/a2a/server.go:158` |
| GET | `/.well-known/agent.json` | ✗ | ✓ | Go (direct 8002 only)† | impl-not-cut-over | `go/internal/a2a/server.go:159` |

**† INCONSISTENCY — A2A path divergence:**
Python serves `POST /a2a` (no slug) as the global A2A JSON-RPC endpoint.
Go implements `POST /a2a/{app_slug}` (with slug, multi-tenant).
These are **different APIs**. Neither is cut over through Traefik for A2A (no Traefik labels for `/a2a` prefix exist in `docker-compose.traefik.yml`).
The Go A2A server runs on port 8002 but is not live through Traefik.
Go's `/.well-known/agent.json` differs from Python's `/.well-known/agent-card.json` (different file names).
These differences are intentional architectural evolution — the Go A2A is a v2 multi-tenant API.
**Resolution needed before cutover:** decide whether to add a `/a2a` → `/a2a/{default_slug}` shim, or rename the Python endpoint to match the Go path first.

---

## Domain: Dashboard WebSocket

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| GET (WS upgrade) | `/ws/dashboard` | ✓ | ✗ | Python (p=100) | not-started | `app/routers/ws_dashboard.py:158` |

**Notes:** Dashboard WS multiplexes multiple real-time channels (run events, app status, agent status). No Go equivalent. High-complexity — depends on Redis pub/sub fan-out. Likely last to migrate.

---

## Domain: Orchestrator Voice/Transcription (Python-only, legacy prefix)

| Method | Path | Python impl | Go impl | Live owner | Migration | Source |
|---|---|---|---|---|---|---|
| POST | `/api/v1/orchestrators/{name}/transcribe` | ✓ | ✗ | Python (p=100) | legacy | `app/routers/transcription.py:41` |
| POST | `/api/v1/orchestrators/{name}/tts` | ✓ | ✗ | Python (p=100) | legacy | `app/routers/tts.py:53` |

**Notes:** These routes use the global orchestrator `{name}` path (deprecated in favor of app-scoped `/apps/{slug}/voice/*`). Mark as **deprecation candidates**. Remove when voice clients move to `/apps/{slug}/voice/*`.

---

## Complete List of Python-Owned Routes (33 routes)

All routes currently served by Python through Traefik:

| # | Method | Path | Domain | Priority |
|---|---|---|---|---|
| 1 | GET | `/health` | Health | 90 |
| 2 | POST | `/api/v1/admin/agents/{id}/test` | Agents | 100 |
| 3 | POST | `/api/v1/admin/agents/discover` | Agents | 100 |
| 4 | POST | `/api/v1/admin/agents/{id}/security-scan` | Agents | 100 |
| 5 | POST | `/api/v1/admin/orchestrators/{id}/test-llm` | Orchestrators | 100 |
| 6 | POST | `/api/v1/admin/orchestrators/{id}/test-voice` | Orchestrators | 100 |
| 7 | POST | `/api/v1/admin/orchestrators/{id}/test-tts` | Orchestrators | 100 |
| 8 | GET | `/api/v1/admin/applications/{id}/export` | Applications | 100 |
| 9 | POST | `/api/v1/admin/applications/import` | Applications | 100 |
| 10 | PUT | `/api/v1/admin/applications/{id}/restore` | Applications | 100 |
| 11 | POST | `/api/v1/admin/applications/bulk-delete` | Applications | 100 |
| 12 | PUT | `/api/v1/admin/applications/{id}/runtime` | Applications | 100 |
| 13 | PUT | `/api/v1/admin/applications/{id}/middleware-wirings` | Applications | 100 |
| 14 | POST | `/api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-llm` | Applications | 100 |
| 15 | POST | `/api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-voice` | Applications | 100 |
| 16 | POST | `/api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-tts` | Applications | 100 |
| 17 | GET | `/api/v1/admin/middleware-defs` | Middleware | 100 |
| 18 | GET | `/api/v1/admin/middleware-defs/{def_id}` | Middleware | 100 |
| 19 | POST | `/api/v1/admin/middleware-defs` | Middleware | 100 |
| 20 | PATCH | `/api/v1/admin/middleware-defs/{def_id}` | Middleware | 100 |
| 21 | DELETE | `/api/v1/admin/middleware-defs/{def_id}` | Middleware | 100 |
| 22 | GET | `/api/v1/admin/system-agents` | System Agents | 100 |
| 23 | PUT | `/api/v1/admin/system-agents` | System Agents | 100 |
| 24 | POST | `/api/v1/admin/system-agents/{role}/test-llm` | System Agents | 100 |
| ~~25~~ | ~~GET~~ | ~~`/api/v1/runs/stats`~~ | ~~Runs~~ | **Go** |
| 26 | GET | `/api/v1/runs/contexts` | Runs | 100 |
| ~~27~~ | ~~POST~~ | ~~`/api/v1/runs/bulk-delete`~~ | ~~Runs~~ | **Go** |
| ~~28~~ | ~~DELETE~~ | ~~`/api/v1/runs/{run_id}`~~ | ~~Runs~~ | **Go** |
| ~~29~~ | ~~PATCH~~ | ~~`/api/v1/runs/{run_id}/cancel`~~ | ~~Runs~~ | **Go** |
| ~~30~~ | ~~GET~~ | ~~`/api/v1/runs/{run_id}/tasks`~~ | ~~Runs~~ | **Go** |
| ~~31~~ | ~~GET~~ | ~~`/api/v1/runs/{run_id}/artifacts`~~ | ~~Runs~~ | **Go** |
| 32 | GET | `/api/v1/runs/context/{context_id}/artifacts` | Runs | 100 |
| 33 | GET | `/api/v1/orchestrators/{name}/transcribe` *(legacy)* | Voice | 100 |
| 34 | POST | `/api/v1/orchestrators/{name}/tts` *(legacy)* | Voice | 100 |
| 35 | GET | `/apps` | Apps | 100 |
| 36 | GET | `/apps/{slug}` | Apps | 100 |
| 37 | POST | `/apps/{slug}` | Apps (REST) | 100 |
| 38 | GET | `/apps/{slug}/tasks/{task_id}` | Apps | 100 |
| 39 | POST | `/apps/{slug}/voice/transcribe` | Apps/Voice | 100 |
| 40 | POST | `/apps/{slug}/voice/tts` | Apps/Voice | 100 |
| 41 | GET | `/apps/{slug}/webrtc/token` | WebRTC | 100 |
| 42 | GET | `/.well-known/agent-card.json` | A2A | 100 |
| 43 | POST | `/a2a` | A2A | 100 |
| 44 | POST | `/a2a/push/{task_id}` | A2A | 100 |
| 45 | GET (WS) | `/ws/orchestrate/{name}` | WS (legacy) | 100 |
| 46 | GET (WS) | `/ws/dashboard` | Dashboard | 100 |

*Routes 33–34 are deprecation candidates. Routes 8–16 (applications special ops) are the primary Wave 8 target.*

---

## Inconsistencies Found

### 1. ✓ RESOLVED: Traefik UUID Regex Bug (Agents + Applications write rules)

**Affected rules:** `them-go-agents-update`, `them-go-apps-update`, `them-go-eps-writes`

**Was:** All three used `[0-9]+` to match IDs. Agents, applications, and entry points all use UUIDs. The regex never matched — all PUT/PATCH/DELETE for these resources silently fell through to Python.

**Fixed:** All three rules updated to `[^/]+` in `docker-compose.traefik.yml`.
Agents PUT/PATCH/DELETE (3 routes), applications PUT/PATCH/DELETE (3 routes), and entry-point POST/PUT/PATCH/DELETE (4 routes) now correctly route to Go (p=115).

**Orchestrators were unaffected** — they use name slugs and already used `[^/]+`.

### 2. ✓ RESOLVED: Go `GET /api/v1/runs*` Rule Over-Captured Python Routes

**Affected rule:** `them-go-admin-reads` (Wave 1b, priority 110)

**Was:** `PathPrefix(/api/v1/runs) && Method(GET)` captured all GET requests under `/runs`, including 5 Python-only routes that Go cannot serve — those requests were returning 404 from Go.

**Fixed:** Rule narrowed to `Path(/api/v1/runs) || PathRegexp(^/api/v1/runs/[^/]+$$)` — matches only the list endpoint and single-ID endpoint. Python-only sub-paths (`/stats`, `/contexts`, `/{run_id}/tasks`, `/{run_id}/artifacts`, `/context/{ctx}/artifacts`) fall through to Python.
Fix applied in both `docker-compose.traefik.yml` and `docker-compose.yml`.

### 3. A2A Path Divergence: Python `/a2a` vs Go `/a2a/{app_slug}`

Go implements a newer multi-tenant A2A API (`POST /a2a/{app_slug}`) that is incompatible with the Python A2A endpoint (`POST /a2a`). There are no Traefik labels for the A2A prefix in `docker-compose.traefik.yml` — all A2A traffic goes to Python through the `them-a2a` base rule.

The Go A2A server is mounted and running on port 8002 but is unreachable through Traefik.

**Resolution needed:** Either add a compatibility shim, or cut over A2A to Go under the new path and update all clients.

### 4. Agent-Card Path Divergence: Python `/.well-known/agent-card.json` vs Go `/.well-known/agent.json`

These are different file names. Python serves the A2A spec-compliant `agent-card.json`. Go serves `agent.json`. Clients expecting one path will not find the other. This appears to be an unintentional divergence. Fix: align both to `agent-card.json` before cutover.

### 5. Wave 2a/2c Traefik Labels Are in `docker-compose.traefik.yml` Not `docker-compose.yml`

The base `docker-compose.yml` contains the Wave 1b, Wave 5, Wave 6, Wave 7 Go routing rules directly on `them-go-bridge`. The Wave 2–4 rules are in the separate `docker-compose.traefik.yml` overlay. This split may cause confusion: the Hetzner/production deployment may use different compose files. Verify which compose files are active on each environment.

---

## Recommended Migration Waves

### Context

- Waves 1–7 are complete.
- Routing fix commit applied: UUID regex corrected, runs GET rule narrowed.
- 46 Python-owned routes remain (including 4 legacy/deprecation candidates).
- The Traefik UUID regex bug has been fixed — agents and applications writes now truly route to Go.

### Wave Groupings by Domain Cohesion

| Wave | Routes | Domain | Dependencies | Difficulty |
|---|---|---|---|---|
| **Wave 8** | Applications special ops: export, import, restore, bulk-delete, runtime | Applications | Port `app_compiler.py` graph logic to Go; Redis flush; no crypto | **Medium** |
| ~~Wave 9~~ | ~~Remaining runs: stats, contexts, cancel, bulk-delete, tasks, artifacts~~ | ~~Runs~~ | ~~complete~~ | ~~complete~~ |
| Wave 10 | Middleware defs CRUD + app middleware-wirings | Middleware | New Go package; Redis flush | Low-Medium |
| Wave 11 | System agents CRUD | System Agents | Config table (already Go) | Low |
| Wave 12 | Apps catalogue: GET /apps, GET /apps/{slug}, POST /apps/{slug} (REST) | Apps/REST | Admission gate; task store | Medium |
| Wave 13 | A2A cutover + agent-card alignment | A2A | Path unification decision | Medium |
| Wave 14 | Dashboard WS `/ws/dashboard` | Dashboard | Redis pub/sub fan-out; complex | High |
| Wave 15 | Legacy WS `/ws/orchestrate/{name}` deprecation | WS Legacy | Requires frontend migration | Low |
| Defer | All `test-*`, voice/TTS, WebRTC | Test endpoints / Voice | LLM Go client; audio infra | High |

### Bug Fixes Required Before Any New Wave

1. **Traefik UUID regex** (affects agents + applications writes — Wave 2 was never truly live)
2. **Runs GET partial coverage** (routes 25–32 return 404 from Go)

---

## Recommended Next Wave: Wave 8 — Applications Special Operations

**Recommended scope:**
```
GET  /api/v1/admin/applications/{id}/export
POST /api/v1/admin/applications/import
PUT  /api/v1/admin/applications/{id}/restore
POST /api/v1/admin/applications/bulk-delete
PUT  /api/v1/admin/applications/{id}/runtime
```

**Reason:** These 5 routes complete the Applications domain in Go. They are all called from the same admin UI page (canvas builder). The `bulk-delete` and `runtime` routes are simple SQL+Redis with no crypto dependency. The `export/import/restore` trio requires porting `app/services/app_compiler.py` (`export_graph`, `compile_graph`, `validate_graph`) — this is the wave's main work, but it is self-contained and has no external service dependencies.

Completing Wave 8 also enables fixing the Traefik UUID regex bug safely: once all application routes are in Go, the full applications domain can be cut over with a single Traefik rule change.

**Routing bugs already fixed (pre-Wave 8):**
1. Traefik UUID regex for agents + applications write rules — fixed.
2. `GET /api/v1/runs*` Traefik rule narrowed to only implemented Go paths — fixed.

**Excluded from Wave 8:**
- `middleware-wirings` (requires Go middleware system — not built)
- `test-llm`, `test-voice`, `test-tts` (require LLM Go client — not in scope)

---

## Source Files Referenced

| File | Role |
|---|---|
| `app/routers/admin_agents.py` | Python agents CRUD + test/discover/security-scan |
| `app/routers/admin_orchestrators.py` | Python orchestrators CRUD + test endpoints |
| `app/routers/admin_applications.py` | Python applications CRUD + export/import/restore/bulk-delete/runtime/mw-wirings/test |
| `app/routers/admin_tokens.py` | Python tokens CRUD |
| `app/routers/admin_sessions.py` | Python sessions list/disconnect |
| `app/routers/admin_llm_providers.py` | Python LLM providers CRUD + routing config |
| `app/routers/admin_monitoring_config.py` | Python monitoring config CRUD |
| `app/routers/admin_middleware.py` | Python middleware defs CRUD |
| `app/routers/admin_system_agents.py` | Python system agents CRUD |
| `app/routers/runs.py` | Python runs API |
| `app/routers/ws_orchestrator.py` | Python WS handler (legacy one-segment) |
| `app/routers/ws_dashboard.py` | Python dashboard WS |
| `app/routers/apps.py` | Python public apps API + voice |
| `app/routers/a2a_server.py` | Python A2A JSON-RPC server |
| `app/routers/webrtc.py` | Python WebRTC token endpoint |
| `app/routers/transcription.py` | Python legacy transcription endpoint |
| `app/routers/tts.py` | Python legacy TTS endpoint |
| `app/routers/health.py` | Python health endpoints |
| `app/main.py` | Python router registration + prefix mapping |
| `go/internal/admin/agents.go` | Go agents handler |
| `go/internal/admin/orchestrators.go` | Go orchestrators handler |
| `go/internal/admin/applications.go` | Go applications handler |
| `go/internal/admin/tokens.go` | Go tokens handler |
| `go/internal/admin/sessions.go` | Go sessions handler |
| `go/internal/admin/llm_providers.go` | Go LLM providers handler |
| `go/internal/admin/llm_routing.go` | Go LLM routing config handler |
| `go/internal/admin/monitoring.go` | Go monitoring config handler |
| `go/internal/admin/runs.go` | Go runs handler |
| `go/internal/admin/router.go` | Go admin router wiring |
| `go/internal/ws/handler.go` | Go WS handler |
| `go/internal/sse/handler.go` | Go SSE handler |
| `go/internal/a2a/server.go` | Go A2A server |
| `go/internal/artifacts/handler.go` | Go artifact download |
| `go/internal/server/server.go` | Go server mount points |
| `go/cmd/them/main.go` | Go binary entrypoint |
| `docker-compose.yml` | Base Traefik rules (Python + some Go) |
| `docker-compose.traefik.yml` | Wave 2–7 Go Traefik rules (overlay) |
