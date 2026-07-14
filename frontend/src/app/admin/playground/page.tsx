'use client';
import { useEffect, useRef, useState, useCallback } from 'react';
import { useSearchParams } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type OrchestratorFull, type TaskOut, type ArtifactOut, type AgentCard, type ContextSession } from '@/lib/api';

function getBridgeWs(): string {
  if (typeof window === 'undefined') return '';
  if (process.env.NEXT_PUBLIC_BRIDGE_WS_URL) return process.env.NEXT_PUBLIC_BRIDGE_WS_URL;
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.host}`;
}

// ── Types ──────────────────────────────────────────────────────────────────

type FileMsg = { filename: string; media_type: string; text: string };
type ChatMsg = { role: 'user' | 'assistant'; text: string; pending?: boolean; file?: FileMsg };

// Activity bar — one entry per active agent, keyed by slug
type AgentActivity = {
  agent: string;
  state: string;       // latest A2A state string
  elapsed_ms: number;
  displayState: string; // what's currently shown (held for 2s min)
  visibleUntil: number; // timestamp after which we can update displayState
};
type TraceEvent = { ts: number; type: string; [key: string]: unknown };
type RecordingState = 'idle' | 'recording' | 'transcribing';
type DebugTab = 'trace' | 'tasks' | 'artifacts' | 'memory' | 'sessions';

type AgentInvocation = {
  slug: string;
  tool: string;
  startedAt: number;
  endedAt?: number;
  latencyMs?: number;
  endpointUrl?: string;
  agentCard?: AgentCard | null;
  fetchingCard?: boolean;
};

// ── Helpers ────────────────────────────────────────────────────────────────

function traceLabel(ev: TraceEvent): string {
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
    case 'usage':           return `Iter ${ev.iteration}: ${ev.input_tokens}+ ${ev.output_tokens}- tokens`;
    case 'run_end':         return `Run ${ev.status} — ${ev.iterations} iterations`;
    case 'error':           return `Error: ${ev.message}`;
    default:                return ev.type;
  }
}

function traceColor(type: string): string {
  if (type === 'error') return '#f87171';
  if (type === 'run_end') return '#4edea3';
  if (type === 'iteration_start') return '#f59e0b';
  if (type.startsWith('tool')) return '#a78bfa';
  if (type === 'usage') return '#60a5fa';
  return 'var(--tm-text-muted)';
}

function stateColor(state: string): string {
  if (state === 'completed') return '#4edea3';
  if (state === 'failed') return '#f87171';
  if (state === 'working') return '#a78bfa';
  if (state === 'submitted') return '#60a5fa';
  if (state === 'canceled' || state === 'rejected') return '#94a3b8';
  return 'var(--tm-text-muted)';
}

// ── Activity Bar ───────────────────────────────────────────────────────────

function ActivityBar({ activities }: { activities: AgentActivity[] }) {
  const [elapsedTick, setElapsedTick] = useState(0);

  // Tick every second to update elapsed time display
  useEffect(() => {
    if (activities.length === 0) return;
    const t = setInterval(() => setElapsedTick(n => n + 1), 1000);
    return () => clearInterval(t);
  }, [activities.length]);

  if (activities.length === 0) return null;

  return (
    <div style={{
      borderTop: '1px solid var(--tm-border)',
      background: 'var(--tm-surface)',
      padding: '5px 20px',
      display: 'flex',
      flexDirection: 'row',
      flexWrap: 'wrap',
      gap: '4px 20px',
      animation: 'fadeSlideUp 0.2s ease-out',
    }}>
      {activities.map(a => {
        const isTerminal = ['TASK_STATE_COMPLETED', 'completed'].includes(a.displayState);
        const isFailed = ['TASK_STATE_FAILED', 'failed', 'TASK_STATE_CANCELED', 'canceled'].includes(a.displayState);
        const isWorking = !isTerminal && !isFailed;
        const elapsedS = (a.elapsed_ms / 1000).toFixed(1);
        const stateLabel = a.displayState.replace(/^TASK_STATE_/, '').toLowerCase();

        return (
          <div key={a.agent} style={{
            display: 'flex', alignItems: 'center', gap: 6,
            animation: 'fadeSlideUp 0.15s ease-out',
          }}>
            {isWorking ? (
              <span style={{
                display: 'inline-block', width: 8, height: 8,
                border: '1.5px solid #7c3aed', borderTopColor: '#a78bfa',
                borderRadius: '50%', animation: 'spin 0.7s linear infinite', flexShrink: 0,
              }} />
            ) : (
              <span style={{ fontSize: 10, color: isFailed ? '#f87171' : '#4edea3', fontWeight: 700 }}>
                {isFailed ? '✗' : '✓'}
              </span>
            )}
            <span style={{ fontSize: 11, color: isWorking ? '#a78bfa' : isFailed ? '#f87171' : '#4edea3', fontFamily: 'monospace' }}>
              {a.agent}
            </span>
            <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
              {isWorking ? `${stateLabel}…` : stateLabel}
            </span>
            <span style={{ fontSize: 10, color: '#6b7280', fontFamily: 'monospace' }}>
              {elapsedS}s
            </span>
          </div>
        );
      })}
    </div>
  );
}

// ── Markdown renderer ─────────────────────────────────────────────────────
// Hand-rolled — no dependencies. Supports: headings, bold, italic, inline
// code, bullet/numbered lists, code blocks (with copy + Mermaid rendering),
// horizontal rules, and plain paragraphs. Matches Claude Desktop behaviour.

declare global {
  interface Window {
    mermaid?: {
      initialize: (cfg: object) => void;
      render: (id: string, code: string) => Promise<{ svg: string }>;
      _initialized?: boolean;
    };
  }
}

function MermaidBlock({ code }: { code: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function render() {
      // Lazy-load mermaid from CDN once
      if (!window.mermaid) {
        await new Promise<void>((resolve, reject) => {
          const s = document.createElement('script');
          s.src = 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js';
          s.onload = () => resolve();
          s.onerror = reject;
          document.head.appendChild(s);
        });
      }
      if (!window.mermaid!._initialized) {
        window.mermaid!.initialize({ startOnLoad: false, theme: 'dark' });
        window.mermaid!._initialized = true;
      }
      try {
        const id = `mmd-${Math.random().toString(36).slice(2)}`;
        const { svg } = await window.mermaid!.render(id, code);
        if (!cancelled) setSvg(svg);
      } catch (e) {
        if (!cancelled) setErr(String(e));
      }
    }

    render();
    return () => { cancelled = true; };
  }, [code]);

  if (err) return (
    <pre style={{ color: '#f87171', fontSize: 12, whiteSpace: 'pre-wrap', margin: '8px 0' }}>{err}</pre>
  );
  if (!svg) return (
    <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: '8px 0' }}>Rendering diagram…</div>
  );
  return (
    <div
      dangerouslySetInnerHTML={{ __html: svg }}
      style={{ overflowX: 'auto', margin: '8px 0', lineHeight: 1 }}
    />
  );
}

function CodeBlock({ lang, code }: { lang: string; code: string }) {
  const [copied, setCopied] = useState(false);

  if (lang === 'mermaid') return <MermaidBlock code={code} />;

  return (
    <div style={{
      position: 'relative',
      background: 'rgba(0,0,0,0.35)',
      border: '1px solid var(--tm-border)',
      borderRadius: 8,
      margin: '8px 0',
      overflow: 'hidden',
    }}>
      {lang && (
        <div style={{
          padding: '4px 12px',
          fontSize: 11,
          fontFamily: 'monospace',
          color: 'var(--tm-text-muted)',
          borderBottom: '1px solid var(--tm-border)',
          background: 'rgba(0,0,0,0.2)',
        }}>{lang}</div>
      )}
      <button
        onClick={() => { navigator.clipboard.writeText(code); setCopied(true); setTimeout(() => setCopied(false), 2000); }}
        style={{
          position: 'absolute', top: lang ? 28 : 6, right: 8,
          padding: '2px 8px', fontSize: 11, borderRadius: 4, border: '1px solid var(--tm-border)',
          background: 'rgba(0,0,0,0.4)', color: 'var(--tm-text-muted)', cursor: 'pointer',
        }}
      >{copied ? 'Copied!' : 'Copy'}</button>
      <pre style={{
        margin: 0, padding: '10px 12px',
        fontSize: 12, fontFamily: 'monospace',
        color: 'var(--tm-text)', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
        overflowX: 'auto',
      }}>{code}</pre>
    </div>
  );
}

// Split raw text into typed segments for inline rendering
type Segment = { t: 'bold'; v: string } | { t: 'italic'; v: string } | { t: 'code'; v: string } | { t: 'text'; v: string };

function parseInline(text: string): Segment[] {
  const out: Segment[] = [];
  const re = /(\*\*(.+?)\*\*|__(.+?)__|(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)|_(.+?)_|`([^`]+)`)/g;
  let last = 0, m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push({ t: 'text', v: text.slice(last, m.index) });
    if (m[2] || m[3]) out.push({ t: 'bold',   v: m[2] || m[3] });
    else if (m[4] || m[5]) out.push({ t: 'italic', v: m[4] || m[5] });
    else if (m[6]) out.push({ t: 'code', v: m[6] });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ t: 'text', v: text.slice(last) });
  return out;
}

function InlineText({ text }: { text: string }) {
  const segs = parseInline(text);
  return (
    <>
      {segs.map((s, i) => {
        if (s.t === 'bold')   return <strong key={i}>{s.v}</strong>;
        if (s.t === 'italic') return <em key={i}>{s.v}</em>;
        if (s.t === 'code')   return (
          <code key={i} style={{
            fontFamily: 'monospace', fontSize: '0.85em',
            background: 'rgba(255,255,255,0.08)', borderRadius: 4, padding: '1px 5px',
          }}>{s.v}</code>
        );
        return <span key={i}>{s.v}</span>;
      })}
    </>
  );
}

type Block =
  | { t: 'h1' | 'h2' | 'h3' | 'h4'; text: string }
  | { t: 'hr' }
  | { t: 'code'; lang: string; code: string }
  | { t: 'ul'; items: string[] }
  | { t: 'ol'; items: string[] }
  | { t: 'p'; text: string };

function parseBlocks(raw: string): Block[] {
  const lines = raw.split('\n');
  const blocks: Block[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block
    if (line.startsWith('```')) {
      const lang = line.slice(3).trim();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith('```')) {
        codeLines.push(lines[i]);
        i++;
      }
      i++; // consume closing ```
      blocks.push({ t: 'code', lang, code: codeLines.join('\n') });
      continue;
    }

    // HR
    if (/^(\*{3,}|-{3,}|_{3,})\s*$/.test(line)) {
      blocks.push({ t: 'hr' });
      i++; continue;
    }

    // Headings
    const h4 = line.match(/^####\s+(.*)/); if (h4) { blocks.push({ t: 'h4', text: h4[1] }); i++; continue; }
    const h3 = line.match(/^###\s+(.*)/);  if (h3) { blocks.push({ t: 'h3', text: h3[1] }); i++; continue; }
    const h2 = line.match(/^##\s+(.*)/);   if (h2) { blocks.push({ t: 'h2', text: h2[1] }); i++; continue; }
    const h1 = line.match(/^#\s+(.*)/);    if (h1) { blocks.push({ t: 'h1', text: h1[1] }); i++; continue; }

    // Unordered list — collect consecutive list items
    if (/^[-*+]\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^[-*+]\s/.test(lines[i])) {
        items.push(lines[i].replace(/^[-*+]\s+/, ''));
        i++;
      }
      blocks.push({ t: 'ul', items });
      continue;
    }

    // Ordered list
    if (/^\d+\.\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\d+\.\s/.test(lines[i])) {
        items.push(lines[i].replace(/^\d+\.\s+/, ''));
        i++;
      }
      blocks.push({ t: 'ol', items });
      continue;
    }

    // Blank line — skip
    if (line.trim() === '') { i++; continue; }

    // Paragraph — collect until blank line or block-level element
    const paraLines: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !/^(#{1,4}\s|[-*+]\s|\d+\.\s|```|(\*{3,}|-{3,}|_{3,})\s*$)/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    if (paraLines.length) blocks.push({ t: 'p', text: paraLines.join(' ') });
  }

  return blocks;
}

function MarkdownText({ text }: { text: string }) {
  const blocks = parseBlocks(text);

  return (
    <div style={{ fontSize: 14, lineHeight: 1.65, color: 'var(--tm-text)' }}>
      {blocks.map((b, i) => {
        switch (b.t) {
          case 'h1': return <h1 key={i} style={{ fontSize: 20, fontWeight: 700, margin: '16px 0 6px', color: 'var(--tm-text)' }}><InlineText text={b.text} /></h1>;
          case 'h2': return <h2 key={i} style={{ fontSize: 17, fontWeight: 700, margin: '14px 0 5px', color: 'var(--tm-text)', borderBottom: '1px solid var(--tm-border)', paddingBottom: 4 }}><InlineText text={b.text} /></h2>;
          case 'h3': return <h3 key={i} style={{ fontSize: 15, fontWeight: 600, margin: '12px 0 4px', color: 'var(--tm-text)' }}><InlineText text={b.text} /></h3>;
          case 'h4': return <h4 key={i} style={{ fontSize: 14, fontWeight: 600, margin: '10px 0 3px', color: 'var(--tm-text-muted)' }}><InlineText text={b.text} /></h4>;
          case 'hr': return <hr key={i} style={{ border: 'none', borderTop: '1px solid var(--tm-border)', margin: '14px 0' }} />;
          case 'code': return <CodeBlock key={i} lang={b.lang} code={b.code} />;
          case 'ul': return (
            <ul key={i} style={{ margin: '6px 0', paddingLeft: 20 }}>
              {b.items.map((item, j) => (
                <li key={j} style={{ margin: '3px 0' }}><InlineText text={item} /></li>
              ))}
            </ul>
          );
          case 'ol': return (
            <ol key={i} style={{ margin: '6px 0', paddingLeft: 20 }}>
              {b.items.map((item, j) => (
                <li key={j} style={{ margin: '3px 0' }}><InlineText text={item} /></li>
              ))}
            </ol>
          );
          case 'p': return <p key={i} style={{ margin: '6px 0' }}><InlineText text={b.text} /></p>;
        }
      })}
    </div>
  );
}

// ── Debug Panel: right tray ────────────────────────────────────────────────

function TabBtn({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      style={{
        padding: '6px 12px',
        borderRadius: 6,
        border: 'none',
        background: active ? '#7c3aed' : 'transparent',
        color: active ? '#fff' : 'var(--tm-text-muted)',
        fontSize: 11,
        fontWeight: 600,
        cursor: 'pointer',
        letterSpacing: '0.05em',
      }}
    >
      {label}
    </button>
  );
}

// Parse a value that might be a JSON string or already an object
function tryParseJson(v: unknown): unknown {
  if (typeof v === 'string') {
    try { return JSON.parse(v); } catch { return v; }
  }
  return v;
}

// Render a tool input/output in a human-readable way
function TracePayload({ value, label }: { value: unknown; label: string }) {
  const [expanded, setExpanded] = useState(false);
  const parsed = tryParseJson(value);

  // Build a summary line from known fields
  function summarize(obj: unknown): string {
    if (typeof obj !== 'object' || obj === null) return String(obj).slice(0, 120);
    const o = obj as Record<string, unknown>;
    const parts: string[] = [];
    if (o.main_point)    parts.push(`"${String(o.main_point).slice(0, 80)}"`);
    if (o.question)      parts.push(`Q: ${String(o.question).slice(0, 80)}`);
    if (o.winner)        parts.push(`winner: ${o.winner}`);
    if (o.winner_reason) parts.push(`reason: ${String(o.winner_reason).slice(0, 60)}`);
    if (o.field)         parts.push(`field: ${o.field}`);
    if (o.approach)      parts.push(`approach: ${o.approach}`);
    if (o.confidence != null) parts.push(`confidence: ${o.confidence}`);
    if (o.agent)         parts.push(`agent: ${o.agent}`);
    if (o.round != null) parts.push(`round: ${o.round}`);
    if (o.position)      parts.push(`position: ${o.position}`);
    if (o.message && typeof o.message === 'string') parts.push(`"${o.message.slice(0, 80)}"`);
    if (parts.length === 0) {
      const keys = Object.keys(o).slice(0, 4);
      for (const k of keys) {
        const v = o[k];
        if (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean')
          parts.push(`${k}: ${String(v).slice(0, 40)}`);
      }
    }
    return parts.join(' · ') || JSON.stringify(obj).slice(0, 120);
  }

  const summary = summarize(parsed);
  const fullText = typeof parsed === 'string' ? parsed : JSON.stringify(parsed, null, 2);

  return (
    <div style={{ paddingLeft: 12, marginTop: 2 }}>
      <div
        onClick={() => setExpanded(e => !e)}
        style={{ cursor: 'pointer', display: 'flex', alignItems: 'flex-start', gap: 6 }}
      >
        <span style={{ fontSize: 10, color: '#6b7280', flexShrink: 0, marginTop: 1 }}>
          {expanded ? '▾' : '▸'} {label}
        </span>
        {!expanded && (
          <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontStyle: 'italic', wordBreak: 'break-word' }}>
            {summary}
          </span>
        )}
      </div>
      {expanded && (
        <pre style={{
          margin: '4px 0 0 16px', fontSize: 11, fontFamily: 'monospace',
          color: 'var(--tm-text-muted)', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
          background: 'rgba(0,0,0,0.2)', borderRadius: 6, padding: '6px 8px',
          maxHeight: 300, overflowY: 'auto',
        }}>
          {fullText}
        </pre>
      )}
    </div>
  );
}

function TraceTab({ trace, traceBottom, runId, contextId }: { trace: TraceEvent[]; traceBottom: React.RefObject<HTMLDivElement | null>; runId?: string | null; contextId?: string | null }) {
  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 6 }}>
      {(runId || contextId) && (
        <div style={{ fontSize: 10, fontFamily: 'monospace', color: 'var(--tm-text-muted)', background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: 4, padding: '6px 8px', display: 'flex', flexDirection: 'column', gap: 2, marginBottom: 4 }}>
          {runId && <span title={runId}><span style={{ opacity: 0.6 }}>run_id: </span>{runId}</span>}
          {contextId && <span title={contextId}><span style={{ opacity: 0.6 }}>ctx_id: </span>{contextId}</span>}
          {contextId && (
            <a
              href={`/temporal/namespaces/default/workflows/ctx-${contextId}`}
              target="_blank"
              rel="noopener noreferrer"
              style={{ color: 'var(--tm-accent, #6ee7b7)', textDecoration: 'none', opacity: 0.85 }}
              onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
              onMouseLeave={e => (e.currentTarget.style.opacity = '0.85')}
            >
              ↗ Temporal workflow
            </a>
          )}
        </div>
      )}
      {trace.length === 0 && (
        <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, textAlign: 'center', marginTop: 40 }}>
          Trace events appear here when a run starts
        </div>
      )}
      {trace.map((ev, i) => (
        <div key={i} style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          <div style={{ fontSize: 12, color: traceColor(ev.type), fontWeight: 500, fontFamily: 'monospace' }}>
            {traceLabel(ev)}
          </div>
          {ev.type === 'tool_start' && ev.input != null && (
            <TracePayload value={ev.input} label="input" />
          )}
          {ev.type === 'tool_done' && ev.output != null && (
            <TracePayload value={ev.output} label="output" />
          )}
        </div>
      ))}
      <div ref={traceBottom} />
    </div>
  );
}

function TasksTab({ runId }: { runId: string | null }) {
  const [tasks, setTasks] = useState<TaskOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    if (!runId) { setTasks([]); return; }
    setLoading(true);
    setErr('');
    themApi.runTasks(runId)
      .then(setTasks)
      .catch(e => setErr(e.message))
      .finally(() => setLoading(false));
  }, [runId]);

  if (!runId) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, textAlign: 'center', marginTop: 40, padding: 16 }}>Start a run to see the task graph</div>;
  if (loading) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>Loading…</div>;
  if (err) return <div style={{ color: '#f87171', fontSize: 12, padding: 16 }}>{err}</div>;
  if (tasks.length === 0) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>No tasks yet</div>;

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
      {tasks.map(t => (
        <div key={t.id} style={{
          padding: '10px 12px',
          borderRadius: 8,
          border: '1px solid var(--tm-border)',
          background: 'var(--tm-surface)',
          marginLeft: t.parent_task_id ? 16 : 0,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <span style={{ fontSize: 10, padding: '2px 6px', borderRadius: 4, background: stateColor(t.state), color: '#fff', fontWeight: 700 }}>
              {t.state}
            </span>
            <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>
              {t.kind}
            </span>
            {t.tokens_used > 0 && (
              <span style={{ fontSize: 11, color: '#60a5fa', marginLeft: 'auto' }}>{t.tokens_used} tok</span>
            )}
          </div>
          <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>
            {t.id.slice(0, 8)}…
          </div>
          {t.error && (
            <div style={{ fontSize: 11, color: '#f87171', marginTop: 4, wordBreak: 'break-word' }}>{t.error}</div>
          )}
          {t.remote_task_id && (
            <div style={{ fontSize: 10, color: 'var(--tm-text-muted)', marginTop: 2 }}>remote: {t.remote_task_id}</div>
          )}
        </div>
      ))}
    </div>
  );
}

function ArtifactsTab({ runId }: { runId: string | null }) {
  const [artifacts, setArtifacts] = useState<ArtifactOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (!runId) { setArtifacts([]); return; }
    setLoading(true);
    setErr('');
    themApi.runArtifacts(runId)
      .then(setArtifacts)
      .catch(e => setErr(e.message))
      .finally(() => setLoading(false));
  }, [runId]);

  const toggle = (id: string) => setExpanded(prev => {
    const next = new Set(prev);
    next.has(id) ? next.delete(id) : next.add(id);
    return next;
  });

  if (!runId) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, textAlign: 'center', marginTop: 40, padding: 16 }}>Start a run to see artifacts</div>;
  if (loading) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>Loading…</div>;
  if (err) return <div style={{ color: '#f87171', fontSize: 12, padding: 16 }}>{err}</div>;
  if (artifacts.length === 0) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>No artifacts yet</div>;

  const downloadPart = (text: string, filename: string, mediaType: string) => {
    const blob = new Blob([text], { type: mediaType });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
      {artifacts.map(a => {
        const isOpen = expanded.has(a.id);
        const filePart = a.parts.find(p => p.filename || p.media_type || p.mediaType);
        // Resolve media_type from either casing (old DB records use camelCase mediaType)
        const resolvedMediaType = filePart?.media_type || filePart?.mediaType;
        const isHtml = resolvedMediaType === 'text/html';
        const isMarkdown = resolvedMediaType === 'text/markdown';
        const textParts = a.parts.filter(p => !p.filename && !p.media_type && !p.mediaType && p.text !== undefined);

        return (
          <div key={a.id} style={{ border: '1px solid var(--tm-border)', borderRadius: 8, background: 'var(--tm-surface)', overflow: 'hidden' }}>
            <button
              onClick={() => toggle(a.id)}
              style={{ width: '100%', padding: '8px 12px', background: 'transparent', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8, textAlign: 'left' }}
            >
              <span style={{ fontSize: 11, color: '#a78bfa', fontWeight: 600 }}>{a.artifact_id}</span>
              {filePart?.filename
                ? <span style={{ fontSize: 11, color: '#4edea3', fontWeight: 600 }}>{filePart.filename}</span>
                : a.name && <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>{a.name}</span>
              }
              {resolvedMediaType && (
                <span style={{ fontSize: 10, color: 'var(--tm-text-muted)', padding: '1px 6px', border: '1px solid var(--tm-border)', borderRadius: 4 }}>{resolvedMediaType}</span>
              )}
              <span style={{ fontSize: 10, color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{isOpen ? '▲' : '▼'}</span>
            </button>

            {isOpen && (
              <div style={{ borderTop: '1px solid var(--tm-border)' }}>
                {/* HTML artifact — inline iframe */}
                {isHtml && filePart?.text && (
                  <div>
                    <div style={{ padding: '6px 12px', display: 'flex', justifyContent: 'flex-end', borderBottom: '1px solid var(--tm-border)' }}>
                      <button
                        onClick={() => downloadPart(filePart.text!, filePart.filename!, resolvedMediaType!)}
                        style={{ fontSize: 11, padding: '3px 10px', borderRadius: 5, border: '1px solid var(--tm-border)', background: 'transparent', color: '#a78bfa', cursor: 'pointer' }}
                      >
                        Download
                      </button>
                    </div>
                    <iframe
                      srcDoc={filePart.text}
                      style={{ width: '100%', height: 500, border: 'none', display: 'block' }}
                      sandbox="allow-scripts allow-same-origin"
                      title={filePart.filename || 'preview'}
                    />
                  </div>
                )}

                {/* Markdown / slides — styled pre with download */}
                {isMarkdown && filePart?.text && (
                  <div style={{ padding: '0 12px 10px' }}>
                    <div style={{ display: 'flex', justifyContent: 'flex-end', padding: '6px 0' }}>
                      <button
                        onClick={() => downloadPart(filePart.text!, filePart.filename!, resolvedMediaType!)}
                        style={{ fontSize: 11, padding: '3px 10px', borderRadius: 5, border: '1px solid var(--tm-border)', background: 'transparent', color: '#a78bfa', cursor: 'pointer' }}
                      >
                        Download
                      </button>
                    </div>
                    <pre style={{ fontSize: 12, color: 'var(--tm-text)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, maxHeight: 400, overflowY: 'auto' }}>
                      {filePart.text}
                    </pre>
                  </div>
                )}

                {/* Plain text parts */}
                {!filePart && (
                  <div style={{ padding: '0 12px 10px' }}>
                    {textParts.map((p, i) => (
                      <pre key={i} style={{ fontSize: 11, color: 'var(--tm-text)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: '8px 0 0' }}>
                        {p.text}
                      </pre>
                    ))}
                    {textParts.length === 0 && (
                      <pre style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', margin: '8px 0 0' }}>
                        {JSON.stringify(a.parts, null, 2)}
                      </pre>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function MemoryTab({ contextId, agentInvocations, allAgents }: {
  contextId: string | null;
  agentInvocations: AgentInvocation[];
  allAgents: OrchestratorFull['allowed_agent_ids'] | null;
}) {
  const [artifacts, setArtifacts] = useState<ArtifactOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');
  const [agentCards, setAgentCards] = useState<Record<string, { card?: AgentCard; fetching?: boolean; error?: string }>>({});

  useEffect(() => {
    if (!contextId) { setArtifacts([]); return; }
    setLoading(true);
    setErr('');
    themApi.contextArtifacts(contextId)
      .then(setArtifacts)
      .catch(e => setErr(e.message))
      .finally(() => setLoading(false));
  }, [contextId]);

  const fetchCard = async (inv: AgentInvocation) => {
    if (!inv.endpointUrl) return;
    const key = inv.slug;
    setAgentCards(prev => ({ ...prev, [key]: { fetching: true } }));
    try {
      const card = await themApi.fetchAgentCard(inv.endpointUrl!);
      setAgentCards(prev => ({ ...prev, [key]: { card } }));
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      setAgentCards(prev => ({ ...prev, [key]: { error: msg } }));
    }
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 12 }}>
      {/* Agent invocations with Fetch Agent Card button */}
      {agentInvocations.length > 0 && (
        <div>
          <div style={{ fontSize: 10, fontWeight: 700, color: 'var(--tm-text-muted)', letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 8 }}>Agents Used</div>
          {agentInvocations.map((inv, i) => {
            const cardState = agentCards[inv.slug];
            return (
              <div key={i} style={{ padding: '10px 12px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', marginBottom: 8 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                  <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text)' }}>{inv.slug}</span>
                  {inv.latencyMs != null && (
                    <span style={{ fontSize: 10, color: 'var(--tm-text-muted)' }}>{inv.latencyMs}ms</span>
                  )}
                  <button
                    onClick={() => fetchCard(inv)}
                    disabled={cardState?.fetching}
                    style={{
                      marginLeft: 'auto', padding: '3px 8px', borderRadius: 5, border: '1px solid var(--tm-border)',
                      background: 'transparent', color: '#a78bfa', fontSize: 10, cursor: 'pointer', fontWeight: 600,
                    }}
                  >
                    {cardState?.fetching ? 'Fetching…' : 'Fetch Agent Card'}
                  </button>
                </div>
                {cardState?.error && (
                  <div style={{ fontSize: 11, color: '#f87171' }}>{cardState.error}</div>
                )}
                {cardState?.card && (
                  <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>
                    <div style={{ color: 'var(--tm-text)', fontWeight: 600, marginBottom: 4 }}>{cardState.card.name} {cardState.card.version && `v${cardState.card.version}`}</div>
                    {cardState.card.description && <div style={{ marginBottom: 4 }}>{cardState.card.description}</div>}
                    {cardState.card.capabilities && (
                      <div style={{ marginBottom: 4 }}>
                        streaming: {cardState.card.capabilities.streaming ? 'yes' : 'no'}
                        {' · '}
                        push: {cardState.card.capabilities.pushNotifications ? 'yes' : 'no'}
                      </div>
                    )}
                    {cardState.card.skills && cardState.card.skills.length > 0 && (
                      <div>Skills: {cardState.card.skills.map(s => s.name).join(', ')}</div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* Context artifacts */}
      <div>
        <div style={{ fontSize: 10, fontWeight: 700, color: 'var(--tm-text-muted)', letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 8 }}>
          Context Memory {contextId && <span style={{ fontWeight: 400, textTransform: 'none', marginLeft: 4 }}>{contextId.slice(0, 8)}…</span>}
        </div>
        {!contextId && <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>Start a run to inspect memory</div>}
        {loading && <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>Loading…</div>}
        {err && <div style={{ color: '#f87171', fontSize: 12 }}>{err}</div>}
        {artifacts.map(a => (
          <div key={a.id} style={{ padding: '8px 10px', borderRadius: 6, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', marginBottom: 6, fontSize: 11 }}>
            <div style={{ color: '#a78bfa', fontWeight: 600, marginBottom: 4 }}>{a.name || a.artifact_id}</div>
            {a.parts.filter(p => p.text).map((p, i) => (
              <div key={i} style={{ color: 'var(--tm-text)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                {String(p.text).slice(0, 400)}{String(p.text).length > 400 ? '…' : ''}
              </div>
            ))}
          </div>
        ))}
        {contextId && !loading && artifacts.length === 0 && !err && (
          <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>No context artifacts yet</div>
        )}
      </div>
    </div>
  );
}

// ── Sessions tab ──────────────────────────────────────────────────────────
function SessionsTab({ onResume, currentContextId }: {
  onResume: (session: ContextSession) => void;
  currentContextId: string | null;
}) {
  const [sessions, setSessions] = useState<ContextSession[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    themApi.contexts().then(s => { setSessions(s); setLoading(false); }).catch(() => setLoading(false));
  }, [currentContextId]);

  const fmt = (iso: string) => {
    const d = new Date(iso);
    const now = new Date();
    const diff = now.getTime() - d.getTime();
    if (diff < 60000) return 'just now';
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
    return d.toLocaleDateString();
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '10px 12px' }}>
      {loading && <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>Loading…</div>}
      {!loading && sessions.length === 0 && (
        <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>No past sessions yet</div>
      )}
      {sessions.map(s => {
        const isCurrent = s.context_id === currentContextId;
        return (
          <div key={s.context_id} style={{
            padding: '8px 10px', borderRadius: 8, marginBottom: 6,
            border: `1px solid ${isCurrent ? '#7c3aed' : 'var(--tm-border)'}`,
            background: isCurrent ? 'rgba(124,58,237,0.08)' : 'var(--tm-surface)',
            cursor: 'pointer',
          }} onClick={() => !isCurrent && onResume(s)}>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text)', marginBottom: 3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {s.title}
            </div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontSize: 11, color: '#a78bfa' }}>{s.orchestrator_name}</span>
              <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>{s.turn_count} turn{s.turn_count !== 1 ? 's' : ''}</span>
              <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{fmt(s.last_active)}</span>
            </div>
            {isCurrent && (
              <div style={{ fontSize: 10, color: '#7c3aed', marginTop: 3 }}>current session</div>
            )}
          </div>
        );
      })}
    </div>
  );
}

// ── Mic SVG icon ───────────────────────────────────────────────────────────
function MicIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
      <path d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.93V21h2v-3.07A7 7 0 0 0 19 11h-2z"/>
    </svg>
  );
}

// ── Spinner ────────────────────────────────────────────────────────────────
function Spinner() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
      <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" style={{ animation: 'spin 1s linear infinite', transformOrigin: 'center' }} />
    </svg>
  );
}

// ── Component ──────────────────────────────────────────────────────────────

export default function PlaygroundPage() {
  const searchParams = useSearchParams();
  const initialOrch = searchParams.get('orchestrator') || '';

  const [orchestrators, setOrchestrators] = useState<OrchestratorFull[]>([]);
  const [webrtcSlug, setWebrtcSlug] = useState<string | null>(null);
  const [selectedOrch, setSelectedOrch] = useState(initialOrch);
  const [messages, setMessages] = useState<ChatMsg[]>([]);
  const [trace, setTrace] = useState<TraceEvent[]>([]);
  const [input, setInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [voiceEnabled, setVoiceEnabled] = useState(false);
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [speaking, setSpeaking] = useState(false);
  const [recordingState, setRecordingState] = useState<RecordingState>('idle');
  const [mediaRecorder, setMediaRecorder] = useState<MediaRecorder | null>(null);
  const [debugTab, setDebugTab] = useState<DebugTab>('trace');
  const [agentInvocations, setAgentInvocations] = useState<AgentInvocation[]>([]);
  const [contextId, setContextId] = useState<string | null>(null);
  const [restoredSession, setRestoredSession] = useState<ContextSession | null>(null);
  const [activities, setActivities] = useState<AgentActivity[]>([]);
  const activitiesRef = useRef<AgentActivity[]>([]);

  const chatWs = useRef<WebSocket | null>(null);
  const dashWs = useRef<WebSocket | null>(null);
  const runId = useRef<string | null>(null);
  const assistantBuf = useRef('');
  const chatBottom = useRef<HTMLDivElement>(null);
  const traceBottom = useRef<HTMLDivElement | null>(null);

  // Restore last session from localStorage on mount
  useEffect(() => {
    const saved = localStorage.getItem('them:playground:context_id');
    if (!saved) return;
    // Verify the session still exists and fetch its metadata
    themApi.contexts().then(sessions => {
      const match = sessions.find(s => s.context_id === saved);
      if (match) setRestoredSession(match);
      else localStorage.removeItem('them:playground:context_id');
    }).catch(() => {});
  }, []);

  // Persist context_id to localStorage whenever it changes
  useEffect(() => {
    if (contextId) localStorage.setItem('them:playground:context_id', contextId);
  }, [contextId]);

  // Load orchestrators list, then check voice_enabled for the selected one
  useEffect(() => {
    themApi.orchestrators().then(list => {
      const enabled = list.filter(o => o.enabled);
      setOrchestrators(enabled);
      const name = initialOrch || (enabled.length > 0 ? enabled[0].name : '');
      if (!initialOrch && enabled.length > 0) setSelectedOrch(enabled[0].name);
      const orch = enabled.find(o => o.name === name);
      setVoiceEnabled(orch?.voice_enabled ?? false);
      setTtsEnabled(orch?.tts_enabled ?? false);
    });
  }, [initialOrch]);

  // When selected orchestrator changes, update voice_enabled + tts_enabled
  useEffect(() => {
    if (!selectedOrch) return;
    const orch = orchestrators.find(o => o.name === selectedOrch);
    setVoiceEnabled(orch?.voice_enabled ?? false);
    setTtsEnabled(orch?.tts_enabled ?? false);
  }, [selectedOrch, orchestrators]);

  // Find a WebRTC app bound to the selected orchestrator
  useEffect(() => {
    if (!selectedOrch) { setWebrtcSlug(null); return; }
    themApi.applications().then(apps => {
      for (const a of apps) {
        if (!a.enabled || a.orchestrator_name !== selectedOrch) continue;
        const webrtcEp = a.entry_points?.find((ep: any) => ep.entry_point_type === 'webrtc' && ep.enabled);
        if (webrtcEp) { setWebrtcSlug(webrtcEp.slug); return; }
      }
      setWebrtcSlug(null);
    }).catch(() => setWebrtcSlug(null));
  }, [selectedOrch]);

  useEffect(() => {
    chatBottom.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  useEffect(() => {
    traceBottom.current?.scrollIntoView({ behavior: 'smooth' });
  }, [trace]);

  // ── Dashboard WS for trace ───────────────────────────────────────────────
  const openDashWs = useCallback(async (rid: string) => {
    const r = await fetch('/api/auth/token');
    if (!r.ok) return;
    const { token } = await r.json();

    const ws = new WebSocket(`${getBridgeWs()}/ws/dashboard?token=${encodeURIComponent(token)}`);
    dashWs.current = ws;

    ws.onopen = () => {
      ws.send(JSON.stringify({ type: 'subscribe', channels: [`run:${rid}`] }));
    };

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'ping') return;
        if (msg.channel?.startsWith('run:')) {
          setTrace(prev => [...prev, { ts: Date.now(), ...msg.event }]);
        }
      } catch {}
    };

    ws.onerror = () => ws.close();
  }, []);

  // ── Send message ─────────────────────────────────────────────────────────
  const sendText = useCallback(async (text: string, currentContextId?: string | null) => {
    if (!text.trim() || !selectedOrch || busy) return;
    setInput('');
    setBusy(true);
    setTrace([]);
    setAgentInvocations([]);
    setMessages(prev => [...prev, { role: 'user', text }]);

    const r = await fetch('/api/auth/token');
    if (!r.ok) { setBusy(false); return; }
    const { token } = await r.json();

    const ws = new WebSocket(`${getBridgeWs()}/ws/orchestrate/${selectedOrch}?token=${encodeURIComponent(token)}`);
    chatWs.current = ws;
    assistantBuf.current = '';

    ws.onopen = () => {
      setStatus('Connected');
      const payload: Record<string, string> = { type: 'message', content: text };
      if (currentContextId) payload.context_id = currentContextId;
      ws.send(JSON.stringify(payload));
    };

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'ready') {
          runId.current = msg.run_id;
          if (msg.context_id) setContextId(msg.context_id as string);
          openDashWs(msg.run_id);
          setMessages(prev => [...prev, { role: 'assistant', text: '', pending: true }]);
          setStatus(`Run ${(msg.run_id as string).slice(0, 8)}…`);

        } else if (msg.type === 'token') {
          assistantBuf.current += msg.text || '';
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant') {
              copy[copy.length - 1] = { ...last, text: assistantBuf.current };
            }
            return copy;
          });

        } else if (msg.type === 'agent_status') {
          const agent = msg.agent as string;
          const state = msg.state as string;
          const elapsed_ms = msg.elapsed_ms as number;
          const now = Date.now();
          const HOLD_MS = 2000;

          setActivities(prev => {
            const existing = prev.find(a => a.agent === agent);
            if (!existing) {
              // New agent — add row, show state immediately
              const next = [...prev, { agent, state, elapsed_ms, displayState: state, visibleUntil: now + HOLD_MS }];
              activitiesRef.current = next;
              return next;
            }
            // Existing — respect 2s hold before updating displayState
            const displayState = now >= existing.visibleUntil ? state : existing.displayState;
            const visibleUntil = now >= existing.visibleUntil ? now + HOLD_MS : existing.visibleUntil;
            const next = prev.map(a => a.agent === agent ? { ...a, state, elapsed_ms, displayState, visibleUntil } : a);
            activitiesRef.current = next;
            return next;
          });

        } else if (msg.type === 'iteration_start') {
          const agents = (msg.agents as string[] | undefined) ?? [];
          setStatus(agents.length > 1
            ? `Iter ${msg.iteration} — waiting for ${agents.join(', ')}…`
            : agents.length === 1
              ? `Iter ${msg.iteration} — calling ${agents[0]}…`
              : `Iteration ${msg.iteration}…`
          );

        } else if (msg.type === 'tool_start') {
          const slug = (msg.tool as string).replace(/^agent__/, '');
          setStatus(`Calling ${slug}…`);
          setAgentInvocations(prev => {
            const existing = prev.find(a => a.tool === msg.tool);
            if (existing) return prev;
            return [...prev, { slug, tool: msg.tool as string, startedAt: Date.now() }];
          });

        } else if (msg.type === 'tool_done') {
          const slug = (msg.tool as string).replace(/^agent__/, '');
          setAgentInvocations(prev => {
            const updated = prev.map(a =>
              a.tool === msg.tool
                ? { ...a, endedAt: Date.now(), latencyMs: msg.latency_ms as number ?? Date.now() - a.startedAt }
                : a
            );
            const stillRunning = updated.filter(a => !a.endedAt).map(a => a.slug);
            if (stillRunning.length > 0) {
              setStatus(`${slug} done — waiting for ${stillRunning.join(', ')}…`);
            } else {
              setStatus(`${slug} done`);
            }
            return updated;
          });

        } else if (msg.type === 'file') {
          // File artifact from A2A agent — inject as a separate chat bubble with download
          const fm: FileMsg = {
            filename: msg.filename as string,
            media_type: msg.media_type as string,
            text: msg.text as string ?? '',
          };
          setMessages(prev => {
            // Close any pending assistant bubble first
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant' && last.pending) {
              copy[copy.length - 1] = { ...last, pending: false };
            }
            return [...copy, { role: 'assistant', text: '', file: fm }];
          });

        } else if (msg.type === 'done') {
          // Fade out activity bar after brief delay so user sees final states
          setTimeout(() => { setActivities([]); activitiesRef.current = []; }, 1500);
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant' && !last.file) copy[copy.length - 1] = { ...last, pending: false };
            return copy;
          });
          // TTS — stream audio chunks via MediaSource for low-latency playback
          if (ttsEnabled && assistantBuf.current) {
            const textToSpeak = assistantBuf.current;
            const orchName = selectedOrch;
            setSpeaking(true);
            themApi.tts(orchName, textToSpeak)
              .then(async res => {
                if (!res.body) throw new Error('no body');
                // MediaSource lets us start playing before all bytes arrive
                const ms = new MediaSource();
                const url = URL.createObjectURL(ms);
                const audio = new Audio(url);
                audio.onended = () => { setSpeaking(false); URL.revokeObjectURL(url); };
                audio.onerror = () => { setSpeaking(false); URL.revokeObjectURL(url); };

                await new Promise<void>((resolve) => { ms.addEventListener('sourceopen', () => resolve(), { once: true }); });
                audio.play();

                const sb = ms.addSourceBuffer('audio/mpeg');
                const reader = res.body.getReader();

                const pump = async () => {
                  while (true) {
                    const { done, value } = await reader.read();
                    if (done) { ms.endOfStream(); break; }
                    // Wait for sourceBuffer to be ready before appending
                    await new Promise<void>(r => {
                      if (!sb.updating) { r(); return; }
                      sb.addEventListener('updateend', () => r(), { once: true });
                    });
                    sb.appendBuffer(value);
                  }
                };
                pump().catch(() => { setSpeaking(false); ms.endOfStream(); });
              })
              .catch(() => setSpeaking(false));
          }
          setStatus(`Done — ${msg.iterations} iteration(s)`);
          setBusy(false);
          ws.close();
          dashWs.current?.close();

        } else if (msg.type === 'canceled') {
          setActivities([]); activitiesRef.current = [];
          setBusy(false);
          setStatus('Canceled');
          ws.close();
          dashWs.current?.close();

        } else if (msg.type === 'error') {
          setActivities([]); activitiesRef.current = [];
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant' && last.pending) {
              copy[copy.length - 1] = { role: 'assistant', text: `Error: ${msg.message}`, pending: false };
            } else {
              copy.push({ role: 'assistant', text: `Error: ${msg.message}` });
            }
            return copy;
          });
          setStatus(`Error: ${msg.message}`);
          setBusy(false);
          ws.close();
        }
      } catch {}
    };

    ws.onerror = (err) => {
      console.error('Orchestrator WS error', err);
      setStatus('WebSocket error — check console');
      setBusy(false);
    };

    ws.onclose = (ev) => {
      console.log('Orchestrator WS closed', ev.code, ev.reason);
      if (busy) setBusy(false);
    };
  }, [selectedOrch, busy, openDashWs]);

  const send = useCallback(() => sendText(input, contextId), [input, contextId, sendText]);

  const stopRun = useCallback(() => {
    if (chatWs.current && chatWs.current.readyState === WebSocket.OPEN) {
      chatWs.current.send(JSON.stringify({ type: 'cancel' }));
    }
    setBusy(false);
    setStatus('Canceling…');
    setMessages(prev => {
      const copy = [...prev];
      const last = copy[copy.length - 1];
      if (last?.role === 'assistant' && last.pending) {
        copy[copy.length - 1] = { ...last, text: last.text || '(stopped)', pending: false };
      }
      return copy;
    });
    // Let the server send a 'canceled' event which closes sockets cleanly.
    // Fallback: force-close after 3s if the server never responds.
    setTimeout(() => {
      chatWs.current?.close();
      dashWs.current?.close();
    }, 3000);
  }, []);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  };

  const clearChat = () => {
    setMessages([]);
    setTrace([]);
    setStatus('');
    setAgentInvocations([]);
    setActivities([]);
    activitiesRef.current = [];
    setContextId(null);
    setRestoredSession(null);
    runId.current = null;
    localStorage.removeItem('them:playground:context_id');
  };

  const resumeSession = (s: ContextSession) => {
    setRestoredSession(null);
    setContextId(s.context_id);
    if (s.orchestrator_name !== selectedOrch) setSelectedOrch(s.orchestrator_name);
    setMessages([{ role: 'assistant', text: `↩ Resumed: **${s.title}** — ${s.turn_count} prior turn${s.turn_count !== 1 ? 's' : ''}. Continue the conversation below.` }]);
    setDebugTab('trace');
  };

  // ── Voice recording ───────────────────────────────────────────────────────
  const startRecording = async () => {
    if (recordingState !== 'idle' || !selectedOrch) return;
    try {
      const mediaDevices = navigator.mediaDevices ??
        // HTTP non-localhost: mediaDevices is undefined; nothing we can do
        (() => { throw new Error('Microphone requires HTTPS or localhost'); })();
      const stream = await mediaDevices.getUserMedia({ audio: true });
      const recorder = new MediaRecorder(stream, { mimeType: 'audio/webm' });
      const chunks: Blob[] = [];
      recorder.ondataavailable = (e) => { if (e.data.size > 0) chunks.push(e.data); };
      recorder.onstop = async () => {
        stream.getTracks().forEach(t => t.stop());
        const blob = new Blob(chunks, { type: 'audio/webm' });
        setRecordingState('transcribing');
        try {
          const result = await themApi.transcribe(selectedOrch, blob);
          if (result.text) {
            await sendText(result.text, contextId);
          }
        } catch (e) {
          console.error('Transcription error', e);
          setStatus('Transcription failed');
          setTimeout(() => setStatus(''), 3000);
        } finally {
          setRecordingState('idle');
        }
      };
      recorder.start();
      setMediaRecorder(recorder);
      setRecordingState('recording');
    } catch (e) {
      console.error('Mic access error', e);
      setStatus('Microphone access denied');
      setTimeout(() => setStatus(''), 3000);
    }
  };

  const stopRecording = () => {
    if (mediaRecorder && recordingState === 'recording') {
      mediaRecorder.stop();
      setMediaRecorder(null);
    }
  };

  // Mic button style
  const micBtnStyle = (): React.CSSProperties => {
    if (recordingState === 'recording') {
      return {
        padding: '10px 14px', borderRadius: 12, border: 'none',
        background: '#ef4444', color: '#fff', cursor: 'pointer',
        alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center',
        outline: '2px solid #f87171',
      };
    }
    if (recordingState === 'transcribing') {
      return {
        padding: '10px 14px', borderRadius: 12, border: 'none',
        background: 'var(--tm-surface)', color: 'var(--tm-text-muted)', cursor: 'not-allowed',
        alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center',
      };
    }
    return {
      padding: '10px 14px', borderRadius: 12, border: 'none',
      background: 'var(--tm-surface-2)', color: 'var(--tm-text-muted)', cursor: 'pointer',
      alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center',
    };
  };

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <AuthGuard>
      <div style={{ display: 'flex', height: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', minWidth: 0 }}>
          {/* Header */}
          <div style={{ padding: '16px 24px', borderBottom: '1px solid var(--tm-border)', display: 'flex', alignItems: 'center', gap: 16 }}>
            <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--tm-text)' }}>Playground</div>
            <select
              value={selectedOrch}
              onChange={e => { setSelectedOrch(e.target.value); clearChat(); }}
              style={{ padding: '6px 12px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', color: 'var(--tm-text)', fontSize: 13, cursor: 'pointer' }}
            >
              {orchestrators.map(o => (
                <option key={o.id} value={o.name}>{o.display_name || o.name}</option>
              ))}
            </select>
            {speaking && (
              <span style={{ fontSize: 12, color: '#a78bfa', marginLeft: 'auto' }}>🔊 Speaking…</span>
            )}
            {!speaking && status && (
              <span style={{ fontSize: 12, color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{status}</span>
            )}
            <button onClick={clearChat} style={{ marginLeft: status ? 0 : 'auto', padding: '4px 12px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: 12 }}>
              Clear
            </button>
          </div>

          {/* Split view */}
          <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
            {/* Chat pane */}
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', borderRight: '1px solid var(--tm-border)' }}>
              <div style={{ flex: 1, overflowY: 'auto', padding: '20px 24px', display: 'flex', flexDirection: 'column', gap: 16 }}>
                {messages.length === 0 && restoredSession && (
                  <div style={{
                    margin: '40px auto', maxWidth: 400, padding: '16px 20px',
                    borderRadius: 12, border: '1px solid #7c3aed',
                    background: 'rgba(124,58,237,0.08)',
                    display: 'flex', flexDirection: 'column', gap: 10,
                  }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tm-text)' }}>
                      ↩ Resume last conversation?
                    </div>
                    <div style={{ fontSize: 12, color: 'var(--tm-text-muted)' }}>
                      <span style={{ color: '#a78bfa', fontWeight: 600 }}>{restoredSession.orchestrator_name}</span>
                      {' · '}{restoredSession.turn_count} turn{restoredSession.turn_count !== 1 ? 's' : ''}
                      {' · '}{(() => {
                        const d = new Date(restoredSession.last_active);
                        const diff = Date.now() - d.getTime();
                        if (diff < 60000) return 'just now';
                        if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
                        if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
                        return d.toLocaleDateString();
                      })()}
                    </div>
                    <div style={{ fontSize: 12, color: 'var(--tm-text)', fontStyle: 'italic', opacity: 0.8 }}>
                      "{restoredSession.title}"
                    </div>
                    <div style={{ display: 'flex', gap: 8 }}>
                      <button onClick={() => resumeSession(restoredSession)} style={{
                        flex: 1, padding: '7px 0', borderRadius: 8, border: 'none',
                        background: '#7c3aed', color: '#fff', fontSize: 12, fontWeight: 600, cursor: 'pointer',
                      }}>
                        Resume
                      </button>
                      <button onClick={() => { setRestoredSession(null); localStorage.removeItem('them:playground:context_id'); }} style={{
                        flex: 1, padding: '7px 0', borderRadius: 8, border: '1px solid var(--tm-border)',
                        background: 'transparent', color: 'var(--tm-text-muted)', fontSize: 12, cursor: 'pointer',
                      }}>
                        Start fresh
                      </button>
                    </div>
                  </div>
                )}
                {messages.length === 0 && !restoredSession && (
                  <div style={{ color: 'var(--tm-text-muted)', fontSize: 14, textAlign: 'center', marginTop: 60 }}>
                    Select an orchestrator and send a message to begin
                  </div>
                )}
                {messages.map((m, i) => (
                  <div key={i} style={{ display: 'flex', justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
                    {m.file ? (
                      /* ── File artifact bubble ── */
                      <div style={{
                        maxWidth: '80%',
                        borderRadius: '16px 16px 16px 4px',
                        border: '1px solid var(--tm-border)',
                        background: 'var(--tm-surface)',
                        overflow: 'hidden',
                      }}>
                        {/* Header bar */}
                        <div style={{
                          display: 'flex', alignItems: 'center', gap: 10,
                          padding: '8px 14px',
                          borderBottom: '1px solid var(--tm-border)',
                          background: 'rgba(124,58,237,0.08)',
                        }}>
                          <span style={{ fontSize: 16 }}>
                            {m.file.media_type === 'text/html' ? '🌐' : m.file.media_type === 'text/markdown' ? '📝' : '📄'}
                          </span>
                          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--tm-text)', flex: 1 }}>
                            {m.file.filename}
                          </span>
                          <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', padding: '2px 6px', border: '1px solid var(--tm-border)', borderRadius: 4 }}>
                            {m.file.media_type}
                          </span>
                          <button
                            onClick={() => {
                              const blob = new Blob([m.file!.text], { type: m.file!.media_type });
                              const url = URL.createObjectURL(blob);
                              const a = document.createElement('a');
                              a.href = url; a.download = m.file!.filename; a.click();
                              URL.revokeObjectURL(url);
                            }}
                            style={{
                              padding: '4px 10px', borderRadius: 6, fontSize: 12, fontWeight: 600,
                              background: '#7c3aed', color: '#fff', border: 'none', cursor: 'pointer',
                            }}
                          >
                            Download
                          </button>
                        </div>
                        {/* Inline preview for HTML */}
                        {m.file.media_type === 'text/html' && m.file.text && (
                          <iframe
                            srcDoc={m.file.text}
                            style={{ width: '100%', height: 400, border: 'none', display: 'block' }}
                            sandbox="allow-scripts allow-same-origin"
                            title={m.file.filename}
                          />
                        )}
                        {/* Inline preview for markdown/slides */}
                        {m.file.media_type === 'text/markdown' && m.file.text && (
                          <pre style={{
                            margin: 0, padding: '12px 14px', fontSize: 12,
                            fontFamily: 'monospace', color: 'var(--tm-text)',
                            whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: 300, overflowY: 'auto',
                          }}>
                            {m.file.text}
                          </pre>
                        )}
                      </div>
                    ) : (
                      /* ── Normal text bubble ── */
                      <div style={{
                        maxWidth: '75%',
                        padding: '10px 14px',
                        borderRadius: m.role === 'user' ? '16px 16px 4px 16px' : '16px 16px 16px 4px',
                        background: m.role === 'user' ? '#7c3aed' : 'var(--tm-surface)',
                        color: m.role === 'user' ? '#fff' : 'var(--tm-text)',
                        fontSize: 14,
                        lineHeight: 1.5,
                        wordBreak: 'break-word',
                      }}>
                        {m.pending && !m.text
                          ? <span style={{ opacity: 0.5 }}>thinking…</span>
                          : m.role === 'assistant'
                            ? <div dir="auto"><MarkdownText text={m.text} /></div>
                            : <span dir="auto" style={{ whiteSpace: 'pre-wrap' }}>{m.text}</span>
                        }
                      </div>
                    )}
                  </div>
                ))}

                <div ref={chatBottom} />
              </div>

              {/* Activity bar — live agent status, sits above input */}
              <ActivityBar activities={activities} />

              {/* Input */}
              <div style={{ padding: '12px 16px', borderTop: '1px solid var(--tm-border)', display: 'flex', gap: 8 }}>
                {voiceEnabled && (
                  <button
                    style={micBtnStyle()}
                    onMouseDown={startRecording}
                    onMouseUp={stopRecording}
                    onTouchStart={startRecording}
                    onTouchEnd={stopRecording}
                    disabled={recordingState === 'transcribing' || busy || !selectedOrch}
                    title={recordingState === 'recording' ? 'Release to transcribe' : 'Hold to record'}
                  >
                    {recordingState === 'transcribing' ? <Spinner /> : <MicIcon />}
                  </button>
                )}
                {/* WebRTC voice room button — shown when a webrtc app is bound to this orchestrator */}
                <button
                  onClick={() => webrtcSlug && window.open(`/apps/${webrtcSlug}/voice`, '_blank', 'noopener')}
                  disabled={!webrtcSlug}
                  title={webrtcSlug ? `Open voice room (${webrtcSlug})` : 'No WebRTC app configured for this orchestrator'}
                  style={{
                    width: 38, height: 38,
                    borderRadius: 10,
                    border: '1.5px solid',
                    borderColor: webrtcSlug ? 'rgba(99,202,183,0.6)' : 'var(--tm-border)',
                    background: webrtcSlug ? 'rgba(99,202,183,0.08)' : 'var(--tm-surface)',
                    cursor: webrtcSlug ? 'pointer' : 'not-allowed',
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    flexShrink: 0, alignSelf: 'flex-end',
                    opacity: webrtcSlug ? 1 : 0.35,
                    transition: 'border-color 150ms ease, background 150ms ease',
                  }}
                >
                  {/* WebRTC logo — three interlocking circles */}
                  <svg width="20" height="20" viewBox="0 0 100 100" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <circle cx="50" cy="28" r="22" fill={webrtcSlug ? '#63cab7' : 'currentColor'} opacity="0.9"/>
                    <circle cx="30" cy="65" r="22" fill={webrtcSlug ? '#f08030' : 'currentColor'} opacity="0.9"/>
                    <circle cx="70" cy="65" r="22" fill={webrtcSlug ? '#63cab7' : 'currentColor'} opacity="0.7"/>
                    <circle cx="50" cy="28" r="22" fill="none" stroke="var(--tm-bg)" strokeWidth="3"/>
                    <circle cx="30" cy="65" r="22" fill="none" stroke="var(--tm-bg)" strokeWidth="3"/>
                    <circle cx="70" cy="65" r="22" fill="none" stroke="var(--tm-bg)" strokeWidth="3"/>
                  </svg>
                </button>
                <textarea
                  value={input}
                  onChange={e => setInput(e.target.value)}
                  onKeyDown={onKey}
                  disabled={busy || !selectedOrch}
                  dir="auto"
                  placeholder="Send a message… (Enter to send, Shift+Enter for newline)"
                  rows={3}
                  style={{
                    flex: 1,
                    padding: '10px 14px',
                    borderRadius: 12,
                    border: '1px solid var(--tm-border)',
                    background: 'var(--tm-surface)',
                    color: 'var(--tm-text)',
                    fontSize: 13,
                    resize: 'none',
                    outline: 'none',
                    fontFamily: 'inherit',
                    lineHeight: 1.5,
                  }}
                />
                {busy ? (
                  <button
                    onClick={stopRun}
                    style={{
                      padding: '10px 18px',
                      borderRadius: 12,
                      border: '1.5px solid #ef4444',
                      background: 'transparent',
                      color: '#ef4444',
                      fontSize: 13,
                      fontWeight: 600,
                      cursor: 'pointer',
                      alignSelf: 'flex-end',
                    }}
                  >
                    Stop
                  </button>
                ) : (
                  <button
                    onClick={send}
                    disabled={!input.trim() || !selectedOrch}
                    style={{
                      padding: '10px 18px',
                      borderRadius: 12,
                      border: 'none',
                      background: !input.trim() || !selectedOrch ? 'var(--tm-surface)' : '#7c3aed',
                      color: !input.trim() || !selectedOrch ? 'var(--tm-text-muted)' : '#fff',
                      fontSize: 13,
                      fontWeight: 600,
                      cursor: !input.trim() || !selectedOrch ? 'not-allowed' : 'pointer',
                      alignSelf: 'flex-end',
                    }}
                  >
                    Send
                  </button>
                )}
              </div>
            </div>

            {/* Debug panel — tabbed right tray */}
            <div style={{ width: 400, display: 'flex', flexDirection: 'column', background: 'var(--tm-bg)', borderLeft: '1px solid var(--tm-border)' }}>
              {/* Tab bar */}
              <div style={{ padding: '8px 12px', borderBottom: '1px solid var(--tm-border)', display: 'flex', gap: 4, alignItems: 'center' }}>
                <TabBtn label="Trace" active={debugTab === 'trace'} onClick={() => setDebugTab('trace')} />
                <TabBtn label="Tasks" active={debugTab === 'tasks'} onClick={() => setDebugTab('tasks')} />
                <TabBtn label="Artifacts" active={debugTab === 'artifacts'} onClick={() => setDebugTab('artifacts')} />
                <TabBtn label="Memory" active={debugTab === 'memory'} onClick={() => setDebugTab('memory')} />
                <TabBtn label="Sessions" active={debugTab === 'sessions'} onClick={() => setDebugTab('sessions')} />
              </div>
              {/* Tab content */}
              <div style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
                {debugTab === 'trace' && <TraceTab trace={trace} traceBottom={traceBottom} runId={runId.current} contextId={contextId} />}
                {debugTab === 'tasks' && <TasksTab runId={runId.current} />}
                {debugTab === 'artifacts' && <ArtifactsTab runId={runId.current} />}
                {debugTab === 'memory' && (
                  <MemoryTab
                    contextId={contextId}
                    agentInvocations={agentInvocations}
                    allAgents={null}
                  />
                )}
                {debugTab === 'sessions' && (
                  <SessionsTab
                    currentContextId={contextId}
                    onResume={resumeSession}
                  />
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </AuthGuard>
  );
}
