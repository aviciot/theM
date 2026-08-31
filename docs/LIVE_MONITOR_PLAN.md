# Live Monitor Feature — Implementation Plan
# Last updated: 2026-08-31

## Goal

Allow an admin to open any Application, select one or more Entry Points, and watch
the live communication between external callers and the agent in real time — tokens,
tool calls, tool results, done/error — for all active sessions on those EPs.

---

## What Already Exists (do not duplicate)

| Mechanism | What it does | Gap |
|---|---|---|
| `/ws/dashboard` + `sessions:{appID}` channel | Delivers `session_start/end/update` events per app | **Never published from Go** — no publisher exists |
| `/ws/dashboard` + `run:{uuid}` channel | Relays `them:dash:run:{runID}` pub/sub to subscribers | **No snapshot on connect** — late subscribers miss prior events |
| `them:dash:run:{runID}:stream` (Redis Stream) | Full durable run event history (XAdd/XRead) | **No XRevRange support** in the adapter — can't do "last N" replay |
| `them:dash:sessions:state:{appID}` (Redis Hash) | Snapshot of active sessions per app | **Never written from Go** — session store doesn't write this key |
| `SessionsView` UI | Canvas + session list, terminate button | Already exists; we add a tab next to it, not replace it |
| Playground | Full live trace for a run *you started* | Not for observing *existing* runs you didn't start |

---

## What Needs to Be Built

### Part A — Go: Session publisher (prerequisite for everything)

**Problem:** `useDashSessions` in the frontend subscribes to `sessions:{appID}` on the
dashboard WS. The Go dashboard handler relays that pub/sub channel to the client. But
nothing in Go ever publishes `session_start` / `session_end` events to
`them:dash:sessions:{appID}`, and nothing writes the snapshot hash
`them:dash:sessions:state:{appID}`. The SessionsView canvas is therefore always dark.

**Fix:** After `Lifecycle.Admit` succeeds in `ws/handler.go`, publish to the dashboard
sessions channel. After `Lifecycle.Release` runs, publish `session_end`.

#### A1 — Add `RunID` to `session.SessionInfo`

File: `go/internal/session/session.go`

Add one field to the struct (line ~74):
```go
RunID string `json:"run_id,omitempty"`
```

Add it to `sessionInfoToFields` (line ~469) and `sessionFromFields` (line ~491) so it
persists in/reads from the Redis hash `them:sess:{session_id}`.

Note: `RunID` is not known at `Lifecycle.Admit` time — it is generated inside `Admit`
at `lifecycle.go:178`. The `handle.RunID` returned from `Admit` is available in
`ws/handler.go` immediately after line 203. We store it in the session hash and include
it in the published `session_start` event.

**BUT** — the session hash is written by `session.Store.Register` which is called inside
`Lifecycle.Admit`. `RunID` is available at that point (generated just before,
`lifecycle.go:178`). So we pass `RunID` through `AdmitRequest` → `AdmitHandle` →
`SessionInfo` inside `Admit`. No change needed after the fact.

Files touched: `go/internal/session/session.go`, `go/internal/transport/transport.go`
(AdmitRequest/AdmitHandle if RunID needs to flow through), `go/internal/ws/handler.go`.

#### A2 — New `DashboardPublisher` in `go/internal/dashboard/`

New file: `go/internal/dashboard/publisher.go`

Minimal interface + implementation that the WS handler injects:

```go
type SessionPublisher interface {
    PublishSessionStart(ctx context.Context, appID string, info session.SessionInfo) error
    PublishSessionEnd(ctx context.Context, appID string, sessionID string) error
}
```

Implementation uses `AdminCacheClient.Publish` (already has `Publish` method at
`go/internal/cache/admin_adapter.go:29`) to publish JSON to
`them:dash:sessions:{appID}`.

Also writes to `them:dash:sessions:state:{appID}` (Redis Hash via `HSetEx`) for the
snapshot, so late subscribers get current state on connect.

TTL on the state hash: 120s (matches the existing snapshot cache convention).

#### A3 — Wire publisher into `ws/handler.go`

The `Handler` struct in `ws/handler.go` gets a `pub dashboard.SessionPublisher` field.

After successful `Lifecycle.Admit` (line ~215): call `pub.PublishSessionStart`.
In the cleanup defer (line ~250): call `pub.PublishSessionEnd`.

The publisher is injected from `cmd/them/main.go` when constructing the WS handler.

---

### Part B — Go: Run stream snapshot on connect

**Problem:** When the monitor subscribes to `run:{uuid}` on the dashboard WS, it only
receives events published *after* connect. If the run is already 30 seconds in, the
client sees nothing until the next event.

**Fix:** When `sendSnapshots` in `go/internal/dashboard/handler.go` handles a `run:`
channel subscription, replay the last N entries from
`them:dash:run:{runID}:stream` via XRANGE (from `"0-0"` up to `"+"`, capped at last 100
entries).

#### B1 — Add `XRevRange` to the runstreamer adapter

File: `go/internal/cache/runstreamer_adapter.go`

Add method:
```go
func (r *RunStreamerRedisClient) XRevRange(ctx context.Context, key, start, stop string, count int64) ([]runstream.StreamEntry, error)
```

This calls `rueidis XREVRANGE key start stop COUNT count`.

#### B2 — Add `XRevRange` to `runstream.RedisStreamer` interface

File: `go/internal/runstream/streamer.go` (interface at line ~95)

Add:
```go
XRevRange(ctx context.Context, key, start, stop string, count int64) ([]runstream.StreamEntry, error)
```

Any test mocks of `RedisStreamer` must also add the method.

#### B3 — Extend `dashRedis` interface + `rueidisAdapter`

The dashboard handler's `dashRedis` interface (line 47 in `dashboard/handler.go`)
currently has `Subscribe`, `HGetAll`, `Get`. Add:

```go
XRevRange(ctx context.Context, key, start, stop string, count int64) ([]StreamEntry, error)
```

Where `StreamEntry` is either imported from `runstream` or a local minimal struct
`{ ID string; Data string }`.

Add the implementation to `rueidisAdapter`.

#### B4 — Add `sendRunSnapshot` to `dashboard/handler.go`

In `sendSnapshots` (line ~238), add a case for `run:` prefix:

```go
case strings.HasPrefix(ch, "run:"):
    runID := ch[len("run:"):]
    h.sendRunSnapshot(ctx, cw, ch, runID)
```

`sendRunSnapshot` calls `XRevRange("them:dash:run:{runID}:stream", "+", "-", 100)`,
reverses the slice (XRevRange returns newest-first), and sends each entry's `data`
field as:
```json
{"channel": "run:{uuid}", "event": <raw event JSON from stream entry>}
```

This gives the client the last 100 events in chronological order on connect.

---

### Part C — Frontend: `MonitorView.tsx`

New file: `go/frontend/src/app/admin/applications/components/MonitorView.tsx`

#### Layout

Full-screen takeover (same pattern as `SessionsView`, `RuntimeView`).

```
┌─────────────────────────────────────────────────────────────────┐
│  ← Back    [App Name] — Live Monitor                            │
├──────────────────┬──────────────────────────────────────────────┤
│  Active Sessions │  Live Feed                                   │
│                  │                                              │
│  EP: chat-ws     │  [Session A ×]  [Session B ×]               │
│  ● session A     │  ┌────────────┐  ┌────────────┐             │
│    2m ago        │  │ token...   │  │ tool_call  │             │
│    agent: planner│  │ token...   │  │   input:.. │             │
│  ● session B     │  │ tool_call  │  │ token...   │             │
│    45s ago       │  │   input:.. │  │ done ✓     │             │
│                  │  │ tool_result│  └────────────┘             │
│  EP: api-sse     │  │ done ✓     │                             │
│  (no sessions)   │  └────────────┘                             │
└──────────────────┴──────────────────────────────────────────────┘
```

#### Data flow

1. `useDashSessions(token, app.id)` — provides the left panel session list (now works
   because Part A publishes session events). Groups sessions by `ep_slug`.

2. User clicks a session row → pins it to the right panel (max 3 pinned).

3. Each pinned session opens a `run:{runID}` subscription on the dashboard WS.
   `run_id` is available from `SessionInfo.run_id` (added in Part A).

4. On subscribe, the snapshot (Part B) delivers the last 100 events immediately.
   Subsequent live events arrive via pub/sub relay.

5. Events rendered per column:
   - `token` → appended to a text buffer (streamed text)
   - `tool_call` → collapsible row: tool name + JSON input (collapsed by default)
   - `tool_result` → appended to the matching tool_call row
   - `done` → green badge "Done", column goes read-only
   - `error` → red badge "Error: {message}"
   - `iteration_start` → section divider "Iteration N"

6. Unpin (×) closes that WS subscription, removes the column.

#### Hook: `useRunFeed`

New hook inside `MonitorView.tsx`:

```typescript
function useRunFeed(token: string | null, runId: string | null): {
  events: RunEvent[];
  connected: boolean;
}
```

Opens a WS to `/ws/dashboard`, subscribes to `run:{runId}`, handles the snapshot
(`run_replay` event type) and live events. Auto-reconnect on disconnect.

Shares the same WS connection pattern as `useDashSessions` (no shared multiplexer
needed — dashboard WS is lightweight).

#### Tab wiring

File: `go/frontend/src/app/admin/applications/page.tsx`

Add `'monitor'` to the view union type (line ~18):
```typescript
useState<'list' | 'definition' | 'sessions' | 'runtime' | 'mcp-credentials' | 'monitor'>('list')
```

Add `monitorApp: Application | null` state.

Add case in render block (before the list fallthrough):
```tsx
if (view === 'monitor' && monitorApp) {
  return <MonitorView app={monitorApp} token={token} onBack={backToList} />;
}
```

Add `onMonitor` callback to `AppCard` → triggers `setMonitorApp(app); setView('monitor')`.

Add a "Monitor" button to `AppCard.tsx` (alongside Sessions, Runtime, Builder buttons).

---

## File Change Summary

| File | Change |
|---|---|
| `go/internal/session/session.go` | Add `RunID` field to `SessionInfo`, `sessionInfoToFields`, `sessionFromFields` |
| `go/internal/transport/transport.go` | Pass `RunID` through `AdmitRequest` / `AdmitHandle` if needed |
| `go/internal/dashboard/publisher.go` | **New file** — `SessionPublisher` interface + implementation |
| `go/internal/dashboard/handler.go` | Add `run:` snapshot case in `sendSnapshots`; add `XRevRange` to `dashRedis` interface + `rueidisAdapter` |
| `go/internal/cache/runstreamer_adapter.go` | Add `XRevRange` method |
| `go/internal/runstream/streamer.go` | Add `XRevRange` to `RedisStreamer` interface |
| `go/internal/ws/handler.go` | Inject `SessionPublisher`; call `PublishSessionStart` after Admit, `PublishSessionEnd` in cleanup defer |
| `go/cmd/them/main.go` | Wire `SessionPublisher` into WS handler |
| `frontend/.../applications/components/MonitorView.tsx` | **New file** — full monitor component + `useRunFeed` hook |
| `frontend/.../applications/components/AppCard.tsx` | Add "Monitor" button + `onMonitor` callback |
| `frontend/.../applications/page.tsx` | Add `'monitor'` view state + `MonitorView` render case |

Tests required (per CLAUDE.md rules):
- `go/internal/session/session_test.go` — verify `RunID` persists through Register/Get
- `go/internal/dashboard/publisher_test.go` — verify `PublishSessionStart/End` JSON and key writes
- `go/internal/dashboard/handler_test.go` — verify `run:` snapshot sends XRevRange entries
- `go/internal/cache/runstreamer_adapter_test.go` — verify `XRevRange` adapter method

---

## Implementation Order

1. Part A1: `session.SessionInfo` + `RunID` field (Go)
2. Part A2: `dashboard/publisher.go` (Go)
3. Part A3: Wire into `ws/handler.go` + `main.go` (Go)
4. Tests for A1–A3
5. Part B1–B4: XRevRange + run snapshot (Go)
6. Tests for B
7. Part C: `MonitorView.tsx` + tab wiring (Frontend)

Do not start Part C until A and B are passing tests.
