# Current Session State — the-M
# Last updated: 2026-08-15
# Replaces: NEXT_SESSION_BRIDGE_HANDOVER.md, NEXT_SESSION_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `888861b` — fix(traefik): sync Go bridge route labels in base compose with Hetzner overlay

---

## Deployment state

All containers healthy. See `docs/STATUS.md` for full container list.

Key facts:
- `them-auth-go` is sole auth service — Python `them-auth-service` removed from compose
- `them-go-bridge` serves all Go-owned routes behind Traefik
- `them-bridge` (Python) still handles remaining routes
- Both Python and Go Temporal workers are registered

---

## Current migration slice

**Agents Store — COMPLETE** (888861b and prior commits this cycle)

What was done:
- `POST /agents/discover` → Go (classify via Anthropic, fetch card)
- `POST /agents/{id}/test` → Go (connectivity check, latency_ms)
- `POST /agents/{id}/security-scan` → Go (async scan job, 202 response)
- `AdminTenantMiddleware` replacing `BearerTenantMiddleware` for UI admin routes
- Go auth service cutover complete; Python auth container removed

---

## Next recommended task

**Runs read/audit tail** — port these Python routes to Go:
- `GET /api/v1/runs/stats`
- `GET /api/v1/runs/contexts`
- `GET /api/v1/runs/{id}/tasks`
- `GET /api/v1/runs/{id}/artifacts`
- `GET /api/v1/runs/context/{ctx_id}/artifacts`

No new schema. Pure SQL reads. Self-contained, one session.

After that: runs writes (cancel, delete, bulk-delete).

---

## Known blockers

1. Auth admin CRUD (users/roles/teams) — not exposed since Python auth removed. Needs Go port.
2. Python Temporal worker is still primary orchestration path. Go worker is parallel but not sole owner.
3. A2A server (`/a2a/*`) still on Python — not yet migrated to Go.

---

## Hard constraints (always in force)

- DB name: `them`, never `odin`
- Never query `auth_service.*` from bridge — use `go/internal/auth/` or `app/services/auth_client.py`
- Bootstrap tenant ID: `00000000-0000-0000-0000-000000000001`
- `go test ./...` must pass before every commit
- `go/TEST_INDEX.md` updated in same commit as new Go tests
- Secrets never in logs — use `cfg.SafeString()`
- Never `git add .` or `git add -A`

---

## Documentation rules (forward)

1. One source of truth per subject.
2. Completed plans/reports → `docs/architecture-v2/archive/`.
3. Update this file (CURRENT.md) at session end — do NOT create new NEXT_SESSION_*.md files.
4. ADRs are permanent — never archive them.
5. STATUS.md describes now, not history.
6. ARCHITECTURE.md describes current design, not migration chronology.
7. REMAINING_ROUTE_OWNERSHIP_INVENTORY.md is temporary — remove when Python is gone.
8. Documentation changes ship in same commit as the code changes they describe.
9. Never create another competing active architecture directory.
10. Code is final truth; stale canonical docs are a bug.
