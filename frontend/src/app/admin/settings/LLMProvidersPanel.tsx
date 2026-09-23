'use client';
import { useEffect, useState } from 'react';
import { themApi, type LLMProviderOut, type LLMProviderUpsertInput, type LLMProviderKeyOut } from '@/lib/api';
import { PROVIDER_MODELS } from './settingsConstants';
import { LLMProviderKeysPanel } from './LLMProviderKeysPanel';

function ProviderCard({
  prov,
  isSuperAdmin,
  onSaveKey,
  saving,
  saveMsg,
}: {
  prov: LLMProviderOut;
  isSuperAdmin: boolean;
  onSaveKey: (apiKey: string) => void;
  saving: boolean;
  saveMsg: { ok: boolean; text: string } | null;
}) {
  const [apiKey, setApiKey] = useState('');
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
    if (!isSuperAdmin) loadKeys();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [prov.name]);

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

  return (
    <div style={{ background: 'var(--tm-card-bg)', border: '1px solid var(--tm-card-border)', borderRadius: '12px', padding: '20px 24px', marginBottom: '16px' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '12px' }}>
        <div>
          <span style={{ fontWeight: 700, fontSize: '15px', color: '#fff' }}>{prov.display_name || prov.name}</span>
          <span style={{ marginLeft: '10px', fontSize: '12px', padding: '2px 8px', borderRadius: '20px', background: prov.enabled ? 'rgba(74,192,136,0.12)' : 'rgba(132,157,188,0.1)', color: prov.enabled ? '#4ac088' : 'var(--tm-text-muted)' }}>
            {prov.enabled ? 'enabled' : 'disabled'}
          </span>
        </div>
        {isSuperAdmin && (
          <span style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)' }}>
            {prov.api_key_set
              ? (prov.api_key_masked ? `Key: ${prov.api_key_masked}` : 'Key set')
              : 'No key set'}
          </span>
        )}
      </div>

      {isSuperAdmin && (
        <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
          <input
            type="password"
            placeholder="New API key (leave blank to keep current)"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            style={{ flex: 1, background: 'var(--tm-input-bg, rgba(0,0,0,0.3))', border: '1px solid rgba(132,157,188,0.2)', borderRadius: '8px', padding: '8px 12px', color: '#fff', fontSize: '13px' }}
          />
          <button
            onClick={() => { onSaveKey(apiKey); setApiKey(''); }}
            disabled={saving}
            style={{ padding: '8px 16px', background: 'var(--tm-accent)', border: 'none', borderRadius: '8px', color: '#fff', fontSize: '13px', fontWeight: 600, cursor: saving ? 'not-allowed' : 'pointer', opacity: saving ? 0.6 : 1 }}
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      )}
      {saveMsg && (
        <p style={{ margin: '6px 0 0', fontSize: '12px', color: saveMsg.ok ? '#4ac088' : '#e05252' }}>{saveMsg.text}</p>
      )}

      {!isSuperAdmin && (
        <>
          <div style={{ marginTop: '14px', paddingTop: '14px', borderTop: '1px solid rgba(132,157,188,.1)' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '10px' }}>
              <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
                Allowed models
              </div>
              <button
                onClick={handleRefresh}
                disabled={refreshing || !defaultKey}
                title={!defaultKey ? 'Add a key below first' : undefined}
                style={{ padding: '4px 10px', borderRadius: '6px', border: '1px solid var(--tm-border)', background: 'transparent', color: defaultKey ? 'var(--tm-text-muted)' : 'var(--tm-text-muted)', cursor: (refreshing || !defaultKey) ? 'not-allowed' : 'pointer', fontSize: '11px', fontWeight: 600, opacity: defaultKey ? 1 : 0.5 }}
              >
                {refreshing ? 'Refreshing…' : 'Refresh from provider'}
              </button>
            </div>
            {refreshErr && <p style={{ margin: '0 0 8px', fontSize: '12px', color: '#e05252' }}>{refreshErr}</p>}
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px', marginBottom: '10px' }}>
              {modelOptions.length === 0 && (
                <span style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)' }}>No known models yet — add a key and refresh.</span>
              )}
              {modelOptions.map((m) => (
                <label key={m} style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '12px', color: 'var(--tm-text)', padding: '4px 10px', borderRadius: '20px', background: allowedModels.includes(m) ? 'rgba(0,209,255,0.1)' : 'rgba(132,157,188,0.06)', cursor: 'pointer' }}>
                  <input type="checkbox" checked={allowedModels.includes(m)} onChange={() => toggleModel(m)} style={{ cursor: 'pointer' }} />
                  {m}
                </label>
              ))}
            </div>
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

export function LLMProvidersPanel({ isSuperAdmin }: { isSuperAdmin: boolean }) {
  const [providers, setProviders] = useState<LLMProviderOut[]>([]);
  const [provLoading, setProvLoading] = useState(false);
  const [provSaving, setProvSaving] = useState<Record<string, boolean>>({});
  const [provSaveMsgs, setProvSaveMsgs] = useState<Record<string, { ok: boolean; text: string } | null>>({});

  async function loadProviders() {
    setProvLoading(true);
    try {
      const list = isSuperAdmin
        ? await themApi.listPlatformProviders()
        : await themApi.listMyLLMProviders();
      setProviders(list);
    } catch {
      setProviders([]);
    } finally {
      setProvLoading(false);
    }
  }

  useEffect(() => {
    loadProviders();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isSuperAdmin]);

  async function handleSaveProvider(name: string, apiKey: string) {
    setProvSaving((p) => ({ ...p, [name]: true }));
    setProvSaveMsgs((p) => ({ ...p, [name]: null }));
    try {
      const prov = providers.find((p) => p.name === name);
      if (isSuperAdmin) {
        if (!prov?.id) throw new Error('provider id missing');
        await themApi.patchPlatformProvider(prov.id, {
          ...(apiKey ? { api_key: apiKey } : {}),
        });
      } else {
        const body: LLMProviderUpsertInput = {
          default_model: prov?.default_model ?? '',
          ...(apiKey ? { api_key: apiKey } : {}),
          enabled: prov?.enabled ?? true,
        };
        await themApi.upsertMyLLMProvider(name, body);
      }
      setProvSaveMsgs((p) => ({ ...p, [name]: { ok: true, text: 'Saved' } }));
      await loadProviders();
    } catch (e: unknown) {
      setProvSaveMsgs((p) => ({ ...p, [name]: { ok: false, text: e instanceof Error ? e.message : 'Save failed' } }));
    } finally {
      setProvSaving((p) => ({ ...p, [name]: false }));
    }
  }

  return (
    <>
      <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: '0 0 24px 0', lineHeight: 1.5 }}>
        {isSuperAdmin
          ? 'Platform-level LLM provider keys used by the-M itself for internal system operations.'
          : 'Your organisation\'s LLM provider keys. The-M never uses platform keys for your tenant calls — you must supply your own.'}
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
          isSuperAdmin={isSuperAdmin}
          onSaveKey={(apiKey) => handleSaveProvider(prov.name, apiKey)}
          saving={!!provSaving[prov.name]}
          saveMsg={provSaveMsgs[prov.name] ?? null}
        />
      ))}
    </>
  );
}
