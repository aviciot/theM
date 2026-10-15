'use client';
import { useEffect, useState, useCallback, Fragment } from 'react';
import { themApi, type GatewayClient, type GatewayPolicy, type GatewayPolicyInput, type GatewayRequest, type Application, type GatewayPolicyProfile } from '@/lib/api';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { PolicyProfilesTab } from './PolicyProfilesTab';

type Tab = 'clients' | 'policy' | 'requests';

const ACCENT = '#7c3aed';
const ACCENT_BG = 'rgba(124,58,237,0.08)';

// ── Shared ────────────────────────────────────────────────────────────────────

function StatusBadge({ status }: { status: string }) {
  const color =
    status === 'ok'          ? '#22c55e'
    : status === 'blocked'   ? '#ef4444'
    : status === 'rate_limited' ? '#f59e0b'
    : '#94a3b8';
  return (
    <span style={{
      display: 'inline-block', padding: '1px 8px', borderRadius: 999,
      background: color + '22', color, fontSize: 12, fontWeight: 600,
    }}>{status}</span>
  );
}

// ── Connect Info Panel ────────────────────────────────────────────────────────

function ConnectInfoPanel({ token, onDismiss }: { token: string; onDismiss?: () => void }) {
  const [copied, setCopied] = useState<string | null>(null);

  const host = typeof window !== 'undefined' ? window.location.hostname : 'your-host';
  const port = typeof window !== 'undefined' && window.location.port ? `:${window.location.port}` : '';
  const protocol = typeof window !== 'undefined' ? window.location.protocol : 'http:';
  const baseURL = `${protocol}//${host}${port}`;
  const endpoint = `${baseURL}/{tenant_slug}/llm/v1/chat/completions`;

  const curl = `curl -X POST "${endpoint}" \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer ${token}" \\
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 256
  }'`;

  const python = `import requests

response = requests.post(
    "${endpoint}",
    headers={
        "Authorization": "Bearer ${token}",
        "Content-Type": "application/json",
    },
    json={
        "model": "claude-sonnet-4-6",
        "messages": [{"role": "user", "content": "Hello!"}],
        "max_tokens": 256,
    },
)
print(response.json())`;

  const copy = (text: string, key: string) => {
    navigator.clipboard.writeText(text);
    setCopied(key);
    setTimeout(() => setCopied(null), 2000);
  };

  const codeStyle: React.CSSProperties = {
    background: '#0f172a', borderRadius: 6, padding: '12px 14px',
    fontFamily: 'monospace', fontSize: 12, whiteSpace: 'pre-wrap',
    wordBreak: 'break-all', color: '#a5f3fc', overflowX: 'auto',
    margin: 0, flex: 1,
  };

  const [tab, setTab] = useState<'curl' | 'python'>('curl');

  return (
    <div style={{ background: '#022c22', border: '1px solid #34d399', borderRadius: 10, padding: 20, marginBottom: 24 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <div style={{ fontWeight: 700, color: '#34d399', fontSize: 15, marginBottom: 4 }}>
            Client created — save your bearer token now
          </div>
          <div style={{ color: '#6ee7b7', fontSize: 12 }}>This token will not be shown again.</div>
        </div>
        {onDismiss && <button onClick={onDismiss} style={{ background: 'none', border: 'none', color: '#6ee7b7', cursor: 'pointer', fontSize: 18, lineHeight: 1 }}>✕</button>}
      </div>

      {/* Token */}
      <div style={{ marginBottom: 16 }}>
        <div style={{ color: '#6ee7b7', fontSize: 12, marginBottom: 6 }}>Bearer token</div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <code style={{ ...codeStyle, color: '#fde68a', padding: '8px 12px', flex: 1 }}>{token}</code>
          <button onClick={() => copy(token, 'token')} style={{ padding: '6px 12px', borderRadius: 6, background: '#065f46', border: '1px solid #34d399', color: '#34d399', cursor: 'pointer', fontSize: 12, whiteSpace: 'nowrap' }}>
            {copied === 'token' ? '✓ Copied' : 'Copy'}
          </button>
        </div>
      </div>

      {/* Endpoint */}
      <div style={{ marginBottom: 16 }}>
        <div style={{ color: '#6ee7b7', fontSize: 12, marginBottom: 6 }}>Endpoint (replace {'{tenant_slug}'} with your tenant)</div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <code style={{ ...codeStyle, color: '#c4b5fd', padding: '8px 12px', flex: 1 }}>{endpoint}</code>
          <button onClick={() => copy(endpoint, 'endpoint')} style={{ padding: '6px 12px', borderRadius: 6, background: '#065f46', border: '1px solid #34d399', color: '#34d399', cursor: 'pointer', fontSize: 12, whiteSpace: 'nowrap' }}>
            {copied === 'endpoint' ? '✓ Copied' : 'Copy'}
          </button>
        </div>
      </div>

      {/* Code examples */}
      <div style={{ color: '#6ee7b7', fontSize: 12, marginBottom: 8 }}>Example request</div>
      <div style={{ display: 'flex', gap: 0, marginBottom: 8, borderBottom: '1px solid #064e3b' }}>
        {(['curl', 'python'] as const).map(t => (
          <button key={t} onClick={() => setTab(t)} style={{
            padding: '5px 14px', background: 'none', border: 'none', cursor: 'pointer',
            color: tab === t ? '#34d399' : '#6ee7b7', fontWeight: tab === t ? 700 : 400,
            borderBottom: tab === t ? '2px solid #34d399' : '2px solid transparent', fontSize: 12,
          }}>{t}</button>
        ))}
      </div>
      <div style={{ display: 'flex', gap: 8, alignItems: 'flex-start' }}>
        <pre style={codeStyle}>{tab === 'curl' ? curl : python}</pre>
        <button onClick={() => copy(tab === 'curl' ? curl : python, 'code')} style={{ padding: '6px 12px', borderRadius: 6, background: '#065f46', border: '1px solid #34d399', color: '#34d399', cursor: 'pointer', fontSize: 12, whiteSpace: 'nowrap', marginTop: 0 }}>
          {copied === 'code' ? '✓ Copied' : 'Copy'}
        </button>
      </div>

      <div style={{ marginTop: 14, padding: '10px 14px', background: '#013220', borderRadius: 6, fontSize: 12, color: '#6ee7b7' }}>
        <strong style={{ color: '#34d399' }}>Note:</strong> If no Profile App is attached to this client, the-M proxies directly to the LLM.
        Attach a Gateway-type App Canvas app in the Profile App column to run a full workflow instead.
      </div>
    </div>
  );
}

// ── Clients tab ───────────────────────────────────────────────────────────────

function ClientsTab() {
  const [clients, setClients]   = useState<GatewayClient[]>([]);
  const [gatewayApps, setGatewayApps] = useState<Application[]>([]);
  const [policyProfiles, setPolicyProfiles] = useState<GatewayPolicyProfile[]>([]);
  const [loading, setLoading]   = useState(true);
  const [newLabel, setNewLabel] = useState('');
  const [creating, setCreating] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null);
  const [assigning, setAssigning] = useState<string | null>(null);
  const [expandedConnect, setExpandedConnect] = useState<string | null>(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [c, apps, profiles] = await Promise.all([
        themApi.listGatewayClients(),
        themApi.applications(),
        themApi.listGatewayPolicyProfiles(),
      ]);
      setClients(c);
      // Only show apps that have at least one gateway entry point.
      setGatewayApps(apps.filter(a => a.entry_points?.some(ep => ep.entry_point_type === 'gateway')));
      setPolicyProfiles(profiles);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(`Failed to load gateway data: ${msg}`);
    }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleCreate() {
    if (!newLabel.trim()) return;
    setCreating(true);
    setError('');
    try {
      const result = await themApi.createGatewayClient({ label: newLabel.trim() });
      setNewToken(result.token ?? null);
      setNewLabel('');
      await load();
    } catch { setError('Failed to create client'); }
    finally { setCreating(false); }
  }

  async function handleAppChange(clientId: string, appId: string) {
    setAssigning(clientId);
    try {
      await themApi.patchGatewayClient(clientId, { app_id: appId || null });
      setClients(prev => prev.map(c =>
        c.id === clientId ? { ...c, app_id: appId || null } : c
      ));
    } catch { setError('Failed to update profile app'); }
    finally { setAssigning(null); }
  }

  async function handlePolicyProfileChange(clientId: string, profileId: string) {
    setAssigning(clientId);
    try {
      await themApi.patchGatewayClient(clientId, { policy_profile_id: profileId || null });
      setClients(prev => prev.map(c =>
        c.id === clientId ? { ...c, policy_profile_id: profileId || null } : c
      ));
    } catch { setError('Failed to update policy profile'); }
    finally { setAssigning(null); }
  }

  async function handleDelete(id: string) {
    if (!confirm('Delete this client? Its bearer token will stop working immediately.')) return;
    try {
      await themApi.deleteGatewayClient(id);
      setClients(prev => prev.filter(c => c.id !== id));
    } catch { setError('Failed to delete client'); }
  }

  return (
    <div>
      {newToken && (
        <ConnectInfoPanel token={newToken} onDismiss={() => setNewToken(null)} />
      )}

      <div style={{ display: 'flex', gap: 8, marginBottom: 20 }}>
        <input
          placeholder="Client label e.g. reporting-cron"
          value={newLabel}
          onChange={e => setNewLabel(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handleCreate()}
          style={{ flex: 1, padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 14 }}
        />
        <button
          onClick={handleCreate}
          disabled={creating || !newLabel.trim()}
          style={{ padding: '8px 16px', borderRadius: 6, background: ACCENT, color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 600, opacity: creating ? 0.6 : 1 }}
        >
          {creating ? '…' : 'Create client'}
        </button>
      </div>

      {error && <div style={{ color: '#ef4444', marginBottom: 12 }}>{error}</div>}

      {loading ? <div style={{ color: '#94a3b8' }}>Loading…</div> : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 14 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid #334155', color: '#94a3b8' }}>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Label</th>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Profile app</th>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Policy Profile</th>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Last seen</th>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Created</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {clients.length === 0 && (
              <tr><td colSpan={6} style={{ padding: 20, color: '#64748b', textAlign: 'center' }}>No clients yet</td></tr>
            )}
            {clients.map(c => (
              <Fragment key={c.id}>
                <tr style={{ borderBottom: expandedConnect === c.id ? 'none' : '1px solid #1e293b' }}>
                  <td style={{ padding: '10px 12px' }}>
                    <div style={{ fontWeight: 600 }}>{c.label}</div>
                    <div style={{ color: '#64748b', fontFamily: 'monospace', fontSize: 11 }}>{c.id.slice(0, 8)}…</div>
                  </td>
                  <td style={{ padding: '10px 12px' }}>
                    <select
                      value={c.app_id ?? ''}
                      disabled={assigning === c.id}
                      onChange={e => handleAppChange(c.id, e.target.value)}
                      style={{
                        padding: '5px 8px', borderRadius: 6, border: '1px solid #334155',
                        background: '#0f172a', color: '#f1f5f9', fontSize: 13,
                        cursor: 'pointer', minWidth: 160,
                        opacity: assigning === c.id ? 0.5 : 1,
                      }}
                    >
                      <option value="">— direct LLM (no profile) —</option>
                      {gatewayApps.map(a => (
                        <option key={a.id} value={a.id}>{a.name}</option>
                      ))}
                    </select>
                    {gatewayApps.length === 0 && !c.app_id && (
                      <div style={{ fontSize: 11, color: '#64748b', marginTop: 4 }}>
                        No gateway apps yet — build one in App Canvas with a <code style={{ fontSize: 10 }}>gateway</code> entry point.
                      </div>
                    )}
                  </td>
                  <td style={{ padding: '10px 12px' }}>
                    <select
                      value={c.policy_profile_id ?? ''}
                      disabled={assigning === c.id}
                      onChange={e => handlePolicyProfileChange(c.id, e.target.value)}
                      style={{
                        padding: '5px 8px', borderRadius: 6, border: '1px solid #334155',
                        background: '#0f172a', color: '#f1f5f9', fontSize: 13,
                        cursor: 'pointer', minWidth: 140,
                        opacity: assigning === c.id ? 0.5 : 1,
                      }}
                    >
                      <option value="">— no policy —</option>
                      {policyProfiles.map(p => (
                        <option key={p.id} value={p.id}>{p.name}</option>
                      ))}
                    </select>
                  </td>
                  <td style={{ padding: '10px 12px', color: '#94a3b8' }}>{c.last_seen ? new Date(c.last_seen).toLocaleString() : '—'}</td>
                  <td style={{ padding: '10px 12px', color: '#94a3b8' }}>{new Date(c.created_at).toLocaleDateString()}</td>
                  <td style={{ padding: '10px 12px', textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button
                      onClick={() => setExpandedConnect(expandedConnect === c.id ? null : c.id)}
                      style={{ color: '#7c3aed', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13, marginRight: 10 }}
                    >{expandedConnect === c.id ? 'Hide' : 'Connect'}</button>
                    <button onClick={() => handleDelete(c.id)} style={{ color: '#ef4444', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13 }}>Delete</button>
                  </td>
                </tr>
                {expandedConnect === c.id && (
                  <tr style={{ borderBottom: '1px solid #1e293b' }}>
                    <td colSpan={6} style={{ padding: '0 12px 16px' }}>
                      <ConnectInfoPanel token="<YOUR_BEARER_TOKEN>" />
                      <div style={{ marginTop: -10, padding: '8px 14px', background: '#1e1a2e', borderRadius: 6, fontSize: 12, color: '#94a3b8', border: '1px solid #334155' }}>
                        Token not shown — it was only visible when this client was created. If lost, delete and recreate the client.
                      </div>
                    </td>
                  </tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// ── Policy tab ────────────────────────────────────────────────────────────────

function PolicyTab() {
  const [policy, setPolicy] = useState<GatewayPolicy | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);

  // Tenant providers (for showing inherited allowed models)
  const [tenantProviders, setTenantProviders] = useState<any[]>([]);
  const [provLoading, setProvLoading] = useState(true);

  // Form state
  const [enabled, setEnabled] = useState(false);
  const [aliases, setAliases] = useState<Array<{ key: string; value: string }>>([]);
  const [maxTokens, setMaxTokens] = useState('');
  const [monthlyBudget, setMonthlyBudget] = useState('');
  const [monthlySpend, setMonthlySpend] = useState<number | null>(null);

  useEffect(() => {
    Promise.all([
      themApi.getGatewayPolicy(),
      themApi.listMyLLMProviders(),
    ]).then(([p, providers]) => {
      setPolicy(p);
      setTenantProviders(providers);
      setEnabled((p.allowed_models?.length ?? 0) > 0 || Object.keys(p.model_aliases ?? {}).length > 0 || (p.max_tokens_per_request ?? 0) > 0 || (p.monthly_budget_usd ?? 0) > 0);
      const aliasObj = p.model_aliases ?? {};
      setAliases(Object.entries(aliasObj).map(([k, v]) => ({ key: k, value: v })));
      setMaxTokens(p.max_tokens_per_request != null ? String(p.max_tokens_per_request) : '');
      setMonthlyBudget(p.monthly_budget_usd != null ? String(p.monthly_budget_usd) : '');
    }).catch(() => setError('Failed to load policy')).finally(() => { setLoading(false); setProvLoading(false); });

    // Load current month spend (simplified — real endpoint would be on themApi)
    // For now we'll just set null; you can add themApi.getGatewayMonthlySpend() later
    setMonthlySpend(null);
  }, []);

  async function handleSave() {
    setSaving(true);
    setError('');
    setSaved(false);
    const aliasObj: Record<string, string> = {};
    aliases.forEach(a => { if (a.key.trim() && a.value.trim()) aliasObj[a.key.trim()] = a.value.trim(); });
    const body: GatewayPolicyInput = {
      allowed_models: [], // Inherit from LLM Providers
      model_aliases: aliasObj,
      max_tokens_per_request: maxTokens ? parseInt(maxTokens) : null,
      monthly_budget_usd: monthlyBudget ? parseFloat(monthlyBudget) : null,
    };
    try {
      const updated = await themApi.putGatewayPolicy(body);
      setPolicy(updated);
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    } catch { setError('Failed to save policy'); }
    finally { setSaving(false); }
  }

  if (loading) return <div style={{ color: '#94a3b8' }}>Loading…</div>;

  const inheritedModels = tenantProviders
    .filter(p => p.enabled)
    .flatMap(p => p.allowed_models ?? [])
    .filter((v, i, a) => a.indexOf(v) === i); // unique

  return (
    <div style={{ maxWidth: 700 }}>
      {/* Enable/Disable toggle */}
      <div style={{ marginBottom: 24, padding: 16, background: '#1e293b', borderRadius: 10, border: '1px solid #334155' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <div>
            <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 4 }}>Gateway Policy Enforcement</div>
            <div style={{ fontSize: 12, color: '#94a3b8' }}>
              {enabled ? 'Active — aliases, budgets, and token limits are enforced' : 'Disabled — all requests pass through unrestricted'}
            </div>
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
            <input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} style={{ width: 18, height: 18, cursor: 'pointer', accentColor: ACCENT }} />
            <span style={{ fontSize: 13, fontWeight: 600, color: enabled ? '#22c55e' : '#94a3b8' }}>{enabled ? 'Enabled' : 'Disabled'}</span>
          </label>
        </div>
      </div>

      {error && <div style={{ color: '#ef4444', marginBottom: 12, padding: 10, background: 'rgba(239,68,68,0.1)', borderRadius: 6 }}>{error}</div>}
      {saved && <div style={{ color: '#22c55e', marginBottom: 12, padding: 10, background: 'rgba(34,197,94,0.1)', borderRadius: 6 }}>✓ Policy saved</div>}

      {/* Inherited allowed models (read-only) */}
      <div style={{ marginBottom: 20, padding: 16, background: '#0f172a', border: '1px solid #1e293b', borderRadius: 8 }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 8 }}>
          Allowed Models (inherited from LLM Providers)
        </div>
        {provLoading ? (
          <div style={{ fontSize: 12, color: '#64748b' }}>Loading…</div>
        ) : inheritedModels.length === 0 ? (
          <div style={{ fontSize: 12, color: '#64748b' }}>
            No models configured. <a href="/admin/settings?tab=llm_providers" style={{ color: ACCENT }}>→ Configure in System Settings</a>
          </div>
        ) : (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {inheritedModels.map(m => (
              <span key={m} style={{ fontSize: 12, padding: '4px 10px', background: 'rgba(124,58,237,0.15)', color: '#c4b5fd', borderRadius: 6, fontFamily: 'monospace' }}>{m}</span>
            ))}
          </div>
        )}
        <div style={{ fontSize: 11, color: '#64748b', marginTop: 8 }}>
          Gateway requests are restricted to these models. <a href="/admin/settings?tab=llm_providers" style={{ color: ACCENT, textDecoration: 'none' }}>Manage in Settings →</a>
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 20, opacity: enabled ? 1 : 0.5, pointerEvents: enabled ? 'auto' : 'none' }}>
        {/* Model aliases */}
        <div>
          <div style={{ fontSize: 13, fontWeight: 600, color: '#94a3b8', marginBottom: 6 }}>
            Model Aliases
            <span style={{ fontSize: 11, fontWeight: 400, color: '#64748b', marginLeft: 8 }}>
              (clients send alias → gateway resolves to real model)
            </span>
          </div>
          <div style={{ fontSize: 12, color: '#64748b', marginBottom: 10, lineHeight: 1.4 }}>
            Let API clients use friendly names like <code style={{ background: '#1e293b', padding: '2px 6px', borderRadius: 4 }}>"fast"</code> instead of full model IDs.
            Example: client sends <code style={{ background: '#1e293b', padding: '2px 6px', borderRadius: 4 }}>"model": "fast"</code> → resolves to <code style={{ background: '#1e293b', padding: '2px 6px', borderRadius: 4 }}>claude-haiku-4-5</code>.
          </div>
          {aliases.map((a, i) => (
            <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <input
                type="text"
                placeholder="Alias (e.g. fast)"
                value={a.key}
                onChange={e => setAliases(prev => prev.map((x, j) => j === i ? { ...x, key: e.target.value } : x))}
                style={{ flex: '0 0 140px', padding: '7px 10px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 13 }}
              />
              <span style={{ alignSelf: 'center', color: '#64748b' }}>→</span>
              <input
                type="text"
                placeholder="Target model (e.g. claude-haiku-4-5)"
                value={a.value}
                onChange={e => setAliases(prev => prev.map((x, j) => j === i ? { ...x, value: e.target.value } : x))}
                style={{ flex: 1, padding: '7px 10px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 13, fontFamily: 'monospace' }}
              />
              <button
                onClick={() => setAliases(prev => prev.filter((_, j) => j !== i))}
                style={{ padding: '6px 10px', borderRadius: 6, border: '1px solid #334155', background: 'transparent', color: '#ef4444', cursor: 'pointer', fontSize: 12 }}
              >Remove</button>
            </div>
          ))}
          <button
            onClick={() => setAliases(prev => [...prev, { key: '', value: '' }])}
            style={{ padding: '6px 12px', borderRadius: 6, border: '1px solid #334155', background: 'transparent', color: '#94a3b8', cursor: 'pointer', fontSize: 12, fontWeight: 600 }}
          >+ Add alias</button>
        </div>

        {/* Token & budget limits */}
        <div style={{ display: 'flex', gap: 16 }}>
          <label style={{ flex: 1 }}>
            <div style={{ color: '#94a3b8', fontSize: 13, marginBottom: 4 }}>Max tokens per request</div>
            <input
              type="number"
              value={maxTokens}
              onChange={e => setMaxTokens(e.target.value)}
              style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box' }}
              placeholder="e.g. 4096 (empty = no cap)"
            />
            <div style={{ fontSize: 11, color: '#64748b', marginTop: 4 }}>Clamps client's max_tokens parameter</div>
          </label>
          <label style={{ flex: 1 }}>
            <div style={{ color: '#94a3b8', fontSize: 13, marginBottom: 4 }}>Monthly budget (USD)</div>
            <input
              type="number"
              step="0.01"
              value={monthlyBudget}
              onChange={e => setMonthlyBudget(e.target.value)}
              style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box' }}
              placeholder="e.g. 50.00 (empty = no cap)"
            />
            {monthlySpend !== null && monthlyBudget && (
              <div style={{ marginTop: 6, fontSize: 11, color: '#94a3b8' }}>
                Spent this month: ${monthlySpend.toFixed(2)} / ${parseFloat(monthlyBudget).toFixed(2)}
              </div>
            )}
          </label>
        </div>

        <button
          onClick={handleSave}
          disabled={saving}
          style={{ alignSelf: 'flex-start', padding: '10px 24px', borderRadius: 6, background: ACCENT, color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 600, fontSize: 14, opacity: saving ? 0.6 : 1 }}
        >
          {saving ? 'Saving…' : 'Save policy'}
        </button>
      </div>
    </div>
  );
}

// ── Requests tab ──────────────────────────────────────────────────────────────

function RequestsTab() {
  const [reqs, setReqs] = useState<GatewayRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    themApi.listGatewayRequests(200).then(setReqs)
      .catch(() => setError('Failed to load requests'))
      .finally(() => setLoading(false));
  }, []);

  const totalCost = reqs.reduce((s, r) => s + r.cost_usd, 0);
  const totalIn   = reqs.reduce((s, r) => s + r.tokens_in, 0);
  const totalOut  = reqs.reduce((s, r) => s + r.tokens_out, 0);

  return (
    <div>
      {error && <div style={{ color: '#ef4444', marginBottom: 12 }}>{error}</div>}

      {!loading && reqs.length > 0 && (
        <div style={{ display: 'flex', gap: 16, marginBottom: 20 }}>
          {[
            { label: 'Requests', value: reqs.length.toLocaleString() },
            { label: 'Tokens in', value: totalIn.toLocaleString() },
            { label: 'Tokens out', value: totalOut.toLocaleString() },
            { label: 'Cost (USD)', value: '$' + totalCost.toFixed(4) },
          ].map(s => (
            <div key={s.label} style={{ background: '#1e293b', borderRadius: 8, padding: '12px 16px', flex: 1 }}>
              <div style={{ color: '#94a3b8', fontSize: 12 }}>{s.label}</div>
              <div style={{ fontSize: 20, fontWeight: 700, marginTop: 4 }}>{s.value}</div>
            </div>
          ))}
        </div>
      )}

      {loading ? <div style={{ color: '#94a3b8' }}>Loading…</div> : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: '1px solid #334155', color: '#94a3b8' }}>
                {['Time', 'Client', 'Model served', 'Status', 'Tokens in', 'Tokens out', 'Cost'].map(h => (
                  <th key={h} style={{ textAlign: 'left', padding: '8px 12px', whiteSpace: 'nowrap' }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {reqs.length === 0 && (
                <tr><td colSpan={7} style={{ padding: 20, color: '#64748b', textAlign: 'center' }}>No requests yet</td></tr>
              )}
              {reqs.map(r => (
                <tr key={r.id} style={{ borderBottom: '1px solid #1e293b' }}>
                  <td style={{ padding: '8px 12px', color: '#94a3b8', whiteSpace: 'nowrap' }}>{new Date(r.created_at).toLocaleString()}</td>
                  <td style={{ padding: '8px 12px' }}>{r.client_label ?? '—'}</td>
                  <td style={{ padding: '8px 12px', fontFamily: 'monospace' }}>{r.model_served ?? r.model_requested ?? '—'}</td>
                  <td style={{ padding: '8px 12px' }}><StatusBadge status={r.status} /></td>
                  <td style={{ padding: '8px 12px', textAlign: 'right' }}>{r.tokens_in.toLocaleString()}</td>
                  <td style={{ padding: '8px 12px', textAlign: 'right' }}>{r.tokens_out.toLocaleString()}</td>
                  <td style={{ padding: '8px 12px', textAlign: 'right' }}>${r.cost_usd.toFixed(6)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function GatewayPage() {
  const [tab, setTab] = useState<Tab>('clients');

  const tabs: { key: Tab; label: string }[] = [
    { key: 'clients', label: 'Clients' },
    { key: 'policy', label: 'Policy Profiles' },
    { key: 'requests', label: 'Requests' },
  ];

  return (
    <AuthGuard>
      <div style={{ minHeight: '100vh', background: '#0f172a', color: '#f1f5f9' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', padding: 32, overflowY: 'auto' }}>
          <div style={{ marginBottom: 24 }}>
            <h1 style={{ fontSize: 22, fontWeight: 700, margin: 0 }}>LLM Gateway</h1>
            <p style={{ color: '#94a3b8', marginTop: 6, fontSize: 14 }}>
              Manage closed-agent clients, governance policy, and inspect request history.
            </p>
          </div>

          {/* Tabs */}
          <div style={{ display: 'flex', gap: 0, marginBottom: 24, borderBottom: '1px solid #1e293b' }}>
            {tabs.map(t => (
              <button
                key={t.key}
                onClick={() => setTab(t.key)}
                style={{
                  padding: '10px 20px', background: 'none', border: 'none', cursor: 'pointer',
                  color: tab === t.key ? ACCENT : '#94a3b8',
                  fontWeight: tab === t.key ? 700 : 400,
                  borderBottom: tab === t.key ? `2px solid ${ACCENT}` : '2px solid transparent',
                  fontSize: 14, transition: 'color 0.15s',
                }}
              >{t.label}</button>
            ))}
          </div>

          {tab === 'clients'  && <ClientsTab />}
          {tab === 'policy'   && <PolicyProfilesTab />}
          {tab === 'requests' && <RequestsTab />}
        </main>
      </div>
    </AuthGuard>
  );
}
