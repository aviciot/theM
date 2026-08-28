/**
 * Canvas Node Registry — frontend UI supplement.
 *
 * Structural node metadata (type, label, emoji, outputArity, isSource, isSink,
 * singleInput, inputField, executable, version, color, bgColor,
 * acceptsDynamicInputs, dynamicOutputs) is the backend's source of truth.
 * Fetch it at runtime via fetchNodeTypes() / useNodeTypes().
 *
 * This file holds only what the frontend owns exclusively:
 *   - summary(cfg) — subtitle rendered from live node config data
 */

// ── Types returned by GET /api/v1/admin/node-types ───────────────────────────

export interface EdgeRules {
  min_in:  number; // 0 = none required; 0+0 on a source = no incoming allowed
  max_in:  number; // 0 = unlimited
  min_out: number; // 0 = none required
  max_out: number; // 0 = unlimited
}

export interface AppParamDecl {
  key: string;
  label: string;
  description: string;
  type: 'secret' | 'string' | 'url' | 'int' | 'bool';
  required: boolean;
  default_value?: string;
}

/** One named data port on a node type. Port IDs are permanent stable identifiers. */
export interface PortDef {
  id: string;        // stable binding handle (e.g. "output", "from_var")
  label: string;     // human-readable name for canvas UX
  required?: boolean;
  multi?: boolean;   // accepts multiple bindings (fan-in)
  type_hint?: string; // loose tag: "text" | "json" | "any"
}

export interface NodeTypeInfo {
  type: string;
  version: number;
  label: string;
  description: string;
  emoji: string;
  output_arity: 'single' | 'multi' | 'none';
  is_source: boolean;
  is_sink: boolean;
  single_input: boolean;
  edges: EdgeRules;
  /** Primary accent CSS color — border, handle, subtitle text. */
  color: string;
  /** Card background CSS color. */
  bg_color: string;
  /** Whether the user can drag a data-out port onto this node to create a named input port. */
  accepts_dynamic_inputs: boolean;
  /** Whether this node's output port names are derived from config (like transform). */
  dynamic_outputs: boolean;
  input_field?: string;
  executable: boolean;
  app_params?: AppParamDecl[];
  /** Static input data ports. Absent for types with dynamic inputs or no data inputs. */
  input_ports?: PortDef[];
  /** Static output data ports. Absent for types with dynamic outputs or no data outputs. */
  output_ports?: PortDef[];
}

// ── Frontend-only: summary function per type ─────────────────────────────────

type SummaryFn = (cfg: Record<string, unknown>) => string;

// ── Merged view used by the builder ──────────────────────────────────────────

export type NodeDef = NodeTypeInfo & { bg: string; border: string; summary: SummaryFn };

// ── Fallback colors (used when Go doesn't provide color — old server version) ─

const FALLBACK_BG     = 'rgba(99,102,241,0.1)';
const FALLBACK_BORDER = 'rgba(99,102,241,0.5)';

// ── Summary functions — the only thing the frontend owns exclusively ──────────

const SUMMARY_FNS: Record<string, SummaryFn> = {
  input: (cfg) => {
    const b = cfg.bindings as Record<string, string> | undefined;
    return b?.text ? `→ ${b.text}` : '→ input';
  },
  llm:        (cfg) => `→ ${(cfg.output_var as string) || 'output'}`,
  http: (cfg) => {
    const url = cfg.url_template as string | undefined;
    return url ? url.replace(/^https?:\/\//, '').slice(0, 22) : 'url not set';
  },
  transform: (cfg) => {
    const fns = cfg.functions as Array<{ output_var?: string }> | undefined;
    const vars = (fns ?? []).map(f => f.output_var).filter(Boolean);
    return vars.length ? `→ ${vars.join(', ')}` : '→ vars';
  },
  response:   (cfg) => `from ${(cfg.from_var as string) || 'output'}`,
  branch:     (cfg) => (cfg.expression as string) ? `if ${(cfg.expression as string).slice(0, 18)}` : 'set expression',
  mcp_call:   (cfg) => (cfg.tool_name as string) || '',
  a2a_call:   (cfg) => (cfg.agent_url as string) ? (cfg.agent_url as string).replace(/^https?:\/\//, '').slice(0, 22) : '',
};

const FALLBACK_SUMMARY: SummaryFn = () => '';

// ── API fetch ─────────────────────────────────────────────────────────────────

/** Fetch node type definitions from the backend (single source of truth). */
export async function fetchNodeTypes(): Promise<NodeDef[]> {
  const res = await fetch('/api/v1/admin/node-types', {
    credentials: 'include',
    headers: { Accept: 'application/json' },
  });
  if (!res.ok) throw new Error(`node-types fetch failed: ${res.status}`);
  const infos: NodeTypeInfo[] = await res.json();
  return infos.map(toNodeDef);
}

function toNodeDef(info: NodeTypeInfo): NodeDef {
  return {
    ...info,
    bg:      info.bg_color || FALLBACK_BG,
    border:  info.color    || FALLBACK_BORDER,
    summary: SUMMARY_FNS[info.type] ?? FALLBACK_SUMMARY,
  };
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
  type, version: 1, label: type, description: '', emoji: '🔧',
  output_arity: 'single', is_source: false, is_sink: false,
  single_input: false, edges: FALLBACK_EDGES, executable: false,
  color: FALLBACK_BORDER, bg_color: FALLBACK_BG,
  accepts_dynamic_inputs: true, dynamic_outputs: false,
  input_ports: undefined, output_ports: undefined,
  bg: FALLBACK_BG, border: FALLBACK_BORDER,
  summary: FALLBACK_SUMMARY,
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
export function acceptsDynamicInputs(type: string): boolean {
  return getNodeDef(type).accepts_dynamic_inputs;
}
export function hasDynamicOutputs(type: string): boolean {
  return getNodeDef(type).dynamic_outputs;
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
