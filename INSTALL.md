# the-M Stack — Installation Guide

**Platform:** Hetzner VPC — Linux (Ubuntu 22.04 / 24.04), Docker Engine ≥ 24, docker compose v2  
**Last updated:** 2026-07-29  
**Repo:** https://github.com/aviciot/theM  
**Target domain:** `them.avico78.com` (not yet externally exposed — see [Expose to Cloudflare](#expose-to-cloudflare-when-ready))

---

## Architecture overview

```
Cloudflare (DNS proxy / Tunnel)
        │
        ▼
External Traefik  (your shared reverse proxy, handles TLS + host routing)
        │  Host("them.yourdomain.com") → them-traefik:8088
        ▼
them-traefik  (internal path router — routes /ws, /api/v1, /, etc.)
   ├── /ws, /sse, /go-health   → Go bridges (them-go-bridge × 2)
   ├── /api/v1, /health,
   │   /apps, /a2a             → Python bridge (them-bridge)
   ├── /temporal               → Temporal UI
   └── /                       → Frontend (Next.js)
```

**Services in the stack:**

| Container | Role | Internal port |
|---|---|---|
| `them-traefik` | Internal path-based router | 8088 |
| `them-postgres` | PostgreSQL 16 database | 5432 |
| `them-redis` | Redis 7 cache / pub-sub | 6379 |
| `them-auth-service` | Auth / JWT / IAM microservice | 8701 |
| `them-bridge` | Python bridge — Admin API + WS fallback | 8001 |
| `them-go-bridge` | Go gateway — primary WS/SSE runtime (replica 1) | 8002 |
| `them-go-bridge-2` | Go gateway — replica 2 | 8002 |
| `them-worker` | Temporal activity worker (Python, LLM calls) | — |
| `temporal-frontend` | Temporal server | 7233 |
| `temporal-ui` | Temporal Web UI | 8080 |
| `them-frontend` | Next.js dashboard | 3200 |

---

## Hetzner VPC notes

This stack is deployed on a **Hetzner Cloud VPC** on the same server as the other services (`omni`, `dna`, `portal`). Key facts confirmed during initial deployment:

| Item | Value |
|---|---|
| Target domain | `them.avico78.com` |
| Docker engine | 29.3.0 |
| Docker compose | v5.1.1 |
| External Traefik container | `infra-traefik` |
| External Traefik network | `proxy-network` |
| External Traefik config | `/home/avi/infrastructure/traefik/` |
| Cloudflare Tunnel container | `infra-cloudflared` (already running) |
| External Traefik entrypoint | `web` — port 80, plain HTTP (TLS at Cloudflare edge) |
| the-M route file | `/home/avi/infrastructure/traefik/dynamic/them-routes.yml` |
| the-M compose overlay | `theM_gateway/docker-compose.cloudflare.yml` |

**Important:** The external Traefik uses **port 80 only** — no TLS config. Cloudflare Tunnel terminates TLS at the edge and forwards plain HTTP inward. All internal traffic between `infra-traefik` and `them-traefik` is plain HTTP.

**Current exposure status:** the UI is **not yet exposed** to the internet. The route in `them-routes.yml` is commented out. See [Expose to Cloudflare](#expose-to-cloudflare-when-ready) when ready to go live.

### What `docker-compose.cloudflare.yml` does

- Overrides `proxy-network` from linux.yml's local `them-proxy` back to the **existing shared `proxy-network`** that all other services use
- Joins `them-traefik` to `proxy-network` so `infra-traefik` can resolve it by container name
- No Traefik labels on `them-traefik` — routing is handled via the file-based config in `them-routes.yml`

### Hetzner start script

**Do not modify `linux-start.sh`** — it is the generic Linux script shared across all deployments.

Instead use `linux-start-hetzner.sh`, which wraps the generic script and then re-applies `docker-compose.cloudflare.yml` to join `them-traefik` to `proxy-network`:

```bash
cd theM_gateway
./scripts/linux-start-hetzner.sh [--build]
```

The Hetzner-specific files are:

| File | Purpose |
|---|---|
| `scripts/linux-start-hetzner.sh` | Hetzner wrapper — calls `linux-start.sh` then applies cloudflare overlay |
| `docker-compose.cloudflare.yml` | Joins `them-traefik` to `proxy-network` (shared with `infra-traefik`) |
| `/home/avi/infrastructure/traefik/dynamic/them-routes.yml` | External Traefik route for `them.avico78.com` (router commented out until UI goes live) |

---

## Prerequisites

```bash
# Docker Engine ≥ 24
docker --version

# docker compose v2
docker compose version

# openssl (for secret generation)
openssl version

# Go 1.23+ (only needed if building Go binaries locally outside Docker)
go version
```

Ensure your external Traefik is already running and connected to a Docker network. Note the exact name of that network — you'll need it in step 4.

---

## Step 1 — Clone and enter the gateway directory

```bash
git clone https://github.com/aviciot/theM theM
cd theM/theM_gateway

# Make scripts executable (one-time setup)
chmod +x scripts/linux-*.sh generate-env.sh
```

> **Note:** All `docker compose` commands and scripts must be run from inside `theM_gateway/`.

---

## Step 2 — Generate secrets

The stack derives all internal credentials from a single master passphrase via HMAC-SHA256. You only need to remember (or store securely) the one master secret.

```bash
# 1. Copy the example secrets file
cp secrets.local.example secrets.local

# 2. Generate a strong master secret
openssl rand -hex 32

# 3. Edit secrets.local and paste the output as THE_M_MASTER_SECRET
nano secrets.local

# 4. Derive all stack secrets into .env
./generate-env.sh
```

`generate-env.sh` writes `.env` with derived values for:
- `THE_M_DB_PASSWORD`
- `THE_M_SECRET_KEY`
- `THE_M_JWT_SECRET`
- LiveKit keys (if using voice)

---

## Step 3 — Add required secrets to `.env`

Open `.env` and fill in the values that are **not** auto-derived:

```bash
nano .env
```

**Required (must be set before starting):**

```dotenv
# LLM provider — required for orchestration to function
ANTHROPIC_API_KEY=sk-ant-api03-...

# Your server's public domain or IP (used for CORS and WebSocket URL)
THE_M_CORS_ORIGINS=https://them.yourdomain.com
THE_M_BRIDGE_WS_URL=wss://them.yourdomain.com

# Application environment
APP_ENV=production
LOG_LEVEL=INFO

# Go gateway event transport (production default)
RUN_EVENTS_MODE=pubsub

# Reconciler dry run — keep true until validated on this environment
RECONCILER_DRY_RUN=true
```

**Optional (leave blank to disable feature):**

```dotenv
# Vision agent
GOOGLE_MAPS_API_KEY=
FAL_API_KEY=

# Voice (LiveKit) — required only for --profile voice
LIVEKIT_PUBLIC_URL=wss://them.yourdomain.com/livekit

# Per-agent API key overrides (falls back to ANTHROPIC_API_KEY)
DOCU_WRITER_ANTHROPIC_API_KEY=
DEBATE_ANTHROPIC_API_KEY=
SECURITY_SCANNER_ANTHROPIC_API_KEY=
```

---

## Step 4 — Connect to external Traefik

Your external Traefik proxy already runs on a Docker network (e.g. `traefik-proxy` or `proxy`). The theM stack's `them-traefik` container must join that network so the external Traefik can route to it.

### 4-A. Identify your external Traefik's network name

```bash
docker network ls | grep traefik
# Look for the network your existing Traefik container is attached to
docker inspect <your-traefik-container> --format '{{json .NetworkSettings.Networks}}' | jq 'keys'
```

### 4-B. Create a Cloudflare overlay compose file

Create `docker-compose.cloudflare.yml` in `theM_gateway/`:

```yaml
# docker-compose.cloudflare.yml
# Connects them-traefik to the external Traefik proxy network
# and adds the labels the external Traefik needs to route the domain.
#
# Replace EXTERNAL_TRAEFIK_NETWORK with your actual network name (e.g. traefik-proxy).
# Replace them.yourdomain.com with your actual domain.

version: "3.9"

networks:
  # Override proxy-network to use your external Traefik's Docker network.
  # Must match the network name your external Traefik container is on.
  proxy-network:
    name: EXTERNAL_TRAEFIK_NETWORK   # ← change this
    external: true

services:
  them-traefik:
    # Join the external Traefik network so the external Traefik can reach us.
    networks:
      - proxy-network
      - them-network
    labels:
      # Tell the external Traefik to route Host("them.yourdomain.com") to this container.
      # Adjust the label prefix/format to match your external Traefik's provider constraints.
      - "traefik.enable=true"
      - "traefik.http.routers.them-external.rule=Host(`them.yourdomain.com`)"   # ← change
      - "traefik.http.routers.them-external.entrypoints=websecure"
      - "traefik.http.routers.them-external.tls=true"
      - "traefik.http.routers.them-external.tls.certresolver=cloudflare"        # ← match your resolver name
      - "traefik.http.services.them-external-svc.loadbalancer.server.port=8088"
      # WebSocket passthrough — required for /ws and /sse
      - "traefik.http.routers.them-external.middlewares=them-ws-headers"
      - "traefik.http.middlewares.them-ws-headers.headers.customrequestheaders.X-Forwarded-Proto=https"
```

> **Tip:** If your external Traefik uses a different label naming convention (e.g. a different `certresolver` name or entrypoint name like `https` instead of `websecure`), adjust accordingly — this should mirror how you configured other services.

### 4-C. If using Cloudflare Tunnel instead of direct IP exposure

**Recommended on Hetzner VPC.** The `cloudflared` daemon runs on the same Hetzner server, connects outbound to Cloudflare's edge, and forwards inbound traffic to `them-traefik:8088` over the VPC private interface. No inbound firewall rule for port 8088 is needed.

```bash
# On the Hetzner server, configure the tunnel to point to them-traefik:8088.
# Use localhost:8088 — them-traefik binds 0.0.0.0:8088 on the host by default.

# /etc/cloudflared/config.yml
tunnel: <your-tunnel-id>
credentials-file: /root/.cloudflared/<tunnel-id>.json

ingress:
  - hostname: them.yourdomain.com
    service: http://localhost:8088
    originRequest:
      connectTimeout: 30s
      # WebSocket passthrough — required for Playground /ws connections
      noTLSVerify: false
  - service: http_status:404
```

In this case cloudflared talks to `them-traefik` via localhost, TLS is terminated at Cloudflare's edge, and no TLS cert config is needed on the Traefik side. Simplify the external labels:

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.them-external.rule=Host(`them.yourdomain.com`)"
  - "traefik.http.routers.them-external.entrypoints=web"
  - "traefik.http.services.them-external-svc.loadbalancer.server.port=8088"
```

> **Hetzner Firewall rule:** ensure the Hetzner Cloud Firewall blocks inbound TCP 8088 from all sources except the Hetzner private network range (e.g. `10.0.0.0/8`). The tunnel is outbound — no inbound rule needed for the application.

---

## Step 5 — Start the stack

**On this Hetzner server**, always use `linux-start-hetzner.sh` — not the generic `linux-start.sh`. The Hetzner wrapper calls the generic script and then applies the Cloudflare overlay.

```bash
cd theM_gateway

# First-time or after a code change — rebuild images:
./scripts/linux-start-hetzner.sh --build

# Subsequent starts (no code change):
./scripts/linux-start-hetzner.sh
```

**What it does (in order):**

1. Validates `.env` — fails fast if required vars are missing
2. Starts Postgres + Redis and waits for healthy status
3. Starts Temporal (workflow runtime)
4. Bootstraps the DB schema (fresh install: applies `db/schema_current.sql`; existing: no-op)
5. Starts auth service
6. Starts Python Temporal worker (waits until it registers on Temporal task queue)
7. Starts both Go bridge replicas (primary WS/SSE gateway)
8. Starts Traefik
9. Starts Python bridge (Admin API) + frontend

> **Note:** `linux-start.sh` uses this compose stack internally:
> ```
> docker-compose.yml
> docker-compose.linux.yml
> docker-compose.integration.yml
> docker-compose.soak.yml
> docker-compose.traefik.yml
> --profile temporal
> ```
>
> When using your cloudflare overlay, append it to the compose command. You can either modify `linux-start.sh` or run the stack manually (see step 6 below).

The `linux-start-hetzner.sh` script handles this automatically — no manual compose command needed.

---

## Step 6 — Seed development users (dev/staging only)

The fresh schema install creates only system records. No user accounts are created.

```bash
# Create default dev users: admin/admin123 and avi/avi123
docker exec -i them-postgres psql -U them -d them < db/seed_users.sql
```

> **Never do this in production.** Manage users through the auth service API (`POST /api/v1/auth/register` or the admin UI).

---

## Step 7 — Verify the stack

```bash
# All containers should be healthy
./scripts/linux-health.sh

# Quick sanity test (DB, Redis, auth service, bridge, containers)
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
# Expected: ~55 passed, 0 failed
```

### Key endpoint verification

```bash
HOST=them.yourdomain.com

# Frontend (via Cloudflare → external Traefik → them-traefik)
curl -sf -o /dev/null -w "%{http_code}\n" https://${HOST}/
# Expected: 200

# Go bridge health (through full proxy chain)
curl -sf https://${HOST}/go-health/live
# Expected: {"status":"ok"}

curl -sf https://${HOST}/go-health/ready
# Expected: {"status":"ok","postgres":"ok","redis":"ok"}

# Admin API gate (unauthenticated → 401)
curl -sf -o /dev/null -w "%{http_code}\n" https://${HOST}/api/v1/admin/agents
# Expected: 401

# WebSocket (confirm routing reaches Python bridge, not a 404)
curl -sf -o /dev/null -w "%{http_code}\n" \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  https://${HOST}/ws/orchestrate/default
# Expected: 403 (JWT gate) — NOT 404
```

### Traefik dashboard

```bash
# Internal dashboard (accessible only from server, not via Cloudflare)
curl http://localhost:8089/dashboard/
```

---

## Step 8 — Log in to the dashboard

Open `https://them.yourdomain.com` in your browser.

- **Admin user:** `admin` / `admin123` (if dev users were seeded)
- **Regular user:** `avi` / `avi123`

In production, create users via the auth service API.

---

## Optional profiles

Enable optional services by adding `--profile <name>` to the compose command.

### Temporal UI (always enabled via linux-start.sh)

Available at `https://them.yourdomain.com/temporal/`

### A2A test agents

```bash
docker compose -f docker-compose.yml -f docker-compose.linux.yml \
  --profile test-agents up -d

# Enable the agents in the DB
docker exec them-postgres psql -U them -d them \
  -c "UPDATE them.agents SET enabled=true WHERE slug IN ('a2a_echo','a2a_slow','a2a_stream');"

# Bust Redis cache
docker exec them-redis redis-cli DEL them:agents:registry
```

### Security scanner agent

```bash
docker compose -f docker-compose.yml -f docker-compose.linux.yml \
  --profile security up -d --build them-security-agent

# Apply migration (first time only)
docker cp db/009_security_scan.sql them-postgres:/tmp/
docker exec them-postgres psql -U them -d them -f /tmp/009_security_scan.sql
docker exec them-redis redis-cli DEL them:agents:registry
```

### Debate agents

```bash
docker compose -f docker-compose.yml -f docker-compose.linux.yml \
  --profile debate up -d
```

### Voice (LiveKit WebRTC)

Requires `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`, `OPENAI_API_KEY` in `.env`.

```bash
docker compose -f docker-compose.yml -f docker-compose.linux.yml \
  --profile voice up -d
```

LiveKit needs a UDP port exposed for WebRTC:
```yaml
# Already in docker-compose.yml: "7882:7882/udp"
```

Ensure UDP 7882 is open in your firewall/security group.

---

## Database management

### Fresh install
DB schema is bootstrapped automatically by `linux-start.sh` using `db/schema_current.sql`.

### Apply a new migration to an existing deployment

```bash
./scripts/linux-db-upgrade.sh db/026_new_feature.sql
```

### Access the database directly

```bash
docker exec -it them-postgres psql -U them -d them
```

### Full 7-phase clean-install validation

```bash
./scripts/linux-validate-clean-install.sh
# Expected: 27/27 PASSED
```

---

## Cloudflare-specific notes

### WebSocket support

Cloudflare requires **WebSockets to be enabled** on the zone. In the Cloudflare dashboard:

> **Network → WebSockets → On**

The the-M Playground connects via `wss://them.yourdomain.com/ws/orchestrate/{app}/{ep}`. Cloudflare Free and Pro plans support WebSockets. Ensure the WebSocket path is not cached (Cloudflare's default: not cached for `wss://`).

### Cloudflare Tunnel (cloudflared) — recommended

Using cloudflared avoids exposing any ports to the internet. The tunnel connects outbound from your server to Cloudflare's edge.

```bash
# Install cloudflared (Debian/Ubuntu)
curl -L --output cloudflared.deb \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
sudo dpkg -i cloudflared.deb

# Authenticate and create a tunnel
cloudflared tunnel login
cloudflared tunnel create them

# Configure the tunnel — /etc/cloudflared/config.yml
tunnel: <your-tunnel-id>
credentials-file: /home/<user>/.cloudflared/<tunnel-id>.json

ingress:
  - hostname: them.yourdomain.com
    service: http://them-traefik:8088
    originRequest:
      connectTimeout: 30s
  - service: http_status:404

# Install as a system service
sudo cloudflared service install
sudo systemctl enable --now cloudflared
```

> **Network requirement:** `cloudflared` must be able to reach `them-traefik:8088`. Either run cloudflared in the same Docker network as `them-traefik`, or bind `them-traefik` to `0.0.0.0:8088` (already done in the base compose) and use `http://localhost:8088` in the config.

### Cloudflare Origin certificates

If using an external Traefik with TLS (instead of Cloudflare Tunnel):

1. In Cloudflare dashboard: **SSL/TLS → Origin Server → Create Certificate**
2. Download the certificate and private key
3. Configure your external Traefik to use these certs for `them.yourdomain.com`
4. Set SSL/TLS mode to **Full (strict)** in Cloudflare

### Hetzner Firewall rules

Configure in Hetzner Cloud → Firewall → the firewall attached to this server:

| Direction | Protocol | Port | Source | Purpose |
|---|---|---|---|---|
| Inbound | TCP | 22 | your management IP | SSH |
| Inbound | TCP | 8089 | Hetzner private network (`10.0.0.0/8`) | Traefik dashboard (internal only) |
| Inbound | ALL | ALL | Hetzner private network (`10.0.0.0/8`) | VPC inter-service traffic |
| Outbound | ALL | ALL | anywhere | Docker pulls, Cloudflare tunnel, Anthropic API |

Do **not** open port 8088 to the public internet — the Cloudflare Tunnel handles all inbound traffic.

### Cloudflare security settings

Recommended Cloudflare settings for this domain:
- **SSL/TLS:** Full (strict) — or Flexible if using Tunnel without TLS on origin
- **Minimum TLS Version:** 1.2
- **HSTS:** enable after confirming HTTPS works end-to-end
- **Bot Fight Mode:** optional — may interfere with A2A agent-to-agent traffic; disable if needed
- **WAF:** custom rules to allow your Hetzner management IPs unrestricted; apply rate limiting to `/apps/*` and `/ws/*`

---

## Traefik route ownership quick reference

| Path | Owner | Notes |
|---|---|---|
| `/ws/*` | Go bridge | Primary WS handler |
| `/sse/*` | Go bridge | Primary SSE handler |
| `/api/v1/*` | Python bridge | Admin API (GET: Go in Wave 1-7, writes: Go in Wave 2) |
| `/health/live`, `/health/ready` | Go bridge | priority 130 |
| `/go-health/*` | Go bridge | Legacy path, rewritten to `/health/*` |
| `/apps/*`, `/a2a/*` | Python bridge | App management + A2A server |
| `/temporal` | Temporal UI | priority 150 |
| `/` | Frontend | Next.js dashboard |

---

## Common operations

### View logs

```bash
./scripts/linux-logs.sh                    # all services
docker logs them-bridge --tail 50 -f      # Python bridge
docker logs them-go-bridge --tail 50 -f   # Go bridge
docker logs them-worker --tail 50 -f      # Temporal worker
```

### Stop the stack

```bash
./scripts/linux-stop.sh
```

### Restart after a code change

```bash
./scripts/linux-start.sh --build
```

### Rollback the Go bridge

```bash
./scripts/linux-rollback.sh --list
./scripts/linux-rollback.sh --tag them_gateway-them-go-bridge:20260721-abc1234
```

### Run the full test suite

```bash
# Get an admin JWT
ADMIN_JWT=$(docker exec them-bridge python3 -c "
import urllib.request, json
body = json.dumps({'username':'admin','password':'admin123'}).encode()
req = urllib.request.Request('http://them-auth-service:8701/api/v1/auth/login',
  data=body, headers={'Content-Type':'application/json'}, method='POST')
with urllib.request.urlopen(req, timeout=10) as r:
  print(json.loads(r.read())['access_token'])
")
ADMIN_JWT=$ADMIN_JWT python3.12 scripts/tests/run_tests.py
# Expected: 985 passed, 0 failed, 6 skipped
```

---

## Expose to Cloudflare when ready

The UI is intentionally not exposed during initial deployment. When you are ready to go live:

### Step 1 — Enable the Traefik route

Edit `/home/avi/infrastructure/traefik/dynamic/them-routes.yml` and uncomment the router block:

```yaml
http:
  routers:
    them-external:
      rule: "Host(`them.avico78.com`)"
      entrypoints:
        - web
      service: them-traefik-svc
      middlewares:
        - security-headers

  services:
    them-traefik-svc:
      loadBalancer:
        servers:
          - url: "http://them-traefik:8088"
```

The external Traefik watches this directory — no restart needed, the route activates within seconds.

### Step 2 — Add the Cloudflare Tunnel ingress rule

Find the cloudflared config (mounted into `infra-cloudflared`) and add an ingress entry for `them.avico78.com`:

```bash
# Find the config file
docker inspect infra-cloudflared --format '{{range .Mounts}}{{.Source}} → {{.Destination}}{{"\n"}}{{end}}'
```

Add to the tunnel's `config.yml` ingress list (before the catch-all `http_status:404`):

```yaml
- hostname: them.avico78.com
  service: http://infra-traefik:80
  originRequest:
    connectTimeout: 30s
```

Then restart cloudflared:

```bash
docker restart infra-cloudflared
```

### Step 3 — Add DNS record in Cloudflare

In the Cloudflare dashboard for `avico78.com`:
- **Type:** CNAME
- **Name:** `them`
- **Target:** your tunnel ID (`<tunnel-id>.cfargotunnel.com`) — or if routing through existing tunnel, it's already covered
- **Proxy:** orange cloud (proxied)

### Step 4 — Verify end-to-end

```bash
curl -sf https://them.avico78.com/go-health/live
# Expected: {"status":"ok"}

curl -sf -o /dev/null -w "%{http_code}\n" https://them.avico78.com/api/v1/admin/agents
# Expected: 401 (auth gate working)
```

---

## Troubleshooting

### `them-traefik` not reachable from external Traefik

Check that both containers are on the same Docker network:

```bash
docker network inspect EXTERNAL_TRAEFIK_NETWORK | grep -A5 them-traefik
```

If `them-traefik` is missing, verify that `docker-compose.cloudflare.yml` was included in the compose command and that `proxy-network` resolves to the correct network name.

### WebSocket connections drop immediately

- Confirm Cloudflare **WebSockets is ON** in the zone settings.
- Confirm `THE_M_BRIDGE_WS_URL` in `.env` uses `wss://` (not `ws://`) for Cloudflare.
- Check that `THE_M_CORS_ORIGINS` includes `https://them.yourdomain.com`.
- Restart the stack after `.env` changes: `./scripts/linux-start.sh --build`

### Go bridge starts but shows `SECRET_KEY is required`

`THE_M_SECRET_KEY` is blank or still at the `CHANGE_ME` placeholder. Re-run `./generate-env.sh` after setting `THE_M_MASTER_SECRET` in `secrets.local`.

### Temporal worker not ready (startup fails at step 6)

```bash
docker logs them-worker --tail 30
# Look for connection errors to temporal-frontend:7233
# Temporal takes up to 60s to initialise on a fresh Postgres
```

If Temporal never becomes available:
```bash
docker exec temporal-admin-tools temporal task-queue describe \
  --task-queue them-orchestration --namespace default
```

### 404 on all routes through external Traefik

The external Traefik cannot reach `them-traefik:8088`. Check:
1. Both containers share the same Docker network (`docker network inspect EXTERNAL_TRAEFIK_NETWORK`)
2. `them-traefik` is healthy: `docker inspect them-traefik --format '{{.State.Health.Status}}'`
3. Port 8088 is listening: `docker exec them-traefik wget -qO- http://localhost:8088/health/live`

### Cloudflare returns 522 (connection timed out)

`cloudflared` or external Traefik cannot reach `them-traefik`. Verify:
- If using Tunnel: cloudflared service is running (`systemctl status cloudflared`) and the ingress config points to the correct host/port.
- If using direct IP: firewall allows inbound 443 from Cloudflare IP ranges.

---

## Environment variable reference

| Variable | Required | Description |
|---|---|---|
| `THE_M_DB_PASSWORD` | Yes | PostgreSQL password (auto-derived by `generate-env.sh`) |
| `THE_M_SECRET_KEY` | Yes | App secret key, min 32 chars (auto-derived) |
| `THE_M_JWT_SECRET` | Yes | JWT signing secret (auto-derived) |
| `ANTHROPIC_API_KEY` | Yes | Claude API key — manual |
| `ANTHROPIC_MODEL` | No | Default: `claude-sonnet-4-6` |
| `THE_M_CORS_ORIGINS` | Yes | Comma-separated allowed origins, e.g. `https://them.domain.com` |
| `THE_M_BRIDGE_WS_URL` | Yes | Browser-facing WebSocket base URL, e.g. `wss://them.domain.com` |
| `APP_ENV` | No | `production` or `development` |
| `LOG_LEVEL` | No | `INFO` (default) or `DEBUG` |
| `RUN_EVENTS_MODE` | No | `pubsub` (default), `dual` (staging), `streams` (future) |
| `RECONCILER_DRY_RUN` | No | `true` until reconciler is validated on this env |
| `THE_M_REDIS_PASSWORD` | No | Leave blank for private-network Redis |
| `LIVEKIT_API_KEY` | Voice only | Auto-derived |
| `LIVEKIT_API_SECRET` | Voice only | Auto-derived |
| `LIVEKIT_PUBLIC_URL` | Voice only | e.g. `wss://them.domain.com/livekit` |
| `GOOGLE_MAPS_API_KEY` | Vision agent | Optional |
| `FAL_API_KEY` | Vision agent | Optional |

---

## File layout reference

```
theM/
├── theM_gateway/           ← all deployment commands run from here
│   ├── docker-compose.yml           base stack definition
│   ├── docker-compose.linux.yml     Linux overlay (named volumes, no bind mounts)
│   ├── docker-compose.integration.yml  Go bridges + Go workers + exposed infra ports
│   ├── docker-compose.soak.yml      Go bridge replica 2
│   ├── docker-compose.traefik.yml   Traefik labels for Go bridge route ownership
│   ├── docker-compose.cloudflare.yml   ← YOU CREATE THIS (step 4-B)
│   ├── .env                         secrets (git-ignored, generated)
│   ├── secrets.local                master passphrase (git-ignored, never commit)
│   ├── generate-env.sh              derives .env from secrets.local
│   ├── scripts/
│   │   ├── linux-start.sh           full stack startup script
│   │   ├── linux-stop.sh            graceful shutdown
│   │   ├── linux-health.sh          health verification
│   │   ├── linux-db-init.sh         DB schema bootstrap (no-op if initialised)
│   │   ├── linux-db-upgrade.sh      apply migration files
│   │   ├── linux-validate-clean-install.sh  7-phase automated validation
│   │   └── linux-rollback.sh        roll back Go bridge image
│   ├── db/
│   │   └── schema_current.sql       canonical schema snapshot for fresh installs
│   └── traefik/
│       └── traefik.yml              internal Traefik static config (port 8088/8089)
└── INSTALL.md                       ← this file
```
