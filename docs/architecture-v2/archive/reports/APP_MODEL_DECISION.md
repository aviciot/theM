# App Model Decision: Multi-Entry-Point vs Single-Entry-Point
# the-M Platform — Architecture Decision Record
# Date: 2026-07-14

---

## Background

The-M started with a simple model: one app = one entry point. An app row in the database
had a slug, an orchestrator, an entry point type (WebSocket, SSE, WebRTC), and access
settings. The consumer endpoint `/apps/{slug}/ws` resolved by slug directly — clean, fast,
unambiguous.

As the platform evolved, users wanted to expose the same orchestrator through multiple
channels simultaneously: a WebSocket endpoint for a chat UI, an SSE stream for a backend
client, a WebRTC voice room for voice interaction. These are all the same "app" logically
— same agents, same system prompt, same orchestrator — but accessed differently.

To support this without a schema change, a shortcut was taken: group multiple `applications`
rows by their shared `orchestrator_id` and visually present them as one app on the canvas.
Each entry point was still its own DB row; the canvas just drew them together.

This is the model currently in production. It works for the simple case. For multi-entry-
point use it has confirmed structural problems, documented below.

---

## Opus Investigation — What the Code Actually Does

An Opus architectural review was run against the live codebase
(`frontend/src/app/admin/applications/page.tsx`, `app/routers/admin_applications.py`,
`app/models.py`, `db/001_schema.sql`). Findings are confirmed from code, not hypothetical.

### The Current DB Model

```sql
them.applications (
  id UUID PRIMARY KEY,
  name TEXT,
  slug TEXT UNIQUE,              -- consumer key, clean
  entry_point_type TEXT,         -- one type per row
  orchestrator_id UUID,          -- grouping hack: siblings share this
  access_policy JSONB,
  conversation_token_limit INT,
  presentation JSONB,            -- exists but never written
  enabled BOOLEAN
)
```

One row = one entry point. Multi-EP "apps" are multiple rows sharing `orchestrator_id`.
The canvas groups them visually but there is no explicit parent-app entity.

### Confirmed Bugs

**A1 — Wrong grouping key (High — data corruption risk)**
`buildNodesFromApp` pulls in every app sharing `orchestrator_id` as siblings
(`page.tsx:469`). Two unrelated apps on the same orchestrator merge onto one canvas.
Saving either one rewrites both. There is no grouping key that means "these belong
together" — only "these happen to share an orchestrator."

**A2 — Layout never persisted (Medium)**
`presentation` JSONB column exists in the schema and the API accepts it
(`admin_applications.py:37`) but `handleSave` never sends it (`page.tsx:2317`).
Node positions are recalculated by Dagre on every open. Any manual arrangement is lost.

**A3 — `slugLocked` is a single boolean for the whole builder (Low)**
With multiple EP nodes, auto-slug from the app name only applies to the first EP node
(`page.tsx:1881`). Additional EP nodes must be slugged manually or save is blocked.

**A4 — Delete is live, not transactional (High)**
Clicking ✕ on an EP node immediately fires `deleteApplication` against the DB
(`page.tsx:1883`). This is outside the Save transaction — delete then cancel is
impossible. Errors are swallowed with `.catch(() => {})`.

**A5 — Chain validation checks one EP only (Medium)**
`analyzeChain` picks one representative EP and reports readiness for it (`page.tsx:1726`).
Deploy proceeds if that one EP is valid. Any additional EP that is wired but missing a
slug is silently dropped from the save loop with no warning to the user.

**A6 — New dragged EP gets name "WebSocket" (Low)**
A freshly dragged entry point node has `label` = the type title from the sidebar
("WebSocket", "SSE", "WebRTC"). If the user doesn't rename it, it's saved to DB with
`name = "WebSocket"`.

**D6 — Save is non-atomic (Medium)**
The save loop iterates EP nodes sequentially, upserts each independently. If the second
EP fails (e.g. slug collision), the first is already committed. Partial deploys with no
rollback are possible.

### The Data Model Question: N Rows vs JSON Array vs Hybrid

Three options were evaluated:

**Option 1 — Current (N rows, group by orchestrator_id)**
- Slug uniqueness: ✅ DB UNIQUE constraint, O(1) consumer lookup
- Per-EP settings: ✅ natural per-row
- Grouping: ❌ wrong key, causes A1
- Save atomicity: ❌ sequential independent writes
- Layout: ❌ no home for canvas state

**Option 2 — Single row, `entry_points[]` JSON array**
- Slug uniqueness: ❌ cannot enforce UNIQUE across array elements at DB layer
- Consumer lookup: ❌ becomes JSONB containment query, needs GIN index, still no uniqueness guarantee
- Save atomicity: ✅ one write
- Verdict: rejected — sacrifices the model's only genuinely clean property

**Option 3 — Parent app + child entry_points table (Hybrid)**
```sql
them.applications  (id, name, orchestrator_id, presentation, enabled)
them.entry_points  (id, application_id, slug UNIQUE, entry_point_type, access_policy, token_limit)
```
- Slug uniqueness: ✅ preserved, DB-enforced on `entry_points.slug`
- Consumer lookup: ✅ `WHERE slug = ?` on `entry_points`, O(1)
- Grouping: ✅ explicit `application_id` foreign key
- Save atomicity: ✅ one PATCH, server diffs, one transaction
- Layout: ✅ `presentation` on parent app row
- Delete: ✅ cascade on `application_id`
- Verdict: correct model

---

## The Case for Single Entry Point Per App

Before committing to multi-EP, the counter-argument deserves a fair hearing.

### Argument 1 — Simplicity of the consumer contract

Right now `/apps/{slug}` is a complete, self-contained address. A slug IS an app.
External clients (Telegram bot, debator service, CI pipeline) hardcode a slug and get
exactly one behavior — one type, one access policy, one token limit. Nothing to
disambiguate.

With multi-EP, a slug still resolves to one entry point — that's preserved. But the
mental model for the builder user becomes "I'm managing an app that has entry points"
rather than "I'm managing an app." That's a small but real increase in conceptual load.

### Argument 2 — Operational isolation

If the WebSocket entry point of an app is misbehaving, you want to disable it without
touching the WebRTC voice room. With single-EP rows, `enabled` is per-row — you flip
one switch. With a parent app model, `enabled` at the app level takes down all entry
points. You'd need `enabled` at the entry_point level too, which is fine to build but
adds a dimension to the UI.

### Argument 3 — Deployment granularity

Today "deploy an app" means "enable this one row." Atomic, unambiguous. With multi-EP,
"deploy" means "enable the parent app, which activates all its entry points." If you want
to deploy the WebSocket entry point but not WebRTC yet (voice infra not ready), you need
per-EP enabled state. Doable, but more surface area.

### Argument 4 — Current users have one EP per app anyway

Looking at the DB, every app currently has exactly one entry point. The multi-EP need
is real but not yet used in practice. The single-EP model is working for 100% of actual
usage. Migrating to multi-EP carries migration risk for a feature nobody is using yet.

### Argument 5 — Simplicity of the builder UX

A single EP per app means the builder canvas is unambiguous: one entry node, one
orchestrator, N agents. There's nothing to figure out about which EP node's name/limit/
access policy you're editing. The right panel always refers to the one EP. No
"select each entry point node to edit its name" messaging needed.

---

## Bottom Line

### The honest assessment

Multi-entry-point is the right *product direction*. Real use cases exist: same
orchestrator, chat + voice + stream. The current implementation has real bugs that will
bite in production. The single-EP model is simpler but artificially limits what an app
can be.

However, the current multi-EP *implementation* is built on the wrong foundation and
should not be shipped as-is. The grouping-by-orchestrator hack (A1) is a data corruption
risk. The live-delete (A4) and non-atomic save (D6) are reliability risks.

### Recommendation

**Go app-based, with single entry point as the default, multi as opt-in.**

Concretely:
- Each app is a real entity (`applications` row with an `id`)
- Each app has exactly **one** entry point by default — the simple case stays simple
- The builder opens an app by `id`, not by orchestrator grouping
- Adding a second entry point is a deliberate action ("+ Add Entry Point") not the default canvas state
- Each entry point has its own slug, type, access policy, token limit, enabled flag
- Save is one atomic PATCH to the parent app (server diffs entry points in one transaction)
- The consumer endpoint `/apps/{slug}` queries `entry_points.slug` — unchanged from
  the external client's perspective

This model is:
- **Simple by default** — most apps will have one EP, builder is unambiguous
- **Correct when complex** — multi-EP is supported, transactional, properly grouped
- **Future-proof** — adding a new EP type is a new enum value, no structural change
- **Migration-safe** — existing single-EP app rows map 1:1 to the new model
  (`application` row + one `entry_points` row per existing `applications` row)

### What to build, in order

**Phase 1 — Stop the bleeding (no schema change, ~2h)**
1. Save `presentation` on every save/load — fixes layout loss (A2)
2. Move EP node delete to "mark for delete on save" — fixes live-delete risk (A4)
3. Make `analyzeChain` validate all EP nodes — fixes silent drop on deploy (A5)

**Phase 2 — Correct foundation (~1 day)**
1. Migration: `them.applications_v2` + `them.entry_points` schema
2. API: one PATCH per app, server diffs entry_points atomically
3. Frontend: builder routes to `/admin/applications/{id}`, opens by app id
4. Builder: "app" is the top-level entity, entry points are explicit children
5. Migration script: backfill existing rows (1:1, each existing row → one app + one EP)

**What stays the same forever**
- `/apps/{slug}` consumer endpoint — no change for external clients
- Per-slug access policy, token limit, type — stays per-entry-point
- Orchestrator/agent layer — untouched

### Clean? Future-proof?

Phase 1 alone: no. Grouping hack stays.
Phase 1 + Phase 2: yes. The model is honest, the contract is clear, and the
complexity budget is appropriate for a platform that will grow.
