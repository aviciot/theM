# Debug Setup — Design Notes
# 2026-08-23

## Problem

The current debug bar asks for `testInput` and an Anthropic API key, but:
- HTTP nodes with `app_param_key` have no way to supply secret values at debug time
- All values are lost on refresh
- There's no distinction between "safe to save" (testInput) and "never save to server" (API keys, secrets)

## Design

### Trigger

When the user clicks **🐛 Debug**, scan `localPipeNodes` to build a `DebugParamSpec[]` —
the list of values the debug session needs before it can run.

### Param types

| Source | Key | Label | Secret? | Always? |
|---|---|---|---|---|
| Built-in | `__test_input` | Test message | No | Yes |
| Built-in | `__anthropic_key` | Anthropic API key | Yes | Only if ≥1 LLM node |
| HTTP node `app_param_key` | the param key (e.g. `geoapify_key`) | from NodeDef.app_params label | Yes | Only if param key set on a node |

### UI flow

1. Click Debug → scan → show **Debug Setup** form (inside debug bar, expanded)
2. Form shows each required param with label, description, secret flag
3. Non-secret values pre-filled from `UserPreferences.debugValues[defId].testInput` (loaded on mount)
4. Secret values pre-filled from `sessionStorage` keyed by `debug_param:{defId}:{key}` (never server)
5. User fills/adjusts → clicks **▶ Start Debug**
6. On Start: save non-secrets to UserPreferences; save secrets to sessionStorage; enter run mode

### Persistence

```
UserPreferences.debugValues = {
  [defId]: {
    testInput: string,   // safe to save server-side
  }
}

sessionStorage:
  key: `debug_param:{defId}:{paramKey}`   // API keys, secrets — browser session only
```

### executeStep changes

`executeStep` receives a `debugParams: Record<string, string>` map.
- `__anthropic_key` → passed as the API key to LLM calls
- HTTP node `app_param_key` → injected into request (same inject_mode logic the Go runtime uses)

### HTTP debug execution

Currently HTTP steps do a direct `fetch(url)` from the browser.
For `app_param_key` injection:
- `inject_mode: "query"` → append to URL before fetching — works browser-side
- `inject_mode: "header"` → add `Authorization: Bearer` header — works browser-side
- `inject_mode: "custom_header"` → add named header — works browser-side
- `inject_mode: "basic"` → base64 encode, add Authorization: Basic — works browser-side

CORS: public APIs (Hebcal, Open-Meteo, Frankfurter) allow browser requests.
APIs that don't support CORS will fail in browser debug mode — this is expected and shown as an error.
The full runtime (Go agent-runtime) has no CORS constraint.

### State changes

Add to `DebugState`:
```typescript
setupComplete: boolean;       // true once user clicks "Start Debug"
debugParams: Record<string, string>;  // all collected param values for this session
```

Add `DebugParamSpec`:
```typescript
interface DebugParamSpec {
  key: string;
  label: string;
  description: string;
  isSecret: boolean;
  required: boolean;
}
```

### Import / Load agent from JSON

Separate feature: add an "Import JSON" button on the agent-level view (not pipeline view).
Paste/load a JSON → populate canvas nodes from the definition.
This is how the Kosher Vacation Planner JSON gets loaded into the builder.
