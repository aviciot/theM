# the-M Navigation & UX Redesign — Assessment and Proposal
# Status: PROPOSAL — do not implement without per-phase sign-off
# Author: Avi Cohen · 2026-09-09

---

## 1. Current State Assessment

### 1.1 Sidebar Architecture

The entire navigation is defined in one file: `frontend/src/components/Sidebar.tsx`.

Three static arrays drive what appears:

| Array | Visible to | Section label |
|---|---|---|
| `NAV` (2 items) | Everyone | "Observe" |
| `ADMIN_NAV` (10 items) | `admin` + `super_admin` | "Admin" |
| `SUPER_ADMIN_NAV` (4 items) | `super_admin` only | *(no label — appended after ADMIN_NAV)* |

The sidebar is `position: fixed; width: 260px`. Every page individually renders `<Sidebar />` and offsets its `<main>` with `marginLeft: '260px'`. There is no shared authenticated layout.

### 1.2 Current Page Inventory

| Label | Route | File | Notes |
|---|---|---|---|
| Command Center | `/dashboard` | `app/dashboard/page.tsx` | Live run overview |
| Run History | `/runs` | `app/runs/page.tsx` | Historical runs |
| Agents | `/admin/agents` | `app/admin/agents/page.tsx` | CRUD, folders, scan, test, discover, Agent Builder button |
| MCP Store | `/admin/mcp-servers` | `app/admin/mcp-servers/page.tsx` | MCP server registry — **missing AuthGuard** |
| Applications | `/admin/applications` | `app/admin/applications/page.tsx` | App CRUD + Canvas Builder + Runtime + MCP Credentials + Monitor views |
| Access Tokens | `/admin/tokens` | `app/admin/tokens/page.tsx` | API token management |
| Playground | `/admin/playground` | `app/admin/playground/page.tsx` | Interactive agent test harness |
| Services | `/admin/services` | `app/admin/services/page.tsx` | Security scan stats dashboard (ServicesStats, SecurityScanStats types) |
| Audit Logs | `/admin/audit-logs` | `app/admin/audit-logs/page.tsx` | Platform audit trail |
| My Tenant | `/tenant/settings` | `app/tenant/settings/page.tsx` | Tenant profile, quota, IDP/OIDC config, group mappings |
| Members | `/tenant/members` | `app/tenant/members/page.tsx` | Tenant membership management |
| Settings | `/admin/settings` | `app/admin/settings/page.tsx` | System agent roles + monitoring config |
| Tenants | `/admin/tenants` | `app/admin/tenants/page.tsx` | Platform-wide tenant management *(super_admin)* |
| Users | `/admin/users` | `app/admin/users/page.tsx` | Global user management *(super_admin)* |
| Managed Apps | `/admin/managed-apps` | `app/admin/managed-apps/page.tsx` | Platform-owned app config + tenant bindings *(super_admin)* |
| Observability | `/admin/observability` | `app/admin/observability/page.tsx` | Cross-tenant usage + quota KPIs *(super_admin)* |

**Pages that exist but are not in the sidebar:**

| Route | File | Notes |
|---|---|---|
| `/admin/agents/builder` | `app/admin/agents/builder/page.tsx` | Agent Builder — reached from Agents page header |
| `/admin/orchestrators` | `app/admin/orchestrators/page.tsx` | Directory exists; not surfaced in sidebar |
| `/agents` | `app/agents/page.tsx` | Alternate agents path; not in sidebar |
| `/apps/[slug]/voice` | `app/apps/[slug]/voice/page.tsx` | Voice channel; not in sidebar |

---

### 1.3 Role Model

**System roles** (on `TheMUser.role`): `super_admin`, `admin`, `developer`, `analyst`, `viewer`

**Tenant membership roles** (separate concept, referenced in tenant pages): `admin`, `member`, `viewer`

**Client-side gate:** `Sidebar.tsx:100` — `admin || super_admin` for ADMIN_NAV; `super_admin` for SUPER_ADMIN_NAV.

**Server-side enforcement:** `AuthGuard` only verifies session validity (calls `/api/auth/me`). Role enforcement relies entirely on the backend returning 403. There is no Next.js middleware blocking routes by role.

**`useRequireSuperAdmin` hook** (`hooks/useRequireSuperAdmin.ts`): Used only on the Observability page — client-side redirect to `/admin/applications` if not `super_admin`. Not present on Tenants, Users, or Managed Apps pages.

---

### 1.4 Problems Identified

#### Navigation structure
1. **One undifferentiated "Admin" section** mixes build-time tools (Agents, Applications, MCP Store), runtime tools (Playground), monitoring tools (Services, Audit Logs), org management (Members, My Tenant), and system config (Settings). 16 items in one scroll area at super_admin level.
2. **No section label on SUPER_ADMIN_NAV** items — they are visually indistinguishable from tenant-admin items despite being platform-wide.
3. **"My Tenant" and "Members"** use `/tenant/settings` and `/tenant/members` routes but live inside the unsectioned Admin block.
4. **"Settings"** covers two unrelated concerns: system agent LLM roles and monitoring configuration — not obvious from the label.

#### Naming confusion
5. **"Services"** is actually a Security Scan statistics dashboard (`ServicesStats`, `SecurityScanStats` types). The label does not reflect what the page does.
6. **"Members"** (tenant membership) vs **"Users"** (global users) — correct distinction, but without section context the difference is unclear.
7. **"My Tenant"** — reasonable, but its content includes OIDC/SSO, group mappings and quotas; "Organization" would better signal scope.
8. **No label distinguishing** the platform-admin section from the tenant-admin section.

#### Authorization inconsistency (report only — do not fix in visual redesign)
9. **`/admin/mcp-servers` lacks `AuthGuard`** — the only admin page without session enforcement. Any unauthenticated request that bypasses Traefik auth can access it in the browser.
10. **`useRequireSuperAdmin`** is used on Observability but not on Tenants, Users, or Managed Apps. All three are hidden from the sidebar but accessible by URL to any `admin` user whose session is valid and whose backend happens to return 200.
11. **Route `/agents`** exists but is not guarded or surfaced.

#### Agent cards (UX)
12. Agent cards render at `minHeight: 280px` — large even when content is sparse. The deploy card is `minHeight: 280px` too, making the grid feel heavy.
13. Folder tiles (collapsed folders) render in the same 3-column grid as full cards and carry full card weight. They should be compact banners.
14. Technical fields (slug, endpoint URL, transport) appear prominently on the card face; they belong in the detail/edit view.

#### Applications page
15. Views (Builder, Runtime, MCP Credentials, Monitor) are implemented as in-page state transitions rather than routes or tabs — deep linking and browser back/forward do not work.
16. The Applications page has its own view switcher but there is no consistent "tab" affordance — each sub-view has its own custom back button.

---

## 2. Complete Route Mapping Table

| Current label | Current route | Actual purpose | Scope | Current access | Proposed group | Proposed label |
|---|---|---|---|---|---|---|
| Command Center | `/dashboard` | Live run overview, system health | Tenant | all roles | Workspace | Overview |
| Run History | `/runs` | Historical run browser | Tenant | all roles | Monitor | Run History |
| Agents | `/admin/agents` | Agent registry, scan, test, discover | Tenant | admin+ | Build & Test | Agents |
| — | `/admin/agents/builder` | Agent definition builder (visual) | Tenant | admin+ | *(from Agents page)* | Agent Builder |
| MCP Store | `/admin/mcp-servers` | MCP server registry | Tenant | admin+ | Build & Test | MCP Servers |
| Applications | `/admin/applications` | Application lifecycle, canvas builder | Tenant | admin+ | Build & Test | Applications |
| Access Tokens | `/admin/tokens` | API token management | Tenant | admin+ | Organization | Access Tokens |
| Playground | `/admin/playground` | Interactive agent test harness | Tenant | admin+ | Build & Test | Playground |
| Services | `/admin/services` | Security scan stats dashboard | Tenant | admin+ | Monitor | Security Scans |
| Audit Logs | `/admin/audit-logs` | Audit trail | Tenant | admin+ | Monitor | Audit Logs |
| My Tenant | `/tenant/settings` | Tenant profile, quota, OIDC/SSO, groups | Tenant | admin+ | Organization | Organization Settings |
| Members | `/tenant/members` | Tenant membership management | Tenant | admin+ | Organization | Members |
| Settings | `/admin/settings` | System agent LLM roles + monitoring config | Platform | admin+ | Platform Admin | System Settings |
| Tenants | `/admin/tenants` | Platform tenant management | Platform | super_admin | Platform Admin | Tenants |
| Users | `/admin/users` | Global user management | Platform | super_admin | Platform Admin | Platform Users |
| Managed Apps | `/admin/managed-apps` | Platform-owned app config + bindings | Platform | super_admin | Platform Admin | Managed Apps |
| Observability | `/admin/observability` | Cross-tenant usage and quota KPIs | Platform | super_admin | Platform Admin | Usage & Quotas |

**Scope legend:** "Tenant" = operates within the currently authenticated user's tenant. "Platform" = operates across all tenants or is platform-global.

**Note on `/admin/settings`:** Currently accessible to `admin` (not only `super_admin`). Contains system agent LLM provider configuration (likely platform-global) and monitoring configuration. Needs backend confirmation of whether this is tenant-scoped or global before moving — flagged as uncertain below.

---

## 3. Proposed Sidebar Layout

### Tenant Workspace (visible to `admin` + `super_admin`)

```
┌─────────────────────────────┐
│  ⊕ the-M          [tenant]  │  ← active tenant name shown here
│  ─────────────────────────  │
│                             │
│  WORKSPACE                  │
│  ◈ Overview                 │  /dashboard
│  ◈ Run History              │  /runs
│                             │
│  BUILD & TEST               │
│  ◈ Applications             │  /admin/applications
│  ◈ Agents                   │  /admin/agents
│  ◈ MCP Servers              │  /admin/mcp-servers
│  ◈ Playground               │  /admin/playground
│                             │
│  MONITOR                    │
│  ◈ Security Scans           │  /admin/services
│  ◈ Audit Logs               │  /admin/audit-logs
│                             │
│  ORGANIZATION               │
│  ◈ Members                  │  /tenant/members
│  ◈ Organization Settings    │  /tenant/settings
│  ◈ Access Tokens            │  /admin/tokens
│  ─────────────────────────  │
│  ☀ Light mode               │
│  [AV] Avi Cohen  super_admin│ [logout]
└─────────────────────────────┘
```

### Platform Administration section (visible to `super_admin` only)

```
│  PLATFORM ADMIN             │  ← second section group, visually separated
│  ◈ Tenants                  │  /admin/tenants
│  ◈ Platform Users           │  /admin/users
│  ◈ Managed Apps             │  /admin/managed-apps
│  ◈ Usage & Quotas           │  /admin/observability
│  ◈ System Settings          │  /admin/settings *
│                             │
```

`*` `/admin/settings` is currently accessible to all admins. Its placement under Platform Admin should be validated against the actual backend permission for this endpoint before moving. Phase 1 will leave it in place.

### Active tenant indicator

Display the current tenant name (read from the JWT or `/api/auth/me` response) in the sidebar header beneath the logo. This is display-only — no tenant-switching UI — matching current backend capability.

---

## 4. Wireframe Sketch

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  ╔════════╗                                                                  │
│  ║  260px ║   [main content area — marginLeft: 260px]                       │
│  ║        ║                                                                  │
│  ║  logo  ║   ┌────────────────────────────────────────────────────────────┐ │
│  ║  ────  ║   │ Applications                                    [+ New]    │ │
│  ║ tenant ║   ├────────────────────────────────────────────────────────────┤ │
│  ║  name  ║   │ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │ │
│  ║  ────  ║   │ │ App card │ │ App card │ │ App card │ │ + New    │      │ │
│  ║WORKSPACE║  │ │ Overview │ │ Builder  │ │ Runtime  │ │          │      │ │
│  ║ Overv. ║   │ │ [→Build] │ │ [→Build] │ │ [→Mntr]  │ │          │      │ │
│  ║ Run H. ║   │ └──────────┘ └──────────┘ └──────────┘ └──────────┘      │ │
│  ║  ────  ║   └────────────────────────────────────────────────────────────┘ │
│  ║BUILD & ║                                                                  │
│  ║  TEST  ║   Application detail — consistent tab bar:                       │
│  ║ Apps   ║   ┌────────────────────────────────────────────────────────────┐ │
│  ║ Agents ║   │ ← Applications  │ Overview  Builder  Runtime  Monitor  MCP │ │
│  ║ MCP    ║   ├────────────────────────────────────────────────────────────┤ │
│  ║ Play.  ║   │ [tab content]                                              │ │
│  ║  ────  ║   └────────────────────────────────────────────────────────────┘ │
│  ║MONITOR ║                                                                  │
│  ║ SecScan║   Agents page — compact card grid:                               │
│  ║ Audit  ║   ┌────────────────────────────────────────────────────────────┐ │
│  ║  ────  ║   │ ▶ Folder: Vision Agents  (3)              [collapse]       │ │
│  ║ ORG    ║   ├──────────────┬──────────────┬──────────────┬──────────────┤ │
│  ║ Members║   │ AgentCard    │ AgentCard    │ AgentCard    │ AgentCard    │ │
│  ║ OrgSet ║   │ Name         │ Name         │ Name         │ Name         │ │
│  ║ Tokens ║   │ [tag] [tag]  │ [tag] [tag]  │ [tag] [tag]  │ [tag] [tag]  │ │
│  ║  ────  ║   │ [Test][Scan] │ [Test][Scan] │ [Test][Scan] │ [Test][Scan] │ │
│  ║PLATFORM║   └──────────────┴──────────────┴──────────────┴──────────────┘ │
│  ║ Tenant.║                                                                  │
│  ║ PltfUsr║                                                                  │
│  ║ MngApps║                                                                  │
│  ║ Usage  ║                                                                  │
│  ║ SysSett║                                                                  │
│  ║  ────  ║                                                                  │
│  ║ [theme]║                                                                  │
│  ║ [user] ║                                                                  │
│  ╚════════╝                                                                  │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## 5. Uncertainties, Scope Conflicts and Authorization Inconsistencies

### Authorization inconsistencies (report only — do not fix in visual redesign)

| # | Issue | File | Action |
|---|---|---|---|
| A | `/admin/mcp-servers` is missing `AuthGuard` | `app/admin/mcp-servers/page.tsx` | Report to backend team; add `<AuthGuard>` as a standalone fix before or after the redesign — not part of the visual phase |
| B | `useRequireSuperAdmin` is used on Observability but not on Tenants, Users, or Managed Apps — those three pages are URL-accessible to any `admin` whose backend returns 200 | `app/admin/observability/page.tsx` vs the other three | Report; fix separately from the visual redesign |
| C | `/admin/settings` is accessible to `admin` (not just `super_admin`) — if its content is platform-global, this may be an over-permission | `app/admin/settings/page.tsx` | Confirm backend permission before reassigning to Platform Admin section |

### Naming validation needed

| Label | Current | Finding | Verdict |
|---|---|---|---|
| Services → Security Scans | `/admin/services` | API types are `ServicesStats`, `SecurityScanStats`, `AppScanRow`, `RecentJobRow` — the page is entirely about security scan metrics | Safe to rename to **Security Scans** |
| My Tenant → Organization Settings | `/tenant/settings` | Contains: profile, quota view, IDP/OIDC provider, group-to-role mappings, OIDC debug — broad org configuration | Safe to rename; **Organisation Settings** is clearer |
| MCP Store → MCP Servers | `/admin/mcp-servers` | Registry of MCP server definitions, not a marketplace | Safe to rename to **MCP Servers** |
| Settings | `/admin/settings` | Tabs: System Agents (LLM provider per role) + Monitoring config | Better as **System Settings** once scope confirmed |

### Scope uncertainties

| Item | Uncertainty |
|---|---|
| `/admin/settings` | Is system-agent LLM configuration per-tenant or global? If global, belongs in Platform Admin; if per-tenant, stays in tenant workspace. Needs API inspection. |
| `/admin/audit-logs` | Do audit logs show only the current tenant's activity or cross-tenant? Affects whether it belongs under Monitor or Platform Admin. |
| `/admin/tokens` | Are tokens always scoped to the current tenant? Assumed yes — placement under Organization seems correct. |
| Orchestrators page | `/admin/orchestrators` exists but is not surfaced anywhere. Needs review before the redesign to decide whether to surface or retire. |

### Builders — preserved as distinct destinations

The two builders must remain distinct and accessible:
- **Agent Builder** (`/admin/agents/builder`) — reached from the Agents page "Build Visually" button. Keep this access path; do not add a direct sidebar link unless there is a concrete reason.
- **Application Canvas Builder** — reached from the Applications list. The current implementation uses in-page state; the redesign proposal would replace this with a proper tab bar per application (see Phase 3). Do not merge or rename.

---

## 6. Phased Implementation Plan

### Guiding principles

- Each phase is independently reviewable via a focused PR.
- Phase 1 is purely structural — no route changes, no label changes, no permission changes.
- Naming changes happen in Phase 2 after validation.
- Route changes (if any) happen last and only with backward-compatible redirects.
- Each phase lists: what changes, what stays the same, how to verify.

---

### Phase 1 — Sidebar grouping only (safe, no functional change)

**Scope:** Restructure `Sidebar.tsx` into four named sections. No route changes, no label changes, no permission changes.

**What changes:**
- Replace the single `ADMIN_NAV` array with four named section groups: WORKSPACE, BUILD_TEST, MONITOR, ORGANIZATION.
- Replace the unlabeled `SUPER_ADMIN_NAV` addition with a visually separated PLATFORM_ADMIN group with its own section label.
- Retain existing `isActive`, `onMouseEnter`/`Leave` logic, dark/light theme, user footer — unchanged.
- Show active tenant name in the sidebar header (read from `useAuthStore` — the `tenant` field if present, else omit).

**What stays the same:**
- Every route, label, icon, and per-link access rule is preserved exactly.
- AuthGuard presence/absence on each page is unchanged.
- No new permissions are added or removed.
- `/admin/mcp-servers` AuthGuard gap is reported separately, not fixed here.

**Files changed:** `frontend/src/components/Sidebar.tsx` only.

**Verification:**
- Visual: all existing links still appear; groups are labelled correctly.
- Functional: `admin` sees Workspace + Build & Test + Monitor + Organization; `super_admin` also sees Platform Admin; `viewer`/`developer` see only Workspace.
- Regression: navigate to every existing route — all load correctly.
- Accessibility: keyboard tab order through nav items unchanged.

---

### Phase 2 — Label and naming corrections

**Scope:** Rename labels in the sidebar where the name has been validated against actual page behavior.

**What changes:**
- "Services" → "Security Scans"
- "My Tenant" → "Organization Settings"
- "MCP Store" → "MCP Servers"
- Update `<h2>` page headings and `<title>` tags to match on the affected pages.

**What stays the same:**
- All routes unchanged.
- All access rules unchanged.
- "Settings" rename deferred until platform-vs-tenant scope is confirmed.
- "Audit Logs" scope deferred (see uncertainty #3).

**Files changed:** `Sidebar.tsx`; page header text in `admin/services/page.tsx`, `tenant/settings/page.tsx`, `admin/mcp-servers/page.tsx`.

**Verification:**
- Each renamed page: title in browser tab matches new label, h2 matches, breadcrumb (if any) matches.
- No link text regression on any other page that references these routes.

---

### Phase 3 — Agent card compaction

**Scope:** Reduce visual weight of agent cards and folder tiles.

**What changes:**
- Reduce card `minHeight` from 280px to ~180px.
- Move slug, endpoint URL, and transport to the edit modal or card back — show only display name, description summary (1 line, truncated), status badge, tags, and action buttons on the card face.
- Folder tile (collapsed state): change to a compact horizontal banner (full-width, ~52px height) instead of a card-sized tile. Show folder name, agent count, and a collapse/expand chevron.
- Add an optional list-view toggle (icon grid / list icon) stored in `localStorage`. List view shows one row per agent with name, status, last-test result, and action icons.

**What stays the same:**
- All existing actions (Test, Scan, Discover, Edit, Delete, Security scan badge, scan modal) preserved.
- Folder drag-drop logic unchanged.
- Builder button and Deploy New Agent button unchanged.

**Files changed:** `AgentCard.tsx`, `FolderHeader.tsx`, `admin/agents/page.tsx`.

**Verification:**
- All agent actions work identically in grid and list view.
- Folder collapse/expand works as before.
- Drag-and-drop still creates and populates folders correctly.
- Dark and light modes both render correctly.

---

### Phase 4 — Application detail tab bar

**Scope:** Replace the current in-page state-based view switcher in the Applications page with a consistent URL-based tab bar per application.

**What changes:**
- Add sub-routes: `/admin/applications/[id]/overview`, `/admin/applications/[id]/builder`, `/admin/applications/[id]/runtime`, `/admin/applications/[id]/mcp-credentials`, `/admin/applications/[id]/monitor`.
- Add a tab bar component rendered inside the application detail shell.
- Existing `CanvasBuilderView`, `RuntimeView`, `MCPCredentialsView`, `MonitorView` become the content of these tabs — no logic changes.
- Breadcrumb: `Applications > [App name] > [Tab]`.

**What stays the same:**
- `/admin/applications` list route unchanged.
- All view logic and API calls unchanged.
- Old state-based entry points from list card buttons become navigations to the new sub-routes.

**Backward compatibility:**
- `/admin/applications` (list) continues to work exactly as before.
- No other page links directly into the view sub-states currently, so no external deep-link breakage.

**Verification:**
- Browser back/forward navigates between tabs correctly.
- Direct URL `/admin/applications/[id]/builder` loads the builder without going through the list first.
- All five views render their existing content unchanged.
- Empty and error states preserved in each tab.

---

### Phase 5 — Security and authorization hardening (separate from visual redesign)

**Scope:** Fix the authorization inconsistencies identified in section 5. This phase is NOT part of the visual redesign and should be reviewed by the backend team independently.

**Items:**
1. Add `<AuthGuard>` to `/admin/mcp-servers/page.tsx`.
2. Add `useRequireSuperAdmin()` (or equivalent) to `/admin/tenants`, `/admin/users`, `/admin/managed-apps` pages.
3. Confirm scope of `/admin/settings` with the backend team; apply `useRequireSuperAdmin` if it is confirmed platform-global.

**Verification:**
- Unauthenticated browser navigation to `/admin/mcp-servers` redirects to `/login`.
- Authenticated `admin` (non-super_admin) navigation to `/admin/tenants`, `/admin/users`, `/admin/managed-apps` redirects to `/admin/applications`.

---

## 7. Implementation Order Summary

| Phase | What | Risk | Files touched | Reviewable alone? |
|---|---|---|---|---|
| 1 | Sidebar section grouping | Very low — cosmetic only | `Sidebar.tsx` | Yes |
| 2 | Label/naming corrections | Low — text only + headings | `Sidebar.tsx` + 3 page headers | Yes |
| 3 | Agent card compaction | Medium — layout change, test all actions | `AgentCard.tsx`, `FolderHeader.tsx`, `agents/page.tsx` | Yes |
| 4 | Application tab bar / sub-routes | Medium — routing change | `applications/` directory | Yes |
| 5 | Auth hardening | Low-medium — security fix | 4 page files | Yes — separate PR |

---

## 8. What Must Not Change (Compatibility Checklist)

The following must be verified unchanged after every phase:

- [ ] All existing routes (`/dashboard`, `/runs`, `/admin/*`, `/tenant/*`) load correctly
- [ ] Role-based sidebar visibility: admin sees admin items; super_admin sees platform items; others see only Workspace
- [ ] Authentication flow: login → redirect, session expiry → redirect to `/login`, logout → session cleared
- [ ] SSO/OIDC settings and group-to-role mapping UI in `/tenant/settings`
- [ ] Agent folder persistence (localStorage + preferences API)
- [ ] Agent security scan WebSocket updates (scan_started, scan_step, scan_complete, scan_failed)
- [ ] Application Canvas Builder — create, edit, publish, draft revisions
- [ ] MCP credentials management per application
- [ ] Dark/light theme toggle and localStorage persistence
- [ ] Keyboard accessibility of all nav links
- [ ] Empty, loading, and error states on all collection pages
- [ ] No backend API changes, no schema changes, no role definition changes
