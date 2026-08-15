# docs/architecture-v2/
# Last updated: 2026-08-15

Active Go migration documentation. One source of truth per subject.

---

## Active files

| File | Purpose |
|---|---|
| `CURRENT.md` | Current HEAD, deployment state, next task, blockers — update at session end |
| `implementation-status.md` | Go package inventory, test counts, route map |
| `REMAINING_ROUTE_OWNERSHIP_INVENTORY.md` | Live route ownership: Go vs Python per endpoint (temporary — remove when Python is gone) |
| `LOCAL_TEST_ENVIRONMENT_RUNBOOK.md` | Docker stack operation, DB init, container recreation |
| `schema-migrations.md` | DB migration ordering and approach |
| `REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md` | Component registry model — future Wave 9 architecture |
| `lessons-learned.md` | Go-specific bugs and non-obvious behaviors |

## ADRs (permanent)

| File | Decision |
|---|---|
| `adr-001-canonical-run-id.md` | Canonical run ID strategy |
| `adr-002-reconciler-status-mapping.md` | Reconciler status mapping |
| `adr-003-redis-streams-event-delivery.md` | Redis streams for event delivery |

## Archive

`archive/` contains completed plans, reports, and handover snapshots. Do not edit.
- `archive/migrations/` — completed migration plans
- `archive/reports/` — implementation reports and reviews
- `archive/handovers/` — old NEXT_SESSION_*.md files
- `archive/architecture-v1/` — pre-Go architecture documents

---

## Rules

1. Do not create new active plan/report documents here — completed work goes straight to archive.
2. Update CURRENT.md at session end, not a new NEXT_SESSION_*.md file.
3. ADRs are permanent — never archive them.
4. REMAINING_ROUTE_OWNERSHIP_INVENTORY.md disappears when Python migration is complete.
