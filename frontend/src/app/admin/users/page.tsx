'use client';
import { useEffect, useState, useCallback } from 'react';
import { themApi, type ManagedUser, type TenantSummary, type UserCreateInput, type UserUpdateInput } from '@/lib/api';
import Sidebar from '@/components/Sidebar';

const ACCENT = '#818cf8';

// ── helpers ───────────────────────────────────────────────────────────────────

function badge(active: boolean) {
  return (
    <span style={{
      fontSize: '11px', fontWeight: 600, padding: '2px 8px', borderRadius: '10px',
      background: active ? 'rgba(52,211,153,.12)' : 'rgba(248,113,113,.1)',
      color: active ? '#34d399' : '#f87171',
      border: `1px solid ${active ? 'rgba(52,211,153,.25)' : 'rgba(248,113,113,.2)'}`,
    }}>{active ? 'active' : 'inactive'}</span>
  );
}

// ── Create user modal ─────────────────────────────────────────────────────────

function CreateUserModal({ tenants, onClose, onCreated }: {
  tenants: TenantSummary[];
  onClose: () => void;
  onCreated: (u: ManagedUser) => void;
}) {
  const [username, setUsername] = useState('');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState('viewer');
  const [tenantId, setTenantId] = useState(tenants[0]?.id ?? '');
  const [tenantRole, setTenantRole] = useState('member');
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr('');
    setSaving(true);
    const input: UserCreateInput = { username, name, password, role };
    if (email) input.email = email;
    if (tenantId) { input.tenant_id = tenantId; input.tenant_role = tenantRole; }
    const result = await themApi.createUser(input);
    setSaving(false);
    if ('detail' in result) { setErr((result as { detail: string }).detail); return; }
    onCreated(result as ManagedUser);
  }

  const overlay: React.CSSProperties = {
    position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)', display: 'flex',
    alignItems: 'center', justifyContent: 'center', zIndex: 100,
  };
  const modal: React.CSSProperties = {
    background: 'var(--tm-bg)', border: '1px solid var(--tm-border)', borderRadius: '14px',
    padding: '28px', width: '420px', maxWidth: '95vw',
  };
  const label: React.CSSProperties = {
    fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', display: 'block', marginBottom: '4px',
  };
  const input: React.CSSProperties = {
    width: '100%', padding: '8px 10px', borderRadius: '8px', border: '1px solid var(--tm-border)',
    background: 'var(--tm-card)', color: 'var(--tm-card-text)', fontSize: '13px', boxSizing: 'border-box',
  };
  const select: React.CSSProperties = { ...input };

  return (
    <div style={overlay} onClick={e => { if (e.target === e.currentTarget) onClose(); }}>
      <div style={modal}>
        <h2 style={{ margin: '0 0 20px', fontSize: '16px', color: 'var(--tm-card-text)' }}>Create User</h2>
        <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
          <div>
            <label style={label}>Username *</label>
            <input style={input} value={username} onChange={e => setUsername(e.target.value)} required />
          </div>
          <div>
            <label style={label}>Display Name *</label>
            <input style={input} value={name} onChange={e => setName(e.target.value)} required />
          </div>
          <div>
            <label style={label}>Email</label>
            <input style={input} type="email" value={email} onChange={e => setEmail(e.target.value)} />
          </div>
          <div>
            <label style={label}>Password *</label>
            <input style={input} type="password" value={password} onChange={e => setPassword(e.target.value)} required />
          </div>
          <div>
            <label style={label}>Platform Role</label>
            <select style={select} value={role} onChange={e => setRole(e.target.value)}>
              {['super_admin', 'developer', 'analyst', 'viewer'].map(r => (
                <option key={r} value={r}>{r}</option>
              ))}
            </select>
          </div>
          <div>
            <label style={label}>Assign to Tenant</label>
            <select style={select} value={tenantId} onChange={e => setTenantId(e.target.value)}>
              <option value="">— none —</option>
              {tenants.map(t => (
                <option key={t.id} value={t.id}>{t.display_name} ({t.slug})</option>
              ))}
            </select>
          </div>
          {tenantId && (
            <div>
              <label style={label}>Tenant Role</label>
              <select style={select} value={tenantRole} onChange={e => setTenantRole(e.target.value)}>
                {['admin', 'member', 'viewer'].map(r => (
                  <option key={r} value={r}>{r}</option>
                ))}
              </select>
            </div>
          )}
          {err && <p style={{ color: '#f87171', margin: 0, fontSize: '13px' }}>{err}</p>}
          <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '8px' }}>
            <button type="button" onClick={onClose}
              style={{ padding: '8px 18px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-card-text)', cursor: 'pointer' }}>
              Cancel
            </button>
            <button type="submit" disabled={saving}
              style={{ padding: '8px 18px', borderRadius: '8px', border: 'none', background: ACCENT, color: '#fff', fontWeight: 600, cursor: 'pointer', opacity: saving ? .6 : 1 }}>
              {saving ? 'Creating…' : 'Create'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ── Edit / detail panel ───────────────────────────────────────────────────────

function UserPanel({ user, onClose, onUpdated, onDeleted }: {
  user: ManagedUser;
  onClose: () => void;
  onUpdated: (u: ManagedUser) => void;
  onDeleted: (id: number) => void;
}) {
  const [tab, setTab] = useState<'info' | 'password'>('info');
  const [name, setName] = useState(user.name);
  const [email, setEmail] = useState(user.email ?? '');
  const [active, setActive] = useState(user.active);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState('');
  const [newPw, setNewPw] = useState('');
  const [pwMsg, setPwMsg] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);

  async function saveInfo(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    setSaving(true);
    const patch: UserUpdateInput = {};
    if (name !== user.name) patch.name = name;
    if (email !== (user.email ?? '')) patch.email = email;
    if (active !== user.active) patch.active = active;
    const result = await themApi.updateUser(user.id, patch);
    setSaving(false);
    if ('detail' in result) { setMsg('Error: ' + (result as { detail: string }).detail); return; }
    onUpdated(result as ManagedUser);
    setMsg('Saved');
  }

  async function resetPw(e: React.FormEvent) {
    e.preventDefault();
    setPwMsg('');
    if (newPw.length < 6) { setPwMsg('Password must be at least 6 characters'); return; }
    const result = await themApi.resetUserPassword(user.id, newPw);
    if ('detail' in result) { setPwMsg('Error: ' + (result as { detail: string }).detail); return; }
    setPwMsg('Password updated'); setNewPw('');
  }

  async function deleteUser() {
    await themApi.deleteUser(user.id);
    onDeleted(user.id);
  }

  const panel: React.CSSProperties = {
    position: 'fixed', right: 0, top: 0, bottom: 0, width: '380px', maxWidth: '100vw',
    background: 'var(--tm-bg)', borderLeft: '1px solid var(--tm-border)',
    padding: '28px', display: 'flex', flexDirection: 'column', zIndex: 50, overflowY: 'auto',
  };
  const tabBtn = (t: 'info' | 'password') => ({
    padding: '6px 16px', borderRadius: '8px', border: 'none', cursor: 'pointer', fontSize: '13px',
    fontWeight: tab === t ? 700 : 400,
    background: tab === t ? `${ACCENT}22` : 'transparent',
    color: tab === t ? ACCENT : 'var(--tm-card-text-muted)',
  } as React.CSSProperties);
  const label: React.CSSProperties = { fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', display: 'block', marginBottom: '4px' };
  const input: React.CSSProperties = {
    width: '100%', padding: '8px 10px', borderRadius: '8px', border: '1px solid var(--tm-border)',
    background: 'var(--tm-card)', color: 'var(--tm-card-text)', fontSize: '13px', boxSizing: 'border-box',
  };

  return (
    <div style={panel}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '20px' }}>
        <div>
          <h2 style={{ margin: '0 0 4px', fontSize: '16px', color: 'var(--tm-card-text)' }}>{user.username}</h2>
          <p style={{ margin: 0, fontSize: '12px', color: 'var(--tm-card-text-muted)' }}>{user.role}</p>
        </div>
        <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tm-card-text-muted)', fontSize: '20px' }}>×</button>
      </div>

      <div style={{ display: 'flex', gap: '6px', marginBottom: '20px' }}>
        <button style={tabBtn('info')} onClick={() => setTab('info')}>Info</button>
        <button style={tabBtn('password')} onClick={() => setTab('password')}>Password</button>
      </div>

      {tab === 'info' && (
        <form onSubmit={saveInfo} style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
          <div>
            <label style={label}>Display Name</label>
            <input style={input} value={name} onChange={e => setName(e.target.value)} required />
          </div>
          <div>
            <label style={label}>Email</label>
            <input style={input} type="email" value={email} onChange={e => setEmail(e.target.value)} />
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            <input type="checkbox" id="active" checked={active} onChange={e => setActive(e.target.checked)} />
            <label htmlFor="active" style={{ fontSize: '13px', color: 'var(--tm-card-text)', cursor: 'pointer' }}>Active</label>
          </div>
          {user.tenant_slug && (
            <div style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', padding: '10px', borderRadius: '8px', background: 'var(--tm-card)' }}>
              Tenant: <strong>{user.tenant_slug}</strong> · Role: <strong>{user.tenant_role}</strong>
            </div>
          )}
          {user.last_login_at && (
            <p style={{ margin: 0, fontSize: '12px', color: 'var(--tm-card-text-muted)' }}>
              Last login: {new Date(user.last_login_at).toLocaleString()}
            </p>
          )}
          {msg && <p style={{ margin: 0, fontSize: '13px', color: msg.startsWith('Error') ? '#f87171' : '#34d399' }}>{msg}</p>}
          <button type="submit" disabled={saving}
            style={{ padding: '8px', borderRadius: '8px', border: 'none', background: ACCENT, color: '#fff', fontWeight: 600, cursor: 'pointer', opacity: saving ? .6 : 1 }}>
            {saving ? 'Saving…' : 'Save Changes'}
          </button>
        </form>
      )}

      {tab === 'password' && (
        <form onSubmit={resetPw} style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
          <div>
            <label style={label}>New Password</label>
            <input style={input} type="password" value={newPw} onChange={e => setNewPw(e.target.value)} required minLength={6} />
          </div>
          {pwMsg && <p style={{ margin: 0, fontSize: '13px', color: pwMsg.startsWith('Error') ? '#f87171' : '#34d399' }}>{pwMsg}</p>}
          <button type="submit"
            style={{ padding: '8px', borderRadius: '8px', border: 'none', background: ACCENT, color: '#fff', fontWeight: 600, cursor: 'pointer' }}>
            Reset Password
          </button>
        </form>
      )}

      <div style={{ marginTop: 'auto', paddingTop: '24px', borderTop: '1px solid var(--tm-border)' }}>
        {!confirmDelete ? (
          <button onClick={() => setConfirmDelete(true)}
            style={{ width: '100%', padding: '8px', borderRadius: '8px', border: '1px solid rgba(248,113,113,.4)', background: 'rgba(248,113,113,.08)', color: '#f87171', cursor: 'pointer', fontWeight: 600 }}>
            Delete User
          </button>
        ) : (
          <div style={{ display: 'flex', gap: '8px' }}>
            <button onClick={() => setConfirmDelete(false)}
              style={{ flex: 1, padding: '8px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-card-text)', cursor: 'pointer' }}>
              Cancel
            </button>
            <button onClick={deleteUser}
              style={{ flex: 1, padding: '8px', borderRadius: '8px', border: 'none', background: '#f87171', color: '#fff', fontWeight: 700, cursor: 'pointer' }}>
              Confirm Delete
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function UsersPage() {
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [tenants, setTenants] = useState<TenantSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<ManagedUser | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [search, setSearch] = useState('');

  const load = useCallback(async () => {
    const [u, t] = await Promise.all([themApi.listUsers(), themApi.listTenantsForUsers()]);
    setUsers(Array.isArray(u) ? u : []);
    setTenants(Array.isArray(t) ? t : []);
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  const filtered = users.filter(u =>
    u.username.toLowerCase().includes(search.toLowerCase()) ||
    u.name.toLowerCase().includes(search.toLowerCase()) ||
    (u.email ?? '').toLowerCase().includes(search.toLowerCase()),
  );

  function handleCreated(u: ManagedUser) {
    setUsers(prev => [...prev, u]);
    setShowCreate(false);
    setSelected(u);
  }

  function handleUpdated(u: ManagedUser) {
    setUsers(prev => prev.map(x => x.id === u.id ? u : x));
    setSelected(u);
  }

  function handleDeleted(id: number) {
    setUsers(prev => prev.filter(x => x.id !== id));
    setSelected(null);
  }

  const page: React.CSSProperties = {
    display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)',
  };
  const main: React.CSSProperties = {
    flex: 1, padding: '32px', overflowY: 'auto',
  };
  const row: React.CSSProperties = {
    display: 'grid', gap: '1px',
    background: 'var(--tm-border)',
    borderRadius: '10px', overflow: 'hidden',
  };
  const cell: React.CSSProperties = {
    background: 'var(--tm-card)', padding: '14px 16px',
    display: 'grid', gridTemplateColumns: '2fr 2fr 1.5fr 1fr 1fr',
    alignItems: 'center', fontSize: '13px', color: 'var(--tm-card-text)', cursor: 'pointer',
  };
  const header: React.CSSProperties = {
    ...cell, cursor: 'default', fontSize: '11px', fontWeight: 700,
    color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', letterSpacing: '.05em',
  };

  return (
    <div style={page}>
      <Sidebar />
      <main style={main}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '24px' }}>
          <h1 style={{ margin: 0, fontSize: '22px', fontWeight: 700, color: 'var(--tm-card-text)' }}>Users</h1>
          <button onClick={() => setShowCreate(true)}
            style={{ padding: '9px 20px', borderRadius: '9px', border: 'none', background: ACCENT, color: '#fff', fontWeight: 600, cursor: 'pointer', fontSize: '14px' }}>
            + Create User
          </button>
        </div>

        <input
          placeholder="Search users…"
          value={search}
          onChange={e => setSearch(e.target.value)}
          style={{
            width: '100%', maxWidth: '360px', padding: '9px 14px', borderRadius: '9px',
            border: '1px solid var(--tm-border)', background: 'var(--tm-card)',
            color: 'var(--tm-card-text)', fontSize: '13px', marginBottom: '20px', boxSizing: 'border-box',
          }}
        />

        {loading ? (
          <p style={{ color: 'var(--tm-card-text-muted)' }}>Loading…</p>
        ) : filtered.length === 0 ? (
          <p style={{ color: 'var(--tm-card-text-muted)' }}>No users found.</p>
        ) : (
          <div style={row}>
            <div style={header}>
              <span>Username</span>
              <span>Name</span>
              <span>Tenant</span>
              <span>Role</span>
              <span>Status</span>
            </div>
            {filtered.map(u => (
              <div key={u.id} style={{ ...cell, background: selected?.id === u.id ? `${ACCENT}10` : 'var(--tm-card)' }}
                onClick={() => setSelected(u)}>
                <span style={{ fontFamily: 'monospace' }}>{u.username}</span>
                <span>{u.name}</span>
                <span style={{ color: 'var(--tm-card-text-muted)' }}>{u.tenant_slug || '—'}</span>
                <span style={{ color: 'var(--tm-card-text-muted)' }}>{u.role}</span>
                {badge(u.active)}
              </div>
            ))}
          </div>
        )}
      </main>

      {selected && (
        <UserPanel
          user={selected}
          onClose={() => setSelected(null)}
          onUpdated={handleUpdated}
          onDeleted={handleDeleted}
        />
      )}

      {showCreate && (
        <CreateUserModal
          tenants={tenants}
          onClose={() => setShowCreate(false)}
          onCreated={handleCreated}
        />
      )}
    </div>
  );
}
