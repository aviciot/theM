'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type SystemAgentRoleOut, type SystemAgentRoleIn } from '@/lib/api';

// ── Design tokens (match agents/page.tsx) ─────────────────────────────────────

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: '8px',
  border: '1px solid var(--tm-input-border)',
  background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), var(--tm-inset)',
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
  fontSize: '14px', color: 'var(--tm-text)', boxSizing: 'border-box',
};

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '16px' }}>
      <label style={{
        display: 'block', fontSize: '11px', fontWeight: 700,
        color: 'var(--tm-text-muted)', marginBottom: '6px',
        textTransform: 'uppercase', letterSpacing: '0.06em',
      }}>
        {label}
      </label>
      {children}
      {hint && (
        <p style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '4px', opacity: 0.75 }}>
          {hint}
        </p>
      )}
    </div>
  );
}

// ── Toggle switch ─────────────────────────────────────────────────────────────

function Toggle({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      onClick={() => onChange(!value)}
      style={{
        display: 'flex', alignItems: 'center', gap: '10px',
        padding: '8px 16px', borderRadius: '9px', border: 'none',
        cursor: 'pointer', fontSize: '14px', fontWeight: 600,
        background: value ? 'rgba(16,185,129,0.15)' : 'rgba(100,116,139,0.15)',
        color: value ? '#34d399' : 'var(--tm-card-text-muted)',
        transition: 'all 0.18s',
      }}
    >
      <span style={{
        width: '32px', height: '18px', borderRadius: '9px', flexShrink: 0,
        background: value ? '#34d399' : '#475569',
        position: 'relative', display: 'inline-block',
        transition: 'background 0.18s',
      }}>
        <span style={{
          position: 'absolute', top: '3px',
          left: value ? '17px' : '3px',
          width: '12px', height: '12px', borderRadius: '50%',
          background: '#fff', transition: 'left 0.18s',
        }} />
      </span>
      {value ? 'Enabled' : 'Disabled'}
    </button>
  );
}

// ── Provider dropdown options (matching orchestrators page) ───────────────────

const PROVIDERS = [
  { value: '', label: '— select provider —' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai',    label: 'OpenAI' },
  { value: 'groq',      label: 'Groq' },
  { value: 'gemini',    label: 'Google Gemini' },
];

// ── Known model IDs per provider ──────────────────────────────────────────────

const PROVIDER_MODELS: Record<string, { value: string; label: string }[]> = {
  anthropic: [
    { value: 'claude-haiku-4-5-20251001', label: 'Claude Haiku 4.5' },
    { value: 'claude-sonnet-4-6',         label: 'Claude Sonnet 4.6' },
    { value: 'claude-sonnet-5',           label: 'Claude Sonnet 5' },
    { value: 'claude-opus-4-8',           label: 'Claude Opus 4.8' },
    { value: 'claude-fable-5',            label: 'Claude Fable 5' },
  ],
  openai: [
    { value: 'gpt-4o-mini',  label: 'GPT-4o Mini' },
    { value: 'gpt-4o',       label: 'GPT-4o' },
    { value: 'gpt-4-turbo',  label: 'GPT-4 Turbo' },
    { value: 'gpt-4.1-nano', label: 'GPT-4.1 Nano' },
  ],
  groq: [
    { value: 'llama-3.3-70b-versatile', label: 'LLaMA 3.3 70B' },
    { value: 'llama-3.1-8b-instant',    label: 'LLaMA 3.1 8B' },
    { value: 'mixtral-8x7b-32768',      label: 'Mixtral 8x7B' },
  ],
  gemini: [
    { value: 'gemini-2.0-flash', label: 'Gemini 2.0 Flash' },
    { value: 'gemini-1.5-pro',   label: 'Gemini 1.5 Pro' },
    { value: 'gemini-1.5-flash', label: 'Gemini 1.5 Flash' },
  ],
};

const CUSTOM_MODEL_SENTINEL = '__custom__';

// ── Default placeholder prompts per role ──────────────────────────────────────

const ROLE_DEFAULTS: Record<string, { label: string; description: string; promptPlaceholder: string; whereUsed: string }> = {
  classifier: {
    label: 'Classifier',
    description: 'Automatically categorizes agents and suggests icons during onboarding / discovery.',
    promptPlaceholder: 'You are an agent classifier. Given an agent\'s name, description, and skills, return ONLY valid JSON: {"category": "<Research|Coding|Vision|Security|A2A|Data|Communication|Agent>", "icon": "<Material Symbols name>"}. No explanation, no markdown, just JSON.',
    whereUsed: 'Runs automatically on agent creation (auto-assigns category & icon) and during Discover / Scan (updates category & icon from the agent card).',
  },
};

function getRoleLabel(role: string) {
  return ROLE_DEFAULTS[role]?.label ?? role.charAt(0).toUpperCase() + role.slice(1);
}
function getRoleDescription(role: string) {
  return ROLE_DEFAULTS[role]?.description ?? '';
}
function getRolePromptPlaceholder(role: string) {
  return ROLE_DEFAULTS[role]?.promptPlaceholder ?? '';
}
function getRoleWhereUsed(role: string) {
  return ROLE_DEFAULTS[role]?.whereUsed ?? '';
}

// ── Per-role form state ───────────────────────────────────────────────────────

interface RoleForm {
  enabled: boolean;
  provider: string;
  model: string;
  api_key: string;       // write-only; never pre-filled from server (only hint shown)
  base_url: string;
  system_prompt: string;
}

function roleToForm(r: SystemAgentRoleOut): RoleForm {
  return {
    enabled:       r.enabled,
    provider:      r.provider ?? '',
    model:         r.model ?? '',
    api_key:       '',               // never pre-fill — only show hint
    base_url:      r.base_url ?? '',
    system_prompt: r.system_prompt ?? '',
  };
}

// ── TestState ─────────────────────────────────────────────────────────────────

interface TestState {
  loading: boolean;
  ok?: boolean;
  latency?: number;
  error?: string;
}

// ── RoleCard ──────────────────────────────────────────────────────────────────

function RoleCard({
  role,
  apiKeyHint,
  form,
  onChange,
  onSave,
  saving,
  saveMsg,
}: {
  role: string;
  apiKeyHint: string | null;
  form: RoleForm;
  onChange: (patch: Partial<RoleForm>) => void;
  onSave: () => void;
  saving: boolean;
  saveMsg: { ok: boolean; text: string } | null;
}) {
  const [testState, setTestState] = useState<TestState>({ loading: false });

  // Determine whether the current model value is a known model for this provider
  const knownModels = PROVIDER_MODELS[form.provider] ?? [];
  const isKnownModel = knownModels.some((m) => m.value === form.model);
  // Show custom text input if provider has no known models, or user picked custom
  const [showCustom, setShowCustom] = useState(!isKnownModel && form.model !== '');

  function handleProviderChange(provider: string) {
    const models = PROVIDER_MODELS[provider] ?? [];
    onChange({ provider, model: models[0]?.value ?? '' });
    setShowCustom(false);
  }

  function handleModelSelectChange(val: string) {
    if (val === CUSTOM_MODEL_SENTINEL) {
      setShowCustom(true);
      onChange({ model: '' });
    } else {
      setShowCustom(false);
      onChange({ model: val });
    }
  }

  async function handleTest() {
    if (!form.provider || !form.model) return;
    setTestState({ loading: true });
    try {
      const res = await themApi.testSystemAgentLlm(role, {
        provider: form.provider,
        model:    form.model,
        api_key:  form.api_key || undefined,
        base_url: form.base_url || undefined,
      });
      setTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: unknown) {
      setTestState({ loading: false, ok: false, error: e instanceof Error ? e.message : 'Test failed' });
    }
  }

  const canTest = !!(form.provider && form.model);

  return (
    <div style={{
      background: 'linear-gradient(160deg, rgba(255,255,255,0.028) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%), var(--tm-card)',
      border: '1px solid var(--tm-card-border)',
      borderRadius: '18px',
      padding: '28px 32px',
      backdropFilter: 'blur(12px)',
      boxShadow: '0 8px 32px rgba(0,0,0,0.4), 0 2px 8px rgba(0,0,0,0.25), inset 0 1px 0 rgba(255,255,255,0.04)',
    }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '20px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '14px' }}>
          <div style={{
            width: '44px', height: '44px', borderRadius: '12px', flexShrink: 0,
            background: 'radial-gradient(circle at 30% 25%, rgba(0,209,255,0.18), transparent 65%), linear-gradient(145deg, rgba(20,32,52,0.96), rgba(8,16,30,0.96))',
            border: '1px solid rgba(0,209,255,0.35)',
            boxShadow: '0 0 16px rgba(0,209,255,0.12), inset 0 1px 0 var(--tm-card-border)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <span className="material-symbols-outlined" style={{ fontSize: '22px', color: '#00d1ff' }}>psychology</span>
          </div>
          <div>
            <h3 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-text)', margin: '0 0 4px 0', letterSpacing: '-0.01em' }}>
              {getRoleLabel(role)}
            </h3>
            <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: 0, lineHeight: 1.4 }}>
              {getRoleDescription(role)}
            </p>
            {getRoleWhereUsed(role) && (
              <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', margin: '6px 0 0 0', lineHeight: 1.4, opacity: 0.7, display: 'flex', alignItems: 'flex-start', gap: '5px' }}>
                <span className="material-symbols-outlined" style={{ fontSize: '14px', flexShrink: 0, marginTop: '1px' }}>info</span>
                {getRoleWhereUsed(role)}
              </p>
            )}
          </div>
        </div>
        <Toggle value={form.enabled} onChange={(v) => onChange({ enabled: v })} />
      </div>

      {/* Divider */}
      <div style={{ height: '1px', background: 'rgba(132,157,188,.1)', marginBottom: '22px' }} />

      {/* Provider + Model row */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
        <Field label="Provider">
          <select
            value={form.provider}
            onChange={(e) => handleProviderChange(e.target.value)}
            style={{ ...inputStyle, appearance: 'none', cursor: 'pointer' }}
          >
            {PROVIDERS.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>
        </Field>
        <Field label="Model">
          {knownModels.length > 0 && !showCustom ? (
            <select
              value={isKnownModel ? form.model : CUSTOM_MODEL_SENTINEL}
              onChange={(e) => handleModelSelectChange(e.target.value)}
              style={{ ...inputStyle, appearance: 'none', cursor: 'pointer' }}
            >
              {knownModels.map((m) => (
                <option key={m.value} value={m.value}>{m.label}</option>
              ))}
              <option value={CUSTOM_MODEL_SENTINEL}>Custom…</option>
            </select>
          ) : (
            <div style={{ display: 'flex', gap: '6px' }}>
              <input
                style={{ ...inputStyle, flex: 1 }}
                value={form.model}
                onChange={(e) => onChange({ model: e.target.value })}
                placeholder="model-id"
                autoFocus={showCustom}
              />
              {knownModels.length > 0 && (
                <button
                  type="button"
                  onClick={() => { setShowCustom(false); onChange({ model: knownModels[0].value }); }}
                  title="Pick from list"
                  style={{
                    padding: '0 10px', borderRadius: '8px', border: '1px solid var(--tm-input-border)',
                    background: 'var(--tm-inset)', color: 'var(--tm-text-muted)',
                    cursor: 'pointer', fontSize: '12px', flexShrink: 0,
                  }}
                >
                  ↩ List
                </button>
              )}
            </div>
          )}
        </Field>
      </div>

      {/* API Key */}
      <Field
        label={apiKeyHint ? `API Key (current: …${apiKeyHint})` : 'API Key'}
        hint={apiKeyHint ? 'Leave blank to keep the current key.' : undefined}
      >
        <input
          style={inputStyle}
          type="password"
          value={form.api_key}
          onChange={(e) => onChange({ api_key: e.target.value })}
          placeholder={apiKeyHint ? '••••••••  (leave blank to keep)' : 'sk-…'}
          autoComplete="new-password"
        />
      </Field>

      {/* Base URL */}
      <Field label="Base URL" hint="Optional — leave blank for the provider default.">
        <input
          style={inputStyle}
          value={form.base_url}
          onChange={(e) => onChange({ base_url: e.target.value })}
          placeholder="https://api.example.com/v1"
        />
      </Field>

      {/* System Prompt */}
      <Field label="System Prompt">
        <textarea
          style={{ ...inputStyle, minHeight: '100px', resize: 'vertical', fontFamily: 'monospace', fontSize: '12px', lineHeight: 1.5 }}
          value={form.system_prompt}
          onChange={(e) => onChange({ system_prompt: e.target.value })}
          placeholder={getRolePromptPlaceholder(role)}
        />
      </Field>

      {/* Test + Save row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginTop: '8px', flexWrap: 'wrap' }}>
        <button
          onClick={handleTest}
          disabled={testState.loading || !canTest}
          style={{
            display: 'flex', alignItems: 'center', gap: '6px',
            padding: '8px 18px', borderRadius: '8px',
            border: '1px solid var(--tm-border)',
            background: 'transparent',
            color: canTest ? 'var(--tm-text)' : 'var(--tm-text-muted)',
            cursor: (testState.loading || !canTest) ? 'not-allowed' : 'pointer',
            fontSize: '13px', fontWeight: 600,
            opacity: canTest ? 1 : 0.5,
            transition: 'border-color 150ms ease, color 150ms ease',
          }}
          title={!canTest ? 'Select a provider and model first' : undefined}
        >
          <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>bolt</span>
          {testState.loading ? 'Testing…' : 'Test'}
        </button>

        {!testState.loading && testState.ok !== undefined && (
          testState.ok
            ? <span style={{ fontSize: '13px', color: '#4edea3', fontWeight: 600 }}>
                Connected ({testState.latency}ms)
              </span>
            : <span style={{ fontSize: '13px', color: '#f87171' }}>
                {testState.error ?? 'Connection failed'}
              </span>
        )}

        <div style={{ flex: 1 }} />

        {saveMsg && (
          <span style={{
            fontSize: '13px', fontWeight: 600,
            color: saveMsg.ok ? '#4edea3' : '#f87171',
          }}>
            {saveMsg.text}
          </span>
        )}

        <button
          onClick={onSave}
          disabled={saving}
          style={{
            padding: '8px 22px', borderRadius: '9px', border: 'none',
            background: saving ? 'rgba(99,102,241,.5)' : 'var(--tm-accent)',
            color: '#fff', cursor: saving ? 'not-allowed' : 'pointer',
            fontSize: '14px', fontWeight: 600, opacity: saving ? 0.7 : 1,
            transition: 'opacity 150ms ease',
          }}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  );
}

// ── Main page ─────────────────────────────────────────────────────────────────

type SettingsTab = 'system_agents';

export default function AdminSettingsPage() {
  const [activeTab, setActiveTab] = useState<SettingsTab>('system_agents');
  const [loading,  setLoading]  = useState(true);
  const [unavailable, setUnavailable] = useState(false);
  // Per-role form state
  const [forms,    setForms]    = useState<Record<string, RoleForm>>({});
  // Per-role server-side api_key_hint
  const [hints,    setHints]    = useState<Record<string, string | null>>({});
  // Per-role save state
  const [saving,   setSaving]   = useState<Record<string, boolean>>({});
  const [saveMsgs, setSaveMsgs] = useState<Record<string, { ok: boolean; text: string } | null>>({});

  // Ordered list of role keys to render (preserve server order, add known defaults)
  const [roleOrder, setRoleOrder] = useState<string[]>([]);

  useEffect(() => {
    themApi.getSystemAgents()
      .then((data) => {
        const order = Object.keys(data.roles);
        // Ensure known roles are included even if not yet in DB
        const merged = Array.from(new Set([...order, ...Object.keys(ROLE_DEFAULTS)]));
        setRoleOrder(merged);
        const newForms: Record<string, RoleForm> = {};
        const newHints: Record<string, string | null> = {};
        for (const role of merged) {
          const srv = data.roles[role];
          newForms[role] = srv ? roleToForm(srv) : {
            enabled: false, provider: '', model: '', api_key: '', base_url: '', system_prompt: '',
          };
          newHints[role] = srv?.api_key_hint ?? null;
        }
        setForms(newForms);
        setHints(newHints);
      })
      .catch(() => {
        // Backend not deployed yet — show graceful empty state
        setUnavailable(true);
        // Still render known roles from defaults so UI isn't blank
        const order = Object.keys(ROLE_DEFAULTS);
        setRoleOrder(order);
        const newForms: Record<string, RoleForm> = {};
        for (const role of order) {
          newForms[role] = { enabled: false, provider: '', model: '', api_key: '', base_url: '', system_prompt: '' };
        }
        setForms(newForms);
        setHints({});
      })
      .finally(() => setLoading(false));
  }, []);

  function patchForm(role: string, patch: Partial<RoleForm>) {
    setForms((prev) => ({ ...prev, [role]: { ...prev[role], ...patch } }));
    // Clear stale save message when user edits
    setSaveMsgs((prev) => ({ ...prev, [role]: null }));
  }

  async function handleSave(role: string) {
    const f = forms[role];
    if (!f) return;
    setSaving((prev) => ({ ...prev, [role]: true }));
    setSaveMsgs((prev) => ({ ...prev, [role]: null }));

    const payload: SystemAgentRoleIn = {
      enabled:       f.enabled,
      provider:      f.provider   || null,
      model:         f.model      || null,
      base_url:      f.base_url   || null,
      system_prompt: f.system_prompt || null,
      // Only include api_key if the user typed something
      ...(f.api_key ? { api_key: f.api_key } : {}),
    };

    try {
      const updated = await themApi.putSystemAgents({ roles: { [role]: payload } });
      // Refresh hint from response
      const srv = updated.roles[role];
      if (srv) {
        setHints((prev) => ({ ...prev, [role]: srv.api_key_hint ?? null }));
        // Clear the api_key field after a successful save
        setForms((prev) => ({ ...prev, [role]: { ...prev[role], api_key: '' } }));
      }
      setSaveMsgs((prev) => ({ ...prev, [role]: { ok: true, text: 'Saved' } }));
      setUnavailable(false);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Save failed';
      setSaveMsgs((prev) => ({ ...prev, [role]: { ok: false, text: msg } }));
    } finally {
      setSaving((prev) => ({ ...prev, [role]: false }));
    }
  }

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1, background: 'var(--tm-bg)' }}>

          {/* Page header */}
          <div style={{ padding: '40px 32px 0' }}>
            <h2 style={{
              fontSize: '40px', fontWeight: 800, color: '#fff',
              margin: '0 0 6px 0', letterSpacing: '-0.03em', lineHeight: 1.1,
            }}>Settings</h2>
            <p style={{ fontSize: '14px', color: 'var(--tm-card-text-muted)', margin: '0 0 28px 0' }}>
              Platform-wide configuration for internal system helpers.
            </p>

            {/* Tab bar */}
            <div style={{
              display: 'flex', gap: '4px',
              borderBottom: '1px solid rgba(132,157,188,.12)',
              marginBottom: '0',
            }}>
              {([
                { id: 'system_agents' as SettingsTab, label: 'System Agents', icon: 'smart_toy' },
              ]).map((tab) => {
                const active = activeTab === tab.id;
                return (
                  <button
                    key={tab.id}
                    onClick={() => setActiveTab(tab.id)}
                    style={{
                      display: 'flex', alignItems: 'center', gap: '7px',
                      padding: '10px 18px',
                      border: 'none', background: 'transparent', cursor: 'pointer',
                      fontSize: '13px', fontWeight: active ? 700 : 500,
                      color: active ? 'var(--tm-accent)' : 'var(--tm-text-muted)',
                      borderBottom: active ? '2px solid var(--tm-accent)' : '2px solid transparent',
                      marginBottom: '-1px',
                      transition: 'color 150ms',
                    }}
                  >
                    <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>{tab.icon}</span>
                    {tab.label}
                  </button>
                );
              })}
            </div>
          </div>

          {/* Tab content */}
          <div style={{ padding: '28px 32px 64px', maxWidth: '860px' }}>

            {activeTab === 'system_agents' && (
              <>
                {/* Section description */}
                <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: '0 0 24px 0', lineHeight: 1.5 }}>
                  Internal LLM roles used by the platform. Each role has its own provider, model, and credentials.
                </p>

                {/* Unavailable banner */}
                {unavailable && !loading && (
                  <div style={{
                    padding: '12px 16px', borderRadius: '10px', marginBottom: '20px',
                    background: 'rgba(230,184,92,0.08)', border: '1px solid rgba(230,184,92,0.22)',
                    color: '#e6b85c', fontSize: '13px', display: 'flex', alignItems: 'center', gap: '8px',
                  }}>
                    <span className="material-symbols-outlined" style={{ fontSize: '16px', flexShrink: 0 }}>info</span>
                    Settings backend not available yet — changes will be saved once the backend is deployed.
                  </div>
                )}

                {/* Loading skeleton */}
                {loading && (
                  <div style={{
                    padding: '60px', textAlign: 'center',
                    color: 'var(--tm-card-text-muted)', fontSize: '14px',
                  }}>
                    Loading settings…
                  </div>
                )}

                {/* Role cards */}
                {!loading && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
                    {roleOrder.map((role) => (
                      <RoleCard
                        key={role}
                        role={role}
                        apiKeyHint={hints[role] ?? null}
                        form={forms[role] ?? { enabled: false, provider: '', model: '', api_key: '', base_url: '', system_prompt: '' }}
                        onChange={(patch) => patchForm(role, patch)}
                        onSave={() => handleSave(role)}
                        saving={!!saving[role]}
                        saveMsg={saveMsgs[role] ?? null}
                      />
                    ))}
                  </div>
                )}
              </>
            )}
          </div>
        </main>
      </div>
    </AuthGuard>
  );
}
