'use client';
import { useState, useEffect } from 'react';
import { themApi, type Application, type AgentParamsResponse, type AppGlobalParam, type AgentLLMNodeStatus } from '@/lib/api';
import { C, glass, PROVIDER_LIST, RUNTIME_MODELS } from '../constants';

// ── Collapsible section wrapper ───────────────────────────────────────────────
function Section({ title, subtitle, icon, children, defaultOpen = true, accent }: {
  title: string; subtitle?: string; icon: string; children: React.ReactNode;
  defaultOpen?: boolean; accent?: string;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div style={{ ...glass, borderRadius: 12, marginBottom: 16, overflow: 'hidden' }}>
      <button
        onClick={() => setOpen(o => !o)}
        style={{
          width: '100%', display: 'flex', alignItems: 'center', gap: 12,
          padding: '14px 20px', background: 'none', border: 'none', cursor: 'pointer',
          borderBottom: open ? '1px solid rgba(255,255,255,0.06)' : 'none',
        }}
      >
        <span className="material-symbols-outlined" style={{ fontSize: 18, color: accent ?? C.purple, flexShrink: 0 }}>{icon}</span>
        <div style={{ flex: 1, textAlign: 'left' }}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text }}>{title}</div>
          {subtitle && <div style={{ fontSize: 11, color: C.textMuted, marginTop: 2 }}>{subtitle}</div>}
        </div>
        <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.textMuted, transition: 'transform 0.15s', transform: open ? 'rotate(180deg)' : 'none' }}>
          expand_more
        </span>
      </button>
      {open && <div style={{ padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 14 }}>{children}</div>}
    </div>
  );
}

// ── Agent section wrapper (accent border left) ────────────────────────────────
function AgentSection({ slug, children, defaultOpen = true }: { slug: string; children: React.ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div style={{ borderRadius: 10, border: '1px solid rgba(208,188,255,0.18)', marginBottom: 12, overflow: 'hidden', background: 'rgba(208,188,255,0.03)' }}>
      <button
        onClick={() => setOpen(o => !o)}
        style={{ width: '100%', display: 'flex', alignItems: 'center', gap: 10, padding: '10px 16px', background: 'none', border: 'none', cursor: 'pointer', borderBottom: open ? '1px solid rgba(208,188,255,0.1)' : 'none' }}
      >
        <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.purple }}>smart_toy</span>
        <span style={{ flex: 1, textAlign: 'left', fontSize: 12, fontWeight: 700, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{slug}</span>
        <span className="material-symbols-outlined" style={{ fontSize: 16, color: C.textMuted, transition: 'transform 0.15s', transform: open ? 'rotate(180deg)' : 'none' }}>expand_more</span>
      </button>
      {open && <div style={{ padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 12 }}>{children}</div>}
    </div>
  );
}

// ── Sub-section label inside an agent card ────────────────────────────────────
function AgentSubLabel({ icon, label }: { icon: string; label: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
      <span className="material-symbols-outlined" style={{ fontSize: 13, color: C.textMuted }}>{icon}</span>
      <span style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.07em' }}>{label}</span>
    </div>
  );
}

// ── RuntimeView ───────────────────────────────────────────────────────────────
export function RuntimeView({ app, onBack, onOrchSaved }: {
  app: Application; onBack: () => void;
  onOrchSaved?: (orchId: string, provider: string, model: string) => void;
}) {
  const emptyRuntime = { max_concurrent_sessions: null, rate_limit_rpm: null, blocked_tokens: [], blocked_user_ids: [], session_timeout_minutes: null };
  const [cfg, setCfg]     = useState<import('@/lib/api').AppRuntimeConfig>(app.runtime_config ?? emptyRuntime);
  const [saving, setSaving]   = useState(false);
  const [saved, setSaved]     = useState(false);
  const [error, setError]     = useState<string | null>(null);
  const [tokensInput, setTokensInput] = useState((app.runtime_config?.blocked_tokens ?? []).join('\n'));
  const [usersInput,  setUsersInput]  = useState((app.runtime_config?.blocked_user_ids ?? []).join(', '));

  // Provider keys
  type KeyStatus = { provider: string; key_set: boolean; key_hint?: string };
  const [keyStatuses, setKeyStatuses] = useState<KeyStatus[]>([]);
  const [keyInputs,   setKeyInputs]   = useState<Record<string, string>>({});
  const [keySaving,   setKeySaving]   = useState<string | null>(null);
  const [keyMsg,      setKeyMsg]      = useState<Record<string, string>>({});
  const [keyTestMsg,  setKeyTestMsg]  = useState<Record<string, string>>({});
  const [keyTesting,  setKeyTesting]  = useState<string | null>(null);

  // Orchestrator LLM
  type OrchLLM = { id: string; name: string; displayName: string; provider: string; model: string };
  const [orchLLMs,  setOrchLLMs]  = useState<OrchLLM[]>(
    (app.app_orchestrators ?? []).map(o => ({
      id: o.id, name: o.name, displayName: o.display_name || o.name,
      provider: o.llm_provider ?? '', model: o.llm_model ?? '',
    }))
  );
  const [orchSaving, setOrchSaving] = useState<string | null>(null);
  const [orchMsg,    setOrchMsg]    = useState<Record<string, string>>({});

  // Orchestrator Summarizer
  type OrchSummarizer = {
    id: string; name: string; displayName: string;
    memoryEnabled: boolean; summarizeEveryN: number; fallbackN: number;
    provider: string; model: string;
  };
  const [orchSummarizers, setOrchSummarizers] = useState<OrchSummarizer[]>(
    (app.app_orchestrators ?? []).map(o => ({
      id: o.id, name: o.name, displayName: o.display_name || o.name,
      memoryEnabled: o.memory_enabled ?? false,
      summarizeEveryN: o.summarize_every_n_calls ?? 10,
      fallbackN: o.memory_raw_fallback_n ?? 3,
      provider: o.summarizer_provider ?? '',
      model: o.summarizer_model ?? '',
    }))
  );
  const [sumSaving, setSumSaving] = useState<string | null>(null);
  const [sumMsg,    setSumMsg]    = useState<Record<string, string>>({});

  // Canvas agent params
  const [agentParamsList,   setAgentParamsList]   = useState<AgentParamsResponse[]>([]);
  const [agentParamInputs,  setAgentParamInputs]  = useState<Record<string, Record<string, string>>>({});
  const [agentParamSaving,  setAgentParamSaving]  = useState<string | null>(null);
  const [agentParamMsg,     setAgentParamMsg]     = useState<Record<string, string>>({});

  // Canvas agent LLM node overrides
  const [agentLLMNodes,  setAgentLLMNodes]  = useState<AgentLLMNodeStatus[]>([]);
  type NodeLLMDraft = { provider: string; model: string };
  const [nodeLLMDrafts,  setNodeLLMDrafts]  = useState<Record<string, NodeLLMDraft>>({});
  const [nodeLLMSaving,  setNodeLLMSaving]  = useState<string | null>(null);
  const [nodeLLMMsg,     setNodeLLMMsg]     = useState<Record<string, string>>({});

  // App global parameters
  const [appParams,        setAppParams]        = useState<AppGlobalParam[]>([]);
  const [newParamName,     setNewParamName]     = useState('');
  const [newParamType,     setNewParamType]     = useState('string');
  const [newParamValue,    setNewParamValue]    = useState('');
  const [editParamInputs,  setEditParamInputs]  = useState<Record<string, string>>({});
  const [paramSaving,      setParamSaving]      = useState<string | null>(null);
  const [paramMsg,         setParamMsg]         = useState<Record<string, string>>({});
  const [addingParam,      setAddingParam]      = useState(false);
  const [addParamSaving,   setAddParamSaving]   = useState(false);
  const [addParamMsg,      setAddParamMsg]      = useState('');

  useEffect(() => {
    themApi.getProviderKeys(app.id).then(setKeyStatuses).catch(() => {});
  }, [app.id]);

  useEffect(() => {
    themApi.getAppParams(app.id).then(p => setAppParams(p ?? [])).catch(() => {});
  }, [app.id]);

  useEffect(() => {
    themApi.listAgentBindings(app.id).then(bindings => {
      Promise.all(bindings.map(b => themApi.getAgentParams(app.id, b.agent_id).catch(() => null)))
        .then(results => setAgentParamsList(results.filter((r): r is AgentParamsResponse => r !== null && r.required_params.length > 0)));
      Promise.all(bindings.map(b => themApi.getAgentLLMNodes(app.id, b.agent_id).catch(() => null)))
        .then(results => {
          const nodes = results.flatMap(r => r ?? []);
          setAgentLLMNodes(nodes);
          const drafts: Record<string, NodeLLMDraft> = {};
          nodes.forEach(n => { drafts[n.node_id] = { provider: n.override_provider ?? n.compiled_provider ?? '', model: n.override_model ?? n.compiled_model ?? '' }; });
          setNodeLLMDrafts(drafts);
        });
    }).catch(() => {});
  }, [app.id]);

  // Derived: providers with a key set
  const setProviders = keyStatuses.filter(k => k.key_set).map(k => k.provider);

  function getKeyStatus(provider: string): KeyStatus {
    return keyStatuses.find(k => k.provider === provider) ?? { provider, key_set: false };
  }

  // Group agent LLM nodes and params by agent
  const agentIds = [...new Set(agentLLMNodes.map(n => n.agent_id))];
  // Also include agents that have params but no LLM nodes
  agentParamsList.forEach(a => { if (!agentIds.includes(a.agent_id)) agentIds.push(a.agent_id); });

  // ── Handlers ─────────────────────────────────────────────────────────────

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
    } finally { setKeySaving(null); }
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
    } finally { setKeySaving(null); }
  }

  async function handleTestKey(provider: string) {
    setKeyTesting(provider);
    setKeyTestMsg(m => ({ ...m, [provider]: '' }));
    try {
      const model = RUNTIME_MODELS[provider]?.[0] ?? 'unknown';
      const res = await themApi.testAppLlm(app.id, provider, model);
      setKeyTestMsg(m => ({ ...m, [provider]: res.ok ? `✓ ${res.latency_ms}ms` : (res.error ?? 'Failed') }));
    } catch (e: unknown) {
      setKeyTestMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Error' }));
    } finally { setKeyTesting(null); }
  }

  async function handleSaveOrchLLM(orchId: string) {
    const orch = orchLLMs.find(o => o.id === orchId);
    if (!orch?.provider || !orch?.model) return;
    setOrchSaving(orchId);
    try {
      await themApi.patchOrchestratorLLM(app.id, orchId, orch.provider, orch.model);
      setOrchMsg(m => ({ ...m, [orchId]: 'Saved' }));
      setTimeout(() => setOrchMsg(m => ({ ...m, [orchId]: '' })), 2500);
      onOrchSaved?.(orchId, orch.provider, orch.model);
    } catch (e: unknown) {
      setOrchMsg(m => ({ ...m, [orchId]: e instanceof Error ? e.message : 'Failed' }));
    } finally { setOrchSaving(null); }
  }

  async function handleSaveOrchSummarizer(orchId: string) {
    const s = orchSummarizers.find(o => o.id === orchId);
    if (!s) return;
    setSumSaving(orchId);
    try {
      await themApi.patchOrchestratorSummarizer(app.id, orchId, {
        memory_enabled: s.memoryEnabled,
        summarize_every_n_calls: s.summarizeEveryN,
        memory_raw_fallback_n: s.fallbackN,
        summarizer_provider: s.provider || null,
        summarizer_model: s.model || null,
      });
      setSumMsg(m => ({ ...m, [orchId]: 'Saved' }));
      setTimeout(() => setSumMsg(m => ({ ...m, [orchId]: '' })), 2500);
    } catch (e: unknown) {
      setSumMsg(m => ({ ...m, [orchId]: e instanceof Error ? e.message : 'Failed' }));
    } finally { setSumSaving(null); }
  }

  async function handleSaveAgentParams(agentId: string) {
    const inputs = agentParamInputs[agentId] ?? {};
    const nonEmpty = Object.fromEntries(Object.entries(inputs).filter(([, v]) => v.trim() !== ''));
    if (Object.keys(nonEmpty).length === 0) return;
    setAgentParamSaving(agentId);
    try {
      await themApi.putAgentParams(app.id, agentId, nonEmpty);
      const updated = await themApi.getAgentParams(app.id, agentId);
      setAgentParamsList(prev => prev.map(a => a.agent_id === agentId ? updated : a));
      setAgentParamInputs(prev => ({ ...prev, [agentId]: {} }));
      setAgentParamMsg(m => ({ ...m, [agentId]: 'Saved' }));
      setTimeout(() => setAgentParamMsg(m => ({ ...m, [agentId]: '' })), 2500);
    } catch (e: unknown) {
      setAgentParamMsg(m => ({ ...m, [agentId]: e instanceof Error ? e.message : 'Failed' }));
    } finally { setAgentParamSaving(null); }
  }

  async function handleSaveNodeLLM(agentId: string, nodeId: string) {
    const draft = nodeLLMDrafts[nodeId];
    if (!draft?.provider || !draft?.model) return;
    const key = `${agentId}::${nodeId}`;
    setNodeLLMSaving(key);
    try {
      await themApi.putNodeLLMOverride(app.id, agentId, nodeId, draft.provider, draft.model);
      setAgentLLMNodes(prev => prev.map(n => n.node_id === nodeId ? { ...n, override_provider: draft.provider, override_model: draft.model } : n));
      setNodeLLMMsg(m => ({ ...m, [nodeId]: 'Saved' }));
      setTimeout(() => setNodeLLMMsg(m => ({ ...m, [nodeId]: '' })), 2500);
    } catch (e: unknown) {
      setNodeLLMMsg(m => ({ ...m, [nodeId]: e instanceof Error ? e.message : 'Failed' }));
    } finally { setNodeLLMSaving(null); }
  }

  async function handleAddAppParam() {
    const name = newParamName.trim();
    const value = newParamValue.trim();
    if (!name || !value) return;
    setAddParamSaving(true);
    try {
      await themApi.setAppParam(app.id, name, value, newParamType);
      setAppParams(await themApi.getAppParams(app.id) ?? []);
      setNewParamName(''); setNewParamValue(''); setNewParamType('string');
      setAddingParam(false); setAddParamMsg('');
    } catch (e: unknown) {
      setAddParamMsg(e instanceof Error ? e.message : 'Failed');
    } finally { setAddParamSaving(false); }
  }

  async function handleUpdateAppParam(name: string) {
    const value = (editParamInputs[name] ?? '').trim();
    if (!value) return;
    const param = appParams.find(p => p.name === name);
    if (!param) return;
    setParamSaving(name);
    try {
      await themApi.setAppParam(app.id, name, value, param.type);
      setAppParams(await themApi.getAppParams(app.id) ?? []);
      setEditParamInputs(prev => ({ ...prev, [name]: '' }));
      setParamMsg(m => ({ ...m, [name]: 'Saved' }));
      setTimeout(() => setParamMsg(m => ({ ...m, [name]: '' })), 2500);
    } catch (e: unknown) {
      setParamMsg(m => ({ ...m, [name]: e instanceof Error ? e.message : 'Failed' }));
    } finally { setParamSaving(null); }
  }

  async function handleDeleteAppParam(name: string) {
    setParamSaving(name);
    try {
      await themApi.deleteAppParam(app.id, name);
      setAppParams(await themApi.getAppParams(app.id) ?? []);
    } catch (e: unknown) {
      setParamMsg(m => ({ ...m, [name]: e instanceof Error ? e.message : 'Failed' }));
    } finally { setParamSaving(null); }
  }

  async function handleSave() {
    setSaving(true); setError(null);
    try {
      const parsedUsers  = usersInput.split(/[\s,]+/).map(s => s.trim()).filter(Boolean).map(Number).filter(n => !isNaN(n));
      const parsedTokens = tokensInput.split(/\n/).map(s => s.trim()).filter(Boolean);
      const payload = { ...cfg, blocked_tokens: parsedTokens, blocked_user_ids: parsedUsers };
      await themApi.putAppRuntime(app.id, payload);
      setCfg(payload); setSaved(true);
      setTimeout(() => setSaved(false), 2500);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally { setSaving(false); }
  }

  // ── Shared styles ─────────────────────────────────────────────────────────

  const field: React.CSSProperties = {
    width: '100%', padding: '9px 12px', borderRadius: 7,
    border: '1px solid rgba(255,255,255,0.1)', background: 'rgba(255,255,255,0.05)',
    color: C.text, fontSize: 13, outline: 'none', boxSizing: 'border-box',
  };
  const lbl: React.CSSProperties = {
    fontSize: 11, fontWeight: 600, color: C.textMuted, letterSpacing: '0.06em',
    textTransform: 'uppercase', marginBottom: 5, display: 'block',
  };
  const badge = (color: string, bg: string, border: string, text: string) => (
    <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 20, background: bg, color, border: `1px solid ${border}` }}>{text}</span>
  );
  const saveBtn = (onClick: () => void, busy: boolean, disabled: boolean, label = 'Save') => (
    <button onClick={onClick} disabled={busy || disabled} style={{ padding: '8px 14px', borderRadius: 7, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap', flexShrink: 0, opacity: busy || disabled ? 0.45 : 1 }}>
      {busy ? '…' : label}
    </button>
  );

  // ── Render ────────────────────────────────────────────────────────────────

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '36px 40px 64px', background: C.bg }}>

      {/* Breadcrumb */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 28 }}>
        <button onClick={onBack} style={{ background: 'none', border: 'none', color: C.textMuted, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 5, fontSize: 13 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 17 }}>arrow_back</span>
          Applications
        </button>
        <span style={{ color: 'rgba(255,255,255,0.18)', fontSize: 16 }}>/</span>
        <span style={{ fontSize: 14, color: C.text, fontWeight: 600 }}>{app.name}</span>
        <span style={{ color: 'rgba(255,255,255,0.18)', fontSize: 16 }}>/</span>
        <span style={{ fontSize: 14, color: '#fb923c', fontWeight: 700 }}>Runtime</span>
      </div>

      <div style={{ maxWidth: 680 }}>

        {/* ── 1. GLOBAL ──────────────────────────────────────────────────── */}
        <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: '0.1em', textTransform: 'uppercase', marginBottom: 8, paddingLeft: 4 }}>
          Global
        </div>

        {/* LLM Provider Keys */}
        <Section title="LLM Provider Keys" icon="key" accent="#fb923c"
          subtitle={setProviders.length > 0 ? `${setProviders.length} of ${PROVIDER_LIST.length} providers configured` : 'No providers configured yet'}>
          <div style={{ fontSize: 12, color: C.textMuted, marginTop: -4 }}>
            API keys are AES-GCM encrypted at rest. Configured providers are available for selection in orchestrators and canvas agents below.
          </div>
          {PROVIDER_LIST.map(provider => {
            const status = getKeyStatus(provider);
            const isBusy = keySaving === provider;
            const isTesting = keyTesting === provider;
            const msg = keyMsg[provider] ?? '';
            const testMsg = keyTestMsg[provider] ?? '';
            const isErr = msg && msg !== 'Saved' && msg !== 'Removed';
            const isTestErr = testMsg && !testMsg.startsWith('✓');
            return (
              <div key={provider} style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: `1px solid ${status.key_set ? 'rgba(74,222,128,0.15)' : 'rgba(255,255,255,0.07)'}` }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, width: 130, flexShrink: 0 }}>
                    <span style={{ fontSize: 13, fontWeight: 700, color: C.text }}>{provider}</span>
                    {status.key_set
                      ? badge(C.green, 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', `set ···${status.key_hint ?? ''}`)
                      : badge('#fb923c', 'rgba(251,146,60,0.1)', 'rgba(251,146,60,0.3)', 'not set')}
                  </div>
                  <input
                    type="password"
                    placeholder={status.key_set ? 'Replace key…' : 'Paste API key…'}
                    value={keyInputs[provider] ?? ''}
                    onChange={e => setKeyInputs(ki => ({ ...ki, [provider]: e.target.value }))}
                    onKeyDown={e => { if (e.key === 'Enter') handleSaveKey(provider); }}
                    style={{ ...field, flex: 1, minWidth: 160 }}
                  />
                  {saveBtn(() => handleSaveKey(provider), isBusy, !(keyInputs[provider] ?? '').trim())}
                  {status.key_set && (
                    <button onClick={() => handleTestKey(provider)} disabled={isBusy || isTesting}
                      style={{ padding: '8px 12px', borderRadius: 7, border: '1px solid rgba(74,222,128,0.3)', background: 'rgba(74,222,128,0.07)', color: C.green, cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy || isTesting ? 0.5 : 1 }}>
                      {isTesting ? '…' : 'Test'}
                    </button>
                  )}
                  {status.key_set && (
                    <button onClick={() => handleDeleteKey(provider)} disabled={isBusy}
                      style={{ padding: '8px 10px', borderRadius: 7, border: '1px solid rgba(248,113,113,0.25)', background: 'rgba(248,113,113,0.06)', color: '#f87171', cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy ? 0.5 : 1 }}>
                      Remove
                    </button>
                  )}
                  {msg && <span style={{ fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{msg}</span>}
                </div>
                {testMsg && <div style={{ marginTop: 6, fontSize: 12, color: isTestErr ? C.error : C.green, fontWeight: 600, paddingLeft: 138 }}>{testMsg}</div>}
              </div>
            );
          })}
        </Section>

        {/* App Global Parameters */}
        <Section title="Global Parameters" icon="variable_insert" accent="#fb923c"
          subtitle={appParams.length > 0 ? `${appParams.length} parameter${appParams.length !== 1 ? 's' : ''} — referenced by canvas agent nodes` : 'Shared named values for canvas agent HTTP and LLM nodes'}>
          <div style={{ fontSize: 12, color: C.textMuted, marginTop: -4 }}>
            Values here are referenced by name in canvas agent nodes via <code style={{ fontFamily: 'monospace', fontSize: 11, background: 'rgba(255,255,255,0.07)', padding: '1px 5px', borderRadius: 4 }}>app_param_ref</code>. Secrets are encrypted and never shown in full.
          </div>

          {appParams.map(param => {
            const isBusy = paramSaving === param.name;
            const msg = paramMsg[param.name] ?? '';
            const isErr = msg && msg !== 'Saved';
            const isSecret = param.type === 'secret';
            const editVal = editParamInputs[param.name] ?? '';
            return (
              <div key={param.name} style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.1)' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 7 }}>
                  <code style={{ fontSize: 12, fontWeight: 700, color: C.text, fontFamily: 'JetBrains Mono, monospace', flex: 1 }}>{param.name}</code>
                  <span style={{ fontSize: 10, fontWeight: 600, padding: '2px 6px', borderRadius: 20, background: 'rgba(132,158,190,0.1)', color: C.textMuted, border: '1px solid rgba(132,158,190,0.18)' }}>{param.type}</span>
                  {param.is_set && badge(C.green, 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', isSecret ? `set ···${param.value_hint ?? ''}` : 'set')}
                </div>
                {!isSecret && param.is_set && param.value && (
                  <div style={{ fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: C.textMuted, marginBottom: 7, wordBreak: 'break-all' }}>{param.value}</div>
                )}
                <div style={{ display: 'flex', gap: 6 }}>
                  <input
                    type={isSecret ? 'password' : 'text'}
                    placeholder={param.is_set ? 'Replace value…' : 'Enter value…'}
                    value={editVal}
                    onChange={e => setEditParamInputs(prev => ({ ...prev, [param.name]: e.target.value }))}
                    onKeyDown={e => { if (e.key === 'Enter') handleUpdateAppParam(param.name); }}
                    style={{ ...field, flex: 1, minWidth: 0 }}
                  />
                  {saveBtn(() => handleUpdateAppParam(param.name), isBusy, !editVal.trim(), 'Update')}
                  <button onClick={() => handleDeleteAppParam(param.name)} disabled={isBusy}
                    style={{ padding: '8px 10px', borderRadius: 7, border: '1px solid rgba(248,113,113,0.25)', background: 'rgba(248,113,113,0.06)', color: '#f87171', cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy ? 0.5 : 1 }}>
                    Remove
                  </button>
                </div>
                {msg && <div style={{ marginTop: 5, fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
              </div>
            );
          })}

          {!addingParam ? (
            <button onClick={() => setAddingParam(true)}
              style={{ width: '100%', padding: '8px 0', borderRadius: 7, border: `1px dashed ${C.outline}`, background: 'transparent', color: C.textMuted, cursor: 'pointer', fontSize: 12, fontWeight: 600 }}>
              + Add parameter
            </button>
          ) : (
            <div style={{ padding: '12px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.18)' }}>
              <div style={{ ...lbl, marginBottom: 10 }}>New Parameter</div>
              <div style={{ display: 'flex', gap: 6, marginBottom: 8 }}>
                <input placeholder="name (a-z0-9_)" value={newParamName}
                  onChange={e => setNewParamName(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
                  style={{ ...field, flex: 2, fontFamily: 'JetBrains Mono, monospace' }} />
                <select value={newParamType} onChange={e => setNewParamType(e.target.value)}
                  style={{ ...field, flex: 1 }}>
                  {['string', 'secret', 'url', 'int', 'bool'].map(t => <option key={t} value={t}>{t}</option>)}
                </select>
              </div>
              <input
                type={newParamType === 'secret' ? 'password' : 'text'}
                placeholder="Value…" value={newParamValue}
                onChange={e => setNewParamValue(e.target.value)}
                style={{ ...field, marginBottom: 8 }} />
              {addParamMsg && <div style={{ fontSize: 12, color: C.error, fontWeight: 600, marginBottom: 6 }}>{addParamMsg}</div>}
              <div style={{ display: 'flex', gap: 6 }}>
                {saveBtn(handleAddAppParam, addParamSaving, !newParamName.trim() || !newParamValue.trim())}
                <button onClick={() => { setAddingParam(false); setNewParamName(''); setNewParamValue(''); setNewParamType('string'); setAddParamMsg(''); }}
                  style={{ padding: '8px 14px', borderRadius: 7, border: `1px solid ${C.outline}`, background: 'transparent', color: C.textMuted, cursor: 'pointer', fontSize: 12 }}>
                  Cancel
                </button>
              </div>
            </div>
          )}
        </Section>

        {/* ── 2. ORCHESTRATORS ───────────────────────────────────────────── */}
        {orchLLMs.length > 0 && (
          <>
            <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: '0.1em', textTransform: 'uppercase', margin: '24px 0 8px', paddingLeft: 4 }}>
              Orchestrators
            </div>
            <Section title="LLM Assignment" icon="hub"
              subtitle="Select which provider and model each orchestrator uses. Only providers with a saved key are available.">
              {orchLLMs.map(orch => {
                const isBusy = orchSaving === orch.id;
                const msg = orchMsg[orch.id] ?? '';
                const isErr = msg && msg !== 'Saved';
                const canSave = orch.provider && orch.model;
                return (
                  <div key={orch.id} style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.1)' }}>
                    <div style={{ fontSize: 12, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 8 }}>
                      {orch.displayName || orch.name}
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <select value={orch.provider}
                        onChange={e => {
                          const p = e.target.value;
                          setOrchLLMs(prev => prev.map(o => {
                            if (o.id !== orch.id) return o;
                            const models = RUNTIME_MODELS[p] ?? [];
                            return { ...o, provider: p, model: models.includes(o.model) ? o.model : (models[0] ?? '') };
                          }));
                        }}
                        style={{ ...field, width: 160, flexShrink: 0 }}>
                        <option value="">— provider —</option>
                        {setProviders.map(p => <option key={p} value={p}>{p}</option>)}
                      </select>
                      <select value={orch.model} disabled={!orch.provider}
                        onChange={e => setOrchLLMs(prev => prev.map(o => o.id === orch.id ? { ...o, model: e.target.value } : o))}
                        style={{ ...field, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                        <option value="">— model —</option>
                        {(RUNTIME_MODELS[orch.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                      </select>
                      {saveBtn(() => handleSaveOrchLLM(orch.id), isBusy, !canSave)}
                    </div>
                    {msg && <div style={{ marginTop: 6, fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
                  </div>
                );
              })}
            </Section>

            <Section title="Memory & Summarizer" icon="memory" defaultOpen={false}
              subtitle="Per-orchestrator context summarization. Enable to compress long conversations; configure which model summarizes.">
              {orchSummarizers.map(s => {
                const isBusy = sumSaving === s.id;
                const msg = sumMsg[s.id] ?? '';
                const isErr = msg && msg !== 'Saved';
                return (
                  <div key={s.id} style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.1)' }}>
                    <div style={{ fontSize: 12, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 10 }}>
                      {s.displayName || s.name}
                    </div>

                    {/* Enable toggle */}
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
                      <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
                        <input type="checkbox" checked={s.memoryEnabled}
                          onChange={e => setOrchSummarizers(prev => prev.map(o => o.id === s.id ? { ...o, memoryEnabled: e.target.checked } : o))}
                          style={{ accentColor: C.purple, width: 14, height: 14 }} />
                        <span style={{ fontSize: 12, fontWeight: 600, color: C.text }}>Enable summarization</span>
                      </label>
                    </div>

                    {/* Numeric settings */}
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10, opacity: s.memoryEnabled ? 1 : 0.45 }}>
                      <div>
                        <label style={lbl}>Summarize every N turns</label>
                        <input type="number" min={1} value={s.summarizeEveryN} disabled={!s.memoryEnabled} style={field}
                          onChange={e => setOrchSummarizers(prev => prev.map(o => o.id === s.id ? { ...o, summarizeEveryN: parseInt(e.target.value) || 10 } : o))} />
                      </div>
                      <div>
                        <label style={lbl}>Keep last N verbatim</label>
                        <input type="number" min={0} value={s.fallbackN} disabled={!s.memoryEnabled} style={field}
                          onChange={e => setOrchSummarizers(prev => prev.map(o => o.id === s.id ? { ...o, fallbackN: parseInt(e.target.value) || 0 } : o))} />
                      </div>
                    </div>

                    {/* Summarizer LLM — same provider/model pattern */}
                    <div style={{ opacity: s.memoryEnabled ? 1 : 0.45 }}>
                      <label style={lbl}>Summarizer model (optional — defaults to orchestrator LLM)</label>
                      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                        <select value={s.provider} disabled={!s.memoryEnabled}
                          onChange={e => {
                            const p = e.target.value;
                            setOrchSummarizers(prev => prev.map(o => {
                              if (o.id !== s.id) return o;
                              const models = RUNTIME_MODELS[p] ?? [];
                              return { ...o, provider: p, model: models.includes(o.model) ? o.model : (models[0] ?? '') };
                            }));
                          }}
                          style={{ ...field, width: 160, flexShrink: 0 }}>
                          <option value="">— same as orchestrator —</option>
                          {setProviders.map(p => <option key={p} value={p}>{p}</option>)}
                        </select>
                        <select value={s.model} disabled={!s.memoryEnabled || !s.provider}
                          onChange={e => setOrchSummarizers(prev => prev.map(o => o.id === s.id ? { ...o, model: e.target.value } : o))}
                          style={{ ...field, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                          <option value="">— model —</option>
                          {(RUNTIME_MODELS[s.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                        </select>
                        {saveBtn(() => handleSaveOrchSummarizer(s.id), isBusy, false)}
                      </div>
                    </div>
                    {msg && <div style={{ marginTop: 6, fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
                  </div>
                );
              })}
            </Section>
          </>
        )}

        {/* ── 3. CANVAS AGENTS ───────────────────────────────────────────── */}
        {agentIds.length > 0 && (
          <>
            <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: '0.1em', textTransform: 'uppercase', margin: '24px 0 8px', paddingLeft: 4 }}>
              Canvas Agents
            </div>

            {agentIds.map(agentId => {
              const nodes = agentLLMNodes.filter(n => n.agent_id === agentId);
              const agentParams = agentParamsList.find(a => a.agent_id === agentId);
              const slug = nodes[0]?.agent_slug ?? agentParams?.agent_slug ?? agentId;
              const paramsBusy = agentParamSaving === agentId;
              const paramsMsg = agentParamMsg[agentId] ?? '';
              const paramsErr = paramsMsg && paramsMsg !== 'Saved';
              const paramInputs = agentParamInputs[agentId] ?? {};
              const hasParamInput = Object.values(paramInputs).some(v => v.trim() !== '');

              // Classify params: HTTP-relevant (url/string used by http nodes) vs other
              const httpParams = agentParams?.required_params.filter(p =>
                p.used_by_nodes?.some(n => n.toLowerCase().includes('http')) ||
                p.type === 'url'
              ) ?? [];
              const otherParams = agentParams?.required_params.filter(p => !httpParams.includes(p)) ?? [];

              return (
                <AgentSection key={agentId} slug={slug}>

                  {/* LLM Nodes */}
                  {nodes.length > 0 && (
                    <div>
                      <AgentSubLabel icon="psychology" label="LLM Nodes" />
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                        {nodes.map(node => {
                          const key = `${agentId}::${node.node_id}`;
                          const isBusy = nodeLLMSaving === key;
                          const msg = nodeLLMMsg[node.node_id] ?? '';
                          const isErr = msg && msg !== 'Saved';
                          const draft = nodeLLMDrafts[node.node_id] ?? { provider: '', model: '' };
                          const canSave = draft.provider && draft.model;
                          const isOverridden = !!(node.override_provider && node.override_model);
                          return (
                            <div key={node.node_id} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: `1px solid ${isOverridden ? 'rgba(251,146,60,0.2)' : 'rgba(255,255,255,0.07)'}` }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                                <span style={{ flex: 1, fontSize: 12, fontWeight: 600, color: C.text }}>{node.label || node.node_id}</span>
                                <span style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>
                                  default: {node.compiled_provider}/{node.compiled_model}
                                </span>
                                {isOverridden && badge('#fb923c', 'rgba(251,146,60,0.1)', 'rgba(251,146,60,0.3)', 'overridden')}
                              </div>
                              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                                {/* Only configured providers shown */}
                                <select value={draft.provider}
                                  onChange={e => {
                                    const p = e.target.value;
                                    setNodeLLMDrafts(prev => {
                                      const models = RUNTIME_MODELS[p] ?? [];
                                      const prevModel = prev[node.node_id]?.model ?? '';
                                      return { ...prev, [node.node_id]: { provider: p, model: models.includes(prevModel) ? prevModel : (models[0] ?? '') } };
                                    });
                                  }}
                                  style={{ ...field, width: 150, flexShrink: 0 }}>
                                  <option value="">— provider —</option>
                                  {setProviders.length > 0
                                    ? setProviders.map(p => <option key={p} value={p}>{p}</option>)
                                    : PROVIDER_LIST.map(p => <option key={p} value={p}>{p}</option>)
                                  }
                                </select>
                                <select value={draft.model} disabled={!draft.provider}
                                  onChange={e => setNodeLLMDrafts(prev => ({ ...prev, [node.node_id]: { ...draft, model: e.target.value } }))}
                                  style={{ ...field, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                                  <option value="">— model —</option>
                                  {(RUNTIME_MODELS[draft.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                                </select>
                                {saveBtn(() => handleSaveNodeLLM(agentId, node.node_id), isBusy, !canSave)}
                              </div>
                              {msg && <div style={{ marginTop: 6, fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  )}

                  {/* HTTP Params */}
                  {httpParams.length > 0 && (
                    <div>
                      <AgentSubLabel icon="http" label="HTTP Nodes" />
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                        {httpParams.map(param => {
                          const isSecret = param.type === 'secret';
                          const currentVal = paramInputs[param.key] ?? '';
                          return (
                            <div key={param.key} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.07)' }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                                <label style={{ ...lbl, marginBottom: 0, flex: 1 }}>{param.label}</label>
                                {param.required && !param.is_set && badge('#f87171', 'rgba(248,113,113,0.1)', 'rgba(248,113,113,0.3)', 'required')}
                                {param.is_set && badge(C.green, 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', `set ···${param.hint}`)}
                              </div>
                              {param.description && <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 6 }}>{param.description}</div>}
                              <input
                                type={isSecret ? 'password' : 'text'}
                                placeholder={param.is_set ? 'Replace…' : (param.default_value ? `default: ${param.default_value}` : 'Enter value…')}
                                value={currentVal}
                                onChange={e => setAgentParamInputs(prev => ({ ...prev, [agentId]: { ...(prev[agentId] ?? {}), [param.key]: e.target.value } }))}
                                style={{ ...field }}
                              />
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  )}

                  {/* Other Params */}
                  {otherParams.length > 0 && (
                    <div>
                      <AgentSubLabel icon="tune" label="Parameters" />
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                        {otherParams.map(param => {
                          const isSecret = param.type === 'secret';
                          const currentVal = paramInputs[param.key] ?? '';
                          return (
                            <div key={param.key} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.07)' }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                                <label style={{ ...lbl, marginBottom: 0, flex: 1 }}>{param.label}</label>
                                {param.required && !param.is_set && badge('#f87171', 'rgba(248,113,113,0.1)', 'rgba(248,113,113,0.3)', 'required')}
                                {param.is_set && badge(C.green, 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', `set ···${param.hint}`)}
                              </div>
                              {param.description && <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 6 }}>{param.description}</div>}
                              <input
                                type={isSecret ? 'password' : 'text'}
                                placeholder={param.is_set ? 'Replace…' : (param.default_value ? `default: ${param.default_value}` : 'Enter value…')}
                                value={currentVal}
                                onChange={e => setAgentParamInputs(prev => ({ ...prev, [agentId]: { ...(prev[agentId] ?? {}), [param.key]: e.target.value } }))}
                                style={{ ...field }}
                              />
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  )}

                  {/* Save params button — spans all param types */}
                  {agentParams && agentParams.required_params.length > 0 && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      {saveBtn(() => handleSaveAgentParams(agentId), paramsBusy, !hasParamInput, 'Save Parameters')}
                      {paramsMsg && <span style={{ fontSize: 12, color: paramsErr ? C.error : C.green, fontWeight: 600 }}>{paramsMsg}</span>}
                    </div>
                  )}
                </AgentSection>
              );
            })}
          </>
        )}

        {/* ── 4. POLICY ──────────────────────────────────────────────────── */}
        <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: '0.1em', textTransform: 'uppercase', margin: '24px 0 8px', paddingLeft: 4 }}>
          Policy
        </div>

        <Section title="Session Limits" icon="timer" defaultOpen={false}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
            <div>
              <label style={lbl}>Max Concurrent Sessions</label>
              <input type="number" min={1} placeholder="Unlimited" value={cfg.max_concurrent_sessions ?? ''} style={field}
                onChange={e => setCfg(c => ({ ...c, max_concurrent_sessions: e.target.value === '' ? null : parseInt(e.target.value) }))} />
              <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>App-wide soft cap. Empty = unlimited.</div>
            </div>
            <div>
              <label style={lbl}>Session Timeout (min)</label>
              <input type="number" min={1} placeholder="No timeout" value={cfg.session_timeout_minutes ?? ''} style={field}
                onChange={e => setCfg(c => ({ ...c, session_timeout_minutes: e.target.value === '' ? null : parseInt(e.target.value) }))} />
              <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Advisory. Empty = no timeout.</div>
            </div>
          </div>
        </Section>

        <Section title="Rate Limiting" icon="speed" defaultOpen={false}>
          <label style={lbl}>Requests per minute</label>
          <input type="number" min={1} placeholder="Unlimited" value={cfg.rate_limit_rpm ?? ''} style={field}
            onChange={e => setCfg(c => ({ ...c, rate_limit_rpm: e.target.value === '' ? null : parseInt(e.target.value) }))} />
          <div style={{ fontSize: 11, color: C.textMuted, marginTop: -8 }}>Applied across all entry points of this app.</div>
        </Section>

        <Section title="Access Control" icon="block" defaultOpen={false}>
          <div>
            <label style={lbl}>Blocked User IDs (comma-separated)</label>
            <input type="text" placeholder="e.g. 42, 107, 889" value={usersInput} style={field}
              onChange={e => setUsersInput(e.target.value)} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Connections from these user IDs are rejected immediately.</div>
          </div>
          <div>
            <label style={lbl}>Blocked Token Hashes (one per line)</label>
            <textarea placeholder="SHA-256 hash of each blocked token" value={tokensInput} rows={3}
              style={{ ...field, resize: 'vertical', fontFamily: 'monospace', fontSize: 12 }}
              onChange={e => setTokensInput(e.target.value)} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Paste the SHA-256 hash of the token — not the raw token. One per line.</div>
          </div>
        </Section>

        {/* Save policy */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 8 }}>
          <button onClick={handleSave} disabled={saving}
            style={{ padding: '11px 28px', borderRadius: 8, border: 'none', cursor: saving ? 'not-allowed' : 'pointer', background: '#fb923c', color: '#000', fontSize: 14, fontWeight: 700, opacity: saving ? 0.6 : 1 }}>
            {saving ? 'Saving…' : 'Save Policy'}
          </button>
          {saved  && <span style={{ fontSize: 13, color: C.green }}>Saved</span>}
          {error  && <span style={{ fontSize: 13, color: C.error }}>{error}</span>}
        </div>

      </div>
    </div>
  );
}
