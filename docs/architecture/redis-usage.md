---
title: Redis Usage (what we store, why, and the risks)
type: architecture
status: current
owns:
  - redis/config/redis.conf
  - redis/config/redis-events.conf
  - go/internal/cache/
  - go/internal/session/
  - go/internal/ratelimit/
  - go/internal/metrics/
  - go/internal/appliveness/
  - go/internal/debugcred/
last_verified: 2026-10-31   # code read + gen_reference.py redis; per-user rate limit key documented
---

# Redis Usage

## Purpose
One page that answers: *what do we keep in Redis, which part of the platform owns it, how long it lives, and what
breaks if Redis loses it.* The exhaustive list of key/channel definitions is generated:
`docs/reference/redis-generated.md` (where each is defined). This page is the explanation.

## Two Redis servers
| | `them-redis` (main) | `them-redis-events` (profile `events`) |
|---|---|---|
| Role | state, caches, rate limits, pub/sub, run streams | durable queue for observability events only (`them:events`, `them:events:dlq`) |
| Config | `redis/config/redis.conf` | `redis/config/redis-events.conf` |
| Memory / eviction | `maxmemory 256mb`, **`allkeys-lru`** | `maxmemory 512mb`, **`noeviction`** |
| Persistence | AOF everysec + RDB snapshots | AOF everysec + RDB snapshots |
| Auth | `requirepass` only if `THE_M_REDIS_PASSWORD` is set | none configured |
| Used by | bridge, workers (`them-go-worker`, `dag-worker`), `them-auth-go`, `them-mcp-service`, `middleware-worker`, `agent-runtime` | event producers (bridge, auth, mcp) and `them-event-writer` — see `events-pipeline.md` |

Everything uses Redis DB 0 on the main server; keys are namespaced by prefix. No ports are published to the host.

## What we use it for (key families)

### 1. Admission and sessions — `them:ep:*`, `them:app:*`, `them:sess:*`, `them:pod*`, `rl:them:*:token:*`
Concurrency caps and queueing for WebSocket/SSE sessions. Sets of live session IDs per entry point and per app plus a
"shadow" key per session (TTL 90 s) used to prune ghosts; session Hash `them:sess:{id}` (90 s); gate queue list
`them:ep:gate:queue:{slug}`; pod heartbeat `them:pod:{instance}` (30 s) and `them:pods`; per-token per-minute counter (90 s).
Owners: `internal/gate`, `internal/session`. Detail: `run-lifecycle.md`. **Keys use the EP slug only, not the tenant (backlog F-31).**

### 2. Run event delivery — `them:dash:run:{runID}:stream`
A Redis Stream per run, written by workers (`runstream.PublishEvent`, AppFlow activities) and read by the bridge for
WS/SSE with replay from `last_event_id` (capped ≈5000 entries, no time TTL). Also read by the dashboard WS. Owners:
`internal/runstream`, `internal/appflow`. Related: `them:wait:{runID}` (4 h) marks a run paused at `wait_for_input`;
`them:run:*` pub/sub carries file-scan results from the middleware worker.

### 3. Caches (all rebuildable from Postgres)
| Key | TTL | Purpose / owner |
|---|---|---|
| `them:session:token:{sha256}` | 300 s | opaque bearer-token cache (L2); `internal/auth` |
| `them:agents:registry:{tenant}` | 600 s | agent registry (L2); `internal/agentregistry` |
| `them:gateway:policy:v2:{clientID}` | 15 min | resolved gateway policy; `internal/llmgateway`, evicted by `admin/policy_cache.go` |
| `them:{tenant}:mcp:manifest:{slug}` / `…:health:{slug}` | 5 min / 90 s | MCP tool list and health; `internal/mcp` |
| `them:dash:app_status_cache` | 120 s | app liveness snapshot for new dashboard clients; `internal/appliveness` |
| `them:app:{app}:orch:*`, `them:orch:loc:*`, `them:orchestrators:{name}` | see source | orchestrator/app lookups, deleted on admin mutation; `admin/service` |
| `them:oidc:last_login:*` | 24 h | SSO debug record per tenant+email; `internal/authserver` |

### 4. Rate limits and quotas — `rl:*`
Per-tenant API/run counters and monthly run counter (`rl:them:{tenant}:api|runs|runs:monthly:*`, `internal/quota`), per-app
and per-token counters (`internal/ratelimit`), per-user per-minute rate limit `rl:user:{id}:minute:{ts}` (90 s TTL, platform
role-based quotas — `internal/ratelimit/user_limiter.go`, `internal/execution/quotas.go`).

### 5. Coordination and invalidation (pub/sub channels and locks)
`them:mcp:leader` (30 s lock, MCP supervisor election), `them:token:revoked`, `them:ep:config:changed`, `them:agents:changed`,
`them:mcp:manifest:changed`, `them:security_config:invalidated:{app}`, `them:sess:control:{sid}` (admin disconnect).
Each tells every replica to drop an in-process cache. Pub/sub is fire-and-forget: a replica that is down misses the message and
relies on the cache TTL.

### 6. Task and workflow state
`them:agent:a2atask:{id}` (24 h, A2A task store for agent-runtime and inbound A2A), `them:hitl:{taskID}` (24 h, handle to a paused
human-in-the-loop Temporal workflow), legacy `them:agent:task:*`. Owners: `internal/agentgen`. Detail: `a2a-agents-agentgen.md`.

### 7. Debug credentials — `them:debug:{tenant}:{run}:{node}:llm_override` / `:secret:{param}`
Per-run LLM key/model overrides and HTTP secrets for App Canvas Debug Mode. TTL ≈ 3 h 40 min, deleted when the run
finalizes. **Stored as plain JSON/text, not encrypted.** Owner: `internal/debugcred`. Detail: `app-canvas-appflow.md`.

### 8. Metrics and dashboard feed
`them:metrics:{tenant}:{date}` (Hash), `…:app:{app}:{date}`, `…:users:{date}` (HyperLogLog), all TTL 32 days (`internal/metrics`);
`them:dash:*` pub/sub channels and `them:dash:services:health` (30 s) / `…:stats` for the live dashboard; `them:{tenant}:scan:state:{agent}`
(300 s hash) and channel `them:dash:agent:{id}` for agent security-scan progress.

### 9. Events queue (second Redis) — `them:events`, `them:events:dlq`
Stream with consumer group `event-writers`; producers use a non-blocking in-memory buffer. See `events-pipeline.md`.

## Key invariants
- Postgres is the source of truth; Redis entries are caches, counters, coordination or short-lived workflow state.
- Per-app/per-tenant state must carry the tenant/app in the key (root `CLAUDE.md`); several gate keys currently do not.
- Subscribe to a run's stream **before** starting its workflow (events are read from the stream, so nothing is lost once subscribed).
- A new key needs a TTL unless it is deliberately managed by code (gate sets) — and must be visible to the generator
  (`gen_reference.py redis`) by using a `them:`/`rl:` literal.

## Failure modes
| What happens | Effect |
|---|---|
| Main Redis down | Admission fails (500/503), streams stop, token/agent caches fall back to Postgres, pub/sub invalidation lost |
| Main Redis restarts | AOF restores it, but live session sets and shadow keys may be stale until they expire |
| **Memory pressure on main Redis** | `allkeys-lru` may **evict any key**, including run streams, HITL handles, `them:wait:*`, gate sets and the MCP leader lock — silently |
| Events Redis full | `noeviction`: writes fail, events dropped and counted (request path unaffected) |
| Pub/sub message missed | Replica serves a stale cache until its TTL (30 s – 10 min) |

## Known gaps (found while writing, 2026-10-25; config read live, code read; nothing stress-tested)
See `docs/plans/code-findings-backlog.md` F-88…F-90.
1. **Durable state in an LRU cache.** The main Redis is configured as a cache (`allkeys-lru`, 256 MB) but holds run streams, HITL handles, wait markers, gate sets and the leader lock that are not rebuildable. Under memory pressure these can disappear.
2. **Optional/no password and no TLS** on the main Redis; the events Redis has no password at all (network isolation only; ports are not published).
3. **Debug-mode secrets are stored unencrypted** in Redis for ~3.7 h.
4. Gate/session keys are not tenant-scoped (F-31).

## Where to change things
| To… | Edit |
|---|---|
| Add or change a key | the owning package (see tables); keep a `them:`/`rl:` literal, set a TTL, then run `python3.12 scripts/gen_reference.py redis` and update this page |
| Change memory/eviction/persistence | `redis/config/redis.conf` / `redis-events.conf`, then recreate the container (see the runbook) |
| Check what is live | `docker exec them-redis redis-cli --scan --count 1000` or `gen_reference.py redis-live` |

## Related
- `docs/reference/redis-generated.md` — where every key is defined (generated)
- `run-lifecycle.md`, `events-pipeline.md`, `mcp-service.md`, `a2a-agents-agentgen.md`, `auth-and-roles.md`, `llm-gateway.md`, `multi-tenancy-rls.md`
