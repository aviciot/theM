# App-Level Global Named Parameters — Implementation Plan

**Status:** Design  
**Date:** 2026-08-25  
**Depends on:** db/038_app_agent_params.sql (merged), agentgen v1 (spec/compiler/interpreter complete)

---

## 1. Overview

This document specifies the end-to-end implementation of **App-Level Global Named Parameters** (`app_params`). The feature adds a pool of named parameters defined at the application level. Canvas agent nodes can reference a parameter by name at build time (an `app_param_ref`); the runtime resolves the reference to the plaintext value at execution time via a new `InvocationContext.AppGlobalParams` map.

### 1.1 What already exists (do not change)

| Feature | Storage | Runtime field |
|---|---|---|
| LLM provider keys | `applications.provider_keys` JSONB | `InvocationContext.AppAPIKey map[string]string` |
| Per-binding agent params | `app_agent_bindings.agent_params` JSONB | `InvocationContext.AgentParams map[string]string` |

Both paths remain 100% unchanged. This feature is purely additive.

### 1.2 What we are building

| Concern | New artefact |
|---|---|
| DB storage | `applications.app_params` JSONB column |
| Backend types | `AppGlobalParam` in `go/internal/admin/dal/` |
| DAL | `GetAppParams`, `SetAppParam`, `DeleteAppParam` |
| Service | `GetAppParams`, `SetAppParam`, `DeleteAppParam`, `GetPlaintextAppParams` |
| REST endpoints | `GET/PUT/DELETE /admin/applications/{id}/app-params/{name}` |
| Spec type | `AgentAppParamRefSpec` + `AgentAppParamRefs` field on `AgentSpec` |
| Compiler | `collectAppParamRefs` in `agentgen/compiler.go` |
| Interpreter | `app_param_ref` resolution in `execHTTP`, `execLLM` |
| Runtime | `loadAppGlobalParams` in `go/cmd/agent-runtime/main.go` |
| Context | `AppGlobalParams map[string]string` field on `InvocationContext` |
| Frontend types | `AppGlobalParam` interface in `frontend/src/lib/api.ts` |
| Frontend API | `getAppParams`, `setAppParam`, `deleteAppParam` |
| Frontend UI | "App Parameters" section in `RuntimeView.tsx` |
| Frontend canvas | `app_param_ref` picker in `RightPanel.tsx` |

---

## 2. Phase 1 — Database

### 2.1 Migration file

File: `db/045_app_global_params.sql` (use the next sequential number).

```sql
-- db/045_app_global_params.sql
-- Adds app_params JSONB to applications for app-level global named parameters.
-- Modelled after provider_keys: AES-GCM encrypted secrets, plaintext non-secrets.
--
-- Storage format (one key per named param):
--   Secrets:     {"geoapify_key": {"ct": "enc:...", "hint": "XXXX"}}
--   Non-secrets: {"target_city":  "Tel Aviv"}
--
-- hint = last 4 chars of plaintext (extracted before encryption, like provider_keys).

ALTER TABLE them.applications
    ADD COLUMN IF NOT EXISTS app_params JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN them.applications.app_params IS
    'App-level global named parameters. Secrets: {"name": {"ct": "enc:...", "hint": "XXXX"}}. '
    'Non-secrets: {"name": "value"}. Encrypted with the platform crypto key.';
```

### 2.2 JSONB shape

```json
{
  "geoapify_key":  {"ct": "enc:BASE64...", "hint": "x3f9"},
  "openai_key":    {"ct": "enc:BASE64...", "hint": "sk-x"},
  "target_city":   "Tel Aviv"
}
```

Rules:
- Secret params (`type == "secret"`) → `{"ct": "enc:...", "hint": "<last-4>"}` — same shape as `providerKeyEntry` already in `service/applications.go`.
- Non-secret params → plain JSON string.
- Key names: `^[a-z0-9_]{1,64}$`.

---

## 3. Phase 2 — Go Backend

### 3.1 DAL type (`go/internal/admin/dal/`)

Add alongside existing application types (e.g. in `dal.go` or `applications.go`):

```go
// AppGlobalParam is one entry in applications.app_params JSONB.
// Type is "secret" | "string" | "url" | "int" | "bool".
// For secrets, ValueHint holds the last 4 chars of the plaintext; Value is always empty.
// For non-secrets, Value holds the plaintext; ValueHint is empty.
type AppGlobalParam struct {
    Name      string `json:"name"`
    Type      string `json:"type"`
    IsSet     bool   `json:"is_set"`
    ValueHint string `json:"value_hint,omitempty"`
    Value     string `json:"value,omitempty"`
}
```

### 3.2 DAL methods (`go/internal/admin/dal/applications.go`)

Add three methods to `*DB`, following the same patterns as `GetProviderKeys`, `SetProviderKey`, `DeleteProviderKey`:

```go
func (d *DB) GetAppParams(ctx context.Context, tenantID, appID string) ([]byte, error) {
    const q = `SELECT COALESCE(app_params, '{}')
               FROM them.applications
               WHERE id=$1::uuid AND tenant_id=$2::uuid`
    var raw []byte
    if err := d.q.QueryRow(ctx, q, appID, tenantID).Scan(&raw); err != nil {
        return nil, err
    }
    return raw, nil
}

func (d *DB) SetAppParam(ctx context.Context, tenantID, appID, name string, valueJSON []byte) error {
    const q = `
        UPDATE them.applications
        SET app_params = jsonb_set(COALESCE(app_params,'{}'), $3::text[], $4::jsonb, true),
            updated_at = now()
        WHERE id=$1::uuid AND tenant_id=$2::uuid`
    return d.q.Exec(ctx, q, appID, tenantID, "{"+name+"}", valueJSON)
}

func (d *DB) DeleteAppParam(ctx context.Context, tenantID, appID, name string) error {
    const q = `
        UPDATE them.applications
        SET app_params = app_params - $3,
            updated_at = now()
        WHERE id=$1::uuid AND tenant_id=$2::uuid`
    return d.q.Exec(ctx, q, appID, tenantID, name)
}
```

### 3.3 Dal interface (`go/internal/admin/service/service.go`)

Add to the `Dal` interface (near the provider-keys comment block):

```go
GetAppParams(ctx context.Context, tenantID, appID string) ([]byte, error)
SetAppParam(ctx context.Context, tenantID, appID, name string, valueJSON []byte) error
DeleteAppParam(ctx context.Context, tenantID, appID, name string) error
```

### 3.4 Service methods (`go/internal/admin/service/applications.go`)

The service reuses the existing `encryptKey`, `decryptKey`, and `providerKeyEntry` helpers — no new crypto code needed. The `providerKeyEntry` struct (`{"ct":"...","hint":"..."}`) is the exact same format used for secrets in `app_params`.

```go
var appParamNameRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

var validAppParamTypes = map[string]struct{}{
    "secret": {}, "string": {}, "url": {}, "int": {}, "bool": {},
}

type AppGlobalParamUpsertInput struct {
    Value string `json:"value"`
    Type  string `json:"type"`
}

func (s *AppService) GetAppParams(ctx context.Context, tenantID, appID string) ([]dal.AppGlobalParam, error) {
    raw, err := s.dal.GetAppParams(ctx, tenantID, appID)
    if err != nil {
        if dal.IsNoRows(err) {
            return nil, ErrNotFound
        }
        return nil, err
    }
    return parseAppParams(raw)
}

func (s *AppService) SetAppParam(ctx context.Context, tenantID, appID, name string, input AppGlobalParamUpsertInput) error {
    if !appParamNameRe.MatchString(name) {
        return validation("app param name must match ^[a-z0-9_]{1,64}$")
    }
    if _, ok := validAppParamTypes[input.Type]; !ok {
        return unprocessable("unsupported param type: " + input.Type)
    }
    if input.Value == "" {
        return validation("value must not be empty")
    }
    var valueJSON []byte
    if input.Type == "secret" {
        hint := input.Value
        if len(hint) > 4 {
            hint = hint[len(hint)-4:]
        }
        ct, err := s.encryptKey(input.Value)
        if err != nil {
            return fmt.Errorf("encrypt app param: %w", err)
        }
        entry := providerKeyEntry{CT: ct, Hint: hint}
        valueJSON, err = json.Marshal(entry)
        if err != nil {
            return err
        }
    } else {
        var err error
        valueJSON, err = json.Marshal(input.Value)
        if err != nil {
            return err
        }
    }
    return s.dal.SetAppParam(ctx, tenantID, appID, name, valueJSON)
}

func (s *AppService) DeleteAppParam(ctx context.Context, tenantID, appID, name string) error {
    if !appParamNameRe.MatchString(name) {
        return validation("invalid app param name")
    }
    return s.dal.DeleteAppParam(ctx, tenantID, appID, name)
}

// GetPlaintextAppParams decrypts all app-level params and returns a name→value map.
// Used by the runtime loader only — never exposed via HTTP.
func (s *AppService) GetPlaintextAppParams(ctx context.Context, tenantID, appID string) (map[string]string, error) {
    raw, err := s.dal.GetAppParams(ctx, tenantID, appID)
    if err != nil {
        return nil, err
    }
    return decryptAppParams(raw, s.decryptKey)
}
```

Internal helpers (add alongside `parseProviderKeys`):

```go
func parseAppParams(raw []byte) ([]dal.AppGlobalParam, error) {
    var top map[string]json.RawMessage
    if err := json.Unmarshal(raw, &top); err != nil {
        return nil, fmt.Errorf("parse app_params: %w", err)
    }
    out := make([]dal.AppGlobalParam, 0, len(top))
    for name, valRaw := range top {
        var entry providerKeyEntry
        if json.Unmarshal(valRaw, &entry) == nil && (entry.CT != "" || entry.Hint != "") {
            out = append(out, dal.AppGlobalParam{
                Name:      name,
                Type:      "secret",
                IsSet:     entry.CT != "",
                ValueHint: entry.Hint,
            })
            continue
        }
        var s string
        if json.Unmarshal(valRaw, &s) == nil {
            out = append(out, dal.AppGlobalParam{
                Name:  name,
                Type:  "string",
                IsSet: s != "",
                Value: s,
            })
        }
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out, nil
}

func decryptAppParams(raw []byte, decryptFn func(string) (string, error)) (map[string]string, error) {
    var top map[string]json.RawMessage
    if err := json.Unmarshal(raw, &top); err != nil {
        return map[string]string{}, nil
    }
    out := make(map[string]string, len(top))
    for name, valRaw := range top {
        var entry providerKeyEntry
        if json.Unmarshal(valRaw, &entry) == nil && entry.CT != "" {
            plain, err := decryptFn(entry.CT)
            if err != nil {
                slog.Warn("app-params: decryption failed", "name", name)
                continue
            }
            out[name] = plain
            continue
        }
        var s string
        if json.Unmarshal(valRaw, &s) == nil {
            out[name] = s
        }
    }
    return out, nil
}
```

### 3.5 REST handler (`go/internal/admin/applications.go`)

Mount in `Routes()`:

```go
app.Get("/app-params", h.GetAppParams)
app.Put("/app-params/{name}", h.SetAppParam)
app.Delete("/app-params/{name}", h.DeleteAppParam)
```

Handler bodies follow the exact same pattern as `GetProviderKeys`, `SetProviderKey`, `DeleteProviderKey` already in the file. Response shape:

- `GET` → `[]AppGlobalParam` (200)
- `PUT` → `{"name":"...","updated":true}` (200)
- `DELETE` → `{"name":"...","deleted":true}` (200)
- Errors: 400 (validation), 404 (app not found), 422 (bad type), 500 (static string)

---

## 4. Phase 3 — Compiler

### 4.1 New spec types (`go/internal/agentgen/spec.go`)

Add to `AgentSpec`:

```go
// AgentAppParamRefs records which app-global params this agent's steps reference.
// Populated by the compiler. Empty when no step uses app_param_ref.
AgentAppParamRefs []AgentAppParamRefSpec `json:"agent_app_param_refs,omitempty"`
```

New type:

```go
// AgentAppParamRefSpec is the compiled record of one app_param_ref binding.
type AgentAppParamRefSpec struct {
    StepID    string `json:"step_id"`
    ParamName string `json:"param_name"`
}
```

Extend `HTTPStepConfig` (add after `InjectHeaderName`):

```go
// AppParamRef references an app-global param by name. Takes precedence over AppParamKey
// when both are set, giving a backward-compatible migration path.
AppParamRef string `json:"app_param_ref,omitempty"`
```

Extend `LLMStepConfig` (add after `ModelOverrideParamKey`):

```go
// ModelOverrideParamRef names an app-global param whose value overrides the compiled model.
ModelOverrideParamRef string `json:"model_override_param_ref,omitempty"`
```

### 4.2 `collectAppParamRefs` (`go/internal/agentgen/compiler.go`)

Add alongside `collectAgentParams`:

```go
func collectAppParamRefs(def *canvasDefinition) ([]AgentAppParamRefSpec, []Issue) {
    seen := make(map[string]bool)
    var refs []AgentAppParamRefSpec

    for _, cs := range def.Skills {
        for _, step := range cs.Steps {
            switch step.Type {
            case StepHTTP:
                var cfg HTTPStepConfig
                if len(step.Config) > 0 && json.Unmarshal(step.Config, &cfg) == nil && cfg.AppParamRef != "" {
                    k := step.ID + ":http:" + cfg.AppParamRef
                    if !seen[k] {
                        seen[k] = true
                        refs = append(refs, AgentAppParamRefSpec{StepID: step.ID, ParamName: cfg.AppParamRef})
                    }
                }
            case StepLLM:
                var cfg LLMStepConfig
                if len(step.Config) > 0 && json.Unmarshal(step.Config, &cfg) == nil && cfg.ModelOverrideParamRef != "" {
                    k := step.ID + ":llm:" + cfg.ModelOverrideParamRef
                    if !seen[k] {
                        seen[k] = true
                        refs = append(refs, AgentAppParamRefSpec{StepID: step.ID, ParamName: cfg.ModelOverrideParamRef})
                    }
                }
            }
        }
    }

    sort.Slice(refs, func(i, j int) bool {
        if refs[i].StepID != refs[j].StepID {
            return refs[i].StepID < refs[j].StepID
        }
        return refs[i].ParamName < refs[j].ParamName
    })
    return refs, nil
}
```

Wire into `Validate` and `CompileForPublish` immediately after `collectAgentParams`:

```go
appParamRefs, refIssues := collectAppParamRefs(def)
issues = append(issues, refIssues...)
```

Wire into `buildSpec` — add `appParamRefs` parameter and set `AgentAppParamRefs: appParamRefs`.

The compiler does **not** validate that referenced names exist in `app_params` — that would require a DB call and make the spec brittle to renames. Validation is deferred to runtime.

---

## 5. Phase 4 — Interpreter

### 5.1 `InvocationContext` (`go/internal/agentgen/context.go`)

Add one field after `AgentParams`:

```go
// AppGlobalParams holds the decrypted values of all app-level named params
// referenced by this agent's spec. Never logged or serialized.
AppGlobalParams map[string]string
```

### 5.2 HTTP step — `execHTTP` (`go/internal/agentgen/interpreter.go`)

Replace the existing injection switch with a shared helper `injectAuthParam`, then check `AppParamRef` before `AppParamKey`:

```go
// injectAuthParam injects paramVal into req using the specified inject mode.
func injectAuthParam(req *http.Request, mode, headerName, paramVal string) error {
    switch mode {
    case "header", "":
        req.Header.Set("Authorization", "Bearer "+paramVal)
    case "query":
        q := req.URL.Query()
        name := headerName
        if name == "" { name = "api_key" }
        q.Set(name, paramVal)
        req.URL.RawQuery = q.Encode()
    case "basic":
        req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(paramVal)))
    case "custom_header":
        if headerName == "" {
            return fmt.Errorf("inject_mode %q requires inject_header_name to be set", mode)
        }
        req.Header.Set(headerName, paramVal)
    default:
        return fmt.Errorf("unknown inject_mode %q", mode)
    }
    return nil
}
```

New resolution logic in `execHTTP` (replaces the existing `if cfg.AppParamKey != ""` block):

```go
// App param resolution — AppParamRef (global) takes precedence over AppParamKey (per-binding).
if cfg.AppParamRef != "" {
    paramVal := ""
    if ic.AppGlobalParams != nil {
        paramVal = ic.AppGlobalParams[cfg.AppParamRef]
    }
    if paramVal != "" {
        if err := injectAuthParam(req, cfg.InjectMode, cfg.InjectHeaderName, paramVal); err != nil {
            return err
        }
    } else if cfg.InjectMode != "" {
        return fmt.Errorf("step requires app param %q (app_param_ref) but param is not set", cfg.AppParamRef)
    }
    // InjectMode empty + no value → silently skip (param optional)
} else if cfg.AppParamKey != "" {
    // ... existing AppParamKey block unchanged, but replace inline switch with injectAuthParam ...
}
```

### 5.3 LLM step — `execLLM` (`go/internal/agentgen/interpreter.go`)

After the existing `ModelOverrideParamKey` block, add:

```go
// App global param model override — takes precedence over ModelOverrideParamKey.
if cfg.ModelOverrideParamRef != "" && ic.AppGlobalParams != nil {
    if override := ic.AppGlobalParams[cfg.ModelOverrideParamRef]; override != "" {
        model = override
    }
}
```

---

## 6. Phase 5 — Runtime Loading

### 6.1 `loadAppGlobalParams` (`go/cmd/agent-runtime/main.go`)

Add method on `Runtime`, modelled on `loadAppAPIKey`:

```go
func (rt *Runtime) loadAppGlobalParams(ctx context.Context, appID string) map[string]string {
    row := rt.pool.QueryRow(ctx,
        `SELECT COALESCE(app_params, '{}') FROM them.applications WHERE id = $1::uuid`, appID)
    var raw []byte
    if err := row.Scan(&raw); err != nil {
        return map[string]string{}
    }

    type secretEntry struct {
        CT   string `json:"ct"`
        Hint string `json:"hint"`
    }
    var top map[string]json.RawMessage
    if err := json.Unmarshal(raw, &top); err != nil {
        return map[string]string{}
    }

    out := make(map[string]string, len(top))
    for name, valRaw := range top {
        var entry secretEntry
        if json.Unmarshal(valRaw, &entry) == nil && entry.CT != "" {
            plain, err := crypto.DecryptStored(rt.cryptoKey, entry.CT)
            if err != nil {
                slog.Warn("agent-runtime: app param decryption failed",
                    "app_id", appID, "name", name)
                continue
            }
            out[name] = plain
            continue
        }
        var s string
        if json.Unmarshal(valRaw, &s) == nil && s != "" {
            out[name] = s
        }
    }
    return out
}
```

### 6.2 Wire into `handle`

In the `handle` method, immediately after the `ic.AgentParams` assignment:

```go
ic.AppGlobalParams = rt.loadAppGlobalParams(r.Context(), ic.ApplicationID)
```

---

## 7. Phase 6 — Frontend

### 7.1 API types and methods (`frontend/src/lib/api.ts`)

```typescript
export interface AppGlobalParam {
  name: string;
  type: 'secret' | 'string' | 'url' | 'int' | 'bool';
  is_set: boolean;
  value_hint?: string;  // last 4 chars; secrets only; never returned for non-secrets
  value?: string;       // non-secrets only; never returned for secrets
}
```

In `themApi`:

```typescript
getAppParams: (appId: string) =>
  api.get<AppGlobalParam[]>(`/admin/applications/${appId}/app-params`),
setAppParam: (appId: string, name: string, body: { value: string; type: string }) =>
  api.put<{ name: string; updated: boolean }>(
    `/admin/applications/${appId}/app-params/${name}`, body),
deleteAppParam: (appId: string, name: string) =>
  api.delete<{ name: string; deleted: boolean }>(
    `/admin/applications/${appId}/app-params/${name}`),
```

### 7.2 RuntimeView — "App Parameters" section (`RuntimeView.tsx`)

Insert a new section after "LLM Provider Keys" and before "LLM Configuration". It mirrors the provider keys UX: list existing params with fill-status badge, input to update value, Remove button; plus an "Add Parameter" row with name + type + value fields.

Key state variables:
- `appParams: AppGlobalParam[]`
- `appParamInputs: Record<string, { value: string; type: string }>`
- `appParamSaving: string | null`
- `appParamMsg: Record<string, string>`
- `newParamName`, `newParamType`, `newParamValue`, `addParamMsg`, `addParamSaving`

Handlers: `handleSetAppParam(name)`, `handleDeleteAppParam(name)`, `handleAddAppParam()` — each calls the corresponding `themApi` method then refreshes the list.

Display: for each param show `name (type)` + a green/amber "set ···XXXX" / "not set" badge + a password/text input + Save + Remove buttons.

### 7.3 Canvas builder picker (`RightPanel.tsx`)

The HTTP step config panel gains a two-mode "APP AUTH PARAM" section:

- **Per-binding mode** (existing): dropdown of `NodeDef.AppParams` → sets `app_param_key` on step config. This is the current behavior, unchanged.
- **App global param mode** (new): dropdown of `AppGlobalParam[]` fetched from the app → sets `app_param_ref` on step config.

A two-button toggle switches between modes. Setting a value in one mode clears the other (they are mutually exclusive at the config level, though the interpreter has a precedence fallback).

The builder page (`page.tsx`) fetches `themApi.getAppParams(app.id)` on mount and passes the result as `appGlobalParams` prop to `RightPanel`.

For the LLM node, a similar picker appears for `model_override_param_ref` alongside the existing `model_override` param key — allowing per-node model selection from the app's global param pool (e.g. a `"chat_model"` param set at app level).

---

## 8. Test Plan

### 8.1 Service tests (`go/internal/admin/service/app_global_params_test.go`)

| ID | Description |
|---|---|
| AGP-1 | `SetAppParam` secret → stored JSON has `ct`+`hint`; `GetPlaintextAppParams` roundtrips correctly |
| AGP-2 | `SetAppParam` non-secret → stored as plain JSON string; `GetAppParams` returns `Value` field |
| AGP-3 | `SetAppParam` bad name → `ErrValidation` |
| AGP-4 | `SetAppParam` bad type → `ErrUnprocessable` |
| AGP-5 | `GetAppParams` for stored secret → `IsSet=true`, `ValueHint` set, `Value` empty |
| AGP-6 | `GetAppParams` for stored non-secret → `IsSet=true`, `Value` set, no `ValueHint` |
| AGP-7 | `DeleteAppParam` → subsequent `GetAppParams` does not include that param |
| AGP-8 | nil crypto key → plain: roundtrip (no-op encryption, tests only) |

Use the existing `fakeDal` pattern from `go/internal/admin/service/` test helpers.

### 8.2 Compiler tests (`go/internal/agentgen/compiler_test.go`)

| ID | Description |
|---|---|
| CMP-10 | HTTP step with `app_param_ref` → `AgentAppParamRefs` contains `{StepID, ParamName}` |
| CMP-11 | LLM step with `model_override_param_ref` → entry in `AgentAppParamRefs` |
| CMP-12 | Step with both `app_param_key` and `app_param_ref` → both `RequiredParams` and `AgentAppParamRefs` populated |
| CMP-13 | No `app_param_ref` on any step → `AgentAppParamRefs` is nil (omitted from JSON) |
| CMP-14 | Same `app_param_ref` name on two different steps → two entries (one per step, same name OK) |

### 8.3 Interpreter tests (`go/internal/agentgen/agentgen_test.go`)

| ID | Description |
|---|---|
| INT-10 | HTTP step with `app_param_ref` + matching `AppGlobalParams` entry → correct `Authorization: Bearer` header |
| INT-11 | HTTP step with `app_param_ref` + absent entry + non-empty `inject_mode` → error |
| INT-12 | HTTP step with `app_param_ref` + absent entry + empty `inject_mode` → silently skips |
| INT-13 | Both `app_param_ref` and `app_param_key` set → `app_param_ref` takes precedence |
| INT-14 | LLM step with `model_override_param_ref` + matching `AppGlobalParams` → override applied |

### 8.4 Handler tests (`go/internal/admin/`)

| ID | Description |
|---|---|
| HTTP-20 | `GET /app-params` → 200, `[]` for app with no params |
| HTTP-21 | `PUT /app-params/geoapify_key` secret → 200; `GET` shows `is_set:true`, correct `value_hint` |
| HTTP-22 | `PUT /app-params/target_city` string → 200; `GET` shows `value:"Tel Aviv"` |
| HTTP-23 | `PUT /app-params/Bad Name` → 400 |
| HTTP-24 | `DELETE /app-params/geoapify_key` → 200; param gone from `GET` |
| HTTP-25 | Wrong tenant cannot access another tenant's params |

### 8.5 Runtime loader tests (`go/cmd/agent-runtime/main_test.go`)

| ID | Description |
|---|---|
| RT-20 | `loadAppGlobalParams` decrypts secret entry → correct plaintext |
| RT-21 | `loadAppGlobalParams` returns plain string for non-secret entries |
| RT-22 | `loadAppGlobalParams` returns empty map on DB error (non-fatal) |

### 8.6 TEST_INDEX.md

Update in the same commit as each test file addition. Add rows, update count, update trigger map.

---

## 9. Rollout and Backward Compatibility

### 9.1 Zero breaking changes

- `app_params DEFAULT '{}'` — existing rows unaffected, no downtime.
- `app_param_key` / `AgentParams` path in interpreter unchanged.
- `ModelOverrideParamKey` / `AgentParams` unchanged.
- `provider_keys` / `AppAPIKey` untouched.
- Agents published before this change have no `AgentAppParamRefs`; `loadAppGlobalParams` returns an empty map and `AppGlobalParams` is unused.

### 9.2 Precedence rules

When both `app_param_ref` and `app_param_key` are set on the same step:
1. Try `AppGlobalParams[app_param_ref]` first.
2. If absent, fall back to `AgentParams[step.ID + ":" + app_param_key]` (existing per-binding path).

This gives operators a migration window — create the global param, then remove the per-binding entry at their convenience.

### 9.3 Deployment order

1. Apply migration `045_app_global_params.sql`.
2. Deploy Go binary (DAL/service/handler/runtime/compiler/interpreter).
3. Deploy frontend (RuntimeView section + canvas picker).

No Temporal, Redis, or Python changes required.

---

## 10. Phase Breakdown Summary

| Phase | What changes | Key files |
|---|---|---|
| 1 — DB | New `app_params` column | `db/045_app_global_params.sql` |
| 2 — Backend | DAL + service + REST endpoints | `dal/applications.go`, `service/applications.go`, `admin/applications.go`, `service/service.go` (Dal interface) |
| 3 — Compiler | `app_param_ref` detection, `AgentAppParamRefs` on spec | `agentgen/spec.go`, `agentgen/compiler.go` |
| 4 — Interpreter | Resolve `app_param_ref` + `model_override_param_ref` | `agentgen/interpreter.go`, `agentgen/context.go` |
| 5 — Runtime | Load + decrypt `app_params` before execution | `cmd/agent-runtime/main.go` |
| 6 — Frontend | RuntimeView CRUD + canvas picker | `frontend/src/lib/api.ts`, `RuntimeView.tsx`, `RightPanel.tsx` |
