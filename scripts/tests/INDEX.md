# Live E2E Test Index — the-M (`scripts/tests/`)

These are **live end-to-end scripts** that talk to a running local stack (Traefik on `:8088`). They are not unit tests.
Go unit/integration tests are indexed separately in `go/TEST_INDEX.md` (`cd go && go test ./...`).

> The old Python-era suite (`run_tests.py`, tests 01–36, `run_*_tests.sh`, pytest `tests/`) was removed in the docs
> reboot: it grepped source files of the removed Python bridge or targeted old ports/paths, and failed against the current stack
> (git history keeps it).

## Prerequisites (all tests)
- Stack up, e.g. `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d`
  (tests 40–42 need the `temporal` profile; 38_phase4 path 1 needs `--profile sso`; test_a2a agents need `--profile test-agents`).
- Local admin login (default dev login `admin` / `admin123`) — the scripts fetch a JWT through `/auth/api/v1/auth/login`, or use `ADMIN_JWT`.
- `python3.12`; test_38_phase4 also needs the `websockets` package.
- **They create and delete real rows (apps, tenants, tokens) on the stack they hit — run against a dev stack only.**

## Tests

| Script | What it proves | Extra needs |
|---|---|---|
| `test_37_sso.py` | Tenant-admin IdP config write path: super-admin login, `PATCH /tenant/settings` sets `idp_configured`, `tenant-lookup` reflects it (does not drive the browser OIDC flow) | `--base-url` optional |
| `test_38_multitenant.py` | Multi-tenant robustness S0–S11: provisioning, tenant-admin role enforcement, RLS scoping, quotas, observability, self-service, token refresh keeps tenant, cross-tenant isolation, SSO | flags `--skip-sso --skip-quota --keep-tenant --tenant-slug`; Keycloak optional |
| `test_38_phase4_external_jwt.py` | Three credential paths end to end: bank RS256 JWT (`external_jwt` EP), backend service token with `X-External-User`, the-M user JWT (`user_jwt` EP) | `--profile sso` for path 1; `--skip-path1/2/3` |
| `test_39_appflow_hil.py` | HIL approval API: approve/reject pending gate, expected HTTP statuses (inserts a `hil_approvals` row via `docker exec psql`) | `ADMIN_JWT` or auto-fetch |
| `test_40_appflow_canvas_e2e.py` | Full canvas run: create app + WS EP → publish (stamps `_resolved_agent_ids`) → WS message → workflow pauses at HIL → approve → run `completed` | `temporal` profile, `a2a-echo` agent |
| `test_41_appflow_fork_join.py` | Fork/Join: both branch agents run, run `completed` | `temporal` profile, `a2a-echo` |
| `test_42_appflow_inline_nodes.py` | Inline LLM (mock provider) + Condition node routing; negative validate cases (`condition_edge_count`, …) | `temporal` profile, `a2a-echo` |

Run one: `python3.12 scripts/tests/test_40_appflow_canvas_e2e.py` (exit code 0 = pass).

## Last run (2026-10-25, local dev stack)
| Script | Result |
|---|---|
| `test_37_sso` | had a SyntaxError (fixed); now 9/11 — `tenant-lookup` by email domain returns 404 (backlog F-93) |
| `test_38_multitenant --skip-sso` | **64/64 pass**, 1 skipped |
| `test_38_phase4_external_jwt` | **37/37 pass** |
| `test_39_appflow_hil` | **pass** |
| `test_40_appflow_canvas_e2e`, `test_41_appflow_fork_join`, `test_42_appflow_inline_nodes` | **fail** at validate/publish with `component_not_found` for `a2a_echo` (backlog F-92) |

## Relationship to the docs
- What each test exercises is described in `docs/architecture/app-canvas-appflow.md`, `auth-and-roles.md` and `multi-tenancy-rls.md`.
- Known weaknesses these tests would not catch (fork-branch errors, guards in branches, cross-tenant export): `docs/plans/code-findings-backlog.md`.
- Other live scripts (performance, event delivery, governance, soak) are in `scripts/` and `go/scripts/`; they are not part of this index.

## Adding a test
Copy the structure of `test_41_appflow_fork_join.py` (self-contained: fetch JWT, create fixtures, assert, always clean up), add a row above, and keep it runnable standalone.
