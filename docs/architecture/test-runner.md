---
title: Test Runner (load and smoke test UI)
type: architecture
status: current
owns:
  - test-runner/backend/
  - test-runner/frontend/app/
  - test-runner/docker-compose.yml
  - test-runner/docker-compose.dev.yml
last_verified: 2026-11-02
---

# Test Runner

## Purpose
A separate mini-app (own compose file, not part of the main stack) that simulates N parallel end-users against a live app entry point and shows pass/fail per user in real time. It is for load/gateway smoke tests; it is **not** the Go test suite or `scripts/tests/` (those are in `scripts/tests/INDEX.md`). Usage walk-through: `test-runner/README.md`.

## How it works
- **Containers:** `them-test-runner-backend` (Go, chi, port 8093→8090) and `them-test-runner-frontend` (Next.js, port 3210). Joins the external network `them-network` to reach `them-traefik:8088` and `them-postgres`. Start: `cd test-runner && docker compose up -d`.
- **State** is plain JSON under `test-runner/data/`: `config.json` (the-M URL + admin login), `scenarios/*.json`, `history/<runId>.json`.
- **Backend routes** (no `/api` prefix; the Next proxy adds it): `/config` (GET/PUT, `/config/test`), `/tenants`, `/tenants/{slug}/apps`, `/apps/{id}/eps`, `/scenarios` CRUD, `POST /run`, `GET /run/{id}/stream` (SSE), `DELETE /run/{id}`, `/history` list/get/delete.
- **Catalog** (`handler/catalog.go`) lists tenants/apps/entry points by reading Postgres directly with `DB_URL`.
- **Run** (`scenario/run.go`): logs in as admin, creates one virtual user per `n_users`, and each sends the scenario `messages` in order. Runners in `them/`: websocket entry points (`runner.go`), A2A JSON-RPC (`a2a_runner.go`), gateway/LLM (`gateway_runner.go`). Auth modes include temporary access tokens per user (created and deleted via the admin API) and user JWTs.
- **Expectations:** scenario field `expect` = `allow` (default) or `deny`. With `deny` a user passes only when the connect is rejected with HTTP 403 (`scenario/expect.go`); being admitted is a failure. Used by 51–60 (one app, seven entry points with different required roles; 58–60 use `auth_mode: user_jwt` — the runner logs in to the-M auth with `login_users` and uses the access token, whose single `role` claim is the membership role (payops-test = admin): `role-user-admin-ws` needs `admin`, `role-user-dev-ws` needs `developer`, `role-user-open-ws` none. The external_jwt ones: `smart-refund-ws` and `role-premium-ws` need `jwt-premium`, `role-partner-ws` needs `m2m-partner`, `role-open-ws` needs none; covers allow, wrong tier, no tier claim at all = fail closed, wrong role, open door) and by `scripts/tests/test_43_ep_required_role.py`, which drives the runner API with temporary scenarios (per-EP required tenant role; setup: `test-runner/scripts/setup_ep_role_scenario.py`, Keycloak `payops_ai` users alice=tier premium, bob=standard; `keycloak_url` must use the issuer host configured in the tenant runtime IdP).
- **Gotcha:** an admitted user's run keeps waiting for more input and counts against the tenant's concurrent-run quota (payops_ai: 3). After a few admitted runs the next connect gets HTTP 429, which is NOT a role denial. Cancel leftover runs (`PATCH /api/v1/runs/{id}/cancel`) between batches.
- **Scenarios** shipped in `data/scenarios/` (gateway PII/rate-limit/token/cost limits, perf with 50–400 users, A2A debator, event delivery). Their README lists what each checks.

## Key invariants
- A run creates temporary access tokens; they must be deleted at the end (check the admin token list after a cancelled run).
- Scenario IDs/history IDs are UUID file names; no database for runner state.

## Failure modes
- Admin login fails (wrong `admin_pass`) → every run fails at start; use `/config/test`.
- Not on `them-network` → DNS errors for `them-traefik`/`them-postgres`.
- Large user counts hit the-M's own per-app rate limits/session caps — that is often the intended result.

## Known gaps
See backlog F-110…F-113: DB credentials in `docker-compose.yml`, admin password in tracked `config.json`, no authentication, CORS `*`, and the dead basic-auth setting.

## Where to change things
Runners: `test-runner/backend/them/`. Scenario schema/run loop: `backend/scenario/`. UI pages: `frontend/app/{scenarios,history,config}/`.
