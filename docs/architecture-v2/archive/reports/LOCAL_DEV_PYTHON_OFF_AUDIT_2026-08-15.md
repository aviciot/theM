# Local Dev — Python-OFF Route Audit
# Generated: 2026-08-15
# HEAD: ca29acd (local dev, aligned to origin/main)

---

## Context

This audit was performed after moving active development from Hetzner to the local Linux server.
It answers: **what breaks when only Python bridge is stopped and Go bridge is never started?**

Stack state during test:
- RUNNING: `them-traefik`, `them-postgres`, `them-redis`, `them-auth-go`, `them-frontend`, `temporal-frontend`, `temporal-ui`, `vision-agent`
- STOPPED: `them-bridge` (Python), `them-worker` (Python Temporal)
- NOT STARTED: `them-go-bridge` (profile `go` — not active in this dev stack)

The key finding: **`them-go-bridge` is never started in the local dev default mode.**
Without it, Traefik knows no routes for `/api/v1/`, `/health/`, `/ws/`, `/sse/`, `/apps/`, `/a2a/`.
The only Traefik-registered routes are: `/auth` (auth-go), `/temporal` (temporal-ui), `/` (frontend).

---

## Phase 10 — Test Results

### Traefik router inventory (Python-OFF state)

| Status  | Priority | Router name              | Rule                    |
|---------|----------|--------------------------|-------------------------|
| enabled | MAX      | api@internal             | PathPrefix(`/api`)      |
| enabled | MAX      | dashboard@internal       | PathPrefix(`/`)         |
| enabled | 120      | them-auth-go-router      | PathPrefix(`/auth`)     |
| enabled | 150      | them-temporal-ui         | PathPrefix(`/temporal`) |
| enabled | 10       | them-ui                  | PathPrefix(`/`)         |

**No routers for `/api/v1/`, `/health/`, `/ws/`, `/sse/`, `/apps/`, `/a2a/`.**

### Route test matrix

| Method | Path                            | Status | Traefik target     | Go handler? | Python router?        |
|--------|---------------------------------|--------|--------------------|-------------|-----------------------|
| GET    | /                               | 200    | them-frontend      | No          | No                    |
| GET    | /admin                          | 404    | them-frontend      | No          | No (SPA Next.js 404)  |
| GET    | /auth/health                    | 200    | them-auth-go       | Yes         | —                     |
| GET    | /auth/api/v1/auth/me            | 401    | them-auth-go       | Yes         | —                     |
| GET    | /health/live                    | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /health                         | 404    | no router          | No          | Yes (Python)          |
| GET    | /api/v1/admin/agents            | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /api/v1/admin/orchestrators     | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /api/v1/admin/applications      | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /api/v1/admin/tokens            | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /api/v1/admin/sessions          | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /api/v1/admin/llm-providers     | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /api/v1/admin/monitoring-config | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /api/v1/runs                    | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /api/v1/runs/stats              | 404    | no router          | No          | Yes (Python)          |
| GET    | /apps                           | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /apps/default                   | 404    | no router          | Yes (Go)    | Yes (Python)          |
| GET    | /a2a                            | 404    | no router          | Yes (Go)    | Yes (Python)          |

### Root cause summary

The 404s are NOT because Go doesn't have handlers — Go has full Wave 1-8 coverage.
The 404s are because **`them-go-bridge` is not running**, so Traefik has no backend for Go routes.

Without `--profile go`, the dev stack is:
- Auth → auth-go (always live)
- Admin + WS/SSE + runs → no backend (404)
- Frontend → them-frontend (always live)

---

## Phase 11 — Missing Go contracts (routes without any Go implementation)

These are routes that exist in Python but have **no Go handler** yet, regardless of deployment state.

### Priority 1 — Runs audit tail (next recommended task)

| Route                                         | Python file              | Status    |
|-----------------------------------------------|--------------------------|-----------|
| `GET /api/v1/runs/stats`                      | `app/routers/runs.py`    | No Go handler |
| `GET /api/v1/runs/contexts`                   | `app/routers/runs.py`    | No Go handler |
| `GET /api/v1/runs/{id}/tasks`                 | `app/routers/runs.py`    | No Go handler |
| `GET /api/v1/runs/{id}/artifacts`             | `app/routers/runs.py`    | No Go handler |
| `GET /api/v1/runs/context/{ctx_id}/artifacts` | `app/routers/runs.py`    | No Go handler |

These are pure SQL reads. No new schema. Self-contained.

### Priority 2 — Runs writes

| Route                                         | Python file              | Status    |
|-----------------------------------------------|--------------------------|-----------|
| `PATCH /api/v1/runs/{id}/cancel`              | `app/routers/runs.py`    | No Go handler |
| `DELETE /api/v1/runs/{id}`                    | `app/routers/runs.py`    | No Go handler |
| `POST /api/v1/runs/bulk-delete`               | `app/routers/runs.py`    | No Go handler |

### Priority 3 — Applications sub-routes (LLM/voice tests + import/export/restore)

| Route                                                         | Python file                       | Status    |
|---------------------------------------------------------------|-----------------------------------|-----------|
| `GET /api/v1/admin/applications/{id}/export`                  | `app/routers/admin_applications.py` | No Go handler |
| `POST /api/v1/admin/applications/import`                      | `app/routers/admin_applications.py` | No Go handler |
| `PUT /api/v1/admin/applications/{id}/restore`                 | `app/routers/admin_applications.py` | No Go handler |
| `PUT /api/v1/admin/applications/{id}/middleware-wirings`      | `app/routers/admin_applications.py` | No Go handler |
| `POST /api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-llm` | `app/routers/admin_applications.py` | No Go handler |
| `POST /api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-voice` | `app/routers/admin_applications.py` | No Go handler |
| `POST /api/v1/admin/applications/{id}/orchestrators/{ao_id}/test-tts` | `app/routers/admin_applications.py` | No Go handler |

### Priority 4 — Orchestrators LLM/voice test endpoints

| Route                                              | Python file                        | Status    |
|----------------------------------------------------|------------------------------------|-----------|
| `POST /api/v1/admin/orchestrators/{id}/test-llm`   | `app/routers/admin_orchestrators.py` | No Go handler |
| `POST /api/v1/admin/orchestrators/{id}/test-voice` | `app/routers/admin_orchestrators.py` | No Go handler |
| `POST /api/v1/admin/orchestrators/{id}/test-tts`   | `app/routers/admin_orchestrators.py` | No Go handler |

### Priority 5 — LLM routing config

| Route                                              | Python file                    | Status    |
|----------------------------------------------------|--------------------------------|-----------|
| `GET /api/v1/admin/llm-providers/routing/config`   | `app/routers/admin_llm_providers.py` | No Go handler |
| `PUT /api/v1/admin/llm-providers/routing/config`   | `app/routers/admin_llm_providers.py` | No Go handler |

### Priority 6 — Apps REST endpoints (non-WS/SSE)

| Route                                              | Python file              | Status    |
|----------------------------------------------------|--------------------------|-----------|
| `GET /apps`                                        | `app/routers/apps.py`    | Go handler exists (`MountApps`) |
| `GET /apps/{slug}`                                 | `app/routers/apps.py`    | Go handler exists |
| `POST /apps/{slug}`                                | `app/routers/apps.py`    | Go handler exists |
| `GET /apps/{slug}/tasks/{task_id}`                 | `app/routers/apps.py`    | Verify Go coverage |
| `POST /apps/{slug}/voice/transcribe`               | `app/routers/apps.py`    | No Go handler |
| `POST /apps/{slug}/voice/tts`                      | `app/routers/apps.py`    | No Go handler |

### Priority 7 — Middleware definitions

| Route                                              | Python file                    | Status    |
|----------------------------------------------------|--------------------------------|-----------|
| `GET /api/v1/admin/middleware`                     | `app/routers/admin_middleware.py` | No Go handler |
| `POST /api/v1/admin/middleware`                    | `app/routers/admin_middleware.py` | No Go handler |
| `GET /api/v1/admin/middleware/{id}`                | `app/routers/admin_middleware.py` | No Go handler |
| `PATCH /api/v1/admin/middleware/{id}`              | `app/routers/admin_middleware.py` | No Go handler |
| `DELETE /api/v1/admin/middleware/{id}`             | `app/routers/admin_middleware.py` | No Go handler |

### Priority 8 — System agents

| Route                                              | Python file                      | Status    |
|----------------------------------------------------|----------------------------------|-----------|
| `GET /api/v1/admin/system-agents`                  | `app/routers/admin_system_agents.py` | No Go handler |
| `PUT /api/v1/admin/system-agents`                  | `app/routers/admin_system_agents.py` | No Go handler |
| `POST /api/v1/admin/system-agents/{role}/test-llm` | `app/routers/admin_system_agents.py` | No Go handler |

### Already in Go (confirmed by handler inspection)

These routes are Go-implemented and registered. They return 404 locally only because `them-go-bridge` is not started (no `--profile go`):

- `GET/POST/PATCH/DELETE /api/v1/admin/agents` + discover, test, security-scan
- `GET/POST/PATCH/DELETE /api/v1/admin/orchestrators`
- `GET/POST/PUT/PATCH/DELETE /api/v1/admin/applications` + entry-points + runtime + bulk-delete
- `GET/POST/PATCH/DELETE /api/v1/admin/tokens`
- `GET /api/v1/admin/sessions` + disconnect
- `GET/POST/PATCH/DELETE /api/v1/admin/llm-providers`
- `GET/PUT /api/v1/admin/monitoring-config`
- `GET /api/v1/runs` + `GET /api/v1/runs/{id}` + `POST /api/v1/runs/{id}/signal`
- `/ws/orchestrate/{app}/{ep}`, `/sse/orchestrate/{app}/{ep}`
- `/apps/{slug}/ws`, `/apps/{slug}/sse`
- `GET/HEAD /health/live`, `GET /health/ready`
- `/a2a` (A2A server)
- `/api/v1/runs/{run_id}/artifacts/{artifact_id}` (artifact download)

---

## Local dev — how to enable Go routes

To enable Go route handling locally (matches Hetzner prod behavior):

```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal --profile go \
  up -d
```

With `--profile go`, `them-go-bridge` starts and Traefik adds Go routes at higher priority (110-130) than Python (100).

---

## Known baseline issue (pre-existing)

`GET /api/v1/runs` returned 500 with Python ON (before stopping Python):
- Error: `ValidationError: 4 validation errors for RunOut` 
- One run row has `orchestrator_id=NULL, user_id=NULL, session_id=NULL, goal=NULL`
- This is a Go-created Temporal run that didn't populate these fields
- Not caused by Python removal; pre-existing data issue
