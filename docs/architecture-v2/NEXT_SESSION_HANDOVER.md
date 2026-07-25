# Session Handover
# Generated: 2026-07-25
# Scope: CLAUDE.md alignment + documentation consolidation

---

## Git State

**Branch:** `main`
**HEAD:** `bc8c461 docs(consolidation): merge go/docs/architecture-v2/ into docs/architecture-v2/`
**origin/main:** `bc8c461` — synchronized, push confirmed
**Working tree:** clean (only `go/them` compiled binary is untracked — do not commit)

### Session commits (newest first)

```
bc8c461  docs(consolidation): merge go/docs/architecture-v2/ into docs/architecture-v2/
5a7f36a  docs: commit untracked wave-review, wave5-plan, handover, and consolidation plan
8d0f20f  docs(guides): align root and Go CLAUDE.md — clear responsibilities, no duplication
4728ef8  cutover(wave5): enable Traefik routing for /api/v1/admin/tokens + /admin/sessions
```

---

## Work Completed This Session

### 1. Root CLAUDE.md alignment
- Added explicit **migration goal**: Python → Go in order (Bridge → Auth → Temporal → remove Python)
- Added **model selection rule**: Opus for planning/architecture, Sonnet for implementation/testing
- Added **session lifecycle / handover procedure** (mandatory, with triggers and 7-step checklist)
- Added **long-answer policy**: write to `docs/architecture-v2/`, return only path + summary
- Added **workflow**: plan → implement → test → commit → report
- Added **tenant-aware design** requirements
- Removed stale "Known State (2026-07-14)" section
- Removed duplicated Go-specific rules that belong in `go/CLAUDE.md`

### 2. go/CLAUDE.md alignment
- Added mandatory reference to `../CLAUDE.md` at the top
- Added **Handler → Service → DAL** architecture rule (no SQL in handlers, no business logic in handlers)
- Added **route ownership verification** rule (Traefik labels alone are not proof; live log hits required)
- Added **integration test** requirement for schema-dependent code
- Fixed broken reference: `docs/architecture-v2/06-domain-model.md` (non-existent) → `docs/architecture-v2/implementation-status.md`
- Removed project-wide rules duplicated from root

### 3. Documentation inventory
- Inventoried all 17 files in `docs/architecture-v2/` and 5 files in `go/docs/architecture-v2/`
- Identified 3 duplicate-name pairs (conflicting content), 1 broken reference, 2 superseded handovers
- Written to: `docs/architecture-v2/DOCUMENTATION_CONSOLIDATION_PLAN.md`

### 4. Documentation consolidation
- **Merged** `implementation-status.md`: go/ version (authoritative, Phase 11c-C + Wave 5) replaced stale docs/ version; unique content from docs/ version preserved
- **Merged** `runbook-reconciler.md`: activation checklist from go/, NotFound scenarios from docs/
- **Merged** `lessons-learned.md`: appended L-01–L-09 from go/ (zero overlap with existing content)
- **Moved** `adr-003-redis-streams-event-delivery.md` from go/docs/ to canonical location (git rename)
- **Moved** `phase-11c-design.md` from go/docs/ to canonical location
- **Archived** `NEXT_SESSION_CODE_RECOVERY_HANDOVER.md` → `archive/` (superseded by Wave 5 handover)
- **Created** `README.md` (directory index), `archive/README.md`
- **Removed** `go/docs/architecture-v2/` entirely

---

## Canonical Documentation Location

**`/opt/docker/them/docs/architecture-v2/`** — single source of truth for Go gateway architecture.
`go/docs/architecture-v2/` has been removed. Do not recreate it.

See `docs/architecture-v2/README.md` for the full file index and update rules.

### Active documents (read these for Go work)

| File | Purpose |
|---|---|
| `implementation-status.md` | Package inventory, route map, test counts — read first for any Go work |
| `lessons-learned.md` | Platform traps, non-obvious behaviors — read before any judgment call |
| `NEXT_SESSION_BRIDGE_HANDOVER.md` | Wave 5 state (still accurate for route ownership) |
| `adr-001-canonical-run-id.md` | Run ID ownership: Go pre-generates, passes to Python |
| `adr-002-reconciler-status-mapping.md` | Temporal → DB status mapping |
| `adr-003-redis-streams-event-delivery.md` | Redis Streams durable event delivery (Phase 11c) |
| `runbook-reconciler.md` | Reconciler operations, activation checklist |
| `schema-migrations.md` | Deferred schema changes (MIG-001, MIG-002) |

---

## Live Migration State

**Current wave:** Wave 5 complete (2026-07-24). Wave 6 not started.

### Go-owned routes (through Traefik, as of 4728ef8)

| Route group | Traefik priority | Notes |
|---|---|---|
| `GET /health/live`, `GET /health/ready` | 130 | Go |
| `GET /api/v1/admin/agents*` | 110 | Go |
| `GET /api/v1/admin/orchestrators*` | 110 | Go |
| `GET /api/v1/admin/applications*` | 110 | Go |
| `GET /api/v1/runs*` | 110 | Go |
| `POST/PUT/DELETE /api/v1/admin/agents*` | 115 | Go |
| `POST/PUT/DELETE /api/v1/admin/orchestrators*` | 115 | Go |
| `POST/PUT/DELETE /api/v1/admin/applications*` | 115 | Go |
| `POST /api/v1/runs/{run_id}/signal` | 115 | Go |
| `GET /apps/{slug}/ws`, `GET,POST /apps/{slug}/sse` | 120 | Go |
| `GET /ws/orchestrate/{app}/{ep}`, `GET,POST /sse/orchestrate/{app}/{ep}` | 120 | Go |
| `ALL /api/v1/admin/tokens*` | 120 | Go — Wave 5 |
| `ALL /api/v1/admin/sessions*` | 120 | Go — Wave 5 |

### Python-owned routes (still to migrate)

| Route | Python file |
|---|---|
| `/api/v1/auth/*` | auth service proxy (`auth_client.py`) |
| `/api/v1/admin/agents/{id}/test` | `admin_agents.py` |
| `/api/v1/admin/agents/{id}/discover` | `admin_agents.py` |
| `/api/v1/admin/agents/{id}/security-scan` | `admin_agents.py` |
| `/api/v1/admin/orchestrators/{name}/test-llm` | `admin_orchestrators.py` |
| `/api/v1/admin/applications/{id}/import` | `admin_applications.py` |
| `/api/v1/admin/applications/{id}/export` | `admin_applications.py` |
| `/api/v1/admin/applications/{id}/restore` | `admin_applications.py` |
| `/api/v1/admin/applications/{id}/bulk-delete` | `admin_applications.py` |
| `/api/v1/admin/applications/{id}/runtime` | `admin_applications.py` |
| `/api/v1/admin/monitoring-config` | `admin_monitoring_config.py` |
| `/api/v1/admin/providers` | `admin_llm_providers.py` |
| `/ws/orchestrate/{name}` (one-segment) | `ws_orchestrator.py` |
| `/ws/dashboard` | `ws_dashboard.py` |

---

## Test Status

### Python suite (last run at 4728ef8)
```
929 passed, 6 skipped, 0 failed
```

### Go unit tests (last run at Wave 5 commit)
```
go test ./...   PASS — 212 unit tests across all packages
```

### Go Wave 5 contract tests (go vs python behavioral parity)
```
40 passed, 0 failed, 0 skipped
```

### Go integration tests
```
go test -tags=integration ./internal/admin/...   PASS (11 tests, live PG + Redis)
```

**Sanity check commands for next session:**
```bash
python3.12 scripts/tests/run_tests.py 01 02 03 04 15   # DB, Redis, auth, bridge health (~15s)
docker ps --filter name=them-go-bridge --format "{{.Names}} {{.Status}}"
```

---

## Deployment State

| Container | State |
|---|---|
| `them-traefik` | Running — port 8088 (host) |
| `them-postgres` | Healthy |
| `them-redis` | Healthy |
| `them-auth-service` | Healthy — port 8701 (internal) |
| `them-bridge` (Python) | Healthy — port 8001 (internal) |
| `them-bridge-2` (Python replica) | Running — profile `replica` |
| `them-go-bridge` | Healthy — port 8002 (internal) |
| `them-go-bridge-2` | Healthy — port 8003 (internal) |
| `them-frontend` | Running — port 3200 (internal) |
| Temporal worker | Running — profile `temporal` |

**Frontend URL:** http://localhost:8088
**Traefik dashboard:** http://localhost:8089
**Temporal UI:** http://10.55.125.43:8088/temporal/

---

## Important Architecture Rules

- **Route ownership proof:** a route is owned by Go only when the handler exists in `go/internal/`, the route is registered in `cmd/them/main.go`, the Traefik label is applied in the compose file, AND a live request through Traefik reaches the Go handler (verified from logs). Traefik labels alone are not proof.
- **Handler → Service → DAL:** no SQL in handlers, no business logic in handlers.
- **Auth never queried directly:** use `internal/auth/` from Go; `app/services/auth_client.py` from Python.
- **DB name/schema is always `them`:** never `odin`.
- **Redis DB index 0** — key prefixes: `them:session:`, `rl:them:`, `them:agents:`, `them:orchestrators:`, `them:bridge:`, `them:dash:`
- **Integration tests required** for any schema-dependent Go code before marking complete.
- **go test -race** required before any PR merge (run in Linux CI — requires GCC not available on Windows).
- **Documentation update in same commit as code change** — stale docs that contradict code are a bug.

## Tenant-Aware Constraints

Every session, run, rate-limit, and runtime gate is scoped to an Application (the tenant boundary):
- DB queries must include `application_id` or `entry_point_id` as appropriate
- Redis keys for per-app state must include the app ID or slug
- Rate limiting and session caps must be per-app, not global
- New routes under `/apps/{slug}/` inherit the app slug from the path

---

## Known Blockers and Unresolved Gaps

| Item | Details |
|---|---|
| `06-domain-model.md` dead link | Fixed in this session — replaced with `implementation-status.md` reference |
| Phase 11c-C staging | Requires explicit approval gate before `RUN_EVENTS_MODE=streams` in staging |
| Phase 11c-D Pub/Sub removal | Requires ≥2 weeks stable in 11c-C + explicit approval |
| MIG-001 (`user_id` on `them.runs`) | Deferred — schema change requires Python coordination |
| MIG-002 (`ep_type` CHECK constraint) | Deferred — Go validates at API boundary until DB constraint added |
| Voice EP | Rejected with 501; implementation not started |
| `vision-agent` | Unhealthy — needs `GOOGLE_MAPS_API_KEY` and `FAL_API_KEY` in `.env` |
| Wave 5 behavioral diffs | Documented and accepted: POST /tokens missing `label` → 422 vs 400; GET /sessions both params → 200 vs 400; error envelope `"detail"` vs `"error"` |

---

## Exact Next Task

**Plan Wave 6** — do not implement yet.

Wave 6 is the next Python Bridge admin route migration. The scope has not been decided.

**Steps for the next session:**

1. Run sanity tests to confirm stack health:
   ```bash
   python3.12 scripts/tests/run_tests.py 01 02 03 04 15
   docker ps --filter name=them-go-bridge --format "{{.Names}} {{.Status}}"
   ```

2. Read the Python admin route files to inventory what remains:
   - `app/routers/admin_llm_providers.py` — LLM provider CRUD
   - `app/routers/admin_monitoring_config.py` — monitoring config CRUD
   - `app/routers/admin_applications.py` — import/export/restore/bulk-delete/runtime (action endpoints)
   - `app/routers/admin_agents.py` — test/discover/security-scan (action endpoints)

3. Use **Opus** to scope Wave 6: identify which of the above routes can be migrated without new Go dependencies, new DB schema changes, or external service calls. Prefer a coherent small batch (2-4 routes) over a large sprawling wave.

4. Write the Wave 6 plan to `docs/architecture-v2/WAVE6_PLAN.md`.

5. Return the plan for review before writing any Go code.

**Wave 6 implementation has NOT started.** No Go code for Wave 6 routes has been written.

---

## First Prompt for Next Session

```
Read first (in this order):
1. /opt/docker/them/CLAUDE.md
2. /opt/docker/them/go/CLAUDE.md
3. /opt/docker/them/docs/architecture-v2/NEXT_SESSION_HANDOVER.md
4. /opt/docker/them/docs/architecture-v2/implementation-status.md

Verify:
- branch is main
- HEAD is bc8c461 or newer
- origin/main is synchronized
- working tree is clean

Run sanity tests:
  python3.12 scripts/tests/run_tests.py 01 02 03 04 15
  docker ps --filter name=them-go-bridge --format "{{.Names}} {{.Status}}"

The task is to plan Wave 6 (not implement). Use Opus.

Read these Python route files:
  app/routers/admin_llm_providers.py
  app/routers/admin_monitoring_config.py
  app/routers/admin_applications.py  (action endpoints only: import/export/restore/bulk-delete/runtime)
  app/routers/admin_agents.py        (action endpoints only: test/discover/security-scan)

Identify which routes can be migrated to Go in Wave 6 without:
- new DB schema changes
- external service calls beyond what Go already makes
- new Redis key prefixes

Propose a focused Wave 6 scope (2–4 routes). Write the plan to:
  docs/architecture-v2/WAVE6_PLAN.md

Return only the plan file path and a one-paragraph summary. Do not write any Go code.
```
