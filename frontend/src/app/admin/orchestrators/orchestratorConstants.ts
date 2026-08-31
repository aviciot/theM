export const VOICE_MODELS: Record<string, string[]> = {
  openai: ['whisper-1', 'gpt-4o-transcribe'],
  groq:   ['whisper-large-v3-turbo'],
};

export const TTS_VOICES = ['nova', 'alloy', 'echo', 'fable', 'onyx', 'shimmer'];

export const MODELS: Record<string, string[]> = {
  anthropic: ['claude-opus-4-8', 'claude-opus-4-7', 'claude-opus-4-6', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
  openai:    ['gpt-4.1', 'gpt-4.1-mini', 'gpt-4o', 'gpt-4o-mini', 'o4-mini'],
  groq:      ['llama-3.3-70b-versatile', 'llama-3.1-70b-versatile', 'llama-3.1-8b-instant', 'mixtral-8x7b-32768', 'gemma2-9b-it'],
  gemini:    ['gemini-2.5-pro', 'gemini-2.5-flash', 'gemini-2.0-flash', 'gemini-1.5-pro', 'gemini-1.5-flash'],
};

export const EMPTY_FORM = {
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

export const BG      = 'var(--tm-bg)';
export const CYAN    = '#00d1ff';
export const PURPLE  = '#a78bfa';
export const GREEN   = '#34d399';
export const TEXT    = 'var(--tm-card-text)';
export const MUTED   = 'var(--tm-card-text-muted)';
export const BORDER  = 'var(--tm-card-border)';

export function providerColor(p: string | null): string {
  if (!p) return MUTED;
  if (p === 'anthropic') return '#d97706';
  if (p === 'openai')    return '#10b981';
  if (p === 'groq')      return '#f59e0b';
  if (p === 'gemini')    return '#3b82f6';
  return CYAN;
}

export function providerIcon(p: string | null): string {
  if (!p) return 'auto_awesome';
  if (p === 'anthropic') return 'brightness_7';
  if (p === 'openai')    return 'hub';
  if (p === 'groq')      return 'bolt';
  if (p === 'gemini')    return 'diamond';
  return 'smart_toy';
}

export const ORCH_CARD_CSS = `
.orch-glass-card {
    background:
      linear-gradient(160deg, rgba(255,255,255,0.032) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%),
      var(--tm-card);
    border: 1px solid var(--tm-card-border);
    backdrop-filter: blur(12px);
    box-shadow:
      0 8px 32px rgba(0,0,0,0.4),
      0 2px 8px rgba(0,0,0,0.25),
      inset 0 1px 0 rgba(255,255,255,0.04);
    transition: border-color 240ms ease, box-shadow 240ms ease;
  }
  .orch-glass-card:hover {
    border-color: rgba(0,209,255,0.28);
    box-shadow:
      0 8px 32px rgba(0,0,0,0.5),
      0 2px 8px rgba(0,0,0,0.28),
      0 0 0 1px rgba(0,209,255,0.1),
      0 0 32px rgba(0,209,255,0.08),
      inset 0 1px 0 rgba(255,255,255,0.055);
  }
  .orch-glass-card:active {
    box-shadow:
      0 4px 16px rgba(0,0,0,0.5),
      inset 0 1px 0 rgba(255,255,255,0.03);
    border-color: rgba(0,209,255,0.4);
    transition: border-color 80ms ease, box-shadow 80ms ease;
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
    background: #00d1ff; color: #021520; border: none;
    box-shadow: 0 0 14px rgba(0,209,255,0.38);
  }
  .orch-card-btn--test:hover {
    background: #22dcff;
    box-shadow: 0 0 22px rgba(0,209,255,0.55);
    transform: translateY(-1px);
  }
  .orch-card-btn--edit {
    background: var(--tm-btn-2-bg); color: #94a3b8;
    border: 1px solid var(--tm-btn-2-border);
  }
  .orch-card-btn--edit:hover {
    border-color: rgba(129,140,248,0.45);
    color: #818cf8;
    background: rgba(99,102,241,0.1);
  }
  .orch-card-btn--toggle-on {
    background: var(--tm-btn-2-bg); color: #f87171;
    border: 1px solid rgba(248,113,113,0.2);
  }
  .orch-card-btn--toggle-on:hover {
    border-color: rgba(248,113,113,0.5);
    background: rgba(248,113,113,0.08);
  }
  .orch-card-btn--toggle-off {
    background: var(--tm-btn-2-bg); color: #34d399;
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

export const INP: React.CSSProperties = {
  width: '100%', background: 'var(--tm-surface-2)', border: '1px solid var(--tm-border)',
  borderRadius: 8, padding: '8px 12px', color: 'var(--tm-text)', fontSize: 13, boxSizing: 'border-box',
};
