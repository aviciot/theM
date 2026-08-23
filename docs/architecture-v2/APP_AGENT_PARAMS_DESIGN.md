# Application Agent Parameters — Architecture Design
Last updated: 2026-08-23
Status: Design (not yet implemented)

---

## 1. Problem Statement

Canvas agents built with HTTP nodes need runtime credentials — bearer tokens, API keys, etc. — that differ per application. Today these credentials have nowhere to live: `credential_bindings` on `app_agent_bindings` is used for a flat slot model that pre-dated the current canvas architecture and is effectively unused (the `loadBinding()` function reads it and immediately discards it with `_ = credJSON`). There is no mechanism for an agent node to declare what secrets it requires, no UI to fill them in per-application, and no runtime path to deliver them to a step during execution.

This document designs the complete system from node declaration to runtime delivery.

---

## 2. Design Principles

These constraints are non-negotiable and follow from the existing architecture:

1. **No secrets in AgentSpec, agent_definitions, or Temporal history.** Param keys (names) are safe to store there; values never are.
2. **Use the existing AES-GCM encryption layer** (`service.encryptAESGCM` / `service.DecryptAESGCM`). Do not introduce a third encryption primitive.
3. **`InvocationContext` is the only path for secrets into a pipeline step.** Secrets arrive there, decrypted, and die with the request.
4. **Compiler is the single source of truth for what an agent needs.** Runtime reads `AgentSpec.RequiredParams`; it does not re-parse node configs.
5. **The new column on `app_agent_bindings` replaces `credential_bindings` logically.** `credential_bindings` is retained in the schema for backward compatibility but its role is superseded.

---

## 3. Data Model: `AppParamDecl` and `AgentParamSpec`

### 3.1 `AppParamDecl` — declared on `NodeDef`

```go
// AppParamDecl declares one runtime parameter that this node type can consume.
// Declared statically on NodeDef; not per-instance. The instance config holds
// only AppParamKey — which param name it uses, not the full declaration.
type AppParamDecl struct {
    Key          string `json:"key"`           // unique within the agent; identifier used in node configs
    Label        string `json:"label"`         // human-readable for the UI form field label
    Description  string `json:"description"`   // tooltip / help text
    Type         string `json:"type"`          // "secret" | "string" | "url" | "int" | "bool"
    Required     bool   `json:"required"`
    DefaultValue string `json:"default_value,omitempty"`
}
```

**Type semantics:**
- `"secret"` — encrypted at rest; rendered as `<input type="password">` in the UI; value is NEVER returned in API responses (only the hint is)
- `"string"` — plaintext; rendered as `<input type="text">`
- `"url"` — plaintext; rendered as `<input type="url">` with URL-format validation
- `"int"` — plaintext; rendered as `<input type="number" step="1">`
- `"bool"` — plaintext; rendered as `<select>` with true/false options

**Where it lives on `NodeDef`:**

```go
type NodeDef struct {
    // ... existing fields unchanged ...

    // AppParams declares the runtime parameters this node type can consume.
    // Empty for most node types. Populated for HTTP, A2A Call, and LLM nodes.
    // These are static — identical for every instance of this node type.
    // The node instance config uses AppParamKey to reference a specific param by name.
    AppParams []AppParamDecl `json:"app_params,omitempty"`

    // ... Validate, Execute unchanged ...
}
```

`AppParams` is included in `NodeTypeInfo` and returned by `GET /admin/node-types` so the frontend can render param-aware property panels without hard-coding per-type logic.

### 3.2 `AgentParamSpec` — compiled into `AgentSpec`

```go
// AgentParamSpec is the published, immutable form of one required parameter.
// Collected by the compiler from all AppParamDecl entries across all skills.
// Stored in AgentSpec.RequiredParams, which lives in agent_runtime_specs.spec JSONB.
type AgentParamSpec struct {
    Key          string   `json:"key"`
    Label        string   `json:"label"`
    Description  string   `json:"description"`
    Type         string   `json:"type"`           // same values as AppParamDecl.Type
    Required     bool     `json:"required"`
    DefaultValue string   `json:"default_value,omitempty"`
    UsedByNodes  []string `json:"used_by_nodes"`  // step IDs that reference this key via AppParamKey
}
```

`AgentSpec` gains one new field:

```go
type AgentSpec struct {
    // ... existing fields unchanged ...
    RequiredParams []AgentParamSpec `json:"required_params,omitempty"`
}
```

`RequiredParams` is `nil` / omitted for agents with no param-aware nodes (the vast majority of existing agents). The frontend and runtime treat `nil` and `[]` identically.

---

## 4. Node Config Changes

### 4.1 `HTTPStepConfig`

```go
type HTTPStepConfig struct {
    Method         string            `json:"method"`
    URLTemplate    string            `json:"url_template"`
    Headers        map[string]string `json:"headers,omitempty"`
    BodyTemplate   string            `json:"body_template,omitempty"`
    Extractions    []JSONPathExtract `json:"extractions"`
    TimeoutSeconds int               `json:"timeout_seconds"`

    // AppParamKey, if non-empty, names the AgentParamSpec.Key that holds the auth credential.
    // The runtime looks up ic.AgentParams[AppParamKey] and injects it according to InjectMode.
    AppParamKey string `json:"app_param_key,omitempty"`

    // InjectMode controls how the credential is injected into the HTTP request.
    // "header"        — sets Authorization: Bearer <value>
    // "query"         — appends ?<InjectHeaderName>=<value>
    // "basic"         — sets Authorization: Basic base64(value)
    // "custom_header" — sets <InjectHeaderName>: <value>
    // Empty string or omitted: no auth injection.
    InjectMode string `json:"inject_mode,omitempty"`

    // InjectHeaderName is used when InjectMode is "custom_header" or "query".
    // For "query": query param name (e.g. "api_key").
    // For "custom_header": header name (e.g. "X-Api-Key").
    // Ignored for "header" and "basic".
    InjectHeaderName string `json:"inject_header_name,omitempty"`
}
```

**Inject mode behavior in `execHTTP`:**

| `InjectMode` | Effect |
|---|---|
| `"header"` or `""` (with key set) | `Authorization: Bearer <value>` |
| `"query"` | Appends `?<InjectHeaderName>=<value>` to URL |
| `"basic"` | `Authorization: Basic base64(<value>)` |
| `"custom_header"` | `<InjectHeaderName>: <value>` |
| `AppParamKey == ""` | No injection (existing behavior) |

### 4.2 `LLMStepConfig`

```go
type LLMStepConfig struct {
    // ... existing fields unchanged ...

    // ModelOverrideParamKey, if non-empty, names the AgentParamSpec.Key whose value
    // overrides the compiled model at runtime.
    // Type of the referenced param MUST be "string".
    // If the param is unset at runtime, the compiled model is used unchanged.
    ModelOverrideParamKey string `json:"model_override_param_key,omitempty"`
}
```

### 4.3 `A2ACallStepConfig`

```go
type A2ACallStepConfig struct {
    Ref            DefinitionRef `json:"ref"`
    InputVar       string        `json:"input_var"`
    OutputVar      string        `json:"output_var"`
    TimeoutSeconds int           `json:"timeout_seconds"`

    // AuthParamKey, if non-empty, names the AgentParamSpec.Key holding the bearer token
    // injected into the outbound A2A request's Authorization header.
    AuthParamKey string `json:"auth_param_key,omitempty"`
}
```

---

## 5. NodeDef Registration — HTTP and LLM Nodes

The HTTP node registration in `nodes.go` is extended:

```go
RegisterNode(NodeDef{
    Type:        StepHTTP,
    // ... existing fields unchanged ...
    AppParams: []AppParamDecl{
        {
            Key:         "bearer_token",
            Label:       "Bearer Token",
            Description: "Authorization: Bearer <value> injected into the HTTP request. Leave unset for no auth.",
            Type:        "secret",
            Required:    false,
        },
        {
            Key:         "api_key",
            Label:       "API Key",
            Description: "Generic API key. Set InjectMode on the step to control injection (header/query/custom_header).",
            Type:        "secret",
            Required:    false,
        },
    },
})
```

The LLM node:

```go
RegisterNode(NodeDef{
    Type:        StepLLM,
    // ... existing fields unchanged ...
    AppParams: []AppParamDecl{
        {
            Key:         "model_override",
            Label:       "Model Override",
            Description: "Override the compiled model name at runtime. Must be a valid model identifier for the configured provider.",
            Type:        "string",
            Required:    false,
        },
    },
})
```

**Important:** `AppParamDecl` entries on a node type are the *universe* of params that node type *can* use. The actual param key used by a specific step instance is set in `HTTPStepConfig.AppParamKey`. A step instance references at most one param per injection slot.

---

## 6. DB Schema Change

New migration file: `db/038_app_agent_params.sql`

```sql
-- db/038_app_agent_params.sql
-- Adds agent_params JSONB to app_agent_bindings for structured, per-agent
-- runtime parameter storage. Supersedes credential_bindings (retained for compat).
--
-- Storage format:
--   Secrets:     {"key": {"ct": "enc:...", "hint": "XXXX"}}
--   Non-secrets: {"key": "plaintext_value"}
--
-- hint = last 4 chars of plaintext, extracted before encryption.

ALTER TABLE them.app_agent_bindings
    ADD COLUMN IF NOT EXISTS agent_params JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN them.app_agent_bindings.agent_params IS
    'Runtime parameters for this agent binding. Secrets encrypted via AES-GCM. '
    'Format: {key: {ct: "enc:...", hint: "XXXX"}} for secrets, '
    '        {key: "value"} for non-secrets.';
```

**No new tables.** The existing `app_agent_bindings` table gains one column. `AgentSpec.RequiredParams` lives in `agent_runtime_specs.spec` JSONB alongside the rest of the spec — no separate column needed.

**`credential_bindings` status:** Retained in schema. `loadBinding()` already discards it (`_ = credJSON`). Never written going forward. Can be dropped in a future migration.

---

## 7. Compiler Changes

### 7.1 Stage 3.5: AppParam collection and validation

Runs after `validateGraph`, before `validateExecutability`. Integrated into both `Validate` and `CompileForPublish`.

```go
// collectAgentParams walks all steps, gathering AppParamDecl from each step's NodeDef
// and validating AppParamKey references.
func collectAgentParams(def *canvasDefinition) ([]AgentParamSpec, []Issue) {
    var issues []Issue
    paramMap := map[string]*AgentParamSpec{}
    usedBy := map[string][]string{} // key → step IDs

    for _, cs := range def.Skills {
        for _, step := range cs.Steps {
            nd, ok := LookupNode(step.Type)
            if !ok {
                continue
            }
            for _, decl := range nd.AppParams {
                existing, seen := paramMap[decl.Key]
                if seen {
                    if existing.Type != decl.Type {
                        issues = append(issues, Issue{
                            Severity: "error",
                            Code:     "PARAM_TYPE_CONFLICT",
                            Message:  fmt.Sprintf("param %q declared as type %q by node %s but type %q by a prior node", decl.Key, decl.Type, step.ID, existing.Type),
                            SkillID:  cs.SkillID,
                            NodeID:   step.ID,
                            Field:    "app_param_key",
                        })
                    }
                    if decl.Required {
                        paramMap[decl.Key].Required = true
                    }
                } else {
                    paramMap[decl.Key] = &AgentParamSpec{
                        Key: decl.Key, Label: decl.Label,
                        Description: decl.Description, Type: decl.Type,
                        Required: decl.Required, DefaultValue: decl.DefaultValue,
                    }
                }
            }
            key := extractAppParamKey(step)
            if key != "" {
                if _, declared := paramMap[key]; !declared {
                    issues = append(issues, Issue{
                        Severity: "error",
                        Code:     "UNDECLARED_APP_PARAM",
                        Message:  fmt.Sprintf("step references app_param_key %q but no node in this agent declares a param with that key", key),
                        SkillID:  cs.SkillID, NodeID: step.ID, Field: "app_param_key",
                    })
                } else {
                    usedBy[key] = append(usedBy[key], step.ID)
                }
            }
        }
    }

    for key, spec := range paramMap {
        spec.UsedByNodes = usedBy[key]
    }
    params := make([]AgentParamSpec, 0, len(paramMap))
    for _, spec := range paramMap {
        params = append(params, *spec)
    }
    sort.Slice(params, func(i, j int) bool { return params[i].Key < params[j].Key })
    return params, issues
}

func extractAppParamKey(step canvasStep) string {
    switch step.Type {
    case StepHTTP:
        var cfg HTTPStepConfig
        if json.Unmarshal(step.Config, &cfg) == nil {
            return cfg.AppParamKey
        }
    case StepLLM:
        var cfg LLMStepConfig
        if json.Unmarshal(step.Config, &cfg) == nil {
            return cfg.ModelOverrideParamKey
        }
    case StepA2ACall:
        var cfg A2ACallStepConfig
        if json.Unmarshal(step.Config, &cfg) == nil {
            return cfg.AuthParamKey
        }
    }
    return ""
}
```

### 7.2 New compile error codes

| Code | Severity | Meaning |
|---|---|---|
| `UNDECLARED_APP_PARAM` | error | A step's `AppParamKey` names a key not declared by any node in the agent |
| `PARAM_TYPE_CONFLICT` | error | Two nodes declare the same key with different types |

### 7.3 `buildSpec` change

```go
func buildSpec(..., params []AgentParamSpec) *AgentSpec {
    return &AgentSpec{
        // ... existing fields unchanged ...
        RequiredParams: params, // nil when len(params) == 0
    }
}
```

---

## 8. InvocationContext Extension

`go/internal/agentgen/context.go`:

```go
type InvocationContext struct {
    TenantID        string
    ApplicationID   string
    AgentID         string
    BindingID       string
    Spec            *AgentSpec
    ConfigOverrides map[string]any
    Policies        InvocationPolicies
    AppAPIKey       map[string]string // provider → plaintext LLM key
    // AgentParams: resolved plaintext values for all declared agent params.
    // Secrets are decrypted in loadBinding() before this map is populated.
    // NEVER logged or serialized — cleared after the request.
    AgentParams map[string]string // param key → plaintext value
}
```

`String()` remains the redacted form and does not include `AgentParams`.

---

## 9. Runtime Changes — `agent-runtime/main.go`

### 9.1 `loadBinding` extended

Adds `agent_params` column to the SELECT. New helper `resolveAgentParams` decrypts secrets and applies defaults:

```go
func (rt *Runtime) resolveAgentParams(raw []byte, decls []agentgen.AgentParamSpec) map[string]string {
    out := make(map[string]string, len(decls))
    var stored map[string]json.RawMessage
    if len(raw) > 0 {
        json.Unmarshal(raw, &stored)
    }
    for _, decl := range decls {
        rawVal, exists := stored[decl.Key]
        if !exists {
            if decl.DefaultValue != "" {
                out[decl.Key] = decl.DefaultValue
            }
            continue
        }
        if decl.Type == "secret" {
            var entry struct {
                CT   string `json:"ct"`
                Hint string `json:"hint"`
            }
            if json.Unmarshal(rawVal, &entry) == nil && entry.CT != "" {
                plain, err := service.DecryptAESGCM(rt.cryptoKey, entry.CT)
                if err != nil {
                    rt.logger.Warn("agent-runtime: param decryption failed", "key", decl.Key)
                    continue
                }
                out[decl.Key] = plain
            }
        } else {
            var s string
            if json.Unmarshal(rawVal, &s) == nil {
                out[decl.Key] = s
            }
        }
    }
    return out
}
```

After `loadBinding`, set `ic.AgentParams = agentParams` in `handle()`.

### 9.2 `execHTTP` extended

After static headers loop, before `http.Do`:

```go
if cfg.AppParamKey != "" {
    paramVal := ic.AgentParams[cfg.AppParamKey]
    if paramVal == "" && cfg.InjectMode != "" {
        return fmt.Errorf("step requires param %q for auth injection but param is not set", cfg.AppParamKey)
    }
    if paramVal != "" {
        switch cfg.InjectMode {
        case "header", "":
            req.Header.Set("Authorization", "Bearer "+paramVal)
        case "query":
            q := req.URL.Query()
            name := cfg.InjectHeaderName
            if name == "" { name = "api_key" }
            q.Set(name, paramVal)
            req.URL.RawQuery = q.Encode()
        case "basic":
            req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(paramVal)))
        case "custom_header":
            if cfg.InjectHeaderName == "" {
                return fmt.Errorf("inject_mode custom_header requires inject_header_name")
            }
            req.Header.Set(cfg.InjectHeaderName, paramVal)
        default:
            return fmt.Errorf("unknown inject_mode %q", cfg.InjectMode)
        }
    }
}
```

### 9.3 `execLLM` extended

```go
model := cfg.Model
if cfg.ModelOverrideParamKey != "" {
    if override := ic.AgentParams[cfg.ModelOverrideParamKey]; override != "" {
        model = override
    }
}
// use model variable in provider construction
```

---

## 10. Admin API Layer

### 10.1 New routes

Mounted under the existing `/admin/applications/{app_id}` sub-tree:

```
GET  /admin/applications/{app_id}/agents/{agent_id}/params
PUT  /admin/applications/{app_id}/agents/{agent_id}/params
```

### 10.2 `GET .../params` response

```json
{
  "agent_id": "uuid",
  "agent_slug": "my_http_agent",
  "required_params": [
    {
      "key": "github_token",
      "label": "GitHub Token",
      "description": "Personal access token for GitHub API calls.",
      "type": "secret",
      "required": true,
      "used_by_nodes": ["step_http_1"],
      "is_set": true,
      "hint": "xb7A"
    },
    {
      "key": "api_base_url",
      "label": "API Base URL",
      "type": "url",
      "required": false,
      "default_value": "https://api.github.com",
      "used_by_nodes": [],
      "is_set": false,
      "hint": ""
    }
  ]
}
```

Secrets are never returned in plaintext — only `is_set` and `hint`.

**Error cases:**
- No published spec for agent: `404` with code `SPEC_NOT_FOUND`
- No binding yet: return `required_params` from spec with all `is_set: false` (binding is auto-created on first PUT)

### 10.3 `PUT .../params` request

```json
{
  "params": {
    "github_token": "ghp_actualtoken",
    "api_base_url": "https://api.staging.example.com"
  }
}
```

Keys absent from the body are left unchanged. Sending `""` clears the stored entry. Handler encrypts `type == "secret"` values via `encryptAESGCM`, extracts `hint` (last 4 chars), stores non-secrets as plain JSON strings. Creates the binding row if it doesn't exist (UPSERT).

**Response:**

```json
{"application_id": "uuid", "agent_id": "uuid", "updated": true}
```

### 10.4 DAL methods

```go
// GetAgentParamsForBinding returns agent_params JSON and RequiredParams from the spec.
GetAgentParamsForBinding(ctx context.Context, applicationID, agentID string) (agentParamsJSON []byte, requiredParams []agentgen.AgentParamSpec, err error)

// UpsertAgentParams merges paramsDelta into agent_params, creating the row if absent.
UpsertAgentParams(ctx context.Context, applicationID, agentID string, paramsDelta []byte) error
```

SQL for `GetAgentParamsForBinding`:
```sql
SELECT COALESCE(b.agent_params, '{}'), s.spec->'required_params'
FROM them.app_agent_bindings b
JOIN them.agent_runtime_specs s ON s.agent_id = b.agent_id
WHERE b.application_id = $1::uuid AND b.agent_id = $2::uuid
```

SQL for `UpsertAgentParams` (JSONB merge — `null` values delete keys):
```sql
INSERT INTO them.app_agent_bindings (application_id, agent_id, agent_params)
VALUES ($1::uuid, $2::uuid, $3::jsonb)
ON CONFLICT (application_id, agent_id) DO UPDATE
    SET agent_params = them.app_agent_bindings.agent_params || $3::jsonb,
        updated_at = now()
```

---

## 11. Frontend — RuntimeView Redesign

### 11.1 New "Agent Parameters" section

Added to `RuntimeView` after the existing LLM Configuration section. Only rendered when at least one bound agent has a published spec with `required_params`.

```
RuntimeView
├── Session Limits               (existing)
├── Rate Limiting                (existing)
├── Access Control               (existing)
├── LLM Provider Keys            (existing)
├── LLM Configuration            (existing)
└── Agent Parameters             (NEW)
    ├── Agent: "My HTTP Agent" [slug]  [badge: "1 missing"]
    │   ├── github_token   [secret] [···· xb7A] [input=password] [Save]
    │   └── api_base_url   [url]    [not set]    [input=url]      [Save]
    └── Agent: "My LLM Agent" [slug]  [badge: "all set"]
        └── model_override [string] [not set]    [input=text]     [Save]
```

- Each agent sub-panel: header with display name + slug + status badge
- One row per declared param: label, type badge, fill status, input widget, per-param Save button
- Secret params: `<input type="password">` showing `···· XXXX` hint when set
- URL/string/int/bool: appropriate HTML5 input types
- Inline save per-param (matches existing provider key UX pattern)

### 11.2 Data loading

```typescript
const paramResults = await Promise.all(
    bindings.map(b => themApi.getAgentParams(app.id, b.agent_id).catch(() => null))
);
// Agents with no published spec (null response) are silently omitted
```

### 11.3 New API client types and methods

In `frontend/src/lib/api.ts`:

```typescript
export interface AgentParamMeta {
  key: string;
  label: string;
  description: string;
  type: 'secret' | 'string' | 'url' | 'int' | 'bool';
  required: boolean;
  default_value?: string;
  used_by_nodes: string[];
  is_set: boolean;
  hint?: string;
}

export interface AgentParamsResponse {
  agent_id: string;
  agent_slug: string;
  required_params: AgentParamMeta[];
}

// In themApi:
getAgentParams: (appId: string, agentId: string) =>
    api.get<AgentParamsResponse>(`/admin/applications/${appId}/agents/${agentId}/params`),

putAgentParams: (appId: string, agentId: string, params: Record<string, string>) =>
    api.put<{ application_id: string; agent_id: string; updated: boolean }>(
        `/admin/applications/${appId}/agents/${agentId}/params`,
        { params },
    ),
```

### 11.4 Builder panel — HTTP step config extension

HTTP step properties panel gains three new fields after the Timeout field:

| Field | Control | Condition |
|---|---|---|
| Auth Param Key | `<input type="text">` placeholder "leave empty for no auth" | Always shown |
| Inject Mode | `<select>` none/header/query/basic/custom_header | Shown when Auth Param Key is non-empty |
| Inject Header Name | `<input type="text">` placeholder "e.g. X-Api-Key" | Shown when inject_mode is "query" or "custom_header" |

These map directly to `HTTPStepConfig.AppParamKey`, `.InjectMode`, and `.InjectHeaderName`.

---

## 12. `NodeTypeInfo` Extension

```go
type NodeTypeInfo struct {
    // ... existing fields unchanged ...
    AppParams []AppParamDecl `json:"app_params,omitempty"`
}
```

`ToInfo()` copies `d.AppParams` into the output. `GET /admin/node-types` returns `app_params` for HTTP, LLM, and A2A Call node types. The frontend builder uses this to:
- Know which node types support `app_param_key` config
- Provide autocomplete for known param key names in the Auth Param Key field

---

## 13. Security Invariants

| Invariant | Enforcement point |
|---|---|
| `secret` params encrypted via AES-GCM before `INSERT`/`UPDATE` | `PutAgentParams` service method |
| Plaintext values never returned in GET responses | Handler returns only `is_set` and `hint` for secrets |
| `ic.AgentParams` never logged | `InvocationContext.String()` does not include it |
| `ic.AgentParams` never serialized to Temporal history | `InvocationContext` never passed to Temporal activities |
| Decryption failures logged at Warn (key name only) and param absent from map | `resolveAgentParams()` |
| Non-secret params stored as plaintext JSON — secret material must not be placed in non-secret params | Code review + type declaration |
| `agent_params` column excluded from export/import operations | Export DAL query excludes the column |

---

## 14. Backward Compatibility

- **Existing agents with no `AppParams`:** `AgentSpec.RequiredParams` is nil. `resolveAgentParams` returns empty map. No behavior change.
- **Existing `app_agent_bindings` rows:** `agent_params` defaults to `'{}'`. `resolveAgentParams` returns empty map. No behavior change.
- **`credential_bindings`:** Untouched. Continues to be selected and discarded by `loadBinding`. Future migration can drop it.
- **Re-publishing an agent that gains new `AppParams`:** New `RequiredParams` in spec; existing binding has no values for them. If step's param is `Required: true` and unset, execution fails with a clear error referencing the param key. Operators are directed to Runtime → Agent Parameters UI.
- **Orphaned params after re-publish:** Keys in `agent_params` JSONB that no longer appear in the new spec are never read — no harm done.

---

## 15. Implementation Phases

### Phase 1 — HTTP Bearer Token (shippable, proves the concept)

**Go backend:**
1. Add `AppParamDecl`, `AgentParamSpec` to `go/internal/agentgen/spec.go`
2. Add `AppParams []AppParamDecl` to `NodeDef` and `NodeTypeInfo` in `noderegistry.go`
3. Register `bearer_token` and `api_key` AppParams on `StepHTTP` in `nodes.go`
4. Add `AppParamKey`, `InjectMode`, `InjectHeaderName` to `HTTPStepConfig` in `spec.go`
5. Add `RequiredParams []AgentParamSpec` to `AgentSpec` in `spec.go`
6. Add `collectAgentParams()` and `extractAppParamKey()` to `compiler.go`; integrate into `Validate` and `CompileForPublish`
7. Add `UNDECLARED_APP_PARAM` and `PARAM_TYPE_CONFLICT` error codes
8. Add `AgentParams map[string]string` to `InvocationContext` in `context.go`
9. Extend `execHTTP` with auth injection in `interpreter.go`

**DB:**
- Write and apply `db/038_app_agent_params.sql`

**Runtime (`agent-runtime/main.go`):**
- Add `agent_params` to `loadBinding` SELECT
- Add `resolveAgentParams()` function
- Set `ic.AgentParams` in `handle()`

**Admin API:**
- Add `GetAgentParamsForBinding` and `UpsertAgentParams` to `Dal` interface
- Implement both in `go/internal/admin/dal/agent_bindings.go`
- Add `GetAgentParams` and `PutAgentParams` handler methods
- Mount routes in `AgentBindingsHandler.MountOn`

**Frontend:**
- Add `AgentParamMeta`, `AgentParamsResponse` types and `getAgentParams`, `putAgentParams` methods to `api.ts`
- Add "Agent Parameters" section to `RuntimeView` in `applications/page.tsx`
- Add `app_param_key`, `inject_mode`, `inject_header_name` fields to HTTP step panel in `builder/page.tsx`

**Tests (all must pass before commit):**
- `collectAgentParams`: happy path, type conflict, undeclared param reference
- `resolveAgentParams`: secret decrypt, non-secret passthrough, default value, missing key
- `execHTTP`: inject modes header/query/basic/custom_header, missing param error
- Handler: `GetAgentParams` 200, `PutAgentParams` 200, binding auto-create on first PUT

### Phase 2 — LLM Model Override

- Add `model_override` AppParam to LLM node
- Add `ModelOverrideParamKey` to `LLMStepConfig`
- Extend `execLLM` with model override logic
- Add `model_override` input to LLM step properties panel

### Phase 3 — A2A Call Auth

- Add `auth_token` AppParam to `StepA2ACall`
- Add `AuthParamKey` to `A2ACallStepConfig`
- Extend `execA2ACall` with auth header injection
- Add `auth_param_key` to A2A Call step properties panel

### Phase 4 — Operator UX Polish

- "Required params not set" warning badge on application cards in the list view
- Inline builder hint: if `app_param_key` references an undeclared key, show `UNDECLARED_APP_PARAM` hint immediately (mirrors compile error)
- Param usage tooltip in Runtime UI: clicking a param row shows `used_by_nodes` step list

---

## 16. Critical Files for Implementation

| File | Change |
|---|---|
| `go/internal/agentgen/spec.go` | Add `AppParamDecl`, `AgentParamSpec`, `AppParams` on `NodeDef`, `RequiredParams` on `AgentSpec`, new fields on step configs |
| `go/internal/agentgen/noderegistry.go` | Add `AppParams []AppParamDecl` to `NodeDef` and `NodeTypeInfo`, copy in `ToInfo()` |
| `go/internal/agentgen/nodes.go` | Register AppParams on HTTP and LLM nodes |
| `go/internal/agentgen/context.go` | Add `AgentParams map[string]string` to `InvocationContext` |
| `go/internal/agentgen/compiler.go` | Add `collectAgentParams`, `extractAppParamKey`, integrate into `Validate`/`CompileForPublish`/`buildSpec` |
| `go/internal/agentgen/interpreter.go` | Extend `execHTTP` with inject logic, extend `execLLM` with model override |
| `go/cmd/agent-runtime/main.go` | Add `agent_params` to SELECT, add `resolveAgentParams`, set `ic.AgentParams` |
| `go/internal/admin/service/service.go` | Add DAL interface methods |
| `go/internal/admin/dal/agent_bindings.go` | Implement `GetAgentParamsForBinding`, `UpsertAgentParams` |
| `go/internal/admin/agent_bindings.go` | Add `GetAgentParams`, `PutAgentParams` handlers, mount routes |
| `frontend/src/lib/api.ts` | Add types and API methods |
| `frontend/src/app/admin/applications/page.tsx` | Add Agent Parameters section to RuntimeView |
| `frontend/src/app/admin/agents/builder/page.tsx` | Add HTTP step auth param fields |
| `db/038_app_agent_params.sql` | New migration |
