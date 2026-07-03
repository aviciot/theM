'use client';
import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { odinApi, type OrchestratorFull } from '@/lib/api';

// ── Model catalogues ──────────────────────────────────────────────────────
const VOICE_MODELS: Record<string, string[]> = {
  openai: ['whisper-1', 'gpt-4o-transcribe'],
  groq:   ['whisper-large-v3-turbo'],
};

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
};

// ── Sub-components ─────────────────────────────────────────────────────────
function Badge({ on }: { on: boolean }) {
  return (
    <span style={{ padding: '2px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: on ? '#4edea318' : '#f8717118', color: on ? '#4edea3' : '#f87171' }}>
      {on ? 'enabled' : 'disabled'}
    </span>
  );
}

function LLMBadge({ provider, model }: { provider: string | null; model: string | null }) {
  if (!provider) return <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontStyle: 'italic' }}>env default</span>;
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, padding: '2px 8px', borderRadius: 6, background: 'var(--tm-accent-subtle)', fontSize: 11, fontWeight: 600, color: 'var(--tm-accent)' }}>
      {provider} / {model ?? '—'}
    </span>
  );
}

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

  async function load() {
    setLoading(true);
    odinApi.orchestrators().then(setList).finally(() => setLoading(false));
  }

  useEffect(() => {
    load();
    odinApi.agents().then(setAllAgents);
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
    });
    setFormError(''); setTestState({ loading: false });
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
      const res = await odinApi.testLlm(editing.id, {
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
      };
      if (form.transcription_api_key) {
        body.transcription_api_key = form.transcription_api_key;
      } else {
        delete body.transcription_api_key;
      }
      if (editing) {
        await odinApi.updateOrchestrator(editing.id, body);
      } else {
        await odinApi.createOrchestrator(body);
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
    await odinApi.deleteOrchestrator(o.id).catch((e) => alert(e.message));
    load();
  }

  const modelOptions = form.llm_provider ? (MODELS[form.llm_provider] ?? []) : [];

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1 }}>
          <header style={{ position: 'sticky', top: 0, zIndex: 30, height: 56, background: 'var(--tm-topbar)', borderBottom: '1px solid var(--tm-topbar-border)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 28px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <span className="material-symbols-outlined" style={{ color: 'var(--tm-accent)', fontSize: 20 }}>account_tree</span>
              <span style={{ fontWeight: 600, fontSize: 15, color: 'var(--tm-text)' }}>Orchestrators</span>
            </div>
            <button onClick={openCreate} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: 'none', cursor: 'pointer', background: 'var(--tm-accent)', color: '#fff', fontSize: 13, fontWeight: 600 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 16 }}>add</span>New orchestrator
            </button>
          </header>

          <div style={{ padding: 28 }}>
            <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: 12, overflow: 'hidden' }}>
              {/* Table header */}
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 200px 80px 80px 80px 80px', padding: '8px 20px', gap: 12, background: 'rgba(255,255,255,.02)', borderBottom: '1px solid var(--tm-border)' }}>
                {['Name', 'LLM', 'Max iter', 'RPM', 'Status', ''].map((h) => (
                  <div key={h} style={{ fontSize: 11, fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{h}</div>
                ))}
              </div>

              {loading ? (
                <div style={{ padding: 40, textAlign: 'center', color: 'var(--tm-text-muted)', fontSize: 13 }}>Loading…</div>
              ) : list.length === 0 ? (
                <div style={{ padding: 60, textAlign: 'center' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 40, color: 'var(--tm-text-muted)', display: 'block', marginBottom: 12 }}>account_tree</span>
                  <div style={{ color: 'var(--tm-text-muted)', fontSize: 14, marginBottom: 16 }}>No orchestrators yet</div>
                  <button onClick={openCreate} style={{ padding: '8px 20px', borderRadius: 8, border: 'none', cursor: 'pointer', background: 'var(--tm-accent)', color: '#fff', fontWeight: 600, fontSize: 13 }}>Create first orchestrator</button>
                </div>
              ) : list.map((o) => (
                <div key={o.id} style={{ display: 'grid', gridTemplateColumns: '1fr 200px 80px 80px 80px 80px', alignItems: 'center', padding: '14px 20px', gap: 12, borderBottom: '1px solid var(--tm-border-subtle)' }}>
                  <div>
                    <div style={{ fontWeight: 600, color: 'var(--tm-text)', fontSize: 13 }}>{o.display_name}</div>
                    <code style={{ fontSize: 11, color: 'var(--tm-text-muted)', background: 'var(--tm-surface-2)', padding: '1px 5px', borderRadius: 4 }}>{o.name}</code>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <LLMBadge provider={o.llm_provider} model={o.llm_model} />
                    {o.llm_api_key_hint && <span title={`Key: ${o.llm_api_key_hint}`} className="material-symbols-outlined" style={{ fontSize: 14, color: '#4edea3' }}>key</span>}
                  </div>
                  <div style={{ fontSize: 13, color: 'var(--tm-text-muted)' }}>{o.max_iterations}</div>
                  <div style={{ fontSize: 13, color: 'var(--tm-text-muted)' }}>{o.rate_limit_rpm}</div>
                  <Badge on={o.enabled} />
                  <div style={{ display: 'flex', gap: 4 }}>
                    <button onClick={() => router.push(`/admin/playground?orchestrator=${o.name}`)} title="Test in Playground" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: '#a78bfa', padding: 4 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 18 }}>science</span>
                    </button>
                    <button onClick={() => openEdit(o)} title="Edit" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--tm-text-muted)', padding: 4 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 18 }}>edit</span>
                    </button>
                    <button onClick={() => del(o)} title="Delete" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: '#f87171', padding: 4 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 18 }}>delete</span>
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
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
