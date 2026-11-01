---
title: Entry Point Role Requirement - Implementation Plan
type: plan
status: current
last_verified: 2026-11-01
---

# Entry Point Role Requirement - Implementation Plan

## Goal
Enable per-entry-point tenant role requirements following the LLM/Memory configuration pattern:
Canvas enables → Runtime configures → Debug shows

## Current State (2026-11-01)

### Done
- Backend: `entry_points.required_role`, admin PATCH endpoint, enforcement in `execution/lifecycle.go` (JWT `roles`), deploy/export/import.
- Canvas: "Require tenant role" checkbox (user_jwt only), saved as `roleRequirementEnabled` in EP canvas config. Dropdown removed.
- Runtime: "Required Tenant Role" dropdown per EP (`RuntimeEPRole.tsx`), saves via the PATCH endpoint, no publish needed.

### Decisions
- **Scope changed (2026-11-01):** the JWT `roles` array of a the-M login holds only the membership role, so tenant roles can never match on `user_jwt`. The role requirement now applies to `external_jwt` EPs: mapped role (`tenant_role_mappings`) name must equal `required_role` (`execution/required_role.go`). Canvas checkbox + Runtime dropdown show only for `external_jwt`.
- Runtime shows the dropdown for any `user_jwt` EP; it does not read the canvas checkbox (Runtime has no canvas config). The checkbox is currently informational.
- Debug mode: skipped. Debug runs (`AppFlowDebugService.Start`) never go through the `lifecycle.go` role check, so a per-run override would do nothing.

### Verified live (2026-11-01)
- Test-runner scenarios 51 (alice, tier=premium → admitted, got a reply) and 52 (bob, tier=standard → HTTP 403) both pass on payops_ai `smart-refund-handler-97f244` (EP set to external_jwt, required_role=jwt-premium via `test-runner/scripts/setup_ep_role_scenario.py`).
- Found+fixed on the way: `access_policy` missing from EP API JSON; CHECK constraint on `required_role` (migration 126).

### Still open
- Make the checkbox meaningful (clear `required_role` when unchecked on publish).
- Later idea: Debug "test as role" (needs `Start` to run the role check).

## Implementation Steps

### Step 1: Canvas checkbox (enable/disable)
Location: `frontend/src/app/admin/applications/components/panel/EntryPointPanel.tsx`

Add after the "Who can connect?" dropdown:
```tsx
{d.accessMode === 'user_jwt' && (
  <div style={fieldWrap}>
    <label style={{ ...labelStyle, display: 'flex', alignItems: 'center', gap: 6 }}>
      <input 
        type="checkbox" 
        checked={d.roleRequirementEnabled ?? false}
        onChange={e => onUpdateNode(selectedNode.id, { roleRequirementEnabled: e.target.checked })}
      />
      Require tenant role
    </label>
    <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>
      When enabled, configure the required role in Runtime tab
    </div>
  </div>
)}
```

Remove current Required Role dropdown from canvas.

### Step 2: Runtime configuration
Location: `frontend/src/app/admin/applications/components/RuntimeEPSections.tsx`

Add state:
```tsx
type EPRoleDraft = { enabled: boolean; role: string | null };
const [epRoleDrafts, setEPRoleDrafts] = useState<Record<string, EPRoleDraft>>({});
const [tenantRoles, setTenantRoles] = useState<Array<{id: string; name: string; display_name: string}>>([]);
```

Fetch tenant roles on mount:
```tsx
useEffect(() => {
  themApi.get('/admin/roles').then((r: any) => setTenantRoles(r || [])).catch(() => {});
}, []);
```

Add UI in EP card (after Memory section):
```tsx
{/* Required Role */}
{ep.roleRequirementEnabled && (
  <div style={{ borderTop: '1px solid rgba(132,158,190,0.1)', paddingTop: 12 }}>
    <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, marginBottom: 7 }}>
      <span className="material-symbols-outlined" style={{ fontSize: 13 }}>shield</span>
      Required Tenant Role
    </div>
    <select 
      value={epRoleDrafts[ep.id]?.role ?? ep.required_role ?? ''}
      onChange={e => setEPRoleDrafts(prev => ({ 
        ...prev, 
        [ep.id]: { enabled: true, role: e.target.value || null } 
      }))}
      style={f}
    >
      <option value="">No role requirement</option>
      {tenantRoles.map(r => (
        <option key={r.id} value={r.name}>{r.display_name || r.name}</option>
      ))}
    </select>
    {/* Save button */}
    <button onClick={() => onSaveEPRole(ep.id)} disabled={busy}>
      Save Role
    </button>
  </div>
)}
```

Add save handler in parent RuntimeView.tsx:
```tsx
async function handleSaveEPRole(epId: string) {
  const draft = epRoleDrafts[epId];
  if (!draft) return;
  
  setEPRoleSaving(epId);
  try {
    await themApi.patch(`/admin/applications/${app.id}/entry-points/${epId}/required-role`, {
      required_role: draft.role
    });
    setEPRoleMsg(prev => ({ ...prev, [epId]: 'Saved' }));
    // Refresh entry points
    const freshApp = await themApi.getApplication(app.id);
    setEntryPoints(freshApp.entry_points ?? []);
  } catch (e) {
    setEPRoleMsg(prev => ({ ...prev, [epId]: 'Failed to save' }));
  } finally {
    setEPRoleSaving(null);
  }
}
```

### Step 3: Debug Mode visibility
Location: Debug panel components (TBD - need to find where debug params are shown)

Show:
- "Required Role: jwt-premium" (if set)
- "Your roles: jwt-premium, m2m-partner" (from debug user JWT)
- Check mark if user has required role

## Data Flow

1. **Canvas**: User enables "Require tenant role" checkbox → saved to EP config
2. **Runtime**: User selects "JWT Premium" from dropdown → saves to `entry_points.required_role`
3. **Debug**: Shows "Required Role: JWT Premium" and user's current roles
4. **Enforcement**: Backend checks JWT `roles` array contains the `required_role` value

## Backend (already complete)
- `entry_points.required_role` TEXT column
- `PATCH /admin/applications/{app_id}/entry-points/{ep_id}/required-role` endpoint
- Enforcement in `execution/lifecycle.go` checks `claims.Roles` array
- Deploy/export/import includes `required_role`
