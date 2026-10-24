# Traefik Routing Verification Matrix
# the-M — verified against effective `docker compose config` output
# Last updated: 2026-08-31

---

## Priority rules summary

| Priority | Router | Target | Notes |
|---|---|---|---|
| 150 | `them-temporal-ui` | temporal-ui-svc | `/temporal*` |
| 150 | `them-go-ws-dashboard` | them-go-bridge-svc | `GET /ws/dashboard` |
| 130 | `them-go-health-sub` | them-go-bridge-svc | `/health/live`, `/health/ready` |
| 121 | `them-go-agent-bindings` | them-go-bridge-svc | `/api/v1/admin/applications/{id}/agent-bindings` |
| 121 | `them-go-debug-proxy` | them-go-bridge-svc | `POST /api/v1/admin/debug-proxy` |
| 121 | `them-go-transform` | them-go-bridge-svc | `/api/v1/admin/transform-*` |
| 120 | `them-auth-go-router` | them-auth-go-svc | `/auth/*` |
| 120 | `them-go-a2a` | them-go-bridge-svc | `/a2a/*` |
| 120 | `them-go-well-known` | them-go-bridge-svc | `Path(/.well-known/agent.json)` — A2A agent card |
| 120 | `them-go-agent-defs` | them-go-bridge-svc | `/api/v1/admin/agent-definitions` |
| 120 | `them-go-apps-sse` | them-go-bridge-svc | `/apps/{slug}/sse` |
| 120 | `them-go-apps-voice` | them-go-bridge-svc | `POST /apps/{slug}/voice/*` |
| 120 | `them-go-apps-ws` | them-go-bridge-svc | `GET /apps/{slug}/ws` |
| 120 | `them-go-component-defs` | them-go-bridge-svc | `/api/v1/admin/component-definitions` |
| 120 | `them-go-llm-providers` | them-go-bridge-svc | `/api/v1/admin/llm-providers` (excl. routing/config) |
| 120 | `them-go-llm-routing-config` | them-go-bridge-svc | `GET\|PUT /api/v1/admin/llm-providers/routing/config` |
| 120 | `them-go-monitoring-config` | them-go-bridge-svc | `GET\|PUT /api/v1/admin/monitoring-config` |
| 120 | `them-go-node-types` | them-go-bridge-svc | `/api/v1/admin/node-types` |
| 120 | `them-go-sessions` | them-go-bridge-svc | `/api/v1/admin/sessions` |
| 120 | `them-go-system-agents` | them-go-bridge-svc | `/api/v1/admin/system-agents` |
| 120 | `them-go-tokens` | them-go-bridge-svc | `/api/v1/admin/tokens` |
| 120 | `them-go-ws-two-seg` | them-go-bridge-svc | `GET /ws/orchestrate/{app}/{ep}` |
| 120 | `them-go-sse-two-seg` | them-go-bridge-svc | `GET\|POST /sse/orchestrate/{app}/{ep}` |
| 116 | `them-go-agents-actions` | them-go-bridge-svc | `POST /api/v1/admin/agents/discover\|test\|security-scan` |
| 116 | `them-go-mcp-credentials` | them-go-bridge-svc | `/api/v1/admin/applications/{id}/mcp-credentials` |
| 116 | `them-go-runs-bulk-delete` | them-go-bridge-svc | `POST /api/v1/runs/bulk-delete` |
| 116 | `them-go-runs-cancel` | them-go-bridge-svc | `PATCH /api/v1/runs/{id}/cancel` |
| 115 | `them-go-agents-create` | them-go-bridge-svc | `POST /api/v1/admin/agents` |
| 115 | `them-go-agents-update` | them-go-bridge-svc | `PUT\|PATCH\|DELETE /api/v1/admin/agents/{id}` |
| 115 | `them-go-apps-create` | them-go-bridge-svc | `POST /api/v1/admin/applications` |
| 115 | `them-go-apps-subroutes` | them-go-bridge-svc | `* /api/v1/admin/applications/{id}/...` |
| 115 | `them-go-apps-update` | them-go-bridge-svc | `PUT\|PATCH\|DELETE /api/v1/admin/applications/{id}` |
| 115 | `them-go-eps-writes` | them-go-bridge-svc | `* /api/v1/admin/applications/{id}/entry-points` |
| 115 | `them-go-mcp-servers` | them-go-bridge-svc | `/api/v1/admin/mcp-servers` |
| 115 | `them-go-orchs-create` | them-go-bridge-svc | `POST /api/v1/admin/orchestrators` |
| 115 | `them-go-orchs-update` | them-go-bridge-svc | `PUT\|PATCH\|DELETE /api/v1/admin/orchestrators/{id}` |
| 115 | `them-go-runs-signal` | them-go-bridge-svc | `POST /api/v1/runs/{id}/signal` |
| 115 | `them-go-runs-sub` | them-go-bridge-svc | `GET /api/v1/runs/{id}/tasks\|artifacts` |
| 114 | `them-go-runs-delete` | them-go-bridge-svc | `DELETE /api/v1/runs/{id}` |
| 110 | `them-go-admin-reads` | them-go-bridge-svc | `GET /api/v1/admin/agents\|orchestrators\|applications\|runs/*` |
| 100 | `them-livekit` | livekit-svc | `/livekit/*` |
| **10** | **`them-ui`** | **them-ui-svc** | **`PathPrefix(/)` — catches all frontend requests** |

**Key invariant:** `them-ui` is the only `PathPrefix(/)` router. Priority 10 means every
explicit Go or auth router (priority ≥ 100) wins. Frontend pages, Next.js assets, and
any unrecognised path fall through to `them-ui-svc` (port 3200). There is no catch-all
Go router — the explicit router set covers all backend paths.

---

## Curl verification matrix

Run from inside the Docker network (e.g. `docker exec them-go-bridge curl -s ...`) or
from the host via port 8088. Replace `<TOKEN>` with a valid admin JWT.

```bash
HOST=http://localhost:8088

# ── Frontend (must reach them-frontend:3200, return HTML) ────────────────────
curl -si $HOST/ | head -3
# expect: HTTP/1.1 200 or 308 redirect, Content-Type: text/html

curl -si $HOST/admin | head -3
# expect: HTTP/1.1 200 or 307/308, Content-Type: text/html

curl -si "$HOST/_next/static/chunks/main.js" | head -2
# expect: HTTP/1.1 200, Content-Type: application/javascript (or 404 from Next.js, never Go)

# ── Health (must reach them-go-bridge:8002) ───────────────────────────────────
curl -s $HOST/health/live
# expect: {"status":"ok",...}

curl -s $HOST/health/ready
# expect: {"status":"ok",...} or 503

# ── Auth (must reach them-auth-go:8703) ──────────────────────────────────────
curl -si -X POST $HOST/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}' | head -2
# expect: HTTP/1.1 200, JSON with access_token

# ── Admin API (must reach them-go-bridge:8002) ───────────────────────────────
curl -si -H "Authorization: Bearer <TOKEN>" $HOST/api/v1/admin/agents | head -2
# expect: HTTP/1.1 200, JSON array

curl -si -H "Authorization: Bearer <TOKEN>" $HOST/api/v1/admin/applications | head -2
# expect: HTTP/1.1 200, JSON array

curl -si -H "Authorization: Bearer <TOKEN>" $HOST/api/v1/admin/node-types | head -2
# expect: HTTP/1.1 200, JSON array of 12 node types

curl -si -H "Authorization: Bearer <TOKEN>" $HOST/api/v1/runs | head -2
# expect: HTTP/1.1 200, JSON array

curl -si -H "Authorization: Bearer <TOKEN>" $HOST/api/v1/admin/llm-providers | head -2
# expect: HTTP/1.1 200, JSON array

curl -si -H "Authorization: Bearer <TOKEN>" $HOST/api/v1/admin/mcp-servers | head -2
# expect: HTTP/1.1 200, JSON array

# ── A2A (must reach them-go-bridge:8002) ─────────────────────────────────────
curl -si -X POST $HOST/a2a/<agent-slug> \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"tasks/send","id":"1","params":{...}}' | head -2
# expect: HTTP/1.1 200 (or 404 if slug unknown), JSON-RPC response

curl -si $HOST/.well-known/agent.json | head -2
# expect: HTTP/1.1 200, Content-Type: application/json, JSON agent card body

# ── WebSocket (must reach them-go-bridge:8002) ────────────────────────────────
# Use wscat or similar — Traefik upgrades the connection to ws://them-go-bridge:8002
# wscat -c ws://localhost:8088/ws/dashboard   → must receive JSON events
# wscat -c ws://localhost:8088/apps/<slug>/ws → must receive orchestration events

# ── SSE (must reach them-go-bridge:8002) ─────────────────────────────────────
curl -si -N -H "Accept: text/event-stream" \
  "$HOST/apps/<slug>/sse?token=<bearer>" | head -5
# expect: HTTP/1.1 200, Content-Type: text/event-stream

# ── Unregistered path → frontend (must reach them-frontend:3200) ─────────────
curl -si $HOST/some-unknown-path | head -3
# expect: Next.js 404 page (HTML), NOT a Go 404 JSON response
```

---

## No-catch-all rationale

A `PathPrefix(/)` router at any priority above 10 would steal requests from `them-ui-svc`.
All Go backend routes are covered by the explicit router set above (priorities 110–150).
Any path not matching an explicit Go router correctly falls through to `them-ui` at priority 10.

If a new Go route prefix is added (e.g. `/metrics`), add an explicit router entry for it —
do NOT re-introduce a broad catch-all.
