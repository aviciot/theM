---
title: Current State
type: plan
status: current
last_verified: 2026-10-25
---

# Current State — the-M
Last updated: 2026-10-25 · Branch: `main` · HEAD: see `git log --oneline -1` (27+ local commits are **not pushed** to GitHub yet)

## Docs (the 2026-10 reboot) — complete
- Plan + tracker (all 15 items done): `docs/plans/docs-reboot-plan.md`. Find docs via `docs/INDEX.md`; 10 architecture docs + `redis-usage.md` in `docs/architecture/`.
- Enforcement: pre-commit hook **blocks** owned-code commits without the owner doc (`DOCS_UNCHANGED_OK=1` + reason for genuine no-doc changes); CI job `docs-lint` (written, never run on GitHub). Clones: `git config core.hooksPath .githooks`.
- Generated reference: `scripts/gen_reference.py` (routes, schema, node-types, redis) → `docs/reference/*-generated.md`; lint: `scripts/docs_lint.py check|index|stale|stray|links`.
- Cleaned 2026-10-25: dead Python-era tests/config/clutter deleted; `scripts/tests/INDEX.md` lists the 7 live E2E scripts.
- Open doc follow-ups: stray `.md` files outside `docs/` (`docs_lint.py stray`), 12 remaining source refs to old doc paths (F-87, mostly fixed), coverage gaps (voice, dashboard/metrics, `test-runner/` app), `.claude/memory/*.md` unreviewed.

## Known problems — `docs/plans/code-findings-backlog.md` (121 items, none fixed)
Highest severity to fix first: F-56 MCP internal API unauthenticated · F-67 agent-runtime unauthenticated · F-68 A2A task access not tenant-checked · F-78 tenant export cross-tenant · F-79 agent-param secrets copied · F-70/F-57/F-48 SSRF · F-01/F-02 fork-branch guards/errors · F-18 logout not honoured · F-19 SSO account takeover · F-31 gate keys not tenant-scoped · F-32 superuser DB creds in services · F-88 non-rebuildable state in an LRU Redis · F-94/F-91 committed tokens (revoke and rotate; the `origin` remote URL embeds a GitHub token).

## Tests
- Go: `cd go && go test ./...` (index `go/TEST_INDEX.md`). Live E2E (dev stack only, creates/deletes rows): `scripts/tests/INDEX.md`.
- Last live run (2026-10-25): `test_38_multitenant` 64/64, `test_38_phase4_external_jwt` 37/37, `test_39_appflow_hil` pass; `test_37_sso` 9/11; `test_40/41/42` fail with `component_not_found` for `a2a_echo` (F-92, undiagnosed).

## Running stack (verified 2026-10-25)
Core + temporal + events profiles up (43 healthy containers). `THE_M_EVENTS_GATEWAY=dual` (Postgres authoritative + ClickHouse mirror); event-writer and ClickHouse running.
Dual-mode cost is **preliminary**: +13% median gateway latency (132→149 ms) from 3 runs per mode, no throughput change — decision on whether to keep `dual` is still open (more runs needed: `scripts/perf_focused_test.py`).
Start/rebuild/recovery commands: `docs/guides/local-dev-and-deploy.md`.

## Next tasks (suggested order)
1. Revoke/rotate exposed tokens (F-94, F-91); decide push to GitHub (then check the Actions tab).
2. Finish doc stabilisation: strays, comment-path sweep, remaining coverage gaps.
3. Fix top-severity backlog items (list above), each with a Go test and an architecture-doc update (the hook enforces the doc).
4. Diagnose E2E failures F-92 and shared test helpers (`scripts/tests/lib/`) + nightly CI run.
5. Run-usage/steps event producers for dual mode (`go/internal/events/`) — deferred from the earlier session.

## Hard constraints
Brand: UI/docs say **the-M**; code uses `them` / `THE_M_`. DB `them`, never `odin`. Never commit/print secrets. Root `CLAUDE.md` and `go/CLAUDE.md` rules apply.
