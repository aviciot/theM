'use client';
import { useEffect, useState, useCallback } from 'react';
import {
  themApi,
  type ManagedApp,
  type ManagedAppDetail,
  type ManagedAppBinding,
  type TenantRecord,
} from '@/lib/api';
import Sidebar from '@/components/Sidebar';

const ACCENT = '#818cf8';
const ACCENT_BORDER = 'rgba(129,140,248,0.4)';

// ── Helpers ────────────────────────────────────────────────────────────────────

function bindingForApp(bindings: ManagedAppBinding[], appId: string): ManagedAppBinding | undefined {
  return bindings.find(b => b.app_id === appId);
}

// ── BindingPanel ───────────────────────────────────────────────────────────────

function BindingPanel({
  app,
  tenant,
  binding,
  onClose,
  onSaved,
}: {
  app: ManagedAppDetail;
  tenant: TenantRecord;
  binding: ManagedAppBinding | undefined;
  onClose: () => void;
  onSaved: (b: ManagedAppBinding) => void;
}) {
  const [enabled, setEnabled] = useState(binding?.enabled ?? true);
  const [config, setConfig] = useState<Record<string, string>>(() => {
    const base: Record<string, string> = {};
    for (const p of app.params) {
      base[p.key] = String((binding?.config ?? {})[p.key] ?? p.default_value ?? '');
    }
    return base;
  });
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState('');

  useEffect(() => {
    setEnabled(binding?.enabled ?? true);
    const base: Record<string, string> = {};
    for (const p of app.params) {
      base[p.key] = String((binding?.config ?? {})[p.key] ?? p.default_value ?? '');
    }
    setConfig(base);
    setMsg('');
  }, [app.id, binding, tenant.id]);

  async function save() {
    setSaving(true); setMsg('');
    try {
      const saved = await themApi.upsertManagedAppBinding(tenant.id, app.id, {
        config,
        enabled,
        app_version: app.version,
      });
      onSaved(saved);
      setMsg('Saved');
    } catch { setMsg('Error saving'); }
    finally { setSaving(false); }
  }

  const inp: React.CSSProperties = {
    width: '100%', padding: '8px 12px', borderRadius: '8px', fontSize: '13px',
    background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)',
    color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box',
  };
  const lbl: React.CSSProperties = {
    fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', marginBottom: '5px', display: 'block',
  };
  const row: React.CSSProperties = { marginBottom: '16px' };

  return (
    <aside style={{
      position: 'fixed', right: 0, top: 0, bottom: 0, width: '420px', zIndex: 50,
      background: 'var(--tm-sidebar)', borderLeft: '1px solid rgba(255,255,255,.08)',
      display: 'flex', flexDirection: 'column', overflowY: 'auto',
    }} className="custom-scrollbar">
      <div style={{ padding: '20px 24px 16px', borderBottom: '1px solid rgba(255,255,255,.06)', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div>
          <p style={{ fontWeight: 700, fontSize: '15px', color: 'var(--tm-card-text)', margin: '0 0 2px 0' }}>{app.name}</p>
          <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', margin: 0 }}>
            Binding for <span style={{ color: ACCENT }}>{tenant.display_name}</span>
          </p>
        </div>
        <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tm-card-text-muted)', padding: '4px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '20px' }}>close</span>
        </button>
      </div>

      <div style={{ padding: '20px 24px', flex: 1 }}>
        <div style={{ ...row, display: 'flex', alignItems: 'center', gap: '10px' }}>
          <label style={{ ...lbl, margin: 0 }}>Active</label>
          <button onClick={() => setEnabled(e => !e)} style={{
            width: '40px', height: '22px', borderRadius: '11px', border: 'none', cursor: 'pointer', padding: 0, position: 'relative',
            background: enabled ? ACCENT : 'rgba(255,255,255,.15)', transition: 'background .2s',
          }}>
            <span style={{ position: 'absolute', top: '3px', left: enabled ? '21px' : '3px', width: '16px', height: '16px', borderRadius: '50%', background: '#fff', transition: 'left .2s' }} />
          </button>
        </div>

        {app.params.length > 0 && (
          <>
            <p style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', margin: '20px 0 12px 0', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
              Parameters
            </p>
            {app.params.map(p => (
              <div key={p.key} style={row}>
                <label style={lbl}>
                  {p.label}
                  {p.required && <span style={{ color: '#f87171', marginLeft: '4px' }}>*</span>}
                  {p.description && (
                    <span style={{ fontWeight: 400, color: 'var(--tm-card-text-muted)', marginLeft: '6px' }}>— {p.description}</span>
                  )}
                </label>
                {p.param_type === 'enum' && p.enum_values && p.enum_values.length > 0 ? (
                  <select
                    value={config[p.key] ?? ''}
                    onChange={e => setConfig(prev => ({ ...prev, [p.key]: e.target.value }))}
                    style={{ ...inp, appearance: 'none' }}
                  >
                    <option value="">— select —</option>
                    {p.enum_values.map(v => <option key={v} value={v}>{v}</option>)}
                  </select>
                ) : (
                  <input
                    type={p.param_type === 'secret' ? 'password' : 'text'}
                    value={config[p.key] ?? ''}
                    onChange={e => setConfig(prev => ({ ...prev, [p.key]: e.target.value }))}
                    style={inp}
                    placeholder={p.default_value ?? ''}
                  />
                )}
              </div>
            ))}
          </>
        )}

        {app.params.length === 0 && (
          <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', marginTop: '12px' }}>
            This managed app has no configurable parameters.
          </p>
        )}

        <button onClick={save} disabled={saving} style={{
          marginTop: '8px', padding: '9px 20px', borderRadius: '10px', fontSize: '13px', fontWeight: 600,
          background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT,
          cursor: saving ? 'not-allowed' : 'pointer', opacity: saving ? 0.6 : 1,
        }}>
          {saving ? 'Saving…' : binding ? 'Save Changes' : 'Activate'}
        </button>
        {msg && (
          <p style={{ fontSize: '12px', color: msg === 'Saved' ? '#34d399' : '#f87171', marginTop: '8px' }}>{msg}</p>
        )}
      </div>
    </aside>
  );
}

// ── AppCard ────────────────────────────────────────────────────────────────────

function AppCard({
  app,
  binding,
  selected,
  onClick,
}: {
  app: ManagedApp;
  binding: ManagedAppBinding | undefined;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <div onClick={onClick} style={{
      background: 'var(--tm-card)', border: selected ? `1.5px solid ${ACCENT_BORDER}` : '1px solid var(--tm-border)',
      borderRadius: '12px', padding: '18px 20px', cursor: 'pointer', transition: 'border .15s',
      boxShadow: selected ? `0 0 0 3px ${ACCENT}22` : 'none',
    }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: '8px' }}>
        <div>
          <p style={{ fontWeight: 700, fontSize: '14px', color: 'var(--tm-card-text)', margin: '0 0 2px 0' }}>{app.name}</p>
          <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: 0, fontFamily: 'monospace' }}>{app.slug} v{app.version}</p>
        </div>
        <div style={{ display: 'flex', gap: '6px', flexShrink: 0 }}>
          {binding ? (
            <span style={{
              fontSize: '11px', fontWeight: 600, padding: '2px 8px', borderRadius: '10px',
              background: binding.enabled ? 'rgba(52,211,153,.12)' : 'rgba(148,163,184,.1)',
              color: binding.enabled ? '#34d399' : 'var(--tm-card-text-muted)',
              border: `1px solid ${binding.enabled ? 'rgba(52,211,153,.25)' : 'rgba(148,163,184,.2)'}`,
            }}>
              {binding.enabled ? 'active' : 'inactive'}
            </span>
          ) : (
            <span style={{
              fontSize: '11px', fontWeight: 600, padding: '2px 8px', borderRadius: '10px',
              background: 'rgba(248,113,113,.1)', color: '#f87171', border: '1px solid rgba(248,113,113,.2)',
            }}>
              not bound
            </span>
          )}
        </div>
      </div>
      {binding && (
        <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: 0 }}>
          {Object.keys(binding.config).length} param(s) configured
        </p>
      )}
    </div>
  );
}

// ── Page ───────────────────────────────────────────────────────────────────────

export default function ManagedAppsPage() {
  const [apps, setApps] = useState<ManagedApp[]>([]);
  const [tenants, setTenants] = useState<TenantRecord[]>([]);
  const [bindings, setBindings] = useState<ManagedAppBinding[]>([]);
  const [selectedTenant, setSelectedTenant] = useState<TenantRecord | null>(null);
  const [selectedApp, setSelectedApp] = useState<{ app: ManagedApp; detail: ManagedAppDetail } | null>(null);
  const [loading, setLoading] = useState(true);
  const [bindingsLoading, setBindingsLoading] = useState(false);
  const [error, setError] = useState('');

  const loadCatalog = useCallback(async () => {
    setLoading(true); setError('');
    try {
      const [appsData, tenantsData] = await Promise.all([
        themApi.listManagedApps(),
        themApi.listTenants(),
      ]);
      setApps(appsData ?? []);
      setTenants(tenantsData ?? []);
      if (tenantsData && tenantsData.length > 0 && !selectedTenant) {
        setSelectedTenant(tenantsData[0]);
      }
    } catch (e) { setError((e as Error).message || 'Failed to load'); }
    finally { setLoading(false); }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const loadBindings = useCallback(async (tenant: TenantRecord) => {
    setBindingsLoading(true);
    try {
      const b = await themApi.listManagedAppBindings(tenant.id);
      setBindings(b ?? []);
    } catch { setBindings([]); }
    finally { setBindingsLoading(false); }
  }, []);

  useEffect(() => { loadCatalog(); }, [loadCatalog]);

  useEffect(() => {
    if (selectedTenant) loadBindings(selectedTenant);
  }, [selectedTenant, loadBindings]);

  async function openApp(app: ManagedApp) {
    try {
      const detail = await themApi.getManagedApp(app.id);
      setSelectedApp({ app, detail });
    } catch { /* ignore */ }
  }

  function handleSaved(b: ManagedAppBinding) {
    setBindings(prev => {
      const idx = prev.findIndex(x => x.app_id === b.app_id);
      if (idx >= 0) {
        const next = [...prev];
        next[idx] = b;
        return next;
      }
      return [...prev, b];
    });
  }

  const tenantSelectStyle: React.CSSProperties = {
    padding: '7px 12px', borderRadius: '8px', fontSize: '13px', fontWeight: 500,
    background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)',
    color: 'var(--tm-card-text)', cursor: 'pointer', outline: 'none',
    appearance: 'none', minWidth: '200px',
  };

  return (
    <>
      <Sidebar />
      <main style={{ marginLeft: '260px', height: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--tm-bg)' }}>
        <header style={{ padding: '24px 32px 16px', flexShrink: 0, borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div>
              <h1 style={{ fontSize: '22px', fontWeight: 800, color: 'var(--tm-card-text)', margin: 0, display: 'flex', alignItems: 'center', gap: '10px' }}>
                <span className="material-symbols-outlined" style={{ fontSize: '22px', color: ACCENT }}>extension</span>
                Managed Apps
              </h1>
              <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: '4px 0 0 0' }}>
                Activate and configure platform-owned apps for each tenant
              </p>
            </div>
            {tenants.length > 0 && (
              <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                <span style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', fontWeight: 600 }}>Tenant</span>
                <select
                  value={selectedTenant?.id ?? ''}
                  onChange={e => {
                    const t = tenants.find(x => x.id === e.target.value);
                    if (t) { setSelectedTenant(t); setSelectedApp(null); }
                  }}
                  style={tenantSelectStyle}
                >
                  {tenants.map(t => (
                    <option key={t.id} value={t.id}>{t.display_name}</option>
                  ))}
                </select>
                {bindingsLoading && (
                  <span className="material-symbols-outlined spin" style={{ fontSize: '16px', color: ACCENT }}>sync</span>
                )}
              </div>
            )}
          </div>
        </header>

        <div
          style={{ flex: 1, overflowY: 'auto', padding: '24px 32px', paddingRight: selectedApp ? '460px' : '32px' }}
          className="custom-scrollbar"
        >
          {loading && (
            <div style={{ textAlign: 'center', padding: '80px 0', color: 'var(--tm-card-text-muted)' }}>
              <span className="material-symbols-outlined spin" style={{ fontSize: '32px', display: 'block', marginBottom: '12px', color: ACCENT }}>sync</span>
              Loading managed apps…
            </div>
          )}
          {!loading && error && (
            <div style={{ textAlign: 'center', padding: '80px 0' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '32px', color: '#f87171', display: 'block', marginBottom: '12px' }}>error</span>
              <p style={{ color: '#f87171', fontSize: '14px', margin: '0 0 16px 0' }}>{error}</p>
              <button onClick={loadCatalog} style={{ padding: '8px 16px', borderRadius: '8px', background: 'rgba(248,113,113,0.1)', border: '1px solid rgba(248,113,113,0.3)', color: '#f87171', cursor: 'pointer', fontSize: '13px' }}>Retry</button>
            </div>
          )}
          {!loading && !error && apps.length === 0 && (
            <div style={{ textAlign: 'center', padding: '80px 0', color: 'var(--tm-card-text-muted)' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '40px', display: 'block', marginBottom: '12px', opacity: 0.3, color: ACCENT }}>extension</span>
              <p style={{ fontSize: '15px', fontWeight: 600, margin: '0 0 6px 0' }}>No managed apps</p>
              <p style={{ fontSize: '13px', margin: 0 }}>Create managed apps via the API to get started.</p>
            </div>
          )}
          {!loading && !error && apps.length > 0 && (
            <>
              {selectedTenant && (
                <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', margin: '0 0 16px 0' }}>
                  Showing bindings for <span style={{ color: ACCENT, fontWeight: 600 }}>{selectedTenant.display_name}</span>
                  {' '}— click an app to configure its binding.
                </p>
              )}
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: '14px', alignContent: 'start' }}>
                {apps.map(app => (
                  <AppCard
                    key={app.id}
                    app={app}
                    binding={bindingForApp(bindings, app.id)}
                    selected={selectedApp?.app.id === app.id}
                    onClick={() => {
                      if (selectedApp?.app.id === app.id) { setSelectedApp(null); return; }
                      openApp(app);
                    }}
                  />
                ))}
              </div>
            </>
          )}
        </div>

        {selectedApp && selectedTenant && (
          <BindingPanel
            app={selectedApp.detail}
            tenant={selectedTenant}
            binding={bindingForApp(bindings, selectedApp.app.id)}
            onClose={() => setSelectedApp(null)}
            onSaved={b => { handleSaved(b); }}
          />
        )}
      </main>
    </>
  );
}
