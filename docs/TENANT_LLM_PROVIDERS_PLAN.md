# Tenant-Level LLM Provider Configuration — Plan
# Status: approved design, implementing — steps 1-3 of 7 complete
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

**Not yet built (steps 4-6 remain):** frontend UI (model checklist + refresh button + keys
sub-section), the platform-admin-on-tenant route mirror (if ever needed),
`them.tenant_system_agent_config` table + classifier/card_synthesizer general-vs-custom wiring.
