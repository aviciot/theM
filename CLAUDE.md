# the-M — Claude Session Guide
# multi-agent orchestration platform
# Last updated: 2026-08-15

---

## Read These First

Docs were reset 2026-10-24 (plan: `docs/plans/docs-reboot-plan.md`). Old docs are in `docs/_archive/` — hints only, never ground truth.

1. `docs/INDEX.md` — generated; find the doc that `owns` the code you are touching.
2. `docs/CURRENT.md` — current state, next task, blockers. Open code bugs found while documenting: `docs/plans/code-findings-backlog.md` (add new ones there).
3. Reference docs when relevant: `docs/reference/schema.md` (DB), `docs/reference/redis.md` (Redis), `docs/guides/a2a-test-agents.md`, `docs/architecture/auth-and-roles.md`, `docs/lessons/` (one file per area — read the matching one before any judgment call), `scripts/tests/INDEX.md` (tests), `docs/guides/local-test-environment-runbook.md` (Docker/deploy/containers).

For Go work, also read `go/CLAUDE.md` — it governs everything under `go/`.

**Before Docker, deployment, environment, or container recreation work, read `docs/guides/local-test-environment-runbook.md`. Never commit or print real secrets.**

---

## This Project

**the-M** is a multi-agent orchestration platform. Fully isolated stack — own Postgres, own Redis, own Docker network.

Brand rules: UI/docs say **the-M**. Code identifiers use **them** / **THE_M_** (no exceptions).
DB name and schema: **them** — never `odin`.

---

## Model Selection

- **Opus** — architecture decisions, migration planning, wave scoping, complex trade-offs
- **Sonnet** — implementation, testing, debugging, routine changes

---

## Answering Style

Keep answers clear, friendly, and short — no walls of text. Get to the point, use plain language, and add a small example when it helps the user understand faster.

If something genuinely needs a long write-up (a detailed review, analysis report, or migration plan), put the details in a Markdown file under `docs/` instead of dumping it in chat — then just give the file path and a one-paragraph summary in chat.

---

## Workflow

Every task follows this order:

1. **Plan** — read relevant docs, design the change, confirm scope before writing code
2. **Implement** — one focused subsystem; do not widen scope mid-task
3. **Test** — run the applicable test suite; zero new failures before commit
4. **Commit** — commit all changed files together with a clear message
5. **Report** — return a short summary: files changed, tests passed, commit hash

---

## Session Lifecycle — Mandatory

Do not wait until context quality has already degraded.

**Prepare a handover when any of these triggers occur:**
- the current focused task is complete and tested
- a new subsystem or migration wave is about to begin
- roughly 5–8 meaningful commits have been created in the session
- context reliability is uncertain (re-reading the same files, conflicting statements, forgetting constraints)
- a major architecture decision is next

**Handover procedure:**
1. Stop implementation.
2. Finish and test the current focused task only — do not widen scope.
3. Commit all safe changes.
4. Push if credentials are available (`git push origin main`).
5. Update `docs/CURRENT.md` with:
   - current HEAD (`git log --oneline -1`)
   - deployment state
   - current migration slice and what was completed
   - next recommended task
   - known blockers
   - any new hard constraints
   Do NOT create a new `NEXT_SESSION_*.md` file — always update CURRENT.md.
6. Recommend closing the current Claude session and opening a new one.
7. Return the exact startup commands and first prompt for the next session.

**Do not begin the next subsystem in the same session.**

---

## Git Rules

```bash
git add <files>
git commit -m "description"
git push origin main
```

- Commit only files relevant to the current task — do not use `git add .` or `git add -A`
- Do not push unless credentials are already available
- Do not amend previous commits — create a new commit
- Do not skip hooks (`--no-verify`)

---

## Secrets and Deployment State

- Secrets are derived via HMAC-SHA256 from `secrets.local` — re-run `./generate-env.sh` (Linux/Mac) or `.\generate-env.ps1` (Windows) to regenerate `.env`
- Never commit `.env` or `secrets.local`
- DB user: `them`, DB name: `them`, DB host (internal): `them-postgres:5432`
- All Redis on DB index 0. Key prefixes: `them:session:`, `rl:them:`, `them:agents:`, `them:orchestrators:`, `them:bridge:`, `them:dash:`
- Never use DB name `odin` or schema `odin`

---

## Tenant-Aware Design

Every session, run, rate-limit, and runtime gate is scoped to an Application (the tenant boundary).
Application ID must flow through every new feature:
- DB queries must include `application_id` or `entry_point_id` as appropriate
- Redis keys for per-app state must include the app ID or slug in the key
- Rate limiting and session caps must be applied per-app, not globally
- New routes under `/apps/{slug}/` inherit the app slug from the path

---

## App Export/Import Must Be Complete (hard rule)

**Anything a user can configure by clicking around in the App Canvas UI must have a defined,
working path into export/import.** Export an app, import it (or deploy it to another
tenant/team), and the result must be functionally identical — no setting silently missing,
nothing left requiring a manual re-click to restore.

**Full plan, rules, and current gap list: `docs/plans/app-canvas-config-completeness-plan.md`.
Read it before adding any new canvas-configurable feature or touching export/import.**

Quick version of the rule (see the plan doc for the full reasoning):
- A new setting must live either **(a)** inside the app's `AppDefinitionDoc` JSON (covered by
  canvas export/import automatically), or **(b)** in its own `application_id`-scoped DB table
  **registered in the "Deploy to Tenant" copy list** (`go/internal/admin/dal/app_deploy.go`) —
  a one-line addition per table, not bespoke code each time.
- Choosing (b) is fine, not automatically a gap — File Guard/PII's instant-toggle-without-publish
  behavior is a real, deliberate reason to use (b). The actual failure mode is picking (b) and
  **not** registering the table — that's what happened to `middleware_wirings`, found live
  2026-09-28 (see `docs/_archive/APP_EXPORT_IMPORT_INVESTIGATION.md` and the completeness plan's Phase 1).
- Do NOT default to a new DB table just because it's easier — check the plan doc's (a)-vs-(b)
  checklist first.

---

## Rules — Code

- **Never** query `auth_service.*` tables directly — use `app/services/auth_client.py` (HTTP to 8701) from Python, or `internal/auth/` from Go
- **Never** use DB name `odin` or schema `odin` — everything is `them`
- New agent transport → new file in `app/adapters/` + register in `factory.py` + document it in the owning `docs/architecture/` doc
- **A2A work** (canvas agent builder, agentgen, agent cards, typed parts, wire format) → invoke `/a2a` skill first — it loads the Go A2A ground truth (wire format, AgentSpec, interpreter, security invariants, Phase D checklist)
- **App Canvas work** (building/debugging an application: entry points, appflow nodes — llm/condition/fork/join/router/hil — connections, Debug Mode API sequence) → invoke `/app-canvas` skill first — node-type rules are live-queryable via `GET /admin/node-types`, but the app-JSON envelope, entry-point creation gotcha, and the full build+debug API sequence are NOT exposed by any endpoint and only documented in that skill. **If the Skill tool returns "Unknown skill", fall back immediately to `Read /opt/docker/them/.claude/skills/app-canvas.md`** — the file is the skill; reading it directly is equivalent.
- Work under `go/` must also follow `go/CLAUDE.md`

---

## Rules — File Size & Structure (UI + Go)

- Keep files focused and preferably under 400 lines.
- If a file approaches 500 lines, stop and propose a logical split before adding more code.
- Split by clear responsibility, not arbitrary line count.
- Avoid oversized components, hooks, and mixed UI/business logic.
- Do not over-fragment into many tiny files.
- Before creating or expanding a large file, show the proposed structure and wait for approval.

---

## Rules — Documentation (mandatory)

- Every code change: find the owning doc (`python3.12 scripts/docs_lint.py owners <paths>`), update it in the same commit, bump `last_verified`. Use `/docs-update` (`.claude/skills/docs-update.md`).
- **No new top-level docs.** New doc = one of `docs/{architecture,reference,guides,decisions,plans,lessons}/` with front matter (`docs/_templates/`). No `*_STATUS`, `*_RESULTS`, `HANDOVER_*` files — state goes in `docs/CURRENT.md`, results in commit messages.
- New Redis key → `docs/reference/redis.md`. New DB table/column → `docs/reference/schema.md` + `db/001_schema.sql`. New/changed flow → `docs/CURRENT.md`. Non-obvious bug → `docs/lessons/<area>.md`.
- Before commit: `python3.12 scripts/docs_lint.py check && python3.12 scripts/docs_lint.py index`. The pre-commit hook (`.githooks/`) warns if owned code changed without its doc.
- Trust code over docs; update the doc when they diverge.

---

## Rules — Testing (mandatory, non-negotiable)

- **Every code change that touches `go/` MUST have a corresponding test** — new behavior = new test, changed behavior = updated test
- **After every change run the full suite** — zero new failures allowed before committing
- **`go/TEST_INDEX.md` MUST be updated** whenever a Go test is added, changed, or its coverage expands
- **CLAUDE.md trigger maps MUST be kept in sync** with their respective INDEX.md files
- Never commit with a test regression — fix the code or the test; do not skip or delete tests

### Go test runner

```bash
cd go && go test ./...   # full suite — must be zero failures before every commit
```

### Go trigger map — which tests to run after changing what

| Changed | Run tests |
|---|---|
| `db/001_schema.sql` | `go test ./internal/admin/...` (schema-dependent DAL tests) |
| `go/internal/authserver/`, `go/cmd/auth-server/`, `Dockerfile.auth-go`, `docker-compose.yml` (them-auth-go) | `go test ./internal/authserver/...` + live login/me/refresh/logout smoke |
| `go/internal/mcp/` (any file), `go/cmd/mcp-service/main.go`, `Dockerfile.mcp-service` | `go test ./internal/mcp/...` |
| `go/internal/admin/mcp_servers.go`, `go/internal/admin/dal/mcp_servers.go`, `go/internal/admin/service/mcp_servers.go` | `go test ./internal/admin/...` |
| `go/internal/events/` (any file), `go/cmd/event-writer/main.go`, `Dockerfile.event-writer`, `docker-compose.events.yml` | `go test ./internal/events/... ./cmd/event-writer/...` + **rebuild + restart them-event-writer** (Redis Streams consumer, must restart to pick up RecoverPending/retention fixes) |
| `go/internal/agentgen/` | `go test ./internal/agentgen/...` |
| `go/internal/temporal/` or `go/cmd/dag-worker/` | `go test ./internal/temporal/...` + **restart them-dag-worker AND them-dag-worker-2** (two replicas of the same image, same Temporal task queues) |
| `go/cmd/agent-runtime/` | `go test ./...` + **rebuild + restart them-agent-runtime** |
| `docker-compose.yml` labels, `traefik/traefik.yml`, `docker-compose.dev.yml` | compose health smoke (bring up stack, check all containers healthy) |
| Before a release / PR merge | `go test ./...` + live E2E smoke |

### E2E test (14) — needs a JWT

```bash
# Get a token first:
curl -s -X POST http://localhost:8701/auth/login -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | python -c "import sys,json; print(json.load(sys.stdin)['access_token'])"

# Then run:
ADMIN_JWT=<token> python3.12 scripts/tests/run_tests.py 14
```

### Temporal worker restart — REQUIRED after editing Go worker files

```bash
# Go Temporal worker registers activities at startup. If you edit go/internal/temporal/ or go/cmd/dag-worker/:
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal restart them-go-worker them-dag-worker them-dag-worker-2
docker logs them-go-worker --tail 5   # confirm polling
```

---

## Container Map

See `docs/architecture/platform-overview.md` (all containers, profiles, routes, startup wiring). Core: `them-traefik`, `them-postgres`, `them-redis`, `them-auth-go`, `them-go-bridge`, `them-go-worker(-2)`, `them-mcp-service`, `them-frontend`. Python services (bridge, `them-auth-service`) are removed.

---

## Key Source Locations

| Concern | Location |
|---|---|
| DB schema source of truth | `db/001_schema.sql` |
| Auth schema source of truth | `auth_service/SCHEMA.sql` |
| Frontend proxy route | `frontend/src/app/api/them/[...path]/route.ts` |
| Frontend auth cookies | `frontend/src/app/api/auth/` |
| Go source root | `go/` — see `go/CLAUDE.md` for full package map |
| Go admin DAL | `go/internal/admin/dal/` |
| Go WS/SSE handlers | `go/internal/ws/`, `go/internal/sse/` |
| Go auth middleware | `go/internal/auth/` |

---

## Common Commands

```bash
# ── Local dev ────────────────────────────────────────────────────────────────
# Stack (core only)
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
docker compose -f docker-compose.yml -f docker-compose.dev.yml ps
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml logs -f them-go-bridge

# Stack with Temporal (required for orchestration)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal ps
docker logs them-go-worker
# Temporal UI: http://localhost:8088/temporal

# Stack with Events (ClickHouse observability)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml -f docker-compose.events.yml --profile events up -d
docker logs them-event-writer --tail 50
# Event modes: THE_M_EVENTS_GATEWAY=postgres (default) | dual (Postgres + ClickHouse mirror)
# Clickhouse-only mode is BLOCKED until budget/body support is complete

# ── Hetzner (production) ──────────────────────────────────────────────────────
./scripts/deploy.sh up          # start / adopt stack
./scripts/deploy.sh status      # container states
./scripts/deploy.sh logs [svc]  # tail logs

# DB init (run once after first up, or after wiping data/)
docker cp db/001_schema.sql them-postgres:/tmp/them_001_schema.sql
docker cp auth_service/SCHEMA.sql them-postgres:/tmp/them_auth_schema.sql
docker cp db/002_seed.sql them-postgres:/tmp/them_002_seed.sql
docker exec them-postgres psql -U them -d them -c "CREATE SCHEMA IF NOT EXISTS auth_service;"
docker exec them-postgres psql -U them -d them -f /tmp/them_001_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_auth_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_002_seed.sql
# Apply remaining migrations in order: 003 through latest (see docs/CURRENT.md for full list)

# DB access
docker exec -it them-postgres psql -U them -d them

# Secrets / .env (run before first up)
.\generate-env.ps1    # Windows
./generate-env.sh     # Linux/Mac

# Enable Go bridge replica 2
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile replica up -d them-go-bridge-2
```

---

## Database Schemas — Quick Reference

**`auth_service` schema** — owned by `them-auth-service` (port 8701). Never access directly from bridge.
Tables: `roles`, `users`, `teams`, `team_members`, `user_overrides`, `auth_audit`, `user_sessions`, `blacklisted_tokens`

**`them` schema** — owned by `them-go-bridge`.
Tables: `llm_providers`, `config`, `agents`, `orchestrators`, `access_tokens`, `runs` (has `entry_point_slug`), `run_steps`, `run_usage`, `audit_logs`, `tasks`, `artifacts`, `task_messages`, `applications` (parent), `entry_points` (child of applications — one row per WS/SSE/WebRTC door)

**Credentials:** derived via HMAC-SHA256 from `secrets.local`. Re-run `.\generate-env.ps1` to regenerate `.env`.
DB user: `them`, DB name: `them`, DB host (internal): `them-postgres:5432`
