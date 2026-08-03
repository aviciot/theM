# Exposing the-M externally at them.avico78.com

## Architecture

```
Browser
  → Cloudflare (them.avico78.com) [TLS termination, Zero Trust OTP]
  → infra-cloudflared (container, http2 protocol, 4 PoP connections)
  → infra-traefik:80 [hostname-based routing, proxy-network]
  → them-traefik:8088 [path-based routing, on proxy-network]
  → them-frontend:3200 (UI, / path)
  → them-bridge:8001 / them-go-bridge:8002 (API/WS/SSE, /api /ws /apps paths)
```

TLS terminates at Cloudflare. All internal traffic is plain HTTP.

## Deployment status (2026-08-03)

| Component | Status |
|---|---|
| CF Zero Trust Access app (email OTP, same policy as omni/netdata/portainer) | Done |
| CF Tunnel ingress `them.avico78.com → http://traefik:80` | Done |
| `them-traefik` on `proxy-network` | Done — via `docker-compose.hetzner.yml` |
| `docker-compose.hetzner.yml` in active project | Done — in `them_gateway` compose project |
| `infra-traefik` `them-external` router | **Done — enabled 2026-08-03** |
| CORS includes `https://them.avico78.com` | Done — already in running containers |
| `NEXT_PUBLIC_BRIDGE_WS_URL` | Not needed — frontend derives `wss://them.avico78.com` from `window.location.host` automatically |
| **Cloudflare DNS CNAME record for `them.avico78.com`** | **PENDING — must be added in CF dashboard** |

## The one remaining step

In the Cloudflare dashboard for `avico78.com`:

1. Go to DNS → Add record
2. Type: `CNAME`
3. Name: `them`
4. Target: `<your-tunnel-UUID>.cfargotunnel.com`
5. Proxied: Yes (orange cloud)

The tunnel UUID is visible in the cloudflared logs (config line) or in the Zero Trust → Tunnels dashboard.

After adding the DNS record, `https://them.avico78.com` will be live immediately.

## Routing detail

### infra-traefik — file-provider router (infrastructure/traefik/dynamic/them-routes.yml)

```yaml
them-external:
  rule: "Host(`them.avico78.com`)"
  entrypoints:
    - web
  service: them-traefik-svc
  middlewares:
    - security-headers
```

Service target: `http://them-traefik:8088`

Traefik file provider watches `/etc/traefik/dynamic` and hot-reloads. No restart needed after editing.

### them-traefik — path-based internal routing

All path routing is handled by Docker labels on service containers. Key paths:
- `/` → `them-frontend:3200` (UI)
- `/api/v1` → `them-bridge:8001` or `them-go-bridge:8002` (routed by method/path)
- `/ws` → `them-bridge:8001`
- `/apps/<slug>/ws` → `them-go-bridge:8002`
- `/apps/<slug>/sse` → `them-go-bridge:8002`

### Frontend API proxy

Browser API calls go through the Next.js server-side proxy (`/api/them/[...path]`), which calls
`http://them-bridge:8001/api/v1/...` internally. Direct `/api/v1/...` calls in the browser
are NOT expected — the frontend always uses the `/api/them/` proxy path.

### WebSocket and SSE

- WS: `NEXT_PUBLIC_BRIDGE_WS_URL` is empty; frontend derives `wss://them.avico78.com` from
  `window.location.host` automatically. WS upgrades pass through Cloudflare → infra-traefik →
  them-traefik → them-bridge or them-go-bridge.
- SSE: Same routing path. Cloudflare buffers SSE by default; see notes below.

## CORS

Both `them-bridge` and `them-auth-service` already include `https://them.avico78.com` in
`CORS_ORIGINS`. No restart required for CORS.

The env var is `THE_M_CORS_ORIGINS` (or `CORS_ORIGINS` per service). Current value includes
the public domain.

## Cloudflare Zero Trust — SSE/streaming note

Cloudflare by default buffers responses. For SSE endpoints (`/apps/<slug>/sse`), add a
**Cache Rule** or use **No Transform** to disable buffering on that path:
- CF Dashboard → Rules → Cache Rules → "No cache + disable buffering" for `them.avico78.com/apps/*/sse`

Alternatively, Cloudflare Tunnel respects `Transfer-Encoding: chunked` and passes it through;
test with real browser before adding CF rules.

## Deployment commands

```bash
# The active them stack already includes docker-compose.hetzner.yml.
# No new compose up needed for the Cloudflare routing change.

# To check router is live:
docker exec infra-traefik wget -qO- http://localhost:8080/api/http/routers | python3 -c "
import sys,json
[print(r['name'], r.get('status','')) for r in json.load(sys.stdin) if 'them-external' in r.get('name','')]
"

# To verify internal routing:
docker exec infra-traefik wget -qO- --header "Host: them.avico78.com" http://them-traefik:8088/health
# Expected: {"status":"ok","db":"ok",...}

# To verify cloudflared tunnel health:
docker logs infra-cloudflared 2>&1 | grep -i "registered\|connected" | tail -5
# Expected: 4 "Registered tunnel connection" lines

# To verify them-traefik is on proxy-network:
docker network inspect proxy-network --format '{{range .Containers}}{{.Name}} {{end}}' | tr ' ' '\n' | grep them-traefik
```

## Verify the UI externally (after DNS is added)

```bash
# 1. DNS resolves through Cloudflare
host them.avico78.com

# 2. HTTPS + 200
curl -sI https://them.avico78.com/ | head -5

# 3. UI HTML
curl -s https://them.avico78.com/ | grep "the-M"

# 4. Frontend proxy path (API through Next.js)
curl -s https://them.avico78.com/api/them/v1/admin/agents
# Expected: {"error":"authentication required"} or {"error":"Unauthorized"} — routing works

# 5. WS upgrade test (websocat or wscat if available)
# wscat -c wss://them.avico78.com/ws/dashboard?token=<token>
```

## Rollback

To disable external exposure without touching the CF tunnel or DNS:

```bash
# Comment out the them-external router block in:
# /home/avi/infrastructure/traefik/dynamic/them-routes.yml
# Traefik hot-reloads. No restart needed.
```

## Known issues

### JWT authentication gap (pre-existing, not a Cloudflare issue)
Some Go admin routes expect an opaque access token while the frontend sends a session JWT.
Admin actions may return 401 from the Go bridge. This is a known application-layer gap, not
a Cloudflare or Traefik routing failure.

### infra-traefik Docker-label routing (pre-existing)
infra-traefik has Docker socket access and picks up them-container labels, creating shadow
routers (e.g. `them-go-admin-reads@docker`) with no `Host()` condition. These match any
hostname and route to go-bridge IPs that infra-traefik cannot reach (different subnet),
causing 503 for direct `/api/v1/...` requests through infra-traefik.

This does **not** affect the browser user flow, because:
- The UI (/) is served correctly through `them-external@file` → them-traefik
- All browser API calls use `/api/them/...` which routes correctly through the them-external router
- Direct `/api/v1/...` requests from a browser are not part of the intended UX

Fix (future): Add `traefik.enable=false` to them containers' labels, or scope infra-traefik
to not use docker-provider at all for them-network containers.

## What to repeat on local RHEL server

1. Ensure `docker-compose.hetzner.yml` is included in the active compose project
2. Ensure `them-traefik` is on `proxy-network` (same as RHEL infra-traefik)
3. Add/uncomment the `them-external` router in the RHEL infra-traefik dynamic config
4. Add DNS CNAME in Cloudflare dashboard (same tunnel, different hostname or same)
5. Verify CORS includes the public hostname
