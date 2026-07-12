'use client';
import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type OrchestratorFull } from '@/lib/api';

// ── Model catalogues ──────────────────────────────────────────────────────
const VOICE_MODELS: Record<string, string[]> = {
  openai: ['whisper-1', 'gpt-4o-transcribe'],
  groq:   ['whisper-large-v3-turbo'],
};

const TTS_VOICES = ['nova', 'alloy', 'echo', 'fable', 'onyx', 'shimmer'];

const MODELS: Record<string, string[]> = {
  anthropic: ['claude-opus-4-8', 'claude-opus-4-7', 'claude-opus-4-6', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
  openai:    ['gpt-4.1', 'gpt-4.1-mini', 'gpt-4o', 'gpt-4o-mini', 'o4-mini'],
  groq:      ['llama-3.3-70b-versatile', 'llama-3.1-70b-versatile', 'llama-3.1-8b-instant', 'mixtral-8x7b-32768', 'gemma2-9b-it'],
  gemini:    ['gemini-2.5-pro', 'gemini-2.5-flash', 'gemini-2.0-flash', 'gemini-1.5-pro', 'gemini-1.5-flash'],
};

const EMPTY_FORM = {
  name: '', display_name: '', system_prompt: '',
  llm_provider: '', llm_model: '', llm_api_key: '', llm_base_url: '',
  max_iterations: 10, max_parallel_tools: 4, rate_limit_rpm: 30,
  daily_budget_usd: '0', enabled: true,
  allowed_agent_ids: [] as string[],
  voice_enabled: false,
  transcription_provider: 'openai',
  transcription_model: 'whisper-1',
  transcription_api_key: '',
  tts_enabled: false,
  tts_provider: 'openai',
  tts_voice: 'nova',
  tts_api_key: '',
  memory_enabled: false,
  summarize_every_n_calls: 3,
  memory_raw_fallback_n: 5,
  summarizer_provider: '',
  summarizer_model: '',
  summarizer_api_key: '',
  history_window: 20,
};

// ── Design tokens (matches agents/applications pages) ──────────────────────
const BG      = '#060a14';
const CYAN    = '#00d1ff';
const PURPLE  = '#a78bfa';
const GREEN   = '#34d399';
const TEXT    = '#e2e8f0';
const MUTED   = '#64748b';
const BORDER  = 'rgba(255,255,255,0.07)';

// provider → accent color
function providerColor(p: string | null): string {
  if (!p) return MUTED;
  if (p === 'anthropic') return '#d97706';   // amber
  if (p === 'openai')    return '#10b981';   // emerald
  if (p === 'groq')      return '#f59e0b';   // yellow
  if (p === 'gemini')    return '#3b82f6';   // blue
  return CYAN;
}

// provider → Material Symbols icon
function providerIcon(p: string | null): string {
  if (!p) return 'auto_awesome';
  if (p === 'anthropic') return 'brightness_7';
  if (p === 'openai')    return 'hub';
  if (p === 'groq')      return 'bolt';
  if (p === 'gemini')    return 'diamond';
  return 'smart_toy';
}

const ORCH_CARD_CSS = `
  .orch-glass-card {
    background:
      linear-gradient(160deg, rgba(255,255,255,0.032) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%),
      rgba(10,18,32,0.92);
    border: 1px solid rgba(255,255,255,0.07);
    backdrop-filter: blur(12px);
    box-shadow:
      0 8px 32px rgba(0,0,0,0.4),
      0 2px 8px rgba(0,0,0,0.25),
      inset 0 1px 0 rgba(255,255,255,0.04);
    transition: border-color 200ms ease, box-shadow 200ms ease, transform 200ms ease;
  }
  .orch-glass-card:hover {
    border-color: rgba(0,209,255,0.22);
    box-shadow:
      0 14px 40px rgba(0,0,0,0.48),
      0 4px 12px rgba(0,0,0,0.28),
      0 0 24px rgba(0,209,255,0.07),
      inset 0 1px 0 rgba(255,255,255,0.055);
    transform: translateY(-3px);
  }
  .orch-card-btn {
    display: flex; align-items: center; justify-content: center; gap: 5px;
    padding: 9px 4px; border-radius: 8px;
    font-size: 11px; font-weight: 700; letter-spacing: 0.01em;
    cursor: pointer; white-space: nowrap;
    transition: border-color 180ms ease, background 180ms ease,
                box-shadow 180ms ease, transform 180ms ease;
  }
  .orch-card-btn--test {
    background: ${CYAN}; color: #021520; border: none;
    box-shadow: 0 0 14px rgba(0,209,255,0.38);
  }
  .orch-card-btn--test:hover {
    background: #22dcff;
    box-shadow: 0 0 22px rgba(0,209,255,0.55);
    transform: translateY(-1px);
  }
  .orch-card-btn--edit {
    background: rgba(30,41,59,0.55); color: #94a3b8;
    border: 1px solid rgba(255,255,255,0.08);
  }
  .orch-card-btn--edit:hover {
    border-color: rgba(129,140,248,0.45);
    color: #818cf8;
    background: rgba(99,102,241,0.1);
  }
  .orch-card-btn--toggle-on {
    background: rgba(30,41,59,0.55); color: #f87171;
    border: 1px solid rgba(248,113,113,0.2);
  }
  .orch-card-btn--toggle-on:hover {
    border-color: rgba(248,113,113,0.5);
    background: rgba(248,113,113,0.08);
  }
  .orch-card-btn--toggle-off {
    background: rgba(30,41,59,0.55); color: #34d399;
    border: 1px solid rgba(52,211,153,0.2);
  }
  .orch-card-btn--toggle-off:hover {
    border-color: rgba(52,211,153,0.5);
    background: rgba(52,211,153,0.08);
  }
  .orch-deploy-card:hover {
    border-color: rgba(99,102,241,0.7) !important;
    background: rgba(99,102,241,0.04) !important;
  }
`;

// ── Sub-components ─────────────────────────────────────────────────────────

function Field({ label, children, disabled }: { label: string; children: React.ReactNode; disabled?: boolean }) {
  return (
    <div style={{ opacity: disabled ? 0.5 : 1 }}>
      <label style={{ display: 'block', fontSize: 11, fontWeight: 700, color: 'var(--tm-text-muted)', marginBottom: 5, textTransform: 'uppercase', letterSpacing: '0.4px' }}>{label}</label>
      {children}
    </div>
  );
}

const INP: React.CSSProperties = {
  width: '100%', background: 'var(--tm-surface-2)', border: '1px solid var(--tm-border)',
  borderRadius: 8, padding: '8px 12px', color: 'var(--tm-text)', fontSize: 13, boxSizing: 'border-box',
};

// ── Page ───────────────────────────────────────────────────────────────────
export default function OrchestratorsPage() {
  const router = useRouter();
  const [list, setList] = useState<OrchestratorFull[]>([]);
  const [allAgents, setAllAgents] = useState<import('@/lib/api').Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<OrchestratorFull | null>(null);
  const [form, setForm] = useState({ ...EMPTY_FORM });
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState('');
  const [testState, setTestState] = useState<{ loading: boolean; ok?: boolean; latency?: number; error?: string }>({ loading: false });
  const [voiceTestState, setVoiceTestState] = useState<{ loading: boolean; ok?: boolean; latency?: number; error?: string }>({ loading: false });
  const [ttsTestState, setTtsTestState] = useState<{ loading: boolean; ok?: boolean; latency?: number; error?: string }>({ loading: false });

  async function load() {
    setLoading(true);
    themApi.orchestrators().then(setList).finally(() => setLoading(false));
  }

  useEffect(() => {
    load();
    themApi.agents().then(setAllAgents);
  }, []);

  function openCreate() {
    setEditing(null);
    setForm({ ...EMPTY_FORM });
    setFormError(''); setTestState({ loading: false });
    setShowForm(true);
  }

  function openEdit(o: OrchestratorFull) {
    setEditing(o);
    setForm({
      name: o.name, display_name: o.display_name, system_prompt: o.system_prompt,
      llm_provider: o.llm_provider ?? '', llm_model: o.llm_model ?? '',
      llm_api_key: '',          // never pre-fill — show hint instead
      llm_base_url: o.llm_base_url ?? '',
      max_iterations: o.max_iterations, max_parallel_tools: o.max_parallel_tools,
      rate_limit_rpm: o.rate_limit_rpm, daily_budget_usd: o.daily_budget_usd,
      enabled: o.enabled,
      allowed_agent_ids: o.allowed_agent_ids ?? [],
      voice_enabled: o.voice_enabled ?? false,
      transcription_provider: o.transcription_provider ?? 'openai',
      transcription_model: o.transcription_model ?? 'whisper-1',
      transcription_api_key: '',
      tts_enabled: o.tts_enabled ?? false,
      tts_provider: o.tts_provider ?? 'openai',
      tts_voice: o.tts_voice ?? 'nova',
      tts_api_key: '',
      memory_enabled: o.memory_enabled ?? false,
      summarize_every_n_calls: o.summarize_every_n_calls ?? 3,
      memory_raw_fallback_n: o.memory_raw_fallback_n ?? 5,
      summarizer_provider: o.summarizer_provider ?? '',
      summarizer_model: o.summarizer_model ?? '',
      summarizer_api_key: '',
      history_window: o.history_window ?? 20,
    });
    setFormError(''); setTestState({ loading: false }); setVoiceTestState({ loading: false }); setTtsTestState({ loading: false });
    setShowForm(true);
  }

  const f = (k: keyof typeof form, v: any) => setForm((p) => ({ ...p, [k]: v }));

  // When provider changes, auto-select first model
  function onProviderChange(provider: string) {
    f('llm_provider', provider);
    if (provider && MODELS[provider]) {
      f('llm_model', MODELS[provider][0]);
    } else {
      f('llm_model', '');
    }
  }

  async function testLlm() {
    if (!editing || !form.llm_provider || !form.llm_model) return;
    setTestState({ loading: true });
    try {
      const res = await themApi.testLlm(editing.id, {
        provider: form.llm_provider,
        model: form.llm_model,
        api_key: form.llm_api_key || undefined,
        base_url: form.llm_base_url || undefined,
      });
      setTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function testVoice() {
    if (!editing || !form.transcription_provider || !form.transcription_model) return;
    setVoiceTestState({ loading: true });
    try {
      const res = await themApi.testVoice(editing.id, {
        provider: form.transcription_provider,
        model: form.transcription_model,
        api_key: form.transcription_api_key || undefined,
      });
      setVoiceTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setVoiceTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function testTts() {
    if (!editing || !form.tts_provider || !form.tts_voice) return;
    setTtsTestState({ loading: true });
    try {
      const res = await themApi.testTts(editing.id, {
        provider: form.tts_provider,
        voice: form.tts_voice,
        api_key: form.tts_api_key || undefined,
      });
      setTtsTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setTtsTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function save() {
    setSaving(true); setFormError('');
    try {
      const body: any = {
        ...form,
        llm_provider: form.llm_provider || null,
        llm_model: form.llm_model || null,
        llm_api_key: form.llm_api_key || undefined,
        llm_base_url: form.llm_base_url || null,
        max_iterations: Number(form.max_iterations),
        max_parallel_tools: Number(form.max_parallel_tools),
        rate_limit_rpm: Number(form.rate_limit_rpm),
        voice_enabled: form.voice_enabled,
        transcription_provider: form.voice_enabled ? form.transcription_provider : null,
        transcription_model: form.voice_enabled ? form.transcription_model : null,
        tts_enabled: form.tts_enabled,
        tts_provider: form.tts_enabled ? form.tts_provider : null,
        tts_voice: form.tts_enabled ? form.tts_voice : null,
        memory_enabled: form.memory_enabled,
        summarize_every_n_calls: Number(form.summarize_every_n_calls),
        memory_raw_fallback_n: Number(form.memory_raw_fallback_n),
        summarizer_provider: form.memory_enabled ? (form.summarizer_provider || null) : null,
        summarizer_model: form.memory_enabled ? (form.summarizer_model || null) : null,
        history_window: Number(form.history_window),
      };
      if (form.transcription_api_key) {
        body.transcription_api_key = form.transcription_api_key;
      } else {
        delete body.transcription_api_key;
      }
      if (form.tts_api_key) {
        body.tts_api_key = form.tts_api_key;
      } else {
        delete body.tts_api_key;
      }
      if (form.summarizer_api_key) {
        body.summarizer_api_key = form.summarizer_api_key;
      } else {
        delete body.summarizer_api_key;
      }
      if (editing) {
        await themApi.updateOrchestrator(editing.id, body);
      } else {
        await themApi.createOrchestrator(body);
      }
      setShowForm(false);
      load();
    } catch (e: any) {
      setFormError(e.message);
    } finally {
      setSaving(false);
    }
  }

  async function del(o: OrchestratorFull) {
    if (!confirm(`Delete "${o.display_name}"?`)) return;
    await themApi.deleteOrchestrator(o.id).catch((e) => alert(e.message));
    load();
  }

  const modelOptions = form.llm_provider ? (MODELS[form.llm_provider] ?? []) : [];

  return (
    <AuthGuard>
      <style>{ORCH_CARD_CSS}</style>
      <div style={{ display: 'flex', minHeight: '100vh', background: BG }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1, padding: '36px 48px' }}>
          {/* Page header */}
          <div style={{ marginBottom: 32 }}>
            <h1 style={{ fontSize: 26, fontWeight: 700, color: TEXT, margin: 0, fontFamily: 'Geist, sans-serif', letterSpacing: -0.5 }}>
              Orchestrators
            </h1>
            <p style={{ fontSize: 13, color: MUTED, margin: '6px 0 0', fontFamily: 'Inter, sans-serif' }}>
              Configure LLM pipelines, allowed agents, rate limits and voice capabilities.
            </p>
          </div>

          {loading ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, color: MUTED, padding: '40px 0' }}>
              <span className="material-symbols-outlined" style={{ fontSize: 18, animation: 'spin 1s linear infinite' }}>autorenew</span>
              Loading…
            </div>
          ) : list.length === 0 ? (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: 20 }}>
              <div className="orch-deploy-card" onClick={openCreate} style={{ borderRadius: 16, border: '2px dashed rgba(99,102,241,0.35)', background: 'rgba(99,102,241,0.02)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, cursor: 'pointer', minHeight: 260, transition: 'border-color 200ms ease, background 200ms ease' }}>
                <div style={{ width: 52, height: 52, borderRadius: 14, border: '2px dashed rgba(99,102,241,0.5)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 26, color: '#818cf8' }}>add</span>
                </div>
                <div style={{ fontSize: 14, fontWeight: 700, color: '#818cf8', fontFamily: 'Geist, sans-serif' }}>New Orchestrator</div>
              </div>
            </div>
          ) : (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: 20 }}>
              {list.map((o) => {
                const pColor = providerColor(o.llm_provider);
                const pGlow  = `${pColor}38`;
                const pBorder = `${pColor}70`;
                const agentCount = o.allowed_agent_ids?.length ?? 0;
                const hasVoice = o.voice_enabled;
                const hasMemory = o.memory_enabled;

                return (
                  <div key={o.id} className="orch-glass-card" style={{ borderRadius: 20, display: 'flex', flexDirection: 'column', position: 'relative' }}>
                    {/* Card body — click to edit */}
                    <div style={{ padding: '22px 22px 16px', flex: 1, display: 'flex', flexDirection: 'column', gap: 14, cursor: 'pointer' }} onClick={() => openEdit(o)}>

                      {/* Header: icon + name + three-dot */}
                      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 14 }}>
                        {/* Icon tile */}
                        <div style={{
                          width: 56, height: 56, borderRadius: 14, flexShrink: 0,
                          background: `radial-gradient(circle at 30% 25%, ${pGlow}, transparent 65%),
                                       linear-gradient(145deg, rgba(20,32,52,0.96), rgba(8,16,30,0.96))`,
                          border: `1px solid ${pBorder}`,
                          boxShadow: `0 0 18px ${pGlow}, inset 0 1px 0 rgba(255,255,255,0.07)`,
                          display: 'flex', alignItems: 'center', justifyContent: 'center',
                        }}>
                          <span className="material-symbols-outlined" style={{ fontSize: 26, color: pColor }}>{providerIcon(o.llm_provider)}</span>
                        </div>

                        {/* Name + badges */}
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ fontWeight: 700, fontSize: 16, color: TEXT, fontFamily: 'Geist, sans-serif', marginBottom: 6, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                            {o.display_name}
                          </div>
                          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center' }}>
                            {/* Enabled pill */}
                            <span style={{
                              display: 'inline-flex', alignItems: 'center', gap: 5,
                              padding: '2px 8px', borderRadius: 20, fontSize: 11, fontWeight: 700,
                              background: o.enabled ? 'rgba(52,211,153,0.1)' : 'rgba(248,113,113,0.1)',
                              color: o.enabled ? GREEN : '#f87171',
                              border: `1px solid ${o.enabled ? 'rgba(52,211,153,0.3)' : 'rgba(248,113,113,0.3)'}`,
                            }}>
                              {o.enabled && <span style={{ width: 5, height: 5, borderRadius: '50%', background: GREEN, display: 'inline-block', boxShadow: `0 0 5px ${GREEN}` }} />}
                              {o.enabled ? 'live' : 'disabled'}
                            </span>
                            {/* Provider badge */}
                            {o.llm_provider && (
                              <span style={{
                                display: 'inline-flex', alignItems: 'center', gap: 4,
                                padding: '2px 8px', borderRadius: 20, fontSize: 11, fontWeight: 700,
                                background: 'rgba(255,255,255,0.04)', color: pColor,
                                border: `1px solid ${pBorder}`,
                              }}>
                                {o.llm_provider}
                              </span>
                            )}
                          </div>
                          {/* Slug */}
                          <div style={{ fontSize: 11, color: MUTED, fontFamily: 'JetBrains Mono, monospace', marginTop: 5 }}>{o.name}</div>
                        </div>

                        {/* Three-dot overflow */}
                        <div style={{ position: 'relative', flexShrink: 0 }} onClick={e => e.stopPropagation()}>
                          <button
                            onClick={() => del(o)}
                            title="Delete orchestrator"
                            style={{ width: 32, height: 32, borderRadius: 8, cursor: 'pointer', background: 'rgba(30,41,59,0.5)', border: '1px solid rgba(255,255,255,0.06)', color: '#64748b', display: 'flex', alignItems: 'center', justifyContent: 'center', transition: 'color 150ms ease, border-color 150ms ease' }}
                            onMouseEnter={e => { e.currentTarget.style.color = '#f87171'; e.currentTarget.style.borderColor = 'rgba(248,113,113,0.4)'; }}
                            onMouseLeave={e => { e.currentTarget.style.color = '#64748b'; e.currentTarget.style.borderColor = 'rgba(255,255,255,0.06)'; }}
                          >
                            <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                          </button>
                        </div>
                      </div>

                      {/* Stat tiles */}
                      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                        {/* Model tile */}
                        <div style={{ padding: '8px 12px', borderRadius: 10, background: 'rgba(255,255,255,0.03)', border: `1px solid ${BORDER}`, display: 'flex', alignItems: 'center', gap: 8 }}>
                          <span className="material-symbols-outlined" style={{ fontSize: 18, color: PURPLE, flexShrink: 0 }}>psychology</span>
                          <div style={{ minWidth: 0 }}>
                            <div style={{ fontSize: 10, color: MUTED, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Model</div>
                            <div style={{ fontSize: 12, color: TEXT, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: 'JetBrains Mono, monospace' }}>
                              {o.llm_model ?? <span style={{ color: MUTED, fontStyle: 'italic' }}>env default</span>}
                            </div>
                          </div>
                        </div>
                        {/* Agents tile */}
                        <div style={{ padding: '8px 12px', borderRadius: 10, background: 'rgba(255,255,255,0.03)', border: `1px solid ${BORDER}`, display: 'flex', alignItems: 'center', gap: 8 }}>
                          <span className="material-symbols-outlined" style={{ fontSize: 18, color: CYAN, flexShrink: 0 }}>smart_toy</span>
                          <div style={{ minWidth: 0 }}>
                            <div style={{ fontSize: 10, color: MUTED, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Agents</div>
                            <div style={{ fontSize: 12, color: TEXT, fontWeight: 600 }}>{agentCount} allowed</div>
                          </div>
                        </div>
                      </div>

                      {/* Limits row */}
                      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                        <span style={{ fontSize: 11, color: MUTED, padding: '3px 8px', borderRadius: 6, background: 'rgba(255,255,255,0.03)', border: `1px solid ${BORDER}` }}>
                          ⚡ {o.max_iterations} iters
                        </span>
                        <span style={{ fontSize: 11, color: MUTED, padding: '3px 8px', borderRadius: 6, background: 'rgba(255,255,255,0.03)', border: `1px solid ${BORDER}` }}>
                          🔄 {o.rate_limit_rpm} rpm
                        </span>
                        {hasVoice && (
                          <span style={{ fontSize: 11, color: CYAN, padding: '3px 8px', borderRadius: 6, background: 'rgba(0,209,255,0.06)', border: '1px solid rgba(0,209,255,0.2)' }}>
                            🎙 voice
                          </span>
                        )}
                        {hasMemory && (
                          <span style={{ fontSize: 11, color: PURPLE, padding: '3px 8px', borderRadius: 6, background: 'rgba(167,139,250,0.06)', border: '1px solid rgba(167,139,250,0.2)' }}>
                            🧠 memory
                          </span>
                        )}
                        {o.llm_api_key_hint && (
                          <span title={`Key: ${o.llm_api_key_hint}`} style={{ fontSize: 11, color: GREEN, padding: '3px 8px', borderRadius: 6, background: 'rgba(52,211,153,0.06)', border: '1px solid rgba(52,211,153,0.2)' }}>
                            🔑 key set
                          </span>
                        )}
                      </div>
                    </div>

                    {/* Action buttons */}
                    <div style={{ borderTop: `1px solid ${BORDER}`, padding: '12px 16px', display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 8 }}>
                      <button className="orch-card-btn orch-card-btn--test" onClick={() => router.push(`/admin/playground?orchestrator=${o.name}`)}>
                        🧪 Test
                      </button>
                      <button className="orch-card-btn orch-card-btn--edit" onClick={() => openEdit(o)}>
                        ✏️ Edit
                      </button>
                      {o.enabled ? (
                        <button className="orch-card-btn orch-card-btn--toggle-on" onClick={async () => { await themApi.updateOrchestrator(o.id, { enabled: false }); load(); }}>
                          🔴 Disable
                        </button>
                      ) : (
                        <button className="orch-card-btn orch-card-btn--toggle-off" onClick={async () => { await themApi.updateOrchestrator(o.id, { enabled: true }); load(); }}>
                          🟢 Enable
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}

              {/* New orchestrator deploy card */}
              <div className="orch-deploy-card" onClick={openCreate} style={{ borderRadius: 16, border: '2px dashed rgba(99,102,241,0.35)', background: 'rgba(99,102,241,0.02)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, cursor: 'pointer', minHeight: 220, transition: 'border-color 200ms ease, background 200ms ease' }}>
                <div style={{ width: 52, height: 52, borderRadius: 14, border: '2px dashed rgba(99,102,241,0.5)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 26, color: '#818cf8' }}>add</span>
                </div>
                <div style={{ fontSize: 14, fontWeight: 700, color: '#818cf8', fontFamily: 'Geist, sans-serif' }}>New Orchestrator</div>
              </div>
            </div>
          )}
        </main>
      </div>

      {/* ── Modal ─────────────────────────────────────────────────────── */}
      {showForm && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.65)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24, overflowY: 'auto' }}>
          <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: 16, width: '100%', maxWidth: 600, maxHeight: '92vh', overflowY: 'auto', padding: 32 }}>
            <h2 style={{ fontSize: 16, fontWeight: 700, color: 'var(--tm-text)', marginBottom: 24 }}>
              {editing ? 'Edit orchestrator' : 'New orchestrator'}
            </h2>

            {formError && <div style={{ marginBottom: 16, padding: '10px 14px', borderRadius: 8, background: '#f8717118', color: '#f87171', fontSize: 13 }}>{formError}</div>}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              {/* Basic */}
              <Field label="Slug" disabled={!!editing}>
                <input value={form.name} onChange={(e) => f('name', e.target.value)} placeholder="my-orchestrator" style={INP} disabled={!!editing} />
              </Field>
              <Field label="Display name">
                <input value={form.display_name} onChange={(e) => f('display_name', e.target.value)} placeholder="My Orchestrator" style={INP} />
              </Field>
              <Field label="System prompt">
                <textarea value={form.system_prompt} onChange={(e) => f('system_prompt', e.target.value)} rows={3} style={{ ...INP, resize: 'vertical', fontFamily: 'inherit' }} />
              </Field>

              {/* ── LLM Configuration ─────────────────────────────────── */}
              <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 18, marginTop: 4 }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tm-accent)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 14, display: 'flex', alignItems: 'center', gap: 6 }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 15 }}>psychology</span>
                  LLM Configuration
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                  <Field label="Provider">
                    <select value={form.llm_provider} onChange={(e) => onProviderChange(e.target.value)} style={INP}>
                      <option value="">— use env default —</option>
                      <option value="anthropic">Anthropic</option>
                      <option value="openai">OpenAI</option>
                      <option value="groq">Groq</option>
                      <option value="gemini">Gemini</option>
                    </select>
                  </Field>
                  <Field label="Model">
                    <select value={form.llm_model} onChange={(e) => f('llm_model', e.target.value)} disabled={!form.llm_provider} style={INP}>
                      {!form.llm_provider && <option value="">select provider first</option>}
                      {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
                    </select>
                  </Field>
                </div>

                <div style={{ marginTop: 12 }}>
                  <Field label={editing?.llm_api_key_hint ? `API key (current: ${editing.llm_api_key_hint})` : 'API key'}>
                    <input
                      type="password"
                      value={form.llm_api_key}
                      onChange={(e) => f('llm_api_key', e.target.value)}
                      placeholder={editing?.llm_api_key_hint ? 'Leave blank to keep existing key' : 'sk-…'}
                      style={INP}
                    />
                  </Field>
                </div>

                <div style={{ marginTop: 12 }}>
                  <Field label="Base URL override (optional)">
                    <input value={form.llm_base_url} onChange={(e) => f('llm_base_url', e.target.value)} placeholder="https://api.example.com" style={INP} />
                  </Field>
                </div>

                {/* Test button — only when editing an existing orchestrator */}
                {editing && form.llm_provider && form.llm_model && (
                  <div style={{ marginTop: 14, display: 'flex', alignItems: 'center', gap: 12 }}>
                    <button onClick={testLlm} disabled={testState.loading} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text)', cursor: testState.loading ? 'wait' : 'pointer', fontSize: 13, fontWeight: 600 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 16 }}>bolt</span>
                      {testState.loading ? 'Testing…' : 'Test connection'}
                    </button>
                    {!testState.loading && testState.ok !== undefined && (
                      testState.ok
                        ? <span style={{ fontSize: 13, color: '#4edea3', fontWeight: 600 }}>✓ Connected ({testState.latency}ms)</span>
                        : <span style={{ fontSize: 13, color: '#f87171' }}>✗ {testState.error}</span>
                    )}
                  </div>
                )}
              </div>

              {/* ── Agents ────────────────────────────────────────────── */}
              <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 18, marginTop: 4 }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tm-accent)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 6 }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 15 }}>smart_toy</span>
                  Allowed Agents
                </div>
                {allAgents.length === 0 ? (
                  <div style={{ fontSize: 13, color: 'var(--tm-text-muted)', fontStyle: 'italic' }}>No agents registered yet — add agents first</div>
                ) : (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {allAgents.map((a) => {
                      const checked = form.allowed_agent_ids.includes(a.id);
                      return (
                        <label key={a.id} style={{ display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer', padding: '8px 10px', borderRadius: 8, border: `1px solid ${checked ? 'var(--tm-accent)' : 'var(--tm-border)'}`, background: checked ? 'var(--tm-accent-bg)' : 'transparent' }}>
                          <input type="checkbox" checked={checked} onChange={(e) => {
                            const ids = e.target.checked
                              ? [...form.allowed_agent_ids, a.id]
                              : form.allowed_agent_ids.filter((id) => id !== a.id);
                            f('allowed_agent_ids', ids);
                          }} />
                          <div>
                            <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tm-text)' }}>{a.display_name}</div>
                            <div style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
                              <code style={{ background: 'var(--tm-surface-2)', padding: '1px 4px', borderRadius: 3 }}>{a.slug}</code>
                              {' · '}{a.transport}
                            </div>
                          </div>
                        </label>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* ── Voice ─────────────────────────────────────────────── */}
              <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 18, marginTop: 4 }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tm-accent)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 6 }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 15 }}>mic</span>
                  Voice
                </div>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', marginBottom: 12 }}>
                  <input type="checkbox" checked={form.voice_enabled} onChange={(e) => f('voice_enabled', e.target.checked)} style={{ width: 16, height: 16 }} />
                  <span style={{ fontSize: 13, color: 'var(--tm-text)' }}>Enable voice input</span>
                </label>
                {form.voice_enabled && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                      <Field label="Transcription provider">
                        <select
                          value={form.transcription_provider}
                          onChange={(e) => {
                            f('transcription_provider', e.target.value);
                            const models = VOICE_MODELS[e.target.value];
                            if (models) f('transcription_model', models[0]);
                          }}
                          style={INP}
                        >
                          <option value="openai">OpenAI</option>
                          <option value="groq">Groq</option>
                        </select>
                      </Field>
                      <Field label="Transcription model">
                        <select
                          value={form.transcription_model}
                          onChange={(e) => f('transcription_model', e.target.value)}
                          style={INP}
                        >
                          {(VOICE_MODELS[form.transcription_provider] ?? []).map((m) => (
                            <option key={m} value={m}>{m}</option>
                          ))}
                        </select>
                      </Field>
                    </div>
                    <Field label="Transcription API key (optional override)">
                      <input
                        type="password"
                        value={form.transcription_api_key}
                        onChange={(e) => f('transcription_api_key', e.target.value)}
                        placeholder="optional override"
                        style={INP}
                      />
                    </Field>
                    {editing && (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                        <button onClick={testVoice} disabled={voiceTestState.loading || !form.transcription_provider || !form.transcription_model} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text)', cursor: voiceTestState.loading ? 'wait' : 'pointer', fontSize: 13, fontWeight: 600 }}>
                          {voiceTestState.loading ? '...' : 'Test connection'}
                        </button>
                        {voiceTestState.ok !== undefined && (
                          <span style={{ fontSize: 12, color: voiceTestState.ok ? '#4ade80' : '#f87171' }}>
                            {voiceTestState.ok ? `✓ ${voiceTestState.latency}ms — ${voiceTestState.error}` : `✗ ${voiceTestState.error}`}
                          </span>
                        )}
                      </div>
                    )}
                  </div>
                )}
              </div>

              {/* TTS */}
              <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 18, marginTop: 4 }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 14 }}>Text-to-Speech (TTS)</div>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', marginBottom: 14 }}>
                  <input type="checkbox" checked={form.tts_enabled} onChange={(e) => f('tts_enabled', e.target.checked)} style={{ width: 16, height: 16 }} />
                  <span style={{ fontSize: 13 }}>Enable TTS — read responses aloud</span>
                </label>
                {form.tts_enabled && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                      <Field label="TTS provider">
                        <select value={form.tts_provider} onChange={(e) => f('tts_provider', e.target.value)} style={INP}>
                          <option value="openai">OpenAI</option>
                        </select>
                      </Field>
                      <Field label="Voice">
                        <select value={form.tts_voice} onChange={(e) => f('tts_voice', e.target.value)} style={INP}>
                          {TTS_VOICES.map((v) => <option key={v} value={v}>{v}</option>)}
                        </select>
                      </Field>
                    </div>
                    <Field label="TTS API key (optional override)">
                      <input type="password" value={form.tts_api_key} onChange={(e) => f('tts_api_key', e.target.value)} placeholder="optional override" style={INP} />
                    </Field>
                    {editing && (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                        <button onClick={testTts} disabled={ttsTestState.loading} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text)', cursor: ttsTestState.loading ? 'wait' : 'pointer', fontSize: 13, fontWeight: 600 }}>
                          {ttsTestState.loading ? '...' : 'Test connection'}
                        </button>
                        {ttsTestState.ok !== undefined && (
                          <span style={{ fontSize: 12, color: ttsTestState.ok ? '#4ade80' : '#f87171' }}>
                            {ttsTestState.ok ? `✓ ${ttsTestState.latency}ms` : `✗ ${ttsTestState.error}`}
                          </span>
                        )}
                      </div>
                    )}
                  </div>
                )}
              </div>

              {/* Memory */}
              <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 18, marginTop: 4 }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 14 }}>Context Memory</div>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', marginBottom: 14 }}>
                  <input type="checkbox" checked={form.memory_enabled} onChange={(e) => f('memory_enabled', e.target.checked)} style={{ width: 16, height: 16 }} />
                  <span style={{ fontSize: 13 }}>Enable context summarization memory</span>
                </label>
                {form.memory_enabled && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                      <Field label="Summarize every N agent calls">
                        <input type="number" min={1} value={form.summarize_every_n_calls} onChange={(e) => f('summarize_every_n_calls', e.target.value)} style={INP} />
                      </Field>
                      <Field label="Raw context fallback N">
                        <input type="number" min={1} value={form.memory_raw_fallback_n} onChange={(e) => f('memory_raw_fallback_n', e.target.value)} style={INP} />
                      </Field>
                    </div>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                      <Field label="Summarizer provider (optional override)">
                        <select
                          value={form.summarizer_provider}
                          onChange={(e) => {
                            f('summarizer_provider', e.target.value);
                            if (e.target.value && MODELS[e.target.value]) {
                              f('summarizer_model', MODELS[e.target.value][0]);
                            } else {
                              f('summarizer_model', '');
                            }
                          }}
                          style={INP}
                        >
                          <option value="">env default (anthropic / haiku)</option>
                          {Object.keys(MODELS).map((p) => <option key={p} value={p}>{p}</option>)}
                        </select>
                      </Field>
                      <Field label="Summarizer model">
                        <select value={form.summarizer_model} onChange={(e) => f('summarizer_model', e.target.value)} style={INP} disabled={!form.summarizer_provider}>
                          <option value="">{form.summarizer_provider ? 'select model' : '—'}</option>
                          {(form.summarizer_provider ? (MODELS[form.summarizer_provider] ?? []) : []).map((m) => <option key={m} value={m}>{m}</option>)}
                        </select>
                      </Field>
                    </div>
                    <Field label="Summarizer API key (optional override)">
                      <input type="password" value={form.summarizer_api_key} onChange={(e) => f('summarizer_api_key', e.target.value)} placeholder="optional override" style={INP} />
                    </Field>
                  </div>
                )}
              </div>

              {/* Limits */}
              <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 18, marginTop: 4 }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 14 }}>Limits</div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12 }}>
                  <Field label="Max iterations">
                    <input type="number" value={form.max_iterations} onChange={(e) => f('max_iterations', e.target.value)} style={INP} />
                  </Field>
                  <Field label="Parallel tools">
                    <input type="number" value={form.max_parallel_tools} onChange={(e) => f('max_parallel_tools', e.target.value)} style={INP} />
                  </Field>
                  <Field label="History window (turns, -1 = unlimited)">
                    <input type="number" min={-1} value={form.history_window} onChange={(e) => f('history_window', e.target.value)} style={INP} />
                  </Field>
                  <Field label="Rate limit (rpm)">
                    <input type="number" value={form.rate_limit_rpm} onChange={(e) => f('rate_limit_rpm', e.target.value)} style={INP} />
                  </Field>
                </div>
              </div>

              <Field label="Enabled">
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
                  <input type="checkbox" checked={form.enabled} onChange={(e) => f('enabled', e.target.checked)} style={{ width: 16, height: 16 }} />
                  <span style={{ fontSize: 13, color: 'var(--tm-text)' }}>Active</span>
                </label>
              </Field>
            </div>

            <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end', marginTop: 28 }}>
              <button onClick={() => setShowForm(false)} style={{ padding: '8px 18px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text)', cursor: 'pointer', fontSize: 13 }}>Cancel</button>
              <button onClick={save} disabled={saving} style={{ padding: '8px 20px', borderRadius: 8, border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: 'pointer', fontWeight: 600, fontSize: 13, opacity: saving ? 0.7 : 1 }}>
                {saving ? 'Saving…' : editing ? 'Save changes' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}
    </AuthGuard>
  );
}
