# Tenant-Level LLM Provider Configuration — Plan
# Status: COMPLETE — all 7 steps done
# Owner: platform
# Last updated: 2026-09-23

---

## Goal

Give each tenant a "General LLM Settings" screen where a tenant admin can, per provider
(Anthropic, OpenAI, Groq, Gemini):

1. **Enable the provider** and pick which **models** are allowed — no key required for this.
2. **Save multiple named API keys** under that provider (e.g. "Key_for_april", "Key_for_QA") —
   keys are optional and independent of enabling the provider/models.
3. **Test** any saved key with a real minimal API call before trusting it.
4. **Refresh** the model list live from the provider's real API (needs a working key) instead of
   only the small hand-seeded starter list.
5. See **usage/cost per named key** (tokens, cost, maybe call count).

Then, any feature that needs an LLM (starting with the **classifier** and **card_synthesizer**
system-agent roles) gets a consistent two-mode picker:
- **General settings (default)** — pick provider + model (from what the tenant allowed) + pick
  *which named key* to use, all sourced from the tenant's general config above.
- **Custom** — today's existing standalone override screen (its own provider/model/key), for the
  exception case.

This same general/custom pattern is meant to extend to other LLM-consuming features later
(orchestrators, etc.) — out of scope for this pass, but the schema/API shape should not paint us
into a corner.

**Hard rule (confirmed): no platform-key fallback for tenants, ever.** If a tenant has no usable
key for what a feature needs, that feature simply doesn't work for that tenant. Do not add a
fallback to any platform-level key.

---

## Why the model changes: one row per key, not one row per provider

The current `them.llm_providers` design (`db/057_tenant_llm_providers.sql`) is **one row per
`(name, tenant_id)`** with a single `api_key_encrypted` column — it structurally cannot hold
multiple named keys per provider. Confirmed by checking every consumer:

- `run_usage` (`db/001_schema.sql:117-129`) records `provider`/`model` as free text, no FK to
  `llm_providers`, no key identifier at all.
- `llmresolve.ResolveProvider` (`go/internal/llmresolve/llmresolve.go:94-119`) returns only the
  decrypted key + base_url + pricing — never the row `id`, let alone a key id.
- `RecordUsage` (`go/internal/runrecorder/recorder.go:170-188`) takes plain `provider, model`
  strings — no key reference parameter exists anywhere in that path today.

So this plan **splits the table**:
- `them.llm_providers` becomes the **provider-level config row** per tenant: enabled + allowed
  models. Drop the single `api_key_encrypted` column's role as "the" key.
- New child table `them.llm_provider_keys`: **multiple named keys** per `(provider, tenant)`,
  each with its own id, name, encrypted secret, and usage rollup columns.
- `them.run_usage` gets a new nullable FK column pointing at the specific key used, so usage/cost
  can be attributed per named key (not just per provider).

This is a bigger change than originally scoped but it's the only way "multiple named keys with
per-key usage" is representable — confirmed there's no existing structure to repurpose.

---

## Schema changes

New migration `db/105_llm_provider_keys.sql`:

```sql
-- 1. llm_providers becomes provider-level config (drop single-key assumption).
ALTER TABLE them.llm_providers
  ADD COLUMN IF NOT EXISTS allowed_models JSONB NOT NULL DEFAULT '[]';
-- api_key_encrypted stays for now (platform-default rows still use single-key semantics);
-- tenant rows stop writing to it going forward — new tenant writes go through llm_provider_keys.

-- 2. Named keys, many per (provider, tenant).
CREATE TABLE them.llm_provider_keys (
    id              BIGSERIAL PRIMARY KEY,
    llm_provider_id INTEGER NOT NULL REFERENCES them.llm_providers(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,               -- user-chosen label, e.g. "Key_for_april"
    api_key_encrypted TEXT NOT NULL,
    is_default      BOOLEAN NOT NULL DEFAULT false, -- default key for this provider+tenant, used by "general settings" mode
    last_tested_at  TIMESTAMPTZ,
    last_test_ok    BOOLEAN,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (llm_provider_id, tenant_id, name)
);
CREATE INDEX idx_llm_provider_keys_provider_tenant ON them.llm_provider_keys(llm_provider_id, tenant_id);

-- Only one default key per (provider, tenant) — partial unique index.
CREATE UNIQUE INDEX llm_provider_keys_one_default_uq
  ON them.llm_provider_keys(llm_provider_id, tenant_id) WHERE is_default;

-- 3. Usage attribution — which key backed a given usage row.
ALTER TABLE them.run_usage
  ADD COLUMN IF NOT EXISTS llm_provider_key_id BIGINT REFERENCES them.llm_provider_keys(id) ON DELETE SET NULL;
```

RLS: `llm_provider_keys` needs the same tenant-isolation policy shape as `db/077_rls_phase_g.sql`
applies to `llm_providers` (read/write restricted to `tenant_id = current_setting('app.tenant_id')`,
`them_admin` bypasses). Add this in the same migration.

Usage rollups (tokens/cost) per key are **computed from `run_usage`** (`SUM(...) GROUP BY
llm_provider_key_id`), not stored redundantly on `llm_provider_keys` — avoids double-bookkeeping.
The UI's "usage/cost per key" column is a query, not a stored counter.

`docs/SCHEMA.md` gets both a corrected `them.run_usage` section (it's currently stale/wrong per
research — actual columns are `tokens_input`/`tokens_output`/`BIGSERIAL id`, not what's documented)
and a new `them.llm_provider_keys` section.

---

## Backend changes (Go)

### 1. New DAL — `go/internal/admin/dal/llm_provider_keys.go`
CRUD for `llm_provider_keys`: `ListKeysForProvider(ctx, providerID, tenantID)`,
`CreateKey`, `UpdateKey` (rename / rotate secret), `DeleteKey`, `SetDefault` (transactionally
clears any existing default for that provider+tenant, then sets the new one — matches the
partial-unique-index constraint).

### 2. `go/internal/admin/dal/llm_providers.go`
- Add `AllowedModelsRaw []byte` to `LLMProvider`/`LLMProviderInput`, same nil-safe JSONB pattern
  as `ModelPricingRaw` (`ModelPricingOrEmpty` → new `AllowedModelsOrEmpty`).
- Tenant-row writes (`UpsertTenantProvider`) stop touching `api_key_encrypted` — that field is
  frozen to legacy/platform semantics; new tenant key management goes through
  `llm_provider_keys` exclusively.

### 3. Service layer — `go/internal/admin/service/llm_providers.go` + new `llm_provider_keys.go`
- `LLMProviderOut` gains `AllowedModels []string` and `Keys []LLMProviderKeyOut` (id, name,
  masked secret, is_default, last_test_ok/last_tested_at) — one list call returns the full
  provider-with-its-keys view for the settings screen.
- `LLMProviderKeyOut { ID int64; Name string; Masked string; IsDefault bool; LastTestOK *bool; LastTestedAt *time.Time }`
- New methods: `CreateKey`, `RenameKey`, `RotateKey`, `DeleteKey`, `SetDefaultKey`,
  `TestKey(ctx, keyID) (ok bool, errMsg string)` — reuses `probeLLMWithBase` exactly as before, just
  resolves provider+model+decrypted-secret from the named key row instead of the provider row.
- `ListAvailableModels(ctx, tenantID, providerName, keyID int64) ([]string, error)` — refresh
  button; needs a specific key to call the provider's real list-models endpoint with.

### 4. New provider-model-fetch code — `go/internal/admin/llm_provider_models.go` (new file)
Same as previously scoped: `fetchAnthropicModels`, `fetchOpenAICompatModels` (openai/groq/custom
base_url), `fetchGeminiModels` — mirrors the existing `probe*` dispatch shape in
`applications_llm.go` but hits each provider's real `GET /v1/models`-equivalent.

### 5. Handler routes — `go/internal/admin/llm_providers.go`
Tenant self-service (`/admin/my/llm-providers/...`) and platform-admin-on-tenant
(`/admin/tenants/{id}/llm-providers/...`) both get the same new sub-resource routes:

```
GET    /llm-providers/{name}/keys              list named keys for this provider (masked)
POST   /llm-providers/{name}/keys              create a named key {name, api_key}
PATCH  /llm-providers/{name}/keys/{keyID}      rename / rotate secret / set default
DELETE /llm-providers/{name}/keys/{keyID}
POST   /llm-providers/{name}/keys/{keyID}/test test this specific key (real probe call)
GET    /llm-providers/{name}/models?key_id=... refresh live model list using that key
```

PATCH on the provider itself (`/llm-providers/{name}`) extends to accept `allowed_models: string[]`
and `enabled`.

### 6. classifier / card_synthesizer — general-settings vs custom picker
- `them.config['system_agents']` role shape gains a `mode` field: `"general" | "custom"`.
  - `mode: "custom"` → behaves exactly as today (own provider/model/api_key_encrypted on the role).
  - `mode: "general"` → role stores `{provider: string, key_id: int64}` instead of its own
    provider/model/key; **model** comes from the provider's `default_model` or first allowed
    model, not stored on the role (keeps it in sync with tenant's general config automatically).
- `classifierDAL`/`synthesizerDAL` interfaces widen to add
  `GetProviderKey(ctx, tenantID string, keyID int64) (provider, model, apiKey, baseURL string, err error)`
  (joins `llm_provider_keys` → `llm_providers` for that tenant, decrypts).
- Resolution in `classifyAgent`/`synthesizeAppCard`:
  1. Load role config (`them.config['system_agents']`) — **now must be read per-tenant**, so this
     moves from a single global `them.config` row to... **open point**: `them.config` has no
     tenant_id column at all (confirmed, no migration ever added one). Storing per-tenant
     general/custom mode + which provider/key to use must live somewhere tenant-scoped. Cleanest:
     a new small table `them.tenant_system_agent_config (tenant_id, role, mode, provider_name,
     key_id, custom_provider, custom_model, custom_api_key_encrypted, custom_base_url,
     custom_system_prompt)` — one row per tenant per role. This replaces the tenant's slice of what
     used to be the global `system_agents` config; the platform-global `system_agents` config
     stays as-is for any platform-internal (non-tenant) use.
  2. `mode = "general"` → look up `(tenant_id, provider_name)` in `llm_providers` +
     `is_default` key in `llm_provider_keys` (or the explicitly stored `key_id`) → resolve →
     call. No key/provider found → graceful no-op, same as today.
  3. `mode = "custom"` → decrypt the row's own stored key, call directly — same code path as today.
- Call sites already have tenant ID in scope or one line away
  (`ep_discover.go:33` unconditionally; `agents.go:275` needs hoisting out of its current `if`
  block) — confirmed by prior research, no new plumbing needed to get `tenantID` to the call site.

---

## Frontend changes

### 1. Types — `frontend/src/lib/apiTypes.ts`
- `LLMProviderOut`: add `allowed_models: string[]`, `keys: LLMProviderKeyOut[]`.
- New `LLMProviderKeyOut { id: number; name: string; masked: string; is_default: boolean; last_test_ok?: boolean | null; last_tested_at?: string | null }`.
- New request types for create/rename/rotate/set-default/test/list-models.

### 2. API client — `frontend/src/lib/api.ts`
New functions mirroring existing `listMyLLMProviders`/`upsertMyLLMProvider` naming:
`listProviderKeys`, `createProviderKey`, `updateProviderKey`, `deleteProviderKey`,
`setDefaultProviderKey`, `testProviderKey`, `listAvailableModels` — each with a tenant-admin
self-service variant (`/my/...`) and a platform-admin-on-tenant variant (`/tenants/{id}/...`),
matching the existing dual-surface pattern.

### 3. UI — `frontend/src/app/admin/settings/page.tsx` (llm_providers tab)
Per provider card:
- Model checklist seeded from `PROVIDER_MODELS[prov.name]` (existing constant,
  `settingsConstants.ts:11-35`), with a **Refresh** button that calls `listAvailableModels`
  (disabled until at least one key exists) and replaces the option list with the live result.
- **Keys sub-section**: table of named keys (name, masked value, default radio/star, last test
  result, usage/cost columns pulled from a `run_usage` aggregate endpoint) with
  **Add key** (name + secret), **Test** per row (reuses the `RoleCard.tsx` test-button UX
  pattern), **Rotate**, **Rename**, **Delete**, **Set as default**.
- Enable/disable toggle for the provider itself, independent of whether any key exists.

### 4. UI — classifier/card_synthesizer role cards (`RoleCard.tsx` + `page.tsx` system_agents tab)
Add a **General settings / Custom** switch at the top of each role card:
- General: provider dropdown (only tenant-enabled providers) → model dropdown (only that
  provider's allowed models) → key dropdown (that provider's named keys, showing which is
  default) — no provider/model/key free-text fields, no test button needed here (testing happens
  once at the key level in the main LLM Providers tab).
- Custom: exactly today's existing form (provider/model/api_key/base_url/system_prompt + Test
  button), unchanged.

---

## Testing (per go/CLAUDE.md — every change needs a test)

- `go/internal/admin/dal/llm_provider_keys_test.go` (new): CRUD, `SetDefault` transactional
  swap, unique-default constraint violation handling.
- `go/internal/admin/dal/llm_providers_test.go`: `allowed_models` round-trip.
- `go/internal/admin/service/llm_provider_keys_test.go` (new): masking, `TestKey` error paths
  (no key → clear error), `ListAvailableModels` error paths.
- `go/internal/admin/classify_test.go`, `synthesize_test.go`: both `mode` branches — general
  (resolves tenant's default key), custom (unchanged behavior), and the "tenant has no usable
  key" graceful no-op in both modes. No cross-tenant leakage (tenant A's config never resolves
  tenant B's key).
- `go/internal/runrecorder/recorder_test.go`: `RecordUsage` accepts and persists the new
  `llm_provider_key_id` (nullable — old call sites that don't know the key still work).
- Update `go/TEST_INDEX.md` in the same commit as each test addition (mandatory).
- `go test ./internal/admin/... ./internal/runrecorder/...` before commit; full `go test ./...`
  before session handover.

---

## Sequencing

1. Migration `105_llm_provider_keys.sql` (new table + `allowed_models` column + `run_usage` FK +
   RLS policy) + corrected `docs/SCHEMA.md` (`llm_providers`, new `llm_provider_keys`, fixed
   `run_usage`).
2. DAL + service for `llm_provider_keys` CRUD + `allowed_models` on `llm_providers` + tests.
3. Test-key and list-models endpoints (per named key) + tests.
4. Frontend: LLM Providers tab — model checklist + refresh + keys sub-section (add/rename/rotate/
   delete/set-default/test).
5. New `them.tenant_system_agent_config` table + DAL/service for per-tenant role mode
   (general/custom) + wiring `classifyAgent`/`synthesizeAppCard` to resolve through it + tests.
6. Frontend: General/Custom switch on classifier & card_synthesizer role cards.
7. `go test ./...`, commit, update `docs/CURRENT.md`.

Steps 1-4 (provider/keys config + testing/refresh UI) are independently useful and shippable before
tackling step 5-6 (system-agent wiring) — recommend landing them as separate commits/checkpoints
given the file-size and test-per-change rules in `go/CLAUDE.md`.

**Step 3 — COMPLETE (2026-09-23).** Test-key and list-models HTTP endpoints landed, tenant
self-service surface only (`/admin/my/llm-providers/{name}/...` — the platform-admin-on-tenant
mirror `/admin/tenants/{id}/llm-providers/{name}/...` was deliberately not built in this pass, since
nothing needs it yet; add it later by mirroring `LLMProviderKeysHandler.TenantScopedRoutes` the same
way `LLMProvidersHandler.TenantProviderRoutes` mirrors `TenantScopedRoutes` today).

New routes, all in `go/internal/admin/llm_provider_keys.go` (`LLMProviderKeysHandler`):
```
GET    /admin/my/llm-providers/{name}/keys
POST   /admin/my/llm-providers/{name}/keys
PATCH  /admin/my/llm-providers/{name}/keys/{keyID}
DELETE /admin/my/llm-providers/{name}/keys/{keyID}
POST   /admin/my/llm-providers/{name}/keys/{keyID}/default
POST   /admin/my/llm-providers/{name}/keys/{keyID}/test
GET    /admin/my/llm-providers/{name}/models?key_id=...
```

Every route resolves `{name}` to the caller's own tenant-scoped `them.llm_providers` row first, via
new `LLMProviderService.GetOwnProviderRow` (wraps the existing `GetProviderByNameForTenant` DAL
call, translates `pgx.ErrNoRows` → `ErrNotFound`/404). **This never falls back to the platform row's
id** — if the tenant hasn't yet called `PUT /my/llm-providers/{name}` to create its own row (e.g. to
enable the provider), key routes 404 rather than silently attaching a key to the platform's row.
This is the concrete enforcement point for the hard "no platform-key fallback" rule at the
provider-resolution level, not just at LLM-call time.

Test fires the existing `probeLLMWithBase` against the key's own decrypted secret and the
provider's `default_model`/`base_url`, then records the outcome via
`LLMProviderKeyService.RecordTestResult` (`last_tested_at`/`last_test_ok` columns already existed
from step 2). List-models is new: `go/internal/admin/llm_provider_models.go` adds
`fetchAnthropicModels`, `fetchOpenAICompatModels` (openai/groq/custom base_url), `fetchGeminiModels`
— real `GET /v1/models`-equivalent calls per provider, dispatched by `listAvailableModels` the same
way `probeLLMWithBase` dispatches probes.

Tests: 2 new service tests (`GetOwnProviderRow`), 6 new tests for the models-fetch helpers (via
`httptest.Server`, no real provider network calls), 6 new handler tests (`LPK-01..06`) — all in
`go/TEST_INDEX.md` as S1-135..137. `go test ./...` — 0 failures, full suite.

**Found but not fixed this session:** commit `465ffe93` (step 2, DAL+service layer) added roughly 64
tests (`llm_provider_keys_test.go` 18, `llm_provider_keys_integration_test.go` 9, plus
`allowed_models` cases folded into the existing `llm_providers_test.go` growth) without adding
corresponding `go/TEST_INDEX.md` rows — the S1 total stayed at 1349 across that commit. Flagged as a
gap row in `go/TEST_INDEX.md` rather than silently backfilled, since reconstructing accurate
per-commit attribution for a prior session's untracked test additions was out of scope for this
step. Worth a dedicated cleanup pass before the count is trusted as precise.

**Step 4 — COMPLETE (2026-09-23).** Frontend LLM Providers tab built.

`frontend/src/app/admin/settings/page.tsx` (300 → 199 lines) had its entire `llm_providers` tab
body extracted into two new components, per the file-size rule (adding the model checklist + full
keys sub-section in-line would have pushed `page.tsx` well past the split threshold):

- `frontend/src/app/admin/settings/LLMProvidersPanel.tsx` (new) — the per-provider card. For
  super_admin (platform rows): unchanged single API-key input + Save, exactly as before. For tenant
  admin (own rows): unchanged single-key path replaced with an **Allowed models** checklist
  (seeded from `PROVIDER_MODELS[name]`, `settingsConstants.ts`) with a **Refresh from provider**
  button — disabled until the provider has at least one named key — that calls
  `GET .../models?key_id=...` and replaces the checklist options with the live result; a **Save
  allowed models** button PUTs `allowed_models` via the existing `upsertMyLLMProvider`. Below that,
  renders `LLMProviderKeysPanel` for the keys sub-section.
- `frontend/src/app/admin/settings/LLMProviderKeysPanel.tsx` (new) — per-key row: name, masked
  value, default badge, last-test-result, **Set default** / **Test** / **Delete** buttons, plus
  inline rename and rotate-secret inputs, and an **Add key** (name + secret) row at the bottom.
  Mirrors `RoleCard.tsx`'s test-button UX pattern (loading → ok/error message).
- `frontend/src/lib/apiTypes.ts` — `LLMProviderOut` gained `allowed_models: string[]`,
  `LLMProviderUpsertInput` gained optional `allowed_models?: string[]` (both already existed
  server-side on `service.LLMProviderOut`/`LLMProviderCreate` since step 2/3 — this was a
  frontend-only gap). New `LLMProviderKeyOut`, `LLMProviderKeyCreateInput`,
  `LLMProviderKeyPatchInput`, `LLMProviderKeyTestResult`, `LLMProviderModelsResult`.
- `frontend/src/lib/api.ts` — new `listProviderKeys`, `createProviderKey`, `updateProviderKey`,
  `deleteProviderNamedKey`, `setDefaultProviderKey`, `testProviderKey`, `listAvailableModels` — all
  tenant self-service only (`/admin/my/llm-providers/{name}/...`), matching the
  `LLMProviderKeysHandler` routes that already existed from step 3. **No platform-admin-on-tenant
  variants added** — the plan's dual-surface pattern for these calls doesn't apply yet since the
  server-side mirror route doesn't exist either (see step 3's note).
  **Naming note:** `deleteProviderKey` was already taken by an unrelated, pre-existing
  application-level single-key feature (`/admin/applications/{id}/provider-keys/{provider}` —
  a different table, `RuntimeView.tsx`'s per-app LLM override, nothing to do with this plan). Named
  the new function `deleteProviderNamedKey` to avoid the collision — found as a `tsc` duplicate-key
  compile error, not by inspection, so worth remembering if extending this surface further.

**Not live-verified in a browser this session** — no browser-automation tool was available in this
environment. Verified instead via: `npx tsc --noEmit` (0 errors, project-wide, confirmed twice),
and a manual line-by-line read-through of both new components against the exact request/response
shapes in `go/internal/admin/service/llm_provider_keys.go` and `llm_providers.go` (field names,
nullability, the `ListForTenant`/`UpsertForTenant` merge behavior that guarantees `default_model`
is always non-empty even before a tenant has its own override row, so the checklist's "Save
allowed models" call never hits the `default_model is required` 400). Recommend an actual
logged-in-browser pass before trusting this fully — in particular the Refresh-from-provider round
trip against a real provider API key, which no test in this repo exercises end-to-end.

**Step 5 — COMPLETE (2026-09-23).** `them.tenant_system_agent_config` table +
classifier/card_synthesizer general-vs-custom wiring. Commit `283db880`.

New migration `db/106_tenant_system_agent_config.sql` (applied live this session): one row per
`(tenant_id, role)`, `mode` CHECK IN ('general','custom'), `provider_name`/`key_id` for general
mode, `custom_provider`/`custom_model`/`custom_api_key_encrypted`/`custom_base_url`/
`custom_system_prompt` for custom mode. RLS + grants + FK `ON DELETE SET NULL` from `key_id` to
`them.llm_provider_keys` (verified live: deleting a key the config points at nulls `key_id` rather
than leaving a dangling reference or blocking the delete).

**Resolution logic** — new `go/internal/admin/system_agent_resolve.go`, `resolveSystemAgentRole`:
- No row, or a `mode='custom'` row with unset custom fields → falls back to the platform-global
  `them.config['system_agents']` row, unchanged prior behavior.
- `mode='general'` → resolves through the tenant's own `them.llm_providers` (`provider_name`) +
  `them.llm_provider_keys` (`key_id`, or that provider's default key when `key_id` is nil).
  **Hard rule enforced here, concretely:** no usable tenant key → resolution fails outright: this
  function never falls back to a platform key for a "general" mode resolution, even when one is
  configured and would otherwise work. Proven by a dedicated test
  (`TestResolveSystemAgentRole_GeneralMode_NoUsableKey_NeverFallsBackToPlatform`) and again through
  `classifyAgent`/`synthesizeAppCard` directly, not just the resolver in isolation.
- `mode='custom'` → decrypts and uses its own stored fields directly.

**`classifyAgent` was hardcoded to call the Anthropic Messages API directly** — a real gap, since
"general" mode can resolve to any provider the tenant has configured (OpenAI, Groq, a custom
base_url), not just Anthropic. Fixed by extracting `dispatchLLMText` out of `synthesize.go`'s
existing multi-provider dispatch (`callSynthesizerLLM` already had this logic for
`card_synthesizer`; `classify.go` just never had it) — both roles now share one dispatch path.
Both `classifyAgent` and `synthesizeAppCard` gained a `tenantID` parameter; both call sites already
had a tenant ID one line away or unconditionally in scope (confirmed by the original research —
`ep_discover.go` had it unconditionally; `agents.go`'s `Discover` handler had it gated inside an
`if authToken == ""` block, hoisted to the top since the route is always tenant-scoped).

**New tenant self-service route:** `GET`/`PUT /admin/my/system-agents/{role}/config`
(`go/internal/admin/tenant_system_agent_config.go`), mirroring the `/admin/my/llm-providers` naming
pattern. `GET` with no row returns 200 with `mode: "custom"` (the implicit default), not 404 — a
tenant that never opts in sees the same shape as one that explicitly chose custom with nothing set.

**Found and closed a pre-existing test-coverage gap, not introduced by this change:** neither
`classifyAgent` nor `synthesizeAppCard` had any direct test before this session — confirmed by grep
before writing new tests, not assumed. Both now do (4 tests each), on top of 8 resolver tests, 11
service tests, 8 handler tests, and 6 DAL integration tests against live Postgres — 41 new tests
total, all passing, `go test ./...` 0 failures full suite, `go test -race ./internal/admin/...`
clean.

**Step 6 — COMPLETE (2026-09-23).** Frontend General/Custom switch on the classifier and
card_synthesizer role cards.

**Design confirmed with the user before implementing:** the General/Custom switch is inherently
tenant-scoped (mode "general" means "use *my own* LLM Providers config"), but the existing System
Agents tab and its `RoleCard.tsx` are wired to the platform-global `/admin/system-agents` route
(super_admin only). A plain tenant admin opening that tab today gets a failed fetch and an
"unavailable" banner — confirmed as a pre-existing gap, not something to paper over. Resolved by
branching the tab's render on role: super_admin keeps today's `RoleCard` unchanged; tenant admin
now gets a new `TenantRoleCard`, backed entirely by step 5's tenant-scoped route. The
platform-global `getSystemAgents()` fetch on page load is now skipped entirely for tenant admins
(previously fired unconditionally and always failed for them) — closes the false "unavailable"
banner as a side effect, not the main goal.

Confirmed with the user: **classifier and card_synthesizer, when a tenant sets "general" mode, run
using that tenant's own API key against that tenant's own workspace only** — resolution is
tenant-scoped end-to-end (step 5's `resolveSystemAgentRole`, keyed by `tenantID` at every DB
lookup), and results (agent category/icon, synthesized entry-point card) are only ever written to
that same tenant's own rows. No cross-tenant reads, no shared state between tenants' general-mode
calls.

**Backend addition needed to keep "Custom" mode fully self-contained:** the plan's step 6 spec
said Custom mode gets "+ Test button, unchanged" — but the existing test route
(`POST /admin/system-agents/{role}/test-llm`) is mounted under the super_admin-only group, so a
tenant admin can't call it for their own custom config. Added a tenant-scoped mirror,
`POST /admin/my/system-agents/{role}/test-llm` (`TenantSystemAgentConfigHandler.Test`, new service
method `ResolveCustomTestInputs`) — same `probeLLMWithBase` call, but gap-fills from the tenant's
own stored `custom_*` fields when the request body omits one, never from the platform config.

**Frontend:**
- New `frontend/src/app/admin/settings/TenantRoleCard.tsx` — General/Custom segmented switch.
  General: provider dropdown (from `listMyLLMProviders()`, filtered to `enabled`) → key dropdown
  (from `listProviderKeys(provider)`, "use default key" as the no-selection option) — no free-text
  fields, no test button (testing happens at the key level in the LLM Providers tab, per the plan).
  Custom: provider/model/api_key/base_url/system_prompt form + Test button, same shape as the
  platform's `RoleCard`, wired to the new tenant-scoped test route and the masked-key display from
  `TenantSystemAgentConfigOut.custom_api_key_masked`.
- `frontend/src/app/admin/settings/page.tsx`: `system_agents` tab now renders `RoleCard` for
  super_admin, `TenantRoleCard` for everyone else; the platform-global config fetch on mount is
  gated behind `isSuperAdmin`.
- `apiTypes.ts`/`api.ts`: `TenantSystemAgentConfigOut`/`In`/`TestInput` types, `themApi.
  getTenantSystemAgentConfig`/`putTenantSystemAgentConfig`/`testTenantSystemAgentLlm`.

Tests: 4 new service tests (`ResolveCustomTestInputs`), 3 new handler tests (the `Test` route) —
`go/TEST_INDEX.md` S1-147, S1-148. `go test ./...` 0 failures full suite. `npx tsc --noEmit` 0
errors. `them-go-bridge` rebuilt (Dockerfile runs the full suite at build time — 0 failures
confirmed again in-image) and force-recreated; confirmed healthy startup via logs, no crash loop.
`them-frontend` picked up the new component via its existing bind-mount + `npm run dev` hot reload
— confirmed compiling with 0 errors via container logs, and `/admin/settings` already served a 200
in those logs before this write-up (a live user request, not a check I ran).

**Not live-verified end-to-end this session** — no browser-automation tool was available, and per
the user's earlier standing preference this session did not probe the live API directly with
`curl` either. The new tenant route was only exercised through Go handler tests with a fake DB.
Recommend an actual logged-in tenant-admin click-through (switch to General, pick a provider/key,
save, reload and confirm it persisted; switch to Custom, test a key, save) before trusting this
fully — nothing in this repo's test suite drives that real round trip yet.

**Not yet built:** the platform-admin-on-tenant route mirror for keys
(`/admin/tenants/{id}/llm-providers/{name}/keys...`, from step 3) remains not built — still not
needed by anything.

---

## Step 7 — final full-suite pass + sign-off — COMPLETE (2026-09-23)

`go test ./...` — 0 failures, full suite, run fresh at sign-off time (not reused from an earlier
step's run).

`go test -race ./...` — found one failure, **confirmed pre-existing and unrelated**:
`internal/llmgateway.TestHandler_Stream_200` fails intermittently (reproduced 1-of-3 runs in
isolation) with a genuine data race in `Service.Stream`'s goroutine. `git log` on
`internal/llmgateway/handler.go` shows it was last touched by commit `936ae696` (LLM Gateway
Phase 2) — weeks before this plan started, and no commit in this plan (`465ffe93` through
`220adc05`) touches `internal/llmgateway` at all. Not fixed as part of this plan — flagged in
`go/TEST_INDEX.md` (`flaky (pre-existing)` row) as a dedicated follow-up, since fixing an unrelated
package's race is out of scope for a plan that never touched it.

`npx tsc --noEmit` — 0 errors, frontend, run fresh at sign-off time.

**Final state of all 7 steps:**
1. Migration (`db/105`, `db/106`) — done.
2. DAL + service layers — done.
3. Test-key/list-models HTTP endpoints — done.
4. Frontend LLM Providers tab (model checklist, refresh, named-keys sub-section) — done.
5. `them.tenant_system_agent_config` + classifier/card_synthesizer resolution wiring — done.
6. Frontend General/Custom switch on the role cards — done.
7. This sign-off — done.

**What was never verified across any step of this plan, by any session:** a real logged-in
browser click-through, or a direct `curl` round trip against the live stack. No
browser-automation tool was available in any session that worked on this plan; direct API probing
was attempted once, declined by the user, and not repeated afterward per that standing signal.
Every verification claim in this document is either a unit/integration test (many run for real
against live Postgres) or a container-log observation (compile success, healthy startup, one
real incidental user request in `them-frontend`'s logs) — never an end-to-end browser or curl
check performed by the assistant. **Recommend a real logged-in walkthrough before trusting this
feature in front of actual tenants**, covering at minimum: enabling a provider and saving allowed
models, adding/testing/deleting a named key, setting a role to general mode and confirming a real
classify/synthesize call uses that tenant's own key, and setting a role to custom mode with its
own key and testing it.
