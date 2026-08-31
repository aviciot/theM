'use client';
import { useState } from 'react';
import { themApi } from '@/lib/api';
import {
  PROVIDERS, PROVIDER_MODELS, CUSTOM_MODEL_SENTINEL,
  getRoleLabel, getRoleDescription, getRolePromptPlaceholder, getRoleWhereUsed,
  inputStyle,
} from './settingsConstants';

export interface RoleForm {
  enabled: boolean;
  provider: string;
  model: string;
  api_key: string;
  base_url: string;
  system_prompt: string;
}

interface TestState {
  loading: boolean;
  ok?: boolean;
  latency?: number;
  error?: string;
}

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

export function Toggle({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      onClick={() => onChange(!value)}
      style={{
        display: 'flex', alignItems: 'center', gap: '10px',
        padding: '8px 16px', borderRadius: '9px', border: 'none',
        cursor: 'pointer', fontSize: '14px', fontWeight: 600,
        background: value ? 'rgba(16,185,129,0.15)' : 'rgba(100,116,139,0.15)',
        color: value ? '#34d399' : 'var(--tm-card-text-muted)',
        transition: 'all 0.18s',
      }}
    >
      <span style={{
        width: '32px', height: '18px', borderRadius: '9px', flexShrink: 0,
        background: value ? '#34d399' : '#475569',
        position: 'relative', display: 'inline-block',
        transition: 'background 0.18s',
      }}>
        <span style={{ position: 'absolute', top: '3px', left: value ? '17px' : '3px', width: '12px', height: '12px', borderRadius: '50%', background: '#fff', transition: 'left 0.18s' }} />
      </span>
      {value ? 'Enabled' : 'Disabled'}
    </button>
  );
}

export function RoleCard({
  role,
  apiKeyHint,
  form,
  onChange,
  onSave,
  saving,
  saveMsg,
}: {
  role: string;
  apiKeyHint: string | null;
  form: RoleForm;
  onChange: (patch: Partial<RoleForm>) => void;
  onSave: () => void;
  saving: boolean;
  saveMsg: { ok: boolean; text: string } | null;
}) {
  const [testState, setTestState] = useState<TestState>({ loading: false });
  const knownModels = PROVIDER_MODELS[form.provider] ?? [];
  const isKnownModel = knownModels.some((m) => m.value === form.model);
  const [showCustom, setShowCustom] = useState(!isKnownModel && form.model !== '');

  function handleProviderChange(provider: string) {
    const models = PROVIDER_MODELS[provider] ?? [];
    onChange({ provider, model: models[0]?.value ?? '' });
    setShowCustom(false);
  }

  function handleModelSelectChange(val: string) {
    if (val === CUSTOM_MODEL_SENTINEL) {
      setShowCustom(true);
      onChange({ model: '' });
    } else {
      setShowCustom(false);
      onChange({ model: val });
    }
  }

  async function handleTest() {
    if (!form.provider || !form.model) return;
    setTestState({ loading: true });
    try {
      const res = await themApi.testSystemAgentLlm(role, {
        provider: form.provider,
        model:    form.model,
        api_key:  form.api_key || undefined,
        base_url: form.base_url || undefined,
      });
      setTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: unknown) {
      setTestState({ loading: false, ok: false, error: e instanceof Error ? e.message : 'Test failed' });
    }
  }

  const canTest = !!(form.provider && form.model);

  return (
    <div style={{
      background: 'linear-gradient(160deg, rgba(255,255,255,0.028) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%), var(--tm-card)',
      border: '1px solid var(--tm-card-border)', borderRadius: '18px', padding: '28px 32px',
      backdropFilter: 'blur(12px)',
      boxShadow: '0 8px 32px rgba(0,0,0,0.4), 0 2px 8px rgba(0,0,0,0.25), inset 0 1px 0 rgba(255,255,255,0.04)',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '20px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '14px' }}>
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
        <Toggle value={form.enabled} onChange={(v) => onChange({ enabled: v })} />
      </div>

      <div style={{ height: '1px', background: 'rgba(132,157,188,.1)', marginBottom: '22px' }} />

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
        <Field label="Provider">
          <select value={form.provider} onChange={(e) => handleProviderChange(e.target.value)} style={{ ...inputStyle, appearance: 'none', cursor: 'pointer' }}>
            {PROVIDERS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
          </select>
        </Field>
        <Field label="Model">
          {knownModels.length > 0 && !showCustom ? (
            <select value={isKnownModel ? form.model : CUSTOM_MODEL_SENTINEL} onChange={(e) => handleModelSelectChange(e.target.value)} style={{ ...inputStyle, appearance: 'none', cursor: 'pointer' }}>
              {knownModels.map((m) => <option key={m.value} value={m.value}>{m.label}</option>)}
              <option value={CUSTOM_MODEL_SENTINEL}>Custom…</option>
            </select>
          ) : (
            <div style={{ display: 'flex', gap: '6px' }}>
              <input style={{ ...inputStyle, flex: 1 }} value={form.model} onChange={(e) => onChange({ model: e.target.value })} placeholder="model-id" autoFocus={showCustom} />
              {knownModels.length > 0 && (
                <button type="button" onClick={() => { setShowCustom(false); onChange({ model: knownModels[0].value }); }} title="Pick from list" style={{ padding: '0 10px', borderRadius: '8px', border: '1px solid var(--tm-input-border)', background: 'var(--tm-inset)', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: '12px', flexShrink: 0 }}>
                  ↩ List
                </button>
              )}
            </div>
          )}
        </Field>
      </div>

      <Field label={apiKeyHint ? `API Key (current: …${apiKeyHint})` : 'API Key'} hint={apiKeyHint ? 'Leave blank to keep the current key.' : undefined}>
        <input style={inputStyle} type="password" value={form.api_key} onChange={(e) => onChange({ api_key: e.target.value })} placeholder={apiKeyHint ? '••••••••  (leave blank to keep)' : 'sk-…'} autoComplete="new-password" />
      </Field>

      <Field label="Base URL" hint="Optional — leave blank for the provider default.">
        <input style={inputStyle} value={form.base_url} onChange={(e) => onChange({ base_url: e.target.value })} placeholder="https://api.example.com/v1" />
      </Field>

      <Field label="System Prompt">
        <textarea style={{ ...inputStyle, minHeight: '100px', resize: 'vertical', fontFamily: 'monospace', fontSize: '12px', lineHeight: 1.5 }} value={form.system_prompt} onChange={(e) => onChange({ system_prompt: e.target.value })} placeholder={getRolePromptPlaceholder(role)} />
      </Field>

      <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginTop: '8px', flexWrap: 'wrap' }}>
        <button onClick={handleTest} disabled={testState.loading || !canTest} style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '8px 18px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', color: canTest ? 'var(--tm-text)' : 'var(--tm-text-muted)', cursor: (testState.loading || !canTest) ? 'not-allowed' : 'pointer', fontSize: '13px', fontWeight: 600, opacity: canTest ? 1 : 0.5 }} title={!canTest ? 'Select a provider and model first' : undefined}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>bolt</span>
          {testState.loading ? 'Testing…' : 'Test'}
        </button>

        {!testState.loading && testState.ok !== undefined && (
          testState.ok
            ? <span style={{ fontSize: '13px', color: '#4edea3', fontWeight: 600 }}>Connected ({testState.latency}ms)</span>
            : <span style={{ fontSize: '13px', color: '#f87171' }}>{testState.error ?? 'Connection failed'}</span>
        )}

        <div style={{ flex: 1 }} />
        {saveMsg && <span style={{ fontSize: '13px', fontWeight: 600, color: saveMsg.ok ? '#4edea3' : '#f87171' }}>{saveMsg.text}</span>}

        <button onClick={onSave} disabled={saving} style={{ padding: '8px 22px', borderRadius: '9px', border: 'none', background: saving ? 'rgba(99,102,241,.5)' : 'var(--tm-accent)', color: '#fff', cursor: saving ? 'not-allowed' : 'pointer', fontSize: '14px', fontWeight: 600, opacity: saving ? 0.7 : 1 }}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  );
}
