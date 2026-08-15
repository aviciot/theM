# buildAppsHandler Cleanup Report
# cmd/them/main.go — standalone refactor
# Date: 2026-07-24

---

## Summary

Removed the `buildAppsHandler` function from `cmd/them/main.go` (was lines 281–311, 31 lines).
Replaced with a 7-line inline dispatcher that delegates to the already-correct
`ws.Handler.AppsWSRoute()` and `sse.Handler.AppsSSERoute()` methods.

---

## What Changed

**File:** `go/cmd/them/main.go`

**Removed:** `buildAppsHandler(wsH *ws.Handler, sseH *sse.Handler) http.Handler` — a standalone
function that registered three chi routes with inline slug-remapping closures, duplicating the
identical logic already in `AppsWSRoute()` and `AppsSSERoute()`.

**Added:** 7-line inline dispatcher at the `MountApps` call site:

```go
wsApps := wsHandler.AppsWSRoute()
sseApps := sseHandler.AppsSSERoute()
srv.MountApps(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if strings.HasSuffix(r.URL.Path, "/ws") {
        wsApps.ServeHTTP(w, r)
    } else {
        sseApps.ServeHTTP(w, r)
    }
}))
```

**Import removed:** `github.com/go-chi/chi/v5` — no longer used in `main.go` after removing
`buildAppsHandler` (chi is still used in the handler packages via their own imports).

**Import added:** `"strings"` — stdlib, used for `strings.HasSuffix`.

**Net change:** −31 lines / +10 lines = −21 lines in `main.go`.

---

## Behavior Verification

The three routes handled are:
- `GET /apps/{slug}/ws` → dispatches to `wsApps` (chi mux with `GET /{slug}/ws`)
- `GET /apps/{slug}/sse` → dispatches to `sseApps` (chi mux with `GET /{slug}/sse` and `POST /{slug}/sse`)
- `POST /apps/{slug}/sse` → dispatches to `sseApps` (same)

The dispatch logic (`strings.HasSuffix(r.URL.Path, "/ws")`) is correct:
- Paths ending in `/ws` → WS handler. Chi's registered `GET /{slug}/ws` matches; `POST /{slug}/ws`
  returns chi's default 405 — identical to the old behavior.
- All other paths → SSE handler. Chi's `GET /{slug}/sse` and `POST /{slug}/sse` match; any other
  method or pattern returns chi's default 404/405 — identical to the old behavior.

The slug-remapping logic (`rctx.URLParams.Add("app_slug", slug)` + `rctx.URLParams.Add("entry_point_slug", slug)`)
is now owned exclusively by `AppsWSRoute()` and `AppsSSERoute()` — the single canonical implementation
of that contract. No behavior change; no route change; no Traefik change; no DB change.

---

## Tests Run

**Suite:** S1 (full unit suite) — required by CLAUDE.md trigger map for `cmd/them/main.go` changes.
**Specific suites exercised:** S1-12 (ws), S1-13 (sse), plus all 23 other packages.

**Command:**
```
docker run --rm -v /opt/docker/them/go:/app -w /app golang:1.24-alpine go test ./... 2>&1
```

**Result:**

| Package | Result | Duration |
|---|---|---|
| `cmd/them` | [no test files] | — |
| `internal/a2a` | ok | 0.014s |
| `internal/admin` | ok | 0.007s |
| `internal/admin/dal` | [no test files] | — |
| `internal/agentregistry` | ok | 0.127s |
| `internal/auth` | ok | 1.480s |
| `internal/cache` | ok | 0.018s |
| `internal/config` | ok | 0.010s |
| `internal/db` | [no test files] | — |
| `internal/domain` | ok | 0.005s |
| `internal/epconfig` | ok | 0.004s |
| `internal/event` | ok | 0.113s |
| `internal/gate` | ok | 0.187s |
| `internal/health` | ok | 0.018s |
| `internal/llm` | ok | 0.010s |
| `internal/orchestrator` | [no test files] | — |
| `internal/ratelimit` | ok | 0.005s |
| `internal/reconciler` | ok | 0.017s |
| `internal/runrecorder` | ok | 0.005s |
| `internal/runstream` | ok | 6.877s |
| `internal/server` | ok | 0.008s |
| `internal/session` | ok | 0.018s |
| `internal/sse` | ok | 0.830s |
| `internal/telemetry` | [no test files] | — |
| `internal/temporal` | [no test files] | — |
| `internal/transport` | [no test files] | — |
| `internal/ws` | ok | 1.239s |

**Totals: 212 unit tests — 0 failed, 0 skipped.**

Binary build also verified clean:
```
docker run --rm -v /opt/docker/them/go:/app -w /app golang:1.24-alpine go build ./cmd/them/
```
Exit 0, no warnings.

---

## What Was NOT Changed

- No routes changed (same three: `GET /{slug}/ws`, `GET /{slug}/sse`, `POST /{slug}/sse`)
- No Traefik configuration changed
- No DB schema changed
- No test files added, changed, or deleted
- No behavior in `AppsWSRoute()` or `AppsSSERoute()` changed
- No Wave 5 work included
- `TEST_INDEX.md` not updated — no new test was added; this is a pure refactor of `main.go` wiring
  with no new testable behavior (existing S1-12 and S1-13 already exercise the handler methods)

---

## Residual Notes

The `AppsWSRoute()` and `AppsSSERoute()` methods were previously dead code — written but never
called (the old `buildAppsHandler` called `wsH.ServeHTTP` / `sseH.ServeHTTP` directly via its
own closures). After this change, they are the live implementation path. The dead code is gone.

The private `tokenHash` duplication in `internal/auth/jwt.go` (identified in POST_REFACTOR_VERIFICATION.md)
was intentionally left untouched — it is a separate concern and not part of this commit.
