// Canvas Node Definition Registry
// Single source of truth for all step node types used in the agent builder canvas.
// Each NodeDef declares visual metadata, arity, and summary logic for one type.

import type { ComponentType } from 'react';

export interface ConfigPanelProps {
  cfg: Record<string, unknown>;
  onChange: (cfg: Record<string, unknown>) => void;
  vars: string[]; // available upstream variable names
}

export interface NodeDef {
  type: string;
  label: string;
  emoji: string;
  bg: string;
  border: string;
  outputArity: 'single' | 'multi' | 'none';
  isSource: boolean;
  isSink: boolean;
  singleInput: boolean;   // only one incoming edge allowed
  inputField?: string;    // config key that holds the input variable name (for auto-fill on connect)
  summary: (cfg: Record<string, unknown>) => string;  // subtitle shown on node card
  ConfigPanel: ComponentType<ConfigPanelProps> | null;  // null = not yet extracted
}

// ── Color tokens (inlined from builder page C constants) ──────────────────────
const greenBg     = 'rgba(74,222,128,0.05)';
const greenBorder = 'rgba(74,222,128,0.3)';
const cyanBg      = 'rgba(0,240,255,0.05)';
const cyanBorder  = 'rgba(0,240,255,0.4)';
const purpleBg    = 'rgba(87,27,193,0.1)';
const purpleBorder = '#d0bcff';
const amberBg     = 'rgba(245,158,11,0.05)';
const amberBorder = 'rgba(245,158,11,0.3)';
const indigoBg    = 'rgba(99,102,241,0.1)';
const indigoBorder = 'rgba(99,102,241,0.5)';

// ── Registry ──────────────────────────────────────────────────────────────────

export const NODE_REGISTRY: Record<string, NodeDef> = {
  input: {
    type: 'input',
    label: 'Input',
    emoji: '📥',
    bg: greenBg,
    border: greenBorder,
    outputArity: 'single',
    isSource: true,
    isSink: false,
    singleInput: false,
    inputField: undefined,
    summary: (cfg) => {
      const bindings = cfg.bindings as Record<string, string> | undefined;
      return bindings?.text ? `→ ${bindings.text}` : '→ input';
    },
    ConfigPanel: null,
  },

  llm: {
    type: 'llm',
    label: 'LLM',
    emoji: '🧠',
    bg: purpleBg,
    border: purpleBorder,
    outputArity: 'single',
    isSource: false,
    isSink: false,
    singleInput: true,
    inputField: 'user_prompt',
    summary: (cfg) => `→ ${(cfg.output_var as string) || 'output'}`,
    ConfigPanel: null,
  },

  http: {
    type: 'http',
    label: 'HTTP',
    emoji: '🌐',
    bg: amberBg,
    border: amberBorder,
    outputArity: 'single',
    isSource: false,
    isSink: false,
    singleInput: false,
    inputField: 'url_template',
    summary: (cfg) => {
      const url = cfg.url_template as string | undefined;
      return url ? url.replace(/^https?:\/\//, '').slice(0, 22) : 'url not set';
    },
    ConfigPanel: null,
  },

  transform: {
    type: 'transform',
    label: 'Transform',
    emoji: '⚙️',
    bg: indigoBg,
    border: indigoBorder,
    outputArity: 'single',
    isSource: false,
    isSink: false,
    singleInput: true,
    inputField: 'expression',
    summary: (cfg) => {
      const keys = Object.keys((cfg.expressions as Record<string, string> | undefined) ?? {});
      return keys.length ? `→ ${keys.join(', ')}` : '→ vars';
    },
    ConfigPanel: null,
  },

  response: {
    type: 'response',
    label: 'Response',
    emoji: '📤',
    bg: cyanBg,
    border: cyanBorder,
    outputArity: 'none',
    isSource: false,
    isSink: true,
    singleInput: true,
    inputField: 'from_var',
    summary: (cfg) => `from ${(cfg.from_var as string) || 'output'}`,
    ConfigPanel: null,
  },

  branch: {
    type: 'branch',
    label: 'Branch',
    emoji: '🔀',
    bg: amberBg,
    border: amberBorder,
    outputArity: 'multi',
    isSource: false,
    isSink: false,
    singleInput: false,
    inputField: undefined,
    summary: () => '',
    ConfigPanel: null,
  },

  loop: {
    type: 'loop',
    label: 'Loop',
    emoji: '🔁',
    bg: amberBg,
    border: amberBorder,
    outputArity: 'single',
    isSource: false,
    isSink: false,
    singleInput: false,
    inputField: undefined,
    summary: () => '',
    ConfigPanel: null,
  },

  parallel: {
    type: 'parallel',
    label: 'Parallel',
    emoji: '⚡',
    bg: purpleBg,
    border: purpleBorder,
    outputArity: 'multi',
    isSource: false,
    isSink: false,
    singleInput: false,
    inputField: undefined,
    summary: () => '',
    ConfigPanel: null,
  },

  a2a_call: {
    type: 'a2a_call',
    label: 'A2A Call',
    emoji: '🤝',
    bg: cyanBg,
    border: cyanBorder,
    outputArity: 'single',
    isSource: false,
    isSink: false,
    singleInput: false,
    inputField: undefined,
    summary: () => '',
    ConfigPanel: null,
  },

  human_wait: {
    type: 'human_wait',
    label: 'Human Wait',
    emoji: '⏸️',
    bg: greenBg,
    border: greenBorder,
    outputArity: 'single',
    isSource: false,
    isSink: false,
    singleInput: false,
    inputField: undefined,
    summary: () => '',
    ConfigPanel: null,
  },

  stream_out: {
    type: 'stream_out',
    label: 'Stream Out',
    emoji: '📡',
    bg: cyanBg,
    border: cyanBorder,
    outputArity: 'none',
    isSource: false,
    isSink: true,
    singleInput: false,
    inputField: undefined,
    summary: () => '',
    ConfigPanel: null,
  },
};

// ── Fallback for unknown types ─────────────────────────────────────────────────

const UNKNOWN_DEF: NodeDef = {
  type: 'unknown',
  label: 'Unknown',
  emoji: '🔧',
  bg: indigoBg,
  border: indigoBorder,
  outputArity: 'single',
  isSource: false,
  isSink: false,
  singleInput: false,
  inputField: undefined,
  summary: () => '',
  ConfigPanel: null,
};

// ── Helpers ───────────────────────────────────────────────────────────────────

export function getNodeDef(type: string): NodeDef {
  return NODE_REGISTRY[type] ?? { ...UNKNOWN_DEF, type, label: type };
}

export function isSingleInput(type: string): boolean {
  return getNodeDef(type).singleInput;
}

export function isSource(type: string): boolean {
  return getNodeDef(type).isSource;
}

export function isSink(type: string): boolean {
  return getNodeDef(type).isSink;
}

export function outputArity(type: string): 'single' | 'multi' | 'none' {
  return getNodeDef(type).outputArity;
}
