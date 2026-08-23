import type { DebugState, ValidationState } from './types';

// ── Design tokens ─────────────────────────────────────────────────────────────
export const C = {
  bg: 'var(--tm-bg)',
  surface: 'var(--tm-panel)',
  cyan: '#00f0ff',
  cyanBg: 'rgba(0,240,255,0.05)',
  cyanBorder: 'rgba(0,240,255,0.4)',
  purple: '#d0bcff',
  purpleBg: 'rgba(87,27,193,0.1)',
  purpleBorder: '#d0bcff',
  green: '#4ade80',
  greenBg: 'rgba(74,222,128,0.05)',
  greenBorder: 'rgba(74,222,128,0.3)',
  amber: '#f59e0b',
  amberBg: 'rgba(245,158,11,0.05)',
  amberBorder: 'rgba(245,158,11,0.3)',
  indigo: '#6366f1',
  indigoBg: 'rgba(99,102,241,0.1)',
  indigoBorder: 'rgba(99,102,241,0.5)',
  text: 'var(--tm-card-text)',
  textMuted: 'var(--tm-card-text-muted)',
  outline: 'var(--tm-canvas-border)',
};

// ── Shared panel styles ────────────────────────────────────────────────────────
export const labelStyle: React.CSSProperties = {
  fontSize: 11, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block', fontWeight: 700,
};
export const inputStyle: React.CSSProperties = {
  width: '100%', background: 'transparent',
  border: '1px solid var(--tm-canvas-border)', color: '#fff',
  padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box',
};
export const textareaStyle: React.CSSProperties = {
  ...inputStyle, resize: 'vertical' as const, fontFamily: 'inherit',
};
export const selectStyle: React.CSSProperties = { ...inputStyle };
export const fieldGap: React.CSSProperties = { marginTop: '12px' };
export const hint: React.CSSProperties = { fontSize: 10, color: '#64748b', marginLeft: 4 };
export const ctxItemStyle: React.CSSProperties = {
  display: 'block', width: '100%', textAlign: 'left',
  background: 'transparent', border: 'none', color: '#e2e8f0',
  padding: '7px 12px', borderRadius: '5px', cursor: 'pointer',
  fontSize: '13px', transition: 'background 0.1s',
};

// ── LLM models ────────────────────────────────────────────────────────────────
export const LLM_MODELS: Record<string, string[]> = {
  anthropic: ['claude-opus-4-8', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
};

// ── Step type palette list ────────────────────────────────────────────────────
export const STEP_TYPES: { type: import('@/lib/api').AgentStepDoc['type']; label: string }[] = [
  { type: 'input',      label: 'Input' },
  { type: 'llm',        label: 'LLM' },
  { type: 'http',       label: 'HTTP Tool' },
  { type: 'transform',  label: 'Transform' },
  { type: 'response',   label: 'Response' },
  { type: 'branch',     label: 'Branch' },
  { type: 'loop',       label: 'Loop' },
  { type: 'parallel',   label: 'Parallel' },
  { type: 'a2a_call',   label: 'A2A Call' },
  { type: 'human_wait', label: 'Human Wait' },
  { type: 'stream_out', label: 'Stream Out' },
];

// ── Initial state ─────────────────────────────────────────────────────────────
export const INITIAL_DEBUG: DebugState = {
  active: false, setupComplete: false, paramSpecs: [], debugParams: {},
  mode: null,
  vars: {}, nodeStates: {}, nodeInputVars: {}, nodeOutputs: {}, nodeErrors: {},
  edgeValues: {}, executionOrder: [], currentStepIndex: 0,
  pendingVarOverrides: {}, error: null,
};

export const INITIAL_VALIDATION: ValidationState = { issues: [], loading: false, lastValidatedAt: null };

// ── UUID generator ────────────────────────────────────────────────────────────
export function genUUID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
    const r = Math.random() * 16 | 0;
    return (c === 'x' ? r : (r & 0x3 | 0x8)).toString(16);
  });
}
