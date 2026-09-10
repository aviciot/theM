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
import AuthGuard from '@/components/AuthGuard';
import { useRequireSuperAdmin } from '@/hooks/useRequireSuperAdmin';

const ACCENT = '#818cf8';
const GREEN  = '#34d399';
const MUTED  = 'rgba(148,163,184,0.6)';

// ── ConfigModal ────────────────────────────────────────────────────────────────
function ConfigModal({ app, tenant, binding, onClose, onSaved }: {
  app: ManagedAppDetail;
  tenant: TenantRecord;
  binding: ManagedAppBinding | undefined;
  onClose: () => void;
  onSaved: (b: ManagedAppBinding) => void;
}) {
  const [config, setConfig] = useState<Record<string, string>>(() => {
    const base: Record<string, string> = {};
    for (const p of app.params) base[p.key] = String((binding?.config ?? {})[p.key] ?? p.default_value ?? '');
    return base;
  });
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState('');

  async function save() {
    setSaving(true); setMsg('');
    try {
      const saved = await themApi.upsertManagedAppBinding(tenant.id, app.id, {
        config, enabled: binding?.enabled ?? true, app_version: app.version,
      });
      onSaved(saved); onClose();
    } catch { setMsg('Save failed'); }
    finally { setSaving(false); }
  }

  const inp: React.CSSProperties = {
    width: '100%', padding: '8px 12px', borderRadius: 8, fontSize: 13,
    background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)',
    color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box',
  };

  return (
    <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.55)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div style={{ background: 'var(--tm-sidebar)', borderRadius: 14, border: '1px solid rgba(255,255,255,.1)', padding: '28px 32px', width: 440, maxHeight: '80vh', overflowY: 'auto' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
          <div>
            <div style={{ fontWeight: 700, fontSize: 15, color: 'var(--tm-card-text)' }}>{app.name}</div>
            <div style={{ fontSize: 12, color: MUTED, marginTop: 2 }}>Configure for <span style={{ color: ACCENT }}>{tenant.display_name}</span></div>
          </div>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: MUTED, padding: 4 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 20 }}>close</span>
          </button>
        </div>

        {app.params.length === 0 ? (
          <p style={{ fontSize: 13, color: MUTED }}>This app has no configurable parameters.</p>
        ) : app.params.map(p => (
          <div key={p.key} style={{ marginBottom: 16 }}>
            <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-card-text-muted)', display: 'block', marginBottom: 5 }}>
              {p.label}{p.required && <span style={{ color: '#f87171', marginLeft: 4 }}>*</span>}
              {p.description && <span style={{ fontWeight: 400, marginLeft: 6, color: MUTED }}>— {p.description}</span>}
            </label>
            {p.param_type === 'enum' && p.enum_values?.length ? (
              <select value={config[p.key] ?? ''} onChange={e => setConfig(c => ({ ...c, [p.key]: e.target.value }))} style={{ ...inp, appearance: 'none' }}>
                <option value="">— select —</option>
                {p.enum_values.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            ) : (
              <input type={p.param_type === 'secret' ? 'password' : 'text'} value={config[p.key] ?? ''} placeholder={p.default_value ?? ''} onChange={e => setConfig(c => ({ ...c, [p.key]: e.target.value }))} style={inp} />
            )}
          </div>
        ))}

        {msg && <div style={{ fontSize: 12, color: '#f87171', marginBottom: 10 }}>{msg}</div>}
        <div style={{ display: 'flex', gap: 10, marginTop: 8 }}>
          <button onClick={save} disabled={saving} style={{ padding: '9px 22px', borderRadius: 8, fontSize: 13, fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT}66`, color: ACCENT, cursor: saving ? 'not-allowed' : 'pointer', opacity: saving ? 0.6 : 1 }}>
            {saving ? 'Saving…' : 'Save'}
          </button>
          <button onClick={onClose} style={{ padding: '9px 16px', borderRadius: 8, fontSize: 13, background: 'none', border: '1px solid rgba(255,255,255,.1)', color: MUTED, cursor: 'pointer' }}>Cancel</button>
        </div>
      </div>
    </div>
  );
}

// ── AssignmentPanel ────────────────────────────────────────────────────────────
function AssignmentPanel({ app, detail, tenants, bindingMap, onBindingChange }: {
  app: ManagedApp;
  detail: ManagedAppDetail | null;
  tenants: TenantRecord[];
  bindingMap: Record<string, ManagedAppBinding>; // keyed by tenantId
  onBindingChange: (tenantId: string, b: ManagedAppBinding) => void;
}) {
  const [addTarget, setAddTarget]   = useState('');
  const [assigning, setAssigning]   = useState(false);
  const [toggling, setToggling]     = useState<string | null>(null);
  const [configFor, setConfigFor]   = useState<{ tenant: TenantRecord } | null>(null);

  const assignedIds = new Set(Object.keys(bindingMap));
  const unassigned  = tenants.filter(t => !assignedIds.has(t.id));
  const assigned    = tenants.filter(t => assignedIds.has(t.id));

  async function assign() {
    if (!addTarget) return;
    setAssigning(true);
    try {
      const b = await themApi.upsertManagedAppBinding(addTarget, app.id, { config: {}, enabled: true, app_version: app.version });
      onBindingChange(addTarget, b);
      setAddTarget('');
    } catch { /* ignore */ }
    finally { setAssigning(false); }
  }

  async function toggleEnabled(tenant: TenantRecord) {
    const cur = bindingMap[tenant.id];
    if (!cur) return;
    setToggling(tenant.id);
    try {
      const b = await themApi.upsertManagedAppBinding(tenant.id, app.id, { config: cur.config, enabled: !cur.enabled, app_version: cur.app_version });
      onBindingChange(tenant.id, b);
    } catch { /* ignore */ }
    finally { setToggling(null); }
  }

  const rowStyle: React.CSSProperties = {
    display: 'grid', gridTemplateColumns: '1fr 90px 110px 120px', alignItems: 'center',
    padding: '12px 16px', borderBottom: '1px solid rgba(255,255,255,.05)',
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflowY: 'auto', padding: '32px 40px' }}>
      <div style={{ marginBottom: 28 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 12 }}>
          <h2 style={{ fontSize: 20, fontWeight: 800, color: 'var(--tm-card-text)', margin: 0 }}>{app.name}</h2>
          <span style={{ fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 20, background: `${ACCENT}18`, color: ACCENT, border: `1px solid ${ACCENT}44` }}>v{app.version}</span>
        </div>
        <div style={{ fontSize: 12, color: MUTED, marginTop: 4 }}>{app.slug} · {assigned.length} tenant{assigned.length !== 1 ? 's' : ''} assigned</div>
      </div>

      {/* Assigned tenants table */}
      <div style={{ background: 'var(--tm-card)', borderRadius: 12, border: '1px solid var(--tm-border)', marginBottom: 28, overflow: 'hidden' }}>
        <div style={{ ...rowStyle, background: 'rgba(255,255,255,.03)', borderBottom: '1px solid rgba(255,255,255,.08)' }}>
          <span style={{ fontSize: 11, fontWeight: 700, color: MUTED, textTransform: 'uppercase', letterSpacing: '0.07em' }}>Tenant</span>
          <span style={{ fontSize: 11, fontWeight: 700, color: MUTED, textTransform: 'uppercase', letterSpacing: '0.07em' }}>Status</span>
          <span style={{ fontSize: 11, fontWeight: 700, color: MUTED, textTransform: 'uppercase', letterSpacing: '0.07em' }}>Params</span>
          <span />
        </div>

        {assigned.length === 0 && (
          <div style={{ padding: '40px 20px', textAlign: 'center', color: MUTED, fontSize: 13 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 32, display: 'block', marginBottom: 8, opacity: 0.4 }}>group_off</span>
            No tenants assigned yet — use the form below to assign one.
          </div>
        )}

        {assigned.map(tenant => {
          const b = bindingMap[tenant.id];
          const paramCount = Object.keys(b.config).length;
          const isToggling = toggling === tenant.id;
          return (
            <div key={tenant.id} style={rowStyle}>
              <div>
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tm-card-text)' }}>{tenant.display_name}</div>
                <div style={{ fontSize: 11, color: MUTED, fontFamily: 'monospace' }}>{tenant.slug}</div>
              </div>

              {/* Enabled toggle */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <button
                  onClick={() => toggleEnabled(tenant)}
                  disabled={isToggling}
                  style={{ width: 38, height: 21, borderRadius: 11, border: 'none', cursor: isToggling ? 'not-allowed' : 'pointer', padding: 0, position: 'relative', background: b.enabled ? GREEN : 'rgba(255,255,255,.12)', transition: 'background .2s', opacity: isToggling ? 0.5 : 1, flexShrink: 0 }}
                >
                  <span style={{ position: 'absolute', top: 3, left: b.enabled ? 19 : 3, width: 15, height: 15, borderRadius: '50%', background: '#fff', transition: 'left .2s' }} />
                </button>
                <span style={{ fontSize: 11, color: b.enabled ? GREEN : MUTED }}>{b.enabled ? 'Active' : 'Off'}</span>
              </div>

              {/* Params badge */}
              <div>
                {paramCount > 0
                  ? <span style={{ fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 20, background: 'rgba(251,191,36,.1)', color: '#fbbf24', border: '1px solid rgba(251,191,36,.2)' }}>{paramCount} set</span>
                  : <span style={{ fontSize: 11, color: MUTED }}>—</span>}
              </div>

              {/* Actions */}
              <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                {detail && (
                  <button
                    onClick={() => setConfigFor({ tenant })}
                    style={{ padding: '5px 12px', borderRadius: 7, fontSize: 12, fontWeight: 600, background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.1)', color: 'var(--tm-card-text-muted)', cursor: 'pointer' }}
                  >Configure</button>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* Assign new tenant */}
      <div style={{ background: 'var(--tm-card)', borderRadius: 12, border: '1px solid var(--tm-border)', padding: '20px 24px' }}>
        <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--tm-card-text)', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 16, color: ACCENT }}>add_circle</span>
          Assign to tenant
        </div>
        {unassigned.length === 0 ? (
          <p style={{ fontSize: 13, color: MUTED, margin: 0 }}>All tenants already have access to this app.</p>
        ) : (
          <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
            <select
              value={addTarget}
              onChange={e => setAddTarget(e.target.value)}
              style={{ flex: 1, padding: '9px 12px', borderRadius: 8, fontSize: 13, background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)', color: 'var(--tm-card-text)', outline: 'none', appearance: 'none' }}
            >
              <option value="">Select tenant…</option>
              {unassigned.map(t => <option key={t.id} value={t.id}>{t.display_name} ({t.slug})</option>)}
            </select>
            <button
              onClick={assign}
              disabled={!addTarget || assigning}
              style={{ padding: '9px 22px', borderRadius: 8, fontSize: 13, fontWeight: 700, background: addTarget ? ACCENT : 'rgba(255,255,255,.07)', border: 'none', color: addTarget ? '#fff' : MUTED, cursor: (!addTarget || assigning) ? 'not-allowed' : 'pointer', opacity: assigning ? 0.6 : 1, whiteSpace: 'nowrap', transition: 'background .15s' }}
            >
              {assigning ? 'Assigning…' : 'Assign →'}
            </button>
          </div>
        )}
      </div>

      {/* Config modal */}
      {configFor && detail && (
        <ConfigModal
          app={detail}
          tenant={configFor.tenant}
          binding={bindingMap[configFor.tenant.id]}
          onClose={() => setConfigFor(null)}
          onSaved={b => { onBindingChange(configFor.tenant.id, b); setConfigFor(null); }}
        />
      )}
    </div>
  );
}

// ── Page ───────────────────────────────────────────────────────────────────────
export default function ManagedAppsPage() {
  useRequireSuperAdmin();
  const [apps, setApps]               = useState<ManagedApp[]>([]);
  const [tenants, setTenants]         = useState<TenantRecord[]>([]);
  const [selectedApp, setSelectedApp] = useState<ManagedApp | null>(null);
  const [detail, setDetail]           = useState<ManagedAppDetail | null>(null);
  const [bindingMap, setBindingMap]   = useState<Record<string, ManagedAppBinding>>({});
  const [loading, setLoading]         = useState(true);
  const [loadingBindings, setLoadingBindings] = useState(false);
  const [error, setError]             = useState('');

  useEffect(() => {
    Promise.all([themApi.listManagedApps(), themApi.listTenants()])
      .then(([a, t]) => { setApps(a ?? []); setTenants(t ?? []); })
      .catch(e => setError((e as Error).message || 'Failed to load'))
      .finally(() => setLoading(false));
  }, []);

  const selectApp = useCallback(async (app: ManagedApp, allTenants: TenantRecord[]) => {
    setSelectedApp(app); setDetail(null); setBindingMap({}); setLoadingBindings(true);
    try {
      const [appDetail, ...bindingResults] = await Promise.all([
        themApi.getManagedApp(app.id),
        ...allTenants.map(t => themApi.listManagedAppBindings(t.id).catch(() => [] as ManagedAppBinding[])),
      ]);
      setDetail(appDetail as ManagedAppDetail);
      const map: Record<string, ManagedAppBinding> = {};
      (bindingResults as ManagedAppBinding[][]).forEach((bindings, i) => {
        const b = bindings.find(b => b.app_id === app.id);
        if (b) map[allTenants[i].id] = b;
      });
      setBindingMap(map);
    } catch { /* ignore */ }
    finally { setLoadingBindings(false); }
  }, []);

  function handleBindingChange(tenantId: string, b: ManagedAppBinding) {
    setBindingMap(prev => ({ ...prev, [tenantId]: b }));
  }

  const assignedCount = (app: ManagedApp) => Object.values(bindingMap).filter(b => b.app_id === app.id).length;

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />

        {/* Left — app catalogue */}
        <div style={{ marginLeft: 260, width: 300, flexShrink: 0, borderRight: '1px solid rgba(255,255,255,.06)', height: '100vh', overflowY: 'auto', display: 'flex', flexDirection: 'column' }}>
          <div style={{ padding: '24px 20px 16px', borderBottom: '1px solid rgba(255,255,255,.06)', flexShrink: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 20, color: ACCENT }}>extension</span>
              <span style={{ fontWeight: 800, fontSize: 16, color: 'var(--tm-card-text)' }}>Managed Apps</span>
            </div>
            <div style={{ fontSize: 12, color: MUTED }}>Select an app to manage tenant access</div>
          </div>

          <div style={{ flex: 1, overflowY: 'auto', padding: '12px 12px' }}>
            {loading && <div style={{ textAlign: 'center', padding: '40px 0', color: MUTED, fontSize: 13 }}>Loading…</div>}
            {!loading && error && <div style={{ padding: '20px 8px', color: '#f87171', fontSize: 13 }}>{error}</div>}
            {!loading && !error && apps.length === 0 && (
              <div style={{ textAlign: 'center', padding: '40px 0', color: MUTED, fontSize: 13 }}>
                <span className="material-symbols-outlined" style={{ fontSize: 32, display: 'block', marginBottom: 8, opacity: 0.3 }}>extension_off</span>
                No managed apps yet
              </div>
            )}
            {apps.map(app => {
              const isSelected = selectedApp?.id === app.id;
              const count = isSelected ? Object.keys(bindingMap).length : null;
              return (
                <div
                  key={app.id}
                  onClick={() => selectApp(app, tenants)}
                  style={{
                    padding: '12px 14px', borderRadius: 10, marginBottom: 6, cursor: 'pointer',
                    background: isSelected ? `${ACCENT}14` : 'var(--tm-card)',
                    border: isSelected ? `1.5px solid ${ACCENT}55` : '1px solid var(--tm-border)',
                    borderLeft: isSelected ? `3px solid ${ACCENT}` : '1px solid var(--tm-border)',
                    transition: 'all .15s',
                  }}
                >
                  <div style={{ fontWeight: 700, fontSize: 13, color: 'var(--tm-card-text)', marginBottom: 3 }}>{app.name}</div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span style={{ fontSize: 10, fontFamily: 'monospace', color: MUTED }}>{app.slug}</span>
                    <span style={{ fontSize: 10, fontWeight: 600, padding: '1px 6px', borderRadius: 10, background: `${ACCENT}18`, color: ACCENT }}>v{app.version}</span>
                  </div>
                  {isSelected && (
                    <div style={{ fontSize: 11, color: MUTED, marginTop: 5 }}>
                      {loadingBindings ? 'Loading…' : `${count} tenant${count !== 1 ? 's' : ''} assigned`}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>

        {/* Right — assignment panel */}
        <div style={{ flex: 1, height: '100vh', overflowY: 'auto', display: 'flex', flexDirection: 'column' }}>
          {!selectedApp ? (
            <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column', color: MUTED }}>
              <span className="material-symbols-outlined" style={{ fontSize: 48, marginBottom: 16, opacity: 0.25 }}>arrow_back</span>
              <div style={{ fontSize: 15, fontWeight: 600 }}>Select an app to manage its tenant assignments</div>
            </div>
          ) : loadingBindings ? (
            <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: MUTED }}>
              <span className="material-symbols-outlined spin" style={{ fontSize: 28, marginRight: 10, color: ACCENT }}>sync</span>
              <span style={{ fontSize: 14 }}>Loading assignments…</span>
            </div>
          ) : (
            <AssignmentPanel
              app={selectedApp}
              detail={detail}
              tenants={tenants}
              bindingMap={bindingMap}
              onBindingChange={handleBindingChange}
            />
          )}
        </div>
      </div>
    </AuthGuard>
  );
}
