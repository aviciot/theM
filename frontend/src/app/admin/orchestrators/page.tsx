'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type OrchestratorFull } from '@/lib/api';
import { EMPTY_FORM, BG, TEXT, MUTED, ORCH_CARD_CSS } from './orchestratorConstants';
import { OrchestratorCard } from './OrchestratorCard';
import { OrchestratorForm } from './OrchestratorForm';

export default function OrchestratorsPage() {
  const [list, setList]         = useState<OrchestratorFull[]>([]);
  const [allAgents, setAllAgents] = useState<import('@/lib/api').Agent[]>([]);
  const [loading, setLoading]   = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing]   = useState<OrchestratorFull | null>(null);
  const [form, setForm]         = useState({ ...EMPTY_FORM });
  const [saving, setSaving]     = useState(false);
  const [formError, setFormError] = useState('');
  const [testState, setTestState]         = useState<{ loading: boolean; ok?: boolean; latency?: number; error?: string }>({ loading: false });
  const [voiceTestState, setVoiceTestState] = useState<{ loading: boolean; ok?: boolean; latency?: number; error?: string }>({ loading: false });
  const [ttsTestState, setTtsTestState]     = useState<{ loading: boolean; ok?: boolean; latency?: number; error?: string }>({ loading: false });

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
    setFormError('');
    setTestState({ loading: false });
    setShowForm(true);
  }

  function openEdit(o: OrchestratorFull) {
    setEditing(o);
    setForm({
      name: o.name, display_name: o.display_name, system_prompt: o.system_prompt,
      llm_provider: o.llm_provider ?? '', llm_model: o.llm_model ?? '',
      llm_api_key: '',
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
    setFormError('');
    setTestState({ loading: false });
    setVoiceTestState({ loading: false });
    setTtsTestState({ loading: false });
    setShowForm(true);
  }

  const setField = (k: string, v: any) => setForm((p) => ({ ...p, [k]: v }));

  async function testLlm() {
    if (!editing || !form.llm_provider || !form.llm_model) return;
    setTestState({ loading: true });
    try {
      const res = await themApi.testLlm(editing.id, { provider: form.llm_provider, model: form.llm_model, api_key: form.llm_api_key || undefined, base_url: form.llm_base_url || undefined });
      setTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function testVoice() {
    if (!editing || !form.transcription_provider || !form.transcription_model) return;
    setVoiceTestState({ loading: true });
    try {
      const res = await themApi.testVoice(editing.id, { provider: form.transcription_provider, model: form.transcription_model, api_key: form.transcription_api_key || undefined });
      setVoiceTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setVoiceTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function testTts() {
    if (!editing || !form.tts_provider || !form.tts_voice) return;
    setTtsTestState({ loading: true });
    try {
      const res = await themApi.testTts(editing.id, { provider: form.tts_provider, voice: form.tts_voice, api_key: form.tts_api_key || undefined });
      setTtsTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setTtsTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function save() {
    setSaving(true);
    setFormError('');
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
      if (!form.transcription_api_key) delete body.transcription_api_key;
      if (!form.tts_api_key) delete body.tts_api_key;
      if (!form.summarizer_api_key) delete body.summarizer_api_key;

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

  return (
    <AuthGuard>
      <style>{ORCH_CARD_CSS}</style>
      <div style={{ display: 'flex', minHeight: '100vh', background: BG }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1, padding: '36px 48px' }}>
          <div style={{ marginBottom: 32 }}>
            <h1 style={{ fontSize: 26, fontWeight: 700, color: TEXT, margin: 0, fontFamily: 'Geist, sans-serif', letterSpacing: -0.5 }}>Orchestrators</h1>
            <p style={{ fontSize: 13, color: MUTED, margin: '6px 0 0', fontFamily: 'Inter, sans-serif' }}>
              Configure LLM pipelines, allowed agents, rate limits and voice capabilities.
            </p>
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: 20 }}>
            {list.map((o) => (
              <OrchestratorCard key={o.id} o={o} onEdit={openEdit} onDelete={del} onReload={load} />
            ))}

            <div
              className="orch-deploy-card"
              onClick={openCreate}
              style={{ borderRadius: 16, border: '2px dashed rgba(99,102,241,0.35)', background: 'rgba(99,102,241,0.02)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, cursor: 'pointer', minHeight: list.length === 0 ? 260 : 220, transition: 'border-color 200ms ease, background 200ms ease' }}
            >
              <div style={{ width: 52, height: 52, borderRadius: 14, border: '2px dashed rgba(99,102,241,0.5)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <span className="material-symbols-outlined" style={{ fontSize: 26, color: '#818cf8' }}>add</span>
              </div>
              <div style={{ fontSize: 14, fontWeight: 700, color: '#818cf8', fontFamily: 'Geist, sans-serif' }}>New Orchestrator</div>
            </div>
          </div>
        </main>
      </div>

      {showForm && (
        <OrchestratorForm
          editing={editing}
          form={form}
          setField={setField}
          allAgents={allAgents}
          saving={saving}
          formError={formError}
          testState={testState}
          voiceTestState={voiceTestState}
          ttsTestState={ttsTestState}
          onTestLlm={testLlm}
          onTestVoice={testVoice}
          onTestTts={testTts}
          onSave={save}
          onCancel={() => setShowForm(false)}
        />
      )}
    </AuthGuard>
  );
}
