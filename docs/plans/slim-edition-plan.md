---
title: the-M Slim — AI Control Tower Edition
type: plan
status: draft
last_verified: 2026-11-04
---

# the-M Slim — AI control tower, simple setup

## 1. Vision
Companies point their apps/scripts at the-M instead of at OpenAI/Anthropic directly. the-M decides
**who may call which model**, **scans for PII**, **tracks tokens and cost**, and shows it all in a dashboard.
One machine, one install command, ~7 containers. Same product as full the-M: advanced features
(orchestration/App Canvas, events/ClickHouse, SSO, file security, voice) can be **switched on later**
without reinstalling or migrating data.

## 2. Principles
1. **One repo, one codebase, one set of images.** Slim is a *distribution* (compose + installer + defaults), not a fork.
2. **Modules.** Everything beyond the core is a module with three parts: a compose overlay, a config flag, a UI/API gate. Enabling later = add overlay + flip flag.
3. **Defaults decide edition.** `THE_M_EDITION=slim|full`; per-module flags override. Full the-M behaviour is unchanged.
4. **Customers never build.** Slim ships pre-built, version-tagged images.
5. **Upgradeable and recoverable.** Auto-migrate on start, one-command backup/restore, tested upgrade + rollback.

## 3. Capability modules

| Module | Contains | Slim | Enable later by |
|---|---|---|---|
| **core** | traefik, postgres, redis, auth-go, bridge, frontend | always on | — |
| **gateway** (part of core) | LLM gateway, policies/allow-lists, budgets, usage/cost, request log, providers/keys, **inline PII scan**, users | on | — |
| **sso (OIDC)** | built into auth-go (any customer IdP: Entra, Okta, Google…) | **on** (configured per tenant, no extra container) | — |
| **tools** | mcp-service: MCP server registry + health monitor (+ tool execution for orchestration) | **on** (registry/health); governed tool-call proxy = future gateway feature | — |
| **orchestration** | Temporal, go-worker, dag-worker(+debug), agent-runtime → App Canvas, agents, chat runs | **off** | `modules/orchestration.yml` + `THE_M_MODULE_ORCHESTRATION=true` |
| **events** | clickhouse, redis-events, event-writer | **off** (Postgres only) | `modules/events.yml` + `THE_M_EVENTS_GATEWAY=dual` |
| **file-security** | minio, clamd, middleware-worker, security-agent | off | `modules/file-security.yml` |
| **bundled-idp** | keycloak — only a test/bundled IdP for customers without one; not the SSO feature | off | `modules/bundled-idp.yml` |
| **voice** | livekit, livekit-agent | off | `modules/voice.yml` |

Never packaged: demo/test agents, product-site.

## 4. Package layout (new, additive)
```
deploy/slim/
  docker-compose.yml        # core, pre-built images, memory limits
  modules/*.yml             # overlays per module
  .env.example
  install.sh                # secrets → .env → pull → up → wait healthy → print URL + admin login
  upgrade.sh / backup.sh / restore.sh
  README.md                 # 1-page customer guide
```
Dev/full compose files stay as they are. CI builds and tags images (`them-bridge:1.x`, …) and publishes to a registry (**decision needed**, see §8).

## 5. Technical changes

**A. Edition/module gating**
- Config: `THE_M_EDITION`, `THE_M_MODULE_<NAME>`; resolved once at startup into an `edition` package; `GET /api/v1/edition` returns enabled modules.
- API: routes of disabled modules answer a clear `404 module_disabled` (not a crash, not a 500). Slim bridge starts with `TEMPORAL_ENABLED=false`.
- UI: nav and pages for disabled modules are hidden from `/edition`; deep links show "module not enabled".

**B. Gateway features for Slim**
- Inline PII scan on gateway calls: reuse `go/internal/middleware/pii/` in `llmgateway/service.go`; modes `off | flag | mask | block`, set in the gateway policy (the policy fields `capture_pii_decisions` exist but are unused).
- Correct cost: replace the static rate card with DB `model_pricing`/updatable table; add token budgets and per-client budgets (today only a tenant-wide monthly USD cap).
- Usage written even on client disconnect (gateway gap 3).
- Retention for `gateway_requests` (+ bodies) on by default with sane days.

**C. Install & bootstrap**
- Idempotent migration runner on startup (records in `them.schema_migrations`), replacing hand-applied SQL (backlog F-35/F-36).
- First-boot bootstrap: default tenant, admin user (password printed once), default policy.
- TLS: Traefik with customer cert or Let's Encrypt; ports 80/443 only.

**D. Hardening before shipping** (from `code-findings-backlog.md`): F-32 superuser DB creds in services, F-56/F-67 unauthenticated internal APIs, F-68 A2A tenant checks, F-70/F-57/F-48 SSRF (gateway `base_url`), gateway gaps 6/8/9, F-136 policy fields stored-but-unenforced.

**E. Upgrade path slim → full modules**
- Orchestration: Temporal uses the same Postgres (own DBs) → overlay up, flip flag, restart bridge. No data move.
- Events: overlay up, flip mode to `dual`. History before that stays in Postgres only.

## 6. Phases and acceptance

| Phase | Work | Done when |
|---|---|---|
| **0 Spike** (1–2 days) | Throwaway stack: bridge with Temporal off, slim container set; call gateway; list what breaks (UI, startup, health) | Written list of blockers; go/no-go for 7 containers |
| **1 Package** | `deploy/slim/` compose + install/upgrade/backup scripts, pre-built images in CI, auto-migrate, bootstrap | Fresh VM → first gateway call in <10 min; `docker stats` RAM measured and recorded |
| **2 Gating** | `edition` package, `/edition` API, 404 for disabled modules, UI nav gating | Slim UI shows only gateway pages; full mode unchanged (existing tests pass) |
| **3 Gateway value** | PII inline, cost/pricing fix, token + per-client budgets, retention | Go tests + live smoke: PII masked/blocked, budget blocks at limit, cost matches rate table |
| **4 Hardening** | Backlog items in §5D, backup/restore test, upgrade + rollback test | Upgrade from N-1 to N on a populated DB works; restore works |
| **5 Modules** | Overlays for tools/orchestration/events; enable-later test | Slim install → enable orchestration → App Canvas run works; same for events |

Each phase: Go tests + `go/TEST_INDEX.md`, owning-doc updates, small commits (repo rules).

## 7. Operational responsibilities (decide before first customer)
Shipping a package to customer machines creates ongoing duties: release/versioning policy, how customers get updates, security-patch SLA, support/diagnostics (`diagnose.sh` bundle without secrets), basic observability (health endpoint + metrics, log level), backup guidance, and a documented build/test/release pipeline (SDLC). These are scoped in Phase 1/4 but need an owner.

## 8. Decisions (2026-11-04)
1. **Registry:** private OCI registry (recommend GHCR, repo is on GitHub) with per-customer pull tokens; images versioned, signed (cosign), SBOM + Trivy scan in CI; **offline bundle** (`docker save` tarball + checksums) for air-gapped on-prem.
2. **TLS:** customer-provided cert (default for on-prem) or ACME/Let's Encrypt when a public DNS name exists; self-signed only for a trial, with a warning.
3. **Security profile** `THE_M_SECURITY_PROFILE=dev|standard|strict`: dev = off (HTTP, relaxed cookies, default policies); standard = TLS, secure cookies, rate limits, PII `flag`, no default creds (default for installs); strict = adds PII `block`, SSO required, request-body capture off, audit log on, internal service auth. Each control stays individually overridable.
4. **MCP in Slim:** registry + health monitor on. MCP has no consumer in the direct gateway path today (gateway ignores `tools`, gap 7); an MCP tool-call governance proxy is a candidate Phase 6 feature. The ClamAV/security-agent stack scans artifact files, not MCP calls — it stays a module.
5. **Support/update model (industry standard):** semver releases; channels `stable` (default) and `early`; support current + previous minor; security advisories + patch SLA by severity (to be set); signed images; `upgrade.sh` pins versions with auto-backup and rollback; `diagnose.sh` redacted support bundle; opt-in version-check/telemetry; health endpoint + metrics for the customer's monitoring. Kubernetes/Helm chart later, only if customers ask.
6. Edition naming: "the-M Slim".

## 9. Verified facts this plan relies on (2026-11-04)
- Direct gateway path calls the provider from the bridge, no Temporal (`llmgateway/service.go`).
- Bridge has a `TEMPORAL_ENABLED=false` branch (`cmd/them/main.go:222-235`); chat-run completion on it is **not verified** (Phase 0).
- Each dag-worker process polls `canvas-dag-nodes` + one AppFlow queue (`cmd/dag-worker/main.go:253`).
- PII scanner exists but is not wired into `llmgateway/`.
- Events: `postgres` default, `dual` live, `clickhouse` blocked for gateway; nothing reads ClickHouse; audit/bodies have no producer.
- Not verified: frontend dependence on App Canvas/Temporal endpoints; effort of auto-migrate; RAM of the slim set.

## 10. Phase 0 spike results (2026-11-04, partial)
Isolated stack `slim_spike` (pg, redis, auth, bridge; own network, localhost ports 18xxx, throwaway secrets; dev stack untouched).
- **Bridge with `TEMPORAL_ENABLED=false` panics at startup** (`execution.NewLifecycle: temporal must not be nil`, `lifecycle.go:131`). The "Go-inline orchestration path" log line is stale. The rest of the code is already nil-safe (`lifecycle.go:763,841` check; `main.go` guards `temporalCli != nil`). A scratch patch removing that one panic made the bridge start and mount the LLM gateway. **Real fix = remove the panic and return `module_disabled` for run entry points** (Phase 2).
- **Fresh install is impossible today**, confirmed: a new Postgres is empty; roles `them_app`, `them_admin` and `them_owner` exist only by hand (init script only grants on DB); no admin/tenant bootstrap exists; `auth_service.roles` and `them.schema_migrations` hold reference data that must be seeded. Spike worked around it with a read-only `pg_dump --schema-only` of the live DB + copied `roles`/`schema_migrations` rows.
- Bridge also logs ERROR "schema BEHIND the code" when `schema_migrations` is empty (log-only unless `THE_M_REQUIRE_MIGRATIONS=true`).
- **Gateway works with Temporal off (verified 2026-11-04, patched bridge):** login into the bootstrap tenant, create gateway client, `POST /{tenant}/llm/v1/chat/completions` with `mock-model-v1` returned 200 and wrote a `gateway_requests` row (200, status ok; mock reports 0 tokens). `GET /admin/applications` still answers 200 with the module off (no `module_disabled` yet).
- **New bug F-138:** a client created through the API stays `config_status='unknown'` and the gateway rejects it (403). Spike set it `valid` by SQL.
- **More install gaps (F-139):** login needs a tenant membership even for super_admin, and the `default` bootstrap tenant row comes from migration data, not schema.
- **RAM (idle):** pg 55 MB, redis 4 MB, auth 6 MB, bridge 9 MB = ~74 MB for the 4 backend containers. Frontend + Traefik not measured yet.
- **PII:** not wired into the gateway (confirmed by design; the spike prompt contained a fake card number and passed through untouched).
- **Verdict: GO** for the slim container set on the backend side. Remaining unknowns: frontend dependence on App Canvas/Temporal endpoints, and RAM with frontend + Traefik.

## 11. Phase 1 progress (2026-11-04)
Done and verified on an empty Postgres (bridge started with `THE_M_REQUIRE_MIGRATIONS=true`, `TEMPORAL_ENABLED=false`):
- `scripts/gen_baseline.sh` writes `deploy/slim/db/` (100_schema.sql from the live DB, 200_seed.sql with only the global reference rows, `BASELINE_VERSION` = 129). Re-run it when the baseline should move forward.
- `deploy/slim/migrate.sh` (runs in a `postgres:16-alpine` one-shot): creates roles `them_owner/them_app/them_admin`, applies the baseline to an empty DB, applies `db/NNN_*.sql` newer than the baseline and not yet in `schema_migrations`, creates the first admin (+ membership in the bootstrap tenant) when `ADMIN_PASSWORD` is set. Idempotent: second run changes nothing.
- Fixed: F-138 (new gateway clients valid), F-140 (bridge without Temporal).
- Result: login -> create client -> gateway call with the new token -> `gateway_requests` row, with no manual SQL.
Still to do in phase 1: `deploy/slim/docker-compose.yml` (core + `migrate` service), `install.sh` (secrets, .env, pull, up, print URL + admin login), Traefik/TLS, pre-built images in CI, backup/upgrade scripts, a CI check that the baseline + newer migrations still equals the live schema.
