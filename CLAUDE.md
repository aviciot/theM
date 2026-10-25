# the-M — Claude Session Guide
# multi-agent orchestration platform
# Last updated: 2026-10-25

---

## Read These First

Docs were reset 2026-10-24 (plan: `docs/plans/docs-reboot-plan.md`). Old docs are in `docs/_archive/` — hints only, never ground truth.

1. `docs/INDEX.md` — generated; find the doc that `owns` the code you are touching.
2. `docs/CURRENT.md` — current state, next task, blockers. Open code bugs found while documenting: `docs/plans/code-findings-backlog.md` (add new ones there).
3. Reference docs when relevant: `docs/reference/schema.md` (DB), `docs/reference/redis-generated.md` (Redis), `docs/guides/a2a-test-agents.md`, `docs/architecture/auth-and-roles.md`, `docs/lessons/` (one file per area — read the matching one before any judgment call), `scripts/tests/INDEX.md` (tests), `docs/guides/local-test-environment-runbook.md` (Docker/deploy/containers).

For Go work, also read `go/CLAUDE.md` — it governs everything under `go/`.

**Which doc for what** (all in `docs/architecture/`; each has Purpose, How it works, Invariants, Failure modes, Known gaps, Where to change things):

| If you work on… | Read |
|---|---|
| containers, routes, startup | `platform-overview.md` |
| WS/SSE, admission, gate, Temporal runs, orchestrator | `run-lifecycle.md` |
| App Canvas / AppFlow, publish, Debug Mode | `app-canvas-appflow.md` |
| login, JWT, SSO, roles | `auth-and-roles.md` |
| tenants, RLS, DB pools | `multi-tenancy-rls.md` |
| LLM gateway, governance, provider keys | `llm-gateway.md` |
| MCP servers/tools | `mcp-service.md` |
| canvas agents, agent-runtime, A2A | `a2a-agents-agentgen.md` |
| export/import, deploy to tenant | `app-export-import-deploy.md` |
| Redis keys and usage | `redis-usage.md` (+ `docs/reference/redis-generated.md`) |
| events / ClickHouse | `events-pipeline.md` |

Known bugs and risks found while documenting: `docs/plans/code-findings-backlog.md` — check it before changing an area, and add new ones there.

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

## Session Lifecycle

- Work in small committed steps; update `docs/CURRENT.md` (HEAD, state, next task, blockers) when a task or tracker item finishes. Do NOT create `NEXT_SESSION_*.md` / `HANDOVER_*` files.
- Keep going from one task to the next in the same session. Hand over to a new session **only when context is almost full** (or the user asks): stop, commit safe changes, update `docs/CURRENT.md`, and give the user a short paste-ready prompt for the next session (template: `docs/plans/docs-reboot-plan.md` section 10).
- Push only when credentials are already available (`git push origin main`).

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
- Do not skip hooks (`--no-verify`). The pre-commit hook **blocks** a commit when staged code is owned by a doc (`owns:` front matter) that is not also staged. Fix it by updating the doc (`/docs-update`). For genuine no-doc changes (formatting, comment-only, test-only) use `DOCS_UNCHANGED_OK=1 git commit …` and say why in the message — tests, markdown and `go.sum` never need a doc.

---

## Secrets and Deployment State

- Secrets are derived via HMAC-SHA256 from `secrets.local` — re-run `./generate-env.sh` (Linux/Mac) or `.\generate-env.ps1` (Windows) to regenerate `.env`
- Never commit `.env` or `secrets.local`
- DB user: `them`, DB name: `them`, DB host (internal): `them-postgres:5432`
- All Redis on DB index 0 (main `them-redis`; events go to `them-redis-events`). Key families, TTLs and owners: `docs/architecture/redis-usage.md`; every key definition: `docs/reference/redis-generated.md`.
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

- **Never** query `auth_service.*` tables from feature code — go through `internal/auth/` (token/JWT validation) or the auth service (`them-auth-go`, `internal/authserver/`); only the auth service and a few explicit role/quota lookups (`execution/roles.go`) read those tables. See `docs/architecture/auth-and-roles.md`.
- **Never** use DB name `odin` or schema `odin` — everything is `them`
- New agent transport → implement it in `go/internal/agentregistry/` (outbound calls) and document it in `docs/architecture/a2a-agents-agentgen.md`
- **A2A work** (canvas agent builder, agentgen, agent cards, typed parts, wire format) → read `docs/architecture/a2a-agents-agentgen.md` first; the `/a2a` skill (`.claude/skills/a2a.md`) is partly stale (see its banner) — code wins
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
- New Redis key → use a `them:`/`rl:` literal with a TTL, run `python3.12 scripts/gen_reference.py redis`, update `docs/architecture/redis-usage.md`. New DB table/column → a new `db/NNN_*.sql` migration (RLS + FORCE + policy + GRANTs to `them_app` and `them_admin` + sequence GRANT, see `docs/architecture/multi-tenancy-rls.md`), then `gen_reference.py schema` (`db/001_schema.sql` is NOT the source of truth; the live database is). New/changed flow → the owning architecture doc and `docs/CURRENT.md`. Non-obvious bug → `docs/lessons/<area>.md`. Found a bug you are not fixing → `docs/plans/code-findings-backlog.md`.
- Before commit: `python3.12 scripts/docs_lint.py check && python3.12 scripts/docs_lint.py index` (CI runs both, plus `scripts/gen_reference.py routes --check`). Generated reference lives in `docs/reference/*-generated.md` — regenerate, never hand-edit. The pre-commit hook (`.githooks/`) **blocks** if owned code is staged without its doc (see Git Rules).
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
| `db/NNN_*.sql` (schema/RLS migrations) | `go test ./internal/admin/...` (+ `go test -tags=integration ./internal/db/...` against a TEST database) |
| `go/internal/authserver/`, `go/cmd/auth-server/`, `Dockerfile.auth-go`, `docker-compose.yml` (them-auth-go) | `go test ./internal/authserver/...` + live login/me/refresh/logout smoke |
| `go/internal/mcp/` (any file), `go/cmd/mcp-service/main.go`, `Dockerfile.mcp-service` | `go test ./internal/mcp/...` |
| `go/internal/admin/mcp_servers.go`, `go/internal/admin/dal/mcp_servers.go`, `go/internal/admin/service/mcp_servers.go` | `go test ./internal/admin/...` |
| `go/internal/events/` (any file), `go/cmd/event-writer/main.go`, `Dockerfile.event-writer`, `docker-compose.events.yml` | `go test ./internal/events/... ./cmd/event-writer/...` + **rebuild + restart them-event-writer** (Redis Streams consumer, must restart to pick up RecoverPending/retention fixes) |
| `go/internal/agentgen/` | `go test ./internal/agentgen/...` |
| `go/internal/temporal/`, `go/internal/appflow/` activities, or `go/cmd/dag-worker/` | `go test ./internal/temporal/... ./internal/appflow/...` + **rebuild and restart ALL dag workers** (`them-dag-worker`, `-2`, `-3`, `-4` and `them-dag-worker-debug` — each service has its own image tag). Orchestrator code (`go/cmd/worker/`): restart `them-go-worker` and `them-go-worker-2`. |
| `go/cmd/agent-runtime/` | `go test ./...` + **rebuild + restart them-agent-runtime** |
| `docker-compose.yml` labels, `traefik/traefik.yml`, `docker-compose.dev.yml` | compose health smoke (bring up stack, check all containers healthy) |
| Before a release / PR merge | `go test ./...` + live E2E smoke |

### E2E test (14) — needs a JWT (`scripts/tests/` is partly Python-era source-grepping; see `scripts/tests/INDEX.md`; verify a test still means something before trusting it)

```bash
# Get a token first (auth service via Traefik; use your local admin login):
curl -s -X POST http://localhost:8088/auth/api/v1/auth/login -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | python -c "import sys,json; print(json.load(sys.stdin)['access_token'])"

# Then run:
ADMIN_JWT=<token> python3.12 scripts/tests/run_tests.py 14
```

### Temporal worker restart — REQUIRED after editing Go worker files

```bash
# Workers register workflows/activities at startup. After editing go/internal/temporal/, go/internal/appflow/ or go/cmd/dag-worker/ (see docs/lessons/temporal-workers.md, docs/lessons/infra-docker.md):
# Rebuild first if code changed (each service builds its own image), then recreate — `restart` alone does NOT pick up a new image:
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d --build them-go-worker them-go-worker-2 them-dag-worker them-dag-worker-2 them-dag-worker-3 them-dag-worker-4 them-dag-worker-debug
docker logs them-go-worker --tail 5   # confirm polling
```

---

## Container Map

See `docs/architecture/platform-overview.md` (all containers, profiles, routes, startup wiring). Core: `them-traefik`, `them-postgres`, `them-redis`, `them-auth-go`, `them-go-bridge`, `them-go-worker(-2)`, `them-mcp-service`, `them-frontend`. Python services (bridge, `them-auth-service`) are removed.

---

## Key Source Locations

| Concern | Location |
|---|---|
| DB schema | the live database (generated: `docs/reference/schema-generated.md`); migrations are `db/NNN_*.sql` applied by hand — `db/001_schema.sql` is stale (backlog F-35) |
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

# DB schema changes: add db/NNN_*.sql, then apply to the running DB by hand, e.g.
#   docker cp db/NNN_x.sql them-postgres:/tmp/x.sql && docker exec them-postgres psql -U them -d them -f /tmp/x.sql
# There is NO reliable fresh-install script today (db/001 + scripts/init_db.sh are stale and reference a removed
# auth_service/ folder — backlog F-35/F-36). Never re-run db/001 against a live database.

# DB access
docker exec -it them-postgres psql -U them -d them

# Secrets / .env (run before first up)
.\generate-env.ps1    # Windows
./generate-env.sh     # Linux/Mac

# Enable Go bridge replica 2
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile replica up -d them-go-bridge-2
```

---

## Database — Quick Reference

Postgres DB and schema are both **them**; a second schema `auth_service` holds users/roles/memberships (owned by `them-auth-go`).
Full, current table list with RLS state and policies: `docs/reference/schema-generated.md` (generated from the live catalog).
Isolation model and the three DB roles (`them_app`, `them_admin`, superuser `them`): `docs/architecture/multi-tenancy-rls.md`.

**Credentials:** derived via HMAC-SHA256 from `secrets.local`; re-run `./generate-env.sh` (Linux/Mac) or `.\generate-env.ps1` (Windows) to regenerate `.env`. DB host (internal): `them-postgres:5432`. Never commit or print real secrets.
