---
title: Local Dev, Docker Compose & Deploy
type: guide
status: current
last_verified: 2026-10-25   # health URLs and running containers checked live; deploy.sh read, not run
---

# Local Dev, Docker Compose & Deploy

Replaces the old 760-line runbook (archived as `docs/_archive/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md`; it described removed Python containers).
What each container is: `docs/architecture/platform-overview.md`.

## Compose files and profiles
| File | Purpose |
|---|---|
| `docker-compose.yml` | all services (production defaults) |
| `docker-compose.dev.yml` | local dev overlay (source bind mounts, `npm run dev`) |
| `docker-compose.temporal.yml` | sets `TEMPORAL_ENABLED=true` for agent-runtime and related settings |
| `docker-compose.events.yml` | ClickHouse + events Redis + event-writer (profile `events`) |
| `docker-compose.hetzner.yml` | production overlay (bridge replica 2, host ports, fewer demo services); used by `scripts/deploy.sh` |

Profiles (opt-in groups): `temporal` (Temporal + all workers — **required**, the bridge cannot start without Temporal), `events`, `sso` (Keycloak),
`test-agents`, `security` (ClamAV/MinIO/scanner), `voice`, `debate`, `docu`, `advisor`. Compose **project name is always `them_gateway`**.

## Start, check, stop (local)
```bash
cd /opt/docker/them
# core + Temporal (needed for any orchestrated / App Canvas run)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
# add what you need: events (also -f docker-compose.events.yml), sso, test-agents, security …
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal ps
docker logs them-go-bridge --tail 50
```
Health through Traefik (`:8088`): `/health/live` and `/health/ready` (bridge), `/auth/health` (auth service), `/` (frontend), `/temporal/` (Temporal UI).
Note `/health` alone returns 404. Never use `docker compose down --volumes` on a stack with data you want.

## Rebuild rules (these burned us before — see `docs/lessons/infra-docker.md`, `temporal-workers.md`)
- `docker restart` / `compose restart` does **not** switch to a newly built image. Use `up -d --build <service>` (recreates the container).
- Each service builds its own image tag. For the `them-dag-worker`, `-2`, `-3`, `-4` and `-debug` workers, rebuild and recreate **all** of them or some keep the old binary.
- Worker code changes (`go/internal/temporal`, `go/internal/appflow`, `go/cmd/dag-worker`, `go/cmd/worker`) need all matching workers recreated.

## Secrets and `.env`
- `./generate-env.sh` reads `secrets.local` (created from `secrets.local.example` on first run; set `THE_M_MASTER_SECRET` to something like `openssl rand -hex 32`) and writes `.env` with derived secrets. `.env` and `secrets.local` are never committed or printed.
- Compose consumes: `THE_M_DB_USER`, `THE_M_DB_PASSWORD`, `THE_M_JWT_SECRET`, `THE_M_SECRET_KEY`, `THE_M_IDP_ENCRYPTION_KEY`, `THE_M_INVOCATION_JWT_KEY`, `THE_M_REDIS_PASSWORD` (optional), `THE_M_PUBLIC_URL`, token-expiry settings. The auth service and the bridge must share the same JWT secret.
- Regenerating from the same `secrets.local` gives the same values; losing it means re-entering anything that was encrypted with the old key (see `docs/architecture/auth-and-roles.md`).

## Databases
```bash
docker exec -it them-postgres psql -U them -d them     # Postgres (superuser `them`; services use `them_app` / `them_admin`)
docker exec them-redis redis-cli                        # Redis (inside the container; see docs/architecture/redis-usage.md)
```
Schema changes are hand-applied `db/NNN_*.sql` files; there is **no reliable fresh-install script** (backlog F-35/F-36). Current schema: `docs/reference/schema-generated.md`.

## Production (Hetzner)
`scripts/deploy.sh config | build | up | status | logs [svc] | restart <svc>` — wraps `docker-compose.yml` + `docker-compose.hetzner.yml` with project name `them_gateway`, anchors paths to the repo root, requires `.env` and `secrets.local`, and never runs `down --volumes`. (Read from the script; not executed in this verification.)

## SSO / Keycloak test environment
Start with `--profile sso`. Realm exports for the test tenants are in `keycloak/` (`bank-realm.json`, `payops_ai-realm.json`, `rnd-realm.json`); Keycloak is reachable through Traefik under `/auth/keycloak`. The E2E check is `scripts/tests/test_38_phase4_external_jwt.py` (see `scripts/tests/INDEX.md`).

## Other things that run locally
- `them-test-runner-backend` (host `:8093`) and `them-test-runner-frontend` (host `:3210`): a separate load/scenario test app in `test-runner/` — not yet documented (docs gap).
- `them-control-plane` and `kind-registry` look like a local `kind` (Kubernetes-in-Docker) cluster; they are not defined in this repo's compose files (unverified what uses them).
