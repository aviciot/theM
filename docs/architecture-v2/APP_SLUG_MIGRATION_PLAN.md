# App Slug Migration Plan
# the-M — URL structure overhaul: add app_slug to all endpoint paths
# Last updated: 2026-08-31

---

## Problem Statement

### Current state

The `them.applications` table has **no slug column** (it was dropped in migration `010`).
All URL identity lives on `entry_points.slug`, which is unique only per-tenant — not per-application.

Current URL shapes:

| Transport | URL |
|---|---|
| WebSocket | `GET /apps/{ep_slug}/ws` |
| SSE | `GET /apps/{ep_slug}/sse` |
| Voice | `POST /apps/{ep_slug}/voice/{action}` |
| A2A | `POST /a2a/{ep_slug}` |

### What this breaks

1. **URL collisions are possible.** Two different applications under the same tenant may not share an EP slug (unique constraint is per-tenant), but the constraint is global across all apps — so an operator can't name an EP "chat" on two different apps without a conflict. More importantly: the URL gives no indication of *which application* an EP belongs to. A client looking at `/a2a/chat` can't tell if it's the support bot or the sales bot.

2. **Agent card is wrong.** The A2A spec expects a per-agent card at `{agent_base_url}/.well-known/agent.json`. With `/a2a/{ep_slug}` as the base, the card should be at `/a2a/{ep_slug}/.well-known/agent.json`. We don't implement this — we only have a global card at `/.well-known/agent.json`.

3. **`New Application` name collision.** The UI hardcodes `"New Application"` as the draft name. Multiple draft apps get the same name. The DB has no `UNIQUE(name)` on `applications`. When we add `app_slug` (derived from name), two drafts will collide at slug generation time.

4. **WS/SSE use the same slug-only pattern.** `/apps/{ep_slug}/ws` has the same problem — which app does this EP belong to? The DB lookup happens to work (because ep_slug is unique per-tenant today), but the URL is opaque.

---

## Target URL Shape

Add `app_slug` as a path segment in every transport:

| Transport | New URL |
|---|---|
| WebSocket | `GET /apps/{app_slug}/{ep_slug}/ws` |
| SSE | `GET /apps/{app_slug}/{ep_slug}/sse` |
| Voice | `POST /apps/{app_slug}/{ep_slug}/voice/{action}` |
| A2A (RPC) | `POST /a2a/{app_slug}/{ep_slug}` |
| A2A card | `GET /a2a/{app_slug}/{ep_slug}/.well-known/agent.json` |

Benefits:
- URLs are self-describing — app + EP identity visible from the path
- EP slug uniqueness constraint can be relaxed to per-application (two apps can both have an EP named `chat`)
- Agent card URL is spec-compliant with no extra machinery
- Traefik rules simplify (see below)

---

## Change 1 — Add `slug` back to `applications`

### DB migration

```sql
-- migration: 030_application_slug.sql
ALTER TABLE them.applications
    ADD COLUMN IF NOT EXISTS slug TEXT;

-- Backfill: derive from name, lowercased, non-alphanum → hyphen, truncated to 48 chars
UPDATE them.applications
SET slug = LOWER(REGEXP_REPLACE(TRIM(name), '[^a-z0-9]+', '-', 'gi'))
WHERE slug IS NULL OR slug = '';

-- Ensure uniqueness per tenant
ALTER TABLE them.applications
    ADD CONSTRAINT applications_slug_check
        CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{0,47}$');

ALTER TABLE them.applications
    ADD CONSTRAINT uq_applications_tenant_slug
        UNIQUE (tenant_id, slug);

ALTER TABLE them.applications
    ALTER COLUMN slug SET NOT NULL;
```

**Backfill result for current data:**

| App name | Derived slug |
|---|---|
| McpAgents | mcpagents |
| VacationPlanner | vacationplanner |
| debator | debator |
| echo-sandbox | echo-sandbox |
| E2E Test App | e2e-test-app |
| doc-artifact-test | doc-artifact-test |
| health-monitor | health-monitor |
| location-reporter | location-reporter |
| New Application (×2) | **collision — see below** |

### Handling draft name collisions

The two `New Application` rows will collide on slug `new-application`. Resolution:

```sql
-- Deduplicate before applying UNIQUE constraint
UPDATE them.applications a
SET slug = 'new-application-' || SUBSTRING(a.id::text, 1, 8)
WHERE a.name = 'New Application'
  AND (
    SELECT COUNT(*) FROM them.applications b
    WHERE b.name = 'New Application' AND b.id <= a.id
  ) > 1;
```

Include this deduplication step in the migration before adding the UNIQUE constraint.

### EP slug uniqueness relaxation

Once `app_slug` is in the URL, the EP slug only needs to be unique **within an application**, not tenant-wide:

```sql
-- Drop existing tenant-scoped unique constraint
ALTER TABLE them.entry_points DROP CONSTRAINT IF EXISTS uq_entry_points_tenant_slug;

-- Add application-scoped unique constraint
ALTER TABLE them.entry_points
    ADD CONSTRAINT uq_entry_points_app_slug UNIQUE (application_id, slug);
```

---

## Change 2 — Go: epconfig query update

`go/internal/epconfig/pgx.go` currently resolves EP by `(tenant_id, ep_slug)`. With the new URL shape, both `app_slug` and `ep_slug` are available. The query can use either:

- **Option A:** Resolve by `(app_slug, ep_slug)` — more precise, no tenant_id needed from token
- **Option B:** Resolve by `(tenant_id, app_slug, ep_slug)` — belt-and-suspenders

Recommend **Option B**: keeps tenant boundary enforcement (a token from tenant A cannot hit tenant B's app even if they share slugs).

```sql
SELECT ep.id, ep.slug, ep.entry_point_type, ep.access_policy, ...
FROM them.entry_points ep
JOIN them.applications a ON a.id = ep.application_id
WHERE a.tenant_id = $1
  AND a.slug      = $2   -- new: app_slug from URL
  AND ep.slug     = $3   -- ep_slug from URL
```

The `AdmitRequest` struct gains an `AppSlug string` field. All three transports (WS, SSE, A2A) extract it from the URL and pass it through.

---

## Change 3 — Go: handler route updates

### A2A server (`go/internal/a2a/server.go`)

```go
// Old
r.Post("/a2a/{app_slug}", s.handleRPC)
r.Get("/.well-known/agent.json", s.handleAgentCard)

// New
r.Post("/a2a/{app_slug}/{ep_slug}", s.handleRPC)
r.Get("/a2a/{app_slug}/{ep_slug}/.well-known/agent.json", s.handleAgentCard)
```

`handleRPC` extracts both `app_slug` and `ep_slug` from the URL.
`handleAgentCard` becomes per-EP: returns a card with the correct URL for that specific EP (name from DB, capabilities from EP config).

Remove the global `/.well-known/agent.json` root card (or keep as a redirect to the agent card index — see Traefik section below).

### WS handler (`go/internal/ws/handler.go`)

```go
// Old: /apps/{slug}/ws (slug doubles as ep_slug)
r.Get("/{slug}/ws", ...)

// New: /apps/{app_slug}/{ep_slug}/ws
r.Get("/{app_slug}/{ep_slug}/ws", ...)
```

Both params extracted and passed to `AdmitRequest`.

### SSE handler (`go/internal/sse/handler.go`)

Same pattern as WS:
```go
r.Get("/{app_slug}/{ep_slug}/sse", ...)
r.Post("/{app_slug}/{ep_slug}/sse", ...)
```

### Voice handler (`go/internal/voice/handler.go`)

```go
// Old
r.Post("/{slug}/voice/chat", h.Chat)
r.Post("/{slug}/voice/tts", h.TTS)
// etc.

// New
r.Post("/{app_slug}/{ep_slug}/voice/chat", h.Chat)
r.Post("/{app_slug}/{ep_slug}/voice/tts", h.TTS)
// etc.
```

The voice handler also resolves EP config — it gains an `app_slug` parameter path.

---

## Change 4 — Traefik simplification

### Current rules (5 routers for /apps and /a2a)

```
them-go-a2a         PathPrefix('/a2a')                          priority 120
them-go-well-known  Path('/.well-known/agent.json')             priority 120
them-go-apps-ws     PathRegexp('^/apps/[^/]+/ws$')             priority 120
them-go-apps-sse    PathRegexp('^/apps/[^/]+/sse$')            priority 120
them-go-apps-voice  PathRegexp('^/apps/[^/]+/voice/')          priority 120
```

### New rules (3 routers — simpler)

With `app_slug` in the path, `PathPrefix` is sufficient for all cases:

```
them-go-a2a         PathPrefix('/a2a')       priority 120   # covers /a2a/{app}/{ep} and /.well-known nested
them-go-apps        PathPrefix('/apps')      priority 120   # covers WS, SSE, voice — one rule
```

The `them-go-well-known` root router **disappears** — the card is now under `/a2a/...` which is already covered by `them-go-a2a`.

The 3 `PathRegexp` rules for ws/sse/voice **collapse into one** `PathPrefix('/apps')`. Traefik doesn't need to distinguish WS/SSE/voice at the routing layer — the Go handler does that internally via the path suffix.

**Before: 5 routers with 3 regexps. After: 2 routers, 0 regexps.**

The only risk: `PathPrefix('/apps')` also catches unknown paths like `/apps/foo/bar/baz`. This is fine — the Go handler returns 404 for unrecognised patterns, and the frontend has no routes under `/apps`.

---

## Change 5 — UI: application name uniqueness

### Problem

`createApplication` is called with hardcoded `name: 'New Application'`. The DB has no `UNIQUE(name)` constraint. With the new `UNIQUE(tenant_id, slug)` on applications, two drafts with the same name will fail at slug derivation time.

### Fix

**Backend:** Add a `slug` field to `ApplicationIn` (the create/update request body). Derive slug server-side from name if not explicitly provided (same slugify logic as the DB migration). Return a `409 Conflict` if slug already taken, with a descriptive error body.

**Frontend — new application creation:**
1. On "New Application" click, generate a unique draft name: `"New Application <timestamp>"` or `"New Application <random-4>"` — ensures no collision even if multiple tabs are open simultaneously.
2. After the canvas is saved and the app has a real name, the slug is re-derived from the final name (user can also edit it manually in settings).
3. The EP builder shows the full URL preview: `https://<host>/apps/{app_slug}/{ep_slug}/ws` — so operators see the real URL before publishing.

**Slug edit rules:**
- Slug is editable only while no active sessions exist on any EP of that application.
- Changing slug is a rename — old URL stops working. Show a warning.
- Slug may be set explicitly (for vanity URLs); if left blank it is auto-derived from name.

---

## Change 6 — Runs table: entry_point_slug column

`them.runs` has `entry_point_slug TEXT` which currently stores only the EP slug. After the migration, this should ideally store the full path identity. Two options:

- **Option A:** Add `application_slug TEXT` column to `runs` — store both separately, join when displaying
- **Option B:** Keep `entry_point_slug` as-is — the `application_id` is already derivable from the run's orchestrator/EP chain

Recommend **Option A** for observability — run history should show `app/ep` identity without a join.

---

## Change 7 — Frontend: EP URL display

Every place the frontend shows a URL (EP builder, settings, share sheet in mobile) must be updated:

| Old | New |
|---|---|
| `/apps/{ep_slug}/ws` | `/apps/{app_slug}/{ep_slug}/ws` |
| `/apps/{ep_slug}/sse` | `/apps/{app_slug}/{ep_slug}/sse` |
| `/a2a/{ep_slug}` | `/a2a/{app_slug}/{ep_slug}` |

The `app_slug` comes from the application object returned by the admin API (new field after this migration).

---

## Migration Sequence (implementation order)

Do these in separate focused sessions, one subsystem per commit:

| Step | Task | Risk |
|---|---|---|
| 1 | DB migration `030_application_slug.sql` — add slug to applications, backfill, dedup drafts, add constraints | Low — additive only |
| 2 | Go: add `slug` to `ApplicationOut` in admin API + update `CreateApplication`/`UpdateApplication` to accept and return slug | Low |
| 3 | Go: update `epconfig` query to accept `app_slug` + `ep_slug`, update `AdmitRequest` | Medium |
| 4 | Go: update A2A server routes (new URL shape + per-EP card) | Medium |
| 5 | Go: update WS + SSE + voice handler routes | Medium |
| 6 | Traefik: replace 5 routers with 2, remove regexp rules | Low (after step 5) |
| 7 | Frontend: update URL generation + EP builder preview + app name uniqueness | Medium |
| 8 | DB: relax EP slug uniqueness from per-tenant to per-application | Low (after step 7 deployed) |
| 9 | Update `docs/SCHEMA.md`, `docs/ARCHITECTURE.md`, `ROUTING_VERIFICATION.md` | Low |

**Backward compatibility:** Steps 1–2 are additive and non-breaking. Steps 3–6 change the URL shape — old URLs stop working. Either do steps 3–7 atomically in one deploy, or run old and new routes in parallel for one release cycle (register both `/a2a/{ep_slug}` and `/a2a/{app_slug}/{ep_slug}` temporarily, remove old after clients migrate).

Given this is a dev/internal environment with one tenant and a handful of EPs, recommend **atomic cutover** — implement steps 3–7 in a single session, update the mobile client simultaneously.

---

## Current data impact

EPs that need new URLs after migration (currently live/enabled):

| App slug (derived) | EP slug | Old URL | New URL |
|---|---|---|---|
| mcpagents | a2a-1 | `/a2a/a2a-1` | `/a2a/mcpagents/a2a-1` |
| mcpagents | ep-a2a-1 | `/a2a/ep-a2a-1` | `/a2a/mcpagents/ep-a2a-1` |
| mcpagents | ep-sse-1 | `/apps/ep-sse-1/sse` | `/apps/mcpagents/ep-sse-1/sse` |
| mcpagents | ep-voice-1 | `/apps/ep-voice-1/voice/*` | `/apps/mcpagents/ep-voice-1/voice/*` |
| mcpagents | ep-websocket-1 | `/apps/ep-websocket-1/ws` | `/apps/mcpagents/ep-websocket-1/ws` |
| vacationplanner | ep-websocket-2 | `/apps/ep-websocket-2/ws` | `/apps/vacationplanner/ep-websocket-2/ws` |
| debator | a2a | `/a2a/a2a` | `/a2a/debator/a2a` |
| debator | my-ws | `/apps/my-ws/ws` | `/apps/debator/my-ws/ws` |
| echo-sandbox | a2a-2 | `/a2a/a2a-2` | `/a2a/echo-sandbox/a2a-2` |
| echo-sandbox | echo-sandbox-ws | `/apps/echo-sandbox-ws/ws` | `/apps/echo-sandbox/echo-sandbox-ws/ws` |
| echo-sandbox | echo-sandbox-sse | `/apps/echo-sandbox-sse/sse` | `/apps/echo-sandbox/echo-sandbox-sse/sse` |

Mobile client must update: `a2a-1` → `/a2a/mcpagents/a2a-1` (or whichever EP it uses).

---

## Open questions (decide before implementing)

1. **Slug editability:** Can an app slug be changed after creation? If yes, all existing client URLs break. Recommend: editable only if zero runs recorded against any EP of that app.
2. **Vanity slugs:** Allow operator to set slug independently of name? Recommend yes — keep name and slug as separate fields.
3. **`/sse/orchestrate/{app_slug}/{ep_slug}` path:** This legacy path already has two segments. It is unaffected by this migration (already correct shape). Confirm it stays.
4. **`/ws/orchestrate/{app_slug}/{ep_slug}` path:** Same — already two segments, no change needed.
5. **Runs table:** Add `application_slug` column (Option A) or leave it to joins (Option B)?
