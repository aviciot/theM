# App Canvas — Configuration Completeness Plan
# Status: PLANNING — no code changes yet. Written 2026-09-28 after a live investigation
# found File Guard/PII config is invisible to export/import, plus a real secret-leak bug
# (fixed separately, see go/TEST_INDEX.md S1-178) in the unrelated "Deploy to Tenant" feature.
# Owner: platform

---

## Why this doc exists

The user asked a direct question: *"if we enable File Guard on the canvas, does exporting the
app include that?"* The answer today is **no** — and investigating why surfaced a general
problem, not a one-off bug: **there is no standing rule that everything configurable on the
canvas must be exportable.** Every new canvas feature has been free to invent its own storage
(own DB table, own save endpoint) with no requirement to also make it travel with the app.

This doc sets that rule going forward, reconciles it with the **three existing, overlapping
pieces of infrastructure** that already touch parts of this problem (so nothing new gets built
that duplicates or contradicts them), and lays out a phased plan to close the current gap.

**Primary goal, in the user's own words:** *"1:1, 100% of the app flow, excluding API tokens
etc."* — used for moving an app between teams and full app backup/restore.

---

## The three existing pieces already in play — read this before touching anything

### 1. Canvas JSON export/import (frontend-only) — `docs/APP_CANVAS_EXPORT_IMPORT_PLAN.md`

Built 2026-09-22, still live (`CanvasBuilderView.tsx`'s `handleExport`/`handleImportJSON`).
Downloads/uploads the `AppDefinitionDoc` JSON (`schema_version`, `components`, `entry_points`,
`connections`) — purely client-side, no backend call. **Explicitly, deliberately out of scope
by design:** Guards, MCP credentials, Temporal config — anything not already inside that JSON
shape. This was the right call for what it was building (a quick canvas-only convenience,
mirroring the agent builder), but it is not, and was never meant to be, the full app
export/import this doc is now planning.

Key fact this feature already proved, which the rest of this plan relies on: **components
resolve by `(kind, namespace, name, version)` — a portable ref — not by DB UUID.**
`definition_id` is explicitly ignored server-side at Validate/Publish
(`go/internal/registry/resolver.go`'s `ResolveForPublish`, third arg always `""`). This is why
the canvas JSON alone is already cross-tenant portable with zero UUID remapping — the model to
extend, not replace.

### 2. "Deploy to Tenant" (DB-level clone) — `go/internal/admin/dal/app_deploy.go`

A backend feature that clones an app's *database rows* (not just canvas JSON) into another
tenant, with real UUID remapping for the two ID families that need it (`application_id`,
`agent_id`+dependents — confirmed shallow, not deep, in
`docs/APP_EXPORT_IMPORT_INVESTIGATION.md` §3). This is the mechanism that actually has a path to
including Guards/MCP bindings/etc., because it already operates at the DB level, not the JSON
level. **Currently also incomplete** — doesn't copy `middleware_wirings`, `app_agent_bindings`,
`app_mcp_credentials`, `app_flow_llm_overrides`, `app_temporal_config`, `app_debug_config`, or
`applications.canvas` (full gap list: `docs/APP_EXPORT_IMPORT_INVESTIGATION.md` §4).

**A real secret-leak bug was found and fixed in this feature already** (2026-09-28,
`go/TEST_INDEX.md` S1-178): `app_params` was copied verbatim, including inline encrypted secret
entries. Fixed, tested, deployed. This is the feature this plan recommends extending.

### 3. Guard config storage — `them.middleware_wirings`

Lives entirely outside both of the above. Read live, on every single node execution — in both
Debug Mode and production — via `internal/middleware/gate.go`'s `resolveSecCfg`/`loadWiringCfg`
(confirmed live 2026-09-28: no separate debug/prod code path exists). This is the "instant
checkbox toggle" behavior the user explicitly wants to keep for Debug Mode.

---

## The rule, going forward

**Every setting a user can configure by clicking around the App Canvas UI must be one of:**

**(a) Inside the app's `AppDefinitionDoc` JSON** (`components[]`/`entry_points[]`/
`connections[]`, or a new top-level key if genuinely node/edge-scoped data that doesn't fit the
existing shape) — automatically covered by canvas export/import (#1 above) and by "Deploy to
Tenant" once it also ships the JSON, with zero extra per-feature code.

**(b) In its own `application_id`-scoped DB table, registered in one place** — see "Option C"
below. Covered by "Deploy to Tenant" (#2) once that table is added to its copy list — a
one-line addition, not bespoke SQL each time.

**Never (c): a feature that lives outside the app's `application_id` scope entirely**, or that
has no defined path into either (a) or (b). This is the actual failure mode that caused the
current gap — Guards were built correctly as (b), but "Deploy to Tenant" was never updated to
know about it. The fix is process, not just code: **closing this gap is a checklist item on any
PR that adds a new `application_id`-scoped table**, and it's linked from `CLAUDE.md`'s existing
"App Export/Import Must Be Complete" rule (added 2026-09-28) so it isn't forgotten again.

### Choosing between (a) and (b) for a new feature

Use **(a) — inside the JSON** when the setting:
- has no reason to change independently of the rest of the canvas (it's part of "what this app
  does"), and
- is fine being subject to the same draft → publish timing as everything else in the JSON today.

Use **(b) — its own table** when the setting:
- must be toggleable instantly, without a publish step (Guards' actual, deliberate requirement —
  confirmed this is genuinely how they work today, in both debug and prod), or
- has its own independent lifecycle (e.g. needs versioning, or is queried across apps, or is
  large/binary), or
- has a real operational reason to be edited live in production without a redeploy.

**Do not choose (b) by default just because it's easier to bolt on.** That default is exactly
how Guards ended up invisible to export — the table was the right call for Guards specifically
(instant-toggle is a real, confirmed requirement), but it must be a deliberate choice each time,
checked against this list, not the path of least resistance.

---

## Debug-instant vs. publish-gated — a separate, secondary design axis

This is NOT the reason for the (a)/(b) rule above — export/import completeness is the primary
goal and applies regardless of this. But it's a related question every new feature should also
answer, because it affects user experience and was the source of real confusion this session:

**Confirmed live 2026-09-28:** today, production and Debug Mode read Guard config through the
literal same code path — a live DB query, every run, no publish step, for both. There is no
existing "draft vs. published" split for anything outside the canvas JSON itself. The canvas
JSON *does* have this split (draft autosave vs. `active_definition_id`'s published snapshot),
but only for JSON-resident settings — not for anything in its own table.

**The user's stated preference, confirmed 2026-09-28:** production changes should require an
explicit Publish (safety — no live traffic silently changes behavior mid-flight), but Debug Mode
should keep working instantly (fast iteration while building/testing).

**This is real, wanted, future work — but it is NOT required to fix export/import,** and doing
it changes runtime behavior, not just storage. Tracked here as **Phase 3 (optional, separate
decision point)** below so it doesn't get silently bundled into the export/import fix.

---

## Phased plan

### Phase 0 — Fix the immediate secret-leak bug

**DONE 2026-09-28.** `DeployApplication`'s `app_params` secret leak — see `go/TEST_INDEX.md`
S1-178. Unblocks the rest of this plan; no dependency on anything below.

### Phase 1 — Close the "Deploy to Tenant" coverage gap (Option C: table registry, not bespoke code)

**DONE 2026-09-28.** All 6 tables below now copy correctly, plus `applications.canvas`.
`appScopedConfigTables` (`go/internal/admin/dal/app_deploy.go`) is the registry — see
`go/TEST_INDEX.md` S1-179 for the full test list and 2 real bugs found/fixed while building it
(an unused SQL parameter, and a `t.Cleanup` LIFO-ordering bug that leaked test fixture rows on
every run). `go build`/`go vet`/`go test ./...` clean; integration tests pass against real
Postgres, re-run 3x with a leftover-row check to confirm the fixture cleanup actually holds.

Extend `app_deploy.go`'s CTE (or a follow-up pass after it, same transaction) to also clone:
`middleware_wirings` (the confirmed, concrete gap that started this investigation),
`app_agent_bindings` (minus `credential_bindings`), `app_mcp_credentials` (binding only, minus
`credential_encrypted`), `app_flow_llm_overrides`, `app_temporal_config`, `app_debug_config`,
`applications.canvas`.

Structure this as a small, explicit **list of `application_id`-scoped tables to copy** (a Go
slice/struct, not one growing raw-SQL CTE) — adding a future table means adding one entry with
its own ID-remap rule (most need none beyond `application_id` itself, per
`docs/APP_EXPORT_IMPORT_INVESTIGATION.md` §3), not hand-writing new CTE clauses each time. This
is "Option C" from the earlier discussion — most of the simplicity of a quick bolt-on fix,
without the "someone forgot to add the new feature's table" risk repeating.

Decision already made (2026-09-28): export captures the **active** app version only, not full
draft/revision history — matches what "Deploy to Tenant" already does.

### Phase 2 — Turn "Deploy to Tenant" into real export/import (file-based, not just tenant-to-tenant)

**Design note, written before implementation (2026-09-28) — the "thin wrapper" framing above
turned out to be wrong once actually investigated; this replaces it.**

**The real problem:** `DeployApplication`'s CTE is genuinely DB-to-DB — one SQL statement reads
directly from a live source app row and writes directly into a live target tenant in the same
breath (`app_deploy.go:297`'s `WITH src AS (SELECT ... FROM them.applications WHERE id = $1)`).
A file sits in between two separate operations (export now, import maybe weeks later, maybe in a
different environment with no network path back to the source) — there is no "live source app"
to `SELECT ... FROM` at import time. This is not a thin wrapper; it's genuinely two new code
paths: **export** (DB rows → one Go value → JSON file) and **import** (JSON file → DB inserts),
sharing the *shape* of what travels but not the SQL itself.

**Avoiding ~10 new hand-written Go structs — use `to_jsonb(row)`, not per-table structs.**
`dal.Application`/`dal.EntryPoint` (the existing summary types returned by `DeployApplication`
today) are deliberately incomplete views — missing `system_prompt`, `mcp_servers`, every guard
config field, etc. — built for the admin UI's summary response, not for a byte-perfect
round-trip. Building a second, complete struct per table (applications,
application_definitions, app_orchestrators, entry_points, plus all 6 tables from Phase 1, plus
agents/component_definitions/agent_definitions/agent_runtime_specs for canvas agents) would be
a lot of new, narrow, easy-to-drift-from-the-schema code. Confirmed live: Postgres'
`to_jsonb(row)` (or `row_to_json`) can serialize any table's row to a JSON object keyed by
column name with zero Go-side struct definition — `SELECT to_jsonb(t) FROM (SELECT * FROM
them.<table> WHERE application_id = $1) t`. **Export becomes: run one `to_jsonb`-shaped SELECT
per table in the registry (reusing `appScopedConfigTables` from Phase 1, extended with a
read-only SELECT variant), collect the results into one JSON envelope.** No new struct per
table — the JSON shape IS the table's column shape, which also means a future column added to
any of these tables is automatically included in export with zero code change (only a genuinely
new *table* needs the Phase 1 registry entry).

**Secret redaction must happen in the export SELECT, not as a separate JSON-filtering pass.**
Same reasoning as Phase 0/1's fixes — a generic `to_jsonb(row)` would include
`credential_bindings`/`credential_encrypted`/`app_params`'s secret entries verbatim, since it has
no concept of "this column is a secret." Each table's export SELECT must explicitly project
secret columns as `NULL`/filtered, mirroring exactly what Phase 1's copy SQL already does
(`NULL` for `credential_bindings`/`credential_encrypted`, the `jsonb_object_agg`/`jsonb_each`
filter for `app_params`) — this is why the registry needs its OWN export-SELECT string per
table, not a blind `SELECT *`.

**Agents travel differently from the other 6 tables — bundled by value, not by reference.**
`CopyAgentsForDeploy` (today, DB-to-DB) matches an agent in the target tenant by
`(kind, namespace, name, version)` and only copies it if genuinely absent or conflict-free. For
a *file* export, there is no live target tenant to check against at export time — the file must
carry each referenced agent's full row (component_definitions + agents +, for canvas agents,
agent_definitions + agent_runtime_specs) inline, and the conflict check moves to **import time**
instead, against whatever tenant the file is being imported into. Same match-by-identity logic
as today, just relocated from "compare two live tenants" to "compare the file's agent block
against the importing tenant."

**Import target: always a NEW application in an EXISTING tenant** (not "create a new tenant" —
tenant creation/selection is a separate, existing admin flow) — matches "move between teams"
(pick the destination team's tenant, import into it) and "restore from backup" (re-import into
the same tenant the backup came from, or a different one for disaster recovery) equally. Reuses
exactly the same new-UUID-generation + `agentIDMap`-style remap Phase 1 already proved correct —
import is structurally "DeployApplication, but the `src` CTE's source is a JSON file's values
bound as query parameters, not a live `SELECT ... FROM them.applications`."

**File shape (envelope):**
```json
{
  "export_version": 1,
  "exported_at": "2026-09-28T12:00:00Z",
  "application": { /* to_jsonb of the applications row, secrets already stripped */ },
  "application_definition": { /* to_jsonb of the active application_definitions row */ },
  "entry_points": [ /* to_jsonb per row */ ],
  "app_orchestrators": [ /* to_jsonb per row, api_key_encrypted columns NULL */ ],
  "scoped_config": {
    "middleware_wirings": [ /* ... */ ],
    "app_agent_bindings": [ /* ..., credential_bindings NULL */ ],
    "app_mcp_credentials": [ /* ..., credential_encrypted NULL */ ],
    "app_flow_llm_overrides": [ /* ... */ ],
    "app_temporal_config": [ /* ... */ ],
    "app_debug_config": [ /* ... */ ]
  },
  "agents": [ /* one entry per referenced agent: component_definition + agent + optional canvas deps */ ]
}
```
`export_version` is a real, checked field from day one (not deferred) — a future schema change to
the envelope must bump this and either migrate or reject an old file at import time, not silently
mis-map old fields into new columns.

**Implementation shape (once approved):**
1. `ExportApplication(ctx, appID) (ExportedApp, error)` — new DAL function/file
   (`app_export.go`), builds the envelope above via one `to_jsonb` SELECT per table (reusing
   Phase 1's `appScopedConfigTables` list, extended with an `exportSQL` field alongside
   `copySQL`) plus the agents block (adapting `CopyAgentsForDeploy`'s existing fetch queries,
   without the target-tenant-exists check).
2. `ImportApplication(ctx, envelope ExportedApp, targetTenantID string) (Application, error)` —
   new DAL function, same file. Structurally mirrors `DeployApplication` + `CopyAgentsForDeploy`
   combined, but sourcing every INSERT's values from the decoded envelope struct instead of a
   `SELECT ... FROM them.applications WHERE id = $1`. Same `agentIDMap` remap logic, same
   conflict handling (`ErrDeployConflict`) for a same-identity/different-content agent already
   present in the target tenant.
3. Two new HTTP routes: `GET /admin/applications/{id}/export` (streams the JSON file,
   `Content-Disposition: attachment`) and `POST /admin/applications/import?target_tenant_id=...`
   (multipart file upload or raw JSON body — decide at implementation time based on what's
   simpler for the frontend to wire up).
4. Frontend: an Export button next to (or replacing/subsuming) the existing canvas-only export
   (`docs/APP_CANVAS_EXPORT_IMPORT_PLAN.md`'s `handleExport`) — **decide whether the canvas-only
   export stays as a separate, smaller "just the canvas JSON" convenience, or is retired in
   favor of this strictly-more-complete one.** Leaning toward keeping both: the canvas-only one
   is useful for quick within-tenant canvas backup/sharing without touching Guards/MCP/etc.,
   while this new one is the "real" 1:1 app export the user asked for. Flag for a decision at
   implementation time, not assumed here.

**Explicitly deferred to implementation time, not decided in this note:**
- Whether `POST .../import` creates the target application inside the same request/transaction
  as parsing the file, or is a two-step "validate file, show a preview/checklist, confirm import"
  flow (the existing deploy checklist — `LLMKeysRequired`/`MCPServers`/`AgentsConflict` — is the
  precedent; import likely wants the same "here's what needs attention" response shape).
- Exact wire format for `POST .../import`'s file upload (multipart vs. raw JSON body).

### Phase 3 — (Optional, separate decision) Debug-instant vs. publish-gated Guards

Only if/when explicitly requested: give production a frozen, publish-time snapshot of guard
config (new column or embedded in `application_definitions.definition`), while Debug Mode keeps
reading `middleware_wirings` live as it does today. This is new runtime behavior, not a storage
migration — `resolveSecCfg`/`loadWiringCfg` would need a debug/production branch that doesn't
exist today. **Do not start this without a fresh confirmation conversation** — it's a genuine
scope expansion beyond export/import, and the "instant toggle" behavior it would change is
currently working as designed for both paths.

---

## What NOT to do (ruled out, with reasons, so it doesn't get re-proposed)

- **Do not move Guards into the canvas JSON ("Option B").** Investigated and rejected
  2026-09-28: Guards' instant-toggle behavior (no publish needed) is a real, confirmed
  requirement for both debug and production today. Moving Guards into the JSON would make them
  inherit the JSON's draft/publish timing, breaking instant toggling — a genuine regression, not
  a wash. It also doesn't fully solve the general problem, since future features with a real
  reason to need their own table would hit the same gap again regardless.
- **Do not build a second, parallel export mechanism.** Three pieces already exist (above); the
  plan is to complete and connect them, not add a fourth.
- **Do not attempt UUID/logical-ID schema refactor.** Investigated and rejected — the rewrite
  chain is shallow (two ID families), per `docs/APP_EXPORT_IMPORT_INVESTIGATION.md` §3/§6. Not
  justified by the stated use cases (one-time move, backup/restore).
