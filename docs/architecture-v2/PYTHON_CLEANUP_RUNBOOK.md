# Python Cleanup Runbook
# Last updated: 2026-08-30
# Status: PARTIALLY DONE — compose changes committed, filesystem deletion pending

---

## What is already done (committed)

- `them-bridge` service block removed from `docker-compose.yml`
- `them-bridge-2` service block removed from `docker-compose.yml`
- `them-worker` service block removed from `docker-compose.yml`
- `livekit-agent THEM_BRIDGE_WS` updated from `ws://them-bridge:8001` → `ws://them-go-bridge:8002`
- `them-worker` container stopped

---

## Remaining: delete Python source from filesystem

Run these commands **once** from the repo root (`/opt/docker/them`):

```bash
# Confirm what will be deleted first
ls app/
ls Dockerfile Dockerfile.worker

# Delete Python bridge source tree and Dockerfiles
rm -rf app/
rm Dockerfile
rm Dockerfile.worker

# Verify gone
ls app/ 2>&1        # should say: No such file or directory
ls Dockerfile 2>&1  # should say: No such file or directory
ls Dockerfile.worker 2>&1  # should say: No such file or directory
```

---

## Commit after deletion

```bash
git add -u app/ Dockerfile Dockerfile.worker
git status   # confirm only these files staged as deleted

git commit -m "chore: delete Python bridge/worker source — the-M is Go-only

Removed:
  app/             Python FastAPI orchestrator (bridge + WS + routers + services + temporal)
  Dockerfile       Python bridge image build
  Dockerfile.worker Python Temporal worker image build

Compose changes already committed (them-bridge, them-bridge-2, them-worker blocks
removed; livekit-agent THEM_BRIDGE_WS updated to them-go-bridge:8002).

them-orchestration Temporal task queue is permanently empty.
All traffic on them-orchestration-go (Go worker).

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Verify stack still starts cleanly after deletion

```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal up -d

docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal ps

# Confirm these are NOT present (no legacy profile started):
#   them-bridge
#   them-bridge-2
#   them-worker

# Confirm these are running:
#   them-go-bridge
#   them-go-worker
#   them-auth-go
#   them-agent-runtime (x2)
#   them-frontend
#   them-postgres
#   them-redis
#   them-traefik
#   temporal-frontend
```

---

## What is NOT deleted by this runbook

| Path | Reason to keep |
|---|---|
| `auth_service/` | Python auth admin CRUD (users/roles/teams) — still serves them-auth-service on port 8701. Retire separately when Go auth admin is built. |
| `scripts/` | Test runner and migration scripts — language-neutral, still used |
| `agents/` | A2A agent implementations — NOT Python bridge code |
| `frontend/` | Next.js frontend — unrelated |
| `go/` | Go source — keep |

---

## What to update in docs after deletion

- `docs/architecture-v2/CURRENT.md`: change hard constraints bullet about `them-bridge`/`them-worker` from "permanently retired" to "deleted"
- `CLAUDE.md` container map: remove `them-bridge` and `them-bridge-2` and `them-worker` rows
- `docs/STATUS.md`: note Python bridge/worker deleted

---

## Notes

- `app/` is entirely the Python FastAPI bridge. It is NOT the Go bridge (`go/internal/`).
- After deletion, `docker compose build` will no longer find `Dockerfile` or `Dockerfile.worker` — that is correct.
- The `auth_service/` Python code runs as a separate container (`them-auth-service`) and is NOT deleted here.
