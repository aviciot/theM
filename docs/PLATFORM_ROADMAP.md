# the-M Platform Roadmap
# Managed Apps, Observability & Redis Metrics
# Last updated: 2026-09-10

This is the working implementation plan for the platform sharing and observability features
discussed in the September 2026 architecture session. Update the status column as phases complete.

---

## Summary

| Phase | Goal | Status | Requires |
|---|---|---|---|
| 1 | Redis metrics — write side | **COMPLETE** `f420688` | — |
| 2 | Observability — per-app breakdown | **COMPLETE** `ad2ea46` | Phase 1 |
| 3 | Managed Apps — shared runtime (Option 1) | **COMPLETE** `fd50ec0` | Phase 2 |
| 4 | Deploy to Tenant — hard fork (Option 2) | **PLANNED** | Phase 3 (optional) |

---

## Phase 1 — Redis Metrics Foundation (write side only)

**Goal:** Accumulate real-time counters in Redis so the admin dashboard can read live numbers
without DB queries. Phase 2 reads these. No read-path plumbing in this phase.

### What's already built
- Redis client available everywhere via `internal/cache/`
- Rate-limit infrastructure in `internal/ratelimit/` (INCR pattern to follow)
- `them.run_usage` table captures tokens per run (30-day DB source of truth)

### What's needed

**New package: `go/internal/metrics/`**

```
go/internal/metrics/
  recorder.go    # Recorder interface + RedisRecorder + NoopRecorder
  recorder_test.go
```

Recorder interface:
```go
type Recorder interface {
    RecordRun(ctx context.Context, tenantID, appID string) error
    RecordTokens(ctx context.Context, tenantID, appID string, tokensIn, tokensOut int64) error
    RecordMCPCall(ctx context.Context, tenantID, appID string) error
    RecordUser(ctx context.Context, tenantID string, userID int64) error
}
```

**Redis key patterns** (document in `docs/REDIS.md`):

| Key | Type | TTL | Increment on |
|---|---|---|---|
| `them:metrics:{tenant_id}:{YYYY-MM-DD}` | Hash | 32 days | any event |
| `them:metrics:{tenant_id}:app:{app_id}:{YYYY-MM-DD}` | Hash | 32 days | any event |
| `them:metrics:{tenant_id}:users:{YYYY-MM-DD}` | HyperLogLog | 32 days | session admit |

Hash fields: `runs`, `tokens_in`, `tokens_out`, `mcp_calls`, `errors`

Implementation: `HINCRBY` for counters, `EXPIRE` reset on each write (sliding 32d window),
`PFADD` for unique users.

### Steps

- [ ] 1. Write `go/internal/metrics/recorder.go` — interface, RedisRecorder (HINCRBY + PFADD), NoopRecorder
- [ ] 2. Write `go/internal/metrics/recorder_test.go` — unit tests with mock Redis (miniredis)
- [ ] 3. Wire `RecordRun` into `go/internal/runrecorder/recorder.go` CreateRun
- [ ] 4. Wire `RecordTokens` into `go/internal/runrecorder/recorder.go` on usage write
- [ ] 5. Wire `RecordMCPCall` into `go/internal/mcp/executor.go` Execute (fire-and-forget goroutine)
- [ ] 6. Wire `RecordUser` into `go/internal/ws/handler.go` + `go/internal/sse/handler.go` admit path
- [ ] 7. Update `docs/REDIS.md` with new key patterns
- [ ] 8. `cd go && go test ./...` — zero failures
- [ ] 9. Build + deploy (`docker compose build them-go-bridge && docker compose up -d them-go-bridge`)

### Key files

| File | Change |
|---|---|
| `go/internal/metrics/recorder.go` | new |
| `go/internal/metrics/recorder_test.go` | new |
| `go/internal/runrecorder/recorder.go` | add Recorder field + calls |
| `go/internal/mcp/executor.go` | add Recorder field + fire-and-forget call |
| `go/internal/ws/handler.go` | add Recorder field + RecordUser call |
| `go/internal/sse/handler.go` | add Recorder field + RecordUser call |
| `go/cmd/them/main.go` | instantiate RedisRecorder, inject into all above |
| `docs/REDIS.md` | document new keys |

### Session handover for Phase 1

Fresh session prompt:
> Read `docs/PLATFORM_ROADMAP.md`. We are starting Phase 1 (Redis Metrics Foundation).
> Create `go/internal/metrics/recorder.go` with the Recorder interface, RedisRecorder
> (HINCRBY + PFADD via rueidis), and NoopRecorder. Then wire into runrecorder, mcp executor,
> ws and sse handlers. Follow `go/CLAUDE.md` — every change needs a test, update TEST_INDEX.md,
> run `cd go && go test ./...` before commit.

---

## Phase 2 — Observability: Per-App Breakdown

**Goal:** Extend `/admin/observability` to show a per-app breakdown within each tenant.
Today the page shows per-tenant totals (30d from DB). After this phase: expand a tenant row
to see per-app stats combining live Redis (today) + DB (30d history).

### What's already built
- `/admin/observability` frontend page (`frontend/src/app/admin/observability/`)
- Backend `GET /api/v1/admin/observability` returns per-tenant 30d totals

### What's needed

**Backend:**
- DAL method `ListAppObservabilitySummary(ctx, tenantID)` — queries `them.runs` + `them.run_usage`
  grouped by `application_id`, joins `them.applications` for display name
- Handler: `GET /api/v1/admin/observability/tenant/{id}/apps`
  - DB call for 30d history
  - Redis HGETALL `them:metrics:{tenant_id}:app:{app_id}:{today}` for live today numbers
  - Merge: live today overrides the DB "today" slice

**Frontend:**
- Clicking a tenant row expands to a per-app table
- Columns: App name, Runs (30d), Tokens in (30d), Tokens out (30d), MCP calls (30d), Active users today
- Today's live numbers come from the new endpoint (Redis-backed), badge indicates "live"

### Steps

- [ ] 1. DAL: `ListAppObservabilitySummary` in `go/internal/admin/dal/`
- [ ] 2. Handler + route registration
- [ ] 3. Tests for DAL + handler
- [ ] 4. Frontend: expandable tenant row with per-app table
- [ ] 5. Redis hybrid read — HGETALL today + DB 30d merge
- [ ] 6. `cd go && go test ./...` — zero failures
- [ ] 7. Build + deploy

### Key files

| File | Change |
|---|---|
| `go/internal/admin/dal/observability.go` | new — ListAppObservabilitySummary |
| `go/internal/admin/observability.go` | new handler |
| `go/cmd/them/main.go` | register new route |
| `frontend/src/app/admin/observability/` | expand row, per-app table |

### Session handover for Phase 2

Fresh session prompt:
> Read `docs/PLATFORM_ROADMAP.md`. Phase 1 is complete. Start Phase 2 (Observability per-app breakdown).
> Backend: add `ListAppObservabilitySummary` DAL method + handler at
> `GET /api/v1/admin/observability/tenant/{id}/apps`. Read Redis today tier (HGETALL) + DB 30d,
> merge and return. Frontend: expand tenant row to per-app table. See `go/CLAUDE.md` for rules.

---

## Phase 3 — Managed Apps (Platform-owned, shared runtime)

**Goal:** the-M Default tenant publishes an app; specific tenants consume it via
`managed_app_bindings`. Runs are attributed to the consuming tenant for quota/billing.

This is Option 1 from the architecture discussion: platform owns, tenants access.

### What's already built
- `applications.is_managed` column in schema
- `managed_app_bindings(tenant_id, application_id)` table in schema
- Admin UI skeleton for managed app bindings (partial — check current state before starting)

### What's needed

**Runtime path fix (critical):**
- `epConfigQuery` in `go/internal/epconfig/pgx.go` currently filters by `tenant_id` of the calling token.
  A Bank user's token has Bank's `tenant_id`, so it can't see a Default-tenant-owned entry point.
- Fix: JOIN `managed_app_bindings` — allow consuming tenant tokens to reach managed entry points.

**Run attribution:**
- Runs created by Bank via a managed app must have `tenant_id = bank_tenant_id` (not Default).
  This ensures Bank's quota, metrics, and observability are correctly charged.

**Admin UI (super-admin only):**
- "Make managed" toggle on app detail panel (PATCH `applications/{id}` with `is_managed: true`)
- "Assign to tenants" panel — list of tenants with add/remove buttons writing to `managed_app_bindings`

### Steps

- [x] 1. `epConfigQuery` JOIN `managed_app_bindings` — consuming tenant can reach managed EP
- [ ] 2. Run attribution: `lifecycle.go:403` uses resolvedCfg.TenantID (EP owner); for managed apps this is the platform tenant, not the consuming tenant. **Known gap — see STATUS.md.** Deferred to Phase 4.
- [x] 3. PATCH handler to toggle `is_managed` on application
- [x] 4. CRUD endpoints for `managed_app_bindings` (pre-existing — S1-92)
- [x] 5. Frontend: "Make managed" toggle (super_admin only) in RuntimeView
- [ ] 6. Observability: verify managed-app runs appear under consuming tenant (needs E2E with real managed-app binding)
- [x] 7. Tests: EC-MA-01..02 (epconfig), MA-01..03 (managed flag handler)
- [ ] 8. Build + deploy

### Key files

| File | Change |
|---|---|
| `go/internal/epconfig/pgx.go` | JOIN managed_app_bindings |
| `go/internal/admin/dal/applications.go` | toggle is_managed, managed_app_bindings CRUD |
| `go/internal/admin/applications.go` | new handlers |
| `go/cmd/them/main.go` | register new routes |
| `frontend/src/app/admin/` | managed toggle + assignment panel |

### Session handover for Phase 3

Fresh session prompt:
> Read `docs/PLATFORM_ROADMAP.md`. Phases 1 and 2 are complete. Start Phase 3 (Managed Apps).
> Critical first step: read `go/internal/epconfig/pgx.go` — the `epConfigQuery` must JOIN
> `managed_app_bindings` so a consuming tenant's access token can resolve a Default-owned
> entry point. Then add the is_managed toggle and tenant assignment endpoints.
> Runs must be attributed to the consuming tenant. See `go/CLAUDE.md`.

---

## Phase 4 — Deploy to Tenant (Hard Fork)

**Goal:** Super-admin clones a blueprint app into a target tenant. After deploy, the tenant owns
the copy fully — no link back to the source. Tenant must supply their own LLM keys and MCP
credentials post-deploy.

This is Option 2 from the architecture discussion: super-admin-only deploy/fork action.

### What's already built
- Nothing specific to this phase — it is a net new feature

### What does "deploy" clone

**Copied (new `tenant_id`):**
- `them.applications` row
- `them.entry_points` rows (cloned under new app)
- Orchestrator binding reference

**NOT copied:**
- `applications.provider_keys` (LLM API keys) — tenant sets their own
- `app_mcp_credentials` — tenant sets their own
- `them.runs` history
- Any secrets or credentials

### API

```
POST /admin/applications/{id}/deploy
Body: {"target_tenant_id": "uuid"}
Response: new Application object under target tenant
```

Implemented as an atomic CTE in a single transaction — no partial state if it fails.

### Steps

- [ ] 1. DAL: `DeployApplication(ctx, sourceAppID, targetTenantID)` — atomic CTE clone
- [ ] 2. Handler + route (RequireSuperAdmin middleware enforced)
- [ ] 3. Frontend: "Deploy to tenant" button on app card (super-admin only) → tenant picker modal
- [ ] 4. Post-deploy checklist returned in response body (keys to configure, MCPs to set)
- [ ] 5. Tests — verify clone is independent (delete source does not affect copy)
- [ ] 6. Build + deploy

### Key files

| File | Change |
|---|---|
| `go/internal/admin/dal/applications.go` | DeployApplication CTE |
| `go/internal/admin/applications.go` | deploy handler |
| `go/cmd/them/main.go` | register route |
| `frontend/src/app/admin/` | deploy button + tenant picker modal |

### Session handover for Phase 4

Fresh session prompt:
> Read `docs/PLATFORM_ROADMAP.md`. Phases 1-3 are complete. Start Phase 4 (Deploy to Tenant).
> Add `DeployApplication(ctx, sourceAppID, targetTenantID)` as an atomic CTE in
> `go/internal/admin/dal/applications.go`. Handler at `POST /admin/applications/{id}/deploy`,
> RequireSuperAdmin enforced. Do NOT copy `provider_keys` or `app_mcp_credentials`.
> Frontend: Deploy button (super-admin only) + tenant picker modal. See `go/CLAUDE.md`.

---

## General Handover Instructions

Use these steps any time a session is ending mid-phase:

1. Run `cd go && go test ./...` — fix any failures before stopping.
2. Commit all changed files: `git add <files> && git commit -m "wip: phase N — describe what's done"`
3. Check off completed steps in this file and update the phase status in the summary table above.
4. Update `docs/CURRENT.md`:
   - Set HEAD to `git log --oneline -1`
   - Set "next recommended task" to the first unchecked step in the current phase
5. Push: `git push origin main`
6. Open a fresh session and paste the handover prompt from the active phase section above.

Never start a new phase in the same session as completing the previous one —
open a fresh session for each phase boundary.

---

## MCP Architecture Reference (settled — no changes needed)

The MCP credential split is already correct and fully wired:

| Credential | Column | Owner | Purpose |
|---|---|---|---|
| Health probe | `mcp_servers.probe_credential_encrypted` | Platform | `them-mcp-service` health checks |
| Runtime | `app_mcp_credentials.credential_encrypted` | Tenant app | Tool calls during runs |

At runtime, `go/internal/mcp/executor.go` resolves `app_mcp_credentials` by `(app_id, mcp_server_id)`.
The Bank tenant sets their own API key; the executor picks it up transparently.
No changes needed here — document for context only.
