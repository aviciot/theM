# Platform-as-Tenant — Plan
# Status: PLANNED, phased. Phase 1 COMPLETE (2026-09-24). Phase 2 COMPLETE (2026-09-24). Phase 3 NEXT.
# Owner: platform
# Last updated: 2026-09-24

---

## Goal

Today, "the-M's own platform-level LLM configuration" (used by the classifier, card_synthesizer,
and security_scanner system-agent roles) is represented as `tenant_id IS NULL` rows in
`them.llm_providers` / `them.llm_provider_keys`, with a full parallel set of DAL/service/handler/
frontend code paths duplicating the tenant-scoped equivalents. This plan **removes that parallel
system** and makes the-M's own platform configuration a real tenant — specifically, the existing
bootstrap tenant (`them.tenants.id = 00000000-0000-0000-0000-000000000001`, `is_bootstrap = true`)
— using the exact same tenant-scoped code every other tenant already uses.

**Concretely, after this plan:**
- Classifier/card_synthesizer/security_scanner resolve their "platform" LLM config by reading the
  bootstrap tenant's own `them.tenant_system_agent_config` row and `them.llm_providers`/
  `llm_provider_keys` rows scoped to `tenant_id = <bootstrap>` — not a separate NULL-tenant path.
- A super_admin who is also a member of the bootstrap tenant (true for `avi`/`admin` today) can
  manage that tenant's own LLM providers/keys through the **same tenant self-service screen**
  every other tenant uses — no separate "platform" screen, no missing UI path.
- Debug Mode's per-node General-mode credential picker (`docs/APP_CANVAS_DEBUG_PLAN.md`) works for
  apps owned by the bootstrap tenant the same way it already works for every other tenant — this
  is the concrete bug that surfaced this gap (see "Origin of this plan" below).
- The bootstrap tenant must **not** be selectable/deletable as an ordinary tenant from platform
  management screens (tenant list/picker) beyond what already exists — `is_bootstrap` already
  guards deletion (`go/internal/admin/dal/tenants.go`); this plan must not weaken that guard, and
  should tighten tenant-listing UI so the bootstrap/platform tenant is visually distinct (not
  silently hidden — a super_admin must still be able to find and manage it).

**Explicitly not changed:** the hard rule from `docs/TENANT_LLM_PROVIDERS_PLAN.md` — "no
platform-key fallback for tenants, ever" — is preserved. A normal tenant with no usable key still
fails, not falls back to the bootstrap tenant's key. This plan removes the *separate NULL-tenant
mechanism*, it does not add a new fallback path.

---

## Origin of this plan

Found live, this session, while walkthrough-testing App Canvas Debug Mode Phase 6 (Step controls)
together with the user: `stage2-graph-llm-condition-v2` (an app owned by the bootstrap tenant) had
no usable LLM key in the debug panel's General mode, because the bootstrap tenant had never
created its own `them.llm_providers` row — only the platform (NULL-tenant) row existed. Attempting
to fix this via Settings → LLM Providers failed twice: as a `super_admin`-role user, that screen
**always** shows the platform-wide view, with no way to reach the bootstrap tenant's own
tenant-scoped self-service screen — even though the logged-in user's own tenant membership
(`avi`/`admin`, role `super_admin`, tenant `00000000-...0001`) is exactly that tenant.

User's diagnosis, confirmed correct by research: this isn't a missing toggle to patch — it's that
"platform" was never a real tenant, so there's structurally no tenant-scoped screen for it to
route to. The user asked for the clean fix: make the-M's own platform config a real tenant, so it
automatically gets everything every other tenant already has, with zero duplicate code.

---

## Current-state map (researched 2026-09-24, see commit history for the full research pass)

### 1. Where `tenant_id IS NULL` genuinely means "the-M itself" today

Only two tables carry this **live, intentional** convention:
- **`them.llm_providers.tenant_id`** (nullable since `db/057_tenant_llm_providers.sql`) — NULL =
  platform-default provider row. Partial unique indexes split platform vs tenant uniqueness:
  `llm_providers_name_platform_uq` (`WHERE tenant_id IS NULL`) /
  `llm_providers_name_tenant_uq` (`WHERE tenant_id IS NOT NULL`).
- **`them.llm_provider_keys.tenant_id`** (made nullable by `db/108_platform_llm_provider_keys.sql`,
  mirroring the same pattern) — NULL = platform-owned named key. Same partial-unique-index split,
  plus a default-key variant of each (`llm_provider_keys_default_platform_uq` /
  `_default_tenant_uq`).

No other table has a live "NULL = platform" convention. (`app_config`, `component_definitions`,
`audit_logs` have NULL conventions too, but with different meanings — "global default," "builtin,
visible to all tenants," and "platform-authored log row" respectively — none of them represent
"the-M's own LLM configuration" and none are in scope for this plan.)

### 2. Duplicated Go code (tenant-scoped fn / platform fn pairs)

| Concern | Tenant-scoped | Platform-only (NULL-tenant) | File |
|---|---|---|---|
| Provider row lookup | `GetProviderByNameForTenant` | `GetProviderByNamePlatform` | `dal/llm_providers.go` |
| Provider list | `ListProvidersForTenant` | `ListProviders` | `dal/llm_providers.go` |
| Provider upsert | `UpsertTenantProvider` | `CreateProvider` | `dal/llm_providers.go` |
| Provider row (service) | (tenant path) | `GetOwnProviderRow` / `GetPlatformProviderRow` | `service/llm_providers.go` |
| Provider CRUD (service) | `ListForTenant` / `UpsertForTenant` | `List` / `Create` / `Update` / `Delete` | `service/llm_providers.go` |
| Provider-key routes | `llm_provider_keys.go` | `llm_provider_keys_platform.go` (header: "Platform-owned mirror of llm_provider_keys.go's tenant-scoped routes") | `internal/admin/` |
| Provider base-URL lookup | (folded into the same query) | `GetProviderBaseURLs` — raw `WHERE (tenant_id = $1::uuid OR tenant_id IS NULL) ... ORDER BY tenant_id NULLS LAST` | `dal/app_config.go:64-90`, called from `service/applications.go:374` (production path, fail-open) — **missed by the original research pass, found in review** |
| Provider list (2nd query shape) | — | a second inline `OR (tenant_id IS NULL AND name NOT IN (...))` predicate, distinct from `ListProviders` above | `dal/llm_providers.go:57-60` — **missed by the original research pass, found in review** |
| System-agent role resolution | `resolveSystemAgentRole` (reads `them.tenant_system_agent_config`) | `resolvePlatformSystemAgentRole` (reads `them.config['system_agents']`) | `system_agent_resolve.go` — share one `resolveGeneralMode` closure already |
| System-agent config handler | `TenantSystemAgentConfigHandler` | `SystemAgentsHandler` | separate files, separate storage tables |
| LLM key resolution | `TenantProviderRow` | `PlatformProviderRow` | `llmresolve.go` — platform row already explicitly never used as an API-key fallback, only base_url/pricing metadata |
| Call-site duplication | — | inline "also try platform" fallback block, repeated 3x | `classify.go`, `synthesize.go`, `security_scan_llm.go` |

### 3. Duplicated routes

`/admin/llm-providers/...` (platform, `RequireSuperAdmin`) vs `/admin/my/llm-providers/...`
(tenant, `RequireTenantAdmin`); `/admin/system-agents...` vs `/admin/my/system-agents/{role}/...`.
Genuinely platform-only with **no** tenant concept at all (out of scope, stays as-is): tenant CRUD
itself (`/admin/tenants`), `/admin/monitoring-config`, `/admin/llm-routing`, `/admin/observability/*`,
`/admin/sessions`.

### 4. Duplicated frontend

`settings/page.tsx`'s `isSuperAdmin` branch → `RoleCard`/`LLMProvidersPanel` (platform) vs
`TenantRoleCard`/tenant-scoped `LLMProvidersPanel` calls (tenant). `GeneralModePicker.tsx` branches
the same way. `admin/tenants/page.tsx` is platform-only (tenant management itself) — already
excludes deletion for `is_bootstrap` tenants; does not currently filter them out of the list/grid.

### 5. The bootstrap tenant — already partway there

`them.tenants.is_bootstrap` (`db/082_tenant_is_bootstrap.sql`) already exists, seeded `true` for
`00000000-0000-0000-0000-000000000001`. Already has a real deletion guard
(`dal/tenants.go`: deletes filtered `AND is_bootstrap = false`; frontend hides Delete button for
it). Already used as "the platform's own tenant" in exactly one other place: managed-app entry
points are owned by the bootstrap tenant via a `managed_app_bindings` join (see `docs/CURRENT.md`'s
managed-apps sections). No existing code connects this to the `llm_providers`/`llm_provider_keys`
NULL convention — the two "platform" concepts (bootstrap tenant, NULL-tenant LLM rows) have been
built as unrelated ideas that happen to both mean "the-M itself."

### 6. RLS — the one real risk area (corrected after review — the two tables are NOT symmetric)

**`them.llm_provider_keys`** (`db/105`/`db/108`): RLS policy is `tenant_id = current_setting('app.tenant_id')`
— NULL rows are excluded from `them_app` **because NULL never equals a set GUC value**, not via an
explicit NULL-exclusion clause. Migrating these rows to the bootstrap tenant's real UUID is safe in
the sense that no *other* tenant could see them before or after (they simply become visible to
queries running as the bootstrap tenant specifically, which is the intended outcome).

**`them.llm_providers` is different and was wrong in an earlier draft of this doc — verified by
review against the actual policy SQL:** `db/077_rls_phase_g.sql`'s `llm_providers_read` policy is
`USING (tenant_id = current_setting(...) OR tenant_id IS NULL)` — an **explicit** `OR tenant_id IS
NULL` clause that deliberately makes every platform-default provider row visible to *every* tenant
today (this is how a tenant sees "anthropic is available, here's its default model" before ever
creating their own row). **Migrating these NULL rows to the bootstrap tenant's real UUID will
silently revoke that visibility for every other tenant** — the `OR tenant_id IS NULL` branch stops
matching once the row has a real tenant_id, and nothing else in the policy grants visibility into
another tenant's row. `db/077_rls_phase_g.sql`'s `llm_providers_read`/`llm_providers_write` policies
must be rewritten as part of Phase 1/3, not just "replace the partial unique indexes" — this was
missing from the original plan and from decision 6 below.

**Practical mitigation, to be verified not assumed:** the two current DB-layer consumers found so
far (`llmresolve.go`'s provider resolution, `workerconfig.PgxLoader`) run on the BYPASSRLS admin
pool, not the `them_app` RLS-scoped pool — so the specific "platform row disappears from a tenant's
own picker" failure mode may not manifest for those two call sites. But `GetProviderBaseURLs`
(`dal/app_config.go:64-90`, item added to Section 2 above) has no stated pool guarantee and must be
checked explicitly — this is exactly the "verify row-by-row, don't assume" instruction already in
this section, now with a concrete example of why it matters.

### 7. No deliberate rationale exists for the NULL-tenant design

Every doc describes it as "mirroring the existing pattern" (`db/057`, `db/108`,
`docs/CURRENT.md`, `docs/SCHEMA.md`) — never as an evaluated tradeoff against a real-tenant-row
alternative. This plan is not overriding a considered decision.

---

## Design decisions (proposed — confirm before implementation)

1. **The bootstrap tenant (`00000000-0000-0000-0000-000000000001`) becomes the-M's own operating
   tenant.** No new tenant row, no new "tenant type" concept — reuse what already exists and is
   already special-cased for deletion protection.
2. **Migrate existing NULL-tenant rows to the bootstrap tenant's ID**, not delete-and-recreate —
   preserves existing platform keys ("MainAnth", "MainKey," etc. — whatever a super_admin has
   already saved) and their usage history.
3. **Delete the platform-only code paths entirely** once migrated — `GetProviderByNamePlatform`,
   `resolvePlatformSystemAgentRole`, `llm_provider_keys_platform.go`, `SystemAgentsHandler`, the
   platform-only frontend branches — rather than keeping both paths "just in case." This is the
   whole point: no duplicate code survives.
4. **`them.config['system_agents']`'s existing JSONB config must migrate into
   `them.tenant_system_agent_config`** rows scoped to the bootstrap tenant, so classifier/
   card_synthesizer/security_scanner read through the one tenant-scoped resolver
   (`resolveSystemAgentRole`) exclusively — `resolvePlatformSystemAgentRole` and its call sites are
   deleted, not deprecated.
5. **Frontend: no separate "Platform" screen.** A `super_admin` who is a member of the bootstrap
   tenant sees exactly the same Settings → LLM Providers / System Agents screens as any tenant
   admin, scoped to their own tenant (which, for `avi`/`admin`, is the bootstrap tenant). Tenant
   management (`/admin/tenants`, creating/listing/deleting OTHER tenants) is unaffected — that
   remains a genuinely platform-only concern, unrelated to LLM configuration.
6. **RLS — two separate fixes needed, not one, per the corrected Section 6 above:**
   - `llm_provider_keys`: replace the NULL-based partial unique indexes with normal non-partial
     tenant-scoped ones (matching every other tenant-scoped table's pattern) once all rows have a
     real `tenant_id`. No policy rewrite needed here — the existing equality policy already behaves
     correctly once NULL is gone.
   - `llm_providers`: **must rewrite** `db/077_rls_phase_g.sql`'s `llm_providers_read`/
     `llm_providers_write` policies — the current `OR tenant_id IS NULL` clause deliberately shows
     platform-default rows to every tenant, and that behavior needs an explicit replacement decision
     (see open question below), not just an index change.
   - Verify with a dedicated cross-tenant isolation test that the bootstrap tenant's LLM rows are
     invisible to every other tenant's direct queries, the same as any two ordinary tenants are
     invisible to each other today.
7. **RESOLVED (confirmed with the user, 2026-09-24): keep cross-tenant visibility of the
   bootstrap tenant's default provider rows.** `db/077_rls_phase_g.sql`'s `llm_providers_read`
   policy's `OR tenant_id IS NULL` clause is replaced with `OR tenant_id = <bootstrap-uuid>`
   (hardcoded constant, matching how `is_bootstrap` is already checked by literal UUID elsewhere in
   this codebase) — every tenant keeps seeing "Anthropic available, default model X" out of the box,
   exactly like today, just naming the bootstrap tenant explicitly instead of relying on NULL.
   `llm_providers_write` stays tenant-equality-only (unchanged) — only the bootstrap tenant's own
   admins can edit its rows, same as any tenant only edits their own.
8. **Open question for the user:** should `them.tenants` gain an explicit `is_platform` (or similar)
   flag distinct from `is_bootstrap`, in case a future need arises to distinguish "the historical
   single-tenant backfill target" from "the-M's own operating tenant" — or is collapsing them into
   one flag (`is_bootstrap` doing double duty) acceptable? Recommend **collapsing them** unless a
   concrete reason to split emerges — no evidence today that these need to differ.

---

## Phases (draft — subject to revision once decisions above are confirmed)

| Phase | What | Depends on |
|---|---|---|
| 1 — Data migration + RLS rewrite | ✅ **COMPLETE (2026-09-24)** — see "Phase 1 — COMPLETE" section below | Decisions above confirmed |
| 2 — Backend consolidation | ✅ **COMPLETE (2026-09-24)** — see "Phase 2 — COMPLETE" section below | Phase 1 |
| 3 — RLS verification | Dedicated integration tests proving bootstrap-tenant LLM rows are invisible to other tenants' queries (or correctly visible, per decision 7's resolution), and vice versa, post-migration | Phase 1 |
| 4 — Frontend consolidation | Remove `isSuperAdmin` branch from Settings → LLM Providers / System Agents; both screens always render the tenant-scoped view for the caller's own tenant | Phase 2 |
| 5 — Tenant management UI | Ensure `/admin/tenants` list clearly marks the bootstrap/platform tenant as such (not hidden, but visually distinct) — no functional change to deletion guard, which already exists | Independent, can run anytime |
| 6 — Verification | Re-run the App Canvas Debug Mode Phase 6 walkthrough that surfaced this gap — confirm `stage2-graph-llm-condition-v2`'s debug panel General mode now shows the bootstrap tenant's own keys/models correctly | All above |

**One phase per session**, same discipline as every other plan this session referenced. Do not
start Phase 2 in the same session as Phase 1, etc., unless explicitly told otherwise.

---

## Phase 1 — COMPLETE (2026-09-24)

`db/110_platform_as_bootstrap_tenant.sql` (+ `db/110_platform_as_bootstrap_tenant_DOWN.sql`),
applied live to this box's `them-postgres`. Summary:

**Discovered at migration-authoring time, changing scope from the original phase description:**
this box's `them.config` has **no `system_agents` row at all** — never configured. The planned
"migrate `them.config['system_agents']` into `tenant_system_agent_config`" step was dropped from
this migration as a result; there was nothing to migrate. `resolvePlatformSystemAgentRole`'s
fail-open default already produces the same effective behavior whether the row is absent or
migrated-and-absent. **Flagged for whoever runs Phase 2 on a box that DOES have a configured
`system_agents` row**: check for one before deleting `resolvePlatformSystemAgentRole`, since that
box's migration would need the config-copy step this one didn't.

**What the migration actually did:**
- Reassigned all 5 `them.llm_providers` rows (`anthropic`, `openai`, `mock`, `gemini`, `groq`) and
  the 1 `them.llm_provider_keys` row ("MainKey") that had `tenant_id IS NULL` to the bootstrap
  tenant (`00000000-0000-0000-0000-000000000001`). Pre-flight collision checks (own `DO` blocks)
  confirmed zero name/key conflicts with the bootstrap tenant's existing rows (it had none) before
  running the `UPDATE`s — would have aborted loudly instead of silently violating a constraint.
- Replaced the partial (NULL-aware) unique indexes on both tables with normal non-partial
  tenant-scoped ones, since no row can have a NULL `tenant_id` anymore.
- Rewrote `llm_providers_read`'s RLS policy (`db/077_rls_phase_g.sql`'s original) to check
  `tenant_id = <bootstrap-uuid>` instead of `tenant_id IS NULL`, per decision 7 (confirmed with the
  user: keep cross-tenant visibility of the bootstrap tenant's default provider rows).
  `llm_providers_write` and `llm_provider_keys`'s policy (db/105) were **not** touched — confirmed
  by this plan's own review that only `llm_providers_read` had the NULL-visibility clause; the
  fix is asymmetric by design, not an oversight.

**Verified live, not assumed:**
- Zero rows remain with `tenant_id IS NULL` on either table (`SELECT count(*) ... WHERE tenant_id
  IS NULL` = 0 on both, post-migration).
- `SET ROLE them_app; SET app.tenant_id = '<other-tenant>'` — confirmed that tenant sees its own
  rows **plus** the bootstrap tenant's provider defaults (7 rows total: 5 bootstrap + 2 own), but
  **cannot see** the bootstrap tenant's "MainKey" row on `llm_provider_keys` (correct — keys stay
  private per-tenant, only provider *defaults* are meant to be visible).
- Attempted `UPDATE ... WHERE id = 1` (a bootstrap-owned row) as `them_app` acting as a different
  tenant — rejected with `permission denied for table llm_providers` (blocked below RLS, at the
  GRANT level — defense in depth).
- `ListProvidersForTenant`'s exact query (`tenant_id = $1 OR (tenant_id IS NULL AND name NOT IN
  (...))`) run directly against the migrated DB for the bootstrap tenant now correctly returns its
  own Anthropic row (the one "MainKey" is attached to) — this is the concrete query
  `/admin/my/llm-providers` (and therefore the App Canvas debug panel's General-mode credential
  picker) uses, so `stage2-graph-llm-condition-v2`'s debug panel should now show "MainKey" once a
  real browser session is used to check (not verified via full browser login this session — same
  standing limitation as every other UI verification this session, no browser-automation tool
  available).
- `go test ./...` (full unit suite) — 0 failures, unaffected as expected (Phase 1 touched no Go
  code). `go test -tags=integration ./internal/admin/...` — 0 failures, run for real against this
  now-migrated live database, including every `LLMProvider*`/`TenantLLMProviders*` integration test.

**Follow-ups explicitly deferred to Phase 2 (not done here — Phase 1 was data + RLS only):**
- `dal/app_config.go`'s `GetProviderBaseURLs` (`WHERE tenant_id = $1 OR tenant_id IS NULL`) and
  `dal/llm_providers.go`'s `ListProvidersForTenant` (`OR (tenant_id IS NULL AND name NOT IN (...))`)
  both still say `IS NULL` in application code — they keep working correctly today only because no
  row has a NULL `tenant_id` anymore (so that branch of each query simply never matches, which is
  harmless), but they should be deleted/rewritten in Phase 2 alongside the other platform-only code,
  not left as dead-but-technically-working NULL checks.
- **Not yet done**: allowed_models for the bootstrap tenant's Anthropic row is still `[]` — the
  user's earlier attempt to save allowed models landed on the (then-platform, now-migrated) row
  before this fix was known to be needed, so the actual saved value never took effect the way the
  user intended. This is a **user follow-up action** (re-save allowed models via Settings → LLM
  Providers, now that it's reachable... **actually still blocked**, see below), not something this
  migration could fix — allowed_models is application data, not something Phase 1's SQL should set.
- **Known remaining gap, not closed by Phase 1 alone**: the frontend's `isSuperAdmin` branch
  (`settings/page.tsx`) still routes any `super_admin`-role session to the platform-only screen,
  with no path to the tenant self-service screen — that's Phase 4's job. Phase 1 fixed the *data*
  so that once Phase 4 ships (or a temporary direct-API workaround is used), the bootstrap tenant's
  own settings will work correctly — but a normal browser session as `avi`/`admin` still cannot
  reach that screen today. This was known and expected going into Phase 1 (see the plan's phase
  table dependencies) — flagging again here so it's not mistaken for a Phase 1 regression.

**Backup taken before migration** (row-level `pg_dump --data-only` of both tables,
pre-migration) — kept in this session's scratchpad, not committed to the repo (contains no secrets
beyond what's already encrypted in the DB itself, but scratchpad is the correct place for a
point-in-time backup artifact, not version control).

---

## Phase 2 — COMPLETE (2026-09-24)

Commit `9cf638a8`. Deleted the platform-only Go code paths per decision 3, now that Phase 1
migrated their data to the bootstrap tenant:

- **Deleted entirely**: `internal/admin/system_agents.go` (`SystemAgentsHandler`,
  `/admin/system-agents` routes) + its integration test; `internal/admin/llm_provider_keys_platform.go`
  (platform-owned key CRUD routes) + its test; `resolvePlatformSystemAgentRole` and
  `platformSystemAgentRoleResolverDAL` (`system_agent_resolve.go`); `GetProviderByNamePlatform`
  (`dal/llm_providers.go`); `GetPlatformProviderRow` (`service/llm_providers.go`). `router.go`'s
  `platformGlobal` group no longer mounts `systemAgents.Routes`/`llmProviderKeys.PlatformRoutes`.
- **`classify.go`/`synthesize.go`/`security_scan_llm.go`** no longer read
  `them.config['system_agents']` at all — each now calls `resolveSystemAgentRole` once for
  `tenantctx.BootstrapTenantID` (the "platform" fallback tier) and feeds that result in as the
  fallback args of the real call for the caller's own tenant. One resolution function, two calls,
  no separate platform code path.
- **`dal/llm_providers.go`/`dal/app_config.go`**'s two Phase-1-flagged leftover `IS NULL` checks
  (`ListProviders`, `ListProvidersForTenant`, `CreateProvider`, `GetProviderBaseURLs`) now reference
  `tenantctx.BootstrapTenantID` explicitly instead of a NULL check that only "worked" because no row
  has had a NULL `tenant_id` since Phase 1's migration.
- **`them.config['system_agents']` migration**: confirmed (again, per Phase 1's flag) that this box
  has no such row — nothing to migrate. A box that does have one still needs that step done manually
  before/during a Phase 2 pass, since this code path is now deleted and can no longer read it.

**Real bug found and fixed, not scope creep**: 4 `llm_provider_keys` integration tests
(`TestDAL_ProviderKey_PlatformOwned_*`) exercised `CreateLLMProviderKey(TenantID: nil)` — a
"platform-owned key" scenario that became unreachable from any HTTP route the moment
`llm_provider_keys_platform.go` (the only caller that ever passed `nil`) was deleted in this same
phase. One of the four (`NameUniqueAmongPlatformKeysOnly`) had also silently gone wrong as of Phase
1: the old NULL-partial unique index (enforcing uniqueness among `tenant_id IS NULL` rows
specifically) was replaced with a plain composite `UNIQUE(llm_provider_id, tenant_id, name)` index,
and Postgres never treats two NULLs as equal in a unique index — so duplicate platform-owned key
names silently stopped colliding. Confirmed live against this box's Postgres before concluding the
code path is dead (not live-but-broken): deleted the 4 tests rather than resurrecting a partial
index nothing calls into anymore. See `go/TEST_INDEX.md`'s "removed (Platform-as-Tenant Phase 2)"
row for the full accounting (-4 tests, S2 total 86 → 82).

**New tests**: `internal/admin/dal/platform_as_tenant_phase2_integration_test.go` (5 tests, all run
live against this box's already-migrated Postgres) proves `ListProvidersForTenant` and
`GetProviderBaseURLs` correctly fall back to the bootstrap tenant's rows for any other tenant, that
the caller's own row still wins over the bootstrap default when both exist, and that the bootstrap
tenant querying itself doesn't double-count its own row against itself.

**Verified**: `go build ./...` + `go vet ./...` clean. `go test ./...` — 0 failures, full suite (58
packages), run fresh in a throwaway `golang:1.25` container (no local Go toolchain in this
environment) against the repo's `go.mod`. `go test -tags=integration ./internal/admin/...` — 0 new
failures, run for real against this box's live, already-Phase-1-migrated `them-postgres` over the
`them-network` Docker network; the only remaining failure is the pre-existing, unrelated
`TestIntegration_CreateToken_201` panic already tracked in `docs/CURRENT.md` (a missing tenant
context in an old test, not touched by any file this phase changed).

**Explicitly not done this phase, unaffected**: the still-live super_admin-only
`LLMProvidersHandler` CRUD surface (`/admin/llm-providers` — `List`/`Create`/`Get`/`Update`/`Delete`)
was deliberately left in place — it's the frontend Settings screen's platform view, still used by
the live UI until Phase 4 removes the `isSuperAdmin` branch that calls it. Its `Create` already
writes to the bootstrap tenant's real `tenant_id` (not NULL) as of this phase's `CreateProvider`
fix, so it needed no further change here. Deleting that handler now would break the live frontend
before Phase 4 gives it a replacement — correctly out of Phase 2's scope per the phase table.

---

## Rollback

Phase 1 is a live data migration against production-adjacent tables plus an RLS policy rewrite —
per this project's standing caution around DB/deployment work, this needs an explicit revert path
before implementation, not an assumption that it's reasonably deferred:

- **Data**: restore `tenant_id` to `NULL` on every row this migration touched (the migration should
  record which row IDs it moved — e.g. a temporary marker column or a captured ID list in the
  migration's own down-script — rather than trying to reverse-infer "which rows used to be platform"
  after the fact, since the bootstrap tenant may by then also have genuinely own rows of its own).
- **Indexes**: restore the original partial unique indexes (`llm_providers_name_platform_uq`, etc.)
  from `db/057`/`db/108` verbatim.
- **RLS**: restore `db/077_rls_phase_g.sql`'s original `llm_providers_read`/`llm_providers_write`
  policy text verbatim (keep the exact original migration file content on hand for this, don't
  hand-reconstruct it).
- **Code**: since Phase 2 deletes the platform-only functions/routes/handlers, a rollback that
  needs to happen *after* Phase 2 has shipped requires restoring that deleted code from version
  control (straightforward — Git history — but explicitly note here that Phase 1's data rollback
  alone is not sufficient once Phase 2 has run; both must be reverted together past that point).
- **Recommend**: take a `them.llm_providers`/`them.llm_provider_keys` row-level backup (or DB
  snapshot) immediately before running Phase 1's migration in any environment that matters, in
  addition to the down-migration script — the usual defense-in-depth for a data migration, not a
  substitute for having a real down-script.

## Explicitly out of scope

- Custom-mode debug credential picker UX (free-text fields instead of provider/model dropdowns) —
  a real, separately-flagged gap found during the same walkthrough, tracked independently, not
  part of this plan.
- Any change to the "no platform-key fallback for tenants" hard rule.
- Any change to how *other* tenants manage their own LLM providers/keys — they are unaffected by
  this plan; their code paths are the ones the bootstrap tenant will start using, unchanged.
- Redesigning the role model (`auth_service.roles` vs `tenant_memberships.role`) — out of scope,
  noted only as context for why a `super_admin`-role user can also be a normal tenant member.
