---
title: Production Deploy (Hetzner) and Environment Variables
type: guide
status: current
last_verified: 2026-10-25   # compose files and deploy.sh read; nothing deployed or run on a server
---

# Production Deploy (Hetzner) and Environment Variables

Nothing here was executed against a server; it is read from `scripts/deploy.sh`, `docker-compose.yml` and `docker-compose.hetzner.yml`.
Local development is in `docs/guides/local-dev-and-deploy.md`; external exposure (Cloudflare → Traefik) in `docs/guides/expose-externally.md` (legacy, unverified).

## What a deploy is
The production host runs the same compose project name **`them_gateway`** as local dev, using `docker-compose.yml` + the production overlay
`docker-compose.hetzner.yml` (shared external `proxy-network`, host ports for Postgres 15432, Redis 16379, Temporal 17233, bridges 8002/8003,
second bridge `them-go-bridge-2`, two orchestrator workers, two dag-workers plus the debug dag-worker, fewer demo services).

## `scripts/deploy.sh`
Run from anywhere; it derives the repo root, then requires `.env` and `secrets.local` (otherwise it stops and tells you to run `./generate-env.sh`).
| Command | What it does |
|---|---|
| `config` | renders the merged compose config (dry run; **prints secrets from `.env` — do not paste it anywhere**) |
| `build` | builds all images, starts nothing |
| `up` | `up -d --no-recreate` with `--profile temporal` — starts missing containers, **never recreates existing ones** |
| `status` | container states of the project |
| `logs [svc]` | tail logs |
| `restart <svc>` | restart one service (no rebuild) |
It never runs `down --volumes`.

## What `deploy.sh` does NOT do (important)
- **It cannot roll out new code.** `up` uses `--no-recreate` and `restart` keeps the old image. To release: `git pull`, `deploy.sh build`, then recreate the changed services yourself, e.g. `docker compose --project-name them_gateway --project-directory <repo> --env-file .env -f docker-compose.yml -f docker-compose.hetzner.yml --profile temporal up -d --build <service>` (recreate **all** dag-worker variants together; see `docs/lessons/infra-docker.md`).
- **It only enables the `temporal` profile.** `them-agent-runtime` (profile `agents`), the events pipeline (`events`: ClickHouse, events Redis, event-writer), security scanning (`security`), SSO (`sso`) and voice are not started unless you add their profiles/overlays by hand.
- **No schema migration step.** Migrations are hand-applied `psql -f db/NNN_*.sql`; there is no fresh-install path (backlog F-35/F-36).
- **No rollback, backup or smoke test.** Rolling back means checking out the previous commit and rebuilding; take a Postgres backup yourself first.

## Suggested release checklist (derived; not an official procedure)
1. `git pull`; read new `db/NNN_*.sql` migrations and apply them to Postgres first (then GRANTs for `them_app`/`them_admin`, see `docs/architecture/multi-tenancy-rls.md`).
2. `./scripts/deploy.sh build`.
3. Recreate changed services (`up -d --build <svc>`), workers all together.
4. Check `GET /health/live`, `/health/ready`, `/auth/health` on `:8088` and `docker logs` of the bridge, workers (polling lines) and auth.
5. Run a smoke: log in, open an app, run one test message (or `scripts/tests/INDEX.md` scripts on a non-production tenant).

## Environment variables
Values come from `.env` (derived by `generate-env.sh` from `secrets.local`) through compose. **Names only below — never commit or log values.**

### Secrets and keys (set in `.env`)
| Variable | Used by | Meaning |
|---|---|---|
| `THE_M_DB_USER` / `THE_M_DB_PASSWORD` | postgres, bridge, auth, mcp, agent-runtime, temporal | Postgres superuser `them` credentials (`DATABASE_*` inside containers) |
| `THEM_DB_URL_APP` / `THEM_DB_URL_ADMIN` | bridge, agent-runtime (and workers, see gap) | DSNs for roles `them_app` (RLS enforced) and `them_admin` (BYPASSRLS); bridge refuses to start without them |
| `THE_M_JWT_SECRET` → `JWT_SECRET` | bridge, auth | HS256 signing key for dashboard tokens — must be identical in both |
| `JWT_PUBLIC_KEY_PEM` | bridge | optional RS256 public key for admin JWTs |
| `THE_M_SECRET_KEY` → `SECRET_KEY` | bridge, workers, mcp, agent-runtime | derives the Fernet key that encrypts stored credentials (provider keys, MCP/agent secrets); must not be the default |
| `THE_M_IDP_ENCRYPTION_KEY` → `IDP_ENCRYPTION_KEY` | auth, bridge | 64-hex AES-256 key for tenant IdP client secrets |
| `THE_M_INVOCATION_JWT_KEY` | agent-runtime | passed in but not used by the code today (identity is plain `X-Them-*` headers, F-67) |
| `THE_M_REDIS_PASSWORD` | redis + clients | optional; Redis only requires a password if this is set (F-89) |
| `ANTHROPIC_API_KEY` (+ `OPENAI`, debate/docu variants) | bridge, workers, agents | platform-level LLM key; tenants must bring their own keys for their traffic |
| `THE_M_S3_*` (endpoint, access/secret key, buckets) | bridge | object store for File Guard quarantine/artifacts; if unset File Guard fails open |
| `LIVEKIT_API_KEY/SECRET`, `FAL_API_KEY`, `GOOGLE_MAPS_API_KEY` | voice / vision agents | third-party keys for those optional services |

### Behaviour and wiring
| Variable | Used by | Meaning / default |
|---|---|---|
| `TEMPORAL_ENABLED`, `TEMPORAL_HOST_PORT` | bridge, workers, agent-runtime | the bridge cannot start with Temporal disabled or unreachable (F-11) |
| `WORKER_TASK_QUEUE` | go-worker | orchestration queue, default `them-orchestration-go` |
| `APPFLOW_TASK_QUEUE_OVERRIDE` | dag-worker-debug | makes that worker poll `appflow-dag-debug` |
| `DAG_WORKER_MAX_CONCURRENT_ACTIVITIES` | dag-worker | activity concurrency, default 50 |
| `MCP_SERVICE_URL` | bridge, workers, agent-runtime | `http://them-mcp-service:8010` |
| `MCP_HEALTH_INTERVAL_SECONDS`, `MCP_ALLOW_HTTP`, `MCP_ALLOW_STDIO` | mcp-service | probe interval (30 s); the two ALLOW flags are read but not enforced (F-57) |
| `THE_M_PUBLIC_URL` | bridge | public base URL used in A2A agent cards |
| `THE_M_EVENTS_GATEWAY`, `THE_M_EVENTS_TOOLS` | bridge, mcp | `postgres` (default) or `dual`; `clickhouse`-only is blocked (`docs/architecture/events-pipeline.md`) |
| `RECONCILER_DRY_RUN` | bridge | default `true`: the run reconciler only logs (and cannot match runs anyway, F-08) |
| `ACCESS_TOKEN_EXPIRY` / `REFRESH_TOKEN_EXPIRY` | auth | seconds; defaults 3600 / 604800 |
| `APP_ENV`, `APP_PORT`, `LOG_LEVEL`, `LOG_FORMAT`, `THE_M_INSTANCE_ID` | most Go services | environment label, listen port, logging, replica identity (`go-bridge-2` etc.) |
| `TENANT_LOGO_DIR` | bridge | directory served at `/static/tenants` |

### Frontend
`THE_M_API_URL` (bridge base, in compose via Traefik), `THE_M_AUTH_URL` (auth service), `COOKIE_SECURE` (`false` disables the Secure flag),
`NODE_ENV`, `PORT`, `HOSTNAME`, `NEXT_PUBLIC_BRIDGE_WS_URL` (baked at **build** time). See `docs/architecture/frontend.md`.

## Known gaps (found while writing, 2026-10-25)
See backlog F-103…F-104.
1. `THEM_DB_URL_APP/ADMIN` are defined for `them-go-worker` and `them-dag-worker` only in `docker-compose.dev.yml`, not in the base or production overlay; if the workers require them, a `deploy.sh` stack would not start them. Verify on the server.
2. `deploy.sh` cannot deploy (no recreate), skips four profiles, has no migration/backup/rollback/smoke step, and `config` prints secrets.
3. `docker-compose.hetzner.yml`'s header still describes merged files that no longer exist (`linux`, `integration`, `soak`, `traefik`, `cloudflare`) and a Python/Go route split that is gone; `version:` keys are obsolete.

## Related
`local-dev-and-deploy.md`, `expose-externally.md`, `docs/architecture/platform-overview.md`, `auth-and-roles.md`, `multi-tenancy-rls.md`, `redis-usage.md`
