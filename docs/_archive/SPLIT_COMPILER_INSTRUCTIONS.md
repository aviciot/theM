# Split Instructions: go/internal/agentgen/compiler.go
# Prepared: 2026-08-31
# Target commit: after ca51f2a (agent-runtime split)

---

## Goal

Split `go/internal/agentgen/compiler.go` (1056 lines) into three files in the
same package (`package agentgen`). Zero behaviour changes. No symbols renamed.
No logic reordered. No new abstractions. Move code verbatim, adjust imports only.

---

## Pre-flight (run before touching anything)

```bash
# 1. Confirm HEAD and clean tree
git log --oneline -3
git status   # must be clean

# 2. Baseline test count — record this number
docker run --rm -v /opt/docker/them/go:/workspace -w /workspace golang:1.25-alpine \
  sh -c "apk add --no-cache git ca-certificates 2>/dev/null && \
         go test ./internal/agentgen/... -v 2>&1 | grep -E '^(=== RUN|--- PASS|--- FAIL|FAIL|ok)' | tail -20"
```

Expected: all PASS, zero FAIL.

---

## File map — what goes where

### File 1: `compiler.go` (keep, shrink to ~180 lines)

**Retains:** package declaration, imports, all type declarations, all public API functions, `buildSpec`, small helpers.

Exact symbols to keep in `compiler.go`:
- Package comment (line 1) and `package agentgen`
- `Issue` struct + `Error()` method
- `CompileError` type alias
- `slugRe` var
- `sanitizeSlug` func
- `canvasDefinition`, `agentRoot`, `canvasSkill`, `Binding`, `canvasStep` types
- `hasErrors` func
- `errorf` func
- `Validate` func (public)
- `CompileForPublish` func (public)
- `Compile` func (public, deprecated alias)
- `buildSpec` func

**Imports needed in `compiler.go`:**
```go
import (
    "encoding/json"
    "fmt"
    "regexp"
    "strings"
)
```

---

### File 2: `compiler_validate.go` (new, ~380 lines)

**Contains:** all `validate*` functions and the two `collect*` functions.

Exact symbols to move (in this order — matches current file order):
1. `validateStructural` (line 124)
2. `validateNodes` (line 173)
3. `validateGraph` (line 216)
4. `collectAgentParams` (line 378)
5. `extractAppParamKey` (line 437)
6. `collectLLMNodes` (line 450)
7. `validateExecutability` (line 481)
8. `validateHumanWaitBackend` (line 508)
9. `validateBindings` (line 784)
10. `validateDataFlow` (line 861)

**Imports needed in `compiler_validate.go`:**
```go
import (
    "encoding/json"
    "fmt"
    "sort"
)
```

Note: `validateStructural` uses `sanitizeSlug` and `slugRe` (staying in `compiler.go`) — that is fine, same package.

---

### File 3: `compiler_topo.go` (new, ~170 lines)

**Contains:** graph utilities — topo sort, binding resolution, var derivation, hash.

Exact symbols to move (in this order — matches current file order):
1. `deriveStepVars` (line 691)
2. `resolveBindings` (line 959)
3. `topoSort` (line 986)
4. `computeSpecHash` (line 1053)

**Imports needed in `compiler_topo.go`:**
```go
import (
    "crypto/sha256"
    "encoding/json"
    "fmt"
)
```

---

## Cross-file dependency map (same package — no visibility issues)

All three files are `package agentgen`. Go sees them as one compilation unit.
These are the internal call edges you must NOT break:

| Caller file | Calls | Lives in |
|---|---|---|
| `compiler.go` / `Validate` | `validateStructural`, `validateNodes`, `validateGraph`, `collectAgentParams`, `collectLLMNodes`, `validateExecutability`, `validateHumanWaitBackend`, `validateDataFlow`, `validateBindings`, `buildSpec` | `compiler_validate.go` / `compiler.go` |
| `compiler.go` / `CompileForPublish` | same as above | same |
| `compiler_validate.go` / `validateGraph` | `topoSort`, `resolveBindings` | `compiler_topo.go` |
| `compiler_validate.go` / `validateStructural` | `sanitizeSlug`, `slugRe`, `hasErrors`, `errorf` | `compiler.go` |
| `compiler_validate.go` / `validateNodes` | `LookupNode` (noderegistry.go), `hasErrors`, `errorf` | in package |
| `compiler_validate.go` / `collectAgentParams` | `extractAppParamKey`, `LookupNode` | same file / noderegistry.go |
| `compiler_topo.go` / `topoSort` | `deriveStepVars` | same file |
| `compiler_topo.go` / `resolveBindings` | `deriveStepVars` | same file |
| `nodes.go` (already there) | `canvasStep`, `Issue` | `compiler.go` |
| `noderegistry.go` (already there) | `canvasStep`, `Issue` | `compiler.go` |

None of these cross a package boundary. No import changes needed outside `compiler*.go`.

---

## Step-by-step procedure

### Step 1 — Create `compiler_validate.go`

Create `/opt/docker/them/go/internal/agentgen/compiler_validate.go` with:
- `package agentgen` header
- imports block (see above)
- Move these functions verbatim in order: `validateStructural`, `validateNodes`,
  `validateGraph`, `collectAgentParams`, `extractAppParamKey`, `collectLLMNodes`,
  `validateExecutability`, `validateHumanWaitBackend`, `validateBindings`, `validateDataFlow`

Do NOT change a single character inside the function bodies.
Do NOT move the section-header comments — they stay in the original file as
anchors; the new file needs no section comments, it is self-evidently a validate file.

### Step 2 — Create `compiler_topo.go`

Create `/opt/docker/them/go/internal/agentgen/compiler_topo.go` with:
- `package agentgen` header
- imports block (see above)
- Move these functions verbatim in order: `deriveStepVars`, `resolveBindings`,
  `topoSort`, `computeSpecHash`

Do NOT change a single character inside the function bodies.

### Step 3 — Trim `compiler.go`

Remove from `compiler.go` every function that was moved to the two new files.
What remains:
- Package comment + `package agentgen`
- Imports (trimmed to what `compiler.go` actually uses: `encoding/json`, `fmt`, `regexp`, `strings`)
- `Issue` + `Error()` + `CompileError`
- `slugRe` + `sanitizeSlug`
- `canvasDefinition`, `agentRoot`, `canvasSkill`, `Binding`, `canvasStep`
- `hasErrors`, `errorf`
- `Validate`, `CompileForPublish`, `Compile`
- `buildSpec`

The section-header comments (`// ── Stage 1: structural ──`, etc.) can be removed
since those stages now live in separate files. Keep the `// ── Public API ───` and
`// ── Internal helpers ───` comments above `Validate` and `buildSpec`.

### Step 4 — Verify imports

Each file must import only what it actually uses.
- `compiler.go`: `encoding/json`, `fmt`, `regexp`, `strings`
- `compiler_validate.go`: `encoding/json`, `fmt`, `sort`
- `compiler_topo.go`: `crypto/sha256`, `encoding/json`, `fmt`

Run `go build ./internal/agentgen/` — if any import is wrong the compiler will tell you exactly which one and in which file.

---

## Verification procedure

Run each step; do not proceed to the next if it fails.

```bash
# Step A — build (catches any missing symbol or wrong import)
docker run --rm -v /opt/docker/them/go:/workspace -w /workspace golang:1.25-alpine \
  sh -c "apk add --no-cache git ca-certificates 2>/dev/null && \
         go build ./internal/agentgen/ 2>&1"
# Expected: no output (silent success)

# Step B — agentgen unit tests (must match pre-flight count exactly)
docker run --rm -v /opt/docker/them/go:/workspace -w /workspace golang:1.25-alpine \
  sh -c "apk add --no-cache git ca-certificates 2>/dev/null && \
         go test ./internal/agentgen/... -v 2>&1 | grep -E '^(--- PASS|--- FAIL|FAIL|ok)'"
# Expected: all PASS, zero FAIL

# Step C — full suite (must be zero failures)
docker run --rm -v /opt/docker/them/go:/workspace -w /workspace golang:1.25-alpine \
  sh -c "apk add --no-cache git ca-certificates 2>/dev/null && \
         go test ./... 2>&1 | grep -E '(FAIL|ok )'"
# Expected: all lines start with "ok", no FAIL lines

# Step D — Docker build (confirms the real binary compiles)
docker build -f Dockerfile.agent-runtime -t them-agent-runtime:split-test .
# Expected: exits 0, layer 14 "go build" completes without errors
docker rmi them-agent-runtime:split-test
```

If Step A fails: check imports — most likely a function body references something
that needs an import you didn't include.

If Step B fails: a function body was accidentally modified during the move.
Do a character-level diff against the original:
```bash
git diff go/internal/agentgen/compiler.go
```
The only diffs in `compiler.go` should be removed lines. Functions in the new
files must be byte-for-byte identical to how they appeared in the original.

If Step C fails but Step B passes: the change broke another package that
depends on `agentgen`. Check the failing package; it almost certainly depends
on a type (`canvasStep`, `Issue`, `Binding`) that was accidentally moved to a
new file and shadowed something — this should not happen since all three files
are `package agentgen`, but double-check the package declaration in each new file.

---

## Commit

Stage only the three files:
```bash
git add go/internal/agentgen/compiler.go \
        go/internal/agentgen/compiler_validate.go \
        go/internal/agentgen/compiler_topo.go
```

Commit message:
```
refactor(agentgen): split compiler.go (1056 lines) into 3 focused modules

compiler.go (~180) — types, public API (Validate/CompileForPublish/Compile), buildSpec
compiler_validate.go (~380) — all validate* + collect* functions
compiler_topo.go (~170) — deriveStepVars, resolveBindings, topoSort, computeSpecHash

Zero behaviour changes. All tests pass. Docker build confirmed.
```

Then push:
```bash
git push origin main
```

---

## What NOT to do

- Do NOT rename any function, type, or variable.
- Do NOT reorder statements inside any function body.
- Do NOT merge or split any function.
- Do NOT add, remove, or change any comments inside function bodies.
- Do NOT move `canvasStep`, `Issue`, `Binding`, or `canvasDefinition` — they must
  stay in `compiler.go` because `nodes.go` and `noderegistry.go` reference them
  directly (same package, but any confusion about which file owns the type will
  cause "declared and not used" or "undefined" errors if you accidentally duplicate them).
- Do NOT add any new tests — this is a pure structural refactor.
- Do NOT update `TEST_INDEX.md` — no tests are added or changed.
- Do NOT touch any file outside `go/internal/agentgen/compiler*.go`.

---

## After the split — remaining candidates

Once this split is committed, the next candidates in priority order are:

| File | Lines | Session |
|---|---|---|
| `go/internal/agentgen/nodes.go` | 1040 | Next after compiler split |
| `go/internal/admin/applications.go` | 972 | After nodes split |
| `go/internal/agentregistry/registry.go` | 834 | After applications split |
