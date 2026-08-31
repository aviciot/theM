'use client';
import { type EntryPoint } from '@/lib/api';
import { C, RUNTIME_MODELS } from '../constants';
import { Section, sharedField, sharedLbl, ToggleBtn, type SaveBtn } from './RuntimeShared';
import { VoicePanel, type VoiceDraft } from './RuntimeVoicePanel';

type OrchMeta = { id: string; name: string; displayName: string };
type EPLLMDraft = { provider: string; model: string };
type EPSummarizerDraft = { memoryEnabled: boolean; summarizeEveryN: number; fallbackN: number; provider: string; model: string };

export function EPSections({
  entryPoints, orchMetas, voiceDrafts, setVoiceDrafts,
  epLLMDrafts, setEPLLMDrafts, epSumDrafts, setEPSumDrafts,
  epLLMSaving, epSumSaving, epToggling,
  epLLMMsg, epSumMsg,
  setProviders,
  voiceSaving, voiceTesting, ttsTesting,
  voiceMsg, voiceTestMsg, ttsTestMsg,
  onToggleEP, onSaveEPLLM, onSaveEPSummarizer,
  onSaveVoice, onTestSTT, onTestTTS,
  saveBtn,
}: {
  entryPoints: EntryPoint[];
  orchMetas: OrchMeta[];
  voiceDrafts: Record<string, VoiceDraft>;
  setVoiceDrafts: React.Dispatch<React.SetStateAction<Record<string, VoiceDraft>>>;
  epLLMDrafts: Record<string, EPLLMDraft>;
  setEPLLMDrafts: React.Dispatch<React.SetStateAction<Record<string, EPLLMDraft>>>;
  epSumDrafts: Record<string, EPSummarizerDraft>;
  setEPSumDrafts: React.Dispatch<React.SetStateAction<Record<string, EPSummarizerDraft>>>;
  epLLMSaving: string | null; epSumSaving: string | null; epToggling: string | null;
  epLLMMsg: Record<string, string>; epSumMsg: Record<string, string>;
  setProviders: string[];
  voiceSaving: string | null; voiceTesting: string | null; ttsTesting: string | null;
  voiceMsg: Record<string, string>; voiceTestMsg: Record<string, string>; ttsTestMsg: Record<string, string>;
  onToggleEP: (epId: string, current: boolean) => void;
  onSaveEPLLM: (epId: string) => void;
  onSaveEPSummarizer: (epId: string) => void;
  onSaveVoice: (orchId: string) => void;
  onTestSTT: (orchId: string) => void;
  onTestTTS: (orchId: string) => void;
  saveBtn: SaveBtn;
}) {
  const f = sharedField;
  const l = sharedLbl;
  const nonVoiceEPs = entryPoints.filter(ep => ep.entry_point_type !== 'voice');
  const voiceEPs    = entryPoints.filter(ep => ep.entry_point_type === 'voice');

  const epIcon = (t: string) => t === 'websocket' ? 'cable' : t === 'sse' ? 'stream' : t === 'voice' ? 'mic' : t === 'a2a' ? 'robot_2' : 'link';

  return (
    <>
      {/* ── Entry Points ── */}
      <Section title="Entry Points" icon="door_open" accent="#00d1ff" defaultOpen
        subtitle={entryPoints.length > 0 ? `${entryPoints.length} entry point${entryPoints.length !== 1 ? 's' : ''}` : 'No entry points configured'}>
        {entryPoints.length === 0 && <div style={{ fontSize: 12, color: C.textMuted }}>No entry points configured for this application.</div>}
        {entryPoints.map(ep => (
          <div key={ep.id} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: `1px solid ${ep.enabled ? 'rgba(74,222,128,0.18)' : 'rgba(255,255,255,0.07)'}` }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16, color: ep.entry_point_type === 'voice' ? '#4ade80' : '#00d1ff', flexShrink: 0 }}>{epIcon(ep.entry_point_type)}</span>
            <span style={{ flex: 1, fontSize: 13, fontWeight: 700, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{ep.slug}</span>
            <span style={{ fontSize: 10, padding: '2px 7px', borderRadius: 20, background: 'rgba(132,158,190,0.1)', color: C.textMuted, border: '1px solid rgba(132,158,190,0.18)', fontWeight: 600 }}>{ep.entry_point_type}</span>
            <div style={{ opacity: epToggling === ep.id ? 0.5 : 1 }}>
              <ToggleBtn on={ep.enabled} onToggle={() => onToggleEP(ep.id, ep.enabled)} title={ep.enabled ? 'Disable entry point' : 'Enable entry point'} />
            </div>
          </div>
        ))}
      </Section>

      {/* ── LLM & Memory ── */}
      {orchMetas.length > 0 && nonVoiceEPs.length > 0 && (
        <Section title="LLM & Memory" icon="hub" accent="#a78bfa" defaultOpen
          subtitle="Conversation model and memory summarizer per entry point.">
          {orchMetas.map(orch => {
            const orchEPs = nonVoiceEPs.filter(ep => ep.app_orchestrator_id === orch.id);
            if (orchEPs.length === 0) return null;
            return (
              <div key={orch.id}>
                {orchMetas.length > 1 && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
                    <span className="material-symbols-outlined" style={{ fontSize: 13, color: C.purple }}>hub</span>
                    <span style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{orch.displayName || orch.name}</span>
                  </div>
                )}
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {orchEPs.map(ep => {
                    const llmDraft = epLLMDrafts[ep.id] ?? { provider: '', model: '' };
                    const sumDraft = epSumDrafts[ep.id] ?? { memoryEnabled: false, summarizeEveryN: 10, fallbackN: 3, provider: '', model: '' };
                    const llmBusy = epLLMSaving === ep.id;
                    const sumBusy = epSumSaving === ep.id;
                    const llmMsg = epLLMMsg[ep.id] ?? '';
                    const sumMsg = epSumMsg[ep.id] ?? '';
                    return (
                      <div key={ep.id} style={{ borderRadius: 8, border: `1px solid ${sumDraft.memoryEnabled ? 'rgba(208,188,255,0.2)' : 'rgba(132,158,190,0.14)'}`, overflow: 'hidden', background: 'rgba(255,255,255,0.02)' }}>
                        <div style={{ padding: '9px 12px', borderBottom: '1px solid rgba(132,158,190,0.1)', display: 'flex', alignItems: 'center', gap: 7 }}>
                          <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted }}>{epIcon(ep.entry_point_type)}</span>
                          <span style={{ fontSize: 12, fontWeight: 700, color: C.text, fontFamily: 'JetBrains Mono, monospace', flex: 1 }}>{ep.slug}</span>
                          <span style={{ fontSize: 10, padding: '2px 7px', borderRadius: 20, background: 'rgba(132,158,190,0.1)', color: C.textMuted, border: '1px solid rgba(132,158,190,0.18)', fontWeight: 600 }}>{ep.entry_point_type}</span>
                        </div>
                        <div style={{ padding: '12px 12px', display: 'flex', flexDirection: 'column', gap: 12 }}>
                          {/* LLM */}
                          <div>
                            <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: 7, display: 'flex', alignItems: 'center', gap: 5 }}>
                              <span className="material-symbols-outlined" style={{ fontSize: 13 }}>psychology</span>
                              Conversation LLM
                            </div>
                            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                              <select value={llmDraft.provider}
                                onChange={e => { const p = e.target.value; setEPLLMDrafts(prev => ({ ...prev, [ep.id]: { provider: p, model: (RUNTIME_MODELS[p] ?? []).includes(llmDraft.model) ? llmDraft.model : (RUNTIME_MODELS[p] ?? [])[0] ?? '' } })); }}
                                style={{ ...f, width: 150, flexShrink: 0 }}>
                                <option value="">— provider —</option>
                                {setProviders.map(p => <option key={p} value={p}>{p}</option>)}
                              </select>
                              <select value={llmDraft.model} disabled={!llmDraft.provider}
                                onChange={e => setEPLLMDrafts(prev => ({ ...prev, [ep.id]: { ...llmDraft, model: e.target.value } }))}
                                style={{ ...f, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                                <option value="">— model —</option>
                                {(RUNTIME_MODELS[llmDraft.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                              </select>
                              {saveBtn(() => onSaveEPLLM(ep.id), llmBusy, !llmDraft.provider || !llmDraft.model)}
                            </div>
                            {llmMsg && <div style={{ marginTop: 5, fontSize: 12, color: llmMsg !== 'Saved' ? C.error : C.green, fontWeight: 600 }}>{llmMsg}</div>}
                          </div>

                          {/* Memory */}
                          <div style={{ borderTop: '1px solid rgba(132,158,190,0.1)', paddingTop: 12 }}>
                            <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: 7, display: 'flex', alignItems: 'center', gap: 5 }}>
                              <span className="material-symbols-outlined" style={{ fontSize: 13 }}>memory</span>
                              Memory & Summarizer
                              <span style={{ marginLeft: 'auto' }}>
                                <ToggleBtn on={sumDraft.memoryEnabled} onToggle={() => setEPSumDrafts(prev => ({ ...prev, [ep.id]: { ...sumDraft, memoryEnabled: !sumDraft.memoryEnabled } }))} colorOn="#a78bfa" title={sumDraft.memoryEnabled ? 'Disable memory' : 'Enable memory'} />
                              </span>
                            </div>
                            <div style={{ opacity: sumDraft.memoryEnabled ? 1 : 0.45, display: 'flex', flexDirection: 'column', gap: 8 }}>
                              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                                <div>
                                  <label style={l}>Every N turns</label>
                                  <input type="number" min={1} value={sumDraft.summarizeEveryN} disabled={!sumDraft.memoryEnabled} style={f}
                                    onChange={e => setEPSumDrafts(prev => ({ ...prev, [ep.id]: { ...sumDraft, summarizeEveryN: parseInt(e.target.value) || 10 } }))} />
                                </div>
                                <div>
                                  <label style={l}>Keep last N verbatim</label>
                                  <input type="number" min={0} value={sumDraft.fallbackN} disabled={!sumDraft.memoryEnabled} style={f}
                                    onChange={e => setEPSumDrafts(prev => ({ ...prev, [ep.id]: { ...sumDraft, fallbackN: parseInt(e.target.value) || 0 } }))} />
                                </div>
                              </div>
                              <div>
                                <label style={l}>Summarizer model (optional — defaults to conversation LLM)</label>
                                <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                                  <select value={sumDraft.provider} disabled={!sumDraft.memoryEnabled}
                                    onChange={e => { const p = e.target.value; setEPSumDrafts(prev => ({ ...prev, [ep.id]: { ...sumDraft, provider: p, model: (RUNTIME_MODELS[p] ?? []).includes(sumDraft.model) ? sumDraft.model : (RUNTIME_MODELS[p] ?? [])[0] ?? '' } })); }}
                                    style={{ ...f, width: 150, flexShrink: 0 }}>
                                    <option value="">— same as LLM —</option>
                                    {setProviders.map(p => <option key={p} value={p}>{p}</option>)}
                                  </select>
                                  <select value={sumDraft.model} disabled={!sumDraft.memoryEnabled || !sumDraft.provider}
                                    onChange={e => setEPSumDrafts(prev => ({ ...prev, [ep.id]: { ...sumDraft, model: e.target.value } }))}
                                    style={{ ...f, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                                    <option value="">— model —</option>
                                    {(RUNTIME_MODELS[sumDraft.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                                  </select>
                                  {saveBtn(() => onSaveEPSummarizer(ep.id), sumBusy, false)}
                                </div>
                              </div>
                            </div>
                            {sumMsg && <div style={{ marginTop: 6, fontSize: 12, color: sumMsg !== 'Saved' ? C.error : C.green, fontWeight: 600 }}>{sumMsg}</div>}
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            );
          })}
        </Section>
      )}

      {/* ── Voice ── */}
      {voiceEPs.length > 0 && orchMetas.length > 0 && (
        <Section title="Voice" icon="mic" accent="#4ade80" defaultOpen={false}
          subtitle={`${voiceEPs.length} voice entry point${voiceEPs.length !== 1 ? 's' : ''} — STT and TTS`}>
          {orchMetas.map(orch => {
            const orchVoiceEPs = voiceEPs.filter(ep => ep.app_orchestrator_id === orch.id);
            if (orchVoiceEPs.length === 0) return null;
            const vd = voiceDrafts[orch.id] ?? { stt_provider: '', stt_model: '', tts_provider: '', tts_voice: '', tts_model: 'tts-1', voice_enabled: false, tts_enabled: false };
            return (
              <div key={orch.id}>
                {orchMetas.length > 1 && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
                    <span className="material-symbols-outlined" style={{ fontSize: 13, color: '#4ade80' }}>hub</span>
                    <span style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{orch.displayName || orch.name}</span>
                  </div>
                )}
                {orchVoiceEPs.map(ep => (
                  <div key={ep.id} style={{ borderRadius: 8, border: '1px solid rgba(74,222,128,0.15)', overflow: 'hidden', background: 'rgba(74,222,128,0.02)', marginBottom: 10 }}>
                    <div style={{ padding: '9px 12px', borderBottom: '1px solid rgba(74,222,128,0.1)', display: 'flex', alignItems: 'center', gap: 7 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 14, color: '#4ade80' }}>mic</span>
                      <span style={{ fontSize: 12, fontWeight: 700, color: C.text, fontFamily: 'JetBrains Mono, monospace', flex: 1 }}>{ep.slug}</span>
                      <span style={{ fontSize: 10, padding: '2px 7px', borderRadius: 20, background: 'rgba(74,222,128,0.1)', color: '#4ade80', border: '1px solid rgba(74,222,128,0.25)', fontWeight: 600 }}>voice</span>
                    </div>
                    <VoicePanel
                      orchId={orch.id} vd={vd}
                      setVd={patch => setVoiceDrafts(prev => ({ ...prev, [orch.id]: { ...vd, ...patch } }))}
                      vBusy={voiceSaving === orch.id} vTest={voiceTesting === orch.id} tTest={ttsTesting === orch.id}
                      vMsg={voiceMsg[orch.id] ?? ''} vtMsg={voiceTestMsg[orch.id] ?? ''} ttMsg={ttsTestMsg[orch.id] ?? ''}
                      onSave={() => onSaveVoice(orch.id)} onTestSTT={() => onTestSTT(orch.id)} onTestTTS={() => onTestTTS(orch.id)}
                      saveBtn={saveBtn}
                    />
                  </div>
                ))}
              </div>
            );
          })}
        </Section>
      )}
    </>
  );
}
