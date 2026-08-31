'use client';
import { useState, useEffect } from 'react';
import { themApi, type Application, type AgentParamsResponse, type AppGlobalParam, type AgentLLMNodeStatus } from '@/lib/api';
import { C, PROVIDER_LIST, RUNTIME_MODELS } from '../constants';
import { Section, sharedField, sharedLbl, badge, makeSaveBtn } from './RuntimeShared';
import { EPSections } from './RuntimeEPSections';
import { CanvasAgentsSection } from './RuntimeAgentsSection';
import type { VoiceDraft } from './RuntimeVoicePanel';

type KeyStatus   = { provider: string; key_set: boolean; key_hint?: string };
type OrchMeta    = { id: string; name: string; displayName: string };
type EPLLMDraft  = { provider: string; model: string };
type EPSumDraft  = { memoryEnabled: boolean; summarizeEveryN: number; fallbackN: number; provider: string; model: string };
type NodeLLMDraft = { provider: string; model: string };

export function RuntimeView({ app, onBack }: { app: Application; onBack: () => void }) {
  const emptyRuntime = { max_concurrent_sessions: null, rate_limit_rpm: null, blocked_tokens: [], blocked_user_ids: [], session_timeout_minutes: null };
  const [cfg, setCfg]         = useState<import('@/lib/api').AppRuntimeConfig>(app.runtime_config ?? emptyRuntime);
  const [saving, setSaving]   = useState(false);
  const [saved, setSaved]     = useState(false);
  const [error, setError]     = useState<string | null>(null);
  const [tokensInput, setTokensInput] = useState((app.runtime_config?.blocked_tokens ?? []).join('\n'));
  const [usersInput,  setUsersInput]  = useState((app.runtime_config?.blocked_user_ids ?? []).join(', '));

  const [keyStatuses, setKeyStatuses] = useState<KeyStatus[]>([]);
  const [keyInputs,   setKeyInputs]   = useState<Record<string, string>>({});
  const [keySaving,   setKeySaving]   = useState<string | null>(null);
  const [keyMsg,      setKeyMsg]      = useState<Record<string, string>>({});
  const [keyTestMsg,  setKeyTestMsg]  = useState<Record<string, string>>({});
  const [keyTesting,  setKeyTesting]  = useState<string | null>(null);

  const [orchMetas, setOrchMetas] = useState<OrchMeta[]>(
    (app.app_orchestrators ?? []).map(o => ({ id: o.id, name: o.name, displayName: o.display_name || o.name }))
  );
  const [voiceDrafts,  setVoiceDrafts]  = useState<Record<string, VoiceDraft>>(() => {
    const init: Record<string, VoiceDraft> = {};
    (app.app_orchestrators ?? []).forEach(o => {
      init[o.id] = { stt_provider: o.transcription_provider ?? '', stt_model: o.transcription_model ?? '', tts_provider: o.tts_provider ?? '', tts_voice: o.tts_voice ?? '', tts_model: 'tts-1', voice_enabled: o.voice_enabled ?? false, tts_enabled: o.tts_enabled ?? false };
    });
    return init;
  });
  const [voiceSaving,  setVoiceSaving]  = useState<string | null>(null);
  const [voiceMsg,     setVoiceMsg]     = useState<Record<string, string>>({});
  const [voiceTesting, setVoiceTesting] = useState<string | null>(null);
  const [voiceTestMsg, setVoiceTestMsg] = useState<Record<string, string>>({});
  const [ttsTesting,   setTtsTesting]   = useState<string | null>(null);
  const [ttsTestMsg,   setTtsTestMsg]   = useState<Record<string, string>>({});

  const [epLLMDrafts,  setEPLLMDrafts]  = useState<Record<string, EPLLMDraft>>(() => { const i: Record<string, EPLLMDraft> = {}; (app.entry_points ?? []).forEach(ep => { i[ep.id] = { provider: ep.llm_provider ?? '', model: ep.llm_model ?? '' }; }); return i; });
  const [epLLMSaving,  setEPLLMSaving]  = useState<string | null>(null);
  const [epLLMMsg,     setEPLLMMsg]     = useState<Record<string, string>>({});
  const [entryPoints,  setEntryPoints]  = useState<import('@/lib/api').EntryPoint[]>(app.entry_points ?? []);
  const [epSumDrafts,  setEPSumDrafts]  = useState<Record<string, EPSumDraft>>(() => { const i: Record<string, EPSumDraft> = {}; (app.entry_points ?? []).forEach(ep => { i[ep.id] = { memoryEnabled: ep.memory_enabled ?? false, summarizeEveryN: ep.summarize_every_n_calls ?? 10, fallbackN: ep.memory_raw_fallback_n ?? 3, provider: ep.summarizer_provider ?? '', model: ep.summarizer_model ?? '' }; }); return i; });
  const [epSumSaving,  setEPSumSaving]  = useState<string | null>(null);
  const [epSumMsg,     setEPSumMsg]     = useState<Record<string, string>>({});
  const [epToggling,   setEPToggling]   = useState<string | null>(null);

  const [agentParamsList,  setAgentParamsList]  = useState<AgentParamsResponse[]>([]);
  const [agentParamInputs, setAgentParamInputs] = useState<Record<string, Record<string, string>>>({});
  const [agentParamSaving, setAgentParamSaving] = useState<string | null>(null);
  const [agentParamMsg,    setAgentParamMsg]    = useState<Record<string, string>>({});
  const [agentLLMNodes,    setAgentLLMNodes]    = useState<AgentLLMNodeStatus[]>([]);
  const [nodeLLMDrafts,    setNodeLLMDrafts]    = useState<Record<string, NodeLLMDraft>>({});
  const [nodeLLMSaving,    setNodeLLMSaving]    = useState<string | null>(null);
  const [nodeLLMMsg,       setNodeLLMMsg]       = useState<Record<string, string>>({});

  const [appParams,       setAppParams]       = useState<AppGlobalParam[]>([]);
  const [newParamName,    setNewParamName]    = useState('');
  const [newParamType,    setNewParamType]    = useState('string');
  const [newParamValue,   setNewParamValue]   = useState('');
  const [editParamInputs, setEditParamInputs] = useState<Record<string, string>>({});
  const [paramSaving,     setParamSaving]     = useState<string | null>(null);
  const [paramMsg,        setParamMsg]        = useState<Record<string, string>>({});
  const [addingParam,     setAddingParam]     = useState(false);
  const [addParamSaving,  setAddParamSaving]  = useState(false);
  const [addParamMsg,     setAddParamMsg]     = useState('');

  useEffect(() => { themApi.getProviderKeys(app.id).then(setKeyStatuses).catch(() => {}); }, [app.id]);
  useEffect(() => {
    themApi.getApplication(app.id).then(fresh => {
      setOrchMetas((fresh.app_orchestrators ?? []).map(o => ({ id: o.id, name: o.name, displayName: o.display_name || o.name })));
      const d: Record<string, VoiceDraft> = {};
      (fresh.app_orchestrators ?? []).forEach(o => { d[o.id] = { stt_provider: o.transcription_provider ?? '', stt_model: o.transcription_model ?? '', tts_provider: o.tts_provider ?? '', tts_voice: o.tts_voice ?? '', tts_model: 'tts-1', voice_enabled: o.voice_enabled ?? false, tts_enabled: o.tts_enabled ?? false }; });
      setVoiceDrafts(d);
    }).catch(() => {});
  }, [app.id]);
  useEffect(() => {
    themApi.listEntryPoints(app.id).then(eps => {
      setEntryPoints(eps);
      const sd: Record<string, EPSumDraft> = {}; const ld: Record<string, EPLLMDraft> = {};
      eps.forEach(ep => {
        sd[ep.id] = { memoryEnabled: ep.memory_enabled ?? false, summarizeEveryN: ep.summarize_every_n_calls ?? 10, fallbackN: ep.memory_raw_fallback_n ?? 3, provider: ep.summarizer_provider ?? '', model: ep.summarizer_model ?? '' };
        ld[ep.id] = { provider: ep.llm_provider ?? '', model: ep.llm_model ?? '' };
      });
      setEPSumDrafts(sd); setEPLLMDrafts(ld);
    }).catch(() => {});
  }, [app.id]);
  useEffect(() => { themApi.getAppParams(app.id).then(p => setAppParams(p ?? [])).catch(() => {}); }, [app.id]);
  useEffect(() => {
    themApi.listAgentBindings(app.id).then(bindings => {
      Promise.all(bindings.map(b => themApi.getAgentParams(app.id, b.agent_id).catch(() => null)))
        .then(r => setAgentParamsList(r.filter((x): x is AgentParamsResponse => x !== null && x.required_params.length > 0)));
      Promise.all(bindings.map(b => themApi.getAgentLLMNodes(app.id, b.agent_id).catch(() => null)))
        .then(r => { const nodes = r.flatMap(x => x ?? []); setAgentLLMNodes(nodes); const d: Record<string, NodeLLMDraft> = {}; nodes.forEach(n => { d[n.node_id] = { provider: n.override_provider ?? n.compiled_provider ?? '', model: n.override_model ?? n.compiled_model ?? '' }; }); setNodeLLMDrafts(d); });
    }).catch(() => {});
  }, [app.id]);

  const setProviders = keyStatuses.filter(k => k.key_set).map(k => k.provider);
  const getKeyStatus = (p: string): KeyStatus => keyStatuses.find(k => k.provider === p) ?? { provider: p, key_set: false };
  const agentIds = [...new Set([...agentLLMNodes.map(n => n.agent_id), ...agentParamsList.map(a => a.agent_id)])];

  const f = sharedField;
  const l = sharedLbl;
  const saveBtn = makeSaveBtn();

  async function handleToggleEP(epId: string, cur: boolean) {
    setEPToggling(epId);
    try { await themApi.patchEntryPoint(app.id, epId, { enabled: !cur }); setEntryPoints(prev => prev.map(ep => ep.id === epId ? { ...ep, enabled: !cur } : ep)); }
    catch { /* ignore */ } finally { setEPToggling(null); }
  }
  async function handleSaveEPLLM(epId: string) {
    const d = epLLMDrafts[epId]; if (!d) return; setEPLLMSaving(epId);
    try { await themApi.patchEntryPointLLM(app.id, epId, { llm_provider: d.provider || null, llm_model: d.model || null }); setEPLLMMsg(m => ({ ...m, [epId]: 'Saved' })); setTimeout(() => setEPLLMMsg(m => ({ ...m, [epId]: '' })), 2500); }
    catch (e: unknown) { setEPLLMMsg(m => ({ ...m, [epId]: e instanceof Error ? e.message : 'Failed' })); } finally { setEPLLMSaving(null); }
  }
  async function handleSaveEPSummarizer(epId: string) {
    const d = epSumDrafts[epId]; if (!d) return; setEPSumSaving(epId);
    try { await themApi.patchEntryPointSummarizer(app.id, epId, { memory_enabled: d.memoryEnabled, summarize_every_n_calls: d.summarizeEveryN, memory_raw_fallback_n: d.fallbackN, summarizer_provider: d.provider || null, summarizer_model: d.model || null }); setEPSumMsg(m => ({ ...m, [epId]: 'Saved' })); setTimeout(() => setEPSumMsg(m => ({ ...m, [epId]: '' })), 2500); }
    catch (e: unknown) { setEPSumMsg(m => ({ ...m, [epId]: e instanceof Error ? e.message : 'Failed' })); } finally { setEPSumSaving(null); }
  }
  async function handleSaveVoice(orchId: string) {
    const d = voiceDrafts[orchId]; if (!d) return; setVoiceSaving(orchId);
    try { await themApi.patchOrchestratorVoice(app.id, orchId, { stt_provider: d.stt_provider, stt_model: d.stt_model, tts_provider: d.tts_provider, tts_voice: d.tts_voice, voice_enabled: d.voice_enabled, tts_enabled: d.tts_enabled }); setVoiceMsg(m => ({ ...m, [orchId]: 'Saved' })); setTimeout(() => setVoiceMsg(m => ({ ...m, [orchId]: '' })), 2500); }
    catch (e: unknown) { setVoiceMsg(m => ({ ...m, [orchId]: e instanceof Error ? e.message : 'Failed' })); } finally { setVoiceSaving(null); }
  }
  async function handleTestSTT(orchId: string) {
    const d = voiceDrafts[orchId]; if (!d?.stt_provider) return; setVoiceTesting(orchId); setVoiceTestMsg(m => ({ ...m, [orchId]: '' }));
    try { const r = await themApi.testAppOrchVoice(app.id, orchId, { provider: d.stt_provider, model: d.stt_model }); setVoiceTestMsg(m => ({ ...m, [orchId]: r.ok ? `✓ STT ${r.latency_ms}ms` : (r.error ?? 'Failed') })); }
    catch (e: unknown) { setVoiceTestMsg(m => ({ ...m, [orchId]: e instanceof Error ? e.message : 'Error' })); } finally { setVoiceTesting(null); }
  }
  async function handleTestTTS(orchId: string) {
    const d = voiceDrafts[orchId]; if (!d?.tts_provider) return; setTtsTesting(orchId); setTtsTestMsg(m => ({ ...m, [orchId]: '' }));
    try { const r = await themApi.testAppOrchTts(app.id, orchId, { provider: d.tts_provider, voice: d.tts_voice }); setTtsTestMsg(m => ({ ...m, [orchId]: r.ok ? `✓ TTS ${r.latency_ms}ms` : (r.error ?? 'Failed') })); }
    catch (e: unknown) { setTtsTestMsg(m => ({ ...m, [orchId]: e instanceof Error ? e.message : 'Error' })); } finally { setTtsTesting(null); }
  }
  async function handleSaveKey(provider: string) {
    const key = (keyInputs[provider] ?? '').trim(); if (!key) return; setKeySaving(provider);
    try { await themApi.setProviderKey(app.id, provider, key); setKeyStatuses(await themApi.getProviderKeys(app.id)); setKeyInputs(ki => ({ ...ki, [provider]: '' })); setKeyMsg(m => ({ ...m, [provider]: 'Saved' })); setTimeout(() => setKeyMsg(m => ({ ...m, [provider]: '' })), 2500); }
    catch (e: unknown) { setKeyMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Failed' })); } finally { setKeySaving(null); }
  }
  async function handleDeleteKey(provider: string) {
    setKeySaving(provider);
    try { await themApi.deleteProviderKey(app.id, provider); setKeyStatuses(await themApi.getProviderKeys(app.id)); setKeyMsg(m => ({ ...m, [provider]: 'Removed' })); setTimeout(() => setKeyMsg(m => ({ ...m, [provider]: '' })), 2500); }
    catch (e: unknown) { setKeyMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Failed' })); } finally { setKeySaving(null); }
  }
  async function handleTestKey(provider: string) {
    setKeyTesting(provider); setKeyTestMsg(m => ({ ...m, [provider]: '' }));
    try {
      if (!RUNTIME_MODELS[provider]) { setKeyTestMsg(m => ({ ...m, [provider]: 'Key saved — test via an orchestrator voice panel' })); return; }
      const r = await themApi.testAppLlm(app.id, provider, RUNTIME_MODELS[provider][0]);
      setKeyTestMsg(m => ({ ...m, [provider]: r.ok ? `✓ ${r.latency_ms}ms` : (r.error ?? 'Failed') }));
    } catch (e: unknown) { setKeyTestMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Error' })); } finally { setKeyTesting(null); }
  }
  async function handleSaveNodeLLM(agentId: string, nodeId: string) {
    const d = nodeLLMDrafts[nodeId]; if (!d?.provider || !d?.model) return; const key = `${agentId}::${nodeId}`; setNodeLLMSaving(key);
    try { await themApi.putNodeLLMOverride(app.id, agentId, nodeId, d.provider, d.model); setAgentLLMNodes(prev => prev.map(n => n.node_id === nodeId ? { ...n, override_provider: d.provider, override_model: d.model } : n)); setNodeLLMMsg(m => ({ ...m, [nodeId]: 'Saved' })); setTimeout(() => setNodeLLMMsg(m => ({ ...m, [nodeId]: '' })), 2500); }
    catch (e: unknown) { setNodeLLMMsg(m => ({ ...m, [nodeId]: e instanceof Error ? e.message : 'Failed' })); } finally { setNodeLLMSaving(null); }
  }
  async function handleSaveAgentParams(agentId: string) {
    const inputs = agentParamInputs[agentId] ?? {}; const nonEmpty = Object.fromEntries(Object.entries(inputs).filter(([, v]) => v.trim() !== '')); if (!Object.keys(nonEmpty).length) return; setAgentParamSaving(agentId);
    try { await themApi.putAgentParams(app.id, agentId, nonEmpty); setAgentParamsList(prev => prev.map(a => a.agent_id === agentId ? { ...a } : a)); setAgentParamInputs(prev => ({ ...prev, [agentId]: {} })); setAgentParamMsg(m => ({ ...m, [agentId]: 'Saved' })); setTimeout(() => setAgentParamMsg(m => ({ ...m, [agentId]: '' })), 2500); }
    catch (e: unknown) { setAgentParamMsg(m => ({ ...m, [agentId]: e instanceof Error ? e.message : 'Failed' })); } finally { setAgentParamSaving(null); }
  }
  async function handleAddAppParam() {
    const name = newParamName.trim(); const value = newParamValue.trim(); if (!name || !value) return; setAddParamSaving(true);
    try { await themApi.setAppParam(app.id, name, value, newParamType); setAppParams(await themApi.getAppParams(app.id) ?? []); setNewParamName(''); setNewParamValue(''); setNewParamType('string'); setAddingParam(false); setAddParamMsg(''); }
    catch (e: unknown) { setAddParamMsg(e instanceof Error ? e.message : 'Failed'); } finally { setAddParamSaving(false); }
  }
  async function handleUpdateAppParam(name: string) {
    const value = (editParamInputs[name] ?? '').trim(); if (!value) return; const param = appParams.find(p => p.name === name); if (!param) return; setParamSaving(name);
    try { await themApi.setAppParam(app.id, name, value, param.type); setAppParams(await themApi.getAppParams(app.id) ?? []); setEditParamInputs(prev => ({ ...prev, [name]: '' })); setParamMsg(m => ({ ...m, [name]: 'Saved' })); setTimeout(() => setParamMsg(m => ({ ...m, [name]: '' })), 2500); }
    catch (e: unknown) { setParamMsg(m => ({ ...m, [name]: e instanceof Error ? e.message : 'Failed' })); } finally { setParamSaving(null); }
  }
  async function handleDeleteAppParam(name: string) {
    setParamSaving(name);
    try { await themApi.deleteAppParam(app.id, name); setAppParams(await themApi.getAppParams(app.id) ?? []); }
    catch (e: unknown) { setParamMsg(m => ({ ...m, [name]: e instanceof Error ? e.message : 'Failed' })); } finally { setParamSaving(null); }
  }
  async function handleSave() {
    setSaving(true); setError(null);
    try { const parsedUsers = usersInput.split(/[\s,]+/).map(s => s.trim()).filter(Boolean).map(Number).filter(n => !isNaN(n)); const parsedTokens = tokensInput.split(/\n/).map(s => s.trim()).filter(Boolean); const payload = { ...cfg, blocked_tokens: parsedTokens, blocked_user_ids: parsedUsers }; await themApi.putAppRuntime(app.id, payload); setCfg(payload); setSaved(true); setTimeout(() => setSaved(false), 2500); }
    catch (e: unknown) { setError(e instanceof Error ? e.message : 'Save failed'); } finally { setSaving(false); }
  }

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '36px 40px 64px', background: C.bg }}>
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

      <div style={{ maxWidth: 720 }}>
        <EPSections
          entryPoints={entryPoints} orchMetas={orchMetas}
          voiceDrafts={voiceDrafts} setVoiceDrafts={setVoiceDrafts}
          epLLMDrafts={epLLMDrafts} setEPLLMDrafts={setEPLLMDrafts}
          epSumDrafts={epSumDrafts} setEPSumDrafts={setEPSumDrafts}
          epLLMSaving={epLLMSaving} epSumSaving={epSumSaving} epToggling={epToggling}
          epLLMMsg={epLLMMsg} epSumMsg={epSumMsg}
          setProviders={setProviders}
          voiceSaving={voiceSaving} voiceTesting={voiceTesting} ttsTesting={ttsTesting}
          voiceMsg={voiceMsg} voiceTestMsg={voiceTestMsg} ttsTestMsg={ttsTestMsg}
          onToggleEP={handleToggleEP} onSaveEPLLM={handleSaveEPLLM} onSaveEPSummarizer={handleSaveEPSummarizer}
          onSaveVoice={handleSaveVoice} onTestSTT={handleTestSTT} onTestTTS={handleTestTTS}
          saveBtn={saveBtn}
        />

        <CanvasAgentsSection
          agentIds={agentIds} agentLLMNodes={agentLLMNodes} agentParamsList={agentParamsList}
          nodeLLMDrafts={nodeLLMDrafts} setNodeLLMDrafts={setNodeLLMDrafts}
          nodeLLMSaving={nodeLLMSaving} nodeLLMMsg={nodeLLMMsg}
          agentParamInputs={agentParamInputs} setAgentParamInputs={setAgentParamInputs}
          agentParamSaving={agentParamSaving} agentParamMsg={agentParamMsg}
          setProviders={setProviders} saveBtn={saveBtn}
          onSaveNodeLLM={handleSaveNodeLLM} onSaveAgentParams={handleSaveAgentParams}
        />

        <Section title="Session Limits" icon="timer" accent="#f59e0b" defaultOpen={false}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
            <div>
              <label style={l}>Max Concurrent Sessions</label>
              <input type="number" min={1} placeholder="Unlimited" value={cfg.max_concurrent_sessions ?? ''} style={f} onChange={e => setCfg(c => ({ ...c, max_concurrent_sessions: e.target.value === '' ? null : parseInt(e.target.value) }))} />
              <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>App-wide soft cap. Empty = unlimited.</div>
            </div>
            <div>
              <label style={l}>Session Timeout (min)</label>
              <input type="number" min={1} placeholder="No timeout" value={cfg.session_timeout_minutes ?? ''} style={f} onChange={e => setCfg(c => ({ ...c, session_timeout_minutes: e.target.value === '' ? null : parseInt(e.target.value) }))} />
              <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Advisory. Empty = no timeout.</div>
            </div>
          </div>
        </Section>

        <Section title="Rate Limiting" icon="speed" accent="#f59e0b" defaultOpen={false}>
          <label style={l}>Requests per minute</label>
          <input type="number" min={1} placeholder="Unlimited" value={cfg.rate_limit_rpm ?? ''} style={f} onChange={e => setCfg(c => ({ ...c, rate_limit_rpm: e.target.value === '' ? null : parseInt(e.target.value) }))} />
          <div style={{ fontSize: 11, color: C.textMuted, marginTop: -8 }}>Applied across all entry points of this app.</div>
        </Section>

        <Section title="Access Control" icon="block" accent="#f59e0b" defaultOpen={false}>
          <div>
            <label style={l}>Blocked User IDs (comma-separated)</label>
            <input type="text" placeholder="e.g. 42, 107, 889" value={usersInput} style={f} onChange={e => setUsersInput(e.target.value)} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Connections from these user IDs are rejected immediately.</div>
          </div>
          <div>
            <label style={l}>Blocked Token Hashes (one per line)</label>
            <textarea placeholder="SHA-256 hash of each blocked token" value={tokensInput} rows={3} style={{ ...f, resize: 'vertical', fontFamily: 'monospace', fontSize: 12 }} onChange={e => setTokensInput(e.target.value)} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Paste the SHA-256 hash of the token — not the raw token. One per line.</div>
          </div>
        </Section>

        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 8, marginBottom: 24 }}>
          <button onClick={handleSave} disabled={saving} style={{ padding: '11px 28px', borderRadius: 8, border: 'none', cursor: saving ? 'not-allowed' : 'pointer', background: '#fb923c', color: '#000', fontSize: 14, fontWeight: 700, opacity: saving ? 0.6 : 1 }}>
            {saving ? 'Saving…' : 'Save Policy'}
          </button>
          {saved && <span style={{ fontSize: 13, color: C.green }}>Saved</span>}
          {error && <span style={{ fontSize: 13, color: C.error }}>{error}</span>}
        </div>

        <Section title="Provider Keys" icon="key" accent="#fb923c" defaultOpen={false}
          subtitle={setProviders.length > 0 ? `${setProviders.length} of ${PROVIDER_LIST.length} configured` : 'No providers configured yet'}>
          <div style={{ fontSize: 12, color: C.textMuted, marginTop: -4 }}>API keys are AES-GCM encrypted at rest.</div>
          {PROVIDER_LIST.map(provider => {
            const status = getKeyStatus(provider); const isBusy = keySaving === provider; const isTesting = keyTesting === provider;
            const msg = keyMsg[provider] ?? ''; const testMsg = keyTestMsg[provider] ?? '';
            const isErr = msg && msg !== 'Saved' && msg !== 'Removed'; const isTestErr = testMsg && !testMsg.startsWith('✓');
            return (
              <div key={provider} style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: `1px solid ${status.key_set ? 'rgba(74,222,128,0.15)' : 'rgba(255,255,255,0.07)'}` }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, width: 130, flexShrink: 0 }}>
                    <span style={{ fontSize: 13, fontWeight: 700, color: C.text }}>{provider}</span>
                    {status.key_set ? badge(C.green, 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', `set ···${status.key_hint ?? ''}`) : badge('#fb923c', 'rgba(251,146,60,0.1)', 'rgba(251,146,60,0.3)', 'not set')}
                  </div>
                  <input type="password" placeholder={status.key_set ? 'Replace key…' : 'Paste API key…'} value={keyInputs[provider] ?? ''} onChange={e => setKeyInputs(ki => ({ ...ki, [provider]: e.target.value }))} onKeyDown={e => { if (e.key === 'Enter') handleSaveKey(provider); }} style={{ ...f, flex: 1, minWidth: 160 }} />
                  {saveBtn(() => handleSaveKey(provider), isBusy, !(keyInputs[provider] ?? '').trim())}
                  {status.key_set && <button onClick={() => handleTestKey(provider)} disabled={isBusy || isTesting} style={{ padding: '8px 12px', borderRadius: 7, border: '1px solid rgba(74,222,128,0.3)', background: 'rgba(74,222,128,0.07)', color: C.green, cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy || isTesting ? 0.5 : 1 }}>{isTesting ? '…' : 'Test'}</button>}
                  {status.key_set && <button onClick={() => handleDeleteKey(provider)} disabled={isBusy} style={{ padding: '8px 10px', borderRadius: 7, border: '1px solid rgba(248,113,113,0.25)', background: 'rgba(248,113,113,0.06)', color: '#f87171', cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy ? 0.5 : 1 }}>Remove</button>}
                  {msg && <span style={{ fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{msg}</span>}
                </div>
                {testMsg && <div style={{ marginTop: 6, fontSize: 12, color: isTestErr ? C.error : C.green, fontWeight: 600, paddingLeft: 138 }}>{testMsg}</div>}
              </div>
            );
          })}
        </Section>

        <Section title="Global Parameters" icon="variable_insert" accent="#fb923c" defaultOpen={false}
          subtitle={appParams.length > 0 ? `${appParams.length} parameter${appParams.length !== 1 ? 's' : ''}` : 'Shared named values for canvas agent nodes'}>
          <div style={{ fontSize: 12, color: C.textMuted, marginTop: -4 }}>Referenced by name in canvas agent nodes via <code style={{ fontFamily: 'monospace', fontSize: 11, background: 'rgba(255,255,255,0.07)', padding: '1px 5px', borderRadius: 4 }}>app_param_ref</code>.</div>
          {appParams.map(param => {
            const isBusy = paramSaving === param.name; const msg = paramMsg[param.name] ?? ''; const isErr = msg && msg !== 'Saved';
            const isSecret = param.type === 'secret'; const editVal = editParamInputs[param.name] ?? '';
            return (
              <div key={param.name} style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.1)' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 7 }}>
                  <code style={{ fontSize: 12, fontWeight: 700, color: C.text, fontFamily: 'JetBrains Mono, monospace', flex: 1 }}>{param.name}</code>
                  <span style={{ fontSize: 10, fontWeight: 600, padding: '2px 6px', borderRadius: 20, background: 'rgba(132,158,190,0.1)', color: C.textMuted, border: '1px solid rgba(132,158,190,0.18)' }}>{param.type}</span>
                  {param.is_set && badge(C.green, 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', isSecret ? `set ···${param.value_hint ?? ''}` : 'set')}
                </div>
                {!isSecret && param.is_set && param.value && <div style={{ fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: C.textMuted, marginBottom: 7, wordBreak: 'break-all' }}>{param.value}</div>}
                <div style={{ display: 'flex', gap: 6 }}>
                  <input type={isSecret ? 'password' : 'text'} placeholder={param.is_set ? 'Replace value…' : 'Enter value…'} value={editVal} onChange={e => setEditParamInputs(prev => ({ ...prev, [param.name]: e.target.value }))} onKeyDown={e => { if (e.key === 'Enter') handleUpdateAppParam(param.name); }} style={{ ...f, flex: 1, minWidth: 0 }} />
                  {saveBtn(() => handleUpdateAppParam(param.name), isBusy, !editVal.trim(), 'Update')}
                  <button onClick={() => handleDeleteAppParam(param.name)} disabled={isBusy} style={{ padding: '8px 10px', borderRadius: 7, border: '1px solid rgba(248,113,113,0.25)', background: 'rgba(248,113,113,0.06)', color: '#f87171', cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy ? 0.5 : 1 }}>Remove</button>
                </div>
                {msg && <div style={{ marginTop: 5, fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
              </div>
            );
          })}
          {!addingParam ? (
            <button onClick={() => setAddingParam(true)} style={{ width: '100%', padding: '8px 0', borderRadius: 7, border: `1px dashed rgba(132,158,190,0.3)`, background: 'transparent', color: C.textMuted, cursor: 'pointer', fontSize: 12, fontWeight: 600 }}>+ Add parameter</button>
          ) : (
            <div style={{ padding: '12px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(132,158,190,0.18)' }}>
              <div style={{ ...l, marginBottom: 10 }}>New Parameter</div>
              <div style={{ display: 'flex', gap: 6, marginBottom: 8 }}>
                <input placeholder="name (a-z0-9_)" value={newParamName} onChange={e => setNewParamName(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))} style={{ ...f, flex: 2, fontFamily: 'JetBrains Mono, monospace' }} />
                <select value={newParamType} onChange={e => setNewParamType(e.target.value)} style={{ ...f, flex: 1 }}>{['string', 'secret', 'url', 'int', 'bool'].map(t => <option key={t} value={t}>{t}</option>)}</select>
              </div>
              <input type={newParamType === 'secret' ? 'password' : 'text'} placeholder="Value…" value={newParamValue} onChange={e => setNewParamValue(e.target.value)} style={{ ...f, marginBottom: 8 }} />
              {addParamMsg && <div style={{ fontSize: 12, color: C.error, fontWeight: 600, marginBottom: 6 }}>{addParamMsg}</div>}
              <div style={{ display: 'flex', gap: 6 }}>
                {saveBtn(handleAddAppParam, addParamSaving, !newParamName.trim() || !newParamValue.trim())}
                <button onClick={() => { setAddingParam(false); setNewParamName(''); setNewParamValue(''); setNewParamType('string'); setAddParamMsg(''); }} style={{ padding: '8px 14px', borderRadius: 7, border: '1px solid rgba(132,158,190,0.3)', background: 'transparent', color: C.textMuted, cursor: 'pointer', fontSize: 12 }}>Cancel</button>
              </div>
            </div>
          )}
        </Section>
      </div>
    </div>
  );
}
