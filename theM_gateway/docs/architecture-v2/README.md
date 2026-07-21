# THEM Architecture v2 — Documentation Index

This folder contains the complete architectural specification for the Go rewrite of the THEM AI orchestration platform.

These documents are the **primary source of truth** for the new platform. Every architectural decision is recorded here before implementation begins.

---

## Document Index

| File | Contents |
|---|---|
| [01-vision-and-goals.md](01-vision-and-goals.md) | Mission, design principles, goals, non-goals, language choice |
| [02-current-architecture-assessment.md](02-current-architecture-assessment.md) | Phase 1 discovery: full map of the Python platform (modules, entry points, data layer, request lifecycle) |
| [03-architectural-review.md](03-architectural-review.md) | Phase 2 critical review: every significant bug and design flaw in the current platform, with severity ratings |
| [03b-alternatives-considered.md](03b-alternatives-considered.md) | Phase 3 alternatives: three architectural options evaluated, with trade-offs and rejection rationale |
| [04-target-architecture.md](04-target-architecture.md) | Phase 4 target: complete Go monolith design — package structure, interfaces, session model, auth model, orchestration, LLM providers, events, security, observability, deployment, technology decisions, ADRs |
| [05-migration-strategy.md](05-migration-strategy.md) | Phase 5 migration: incremental 7-phase plan from Python to Go with validation and rollback strategy per phase |
| [06-domain-model.md](06-domain-model.md) | Ubiquitous language glossary, entity catalogue with invariants, value objects, ER diagram, AppOrchestrator resolution algorithm, tenant boundary decision |
| [07-bounded-contexts.md](07-bounded-contexts.md) | Five named bounded contexts, context map, data ownership table, anti-corruption layers, aggregate roots per context |
| [08-state-machines.md](08-state-machines.md) | State machines for Task, Run, Session, OrchestrationWorkflow (Temporal), and A2A adapter — with all transitions, guards, side effects, and forbidden transitions |
| [09-domain-events.md](09-domain-events.md) | Domain event catalogue, the `ready` bootstrap handshake, streaming events, cache invalidation signals, delivery guarantees table |
| [10-sequence-diagrams.md](10-sequence-diagrams.md) | Six Mermaid sequence diagrams: WS lifecycle, WS termination paths, HITL flow, token revocation broadcast, agent registry invalidation, graceful shutdown |
| [11-component-diagram.md](11-component-diagram.md) | Package dependency graph, external dependency map, event.Bus wiring, deployment topology (dev + VPS), data flow narrative |

---

## Key Decisions (Summary)

| Decision | Choice | Rationale |
|---|---|---|
| Architecture | Monolith-First Go Service | Lowest complexity, fixes all Critical/High findings, extractable later |
| Auth | Local RS256 JWT validation | Eliminates Auth service HTTP call on hot path |
| Token revocation | Redis pub/sub broadcast | Multi-pod revocation effective in <1s |
| Temporal | Retained (Go SDK) | Correct model for HITL, durable state, sub-orchestrators |
| HTTP framework | chi | Stdlib-compatible, no magic, composable middleware |
| DB driver | pgx/v5 (direct) | Full PostgreSQL feature support, trace hooks for OTel |
| Redis client | rueidis | Best-in-class Go Redis client, RESP3, built-in hook support |
| Logging | slog (stdlib) | No dependency, OTel-compatible, structured |
| Observability | OTel + Prometheus | Standard, vendor-neutral, wired at startup |

---

## Architecture Principles

1. **Single code path** — no parallel implementations
2. **Fail loudly at startup** — no silent config defaults accepted
3. **Correctness over cleverness** — every shortcut must be explicitly justified
4. **Observability first** — traces and metrics wired before business logic
5. **Monolith with seams** — clean package boundaries enable future service extraction

---

## Status

| Phase | Status |
|---|---|
| Phase 1 — Discovery | ✅ Complete |
| Phase 2 — Critical Review | ✅ Complete |
| Phase 3 — Alternatives | ✅ Complete |
| Phase 4 — Target Architecture | ✅ Complete |
| Phase 5 — Migration Strategy | ✅ Complete |
| Domain Model + Bounded Contexts | ✅ Complete |
| State Machines | ✅ Complete |
| Domain Events | ✅ Complete |
| Sequence Diagrams | ✅ Complete |
| Component Diagrams | ✅ Complete |
| **Implementation** | 🔄 In progress — Phase 11c partial |

### Implementation state (as of 2026-07-21)

**Implemented in Go (Phase 11c):**
- `internal/runstream/` — Redis Streams XADD/XRANGE/XREAD replay + live cursor
- `internal/runrecorder/` — stamps `events_transport` on new run rows  
- `internal/cache/` — rueidis-backed RedisStreamer adapter
- `internal/config/` — `RUN_EVENTS_MODE` parsing (dual/streams/pubsub)
- `/health/live`, `/health/ready`, `/metrics` endpoints (both bridge replicas, behind `them-go-health` Traefik router at priority 120)

**Not yet implemented — Python bridge owns these:**
- WS upgrade handler: Go binary has handler code but no active Traefik route; Python `them-ws` router (priority 100) handles all `/ws/*` traffic
- SSE stream handler: same — no active Traefik route to Go; Python handles `/sse/*`
- Go Temporal worker: all orchestration executes in Python `them-worker`
- Admin API, auth service, app/EP management: all Python

See `docs/STATUS.md` for current Playground validation state, blockers, and FK decision status.

---

## Resolved Decisions

Previously open questions — all closed:

1. **Tenant boundary** — Application is the tenant boundary (see `06-domain-model.md` §6)
2. **Temporal hosting** — self-hosted on VPS for development; Temporal Cloud path documented for production (see `11-component-diagram.md` §4)
3. **AppOrchestrator vs Orchestrator resolution** — AppOrchestrator-first by application_id+name, template fallback (see `06-domain-model.md` §5)
4. **Token revocation multi-pod** — Redis pub/sub `them:token:revoked` broadcast, L1 invalidated in <1s (see `09-domain-events.md` §5)
5. **`ready` bootstrap race** — subscribe to context channel BEFORE calling temporal.StartWorkflow() (see `09-domain-events.md` §3)

## Remaining Open Questions (non-blocking)

1. **PostgreSQL hosting** — same VPS vs managed (RDS/Supabase). Does not affect implementation.
2. **JWT key rotation policy** — key rotation interval and multi-key validation window. Can be decided during auth package implementation.
3. **LiveKit hosting** — self-hosted vs LiveKit Cloud. Does not block foundation or auth phases.
4. **Agent card schema version** — backward compatibility policy for A2A protocol upgrades. Relevant only when agent adapter is implemented.
5. **Prompt template versioning** — git history is sufficient for now; DB versioning is a future enhancement.
