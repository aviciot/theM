# Apps Dispatcher Tighten Report
# cmd/them/main.go + cmd/them/dispatcher_test.go
# Date: 2026-07-24

---

## Problem Fixed

The previous dispatcher used `else → sseApps` as a catch-all, meaning any unrecognised
path (e.g. `/myapp/grpc`, `/myapp/`, `/myapp/ws-extra`) was silently forwarded to the SSE
sub-handler, which then returned its own chi 404 from inside the SSE router. Unknown paths
should be rejected at the dispatcher level — not leaked into a sub-handler.

---

## What Changed

**`go/cmd/them/main.go`**

Extracted the inline closure into a named function `appsDispatcher(wsApps, sseApps http.Handler) http.Handler`.
The dispatcher now uses an explicit `switch` with three cases:

```go
func appsDispatcher(wsApps, sseApps http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch {
        case strings.HasSuffix(r.URL.Path, "/ws"):
            wsApps.ServeHTTP(w, r)
        case strings.HasSuffix(r.URL.Path, "/sse"):
            sseApps.ServeHTTP(w, r)
        default:
            http.NotFound(w, r)
        }
    })
}
```

Call site in `run()` is now a single line:
```go
srv.MountApps(appsDispatcher(wsHandler.AppsWSRoute(), sseHandler.AppsSSERoute()))
```

Extracting to a named function also makes the function directly testable from `package main`
tests without any mocking of the full `ws.Handler` or `sse.Handler`.

**`go/cmd/them/dispatcher_test.go`** (new file, `package main`)

Five focused tests covering every dispatch branch:

| Test | What it covers |
|---|---|
| `TestAppsDispatcher_WSPath` | `GET /{slug}/ws` → WS handler; SSE not called |
| `TestAppsDispatcher_SSEPath_GET` | `GET /{slug}/sse` → SSE handler; WS not called |
| `TestAppsDispatcher_SSEPath_POST` | `POST /{slug}/sse` → SSE handler; WS not called |
| `TestAppsDispatcher_UnknownPath_Returns404` | 4 unknown paths → 404; neither handler called |
| `TestAppsDispatcher_UnsupportedMethod_WS` | `POST /{slug}/ws` → forwarded to WS (returns 405); SSE not called |

The method-not-allowed test (`TestAppsDispatcher_UnsupportedMethod_WS`) confirms that method
enforcement is correctly delegated to the chi sub-handler, not the dispatcher. The dispatcher's
only job is path-suffix routing.

**`go/TEST_INDEX.md`**

Added S1-24, updated S1 total (212 → 217), updated grand total (255 → 260), updated trigger
map entry for `cmd/them/main.go`.

---

## Tests Run

**Command:** `docker run --rm -v /opt/docker/them/go:/app -w /app golang:1.24-alpine go test ./...`

**Result — all packages:**

| Package | Result |
|---|---|
| `cmd/them` | ok (5 new dispatcher tests) |
| `internal/a2a` | ok |
| `internal/admin` | ok |
| `internal/agentregistry` | ok |
| `internal/auth` | ok |
| `internal/cache` | ok |
| `internal/config` | ok |
| `internal/domain` | ok |
| `internal/epconfig` | ok |
| `internal/event` | ok |
| `internal/gate` | ok |
| `internal/health` | ok |
| `internal/llm` | ok |
| `internal/ratelimit` | ok |
| `internal/reconciler` | ok |
| `internal/runrecorder` | ok |
| `internal/runstream` | ok |
| `internal/server` | ok |
| `internal/session` | ok |
| `internal/sse` | ok |
| `internal/ws` | ok |

**Totals: 217 unit tests — 0 failed, 0 skipped.**

---

## Behavior Confirmation

- Routes unchanged: `GET /apps/{slug}/ws`, `GET /apps/{slug}/sse`, `POST /apps/{slug}/sse`
- Unknown paths now return a clean `404` at the dispatcher level instead of a chi 404
  from inside the SSE router
- No Traefik change, no DB change, no Wave 5 work
