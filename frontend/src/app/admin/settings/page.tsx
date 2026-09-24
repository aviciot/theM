'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type MonitoringConfig } from '@/lib/api';
import { ROLE_DEFAULTS, MONITORING_DEFAULTS } from './settingsConstants';
import { TenantRoleCard } from './TenantRoleCard';
import { MonitoringPanel } from './MonitoringPanel';
import { LLMProvidersPanel } from './LLMProvidersPanel';

type SettingsTab = 'system_agents' | 'monitoring' | 'llm_providers';

export default function AdminSettingsPage() {
  const [activeTab, setActiveTab] = useState<SettingsTab>('system_agents');
  const [monConfig,  setMonConfig]  = useState<MonitoringConfig>(MONITORING_DEFAULTS);
  const [monSaving,  setMonSaving]  = useState(false);
  const [monSaveMsg, setMonSaveMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const roleOrder = Object.keys(ROLE_DEFAULTS);

  useEffect(() => {
    themApi.getMonitoringConfig()
      .then((cfg) => setMonConfig(cfg))
      .catch(() => setMonConfig(MONITORING_DEFAULTS));
  }, []);

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
              Configuration for your organisation's internal system helpers.
            </p>

            <div style={{ display: 'flex', gap: '4px', borderBottom: '1px solid rgba(132,157,188,.12)', marginBottom: '0' }}>
              {([
                { id: 'system_agents'  as SettingsTab, label: 'System Agents',  icon: 'smart_toy' },
                { id: 'monitoring'     as SettingsTab, label: 'Monitoring',      icon: 'monitoring' },
                { id: 'llm_providers'  as SettingsTab, label: 'LLM Providers',   icon: 'key' },
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

            {activeTab === 'llm_providers' && (
              <LLMProvidersPanel />
            )}

            {activeTab === 'system_agents' && (
              <>
                <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: '0 0 24px 0', lineHeight: 1.5 }}>
                  Choose how each role calls an LLM: General settings uses your own saved LLM Providers key; Custom lets you set a separate one just for this role.
                </p>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
                  {roleOrder.map((role) => (
                    <TenantRoleCard key={role} role={role} />
                  ))}
                </div>
              </>
            )}
          </div>
        </main>
      </div>
    </AuthGuard>
  );
}
