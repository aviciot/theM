# Documentation Index — the-M
# Last updated: 2026-08-21

One line per doc. Read this first, then open only what you need.
**Code beats docs.** If they diverge, fix the doc — stale docs are a bug.

---

## Session start

| File | Read when |
|---|---|
| `CLAUDE.md` | Every session — always read first |
| `docs/STATUS.md` | Start of session — current containers, migration state, known blockers |
| `docs/architecture-v2/CURRENT.md` | Start of session — current HEAD, next task, hard constraints |

---

## System reference

| File | Subject | Update when |
|---|---|---|
| `docs/ARCHITECTURE.md` | Current Go/Python hybrid architecture, Traefik routing, application model | Any flow or component changes |
| `docs/AUTH.md` | Auth service contract, JWT claims, AdminTenantMiddleware, machine tokens | Auth flow changes |
| `docs/SCHEMA.md` | All `them.*` tables — columns, FKs, rationale | DB table or column changes |
| `docs/REDIS.md` | Every Redis key pattern, TTL, owner, pub/sub channels | Redis key added or renamed |
| `docs/A2A_REFERENCE.md` | A2A SDK v1.1.0 — Part types, AgentCard/Skill fields, wire format | A2A SDK version change |
| `docs/A2A_AGENTS.md` | A2A test agents — start/stop, DB enable, cache bust, test commands | A2A agent changes |
| `docs/LESSONS.md` | Past bugs and non-obvious fixes — append only | Any bug fix or unexpected behavior |

---

## Migration tracking (temporary — remove when Python is gone)

| File | Subject |
|---|---|
| `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md` | Live route ownership: Go vs Python per endpoint |
| `docs/architecture-v2/implementation-status.md` | Go package inventory, test counts, route map |

---

## Architecture decisions (permanent)

| File | Decision |
|---|---|
| `docs/architecture-v2/adr-001-canonical-run-id.md` | Canonical run ID strategy |
| `docs/architecture-v2/adr-002-reconciler-status-mapping.md` | Reconciler status mapping |
| `docs/architecture-v2/adr-003-redis-streams-event-delivery.md` | Redis streams for event delivery |
| `docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md` | Component registry model (Wave 9 foundation) |

---

## Operational runbooks

| File | When to read |
|---|---|
| `docs/architecture-v2/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md` | Docker, deployment, container recreation |
| `go/TEST_INDEX.md` | Before running or writing Go tests |

---

## Test runner

```bash
# Go tests
cd go && go test ./...
```

---

## What lives where (quick lookup)

| Question | Read |
|---|---|
| What's running right now? | `docs/STATUS.md` |
| What's the next task? | `docs/architecture-v2/CURRENT.md` |
| How does the LLM agentic loop work? | `docs/ARCHITECTURE.md` |
| How does auth work? | `docs/AUTH.md` |
| What columns does `them.agents` have? | `docs/SCHEMA.md` |
| What Redis TTL does `them:token:*` have? | `docs/REDIS.md` |
| Which routes does Go own? | `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md` |
| What burned us before? | `docs/LESSONS.md` |
| Historical plans and reports? | `docs/architecture-v2/archive/` |
