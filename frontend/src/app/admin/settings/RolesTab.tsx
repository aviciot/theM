'use client';
import { useEffect, useState } from 'react';
import { themApi, type TenantRole, type RoleGrant, type RoleMapping, type Application } from '@/lib/api';
import { inputStyle } from './settingsConstants';

// ── small helpers ────────────────────────────────────────────────────────────

function Pill({ color, children }: { color: string; children: React.ReactNode }) {
  return (
    <span style={{ fontSize: '11px', fontWeight: 700, padding: '2px 8px', borderRadius: '6px', background: color, color: '#fff', letterSpacing: '0.04em' }}>
      {children}
    </span>
  );
}

function IconBtn({ icon, title, danger, onClick }: { icon: string; title: string; danger?: boolean; onClick: () => void }) {
  return (
    <button title={title} onClick={onClick} style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: danger ? '#f87171' : 'var(--tm-text-muted)', padding: '4px', borderRadius: '6px', display: 'flex', alignItems: 'center' }}>
      <span className="material-symbols-outlined" style={{ fontSize: '18px' }}>{icon}</span>
    </button>
  );
}

// ── RoleDetail — expanded view of one role ───────────────────────────────────

function RoleDetail({ role, apps, onClose, onUpdated }: { role: TenantRole; apps: Application[]; onClose: () => void; onUpdated: (r: TenantRole) => void }) {
  const [grants, setGrants]     = useState<RoleGrant[]>([]);
  const [mappings, setMappings] = useState<RoleMapping[]>([]);
  const [loading, setLoading]   = useState(true);

  const [newApp, setNewApp]       = useState('');
  const [newSource, setNewSource] = useState<'jwt_claim' | 'header'>('jwt_claim');
  const [newField, setNewField]   = useState('');
  const [newValue, setNewValue]   = useState('');
  const [saving, setSaving]       = useState(false);
  const [err, setErr]             = useState('');

  // Edit mode
  const [editing, setEditing]         = useState(false);
  const [editDisplay, setEditDisplay] = useState(role.display_name);
  const [editDesc, setEditDesc]       = useState(role.description);
  const [editSaving, setEditSaving]   = useState(false);
  const [editErr, setEditErr]         = useState('');

  useEffect(() => {
    setLoading(true);
    Promise.all([
      themApi.listGrants(role.id),
      themApi.listMappings(role.id),
    ]).then(([g, m]) => { setGrants(g); setMappings(m); }).finally(() => setLoading(false));
  }, [role.id]);

  async function saveEdit() {
    setEditSaving(true); setEditErr('');
    try {
      const updated = await themApi.updateRole(role.id, { name: role.name, display_name: editDisplay, description: editDesc });
      onUpdated(updated);
      setEditing(false);
    } catch { setEditErr('Failed to save'); }
    finally { setEditSaving(false); }
  }

  const grantedAppIds = new Set(grants.map((g) => g.application_id));
  const availableApps = apps.filter((a) => !grantedAppIds.has(a.id));

  async function addGrant() {
    if (!newApp) return;
    setSaving(true); setErr('');
    try {
      const g = await themApi.addGrant(role.id, newApp);
      setGrants((prev) => [...prev, g]);
      setNewApp('');
    } catch { setErr('Failed to add grant'); }
    finally { setSaving(false); }
  }

  async function removeGrant(grantId: string) {
    await themApi.deleteGrant(role.id, grantId);
    setGrants((prev) => prev.filter((g) => g.id !== grantId));
  }

  async function addMapping() {
    if (!newField || !newValue) { setErr('Field and value are required'); return; }
    setSaving(true); setErr('');
    try {
      const m = await themApi.addMapping(role.id, { source: newSource, field: newField, value: newValue });
      setMappings((prev) => [...prev, m]);
      setNewField(''); setNewValue('');
    } catch { setErr('Failed to add mapping'); }
    finally { setSaving(false); }
  }

  async function removeMapping(mappingId: string) {
    await themApi.deleteMapping(role.id, mappingId);
    setMappings((prev) => prev.filter((m) => m.id !== mappingId));
  }

  const appName = (id: string) => apps.find((a) => a.id === id)?.name ?? id.slice(0, 8);

  return (
    <div style={{ marginTop: '16px', padding: '24px', background: 'rgba(0,0,0,0.2)', borderRadius: '14px', border: '1px solid rgba(132,157,188,.12)' }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: '20px' }}>
        {editing ? (
          <div style={{ flex: 1, marginRight: '12px' }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px', marginBottom: '8px' }}>
              <div>
                <label style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: '4px' }}>Display Name</label>
                <input value={editDisplay} onChange={(e) => setEditDisplay(e.target.value)} style={inputStyle} />
              </div>
              <div>
                <label style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: '4px' }}>Description</label>
                <input value={editDesc} onChange={(e) => setEditDesc(e.target.value)} placeholder="Optional" style={inputStyle} />
              </div>
            </div>
            {editErr && <div style={{ fontSize: '12px', color: '#f87171', marginBottom: '8px' }}>{editErr}</div>}
            <div style={{ display: 'flex', gap: '8px' }}>
              <button onClick={saveEdit} disabled={editSaving} style={{ padding: '6px 16px', borderRadius: '7px', border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: editSaving ? 'not-allowed' : 'pointer', fontSize: '12px', fontWeight: 600 }}>
                {editSaving ? 'Saving…' : 'Save'}
              </button>
              <button onClick={() => { setEditing(false); setEditErr(''); }} style={{ padding: '6px 14px', borderRadius: '7px', border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: '12px' }}>
                Cancel
              </button>
            </div>
          </div>
        ) : (
          <div>
            <div style={{ fontSize: '15px', fontWeight: 700, color: 'var(--tm-text)' }}>{role.display_name || role.name}</div>
            {role.description && <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)', marginTop: '2px' }}>{role.description}</div>}
          </div>
        )}
        <div style={{ display: 'flex', gap: '4px', flexShrink: 0 }}>
          {!editing && <IconBtn icon="edit" title="Edit role" onClick={() => { setEditing(true); setEditDisplay(role.display_name); setEditDesc(role.description); }} />}
          <button onClick={onClose} style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--tm-text-muted)', padding: '4px', display: 'flex', alignItems: 'center' }}>
            <span className="material-symbols-outlined" style={{ fontSize: '18px' }}>close</span>
          </button>
        </div>
      </div>

      {loading && <div style={{ color: 'var(--tm-text-muted)', fontSize: '13px' }}>Loading…</div>}
      {!loading && (<>

        {/* App Grants */}
        <div style={{ marginBottom: '24px' }}>
          <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: '10px' }}>App Access</div>
          {grants.length === 0 && <div style={{ fontSize: '13px', color: 'var(--tm-text-muted)', marginBottom: '10px' }}>No apps granted — role can access all apps (open).</div>}
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px', marginBottom: '12px' }}>
            {grants.map((g) => (
              <div key={g.id} style={{ display: 'flex', alignItems: 'center', gap: '6px', background: 'rgba(99,102,241,0.12)', border: '1px solid rgba(99,102,241,0.3)', borderRadius: '8px', padding: '4px 10px 4px 12px' }}>
                <span style={{ fontSize: '13px', color: '#a5b4fc' }}>{appName(g.application_id)}</span>
                <IconBtn icon="close" title="Remove grant" danger onClick={() => removeGrant(g.id)} />
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
            <select value={newApp} onChange={(e) => setNewApp(e.target.value)} style={{ ...inputStyle, flex: 1 }}>
              <option value="">Select application…</option>
              {availableApps.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
            </select>
            <button onClick={addGrant} disabled={!newApp || saving} style={{ padding: '8px 16px', borderRadius: '8px', border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: (!newApp || saving) ? 'not-allowed' : 'pointer', fontSize: '13px', fontWeight: 600, opacity: (!newApp || saving) ? 0.5 : 1, whiteSpace: 'nowrap' }}>
              Add
            </button>
          </div>
        </div>

        {/* Claim Mappings */}
        <div>
          <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: '10px' }}>Role Mapping Rules</div>
          <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)', marginBottom: '10px', lineHeight: 1.5 }}>
            Map JWT claim values or M2M headers to this role. When a matching claim/header arrives, the caller gets this role.
          </div>

          {mappings.length === 0 && <div style={{ fontSize: '13px', color: 'var(--tm-text-muted)', marginBottom: '10px' }}>No mapping rules yet.</div>}
          <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', marginBottom: '12px' }}>
            {mappings.map((m) => (
              <div key={m.id} style={{ display: 'flex', alignItems: 'center', gap: '8px', background: 'rgba(0,0,0,0.2)', borderRadius: '8px', padding: '8px 12px' }}>
                <Pill color={m.source === 'jwt_claim' ? '#6366f1' : '#0891b2'}>{m.source === 'jwt_claim' ? 'JWT' : 'Header'}</Pill>
                <span style={{ fontSize: '13px', color: 'var(--tm-text)', fontFamily: 'monospace' }}>{m.field}</span>
                <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>=</span>
                <span style={{ fontSize: '13px', color: '#34d399', fontFamily: 'monospace' }}>{m.value}</span>
                <div style={{ flex: 1 }} />
                <IconBtn icon="delete" title="Remove mapping" danger onClick={() => removeMapping(m.id)} />
              </div>
            ))}
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr 1fr auto', gap: '8px', alignItems: 'center' }}>
            <select value={newSource} onChange={(e) => setNewSource(e.target.value as 'jwt_claim' | 'header')} style={{ ...inputStyle }}>
              <option value="jwt_claim">JWT Claim</option>
              <option value="header">M2M Header</option>
            </select>
            <input value={newField} onChange={(e) => setNewField(e.target.value)} placeholder={newSource === 'jwt_claim' ? 'claim name (e.g. tier)' : 'header name (e.g. X-User-Tier)'} style={inputStyle} />
            <input value={newValue} onChange={(e) => setNewValue(e.target.value)} placeholder="value to match" style={inputStyle} />
            <button onClick={addMapping} disabled={!newField || !newValue || saving} style={{ padding: '8px 16px', borderRadius: '8px', border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: (!newField || !newValue || saving) ? 'not-allowed' : 'pointer', fontSize: '13px', fontWeight: 600, opacity: (!newField || !newValue || saving) ? 0.5 : 1, whiteSpace: 'nowrap' }}>
              Add
            </button>
          </div>
        </div>

        {err && <div style={{ marginTop: '12px', fontSize: '13px', color: '#f87171' }}>{err}</div>}
      </>)}
    </div>
  );
}

// ── RolesTab — main component ─────────────────────────────────────────────────

export function RolesTab() {
  const [roles, setRoles]       = useState<TenantRole[]>([]);
  const [apps, setApps]         = useState<Application[]>([]);
  const [loading, setLoading]   = useState(true);
  const [expanded, setExpanded] = useState<string | null>(null);

  const [creating, setCreating]     = useState(false);
  const [newName, setNewName]       = useState('');
  const [newDisplay, setNewDisplay] = useState('');
  const [newDesc, setNewDesc]       = useState('');
  const [saving, setSaving]         = useState(false);
  const [err, setErr]               = useState('');

  useEffect(() => {
    Promise.all([themApi.listRoles(), themApi.applications()])
      .then(([r, a]) => { setRoles(r); setApps(a); })
      .finally(() => setLoading(false));
  }, []);

  async function createRole() {
    if (!newName) { setErr('Name is required'); return; }
    setSaving(true); setErr('');
    try {
      const r = await themApi.createRole({ name: newName, display_name: newDisplay, description: newDesc });
      setRoles((prev) => [...prev, r]);
      setNewName(''); setNewDisplay(''); setNewDesc('');
      setCreating(false);
      setExpanded(r.id);
    } catch { setErr('Failed to create role'); }
    finally { setSaving(false); }
  }

  async function deleteRole(id: string) {
    if (!confirm('Delete this role? This will remove all grants and mapping rules.')) return;
    await themApi.deleteRole(id);
    setRoles((prev) => prev.filter((r) => r.id !== id));
    if (expanded === id) setExpanded(null);
  }

  function updateRole(updated: TenantRole) {
    setRoles((prev) => prev.map((r) => r.id === updated.id ? updated : r));
  }

  if (loading) return <div style={{ padding: '40px', textAlign: 'center', color: 'var(--tm-text-muted)', fontSize: '14px' }}>Loading roles…</div>;

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '20px' }}>
        <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: 0, lineHeight: 1.5 }}>
          Define roles for your tenant. Grant each role access to specific applications and configure how the role is resolved from JWT claims or M2M headers.
        </p>
        <button onClick={() => { setCreating(true); setErr(''); }} style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '8px 16px', borderRadius: '9px', border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: 'pointer', fontSize: '13px', fontWeight: 600, whiteSpace: 'nowrap', marginLeft: '16px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>add</span>
          New Role
        </button>
      </div>

      {creating && (
        <div style={{ marginBottom: '20px', padding: '20px', background: 'rgba(99,102,241,0.08)', border: '1px solid rgba(99,102,241,0.25)', borderRadius: '14px' }}>
          <div style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-text)', marginBottom: '14px' }}>New Role</div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px', marginBottom: '12px' }}>
            <div>
              <label style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: '5px' }}>Name * (slug)</label>
              <input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="e.g. premium-customer" style={inputStyle} />
            </div>
            <div>
              <label style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: '5px' }}>Display Name</label>
              <input value={newDisplay} onChange={(e) => setNewDisplay(e.target.value)} placeholder="e.g. Premium Customer" style={inputStyle} />
            </div>
          </div>
          <div style={{ marginBottom: '14px' }}>
            <label style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: '5px' }}>Description</label>
            <input value={newDesc} onChange={(e) => setNewDesc(e.target.value)} placeholder="Optional description" style={inputStyle} />
          </div>
          {err && <div style={{ fontSize: '13px', color: '#f87171', marginBottom: '10px' }}>{err}</div>}
          <div style={{ display: 'flex', gap: '8px' }}>
            <button onClick={createRole} disabled={saving} style={{ padding: '8px 20px', borderRadius: '8px', border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: saving ? 'not-allowed' : 'pointer', fontSize: '13px', fontWeight: 600 }}>
              {saving ? 'Creating…' : 'Create'}
            </button>
            <button onClick={() => { setCreating(false); setErr(''); }} style={{ padding: '8px 16px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: '13px' }}>
              Cancel
            </button>
          </div>
        </div>
      )}

      {roles.length === 0 && !creating && (
        <div style={{ padding: '40px', textAlign: 'center', color: 'var(--tm-text-muted)', fontSize: '14px', border: '1px dashed rgba(132,157,188,.2)', borderRadius: '14px' }}>
          No roles yet. Create one to start gating application access.
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
        {roles.map((role) => (
          <div key={role.id} style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-card-border)', borderRadius: '14px', overflow: 'hidden' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '14px', padding: '16px 20px', cursor: 'pointer' }} onClick={() => setExpanded(expanded === role.id ? null : role.id)}>
              <div style={{ width: '36px', height: '36px', borderRadius: '10px', background: 'rgba(99,102,241,0.15)', border: '1px solid rgba(99,102,241,0.3)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                <span className="material-symbols-outlined" style={{ fontSize: '18px', color: '#a5b4fc' }}>badge</span>
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: '14px', fontWeight: 700, color: 'var(--tm-text)' }}>{role.display_name || role.name}</div>
                <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>{role.name}</div>
                {role.description && <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)', marginTop: '2px', opacity: 0.8 }}>{role.description}</div>}
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <IconBtn icon="delete" title="Delete role" danger onClick={() => deleteRole(role.id)} />
                <span className="material-symbols-outlined" style={{ fontSize: '18px', color: 'var(--tm-text-muted)', transition: 'transform 0.2s', transform: expanded === role.id ? 'rotate(180deg)' : 'none' }}>expand_more</span>
              </div>
            </div>
            {expanded === role.id && (
              <div style={{ padding: '0 20px 20px' }}>
                <RoleDetail role={role} apps={apps} onClose={() => setExpanded(null)} onUpdated={updateRole} />
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
