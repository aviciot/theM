'use client';
import { useState, useEffect } from 'react';
import { themApi, type Application, type AgentParamsResponse, type AppGlobalParam, type AgentLLMNodeStatus } from '@/lib/api';
import { C, glass, PROVIDER_LIST, RUNTIME_MODELS } from '../constants';

// ── RuntimeView ───────────────────────────────────────────────────────────────
export function RuntimeView({ app, onBack, onOrchSaved }: { app: Application; onBack: () => void; onOrchSaved?: (orchId: string, provider: string, model: string) => void }) {
  const emptyRuntime = { max_concurrent_sessions: null, rate_limit_rpm: null, blocked_tokens: [], blocked_user_ids: [], session_timeout_minutes: null };
  const [cfg, setCfg] = useState<import('@/lib/api').AppRuntimeConfig>(app.runtime_config ?? emptyRuntime);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Tag input helpers
  const [tokensInput, setTokensInput] = useState((app.runtime_config?.blocked_tokens ?? []).join('\n'));
  const [usersInput, setUsersInput] = useState((app.runtime_config?.blocked_user_ids ?? []).join(', '));

  // Provider keys state
  type KeyStatus = { provider: string; key_set: boolean; key_hint?: string };
  const [keyStatuses, setKeyStatuses] = useState<KeyStatus[]>([]);
  const [keyInputs, setKeyInputs] = useState<Record<string, string>>({});
  const [keySaving, setKeySaving] = useState<string | null>(null);
  const [keyMsg, setKeyMsg] = useState<Record<string, string>>({});
  const [keyTestMsg, setKeyTestMsg] = useState<Record<string, string>>({});
  const [keyTesting, setKeyTesting] = useState<string | null>(null);

  // LLM objects state (per-orchestrator provider+model assignment)
  type OrchLLM = { id: string; name: string; displayName: string; provider: string; model: string };
  const [orchLLMs, setOrchLLMs] = useState<OrchLLM[]>(
    (app.app_orchestrators ?? []).map(o => ({
      id: o.id,
      name: o.name,
      displayName: o.display_name || o.name,
      provider: o.llm_provider ?? '',
      model: o.llm_model ?? '',
    }))
  );
  const [orchSaving, setOrchSaving] = useState<string | null>(null);
  const [orchMsg, setOrchMsg] = useState<Record<string, string>>({});

  // Agent params state (canvas agents bound to this app)
  const [agentParamsList, setAgentParamsList] = useState<AgentParamsResponse[]>([]);
  const [agentParamInputs, setAgentParamInputs] = useState<Record<string, Record<string, string>>>({});
  const [agentParamSaving, setAgentParamSaving] = useState<string | null>(null);
  const [agentParamMsg, setAgentParamMsg] = useState<Record<string, string>>({});

  // Canvas agent LLM node overrides
  const [agentLLMNodes, setAgentLLMNodes] = useState<AgentLLMNodeStatus[]>([]);
  type NodeLLMDraft = { provider: string; model: string };
  const [nodeLLMDrafts, setNodeLLMDrafts] = useState<Record<string, NodeLLMDraft>>({});
  const [nodeLLMSaving, setNodeLLMSaving] = useState<string | null>(null);
  const [nodeLLMMsg, setNodeLLMMsg] = useState<Record<string, string>>({});

  // App global parameters state
  const [appParams, setAppParams] = useState<AppGlobalParam[]>([]);
  const [newParamName, setNewParamName] = useState('');
  const [newParamType, setNewParamType] = useState('string');
  const [newParamValue, setNewParamValue] = useState('');
  const [editParamInputs, setEditParamInputs] = useState<Record<string, string>>({});
  const [paramSaving, setParamSaving] = useState<string | null>(null);
  const [paramMsg, setParamMsg] = useState<Record<string, string>>({});
  const [addingParam, setAddingParam] = useState(false);
  const [addParamSaving, setAddParamSaving] = useState(false);
  const [addParamMsg, setAddParamMsg] = useState('');

  useEffect(() => {
    themApi.getProviderKeys(app.id)
      .then(keys => setKeyStatuses(keys))
      .catch(() => {});
  }, [app.id]);

  useEffect(() => {
    themApi.getAppParams(app.id)
      .then(params => setAppParams(params ?? []))
      .catch(() => {});
  }, [app.id]);

  useEffect(() => {
    // Load agent params and LLM nodes for all canvas agents bound to this app
    themApi.listAgentBindings(app.id).then(bindings => {
      Promise.all(
        bindings.map(b => themApi.getAgentParams(app.id, b.agent_id).catch(() => null))
      ).then(results => {
        setAgentParamsList(results.filter((r): r is AgentParamsResponse => r !== null && r.required_params.length > 0));
      });
      Promise.all(
        bindings.map(b => themApi.getAgentLLMNodes(app.id, b.agent_id).catch(() => null))
      ).then(results => {
        const nodes = results.flatMap(r => r ?? []);
        setAgentLLMNodes(nodes);
        const drafts: Record<string, NodeLLMDraft> = {};
        nodes.forEach(n => {
          drafts[n.node_id] = {
            provider: n.override_provider ?? n.compiled_provider ?? '',
            model: n.override_model ?? n.compiled_model ?? '',
          };
        });
        setNodeLLMDrafts(drafts);
      });
    }).catch(() => {});
  }, [app.id]);

  function getKeyStatus(provider: string): KeyStatus {
    return keyStatuses.find(k => k.provider === provider) ?? { provider, key_set: false };
  }

  const setProviders = keyStatuses.filter(k => k.key_set).map(k => k.provider);

  async function handleSaveKey(provider: string) {
    const key = (keyInputs[provider] ?? '').trim();
    if (!key) return;
    setKeySaving(provider);
    try {
      await themApi.setProviderKey(app.id, provider, key);
      const keys = await themApi.getProviderKeys(app.id);
      setKeyStatuses(keys);
      setKeyInputs(ki => ({ ...ki, [provider]: '' }));
      setKeyMsg(m => ({ ...m, [provider]: 'Saved' }));
      setTimeout(() => setKeyMsg(m => ({ ...m, [provider]: '' })), 2500);
    } catch (e: unknown) {
      setKeyMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setKeySaving(null);
    }
  }

  async function handleDeleteKey(provider: string) {
    setKeySaving(provider);
    try {
      await themApi.deleteProviderKey(app.id, provider);
      const keys = await themApi.getProviderKeys(app.id);
      setKeyStatuses(keys);
      setKeyMsg(m => ({ ...m, [provider]: 'Removed' }));
      setTimeout(() => setKeyMsg(m => ({ ...m, [provider]: '' })), 2500);
    } catch (e: unknown) {
      setKeyMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setKeySaving(null);
    }
  }

  async function handleTestKey(provider: string) {
    setKeyTesting(provider);
    setKeyTestMsg(m => ({ ...m, [provider]: '' }));
    try {
      const model = RUNTIME_MODELS[provider]?.[0] ?? 'unknown';
      const res = await themApi.testAppLlm(app.id, provider, model);
      if (res.ok) {
        setKeyTestMsg(m => ({ ...m, [provider]: `✓ ${res.latency_ms}ms` }));
      } else {
        setKeyTestMsg(m => ({ ...m, [provider]: res.error ?? 'Failed' }));
      }
    } catch (e: unknown) {
      setKeyTestMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Error' }));
    } finally {
      setKeyTesting(null);
    }
  }

  async function handleSaveOrchLLM(orchId: string) {
    const orch = orchLLMs.find(o => o.id === orchId);
    if (!orch || !orch.provider || !orch.model) return;
    setOrchSaving(orchId);
    try {
      await themApi.patchOrchestratorLLM(app.id, orchId, orch.provider, orch.model);
      setOrchMsg(m => ({ ...m, [orchId]: 'Saved' }));
      setTimeout(() => setOrchMsg(m => ({ ...m, [orchId]: '' })), 2500);
      onOrchSaved?.(orchId, orch.provider, orch.model);
    } catch (e: unknown) {
      setOrchMsg(m => ({ ...m, [orchId]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setOrchSaving(null);
    }
  }

  async function handleSaveAgentParams(agentId: string) {
    const inputs = agentParamInputs[agentId] ?? {};
    const nonEmpty = Object.fromEntries(Object.entries(inputs).filter(([, v]) => v.trim() !== ''));
    if (Object.keys(nonEmpty).length === 0) return;
    setAgentParamSaving(agentId);
    try {
      await themApi.putAgentParams(app.id, agentId, nonEmpty);
      // Refresh this agent's param statuses
      const updated = await themApi.getAgentParams(app.id, agentId);
      setAgentParamsList(prev => prev.map(a => a.agent_id === agentId ? updated : a));
      setAgentParamInputs(prev => ({ ...prev, [agentId]: {} }));
      setAgentParamMsg(m => ({ ...m, [agentId]: 'Saved' }));
      setTimeout(() => setAgentParamMsg(m => ({ ...m, [agentId]: '' })), 2500);
    } catch (e: unknown) {
      setAgentParamMsg(m => ({ ...m, [agentId]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setAgentParamSaving(null);
    }
  }

  async function handleSaveNodeLLM(agentId: string, nodeId: string) {
    const draft = nodeLLMDrafts[nodeId];
    if (!draft?.provider || !draft?.model) return;
    const key = `${agentId}::${nodeId}`;
    setNodeLLMSaving(key);
    try {
      await themApi.putNodeLLMOverride(app.id, agentId, nodeId, draft.provider, draft.model);
      setAgentLLMNodes(prev => prev.map(n =>
        n.node_id === nodeId ? { ...n, override_provider: draft.provider, override_model: draft.model } : n
      ));
      setNodeLLMMsg(m => ({ ...m, [nodeId]: 'Saved' }));
      setTimeout(() => setNodeLLMMsg(m => ({ ...m, [nodeId]: '' })), 2500);
    } catch (e: unknown) {
      setNodeLLMMsg(m => ({ ...m, [nodeId]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setNodeLLMSaving(null);
    }
  }

  async function handleAddAppParam() {
    const name = newParamName.trim();
    const value = newParamValue.trim();
    if (!name || !value) return;
    setAddParamSaving(true);
    try {
      await themApi.setAppParam(app.id, name, value, newParamType);
      const params = await themApi.getAppParams(app.id);
      setAppParams(params ?? []);
      setNewParamName('');
      setNewParamValue('');
      setNewParamType('string');
      setAddingParam(false);
      setAddParamMsg('');
    } catch (e: unknown) {
      setAddParamMsg(e instanceof Error ? e.message : 'Failed');
    } finally {
      setAddParamSaving(false);
    }
  }

  async function handleUpdateAppParam(name: string) {
    const value = (editParamInputs[name] ?? '').trim();
    if (!value) return;
    const param = appParams.find(p => p.name === name);
    if (!param) return;
    setParamSaving(name);
    try {
      await themApi.setAppParam(app.id, name, value, param.type);
      const params = await themApi.getAppParams(app.id);
      setAppParams(params ?? []);
      setEditParamInputs(prev => ({ ...prev, [name]: '' }));
      setParamMsg(m => ({ ...m, [name]: 'Saved' }));
      setTimeout(() => setParamMsg(m => ({ ...m, [name]: '' })), 2500);
    } catch (e: unknown) {
      setParamMsg(m => ({ ...m, [name]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setParamSaving(null);
    }
  }

  async function handleDeleteAppParam(name: string) {
    setParamSaving(name);
    try {
      await themApi.deleteAppParam(app.id, name);
      const params = await themApi.getAppParams(app.id);
      setAppParams(params ?? []);
      setParamMsg(m => ({ ...m, [name]: '' }));
    } catch (e: unknown) {
      setParamMsg(m => ({ ...m, [name]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setParamSaving(null);
    }
  }

  async function handleSave() {
    setSaving(true);
    setError(null);
    try {
      const parsedUsers = usersInput.split(/[\s,]+/).map(s => s.trim()).filter(Boolean).map(Number).filter(n => !isNaN(n));
      const parsedTokens = tokensInput.split(/\n/).map(s => s.trim()).filter(Boolean);
      const payload = { ...cfg, blocked_tokens: parsedTokens, blocked_user_ids: parsedUsers };
      await themApi.putAppRuntime(app.id, payload);
      setCfg(payload);
      setSaved(true);
      setTimeout(() => setSaved(false), 2500);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }

  const fieldStyle: React.CSSProperties = {
    width: '100%', padding: '10px 12px', borderRadius: 8,
    border: '1px solid rgba(255,255,255,0.12)', background: 'rgba(255,255,255,0.05)',
    color: C.text, fontSize: 14, outline: 'none', boxSizing: 'border-box',
  };
  const labelStyle: React.CSSProperties = { fontSize: 12, fontWeight: 600, color: C.textMuted, letterSpacing: '0.06em', textTransform: 'uppercase', marginBottom: 6, display: 'block' };
  const sectionStyle: React.CSSProperties = { ...glass, borderRadius: 12, padding: '20px 24px', display: 'flex', flexDirection: 'column', gap: 16, marginBottom: 20 };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '40px 40px 60px', background: C.bg }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 32 }}>
        <button onClick={onBack} style={{ background: 'none', border: 'none', color: C.textMuted, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 18 }}>arrow_back</span>
          Applications
        </button>
        <span style={{ color: 'rgba(255,255,255,0.2)', fontSize: 18 }}>/</span>
        <span style={{ fontSize: 14, color: C.text, fontWeight: 600 }}>{app.name}</span>
        <span style={{ color: 'rgba(255,255,255,0.2)', fontSize: 18 }}>/</span>
        <span style={{ fontSize: 14, color: '#fb923c', fontWeight: 700 }}>Runtime Policy</span>
      </div>

      <div style={{ maxWidth: 640 }}>
        {/* Session Limits */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>Session Limits</div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
            <div>
              <label style={labelStyle}>Max Concurrent Sessions</label>
              <input type="number" min={1} placeholder="Unlimited"
                value={cfg.max_concurrent_sessions ?? ''} style={fieldStyle}
                onChange={e => setCfg(c => ({ ...c, max_concurrent_sessions: e.target.value === '' ? null : parseInt(e.target.value) }))} />
              <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>App-wide soft cap. Empty = unlimited.</div>
            </div>
            <div>
              <label style={labelStyle}>Session Timeout (minutes)</label>
              <input type="number" min={1} placeholder="No timeout"
                value={cfg.session_timeout_minutes ?? ''} style={fieldStyle}
                onChange={e => setCfg(c => ({ ...c, session_timeout_minutes: e.target.value === '' ? null : parseInt(e.target.value) }))} />
              <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Advisory. Empty = no timeout.</div>
            </div>
          </div>
        </div>

        {/* Rate Limiting */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>Rate Limiting</div>
          <div>
            <label style={labelStyle}>App Rate Limit (requests per minute)</label>
            <input type="number" min={1} placeholder="Unlimited"
              value={cfg.rate_limit_rpm ?? ''} style={fieldStyle}
              onChange={e => setCfg(c => ({ ...c, rate_limit_rpm: e.target.value === '' ? null : parseInt(e.target.value) }))} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Applied across all entry points of this app. Separate from per-orchestrator rate limits.</div>
          </div>
        </div>

        {/* Access Control */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>Access Control</div>
          <div>
            <label style={labelStyle}>Blocked User IDs (comma-separated)</label>
            <input type="text" placeholder="e.g. 42, 107, 889"
              value={usersInput} style={fieldStyle}
              onChange={e => setUsersInput(e.target.value)} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Connections from these user IDs are rejected before any processing.</div>
          </div>
          <div>
            <label style={labelStyle}>Blocked Token Hashes (one per line)</label>
            <textarea placeholder="sha256 hash of each blocked access token"
              value={tokensInput} rows={4}
              style={{ ...fieldStyle, resize: 'vertical', fontFamily: 'monospace', fontSize: 12 }}
              onChange={e => setTokensInput(e.target.value)} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Paste the SHA-256 hash of the token (not the raw token). One hash per line.</div>
          </div>
        </div>

        {/* Provider Keys */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>LLM Provider Keys</div>
          <div style={{ fontSize: 12, color: C.textMuted, marginBottom: 8 }}>
            One API key per provider. Keys are AES-GCM encrypted at rest. Use Test to verify the key works.
          </div>
          {PROVIDER_LIST.map(provider => {
            const status = getKeyStatus(provider);
            const isBusy = keySaving === provider;
            const isTesting = keyTesting === provider;
            const msg = keyMsg[provider] ?? '';
            const testMsg = keyTestMsg[provider] ?? '';
            const isError = msg && msg !== 'Saved' && msg !== 'Removed';
            const isTestError = testMsg && !testMsg.startsWith('✓');
            return (
              <div key={provider} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                  {/* Provider label + key-set badge */}
                  <div style={{ width: 90, flexShrink: 0 }}>
                    <span style={{ fontSize: 13, fontWeight: 600, color: C.text }}>{provider}</span>
                    <span style={{
                      marginLeft: 8, fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 20,
                      background: status.key_set ? 'rgba(74,222,128,0.12)' : 'rgba(251,146,60,0.12)',
                      color: status.key_set ? C.green : '#fb923c',
                      border: `1px solid ${status.key_set ? 'rgba(74,222,128,0.3)' : 'rgba(251,146,60,0.3)'}`,
                    }}>
                      {status.key_set ? `set ···${status.key_hint ?? ''}` : 'not set'}
                    </span>
                  </div>
                  {/* Key input */}
                  <input
                    type="password"
                    placeholder={status.key_set ? 'Enter new key to replace…' : 'Paste API key…'}
                    value={keyInputs[provider] ?? ''}
                    onChange={e => setKeyInputs(ki => ({ ...ki, [provider]: e.target.value }))}
                    style={{ ...fieldStyle, flex: 1, minWidth: 180, fontSize: 13 }}
                    onKeyDown={e => { if (e.key === 'Enter') handleSaveKey(provider); }}
                  />
                  {/* Save key button */}
                  <button
                    onClick={() => handleSaveKey(provider)}
                    disabled={isBusy || !(keyInputs[provider] ?? '').trim()}
                    style={{ padding: '8px 14px', borderRadius: 8, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap', opacity: isBusy || !(keyInputs[provider] ?? '').trim() ? 0.5 : 1 }}
                  >
                    {isBusy ? '…' : 'Save'}
                  </button>
                  {/* Test button — only when key is set */}
                  {status.key_set && (
                    <button
                      onClick={() => handleTestKey(provider)}
                      disabled={isBusy || isTesting}
                      style={{ padding: '8px 12px', borderRadius: 8, border: '1px solid rgba(74,222,128,0.3)', background: 'rgba(74,222,128,0.07)', color: C.green, cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy || isTesting ? 0.5 : 1 }}
                    >
                      {isTesting ? '…' : 'Test'}
                    </button>
                  )}
                  {/* Remove button — only shown when a key is set */}
                  {status.key_set && (
                    <button
                      onClick={() => handleDeleteKey(provider)}
                      disabled={isBusy}
                      style={{ padding: '8px 10px', borderRadius: 8, border: '1px solid rgba(248,113,113,0.3)', background: 'rgba(248,113,113,0.07)', color: '#f87171', cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy ? 0.5 : 1 }}
                    >
                      Remove
                    </button>
                  )}
                  {msg && <span style={{ fontSize: 12, color: isError ? C.error : C.green, fontWeight: 600 }}>{msg}</span>}
                </div>
                {testMsg && (
                  <div style={{ fontSize: 12, color: isTestError ? C.error : C.green, fontWeight: 600, paddingLeft: 98 }}>{testMsg}</div>
                )}
              </div>
            );
          })}
        </div>

        {/* App Global Parameters */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>App Global Parameters</div>
          <div style={{ fontSize: 12, color: C.textMuted, marginBottom: 8 }}>
            Named parameters available to all canvas agents in this app. Canvas agent HTTP and LLM nodes can reference these by name using <code style={{ fontFamily: 'monospace', fontSize: 11, background: 'rgba(255,255,255,0.07)', padding: '1px 4px', borderRadius: 4 }}>app_param_ref</code>.
            Secrets are stored encrypted and never displayed in full.
          </div>

          {/* Existing params */}
          {appParams.map(param => {
            const isBusy = paramSaving === param.name;
            const msg = paramMsg[param.name] ?? '';
            const isError = msg && msg !== 'Saved';
            const isSecret = param.type === 'secret';
            const editVal = editParamInputs[param.name] ?? '';
            return (
              <div key={param.name} style={{ marginBottom: 10, padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.12)' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                  <code style={{ fontSize: 12, fontWeight: 700, color: C.text, fontFamily: 'JetBrains Mono, monospace', flex: 1 }}>{param.name}</code>
                  <span style={{ fontSize: 10, fontWeight: 600, padding: '2px 6px', borderRadius: 20, background: 'rgba(132,158,190,0.12)', color: C.textMuted, border: '1px solid rgba(132,158,190,0.2)' }}>{param.type}</span>
                  {param.is_set && (
                    <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 20, background: 'rgba(74,222,128,0.12)', color: C.green, border: '1px solid rgba(74,222,128,0.3)' }}>
                      {isSecret ? `set ···${param.value_hint ?? ''}` : 'set'}
                    </span>
                  )}
                </div>
                {!isSecret && param.is_set && param.value && (
                  <div style={{ fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: C.textMuted, marginBottom: 6, wordBreak: 'break-all' }}>{param.value}</div>
                )}
                <div style={{ display: 'flex', gap: 6 }}>
                  <input
                    type={isSecret ? 'password' : 'text'}
                    placeholder={param.is_set ? 'Enter new value to replace…' : 'Enter value…'}
                    value={editVal}
                    onChange={e => setEditParamInputs(prev => ({ ...prev, [param.name]: e.target.value }))}
                    style={{ ...fieldStyle, flex: 1, minWidth: 0, fontSize: 13 }}
                    onKeyDown={e => { if (e.key === 'Enter') handleUpdateAppParam(param.name); }}
                  />
                  <button
                    onClick={() => handleUpdateAppParam(param.name)}
                    disabled={isBusy || !editVal.trim()}
                    style={{ padding: '8px 14px', borderRadius: 8, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap', opacity: isBusy || !editVal.trim() ? 0.5 : 1 }}
                  >
                    {isBusy ? '…' : 'Update'}
                  </button>
                  <button
                    onClick={() => handleDeleteAppParam(param.name)}
                    disabled={isBusy}
                    style={{ padding: '8px 10px', borderRadius: 8, border: '1px solid rgba(248,113,113,0.3)', background: 'rgba(248,113,113,0.07)', color: '#f87171', cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy ? 0.5 : 1 }}
                  >
                    Remove
                  </button>
                </div>
                {msg && <div style={{ marginTop: 4, fontSize: 12, color: isError ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
              </div>
            );
          })}

          {/* Add new param */}
          {!addingParam ? (
            <button
              onClick={() => setAddingParam(true)}
              style={{ width: '100%', padding: '8px 0', borderRadius: 8, border: `1px dashed ${C.outline}`, background: 'transparent', color: C.textMuted, cursor: 'pointer', fontSize: 12, fontWeight: 600 }}
            >
              + Add parameter
            </button>
          ) : (
            <div style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.18)' }}>
              <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, marginBottom: 8, textTransform: 'uppercase', letterSpacing: '0.06em' }}>New Parameter</div>
              <div style={{ display: 'flex', gap: 6, marginBottom: 8 }}>
                <input
                  placeholder="name (a-z0-9_)"
                  value={newParamName}
                  onChange={e => setNewParamName(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
                  style={{ ...fieldStyle, flex: 2, fontSize: 13, fontFamily: 'JetBrains Mono, monospace' }}
                />
                <select
                  value={newParamType}
                  onChange={e => setNewParamType(e.target.value)}
                  style={{ ...fieldStyle, flex: 1, fontSize: 13 }}
                >
                  <option value="string">string</option>
                  <option value="secret">secret</option>
                  <option value="url">url</option>
                  <option value="int">int</option>
                  <option value="bool">bool</option>
                </select>
              </div>
              <input
                type={newParamType === 'secret' ? 'password' : 'text'}
                placeholder="Value…"
                value={newParamValue}
                onChange={e => setNewParamValue(e.target.value)}
                style={{ ...fieldStyle, fontSize: 13, marginBottom: 8 }}
              />
              {addParamMsg && <div style={{ fontSize: 12, color: C.error, fontWeight: 600, marginBottom: 6 }}>{addParamMsg}</div>}
              <div style={{ display: 'flex', gap: 6 }}>
                <button
                  onClick={handleAddAppParam}
                  disabled={addParamSaving || !newParamName.trim() || !newParamValue.trim()}
                  style={{ padding: '8px 14px', borderRadius: 8, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: addParamSaving || !newParamName.trim() || !newParamValue.trim() ? 0.5 : 1 }}
                >
                  {addParamSaving ? '…' : 'Save'}
                </button>
                <button
                  onClick={() => { setAddingParam(false); setNewParamName(''); setNewParamValue(''); setNewParamType('string'); setAddParamMsg(''); }}
                  style={{ padding: '8px 14px', borderRadius: 8, border: `1px solid ${C.outline}`, background: 'transparent', color: C.textMuted, cursor: 'pointer', fontSize: 12 }}
                >
                  Cancel
                </button>
              </div>
            </div>
          )}
        </div>

        {/* LLM Objects — assign provider+model per orchestrator */}
        {orchLLMs.length > 0 && (
          <div style={sectionStyle}>
            <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>LLM Configuration</div>
            <div style={{ fontSize: 12, color: C.textMuted, marginBottom: 8 }}>
              Assign a provider and model to each orchestrator. Providers with a saved key are marked ✓.
            </div>
            {orchLLMs.map(orch => {
              const isBusy = orchSaving === orch.id;
              const msg = orchMsg[orch.id] ?? '';
              const isError = msg && msg !== 'Saved';
              const canSave = orch.provider && orch.model;
              return (
                <div key={orch.id} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.12)', marginBottom: 8 }}>
                  <div style={{ fontSize: 12, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 8 }}>
                    {orch.displayName || orch.name}
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    {/* Provider — only shows providers with a saved key */}
                    <select
                      value={orch.provider}
                      onChange={e => {
                        const p = e.target.value;
                        setOrchLLMs(prev => prev.map(o => {
                          if (o.id !== orch.id) return o;
                          const models = RUNTIME_MODELS[p] ?? [];
                          const model = models.includes(o.model) ? o.model : (models[0] ?? '');
                          return { ...o, provider: p, model };
                        }));
                      }}
                      style={{ ...fieldStyle, width: 160, fontSize: 13, flexShrink: 0 }}
                    >
                      <option value="">— select provider —</option>
                      {setProviders.map(p => <option key={p} value={p}>{p}</option>)}
                    </select>
                    {/* Model — dropdown of known models for the chosen provider */}
                    <select
                      value={orch.model}
                      onChange={e => setOrchLLMs(prev => prev.map(o => o.id === orch.id ? { ...o, model: e.target.value } : o))}
                      style={{ ...fieldStyle, flex: 1, fontSize: 12, fontFamily: 'JetBrains Mono, monospace', flexShrink: 1 }}
                      disabled={!orch.provider}
                    >
                      <option value="">— select model —</option>
                      {(RUNTIME_MODELS[orch.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                    </select>
                    <button
                      onClick={() => handleSaveOrchLLM(orch.id)}
                      disabled={isBusy || !canSave}
                      style={{ padding: '8px 14px', borderRadius: 8, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap', flexShrink: 0, opacity: isBusy || !canSave ? 0.5 : 1 }}
                    >
                      {isBusy ? '…' : 'Save'}
                    </button>
                  </div>
                  {msg && <div style={{ marginTop: 6, fontSize: 12, color: isError ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
                </div>
              );
            })}
          </div>
        )}

        {/* Canvas Agent LLM Nodes */}
        {agentLLMNodes.length > 0 && (
          <div style={sectionStyle}>
            <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>Canvas Agent LLM Nodes</div>
            <div style={{ fontSize: 12, color: C.textMuted, marginBottom: 8 }}>
              Override the provider and model for each LLM node in your canvas agents.
              The compiled defaults are shown; leave as-is to use them, or pick a different provider/model.
              Providers with a saved key are available in the dropdown.
            </div>
            {agentLLMNodes.map(node => {
              const agentId = node.agent_id;
              const key = `${agentId}::${node.node_id}`;
              const isBusy = nodeLLMSaving === key;
              const msg = nodeLLMMsg[node.node_id] ?? '';
              const isError = msg && msg !== 'Saved';
              const draft = nodeLLMDrafts[node.node_id] ?? { provider: '', model: '' };
              const canSave = draft.provider && draft.model;
              const isOverridden = node.override_provider && node.override_model;
              const allProviders = PROVIDER_LIST;
              return (
                <div key={node.node_id} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.12)', marginBottom: 8 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                    <span style={{ fontSize: 12, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', flex: 1 }}>
                      {node.label || node.node_id}
                    </span>
                    <span style={{ fontSize: 10, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>
                      default: {node.compiled_provider}/{node.compiled_model}
                    </span>
                    {isOverridden && (
                      <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 20, background: 'rgba(251,146,60,0.12)', color: '#fb923c', border: '1px solid rgba(251,146,60,0.3)' }}>
                        overridden
                      </span>
                    )}
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <select
                      value={draft.provider}
                      onChange={e => {
                        const p = e.target.value;
                        setNodeLLMDrafts(prev => {
                          const models = RUNTIME_MODELS[p] ?? [];
                          const prevModel = prev[node.node_id]?.model ?? '';
                          const model = models.includes(prevModel) ? prevModel : (models[0] ?? '');
                          return { ...prev, [node.node_id]: { provider: p, model } };
                        });
                      }}
                      style={{ ...fieldStyle, width: 160, fontSize: 13, flexShrink: 0 }}
                    >
                      <option value="">— provider —</option>
                      {allProviders.map(p => (
                        <option key={p} value={p}>
                          {p}{setProviders.includes(p) ? ' ✓' : ''}
                        </option>
                      ))}
                    </select>
                    <select
                      value={draft.model}
                      onChange={e => setNodeLLMDrafts(prev => ({ ...prev, [node.node_id]: { ...draft, model: e.target.value } }))}
                      style={{ ...fieldStyle, flex: 1, fontSize: 12, fontFamily: 'JetBrains Mono, monospace' }}
                      disabled={!draft.provider}
                    >
                      <option value="">— model —</option>
                      {(RUNTIME_MODELS[draft.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                    </select>
                    <button
                      onClick={() => handleSaveNodeLLM(agentId, node.node_id)}
                      disabled={isBusy || !canSave}
                      style={{ padding: '8px 14px', borderRadius: 8, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap', flexShrink: 0, opacity: isBusy || !canSave ? 0.5 : 1 }}
                    >
                      {isBusy ? '…' : 'Save'}
                    </button>
                  </div>
                  {msg && <div style={{ marginTop: 6, fontSize: 12, color: isError ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
                </div>
              );
            })}
          </div>
        )}

        {/* Agent Parameters — canvas agents bound to this app */}
        {agentParamsList.length > 0 && (
          <div style={sectionStyle}>
            <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>Agent Parameters</div>
            <div style={{ fontSize: 12, color: C.textMuted, marginBottom: 8 }}>
              Runtime parameters required by canvas agents bound to this app. Secrets are stored encrypted and never displayed.
            </div>
            {agentParamsList.map(agentParams => {
              const isBusy = agentParamSaving === agentParams.agent_id;
              const msg = agentParamMsg[agentParams.agent_id] ?? '';
              const isError = msg && msg !== 'Saved';
              const inputs = agentParamInputs[agentParams.agent_id] ?? {};
              const hasAnyInput = Object.values(inputs).some(v => v.trim() !== '');
              return (
                <div key={agentParams.agent_id} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.12)', marginBottom: 8 }}>
                  <div style={{ fontSize: 12, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 10 }}>
                    {agentParams.agent_slug}
                  </div>
                  {agentParams.required_params.map(param => {
                    const isSecret = param.type === 'secret';
                    const currentVal = inputs[param.key] ?? '';
                    return (
                      <div key={param.key} style={{ marginBottom: 10 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                          <label style={{ ...labelStyle, marginBottom: 0, flex: 1 }}>{param.label}</label>
                          {param.required && !param.is_set && (
                            <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 20, background: 'rgba(248,113,113,0.12)', color: '#f87171', border: '1px solid rgba(248,113,113,0.3)' }}>required</span>
                          )}
                          {param.is_set && (
                            <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 20, background: 'rgba(74,222,128,0.12)', color: C.green, border: '1px solid rgba(74,222,128,0.3)' }}>
                              set ···{param.hint}
                            </span>
                          )}
                        </div>
                        {param.description && (
                          <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 4 }}>{param.description}</div>
                        )}
                        <input
                          type={isSecret ? 'password' : 'text'}
                          placeholder={param.is_set ? 'Enter new value to replace…' : (param.default_value ? `default: ${param.default_value}` : 'Enter value…')}
                          value={currentVal}
                          onChange={e => setAgentParamInputs(prev => ({
                            ...prev,
                            [agentParams.agent_id]: { ...(prev[agentParams.agent_id] ?? {}), [param.key]: e.target.value },
                          }))}
                          style={{ ...fieldStyle, fontFamily: isSecret ? 'monospace' : 'inherit', fontSize: 13 }}
                        />
                      </div>
                    );
                  })}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
                    <button
                      onClick={() => handleSaveAgentParams(agentParams.agent_id)}
                      disabled={isBusy || !hasAnyInput}
                      style={{ padding: '8px 14px', borderRadius: 8, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy || !hasAnyInput ? 0.5 : 1 }}
                    >
                      {isBusy ? '…' : 'Save Params'}
                    </button>
                    {msg && <span style={{ fontSize: 12, color: isError ? C.error : C.green, fontWeight: 600 }}>{msg}</span>}
                  </div>
                </div>
              );
            })}
          </div>
        )}

        {/* Save */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <button
            onClick={handleSave}
            disabled={saving}
            style={{
              padding: '11px 28px', borderRadius: 8, border: 'none', cursor: saving ? 'not-allowed' : 'pointer',
              background: '#fb923c', color: '#000', fontSize: 14, fontWeight: 700,
              opacity: saving ? 0.6 : 1,
            }}
          >
            {saving ? 'Saving…' : 'Save Runtime Config'}
          </button>
          {saved && <span style={{ fontSize: 13, color: C.green }}>Saved</span>}
          {error && <span style={{ fontSize: 13, color: C.error }}>{error}</span>}
        </div>
      </div>
    </div>
  );
}
