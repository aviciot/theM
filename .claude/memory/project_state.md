---
name: project-state
description: "Odin stack layout, ports, shared containers, current build phase"
metadata:
  type: project
---

## Server
- Dev: billing-43 (same as Omni), path `/opt/docker/odin/`
- Stack is completely separate from `/opt/docker/omni-stack/`

## Shared infrastructure (from Omni)
- Postgres: `omni-postgres` container — Odin uses DB `odin`, role `odin`
- Redis: `omni-redis` container — Odin uses **DB index 1** (Omni uses 0)
- Networks: `omni-network` (internal), `proxy-network` (Traefik)

## Key ports
| Service | Port |
|---|---|
| odin-bridge | 8001 (Omni bridge is 8000) |
| odin-auth-service | 8701 (Omni auth is 8700) |

## Build phase (2026-06-28)
Phase 0+1 complete: skeleton, config, database, models, health router, auth_service copy.
Phases 2-7 pending. See docs/STATUS.md.

## Git
Not yet initialized. Run `git init && git add . && git commit -m "init"` to start.
