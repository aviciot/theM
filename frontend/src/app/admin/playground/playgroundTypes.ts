// Shared types and pure helpers for the Playground feature.
// No React imports, no @/lib/api imports — keeps this tree-shakeable.

// ── Connection target ──────────────────────────────────────────────────────
export type ConnTarget =
  | { kind: 'orchestrator'; name: string; label: string }
  | { kind: 'entrypoint'; slug: string; appSlug: string; epType: 'websocket' | 'sse' | 'voice' | 'a2a'; appName: string; orchName: string };

export function targetLabel(t: ConnTarget): string {
  if (t.kind === 'orchestrator') return t.label;
  return `${t.appName} · ${t.slug}`;
}

export function targetId(t: ConnTarget): string {
  if (t.kind === 'orchestrator') return `orch:${t.name}`;
  return `ep:${t.appSlug}/${t.slug}`;
}

export function targetStorageKey(t: ConnTarget): string {
  if (t.kind === 'orchestrator') return t.name;
  return `${t.appSlug}/${t.slug}`;
}

export function targetWsUrl(t: ConnTarget, token: string): string {
  const base = getBridgeWs();
  if (t.kind === 'orchestrator') return `${base}/ws/orchestrate/${t.name}?token=${encodeURIComponent(token)}`;
  return `${base}/apps/${t.appSlug}/${t.slug}/ws?token=${encodeURIComponent(token)}`;
}

// Tab colour palette — cycles for each open tab
export const TAB_COLORS = ['#7c3aed', '#0ea5e9', '#10b981', '#f59e0b', '#ec4899', '#f87171'];

export function getBridgeWs(): string {
  if (typeof window === 'undefined') return '';
  if (process.env.NEXT_PUBLIC_BRIDGE_WS_URL) return process.env.NEXT_PUBLIC_BRIDGE_WS_URL;
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.host}`;
}

// ── Shared types ──────────────────────────────────────────────────────────

export type FileMsg = { filename: string; media_type: string; text: string };
export type ChatMsg = { role: 'user' | 'assistant'; text: string; pending?: boolean; file?: FileMsg };

export type AgentActivity = {
  agent: string;
  state: string;
  elapsed_ms: number;
  displayState: string;
  visibleUntil: number;
};

export type TraceEvent = { ts: number; type: string; [key: string]: unknown };
export type TraceGroup = {
  iteration: number;
  startTs: number;
  agents: string[];
  events: TraceEvent[];
  usage?: TraceEvent;
};

export type RecordingState = 'idle' | 'recording' | 'transcribing';
export type DebugTab = 'trace' | 'tasks' | 'artifacts' | 'sessions';

// Inline markdown segment types
export type Segment = { t: 'bold'; v: string } | { t: 'italic'; v: string } | { t: 'code'; v: string } | { t: 'text'; v: string };
export type Block =
  | { t: 'h1' | 'h2' | 'h3' | 'h4'; text: string }
  | { t: 'hr' }
  | { t: 'code'; lang: string; code: string }
  | { t: 'ul'; items: string[] }
  | { t: 'ol'; items: string[] }
  | { t: 'p'; text: string };

// ── Trace/state helpers ───────────────────────────────────────────────────

export function fmtRelMs(ms: number): string {
  if (ms < 0) return '+0.0s';
  if (ms < 1000) return `+${(ms / 1000).toFixed(2)}s`;
  return `+${(ms / 1000).toFixed(1)}s`;
}

export function fmtDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const m = Math.floor(ms / 60000);
  const s = ((ms % 60000) / 1000).toFixed(0);
  return `${m}m${s}s`;
}

export function traceLabel(ev: TraceEvent): string {
  switch (ev.type) {
    case 'run_start':       return `Run started — ${ev.goal as string}`;
    case 'iteration_start': {
      const agents = (ev.agents as string[] | undefined);
      return agents?.length
        ? `Iteration ${ev.iteration} — calling ${agents.join(', ')}`
        : `Iteration ${ev.iteration}`;
    }
    case 'tool_start':      return `${(ev.tool as string).replace(/^agent__/, '')} called`;
    case 'tool_done':       return `${(ev.tool as string).replace(/^agent__/, '')} done (${ev.latency_ms}ms)`;
    case 'tool_result':     return `${String(ev.tool || ev.agent || '').replace(/^agent__/, '')} result`;
    case 'usage':           return `Iter ${ev.iteration}: ${ev.input_tokens}↑ ${ev.output_tokens}↓ tokens`;
    case 'run_end':         return `Run ${ev.status} — ${ev.iterations} iter, ${fmtDuration((ev.duration_ms as number) || 0)}`;
    case 'ready':           return `Run started — ${ev.run_id as string}`;
    case 'done':            return 'Run complete';
    case 'agent_status':    return `${ev.agent as string} — ${ev.state as string}`;
    case 'error':           return `Error: ${(ev.message || ev.detail) as string}`;
    default:                return ev.type;
  }
}

export function traceColor(type: string): string {
  if (type === 'error') return '#f87171';
  if (type === 'run_end' || type === 'done') return '#4edea3';
  if (type === 'ready') return '#34d399';
  if (type === 'iteration_start') return '#f59e0b';
  if (type.startsWith('tool')) return '#a78bfa';
  if (type === 'agent_status') return '#c084fc';
  if (type === 'usage') return '#60a5fa';
  return 'var(--tm-text-muted)';
}

export function groupTraceEvents(trace: TraceEvent[]): { preamble: TraceEvent[]; groups: TraceGroup[]; tail: TraceEvent[] } {
  const preamble: TraceEvent[] = [];
  const groups: TraceGroup[] = [];
  const tail: TraceEvent[] = [];
  let current: TraceGroup | null = null;

  for (const ev of trace) {
    if (ev.type === 'ready' || ev.type === 'run_start') {
      preamble.push(ev);
      continue;
    }
    if (ev.type === 'iteration_start') {
      current = {
        iteration: ev.iteration as number,
        startTs: ev.ts,
        agents: (ev.agents as string[] | undefined) || [],
        events: [],
      };
      groups.push(current);
      continue;
    }
    if (ev.type === 'usage' && current) {
      current.usage = ev;
      continue;
    }
    if (ev.type === 'run_end' || ev.type === 'done' || ev.type === 'error') {
      tail.push(ev);
      continue;
    }
    if (current) {
      current.events.push(ev);
    } else {
      preamble.push(ev);
    }
  }
  return { preamble, groups, tail };
}

export function stateColor(state: string): string {
  if (state === 'completed') return '#4edea3';
  if (state === 'failed') return '#f87171';
  if (state === 'working') return '#a78bfa';
  if (state === 'submitted') return '#60a5fa';
  if (state === 'canceled' || state === 'rejected') return '#94a3b8';
  return 'var(--tm-text-muted)';
}
