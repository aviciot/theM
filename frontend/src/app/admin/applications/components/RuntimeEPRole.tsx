'use client';
import { useEffect, useState } from 'react';
import { api, themApi } from '@/lib/api';
import { C } from '../constants';
import { sharedField } from './RuntimeShared';

type Role = { id: string; name: string; display_name: string };

// Per-EP tenant role requirement. Saves on its own (PATCH .../required-role), no publish needed.
export function EPRoleField({ appId, epId, role }: { appId: string; epId: string; role: string | null }) {
  const [roles, setRoles] = useState<Role[]>([]);
  const [saved, setSaved] = useState<string>(role ?? '');
  const [value, setValue] = useState<string>(role ?? '');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');

  useEffect(() => {
    themApi.listRoles().then((r: Role[]) => setRoles(r || [])).catch(() => setRoles([]));
  }, []);

  async function save() {
    setBusy(true); setMsg('');
    try {
      await api.patch(`/admin/applications/${appId}/entry-points/${epId}/required-role`, { required_role: value || null });
      setSaved(value); setMsg('Saved');
    } catch { setMsg('Failed to save'); }
    finally { setBusy(false); }
  }

  return (
    <div>
      <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: 7, display: 'flex', alignItems: 'center', gap: 5 }}>
        <span className="material-symbols-outlined" style={{ fontSize: 13 }}>shield</span>
        Required Tenant Role
      </div>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <select value={value} onChange={e => { setValue(e.target.value); setMsg(''); }} style={{ ...sharedField, flex: 1 }}>
          <option value="">No role requirement</option>
          {roles.map(r => <option key={r.id} value={r.name}>{r.display_name || r.name}</option>)}
        </select>
        <button onClick={save} disabled={busy || value === saved}
          style={{ padding: '6px 12px', borderRadius: 6, border: '1px solid rgba(132,158,190,0.3)', background: 'transparent', color: C.text, fontSize: 12, cursor: busy || value === saved ? 'default' : 'pointer', opacity: busy || value === saved ? 0.5 : 1 }}>
          Save
        </button>
        {msg && <span style={{ fontSize: 12, color: msg === 'Saved' ? C.green : C.error, fontWeight: 600 }}>{msg}</span>}
      </div>
      {roles.length === 0 && (
        <div style={{ fontSize: 11, color: '#f59e0b', marginTop: 5 }}>No roles configured. Go to Settings → Roles.</div>
      )}
    </div>
  );
}
