# LLM Orchestrator Config Redesign — Implementation Plan

**Last updated:** 2026-08-20

## 1. Summary

App Runtime becomes the single place for all LLM configuration. API keys stay in `applications.provider_keys` (encrypted). Provider + model assignment for each orchestrator moves from the canvas properties panel into a new "LLM Objects" section in Runtime. The canvas properties panel drops its `llm_provider`/`llm_model` fields.

---

## 2. Backend Changes

### 2a. DAL — `go/internal/admin/dal/applications.go`

```go
// SetOrchestratorLLM updates llm_provider + llm_model on one app_orchestrators row.
// Scoped to appID so a caller cannot modify an orchestrator from another app.
func (d *DB) SetOrchestratorLLM(ctx context.Context, appID, orchID, provider, model string) error
// SQL: UPDATE them.app_orchestrators SET llm_provider=$3, llm_model=$4
//      WHERE id=$1::uuid AND application_id=$2::uuid
```

Add `SetOrchestratorLLM` to the `Dal` interface in `service/interfaces.go`.

### 2b. Service — `go/internal/admin/service/applications.go`

```go
// SetOrchestratorLLM validates the provider has a key set, then delegates to DAL.
func (s *AppService) SetOrchestratorLLM(ctx context.Context, tenantID, appID, orchID, provider, model string) error
```

Validation steps:
1. `provider` must be in `validProviders`
2. `model` must not be empty
3. `GetProviderKeys(ctx, tenantID, appID)` → find entry where `Provider == provider && KeySet == true`; if not found return `ErrUnprocessable("no key stored for provider " + provider)`
4. Call `d.SetOrchestratorLLM(ctx, appID, orchID, provider, model)`
5. Return DAL error as-is (pgx no-rows → not found)

### 2c. Handler — `go/internal/admin/applications.go`

```go
// PatchOrchestratorLLM handles PATCH /api/v1/admin/applications/{id}/orchestrators/{orch_id}/llm
// Body: {"provider": "anthropic", "model": "claude-haiku-4-5-20251001"}
// Returns: {"id": "...", "name": "...", "llm_provider": "anthropic", "llm_model": "claude-haiku-4-5-20251001"}
func (h *ApplicationsHandler) PatchOrchestratorLLM(w http.ResponseWriter, r *http.Request)
```

### 2d. Router — `go/internal/admin/applications.go` `Routes()`

Inside the existing `r.Route("/applications/{id}", ...)` sub-tree:

```go
app.Patch("/orchestrators/{orch_id}/llm", h.PatchOrchestratorLLM)
```

### 2e. workerconfig safety net — `go/internal/temporal/workerconfig/loader.go`

In `LoadRunConfig`, change the provider resolution from:
```go
if providerName != "" {
    apiKey, _ = l.loadProviderKey(ctx, applicationID, providerName)
}
```
to:
```go
lookupProvider := providerName
if lookupProvider == "" {
    lookupProvider = "anthropic" // safety net: try app key even if orch has no provider set
}
apiKey, _ = l.loadProviderKey(ctx, applicationID, lookupProvider)
```

---

## 3. Frontend Changes

### 3a. RuntimeView — `frontend/src/app/admin/applications/page.tsx`

**Section 1 — API Keys (existing + Test button)**

Each provider row gains a **Test** button:
- Calls `POST /api/v1/admin/applications/{id}/test-llm` with `{provider, model: defaultModelFor(provider)}`
- Default models: `anthropic → claude-haiku-4-5-20251001`, `openai → gpt-4o-mini`, `groq → llama3-8b-8192`, `gemini → gemini-1.5-flash`
- Show inline: green "✓ {latency}ms" on success, red error message on failure
- Button disabled if `!keySet`

```ts
const DEFAULT_MODELS: Record<string, string> = {
  anthropic: 'claude-haiku-4-5-20251001',
  openai: 'gpt-4o-mini',
  groq: 'llama3-8b-8192',
  gemini: 'gemini-1.5-flash',
}
```

**Section 2 — LLM Objects (new)**

Data source: `app.app_orchestrators` (already returned by `GET /admin/applications/{id}`).

Rendered as a list. Each row:
```
[display_name or name]  Provider: [dropdown]  Model: [input]  [Save]
```

- Provider dropdown options: only providers where `keySet === true` in Section 1 state
- Model input: free text (no server-side validation — user can type any model name)
- Save calls `PATCH /api/v1/admin/applications/{id}/orchestrators/{orch_id}/llm`
- Per-row save state: idle / saving / saved / error
- If `providerKeyStatuses` is empty (no keys set): show grey message "Set an API key above first"

New API helper in `frontend/src/lib/api.ts`:
```ts
patchOrchestratorLLM(appId: string, orchId: string, provider: string, model: string): Promise<void>
```

### 3b. Canvas properties panel — `frontend/src/app/admin/applications/page.tsx`

Remove the `llm_provider` and `llm_model` fields from the orchestrator properties panel (around lines 4663–4674). They are replaced by Runtime.

---

## 4. Implementation Order

1. **DAL** — `SetOrchestratorLLM` + interface update
2. **Service** — `SetOrchestratorLLM` with validation
3. **Handler + Router** — `PatchOrchestratorLLM` endpoint
4. **workerconfig** — safety net default provider
5. **Frontend** — Test button in Section 1
6. **Frontend** — LLM Objects section (Section 2)
7. **Frontend** — remove canvas llm_provider/llm_model fields
8. **Tests** — see section below

---

## 5. Tests Required

| Test | File |
|---|---|
| `TestSetOrchestratorLLM_ValidProviderWithKey` | `service/applications_wave8_test.go` (or new file) |
| `TestSetOrchestratorLLM_UnknownProvider` | same |
| `TestSetOrchestratorLLM_NoKeyStored` | same — should return ErrUnprocessable |
| `TestSetOrchestratorLLM_EmptyModel` | same |
| `TestPatchOrchestratorLLM_HTTP_200` | `admin/applications_wave8_test.go` |
| `TestPatchOrchestratorLLM_HTTP_422_NoKey` | same |
| `TestWorkerconfig_DefaultProviderFallback` | `workerconfig/loader_test.go` — construction only (no live DB) |

TEST_INDEX.md must be updated in the same commit as the tests (CLAUDE.md rule).
