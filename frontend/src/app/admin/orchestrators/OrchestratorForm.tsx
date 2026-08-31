'use client';
import type { Agent, OrchestratorFull } from '@/lib/api';
import { MODELS, VOICE_MODELS, TTS_VOICES, INP } from './orchestratorConstants';

type FormState = ReturnType<typeof import('./orchestratorConstants').EMPTY_FORM extends infer T ? () => T : never>;

function Field({ label, children, disabled }: { label: string; children: React.ReactNode; disabled?: boolean }) {
  return (
    <div style={{ opacity: disabled ? 0.5 : 1 }}>
      <label style={{ display: 'block', fontSize: 11, fontWeight: 700, color: 'var(--tm-text-muted)', marginBottom: 5, textTransform: 'uppercase', letterSpacing: '0.4px' }}>{label}</label>
      {children}
    </div>
  );
}

type TestState = { loading: boolean; ok?: boolean; latency?: number; error?: string };

export function OrchestratorForm({
  editing,
  form,
  setField,
  allAgents,
  saving,
  formError,
  testState,
  voiceTestState,
  ttsTestState,
  onTestLlm,
  onTestVoice,
  onTestTts,
  onSave,
  onCancel,
}: {
  editing: OrchestratorFull | null;
  form: typeof import('./orchestratorConstants').EMPTY_FORM;
  setField: (k: string, v: any) => void;
  allAgents: Agent[];
  saving: boolean;
  formError: string;
  testState: TestState;
  voiceTestState: TestState;
  ttsTestState: TestState;
  onTestLlm: () => void;
  onTestVoice: () => void;
  onTestTts: () => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const modelOptions = form.llm_provider ? (MODELS[form.llm_provider] ?? []) : [];

  function onProviderChange(provider: string) {
    setField('llm_provider', provider);
    if (provider && MODELS[provider]) {
      setField('llm_model', MODELS[provider][0]);
    } else {
      setField('llm_model', '');
    }
  }

  return (
    <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.65)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24, overflowY: 'auto' }}>
      <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: 16, width: '100%', maxWidth: 600, maxHeight: '92vh', overflowY: 'auto', padding: 32 }}>
        <h2 style={{ fontSize: 16, fontWeight: 700, color: 'var(--tm-text)', marginBottom: 24 }}>
          {editing ? 'Edit orchestrator' : 'New orchestrator'}
        </h2>

        {formError && <div style={{ marginBottom: 16, padding: '10px 14px', borderRadius: 8, background: '#f8717118', color: '#f87171', fontSize: 13 }}>{formError}</div>}

        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {/* Basic */}
          <Field label="Slug" disabled={!!editing}>
            <input value={form.name} onChange={(e) => setField('name', e.target.value)} placeholder="my-orchestrator" style={INP} disabled={!!editing} />
          </Field>
          <Field label="Display name">
            <input value={form.display_name} onChange={(e) => setField('display_name', e.target.value)} placeholder="My Orchestrator" style={INP} />
          </Field>
          <Field label="System prompt">
            <textarea value={form.system_prompt} onChange={(e) => setField('system_prompt', e.target.value)} rows={3} style={{ ...INP, resize: 'vertical', fontFamily: 'inherit' }} />
          </Field>

          {/* LLM Configuration */}
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
                <select value={form.llm_model} onChange={(e) => setField('llm_model', e.target.value)} disabled={!form.llm_provider} style={INP}>
                  {!form.llm_provider && <option value="">select provider first</option>}
                  {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
                </select>
              </Field>
            </div>
            <div style={{ marginTop: 12 }}>
              <Field label={editing?.llm_api_key_hint ? `API key (current: ${editing.llm_api_key_hint})` : 'API key'}>
                <input type="password" value={form.llm_api_key} onChange={(e) => setField('llm_api_key', e.target.value)} placeholder={editing?.llm_api_key_hint ? 'Leave blank to keep existing key' : 'sk-…'} style={INP} />
              </Field>
            </div>
            <div style={{ marginTop: 12 }}>
              <Field label="Base URL override (optional)">
                <input value={form.llm_base_url} onChange={(e) => setField('llm_base_url', e.target.value)} placeholder="https://api.example.com" style={INP} />
              </Field>
            </div>
            {editing && form.llm_provider && form.llm_model && (
              <div style={{ marginTop: 14, display: 'flex', alignItems: 'center', gap: 12 }}>
                <button onClick={onTestLlm} disabled={testState.loading} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text)', cursor: testState.loading ? 'wait' : 'pointer', fontSize: 13, fontWeight: 600 }}>
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

          {/* Allowed Agents */}
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
                        setField('allowed_agent_ids', ids);
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

          {/* Voice */}
          <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 18, marginTop: 4 }}>
            <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tm-accent)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 6 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 15 }}>mic</span>
              Voice
            </div>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', marginBottom: 12 }}>
              <input type="checkbox" checked={form.voice_enabled} onChange={(e) => setField('voice_enabled', e.target.checked)} style={{ width: 16, height: 16 }} />
              <span style={{ fontSize: 13, color: 'var(--tm-text)' }}>Enable voice input</span>
            </label>
            {form.voice_enabled && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                  <Field label="Transcription provider">
                    <select value={form.transcription_provider} onChange={(e) => { setField('transcription_provider', e.target.value); const models = VOICE_MODELS[e.target.value]; if (models) setField('transcription_model', models[0]); }} style={INP}>
                      <option value="openai">OpenAI</option>
                      <option value="groq">Groq</option>
                    </select>
                  </Field>
                  <Field label="Transcription model">
                    <select value={form.transcription_model} onChange={(e) => setField('transcription_model', e.target.value)} style={INP}>
                      {(VOICE_MODELS[form.transcription_provider] ?? []).map((m) => <option key={m} value={m}>{m}</option>)}
                    </select>
                  </Field>
                </div>
                <Field label="Transcription API key (optional override)">
                  <input type="password" value={form.transcription_api_key} onChange={(e) => setField('transcription_api_key', e.target.value)} placeholder="optional override" style={INP} />
                </Field>
                {editing && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <button onClick={onTestVoice} disabled={voiceTestState.loading || !form.transcription_provider || !form.transcription_model} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text)', cursor: voiceTestState.loading ? 'wait' : 'pointer', fontSize: 13, fontWeight: 600 }}>
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
              <input type="checkbox" checked={form.tts_enabled} onChange={(e) => setField('tts_enabled', e.target.checked)} style={{ width: 16, height: 16 }} />
              <span style={{ fontSize: 13 }}>Enable TTS — read responses aloud</span>
            </label>
            {form.tts_enabled && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                  <Field label="TTS provider">
                    <select value={form.tts_provider} onChange={(e) => setField('tts_provider', e.target.value)} style={INP}>
                      <option value="openai">OpenAI</option>
                    </select>
                  </Field>
                  <Field label="Voice">
                    <select value={form.tts_voice} onChange={(e) => setField('tts_voice', e.target.value)} style={INP}>
                      {TTS_VOICES.map((v) => <option key={v} value={v}>{v}</option>)}
                    </select>
                  </Field>
                </div>
                <Field label="TTS API key (optional override)">
                  <input type="password" value={form.tts_api_key} onChange={(e) => setField('tts_api_key', e.target.value)} placeholder="optional override" style={INP} />
                </Field>
                {editing && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <button onClick={onTestTts} disabled={ttsTestState.loading} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text)', cursor: ttsTestState.loading ? 'wait' : 'pointer', fontSize: 13, fontWeight: 600 }}>
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
              <input type="checkbox" checked={form.memory_enabled} onChange={(e) => setField('memory_enabled', e.target.checked)} style={{ width: 16, height: 16 }} />
              <span style={{ fontSize: 13 }}>Enable context summarization memory</span>
            </label>
            {form.memory_enabled && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                  <Field label="Summarize every N agent calls">
                    <input type="number" min={1} value={form.summarize_every_n_calls} onChange={(e) => setField('summarize_every_n_calls', e.target.value)} style={INP} />
                  </Field>
                  <Field label="Raw context fallback N">
                    <input type="number" min={1} value={form.memory_raw_fallback_n} onChange={(e) => setField('memory_raw_fallback_n', e.target.value)} style={INP} />
                  </Field>
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                  <Field label="Summarizer provider (optional override)">
                    <select value={form.summarizer_provider} onChange={(e) => { setField('summarizer_provider', e.target.value); if (e.target.value && MODELS[e.target.value]) { setField('summarizer_model', MODELS[e.target.value][0]); } else { setField('summarizer_model', ''); } }} style={INP}>
                      <option value="">env default (anthropic / haiku)</option>
                      {Object.keys(MODELS).map((p) => <option key={p} value={p}>{p}</option>)}
                    </select>
                  </Field>
                  <Field label="Summarizer model">
                    <select value={form.summarizer_model} onChange={(e) => setField('summarizer_model', e.target.value)} style={INP} disabled={!form.summarizer_provider}>
                      <option value="">{form.summarizer_provider ? 'select model' : '—'}</option>
                      {(form.summarizer_provider ? (MODELS[form.summarizer_provider] ?? []) : []).map((m) => <option key={m} value={m}>{m}</option>)}
                    </select>
                  </Field>
                </div>
                <Field label="Summarizer API key (optional override)">
                  <input type="password" value={form.summarizer_api_key} onChange={(e) => setField('summarizer_api_key', e.target.value)} placeholder="optional override" style={INP} />
                </Field>
              </div>
            )}
          </div>

          {/* Limits */}
          <div style={{ borderTop: '1px solid var(--tm-border)', paddingTop: 18, marginTop: 4 }}>
            <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 14 }}>Limits</div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12 }}>
              <Field label="Max iterations">
                <input type="number" value={form.max_iterations} onChange={(e) => setField('max_iterations', e.target.value)} style={INP} />
              </Field>
              <Field label="Parallel tools">
                <input type="number" value={form.max_parallel_tools} onChange={(e) => setField('max_parallel_tools', e.target.value)} style={INP} />
              </Field>
              <Field label="History window (turns, -1 = unlimited)">
                <input type="number" min={-1} value={form.history_window} onChange={(e) => setField('history_window', e.target.value)} style={INP} />
              </Field>
              <Field label="Rate limit (rpm)">
                <input type="number" value={form.rate_limit_rpm} onChange={(e) => setField('rate_limit_rpm', e.target.value)} style={INP} />
              </Field>
            </div>
          </div>

          <Field label="Enabled">
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
              <input type="checkbox" checked={form.enabled} onChange={(e) => setField('enabled', e.target.checked)} style={{ width: 16, height: 16 }} />
              <span style={{ fontSize: 13, color: 'var(--tm-text)' }}>Active</span>
            </label>
          </Field>
        </div>

        <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end', marginTop: 28 }}>
          <button onClick={onCancel} style={{ padding: '8px 18px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text)', cursor: 'pointer', fontSize: 13 }}>Cancel</button>
          <button onClick={onSave} disabled={saving} style={{ padding: '8px 20px', borderRadius: 8, border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: 'pointer', fontWeight: 600, fontSize: 13, opacity: saving ? 0.7 : 1 }}>
            {saving ? 'Saving…' : editing ? 'Save changes' : 'Create'}
          </button>
        </div>
      </div>
    </div>
  );
}
