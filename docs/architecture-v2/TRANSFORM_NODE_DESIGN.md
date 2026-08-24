# Transform Node — Function Pipeline Design
# the-M multi-agent orchestration platform
# Status: DESIGN — not yet implemented
# Last updated: 2026-08-24

---

## Table of Contents

1. [Overview & Motivation](#1-overview--motivation)
2. [Data Model](#2-data-model)
3. [Go Package Structure & Registry Design](#3-go-package-structure--registry-design)
4. [API Endpoints](#4-api-endpoints)
5. [Full Function Catalog](#5-full-function-catalog)
6. [UI Design — Three-Tab Transform Panel](#6-ui-design--three-tab-transform-panel)
7. [Go Runtime Implementation Plan](#7-go-runtime-implementation-plan)
8. [Browser Implementation Plan](#8-browser-implementation-plan)
9. [Migration: Backward Compatibility](#9-migration-backward-compatibility)
10. [Adding a New Function End-to-End](#10-adding-a-new-function-end-to-end)

---

## 1. Overview & Motivation

### Current State

The Transform node (`StepType = "transform"`) currently supports two operation modes:

- **`expressions`** — a map of `output_var → Go template string` evaluated via `text/template`. Example: `{"greeting": "Hello, {{.user_name}}!"}`.
- **`extractions`** — an ordered list of JSON path pulls that parse a JSON-string pipeline variable and extract named fields. Example: extract `city.name` from `http_response` into `city_name`.

These cover ~60% of real-world data-shaping needs. The gaps that regularly appear in production pipelines:

- **String normalization** — trim whitespace, strip markdown fences, upper/lower case, regex replace. Every LLM agent needs this.
- **JSON manipulation** — parse JSON, merge objects, extract deep paths with bracket notation, get array items by index.
- **Type coercion and validation** — check if a var is a valid JSON object, convert to number, assert a value exists before passing it downstream.
- **Numeric operations** — arithmetic, rounding, clamping. Common in routing and scoring pipelines.
- **Date/time** — format timestamps, compute diffs, add days. Needed for any workflow that touches calendars or SLAs.
- **Conditional defaults** — return a fallback if a variable is empty or null. Currently requires a full Branch node for what is a one-liner elsewhere.
- **Encoding** — base64, URL-encode, hash. Required for auth header construction.
- **LLM-era cleaning** — strip markdown code fences from LLM output, truncate to approximate token count, normalize whitespace. These appear in almost every agent built on the platform.

### Design Goal

Introduce a **function pipeline** — an ordered list of named function calls — as a third mode alongside `expressions` and `extractions`. The pipeline is:

- **Go-only** — all functions are implemented in Go. There are no TypeScript function implementations. The browser fetches the self-describing function catalog from the Go backend at panel open time. Adding a new function means editing two Go files; the browser gets it for free.
- **Categorized** — functions are organized by category. Adding a function = add one entry to `registry.go` + one implementation in `functions.go`.
- **Debuggable via backend** — the Test tab POSTs sample input vars to `POST /api/v1/admin/transform-test`; Go runs the exact same `execTransform()` used in production and returns a step-by-step trace. No TypeScript reimplementation, guaranteed parity.
- **Self-describing** — each function declares its name, category, description, arg spec, and examples. The registry feeds the browser function picker, the test endpoint, the AI assistant (future), and the production pipeline executor.
- **Backward-compatible** — existing `expressions` and `extractions` continue to work unchanged. The new `functions` array is additive.

### Analogy

Think Informatica PowerCenter expression editor, but:
- Web-native and canvas-integrated
- LLM-era aware (strip fences, truncate tokens, detect language)
- Debuggable against the real Go backend — no parity drift
- Extensible in two Go files; zero other files change

---

## 2. Data Model

### 2.1 TransformStepConfig (extended)

The existing Go struct gains a new field:

```go
// TransformStepConfig configures one transform step.
// All three modes (expressions, extractions, functions) are additive and
// execute in order: expressions first, extractions second, functions third.
type TransformStepConfig struct {
    // Existing fields — unchanged, zero-value when absent.
    Expressions map[string]string  `json:"expressions,omitempty"`
    Extractions []TransformExtract `json:"extractions,omitempty"`

    // New: ordered function pipeline.
    Functions []FunctionStep `json:"functions,omitempty"`
}
```

### 2.2 FunctionStep — one call in the pipeline

```go
// FunctionStep is one call in the function pipeline.
// The output of step N is available as a pipeline variable immediately;
// step N+1 can use it as its input_var.
type FunctionStep struct {
    // Fn is the function name, e.g. "trim", "parse_json", "base64_encode".
    // Must be a key in the function registry.
    Fn string `json:"fn"`

    // InputVar is the pipeline variable whose value is passed as the
    // primary input to the function. Required for all functions.
    InputVar string `json:"input_var"`

    // OutputVar is the pipeline variable to write the result into.
    // May be the same as InputVar to update in place.
    OutputVar string `json:"output_var"`

    // Args holds additional named arguments for functions that require them.
    // Keys and value types are defined per-function in the registry.
    // Values are always strings; numeric conversion happens inside the function.
    Args map[string]string `json:"args,omitempty"`
}
```

### 2.3 JSON Schema (for canvas serialization and API validation)

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "FunctionStep",
  "type": "object",
  "required": ["fn", "input_var", "output_var"],
  "additionalProperties": false,
  "properties": {
    "fn": {
      "type": "string",
      "description": "Function name. Must be a key in the function registry."
    },
    "input_var": {
      "type": "string",
      "description": "Pipeline variable name holding the primary input value."
    },
    "output_var": {
      "type": "string",
      "description": "Pipeline variable name to write the result into."
    },
    "args": {
      "type": "object",
      "description": "Named arguments specific to this function. Keys and semantics defined per-function.",
      "additionalProperties": { "type": "string" }
    }
  }
}
```

### 2.4 Example: complete TransformStepConfig with all three modes

```json
{
  "expressions": {
    "greeting": "Hello, {{.user_name}}!"
  },
  "extractions": [
    { "from_var": "http_response", "json_path": "data.user.email", "var": "user_email" }
  ],
  "functions": [
    { "fn": "strip_fences",  "input_var": "llm_output",   "output_var": "clean_output" },
    { "fn": "trim",          "input_var": "clean_output",  "output_var": "clean_output" },
    { "fn": "upper",         "input_var": "user_name",     "output_var": "user_name_upper" },
    { "fn": "to_number",     "input_var": "score_str",     "output_var": "score" },
    { "fn": "round",         "input_var": "score",         "output_var": "score_rounded",
      "args": { "decimals": "2" } },
    { "fn": "coalesce",      "input_var": "preferred_lang","output_var": "lang",
      "args": { "fallback": "en" } },
    { "fn": "base64_encode", "input_var": "api_secret",    "output_var": "api_secret_b64" }
  ]
}
```

### 2.5 Execution semantics

The interpreter executes the three sections in order:
1. `expressions` — Go template evaluation
2. `extractions` — JSON path pulls
3. `functions` — function pipeline (left to right)

Within `functions`, each step executes sequentially. The output variable is written into `PipelineVars` immediately, so the next step can reference it as its `input_var`. A function that writes to the same variable as its `input_var` is an in-place update — this is valid and common.

---

## 3. Go Package Structure & Registry Design

### 3.1 Principle: Go is the single source of truth

All transform functions are implemented in Go only. There are no TypeScript function implementations.

The browser does not replicate function logic. Instead:
- At panel open, the browser calls `GET /api/v1/admin/transform-functions` to fetch the self-describing catalog (categories, function names, arg specs, examples). This drives the function picker dropdown.
- The Test tab POSTs to `POST /api/v1/admin/transform-test`, which runs `execTransform()` server-side and returns a `StepTrace` (step-by-step input/output/error/duration). The browser renders the trace.

This eliminates parity drift entirely. The test tab always shows exactly what production will do.

### 3.2 Package layout

```
go/internal/agentgen/transform/
  registry.go    — FunctionDef catalog; self-describing (name, category, description, args, examples)
  functions.go   — all built-in implementations
  executor.go    — runs a chain; returns StepTrace (per-step input/output/error/duration_ns)
  custom.go      — placeholder for future custom Go snippets; not implemented; file exists, exports nothing
```

No other files change when a new function is added.

### 3.3 Self-describing registry — `registry.go`

```go
// go/internal/agentgen/transform/registry.go

package transform

import "sync"

// ArgDef describes one named argument accepted by a function.
type ArgDef struct {
    Key         string `json:"key"`
    Description string `json:"description"`
    Required    bool   `json:"required"`
    Default     string `json:"default,omitempty"` // string form; empty means no default
}

// Example shows one sample invocation of a function.
type Example struct {
    In   string            `json:"in"`
    Args map[string]string `json:"args,omitempty"`
    Out  string            `json:"out"`
}

// FunctionDef is the self-describing catalog entry for one function.
// Served verbatim to the browser; also consumed by the executor and the AI assistant.
type FunctionDef struct {
    Name        string    `json:"name"`
    Category    string    `json:"category"`    // e.g. "llm-era", "string", "json"
    Description string    `json:"description"`
    Args        []ArgDef  `json:"args"`
    Examples    []Example `json:"examples"`
}

// transformFn is the signature every built-in function must satisfy.
// input is always a string (pipeline vars are stringly typed at the boundary).
// args are the caller-supplied named args; functions must validate required args.
// Returns the result as a string, or a descriptive error.
// Functions MUST be pure: no I/O, no side effects, no shared mutable state.
type transformFn func(input string, args map[string]string) (string, error)

type registryEntry struct {
    Def FunctionDef
    Fn  transformFn
}

var (
    mu       sync.RWMutex
    byName   = map[string]registryEntry{} // O(1) lookup by function name
    catalog  []FunctionDef                // ordered list for API response
)

// Register adds one function to the registry.
// Panics on name collision — catches duplicates at process startup, not at runtime.
// Call from init() in functions.go only.
func Register(def FunctionDef, fn transformFn) {
    mu.Lock()
    defer mu.Unlock()
    if _, exists := byName[def.Name]; exists {
        panic("transform: function already registered: " + def.Name)
    }
    byName[def.Name] = registryEntry{Def: def, Fn: fn}
    catalog = append(catalog, def)
}

// Lookup returns the implementation for a named function, or (nil, false).
// O(1) map lookup.
func Lookup(name string) (transformFn, bool) {
    mu.RLock()
    defer mu.RUnlock()
    e, ok := byName[name]
    return e.Fn, ok
}

// Catalog returns a snapshot of all registered FunctionDef entries.
// Used by the GET /transform-functions handler.
func Catalog() []FunctionDef {
    mu.RLock()
    defer mu.RUnlock()
    out := make([]FunctionDef, len(catalog))
    copy(out, catalog)
    return out
}
```

### 3.4 Registry example entry

Every function declaration follows this shape:

```go
Register(FunctionDef{
    Name:        "strip_fences",
    Category:    "llm-era",
    Description: "Removes markdown code fences (``` or ~~~, with optional language tag) and returns the inner content trimmed.",
    Args:        []ArgDef{},
    Examples: []Example{
        {In: "```json\n{}\n```", Out: "{}"},
    },
}, stripFences)
```

### 3.5 Registry feeds four consumers

| Consumer | How it uses the registry |
|---|---|
| `GET /admin/transform-functions` | Returns `Catalog()` as JSON → browser function picker |
| `POST /admin/transform-test` | `Lookup(fn)` for each step; executes chain; returns `StepTrace` |
| `POST /admin/transform-assist` | Passes `Catalog()` to LLM for chain suggestion (stub, 501 now) |
| `execTransform()` in `executor.go` | `Lookup(fn)` for each step during production pipeline execution |

---

## 4. API Endpoints

Three endpoints are added. All require admin auth (same middleware as other `/api/v1/admin/` routes).

### 4.1 GET /api/v1/admin/transform-functions

Returns the full self-describing function catalog. Called by the browser when the Transform node config panel opens.

**Response (200 OK):**

```json
{
  "functions": [
    {
      "name": "strip_fences",
      "category": "llm-era",
      "description": "Removes markdown code fences and returns the inner content trimmed.",
      "args": [],
      "examples": [
        { "in": "```json\n{}\n```", "out": "{}" }
      ]
    },
    {
      "name": "trim",
      "category": "string",
      "description": "Remove leading and trailing whitespace.",
      "args": [],
      "examples": [
        { "in": "  hello  ", "out": "hello" }
      ]
    }
    // ... all registered functions
  ]
}
```

No query parameters. Response is stable for the lifetime of the Go process (catalog is static after `init()`).

### 4.2 POST /api/v1/admin/transform-test

Runs a function chain against caller-supplied input vars. Go runs the exact same `execTransform()` path used in production. Returns a step-by-step `StepTrace`.

**Request body:**

```json
{
  "functions": [
    { "fn": "strip_fences", "input_var": "llm_output", "output_var": "clean" },
    { "fn": "trim",         "input_var": "clean",       "output_var": "clean" },
    { "fn": "parse_json",   "input_var": "clean",       "output_var": "parsed" }
  ],
  "vars": {
    "llm_output": "```json\n{ \"city\": \"NY\" }\n```"
  }
}
```

**Response (200 OK):**

```json
{
  "steps": [
    {
      "index": 0,
      "fn": "strip_fences",
      "input_var": "llm_output",
      "input_value": "```json\n{ \"city\": \"NY\" }\n```",
      "output_var": "clean",
      "output_value": "{ \"city\": \"NY\" }",
      "error": null,
      "duration_ns": 4200
    },
    {
      "index": 1,
      "fn": "trim",
      "input_var": "clean",
      "input_value": "{ \"city\": \"NY\" }",
      "output_var": "clean",
      "output_value": "{\"city\":\"NY\"}",
      "error": null,
      "duration_ns": 800
    },
    {
      "index": 2,
      "fn": "parse_json",
      "input_var": "clean",
      "input_value": "{\"city\":\"NY\"}",
      "output_var": "parsed",
      "output_value": "{\"city\":\"NY\"}",
      "error": null,
      "duration_ns": 1100
    }
  ],
  "final_vars": {
    "llm_output": "```json\n{ \"city\": \"NY\" }\n```",
    "clean": "{\"city\":\"NY\"}",
    "parsed": "{\"city\":\"NY\"}"
  },
  "error_at": null
}
```

On error mid-chain, `error_at` is the zero-based step index of the first failure. Steps after the error are omitted from `steps`. The response is still 200 — the trace itself conveys the error; 4xx is reserved for malformed requests.

**Response on unknown function (400 Bad Request):**

```json
{ "error": "unknown function: \"typo_fn\"" }
```

### 4.3 POST /api/v1/admin/transform-assist

Stub endpoint for the future AI chain suggestion feature. Returns 501 now.

**Response (501 Not Implemented):**

```json
{ "error": "transform-assist is not yet implemented" }
```

This endpoint is wired in the router immediately so the UI "AI Assistant" tab can reference it without requiring a router change when the feature ships.

---

## 5. Full Function Catalog

Each entry shows: **name**, description, args (if any), example input → output.

All functions receive `input` as a `string`. When a function operates on a non-string type (e.g. a number), it parses the string first and returns the result as a string. Pipeline vars are stringly typed at the boundary; downstream functions do their own coercion.

**Phase 1** functions are in scope for the initial implementation (~20 functions covering the most common LLM pipeline needs). **Phase 2** categories (Date/Time, Encoding, Conditional) follow in a separate implementation wave.

---

### 5.1 LLM-era (Phase 1)

Functions designed for the data patterns that appear specifically when working with LLM outputs. Highest priority — these appear in almost every agent built on the platform.

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `strip_fences` | Remove markdown code fences (``` or ~~~, with optional language tag) and return inner content trimmed | — | ` ```json\n{"a":1}\n``` ` → `'{"a":1}'` |
| `normalize_whitespace` | Collapse all runs of whitespace (spaces, tabs, newlines) to single spaces; trim | — | `"hello\n\n  world  "` → `"hello world"` |
| `extract_code_block` | Extract the first code block of a given language (e.g. `json`, `python`); returns empty string if not found | `language` | ` ```python\nprint("hi")\n``` ` + `{language:"python"}` → `'print("hi")'` |

---

### 5.2 JSON / Structure (Phase 1)

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `parse_json` | Parse a JSON string and return it as-is (validates it is valid JSON; fails hard if not) | — | `'{"a":1}'` → `'{"a":1}'` (identity, but errors if malformed) |
| `to_string` | Serialize any value (including objects/arrays) to a compact JSON string | — | `{"a":1}` (as Go map) → `'{"a":1}'` |
| `json_path` | Extract a value from a JSON object by dot-path or bracket notation (e.g. `data.users[0].name`) | `path` | `'{"data":{"city":"NY"}}'` + `{path:"data.city"}` → `"NY"` |
| `merge_json` | Deep-merge a JSON literal into the input object; input wins on key conflicts | `patch` | `'{"a":1}'` + `{patch:'{"b":2}'}` → `'{"a":1,"b":2}'` |

**Phase 2** (JSON): `json_keys`, `array_length`, `array_get`, `flatten`

---

### 5.3 String (Phase 1)

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `trim` | Remove leading and trailing whitespace | — | `"  hello  "` → `"hello"` |
| `upper` | Convert to UPPER CASE | — | `"hello world"` → `"HELLO WORLD"` |
| `lower` | Convert to lower case | — | `"HELLO WORLD"` → `"hello world"` |
| `replace` | Replace all occurrences of a substring | `old`, `new` | `"cat sat mat"` + `{old:"at",new:"ig"}` → `"cig sig mig"` |
| `substring` | Extract a substring by start and optional end index (0-based, exclusive end) | `start`, `end?` | `"hello world"` + `{start:"6",end:"11"}` → `"world"` |
| `concat` | Append a literal value to the input | `value` | `"Hello"` + `{value:", world!"}` → `"Hello, world!"` |
| `split` | Split by a delimiter; serializes result as JSON array | `delimiter` | `"a,b,c"` + `{delimiter:","}` → `'["a","b","c"]'` |
| `join` | Join a JSON array of strings with a separator | `separator` | `'["a","b","c"]'` + `{separator:", "}` → `"a, b, c"` |

**Phase 2** (String): `ltrim`, `rtrim`, `length`, `pad_left`, `pad_right`, `regex_replace`, `regex_extract`

---

### 5.4 Type / Validation (Phase 1)

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `is_json` | Return `"true"` if the input is valid JSON, `"false"` otherwise | — | `'{"ok":true}'` → `"true"` |
| `is_empty` | Return `"true"` if the input is empty string or whitespace-only | — | `"  "` → `"true"` |
| `coalesce` | Return the input if non-empty; otherwise return the fallback | `fallback` | `""` + `{fallback:"default"}` → `"default"` |
| `default_if_empty` | Alias for `coalesce` (more readable in conditional pipelines) | `default` | `""` + `{default:"N/A"}` → `"N/A"` |
| `assert_json` | Pass through if valid JSON; **error the step** (halt pipeline) if not | — | `"not json"` → error: `"assert_json: invalid JSON"` |

**Phase 2** (Type): `is_number`, `type_of`

---

### 5.5 Numeric (Phase 1)

All numeric functions parse their input as a float64. Output is a decimal string.

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `to_number` | Parse string to number; returns the canonical decimal form | — | `"  3.14  "` → `"3.14"` |
| `round` | Round to N decimal places (default 0) | `decimals?` | `"3.14159"` + `{decimals:"2"}` → `"3.14"` |

**Phase 2** (Numeric): `to_int`, `floor`, `ceil`, `abs`, `add`, `subtract`, `multiply`, `divide`, `modulo`, `min`, `max`

---

### 5.6 Date / Time (Phase 2)

All datetime functions use RFC3339 (e.g. `"2024-03-15T10:30:00Z"`) as the canonical wire format unless `format` is specified. When input is a Unix timestamp (integer string), functions detect and parse accordingly.

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `now` | Return the current UTC time in RFC3339 format (ignores input) | — | (any) → `"2024-03-15T10:30:00Z"` |
| `format_date` | Format a datetime string using a Go layout string | `layout` | `"2024-03-15T10:30:00Z"` + `{layout:"Jan 2, 2006"}` → `"Mar 15, 2024"` |
| `parse_date` | Parse a date string in a given layout to RFC3339 | `layout` | `"15/03/2024"` + `{layout:"02/01/2006"}` → `"2024-03-15T00:00:00Z"` |
| `date_diff` | Compute the difference between the input date and a reference date | `from`, `unit` (`days`/`hours`/`minutes`) | `"2024-03-20T00:00:00Z"` + `{from:"2024-03-15T00:00:00Z",unit:"days"}` → `"5"` |
| `add_days` | Add N days to a datetime | `days` | `"2024-03-15T00:00:00Z"` + `{days:"7"}` → `"2024-03-22T00:00:00Z"` |
| `timezone_convert` | Convert a UTC datetime to a named timezone | `timezone` | `"2024-03-15T10:00:00Z"` + `{timezone:"America/New_York"}` → `"2024-03-15T06:00:00-04:00"` |

---

### 5.7 Conditional / Flow (Phase 2)

These functions implement conditional logic that would otherwise require a Branch node. They operate inline without routing.

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `if_empty` | Return `then` if input is empty; otherwise return input unchanged | `then` | `""` + `{then:"fallback"}` → `"fallback"` |
| `if_null` | Return `then` if input is empty string or the literal `"null"`; otherwise return input | `then` | `"null"` + `{then:"N/A"}` → `"N/A"` |
| `if_equals` | Return `then` if input equals `value`; else return `else` | `value`, `then`, `else` | `"yes"` + `{value:"yes",then:"1",else:"0"}` → `"1"` |
| `switch` | Match input against a comma-separated key:value list; return default if no match | `cases`, `default` | `"fr"` + `{cases:"en:English,fr:French,de:German",default:"Unknown"}` → `"French"` |
| `not` | Return `"true"` if input is falsy (`""`, `"false"`, `"0"`, `"null"`); else `"false"` | — | `"false"` → `"true"` |

---

### 5.8 Encoding (Phase 2)

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `base64_encode` | Standard Base64 encode (RFC 4648) | — | `"hello"` → `"aGVsbG8="` |
| `base64_decode` | Standard Base64 decode; error on invalid input | — | `"aGVsbG8="` → `"hello"` |
| `url_encode` | Percent-encode for use in URL query strings | — | `"hello world"` → `"hello+world"` |
| `url_decode` | Decode percent-encoded URL query strings | — | `"hello+world"` → `"hello world"` |
| `md5` | Return hex-encoded MD5 hash | — | `"hello"` → `"5d41402abc4b2a76b9719d911017c592"` |
| `sha256` | Return hex-encoded SHA-256 hash | — | `"hello"` → `"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"` |

---

### 5.9 LLM-era additional functions (Phase 2)

| Function | Description | Args | Input → Output |
|---|---|---|---|
| `truncate_tokens` | Approximate token truncation: split on whitespace and punctuation, keep first N tokens, rejoin | `max_tokens` | `"the quick brown fox"` + `{max_tokens:"3"}` → `"the quick brown"` |
| `detect_language` | Detect the BCP-47 language tag of the input text using a lightweight heuristic | — | `"Bonjour le monde"` → `"fr"` |

---

## 6. UI Design — Three-Tab Transform Panel

The Transform node's config panel (right sidebar on the canvas) has three tabs: **Build**, **Test**, and **AI Assistant**. The existing config UI (expressions, extractions) lives in the Build tab alongside the new function pipeline section.

```
┌─────────────────────────────────────────────────────────────┐
│  Transform                                         [×]       │
│─────────────────────────────────────────────────────────────│
│  [ Build ]  [ Test ]  [ AI Assistant ]                       │
└─────────────────────────────────────────────────────────────┘
```

### 6.1 Build tab

Contains the existing expressions and extractions UI unchanged, followed by a new "FUNCTION PIPELINE" section:

```
┌─────────────────────────────────────────────────────┐
│  FUNCTION PIPELINE                                   │
│  ─────────────────────────────────────────────────  │
│  Step 1  [strip_fences ▾]  in:[llm_output] →        │
│                            out:[clean_output]  [×]  │
│  Step 2  [trim          ▾]  in:[clean_output] →      │
│                            out:[clean_output]  [×]  │
│  Step 3  [json_path     ▾]  in:[clean_output] →      │
│           args: path=[city]  out:[city_name]   [×]  │
│  ─────────────────────────────────────────────────  │
│  [+ Add function step]                               │
└─────────────────────────────────────────────────────┘
```

**Function picker:** On panel open, the browser calls `GET /api/v1/admin/transform-functions` and caches the result for the session. The function name field is a `<select>` grouped by category using `<optgroup>` elements. Selecting a function updates `fn` in the step and renders the correct `args` fields (from the catalog's `args` array). Does not clear `input_var` or `output_var`.

**Input var dropdown:** Populated from the pipeline variables available at the point of this step (upstream outputs). Falls back to a free-text input.

### 6.2 Test tab

Paste input vars, click Run Test, see the step-by-step trace returned by Go.

```
┌─────────────────────────────────────────────────────────────┐
│  [ Build ]  [ Test ]  [ AI Assistant ]                       │
│──────────────── Test tab active ───────────────────────────│
│                                                              │
│  SAMPLE INPUT VARIABLES                                      │
│  ┌──────────────────┬────────────────────────────────────┐  │
│  │ variable name    │ value                              │  │
│  ├──────────────────┼────────────────────────────────────┤  │
│  │ llm_output       │ ```json\n{"city":"NY"}\n```        │  │
│  ├──────────────────┼────────────────────────────────────┤  │
│  │ score_str        │ 3.14159                            │  │
│  ├──────────────────┼────────────────────────────────────┤  │
│  │ [+ Add variable] │                                    │  │
│  └──────────────────┴────────────────────────────────────┘  │
│                                                              │
│  [▶ Run Test]                                                │
│                                                              │
│  STEP-BY-STEP RESULTS                                        │
│  ─────────────────────────────────────────────────────────  │
│  Step 1 · strip_fences                          4.2 µs       │
│    in:  llm_output → ```json\n{"city":"NY"}\n```            │
│    out: clean_output → {"city":"NY"}              ✓         │
│  ─────────────────────────────────────────────────────────  │
│  Step 2 · trim                                  0.8 µs       │
│    in:  clean_output → {"city":"NY"}                        │
│    out: clean_output → {"city":"NY"}              ✓         │
│  ─────────────────────────────────────────────────────────  │
│  Step 3 · json_path   args: path=city           1.1 µs       │
│    in:  clean_output → {"city":"NY"}                        │
│    out: city_name    → NY                         ✓         │
│  ─────────────────────────────────────────────────────────  │
│                                                              │
│  3 variables written. 0 errors.                              │
└─────────────────────────────────────────────────────────────┘
```

**Behavior:**

- Input variables panel is pre-populated with the unique `input_var` names from all configured function steps (auto-detected so the builder knows what to provide).
- Each row: editable `variable name` | editable `value` (multi-line textarea) | `[×]` delete.
- `[+ Add variable]` appends a blank row.
- `[▶ Run Test]` POSTs `{ functions: [...], vars: {...} }` to `POST /api/v1/admin/transform-test` and renders the `StepTrace` response.
- One result card per step: step number, function name, args (if any), input var name + value (truncated to ~80 chars with `[show more]` toggle), output var name + value, timing from `duration_ns`, success checkmark or red error text.
- On error mid-chain, steps after the error are shown greyed out and marked as not executed.
- Tab switching does NOT reset test inputs — they persist for the lifetime of the panel being open. Closing the panel clears them.

### 6.3 AI Assistant tab

Placeholder tab. Displays a coming-soon message. The tab is present now so the layout is established and no router or UI restructuring is needed when the feature ships.

```
┌─────────────────────────────────────────────────────────────┐
│  [ Build ]  [ Test ]  [ AI Assistant ]                       │
│────────── AI Assistant tab active ─────────────────────────│
│                                                              │
│  AI-assisted chain building is coming soon.                  │
│                                                              │
│  Describe what you want to do with your data and the AI      │
│  will suggest a function chain to get you there.             │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

The "Run" button in this tab POSTs to `POST /api/v1/admin/transform-assist`, which currently returns 501. The UI should handle the 501 gracefully (show the same coming-soon message).

---

## 7. Go Runtime Implementation Plan

### 7.1 Package and file layout

Create the package `go/internal/agentgen/transform/` with four files:

| File | Contents |
|---|---|
| `registry.go` | `FunctionDef`, `ArgDef`, `Example`, `transformFn`, `Register`, `Lookup`, `Catalog` (see section 3.3) |
| `functions.go` | All built-in `init()` registrations and helper functions |
| `executor.go` | `StepTrace`, `StepResult`, `ExecChain`, integration with `execTransform` |
| `custom.go` | Package comment noting this is the future hook for custom Go snippets; no exports |

### 7.2 `functions.go` structure

The `init()` function calls `Register` for every built-in. Functions are grouped by category with comments. Each registration is a self-contained block: the `FunctionDef` followed by the implementation.

```go
// go/internal/agentgen/transform/functions.go

package transform

import (
    "encoding/json"
    "fmt"
    "regexp"
    "strconv"
    "strings"
    "unicode"
)

func init() {
    // ── LLM-era ──────────────────────────────────────────────────────────────
    Register(FunctionDef{
        Name:        "strip_fences",
        Category:    "llm-era",
        Description: "Removes markdown code fences (``` or ~~~, with optional language tag) and returns the inner content trimmed.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: "```json\n{}\n```", Out: "{}"}},
    }, func(input string, _ map[string]string) (string, error) {
        return stripMarkdownFences(input), nil
    })

    Register(FunctionDef{
        Name:        "normalize_whitespace",
        Category:    "llm-era",
        Description: "Collapses all runs of whitespace (spaces, tabs, newlines) to single spaces and trims.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: "hello\n\n  world  ", Out: "hello world"}},
    }, func(input string, _ map[string]string) (string, error) {
        return strings.Join(strings.Fields(input), " "), nil
    })

    Register(FunctionDef{
        Name:        "extract_code_block",
        Category:    "llm-era",
        Description: "Extracts the first code block of a given language tag; returns empty string if not found.",
        Args:        []ArgDef{{Key: "language", Description: "Language tag (e.g. json, python)", Required: true}},
        Examples:    []Example{{In: "```python\nprint('hi')\n```", Args: map[string]string{"language": "python"}, Out: "print('hi')"}},
    }, extractCodeBlock)

    // ── String ────────────────────────────────────────────────────────────────
    Register(FunctionDef{
        Name:        "trim",
        Category:    "string",
        Description: "Removes leading and trailing whitespace.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: "  hello  ", Out: "hello"}},
    }, func(input string, _ map[string]string) (string, error) {
        return strings.TrimSpace(input), nil
    })

    Register(FunctionDef{
        Name:        "upper",
        Category:    "string",
        Description: "Converts to UPPER CASE.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: "hello world", Out: "HELLO WORLD"}},
    }, func(input string, _ map[string]string) (string, error) {
        return strings.ToUpper(input), nil
    })

    Register(FunctionDef{
        Name:        "lower",
        Category:    "string",
        Description: "Converts to lower case.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: "HELLO WORLD", Out: "hello world"}},
    }, func(input string, _ map[string]string) (string, error) {
        return strings.ToLower(input), nil
    })

    Register(FunctionDef{
        Name:        "replace",
        Category:    "string",
        Description: "Replaces all occurrences of old with new.",
        Args: []ArgDef{
            {Key: "old", Description: "Substring to find", Required: true},
            {Key: "new", Description: "Replacement string", Required: true},
        },
        Examples: []Example{{In: "cat sat mat", Args: map[string]string{"old": "at", "new": "ig"}, Out: "cig sig mig"}},
    }, func(input string, args map[string]string) (string, error) {
        return strings.ReplaceAll(input, args["old"], args["new"]), nil
    })

    Register(FunctionDef{
        Name:        "substring",
        Category:    "string",
        Description: "Extracts a substring by start and optional end index (0-based, exclusive end). Unicode-safe.",
        Args: []ArgDef{
            {Key: "start", Description: "Start index (0-based)", Required: true},
            {Key: "end", Description: "End index (exclusive); omit for rest of string", Required: false},
        },
        Examples: []Example{{In: "hello world", Args: map[string]string{"start": "6", "end": "11"}, Out: "world"}},
    }, substringFn)

    Register(FunctionDef{
        Name:        "concat",
        Category:    "string",
        Description: "Appends a literal value to the input.",
        Args:        []ArgDef{{Key: "value", Description: "String to append", Required: true}},
        Examples:    []Example{{In: "Hello", Args: map[string]string{"value": ", world!"}, Out: "Hello, world!"}},
    }, func(input string, args map[string]string) (string, error) {
        return input + args["value"], nil
    })

    Register(FunctionDef{
        Name:        "split",
        Category:    "string",
        Description: "Splits by a delimiter and returns a JSON array of strings.",
        Args:        []ArgDef{{Key: "delimiter", Description: "Delimiter string", Required: true}},
        Examples:    []Example{{In: "a,b,c", Args: map[string]string{"delimiter": ","}, Out: `["a","b","c"]`}},
    }, func(input string, args map[string]string) (string, error) {
        parts := strings.Split(input, args["delimiter"])
        b, _ := json.Marshal(parts)
        return string(b), nil
    })

    Register(FunctionDef{
        Name:        "join",
        Category:    "string",
        Description: "Joins a JSON array of strings with a separator.",
        Args:        []ArgDef{{Key: "separator", Description: "Separator string", Required: true}},
        Examples:    []Example{{In: `["a","b","c"]`, Args: map[string]string{"separator": ", "}, Out: "a, b, c"}},
    }, func(input string, args map[string]string) (string, error) {
        var parts []string
        if err := json.Unmarshal([]byte(input), &parts); err != nil {
            return "", fmt.Errorf("join: input is not a JSON array of strings")
        }
        return strings.Join(parts, args["separator"]), nil
    })

    // ── JSON / Structure ──────────────────────────────────────────────────────
    Register(FunctionDef{
        Name:        "parse_json",
        Category:    "json",
        Description: "Validates that the input is well-formed JSON and passes it through. Errors if not.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: `{"a":1}`, Out: `{"a":1}`}},
    }, func(input string, _ map[string]string) (string, error) {
        var v any
        if err := json.Unmarshal([]byte(input), &v); err != nil {
            return "", fmt.Errorf("parse_json: invalid JSON: %w", err)
        }
        return input, nil
    })

    Register(FunctionDef{
        Name:        "to_string",
        Category:    "json",
        Description: "Serializes any value to a compact JSON string.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: `{"a":1}`, Out: `{"a":1}`}},
    }, func(input string, _ map[string]string) (string, error) {
        var v any
        if err := json.Unmarshal([]byte(input), &v); err != nil {
            // not JSON — return as quoted string
            b, _ := json.Marshal(input)
            return string(b), nil
        }
        b, _ := json.Marshal(v)
        return string(b), nil
    })

    Register(FunctionDef{
        Name:        "json_path",
        Category:    "json",
        Description: "Extracts a value from a JSON object by dot-path or bracket notation.",
        Args:        []ArgDef{{Key: "path", Description: "Dot-path expression, e.g. data.users[0].name", Required: true}},
        Examples:    []Example{{In: `{"data":{"city":"NY"}}`, Args: map[string]string{"path": "data.city"}, Out: "NY"}},
    }, jsonPathFn)

    Register(FunctionDef{
        Name:        "merge_json",
        Category:    "json",
        Description: "Deep-merges a JSON literal into the input object; input keys win on conflict.",
        Args:        []ArgDef{{Key: "patch", Description: "JSON object to merge in", Required: true}},
        Examples:    []Example{{In: `{"a":1}`, Args: map[string]string{"patch": `{"b":2}`}, Out: `{"a":1,"b":2}`}},
    }, mergeJSONFn)

    // ── Type / Validation ─────────────────────────────────────────────────────
    Register(FunctionDef{
        Name:        "is_json",
        Category:    "type",
        Description: `Returns "true" if the input is valid JSON, "false" otherwise.`,
        Args:        []ArgDef{},
        Examples:    []Example{{In: `{"ok":true}`, Out: "true"}, {In: "not json", Out: "false"}},
    }, func(input string, _ map[string]string) (string, error) {
        var v any
        if json.Unmarshal([]byte(input), &v) == nil {
            return "true", nil
        }
        return "false", nil
    })

    Register(FunctionDef{
        Name:        "is_empty",
        Category:    "type",
        Description: `Returns "true" if the input is empty string or whitespace-only.`,
        Args:        []ArgDef{},
        Examples:    []Example{{In: "  ", Out: "true"}, {In: "hello", Out: "false"}},
    }, func(input string, _ map[string]string) (string, error) {
        if strings.TrimSpace(input) == "" {
            return "true", nil
        }
        return "false", nil
    })

    Register(FunctionDef{
        Name:        "coalesce",
        Category:    "type",
        Description: "Returns the input if non-empty; otherwise returns the fallback.",
        Args:        []ArgDef{{Key: "fallback", Description: "Value to return if input is empty", Required: true}},
        Examples:    []Example{{In: "", Args: map[string]string{"fallback": "default"}, Out: "default"}},
    }, func(input string, args map[string]string) (string, error) {
        if strings.TrimSpace(input) == "" {
            return args["fallback"], nil
        }
        return input, nil
    })

    Register(FunctionDef{
        Name:        "default_if_empty",
        Category:    "type",
        Description: "Alias for coalesce. Returns the input if non-empty; otherwise returns the default.",
        Args:        []ArgDef{{Key: "default", Description: "Value to return if input is empty", Required: true}},
        Examples:    []Example{{In: "", Args: map[string]string{"default": "N/A"}, Out: "N/A"}},
    }, func(input string, args map[string]string) (string, error) {
        if strings.TrimSpace(input) == "" {
            return args["default"], nil
        }
        return input, nil
    })

    Register(FunctionDef{
        Name:        "assert_json",
        Category:    "type",
        Description: "Passes through if input is valid JSON; halts the pipeline with an error if not.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: `{"ok":true}`, Out: `{"ok":true}`}},
    }, func(input string, _ map[string]string) (string, error) {
        var v any
        if err := json.Unmarshal([]byte(input), &v); err != nil {
            return "", fmt.Errorf("assert_json: invalid JSON")
        }
        return input, nil
    })

    // ── Numeric ───────────────────────────────────────────────────────────────
    Register(FunctionDef{
        Name:        "to_number",
        Category:    "numeric",
        Description: "Parses string to number and returns its canonical decimal form.",
        Args:        []ArgDef{},
        Examples:    []Example{{In: "  3.14  ", Out: "3.14"}},
    }, func(input string, _ map[string]string) (string, error) {
        f, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
        if err != nil {
            return "", fmt.Errorf("to_number: cannot parse %q as a number", input)
        }
        return strconv.FormatFloat(f, 'f', -1, 64), nil
    })

    Register(FunctionDef{
        Name:        "round",
        Category:    "numeric",
        Description: "Rounds to N decimal places (default 0).",
        Args:        []ArgDef{{Key: "decimals", Description: "Number of decimal places (default 0)", Required: false, Default: "0"}},
        Examples:    []Example{{In: "3.14159", Args: map[string]string{"decimals": "2"}, Out: "3.14"}},
    }, roundFn)
}
```

### 7.3 Key helpers in `functions.go`

```go
var fenceRE = regexp.MustCompile(
    `(?s)^` +
    "(?:```|~~~)" +
    `[a-zA-Z0-9_+\-]*\n?` +
    `(.*?)` +
    `\n?(?:` + "```" + `|~~~)\s*$`,
)

func stripMarkdownFences(input string) string {
    input = strings.TrimSpace(input)
    if m := fenceRE.FindStringSubmatch(input); m != nil {
        return strings.TrimSpace(m[1])
    }
    return input
}

func substringFn(input string, args map[string]string) (string, error) {
    runes := []rune(input)
    start, err := strconv.Atoi(args["start"])
    if err != nil {
        return "", fmt.Errorf("substring: start must be an integer")
    }
    if start < 0 {
        start = 0
    }
    if start > len(runes) {
        return "", nil
    }
    if endStr, ok := args["end"]; ok && endStr != "" {
        end, err := strconv.Atoi(endStr)
        if err != nil {
            return "", fmt.Errorf("substring: end must be an integer")
        }
        if end > len(runes) {
            end = len(runes)
        }
        return string(runes[start:end]), nil
    }
    return string(runes[start:]), nil
}
```

`jsonPathFn`, `mergeJSONFn`, `extractCodeBlock`, and `roundFn` are similarly standalone named functions in the same file.

### 7.4 `executor.go` — StepTrace and ExecChain

```go
// go/internal/agentgen/transform/executor.go

package transform

import (
    "fmt"
    "time"
)

// StepResult holds the trace for one step in the pipeline.
type StepResult struct {
    Index       int               `json:"index"`
    Fn          string            `json:"fn"`
    InputVar    string            `json:"input_var"`
    InputValue  string            `json:"input_value"`
    OutputVar   string            `json:"output_var"`
    OutputValue string            `json:"output_value,omitempty"`
    Error       string            `json:"error,omitempty"`
    DurationNs  int64             `json:"duration_ns"`
}

// StepTrace is the full trace returned by POST /transform-test.
type StepTrace struct {
    Steps     []StepResult      `json:"steps"`
    FinalVars map[string]string `json:"final_vars"`
    ErrorAt   *int              `json:"error_at"` // nil if no error
}

// ExecChain runs a slice of FunctionSteps against a copy of the provided vars.
// Always returns a StepTrace — even partial on error. Used by the test endpoint
// and by execTransform in production (production callers use the error return only).
func ExecChain(steps []FunctionStep, vars map[string]string) (StepTrace, error) {
    localVars := make(map[string]string, len(vars))
    for k, v := range vars {
        localVars[k] = v
    }

    trace := StepTrace{FinalVars: localVars}

    for i, step := range steps {
        fn, ok := Lookup(step.Fn)
        if !ok {
            errMsg := fmt.Sprintf("unknown function: %q", step.Fn)
            idx := i
            trace.ErrorAt = &idx
            return trace, fmt.Errorf("transform step %d: %s", i+1, errMsg)
        }

        inputValue := localVars[step.InputVar] // empty string if absent

        start := time.Now()
        result, err := fn(inputValue, step.Args)
        dur := time.Since(start).Nanoseconds()

        sr := StepResult{
            Index:      i,
            Fn:         step.Fn,
            InputVar:   step.InputVar,
            InputValue: inputValue,
            OutputVar:  step.OutputVar,
            DurationNs: dur,
        }

        if err != nil {
            sr.Error = err.Error()
            trace.Steps = append(trace.Steps, sr)
            idx := i
            trace.ErrorAt = &idx
            return trace, fmt.Errorf("transform step %d (%s): %w", i+1, step.Fn, err)
        }

        sr.OutputValue = result
        localVars[step.OutputVar] = result
        trace.Steps = append(trace.Steps, sr)
    }

    return trace, nil
}
```

### 7.5 Integration with `execTransform`

The interpreter calls `ExecChain` for the `functions` section:

```go
func (interp *Interpreter) execTransform(step *StepSpec, vars PipelineVars) error {
    var cfg TransformStepConfig
    if err := json.Unmarshal(step.Config, &cfg); err != nil {
        return fmt.Errorf("parse transform config: %w", err)
    }
    // 1. Expressions (existing)
    for outputVar, expr := range cfg.Expressions {
        val, err := renderTemplate(expr, vars)
        if err != nil {
            return fmt.Errorf("transform expression for %q: %w", outputVar, err)
        }
        vars[outputVar] = val
    }
    // 2. Extractions (existing)
    for _, ext := range cfg.Extractions {
        raw, ok := vars[ext.FromVar]
        if !ok { continue }
        var parsed map[string]any
        switch v := raw.(type) {
        case map[string]any:
            parsed = v
        case string:
            if err := json.Unmarshal([]byte(v), &parsed); err != nil { continue }
        default:
            continue
        }
        if val := extractJSONPath(parsed, ext.JSONPath); val != "" {
            vars[ext.Var] = val
        }
    }
    // 3. Function pipeline (new)
    if len(cfg.Functions) > 0 {
        strVars := pipelineVarsToStrings(vars)
        _, err := transform.ExecChain(cfg.Functions, strVars)
        if err != nil {
            return err
        }
        // write results back into vars
        for k, v := range strVars {
            vars[k] = v
        }
    }
    return nil
}
```

### 7.6 HTTP handlers (in `go/internal/agentgen/` router file)

```go
// GET /api/v1/admin/transform-functions
func handleTransformFunctions(w http.ResponseWriter, r *http.Request) {
    catalog := transform.Catalog()
    writeJSON(w, http.StatusOK, map[string]any{"functions": catalog})
}

// POST /api/v1/admin/transform-test
func handleTransformTest(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Functions []transform.FunctionStep `json:"functions"`
        Vars      map[string]string        `json:"vars"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
        return
    }
    // validate all functions exist before running
    for _, step := range req.Functions {
        if _, ok := transform.Lookup(step.Fn); !ok {
            writeJSON(w, http.StatusBadRequest, map[string]string{
                "error": fmt.Sprintf("unknown function: %q", step.Fn),
            })
            return
        }
    }
    trace, _ := transform.ExecChain(req.Functions, req.Vars)
    writeJSON(w, http.StatusOK, trace)
}

// POST /api/v1/admin/transform-assist
func handleTransformAssist(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusNotImplemented, map[string]string{
        "error": "transform-assist is not yet implemented",
    })
}
```

### 7.7 Error handling policy

- **Unknown function** — 400 Bad Request at the test endpoint; hard error at runtime. Prevents silent data loss from typos.
- **Missing `input_var`** — if the variable does not exist in vars, `inputValue` is the empty string. Functions that require non-empty input return an error.
- **`assert_json`** — the one function that deliberately errors the step, halting the pipeline. All other functions should return meaningful errors for bad input rather than silently returning empty string.
- **`args` key missing** — each function must check `args["key"]` and return a clear error if required. Optional args use the default from their `ArgDef.Default`.
- **Error format** — `fmt.Errorf("transform step %d (%s): %w", i+1, step.Fn, err)` — includes step number and function name.

### 7.8 Performance requirements

- Registry lookup is O(1) by map key — the `byName` map in `registry.go`.
- Functions must be pure: no I/O, no side effects, no shared mutable state. This enables safe parallel testing in future.
- `StepTrace` includes `duration_ns` per step for future performance visibility.
- Adding a new function = edit `registry.go` (add `FunctionDef`) + edit `functions.go` (add `Register` call + implementation). Zero other files change.

### 7.9 Required Go packages

All Phase 1 functions use the standard library only:
- `encoding/json`, `fmt`, `regexp`, `strconv`, `strings`, `time`, `unicode`

Phase 2 additions:
- `detect_language` — requires `golang.org/x/text/language` (add to `go.mod`).
- `timezone_convert` — uses `time.LoadLocation`; requires `import _ "time/tzdata"` in environments without system timezone data.

No external LLM libraries. No reflection.

### 7.10 Tests

New test file: `go/internal/agentgen/transform/functions_test.go`

Each function gets at minimum:
- Happy path: expected input → expected output
- Error path: bad args or non-parseable input returns a wrapped error
- Edge cases: empty string, unicode, very long strings

Integration test in `executor_test.go`: full `ExecChain` with a mixed-category chain against known inputs, verifying the full `StepTrace` including `DurationNs > 0`.

Update `go/TEST_INDEX.md` in the same commit.

---

## 8. Browser Implementation Plan

### 8.1 Fetching the catalog

The browser does not contain any function implementations. On Transform panel open, fetch once:

```typescript
// frontend/src/lib/transformCatalog.ts

export type ArgDef = {
  key: string;
  description: string;
  required: boolean;
  default?: string;
};

export type Example = {
  in: string;
  args?: Record<string, string>;
  out: string;
};

export type FunctionDef = {
  name: string;
  category: string;
  description: string;
  args: ArgDef[];
  examples: Example[];
};

export type TransformCatalog = {
  functions: FunctionDef[];
};

// Keyed by function name for O(1) arg/description lookup in the UI.
export type FunctionIndex = Record<string, FunctionDef>;

let _catalogCache: TransformCatalog | null = null;

export async function fetchTransformCatalog(): Promise<TransformCatalog> {
  if (_catalogCache) return _catalogCache;
  const res = await fetch('/api/v1/admin/transform-functions');
  if (!res.ok) throw new Error('Failed to load transform function catalog');
  _catalogCache = await res.json();
  return _catalogCache!;
}

export function buildFunctionIndex(catalog: TransformCatalog): FunctionIndex {
  return Object.fromEntries(catalog.functions.map(f => [f.name, f]));
}
```

### 8.2 Test tab runner

```typescript
// frontend/src/lib/transformTester.ts

import type { FunctionStep } from './transformTypes';

export type StepResult = {
  index: number;
  fn: string;
  input_var: string;
  input_value: string;
  output_var: string;
  output_value?: string;
  error?: string;
  duration_ns: number;
};

export type StepTrace = {
  steps: StepResult[];
  final_vars: Record<string, string>;
  error_at: number | null;
};

export async function runTransformTest(
  functions: FunctionStep[],
  vars: Record<string, string>,
): Promise<StepTrace> {
  const res = await fetch('/api/v1/admin/transform-test', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ functions, vars }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error ?? `transform-test returned ${res.status}`);
  }
  return res.json();
}
```

### 8.3 Changes to `builder/page.tsx`

**A. State additions** (alongside existing `selectedStep`, `cfg`)

```typescript
const [transformTab, setTransformTab] = useState<'build' | 'test' | 'assist'>('build');
const [catalog, setCatalog] = useState<TransformCatalog | null>(null);
const [functionIndex, setFunctionIndex] = useState<FunctionIndex>({});
const [testVars, setTestVars] = useState<Array<{ key: string; value: string }>>([]);
const [testTrace, setTestTrace] = useState<StepTrace | null>(null);
const [testRunning, setTestRunning] = useState(false);
```

**B. Catalog fetch on panel open** (when `d.step_type === 'transform'` is selected)

```typescript
useEffect(() => {
  if (selectedStep?.step_type !== 'transform') return;
  fetchTransformCatalog().then(cat => {
    setCatalog(cat);
    setFunctionIndex(buildFunctionIndex(cat));
  });
}, [selectedStep?.id]);
```

**C. Config panel — three-tab bar** (replaces the plain header)

Render three tabs: `[ Build ]` `[ Test ]` `[ AI Assistant ]`. Active tab highlighted with the node's indigo accent color.

**D. Build tab — function pipeline section** (new subsection below existing expressions/extractions)

- Ordered list of function steps.
- Each step: function picker `<select>` grouped by `<optgroup>` per category (from `catalog.functions`), input_var text field (or dropdown of upstream vars), output_var text field, dynamic arg fields (from `functionIndex[fn].args`), delete button.
- `[+ Add function step]` appends `{ fn: '', input_var: '', output_var: '', args: {} }`.

```typescript
const updateFunction = (index: number, patch: Partial<FunctionStep>) => {
  const fns = [...((cfg.functions as FunctionStep[]) ?? [])];
  fns[index] = { ...fns[index], ...patch };
  updateStepConfig('functions', fns);
};
const addFunction = () => {
  const fns = [...((cfg.functions as FunctionStep[]) ?? [])];
  updateStepConfig('functions', [...fns, { fn: '', input_var: '', output_var: '', args: {} }]);
};
const removeFunction = (index: number) => {
  const fns = ((cfg.functions as FunctionStep[]) ?? []).filter((_, j) => j !== index);
  updateStepConfig('functions', fns);
};
```

**E. Test tab** (see wireframe in section 6.2)

- Input vars table auto-populated from unique `input_var` names across all function steps.
- `[▶ Run Test]` calls `runTransformTest(cfg.functions, testVarsAsMap)`, sets `testTrace`.
- Renders `StepTrace.steps` as one card per step with timing.

**F. AI Assistant tab**

Static JSX. Shows coming-soon message. Does not call the backend unless the user explicitly clicks a (disabled/greyed) button.

**G. `nodeRegistry.ts` — update `transform.summary()`**

```typescript
transform: {
  bg: indigoBg, border: indigoBorder,
  summary: (cfg) => {
    const fns = (cfg.functions as Array<{fn: string}> | undefined) ?? [];
    const exprs = Object.keys((cfg.expressions as Record<string, string> | undefined) ?? {});
    if (fns.length) return `${fns.length} fn${fns.length > 1 ? 's' : ''} · ${fns.map(f => f.fn).join(', ').slice(0, 30)}`;
    return exprs.length ? `→ ${exprs.join(', ')}` : '→ vars';
  },
},
```

---

## 9. Migration: Backward Compatibility

### 9.1 Existing configs are unaffected

`TransformStepConfig` gains the `Functions []FunctionStep` field with `omitempty`. Existing configs that lack the field unmarshal with `Functions = nil`. `execTransform` short-circuits on `len(cfg.Functions) == 0` — no-op for old configs.

**No migration SQL needed. No schema changes. No compiler changes.**

### 9.2 Execution order — all three modes coexist

The executor runs all three modes in order: expressions → extractions → functions. A single Transform node may use one, two, or all three modes. The output of `expressions` and `extractions` is available as input to `functions` steps within the same node.

### 9.3 Expressions remain first-class for simple interpolation

Go template expressions are faster and more readable for simple variable interpolation. The function pipeline is a supplement, not a replacement. The UI presents "Expressions", "JSON Extractions", and "Function Pipeline" as distinct labeled sections.

### 9.4 Compiler and spec serialization

The compiler (`go/internal/agentgen/compiler.go`) serializes `TransformStepConfig` as `json.RawMessage` into `StepSpec.Config`. Because `FunctionStep` uses `json:"functions,omitempty"`, configs without functions serialize identically to before. The compiler needs no changes.

### 9.5 Validation at publish time

The compiler's `Validate` / `CompileForPublish` pass should add:

- For each `FunctionStep`, verify `fn` is a key in the registry (`transform.Lookup(fn)`). Catches typos at publish time, not at runtime.
- For each `FunctionStep`, verify `input_var` and `output_var` are non-empty strings.
- Unknown `args` keys are a warning (logged), not an error — forward compatibility.

This validation runs in `CompileForPublish`, not in the live interpreter (which returns a runtime error instead).

---

## 10. Adding a New Function End-to-End

The minimal change required to add a new function, e.g. `json_minify`:

### Step 1: Add the FunctionDef to `registry.go`

In `go/internal/agentgen/transform/functions.go`, add one `Register` block inside `init()`:

```go
Register(FunctionDef{
    Name:        "json_minify",
    Category:    "json",
    Description: "Parses and re-serializes JSON with no whitespace.",
    Args:        []ArgDef{},
    Examples:    []Example{{In: `{ "a": 1,  "b": 2 }`, Out: `{"a":1,"b":2}`}},
}, func(input string, _ map[string]string) (string, error) {
    var v any
    if err := json.Unmarshal([]byte(input), &v); err != nil {
        return "", fmt.Errorf("json_minify: invalid JSON: %w", err)
    }
    b, _ := json.Marshal(v)
    return string(b), nil
})
```

That is the complete Go-side change. The function is immediately:
- Returned by `GET /admin/transform-functions` → browser function picker shows it
- Executable by `POST /admin/transform-test` → test tab works
- Executable by `execTransform()` → production works
- Validated by `CompileForPublish` → publish-time typo detection works

### Step 2: Add tests

In `go/internal/agentgen/transform/functions_test.go`:

```go
func TestJsonMinify(t *testing.T) {
    fn, ok := Lookup("json_minify")
    require.True(t, ok)

    out, err := fn(`{ "a": 1,  "b": 2 }`, nil)
    require.NoError(t, err)
    require.Equal(t, `{"a":1,"b":2}`, out)

    _, err = fn("not json", nil)
    require.Error(t, err)
}
```

### Step 3: Update TEST_INDEX.md

Add the new test case to `go/TEST_INDEX.md` per Go rules.

### Summary of changes

| File | Change |
|---|---|
| `go/internal/agentgen/transform/functions.go` | Add one `Register(...)` block in `init()` |
| `go/internal/agentgen/transform/functions_test.go` | Add happy-path + error-path test |
| `go/TEST_INDEX.md` | Update test count and trigger map |

No TypeScript changes. No schema changes. No compiler changes. No Traefik changes. No migration SQL.

---

## Appendix A: Implementation Order (Recommended)

1. **Go: package scaffold** — create `go/internal/agentgen/transform/` with `registry.go`, `executor.go`, `functions.go` (empty `init()`), `custom.go` (comment only).
2. **Go: Phase 1 functions** — implement all ~20 Phase 1 functions in `functions.go`. String + LLM-era first (highest coverage per line of code), then JSON, then Type/Validation, then Numeric.
3. **Go: HTTP handlers** — wire `handleTransformFunctions`, `handleTransformTest`, `handleTransformAssist` into the admin router.
4. **Go: `execTransform` integration** — call `transform.ExecChain` from the interpreter.
5. **Go: tests** — `functions_test.go` + `executor_test.go`, update `go/TEST_INDEX.md`.
6. **Frontend: catalog fetch** — `transformCatalog.ts` with `fetchTransformCatalog`.
7. **Frontend: test runner** — `transformTester.ts` with `runTransformTest`.
8. **Frontend: Build tab** — function pipeline UI in `builder/page.tsx`.
9. **Frontend: Test tab** — test var inputs + `StepTrace` rendering.
10. **Frontend: AI Assistant tab** — static coming-soon placeholder.
11. **Frontend: `nodeRegistry.ts`** — update `transform.summary()`.
12. **Go: Phase 2 functions** — Date/Time, Encoding, Conditional, remaining Numeric/String/JSON. Separate commit.

Items 1–5 can be committed and exercised by the runtime before the UI lands. Items 6–11 are a single UI-layer commit. Item 12 is a follow-on.

---

## Appendix B: Scope Boundaries

The following are explicitly out of scope for this design:

- **User-defined functions** — no custom function registration at runtime or per-agent. The registry is code-only. `custom.go` is a placeholder for a future phase where operators can ship custom Go functions as a plugin; not in scope now.
- **AI chain suggestion (transform-assist)** — stub endpoint and placeholder UI tab only. The self-describing registry already provides the function catalog the AI will need when this is implemented.
- **Async functions in the pipeline** — functions must be synchronous. No HTTP calls, no LLM calls inside a transform function.
- **Looping inside a function** — iteration belongs in the Loop node. Functions operate on one value at a time.
- **Side effects** — functions must be pure. No writes to external state.
- **Access to other pipeline vars** — each function receives only its `input_var`. Cross-var operations should be composed as multiple steps.
- **Schema validation against a JSON Schema** — `assert_json` validates JSON syntax. Full JSON Schema validation is a separate feature.
- **Token counting via LLM API** — `truncate_tokens` uses a whitespace heuristic, not a tokenizer.
