# Exposing the-M externally at them.avico78.com

## Background

The them stack has its own internal Traefik (`them-traefik`) that handles path-based routing
between services (frontend, API, WebSocket, Temporal UI, etc.) on port 8088.

The infra stack's Traefik (`infra-traefik`) sits in front of everything and routes by hostname.
Cloudflared tunnels external traffic into infra-traefik.

The full flow when exposed:
```
Browser → Cloudflare (them.avico78.com) → cloudflared → infra-traefik → them-traefik:8088 → services
```

## What's already done

- CF Zero Trust Access app created for `them.avico78.com` (email OTP, same policy as omni/netdata/portainer)
- CF tunnel ingress entry added: `them.avico78.com` → `http://infra-traefik:80`
- `infra-traefik` is already on `proxy-network` and `them-traefik` is also on `proxy-network`

## What still needs to be done

### 1. Enable the router in `infrastructure/traefik/dynamic/them-routes.yml`

Uncomment the `them-external` router block (currently commented out):

```yaml
them-external:
  rule: "Host(`them.avico78.com`)"
  entrypoints:
    - web
  service: them-traefik-svc
  middlewares:
    - security-headers
```

### 2. Fix CORS in `them/docker-compose.yml`

The `CORS_ORIGINS` env var on `them-bridge` and `them-auth-service` currently only allows
`http://localhost:3111`. Add `https://them.avico78.com`:

```yaml
CORS_ORIGINS: "http://localhost:3111,https://them.avico78.com"
```

### 3. Fix the frontend API URL

`them-frontend` has `NEXT_PUBLIC_BRIDGE_WS_URL: ""` — this needs to point to the public WS URL:

```yaml
NEXT_PUBLIC_BRIDGE_WS_URL: "wss://them.avico78.com/ws"
```

### 4. Restart affected containers

After the compose changes:
```bash
cd /home/avi/them
docker compose up -d them-bridge them-auth-service them-frontend
docker restart infra-traefik
```

## Notes

- `them-traefik` listens on port 8088 (exposed to host) and is already reachable from `proxy-network`
- The infra `them-routes.yml` already has the service definition pointing to `http://them-traefik:8088`
- No changes needed to the them stack's own Traefik config
- The CF Zero Trust policy allows `avico78@gmail.com` and `avicoiot@gmail.com` (same as other apps)
