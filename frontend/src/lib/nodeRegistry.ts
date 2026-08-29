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
  id: string;              // stable binding handle (e.g. "output", "from_var")
  label: string;           // human-readable name for canvas UX
  required?: boolean;
  multi?: boolean;         // accepts multiple bindings (fan-in)
  type_hint?: string;      // loose tag: "text" | "json" | "any"
  color?: string;          // per-port accent color override (e.g. branch true=green, false=red)
  max_connections?: number; // 0 = unlimited; used by connection guard
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
  /**
   * Named control-flow output ports. Populated only for nodes with multiple named
   * control exits (e.g. branch: true/false). Empty = single anonymous control output.
   * Handle IDs: "ctrl-out-{portID}" (e.g. "ctrl-out-true", "ctrl-out-false").
   */
  control_output_ports?: PortDef[];
  /**
   * JSONPath-like expression identifying which config field drives dynamic output
   * port names. Only meaningful when dynamic_outputs=true.
   * Format: "functions[].output_var" — iterate cfg[array], collect field value.
   */
  dynamic_output_source?: string;
}

// ── Port resolution — generic, driven entirely by NodeDef ────────────────────

/**
 * A resolved port ready for React Flow Handle rendering.
 * Direction and kind are derived from context (input vs output array, control vs data).
 */
export interface ResolvedPort {
  id: string;        // stable handle ID (used as React Flow Handle id)
  label: string;     // truncated for display (~8 chars max)
  kind: 'control' | 'data';
  direction: 'in' | 'out';
  color: string;     // CSS color for the handle dot
  required: boolean;
  maxConnections: number; // 0 = unlimited
}

const LABEL_MAX = 8;
function truncLabel(s: string): string {
  return s.length <= LABEL_MAX ? s : s.slice(0, LABEL_MAX - 1) + '…';
}

/**
 * Resolve the dynamic output source path (e.g. "functions[].output_var") against
 * a live step config object, returning the list of port names.
 * Supports exactly the "array[].field" pattern used by transform.
 */
export function resolveDynamicOutputNames(
  source: string,
  cfg: Record<string, unknown>,
): string[] {
  // Parse "arrayKey[].fieldKey"
  const m = source.match(/^(\w+)\[\]\.(\w+)$/);
  if (!m) return [];
  const [, arrayKey, fieldKey] = m;
  const arr = cfg[arrayKey];
  if (!Array.isArray(arr)) return [];
  const seen = new Set<string>();
  const names: string[] = [];
  for (const item of arr as Record<string, unknown>[]) {
    const val = item[fieldKey];
    if (typeof val === 'string' && val && !seen.has(val)) {
      seen.add(val);
      names.push(val);
    }
  }
  return names;
}

/**
 * Resolve all input ports for a node instance — static registry ports plus any
 * dynamic input ports committed to the step (from data-edge bindings).
 *
 * Dynamic input port names come from the step's committed inputs map; they are
 * rendered as data-kind handles on the target side.
 */
export function resolveInputPorts(
  nodeDef: NodeDef,
  committedInputPortIDs: string[], // keys from step.inputs (already-wired port IDs)
): ResolvedPort[] {
  const ports: ResolvedPort[] = [];

  const emittedIDs = new Set<string>();

  // Static registry input ports first — prefer registry metadata (label, color, required)
  // over the plain port-ID fallback used for dynamic ports.
  // Only render if already wired (present in committedInputPortIDs).
  const committedSet = new Set(committedInputPortIDs);
  for (const port of nodeDef.input_ports ?? []) {
    if (!committedSet.has(port.id)) continue;
    const handleID = `data-in-${port.id}`;
    emittedIDs.add(handleID);
    ports.push({
      id: handleID,
      label: truncLabel(port.label || port.id),
      kind: 'data',
      direction: 'in',
      color: port.color || '#f97316',
      required: port.required ?? false,
      maxConnections: port.max_connections ?? 1,
    });
  }

  // Dynamic committed inputs — ports created by drag-and-wire that are NOT in the
  // static registry (e.g. user-named variables). Skip any already emitted above.
  for (const portID of committedInputPortIDs) {
    const handleID = `data-in-${portID}`;
    if (emittedIDs.has(handleID)) continue;
    ports.push({
      id: handleID,
      label: truncLabel(portID),
      kind: 'data',
      direction: 'in',
      color: '#f97316',
      required: false,
      maxConnections: 1,
    });
  }

  return ports;
}

/**
 * Resolve all output ports for a node instance:
 * 1. Named control-flow output ports (from control_output_ports, e.g. branch true/false)
 * 2. One anonymous control output (when no named control ports and node is not a sink)
 * 3. Dynamic data output ports (from dynamic_output_source evaluated against cfg)
 * 4. Static data output ports (from output_ports) — only if already wired, unless
 *    options.includeUnwiredStatic is true (used by PortsPopover to show all available outputs)
 */
export function resolveOutputPorts(
  nodeDef: NodeDef,
  cfg: Record<string, unknown>,
  committedOutputPortIDs: string[] = [], // port IDs that already have a data edge from them
  options?: { includeUnwiredStatic?: boolean },
): ResolvedPort[] {
  const ports: ResolvedPort[] = [];

  // Named control-flow outputs (e.g. branch: true/false)
  const ctrlPorts = nodeDef.control_output_ports ?? [];
  if (ctrlPorts.length > 0) {
    for (const port of ctrlPorts) {
      ports.push({
        id: `ctrl-out-${port.id}`,
        label: truncLabel(port.label || port.id),
        kind: 'control',
        direction: 'out',
        color: port.color || nodeDef.border || '#64748b',
        required: port.required ?? false,
        maxConnections: port.max_connections ?? 1,
      });
    }
  } else if (!nodeDef.is_sink) {
    // Single anonymous control output — the common case
    ports.push({
      id: 'ctrl-out',
      label: '',
      kind: 'control',
      direction: 'out',
      color: nodeDef.border || '#64748b',
      required: false,
      maxConnections: 0,
    });
  }

  // Dynamic data output ports (transform: functions[].output_var)
  if (nodeDef.dynamic_outputs && nodeDef.dynamic_output_source) {
    const names = resolveDynamicOutputNames(nodeDef.dynamic_output_source, cfg);
    for (const name of names) {
      ports.push({
        id: `data-out-${name}`,
        label: truncLabel(name),
        kind: 'data',
        direction: 'out',
        color: '#818cf8', // indigo — data output
        required: false,
        maxConnections: 0,
      });
    }
  }

  // Static data output ports (llm: output, etc.) — only show if already wired via a data edge,
  // unless includeUnwiredStatic is true (for PortsPopover showing all available ports).
  const wiredOutputSet = new Set(committedOutputPortIDs);
  for (const port of nodeDef.output_ports ?? []) {
    if (!options?.includeUnwiredStatic && !wiredOutputSet.has(port.id)) continue;
    ports.push({
      id: `data-out-${port.id}`,
      label: truncLabel(port.label || port.id),
      kind: 'data',
      direction: 'out',
      color: port.color || '#818cf8',
      required: port.required ?? false,
      maxConnections: port.max_connections ?? 0,
    });
  }

  return ports;
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
