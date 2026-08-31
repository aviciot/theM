# the-M — Current Status
# Last updated: 2026-08-31
# HEAD: 4a6241b

---

## What is running

### Local Linux (dev overlay) — startup command
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
```
UI: `http://<server-ip>:8088`

| Container | Source | Port | Status |
|---|---|---|---|
| `them-traefik` | traefik:v3.6 | 8088 (host) | Running |
| `them-postgres` | postgres:16 | 5432 (internal) | Running |
| `them-redis` | redis:7 | 6379 (internal) | Running |
| `them-auth-go` | `go/cmd/auth-server` | 8703 (internal) | Running — sole auth service |
| `them-go-bridge` | `go/cmd/them` | 8002 (internal) | Running — all API routes |
| `them-go-worker` | `go/cmd/worker` | — | Running — sole Temporal worker |
| `them-agent-runtime` | `go/cmd/agent-runtime` (×2) | 9300 (internal) | Running — canvas A2A execution |
| `them-frontend` | `frontend/` (Next.js) | 3200 (internal) | Running |
| `temporal-frontend` | temporalio/auto-setup | 7233 (internal) | Running |

**Python bridge is fully deleted (2026-08-31):**
- `them-bridge` (Python FastAPI) — `app/`, `Dockerfile`, `Dockerfile.worker` deleted from filesystem
- `them-worker` (Python Temporal) — deleted; all traffic on `them-orchestration-go`
- All stale test scripts targeting port 8001 / them-bridge container deleted
- `docker-compose.hetzner.yml` cleaned of bridge/worker blocks

**Optional profiles:**
- `--profile test-agents` — adds A2A echo/slow/stream test agents (ports 9200-9202)
- `--profile security` — adds `them-security-agent` (port 9500)
- `--profile agents` — adds `them-agent-runtime` replicas (port 9300)

---

## Go route ownership — all routes

All routes served by `them-go-bridge` (port 8002, behind Traefik on 8088):

| Domain | Routes |
|---|---|
| Health | `GET /health/live`, `GET /health/ready` |
| Auth | `/api/v1/auth/*` (login, me, refresh, logout) via `them-auth-go` |
| Admin agents | Full CRUD + discover + test + security-scan |
| Admin orchestrators | Full CRUD |
| Admin applications | Full CRUD + entry-points + runtime + bulk-delete + sub-routes |
| Admin tokens | Full CRUD |
| Admin sessions | List + disconnect |
| Admin LLM providers | Full CRUD + routing/config |
| Admin monitoring-config | GET + PUT |
| Admin component-definitions | Full ownership |
| Admin agent-definitions | Full ownership |
| Admin system-agents | Full ownership |
| Runs | All: list, stats, detail, tasks, artifacts, signal, cancel, delete, bulk-delete |
| WS/SSE | `/apps/{slug}/ws`, `/apps/{slug}/sse` + legacy two-segment paths |
| Dashboard | `GET /ws/dashboard` |
| A2A server | `/a2a/*` — Go handler in `internal/a2a/`, wired via Traefik |

**Not in Go (no handler needed):**
- `GET /api/v1/admin/users`, `/roles`, `/teams` — auth CRUD (served by `them-auth-service` on 8701 directly)
- Applications export/import/restore — not migrated; no active use

---

## Current feature state

### Multi-artifact — CONFIRMED LIVE (2026-08-23)
- A2A agents can return multiple files per response — `task.artifacts[]` is fully iterated
- Single file → `{"artifact":{}}` (backward compat); multiple → `{"artifacts":[...]}`
- Orchestrator records and emits each artifact independently
- **Verified:** run `5691b24a` — 2 artifacts (HTML + zip) from `a2a-stream`

### Streaming — CONFIRMED LIVE (2026-08-23)
- `AgentConfig.SupportsStreaming` set from agent card `capabilities.streaming` on discover
- `InvokeForRunStreaming` sends `SendStreamingMessage` → SSE, fires `onArtifact` per `lastChunk:true`
- Wire format: `"role":"ROLE_USER"` (string), camelCase JSON tags (`artifactUpdate`, `lastChunk`)
- Non-streaming agents fall through to `InvokeForRun` transparently
- **Verified:** run `23aeb8bf` (single zip), run `5691b24a` (HTML + zip, two independent artifacts)

### Playground Artifacts tab
- Renders: `image/*` → `<img>`, `application/pdf` → iframe, `text/html` → srcDoc iframe,
  `text/markdown`/text → `<pre>`, unknown → download button

### docu-writer agent
- Haiku 4.5 (async), formats: html / markdown / pdf
- PDF via fpdf2: `part.raw` (bytes), NOT `part.data` (protobuf JSON Value)

### a2a-stream test agent (v1.2.0)
- Words streamed word-by-word, then HTML file, then zip file — all via SSE artifacts
- Start with: `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile test-agents up -d a2a-stream`

### Canvas A2A Agent Builder
- All phases complete: builder UI, compiler, publish pipeline, runtime wiring, BuildValidator
- LLM keys per-app in `applications.provider_keys` (AES-GCM), no global fallback

---

## Known issues / blockers

1. **E2E canvas agent run not verified** — infrastructure complete but no confirmed end-to-end run through a canvas agent on the live stack.

2. **Auth admin CRUD** — `them-auth-service` (Python, 8701) still serves users/roles/teams for the frontend. No Go implementation yet.

---

## Deployment environments

| Environment | Compose files | Entry point |
|---|---|---|
| Local dev | `docker-compose.yml` + `docker-compose.dev.yml` | `http://localhost:8088` |
| Hetzner prod | `docker-compose.yml` + `docker-compose.hetzner.yml` | `https://them.avico78.com` |

Secrets: derived via HMAC-SHA256 from `secrets.local`. Run `./generate-env.sh` to regenerate `.env`.
