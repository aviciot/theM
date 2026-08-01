# Docker Persistence and Rebuild-Safety Audit
**Date:** 2026-08-01
**Scope:** All services defined in `docker-compose.yml` + overlay files (`docker-compose.linux.yml`, `docker-compose.integration.yml`, `docker-compose.soak.yml`, `docker-compose.traefik.yml`, `docker-compose.cloudflare.yml`)
**Constraint:** Read-only — no container restarts, no image rebuilds, no secret exposure, no file modifications
**Goal:** Prove that a Docker image rebuild and container recreate will not accidentally lose source code, configuration, runtime data, artifacts, or generated files

---

## Executive Summary

The stack is **rebuild-safe for all stateful data**. Named volumes (`them-postgres-data`, `them-redis-data`, `them-logs`) survive `docker compose down` (without `--volumes`) and container recreation unconditionally. Go services are fully image-baked with no writable mounts — safest possible rebuild posture. Python services run from a source bind mount — code is always the git working tree, never the image layer. One configuration mismatch exists for the frontend that affects hot-path development ergonomics but not data safety.

---

## Volume Inventory

### Named Volumes

| Volume | Driver | Host Mountpoint | Container Mount | Services |
|---|---|---|---|---|
| `them-postgres-data` | local | `/var/lib/docker/volumes/them-postgres-data/_data` | `/var/lib/postgresql/data` | `them-postgres` |
| `them-redis-data` | local | `/var/lib/docker/volumes/them-redis-data/_data` | `/data` | `them-redis` |
| `them-logs` | local | `/var/lib/docker/volumes/them-logs/_data` | `/app/logs` | `them-bridge`, `them-worker` |

All three named volumes use the `local` driver. They are created at first `up` and survive:
- `docker compose down` (without `--volumes`)
- Container recreation via `--force-recreate`
- Image rebuilds
- Project name changes (ownership is by volume name, not project)

They are only deleted by `docker volume rm <name>` or `docker compose down --volumes`. `scripts/deploy.sh` explicitly never runs `down --volumes`.

### Anonymous / Inline Volumes

| Service | Container Path | Size | Created When |
|---|---|---|---|
| `them-frontend` | `/app/.next` | ~118 MB | Container first start (or recreate) |
| `them-frontend` | `/app/node_modules` | ~600 MB+ | Container first start (or recreate) |

Anonymous volumes are per-container. On `--force-recreate`, new empty anonymous volumes are created. The build cache (`/app/.next`) is lost and Next.js must recompile. Node modules are re-installed during container init. Neither contains source code or user data.

---

## Service-by-Service Analysis

### `them-postgres`

| Property | Value |
|---|---|
| Dockerfile | `postgres:16` (official image — no custom Dockerfile) |
| Source code in image? | No |
| Mounts | Named vol `them-postgres-data → /var/lib/postgresql/data`; bind `/home/avi/them/postgres/init → /docker-entrypoint-initdb.d` (ro, init-only) |
| Rebuild effect | Zero data loss — `pgdata` is in the named volume |
| State after recreate | All DB data intact |
| Schema + data risk | None from rebuild or recreate |
| Row counts at audit time | `runs`: 0, `artifacts`: 0, `run_steps`: 0 |

The init bind mount (`postgres/init/`) runs only on first DB initialization (when `pgdata` is empty). It does not run on container recreation if the volume already contains data.

**Rebuild verdict: Safe.**

---

### `them-redis`

| Property | Value |
|---|---|
| Dockerfile | `redis:7-alpine` (official image) |
| Source code in image? | No |
| Mounts | Named vol `them-redis-data → /data`; bind `/home/avi/them/redis/config/redis.conf → /etc/redis/redis.conf` (ro) |
| Rebuild effect | Zero data loss — RDB/AOF persisted in named volume |
| State after recreate | All Redis data intact |

Redis persistence is configured via the bind-mounted `redis.conf`. On recreate, the config is re-read from the host file (no copy into image). The named volume survives.

**Rebuild verdict: Safe.**

---

### `them-auth-service`

| Property | Value |
|---|---|
| Dockerfile | `auth_service/Dockerfile` (Python 3.11-slim, uv) |
| Source code baked into image? | Yes (`COPY . .` at build time) |
| Mounts at runtime | None |
| State storage | All state in `auth_service` schema in `them-postgres-data` named volume |
| Rebuild effect | New image carries new source; no local state lost because none exists |
| Writable paths at runtime | In-container writes only (ephemeral); no persistent local data |

On rebuild + recreate, the running code updates to current HEAD. No user data at risk.

**Rebuild verdict: Safe. State is entirely in Postgres named volume.**

---

### `them-bridge` (Python orchestrator, replicas 1 and 2)

| Property | Value |
|---|---|
| Dockerfile | `Dockerfile` (Python 3.13-slim) |
| Source baked into image? | Yes (`COPY . .`), but overridden at runtime |
| Mounts | Bind `/home/avi/them → /app` (rw); named vol `them-logs → /app/logs` |
| Source code in effect | Always the git working tree at `/home/avi/them/app/` — not the baked image layer |
| Rebuild effect on source | None — bind mount takes precedence |
| Log data | Named volume `them-logs` — survives recreate |
| Writable runtime paths | `/app/logs` (named vol) + anything else under bind mount (writes would land on host) |

The bind mount means the running code is always the live git working tree. Image rebuilds have no effect on which source code runs. `git pull` + container restart is the code deployment mechanism.

**Rebuild verdict: Safe. Source is from the host; logs are in named volume.**

---

### `them-worker` (Python Temporal worker)

Same as `them-bridge` (identical Dockerfile, same bind mount `/home/avi/them → /app`, same named logs volume). Temporal workflow state is stored in `them-postgres-data`.

**Rebuild verdict: Safe.**

---

### `them-go-bridge`, `them-go-bridge-2` (Go gateway)

| Property | Value |
|---|---|
| Dockerfile | `Dockerfile.go` (multi-stage: golang:1.23-alpine builder → alpine:3.20 runtime) |
| Build process | `go mod tidy && go test ./... && go build -o /them` in builder stage |
| Runtime image contents | Binary `/app/them` + `ca-certificates` only |
| Mounts at runtime | **None** |
| State storage | All state in Postgres (named volume) and Redis (named volume) |
| Rebuild triggers full test suite | Yes — `go test ./...` must pass before binary is produced |
| Rebuild effect | New binary from current source; no local data at risk |

The runtime image contains no source code and no Go toolchain. Container recreate creates a fresh container from the baked image. There is no writable filesystem state to lose.

**Rebuild verdict: Safest possible. Pure image, no mounts, tests enforced at build time.**

---

### `them-go-worker`, `them-go-worker-2` (Go Temporal workers)

Same as Go gateway above. Built from `Dockerfile.go-worker` (identical multi-stage pattern; binary is `them-worker`). No mounts. No writable state.

**Rebuild verdict: Safe.**

---

### `them-frontend` (Next.js)

| Property | Value |
|---|---|
| Dockerfile in use | `frontend/Dockerfile.dev` (runs `npm run dev`) |
| Expected Dockerfile | `frontend/Dockerfile` (multi-stage production build — `npm run build` + standalone runner) |
| NODE_ENV | `production` (set in `docker-compose.linux.yml`) |
| Mounts | Bind `/home/avi/them/frontend → /app` (rw); anon vol `/app/.next`; anon vol `/app/node_modules` |
| Running command | `npm run dev` (Next.js dev server with HMR) |

**Configuration mismatch (Finding F-1):** The Linux overlay sets `NODE_ENV=production` and `volumes: []` (intended to remove the bind mount), but `volumes: []` in a Compose override replaces the named volume list while anonymous inline volumes survive the merge. The rendered config still carries the source bind mount from `docker-compose.yml`. The running container uses `Dockerfile.dev` + `npm run dev` + source bind mount despite `NODE_ENV=production`.

**Rebuild effect:** On container recreate, new empty anonymous volumes are created. Next.js recompiles during `npm run dev` startup (writes compiled output to `/app/.next`). The source bind mount is re-attached. No source code is lost — it lives on the host. The `.next` build cache is regenerated within a few seconds of startup.

**Data at risk on recreate:** None. The anonymous volumes contain only derived/compiled artifacts, not user data or source code.

**Rebuild verdict: Safe for data. Cosmetic finding only — see F-1 below.**

---

### `them-traefik`

| Property | Value |
|---|---|
| Image | `traefik:v3.2` (official) |
| Mounts | Bind `/var/run/docker.sock` (ro); bind `/home/avi/them/traefik/traefik.yml` (ro) |
| State storage | Stateless — routing config is in `traefik.yml` and Docker labels |
| Rebuild effect | Pull new Traefik image; config re-read from host files on start |

**Rebuild verdict: Safe. Stateless.**

---

## Data Persistence Summary

| Data type | Where stored | Survives `down` (no `--volumes`)? | Survives recreate? | Survives image rebuild? |
|---|---|---|---|---|
| PostgreSQL DB (schema + data) | `them-postgres-data` named vol | Yes | Yes | Yes |
| Redis state (sessions, rate limits) | `them-redis-data` named vol | Yes | Yes | Yes |
| Bridge + worker logs | `them-logs` named vol | Yes | Yes | Yes |
| Python source code | Host git working tree (bind mount) | Yes | Yes | N/A — never in image layer |
| Go binaries | Image layer (baked at build) | N/A | Yes (image unchanged) | Rebuilt from source |
| Frontend source | Host git working tree (bind mount, see F-1) | Yes | Yes | N/A |
| Frontend compiled build (`.next`) | Anonymous vol | No | No (regenerated at start) | No (regenerated at start) |
| Temporal workflow history | `them-postgres-data` named vol (temporal schema) | Yes | Yes | Yes |
| Artifacts (DB-backed) | `artifacts` table in `them-postgres-data` | Yes | Yes | Yes |
| Run records | `runs`, `run_steps` tables in `them-postgres-data` | Yes | Yes | Yes |

---

## Findings

### F-1 — Frontend Dockerfile/config mismatch (Low severity)

**Service:** `them-frontend`
**Observation:** `docker-compose.yml` specifies `dockerfile: Dockerfile.dev` and a source bind mount. `docker-compose.linux.yml` sets `volumes: []` and `NODE_ENV=production`, intending a production build without the bind mount. Due to Compose merge semantics, `volumes: []` in an overlay replaces the named volumes list but does not remove anonymous inline volume entries. The rendered config retains the source bind mount and anonymous volumes. The running container uses `Dockerfile.dev` + `npm run dev`.

**Data impact:** None. Source code, compiled output, and user data are all preserved correctly.

**Functional impact:** The frontend runs a dev server instead of the production Next.js standalone server. HMR is active. Startup is slower after a cold recreate (needs to recompile). The dev server has slightly different routing behavior than the standalone production runner.

**Fix path (not in scope for this audit):** Move the source bind mount and anon volumes from `docker-compose.yml` into a `docker-compose.dev.yml` overlay. The base file should carry only the production `Dockerfile` (multi-stage) with no bind mount. The linux overlay would then correctly override to production without residual volume entries.

---

### F-2 — Go services produce no local state (Note, not a finding)

Go gateway and worker containers have zero mounts. Any in-container filesystem write (logs, temp files) is lost on container death. Confirmed that Go services write logs to stdout/stderr only (captured by Docker json-file driver) and store all persistent state in Postgres and Redis. This is intentional and correct.

---

### F-3 — Artifact storage is fully DB-backed (Confirmed, not a risk)

`go/internal/artifacts/handler.go` header comment and code explicitly document: "Artifact data fetched from PostgreSQL, never from the filesystem." Zero filesystem reads in the artifact download path. All artifact data lives in the `artifacts` table in `them-postgres-data`. Current row count: 0 (no production artifacts in this environment).

---

## Rebuild Safety Procedures

### Safe to rebuild at any time (no coordination required)
- `them-go-bridge`, `them-go-bridge-2`
- `them-go-worker`, `them-go-worker-2`
- `them-auth-service`

### Safe to rebuild; source code runs from host (image rebuild does not update running code)
- `them-bridge` (code change requires `git pull` + container restart, not image rebuild)
- `them-worker` (same)
- `them-frontend` (same, due to F-1 source bind mount)

### Never rebuild with `--volumes`
- `them-postgres` — `them-postgres-data` volume would be destroyed
- `them-redis` — `them-redis-data` volume would be destroyed

### Stateless — rebuild any time
- `them-traefik`

---

## `scripts/deploy.sh` Rebuild Safety

The deployment script encodes these constraints:
- `up` always uses `--no-recreate` — never force-destroys running containers
- No `down` command exists in the script — operator must invoke `docker compose down` manually (preventing accidental `--volumes`)
- `build` command builds images without starting/stopping containers

---

## Files Referenced

| File | Role |
|---|---|
| `Dockerfile` | Python bridge + worker (python:3.13-slim) |
| `Dockerfile.worker` | Python Temporal worker (same base, different CMD) |
| `Dockerfile.go` | Go gateway multi-stage build |
| `Dockerfile.go-worker` | Go Temporal worker multi-stage build |
| `frontend/Dockerfile` | Frontend production build (not currently active — see F-1) |
| `frontend/Dockerfile.dev` | Frontend dev server (currently active) |
| `auth_service/Dockerfile` | Auth service (python:3.11-slim + uv) |
| `docker-compose.yml` | Base service definitions |
| `docker-compose.linux.yml` | Linux/production overlay (frontend config, network mode) |
| `docker-compose.integration.yml` | Go Workers + Go bridge + exposed infra ports |
| `scripts/deploy.sh` | Path-independent deploy helper; never runs `down --volumes` |
