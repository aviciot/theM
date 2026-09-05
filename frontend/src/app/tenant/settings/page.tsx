'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type TenantRecord, type TenantQuota } from '@/lib/api';

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

  const [activeTab, setActiveTab] = useState<'general' | 'quota'>('general');

  useEffect(() => {
    Promise.all([
      themApi.getTenantSettings(),
      themApi.getTenantSelfQuota().catch(() => null),
    ]).then(([t, q]) => {
      setTenant(t);
      setDisplayName(t.display_name);
      setEmailDomain(t.email_domain ?? '');
      setQuota(q);
    }).catch(() => {
      setError('Failed to load tenant settings.');
    }).finally(() => setLoading(false));
  }, []);

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
              My Tenant
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
