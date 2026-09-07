'use client';
import { useCallback, useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi } from '@/lib/api';
import type { TenantMember } from '@/lib/api';
import { useAuthStore } from '@/stores/authStore';

const ACCENT = '#818cf8';

// ── Role badge ────────────────────────────────────────────────────────────────

function RoleBadge({ role }: { role: string }) {
  const color =
    role === 'admin' ? '#818cf8' :
    role === 'member' ? '#34d399' :
    '#94a3b8';
  return (
    <span style={{
      display: 'inline-block', padding: '2px 8px', borderRadius: 12,
      fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em',
      background: color + '22', color,
    }}>
      {role}
    </span>
  );
}

// ── Side panel ────────────────────────────────────────────────────────────────

function MemberPanel({
  member,
  canEdit,
  onClose,
  onSaved,
}: {
  member: TenantMember;
  canEdit: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [role, setRole] = useState(member.role);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const handleSave = async () => {
    setSaving(true);
    setMsg(null);
    try {
      // Uses PATCH /auth/api/v1/admin/users/{id} with tenant_role
      await themApi.updateUser(member.user_id, { tenant_role: role });
      setMsg({ ok: true, text: 'Saved' });
      onSaved();
    } catch {
      setMsg({ ok: false, text: 'Failed to save' });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{
      position: 'fixed', top: 0, right: 0, bottom: 0, width: 340,
      background: '#0f1117', borderLeft: '1px solid var(--tm-border)',
      padding: '28px 24px', zIndex: 100, display: 'flex', flexDirection: 'column', gap: 16,
      overflowY: 'auto',
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: 'var(--tm-text)' }}>
          {member.username}
        </h3>
        <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tm-text-muted)', fontSize: 18 }}>✕</button>
      </div>

      <div style={{ fontSize: 13, color: 'var(--tm-text-muted)' }}>{member.email}</div>

      <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 16 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 12 }}>Profile</div>
        <div style={{ fontSize: 13, color: 'var(--tm-text)', marginBottom: 6 }}>
          <span style={{ color: 'var(--tm-text-muted)' }}>Username: </span>{member.username}
        </div>
        <div style={{ fontSize: 13, color: 'var(--tm-text)', marginBottom: 6 }}>
          <span style={{ color: 'var(--tm-text-muted)' }}>Email: </span>{member.email}
        </div>
        <div style={{ fontSize: 13, color: 'var(--tm-text)' }}>
          <span style={{ color: 'var(--tm-text-muted)' }}>Joined: </span>
          {new Date(member.created_at).toLocaleDateString()}
        </div>
      </div>

      {canEdit && (
        <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 16 }}>
          <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 12 }}>Membership</div>
          <label style={{ display: 'block', fontSize: 12, color: 'var(--tm-text-muted)', marginBottom: 6 }}>Role</label>
          <select
            value={role}
            onChange={e => setRole(e.target.value)}
            style={{
              width: '100%', padding: '8px 10px', borderRadius: 7,
              border: '1px solid var(--tm-border)', background: 'var(--tm-card)',
              color: 'var(--tm-text)', fontSize: 13, marginBottom: 14,
            }}
          >
            <option value="viewer">viewer</option>
            <option value="member">member</option>
            <option value="admin">admin</option>
          </select>

          <button
            onClick={handleSave}
            disabled={saving || role === member.role}
            style={{
              width: '100%', padding: '9px 0', borderRadius: 8, border: 'none',
              background: ACCENT, color: '#fff', fontWeight: 600, fontSize: 13,
              cursor: saving || role === member.role ? 'not-allowed' : 'pointer',
              opacity: saving || role === member.role ? 0.6 : 1,
            }}
          >
            {saving ? 'Saving…' : 'Save'}
          </button>

          {msg && (
            <div style={{ marginTop: 10, fontSize: 12, color: msg.ok ? '#34d399' : '#f87171', textAlign: 'center' }}>
              {msg.text}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function TenantMembersPage() {
  const { user } = useAuthStore();
  const [members, setMembers] = useState<TenantMember[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<TenantMember | null>(null);

  const canEdit = user?.role === 'admin' || user?.role === 'super_admin';

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await themApi.listMyMembers();
      setMembers(data ?? []);
    } catch (e) {
      setError((e as Error).message ?? 'Failed to load');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1, padding: '32px 32px 64px' }}>
          {/* Header */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
            <div>
              <h2 style={{ margin: 0, fontSize: 20, fontWeight: 700, color: 'var(--tm-text)' }}>Members</h2>
              <div style={{ fontSize: 13, color: 'var(--tm-text-muted)', marginTop: 2 }}>Users in your tenant</div>
            </div>
            <button onClick={load} title="Refresh" style={{ padding: '6px 10px', borderRadius: 7, border: '1px solid var(--tm-border)', background: 'var(--tm-card)', color: 'var(--tm-text-muted)', cursor: 'pointer', display: 'flex', alignItems: 'center' }}>
              <span className="material-symbols-outlined" style={{ fontSize: 18 }}>refresh</span>
            </button>
          </div>

          {/* Status */}
          {loading && <div style={{ color: 'var(--tm-text-muted)', fontSize: 14 }}>Loading…</div>}
          {error && <div style={{ color: '#f87171', fontSize: 14 }}>{error}</div>}

          {/* Table */}
          {!loading && !error && (
            members.length === 0 ? (
              <div style={{ color: 'var(--tm-text-muted)', fontSize: 14 }}>No members yet.</div>
            ) : (
              <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: 12, overflow: 'hidden' }}>
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                  <thead>
                    <tr style={{ background: 'rgba(255,255,255,0.03)' }}>
                      {['Username', 'Email', 'Role', 'Joined'].map(h => (
                        <th key={h} style={{ textAlign: 'left', padding: '10px 16px', fontSize: 11, fontWeight: 600, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', borderBottom: '1px solid var(--tm-border)' }}>
                          {h}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {members.map((m, i) => (
                      <tr
                        key={m.id}
                        onClick={() => setSelected(m)}
                        style={{
                          borderBottom: i < members.length - 1 ? '1px solid var(--tm-border)' : 'none',
                          cursor: 'pointer',
                          background: selected?.id === m.id ? 'rgba(129,140,248,0.08)' : 'transparent',
                        }}
                        onMouseEnter={e => { if (selected?.id !== m.id) (e.currentTarget as HTMLElement).style.background = 'rgba(255,255,255,0.03)'; }}
                        onMouseLeave={e => { if (selected?.id !== m.id) (e.currentTarget as HTMLElement).style.background = 'transparent'; }}
                      >
                        <td style={{ padding: '12px 16px', color: 'var(--tm-text)', fontWeight: 500 }}>{m.username}</td>
                        <td style={{ padding: '12px 16px', color: 'var(--tm-text-muted)' }}>{m.email || '—'}</td>
                        <td style={{ padding: '12px 16px' }}><RoleBadge role={m.role} /></td>
                        <td style={{ padding: '12px 16px', color: 'var(--tm-text-muted)' }}>{new Date(m.created_at).toLocaleDateString()}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          )}
        </main>

        {selected && (
          <MemberPanel
            member={selected}
            canEdit={canEdit}
            onClose={() => setSelected(null)}
            onSaved={() => { load(); }}
          />
        )}
      </div>
    </AuthGuard>
  );
}
