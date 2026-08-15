# Compose Root Preparation Report — Stage A
**Date:** 2026-08-01
**Stage:** A (Prepare root Compose — no containers touched)
**Based on:** `docs/architecture-v2/COMPOSE_LAYOUT_CONSOLIDATION_PLAN.md`

---

## What Was Done

Five Compose overlay files were copied from `theM_gateway/` to the repository root (`/home/avi/them`). Build contexts referencing `..` (the parent directory from inside `theM_gateway/`) were rewritten to `.` (already the root). No running containers were touched, no secrets were created, `theM_gateway/` was not deleted.

---

## Files Copied or Created

| File | Source | Path changes made |
|---|---|---|
| `docker-compose.linux.yml` | `theM_gateway/docker-compose.linux.yml` | None — no `..` references |
| `docker-compose.traefik.yml` | `theM_gateway/docker-compose.traefik.yml` | None — no `..` references |
| `docker-compose.integration.yml` | `theM_gateway/docker-compose.integration.yml` | 3× `context: ..` → `context: .` |
| `docker-compose.soak.yml` | `theM_gateway/docker-compose.soak.yml` | 1× `context: ..` → `context: .` |
| `docker-compose.cloudflare.yml` | `theM_gateway/docker-compose.cloudflare.yml` | None — no `..` references |

Dockerfile referenced by integration.yml and soak.yml for Go Workers: `Dockerfile.go-worker` (canonical, created 2026-07-29, 900 bytes). This file already exists at the root and is the correct one.

---

## Validation — `docker compose config`

Command run from `/home/avi/them`:
```
docker compose \
  --project-name them_gateway \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal \
  config --quiet
```

**Result:** No errors. Warnings only:
- `version` attribute obsolete (cosmetic — all 6 files use `version: "3.9"`)
- `LIVEKIT_API_KEY` / `LIVEKIT_API_SECRET` not set (expected — no `.env` at root yet)

---

## Service Comparison

### Rendered by root Compose (`--profile temporal`)
```
temporal-admin-tools
temporal-frontend
temporal-ui
them-auth-service
them-bridge
them-frontend
them-go-bridge
them-go-bridge-2
them-go-worker
them-go-worker-2
them-postgres
them-redis
them-traefik
them-worker
vision-agent
```
**Total: 15 services**

### Currently running in `them_gateway` project
```
temporal-admin-tools
temporal-frontend
temporal-ui
them-auth-service
them-bridge
them-frontend
them-go-bridge
them-go-bridge-2
them-go-worker
them-go-worker-2
them-postgres
them-redis
them-traefik
them-worker
vision-agent
```
**Total: 15 containers**

### Match: EXACT — all 15 services align

Note: `a2a-echo`, `a2a-slow`, `a2a-stream`, debate agents (`agent-evidence/logic/creative/judge`) are defined in root `docker-compose.yml` under `test-agents` and `debate` profiles respectively. They render when those profiles are activated. The 4 debate agents currently running (`agent-judge`, `agent-creative`, `agent-evidence`, `agent-logic`) belong to the running `them_gateway` project under the `debate` profile — they would be included if `--profile temporal --profile debate` is used.

---

## Missing Environment Variable Names

These variables have no fallback (or have `:-` empty fallback) and require `.env` to be set before the stack can run correctly:

| Variable | Used by | Required for |
|---|---|---|
| `THE_M_SECRET_KEY` | all bridges, workers | Fernet encryption, config validation (must not be default) |
| `THE_M_JWT_SECRET` | Go bridge, auth service, workers | HS256 JWT validation |
| `THE_M_DB_PASSWORD` | all services connecting to Postgres | DB authentication |
| `THE_M_DB_USER` | all services connecting to Postgres | DB authentication |
| `THE_M_REDIS_PASSWORD` | bridges, workers | Redis AUTH |
| `ANTHROPIC_API_KEY` | bridges, workers, agents | LLM calls |
| `THE_M_CORS_ORIGINS` | bridge, auth service | CORS policy |
| `THE_M_HOSTNAME` | (in legacy `.env` — not referenced in Compose files directly) | Traefik host rule in production |
| `THE_M_UI_HOSTNAME` | (in legacy `.env` — not referenced in Compose files directly) | Frontend hostname |
| `LIVEKIT_API_KEY` / `LIVEKIT_API_SECRET` | livekit, livekit-agent | Voice profile only |
| `LIVEKIT_PUBLIC_URL` | them-bridge | Voice routing |
| `DEBATE_ANTHROPIC_API_KEY` | debate agents | Debate profile only |
| `DOCU_WRITER_ANTHROPIC_API_KEY` | docu-writer | Docu profile only |
| `SECURITY_SCANNER_ANTHROPIC_API_KEY` | them-security-agent | Security profile only |
| `APP_ENV` | bridges, worker | Environment label |
| `LOG_LEVEL` | all services | Log verbosity |
| `RUN_EVENTS_MODE` | Go bridges | Streams vs pubsub vs dual |
| `JWT_PUBLIC_KEY_PEM` | Go bridge | RS256 JWT (optional — falls back to HS256) |
| `ANTHROPIC_MODEL` | bridges, agents | LLM model selection |
| `GOOGLE_MAPS_API_KEY` / `FAL_API_KEY` | vision-agent | Vision profile |
| `OPENAI_API_KEY` | livekit-agent | Voice profile only |

**Variables needed before any `up`:** `THE_M_SECRET_KEY`, `THE_M_JWT_SECRET`, `THE_M_DB_PASSWORD`, `THE_M_DB_USER`, `THE_M_REDIS_PASSWORD`, `ANTHROPIC_API_KEY`

---

## What Is Preserved

| Concern | Status |
|---|---|
| `them-postgres-data` named volume | Preserved — `docker-compose.linux.yml` declares `name: them-postgres-data` |
| `them-redis-data` named volume | Preserved — `docker-compose.linux.yml` declares `name: them-redis-data` |
| `them-logs` named volume | Preserved — `docker-compose.linux.yml` declares `name: them-logs` |
| External `proxy-network` | Preserved — `docker-compose.cloudflare.yml` overrides to `external: true`, `name: proxy-network` |
| All Traefik routing labels (Waves 1–7) | Preserved — `docker-compose.traefik.yml` copied verbatim |
| Cloudflare configuration | Preserved — `docker-compose.cloudflare.yml` copied verbatim |
| `them-go-bridge-2` | Preserved — defined in `docker-compose.soak.yml` |
| Both Go Workers | Preserved — defined in `docker-compose.integration.yml`, using `Dockerfile.go-worker` |
| Python bridge, worker, auth service | Preserved — base `docker-compose.yml` unchanged |
| Project name `them_gateway` | Preserved via `--project-name them_gateway` flag |

---

## Root Compose Structural Readiness

**Root Compose is now structurally ready** to represent the full running stack.

The remaining prerequisite before any `up` is Stage B from the consolidation plan:
1. Copy `.env` from `theM_gateway/.env` to root (not done — awaiting explicit instruction per Stage A scope)
2. Copy `secrets.local` from `theM_gateway/` to root (not done — same)
3. Optionally add `COMPOSE_PROJECT_NAME=them_gateway` to root `.env` to avoid needing `--project-name` on every command

No code changes, no container restarts, no migrations required for Stage A. The 5 new files are the only addition.

---

## Remaining Gap: `docker-compose.hetzner-build.yml`

The legacy stack uses `docker-compose.hetzner-build.yml` to override build contexts for all services to `..`. At root, those contexts are already `.` — so the hetzner-build.yml is **not needed at root**. The `context: ..` → `context: .` rewrite in integration.yml and soak.yml handles the equivalent for Go services. All other build contexts in `docker-compose.yml` already point to `./auth_service`, `./frontend`, `./agents/*` etc. — correct relative paths from root.

---

## Complete Root Compose File List (after Stage A)

```
docker-compose.yml           # base stack (unchanged)
docker-compose.linux.yml     # NEW — named volumes, production overrides
docker-compose.integration.yml  # NEW — Go Workers + Go bridge with full env, test ports
docker-compose.soak.yml      # NEW — them-go-bridge-2 replica
docker-compose.traefik.yml   # NEW — Waves 1–7 Go bridge Traefik routing
docker-compose.cloudflare.yml   # NEW — external proxy-network (Cloudflare ingress)
docker-compose.local.yml     # existing — local dev overlay (not used in production)
```

Production launch command (Stage F):
```bash
cd /home/avi/them
docker compose \
  --project-name them_gateway \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  -f docker-compose.cloudflare.yml \
  --profile temporal \
  up -d --no-recreate
```
