# Task: Tenant-Scoped Entry Point URLs
# Status: READY TO START
# HEAD at time of writing: e4f721c

---

## Goal

Change all runtime entry point URLs from the current flat structure to a tenant-scoped structure:

| Protocol | Current | New |
|---|---|---|
| WebSocket | `/apps/{app_slug}/{ep_slug}/ws` | `/{tenant_slug}/apps/{app_slug}/{ep_slug}/ws` |
| SSE | `/apps/{app_slug}/{ep_slug}/sse` | `/{tenant_slug}/apps/{app_slug}/{ep_slug}/sse` |
| Voice | `/apps/{app_slug}/{ep_slug}/voice/*` | `/{tenant_slug}/apps/{app_slug}/{ep_slug}/voice/*` |
| A2A JSON-RPC | `/a2a/{app_slug}/{ep_slug}` | `/{tenant_slug}/a2a/{app_slug}/{ep_slug}` |
| A2A agent card | `/a2a/{app_slug}/{ep_slug}/.well-known/agent.json` | `/{tenant_slug}/a2a/{app_slug}/{ep_slug}/.well-known/agent.json` |

**Why:** Without the tenant slug in the URL, two tenants with identically-named entry points are indistinguishable at the router level. The tenant slug acts as a workspace root (analogous to GitHub's `/{org}/{repo}`).

**No existing clients to break** — confirmed by user. Safe to make the change without a compatibility shim.

---

## Existing tenant slugs (live DB as of 2026-09-07)

| Tenant | Slug | Notes |
|---|---|---|
| Default Tenant | `default` | bootstrap, `is_bootstrap=true` |
| avi test | `avi-test` | test tenant |

---

## Files to change

### 1. Go — server mount (`go/internal/server/server.go`)

`MountApps` currently mounts at `/apps`. Change to mount at `/` (root), because the tenant slug comes before `/apps`.

```go
// Before
func (s *Server) MountApps(h http.Handler) {
    s.router.Mount("/apps", h)
}

// After — mount at root; handler owns the full /{tenant_slug}/apps/... path
func (s *Server) MountApps(h http.Handler) {
    s.router.Mount("/", h)
}
```

Similarly `MountA2A` needs to mount at `/` (root) so `/{tenant_slug}/a2a/...` is reachable.

---

### 2. Go — apps dispatcher (`go/cmd/them/main.go`)

`appsDispatcher` currently checks `r.URL.Path` for `/voice/`. With the new prefix, the path detection logic still works (it still contains `/voice/`), but comments and the function signature need updating.

Route registration inside `appsDispatcher` must change from:
```
/{app_slug}/{ep_slug}/ws   →   /{tenant_slug}/apps/{app_slug}/{ep_slug}/ws
/{app_slug}/{ep_slug}/sse  →   /{tenant_slug}/apps/{app_slug}/{ep_slug}/sse
```

The dispatcher must extract `{tenant_slug}` from the path and pass it downstream (or inject into context).

---

### 3. Go — WS handler (`go/internal/ws/handler.go`)

`AppsWSRoute` currently registers `/{app_slug}/{ep_slug}/ws`. Change to `/{tenant_slug}/apps/{app_slug}/{ep_slug}/ws`.

The handler calls `EPConfigLoader.Load(ctx, tenantID, appSlug, epSlug)`. Currently `tenantID` comes from the JWT. After this change, `tenantID` must be resolved from `tenant_slug` in the URL:

- Add a `TenantSlugResolver` interface: `Resolve(ctx, tenantSlug) (tenantID string, error)`
- Implement with a simple DB query: `SELECT id FROM them.tenants WHERE slug = $1 AND enabled = true`
- Inject into WS handler; resolve at request time; use resolved `tenantID` for `EPConfigLoader.Load`

---

### 4. Go — SSE handler (`go/internal/sse/handler.go`)

Same changes as WS handler above. `AppsSSERoute` registers `/{app_slug}/{ep_slug}/sse` → `/{tenant_slug}/apps/{app_slug}/{ep_slug}/sse`. Same `TenantSlugResolver` injection.

---

### 5. Go — A2A server (`go/internal/a2a/server.go`, `card.go`, `pgx.go`)

Routes change from:
```
POST /a2a/{app_slug}/{ep_slug}
GET  /a2a/{app_slug}/{ep_slug}/.well-known/agent.json
```
to:
```
POST /{tenant_slug}/a2a/{app_slug}/{ep_slug}
GET  /{tenant_slug}/a2a/{app_slug}/{ep_slug}/.well-known/agent.json
```

`LoadEPCard` interface and `PgxCardLoader` SQL query must add `tenant_slug` filtering:

```go
// Before
LoadEPCard(ctx context.Context, appSlug, epSlug string) (EPCardRow, error)

// After
LoadEPCard(ctx context.Context, tenantSlug, appSlug, epSlug string) (EPCardRow, error)
```

SQL update in `pgx.go`:
```sql
-- Add JOIN to tenants and filter by tenant slug
JOIN them.tenants t ON t.id = a.tenant_id
WHERE t.slug  = $1   -- tenantSlug
  AND a.slug  = $2   -- appSlug
  AND ep.slug = $3   -- epSlug
  AND ep.entry_point_type = 'a2a'
```

The `epURL` in `card.go` (returned in the agent card JSON) must also use the new path format.

---

### 6. Go — Voice handler (`go/internal/voice/`)

Check for similar slug extraction — voice uses the same `/apps/{app_slug}/{ep_slug}/voice/*` pattern. Apply same tenant slug extraction + resolver pattern.

---

### 7. Go — TenantSlugResolver (new, small)

Create a shared resolver — suggest placing in `go/internal/tenantctx/` or a new `go/internal/tenantresolver/`:

```go
type TenantSlugResolver interface {
    ResolveSlug(ctx context.Context, slug string) (tenantID string, err error)
}
```

Implement with a single SQL query against `them.tenants`. Cache results in a short-TTL sync.Map or Redis (tenants are rarely renamed). Wire into WS, SSE, A2A, and voice handlers via main.go.

---

### 8. Traefik labels (`docker-compose.yml`)

Two routers currently handle `/apps` and `/a2a`. After the change, both prefix paths contain the tenant slug first.

```yaml
# Before
- "traefik.http.routers.them-go-apps.rule=PathPrefix(`/apps`)"
- "traefik.http.routers.them-go-a2a.rule=PathPrefix(`/a2a`)"

# After — route by second path segment
- "traefik.http.routers.them-go-apps.rule=PathRegexp(`^/[^/]+/apps`)"
- "traefik.http.routers.them-go-a2a.rule=PathRegexp(`^/[^/]+/a2a`)"
```

**Important:** Make sure the priority stays high enough so these don't clash with `/api/v1/admin/...` routes. Current priority is 120 — keep or raise.

---

### 9. Frontend (`frontend/src/`)

Six spots to update — all are simple string changes, no logic change needed:

| File | Current | New |
|---|---|---|
| `src/app/admin/playground/playgroundTypes.ts:27` | `` `${base}/apps/${t.appSlug}/${t.slug}/ws` `` | `` `${base}/${t.tenantSlug}/apps/${t.appSlug}/${t.slug}/ws` `` |
| `src/app/admin/playground/page.tsx:203` | `` `/apps/${activeWebrtc.appSlug}/${activeWebrtc.epSlug}/voice` `` | `` `/${activeWebrtc.tenantSlug}/apps/${...}/voice` `` |
| `src/lib/api.ts:233` | `` `/api/them/apps/${appSlug}/${slug}/voice/chat` `` | add tenantSlug param |
| `src/lib/api.ts:253` | `` `/api/them/apps/${appSlug}/${slug}/voice/tts` `` | add tenantSlug param |
| `src/lib/api.ts:274` | `` `/api/them/apps/${appSlug}/${slug}/voice/stream` `` | add tenantSlug param |
| `src/lib/api.ts:435` | `` `/api/apps/${slug}` `` | add tenantSlug param |

`tenantSlug` is already available on the `Application` and `EntryPoint` objects from the API — just needs to be plumbed through. If it's not on the API response types, add it to `apiTypes.ts` and the Go DAL query.

---

### 10. Playground page — fetch entry points with tenant slug

The playground currently fetches entry points for an application. The `tenantSlug` is needed to build the WS URL. Ensure the EP list API response includes `tenant_slug` (join from `them.tenants` via `entry_points.tenant_id`).

---

## Implementation order

1. **TenantSlugResolver** — wire the slug→tenantID lookup (small, shared utility)
2. **Go router changes** — server.go mount points + main.go dispatcher
3. **WS handler** — new URL pattern + resolver injection
4. **SSE handler** — same as WS
5. **A2A server + pgx** — new URL pattern + LoadEPCard signature update
6. **Voice handler** — same pattern
7. **Traefik labels** — update docker-compose.yml
8. **Frontend** — update URL construction in 6 spots
9. **Tests** — update all affected handler tests for new URL patterns; add resolver tests
10. **`go test ./...`** — must pass before commit
11. **Rebuild + smoke test** — WS connect, SSE stream, A2A card fetch, publish

---

## Constraints / watch-outs

- The Traefik `PathRegexp` rule must NOT catch `/api/v1/admin/...` — verify priority ordering
- `/.well-known/agent.json` is a standard A2A path — the new URL `/{tenant_slug}/a2a/{app_slug}/{ep_slug}/.well-known/agent.json` is spec-compliant
- Voice handler currently hard-codes `tenantctx.BootstrapTenantID` in `main.go` — this must be replaced with the slug-resolved tenant ID
- Do NOT touch `/api/v1/admin/...` routes — those are admin-only and are not affected
- DB query for slug resolution must include `AND enabled = true` to block disabled tenants at the door
- After this change, the `db/001_schema.sql` unique constraints do not need to change — `(application_id, slug)` on entry_points is already correct

---

## Startup commands for the new session

```bash
cd /opt/docker/them
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml ps
```

First prompt:
> Read docs/TASK_TENANT_URL_RESTRUCTURE.md then docs/CURRENT.md. Implement the tenant-scoped entry point URL restructure exactly as specified. Start with step 1 (TenantSlugResolver), confirm the plan with me before writing any code.
