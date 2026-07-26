# Session Handover
# Generated: 2026-07-26
# Scope: Tenant Foundation Gate complete — Wave 7 Phase 3 has not started

---

## Git State

**Branch:** `main`
**HEAD:** `fb99eb2 docs(tenant): Tenant Foundation Gate — ownership decisions and Wave 7 impact`
**origin/main:** synchronized — all commits pushed successfully (`44208ee..cc03e2f`).
**Working tree:** clean (only `go/them` compiled binary is untracked — do not commit)

### Commits since Wave 6 base (newest first)

```
fb99eb2  docs(tenant): Tenant Foundation Gate — ownership decisions and Wave 7 impact
6637887  docs(wave7): Phase 2 implementation report, lessons-learned, TEST_INDEX, handover
dc391b7  feat(admin/service): Wave 7 Phase 2 — LLMProviderService + unit tests
9df65cd  feat(admin/dal): Wave 7 Phase 2 — LLM provider DAL and interface
44208ee  feat(crypto): Wave 7 Phase 1 — Go Fernet compatibility package
```

Wave 6 base: `f4faa06`

---

## Push Status

**Pushed successfully.** All commits delivered to `origin/main` (`44208ee..cc03e2f`).
Branch is up to date with `origin/main`.

---

## Wave 7 Status

| Phase | Description | Status | Commit |
|---|---|---|---|
| Phase 1 | Fernet crypto package (`go/internal/crypto/`) | **COMPLETE** | `44208ee` |
| Phase 2 | LLM provider DAL + service layer | **COMPLETE** | `9df65cd` + `dc391b7` |
| Tenant Gate | Tenant foundation decisions | **COMPLETE** | `fb99eb2` |
| Phase 3 | HTTP handlers + Traefik cutover | **NOT STARTED** — awaiting Go-Native Engineering Gate |

---

## Tenant Foundation Decisions Summary

Full document: `docs/architecture-v2/TENANT_FOUNDATION_DECISIONS.md`

### Ownership confirmed

| Resource | Ownership | Impact |
|---|---|---|
| `them.llm_providers` | **Platform-global** | No `tenant_id` column. UNIQUE(name) correct as-is. |
| `them.config['llm_routing']` | **Platform-global** | Wave 6 Go routes correct. No changes needed. |
| `them.config['monitoring']` | **Platform-global** | Wave 6 Go routes correct. No changes needed. |
| `them.applications` | Tenant-owned | `tenant_id` column to be added in dedicated tenant wave. |
| `them.agents`, `them.orchestrators` | Tenant-owned (future) | Currently platform-global; dedicated migration wave. |
| `them.access_tokens` | Tenant-owned (future) | `tenant_id` to be added in tenant wave. |

### Wave 7 Phase 2 — no tenant changes required

The DAL (`go/internal/admin/dal/llm_providers.go`) and service layer
(`go/internal/admin/service/llm_providers.go`) are correct as committed:
- No `tenant_id` parameter — correct for platform-global resource.
- `UNIQUE(name)` at platform scope — correct.
- `THE_M_SECRET_KEY` encryption — correct for platform credentials.
- **Phase 2 requires no rework.**

### Wave 7 Phase 3 — tenant-safe, not yet started

The 5 LLM provider CRUD handlers will operate on a platform-global table. They require no
`tenant_id` parameter and are mounted behind `RequireSuperAdmin` (Platform Admin only).
Phase 3 is architecturally safe to implement once the Go-Native Engineering Gate is complete.

**Phase 3 must not be started before the Go-Native Engineering Gate result.**

### 8 open tenant decisions (see TENANT_FOUNDATION_DECISIONS.md §9)

| # | Question |
|---|---|
| O-01 | Agents/orchestrators: tenant-owned or platform-global templates with overrides? |
| O-02 | `access_tokens.tenant_id`: direct column or inferred through orchestrator chain? |
| O-03 | Billing model (per-run, per-token, per-session, subscription) → drives `run_usage` scoping. |
| O-04 | Auth service users: per-tenant namespaces or platform-global with tenant memberships? |
| O-05 | Redis isolation: key-prefix-per-tenant (Tier 2) or shared with DB row-level only (Tier 0)? |
| O-06 | Tenant Portal: separate deployment or filtered view of existing admin UI? |
| O-07 | Tier 1 dedicated Temporal workers: at first paying customer, or at N tenants? |
| O-08 | Per-tenant LLM key override: row in `them.llm_providers` (add `tenant_id`) or separate `them.tenant_llm_providers` table? |

These open decisions must be resolved before the tenant migration wave begins. They do not
block Wave 7 Phase 3 or the Go-Native Engineering Gate.

---

## Live Traefik Route Ownership (unchanged)

### Go-owned routes

| Router name | Rule | Priority | Owner |
|---|---|---|---|
| `them-go-health-sub` | `PathPrefix(/health/live) \|\| PathPrefix(/health/ready)` | 130 | Go |
| `them-go-admin-reads` | `PathPrefix(/api/v1/admin/agents\|orchestrators\|applications) \|\| PathPrefix(/api/v1/runs) && Method(GET)` | 110 | Go |
| `them-go-tokens` | `PathPrefix(/api/v1/admin/tokens)` | 120 | Go (Wave 5) |
| `them-go-sessions` | `PathPrefix(/api/v1/admin/sessions)` | 120 | Go (Wave 5) |
| `them-go-monitoring-config` | `Path(/api/v1/admin/monitoring-config)` | 120 | Go (Wave 6) |
| `them-go-llm-routing-config` | `Path(/api/v1/admin/llm-providers/routing/config)` | 120 | Go (Wave 6) |

### Python still owns

| Route | Python file |
|---|---|
| `/api/v1/admin/llm-providers` (CRUD — 5 routes) | `admin_llm_providers.py` — Wave 7 Phase 3 (not started) |
| `POST/PUT/DELETE /api/v1/admin/agents*` | `admin_agents.py` |
| `POST/PUT/DELETE /api/v1/admin/orchestrators*` | `admin_orchestrators.py` |
| `POST/PUT/DELETE /api/v1/admin/applications*` | `admin_applications.py` |
| `/api/v1/auth/*` | auth service proxy |
| `/ws/dashboard` | `ws_dashboard.py` |
| `/ws/orchestrate/{name}` | `ws_orchestrator.py` |

---

## Test State (end of Phase 2)

```
go test ./...                          23 packages PASS, 0 failures
go test -race ./internal/admin/...     PASS, 0 data races
go test -tags=integration ./internal/admin/dal/...   11/11 PASS (live Postgres)
go test ./internal/crypto/...          32/32 PASS
python3.12 scripts/tests/run_tests.py 01 02 03 04 15   55/55 PASS
```

---

## Hard Constraints That Must Remain in Force

1. No plaintext API key may be returned, logged, embedded in errors, or persisted unencrypted.
2. WARN logs may contain provider ID and error category only — never plaintext, ciphertext, or key material.
3. `api_key_encrypted` DB column must never appear in any JSON response field.
4. `THE_M_SECRET_KEY` is validated at startup (non-empty, non-default) — do not remove.
5. Plaintext bytes must be zeroed immediately after masking.
6. `APIKeyPresent` flag must be set correctly by the handler — absence and null are distinct.
7. No SQL outside DAL. No crypto inside handlers.
8. All list endpoints return `[]` not `null`.
9. `go test ./...` must pass before every commit.
10. `TEST_INDEX.md` must be updated in the same commit as any test change.
11. LLM providers are platform-global — no `tenant_id` parameter in any Wave 7 handler.
12. Wave 7 Phase 3 handlers must be classified as Platform control-plane API in any catalogue.

---

## Exact Next Task

**Go-Native Engineering Gate** — before Wave 7 Phase 3 handlers are written.

This gate evaluates whether Go is ready to own the LLM provider CRUD routes in production.
It must be planned and completed as a separate task before any Phase 3 implementation begins.

**Do not implement handlers. Do not add Traefik labels. Do not begin cutover.**

Once the gate result is returned:
- If approved: proceed to Wave 7 Phase 3 (handlers + Traefik cutover).
- If conditions: complete the conditions first, then Phase 3.
- If rejected: document the blockers; do not proceed.

---

## First Prompt for Next Session (Go-Native Engineering Gate)

```
Read first, in this exact order:
1. /opt/docker/them/CLAUDE.md
2. /opt/docker/them/go/CLAUDE.md
3. /opt/docker/them/docs/architecture-v2/NEXT_SESSION_HANDOVER.md
4. /opt/docker/them/docs/architecture-v2/TENANT_FOUNDATION_DECISIONS.md
5. /opt/docker/them/docs/architecture-v2/WAVE7_IMPLEMENTATION_REPORT.md

Verify:
- branch is main
- HEAD is fb99eb2 or newer
- origin/main is synchronized
- working tree is clean

Use Opus.

This is a gate task only. Do not implement handlers. Do not add Traefik labels.
Do not begin Wave 7 Phase 3.

Evaluate whether the Go gateway is ready to own the LLM provider CRUD routes
in production. Consider:

1. Operational readiness: health checks, startup validation, graceful shutdown,
   structured logging, Prometheus metrics — are they present and sufficient?

2. Error handling completeness: does the existing admin handler pattern handle
   all failure modes correctly? Are 500 responses safe (no internal detail leaked)?

3. Security posture: are the security constraints from WAVE7_IMPLEMENTATION_REPORT.md
   and TENANT_FOUNDATION_DECISIONS.md enforced by the existing infrastructure, or
   do they require handler-level enforcement in Phase 3?

4. Observability gap: when a Phase 3 handler serves a request, will there be
   enough signal (logs, metrics, traces) to debug a production incident?

5. Rollback confidence: is the Traefik rollback procedure (remove label, restart
   Go bridge) fast enough and reliable enough for a production cutover?

Write the gate result to:
docs/architecture-v2/GO_NATIVE_ENGINEERING_GATE.md

The document must end with:
- gate result: APPROVED / APPROVED WITH CONDITIONS / BLOCKED
- if conditions: list each condition with the file and change required
- if blocked: list each blocker with severity and remediation
- whether Wave 7 Phase 3 may proceed
- exact first step of Phase 3 once approved

Return only: document path, gate result, and whether Phase 3 may proceed.
Stop after the gate. Do not implement anything.
```
