# MCP Store — UI Design Plan
# Status: design (not yet implemented)
# Date: 2026-08-24

---

## 0. Summary

The MCP Store UI follows the same structural pattern as the Agents tab: a dedicated top-level admin
page at `/admin/mcp-servers`, a card grid, a slide-in properties panel, and a "Test connection"
action. In addition, Application settings gain an **MCP Credentials** tab (mirrors the existing
LLM provider key UX).

There are three UI deliverables, each independently shippable:

| Phase | Deliverable | Blocks |
|---|---|---|
| **UI-1** | MCP Servers admin page (list, create, edit, delete, test) | Nothing |
| **UI-2** | Application Settings → MCP Credentials tab | UI-1 (needs server list) |
| **UI-3** | Canvas `mcp_server` node + properties panel | UI-1 + canvas node-type registration |

---

## 1. Nav & routing

Add one entry to `ADMIN_NAV` in `frontend/src/components/Sidebar.tsx`:

```ts
{ href: '/admin/mcp-servers', icon: 'electrical_services', label: 'MCP Store' },
```

Place it after `Agents`, before `Applications`.

New page: `frontend/src/app/admin/mcp-servers/page.tsx`

---

## 2. API types — `frontend/src/lib/api.ts`

```ts
export interface MCPServer {
  id: string;
  tenant_id: string;
  name: string;
  slug: string;
  description: string;
  transport: 'http' | 'sse' | 'stdio';
  url: string;
  auth_type: 'none' | 'bearer' | 'header' | 'oauth2';
  health_status: 'unknown' | 'healthy' | 'degraded' | 'unreachable';
  last_checked_at: string | null;
  last_error: string;
  tools_manifest: MCPTool[];
  tools_count: number;
  capabilities: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface MCPTool {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
}

export interface MCPCredentialMeta {
  mcp_server_id: string;
  slug: string;
  name: string;
  credential_set: boolean;
  auth_header_name: string;
}
```

API methods to add to `themApi`:

```ts
listMCPServers: () => api.get<MCPServer[]>('/admin/mcp-servers'),
createMCPServer: (body: unknown) => api.post<MCPServer>('/admin/mcp-servers', body),
getMCPServer: (id: string) => api.get<MCPServer>(`/admin/mcp-servers/${id}`),
updateMCPServer: (id: string, body: unknown) => api.patch<MCPServer>(`/admin/mcp-servers/${id}`, body),
deleteMCPServer: (id: string) => api.delete<void>(`/admin/mcp-servers/${id}`),
probeMCPServer: (id: string) => api.post<{ health_status: string; tools_count: number; last_error?: string }>(`/admin/mcp-servers/${id}/probe`, {}),

listAppMCPCredentials: (appId: string) => api.get<MCPCredentialMeta[]>(`/admin/applications/${appId}/mcp-credentials`),
setAppMCPCredential: (appId: string, serverId: string, body: { credential: string; auth_header_name?: string }) =>
  api.put<void>(`/admin/applications/${appId}/mcp-credentials/${serverId}`, body),
deleteAppMCPCredential: (appId: string, serverId: string) =>
  api.delete<void>(`/admin/applications/${appId}/mcp-credentials/${serverId}`),
```

Note: `probeMCPServer` calls `POST /api/v1/admin/mcp-servers/{id}/probe` — this route is NOT yet
implemented in Go (it needs to proxy to `them-mcp-service`). It must be added in a later wave
before the "Test connection" button goes live. For UI-1, the button can render but show a
"not available yet" message.

---

## 3. MCP Servers page (`/admin/mcp-servers`) — UI-1

### 3.1 Layout

Identical structural pattern to the Agents page:

```
┌─────────────────────────────────────────────────────────────────┐
│  MCP Store                              [+ Add Server]          │
│  ─────────────────────────────────────────────────────────────  │
│  [Search...]    [All] [Healthy] [Degraded] [Unknown]            │
│                                                                 │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │ ● GitHub MCP │  │ ○ Slack MCP  │  │ ? Internal   │          │
│  │ healthy      │  │ unreachable  │  │ unknown      │          │
│  │ 12 tools     │  │ 0 tools      │  │ 0 tools      │          │
│  │ bearer · http│  │ none · http  │  │ none · http  │          │
│  └──────────────┘  └──────────────┘  └──────────────┘          │
│                                                                 │
│                              [Selected server properties panel] │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 Server card

```
┌─ GitHub MCP ──────────────────────────── ● healthy ─┐
│  github-mcp                                          │
│  Manage GitHub repos, issues, PRs via MCP            │
│  12 tools  │  bearer  │  http                        │
│  Last checked: 3 min ago                             │
└──────────────────────────────────────────────────────┘
```

Health badge colours:
- `healthy` → green dot
- `degraded` → amber dot
- `unreachable` → red dot
- `unknown` → grey dot

### 3.3 Properties panel (slide-in, same pattern as Agent properties)

**General tab:**
```
Name           [GitHub MCP                    ]
Slug           [github-mcp                    ]  (slug is immutable after create)
Description    [Manage repos, issues, PRs...  ]
Transport      [http ▼]
URL            [https://github-mcp.example.com]
Auth type      [bearer ▼]
Enabled        [✓]

[Save]  [Delete]
```

**Status tab:**
```
Health status    ● healthy
Last checked     3 minutes ago
Last error       —
Tools count      12

[Test connection ▶]   ← calls POST .../probe; shows inline result

── Tools manifest ───────────────────────────────
  create_issue      Create a GitHub issue
  list_prs          List open pull requests
  close_issue       Close an issue
  ...
```

**Rules:**
- Slug is editable only on CREATE. After save it becomes read-only (shown as a monospace badge).
- `transport = stdio` is shown as an option but disabled with tooltip "stdio not yet supported in this deployment" (MCP_ALLOW_STDIO=false default).
- Auth type `oauth2` shows a "coming soon" badge — not yet implemented.
- "Test connection" button calls the probe endpoint; shows spinner then inline result:
  `✓ healthy — 12 tools` or `✗ unreachable — timeout after 10s`.

---

## 4. Application Settings → MCP Credentials tab — UI-2

Added as a new tab in the existing Application settings panel
(`frontend/src/app/admin/applications/page.tsx`), alongside the existing Runtime, Orchestrators,
and other tabs.

```
┌─ App: My Assistant ─────────────────────────────────────────────┐
│  [Overview] [Entry Points] [Orchestrators] [Runtime] [MCP Creds]│
│  ─────────────────────────────────────────────────────────────  │
│  MCP Credentials                                                │
│  Configure per-application API keys for connected MCP servers.  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  GitHub MCP        bearer    ● key set     [Edit] [Clear] │  │
│  │  Slack MCP         header    ○ no key      [Set]          │  │
│  │  Internal Tools    none      n/a                          │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

Set/Edit → modal with masked input:
```
┌─ Set credential — GitHub MCP ───────────────────────┐
│                                                      │
│  Bearer token  [●●●●●●●●●●●●●●●●●●●●●●●●●●●●●●●●] │
│  (Enter the full bearer token; it will be encrypted) │
│                                                      │
│  Auth header name  [Authorization          ]         │
│  (Only for auth_type=header; default: Authorization) │
│                                                      │
│                          [Cancel]  [Save credential] │
└──────────────────────────────────────────────────────┘
```

Rules:
- Only servers with `auth_type != 'none'` show Set/Edit/Clear.
- Servers with `auth_type = 'none'` show "n/a" — no credential needed.
- After save: badge changes to `● key set`. Value is never shown again.
- "Clear" calls `DELETE .../mcp-credentials/{server_id}`, badge returns to `○ no key`.
- List is fetched from `GET .../mcp-credentials` which returns metadata only (no decrypted value).

---

## 5. Canvas MCP node — UI-3

### 5.1 Node type registration

Add to `go/internal/admin/node_types.go` (served by `GET /admin/node-types`):

```json
{
  "type": "mcp_server",
  "category": "integrations",
  "label": "MCP Server",
  "icon": "electrical_services",
  "color": "#6366f1",
  "inputs": ["control_flow"],
  "outputs": ["control_flow"],
  "description": "Call tools on a connected MCP server",
  "config_schema": {
    "mcp_server_slug":  { "type": "string",  "required": true },
    "selected_tools":   { "type": "array",   "items": { "type": "string" } },
    "expose_all_tools": { "type": "boolean", "default": false }
  }
}
```

### 5.2 Canvas properties panel for mcp_server node

When an `mcp_server` node is selected in the builder:

```
┌─ MCP Server ──────────────────────────────────────────┐
│                                                        │
│  Server   [GitHub MCP ▼]              ● healthy        │
│           Fetched from GET /admin/mcp-servers          │
│                                                        │
│  Tools                                                 │
│  ◉ All tools (LLM sees full manifest)                  │
│  ○ Selected tools only                                 │
│    ☑ create_issue   ☑ list_prs   ☐ close_issue        │
│                                                        │
│  ▼ Test credential  (canvas session only, not saved)   │
│    Bearer token [________________________]             │
│    [Test connection ▶]  → inline result               │
│                                                        │
└────────────────────────────────────────────────────────┘
```

Rules:
- Server dropdown populated from `GET /admin/mcp-servers` (enabled only).
- Health badge reflects `health_status` from the server list response.
- Tool checklist from `tools_manifest` on the selected server (already in the list response).
- "Test credential" is React state only — never written to the canvas definition.
  Value is sent in the test-run body as `test_mcp_credentials: { [slug]: "token" }`.
- Canvas definition stores only `mcp_server_slug` + `selected_tools` + `expose_all_tools`.
  No IDs, URLs, or credentials ever in the JSON.

### 5.3 Canvas definition doc shape

```jsonc
{
  "id": "node-abc",
  "type": "mcp_server",
  "data": {
    "mcp_server_slug": "github-mcp",
    "selected_tools": ["create_issue", "list_prs"],
    "expose_all_tools": false,
    "label": "GitHub MCP"
  }
}
```

---

## 6. Implementation order

Each phase is independently deployable without breaking existing features.

### Phase UI-1 — MCP Servers admin page

Files to create/edit:
1. `frontend/src/lib/api.ts` — add `MCPServer`, `MCPTool`, `MCPCredentialMeta` types + all `themApi` methods
2. `frontend/src/components/Sidebar.tsx` — add MCP Store nav entry
3. `frontend/src/app/admin/mcp-servers/page.tsx` — new page (card grid + properties panel)

Backend dependency: MCP-1 Go CRUD is done ✓. `POST .../probe` endpoint is NOT done yet — button shows "Probe not available" until MCP-2 wires it.

### Phase UI-2 — App MCP Credentials tab

Files to edit:
1. `frontend/src/app/admin/applications/page.tsx` — add MCP Credentials tab to ApplicationView

Backend dependency: UI-1 done (needs server list to populate dropdown).

### Phase UI-3 — Canvas MCP node

Files to create/edit:
1. `go/internal/admin/node_types.go` — add `mcp_server` node type
2. `frontend/src/app/admin/agents/builder/components/RightPanel.tsx` — add mcp_server case to properties panel renderer
3. `frontend/src/app/admin/agents/builder/page.tsx` — any canvas-level changes needed for the new node

Backend dependency: UI-1 done (needs server list for dropdown). Probe endpoint (MCP-2) needed for "Test connection" button.

---

## 7. What is NOT in scope for these UI phases

- OAuth2 credential flow UI — `auth_type='oauth2'` shows "coming soon" badge only
- Per-tool ACL UI — all selected tools available once wired
- Streaming MCP responses — not in this design
- MCP server marketplace / catalog — bring-your-own-URL only for now
- Publish-time MCP validation warnings in the UI — the builder will show them as text; no special rendering needed initially

---

## 8. Probe endpoint — backend gap

`POST /api/v1/admin/mcp-servers/{id}/probe` must be added to `them-go-bridge` as a thin proxy
to `POST http://them-go-bridge:8010/internal/probe/{id}` on `them-mcp-service`. This requires:

1. `MCP_SERVICE_URL=http://them-mcp-service:8010` env var on `them-go-bridge`
2. A new route in `go/internal/admin/mcp_servers.go`: `r.Post("/mcp-servers/{id}/probe", h.Probe)`
3. `h.Probe` makes an HTTP call to `{MCP_SERVICE_URL}/internal/probe/{id}` and returns the result

This is a Phase MCP-2 item. Until it exists, the "Test connection" button in UI-1/UI-3 should
show a 503 message gracefully rather than crashing.
