---
title: Docs Reboot Plan and Progress Tracker
type: plan
status: current
last_verified: 2026-10-25
---

> Section 9 is the progress tracker; update every session.

# Docs Reboot Plan

Status: PROPOSAL (2026-10-24) — nothing moved or deleted yet.

## 1. Findings (measured, not guessed)

- **168 md files, ~62k lines** under `docs/` (+ `go/CLAUDE.md`, `scripts/tests/INDEX.md`, `.claude/skills/*`).
- **142 of 168 are not in `INDEX.md`**; INDEX itself was last updated 2026-09-06, and says `STATUS.md` is "historical" while `CLAUDE.md` tells Claude to read it.
- **Created-by-session sprawl**: ~39 `*_PLAN`, 10 `HANDOVER_*`, 12 `*STATUS*`, 10 `*RESULTS*`, 22 `*TEST*`. These are work logs, not documentation.
- **Clear duplication clusters** (same topic, many files):
  - Events/delivery: ~25 files (`EVENT_DELIVERY_*`, `EVENTS_*`, `POISON_*`, `RESILIENCE_*`, `VERIFICATION_*`)
  - Gateway/quotas/tracing/policy: ~12 files
  - Roles/auth: ~10 files (`ROLES_*`, `ROLE_BASED_*`, `USER_AUTH_*`, `END_USER_AUTH_*`, `UNIFIED_ROLE_*`)
  - Two lessons files (`LESSONS.md` 1784 lines, `lessons-learned.md` 1187 lines)
- **Root cause**: no rule for *where new information goes*, so every task creates a new file, and nothing forces an update to an existing one. Docs are written once at task time and never revisited.
- Things that already work and should be kept as the model: `SCHEMA.md`, `REDIS.md`, `go/CLAUDE.md` trigger map, `scripts/tests/INDEX.md`, the two skills.

## 2. Principles

1. **Few docs, each with an owner in code.** A doc exists only if it describes something that exists *now*.
2. **Generate what can be generated** (schema, routes, node types, test index); hand-write only the *why* and *how it flows*.
3. **Work logs don't belong in docs/.** Status, test results, handovers → commit message / PR / `CURRENT.md`.
4. **Staleness must be detectable by a machine**, not by someone remembering.
5. **Claude is told, per code path, which doc to update** — same mechanism as the existing test trigger map.

## 3. Proposed structure (6 categories, based on Diátaxis + ADRs)

```
docs/
  INDEX.md              generated from front matter — never hand-edited
  CURRENT.md            the ONLY "state" file: HEAD, deployed, next task, blockers
  architecture/         one doc per subsystem: how it works, flows, invariants, failure modes
  reference/            facts: SCHEMA, REDIS, ROUTES, NODE_TYPES, ENV, EVENTS (generated where possible)
  guides/               how-to + runbooks (local env, deploy, add a node, add an adapter, tenant provisioning)
  decisions/            ADRs — immutable, numbered, superseded not edited
  plans/                active design/plan docs, each with owner + exit criteria; archived when done
  lessons/              ONE file per area (append-only), replaces the 2 giant files
  _archive/             everything retired (kept in git, excluded from INDEX and from Claude's search)
```

**Architecture docs to write first (the critical "how it works" set, ~10):**
platform overview & container map · request/run lifecycle (WS/SSE → bridge → Temporal → agents) ·
App Canvas / AppFlow compile+publish+runtime · auth & roles (single doc, unify the 10) ·
multi-tenancy & RLS · LLM Gateway (policy, quotas, tracing) · events pipeline (Redis Streams → ClickHouse) ·
MCP service · A2A agents · export/import & deploy-to-tenant.

## 4. Front matter on every doc (the enforcement hook)

```yaml
---
title: Events Pipeline
type: architecture        # architecture | reference | guide | decision | plan | lessons
status: current           # current | draft | superseded
owns:                     # code paths this doc describes
  - go/internal/events/
  - go/cmd/event-writer/
last_verified: 2026-10-24 # commit hash or date someone confirmed doc == code
---
```

`owns:` is the key idea: it lets tooling answer "which doc describes this file?" in both directions.

## 5. Keeping it fresh (the part that actually solves the problem)

| Mechanism | What it does |
|---|---|
| **Doc trigger map in `CLAUDE.md`** (generated from `owns:`) | Same shape as the Go test map: "changed `go/internal/events/` → update `architecture/events.md`". Claude reads it before commit. |
| **Stop/pre-commit hook** | If staged files match an `owns:` path and the owning doc isn't also staged → warn/block with the doc name. |
| **CI `docs-lint`** | Front matter valid, links resolve, no md outside the 6 folders, no doc whose `owns:` paths no longer exist, no doc newer-code-than-`last_verified` older than N days (report only at first). |
| **Generated reference** | `SCHEMA` via `tbls`/`schemaspy` from live Postgres, routes from router/OpenAPI, node types from `GET /admin/node-types`, Redis keys + test index from a script. CI fails if generated output differs from committed. |
| **`/docs-update` skill** | Claude's checklist: find owning doc via `owns:`, update, bump `last_verified`, add lesson if non-obvious. Replaces the long "Rules — Documentation" prose. |
| **Hard rule: no new top-level file** | New information goes into an existing owner doc; a new doc needs a new `owns:` entry. Plans get archived on completion. |

## 6. Tooling options (decide at step 3)

- **Minimum (recommended start):** plain markdown + front matter + one Python lint/index script + hooks. Zero new infra.
- **Next step:** MkDocs Material (or Docusaurus) to render a searchable site from the same folder, Mermaid diagrams, built in CI. Optional, doesn't change the structure.
- **Later:** doc search as an MCP tool so Claude queries docs by topic instead of reading INDEX.

## 7. Rollout (small, each step independently valuable)

1. **Freeze + archive**: `git mv` everything except the keep-list (CLAUDE.md-referenced docs, `SCHEMA`, `REDIS`, `LESSONS`, ADRs, runbook, `CURRENT`) into `_archive/`. Nothing deleted; git history keeps it.
2. **Triage table**: for each archived file mark `distill` (has facts worth pulling into a new architecture doc) / `drop`. Events, gateway, roles clusters first.
3. **Write the skeleton**: the 6 folders, front-matter spec, lint+index script, hook, `/docs-update` skill, rewrite CLAUDE.md "Read These First" + "Documentation" sections (shorter, points to trigger map).
4. **Write the ~10 architecture docs** one subsystem per session, verified against code (not copied from old docs). Each ends with `last_verified`.
5. **Generate reference docs** + CI check.
6. **Turn on enforcement** (hook blocking, CI failing) once the set is stable.

## 8. Open decisions for the owner

- Archive-all-then-rebuild (recommended, matches your instinct) vs. in-place cleanup?
- Rendered site (MkDocs) now or later?
- Hook strictness: warn first, block later?
- Should `LESSONS.md` be split per area now or at step 3?

## 9. Progress tracker (update this table every session)

Status: `todo` → `done` (written from code, verified, committed). Method per doc: read code + tests, fill `docs/_templates/architecture.md`, list real gaps found, `docs_lint.py check && index`, commit.

| # | Item | Suggested `owns:` (start here) | Status |
|---|---|---|---|
| 0 | Archive old docs + skeleton + hook + `/docs-update` skill | — | done (`6cbf4cd9`) |
| 1 | Events pipeline (pilot) | `go/internal/events/`, `go/cmd/event-writer/` | done (`bfba7c3a`) |
| 2 | Platform overview & container map | `docker-compose*.yml`, `traefik/`, `go/cmd/them/` | done (see git log) |
| 3 | Run lifecycle (WS/SSE → gate → Temporal → agents) | `go/internal/ws/`, `sse/`, `gate/`, `session/`, `temporal/`, `orchestrator/` | done (see git log) |
| 4 | App Canvas / AppFlow (compile, publish, runtime) | `go/internal/appflow/`, `go/cmd/dag-worker/` | done (see git log) |
| 5 | Auth & roles (unify the ~10 archived role/auth docs) | `go/internal/auth/`, `authserver/`, `execution/roles.go` | done (see git log) |
| 6 | Multi-tenancy & RLS | `go/internal/db/`, `db/0*_*.sql` | done (see git log) |
| 7 | LLM Gateway (policy, quotas, tracing) | `go/internal/llmgateway/`, `governance/`, `llmresolve/` | done (see git log) |
| 8 | MCP service | `go/internal/mcp/`, `go/cmd/mcp-service/` | done (see git log) |
| 9 | A2A agents & agentgen | `go/cmd/agent-runtime/`, `go/internal/agentgen/`, `a2a/` | done (see git log) |
| 10 | App export/import & Deploy-to-Tenant | `go/internal/admin/dal/app_deploy.go` | done (see git log) |
| 11 | Give kept docs front matter + move into folders (`SCHEMA`, `REDIS` → `reference/`; ADRs → `decisions/`; runbook → `guides/`; `AUTH` merges into #5) | — | done (see git log) |
| 12 | Split `LESSONS.md` per area into `docs/lessons/` | — | done (see git log) |
| 13 | Generated reference (schema, routes, node types) + CI `docs-lint` | — | done (see git log) |
| 14 | Switch hook from warn to block (once 2–10 are done and stable) | — | done (see git log) |

Known gap to fix when touching REDIS.md: `them:events` / `them:events:dlq` are missing (found in #1).

Found in #2: root `CLAUDE.md` still has Python-era rules (`app/adapters/`, `auth_client.py`, `auth_service.*`, port 8701, `python3.12 run_tests.py`) — clean up in item #5 (auth) / #3. Repo root has many stray status/result files to triage.

Found in #3 (details in `architecture/run-lifecycle.md` Known gaps): Temporal is mandatory (nil client panics `NewLifecycle`); reconciler workflow-ID mismatch (never repairs runs); `session.Touch` never called (gate undercounts after 90s); pre-orchestrator failures emit no `error` event. Fix as code tasks, not docs tasks. `REDIS.md` session/gate keys are Python-era (item #11).

Found in #4 (details in `architecture/app-canvas-appflow.md` Known gaps): fork branch errors are swallowed; `walkBranch` supports only 6 node kinds; Text/File Guards are skipped inside fork branches; agent-only flows may send no answer text over WS/SSE (`done.final_text` dropped). Code tasks, not docs tasks.

**Findings backlog:** every bug/risk/inefficiency found while documenting goes in `docs/plans/code-findings-backlog.md` (add a row in the same commit; mark fixed there when fixed). Fixed after the reboot completes.

## 10. Session handover rule

Do **not** end the session after each tracker item. Finish an item (commit + update the section 9 tracker and the "DOCS REBOOT IN PROGRESS" block in `docs/CURRENT.md`), then continue straight into the next item.

Hand over **only when context is almost full** (or the user asks). At that point Claude must:
1. Commit the work and make sure the tracker and `docs/CURRENT.md` (next item number, HEAD) are current.
2. **Send the user a short handover prompt in chat** (3–4 lines) they can paste into a fresh session. Template:

> Continue the docs reboot. Read `docs/CURRENT.md` and `docs/plans/docs-reboot-plan.md` section 9, then do item #N (<name>) using `/docs-update` and `docs/_templates/architecture.md`. Write from the code, not the archive. Update the tracker and commit.

Done in #11: moved to `reference/schema-generated.md`, `reference/redis.md`, `decisions/00N-*.md`, `guides/local-dev-and-deploy.md`, `guides/a2a-test-agents.md`, `lessons/general.md` (split in #12), `plans/app-canvas-config-completeness-plan.md`, `plans/docs-reboot-plan.md`; archived `AUTH.md`, `A2A_REFERENCE.md`. All references updated in docs, CLAUDE.md files, skills, hooks and scripts (the test scripts read `docs/reference/redis-generated.md`). Legacy docs carry `last_verified: unverified` and `status: draft` until re-verified. Source-code comments that point at archived plan docs are tracked as backlog F-87. No Go toolchain on this host: Go/TS edits were comment-only (checked by diff), not compiled.

Done in #12: 100 lesson entries split verbatim into 12 area files under `docs/lessons/` (content size conserved apart from added front matter); `lessons/general.md` removed. Python-era entries (asyncpg, SQLAlchemy, pytest, …) are in `python-legacy.md` (`status: superseded`). References to the old file now point at `docs/lessons/<area>.md`.

Done in #13: `scripts/gen_reference.py` (`routes` static scan, `schema` from the live catalog, `node-types` from the admin API; `--check` mode, deterministic, no timestamps/line numbers) → `docs/reference/{routes,schema,node-types}-generated.md`. `docs_lint.py` gained `links` (dead `docs/*.md` refs: errors in the docs folder, the CLAUDE.md files and skills; only counted in source comments), `stale`, `stray` (report-only), and `check` now includes the link check. CI job `docs-lint` in `.github/workflows/ci.yml` (untested on GitHub — written, not run there). Only `routes` can be checked in CI (schema/node-types need the live stack). Routes are registration paths relative to each router, not full external URLs. The legacy `reference/schema-generated.md` / `redis.md` stay until their facts are folded in or retired; Redis keys are not generated yet. Open from this step: 22 stray `.md` files outside `docs/` (root status/result files, `test-results/`, `test-runner/`), 22 docs `unverified`, 215 source comments with dead doc refs (F-87).

Done in #14: `.githooks/pre-commit` now **blocks** (`docs_lint.py staged`, exit 1) when staged code is owned by a doc that is not also staged. Docs, markdown, `*_test.go`/`*_test.py`, `go.mod`/`go.sum` and testdata never require a doc. Escape hatch for genuine no-doc changes: `DOCS_UNCHANGED_OK=1 git commit …` (explain in the message); `--warn` restores the old behaviour. New `docs_lint.py changed BASE` + a pull-request-only CI step enforce the same rule server-side (written, not yet run on GitHub). The hook was tested end-to-end in a throwaway repo (block, escape hatch, doc staged, test-only, unowned file, `--warn`, `changed`); `core.hooksPath` is `.githooks` locally, so each clone must set it (`git config core.hooksPath .githooks`). Owner docs are file-level `owns:` lists, so a change to any file in an owned folder asks for that doc — keep `owns:` narrow where that is too noisy. **Decisions section 8 is now answered: warn first, then block (done).**

Redis follow-up (after #14): `gen_reference.py redis` → `docs/reference/redis-generated.md` (static scan of `them:`/`rl:` key literals, CI-checkable with `--check`; `redis-live` reports live key patterns the code does not define). Legacy hand-written `REDIS.md` archived; `scripts/tests/{run_tests,test_17_memory}.py` now read `docs/_archive/REDIS.md`. TTLs/owners are not generated — read the listed source file or the architecture docs.
Redis narrative: `docs/architecture/redis-usage.md` (two servers, key families, TTLs, owners, failure modes, findings F-88..F-90).

Python cleanup (after #14): removed the dead Python-era test suite (`tests/`, `pytest.ini`, `pyproject.toml`, root `requirements.txt`, `scripts/tests/run_tests.py` + tests 01–36 + `run_*_tests.sh`, `scripts/{run_tests.sh,init_db.sh,perf_test_runner.sh}`) and `lessons/python-legacy.md`; stopped tracking `.claude/worktrees/` (803 files, already gitignored). Evidence: structural tests grep removed `app/*.py`; live ones failed on the current stack (test 12: 5/5 fail, test 15: 11 fail, test 20 crash). Kept: live E2E `scripts/tests/test_37..42` (indexed in `scripts/tests/INDEX.md`), the Oct perf/event scripts, and `agents/` (running Python demo agents). Still to review: July scripts (`test_temporal_phase4/5`, `seed_debate_master`, `test_wave7_fernet_compat`, `go/scripts/soak_*`), `mock_agent/`, `scripts/perf_comparison.py`.

Verification pass (2026-10-25): the 24 `unverified` docs were checked — ADR-001 superseded by ADR-004, ADR-002/003 annotated with what code still does, `reference/schema.md` archived (replaced by the generated schema), `guides/a2a-test-agents.md` and the new `guides/local-dev-and-deploy.md` rewritten from the live stack (old runbook archived), `CURRENT.md` rewritten, completeness plan status updated; generated docs and lessons are exempt from the staleness report (`docs_lint.py stale`).
