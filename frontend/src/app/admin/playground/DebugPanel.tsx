'use client';
import { useEffect, useState } from 'react';
import { themApi, type TaskOut, type ArtifactOut, type ArtifactPart, type ContextSession } from '@/lib/api';
import {
  fmtRelMs, fmtDuration, traceColor, traceLabel, groupTraceEvents, stateColor,
  type TraceEvent, type TraceGroup, type DebugTab,
} from './playgroundTypes';

// ── TabBtn ────────────────────────────────────────────────────────────────

export function TabBtn({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
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

// ── TracePayload ──────────────────────────────────────────────────────────

function tryParseJson(v: unknown): unknown {
  if (typeof v === 'string') {
    try { return JSON.parse(v); } catch { return v; }
  }
  return v;
}

function TracePayload({ value, label }: { value: unknown; label: string }) {
  const [expanded, setExpanded] = useState(false);
  const parsed = tryParseJson(value);

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

// ── TraceEventRow ─────────────────────────────────────────────────────────

function TraceEventRow({ ev, runStartTs, indent }: { ev: TraceEvent; runStartTs: number; indent?: boolean }) {
  const isError = ev.type === 'error';
  const rel = fmtRelMs(ev.ts - runStartTs);
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', gap: 1,
      marginLeft: indent ? 16 : 0,
      borderLeft: indent ? '2px solid rgba(167,139,250,0.25)' : undefined,
      paddingLeft: indent ? 8 : 0,
      ...(isError ? {
        background: 'rgba(248,113,113,0.08)',
        border: '1px solid rgba(248,113,113,0.3)',
        borderRadius: 6, padding: '5px 8px',
      } : {}),
    }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
        <span style={{ fontSize: 9, color: 'rgba(107,114,128,0.7)', fontFamily: 'monospace', flexShrink: 0 }}>{rel}</span>
        <span style={{ fontSize: 12, color: traceColor(ev.type), fontWeight: 500, fontFamily: 'monospace', wordBreak: 'break-all' }}>
          {traceLabel(ev)}
        </span>
      </div>
      {ev.type === 'tool_start' && ev.input != null && (
        <TracePayload value={ev.input} label="input" />
      )}
      {(ev.type === 'tool_done' || ev.type === 'tool_result') && ev.output != null && (
        <TracePayload value={ev.output} label="output" />
      )}
      {ev.type === 'agent_status' && ev.detail != null && (
        <TracePayload value={ev.detail} label="detail" />
      )}
    </div>
  );
}

// ── TraceIterationGroup ───────────────────────────────────────────────────

function TraceIterationGroup({ group, runStartTs }: { group: TraceGroup; runStartTs: number }) {
  const [collapsed, setCollapsed] = useState(false);
  const agentPills = group.agents.map(a => a.replace(/^agent__/, ''));
  const relStart = fmtRelMs(group.startTs - runStartTs);
  const usageEv = group.usage;

  return (
    <div style={{ borderRadius: 8, border: '1px solid rgba(245,158,11,0.25)', overflow: 'hidden' }}>
      <div
        onClick={() => setCollapsed(c => !c)}
        style={{
          display: 'flex', alignItems: 'center', gap: 8, padding: '6px 10px',
          background: 'rgba(245,158,11,0.08)', cursor: 'pointer', userSelect: 'none',
        }}
      >
        <span style={{ fontSize: 10, color: 'rgba(107,114,128,0.6)', fontFamily: 'monospace', flexShrink: 0 }}>{relStart}</span>
        <span style={{ fontSize: 11, color: '#f59e0b', fontWeight: 700 }}>Iter {group.iteration}</span>
        {agentPills.map(a => (
          <span key={a} style={{
            fontSize: 9, padding: '1px 6px', borderRadius: 10,
            background: 'rgba(167,139,250,0.15)', color: '#a78bfa', fontFamily: 'monospace',
          }}>{a}</span>
        ))}
        {usageEv && (
          <span style={{ fontSize: 10, color: '#60a5fa', marginLeft: 'auto', flexShrink: 0 }}>
            {usageEv.input_tokens as number}↑ {usageEv.output_tokens as number}↓
          </span>
        )}
        <span style={{ fontSize: 10, color: 'rgba(107,114,128,0.5)', flexShrink: 0 }}>
          {collapsed ? '▸' : '▾'}
        </span>
      </div>
      {!collapsed && group.events.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, padding: '6px 10px', background: 'rgba(0,0,0,0.12)' }}>
          {group.events.map((ev, i) => (
            <TraceEventRow key={i} ev={ev} runStartTs={runStartTs} indent />
          ))}
        </div>
      )}
      {!collapsed && group.events.length === 0 && (
        <div style={{ padding: '4px 10px', fontSize: 11, color: 'rgba(107,114,128,0.5)', fontStyle: 'italic' }}>
          LLM processing…
        </div>
      )}
    </div>
  );
}

// ── TraceRunSummary ───────────────────────────────────────────────────────

function TraceRunSummary({ ev, runStartTs }: { ev: TraceEvent; runStartTs: number }) {
  const durationMs = (ev.duration_ms as number) || (ev.ts - runStartTs);
  const ok = ev.status === 'completed' || ev.type === 'done';
  return (
    <div style={{
      borderRadius: 8, border: `1px solid ${ok ? 'rgba(78,222,163,0.4)' : 'rgba(248,113,113,0.4)'}`,
      background: ok ? 'rgba(78,222,163,0.07)' : 'rgba(248,113,113,0.07)',
      padding: '8px 12px', display: 'flex', flexDirection: 'column', gap: 3,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ fontSize: 13, color: ok ? '#4edea3' : '#f87171', fontWeight: 700 }}>
          {ok ? '✓ Run complete' : '✗ Run failed'}
        </span>
        {ev.status != null && <span style={{ fontSize: 10, color: 'rgba(107,114,128,0.7)', fontFamily: 'monospace' }}>{String(ev.status)}</span>}
      </div>
      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
        {ev.iterations != null && (
          <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
            <span style={{ color: 'var(--tm-text)', fontWeight: 600 }}>{ev.iterations as number}</span> iterations
          </span>
        )}
        <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
          <span style={{ color: 'var(--tm-text)', fontWeight: 600 }}>{fmtDuration(durationMs)}</span> duration
        </span>
        {ev.input_tokens != null && (
          <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
            <span style={{ color: '#60a5fa', fontWeight: 600 }}>{ev.input_tokens as number}</span>↑{' '}
            <span style={{ color: '#60a5fa', fontWeight: 600 }}>{ev.output_tokens as number}</span>↓ tokens
          </span>
        )}
      </div>
    </div>
  );
}

// ── TraceTab ──────────────────────────────────────────────────────────────

export function TraceTab({ trace, traceBottom, runId, contextId }: { trace: TraceEvent[]; traceBottom: React.RefObject<HTMLDivElement | null>; runId?: string | null; contextId?: string | null }) {
  const runStartTs = trace.find(e => e.type === 'ready' || e.type === 'run_start')?.ts ?? (trace[0]?.ts ?? Date.now());
  const { preamble, groups, tail } = groupTraceEvents(trace);

  return (
    <div className="dark-scrollbar" style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 6 }}>
      {(runId || contextId) && (
        <div style={{ fontSize: 10, fontFamily: 'monospace', color: 'var(--tm-text-muted)', background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: 4, padding: '6px 8px', display: 'flex', flexDirection: 'column', gap: 2, marginBottom: 2 }}>
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
      {preamble.map((ev, i) => (
        <TraceEventRow key={`pre-${i}`} ev={ev} runStartTs={runStartTs} />
      ))}
      {groups.map((g, i) => (
        <TraceIterationGroup key={`iter-${i}`} group={g} runStartTs={runStartTs} />
      ))}
      {tail.map((ev, i) => (
        ev.type === 'run_end' || ev.type === 'done'
          ? <TraceRunSummary key={`tail-${i}`} ev={ev} runStartTs={runStartTs} />
          : <TraceEventRow key={`tail-${i}`} ev={ev} runStartTs={runStartTs} />
      ))}
      <div ref={traceBottom} />
    </div>
  );
}

// ── TasksTab ──────────────────────────────────────────────────────────────

function taskStateIcon(state: string): string {
  if (state === 'completed') return '✓';
  if (state === 'failed') return '✗';
  if (state === 'working') return '⟳';
  if (state === 'canceled') return '⊘';
  return '·';
}

export function TasksTab({ runId, busy }: { runId: string | null; busy: boolean }) {
  const [tasks, setTasks] = useState<TaskOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    if (!runId) { setTasks([]); return; }
    let cancelled = false;
    const load = () => {
      themApi.runTasks(runId)
        .then(t => { if (!cancelled) setTasks(t); })
        .catch(e => { if (!cancelled) setErr(e.message); })
        .finally(() => { if (!cancelled) setLoading(false); });
    };
    setLoading(true);
    setErr('');
    load();
    if (!busy) return () => { cancelled = true; };
    const interval = setInterval(load, 2500);
    return () => { cancelled = true; clearInterval(interval); };
  }, [runId, busy]);

  if (!runId) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, textAlign: 'center', marginTop: 40, padding: 16 }}>Start a run to see the task graph</div>;
  if (loading && tasks.length === 0) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>Loading…</div>;
  if (err) return <div style={{ color: '#f87171', fontSize: 12, padding: 16 }}>{err}</div>;
  if (tasks.length === 0) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>No tasks yet</div>;

  const root = tasks.find(t => t.kind === 'root');
  const children = tasks.filter(t => t.kind !== 'root');

  return (
    <div className="dark-scrollbar" style={{ flex: 1, overflowY: 'auto', padding: '10px 14px', display: 'flex', flexDirection: 'column', gap: 6 }}>
      {root && (
        <div style={{
          padding: '8px 12px', borderRadius: 8,
          border: `1px solid ${stateColor(root.state)}44`,
          background: 'var(--tm-surface)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: 13, color: stateColor(root.state), fontWeight: 700 }}>{taskStateIcon(root.state)}</span>
            <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text)' }}>Orchestrator</span>
            <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 10, background: 'rgba(107,114,128,0.15)', color: 'var(--tm-text-muted)' }}>root</span>
            <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 10, background: `${stateColor(root.state)}22`, color: stateColor(root.state), marginLeft: 'auto' }}>{root.state}</span>
          </div>
          {root.error && <div style={{ fontSize: 11, color: '#f87171', marginTop: 4 }}>{root.error}</div>}
        </div>
      )}
      {children.length > 0 && (
        <div style={{ paddingLeft: 16, display: 'flex', flexDirection: 'column', gap: 4, borderLeft: '2px solid var(--tm-border)' }}>
          {children.map(t => {
            const name = (t as TaskOut & { agent_name?: string; agent_slug?: string; duration_ms?: number }).agent_name
              || (t as TaskOut & { agent_name?: string; agent_slug?: string; duration_ms?: number }).agent_slug
              || t.agent_id?.slice(0, 8) || 'agent';
            const dur = (t as TaskOut & { duration_ms?: number }).duration_ms;
            return (
              <div key={t.id} style={{
                padding: '7px 10px', borderRadius: 7,
                border: `1px solid ${stateColor(t.state)}44`,
                background: 'var(--tm-bg)',
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
                  <span style={{ fontSize: 12, color: stateColor(t.state), fontWeight: 700, flexShrink: 0 }}>{taskStateIcon(t.state)}</span>
                  <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{name}</span>
                  {dur != null && (
                    <span style={{ fontSize: 10, color: 'var(--tm-text-muted)', flexShrink: 0 }}>{dur < 1000 ? `${dur}ms` : `${(dur / 1000).toFixed(1)}s`}</span>
                  )}
                  {t.tokens_used != null && t.tokens_used > 0 && (
                    <span style={{ fontSize: 10, color: '#60a5fa', flexShrink: 0 }}>{t.tokens_used} tok</span>
                  )}
                  <span style={{ fontSize: 10, padding: '1px 5px', borderRadius: 8, background: `${stateColor(t.state)}22`, color: stateColor(t.state), flexShrink: 0 }}>{t.state}</span>
                </div>
                {t.error && <div style={{ fontSize: 11, color: '#f87171', marginTop: 3 }}>{t.error}</div>}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ── ArtifactsTab ──────────────────────────────────────────────────────────

export function ArtifactsTab({ runId, busy }: { runId: string | null; busy: boolean }) {
  const [artifacts, setArtifacts] = useState<ArtifactOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (!runId) { setArtifacts([]); return; }
    let cancelled = false;
    const load = () => {
      themApi.runArtifacts(runId)
        .then(a => { if (!cancelled) setArtifacts(a); })
        .catch(e => { if (!cancelled) setErr(e.message); })
        .finally(() => { if (!cancelled) setLoading(false); });
    };
    setLoading(true);
    setErr('');
    load();
    if (!busy) {
      let ticks = 5;
      const grace = setInterval(() => {
        if (cancelled || ticks-- <= 0) { clearInterval(grace); return; }
        load();
      }, 3000);
      return () => { cancelled = true; clearInterval(grace); };
    }
    const interval = setInterval(load, 3000);
    return () => { cancelled = true; clearInterval(interval); };
  }, [runId, busy]);

  const toggle = (id: string) => setExpanded(prev => {
    const next = new Set(prev);
    next.has(id) ? next.delete(id) : next.add(id);
    return next;
  });

  if (!runId) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, textAlign: 'center', marginTop: 40, padding: 16 }}>Start a run to see artifacts</div>;
  if (loading && artifacts.length === 0) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>Loading…</div>;
  if (err) return <div style={{ color: '#f87171', fontSize: 12, padding: 16 }}>{err}</div>;
  if (artifacts.length === 0) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>No artifacts yet — agents produce artifacts when they return structured output</div>;

  const downloadPart = (part: ArtifactPart) => {
    let blob: Blob;
    if (part.data) {
      const bytes = Uint8Array.from(atob(part.data), c => c.charCodeAt(0));
      blob = new Blob([bytes], { type: part.media_type || part.mediaType || 'application/octet-stream' });
    } else {
      blob = new Blob([part.text || ''], { type: part.media_type || part.mediaType || 'text/plain' });
    }
    const url = URL.createObjectURL(blob);
    const el = document.createElement('a');
    el.href = url;
    el.download = part.filename || 'artifact';
    el.click();
    URL.revokeObjectURL(url);
  };

  const DownloadBtn = ({ part }: { part: ArtifactPart }) => (
    <button
      onClick={() => downloadPart(part)}
      style={{ fontSize: 11, padding: '3px 10px', borderRadius: 5, border: '1px solid var(--tm-border)', background: 'transparent', color: '#a78bfa', cursor: 'pointer' }}
    >
      Download
    </button>
  );

  return (
    <div className="dark-scrollbar" style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
      {artifacts.map(a => {
        const isOpen = expanded.has(a.id);
        const filePart = a.parts.find(p => p.filename || p.media_type || p.mediaType);
        const mt = filePart?.media_type || filePart?.mediaType || '';
        const isHtml     = mt === 'text/html';
        const isMarkdown = mt === 'text/markdown';
        const isImage    = mt.startsWith('image/');
        const isPDF      = mt === 'application/pdf';
        const isText     = mt === 'text/plain' || mt === 'text/csv' || mt === 'application/json' || mt === 'application/xml' || mt === 'text/xml';
        const textParts  = a.parts.filter(p => !p.filename && !p.media_type && !p.mediaType && p.text !== undefined);
        const dataURI = (filePart?.data && mt) ? `data:${mt};base64,${filePart.data}` : null;

        return (
          <div key={a.id} style={{ border: '1px solid var(--tm-border)', borderRadius: 8, background: 'var(--tm-surface)', overflow: 'hidden' }}>
            <button
              onClick={() => toggle(a.id)}
              style={{ width: '100%', padding: '8px 12px', background: 'transparent', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8, textAlign: 'left' }}
            >
              <span style={{ fontSize: 11, color: '#a78bfa', fontWeight: 600 }}>{a.artifact_id || a.id}</span>
              {filePart?.filename
                ? <span style={{ fontSize: 11, color: '#4edea3', fontWeight: 600 }}>{filePart.filename}</span>
                : a.name && <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>{a.name}</span>
              }
              {mt && (
                <span style={{ fontSize: 10, color: 'var(--tm-text-muted)', padding: '1px 6px', border: '1px solid var(--tm-border)', borderRadius: 4 }}>{mt}</span>
              )}
              <span style={{ fontSize: 10, color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{isOpen ? '▲' : '▼'}</span>
            </button>

            {isOpen && filePart && (
              <div style={{ borderTop: '1px solid var(--tm-border)' }}>
                <div style={{ padding: '6px 12px', display: 'flex', justifyContent: 'flex-end', borderBottom: '1px solid var(--tm-border)' }}>
                  <DownloadBtn part={filePart} />
                </div>
                {isHtml && filePart.text && (
                  <iframe
                    srcDoc={filePart.text}
                    style={{ width: '100%', height: 500, border: 'none', display: 'block' }}
                    sandbox="allow-scripts allow-same-origin"
                    title={filePart.filename || 'preview'}
                  />
                )}
                {isImage && dataURI && (
                  <div style={{ padding: 12, textAlign: 'center', background: '#111' }}>
                    {/* eslint-disable-next-line @next/next/no-img-element */}
                    <img src={dataURI} alt={filePart.filename || 'image'} style={{ maxWidth: '100%', maxHeight: 500, objectFit: 'contain' }} />
                  </div>
                )}
                {isPDF && dataURI && (
                  <iframe
                    src={dataURI}
                    style={{ width: '100%', height: 600, border: 'none', display: 'block' }}
                    title={filePart.filename || 'pdf'}
                  />
                )}
                {isMarkdown && (filePart.text || filePart.data) && (
                  <pre style={{ fontSize: 12, color: 'var(--tm-text)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, padding: '12px', maxHeight: 400, overflowY: 'auto' }}>
                    {filePart.text || atob(filePart.data!)}
                  </pre>
                )}
                {isText && (filePart.text || filePart.data) && (
                  <pre style={{ fontSize: 12, color: 'var(--tm-text)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, padding: '12px', maxHeight: 400, overflowY: 'auto' }}>
                    {filePart.text || atob(filePart.data!)}
                  </pre>
                )}
                {!isHtml && !isImage && !isPDF && !isMarkdown && !isText && (
                  <div style={{ padding: '10px 12px', fontSize: 11, color: 'var(--tm-text-muted)' }}>
                    Binary file — use the Download button to save it.
                  </div>
                )}
              </div>
            )}

            {isOpen && !filePart && (
              <div style={{ borderTop: '1px solid var(--tm-border)', padding: '0 12px 10px' }}>
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
        );
      })}
    </div>
  );
}

// ── SessionsTab ───────────────────────────────────────────────────────────

export function SessionsTab({ onResume, currentContextId }: {
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
    <div className="dark-scrollbar" style={{ flex: 1, overflowY: 'auto', padding: '10px 12px' }}>
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

// ── DebugPanel (aggregate) ────────────────────────────────────────────────
// Exported for ChatColumn to render the tabbed debug tray.

export type { DebugTab };
