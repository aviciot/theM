# Unified Role Governance — Apps, LLM Gateway, MCP

Status: PROPOSED — design doc, not started
Author: session 2026-09-24
Companion docs: `docs/LLM_GATEWAY_DESIGN.md`, `docs/MCP_CONTROL_PLANE_DESIGN.md`,
`docs/MCP_SECURITY_SCANNING_DESIGN.md`, `docs/ROLE_MANAGEMENT.md`

## Why this doc exists

Three separate conversations converged on the same fact: **the-M currently
has three unrelated permission systems, and is about to grow a fourth if
nothing changes.**

1. **Org Roles** (`them.tenant_roles` / `tenant_role_grants` /
   `tenant_role_mappings`, `db/095_tenant_roles.sql`) — answers "which
   Applications can this caller touch."
2. **LLM Gateway** (`them.gateway_clients` / `gateway_profiles` /
   `gateway_policies`, `db/100_llm_gateway.sql`) — answers "which
   models/budget can this caller use." Built with **no awareness of Org
   Roles at all** — confirmed by grep, zero references either direction.
3. **MCP** (planned, `docs/MCP_CONTROL_PLANE_DESIGN.md`) — was about to
   answer "which tools can this caller use" as a *third*, independent
   profile concept, which would make it a fourth unconnected system.

This doc proposes collapsing 2 and 3 into extensions of 1, so there is
**one caller identity (Role) that answers three questions**, not three
identities that each answer one. It also documents a real, small bug
found along the way — the M2M role-header mapping path is wired end-to-end
except for one missing write — and fixes it, because the unified model in
Part 2 depends on that path actually working.

---

## Part 0 — How a caller's identity is established today (ground truth)

Two independent mechanisms exist, both already implemented, for different
callers:

**A. `external_jwt` (SSO passthrough).** The end-user's own device holds a
JWT signed by the tenant's own IdP. The tenant configures a JWKS URL once
per tenant (`them.tenant_runtime_config`, `go/internal/admin/tenant_runtime_idp.go`).
On every request, `go/internal/auth/external_jwt.go` verifies the
signature against that JWKS and extracts claims. The role gate then maps
`jwt_claim` rules in `tenant_role_mappings` against those claims
(`go/internal/roles/roles.go:43-57`, `ResolveRole`, first-match, exact
string equality).

**B. M2M (opaque token + forwarded identity).** A backend service (a bank's
own server, a cron job) holds one the-M-issued bearer token
(`them.access_tokens`, `is_backend=true`). It calls the-M once per
end-user action, and — because it's a trusted backend, not the end-user's
own signed proof — asserts identity via a header (`X-External-User`,
already read today: `go/internal/ws/handler.go:206-210`,
`go/internal/sse/handler.go:187-210`, both gated on
`tokenInfo.IsBackend`).

Both paths converge on the same role gate
(`go/internal/execution/lifecycle.go:396-411`), which calls
`roleChecker.ResolveRole(ctx, tenantID, req.ExternalClaims, req.RoleHeaders)`
then `CheckGrant` against `tenant_role_grants`. Enforcement is real
(403 on a denied/unresolved role — `AdmitErrForbidden`) but **fail-open**:
if a tenant never adds a grant row for an Application, nothing is blocked.

### The one bug: `RoleHeaders` is read but never written

`ExecutionRequest.RoleHeaders map[string]string`
(`go/internal/execution/request.go:37-39`) is declared and consumed by
`ResolveRole`, but no code anywhere constructs a request with it
populated — confirmed by grep across `go/internal/`. The `header`-source
row type in `tenant_role_mappings` (`source = 'header'`) is fully
supported by schema, UI, and `ResolveRole`'s matching logic — it simply
never receives input. Meanwhile its sibling, `X-External-User` for
*identity*, is fully wired for the exact same caller class
(`IsBackend=true`).

**Fix (small, mechanical, two files):**

In `go/internal/ws/handler.go`, immediately after the existing block at
line 206-210 that reads `X-External-User` when `tokenInfo.IsBackend`:

```go
roleHeaders := map[string]string{}
if tokenInfo != nil && tokenInfo.IsBackend {
    if v := r.Header.Get("X-End-User-Role"); v != "" {
        roleHeaders["X-End-User-Role"] = v
    }
    externalUserID = r.Header.Get("X-External-User")
}
```

...and pass `RoleHeaders: roleHeaders` into the `ExecutionRequest{}`
literal at line 223 alongside `ExternalUserID: externalUserID`. Mirror the
identical change in `go/internal/sse/handler.go` (same pattern, lines
187-210). No schema change, no new config — the mapping rule type already
exists and is already evaluated; this closes the one missing write.
Per `go/CLAUDE.md`, this needs a new test in
`go/internal/ws/` and `go/internal/sse/` asserting a backend-token request
with `X-End-User-Role` set resolves the expected role, plus a TEST_INDEX.md
row.

**Decide the header name before implementing** — `X-End-User-Role` is a
placeholder matching the doc comment already in `request.go:38`; if the
bank's own header convention differs, use theirs. This is the one open
decision blocking the fix; everything else is mechanical.

---

## Part 1 — Should LLM Gateway / MCP profiles be Roles?

**Yes.** Argument, plainly:

- A caller (an M2M backend acting for a bank employee, or an SSO
  passthrough end-user) already gets resolved to exactly one Role today,
  for the sole purpose of Application access. That resolution step —
  JWT claim or M2M header → Role — is caller-identity work, and it is
  already built, tested, and enforced.
- LLM Gateway `gateway_clients` re-implements a parallel, weaker version
  of the same idea: one bearer token → one `profile_id`. It has no claim
  mapping, no header mapping, no per-app scoping — just a flat
  token-to-profile lookup.
- If MCP grows a third parallel concept (a `mcp_profile_id` on some new
  `mcp_clients` table), a tenant admin managing permissions has to
  configure the *same person/service* three times, in three screens, with
  no guarantee they stay consistent. A bank's "claims-adjuster" role
  changing what Application it can reach would have zero effect on what
  LLM models or MCP tools that same real-world caller can reach — those
  would keep drifting independently until someone remembers to update all
  three.
- The fix in Part 0 makes the M2M header path support role resolution.
  Once that lands, *every* caller class the-M already supports (SSO
  passthrough, M2M backend) resolves to a Role before this doc adds
  anything. Reusing Role as the anchor for LLM/MCP permissions costs
  nothing new on the identity-resolution side — it's purely a schema
  question of what a Role controls, not how a Role gets assigned.

**What does NOT change:** Org Settings' Roles UI, the resolution mechanism
(JWT claim / header → Role), and the Application-grant concept. This is
additive — two new sections on the same Role, not a rewrite.

---

## Part 2 — The unified model

```
Caller (M2M backend, or SSO end-user)
        │
        ▼
  Role resolution (existing, Part 0)
        │
        ▼
      Role  ──────────────┬──────────────────┬──────────────────────┐
                           │                  │                      │
                    App Access          LLM Access             MCP Access
                    (existing:          (NEW: role_id on       (NEW: role_id on
                    tenant_role_        gateway_policies /      new mcp_role_
                    grants)             a per-role override     tool_grants)
                                        of the tenant default)
```

### 2a. LLM Access — extend, don't replace, `gateway_policies`

Today `gateway_policies` is one row per tenant (`db/100_llm_gateway.sql`
§5) — a single flat allow-list/budget for the whole tenant, with
`gateway_clients.profile_id` pointing at a `gateway_profiles` row that,
per the schema comment, "ships with zero configured steps" — profiles
exist as a naming shell today, not an enforcement unit.

Change: add `role_id UUID REFERENCES them.tenant_roles(id)` to
`gateway_policies`, and change its primary key from `tenant_id` alone to
`(tenant_id, role_id)` with `role_id` nullable — `NULL` row = tenant-wide
default (today's behavior, unchanged), a row with `role_id` set = an
override for that specific Role. Resolution order: Role-specific row →
tenant-default row → allow-all. This is the same override-chain shape
already used elsewhere in this codebase for provider config precedence
(app → tenant → platform, per `internal/llmresolve`) — reusing a pattern
that's already proven, not inventing a new one.

`gateway_clients.profile_id` becomes optional/legacy once a client is
tied to a Role instead — a client's permissions come from *its caller's
Role*, resolved the same way an Application request resolves a Role
today. `gateway_profiles`/`gateway_profile_steps` (the PII/injection guard
pipeline) stay as-is; they are pipeline components a Role's LLM Access
section can attach, not replaced by this change.

### 2b. MCP Access — new table, same shape as `tenant_role_grants`

Mirrors the existing grant table exactly, extended with a tool scope:

```sql
CREATE TABLE them.tenant_role_mcp_grants (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    role_id       uuid NOT NULL REFERENCES them.tenant_roles(id) ON DELETE CASCADE,
    mcp_server_id uuid NOT NULL REFERENCES them.mcp_servers(id) ON DELETE CASCADE,
    tool_name     text NOT NULL,   -- one row per (role, server, allowed tool)
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (role_id, mcp_server_id, tool_name)
);
```

One row per allowed tool, same granularity as the permissions-matrix UI
already sketched in this project's conversation history (checkbox per
tool, grouped by server). No `tenant_role_grants`-style row for a server
= no tools allowed on that server for that Role (default-deny, unlike the
Application grant table's fail-open behavior — deliberately stricter,
because an unreviewed new tool showing up after a health-check re-probe
should not be silently reachable).

Enforcement point: `go/internal/mcp/executor.go`'s `Execute()`, per
`MCP_CONTROL_PLANE_DESIGN.md` §2b/Gap 1 — add a check against
`tenant_role_mcp_grants` for the caller's resolved Role, alongside the
tenant-scoped server lookup that already happens there
(`GetServerBySlugAndTenant`, `executor.go:69`). The caller's Role has to
be threaded from the admission-layer resolution (Part 0) down to the MCP
executor call — today `executor.go` only receives `ApplicationID`, so this
requires passing the resolved `role_id` alongside it through the
orchestrator → `them-mcp-service` call, the same way `ApplicationID`
already flows.

### 2c. UI — one Role edit screen, three tabs

The Org Settings Roles UI (`RolesTab.tsx`) gains two new sections inside
a Role's detail view, alongside the existing "App Access" grant list:

- **App Access** (existing, unchanged)
- **LLM Access** (new): allowed models / aliases / budget override for
  this Role — same fields `gateway_policies` already has, now scoped
  per-role instead of only tenant-wide
- **MCP Access** (new): the permissions-matrix grid sketched earlier in
  this project (per-server, checkbox-per-tool, grouped, risk column
  fed by `MCP_SECURITY_SCANNING_DESIGN.md`'s scan results)

The standalone `/admin/gateway` page's Clients/Profiles/Policy/Requests
tabs stay — Policy tab becomes "tenant default," Clients tab gains a
Role picker instead of (or alongside) `profile_id`, Requests tab is
unchanged (it's an audit log, orthogonal to who's allowed what).

---

## Part 3 — Rollout waves

Land one, test, commit, before starting the next — same discipline as the
companion docs:

1. **Wave 1 — fix the M2M header bug (Part 0).** Small, isolated,
   immediately testable, and everything else in this doc depends on Role
   resolution actually working for M2M callers.
2. **Wave 2 — LLM Access per Role.** Add `role_id` to `gateway_policies`,
   wire resolution order, add the UI section. No MCP dependency.
3. **Wave 3 — MCP Access per Role.** New `tenant_role_mcp_grants` table,
   thread `role_id` through to `executor.go`, enforce, add the matrix UI.
   Depends on the MCP gateway enforcement work already scoped in
   `MCP_CONTROL_PLANE_DESIGN.md` Gap 1 landing first (the policy-check
   insertion point needs to exist before this wave adds a Role dimension
   to it).
4. **Wave 4 — retire the free-floating `gateway_clients.profile_id`
   path** once every client has been migrated to Role-based resolution,
   so there is exactly one permission model left, not two running in
   parallel indefinitely.

## Open items for a future planning session

- Exact header name for the M2M role-mapping fix (Part 0) — needs
  confirmation against the actual bank integration's existing header
  conventions before implementing, so it isn't invented twice.
- Whether `tenant_role_grants` (Application access) should also move to
  default-deny like the new MCP table, for consistency — currently
  intentionally left as-is in this doc since changing existing enforced
  behavior is a bigger, separate decision than adding new tables.
- Whether platform roles (`super_admin`/`admin`/etc., JWT-embedded, fixed
  enum) ever need a LLM/MCP dimension too, or whether this is scoped to
  tenant-custom Roles only, as drafted here.
