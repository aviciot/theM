# App Canvas — Export / Import JSON
# Status: dangling-root gap FIXED (2026-09-22, backend). Export/import feature itself PLANNED, not started.
# Date: 2026-09-21

---

## Goal

Give the app canvas (`frontend/src/app/admin/applications/`) the same "Export JSON" /
"Import JSON" convenience the agent builder already has — download the current design as a
file, and load a file back into the canvas — without inventing new backend validation. The
backend already validates a definition correctly (see "Why this is safe" below); this feature
is a frontend convenience layer on top of the existing save/validate/publish contract.

---

## What already exists (verified this session, not assumed)

- **Agent builder has export/import today.** `useDefinitionLifecycle.ts`'s `handleExport` /
  `handleImportJSON` / `handleImportFileChange`, wired into `BuilderTopBar.tsx`. Pure
  client-side: export = `Blob` + synthetic `<a download>` click; import = `FileReader` +
  `JSON.parse` + a **shallow shape check** (`doc.agent_root` exists, `doc.skills` is an array)
  + `loadDefinitionDoc(doc)` into canvas state. No backend call during import — the user still
  clicks Save afterward. No dedicated backend export/import endpoint exists for either canvas;
  this is 100% a frontend feature riding on the existing create/update contract.
- **App canvas has no equivalent today.** No export/import buttons, no file handling anywhere
  in `CanvasBuilderView.tsx` / `DefinitionView.tsx`.
- **`AppDefinitionDoc` is already a complete, self-contained description of an app's canvas** —
  `schema_version`, `name`, `execution_backend`, `components[]`, `entry_points[]`,
  `connections[]`. This is exactly what `canvasToDoc`/`docToCanvas` (`CanvasHelpers.ts`)
  already convert to/from React Flow state, and exactly what Save/Publish already send/receive.
  Nothing new needs to be invented for the JSON shape — it's the existing shape.
- **Components resolve by `definition_ref` (kind+namespace+name+version), tenant-scoped —
  `definition_id` (UUID) is explicitly ignored server-side at Validate/Publish time.**
  Confirmed in `registry/resolver.go` and `admin/service/publish.go`: the third arg to
  `ResolveForPublish` (`definitionID`) is always passed as `""`. This is deliberate — the code
  comment calls these "stable portable refs." This is the single most important fact for
  import: **an exported app JSON is already portable across tenants/environments** as long as
  the target has same-named components. No UUID remapping is needed or done anywhere in the
  codebase (confirmed by the "Deploy to Tenant" cross-tenant clone feature, which explicitly
  leaves stale `definition_id`s in the JSON blob and relies on ref-based re-resolution).
- **Draft save has no registry validation at all** (`CreateDraft`/`UpdateDraft` only run the
  shallow structural check: JSON shape, secret-key leak guard, duplicate `instance_id` within
  `components`, non-empty `source`/`target`/`slug`/`protocol` strings). Cross-tenant/bogus
  `definition_ref`s save successfully with zero pushback. Registry resolution — the check that
  actually catches "this agent doesn't exist here" — only runs at Validate/Publish.

## Why this is safe (the load-bearing fact for a small implementation)

**Import does not need new validation.** The existing Validate flow already produces exactly
the errors an import needs to surface:

| Problem an import could introduce | Already caught by | Error code |
|---|---|---|
| Component references an agent/orchestrator/middleware that doesn't exist in this tenant | `validateDoc` → registry resolve | `component_not_found` |
| ...exists but disabled | `validateDoc` → registry resolve | `component_disabled` |
| ...exists but deprecated | `validateDoc` → registry resolve | `component_deprecated` |
| Duplicate `instance_id` (e.g. re-importing the same file twice into one draft) | `validateDoc` | `duplicate_instance_id` |
| Connection points at an unknown `instance_id` | `validateDoc` | `dangling_connection` |
| Entry point protocol not in the known set | `validateDoc` | `invalid_protocol` |
| (Temporal backend) router/fork/join/condition/llm graph-shape errors | `appflow.Validate` | `router_no_edges`, `condition_edge_count`, `llm_no_prompt`, etc. |
| Malformed/garbage JSON file | New: client-side `JSON.parse` try/catch (mirrors agent builder) | n/a (client-only) |

`CanvasBuilderView.tsx` already renders validation errors (banner + red node highlighting) —
this is the same UI an import-then-validate flow reuses unchanged.

**What is genuinely NOT caught anywhere today, and import must not silently ignore:**
- **Dangling `entry_points[].root`** — confirmed NOT checked in `validateDoc`. An EP whose
  `root` points at a nonexistent `instance_id` passes Validate silently and just leaves the
  entry point's orchestrator unset at publish time (no error, no signal). This is a **pre-existing
  gap**, not something import introduces — but import is exactly the workflow most likely to hit
  it (hand-edited or partially-corrupted JSON with a typo'd root reference). Decision needed:
  fix this gap as part of this work, or explicitly flag and defer it (see Open Questions).
- **MCP server references** — orchestrator `config.mcp_servers` is an opaque JSON blob, never
  validated against the target tenant's actual MCP servers, in either Save or Validate. An
  imported app referencing MCP servers that don't exist in the target tenant will pass Validate
  and Publish, then fail at runtime. Same class of gap as the existing "Deploy to Tenant"
  feature, which handles it by surfacing an explicit post-deploy checklist rather than blocking.

---

## Scope

**In scope:**
1. Export button — download the current canvas as `{app-slug}.json` (mirrors agent builder).
2. Import button — load a `.json` file into canvas state, client-side only, gated the same way
   the agent builder gates it (new/unsaved draft only — see Design decision #1).
3. Client-side shape validation on import (deeper than the agent builder's 2-field check — see
   Design decision #2), with a friendly error message on failure, not a raw JS exception.
4. After a successful import, the user still explicitly Saves, then Validates/Publishes through
   the existing flow — import never talks to the backend directly.
5. A short user-facing note (in the import success/error area) that cross-environment imports
   need Validate to confirm all referenced components exist in this tenant.

**Out of scope (explicitly, not silently):**
- Fixing the dangling-`entry_points[].root` validation gap — this is a pre-existing bug in
  `validateDoc`, unrelated to import specifically. Recommend a separate, small follow-up task:
  add a `root` existence check to `validateDoc` (same pattern as `dangling_connection`). Flagging
  it here because import makes it more likely to bite, not because import causes it.
- MCP server cross-tenant validation — same reasoning; a pre-existing gap shared with Deploy.
- Any new backend endpoint — this stays a pure frontend feature, matching the agent builder's
  existing precedent exactly. No `/export` or `/import` route.
- Remapping/rewriting `definition_id`/UUIDs on import — confirmed unnecessary; the codebase's own
  Deploy-to-Tenant feature doesn't do this either, and ref-based resolution makes it redundant.
- A generic "diff before import" / merge feature — import always replaces canvas state wholesale,
  same as the agent builder.

---

## Design decisions (with rationale, so they don't get re-litigated)

1. **Import is only available for a new/unsaved draft, not for editing an existing saved
   definition** — mirrors the agent builder's `activeView === 'agent' && !defId` gate exactly.
   Rationale: importing into an existing draft would silently discard in-progress canvas work
   with no undo path beyond the editor's own undo stack; scoping it to "new draft only" removes
   the ambiguity of "did this replace my saved definition or just the in-memory draft."
2. **Client-side shape validation is deeper than the agent builder's.** The agent builder only
   checks 2 top-level fields (`agent_root`, `skills` array) and then lets a malformed deeper
   shape throw an uncaught-but-caught raw `TypeError` with no friendly message. For the app
   canvas, validate at import time: `schema_version === 2`, `components`/`entry_points`/
   `connections` are all arrays, and each element has its required non-empty string fields
   (`instance_id`, `definition_ref.kind`/`name` for components; `instance_id`/`slug`/`protocol`
   for entry points; `source`/`target` for connections) — i.e., client-side pre-check of exactly
   the same shape rules `validateDefinition` enforces server-side, so obviously-broken JSON
   never even reaches `docToCanvas`. This is a real, deliberate improvement over the agent
   builder's precedent, not scope creep — the failure mode being prevented (raw JS exception
   surfaced as an "Import failed" message) is exactly what the agent builder does today and
   is worth not repeating.
3. **No new backend validation, no new backend endpoint.** Justified above under "Why this is
   safe" — the existing Validate flow already does the real work of catching cross-environment
   reference problems; duplicating that logic client-side would be redundant and could drift.
4. **Export always available regardless of draft/dirty state** — mirrors the agent builder
   exactly (no gating on `onExport`).
5. **A one-line hint shown after import**, something like *"Imported. Click Validate to confirm
   every agent/orchestrator/middleware this app references exists in this tenant."* — a small,
   honest nudge given the "saves fine, fails at Validate" behavior confirmed above. Not a modal,
   not a blocking confirmation — just visible text, same tone as the agent builder's existing
   error-message area.

---

## Implementation plan

### Frontend only

1. **`frontend/src/app/admin/applications/components/CanvasBuilderView.tsx`**
   - Add `importFileRef` (hidden `<input type="file" accept=".json,application/json">`).
   - Add `handleExport()`: build `AppDefinitionDoc` via existing `canvasToDoc(nodes, edges, ...)`
     (already used for Save), wrap in `Blob`, synthetic `<a download="{app.slug}.json">` click —
     identical mechanism to the agent builder's `handleExport`.
   - Add `handleImportJSON()` (click the hidden input) and `handleImportFileChange()`
     (`FileReader` → `JSON.parse` in a `try/catch` → shape-check per Design decision #2 → on
     success, call `docToCanvas(doc, componentDefs, {}, agentIconBySlug, mwVisualById)` and feed
     the result into `setNodes`/`setEdges`, mark dirty; on any failure, set a friendly error
     string, do not touch existing canvas state).
   - Gate the Import button the same way as the agent builder: only show when there's no
     `activeDef` yet (equivalent of agent builder's `!defId` — a fresh, unsaved app).
   - Add the one-line post-import hint (Design decision #5).
2. **No changes needed to `CanvasHelpers.ts`** — `canvasToDoc`/`docToCanvas` are reused exactly
   as they exist today; this is precisely why the research was worth doing first.
3. **No changes needed to `types.ts` / `apiTypes.ts`** — `AppDefinitionDoc` is already the
   complete shape.
4. **No backend changes.**

### Testing

- `tsc --noEmit` clean (project gate).
- Manual round-trip in the browser (this feature is impossible to meaningfully unit-test
  end-to-end without a browser — file download/upload, `Blob`, `FileReader` are all DOM APIs):
  1. Build a small app in the canvas (e.g. the "Sentiment Router" flow already proposed this
     session), Export it.
  2. Start a brand-new app, Import the exported file, confirm the canvas renders identically.
  3. Publish-test: import into a *different* app/tenant context lacking one referenced agent,
     confirm Validate surfaces `component_not_found` for that node (proves the "no new backend
     validation needed" claim rather than just asserting it).
  4. Negative: import a garbage/non-JSON file, confirm a friendly error, no crash.
  5. Negative: import valid JSON with wrong shape (e.g. missing `components`), confirm a
     friendly error, no crash.

---

## Open questions (for the user, not decided unilaterally)

1. ~~Should the dangling-`entry_points[].root` validation gap be fixed in the same piece of
   work, or filed separately?~~ **RESOLVED 2026-09-22 — fixed ahead of the export/import feature
   itself**, as its own small backend change (no backward-compat concern per explicit user
   instruction — existing apps just get validated correctly going forward). See "Dangling-root
   fix" below.
2. Is "new draft only" the right gate for Import, or should it also be offered for an existing
   draft (with a clear "this replaces your current canvas" warning)? The agent builder chose the
   stricter option; matching it is the safe default but not the only option.

---

## Dangling-root fix — COMPLETE (2026-09-22)

`go/internal/admin/service/publish.go`'s `validateDoc` — the entry_points loop now checks that
`ep.Root` (when non-empty) matches a known `instance_id` from the same document (components are
validated first in this function, so the ID set is fully populated by the time entry points are
checked). New error code `dangling_root`, same shape/pattern as the existing `dangling_connection`
check. No backward-compatibility handling — existing apps with a bad root will now correctly fail
Validate, per explicit user instruction to not special-case old data.

Tests: `TestValidateDefinition_DanglingRoot_ReturnsError` (bad root → error),
`TestValidateDefinition_ValidRoot_NoError` (correct root → no false positive) —
`go/internal/admin/service/definitions_publish_test.go`. `go test ./...` 0 failures (full suite);
`test_42_appflow_inline_nodes.py` 29/29 live after rebuild + force-recreate of `them-go-bridge`
(its own test fixtures all have valid roots, confirming the new check doesn't false-positive on
well-formed apps).
