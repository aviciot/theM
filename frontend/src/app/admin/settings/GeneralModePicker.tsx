'use client';
import { useEffect, useState } from 'react';
import { themApi, type LLMProviderOut, type LLMProviderKeyOut } from '@/lib/api';
import { inputStyle } from './settingsConstants';

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

/**
 * Shared "General mode" picker for system-agent roles (classifier,
 * card_synthesizer, security_scanner) — one bank of Provider → Model →
 * Named Key, sourced from either the tenant's own LLM Providers config or
 * the platform's (isSuperAdmin). Used by TenantRoleCard and RoleCard so the
 * fetch/select logic lives in exactly one place; runtime wiring can reuse
 * this same component later.
 */
export function GeneralModePicker({
  isSuperAdmin,
  provider,
  model,
  keyId,
  onProviderChange,
  onModelChange,
  onKeyIdChange,
}: {
  isSuperAdmin: boolean;
  provider: string;
  model: string | null;
  keyId: number | null;
  onProviderChange: (provider: string) => void;
  onModelChange: (model: string) => void;
  onKeyIdChange: (keyId: number | null) => void;
}) {
  const [providers, setProviders] = useState<LLMProviderOut[]>([]);
  const [keys, setKeys] = useState<LLMProviderKeyOut[]>([]);
  const [keysLoading, setKeysLoading] = useState(false);

  useEffect(() => {
    const load = isSuperAdmin ? themApi.listPlatformProviders() : themApi.listMyLLMProviders();
    load.then((provs) => setProviders(provs.filter((p) => p.enabled))).catch(() => setProviders([]));
  }, [isSuperAdmin]);

  useEffect(() => {
    if (!provider) {
      setKeys([]);
      return;
    }
    setKeysLoading(true);
    const load = isSuperAdmin ? themApi.listPlatformProviderKeys(provider) : themApi.listProviderKeys(provider);
    load.then((k) => setKeys(k)).catch(() => setKeys([])).finally(() => setKeysLoading(false));
  }, [isSuperAdmin, provider]);

  const selectedProvider = providers.find((p) => p.name === provider);
  const allowedModels = selectedProvider?.allowed_models ?? [];

  function handleProviderChange(name: string) {
    onProviderChange(name);
    onModelChange('');
    onKeyIdChange(null);
  }

  return (
    <>
      <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', margin: '0 0 16px 0', lineHeight: 1.5 }}>
        Uses {isSuperAdmin ? "the-M's own platform" : 'your'} LLM Providers configuration — pick a
        provider, one of its allowed models, and a named key. Test the key itself from the LLM
        Providers tab.
      </p>
      {providers.length === 0 && (
        <div style={{ padding: '12px 16px', borderRadius: '8px', background: 'rgba(230,184,92,0.08)', border: '1px solid rgba(230,184,92,0.22)', color: '#e6b85c', fontSize: '13px', marginBottom: '16px' }}>
          No {isSuperAdmin ? 'platform ' : ''}providers enabled yet — enable one in the LLM Providers tab first.
        </div>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
        <Field label="Provider">
          <select value={provider} onChange={(e) => handleProviderChange(e.target.value)} style={{ ...inputStyle, appearance: 'none', cursor: 'pointer' }}>
            <option value="">— select provider —</option>
            {providers.map((p) => <option key={p.name} value={p.name}>{p.display_name || p.name}</option>)}
          </select>
        </Field>
        <Field
          label="Model"
          hint={provider && allowedModels.length === 0 ? 'No allowed models set for this provider yet — set them in the LLM Providers tab.' : undefined}
        >
          <select
            value={model ?? ''}
            onChange={(e) => onModelChange(e.target.value)}
            disabled={!provider || allowedModels.length === 0}
            style={{ ...inputStyle, appearance: 'none', cursor: 'pointer' }}
          >
            <option value="">— use provider default —</option>
            {allowedModels.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </Field>
      </div>
      <Field label="Key" hint={keysLoading ? 'Loading keys…' : (keys.length === 0 && provider ? 'No named keys saved for this provider yet.' : undefined)}>
        <select
          value={keyId ?? ''}
          onChange={(e) => onKeyIdChange(e.target.value ? Number(e.target.value) : null)}
          disabled={!provider || keysLoading}
          style={{ ...inputStyle, appearance: 'none', cursor: 'pointer' }}
        >
          <option value="">— use default key —</option>
          {keys.map((k) => (
            <option key={k.id} value={k.id}>{k.name}{k.is_default ? ' (default)' : ''}</option>
          ))}
        </select>
      </Field>
    </>
  );
}
