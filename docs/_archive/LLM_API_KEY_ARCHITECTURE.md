# LLM API Key Architecture — Multi-Provider Keys with Per-EP Config

Status: recommendation (design only, no code)
Date: 2026-08-17
Scope: how API keys are stored and resolved when a single orchestrator serves multiple
entry points, each with its own provider+model.

---

## TL;DR

Keys are a **platform concern**, not an app/canvas concern. We already have the right table:
`them.llm_providers` holds exactly one encrypted key per provider (`api_key_encrypted`).
Make that the single source of truth for credentials. The canvas doc and the orchestrator
row store only **provider + model** (non-secret references); the runtime joins provider name →
`them.llm_providers.api_key_encrypted` at session time. No new secrets column, no keys in JSONB.

---

## 1. The right data model

Three distinct abstraction levels, each owning exactly what it should:

| Level | Stores | Secret? |
|---|---|---|
| **Platform** — `them.llm_providers` | one encrypted key per provider (`anthropic`, `openai`, …) | **yes** — `api_key_encrypted` |
| **Orchestrator** — `them.orchestrators` | the orchestrator's *default* provider + model | no |
| **Canvas doc** — `app_definitions.doc` | per-EP override: `{ "provider": "...", "model": "..." }` | no |

Do **not** put per-provider keys on the orchestrator row. An orchestrator that serves
EP-1→Anthropic and EP-2→OpenAI does not need to "own" two keys — both keys already live in
`them.llm_providers`, keyed by provider name. The orchestrator only needs to know *which
provider name* to look up, and the canvas doc supplies that per EP.

This means the existing `them.orchestrators.llm_api_key_encrypted` column becomes **legacy/optional
override**, not the primary path. Most orchestrators use one provider for all EPs, so the default
`(llm_provider, llm_model)` on the row + platform key is all they need — the single-provider flow
is untouched.

## 2. Schema change

**Minimal — effectively none required.** `them.llm_providers` already has `name` (unique),
`api_key_encrypted`, `base_url`, `default_model`, `enabled`. That is the full credential store.

Two small, optional additions for correctness:

1. `them.orchestrators.llm_api_key_encrypted` → keep, but redefine semantics: **per-orchestrator
   override**. NULL/empty means "use the platform key for this provider." No DDL needed; only a
   documented precedence rule (below).
2. (Optional, later) a `them.llm_providers` uniqueness guarantee on `name` already exists — good.
   If we ever want per-tenant keys, add `tenant_id` to `llm_providers` and a partial unique index
   `(tenant_id, name)`. **Do not build this now** — single shared platform keys are correct for a
   small team.

## 3. Canvas UI — surfacing key config without leaking secrets

The canvas orchestrator panel already renders a per-EP section with **provider + model** selects.
Keep that exactly as is — it writes only `ep_llm[ep_id] = {provider, model}` into the doc.

For keys, the UI does **not** put a key field in the EP section. Instead:

- The **provider dropdown** is populated from `GET them.llm_providers` (enabled rows). Each option
  shows the provider name plus a **key-status badge**: `key set` (green) when
  `api_key_encrypted` is non-null, `no key` (amber) when null.
- If an admin selects a provider that has no platform key, show an inline warning with a link to
  **Settings → LLM Providers**, where keys are entered once, globally. That is the single place
  keys are ever typed.
- The existing **Test** button calls the runtime resolver (section 4) server-side, so it exercises
  the real key without ever returning it to the browser.

Result: zero new per-EP key UI, no key ever crosses into the doc or the browser, and the admin has
one obvious place to manage credentials.

## 4. Runtime resolution at session time

The Go runtime needs `(provider, model, apiKey, baseURL)`. Resolve in this precedence order,
per EP, at connection/session start (this is the natural extension of `internal/epconfig`):

```
1. provider, model  ← app_definitions.doc.ep_llm[ep_id]          (canvas per-EP override)
2. if absent        ← them.orchestrators.(llm_provider, llm_model) (orchestrator default)
3. apiKey, baseURL  ← them.llm_providers WHERE name = provider    (platform key, decrypted)
4. apiKey override  ← them.orchestrators.llm_api_key_encrypted    (ONLY if non-null — rare)
```

Concretely, add a small resolver alongside `epconfig` (e.g. `internal/llmresolve`) that returns a
typed `ResolvedLLM{Provider, Model, APIKey, BaseURL}`. Decryption uses the existing
`internal/crypto.DecryptStored`. The provider factory (`internal/llm`) is constructed **per resolved
config**, replacing the current hard-wired `cfg.AnthropicAPIKey` path in `go/cmd/worker/main.go` —
that env key becomes only a dev/bootstrap fallback, not the production source.

Cache the decrypted key in-process with a short TTL (mirror `epconfig`'s 30s + pub/sub invalidation
on `them:ep:config:changed` / a new `them:provider:key:changed` channel) so we do not decrypt on
every message. Never log the key; use `cfg.SafeString()` conventions.

Fail-closed: if the resolved provider has no key, reject the session with a clear config error —
do not silently fall back to the mock provider in production.

## 5. Migration path from the single-key model

No destructive migration; the change is additive and backward-compatible.

1. **Backfill platform keys.** For each distinct provider currently used by any orchestrator, ensure
   a `them.llm_providers` row exists with `api_key_encrypted` set. A one-time script copies each
   orchestrator's `llm_api_key_encrypted` into the matching provider row where the provider row's key
   is still null. (Manual review for conflicts; small team, small N.)
2. **Redefine orchestrator key as override.** Leave existing `llm_api_key_encrypted` values in place —
   they keep working via precedence step 4. New orchestrators created through the canvas simply leave
   it null and inherit the platform key.
3. **Switch the worker.** Replace the `cfg.AnthropicAPIKey`-only construction in
   `go/cmd/worker/main.go` with the resolver from section 4. Keep `ANTHROPIC_API_KEY` as a dev
   fallback only.
4. **Deprecate gradually.** Once all orchestrators resolve cleanly via platform keys, the
   `orchestrators.llm_api_key_encrypted` column can be dropped in a later migration. Not urgent.

---

## What we explicitly are NOT doing

- **No keys in `app_definitions.doc`** — the doc stays fully non-secret and safe to export/import.
- **No per-EP key UI** — keys are entered once in Settings → LLM Providers.
- **No separate provider-credentials table** — `them.llm_providers` already is that table.
- **No per-tenant keys yet** — single shared platform keys; add `tenant_id` only if a real tenant
  isolation requirement appears.
- **No breaking of the single-provider flow** — an orchestrator with one provider for all EPs needs
  zero canvas config; the orchestrator default + platform key just works.
