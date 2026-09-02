'use client';
import { useEffect, useState, useCallback } from 'react';
import { themApi, type TenantRecord, type IDPConfig, type TenantQuota, type QuotaPlan } from '@/lib/api';
import Sidebar from '@/components/Sidebar';

const ACCENT = '#818cf8';
const ACCENT_BORDER = 'rgba(129,140,248,0.4)';

// ── Tenant card ────────────────────────────────────────────────────────────────

function TenantCard({ tenant, selected, onClick }: { tenant: TenantRecord; selected: boolean; onClick: () => void }) {
  return (
    <div onClick={onClick} style={{
      background: 'var(--tm-card)', border: selected ? `1.5px solid ${ACCENT_BORDER}` : '1px solid var(--tm-border)',
      borderRadius: '12px', padding: '20px', cursor: 'pointer', transition: 'border .15s',
      boxShadow: selected ? `0 0 0 3px ${ACCENT}22` : 'none',
    }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: '8px' }}>
        <div>
          <p style={{ fontWeight: 700, fontSize: '15px', color: 'var(--tm-card-text)', margin: '0 0 2px 0' }}>{tenant.display_name}</p>
          <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', margin: 0, fontFamily: 'monospace' }}>{tenant.slug}</p>
        </div>
        <div style={{ display: 'flex', gap: '6px' }}>
          <span style={{ fontSize: '11px', fontWeight: 600, padding: '2px 8px', borderRadius: '10px',
            background: tenant.enabled ? 'rgba(52,211,153,.12)' : 'rgba(248,113,113,.1)',
            color: tenant.enabled ? '#34d399' : '#f87171', border: `1px solid ${tenant.enabled ? 'rgba(52,211,153,.25)' : 'rgba(248,113,113,.2)'}`,
          }}>
            {tenant.enabled ? 'enabled' : 'disabled'}
          </span>
          {tenant.idp_configured && (
            <span style={{ fontSize: '11px', fontWeight: 600, padding: '2px 8px', borderRadius: '10px',
              background: `${ACCENT}18`, color: ACCENT, border: `1px solid ${ACCENT_BORDER}`,
            }}>IdP</span>
          )}
        </div>
      </div>
      <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: 0 }}>
        Created {new Date(tenant.created_at).toLocaleDateString()}
      </p>
    </div>
  );
}

// ── Side panel ─────────────────────────────────────────────────────────────────

const PLANS: QuotaPlan[] = ['trial', 'starter', 'pro', 'enterprise'];

function TenantPanel({ tenant, onClose, onPatched }: {
  tenant: TenantRecord;
  onClose: () => void;
  onPatched: (t: TenantRecord) => void;
}) {
  const [tab, setTab] = useState<'general' | 'idp' | 'quota'>('general');
  const [displayName, setDisplayName] = useState(tenant.display_name);
  const [enabled, setEnabled] = useState(tenant.enabled);
  const [genSaving, setGenSaving] = useState(false);
  const [genMsg, setGenMsg] = useState('');

  const [discoveryURL, setDiscoveryURL] = useState('');
  const [clientID, setClientID] = useState('');
  const [clientSecret, setClientSecret] = useState('');
  const [redirectURI, setRedirectURI] = useState('');
  const [idpSaving, setIdpSaving] = useState(false);
  const [idpMsg, setIdpMsg] = useState('');

  const emptyQuota = (): Omit<TenantQuota, 'tenant_id'> => ({
    plan: 'trial', max_agents: null, max_apps: null, max_mcp_servers: null,
    max_concurrent_runs: null, max_users: null, monthly_llm_tokens: null,
    monthly_runs: null, api_requests_per_minute: null, runs_per_minute: null,
  });
  const [quota, setQuota] = useState<Omit<TenantQuota, 'tenant_id'>>(emptyQuota());
  const [quotaLoading, setQuotaLoading] = useState(false);
  const [quotaSaving, setQuotaSaving] = useState(false);
  const [quotaMsg, setQuotaMsg] = useState('');

  useEffect(() => {
    setDisplayName(tenant.display_name);
    setEnabled(tenant.enabled);
    setDiscoveryURL('');
    setClientID('');
    setClientSecret('');
    setRedirectURI('');
    setGenMsg('');
    setIdpMsg('');
    setQuota(emptyQuota());
    setQuotaMsg('');
  }, [tenant.id]);

  useEffect(() => {
    if (tab !== 'quota') return;
    setQuotaLoading(true);
    themApi.getTenantQuota(tenant.id)
      .then(q => setQuota({ plan: q.plan, max_agents: q.max_agents, max_apps: q.max_apps, max_mcp_servers: q.max_mcp_servers, max_concurrent_runs: q.max_concurrent_runs, max_users: q.max_users, monthly_llm_tokens: q.monthly_llm_tokens, monthly_runs: q.monthly_runs, api_requests_per_minute: q.api_requests_per_minute, runs_per_minute: q.runs_per_minute }))
      .catch(() => { /* quota row may not exist yet — leave form empty */ })
      .finally(() => setQuotaLoading(false));
  }, [tab, tenant.id]);

  async function saveGeneral() {
    setGenSaving(true); setGenMsg('');
    try {
      const t = await themApi.patchTenant(tenant.id, { display_name: displayName, enabled });
      onPatched(t);
      setGenMsg('Saved');
    } catch { setGenMsg('Error saving'); }
    finally { setGenSaving(false); }
  }

  async function saveIDP() {
    if (!discoveryURL || !clientID || !redirectURI) { setIdpMsg('discovery_url, client_id and redirect_uri are required'); return; }
    setIdpSaving(true); setIdpMsg('');
    const cfg: IDPConfig = { discovery_url: discoveryURL, client_id: clientID, redirect_uri: redirectURI };
    if (clientSecret) cfg.client_secret = clientSecret;
    try {
      const t = await themApi.patchTenant(tenant.id, { idp_config: cfg });
      onPatched(t);
      setIdpMsg('IdP config saved');
      setClientSecret('');
    } catch { setIdpMsg('Error saving IdP config'); }
    finally { setIdpSaving(false); }
  }

  async function clearIDP() {
    setIdpSaving(true); setIdpMsg('');
    try {
      const t = await themApi.patchTenant(tenant.id, { idp_config: null });
      onPatched(t);
      setIdpMsg('IdP config cleared');
    } catch { setIdpMsg('Error clearing IdP config'); }
    finally { setIdpSaving(false); }
  }

  async function saveQuota() {
    setQuotaSaving(true); setQuotaMsg('');
    try {
      const saved = await themApi.upsertTenantQuota(tenant.id, quota);
      setQuota({ plan: saved.plan, max_agents: saved.max_agents, max_apps: saved.max_apps, max_mcp_servers: saved.max_mcp_servers, max_concurrent_runs: saved.max_concurrent_runs, max_users: saved.max_users, monthly_llm_tokens: saved.monthly_llm_tokens, monthly_runs: saved.monthly_runs, api_requests_per_minute: saved.api_requests_per_minute, runs_per_minute: saved.runs_per_minute });
      setQuotaMsg('Saved');
    } catch { setQuotaMsg('Error saving quotas'); }
    finally { setQuotaSaving(false); }
  }

  function numField(label: string, key: keyof Omit<TenantQuota, 'tenant_id' | 'plan'>) {
    const val = quota[key] as number | null;
    return (
      <div style={{ marginBottom: '14px' }}>
        <label style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', display: 'block', marginBottom: '5px' }}>
          {label} <span style={{ fontWeight: 400 }}>(blank = unlimited)</span>
        </label>
        <input
          type="number" min={1}
          value={val ?? ''}
          onChange={e => setQuota(q => ({ ...q, [key]: e.target.value === '' ? null : parseInt(e.target.value, 10) }))}
          style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', fontSize: '13px', background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)', color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box' as const }}
        />
      </div>
    );
  }

  const inp: React.CSSProperties = {
    width: '100%', padding: '8px 12px', borderRadius: '8px', fontSize: '13px',
    background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)',
    color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box',
  };
  const lbl: React.CSSProperties = { fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', marginBottom: '5px', display: 'block' };
  const row: React.CSSProperties = { marginBottom: '16px' };

  return (
    <aside style={{
      position: 'fixed', right: 0, top: 0, bottom: 0, width: '400px', zIndex: 50,
      background: 'var(--tm-sidebar)', borderLeft: '1px solid rgba(255,255,255,.08)',
      display: 'flex', flexDirection: 'column', overflowY: 'auto',
    }} className="custom-scrollbar">
      <div style={{ padding: '20px 24px 16px', borderBottom: '1px solid rgba(255,255,255,.06)', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div>
          <p style={{ fontWeight: 700, fontSize: '15px', color: 'var(--tm-card-text)', margin: '0 0 2px 0' }}>{tenant.display_name}</p>
          <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', margin: 0, fontFamily: 'monospace' }}>{tenant.slug}</p>
        </div>
        <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tm-card-text-muted)', padding: '4px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '20px' }}>close</span>
        </button>
      </div>

      <div style={{ display: 'flex', gap: '4px', padding: '12px 24px 0', borderBottom: '1px solid rgba(255,255,255,.06)' }}>
        {(['general', 'idp', 'quota'] as const).map(t => (
          <button key={t} onClick={() => setTab(t)} style={{
            padding: '7px 14px', borderRadius: '8px 8px 0 0', fontSize: '13px', fontWeight: tab === t ? 600 : 400,
            background: tab === t ? 'rgba(255,255,255,.07)' : 'transparent',
            border: 'none', color: tab === t ? 'var(--tm-card-text)' : 'var(--tm-card-text-muted)', cursor: 'pointer',
          }}>
            {t === 'general' ? 'General' : t === 'idp' ? 'Identity Provider' : 'Quotas'}
          </button>
        ))}
      </div>

      <div style={{ padding: '20px 24px', flex: 1 }}>
        {tab === 'general' && (
          <>
            <div style={row}>
              <label style={lbl}>Display Name</label>
              <input value={displayName} onChange={e => setDisplayName(e.target.value)} style={inp} />
            </div>
            <div style={{ ...row, display: 'flex', alignItems: 'center', gap: '10px' }}>
              <label style={{ ...lbl, margin: 0 }}>Enabled</label>
              <button onClick={() => setEnabled(e => !e)} style={{
                width: '40px', height: '22px', borderRadius: '11px', border: 'none', cursor: 'pointer', padding: 0, position: 'relative',
                background: enabled ? ACCENT : 'rgba(255,255,255,.15)', transition: 'background .2s',
              }}>
                <span style={{ position: 'absolute', top: '3px', left: enabled ? '21px' : '3px', width: '16px', height: '16px', borderRadius: '50%', background: '#fff', transition: 'left .2s' }} />
              </button>
            </div>
            <button onClick={saveGeneral} disabled={genSaving} style={{ padding: '9px 20px', borderRadius: '10px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: genSaving ? 'not-allowed' : 'pointer', opacity: genSaving ? 0.6 : 1 }}>
              {genSaving ? 'Saving…' : 'Save'}
            </button>
            {genMsg && <p style={{ fontSize: '12px', color: genMsg === 'Saved' ? '#34d399' : '#f87171', marginTop: '8px' }}>{genMsg}</p>}
          </>
        )}

        {tab === 'idp' && (
          <>
            <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginTop: 0, marginBottom: '16px' }}>
              Configure an OIDC identity provider for SSO login to this tenant.
              {tenant.idp_configured && <span style={{ color: '#34d399', marginLeft: '6px' }}>✓ IdP configured</span>}
            </p>
            {['Discovery URL', 'Client ID', 'Redirect URI'].map((label, i) => {
              const vals = [discoveryURL, clientID, redirectURI];
              const setters = [setDiscoveryURL, setClientID, setRedirectURI];
              return (
                <div key={label} style={row}>
                  <label style={lbl}>{label}</label>
                  <input value={vals[i]} onChange={e => setters[i](e.target.value)} style={inp} placeholder={i === 0 ? 'https://accounts.google.com' : i === 2 ? 'https://yourapp.com/auth/oidc/callback' : ''} />
                </div>
              );
            })}
            <div style={row}>
              <label style={lbl}>Client Secret <span style={{ fontWeight: 400, color: 'var(--tm-card-text-muted)' }}>(write-only, leave blank to keep existing)</span></label>
              <input type="password" value={clientSecret} onChange={e => setClientSecret(e.target.value)} style={inp} placeholder="••••••••" />
            </div>
            <div style={{ display: 'flex', gap: '10px', flexWrap: 'wrap' }}>
              <button onClick={saveIDP} disabled={idpSaving} style={{ padding: '9px 20px', borderRadius: '10px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: idpSaving ? 'not-allowed' : 'pointer', opacity: idpSaving ? 0.6 : 1 }}>
                {idpSaving ? 'Saving…' : 'Save IdP Config'}
              </button>
              {tenant.idp_configured && (
                <button onClick={clearIDP} disabled={idpSaving} style={{ padding: '9px 20px', borderRadius: '10px', fontSize: '13px', fontWeight: 600, background: 'rgba(248,113,113,.1)', border: '1px solid rgba(248,113,113,.25)', color: '#f87171', cursor: idpSaving ? 'not-allowed' : 'pointer', opacity: idpSaving ? 0.6 : 1 }}>
                  Clear
                </button>
              )}
            </div>
            {idpMsg && <p style={{ fontSize: '12px', color: idpMsg.startsWith('Error') ? '#f87171' : '#34d399', marginTop: '8px' }}>{idpMsg}</p>}
          </>
        )}

        {tab === 'quota' && (
          <>
            <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginTop: 0, marginBottom: '16px' }}>
              Resource caps and rate limits for this tenant. Leave a field blank to apply no limit.
            </p>
            {quotaLoading ? (
              <p style={{ color: 'var(--tm-card-text-muted)', fontSize: '13px' }}>Loading…</p>
            ) : (
              <>
                <div style={{ marginBottom: '14px' }}>
                  <label style={lbl}>Plan</label>
                  <select value={quota.plan} onChange={e => setQuota(q => ({ ...q, plan: e.target.value as QuotaPlan }))} style={{ ...inp, cursor: 'pointer' }}>
                    {PLANS.map(p => <option key={p} value={p}>{p}</option>)}
                  </select>
                </div>
                {numField('Max agents', 'max_agents')}
                {numField('Max applications', 'max_apps')}
                {numField('Max MCP servers', 'max_mcp_servers')}
                {numField('Max concurrent runs', 'max_concurrent_runs')}
                {numField('Max users', 'max_users')}
                {numField('Monthly LLM tokens', 'monthly_llm_tokens')}
                {numField('Monthly runs', 'monthly_runs')}
                {numField('API requests / minute', 'api_requests_per_minute')}
                {numField('Runs / minute', 'runs_per_minute')}
                <button onClick={saveQuota} disabled={quotaSaving} style={{ padding: '9px 20px', borderRadius: '10px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: quotaSaving ? 'not-allowed' : 'pointer', opacity: quotaSaving ? 0.6 : 1 }}>
                  {quotaSaving ? 'Saving…' : 'Save Quotas'}
                </button>
                {quotaMsg && <p style={{ fontSize: '12px', color: quotaMsg === 'Saved' ? '#34d399' : '#f87171', marginTop: '8px' }}>{quotaMsg}</p>}
              </>
            )}
          </>
        )}
      </div>
    </aside>
  );
}

// ── Create modal ───────────────────────────────────────────────────────────────

function CreateModal({ onClose, onCreated }: { onClose: () => void; onCreated: (t: TenantRecord) => void }) {
  const [slug, setSlug] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!slug || !displayName) { setErr('Both fields required'); return; }
    setSaving(true); setErr('');
    try {
      const t = await themApi.createTenant({ slug, display_name: displayName });
      onCreated(t);
    } catch (ex) { setErr((ex as Error).message || 'Error creating tenant'); }
    finally { setSaving(false); }
  }

  const inp: React.CSSProperties = { width: '100%', padding: '8px 12px', borderRadius: '8px', fontSize: '13px', background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)', color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box' };

  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 60, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,.55)' }} onClick={onClose}>
      <div style={{ background: 'var(--tm-sidebar)', borderRadius: '16px', padding: '28px', width: '400px', border: '1px solid rgba(255,255,255,.1)' }} onClick={e => e.stopPropagation()}>
        <h2 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-card-text)', margin: '0 0 20px 0' }}>New Tenant</h2>
        <form onSubmit={submit}>
          <div style={{ marginBottom: '14px' }}>
            <label style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', display: 'block', marginBottom: '5px' }}>Slug</label>
            <input value={slug} onChange={e => setSlug(e.target.value)} style={inp} placeholder="acme-corp" />
            <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: '4px 0 0 0' }}>Lowercase letters, numbers, hyphens, underscores (max 64 chars)</p>
          </div>
          <div style={{ marginBottom: '20px' }}>
            <label style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', display: 'block', marginBottom: '5px' }}>Display Name</label>
            <input value={displayName} onChange={e => setDisplayName(e.target.value)} style={inp} placeholder="Acme Corp" />
          </div>
          {err && <p style={{ fontSize: '12px', color: '#f87171', margin: '0 0 14px 0' }}>{err}</p>}
          <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
            <button type="button" onClick={onClose} style={{ padding: '8px 16px', borderRadius: '8px', fontSize: '13px', background: 'transparent', border: '1px solid rgba(255,255,255,.12)', color: 'var(--tm-card-text-muted)', cursor: 'pointer' }}>Cancel</button>
            <button type="submit" disabled={saving} style={{ padding: '8px 18px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: saving ? 'not-allowed' : 'pointer', opacity: saving ? 0.6 : 1 }}>
              {saving ? 'Creating…' : 'Create Tenant'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ── Page ───────────────────────────────────────────────────────────────────────

export default function TenantsPage() {
  const [tenants, setTenants] = useState<TenantRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<TenantRecord | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const load = useCallback(async () => {
    setLoading(true); setError('');
    try { setTenants(await themApi.listTenants() ?? []); }
    catch (e) { setError((e as Error).message || 'Failed to load tenants'); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  function handlePatched(updated: TenantRecord) {
    setTenants(prev => prev.map(t => t.id === updated.id ? updated : t));
    setSelected(updated);
  }
  function handleCreated(created: TenantRecord) {
    setTenants(prev => [...prev, created]);
    setSelected(created);
    setShowCreate(false);
  }

  return (
    <>
      <Sidebar />
      <main style={{ marginLeft: '260px', height: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--tm-bg)' }}>
        <header style={{ padding: '24px 32px 16px', flexShrink: 0, borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div>
              <h1 style={{ fontSize: '22px', fontWeight: 800, color: 'var(--tm-card-text)', margin: 0, display: 'flex', alignItems: 'center', gap: '10px' }}>
                <span className="material-symbols-outlined" style={{ fontSize: '22px', color: ACCENT }}>domain</span>
                Tenants
              </h1>
              <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: '4px 0 0 0' }}>
                Platform tenants — each with isolated applications, agents, and identity providers
              </p>
            </div>
            <button onClick={() => setShowCreate(true)} style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '9px 18px', borderRadius: '10px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: 'pointer' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>add</span>
              New Tenant
            </button>
          </div>
        </header>

        <div style={{ flex: 1, overflowY: 'auto', padding: '24px 32px', paddingRight: selected ? '440px' : '32px' }} className="custom-scrollbar">
          {loading && (
            <div style={{ textAlign: 'center', padding: '80px 0', color: 'var(--tm-card-text-muted)' }}>
              <span className="material-symbols-outlined spin" style={{ fontSize: '32px', display: 'block', marginBottom: '12px', color: ACCENT }}>sync</span>
              Loading tenants…
            </div>
          )}
          {!loading && error && (
            <div style={{ textAlign: 'center', padding: '80px 0' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '32px', color: '#f87171', display: 'block', marginBottom: '12px' }}>error</span>
              <p style={{ color: '#f87171', fontSize: '14px', margin: '0 0 16px 0' }}>{error}</p>
              <button onClick={load} style={{ padding: '8px 16px', borderRadius: '8px', background: 'rgba(248,113,113,0.1)', border: '1px solid rgba(248,113,113,0.3)', color: '#f87171', cursor: 'pointer', fontSize: '13px' }}>Retry</button>
            </div>
          )}
          {!loading && !error && tenants.length === 0 && (
            <div style={{ textAlign: 'center', padding: '80px 0', color: 'var(--tm-card-text-muted)' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '40px', display: 'block', marginBottom: '12px', opacity: 0.3, color: ACCENT }}>domain</span>
              <p style={{ fontSize: '15px', fontWeight: 600, margin: '0 0 6px 0' }}>No tenants yet</p>
              <p style={{ fontSize: '13px', margin: 0 }}>Create your first tenant to get started with multi-tenancy.</p>
            </div>
          )}
          {!loading && !error && tenants.length > 0 && (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: '16px', alignContent: 'start' }}>
              {tenants.map(t => (
                <TenantCard key={t.id} tenant={t} selected={selected?.id === t.id} onClick={() => setSelected(prev => prev?.id === t.id ? null : t)} />
              ))}
            </div>
          )}
        </div>

        {selected && <TenantPanel tenant={selected} onClose={() => setSelected(null)} onPatched={handlePatched} />}
        {showCreate && <CreateModal onClose={() => setShowCreate(false)} onCreated={handleCreated} />}
      </main>
    </>
  );
}
