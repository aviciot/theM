/**
 * Canvas Node Registry — frontend UI supplement.
 *
 * Structural node metadata (type, label, emoji, outputArity, isSource, isSink,
 * singleInput, inputField, executable, version) is the backend's source of truth.
 * Fetch it at runtime via fetchNodeTypes() / useNodeTypes().
 *
 * This file holds only what the frontend owns exclusively:
 *   - bg / border colours (design-system tokens)
 *   - summary(cfg) — subtitle rendered from live node config data
 */

// ── Types returned by GET /api/v1/admin/node-types ───────────────────────────

export interface EdgeRules {
  min_in:  number; // 0 = none required; 0+0 on a source = no incoming allowed
  max_in:  number; // 0 = unlimited
  min_out: number; // 0 = none required
  max_out: number; // 0 = unlimited
}

export interface NodeTypeInfo {
  type: string;
  version: number;
  label: string;
  emoji: string;
  output_arity: 'single' | 'multi' | 'none';
  is_source: boolean;
  is_sink: boolean;
  single_input: boolean;
  edges: EdgeRules;
  input_field?: string;
  executable: boolean;
}

// ── Frontend-only UI supplement per type ─────────────────────────────────────

export interface NodeUISupp {
  bg: string;
  border: string;
  summary: (cfg: Record<string, unknown>) => string;
}

// ── Merged view used by the builder ──────────────────────────────────────────

export type NodeDef = NodeTypeInfo & NodeUISupp;

// ── Color tokens ─────────────────────────────────────────────────────────────

const greenBg      = 'rgba(74,222,128,0.05)';
const greenBorder  = 'rgba(74,222,128,0.3)';
const cyanBg       = 'rgba(0,240,255,0.05)';
const cyanBorder   = 'rgba(0,240,255,0.4)';
const purpleBg     = 'rgba(87,27,193,0.1)';
const purpleBorder = '#d0bcff';
const amberBg      = 'rgba(245,158,11,0.05)';
const amberBorder  = 'rgba(245,158,11,0.3)';
const indigoBg     = 'rgba(99,102,241,0.1)';
const indigoBorder = 'rgba(99,102,241,0.5)';

// ── UI supplement — only colours and summary logic ───────────────────────────

const UI_SUPPS: Record<string, NodeUISupp> = {
  input: {
    bg: greenBg, border: greenBorder,
    summary: (cfg) => {
      const b = cfg.bindings as Record<string, string> | undefined;
      return b?.text ? `→ ${b.text}` : '→ input';
    },
  },
  llm: {
    bg: purpleBg, border: purpleBorder,
    summary: (cfg) => `→ ${(cfg.output_var as string) || 'output'}`,
  },
  http: {
    bg: amberBg, border: amberBorder,
    summary: (cfg) => {
      const url = cfg.url_template as string | undefined;
      return url ? url.replace(/^https?:\/\//, '').slice(0, 22) : 'url not set';
    },
  },
  transform: {
    bg: indigoBg, border: indigoBorder,
    summary: (cfg) => {
      const keys = Object.keys((cfg.expressions as Record<string, string> | undefined) ?? {});
      return keys.length ? `→ ${keys.join(', ')}` : '→ vars';
    },
  },
  response:   { bg: cyanBg,   border: cyanBorder,   summary: (cfg) => `from ${(cfg.from_var as string) || 'output'}` },
  branch:     { bg: amberBg,  border: amberBorder,  summary: () => '' },
  loop:       { bg: amberBg,  border: amberBorder,  summary: () => '' },
  parallel:   { bg: purpleBg, border: purpleBorder, summary: () => '' },
  a2a_call:   { bg: cyanBg,   border: cyanBorder,   summary: () => '' },
  human_wait: { bg: greenBg,  border: greenBorder,  summary: () => '' },
  stream_out: { bg: cyanBg,   border: cyanBorder,   summary: () => '' },
};

const FALLBACK_SUPP: NodeUISupp = {
  bg: indigoBg, border: indigoBorder,
  summary: () => '',
};

// ── API fetch ─────────────────────────────────────────────────────────────────

/** Fetch node type definitions from the backend (single source of truth). */
export async function fetchNodeTypes(): Promise<NodeDef[]> {
  const res = await fetch('/api/v1/admin/node-types', {
    credentials: 'include',
    headers: { Accept: 'application/json' },
  });
  if (!res.ok) throw new Error(`node-types fetch failed: ${res.status}`);
  const infos: NodeTypeInfo[] = await res.json();
  return infos.map(mergeSupp);
}

function mergeSupp(info: NodeTypeInfo): NodeDef {
  const supp = UI_SUPPS[info.type] ?? FALLBACK_SUPP;
  return { ...info, ...supp };
}

// ── In-memory cache populated after first fetch ───────────────────────────────

let _cache: NodeDef[] | null = null;
let _byType: Record<string, NodeDef> = {};

export function setCachedNodeTypes(defs: NodeDef[]): void {
  _cache = defs;
  _byType = {};
  for (const d of defs) _byType[d.type] = d;
}

export function getCachedNodeTypes(): NodeDef[] {
  return _cache ?? [];
}

// ── Helpers ───────────────────────────────────────────────────────────────────

const FALLBACK_EDGES: EdgeRules = { min_in: 0, max_in: 0, min_out: 0, max_out: 0 };

const FALLBACK_DEF = (type: string): NodeDef => ({
  type, version: 1, label: type, emoji: '🔧',
  output_arity: 'single', is_source: false, is_sink: false,
  single_input: false, edges: FALLBACK_EDGES, executable: false,
  ...FALLBACK_SUPP,
});

export function getNodeDef(type: string): NodeDef {
  return _byType[type] ?? FALLBACK_DEF(type);
}

export function isSingleInput(type: string): boolean  { return getNodeDef(type).single_input; }
export function isSource(type: string): boolean        { return getNodeDef(type).is_source; }
export function isSink(type: string): boolean          { return getNodeDef(type).is_sink; }
export function outputArity(type: string): 'single' | 'multi' | 'none' {
  return getNodeDef(type).output_arity;
}

// canAddIncoming: convention — is_source=true means no incoming allowed.
// max_in>0 caps at that count. max_in=0 means unlimited.
export function canAddIncoming(type: string, currentInCount: number): boolean {
  const def = getNodeDef(type);
  if (def.is_source) return false;
  if (def.edges.max_in > 0 && currentInCount >= def.edges.max_in) return false;
  return true;
}

// canAddOutgoing: convention — is_sink=true means no outgoing allowed.
// max_out>0 caps at that count. max_out=0 means unlimited.
export function canAddOutgoing(type: string, currentOutCount: number): boolean {
  const def = getNodeDef(type);
  if (def.is_sink) return false;
  if (def.edges.max_out > 0 && currentOutCount >= def.edges.max_out) return false;
  return true;
}
