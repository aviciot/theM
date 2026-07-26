# Local / Test Environment Runbook

**the-M — multi-agent orchestration platform**  
**Last updated:** 2026-07-26  
**Mandatory reading:** before any Docker, deployment, environment, or container recreation work.

---

## 1. Compose File Architecture

### Two separate Compose projects run simultaneously

The stack is split across two directories. Understanding which project owns which container is critical before running any `docker compose` command.

| Compose project | Directory | Services owned |
|---|---|---|
| `them_gateway` | `/opt/docker/them/theM_gateway/` | `them-traefik`, `them-postgres`, `them-redis`, `them-auth-service`, `them-bridge`, `them-frontend`, `them-worker`, Temporal services |
| `them` | `/opt/docker/them/` | `them-go-bridge` only |

> **Why two projects?** The production stack was deployed from `theM_gateway/` before the Go bridge existed. The Go bridge was added later as a `--profile go` service in the parent directory. Traefik (in `them_gateway`) discovers both via shared Docker labels on the `them-network` network.

### Compose files — `them_gateway` project

All commands for this project must run from `/opt/docker/them/theM_gateway/`.

| File | Purpose |
|---|---|
| `docker-compose.yml` | Base: all services, service definitions, DB/Redis/Traefik config |
| `docker-compose.linux.yml` | Linux overlay: named volumes, production-mode labels, Go-first route ownership |
| `docker-compose.integration.yml` | Integration overlay: exposes host ports (Postgres→15432, Redis→16379, Temporal→17233), adds `them-go-bridge` to the `temporal` profile with full env |
| `docker-compose.soak.yml` | Soak overlay: adds `them-go-bridge-2` (second Go bridge replica on port 8003) |
| `docker-compose.traefik.yml` | Traefik labels overlay: all Go bridge router blocks (Waves 1–7) |
| `docker-compose.local.yml` | Linux alias/stub — exists for test compatibility only; do NOT use in `-f` chains on Linux (use `docker-compose.linux.yml` instead) |

**Currently running command** (verified from `docker inspect them-bridge`):

```bash
cd /opt/docker/them/theM_gateway
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  --profile temporal up -d
```

### Compose files — `them` project (Go bridge only)

All commands for this project must run from `/opt/docker/them/`.

| File | Purpose |
|---|---|
| `docker-compose.yml` | Go bridge service definition (`--profile go`), Traefik labels for Go bridge |
| `docker-compose.local.yml` | Local dev overlay: creates `proxy-network` locally, dev-mode labels |

**Go bridge start command:**

```bash
cd /opt/docker/them
docker compose --profile go up -d them-go-bridge
```

---

## 2. Container and Service Name Reference

| Container name | Compose service | Compose project | Internal port | Host port |
|---|---|---|---|---|
| `them-traefik` | `them-traefik` | `them_gateway` | 8088, 8089 | 8088, 127.0.0.1:8089 |
| `them-postgres` | `them-postgres` | `them_gateway` | 5432 | 15432 |
| `them-redis` | `them-redis` | `them_gateway` | 6379 | 16379 |
| `them-auth-service` | `them-auth-service` | `them_gateway` | 8701 | — |
| `them-bridge` | `them-bridge` | `them_gateway` | 8001 | — |
| `them-frontend` | `them-frontend` | `them_gateway` | 3200 | — |
| `them-worker` | `them-worker` | `them_gateway` | — | — |
| `them-go-bridge` | `them-go-bridge` | `them` | 8002 | 8002 |
| `temporal-frontend` | `temporal-frontend` | `them_gateway` | 7233 | 17233 |

Access via Traefik at `http://localhost:8088` for all routes.  
Direct Go bridge access (bypasses Traefik): `http://localhost:8002`.

---

## 3. Compose Commands

All commands below specify the directory they must run from.

### Inspect the running stack

```bash
# Show all them-related containers and health
docker ps --filter "name=them" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# Which compose files started a container
docker inspect them-bridge --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' | tr ',' '\n'
docker inspect them-go-bridge --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' | tr ',' '\n'

# Environment variable names present in a container (names only — no values)
docker inspect them-go-bridge --format '{{range .Config.Env}}{{println .}}{{end}}' | cut -d= -f1 | sort
```

### Go bridge — build, restart, recreate, stop

```bash
cd /opt/docker/them

# Rebuild image only
docker compose --profile go build them-go-bridge

# Recreate container (picks up new image and updated docker-compose.yml labels)
docker compose --profile go up --no-deps -d them-go-bridge

# Stop and remove container (stack keeps running without it)
docker compose --profile go stop them-go-bridge
docker compose --profile go rm -f them-go-bridge

# Tail logs
docker logs -f them-go-bridge

# Follow last 50 lines
docker logs them-go-bridge --tail 50
```

### Python bridge — restart (from theM_gateway)

```bash
cd /opt/docker/them/theM_gateway

# Restart bridge (no rebuild)
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  --profile temporal restart them-bridge

# Tail logs
docker logs -f them-bridge
```

### Verify Traefik routing

```bash
# List all discovered Go routers (expect 5+ for Wave 7)
docker exec them-traefik wget -qO- http://localhost:8089/api/rawdata 2>/dev/null | python3 -c "
import json,sys
data=json.load(sys.stdin)
routers=data.get('routers',{})
go_routers={k:v for k,v in routers.items() if 'go' in k.lower()}
print(f'Total routers: {len(routers)}, Go routers: {len(go_routers)}')
for k in sorted(go_routers.keys()):
    print(f'  {k}: priority={go_routers[k].get(\"priority\",\"?\")}')
"

# Confirm Go bridge is serving LLM provider routes
docker exec them-go-bridge python3 2>/dev/null || \
  docker exec them-bridge python3 -c "
import http.client, json
conn = http.client.HTTPConnection('them-auth-service', 8701)
conn.request('POST', '/api/v1/auth/login', json.dumps({'username':'admin','password':'admin123'}), {'Content-Type':'application/json'})
token = json.loads(conn.getresponse().read())['access_token']
conn2 = http.client.HTTPConnection('them-traefik', 8088)
conn2.request('GET', '/api/v1/admin/llm-providers', headers={'Authorization': f'Bearer {token}'})
r = conn2.getresponse()
print(f'Status: {r.status} (expect 200)')
"
```

### DB connectivity check

```bash
# From host (port 15432 exposed by docker-compose.integration.yml)
docker exec them-postgres psql -U them -d them -c "SELECT count(*) FROM them.llm_providers;"

# Check DB is reachable from Go bridge
docker logs them-go-bridge 2>&1 | grep "postgres connected"
```

### Redis connectivity check

```bash
docker exec them-redis redis-cli ping    # expect PONG
docker logs them-go-bridge 2>&1 | grep "redis connected"
```

---

## 4. Environment Variable Contract

### Variable-name mapping across services

The same logical secret uses different environment variable names in different services. This is the most common source of misconfiguration.

| Logical secret | Compose `.env` variable | Python bridge (`SECRET_KEY` field) | Go bridge env var | Auth service env var | Notes |
|---|---|---|---|---|---|
| Platform Fernet/signing key | `THE_M_SECRET_KEY` | `SECRET_KEY` | `SECRET_KEY` | — | Must be identical in Python bridge and Go bridge |
| JWT HS256 signing key | `THE_M_JWT_SECRET` | — (not used; Python bridge uses `SECRET_KEY` for its own JWT) | `JWT_SECRET` | `JWT_SECRET` | Must be identical in Go bridge and auth service |
| DB password | `THE_M_DB_PASSWORD` | `DATABASE_PASSWORD` | `DATABASE_PASSWORD` | part of `DATABASE_URL` | Must be identical everywhere |
| DB user | `THE_M_DB_USER` | `DATABASE_USER` | `DATABASE_USER` | part of `DATABASE_URL` | Must be identical everywhere |
| Redis password | `THE_M_REDIS_PASSWORD` | `REDIS_PASSWORD` | `REDIS_PASSWORD` | — | Usually blank (no auth on private network) |

### Full variable table

| Variable name (in container) | Service(s) | Source field | Required | Safe default (local dev) | Python↔Go must match? | Consequence if missing or wrong |
|---|---|---|---|---|---|---|
| `SECRET_KEY` | Python bridge, Go bridge | Python: `config.py:SECRET_KEY`; Go: `config.go:SecretKey` via `SECRET_KEY` | **Yes** | — (must be set) | **Yes** | Go: crash at startup (`SECRET_KEY is required`). Python: insecure default warning. Fernet decryption of stored API keys fails if values differ between replicas |
| `JWT_SECRET` | Go bridge, auth service | Go: `config.go:JWTSecret` via `JWT_SECRET`; auth: `settings.py:JWT_SECRET` | **Yes** (Go bridge) | — (must be set) | N/A — must match auth service | Go bridge returns 401 for all admin requests if this does not match the auth service's signing key |
| `DATABASE_HOST` | Python bridge, Go bridge | Python: `DATABASE_HOST`; Go: `DATABASE_HOST` | **Yes** | `them-postgres` (internal) | Same value | Service cannot connect to DB |
| `DATABASE_PORT` | Python bridge, Go bridge | Python: `DATABASE_PORT`; Go: `DATABASE_PORT` | No | `5432` | Same value | Service uses wrong port |
| `DATABASE_USER` | Python bridge, Go bridge | Python: `DATABASE_USER`; Go: `DATABASE_USER` | **Yes** | `them` | Same value | Auth failure on DB connection |
| `DATABASE_PASSWORD` | Python bridge, Go bridge | Python: `DATABASE_PASSWORD`; Go: `DATABASE_PASSWORD` | **Yes** | — (must be set) | Same value | Go: crash at startup. Python: DB connection failure |
| `DATABASE_NAME` | Python bridge, Go bridge | Python: `DATABASE_NAME`; Go: `DATABASE_NAME` | No | `them` | Same value | Connects to wrong database |
| `REDIS_HOST` | Python bridge, Go bridge | Python: `REDIS_HOST`; Go: `REDIS_HOST` | No | `them-redis` (internal) | Same value | Redis connection failure |
| `REDIS_PORT` | Python bridge, Go bridge | Python: `REDIS_PORT`; Go: `REDIS_PORT` | No | `6379` | Same value | Redis connection failure |
| `REDIS_PASSWORD` | Python bridge, Go bridge | Python: `REDIS_PASSWORD`; Go: `REDIS_PASSWORD` | No | blank | Same value | Redis auth failure if Redis has a password set |
| `REDIS_DB` | Python bridge, Go bridge | Python: `REDIS_DB`; Go: `REDIS_DB` | No | `0` | **Yes — must be 0** | Keys invisible across services if different |
| `TEMPORAL_ENABLED` | Python bridge, Go bridge | Python: `TEMPORAL_ENABLED`; Go: `TEMPORAL_ENABLED` | No | `true` | Yes | Temporal workflows not started |
| `TEMPORAL_HOST` | Python bridge | Python: `TEMPORAL_HOST` | No | `temporal-frontend:7233` | — | Python cannot reach Temporal |
| `TEMPORAL_HOST_PORT` | Go bridge | Go: `TEMPORAL_HOST_PORT` | No | `temporal-frontend:7233` | — | Go cannot reach Temporal |
| `TEMPORAL_NAMESPACE` | Python bridge | Python: `TEMPORAL_NAMESPACE` | No | `default` | — | Wrong namespace |
| `TEMPORAL_TASK_QUEUE` | Python bridge | Python: `TEMPORAL_TASK_QUEUE` | No | `them-orchestration` | — | Worker does not pick up tasks |
| `JWT_PUBLIC_KEY_PEM` | Go bridge | Go: `JWT_PUBLIC_KEY_PEM` | No | blank (HS256 used instead) | — | When set, Go uses RS256; must contain PEM of auth service's signing key |
| `ANTHROPIC_API_KEY` | Python bridge, Go bridge, them-worker | Various | No | blank (mock LLM used) | No | LLM calls fail; mock provider used in Go bridge |
| `THE_M_INSTANCE_ID` | Python bridge, Go bridge | Python: `THE_M_INSTANCE_ID`; Go: `THE_M_INSTANCE_ID` | No | `bridge-1` / `go-bridge-1` | Must be unique per replica | Session routing and Redis pod keys collide across replicas |
| `RUN_EVENTS_MODE` | Go bridge | Go: `RUN_EVENTS_MODE` | No | `pubsub` | — | Controls run event delivery path (`pubsub`/`dual`/`streams`) |
| `RECONCILER_DRY_RUN` | Go bridge | Go: `RECONCILER_DRY_RUN` | No | `true` | — | When `false`, reconciler writes stale-run corrections to DB |

### Key naming mismatch reference

```
.env variable         Compose passes as              Container sees as
─────────────────────────────────────────────────────────────────────
THE_M_SECRET_KEY  →  SECRET_KEY=${THE_M_SECRET_KEY}  →  SECRET_KEY
THE_M_DB_USER     →  DATABASE_USER=${THE_M_DB_USER}  →  DATABASE_USER
THE_M_DB_PASSWORD →  DATABASE_PASSWORD=${THE_M_DB_PASSWORD} → DATABASE_PASSWORD
THE_M_JWT_SECRET  →  JWT_SECRET=${THE_M_JWT_SECRET}  →  JWT_SECRET
THE_M_REDIS_PASSWORD → REDIS_PASSWORD=${THE_M_REDIS_PASSWORD} → REDIS_PASSWORD
```

The `.env` file uses `THE_M_*` prefixed names. Compose maps them to the un-prefixed names that containers actually read.

---

## 5. Secret Handling

### Source of truth for local secrets

| File | Location | Purpose | Committed? |
|---|---|---|---|
| `secrets.local` | `/opt/docker/them/secrets.local` | Single master passphrase; all other secrets are derived from it | **Never** |
| `.env` | `/opt/docker/them/.env` | Generated by `generate-env.sh`; consumed by Compose | **Never** |
| `.env.example` | `/opt/docker/them/.env.example` | Variable names with fake placeholders; safe to commit | Yes |
| `secrets.local.example` | `/opt/docker/them/secrets.local.example` | Shows expected format; no real values | Yes |

### Verify files are gitignored

```bash
# These must return nothing (not tracked by git)
git ls-files /opt/docker/them/.env /opt/docker/them/secrets.local

# Confirm .gitignore entries
grep -E "\.env|secrets\.local" /opt/docker/them/.gitignore
```

Expected `.gitignore` entries:
```
.env
.env.test
.env.local
secrets.local
```

### Creating secrets safely from scratch

```bash
cd /opt/docker/them

# Step 1 — create secrets.local from the example
cp secrets.local.example secrets.local

# Step 2 — set a real random master passphrase (never reuse one from documentation)
PASSPHRASE=$(openssl rand -hex 32)
sed -i "s/replace-this-with-a-strong-random-passphrase/$PASSPHRASE/" secrets.local

# Step 3 — derive all secrets and write .env
./generate-env.sh

# Step 4 — add your ANTHROPIC_API_KEY to .env manually if needed
# Do NOT add it to secrets.local or CLAUDE.md or any committed file
```

### What generate-env.sh writes

`generate-env.sh` derives secrets via HMAC-SHA256 from the master passphrase and writes:

```
THE_M_DB_USER=them
THE_M_DB_PASSWORD=<derived>
THE_M_SECRET_KEY=<derived>
THE_M_JWT_SECRET=<derived>
THE_M_REDIS_PASSWORD=          # blank (no Redis auth in local stack)
ANTHROPIC_API_KEY=             # blank — add manually
APP_ENV=development
LOG_LEVEL=INFO
LIVEKIT_API_KEY=<derived>
LIVEKIT_API_SECRET=<derived>
```

### Security rules — never violate

- **Never** copy actual secret values from container inspection into any documentation, commit, log, prompt, or chat message.
- **Never** hardcode or guess secrets — always derive from `secrets.local` via `generate-env.sh`.
- `SafeString()` in the Go config redacts all secret fields as `***`; use it when logging config.
- `git diff --stat` before every commit; verify `.env` and `secrets.local` are not staged.

---

## 6. Configuration Validation Commands

These commands show configuration state without printing secret values.

### Container health

```bash
docker ps --filter "name=them" --format "table {{.Names}}\t{{.Status}}"
```

### Go bridge — healthy startup log sequence

```bash
docker logs them-go-bridge 2>&1 | grep -E "loaded|connected|mounted|starting server|JWT middleware"
```

Expected lines (order may vary):
```
configuration loaded  ... jwt_middleware=enabled (HS256/JWT_SECRET) ...
postgres connected ... host=them-postgres
redis connected ... addr=them-redis:6379
JWT middleware enabled (HS256 via JWT_SECRET)
server starting ... addr=0.0.0.0:8002
```

If `jwt_middleware=enabled (HS256/SECRET_KEY)` appears instead of `HS256/JWT_SECRET`, the `JWT_SECRET` env var is not set — Go bridge will reject tokens signed by the auth service.

### Go bridge — env var names present (safe, no values)

```bash
docker inspect them-go-bridge --format '{{range .Config.Env}}{{println .}}{{end}}' | cut -d= -f1 | sort
```

### Traefik routers discovered

```bash
docker exec them-traefik wget -qO- http://localhost:8089/api/rawdata 2>/dev/null \
  | python3 -c "
import json, sys
d = json.load(sys.stdin)
routers = d.get('routers', {})
print(f'Total routers: {len(routers)}')
for k in sorted(routers):
    r = routers[k]
    print(f'  [{r.get(\"priority\",\"?\"):>3}] {k}')
"
```

### JWT validation end-to-end

```bash
docker exec them-bridge python3 -c "
import http.client, json
conn = http.client.HTTPConnection('them-auth-service', 8701)
conn.request('POST', '/api/v1/auth/login',
    json.dumps({'username':'admin','password':'admin123'}),
    {'Content-Type':'application/json'})
token = json.loads(conn.getresponse().read())['access_token']
# Test against Go bridge directly
conn2 = http.client.HTTPConnection('them-go-bridge', 8002)
conn2.request('GET', '/api/v1/admin/llm-providers', headers={'Authorization': f'Bearer {token}'})
r = conn2.getresponse()
print(f'Go bridge direct: {r.status}  (expect 200)')
r.read()
# Test through Traefik
conn3 = http.client.HTTPConnection('them-traefik', 8088)
conn3.request('GET', '/api/v1/admin/llm-providers', headers={'Authorization': f'Bearer {token}'})
r3 = conn3.getresponse()
print(f'Via Traefik: {r3.status}  (expect 200)')
"
```

### Both Go replicas receiving traffic (when soak profile active)

```bash
# Send a request to each directly and confirm response
for port in 8002 8003; do
  docker exec them-bridge python3 -c "
import http.client
conn = http.client.HTTPConnection('host.docker.internal', $port)
conn.request('GET', '/health/live')
r = conn.getresponse()
print(f'port $port → {r.status}')
" 2>/dev/null || echo "port $port not reachable (soak not running?)"
done

# Or from the host directly
for port in 8002 8003; do
  curl -s -o /dev/null -w "%{http_code}" http://localhost:$port/health/live && echo " (port $port)"
done
```

---

## 7. Recovery Procedures

### .env is missing

```bash
cd /opt/docker/them
./generate-env.sh    # requires secrets.local to exist first

# If secrets.local is also missing:
cp secrets.local.example secrets.local
# Edit secrets.local — set a real passphrase, then:
./generate-env.sh
```

**Warning:** If the previous `.env` was generated from a different `secrets.local`, all derived secrets will be different. Any Fernet-encrypted values in the database (e.g. `api_key_encrypted` in `them.llm_providers`) will become unreadable. Recovery requires either restoring the original `secrets.local` or re-entering all API keys via the admin UI.

### Container recreated with Compose defaults

Symptom: Go bridge logs `FATAL: config: SECRET_KEY must not use the default value "change-this-in-production"`.

Cause: The container was started without `.env`, so `${THE_M_SECRET_KEY:-change-this-in-production}` resolved to the insecure default.

Fix:
```bash
cd /opt/docker/them
# Verify .env exists and is not empty
ls -la .env

# Recreate with correct env
docker compose --profile go up --no-deps -d them-go-bridge
docker logs them-go-bridge 2>&1 | head -5
```

**Do not guess or hardcode secrets.** Always use `generate-env.sh`.

### DB credentials do not match

Symptom: `docker logs them-go-bridge` shows `failed to connect to postgres` or `password authentication failed`.

Diagnosis:
```bash
# Check DB user and host (not password)
docker inspect them-go-bridge --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | grep -E "DATABASE_USER|DATABASE_HOST|DATABASE_NAME|DATABASE_PORT"

# Verify DB is accepting connections
docker exec them-postgres psql -U them -d them -c "SELECT 1;"
```

Fix: Ensure `THE_M_DB_USER` and `THE_M_DB_PASSWORD` in `.env` match the values used when the DB was initialized. If the DB was re-created, re-run `./generate-env.sh` from the same `secrets.local`, or re-set the DB user password:
```bash
docker exec them-postgres psql -U postgres -c "ALTER USER them PASSWORD 'your-new-password';"
# Then update .env accordingly
```

### THE_M_SECRET_KEY differs between Python and Go

Symptom: Fernet-encrypted values (e.g. LLM provider API keys) decrypt correctly in Python but fail in Go (or vice versa).

Diagnosis:
```bash
# Verify both containers were started from the same .env
docker inspect them-bridge --format '{{range .Config.Env}}{{println .}}{{end}}' | grep "SECRET_KEY" | wc -c
docker inspect them-go-bridge --format '{{range .Config.Env}}{{println .}}{{end}}' | grep "SECRET_KEY" | wc -c
# These should be equal length (same key length → likely same value)
# Do NOT print the actual values
```

Fix: Regenerate `.env` from `secrets.local` and recreate both bridge containers.

### Go bridge enters a restart loop

Diagnosis steps:
```bash
docker logs them-go-bridge 2>&1 | head -20
```

Common causes and fixes:

| Log message | Cause | Fix |
|---|---|---|
| `SECRET_KEY is required but was not set` | `.env` missing or `THE_M_SECRET_KEY` not set | Recreate `.env` with `generate-env.sh`, restart |
| `SECRET_KEY must not use the default value` | Container started without `.env` | Ensure `.env` exists in `/opt/docker/them/`, restart |
| `DATABASE_PASSWORD is required` | `THE_M_DB_PASSWORD` not in `.env` | Add to `.env`, restart |
| `DATABASE_HOST is required` | `DATABASE_HOST` env var empty | Verify compose `DATABASE_HOST=them-postgres` passes through |
| `failed to connect to postgres` | DB not up or wrong credentials | Check `docker ps | grep them-postgres`, verify credentials |
| `failed to connect to redis` | Redis not up or wrong password | Check `docker ps | grep them-redis` |

---

## 8. Source of Truth

| What | Where |
|---|---|
| Variable names (Go runtime) | `go/internal/config/config.go` — `Load()` function |
| Variable names (Python runtime) | `app/config.py` — `Settings` class |
| Variable names (auth service) | `auth_service/config/settings.py` |
| Compose variable names (`.env` → container) | `theM_gateway/docker-compose.yml` and `docker-compose.yml` `environment:` blocks |
| Local test values (never committed) | `/opt/docker/them/.env` (generated) |
| Secret derivation logic | `generate-env.sh` / `generate-env.ps1` |
| Variable name template (safe to commit) | `.env.example` |
| Master passphrase template | `secrets.local.example` |
| This runbook | `docs/architecture-v2/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md` |

**Future Claude sessions:** Read this file before any Docker, deployment, environment, or container recreation work. Read `go/internal/config/config.go` to verify Go variable names before touching compose environment blocks.
