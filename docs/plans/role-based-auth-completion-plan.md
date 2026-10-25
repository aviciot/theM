---
title: Role-Based Authorization — Completion Plan
type: plan
status: current
last_verified: 2026-10-25
---

> Single source of truth for where the role/quota feature stands. Original design docs (hints only): `docs/_archive/ROLE_BASED_AUTH_IMPLEMENTATION_PLAN.md`, `ROLE_BASED_AUTH_UI_CHANGES.md`, `ROLES_CLARIFICATION.md`. Their progress tables are stale ("Not Started").

## Feature in one paragraph
Platform roles (`auth_service.roles`: super_admin/developer/analyst/viewer/end_user) give a user a rate limit (rpm) and a daily cost limit. Entry points can set `required_role`. Enforced only for `user_jwt` mode. Distinct from tenant roles (`them.tenant_roles`, which apps a user may open).

## Status (verified against code + tests, 2026-10-25)
| Phase | Status | Evidence |
|---|---|---|
| 1 Canvas role enforcement | Backend done | `6761602b`; `db/124` applied; `execution/roles.go`, `lifecycle.go` Admit 3.7; tests S1-182 |
| 2 Canvas per-user quotas | Backend done | `bbfcd68a`; `execution/quotas.go`, `ratelimit/user_limiter.go`; tests S1-183 |
| 3 Gateway user JWT | Unclear | no dedicated commit; `llmgateway/handler.go` reads JWT claims; `db/125` adds `gateway_requests.user_id`. Confirm the gateway accepts `user_jwt` auth |
| 4 Gateway per-user quotas | Backend done | `69a75a42`; `handler_quota_test.go` |
| 5 X-External-User | Deferred | — |
| UI | Not started | no `required_role` in `frontend/src` |

Tests: `go test ./internal/execution/... ./internal/llmgateway/... ./internal/ratelimit/... ./internal/epconfig/... ./internal/admin/...` all pass (2026-10-25, run via `golang:1.25` container; `go` is not on the host PATH). Live E2E scenarios `48/49/50` (test-runner JSON) exist but have **never been confirmed run**.

## Gaps (the work left)
1. **No admin API/DAL writes `required_role`** — no code in `go/internal/admin` handles it; today it can only be set in SQL. Needed before any UI.
2. **Export/import + Deploy to Tenant lose it** — `admin/dal/app_deploy.go` `new_eps` INSERT omits `required_role` (violates the export-completeness hard rule). Check `AppDefinitionDoc` export too.
3. **Fail-open quotas** — role/rate/cost lookup errors let the request through (execution and gateway). Decide: keep (availability) or fail closed. Related: F-26.
4. **Role-change lag / JWT expiry** — design doc notes up to 5 min lag; confirm real behaviour.
5. **Cost tracking delay** — daily cost reads `run_usage`; confirm gateway spend is included.
6. **Docs** — add a "platform roles & quotas" section to `docs/architecture/auth-and-roles.md` and `run-lifecycle.md` (Admit 3.7/3.8); new Redis key `rl:user:{id}:minute:{ts}` must appear in `redis-usage.md` / generated reference.
7. **UI** (after 1–2): Entry Point "Required Role" dropdown (user_jwt only); show 402/429 messages; profile role+quota; admin role mgmt (optional). Design in archived `ROLE_BASED_AUTH_UI_CHANGES.md`.

## Suggested order
1. Run E2E 48–50 on the dev stack; settle Phase 3.
2. Gap 1 + 2 together (admin API field + deploy copy + test + `app-export-import-deploy.md`).
3. Gap 6 docs.
4. UI phases 1 and 4, then profile/quota display.
5. Decide gap 3.
