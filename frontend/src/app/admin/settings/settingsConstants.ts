import type { MonitoringConfig } from '@/lib/api';

export const PROVIDERS = [
  { value: '', label: '— select provider —' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai',    label: 'OpenAI' },
  { value: 'groq',      label: 'Groq' },
  { value: 'gemini',    label: 'Google Gemini' },
];

export const PROVIDER_MODELS: Record<string, { value: string; label: string }[]> = {
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

export const CUSTOM_MODEL_SENTINEL = '__custom__';

export const ROLE_DEFAULTS: Record<string, { label: string; description: string; promptPlaceholder: string; whereUsed: string }> = {
  classifier: {
    label: 'Classifier',
    description: 'Automatically categorizes agents and suggests icons during onboarding / discovery.',
    promptPlaceholder: 'You are an agent classifier. Given an agent\'s name, description, and skills, return ONLY valid JSON: {"category": "<Research|Coding|Vision|Security|A2A|Data|Communication|Agent>", "icon": "<Material Symbols name>"}. No explanation, no markdown, just JSON.',
    whereUsed: 'Runs automatically on agent creation (auto-assigns category & icon) and during Discover / Scan (updates category & icon from the agent card).',
  },
  card_synthesizer: {
    label: 'Card Synthesizer',
    description: 'Synthesizes an A2A agent card for application entry points by analysing the orchestrator purpose and sub-agent skills.',
    promptPlaceholder: 'You are an AI application analyst. Given an orchestrator\'s purpose and its sub-agents, synthesize a JSON A2A agent card. Return ONLY: {"name":"...","description":"...","skills":[{"id":"...","name":"...","description":"...","tags":[...]}]}',
    whereUsed: 'Runs when clicking "Synthesize Card" on an A2A entry point in the Applications admin. The result is stored per entry point and served as the public A2A agent card.',
  },
};

export function getRoleLabel(role: string) {
  return ROLE_DEFAULTS[role]?.label ?? role.charAt(0).toUpperCase() + role.slice(1);
}
export function getRoleDescription(role: string) {
  return ROLE_DEFAULTS[role]?.description ?? '';
}
export function getRolePromptPlaceholder(role: string) {
  return ROLE_DEFAULTS[role]?.promptPlaceholder ?? '';
}
export function getRoleWhereUsed(role: string) {
  return ROLE_DEFAULTS[role]?.whereUsed ?? '';
}

export const MONITORING_DEFAULTS: MonitoringConfig = {
  heatmap_low:           1,
  heatmap_medium:        10,
  heatmap_high:          50,
  edge_thin:             1,
  edge_medium:           10,
  edge_thick:            50,
  panel_max_sessions:    50,
  stats_window_seconds:  300,
};

export const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: '8px',
  border: '1px solid var(--tm-input-border)',
  background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), var(--tm-inset)',
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
  fontSize: '14px', color: 'var(--tm-text)', boxSizing: 'border-box',
};
