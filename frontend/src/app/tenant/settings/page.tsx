'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type TenantRecord, type TenantQuota, type IDPConfig, type GroupMapping, type GroupMappingInput, type OIDCDebugRecord } from '@/lib/api';

const ACCENT = '#818cf8';

// ── Field ──────────────────────────────────────────────────────────────────────

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '16px' }}>
      <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', marginBottom: '6px', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
        {label}
      </label>
      {children}
    </div>
  );
}

// ── QuotaRow ──────────────────────────────────────────────────────────────────

function QuotaRow({ label, value }: { label: string; value: number | null }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '10px 0', borderBottom: '1px solid var(--tm-border)' }}>
      <span style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)' }}>{label}</span>
      <span style={{ fontSize: '13px', fontWeight: 600, color: value === null ? 'rgba(129,140,248,.7)' : 'var(--tm-card-text)' }}>
        {value === null ? 'Unlimited' : value.toLocaleString()}
      </span>
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function TenantSettingsPage() {
  const [tenant, setTenant] = useState<TenantRecord | null>(null);
  const [quota, setQuota] = useState<TenantQuota | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [displayName, setDisplayName] = useState('');
  const [emailDomain, setEmailDomain] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveMsg, setSaveMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const [activeTab, setActiveTab] = useState<'general' | 'sso' | 'mappings' | 'quota'>('general');

  // SSO tab state
  const [idpDiscoveryUrl, setIdpDiscoveryUrl] = useState('');
  const [idpClientId, setIdpClientId] = useState('');
  const [idpClientSecret, setIdpClientSecret] = useState('');
  const [idpRedirectUri, setIdpRedirectUri] = useState('');
  const [idpGroupsClaim, setIdpGroupsClaim] = useState('');
  const [idpUnmatchedAction, setIdpUnmatchedAction] = useState('viewer');
  const [idpSecretChanged, setIdpSecretChanged] = useState(false);
  const [savingSso, setSavingSso] = useState(false);
  const [saveMsgSso, setSaveMsgSso] = useState<{ ok: boolean; text: string } | null>(null);
  const [clearingSso, setClearingSso] = useState(false);

  // Group Mappings tab state
  const [mappings, setMappings] = useState<GroupMapping[]>([]);
  const [mapGroupClaim, setMapGroupClaim] = useState('');
  const [mapRole, setMapRole] = useState<'admin' | 'member' | 'viewer'>('member');
  const [mapPriority, setMapPriority] = useState(10);
  const [savingMap, setSavingMap] = useState(false);
  const [mapMsg, setMapMsg] = useState<{ ok: boolean; text: string } | null>(null);

  // Debug box state
  const [debugEmail, setDebugEmail] = useState('');
  const [debugRecord, setDebugRecord] = useState<OIDCDebugRecord | null>(null);
  const [debugMsg, setDebugMsg] = useState<string | null>(null);
  const [loadingDebug, setLoadingDebug] = useState(false);

  useEffect(() => {
    Promise.all([
      themApi.getTenantSettings(),
      themApi.getTenantSelfQuota().catch(() => null),
    ]).then(([t, q]) => {
      setTenant(t);
      setDisplayName(t.display_name);
      setEmailDomain(t.email_domain ?? '');
      if (t.idp_config) {
        setIdpDiscoveryUrl(t.idp_config.discovery_url ?? '');
        setIdpClientId(t.idp_config.client_id ?? '');
        setIdpRedirectUri(t.idp_config.redirect_uri ?? '');
        setIdpGroupsClaim(t.idp_config.groups_claim ?? '');
        setIdpUnmatchedAction(t.idp_config.unmatched_action ?? 'viewer');
      }
      setQuota(q);
    }).catch(() => {
      setError('Failed to load tenant settings.');
    }).finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    if (activeTab === 'mappings') {
      themApi.listMyGroupMappings().then(setMappings).catch(() => setMappings([]));
    }
  }, [activeTab]);

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    if (!tenant) return;
    setSaving(true);
    setSaveMsg(null);
    try {
      const updated = await themApi.patchTenantSettings({
        display_name: displayName.trim() || undefined,
        email_domain: emailDomain.trim() || null,
      });
      setTenant(updated);
      setSaveMsg({ ok: true, text: 'Settings saved.' });
    } catch {
      setSaveMsg({ ok: false, text: 'Failed to save settings.' });
    } finally {
      setSaving(false);
    }
  }

  async function handleSaveSso(e: React.FormEvent) {
    e.preventDefault();
    if (!tenant) return;
    setSavingSso(true);
    setSaveMsgSso(null);
    try {
      const idpConfig: IDPConfig = {
        discovery_url: idpDiscoveryUrl.trim(),
        client_id: idpClientId.trim(),
        redirect_uri: idpRedirectUri.trim(),
        groups_claim: idpGroupsClaim.trim() || undefined,
        unmatched_action: idpUnmatchedAction !== 'viewer' ? idpUnmatchedAction : undefined,
      };
      if (idpSecretChanged && idpClientSecret) {
        idpConfig.client_secret = idpClientSecret;
      }
      const updated = await themApi.patchTenantSettings({ idp_config: idpConfig });
      setTenant(updated);
      setIdpSecretChanged(false);
      setIdpClientSecret('');
      setSaveMsgSso({ ok: true, text: 'SSO configuration saved.' });
    } catch {
      setSaveMsgSso({ ok: false, text: 'Failed to save SSO configuration.' });
    } finally {
      setSavingSso(false);
    }
  }

  async function handleClearSso() {
    if (!tenant) return;
    if (!window.confirm('Clear SSO configuration? Users will no longer be able to log in via SSO for this tenant.')) return;
    setClearingSso(true);
    setSaveMsgSso(null);
    try {
      const updated = await themApi.patchTenantSettings({ idp_config: null });
      setTenant(updated);
      setIdpDiscoveryUrl(''); setIdpClientId(''); setIdpClientSecret('');
      setIdpRedirectUri(''); setIdpGroupsClaim(''); setIdpUnmatchedAction('viewer');
      setIdpSecretChanged(false);
      setSaveMsgSso({ ok: true, text: 'SSO configuration cleared.' });
    } catch {
      setSaveMsgSso({ ok: false, text: 'Failed to clear SSO configuration.' });
    } finally {
      setClearingSso(false);
    }
  }

  async function handleAddMapping(e: React.FormEvent) {
    e.preventDefault();
    if (!mapGroupClaim.trim()) return;
    setSavingMap(true);
    setMapMsg(null);
    try {
      const input: GroupMappingInput = { group_claim: mapGroupClaim.trim(), role: mapRole, priority: mapPriority };
      const m = await themApi.upsertMyGroupMapping(input);
      setMappings(prev => {
        const idx = prev.findIndex(x => x.id === m.id);
        if (idx >= 0) { const next = [...prev]; next[idx] = m; return next; }
        return [...prev, m];
      });
      setMapGroupClaim('');
      setMapMsg({ ok: true, text: 'Mapping saved.' });
    } catch {
      setMapMsg({ ok: false, text: 'Failed to save mapping.' });
    } finally {
      setSavingMap(false);
    }
  }

  async function handleDeleteMapping(id: string) {
    if (!window.confirm('Delete this group mapping?')) return;
    try {
      await themApi.deleteMyGroupMapping(id);
      setMappings(prev => prev.filter(m => m.id !== id));
    } catch {
      setMapMsg({ ok: false, text: 'Failed to delete mapping.' });
    }
  }

  async function handleDebugLookup(e: React.FormEvent) {
    e.preventDefault();
    if (!debugEmail.trim()) return;
    setLoadingDebug(true);
    setDebugRecord(null);
    setDebugMsg(null);
    try {
      const rec = await themApi.getOIDCDebug(debugEmail.trim());
      setDebugRecord(rec);
    } catch (err: unknown) {
      const status = (err as { status?: number } | null)?.status;
      if (status === 404) setDebugMsg('No SSO login record found for this email (no SSO login in last 24h, or this user belongs to a different tenant).');
      else if (status === 503) setDebugMsg('Debug log unavailable (Redis not configured on this server).');
      else if (status === 403) setDebugMsg('Access denied — only tenant admins can view SSO debug records.');
      else setDebugMsg(`Lookup failed (HTTP ${status ?? 'unknown'}). Check that this email has performed an SSO login recently.`);
    } finally {
      setLoadingDebug(false);
    }
  }

  const inputStyle: React.CSSProperties = {
    width: '100%', padding: '9px 12px', borderRadius: '8px',
    background: 'var(--tm-input-bg, rgba(255,255,255,.06))',
    border: '1px solid var(--tm-border)',
    color: 'var(--tm-card-text)', fontSize: '14px', outline: 'none',
    boxSizing: 'border-box',
  };

  const readOnlyStyle: React.CSSProperties = {
    ...inputStyle,
    background: 'rgba(255,255,255,.03)',
    color: 'var(--tm-card-text-muted)',
    cursor: 'not-allowed',
  };

  const tabStyle = (active: boolean): React.CSSProperties => ({
    padding: '8px 16px', borderRadius: '8px', cursor: 'pointer', border: 'none',
    background: active ? `${ACCENT}20` : 'transparent',
    color: active ? ACCENT : 'var(--tm-card-text-muted)',
    fontWeight: active ? 700 : 400, fontSize: '13px', transition: 'all .15s',
  });

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1, padding: '32px 40px' }}>
          <div style={{ maxWidth: '680px' }}>
            <h1 style={{ fontSize: '22px', fontWeight: 700, color: 'var(--tm-card-text)', marginBottom: '4px' }}>
              Organization Settings
            </h1>
            <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', marginBottom: '28px' }}>
              Manage your tenant settings and view resource limits.
            </p>

            {loading && (
              <p style={{ color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>Loading…</p>
            )}

            {error && (
              <div style={{ padding: '12px 16px', borderRadius: '8px', background: 'rgba(248,113,113,.1)', border: '1px solid rgba(248,113,113,.25)', color: '#f87171', fontSize: '13px', marginBottom: '20px' }}>
                {error}
              </div>
            )}

            {tenant && (
              <>
                {/* Tabs */}
                <div style={{ display: 'flex', gap: '4px', marginBottom: '24px' }}>
                  <button style={tabStyle(activeTab === 'general')} onClick={() => setActiveTab('general')}>General</button>
                  <button style={tabStyle(activeTab === 'sso')} onClick={() => setActiveTab('sso')}>SSO / Identity Provider</button>
                  <button style={tabStyle(activeTab === 'mappings')} onClick={() => setActiveTab('mappings')}>Group Mappings</button>
                  <button style={tabStyle(activeTab === 'quota')} onClick={() => setActiveTab('quota')}>Quota &amp; Limits</button>
                </div>

                {/* General tab */}
                {activeTab === 'general' && (
                  <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: '12px', padding: '28px' }}>
                    <form onSubmit={handleSave}>
                      <Field label="Tenant slug (read-only)">
                        <input style={readOnlyStyle} value={tenant.slug} readOnly />
                      </Field>

                      <Field label="Status">
                        <span style={{
                          display: 'inline-block', fontSize: '12px', fontWeight: 600,
                          padding: '3px 10px', borderRadius: '10px',
                          background: tenant.enabled ? 'rgba(52,211,153,.12)' : 'rgba(248,113,113,.1)',
                          color: tenant.enabled ? '#34d399' : '#f87171',
                          border: `1px solid ${tenant.enabled ? 'rgba(52,211,153,.25)' : 'rgba(248,113,113,.2)'}`,
                        }}>
                          {tenant.enabled ? 'Enabled' : 'Disabled'}
                        </span>
                        <span style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', marginLeft: '10px' }}>
                          Status can only be changed by a platform admin.
                        </span>
                      </Field>

                      <Field label="Display name">
                        <input
                          style={inputStyle}
                          value={displayName}
                          onChange={e => setDisplayName(e.target.value)}
                          placeholder="Tenant display name"
                        />
                      </Field>

                      <Field label="Email domain (for SSO routing)">
                        <input
                          style={inputStyle}
                          value={emailDomain}
                          onChange={e => setEmailDomain(e.target.value)}
                          placeholder="example.com or leave blank"
                        />
                        <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', marginTop: '4px' }}>
                          Users logging in with this domain will be routed to your tenant.
                        </p>
                      </Field>

                      <Field label="IdP configured">
                        <span style={{
                          display: 'inline-block', fontSize: '12px', fontWeight: 600,
                          padding: '3px 10px', borderRadius: '10px',
                          background: tenant.idp_configured ? `${ACCENT}18` : 'rgba(255,255,255,.05)',
                          color: tenant.idp_configured ? ACCENT : 'var(--tm-card-text-muted)',
                          border: `1px solid ${tenant.idp_configured ? `${ACCENT}40` : 'var(--tm-border)'}`,
                        }}>
                          {tenant.idp_configured ? 'Configured' : 'Not configured'}
                        </span>
                      </Field>

                      {saveMsg && (
                        <div style={{
                          padding: '10px 14px', borderRadius: '8px', marginBottom: '16px', fontSize: '13px',
                          background: saveMsg.ok ? 'rgba(52,211,153,.1)' : 'rgba(248,113,113,.1)',
                          border: `1px solid ${saveMsg.ok ? 'rgba(52,211,153,.25)' : 'rgba(248,113,113,.25)'}`,
                          color: saveMsg.ok ? '#34d399' : '#f87171',
                        }}>
                          {saveMsg.text}
                        </div>
                      )}

                      <button type="submit" disabled={saving} style={{
                        padding: '9px 20px', borderRadius: '8px', border: 'none',
                        background: ACCENT, color: '#fff', fontWeight: 600, fontSize: '13px',
                        cursor: saving ? 'not-allowed' : 'pointer', opacity: saving ? 0.6 : 1,
                        transition: 'opacity .15s',
                      }}>
                        {saving ? 'Saving…' : 'Save changes'}
                      </button>
                    </form>
                  </div>
                )}

                {/* SSO tab */}
                {activeTab === 'sso' && (
                  <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: '12px', padding: '28px' }}>
                    <div style={{ marginBottom: '20px' }}>
                      <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: 0 }}>
                        Configure an OIDC Identity Provider so users can log in via SSO. The client secret is write-only and never returned by the API.
                      </p>
                      <div style={{ marginTop: '12px' }}>
                        <span style={{
                          display: 'inline-block', fontSize: '12px', fontWeight: 600,
                          padding: '3px 10px', borderRadius: '10px',
                          background: tenant.idp_configured ? `${ACCENT}18` : 'rgba(255,255,255,.05)',
                          color: tenant.idp_configured ? ACCENT : 'var(--tm-card-text-muted)',
                          border: `1px solid ${tenant.idp_configured ? `${ACCENT}40` : 'var(--tm-border)'}`,
                        }}>
                          {tenant.idp_configured ? 'SSO configured' : 'SSO not configured'}
                        </span>
                      </div>
                    </div>

                    <form onSubmit={handleSaveSso}>
                      <Field label="Discovery URL">
                        <input
                          style={inputStyle}
                          value={idpDiscoveryUrl}
                          onChange={e => setIdpDiscoveryUrl(e.target.value)}
                          placeholder="https://idp.example.com/realms/my-realm"
                          required
                        />
                        <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', marginTop: '4px' }}>
                          OIDC provider well-known discovery document base URL (without /.well-known/openid-configuration).
                        </p>
                      </Field>

                      <Field label="Client ID">
                        <input
                          style={inputStyle}
                          value={idpClientId}
                          onChange={e => setIdpClientId(e.target.value)}
                          placeholder="my-client-id"
                          required
                        />
                      </Field>

                      <Field label="Client Secret">
                        <input
                          type="password"
                          style={inputStyle}
                          value={idpClientSecret}
                          onChange={e => { setIdpClientSecret(e.target.value); setIdpSecretChanged(true); }}
                          onFocus={() => setIdpSecretChanged(true)}
                          placeholder={tenant.idp_configured && !idpSecretChanged ? '••••••••••••••••' : 'Enter client secret'}
                          autoComplete="new-password"
                        />
                        {tenant.idp_configured && !idpSecretChanged && (
                          <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', marginTop: '4px' }}>
                            A secret is already configured. Leave blank to keep the existing secret, or type a new one to replace it.
                          </p>
                        )}
                      </Field>

                      <Field label="Redirect URI">
                        <input
                          style={inputStyle}
                          value={idpRedirectUri}
                          onChange={e => setIdpRedirectUri(e.target.value)}
                          placeholder="http://localhost:8088/auth/api/v1/auth/oidc/callback"
                          required
                        />
                        <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', marginTop: '4px' }}>
                          Must match the redirect URI registered in your IdP.
                        </p>
                      </Field>

                      <Field label="Groups claim name">
                        <input
                          style={inputStyle}
                          value={idpGroupsClaim}
                          onChange={e => setIdpGroupsClaim(e.target.value)}
                          placeholder='groups  (leave blank for default "groups")'
                        />
                        <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', marginTop: '4px' }}>
                          The ID token claim that carries group values. Defaults to &quot;groups&quot; when blank. &quot;groups&quot; is not a guaranteed OIDC Core claim — check your IdP's token configuration.
                        </p>
                      </Field>

                      <Field label="Unmatched user policy">
                        <select
                          style={{ ...inputStyle, cursor: 'pointer' }}
                          value={idpUnmatchedAction}
                          onChange={e => setIdpUnmatchedAction(e.target.value)}
                        >
                          <option value="viewer">viewer — allow login with viewer role</option>
                          <option value="deny">deny — reject login if no group mapping matches</option>
                        </select>
                        <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', marginTop: '4px' }}>
                          Controls what happens when an SSO user&apos;s groups don&apos;t match any group mapping. &quot;deny&quot; is recommended for tightly-controlled tenants.
                        </p>
                      </Field>

                      {saveMsgSso && (
                        <div style={{
                          padding: '10px 14px', borderRadius: '8px', marginBottom: '16px', fontSize: '13px',
                          background: saveMsgSso.ok ? 'rgba(52,211,153,.1)' : 'rgba(248,113,113,.1)',
                          border: `1px solid ${saveMsgSso.ok ? 'rgba(52,211,153,.25)' : 'rgba(248,113,113,.25)'}`,
                          color: saveMsgSso.ok ? '#34d399' : '#f87171',
                        }}>
                          {saveMsgSso.text}
                        </div>
                      )}

                      <div style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
                        <button type="submit" disabled={savingSso} style={{
                          padding: '9px 20px', borderRadius: '8px', border: 'none',
                          background: ACCENT, color: '#fff', fontWeight: 600, fontSize: '13px',
                          cursor: savingSso ? 'not-allowed' : 'pointer', opacity: savingSso ? 0.6 : 1,
                          transition: 'opacity .15s',
                        }}>
                          {savingSso ? 'Saving…' : 'Save SSO config'}
                        </button>

                        {tenant.idp_configured && (
                          <button
                            type="button"
                            onClick={handleClearSso}
                            disabled={clearingSso}
                            style={{
                              padding: '9px 20px', borderRadius: '8px',
                              border: '1px solid rgba(248,113,113,.35)',
                              background: 'rgba(248,113,113,.08)', color: '#f87171',
                              fontWeight: 600, fontSize: '13px',
                              cursor: clearingSso ? 'not-allowed' : 'pointer',
                              opacity: clearingSso ? 0.6 : 1, transition: 'opacity .15s',
                            }}
                          >
                            {clearingSso ? 'Clearing…' : 'Clear SSO config'}
                          </button>
                        )}
                      </div>
                    </form>
                  </div>
                )}

                {/* Group Mappings tab */}
                {activeTab === 'mappings' && (
                  <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: '12px', padding: '28px' }}>
                    <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', marginTop: 0, marginBottom: '20px' }}>
                      Map IdP group claim values to tenant roles. The highest-priority match (lowest number) wins. Role takes effect on the next SSO login.
                    </p>

                    {/* Claim & Policy Settings */}
                    <div style={{ padding: '16px 18px', borderRadius: '10px', border: '1px solid var(--tm-border)', background: 'rgba(255,255,255,.02)', marginBottom: '24px' }}>
                      <p style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginTop: 0, marginBottom: '14px' }}>
                        Claim &amp; Policy Settings
                      </p>
                      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr auto', gap: '12px', alignItems: 'flex-end' }}>
                        <Field label='Groups claim name'>
                          <input
                            style={inputStyle}
                            value={idpGroupsClaim}
                            onChange={e => setIdpGroupsClaim(e.target.value)}
                            placeholder='groups  (default)'
                          />
                        </Field>
                        <Field label='Unmatched user policy'>
                          <select
                            style={{ ...inputStyle, cursor: 'pointer' }}
                            value={idpUnmatchedAction}
                            onChange={e => setIdpUnmatchedAction(e.target.value)}
                          >
                            <option value="viewer">viewer — allow with viewer role</option>
                            <option value="deny">deny — reject login if no match</option>
                          </select>
                        </Field>
                        <Field label=" ">
                          <button
                            type="button"
                            disabled={savingSso || !tenant.idp_configured}
                            onClick={async () => {
                              setSavingSso(true);
                              setSaveMsgSso(null);
                              try {
                                const idpConfig: IDPConfig = {
                                  discovery_url: idpDiscoveryUrl,
                                  client_id: idpClientId,
                                  redirect_uri: idpRedirectUri,
                                  groups_claim: idpGroupsClaim.trim() || undefined,
                                  unmatched_action: idpUnmatchedAction !== 'viewer' ? idpUnmatchedAction : undefined,
                                };
                                const updated = await themApi.patchTenantSettings({ idp_config: idpConfig });
                                setTenant(updated);
                                setSaveMsgSso({ ok: true, text: 'Claim settings saved.' });
                              } catch {
                                setSaveMsgSso({ ok: false, text: 'Failed to save claim settings.' });
                              } finally {
                                setSavingSso(false);
                              }
                            }}
                            style={{
                              padding: '9px 18px', borderRadius: '8px', border: 'none',
                              background: tenant.idp_configured ? ACCENT : 'rgba(255,255,255,.1)',
                              color: '#fff', fontWeight: 600, fontSize: '13px',
                              cursor: (savingSso || !tenant.idp_configured) ? 'not-allowed' : 'pointer',
                              opacity: (savingSso || !tenant.idp_configured) ? 0.5 : 1,
                            }}
                          >
                            {savingSso ? '…' : 'Save'}
                          </button>
                        </Field>
                      </div>
                      {!tenant.idp_configured && (
                        <p style={{ fontSize: '11px', color: '#f87171', marginTop: '4px', marginBottom: 0 }}>
                          SSO must be configured in the SSO tab before claim settings can be saved.
                        </p>
                      )}
                      {saveMsgSso && (
                        <div style={{
                          padding: '8px 12px', borderRadius: '6px', marginTop: '10px', fontSize: '12px',
                          background: saveMsgSso.ok ? 'rgba(52,211,153,.1)' : 'rgba(248,113,113,.1)',
                          border: `1px solid ${saveMsgSso.ok ? 'rgba(52,211,153,.25)' : 'rgba(248,113,113,.25)'}`,
                          color: saveMsgSso.ok ? '#34d399' : '#f87171',
                        }}>
                          {saveMsgSso.text}
                        </div>
                      )}
                    </div>

                    {/* Existing mappings */}
                    {mappings.length > 0 && (
                      <table style={{ width: '100%', borderCollapse: 'collapse', marginBottom: '24px', fontSize: '13px' }}>
                        <thead>
                          <tr style={{ borderBottom: '1px solid var(--tm-border)' }}>
                            {['Group claim', 'Role', 'Priority', ''].map(h => (
                              <th key={h} style={{ textAlign: 'left', padding: '8px 10px', color: 'var(--tm-card-text-muted)', fontWeight: 600, fontSize: '11px', textTransform: 'uppercase' }}>{h}</th>
                            ))}
                          </tr>
                        </thead>
                        <tbody>
                          {mappings.sort((a, b) => a.priority - b.priority).map(m => (
                            <tr key={m.id} style={{ borderBottom: '1px solid rgba(255,255,255,.04)' }}>
                              <td style={{ padding: '9px 10px', color: 'var(--tm-card-text)', fontFamily: 'monospace' }}>{m.group_claim}</td>
                              <td style={{ padding: '9px 10px' }}>
                                <span style={{ fontSize: '11px', fontWeight: 600, padding: '2px 8px', borderRadius: '8px', background: `${ACCENT}18`, color: ACCENT, border: `1px solid ${ACCENT}40` }}>
                                  {m.role}
                                </span>
                              </td>
                              <td style={{ padding: '9px 10px', color: 'var(--tm-card-text-muted)' }}>{m.priority}</td>
                              <td style={{ padding: '9px 10px', textAlign: 'right' }}>
                                <button
                                  onClick={() => handleDeleteMapping(m.id)}
                                  style={{ fontSize: '11px', padding: '3px 10px', borderRadius: '6px', border: '1px solid rgba(248,113,113,.3)', background: 'rgba(248,113,113,.08)', color: '#f87171', cursor: 'pointer' }}
                                >
                                  Delete
                                </button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    )}
                    {mappings.length === 0 && (
                      <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', marginBottom: '20px' }}>No group mappings configured.</p>
                    )}

                    {/* Add mapping form */}
                    <form onSubmit={handleAddMapping}>
                      <div style={{ display: 'grid', gridTemplateColumns: '1fr auto auto auto', gap: '10px', alignItems: 'flex-end' }}>
                        <Field label="Group claim value">
                          <input
                            style={inputStyle}
                            value={mapGroupClaim}
                            onChange={e => setMapGroupClaim(e.target.value)}
                            placeholder="e.g. bank-admins"
                            required
                          />
                        </Field>
                        <Field label="Role">
                          <select style={{ ...inputStyle, cursor: 'pointer', width: '120px' }} value={mapRole} onChange={e => setMapRole(e.target.value as 'admin' | 'member' | 'viewer')}>
                            <option value="admin">admin</option>
                            <option value="member">member</option>
                            <option value="viewer">viewer</option>
                          </select>
                        </Field>
                        <Field label="Priority">
                          <input
                            type="number"
                            style={{ ...inputStyle, width: '80px' }}
                            value={mapPriority}
                            onChange={e => setMapPriority(Number(e.target.value))}
                            min={0}
                          />
                        </Field>
                        <Field label=" ">
                          <button type="submit" disabled={savingMap} style={{
                            padding: '9px 18px', borderRadius: '8px', border: 'none',
                            background: ACCENT, color: '#fff', fontWeight: 600, fontSize: '13px',
                            cursor: savingMap ? 'not-allowed' : 'pointer', opacity: savingMap ? 0.6 : 1,
                          }}>
                            {savingMap ? '…' : 'Save'}
                          </button>
                        </Field>
                      </div>
                    </form>

                    {mapMsg && (
                      <div style={{
                        padding: '10px 14px', borderRadius: '8px', marginTop: '12px', fontSize: '13px',
                        background: mapMsg.ok ? 'rgba(52,211,153,.1)' : 'rgba(248,113,113,.1)',
                        border: `1px solid ${mapMsg.ok ? 'rgba(52,211,153,.25)' : 'rgba(248,113,113,.25)'}`,
                        color: mapMsg.ok ? '#34d399' : '#f87171',
                      }}>
                        {mapMsg.text}
                      </div>
                    )}

                    {/* Debug box */}
                    <div style={{ marginTop: '36px', padding: '20px', borderRadius: '10px', border: '1px solid var(--tm-border)', background: 'rgba(255,255,255,.02)' }}>
                      <p style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginTop: 0, marginBottom: '12px' }}>
                        SSO Login Debug
                      </p>
                      <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginTop: 0, marginBottom: '14px' }}>
                        Inspect the most recent SSO login outcome for a user in this tenant (24h retention).
                      </p>
                      <form onSubmit={handleDebugLookup} style={{ display: 'flex', gap: '10px' }}>
                        <input
                          style={{ ...inputStyle, flex: 1 }}
                          value={debugEmail}
                          onChange={e => setDebugEmail(e.target.value)}
                          placeholder="user@example.com"
                          type="email"
                          required
                        />
                        <button type="submit" disabled={loadingDebug} style={{
                          padding: '9px 18px', borderRadius: '8px', border: 'none',
                          background: `${ACCENT}30`, color: ACCENT, fontWeight: 600, fontSize: '13px',
                          cursor: loadingDebug ? 'not-allowed' : 'pointer', opacity: loadingDebug ? 0.6 : 1, flexShrink: 0,
                        }}>
                          {loadingDebug ? '…' : 'Lookup'}
                        </button>
                      </form>
                      {debugMsg && (
                        <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginTop: '10px' }}>{debugMsg}</p>
                      )}
                      {debugRecord && (
                        <div style={{ marginTop: '14px', padding: '14px', borderRadius: '8px', background: 'rgba(255,255,255,.03)', border: '1px solid var(--tm-border)', fontSize: '12px' }}>
                          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 14px' }}>
                            <span style={{ color: 'var(--tm-card-text-muted)', fontWeight: 600 }}>Email</span>
                            <span style={{ color: 'var(--tm-card-text)' }}>{debugRecord.email}</span>
                            <span style={{ color: 'var(--tm-card-text-muted)', fontWeight: 600 }}>Outcome</span>
                            <span style={{ color: debugRecord.outcome === 'matched' ? '#34d399' : debugRecord.outcome.startsWith('unmatched_denied') || debugRecord.outcome === 'lookup_error' ? '#f87171' : 'var(--tm-card-text)' }}>
                              {debugRecord.outcome}
                            </span>
                            <span style={{ color: 'var(--tm-card-text-muted)', fontWeight: 600 }}>Groups received</span>
                            <span style={{ color: 'var(--tm-card-text)', fontFamily: 'monospace' }}>{debugRecord.groups_received?.join(', ') || '(none)'}</span>
                            {debugRecord.matched_group && <>
                              <span style={{ color: 'var(--tm-card-text-muted)', fontWeight: 600 }}>Matched group</span>
                              <span style={{ color: 'var(--tm-card-text)', fontFamily: 'monospace' }}>{debugRecord.matched_group}</span>
                            </>}
                            {debugRecord.matched_role && <>
                              <span style={{ color: 'var(--tm-card-text-muted)', fontWeight: 600 }}>Matched role</span>
                              <span style={{ color: ACCENT }}>{debugRecord.matched_role}</span>
                            </>}
                            <span style={{ color: 'var(--tm-card-text-muted)', fontWeight: 600 }}>Login at</span>
                            <span style={{ color: 'var(--tm-card-text-muted)' }}>{new Date(debugRecord.login_at).toLocaleString()}</span>
                          </div>
                        </div>
                      )}
                    </div>
                  </div>
                )}

                {/* Quota tab */}
                {activeTab === 'quota' && (
                  <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: '12px', padding: '28px' }}>
                    {quota ? (
                      <>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '20px' }}>
                          <span style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)' }}>Plan:</span>
                          <span style={{
                            fontSize: '12px', fontWeight: 700, padding: '3px 10px', borderRadius: '10px',
                            background: `${ACCENT}18`, color: ACCENT, border: `1px solid ${ACCENT}40`,
                            textTransform: 'capitalize',
                          }}>
                            {quota.plan}
                          </span>
                        </div>
                        <QuotaRow label="Max agents" value={quota.max_agents} />
                        <QuotaRow label="Max applications" value={quota.max_apps} />
                        <QuotaRow label="Max MCP servers" value={quota.max_mcp_servers} />
                        <QuotaRow label="Max concurrent runs" value={quota.max_concurrent_runs} />
                        <QuotaRow label="Max users" value={quota.max_users} />
                        <QuotaRow label="Monthly LLM tokens" value={quota.monthly_llm_tokens} />
                        <QuotaRow label="Monthly runs" value={quota.monthly_runs} />
                        <QuotaRow label="API requests per minute" value={quota.api_requests_per_minute} />
                        <QuotaRow label="Runs per minute" value={quota.runs_per_minute} />
                      </>
                    ) : (
                      <p style={{ color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>
                        No quota configured for this tenant. Contact a platform admin to set limits.
                      </p>
                    )}
                  </div>
                )}
              </>
            )}
          </div>
        </main>
      </div>
    </AuthGuard>
  );
}
