---
title: Lessons — Export / Import & Deploy
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## `DeployApplication` was copying encrypted secrets into the target tenant via `app_params` (found 2026-09-28)

**Found while:** investigating a full app export/import feature (docs/APP_EXPORT_IMPORT_
INVESTIGATION.md), not while looking for a bug — surfaced as a side effect of inventorying every
table an app touches and checking what each one's export behavior should be.

**Root cause:** `them.applications.app_params` (`db/045_app_global_params.sql`) stores per-app
named parameters in one JSONB blob where secrets and non-secrets share the same shape: a secret
entry is `{"name": {"ct": "enc:...", "hint": "XXXX"}}`, a non-secret entry is a plain scalar
(`{"name": "value"}`). `go/internal/admin/dal/app_deploy.go`'s `DeployApplication` — the existing
"deploy this app to another tenant" feature — copied the whole `app_params` column verbatim into
the newly cloned application row, with no filtering. Every other secret-bearing column in this
same function (`llm_api_key_encrypted`, `transcription_api_key_encrypted`, etc.) was already
explicitly nulled out — `app_params` was simply missed, because unlike those columns its secret-
ness isn't visible from the column name or type; it's a runtime property of individual JSON keys
inside one JSONB column.

**Fix:** filter `app_params` inside the existing CTE itself — `jsonb_object_agg(key, value) FROM
jsonb_each(app_params) WHERE NOT (jsonb_typeof(value) = 'object' AND value ? 'ct')` — dropping any
entry shaped like a secret, keeping everything else untouched. Verified with 3 new integration
tests against real Postgres (`app_deploy_secrets_integration_test.go`) since a fake Querier cannot
execute Postgres jsonb functions and would have silently passed either way.

**Watch for:** a JSONB (or any semi-structured) column that mixes secret and non-secret values by
convention, not by a separate column/table, is invisible to the usual "grep for `_encrypted`/
`api_key` column names" secret audit. Any future copy/clone/export code path that touches a JSONB
config blob must be checked for this pattern specifically — the column name (`app_params`) gives
no hint that it can carry ciphertext. Worth a broader audit: are there other JSONB columns in this
schema with the same secret-and-non-secret-mixed-in-one-blob shape (`runtime_config`? `security_config`?)
that a future copy path could miss the same way?

---

---

## `to_jsonb`/import round-trip: a real array column is not the same as a JSONB column (found 2026-09-28)

**Found while:** building Phase 2's file-based app export/import
(docs/plans/app-canvas-config-completeness-plan.md) — `TestDAL_ExportImportApplication_RoundTrip`
failed on `ImportApplication`'s very first orchestrator insert: `column "edges" is of type
text[] but expression is of type jsonb`.

**Root cause:** `them.app_orchestrators.edges` is a real Postgres `text[]` column (default
`'{websocket}'::text[]`), not JSONB. `ExportApplication`'s `to_jsonb(row)`-based export SELECT
correctly turns it into a JSON array in the file (`to_jsonb` handles this transparently — a
Postgres array becomes a JSON array with no special-casing needed on the export side). The
`importOrchestratorRow` Go struct originally declared `Edges json.RawMessage`, and the import
INSERT bound it as `$28::jsonb` — which is wrong on the way IN, even though the way OUT (export)
worked by accident. `to_jsonb` erasing the distinction between "real array column" and "JSONB
column" in the *output* JSON shape doesn't mean the two are interchangeable on the way back in;
the target column's real type still has to be respected.

**Fix:** changed the Go field to `Edges []string` (so `encoding/json` decodes the JSON array
into a real Go slice) and the INSERT parameter to `$28::text[]`, passing `o.Edges` directly
instead of `string(o.Edges)`.

**Watch for:** any future table added to the export/import registry that has an array column
(`text[]`, `uuid[]`, etc.) needs its import-side Go type to be a real slice (`[]string`,
`[]int`, ...) bound with the matching `::type[]` cast — not `json.RawMessage` bound as `::jsonb`
just because that's the default pattern used for the genuinely-JSONB columns elsewhere in the
same table. `to_jsonb` on export flattens this distinction away in the file's own shape; the
database schema on the import side does not.

---

---

## Two "Export"/"Import" features with the same name, different scope, caused real confusion (found 2026-09-28)

**What happened:** while building the new full-app export/import (docs/
APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md Phase 2), a much older, narrower canvas-only Export/Import
feature already existed in the App Canvas builder (`CanvasBuilderView.tsx`'s `handleExport`/
`handleImportJSON`, per `docs/_archive/APP_CANVAS_EXPORT_IMPORT_PLAN.md`, built 2026-09-22) — pure
frontend, canvas JSON only (nodes/edges), no guards/agents/cross-tenant support. Adding the new
Runtime-tab Export/Import next to it, without first checking whether the old one should be
removed, meant the user ended up looking at two buttons both labeled "Export"/"Import" with no
way to tell from the UI alone that they did completely different things.

**Root cause:** the earlier investigation (docs/_archive/APP_EXPORT_IMPORT_INVESTIGATION.md) inventoried
every *table* an app touches, but never checked what *frontend features* already claimed to
solve export/import — so the new work was designed and half-built before the collision was
noticed, by the user, not by the person building it.

**Fix:** removed the old feature entirely (not deprecated, not left "for canvas-only quick
sharing" — a real option considered and rejected, since the confusion of two same-named buttons
outweighs the narrower convenience). `CanvasExportImport.ts` deleted; `CanvasTopBar`'s two
single-purpose slots (`exportButton`/`importControls`) collapsed into one generic `extraActions`
slot, now used only for the unrelated "Debug this draft" button that happened to share the same
slot.

**Watch for:** before adding a new feature whose name overlaps an existing one ("export",
"import", "deploy", "publish" — anything that sounds like a generic verb), grep the frontend for
that name FIRST, not after building — `grep -rl "handleExport\|Export JSON"` would have surfaced
this in seconds, before any design or code was written. A feature-name collision is a UX bug
regardless of whether the underlying code is correct.

---
