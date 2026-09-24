'use client';
import { useEffect, useState } from 'react';
import { themApi, type LLMProviderOut, type LLMProviderUpsertInput, type LLMProviderKeyOut } from '@/lib/api';
import { PROVIDER_MODELS } from './settingsConstants';
import { LLMProviderKeysPanel } from './LLMProviderKeysPanel';
import { Toggle } from './Toggle';

function ModelRow({ label, checked, onChange }: { label: string; checked: boolean; onChange: () => void }) {
  return (
    <label
      style={{
        display: 'flex', alignItems: 'center', gap: '10px',
        padding: '9px 12px', borderRadius: '8px', cursor: 'pointer',
        background: checked ? 'rgba(0,209,255,0.06)' : 'transparent',
        border: `1px solid ${checked ? 'rgba(0,209,255,0.25)' : 'transparent'}`,
        transition: 'background 0.12s, border-color 0.12s',
      }}
    >
      <input type="checkbox" checked={checked} onChange={onChange} style={{ cursor: 'pointer', width: '15px', height: '15px', flexShrink: 0, accentColor: 'var(--tm-accent)' }} />
      <span style={{ fontSize: '13px', color: checked ? '#fff' : 'var(--tm-text-muted)', fontWeight: checked ? 600 : 400 }}>{label}</span>
    </label>
  );
}

function ProviderCard({
  prov,
  onToggleEnabled,
}: {
  prov: LLMProviderOut;
  onToggleEnabled: (enabled: boolean) => void;
}) {
  const [allowedModels, setAllowedModels] = useState<string[]>(prov.allowed_models ?? []);
  const [modelOptions, setModelOptions] = useState<string[]>(PROVIDER_MODELS[prov.name]?.map((m) => m.value) ?? []);
  const [savingModels, setSavingModels] = useState(false);
  const [modelsMsg, setModelsMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshErr, setRefreshErr] = useState<string | null>(null);

  const [keys, setKeys] = useState<LLMProviderKeyOut[]>([]);
  const [keysLoaded, setKeysLoaded] = useState(false);
  const defaultKey = keys.find((k) => k.is_default) ?? keys[0];

  useEffect(() => {
    setAllowedModels(prov.allowed_models ?? []);
  }, [prov.allowed_models]);

  async function loadKeys() {
    try {
      const list = await themApi.listProviderKeys(prov.name);
      setKeys(list);
    } catch {
      setKeys([]);
    } finally {
      setKeysLoaded(true);
    }
  }

  useEffect(() => {
    if (prov.enabled) loadKeys();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [prov.name, prov.enabled]);

  function toggleModel(model: string) {
    setAllowedModels((prev) => prev.includes(model) ? prev.filter((m) => m !== model) : [...prev, model]);
    setModelsMsg(null);
  }

  async function handleSaveModels() {
    setSavingModels(true);
    setModelsMsg(null);
    try {
      const body: LLMProviderUpsertInput = {
        default_model: prov.default_model,
        enabled: prov.enabled,
        allowed_models: allowedModels,
      };
      await themApi.upsertMyLLMProvider(prov.name, body);
      setModelsMsg({ ok: true, text: 'Saved' });
    } catch (e: unknown) {
      setModelsMsg({ ok: false, text: e instanceof Error ? e.message : 'Save failed' });
    } finally {
      setSavingModels(false);
    }
  }

  async function handleRefresh() {
    if (!defaultKey) return;
    setRefreshing(true);
    setRefreshErr(null);
    try {
      const res = await themApi.listAvailableModels(prov.name, defaultKey.id);
      setModelOptions(res.models);
    } catch (e: unknown) {
      setRefreshErr(e instanceof Error ? e.message : 'Refresh failed');
    } finally {
      setRefreshing(false);
    }
  }

  const showKeysBody = prov.enabled;

  return (
    <div style={{ background: 'var(--tm-card-bg)', border: '1px solid var(--tm-card-border)', borderRadius: '12px', padding: '20px 24px', marginBottom: '16px' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '16px' }}>
        <div>
          <span style={{ fontWeight: 700, fontSize: '15px', color: '#fff' }}>{prov.display_name || prov.name}</span>
        </div>
        <Toggle value={prov.enabled} onChange={onToggleEnabled} />
      </div>

      {!prov.enabled && (
        <p style={{ margin: '10px 0 0', fontSize: '12px', color: 'var(--tm-card-text-muted)' }}>
          Turn this on to choose which models are allowed and manage named API keys.
        </p>
      )}

      {showKeysBody && (
        <>
          <div style={{ marginTop: '18px', paddingTop: '18px', borderTop: '1px solid rgba(132,157,188,.1)' }}>
            <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: '10px' }}>
              <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
                Allowed models
              </div>
              <button
                onClick={handleRefresh}
                disabled={refreshing || !defaultKey}
                title={!defaultKey ? 'Add a key below first' : undefined}
                style={{ padding: '4px 10px', borderRadius: '6px', border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text-muted)', cursor: (refreshing || !defaultKey) ? 'not-allowed' : 'pointer', fontSize: '11px', fontWeight: 600, opacity: defaultKey ? 1 : 0.5 }}
              >
                {refreshing ? 'Refreshing…' : 'Refresh from provider'}
              </button>
            </div>
            {refreshErr && <p style={{ margin: '0 0 8px', fontSize: '12px', color: '#e05252' }}>{refreshErr}</p>}

            {modelOptions.length === 0 ? (
              <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', margin: '0 0 12px' }}>No known models yet — add a key below, then refresh.</p>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '2px', marginBottom: '14px' }}>
                {modelOptions.map((m) => (
                  <ModelRow key={m} label={m} checked={allowedModels.includes(m)} onChange={() => toggleModel(m)} />
                ))}
              </div>
            )}

            <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
              <button
                onClick={handleSaveModels}
                disabled={savingModels}
                style={{ padding: '6px 14px', background: 'var(--tm-accent)', border: 'none', borderRadius: '6px', color: '#fff', fontSize: '12px', fontWeight: 600, cursor: savingModels ? 'not-allowed' : 'pointer', opacity: savingModels ? 0.6 : 1 }}
              >
                {savingModels ? 'Saving…' : 'Save allowed models'}
              </button>
              {modelsMsg && <span style={{ fontSize: '12px', color: modelsMsg.ok ? '#4ac088' : '#e05252' }}>{modelsMsg.text}</span>}
            </div>
          </div>

          {keysLoaded && (
            <LLMProviderKeysPanel providerName={prov.name} keys={keys} onChanged={loadKeys} />
          )}
        </>
      )}
    </div>
  );
}

export function LLMProvidersPanel() {
  const [providers, setProviders] = useState<LLMProviderOut[]>([]);
  const [provLoading, setProvLoading] = useState(false);

  async function loadProviders() {
    setProvLoading(true);
    try {
      const list = await themApi.listMyLLMProviders();
      setProviders(list);
    } catch {
      setProviders([]);
    } finally {
      setProvLoading(false);
    }
  }

  useEffect(() => {
    loadProviders();
  }, []);

  async function handleToggleEnabled(name: string, enabled: boolean) {
    const prov = providers.find((p) => p.name === name);
    // Optimistic update so the toggle feels instant.
    setProviders((prev) => prev.map((p) => p.name === name ? { ...p, enabled } : p));
    try {
      await themApi.upsertMyLLMProvider(name, {
        default_model: prov?.default_model ?? '',
        enabled,
      });
    } catch {
      // Revert on failure.
      setProviders((prev) => prev.map((p) => p.name === name ? { ...p, enabled: !enabled } : p));
    }
  }

  return (
    <>
      <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: '0 0 24px 0', lineHeight: 1.5 }}>
        Your organisation&apos;s LLM providers. The-M never uses platform keys for your tenant calls — you must supply your own.
      </p>
      {provLoading && <div style={{ padding: '40px', textAlign: 'center', color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>Loading…</div>}
      {!provLoading && providers.length === 0 && (
        <div style={{ padding: '16px', borderRadius: '10px', background: 'rgba(132,157,188,0.06)', color: 'var(--tm-card-text-muted)', fontSize: '13px' }}>
          No LLM providers configured.
        </div>
      )}
      {!provLoading && providers.map((prov) => (
        <ProviderCard
          key={prov.name}
          prov={prov}
          onToggleEnabled={(enabled) => handleToggleEnabled(prov.name, enabled)}
        />
      ))}
    </>
  );
}
