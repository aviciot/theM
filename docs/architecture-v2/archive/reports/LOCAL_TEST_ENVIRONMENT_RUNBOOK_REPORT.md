# Local Test Environment Runbook — Creation Report

**Date:** 2026-07-26  
**Task:** Document local/test deployment configuration for future Claude sessions.

---

## Files Created or Updated

| File | Action | Notes |
|---|---|---|
| `docs/architecture-v2/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md` | Created | Full runbook — 8 sections |
| `.env.example` | Updated | Added Go bridge requirement notes, variable mapping comment, fake placeholders updated |
| `CLAUDE.md` | Updated | Added runbook row to "Read These First" table + mandatory rule |

---

## What Was Inspected

### Running containers

Inspected with `docker ps`, `docker inspect`, and `docker logs`:
- `them-bridge` — project `them_gateway`, working dir `/opt/docker/them/theM_gateway`
- `them-go-bridge` — project `them`, working dir `/opt/docker/them`
- `them-traefik` — project `them_gateway`
- `them-postgres`, `them-redis`, `them-auth-service`, `them-worker`, `them-frontend` — all `them_gateway`

### Compose files read

All files inspected directly (not from memory or assumption):
- `/opt/docker/them/theM_gateway/docker-compose.yml`
- `/opt/docker/them/theM_gateway/docker-compose.linux.yml`
- `/opt/docker/them/theM_gateway/docker-compose.integration.yml`
- `/opt/docker/them/theM_gateway/docker-compose.soak.yml`
- `/opt/docker/them/theM_gateway/docker-compose.traefik.yml`
- `/opt/docker/them/theM_gateway/docker-compose.local.yml`
- `/opt/docker/them/docker-compose.yml`
- `/opt/docker/them/docker-compose.local.yml`

### Config source files read

- `go/internal/config/config.go` — all Go env var names, validation rules, `SafeString()`
- `app/config.py` — all Python env var names
- `auth_service/config/settings.py` — auth service env var names
- `generate-env.sh` — secret derivation logic and exact `.env` output format

### Variable name mismatches documented

The following naming inconsistencies were confirmed in running containers and documented:

| `.env` variable | Container variable |
|---|---|
| `THE_M_SECRET_KEY` | `SECRET_KEY` |
| `THE_M_DB_USER` | `DATABASE_USER` |
| `THE_M_DB_PASSWORD` | `DATABASE_PASSWORD` |
| `THE_M_JWT_SECRET` | `JWT_SECRET` |
| `THE_M_REDIS_PASSWORD` | `REDIS_PASSWORD` |

Python bridge uses `TEMPORAL_HOST` (hostname only); Go bridge uses `TEMPORAL_HOST_PORT` (host:port). Documented separately.

---

## Security Checks

### .env and secrets.local are gitignored

Confirmed via `grep` on `.gitignore`:
```
.env
.env.test
.env.local
secrets.local
```

Confirmed not tracked via `git ls-files`:
- `/opt/docker/them/.env` — not tracked ✓
- `/opt/docker/them/secrets.local` — not tracked ✓

### No real secrets in any committed file

The following was verified in the git diff before committing:

- `LOCAL_TEST_ENVIRONMENT_RUNBOOK.md` — contains no secret values; uses descriptions and placeholder variable names only
- `.env.example` — updated placeholders are `replace-with-local-test-secret` and `replace-with-local-jwt-secret`; no real values
- `CLAUDE.md` — references runbook path only; no secrets, no values
- `LOCAL_TEST_ENVIRONMENT_RUNBOOK_REPORT.md` (this file) — no secret values

No secret values were copied from container inspection into any committed file.

### Application code unchanged

Confirmed: only documentation files and `.env.example` were modified. No files under `app/`, `go/`, `auth_service/`, `frontend/`, `agents/`, `db/`, or `scripts/` were changed.

---

## Key Findings Documented

1. **Two-project architecture**: `them_gateway` project (all production services) and `them` project (Go bridge only). This split was the root cause of the Traefik cutover failures in Wave 7 — documented in L-11 and L-12 in `lessons-learned.md`.

2. **`THE_M_JWT_SECRET` is critical for Go bridge**: Without it, Go bridge falls back to `SECRET_KEY` for HS256 JWT validation — a different key from the auth service. Silent 401 on all admin routes. This was the issue discovered and fixed during Wave 7 Phase 3b.

3. **`traefik-instance=them` label is mandatory**: Traefik's Docker provider uses this label as a discovery constraint. Missing it means zero routers visible — also a Wave 7 discovery.

4. **`generate-env.sh` derives all secrets from a single master passphrase**: If `secrets.local` is lost, all derived secrets change and Fernet-encrypted DB values become unreadable. Recovery requires restoring the original passphrase or re-entering all API keys.

5. **Compose command for the running stack**: Verified from `docker inspect` — the production stack uses 5 compose files from `theM_gateway/` with `--profile temporal`.

---

## No Application Code Changed

Confirmed with `git diff --stat`:
```
CLAUDE.md
.env.example
docs/architecture-v2/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md
docs/architecture-v2/LOCAL_TEST_ENVIRONMENT_RUNBOOK_REPORT.md
```

No `.go`, `.py`, `.ts`, `.sql`, or Dockerfile files were modified.
