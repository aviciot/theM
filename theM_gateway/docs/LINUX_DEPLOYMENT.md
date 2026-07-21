# Linux Deployment Guide
# the-M — Multi-Agent Orchestration Platform

**Last updated:** 2026-07-21  
**Applies to:** Docker Engine >= 24 + docker compose v2 on Linux (Ubuntu 22.04/24.04)  
**Windows development:** unchanged — use `docker-compose.local.yml` as before

---

## Architecture: Windows vs Linux

| Aspect | Windows development | Linux deployment |
|---|---|---|
| Docker engine | Docker Desktop (WSL2 backend) | Docker Engine (native) |
| Compose overlay | `docker-compose.local.yml` | `docker-compose.linux.yml` |
| Python bridge source | Bind-mounted `.:/app` (live reload) | Baked into image at build time |
| Data persistence | `./data/` bind mounts | Named Docker volumes |
| Traefik dashboard | `127.0.0.1:8089` (loopback only) | `0.0.0.0:8089` (all interfaces) |
| Secret generation | `.\generate-env.ps1` (PowerShell) | `./generate-env.sh` (bash) |
| Test runner | `python3.12` (explicit) | Auto-detected (3.10+) |

---

## Compose file stacking

### Windows (development)
```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.local.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  --profile temporal up -d
```

### Linux (deployment)
```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.linux.yml \
  -f docker-compose.integration.yml \
  -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml \
  --profile temporal up -d
```

The only difference is `docker-compose.linux.yml` replaces `docker-compose.local.yml`.

### Shortcut (use the script)
```bash
cd theM_gateway
./scripts/linux-start.sh [--build]
```

---

## First-time setup on a Linux server

```bash
# 1. Clone the repo
git clone <repo-url> theM
cd theM/theM_gateway

# 2. Make scripts executable
chmod +x scripts/linux-start.sh scripts/linux-stop.sh scripts/linux-migrate.sh
chmod +x scripts/linux-health.sh scripts/linux-logs.sh scripts/linux-rollback.sh
chmod +x generate-env.sh

# 3. Generate secrets
cp secrets.local.example secrets.local
# Edit secrets.local — replace THE_M_MASTER_SECRET with a strong random value:
#   openssl rand -hex 32
nano secrets.local
./generate-env.sh
# Then edit .env to add ANTHROPIC_API_KEY

# 4. Start infrastructure only (Postgres + Redis)
docker compose -f docker-compose.yml -f docker-compose.linux.yml \
  up -d them-postgres them-redis

# 5. Apply DB migrations
./scripts/linux-migrate.sh

# 6. Start full stack
./scripts/linux-start.sh --build

# 7. Verify health
./scripts/linux-health.sh
```

---

## Pre-deployment validation checklist

Run this checklist before every deployment to Linux staging or production.

### Phase 1 — Compose config validation
```bash
cd theM_gateway

# Validate compose config resolves without errors
docker compose \
  -f docker-compose.yml -f docker-compose.linux.yml \
  -f docker-compose.integration.yml -f docker-compose.soak.yml \
  -f docker-compose.traefik.yml --profile temporal config --quiet

echo "Compose config: OK"
```

### Phase 2 — Clean stack startup
```bash
# Stop any running stack
./scripts/linux-stop.sh --remove-orphans

# Build all images from scratch
./scripts/linux-start.sh --build

# Verify all containers healthy
./scripts/linux-health.sh
```

### Phase 3 — DB migrations
```bash
./scripts/linux-migrate.sh

# Verify schema
docker exec them-postgres psql -U them -d them \
  -c "\dt them.*" | head -20

# Verify events_transport column (Phase 11c)
docker exec them-postgres psql -U them -d them \
  -c "\d them.runs" | grep events_transport
```

### Phase 4 — Go unit tests (run on build host or CI)
```bash
cd go
export PATH="$HOME/go-sdk/go/bin:$PATH"

# Full unit suite — must pass before any deployment
go test ./...
echo "Unit tests: OK"

# Race detector — required before staging merge
# (Needs gcc: apt-get install -y gcc on Ubuntu)
go test -race ./...
echo "Race detector: OK"
```

### Phase 5 — Integration tests (run against the live Linux stack)
```bash
cd theM_gateway
REDIS_ADDR=localhost:16379 \
  go test -tags=integration -v -timeout 180s \
  ./internal/runstream/...

# Run MAXLEN scenarios (180s timeout — scenarios 2+3 write 5k/6k events)
REDIS_ADDR=localhost:16379 \
  go test -tags=integration -v -timeout 300s \
  -run "TestMAXLEN|TestIntegration_WS|TestIntegration_Cross" \
  ./internal/runstream/...
```

### Phase 6 — Traefik routing
```bash
HOST_IP=$(hostname -I | awk '{print $1}')

# Go health via Traefik (replacePathRegex: /go-health/* → /health/*)
curl -sf "http://${HOST_IP}:8088/go-health/live" | grep '"status":"ok"'
curl -sf "http://${HOST_IP}:8088/go-health/ready" | grep '"status":"ok"'

# Prometheus metrics (both bridges)
curl -sf "http://localhost:8002/metrics" | grep "them_runstream_mode"
curl -sf "http://localhost:8003/metrics" | grep "them_runstream_mode"

echo "Traefik routing: OK"
```

### Phase 7 — WS and SSE reconnect via Traefik
```bash
# WebSocket upgrade path reaches Go bridge (not Python bridge)
# Expect 401 (no auth) from Go bridge, not 404 (wrong service)
STATUS=$(curl -sf -o /dev/null -w "%{http_code}" \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  "http://${HOST_IP}:8088/ws/orchestrate/app/ep" 2>/dev/null || true)
echo "WS route status: ${STATUS}  (expect 401 or 400 from Go bridge)"

# SSE path (expect 400 or 401 from Go bridge)
STATUS=$(curl -sf -o /dev/null -w "%{http_code}" \
  "http://${HOST_IP}:8088/sse/orchestrate/app/ep" 2>/dev/null || true)
echo "SSE route status: ${STATUS}  (expect 400 or 401 from Go bridge)"
```

### Phase 8 — Multi-replica restart
```bash
# Stop bridge-2, verify bridge-1 still serves
docker stop --timeout=10 them-go-bridge-2
curl -sf http://localhost:8002/health/live | grep '"status":"ok"'
curl -sf http://localhost:8088/go-health/live | grep '"status":"ok"'

# Restart bridge-2
docker start them-go-bridge-2
sleep 8
docker inspect them-go-bridge-2 --format='{{.State.Health.Status}}'
# Expected: healthy

echo "Multi-replica restart: OK"
```

### Phase 9 — Redis recovery
```bash
# Stop Redis and verify bridges detect unhealthy state
docker stop them-redis
sleep 5
curl -sf http://localhost:8002/health/ready && echo "WARN: ready should be 503" || echo "Ready correctly degraded"

# Restart Redis
docker start them-redis
sleep 10
docker inspect them-redis --format='{{.State.Health.Status}}'
curl -sf http://localhost:8002/health/ready | grep '"redis":"ok"'

echo "Redis recovery: OK"
```

### Phase 10 — Graceful shutdown
```bash
# SIGTERM → exit 0 (not SIGKILL exit 137)
docker stop --timeout=15 them-go-bridge-2
EXIT_CODE=$(docker inspect them-go-bridge-2 --format='{{.State.ExitCode}}')
echo "Bridge-2 exit code: ${EXIT_CODE}  (expect 0)"
[ "${EXIT_CODE}" = "0" ] || echo "WARN: non-zero exit code"
docker start them-go-bridge-2
```

### Phase 11 — Soak and load test
```bash
# Full soak via Traefik: verify both bridges receive traffic
# Prerequisite: registered app/EP + valid token (see CLAUDE.md)
#
# Manual validation:
#   1. Start N WebSocket clients via ws://HOST:8088/ws/orchestrate/APP/EP
#   2. Watch docker stats to confirm CPU/memory within limits
#   3. Watch logs: docker compose logs -f them-go-bridge them-go-bridge-2
#   4. Verify Traefik round-robins: each bridge should show ~50% of requests
#
# Automated: run python3 go/scripts/soak_runner.py (if soak runner is implemented)
echo "Soak: manual validation required (see LINUX_DEPLOYMENT.md §Phase 11)"
```

### Phase 12 — Metrics and monitoring
```bash
# Verify all runstream metrics present
curl -sf http://localhost:8002/metrics | grep -E "them_runstream_mode|them_runstream_replay_sessions"

# Expected: them_runstream_mode 1  (dual mode)
# After a real run: them_runstream_replay_sessions_total > 0

# Reconciler metrics (should show non-zero scanned after ~30s)
sleep 35
curl -sf http://localhost:8002/metrics | grep them_reconciler_scanned
```

---

## Key differences from Windows development

### 1. No source bind mount on Linux
On Linux, `docker-compose.linux.yml` removes the `.:/app` volume from `them-bridge` and `them-worker`. The application code is baked into the image at `docker build` time. This means:

- **Code changes require a rebuild**: `./scripts/linux-start.sh --build`
- **No hot-reload**: restart the service after any code change
- **File ownership**: no longer an issue (image COPY sets correct ownership)

### 2. Named volumes instead of bind mounts for data
`docker-compose.linux.yml` replaces `./data/them-postgres/pgdata` and `./data/them-redis` bind mounts with Docker named volumes (`them-postgres-data`, `them-redis-data`). Benefits:

- Docker manages volume ownership — no manual `chown` needed
- Volumes survive `docker compose down` (but not `docker compose down --volumes`)
- Volume location: `/var/lib/docker/volumes/them-postgres-data/`

### 3. Traefik dashboard accessible on all interfaces
`docker-compose.linux.yml` changes the Traefik dashboard port from `127.0.0.1:8089:8089` to `8089:8089` (all interfaces). On a remote Linux server, the dashboard would otherwise be inaccessible. **Protect with a firewall rule** to restrict access to trusted IPs only:
```bash
# Allow only from trusted subnet
iptables -I INPUT -p tcp --dport 8089 -s 10.0.0.0/8 -j ACCEPT
iptables -I INPUT -p tcp --dport 8089 -j DROP
```

### 4. `python3` version resolution
On Windows dev machines, `python3` may be 3.6 (breaks imports). On Linux, `python3` is typically 3.10+. All shell test scripts now auto-detect the best available Python (3.12 > 3.11 > 3.10 > python3, requiring >= 3.10).

### 5. Secret generation
```bash
# Linux: use bash script
./generate-env.sh

# Windows: use PowerShell
.\generate-env.ps1
```
Both produce an identical `.env` using the same HMAC-SHA256 derivation.

---

## Rollback

To roll back only the Go binary (stateless — safe to roll back independently):
```bash
./scripts/linux-rollback.sh --list
./scripts/linux-rollback.sh --tag <image-tag>
./scripts/linux-health.sh
```

To roll back the Python bridge or database schema: restore from a DB backup (outside scope of this runbook — use your backup/restore procedure).

---

## Log collection
```bash
# Last 200 lines from Go bridges
./scripts/linux-logs.sh --tail 200 them-go-bridge them-go-bridge-2

# Last hour, all services, saved to archive
./scripts/linux-logs.sh --since 1h --save /tmp/them-logs-$(date +%Y%m%d-%H%M%S)
```

---

## Known Linux-only considerations

| Item | Status | Action |
|---|---|---|
| Containers run as root (except `them-auth-service`) | Acceptable for private network | Add `user:` directive to hardened envs |
| `vision_agent` PORT env var hardcoded in CMD | Fixed — uses `ENV PORT=9100` default | Override via compose `environment:` if needed |
| Race detector requires gcc | `apt-get install -y gcc` on test runner | Add to CI image |
| `auth_service/docker-compose.yml` references legacy `omni` DB | Orphaned file — do not use standalone | Only use main `docker-compose.yml` |
| `auth_service/.env.example` has wrong DB URL | Doc drift only | Use main `.env.linux.example` |
