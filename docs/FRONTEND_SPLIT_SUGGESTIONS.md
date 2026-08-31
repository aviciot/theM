# Frontend File Split — Improvement Suggestions

Generated during the file-split refactor session (2026-08-31).

---

## Completed splits

| Original file | Lines | Now split into |
|---|---|---|
| `admin/orchestrators/page.tsx` | 863 | 4 files, largest 321 lines |
| `admin/mcp-servers/page.tsx` | 1,022 | 7 files, largest 238 lines |
| `admin/settings/page.tsx` | 905 | 5 files, largest 217 lines |
| `admin/agents/agentTypes.ts` | extracted | types + diff + folder persistence |
| `admin/agents/agentUtils.ts` | extracted | pure display helpers |

---

## Remaining large files (not yet split)

| File | Lines | Notes |
|---|---|---|
| `admin/agents/page.tsx` | ~2,529 | `AgentCard`, `FolderHeader`, modals still inline — see plan below |
| `admin/playground/page.tsx` | 2,309 | `ChatColumn` is a WS state machine, tricky to split |
| `lib/api.ts` | 1,152 | `themApi` object + all types inline |
| `admin/applications/components/CanvasBuilderView.tsx` | 1,152 | `renderPropertiesPanel` is 550 lines inline |
| `admin/applications/components/PropertiesPanel.tsx` | 936 | 6 logical sub-panels |

---

## Remaining split plan for agents/page.tsx

The agents page still has `AgentCard` (~460 lines), `FolderHeader` (~260 lines), and all modal JSX inline.

### Proposed extraction

```
agents/
  AgentCard.tsx        — AgentCard component + nestedSurface/inputStyle + _inFlightScans set
  FolderHeader.tsx     — FolderHeader (collapsed card + expanded header + rename)
  AgentModals.tsx      — Modal + Field + SectionLabel + ChangedBadge + DiffRow +
                          all modal JSX: Create/Edit form, Discover popup, Scan report,
                          Delete confirm, folder-name prompt
  agentTypes.ts        ✓ done
  agentUtils.ts        ✓ done
  page.tsx             — page state, folder CRUD, scan/test/discover handlers,
                          filtering, layout (target ~430 lines)
```

**Key props contracts:**
- `AgentCard` already accepts all callbacks — no lifting needed
- `AgentModals` needs ~15 props from page state (form, editing, scanning, discover popup, etc.)
- `FolderHeader` is fully self-contained via props

---

## lib/api.ts split suggestions

Current structure: HTTP plumbing + all types + `themApi` object (3 domains mixed).

**Option A — minimal churn (recommended):**
```
lib/apiClient.ts    — request(), tryRefresh(), api base object (~75 lines)
lib/apiTypes.ts     — all exported interfaces/types (~470 lines)
lib/api.ts          — themApi + re-exports (stays ~610 lines but callers unchanged)
```

**Option B — full domain split of themApi:**
```
lib/themApi.agents.ts          — agent CRUD + scan + discover
lib/themApi.orchestrators.ts   — orchestrators + test-llm/voice/tts
lib/themApi.applications.ts    — applications, entry-points, runtime, provider-keys
lib/themApi.canvas.ts          — component registry, definitions, canvas agent builder
lib/themApi.mcp.ts             — MCP server + credential methods
lib/themApi.misc.ts            — runs, tokens, voice streams, a2a, health
lib/api.ts                     — assembles themApi, re-exports types (~80 lines)
```

Option B requires updating ~15 import sites but achieves strict <400 line files.

---

## CanvasBuilderView.tsx split suggestions

The `renderPropertiesPanel()` inline function is ~550 lines and covers 4 node types.

```
applications/components/
  CanvasBuilderView.tsx          — state, effects, canvas handlers, layout (~370 lines)
  CanvasNodePropertiesPanel.tsx  — renderPropertiesPanel as a real component (~560 lines)
  CanvasComponentPalette.tsx     — left palette: entry-point tiles + draggable rows (~80 lines)
```

`CanvasNodePropertiesPanel` receives: `selectedNode`, `nodes`, `edges`, `setNodes`,
`setIsDirty`, `configPanelText`, `setConfigPanelText`, `openSections`, `setOpenSections`,
`llmTestState`, `setLlmTestState`, `providerKeyStatuses`, `availableMCPServers`,
`mcpExpanded`, `setMcpExpanded`, `app`, `setEpConfig`, `testOrchLlm`.

---

## PropertiesPanel.tsx split suggestions

6 logical sub-panels already visually separated by section headers.

```
applications/components/
  PropertiesPanel.tsx           — shell: state, handlers, tab routing (~190 lines)
  PropsPanelApp.tsx             — application info (no node selected) (~115 lines)
  PropsPanelEntryPoint.tsx      — entry point properties tab (~145 lines)
  PropsPanelOrchestrator.tsx    — orchestrator props + config tabs (~360 lines)
  PropsPanelAgent.tsx           — agent node properties (~130 lines)
  PropsPanelMiddleware.tsx      — middleware node properties (~100 lines)
```

---

## High-value improvement suggestions

### 1. Shared design-token file (high value)
**Problem:** `inputStyle`, `nestedSurface`, accent colors, and border constants are copy-pasted across 5+ admin pages.

**Suggestion:** Extract `frontend/src/app/admin/shared/designTokens.ts` with:
- `inputStyle`, `labelStyle`, `nestedSurface`, `sectionLabel`
- `CYAN`, `PURPLE`, `GREEN`, `TEXT`, `MUTED`, `BORDER` (or CSS vars)
- `providerColor()`, `providerIcon()` (used by both orchestrators and settings)

This would eliminate ~200 lines of duplication across the admin section.

### 2. Shared `Modal` wrapper component (high value)
**Problem:** Each page implements its own modal backdrop/card. At least 4 pages duplicate the same backdrop + rounded-card pattern.

**Suggestion:** `frontend/src/components/AdminModal.tsx` — accept `title`, `onClose`, `wide?`, `children`. Already done well in `agents/page.tsx` — extract and share.

### 3. `AgentCard` → lazy-load scan report (medium value)
**Problem:** Every `AgentCard` re-renders when `scanResults` changes because the parent passes `scanResult` as a prop. With many agents, a single scan triggers a full grid re-render.

**Suggestion:** Move `scanResult` selection inside `AgentCard` using a memoized selector or `useSyncExternalStore`, or wrap `AgentCard` in `React.memo` with a custom comparator.

### 4. Playground `ChatColumn` WS reconnection (medium value)
**Problem:** `ChatColumn` manages its own WS with a `useEffect` reconnect loop. In compare mode (2 columns), both columns manage independent WS connections with duplicated state machines.

**Suggestion:** Extract `useWebSocket(url)` hook that encapsulates connect/disconnect/message/reconnect logic. `ChatColumn` then becomes a pure render component that consumes the hook. This also makes the WS logic unit-testable.

### 5. `lib/api.ts` — no retry/timeout logic (low-medium value)
**Problem:** `request()` has no timeout or retry. A slow Go bridge causes the entire UI to hang indefinitely on any fetch.

**Suggestion:** Add `AbortController` with a configurable timeout (e.g. 30s) to `request()`. Optionally add a single retry on 503/network errors.

### 6. CSS-in-JS inline styles → CSS modules or Tailwind (low value, high effort)
**Problem:** All components use inline `style={{}}` objects. This prevents browser stylesheet caching and makes dark-mode/theme overrides difficult.

**Suggestion:** Long-term, migrate to CSS Modules (`.module.css`) for component-scoped styles. Short-term, at minimum move the large `<style>` blocks (scan animations, card CSS) into `.module.css` files to enable browser caching and remove runtime string injection.
