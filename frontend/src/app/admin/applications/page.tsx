'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Application, type OrchestratorFull } from '@/lib/api';

const ENTRY_POINT_TYPES = ['websocket', 'sse', 'webrtc'] as const;
type EntryPointType = typeof ENTRY_POINT_TYPES[number];

const EP_LABELS: Record<EntryPointType, string> = {
  websocket: 'WebSocket',
  sse:       'SSE (streaming)',
  webrtc:    'WebRTC',
};

const EP_ICONS: Record<EntryPointType, string> = {
  websocket: 'chat',
  sse:       'stream',
  webrtc:    'videocam',
};

const EMPTY_FORM = {
  name: '',
  slug: '',
  entry_point_type: 'websocket' as EntryPointType,
  orchestrator_id: '',
  access_mode: 'token' as 'token' | 'public',
  enabled: true,
};

function Badge({ on }: { on: boolean }) {
  return (
    <span style={{ padding: '2px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600,
      background: on ? '#4edea318' : '#f8717118', color: on ? '#4edea3' : '#f87171' }}>
      {on ? 'enabled' : 'disabled'}
    </span>
  );
}

function EPBadge({ type }: { type: string }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4,
      padding: '2px 8px', borderRadius: 6, fontSize: 11, fontWeight: 600,
      background: 'var(--tm-accent-subtle)', color: 'var(--tm-accent)' }}>
      <span className="material-icons" style={{ fontSize: 13 }}>
        {EP_ICONS[type as EntryPointType] ?? 'extension'}
      </span>
      {EP_LABELS[type as EntryPointType] ?? type}
    </span>
  );
}

function CopyBox({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  function copy() {
    navigator.clipboard?.writeText(value).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
      <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', minWidth: 80 }}>{label}</span>
      <code style={{ flex: 1, fontSize: 11, fontFamily: 'monospace', color: 'var(--tm-text)',
        background: 'var(--tm-surface-2)', padding: '3px 8px', borderRadius: 4 }}>{value}</code>
      <button onClick={copy} style={{ flexShrink: 0, padding: '3px 10px', borderRadius: 5, border: 'none',
        cursor: 'pointer', background: copied ? '#4edea3' : 'var(--tm-surface-2)',
        color: copied ? '#fff' : 'var(--tm-text-muted)', fontSize: 11 }}>
        {copied ? 'Copied!' : 'Copy'}
      </button>
    </div>
  );
}

export default function ApplicationsPage() {
  const [list, setList] = useState<Application[]>([]);
  const [orchestrators, setOrchestrators] = useState<OrchestratorFull[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [form, setForm] = useState({ ...EMPTY_FORM });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [expandedId, setExpandedId] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    Promise.all([themApi.applications(), themApi.orchestrators()])
      .then(([apps, orchs]) => { setList(apps); setOrchestrators(orchs); })
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(); }, []);

  function openCreate() {
    setEditId(null);
    setForm({ ...EMPTY_FORM });
    setError('');
    setShowForm(true);
  }

  function openEdit(app: Application) {
    setEditId(app.id);
    setForm({
      name: app.name,
      slug: app.slug,
      entry_point_type: (app.entry_point_type as EntryPointType) ?? 'websocket',
      orchestrator_id: app.orchestrator_id,
      access_mode: ((app.access_policy as any)?.mode ?? 'token') as 'token' | 'public',
      enabled: app.enabled,
    });
    setError('');
    setShowForm(true);
  }

  async function save() {
    setSaving(true); setError('');
    try {
      const body = {
        name: form.name,
        slug: form.slug,
        entry_point_type: form.entry_point_type,
        orchestrator_id: form.orchestrator_id,
        access_policy: { mode: form.access_mode },
        enabled: form.enabled,
      };
      if (editId) {
        await themApi.updateApplication(editId, body);
      } else {
        await themApi.createApplication(body);
      }
      setShowForm(false);
      await load();
    } catch (e: any) {
      setError(e?.message ?? 'Save failed');
    } finally {
      setSaving(false);
    }
  }

  async function toggleEnabled(app: Application) {
    try {
      await themApi.updateApplication(app.id, { enabled: !app.enabled });
      await load();
    } catch {/* ignore */}
  }

  async function del(app: Application) {
    if (!confirm(`Delete application "${app.name}"?`)) return;
    try {
      await themApi.deleteApplication(app.id);
      await load();
    } catch {/* ignore */}
  }

  // Build the endpoint URLs for an app (relative to the platform host)
  function wsUrl(slug: string) { return `ws://<host>:8088/apps/${slug}/ws`; }
  function restUrl(slug: string) { return `http://<host>:8088/apps/${slug}`; }

  const inp = (field: keyof typeof form, type: string = 'text') => ({
    type,
    value: String(form[field]),
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm(f => ({ ...f, [field]: type === 'checkbox' ? (e.target as HTMLInputElement).checked : e.target.value })),
    style: { width: '100%', padding: '8px 12px', borderRadius: 8, border: '1px solid var(--tm-border)',
      background: 'var(--tm-surface-2)', color: 'var(--tm-text)', fontSize: 14 } as React.CSSProperties,
  });

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ flex: 1, padding: '32px 40px', maxWidth: 900 }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 28 }}>
            <div>
              <h1 style={{ fontSize: 22, fontWeight: 700, color: 'var(--tm-text)', margin: 0 }}>Applications</h1>
              <p style={{ fontSize: 13, color: 'var(--tm-text-muted)', margin: '4px 0 0' }}>
                Compose an orchestrator + entry point into a deployable agentic app.
              </p>
            </div>
            <button onClick={openCreate}
              style={{ padding: '9px 20px', borderRadius: 8, border: 'none', cursor: 'pointer',
                background: 'var(--tm-accent)', color: '#fff', fontWeight: 600, fontSize: 14 }}>
              + New Application
            </button>
          </div>

          {loading ? (
            <p style={{ color: 'var(--tm-text-muted)', fontSize: 14 }}>Loading…</p>
          ) : list.length === 0 ? (
            <div style={{ textAlign: 'center', padding: '60px 0', color: 'var(--tm-text-muted)' }}>
              <span className="material-icons" style={{ fontSize: 48, marginBottom: 12, opacity: 0.3 }}>apps</span>
              <p>No applications yet. Create one to expose an orchestrator as a shareable endpoint.</p>
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              {list.map(app => (
                <div key={app.id}
                  style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)',
                    borderRadius: 12, overflow: 'hidden' }}>
                  {/* Row */}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '14px 20px' }}>
                    <span className="material-icons" style={{ fontSize: 20, color: 'var(--tm-accent)', flexShrink: 0 }}>
                      {EP_ICONS[app.entry_point_type as EntryPointType] ?? 'extension'}
                    </span>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                        <span style={{ fontWeight: 600, fontSize: 15, color: 'var(--tm-text)' }}>{app.name}</span>
                        <code style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>{app.slug}</code>
                        <Badge on={app.enabled} />
                      </div>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
                        <EPBadge type={app.entry_point_type} />
                        {app.orchestrator_name && (
                          <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
                            → {app.orchestrator_name}
                          </span>
                        )}
                        <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
                          · access: {(app.access_policy as any)?.mode ?? 'token'}
                        </span>
                      </div>
                    </div>
                    <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
                      <button onClick={() => setExpandedId(expandedId === app.id ? null : app.id)}
                        style={{ padding: '5px 12px', borderRadius: 6, border: '1px solid var(--tm-border)',
                          background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: 12 }}>
                        {expandedId === app.id ? 'Hide URLs' : 'URLs'}
                      </button>
                      <button onClick={() => toggleEnabled(app)}
                        style={{ padding: '5px 12px', borderRadius: 6, border: '1px solid var(--tm-border)',
                          background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: 12 }}>
                        {app.enabled ? 'Disable' : 'Enable'}
                      </button>
                      <button onClick={() => openEdit(app)}
                        style={{ padding: '5px 12px', borderRadius: 6, border: '1px solid var(--tm-border)',
                          background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: 12 }}>
                        Edit
                      </button>
                      <button onClick={() => del(app)}
                        style={{ padding: '5px 12px', borderRadius: 6, border: 'none',
                          background: '#f8717118', color: '#f87171', cursor: 'pointer', fontSize: 12, fontWeight: 600 }}>
                        Delete
                      </button>
                    </div>
                  </div>
                  {/* Expanded URLs panel */}
                  {expandedId === app.id && (
                    <div style={{ borderTop: '1px solid var(--tm-border)', padding: '14px 20px',
                      background: 'var(--tm-surface-2)' }}>
                      <div style={{ fontSize: 11, fontWeight: 700, color: 'var(--tm-text-muted)',
                        textTransform: 'uppercase', letterSpacing: 1, marginBottom: 10 }}>
                        Entry Point URLs
                      </div>
                      {app.entry_point_type === 'websocket' && (
                        <CopyBox label="WebSocket" value={wsUrl(app.slug)} />
                      )}
                      {app.entry_point_type === 'sse' && (
                        <CopyBox label="SSE" value={`${restUrl(app.slug)}/sse?message={your message}`} />
                      )}
                      {app.entry_point_type === 'webrtc' && (
                        <CopyBox label="WebRTC (coming soon)" value={wsUrl(app.slug)} />
                      )}
                      <div style={{ marginTop: 10, fontSize: 11, color: 'var(--tm-text-muted)' }}>
                        {(app.access_policy as any)?.mode === 'public'
                          ? 'No auth required — public access'
                          : 'Bearer token required — use /api/v1/admin/tokens to create one'}
                      </div>
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}

          {/* ── Modal form ── */}
          {showForm && (
            <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.55)',
              display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}
              onClick={e => { if (e.target === e.currentTarget) setShowForm(false); }}>
              <div style={{ background: 'var(--tm-surface)', borderRadius: 14, padding: '28px 32px',
                width: '100%', maxWidth: 520, boxShadow: '0 20px 60px rgba(0,0,0,.4)' }}>
                <h2 style={{ fontSize: 18, fontWeight: 700, color: 'var(--tm-text)', margin: '0 0 20px' }}>
                  {editId ? 'Edit Application' : 'New Application'}
                </h2>

                <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                  <label style={{ fontSize: 13, color: 'var(--tm-text-muted)' }}>Name
                    <input {...inp('name')} placeholder="My Chat App" style={{ ...inp('name').style, marginTop: 5 }} />
                  </label>

                  <label style={{ fontSize: 13, color: 'var(--tm-text-muted)' }}>Slug
                    <input {...inp('slug')} placeholder="my-chat-app"
                      style={{ ...inp('slug').style, marginTop: 5, fontFamily: 'monospace' }} />
                    <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
                      lowercase letters, numbers, - and _ only · used in the URL
                    </span>
                  </label>

                  <label style={{ fontSize: 13, color: 'var(--tm-text-muted)' }}>Entry Point Type
                    <select value={form.entry_point_type}
                      onChange={e => setForm(f => ({ ...f, entry_point_type: e.target.value as EntryPointType }))}
                      style={{ width: '100%', marginTop: 5, padding: '8px 12px', borderRadius: 8,
                        border: '1px solid var(--tm-border)', background: 'var(--tm-surface-2)',
                        color: 'var(--tm-text)', fontSize: 14 }}>
                      {ENTRY_POINT_TYPES.map(t => (
                        <option key={t} value={t}>{EP_LABELS[t]}</option>
                      ))}
                    </select>
                  </label>

                  <label style={{ fontSize: 13, color: 'var(--tm-text-muted)' }}>Orchestrator
                    <select value={form.orchestrator_id}
                      onChange={e => setForm(f => ({ ...f, orchestrator_id: e.target.value }))}
                      style={{ width: '100%', marginTop: 5, padding: '8px 12px', borderRadius: 8,
                        border: '1px solid var(--tm-border)', background: 'var(--tm-surface-2)',
                        color: 'var(--tm-text)', fontSize: 14 }}>
                      <option value="">— select —</option>
                      {orchestrators.filter(o => o.enabled).map(o => (
                        <option key={o.id} value={o.id}>{o.display_name} ({o.name})</option>
                      ))}
                    </select>
                  </label>

                  <label style={{ fontSize: 13, color: 'var(--tm-text-muted)' }}>Access
                    <select value={form.access_mode}
                      onChange={e => setForm(f => ({ ...f, access_mode: e.target.value as 'token' | 'public' }))}
                      style={{ width: '100%', marginTop: 5, padding: '8px 12px', borderRadius: 8,
                        border: '1px solid var(--tm-border)', background: 'var(--tm-surface-2)',
                        color: 'var(--tm-text)', fontSize: 14 }}>
                      <option value="token">Token required (default)</option>
                      <option value="public">Public (no auth)</option>
                    </select>
                  </label>

                  <label style={{ display: 'flex', alignItems: 'center', gap: 10,
                    fontSize: 13, color: 'var(--tm-text-muted)', cursor: 'pointer' }}>
                    <input type="checkbox" checked={form.enabled}
                      onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
                    Enabled
                  </label>

                  {error && (
                    <div style={{ padding: '10px 14px', borderRadius: 8, background: '#f8717118',
                      color: '#f87171', fontSize: 13 }}>{error}</div>
                  )}

                  <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end', marginTop: 6 }}>
                    <button onClick={() => setShowForm(false)}
                      style={{ padding: '8px 18px', borderRadius: 8, border: '1px solid var(--tm-border)',
                        background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: 14 }}>
                      Cancel
                    </button>
                    <button onClick={save} disabled={saving || !form.name || !form.slug || !form.orchestrator_id}
                      style={{ padding: '8px 22px', borderRadius: 8, border: 'none', cursor: 'pointer',
                        background: 'var(--tm-accent)', color: '#fff', fontWeight: 600, fontSize: 14,
                        opacity: saving || !form.name || !form.slug || !form.orchestrator_id ? 0.5 : 1 }}>
                      {saving ? 'Saving…' : editId ? 'Update' : 'Create'}
                    </button>
                  </div>
                </div>
              </div>
            </div>
          )}
        </main>
      </div>
    </AuthGuard>
  );
}
