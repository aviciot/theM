'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type AccessToken, type OrchestratorFull } from '@/lib/api';

// ── Design tokens ─────────────────────────────────────────────────────────────
const BG     = '#060a14';
const CYAN   = '#00d1ff';
const AMBER  = '#f59e0b';
const GREEN  = '#34d399';
const RED    = '#f87171';
const TEXT   = '#e2e8f0';
const MUTED  = '#64748b';
const BORDER = 'rgba(255,255,255,0.07)';
const SURFACE = 'rgba(10,18,32,0.92)';

const TOKEN_CSS = `
  .token-panel {
    background:
      linear-gradient(160deg, rgba(255,255,255,0.028) 0%, rgba(255,255,255,0.004) 50%, rgba(0,0,0,0.06) 100%),
      ${SURFACE};
    border: 1px solid ${BORDER};
    backdrop-filter: blur(12px);
    border-radius: 20px;
    box-shadow:
      0 8px 32px rgba(0,0,0,0.4),
      0 2px 8px rgba(0,0,0,0.25),
      inset 0 1px 0 rgba(255,255,255,0.04);
    overflow: hidden;
  }
  .token-row {
    display: grid;
    grid-template-columns: 1fr 200px 130px 130px 90px 80px;
    gap: 12px;
    align-items: center;
    padding: 14px 20px;
    border-bottom: 1px solid rgba(255,255,255,0.05);
    transition: background 180ms ease, box-shadow 180ms ease;
  }
  .token-row:last-of-type {
    border-bottom: none;
  }
  .token-row:hover {
    background: rgba(0,209,255,0.03);
    box-shadow: inset 3px 0 0 rgba(0,209,255,0.45);
  }
  .token-new-row {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 14px 20px;
    cursor: pointer;
    border-top: 1px dashed rgba(99,102,241,0.28);
    transition: background 180ms ease;
    color: #818cf8;
    font-size: 13px;
    font-weight: 700;
    font-family: Geist, sans-serif;
  }
  .token-new-row:hover {
    background: rgba(99,102,241,0.05);
  }
  .token-action-btn {
    display: flex; align-items: center; justify-content: center;
    width: 32px; height: 32px; border-radius: 8px; border: none;
    cursor: pointer; flex-shrink: 0;
    transition: background 150ms ease, color 150ms ease;
  }
  .token-action-btn--toggle-on  { background: rgba(248,113,113,0.08); color: ${RED}; }
  .token-action-btn--toggle-on:hover  { background: rgba(248,113,113,0.18); }
  .token-action-btn--toggle-off { background: rgba(52,211,153,0.08);  color: ${GREEN}; }
  .token-action-btn--toggle-off:hover { background: rgba(52,211,153,0.18); }
  .token-action-btn--delete { background: rgba(248,113,113,0.06); color: ${MUTED}; }
  .token-action-btn--delete:hover { background: rgba(248,113,113,0.15); color: ${RED}; }
`;

// ── Helpers ───────────────────────────────────────────────────────────────────
function fmt(dt: string | null) {
  if (!dt) return '—';
  return new Date(dt).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

// ── Copy banner (shown after token creation) ──────────────────────────────────
function CopyBox({ value, onDone }: { value: string; onDone: () => void }) {
  const [copied, setCopied] = useState(false);
  function copy() {
    navigator.clipboard?.writeText(value).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }
  return (
    <div style={{
      marginBottom: 24, padding: '16px 20px', borderRadius: 14,
      background: 'rgba(245,158,11,0.07)', border: '1px solid rgba(245,158,11,0.3)',
      backdropFilter: 'blur(12px)',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 16, color: AMBER }}>warning</span>
          <span style={{ fontSize: 12, fontWeight: 700, color: AMBER }}>Copy now — this token won't be shown again</span>
        </div>
        <button onClick={onDone} style={{ background: 'none', border: 'none', cursor: 'pointer', color: MUTED, display: 'flex', alignItems: 'center' }}>
          <span className="material-symbols-outlined" style={{ fontSize: 18 }}>close</span>
        </button>
      </div>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <code style={{
          flex: 1, fontSize: 11, color: TEXT, wordBreak: 'break-all',
          fontFamily: 'JetBrains Mono, monospace',
          background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 8,
          border: '1px solid rgba(255,255,255,0.06)',
        }}>{value}</code>
        <button onClick={copy} style={{
          flexShrink: 0, padding: '8px 16px', borderRadius: 8, border: 'none',
          cursor: 'pointer', fontWeight: 700, fontSize: 12,
          background: copied ? GREEN : CYAN,
          color: copied ? '#021a10' : '#021520',
          transition: 'background 200ms ease',
        }}>
          {copied ? '✓ Copied' : 'Copy'}
        </button>
      </div>
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────
export default function TokensPage() {
  const [list, setList]                   = useState<AccessToken[]>([]);
  const [orchestrators, setOrchestrators] = useState<OrchestratorFull[]>([]);
  const [loading, setLoading]             = useState(true);
  const [showForm, setShowForm]           = useState(false);
  const [newToken, setNewToken]           = useState('');
  const [form, setForm]                   = useState({ label: '', user_id: 1, orchestrator_id: '' });
  const [saving, setSaving]               = useState(false);
  const [error, setError]                 = useState('');

  async function load() {
    setLoading(true);
    Promise.all([themApi.tokens(), themApi.orchestrators()])
      .then(([t, o]) => { setList(t); setOrchestrators(o); })
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(); }, []);

  function openCreate() {
    setForm({ label: '', user_id: 1, orchestrator_id: '' });
    setError('');
    setShowForm(true);
  }

  async function save() {
    setSaving(true); setError('');
    try {
      const body: any = { label: form.label, user_id: Number(form.user_id) };
      if (form.orchestrator_id) body.orchestrator_id = form.orchestrator_id;
      const created = await themApi.createToken(body);
      setNewToken(created.token ?? '');
      setShowForm(false);
      load();
    } catch (e: any) {
      setError(e.message);
    } finally {
      setSaving(false);
    }
  }

  async function toggle(t: AccessToken) {
    await themApi.updateToken(t.id, { enabled: !t.enabled }).catch((e) => alert(e.message));
    load();
  }

  async function del(t: AccessToken) {
    if (!confirm(`Delete token "${t.label}"?`)) return;
    await themApi.deleteToken(t.id).catch((e) => alert(e.message));
    load();
  }

  const orchName = (id: string | null) =>
    id ? (orchestrators.find((o) => o.id === id)?.display_name ?? id.slice(0, 8)) : 'All';

  return (
    <AuthGuard>
      <style>{TOKEN_CSS}</style>
      <div style={{ display: 'flex', minHeight: '100vh', background: BG }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1, padding: '36px 48px' }}>

          {/* Page header */}
          <div style={{ marginBottom: 32 }}>
            <h1 style={{ fontSize: 26, fontWeight: 700, color: TEXT, margin: 0, fontFamily: 'Geist, sans-serif', letterSpacing: -0.5 }}>
              Access Tokens
            </h1>
            <p style={{ fontSize: 13, color: MUTED, margin: '6px 0 0', fontFamily: 'Inter, sans-serif' }}>
              Bearer tokens that authenticate API and WebSocket clients to orchestrators.
            </p>
          </div>

          {/* New token copy banner */}
          {newToken && <CopyBox value={newToken} onDone={() => setNewToken('')} />}

          {/* Token panel */}
          {!loading && (
            <div className="token-panel">

              {/* Column headers */}
              <div style={{
                display: 'grid',
                gridTemplateColumns: '1fr 200px 130px 130px 90px 80px',
                gap: 12, padding: '10px 20px',
                borderBottom: `1px solid ${BORDER}`,
                background: 'rgba(255,255,255,0.015)',
              }}>
                {['Token', 'Scope', 'Expires', 'Last used', 'Status', ''].map((h) => (
                  <div key={h} style={{ fontSize: 10, fontWeight: 700, color: MUTED, textTransform: 'uppercase', letterSpacing: '0.6px' }}>{h}</div>
                ))}
              </div>

              {/* Empty state */}
              {list.length === 0 && (
                <div style={{ padding: '60px 0', textAlign: 'center', color: MUTED }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 40, opacity: 0.25, display: 'block', marginBottom: 12 }}>key</span>
                  <div style={{ fontSize: 14, marginBottom: 4 }}>No tokens yet</div>
                  <div style={{ fontSize: 12 }}>Create one to authenticate API clients.</div>
                </div>
              )}

              {/* Token rows */}
              {list.map((t) => (
                <div key={t.id} className="token-row">

                  {/* Icon + label + user */}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12, minWidth: 0 }}>
                    <div style={{
                      width: 38, height: 38, borderRadius: 10, flexShrink: 0,
                      background: 'radial-gradient(circle at 30% 30%, rgba(245,158,11,0.22), transparent 70%)',
                      border: '1px solid rgba(245,158,11,0.38)',
                      display: 'flex', alignItems: 'center', justifyContent: 'center',
                    }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 18, color: AMBER }}>key</span>
                    </div>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontWeight: 700, fontSize: 14, color: TEXT, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {t.label}
                      </div>
                      <div style={{ fontSize: 11, color: MUTED, marginTop: 2 }}>user #{t.user_id}</div>
                    </div>
                  </div>

                  {/* Scope */}
                  <div>
                    <span style={{
                      display: 'inline-flex', alignItems: 'center', gap: 5,
                      padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 600,
                      background: 'rgba(167,139,250,0.1)', color: '#a78bfa',
                      border: '1px solid rgba(167,139,250,0.25)',
                    }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 13 }}>hub</span>
                      {orchName(t.orchestrator_id)}
                    </span>
                  </div>

                  {/* Expires */}
                  <div style={{ fontSize: 12, color: MUTED, fontFamily: 'JetBrains Mono, monospace' }}>
                    {fmt(t.expires_at)}
                  </div>

                  {/* Last used */}
                  <div style={{ fontSize: 12, color: MUTED, fontFamily: 'JetBrains Mono, monospace' }}>
                    {fmt(t.last_used_at)}
                  </div>

                  {/* Status pill */}
                  <span style={{
                    display: 'inline-flex', alignItems: 'center', gap: 5,
                    padding: '3px 9px', borderRadius: 20, fontSize: 11, fontWeight: 700,
                    background: t.enabled ? 'rgba(52,211,153,0.1)' : 'rgba(248,113,113,0.1)',
                    color: t.enabled ? GREEN : RED,
                    border: `1px solid ${t.enabled ? 'rgba(52,211,153,0.3)' : 'rgba(248,113,113,0.3)'}`,
                  }}>
                    {t.enabled && <span style={{ width: 5, height: 5, borderRadius: '50%', background: GREEN, display: 'inline-block', boxShadow: `0 0 5px ${GREEN}` }} />}
                    {t.enabled ? 'active' : 'disabled'}
                  </span>

                  {/* Actions */}
                  <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                    <button
                      className={`token-action-btn ${t.enabled ? 'token-action-btn--toggle-on' : 'token-action-btn--toggle-off'}`}
                      onClick={() => toggle(t)}
                      title={t.enabled ? 'Disable' : 'Enable'}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 16 }}>
                        {t.enabled ? 'toggle_on' : 'toggle_off'}
                      </span>
                    </button>
                    <button
                      className="token-action-btn token-action-btn--delete"
                      onClick={() => del(t)}
                      title="Delete token"
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                    </button>
                  </div>
                </div>
              ))}

              {/* Add new row at bottom */}
              <div className="token-new-row" onClick={openCreate}>
                <div style={{
                  width: 22, height: 22, borderRadius: 6,
                  border: '1.5px dashed rgba(99,102,241,0.5)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 15, color: '#818cf8' }}>add</span>
                </div>
                New Token
              </div>
            </div>
          )}
        </main>
      </div>

      {/* ── Create modal ──────────────────────────────────────────────────── */}
      {showForm && (
        <div
          style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.72)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24 }}
          onClick={() => setShowForm(false)}
        >
          <div
            style={{
              background: 'rgba(10,18,32,0.97)', border: `1px solid ${BORDER}`,
              backdropFilter: 'blur(20px)', borderRadius: 20,
              width: '100%', maxWidth: 440, padding: 32,
              boxShadow: '0 24px 64px rgba(0,0,0,0.6)',
            }}
            onClick={(e) => e.stopPropagation()}
          >
            {/* Modal header */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
              <div style={{
                width: 40, height: 40, borderRadius: 11,
                background: 'radial-gradient(circle at 30% 30%, rgba(245,158,11,0.25), transparent 70%)',
                border: '1px solid rgba(245,158,11,0.4)',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <span className="material-symbols-outlined" style={{ fontSize: 20, color: AMBER }}>key</span>
              </div>
              <h2 style={{ fontSize: 16, fontWeight: 700, color: TEXT, margin: 0 }}>New access token</h2>
            </div>

            {error && (
              <div style={{ marginBottom: 16, padding: '10px 14px', borderRadius: 8, background: 'rgba(248,113,113,0.1)', border: '1px solid rgba(248,113,113,0.3)', color: RED, fontSize: 13 }}>
                {error}
              </div>
            )}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div>
                <label style={LBL}>Label</label>
                <input
                  value={form.label}
                  onChange={(e) => setForm((p) => ({ ...p, label: e.target.value }))}
                  placeholder="e.g. CI pipeline token"
                  style={INP}
                  autoFocus
                />
              </div>
              <div>
                <label style={LBL}>Scope (orchestrator)</label>
                <select
                  value={form.orchestrator_id}
                  onChange={(e) => setForm((p) => ({ ...p, orchestrator_id: e.target.value }))}
                  style={INP}
                >
                  <option value="">All orchestrators</option>
                  {orchestrators.map((o) => <option key={o.id} value={o.id}>{o.display_name}</option>)}
                </select>
              </div>
            </div>

            <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end', marginTop: 28 }}>
              <button
                onClick={() => setShowForm(false)}
                style={{ padding: '8px 18px', borderRadius: 8, border: `1px solid ${BORDER}`, background: 'transparent', color: TEXT, cursor: 'pointer', fontSize: 13 }}
              >
                Cancel
              </button>
              <button
                onClick={save}
                disabled={saving || !form.label}
                style={{
                  padding: '8px 22px', borderRadius: 8, border: 'none',
                  background: CYAN, color: '#021520',
                  cursor: saving || !form.label ? 'not-allowed' : 'pointer',
                  fontWeight: 700, fontSize: 13,
                  opacity: saving || !form.label ? 0.5 : 1,
                  boxShadow: '0 0 14px rgba(0,209,255,0.3)',
                }}
              >
                {saving ? 'Creating…' : 'Create token'}
              </button>
            </div>
          </div>
        </div>
      )}
    </AuthGuard>
  );
}

const INP: React.CSSProperties = {
  width: '100%',
  background: 'rgba(255,255,255,0.04)',
  border: `1px solid rgba(255,255,255,0.07)`,
  borderRadius: 8, padding: '9px 12px',
  color: '#e2e8f0', fontSize: 13, boxSizing: 'border-box',
  outline: 'none', fontFamily: 'Inter, sans-serif',
};

const LBL: React.CSSProperties = {
  display: 'block', fontSize: 11, fontWeight: 700, color: '#64748b',
  marginBottom: 6, textTransform: 'uppercase', letterSpacing: '0.5px',
};
