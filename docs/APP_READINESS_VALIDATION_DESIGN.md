# App Readiness Validation — Design
# Last updated: 2026-09-13

---

## Problem

Today an application can be enabled and accept real traffic even when:

1. No LLM provider/model is configured on the orchestrator or EP
2. No API key is set on the application
3. The platform silently falls back to a **platform-level** Anthropic key with no tenant awareness

This means:
- The platform pays LLM bills on behalf of tenants who forgot to configure their app
- There is no signal to the tenant that their app is misconfigured
- Platform-level keys (`them.llm_providers` with `tenant_id IS NULL`) are used for tenant workloads — which is wrong

---

## What should happen

### Rule 1 — No platform-level key fallback for tenant workloads

`them.llm_providers` rows with `tenant_id IS NULL` must only be used for internal platform operations:
- Agent discovery
- Security scanning
- Card synthesis
- Other system agents

They must **never** be used as a fallback when a tenant app has no key configured. If no tenant key resolves, the run must fail with a clear error.

### Rule 2 — App cannot be enabled without mandatory runtime config

When a tenant tries to enable an app (toggle enabled=true), the platform must validate:

1. At least one orchestrator is connected to at least one EP
2. The orchestrator has `llm_provider` and `llm_model` set (or the EP overrides both)
3. A provider key exists for the configured provider — either:
   - On the application (`applications.provider_keys`)
   - Or on the tenant (`llm_providers` with this `tenant_id`)
4. If memory is enabled: `summarizer_provider`, `summarizer_model`, and a key for that provider must be set

If any check fails, the enable is rejected with a specific error message naming exactly what is missing.

### Rule 3 — Runtime config fields drive validation

The Canvas "Runtime" tab exposes provider/model/key fields. These are the source of truth. Validation must be derived from what is actually configured there — not from a hardcoded list. If a new field is added to runtime config, validation picks it up automatically because validation reads the same fields the worker reads.

---

## Current key resolution chain (to be changed)

```
orchestrator.llm_provider / llm_model
  → EP.llm_provider / llm_model (override)
  → hardcoded default "anthropic" / empty model   ← REMOVE
  → llm_routing config (model fallback)           ← REMOVE for tenant runs
  → tenant llm_providers row                      ← keep
  → platform llm_providers row (tenant_id IS NULL) ← REMOVE for tenant runs
  → application.provider_keys                     ← keep
  → env var ANTHROPIC_API_KEY                     ← REMOVE for tenant runs
```

**After fix:**
```
orchestrator.llm_provider / llm_model (required — fail if missing)
  → EP.llm_provider / llm_model (optional override)
  → tenant llm_providers row (key lookup)
  → application.provider_keys (key lookup)
  → fail with clear error if no key found
```

---

## Where to implement

### Validation — `go/internal/admin/service/applications.go`

Add `validateReadiness(ctx, appID, tenantID) error` called from:
- `SetEntryPointEnabled(..., enabled=true)`
- `Update(..., enabled=true)` (app-level enable)
- Any Canvas "Save & Enable" path

### Worker key resolution — `go/internal/temporal/workerconfig/loader.go`

- Remove the hardcoded `providerName = "anthropic"` default (line 248-249)
- Remove the platform-default fallback in `loadTenantProviderKey` (line 496-497)
- Return a typed error (`ErrNoProviderKey`) when no key resolves, so the run fails cleanly rather than using a wrong key

### Platform-only operations

System agents (discovery, security scan, card synthesis) call the worker config loader with a flag or a separate loader that is allowed to use platform keys. Tenant runs use a strict loader that does not fall back to platform keys.

---

## Error messages (tenant-facing)

| Missing | Error |
|---|---|
| No orchestrator connected | "This app has no orchestrator. Connect one in the Canvas before enabling." |
| No LLM provider/model | "Orchestrator has no LLM configured. Set provider and model in Runtime settings." |
| No API key for provider | "No API key found for {provider}. Add one in Runtime → Provider Keys." |
| Memory on, no summarizer | "Memory is enabled but no summarizer provider/model is set." |

---

## What NOT to do

- Do not add a separate "validate" button — validation runs automatically on enable
- Do not silently fall back to any platform key for tenant runs
- Do not hardcode provider names anywhere in the worker config loader
- Do not block app creation or draft editing — only block enabling

---

## Implementation order

1. Fix `workerconfig/loader.go` — remove platform fallbacks, return error on missing key
2. Add `validateReadiness` in `applications.go` service
3. Wire validation into enable paths (EP enable + app enable)
4. Add frontend error display on enable failure
5. Add tests for each failure case
