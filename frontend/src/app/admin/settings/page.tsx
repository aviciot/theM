'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type SystemAgentRoleOut, type SystemAgentRoleIn, type MonitoringConfig } from '@/lib/api';
import { ROLE_DEFAULTS, MONITORING_DEFAULTS } from './settingsConstants';
import { RoleCard, type RoleForm } from './RoleCard';
import { MonitoringPanel } from './MonitoringPanel';

function roleToForm(r: SystemAgentRoleOut): RoleForm {
  return {
    enabled:       r.enabled,
    provider:      r.provider ?? '',
    model:         r.model ?? '',
    api_key:       '',
    base_url:      r.base_url ?? '',
    system_prompt: r.system_prompt ?? '',
  };
}

type SettingsTab = 'system_agents' | 'monitoring';

export default function AdminSettingsPage() {
  const [activeTab, setActiveTab] = useState<SettingsTab>('system_agents');
  const [loading,   setLoading]   = useState(true);
  const [unavailable, setUnavailable] = useState(false);
  const [forms,    setForms]    = useState<Record<string, RoleForm>>({});
  const [hints,    setHints]    = useState<Record<string, string | null>>({});
  const [saving,   setSaving]   = useState<Record<string, boolean>>({});
  const [saveMsgs, setSaveMsgs] = useState<Record<string, { ok: boolean; text: string } | null>>({});
  const [roleOrder, setRoleOrder] = useState<string[]>([]);
  const [monConfig,  setMonConfig]  = useState<MonitoringConfig>(MONITORING_DEFAULTS);
  const [monSaving,  setMonSaving]  = useState(false);
  const [monSaveMsg, setMonSaveMsg] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    const agentsFetch = themApi.getSystemAgents()
      .then((data) => {
        const order = Object.keys(data.roles);
        const merged = Array.from(new Set([...order, ...Object.keys(ROLE_DEFAULTS)]));
        setRoleOrder(merged);
        const newForms: Record<string, RoleForm> = {};
        const newHints: Record<string, string | null> = {};
        for (const role of merged) {
          const srv = data.roles[role];
          newForms[role] = srv ? roleToForm(srv) : { enabled: false, provider: '', model: '', api_key: '', base_url: '', system_prompt: '' };
          newHints[role] = srv?.api_key_hint ?? null;
        }
        setForms(newForms);
        setHints(newHints);
      })
      .catch(() => {
        setUnavailable(true);
        const order = Object.keys(ROLE_DEFAULTS);
        setRoleOrder(order);
        const newForms: Record<string, RoleForm> = {};
        for (const role of order) {
          newForms[role] = { enabled: false, provider: '', model: '', api_key: '', base_url: '', system_prompt: '' };
        }
        setForms(newForms);
        setHints({});
      });

    const monFetch = themApi.getMonitoringConfig()
      .then((cfg) => setMonConfig(cfg))
      .catch(() => setMonConfig(MONITORING_DEFAULTS));

    Promise.all([agentsFetch, monFetch]).finally(() => setLoading(false));
  }, []);

  function patchForm(role: string, patch: Partial<RoleForm>) {
    setForms((prev) => ({ ...prev, [role]: { ...prev[role], ...patch } }));
    setSaveMsgs((prev) => ({ ...prev, [role]: null }));
  }

  async function handleSave(role: string) {
    const f = forms[role];
    if (!f) return;
    setSaving((prev) => ({ ...prev, [role]: true }));
    setSaveMsgs((prev) => ({ ...prev, [role]: null }));

    const payload: SystemAgentRoleIn = {
      enabled:       f.enabled,
      provider:      f.provider   || null,
      model:         f.model      || null,
      base_url:      f.base_url   || null,
      system_prompt: f.system_prompt || null,
      ...(f.api_key ? { api_key: f.api_key } : {}),
    };

    try {
      const updated = await themApi.putSystemAgents({ roles: { [role]: payload } });
      const srv = updated.roles[role];
      if (srv) {
        setHints((prev) => ({ ...prev, [role]: srv.api_key_hint ?? null }));
        setForms((prev) => ({ ...prev, [role]: { ...prev[role], api_key: '' } }));
      }
      setSaveMsgs((prev) => ({ ...prev, [role]: { ok: true, text: 'Saved' } }));
      setUnavailable(false);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Save failed';
      setSaveMsgs((prev) => ({ ...prev, [role]: { ok: false, text: msg } }));
    } finally {
      setSaving((prev) => ({ ...prev, [role]: false }));
    }
  }

  async function handleSaveMonitoring() {
    setMonSaving(true);
    setMonSaveMsg(null);
    try {
      const saved = await themApi.putMonitoringConfig(monConfig);
      setMonConfig(saved);
      setMonSaveMsg({ ok: true, text: 'Saved' });
    } catch (e: unknown) {
      setMonSaveMsg({ ok: false, text: e instanceof Error ? e.message : 'Save failed' });
    } finally {
      setMonSaving(false);
    }
  }

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1, background: 'var(--tm-bg)' }}>
          <div style={{ padding: '40px 32px 0' }}>
            <h2 style={{ fontSize: '40px', fontWeight: 800, color: '#fff', margin: '0 0 6px 0', letterSpacing: '-0.03em', lineHeight: 1.1 }}>Settings</h2>
            <p style={{ fontSize: '14px', color: 'var(--tm-card-text-muted)', margin: '0 0 28px 0' }}>
              Platform-wide configuration for internal system helpers.
            </p>

            <div style={{ display: 'flex', gap: '4px', borderBottom: '1px solid rgba(132,157,188,.12)', marginBottom: '0' }}>
              {([
                { id: 'system_agents' as SettingsTab, label: 'System Agents', icon: 'smart_toy' },
                { id: 'monitoring'    as SettingsTab, label: 'Monitoring',    icon: 'monitoring' },
              ]).map((tab) => {
                const active = activeTab === tab.id;
                return (
                  <button key={tab.id} onClick={() => setActiveTab(tab.id)} style={{ display: 'flex', alignItems: 'center', gap: '7px', padding: '10px 18px', border: 'none', background: 'transparent', cursor: 'pointer', fontSize: '13px', fontWeight: active ? 700 : 500, color: active ? 'var(--tm-accent)' : 'var(--tm-text-muted)', borderBottom: active ? '2px solid var(--tm-accent)' : '2px solid transparent', marginBottom: '-1px', transition: 'color 150ms' }}>
                    <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>{tab.icon}</span>
                    {tab.label}
                  </button>
                );
              })}
            </div>
          </div>

          <div style={{ padding: '28px 32px 64px', maxWidth: '860px' }}>
            {activeTab === 'monitoring' && (
              <MonitoringPanel monConfig={monConfig} setMonConfig={setMonConfig} monSaving={monSaving} monSaveMsg={monSaveMsg} onSave={handleSaveMonitoring} />
            )}

            {activeTab === 'system_agents' && (
              <>
                <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: '0 0 24px 0', lineHeight: 1.5 }}>
                  Internal LLM roles used by the platform. Each role has its own provider, model, and credentials.
                </p>
                {unavailable && !loading && (
                  <div style={{ padding: '12px 16px', borderRadius: '10px', marginBottom: '20px', background: 'rgba(230,184,92,0.08)', border: '1px solid rgba(230,184,92,0.22)', color: '#e6b85c', fontSize: '13px', display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <span className="material-symbols-outlined" style={{ fontSize: '16px', flexShrink: 0 }}>info</span>
                    Settings backend not available yet — changes will be saved once the backend is deployed.
                  </div>
                )}
                {loading && <div style={{ padding: '60px', textAlign: 'center', color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>Loading settings…</div>}
                {!loading && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
                    {roleOrder.map((role) => (
                      <RoleCard
                        key={role}
                        role={role}
                        apiKeyHint={hints[role] ?? null}
                        form={forms[role] ?? { enabled: false, provider: '', model: '', api_key: '', base_url: '', system_prompt: '' }}
                        onChange={(patch) => patchForm(role, patch)}
                        onSave={() => handleSave(role)}
                        saving={!!saving[role]}
                        saveMsg={saveMsgs[role] ?? null}
                      />
                    ))}
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
