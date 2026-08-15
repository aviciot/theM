# Documentation Consolidation Report
# Executed: 2026-07-25

---

## Summary

Consolidated `go/docs/architecture-v2/` (5 files) into `docs/architecture-v2/` (canonical location).
All unique content preserved. All conflicts resolved. Broken reference fixed. `go/docs/architecture-v2/` removed.

---

## Files Merged

### implementation-status.md
- **Action:** Replaced docs/ version with go/ version as authoritative baseline
- **go/ version was authoritative:** Phase 11c-C complete, 212 unit tests (docs/ was stale at 200, Phase 11b only)
- **Unique content preserved from docs/ version:** Architectural Findings Fixed table (full detail), build/test commands with integration test port map, Wave 5 route additions
- **Result:** Single unified file reflecting post-Wave-5 + Phase 11c-C state; admin/dal, admin/service, and transport packages added to package inventory

### runbook-reconciler.md
- **Action:** Merged both versions — go/ version as base (had activation checklist + results), docs/ version contributed unique sections
- **From go/ version (kept):** Controlled Write Activation checklist, actual activation results table (2026-07-20), env-var configuration table, rollback steps with compose YAML examples
- **From docs/ version (added):** NotFound investigation Scenarios A/B/C, Python-native run explanation with `ctx-{context_id}` details, simplified enabling checklist
- **Contradiction resolved:** docs/ version said "Set `DryRun = false` in the Config and redeploy"; go/ version correctly uses `RECONCILER_DRY_RUN` env var. **go/ version is correct** — the env var approach is the actual implementation.

### lessons-learned.md
- **Action:** Appended go/ content as a new section (zero overlap — completely different lessons)
- **docs/ version:** Phase 1-11a lessons (go.mod, JWT, session, event bus, domain types, orchestration, Temporal, gate, EP config, auth wiring, voice EP rejection, runstream reconnect)
- **go/ version (appended as L-01 through L-09):** gorilla/websocket close handshake, Windows CP1252 terminal, docker cp path mangling, soak schema mismatch, Temporal cold-start healthcheck, reconciler DryRun safe default, token cache invalidation by hash, Python/Go bool repr in tests, pgx.ErrNoRows in test doubles
- **Result:** Single file with all lessons; chronologically organized with platform/integration lessons clearly sectioned

---

## Files Moved (go/docs/ → docs/)

| Source | Destination | Notes |
|---|---|---|
| `go/docs/architecture-v2/adr-003-redis-streams-event-delivery.md` | `docs/architecture-v2/adr-003-redis-streams-event-delivery.md` | ADR-003 reference updated (rollout status table now includes "Complete" for phases A+B) |
| `go/docs/architecture-v2/phase-11c-design.md` | `docs/architecture-v2/phase-11c-design.md` | Added `Historical` status header; added references to ADR-003 and implementation-status |

---

## Files Archived

| File | Archived to | Reason |
|---|---|---|
| `NEXT_SESSION_CODE_RECOVERY_HANDOVER.md` | `archive/NEXT_SESSION_CODE_RECOVERY_HANDOVER.md` | Superseded by `NEXT_SESSION_BRIDGE_HANDOVER.md` (Wave 5 is the current state) |

Archive directory created: `docs/architecture-v2/archive/` with `README.md` explaining its purpose.

---

## Broken References Fixed

| Location | Was | Now | Note |
|---|---|---|---|
| `go/CLAUDE.md` line 189 | `docs/architecture-v2/06-domain-model.md` | `docs/architecture-v2/implementation-status.md` (Architectural Findings Fixed table) | `06-domain-model.md` does not exist in either location; domain boundary decision is inline in CLAUDE.md and in implementation-status.md |

---

## Directory Removed

`go/docs/architecture-v2/` — all 5 files removed via `git rm` after confirming
every unique file is preserved in `docs/architecture-v2/`.

The parent `go/docs/` directory becomes empty after this removal. Git does not
track empty directories; the directory disappears from the working tree.

---

## New Files Created

| File | Purpose |
|---|---|
| `docs/architecture-v2/README.md` | Directory index: active vs historical vs archived docs, update rules |
| `docs/architecture-v2/adr-003-redis-streams-event-delivery.md` | Moved from go/docs/ |
| `docs/architecture-v2/phase-11c-design.md` | Moved from go/docs/ |
| `docs/architecture-v2/archive/README.md` | Archive directory explanation |
| `docs/architecture-v2/archive/NEXT_SESSION_CODE_RECOVERY_HANDOVER.md` | Archived handover |

---

## References Checked

### go/CLAUDE.md references
All `docs/architecture-v2/` paths in `go/CLAUDE.md` verified:
- `docs/architecture-v2/implementation-status.md` — exists, updated ✓
- `docs/architecture-v2/lessons-learned.md` — exists, updated ✓
- `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` — exists ✓
- `docs/architecture-v2/06-domain-model.md` — **was broken; fixed** ✓

### CLAUDE.md references
- `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` — exists ✓
- `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` — does not yet exist (future handover target; correct — it will be created at next session end)
- `docs/architecture-v2/` as documentation target — canonical ✓

### Internal cross-references within docs/architecture-v2/
- `runbook-reconciler.md` → `adr-002-reconciler-status-mapping.md` — exists ✓
- `adr-003-redis-streams-event-delivery.md` → `phase-11c-design.md` — exists ✓
- `adr-003-redis-streams-event-delivery.md` → `docs/REDIS.md` — exists ✓
- `lessons-learned.md` → `schema-migrations.md` (MIG-002 reference) — exists ✓

### No remaining references to go/docs/architecture-v2/
Search confirmed: zero references to `go/docs/architecture-v2` in any tracked file.

---

## No Application Code Changed

- All changed files are under `docs/architecture-v2/`, `go/docs/architecture-v2/`, or `go/CLAUDE.md`
- `go/CLAUDE.md` change: one table cell text replacement (broken link → correct path) — no behavior change
- Zero files under `go/internal/`, `go/cmd/`, `app/`, `auth_service/`, `frontend/`, `agents/` were touched

---

## Conflicting Active Duplicates — None Remaining

| Pair | Resolution |
|---|---|
| `implementation-status.md` × 2 | Merged into one; go/ version was authoritative; docs/ deleted |
| `runbook-reconciler.md` × 2 | Merged into one canonical file in docs/; go/ deleted |
| `lessons-learned.md` × 2 | Appended into one canonical file in docs/; go/ deleted |
