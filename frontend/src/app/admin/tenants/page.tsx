'use client';
import { useEffect, useState, useCallback } from 'react';
import { themApi, type TenantRecord, type TenantPatch, type IDPConfig, type TenantQuota, type QuotaPlan, type TenantMember, type GroupMapping, type GroupMappingInput } from '@/lib/api';
import Sidebar from '@/components/Sidebar';
import { useRequireSuperAdmin } from '@/hooks/useRequireSuperAdmin';
import ProvisionWizard from './ProvisionWizard';

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

function TenantPanel({ tenant, onClose, onPatched, onDeleted }: {
  tenant: TenantRecord;
  onClose: () => void;
  onPatched: (t: TenantRecord) => void;
  onDeleted: (id: string) => void;
}) {
  const [tab, setTab] = useState<'general' | 'idp' | 'quota' | 'members' | 'groups'>('general');
  const [displayName, setDisplayName] = useState(tenant.display_name);
  const [enabled, setEnabled] = useState(tenant.enabled);
  const [emailDomain, setEmailDomain] = useState(tenant.email_domain ?? '');
  const [genSaving, setGenSaving] = useState(false);
  const [genMsg, setGenMsg] = useState('');
  const [deleteConfirm, setDeleteConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);

  const [discoveryURL, setDiscoveryURL] = useState('');
  const [clientID, setClientID] = useState('');
  const [clientSecret, setClientSecret] = useState('');
  const [redirectURI, setRedirectURI] = useState('');
  const [idpSaving, setIdpSaving] = useState(false);
  const [idpMsg, setIdpMsg] = useState('');
  const [discoveryResult, setDiscoveryResult] = useState<{ issuer: string; auth: string; token: string } | null>(null);
  const [discoveryErr, setDiscoveryErr] = useState('');

  const emptyQuota = (): Omit<TenantQuota, 'tenant_id'> => ({
    plan: 'trial', max_agents: null, max_apps: null, max_mcp_servers: null,
    max_concurrent_runs: null, max_users: null, monthly_llm_tokens: null,
    monthly_runs: null, api_requests_per_minute: null, runs_per_minute: null,
  });
  const [quota, setQuota] = useState<Omit<TenantQuota, 'tenant_id'>>(emptyQuota());
  const [quotaLoading, setQuotaLoading] = useState(false);
  const [quotaSaving, setQuotaSaving] = useState(false);
  const [quotaMsg, setQuotaMsg] = useState('');
  const [members, setMembers] = useState<TenantMember[]>([]);
  const [membersLoading, setMembersLoading] = useState(false);
  const [groupMappings, setGroupMappings] = useState<GroupMapping[]>([]);
  const [groupsLoading, setGroupsLoading] = useState(false);
  const [groupsMsg, setGroupsMsg] = useState('');
  const [newGroupClaim, setNewGroupClaim] = useState('');
  const [newGroupRole, setNewGroupRole] = useState('viewer');
  const [newGroupPriority, setNewGroupPriority] = useState(10);
  const [groupsSaving, setGroupsSaving] = useState(false);

  useEffect(() => {
    setDisplayName(tenant.display_name);
    setEnabled(tenant.enabled);
    setEmailDomain(tenant.email_domain ?? '');
    setDiscoveryURL('');
    setClientID('');
    setClientSecret('');
    setRedirectURI('');
    setGenMsg('');
    setIdpMsg('');
    setDiscoveryResult(null);
    setDiscoveryErr('');
    setQuota(emptyQuota());
    setQuotaMsg('');
    setMembers([]);
    setGroupMappings([]);
    setGroupsMsg('');
    setNewGroupClaim('');
    setNewGroupRole('viewer');
    setNewGroupPriority(10);
    setDeleteConfirm(false);
    // Fetch full detail to get IdP config fields (list endpoint omits them)
    themApi.getTenant(tenant.id).then(detail => {
      if (detail.idp_config) {
        setDiscoveryURL(detail.idp_config.discovery_url ?? '');
        setClientID(detail.idp_config.client_id ?? '');
        setRedirectURI(detail.idp_config.redirect_uri ?? '');
      }
    }).catch(() => { /* ignore — panel still works, fields just stay blank */ });
  }, [tenant.id]);

  async function deleteTenantHandler() {
    setDeleting(true); setGenMsg('');
    try {
      await themApi.deleteTenant(tenant.id);
      onDeleted(tenant.id);
    } catch (e) { setGenMsg((e as Error).message || 'Error deleting tenant'); }
    finally { setDeleting(false); setDeleteConfirm(false); }
  }

  useEffect(() => {
    if (tab !== 'members') return;
    setMembersLoading(true);
    themApi.listTenantMembers(tenant.id)
      .then(m => setMembers(Array.isArray(m) ? m : []))
      .catch(() => setMembers([]))
      .finally(() => setMembersLoading(false));
  }, [tab, tenant.id]);

  useEffect(() => {
    if (tab !== 'groups') return;
    setGroupsLoading(true);
    themApi.listGroupMappings(tenant.id)
      .then(m => setGroupMappings(Array.isArray(m) ? m : []))
      .catch(() => setGroupMappings([]))
      .finally(() => setGroupsLoading(false));
  }, [tab, tenant.id]);

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
      const patch: TenantPatch = {
        display_name: displayName,
        enabled,
        email_domain: emailDomain.trim() === '' ? null : emailDomain.trim().toLowerCase(),
      };
      const t = await themApi.patchTenant(tenant.id, patch);
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

  async function testDiscovery() {
    const url = discoveryURL.trim();
    if (!url) { setDiscoveryErr('Enter a Discovery URL first'); return; }
    setDiscoveryResult(null); setDiscoveryErr('');
    try {
      // Internal Docker hostnames (them-keycloak:8080) are unreachable from the browser.
      // Rewrite to go through Traefik which proxies /auth/keycloak → them-keycloak.
      const browserUrl = url
        .replace('http://them-keycloak:8080/auth/keycloak', '/auth/keycloak')
        .replace('http://them-keycloak:8080', '/auth/keycloak')
        .replace(/\/$/, '') + '/.well-known/openid-configuration';
      const res = await fetch(browserUrl);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const raw = await res.json();
      setDiscoveryResult({
        issuer: raw.issuer ?? '—',
        auth: raw.authorization_endpoint ?? '—',
        token: raw.token_endpoint ?? '—',
      });
    } catch (e) {
      setDiscoveryErr('Could not reach discovery URL: ' + (e as Error).message);
    }
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

  async function saveGroupMapping() {
    if (!newGroupClaim.trim()) { setGroupsMsg('Group claim is required'); return; }
    setGroupsSaving(true); setGroupsMsg('');
    const input: GroupMappingInput = { group_claim: newGroupClaim.trim(), role: newGroupRole, priority: newGroupPriority };
    try {
      const saved = await themApi.upsertGroupMapping(tenant.id, input);
      setGroupMappings(prev => {
        const idx = prev.findIndex(m => m.group_claim === saved.group_claim);
        return idx >= 0 ? prev.map((m, i) => i === idx ? saved : m) : [...prev, saved];
      });
      setNewGroupClaim('');
      setNewGroupPriority(10);
      setGroupsMsg('Saved');
    } catch { setGroupsMsg('Error saving mapping'); }
    finally { setGroupsSaving(false); }
  }

  async function deleteGroupMappingHandler(mappingId: string) {
    setGroupsMsg('');
    try {
      await themApi.deleteGroupMapping(tenant.id, mappingId);
      setGroupMappings(prev => prev.filter(m => m.id !== mappingId));
    } catch { setGroupsMsg('Error deleting mapping'); }
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

      <div style={{ display: 'flex', gap: '4px', padding: '12px 24px 0', borderBottom: '1px solid rgba(255,255,255,.06)', flexWrap: 'wrap' }}>
        {(['general', 'idp', 'quota', 'members', 'groups'] as const).map(t => (
          <button key={t} onClick={() => setTab(t)} style={{
            padding: '7px 14px', borderRadius: '8px 8px 0 0', fontSize: '13px', fontWeight: tab === t ? 600 : 400,
            background: tab === t ? 'rgba(255,255,255,.07)' : 'transparent',
            border: 'none', color: tab === t ? 'var(--tm-card-text)' : 'var(--tm-card-text-muted)', cursor: 'pointer',
          }}>
            {t === 'general' ? 'General' : t === 'idp' ? 'Identity Provider' : t === 'quota' ? 'Quotas' : t === 'members' ? 'Members' : 'Group Mappings'}
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
            <div style={row}>
              <label style={lbl}>Email Domain <span style={{ fontWeight: 400, color: 'var(--tm-card-text-muted)' }}>(for SSO routing, e.g. acme.com)</span></label>
              <input value={emailDomain} onChange={e => setEmailDomain(e.target.value)} style={inp} placeholder="acme.com (leave blank to clear)" />
            </div>
            <button onClick={saveGeneral} disabled={genSaving} style={{ padding: '9px 20px', borderRadius: '10px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: genSaving ? 'not-allowed' : 'pointer', opacity: genSaving ? 0.6 : 1 }}>
              {genSaving ? 'Saving…' : 'Save'}
            </button>
            {genMsg && <p style={{ fontSize: '12px', color: genMsg === 'Saved' ? '#34d399' : '#f87171', marginTop: '8px' }}>{genMsg}</p>}

            {!tenant.is_bootstrap && (
              <div style={{ marginTop: '32px', paddingTop: '20px', borderTop: '1px solid rgba(248,113,113,.15)' }}>
                <p style={{ fontSize: '12px', fontWeight: 600, color: '#f87171', margin: '0 0 10px 0' }}>Danger Zone</p>
                {!deleteConfirm ? (
                  <button onClick={() => setDeleteConfirm(true)} style={{ padding: '8px 16px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: 'rgba(248,113,113,.1)', border: '1px solid rgba(248,113,113,.25)', color: '#f87171', cursor: 'pointer' }}>
                    Delete Tenant
                  </button>
                ) : (
                  <div style={{ background: 'rgba(248,113,113,.08)', border: '1px solid rgba(248,113,113,.2)', borderRadius: '8px', padding: '14px' }}>
                    <p style={{ fontSize: '13px', color: '#f87171', margin: '0 0 12px 0', fontWeight: 600 }}>
                      Delete <strong>{tenant.display_name}</strong>? This cannot be undone.
                    </p>
                    <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', margin: '0 0 12px 0' }}>
                      The tenant must have no applications, agents, or users first.
                    </p>
                    <div style={{ display: 'flex', gap: '8px' }}>
                      <button onClick={() => setDeleteConfirm(false)} disabled={deleting} style={{ padding: '7px 14px', borderRadius: '8px', fontSize: '13px', background: 'transparent', border: '1px solid rgba(255,255,255,.12)', color: 'var(--tm-card-text-muted)', cursor: 'pointer' }}>
                        Cancel
                      </button>
                      <button onClick={deleteTenantHandler} disabled={deleting} style={{ padding: '7px 14px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: 'rgba(248,113,113,.15)', border: '1px solid rgba(248,113,113,.3)', color: '#f87171', cursor: deleting ? 'not-allowed' : 'pointer', opacity: deleting ? 0.6 : 1 }}>
                        {deleting ? 'Deleting…' : 'Yes, delete'}
                      </button>
                    </div>
                  </div>
                )}
              </div>
            )}
          </>
        )}

        {tab === 'idp' && (
          <>
            <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginTop: 0, marginBottom: '16px' }}>
              Configure an OIDC identity provider for SSO login to this tenant.
              {tenant.idp_configured && <span style={{ color: '#34d399', marginLeft: '6px' }}>✓ IdP configured</span>}
            </p>

            {/* Discovery URL row with inline Test button */}
            <div style={row}>
              <label style={lbl}>Discovery URL</label>
              <div style={{ display: 'flex', gap: '8px' }}>
                <input value={discoveryURL} onChange={e => { setDiscoveryURL(e.target.value); setDiscoveryResult(null); setDiscoveryErr(''); }} style={{ ...inp, flex: 1 }} placeholder="https://accounts.google.com" />
                <button onClick={testDiscovery} style={{ padding: '8px 14px', borderRadius: '8px', fontSize: '12px', fontWeight: 600, background: 'rgba(129,140,248,.15)', border: '1px solid rgba(129,140,248,.3)', color: ACCENT, cursor: 'pointer', whiteSpace: 'nowrap' }}>
                  Test ▶
                </button>
              </div>
              {/* Discovery result */}
              {discoveryResult && (
                <div style={{ marginTop: '10px', padding: '10px 12px', borderRadius: '8px', background: 'rgba(52,211,153,.08)', border: '1px solid rgba(52,211,153,.2)', fontSize: '12px', lineHeight: 1.7 }}>
                  <div style={{ color: '#34d399', fontWeight: 600, marginBottom: '4px' }}>✓ Discovery reachable</div>
                  <div style={{ color: 'var(--tm-card-text-muted)' }}><span style={{ color: 'var(--tm-card-text)' }}>Issuer:</span> {discoveryResult.issuer}</div>
                  <div style={{ color: 'var(--tm-card-text-muted)' }}><span style={{ color: 'var(--tm-card-text)' }}>Auth:</span> {discoveryResult.auth}</div>
                  <div style={{ color: 'var(--tm-card-text-muted)' }}><span style={{ color: 'var(--tm-card-text)' }}>Token:</span> {discoveryResult.token}</div>
                </div>
              )}
              {discoveryErr && (
                <div style={{ marginTop: '8px', fontSize: '12px', color: '#f87171' }}>{discoveryErr}</div>
              )}
            </div>

            {['Client ID', 'Redirect URI'].map((label, i) => {
              const vals = [clientID, redirectURI];
              const setters = [setClientID, setRedirectURI];
              return (
                <div key={label} style={row}>
                  <label style={lbl}>{label}</label>
                  <input value={vals[i]} onChange={e => setters[i](e.target.value)} style={inp} placeholder={i === 1 ? 'https://yourapp.com/auth/oidc/callback' : ''} />
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

            {/* SSO login test link — only shown when IdP is configured */}
            {tenant.idp_configured && (
              <div style={{ marginTop: '20px', padding: '12px 14px', borderRadius: '10px', background: 'rgba(129,140,248,.06)', border: '1px solid rgba(129,140,248,.15)' }}>
                <div style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text)', marginBottom: '6px' }}>Test SSO Login</div>
                <div style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginBottom: '10px' }}>
                  Opens the full SSO login flow for this tenant in a new tab. Use your IdP test credentials.
                </div>
                <a
                  href={`/auth/oidc/start?tenant=${tenant.slug}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{ display: 'inline-block', padding: '8px 16px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, textDecoration: 'none' }}
                >
                  Start SSO Login for {tenant.slug} ↗
                </a>
              </div>
            )}
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

        {tab === 'members' && (
          membersLoading ? (
            <p style={{ color: 'var(--tm-card-text-muted)', fontSize: '13px' }}>Loading members…</p>
          ) : members.length === 0 ? (
            <p style={{ color: 'var(--tm-card-text-muted)', fontSize: '13px' }}>No members yet.</p>
          ) : (
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '13px' }}>
              <thead>
                <tr>
                  {['Username', 'Email', 'Role', 'Joined'].map(h => (
                    <th key={h} style={{ textAlign: 'left', padding: '6px 8px', fontSize: '11px', fontWeight: 600, color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', borderBottom: '1px solid rgba(255,255,255,.06)' }}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {members.map((m, i) => (
                  <tr key={m.id} style={{ borderBottom: i < members.length - 1 ? '1px solid rgba(255,255,255,.04)' : 'none' }}>
                    <td style={{ padding: '8px 8px', color: 'var(--tm-card-text)', fontWeight: 500 }}>{m.username}</td>
                    <td style={{ padding: '8px 8px', color: 'var(--tm-card-text-muted)', fontSize: 12 }}>{m.email || '—'}</td>
                    <td style={{ padding: '8px 8px', color: 'var(--tm-card-text-muted)' }}>{m.role}</td>
                    <td style={{ padding: '8px 8px', color: 'var(--tm-card-text-muted)', fontSize: 12 }}>{new Date(m.created_at).toLocaleDateString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        )}

        {tab === 'groups' && (
          <>
            <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginTop: 0, marginBottom: '16px' }}>
              Map IdP group claims to tenant membership roles. The highest-priority match (lowest number) wins.
            </p>

            {groupsLoading ? (
              <p style={{ color: 'var(--tm-card-text-muted)', fontSize: '13px' }}>Loading…</p>
            ) : (
              <>
                {groupMappings.length === 0 ? (
                  <p style={{ color: 'var(--tm-card-text-muted)', fontSize: '13px', marginBottom: '16px' }}>No group mappings yet.</p>
                ) : (
                  <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '13px', marginBottom: '20px' }}>
                    <thead>
                      <tr>
                        {['Group Claim', 'Role', 'Priority', ''].map(h => (
                          <th key={h} style={{ textAlign: 'left', padding: '6px 8px', fontSize: '11px', fontWeight: 600, color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', borderBottom: '1px solid rgba(255,255,255,.06)' }}>
                            {h}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {groupMappings.map((m, i) => (
                        <tr key={m.id} style={{ borderBottom: i < groupMappings.length - 1 ? '1px solid rgba(255,255,255,.04)' : 'none' }}>
                          <td style={{ padding: '8px 8px', color: 'var(--tm-card-text)', fontFamily: 'monospace', fontSize: 12 }}>{m.group_claim}</td>
                          <td style={{ padding: '8px 8px', color: 'var(--tm-card-text-muted)' }}>{m.role}</td>
                          <td style={{ padding: '8px 8px', color: 'var(--tm-card-text-muted)' }}>{m.priority}</td>
                          <td style={{ padding: '8px 8px' }}>
                            <button
                              onClick={() => deleteGroupMappingHandler(m.id)}
                              style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#f87171', padding: '2px 6px', fontSize: '12px', borderRadius: '6px' }}
                              title="Delete"
                            >✕</button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}

                <div style={{ background: 'rgba(255,255,255,.03)', border: '1px solid rgba(255,255,255,.08)', borderRadius: '10px', padding: '14px' }}>
                  <p style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text)', margin: '0 0 12px 0' }}>Add / update mapping</p>
                  <div style={row}>
                    <label style={lbl}>Group Claim (exact value from IdP)</label>
                    <input
                      value={newGroupClaim}
                      onChange={e => setNewGroupClaim(e.target.value)}
                      style={inp}
                      placeholder="bank-admins"
                    />
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px', marginBottom: '16px' }}>
                    <div>
                      <label style={lbl}>Role</label>
                      <select value={newGroupRole} onChange={e => setNewGroupRole(e.target.value)} style={{ ...inp, cursor: 'pointer' }}>
                        {['viewer', 'member', 'admin', 'super_admin'].map(r => (
                          <option key={r} value={r}>{r}</option>
                        ))}
                      </select>
                    </div>
                    <div>
                      <label style={lbl}>Priority (lower = higher priority)</label>
                      <input
                        type="number" min={1}
                        value={newGroupPriority}
                        onChange={e => setNewGroupPriority(parseInt(e.target.value, 10) || 10)}
                        style={inp}
                      />
                    </div>
                  </div>
                  <button onClick={saveGroupMapping} disabled={groupsSaving} style={{ padding: '9px 20px', borderRadius: '10px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: groupsSaving ? 'not-allowed' : 'pointer', opacity: groupsSaving ? 0.6 : 1 }}>
                    {groupsSaving ? 'Saving…' : 'Save Mapping'}
                  </button>
                  {groupsMsg && <p style={{ fontSize: '12px', color: groupsMsg === 'Saved' ? '#34d399' : '#f87171', marginTop: '8px' }}>{groupsMsg}</p>}
                </div>
              </>
            )}
          </>
        )}
      </div>
    </aside>
  );
}

// ── Create modal ───────────────────────────────────────────────────────────────


// ── Page ───────────────────────────────────────────────────────────────────────

export default function TenantsPage() {
  useRequireSuperAdmin();
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
  function handleDeleted(id: string) {
    setTenants(prev => prev.filter(t => t.id !== id));
    setSelected(null);
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

        {selected && <TenantPanel tenant={selected} onClose={() => setSelected(null)} onPatched={handlePatched} onDeleted={handleDeleted} />}
        {showCreate && <ProvisionWizard onClose={() => setShowCreate(false)} onCreated={handleCreated} />}
      </main>
    </>
  );
}
