'use client';
import { useEffect, useState } from 'react';
import { themApi, type TenantSystemAgentConfigOut } from '@/lib/api';
import { getRoleLabel, getRoleDescription, getRoleWhereUsed, getRolePromptPlaceholder, inputStyle } from './settingsConstants';
import { GeneralModePicker } from './GeneralModePicker';

type Mode = 'general' | 'custom';

interface TestState {
  loading: boolean;
  ok?: boolean;
  error?: string;
}

const segBtnStyle = (active: boolean): React.CSSProperties => ({
  flex: 1, padding: '8px 16px', borderRadius: '8px', border: 'none', cursor: 'pointer',
  fontSize: '13px', fontWeight: 600,
  background: active ? 'var(--tm-accent)' : 'transparent',
  color: active ? '#fff' : 'var(--tm-text-muted)',
  transition: 'all 0.15s',
});

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '16px' }}>
      <label style={{ display: 'block', fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', marginBottom: '6px', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        {label}
      </label>
      {children}
      {hint && <p style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '4px', opacity: 0.75 }}>{hint}</p>}
    </div>
  );
}

export function TenantRoleCard({ role }: { role: string }) {
  const [loading, setLoading] = useState(true);
  const [cfg, setCfg] = useState<TenantSystemAgentConfigOut | null>(null);
  const [mode, setMode] = useState<Mode>('custom');

  const [generalProvider, setGeneralProvider] = useState('');
  const [generalModel, setGeneralModel] = useState('');
  const [generalKeyId, setGeneralKeyId] = useState<number | null>(null);

  const [customProvider, setCustomProvider] = useState('');
  const [customModel, setCustomModel] = useState('');
  const [customApiKey, setCustomApiKey] = useState('');
  const [customBaseUrl, setCustomBaseUrl] = useState('');
  const [customSystemPrompt, setCustomSystemPrompt] = useState('');

  const [saving, setSaving] = useState(false);
  const [saveMsg, setSaveMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [testState, setTestState] = useState<TestState>({ loading: false });

  useEffect(() => {
    themApi.getTenantSystemAgentConfig(role).then((c) => {
      setCfg(c);
      setMode(c.mode);
      setGeneralProvider(c.provider_name ?? '');
      setGeneralModel(c.general_model ?? '');
      setGeneralKeyId(c.key_id ?? null);
      setCustomProvider(c.custom_provider ?? '');
      setCustomModel(c.custom_model ?? '');
      setCustomBaseUrl(c.custom_base_url ?? '');
      setCustomSystemPrompt(c.custom_system_prompt ?? '');
    }).catch(() => {
      setCfg({ role, mode: 'custom', provider_name: null, key_id: null, general_model: null, custom_provider: null, custom_model: null, custom_api_key_masked: null, custom_base_url: null, custom_system_prompt: null });
    }).finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [role]);

  async function handleSave() {
    setSaving(true);
    setSaveMsg(null);
    try {
      const body = mode === 'general'
        ? { mode: 'general' as const, provider_name: generalProvider || null, general_model: generalModel || null, key_id: generalKeyId }
        : {
            mode: 'custom' as const,
            custom_provider: customProvider || null,
            custom_model: customModel || null,
            custom_base_url: customBaseUrl || null,
            custom_system_prompt: customSystemPrompt || null,
            ...(customApiKey ? { custom_api_key: customApiKey } : {}),
          };
      const updated = await themApi.putTenantSystemAgentConfig(role, body);
      setCfg(updated);
      setCustomApiKey('');
      setSaveMsg({ ok: true, text: 'Saved' });
    } catch (e: unknown) {
      setSaveMsg({ ok: false, text: e instanceof Error ? e.message : 'Save failed' });
    } finally {
      setSaving(false);
    }
  }

  async function handleTest() {
    if (!customProvider || !customModel) return;
    setTestState({ loading: true });
    try {
      const res = await themApi.testTenantSystemAgentLlm(role, {
        provider: customProvider,
        model: customModel,
        api_key: customApiKey || undefined,
        base_url: customBaseUrl || undefined,
      });
      setTestState({ loading: false, ok: res.ok, error: res.error });
    } catch (e: unknown) {
      setTestState({ loading: false, ok: false, error: e instanceof Error ? e.message : 'Test failed' });
    }
  }

  const canTest = !!(customProvider && customModel);

  if (loading) {
    return (
      <div style={{ padding: '40px', textAlign: 'center', color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>Loading…</div>
    );
  }

  return (
    <div style={{
      background: 'linear-gradient(160deg, rgba(255,255,255,0.028) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%), var(--tm-card)',
      border: '1px solid var(--tm-card-border)', borderRadius: '18px', padding: '28px 32px',
      backdropFilter: 'blur(12px)',
      boxShadow: '0 8px 32px rgba(0,0,0,0.4), 0 2px 8px rgba(0,0,0,0.25), inset 0 1px 0 rgba(255,255,255,0.04)',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '14px', marginBottom: '20px' }}>
        <div style={{
          width: '44px', height: '44px', borderRadius: '12px', flexShrink: 0,
          background: 'radial-gradient(circle at 30% 25%, rgba(0,209,255,0.18), transparent 65%), linear-gradient(145deg, rgba(20,32,52,0.96), rgba(8,16,30,0.96))',
          border: '1px solid rgba(0,209,255,0.35)',
          boxShadow: '0 0 16px rgba(0,209,255,0.12), inset 0 1px 0 var(--tm-card-border)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: '22px', color: '#00d1ff' }}>psychology</span>
        </div>
        <div>
          <h3 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-text)', margin: '0 0 4px 0', letterSpacing: '-0.01em' }}>
            {getRoleLabel(role)}
          </h3>
          <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: 0, lineHeight: 1.4 }}>
            {getRoleDescription(role)}
          </p>
          {getRoleWhereUsed(role) && (
            <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', margin: '6px 0 0 0', lineHeight: 1.4, opacity: 0.7, display: 'flex', alignItems: 'flex-start', gap: '5px' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '14px', flexShrink: 0, marginTop: '1px' }}>info</span>
              {getRoleWhereUsed(role)}
            </p>
          )}
        </div>
      </div>

      <div style={{ height: '1px', background: 'rgba(132,157,188,.1)', marginBottom: '20px' }} />

      <div style={{ display: 'flex', gap: '4px', background: 'rgba(0,0,0,0.2)', borderRadius: '10px', padding: '4px', marginBottom: '22px' }}>
        <button style={segBtnStyle(mode === 'general')} onClick={() => { setMode('general'); setSaveMsg(null); }}>
          General settings
        </button>
        <button style={segBtnStyle(mode === 'custom')} onClick={() => { setMode('custom'); setSaveMsg(null); }}>
          Custom
        </button>
      </div>

      {mode === 'general' ? (
        <GeneralModePicker
          isSuperAdmin={false}
          provider={generalProvider}
          model={generalModel}
          keyId={generalKeyId}
          onProviderChange={(p) => { setGeneralProvider(p); setSaveMsg(null); }}
          onModelChange={(m) => { setGeneralModel(m); setSaveMsg(null); }}
          onKeyIdChange={(k) => { setGeneralKeyId(k); setSaveMsg(null); }}
        />
      ) : (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
            <Field label="Provider">
              <input style={inputStyle} value={customProvider} onChange={(e) => setCustomProvider(e.target.value)} placeholder="anthropic" />
            </Field>
            <Field label="Model">
              <input style={inputStyle} value={customModel} onChange={(e) => setCustomModel(e.target.value)} placeholder="model-id" />
            </Field>
          </div>

          <Field label={cfg?.custom_api_key_masked ? `API Key (current: …${cfg.custom_api_key_masked})` : 'API Key'} hint={cfg?.custom_api_key_masked ? 'Leave blank to keep the current key.' : undefined}>
            <input style={inputStyle} type="password" value={customApiKey} onChange={(e) => setCustomApiKey(e.target.value)} placeholder={cfg?.custom_api_key_masked ? '••••••••  (leave blank to keep)' : 'sk-…'} autoComplete="new-password" />
          </Field>

          <Field label="Base URL" hint="Optional — leave blank for the provider default.">
            <input style={inputStyle} value={customBaseUrl} onChange={(e) => setCustomBaseUrl(e.target.value)} placeholder="https://api.example.com/v1" />
          </Field>

          <Field label="System Prompt">
            <textarea style={{ ...inputStyle, minHeight: '100px', resize: 'vertical', fontFamily: 'monospace', fontSize: '12px', lineHeight: 1.5 }} value={customSystemPrompt} onChange={(e) => setCustomSystemPrompt(e.target.value)} placeholder={getRolePromptPlaceholder(role)} />
          </Field>

          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '4px' }}>
            <button onClick={handleTest} disabled={testState.loading || !canTest} style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '8px 18px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', color: canTest ? 'var(--tm-text)' : 'var(--tm-text-muted)', cursor: (testState.loading || !canTest) ? 'not-allowed' : 'pointer', fontSize: '13px', fontWeight: 600, opacity: canTest ? 1 : 0.5 }} title={!canTest ? 'Enter a provider and model first' : undefined}>
              <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>bolt</span>
              {testState.loading ? 'Testing…' : 'Test'}
            </button>
            {!testState.loading && testState.ok !== undefined && (
              testState.ok
                ? <span style={{ fontSize: '13px', color: '#4edea3', fontWeight: 600 }}>Connected</span>
                : <span style={{ fontSize: '13px', color: '#f87171' }}>{testState.error ?? 'Connection failed'}</span>
            )}
          </div>
        </>
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginTop: '18px' }}>
        <div style={{ flex: 1 }} />
        {saveMsg && <span style={{ fontSize: '13px', fontWeight: 600, color: saveMsg.ok ? '#4edea3' : '#f87171' }}>{saveMsg.text}</span>}
        <button onClick={handleSave} disabled={saving} style={{ padding: '8px 22px', borderRadius: '9px', border: 'none', background: saving ? 'rgba(99,102,241,.5)' : 'var(--tm-accent)', color: '#fff', cursor: saving ? 'not-allowed' : 'pointer', fontSize: '14px', fontWeight: 600, opacity: saving ? 0.7 : 1 }}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  );
}
