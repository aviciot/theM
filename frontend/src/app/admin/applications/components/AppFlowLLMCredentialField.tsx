'use client';
import { useEffect, useState } from 'react';
import { themApi, type LLMProviderOut, type LLMProviderKeyOut } from '@/lib/api';
import { C } from '../constants';
import type { AppFlowRuntimeParamSpec, AppFlowLLMCredentialValue } from '../types';

// AppFlowLLMCredentialField — one per LLM node declaring a runtime
// llm_credential param (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md). Compact inline
// version of TenantRoleCard.tsx's General/Custom picker, scoped to a single
// canvas node rather than a whole settings card — this is what makes the
// debug panel's picker per-node instead of one shared selection.

const inputStyle: React.CSSProperties = {
  background: 'rgba(0,0,0,0.3)', border: `1px solid ${C.outline}`, borderRadius: 6,
  color: C.text, padding: '5px 8px', outline: 'none', fontSize: '11px',
};

const segBtnStyle = (active: boolean): React.CSSProperties => ({
  padding: '3px 10px', borderRadius: 5, border: 'none', cursor: 'pointer',
  fontSize: '10px', fontWeight: 700,
  background: active ? 'rgba(245,158,11,0.25)' : 'transparent',
  color: active ? C.amber : C.textMuted,
});

export function AppFlowLLMCredentialField({
  spec,
  value,
  disabled,
  onChange,
}: {
  spec: AppFlowRuntimeParamSpec;
  value: AppFlowLLMCredentialValue | undefined;
  disabled?: boolean;
  onChange: (value: AppFlowLLMCredentialValue) => void;
}) {
  const mode = value?.mode ?? 'general';
  const [providers, setProviders] = useState<LLMProviderOut[]>([]);
  const [providerKeys, setProviderKeys] = useState<LLMProviderKeyOut[]>([]);
  const [keysLoading, setKeysLoading] = useState(false);

  useEffect(() => {
    themApi.listMyLLMProviders().then(provs => setProviders(provs.filter(p => p.enabled))).catch(() => setProviders([]));
  }, []);

  const generalProvider = value?.mode === 'general' ? value.provider : '';
  useEffect(() => {
    if (mode !== 'general' || !generalProvider) { setProviderKeys([]); return; }
    setKeysLoading(true);
    themApi.listProviderKeys(generalProvider)
      .then(keys => setProviderKeys(keys))
      .catch(() => setProviderKeys([]))
      .finally(() => setKeysLoading(false));
  }, [mode, generalProvider]);

  function setMode(next: 'general' | 'custom') {
    onChange(next === 'general'
      ? { mode: 'general', provider: '', keyId: null, model: '' }
      : { mode: 'custom', provider: '', model: '', apiKey: '', baseUrl: '' });
  }

  // The provider row's own allowed_models (empty = no allow-list configured,
  // any model is accepted server-side too — resolveLLMOverride's behavior).
  const selectedProvider = mode === 'general'
    ? providers.find(p => p.name === generalProvider)
    : undefined;
  const allowedModels = selectedProvider?.allowed_models ?? [];

  return (
    <div style={{
      display: 'flex', flexDirection: 'column', gap: 4, padding: '6px 8px',
      background: 'rgba(0,0,0,0.15)', borderRadius: 6, border: `1px solid ${C.outline}`,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <label style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>
          {spec.nodeLabel || spec.nodeId}
        </label>
        <div style={{ display: 'flex', gap: 2, background: 'rgba(0,0,0,0.2)', borderRadius: 6, padding: 2 }}>
          <button type="button" disabled={disabled} style={segBtnStyle(mode === 'general')} onClick={() => setMode('general')}>General</button>
          <button type="button" disabled={disabled} style={segBtnStyle(mode === 'custom')} onClick={() => setMode('custom')}>Custom</button>
        </div>
      </div>

      {mode === 'general' ? (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          <select
            value={value?.mode === 'general' ? value.provider : ''}
            disabled={disabled}
            onChange={e => onChange({ mode: 'general', provider: e.target.value, keyId: null, model: '' })}
            style={{ ...inputStyle, width: 130 }}
          >
            <option value="">— provider —</option>
            {providers.map(p => <option key={p.name} value={p.name}>{p.display_name || p.name}</option>)}
          </select>
          <select
            value={value?.mode === 'general' ? value.keyId ?? '' : ''}
            disabled={disabled || !generalProvider || keysLoading}
            onChange={e => onChange({ mode: 'general', provider: generalProvider, keyId: e.target.value ? Number(e.target.value) : null, model: value?.mode === 'general' ? value.model : '' })}
            style={{ ...inputStyle, width: 130 }}
          >
            <option value="">— default key —</option>
            {providerKeys.map(k => <option key={k.id} value={k.id}>{k.name}{k.is_default ? ' (default)' : ''}</option>)}
          </select>
          {allowedModels.length > 0 ? (
            <select
              value={value?.mode === 'general' ? value.model : ''}
              disabled={disabled || !generalProvider}
              onChange={e => onChange({ mode: 'general', provider: generalProvider, keyId: value?.mode === 'general' ? value.keyId : null, model: e.target.value })}
              style={{ ...inputStyle, width: 140 }}
              title="Model — restricted to this provider's allowed models"
            >
              <option value="">— default model —</option>
              {allowedModels.map(m => <option key={m} value={m}>{m}</option>)}
            </select>
          ) : (
            <input
              placeholder={generalProvider ? 'model (default: ' + (selectedProvider?.default_model ?? '') + ')' : 'model'}
              disabled={disabled || !generalProvider}
              value={value?.mode === 'general' ? value.model : ''}
              onChange={e => onChange({ mode: 'general', provider: generalProvider, keyId: value?.mode === 'general' ? value.keyId : null, model: e.target.value })}
              style={{ ...inputStyle, width: 140 }}
              title="No allowed-models list configured for this provider — any model is accepted"
            />
          )}
        </div>
      ) : (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          <input
            placeholder="provider" disabled={disabled}
            value={value?.mode === 'custom' ? value.provider : ''}
            onChange={e => onChange({ mode: 'custom', provider: e.target.value, model: value?.mode === 'custom' ? value.model : '', apiKey: value?.mode === 'custom' ? value.apiKey : '', baseUrl: value?.mode === 'custom' ? value.baseUrl : '' })}
            style={{ ...inputStyle, width: 90 }}
          />
          <input
            placeholder="model" disabled={disabled}
            value={value?.mode === 'custom' ? value.model : ''}
            onChange={e => onChange({ mode: 'custom', provider: value?.mode === 'custom' ? value.provider : '', model: e.target.value, apiKey: value?.mode === 'custom' ? value.apiKey : '', baseUrl: value?.mode === 'custom' ? value.baseUrl : '' })}
            style={{ ...inputStyle, width: 100 }}
          />
          <input
            placeholder="API key" type="password" autoComplete="new-password" disabled={disabled}
            value={value?.mode === 'custom' ? value.apiKey : ''}
            onChange={e => onChange({ mode: 'custom', provider: value?.mode === 'custom' ? value.provider : '', model: value?.mode === 'custom' ? value.model : '', apiKey: e.target.value, baseUrl: value?.mode === 'custom' ? value.baseUrl : '' })}
            style={{ ...inputStyle, width: 110 }}
          />
          <input
            placeholder="base URL (optional)" disabled={disabled}
            value={value?.mode === 'custom' ? value.baseUrl : ''}
            onChange={e => onChange({ mode: 'custom', provider: value?.mode === 'custom' ? value.provider : '', model: value?.mode === 'custom' ? value.model : '', apiKey: value?.mode === 'custom' ? value.apiKey : '', baseUrl: e.target.value })}
            style={{ ...inputStyle, width: 160 }}
            title="Endpoint URL for this node's request — leave blank to use the provider's default"
          />
        </div>
      )}
    </div>
  );
}
