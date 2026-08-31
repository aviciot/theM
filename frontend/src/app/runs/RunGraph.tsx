'use client';
import { useState } from 'react';
import { statusColor, formatDuration, type GraphNode, type GraphRow } from './runsTypes';

// ── Status badge ──────────────────────────────────────────────────────────────

export function StatusBadge({ status }: { status: string }) {
  const col = statusColor(status);
  return (
    <span style={{
      fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px',
      background: `${col}18`, color: col,
    }}>{status}</span>
  );
}

// ── Individual node cards ─────────────────────────────────────────────────────

function NodeCard({ node, expanded, onToggle }: { node: GraphNode; expanded: boolean; onToggle: () => void }) {
  const baseStyle: React.CSSProperties = {
    borderRadius: '12px', border: '1px solid var(--tm-border)',
    background: 'var(--tm-surface)', cursor: 'pointer',
    transition: 'box-shadow 0.15s, border-color 0.15s',
    minWidth: '180px', maxWidth: '280px',
    userSelect: 'none',
  };

  if (node.kind === 'user') {
    return (
      <div onClick={onToggle} style={{ ...baseStyle, border: '1px solid rgba(91,127,255,.4)', background: 'rgba(91,127,255,.06)', padding: '12px 16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: expanded ? '8px' : 0 }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: '#5b7fff' }}>person</span>
          <span style={{ fontSize: '12px', fontWeight: 700, color: '#5b7fff' }}>User</span>
        </div>
        {expanded && <div style={{ fontSize: '12px', color: 'var(--tm-text)', lineHeight: 1.5, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{node.text}</div>}
        {!expanded && <div style={{ fontSize: '12px', color: 'var(--tm-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{node.text}</div>}
      </div>
    );
  }

  if (node.kind === 'orchestrator') {
    const r = node.run;
    const col = statusColor(r.status);
    const durationMs = r.duration_ms ?? (r.ended_at && r.started_at ? new Date(r.ended_at).getTime() - new Date(r.started_at).getTime() : null);
    return (
      <div onClick={onToggle} style={{ ...baseStyle, border: `1px solid ${col}40`, background: `${col}08`, padding: '12px 16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: col }}>hub</span>
          <span style={{ fontSize: '12px', fontWeight: 700, color: col }}>Orchestrator</span>
          <StatusBadge status={r.status} />
        </div>
        <div style={{ fontSize: '13px', fontWeight: 600, color: 'var(--tm-text)', marginBottom: expanded ? '10px' : 0 }}>{r.orchestrator_name}</div>
        {expanded && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '4px', fontSize: '11px', color: 'var(--tm-text-muted)' }}>
            <div>Iterations: <strong style={{ color: 'var(--tm-text)' }}>{r.iterations ?? '—'}</strong></div>
            <div>Duration: <strong style={{ color: 'var(--tm-text)' }}>{formatDuration(durationMs)}</strong></div>
            <div>Tokens in: <strong style={{ color: 'var(--tm-text)' }}>{(r.total_tokens_in ?? 0).toLocaleString()}</strong></div>
            <div>Tokens out: <strong style={{ color: 'var(--tm-text)' }}>{(r.total_tokens_out ?? 0).toLocaleString()}</strong></div>
            {r.error && <div style={{ color: '#f87171', marginTop: '4px' }}>Error: {r.error}</div>}
          </div>
        )}
      </div>
    );
  }

  if (node.kind === 'agent') {
    const { step } = node;
    const col = statusColor(step.status);
    return (
      <div onClick={onToggle} style={{ ...baseStyle, border: `1px solid ${col}40`, background: `${col}06`, padding: '12px 16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: col }}>smart_toy</span>
          <span style={{ fontSize: '12px', fontWeight: 700, color: col }}>{step.agent_slug}</span>
          {step.latency_ms != null && (
            <span style={{ fontSize: '10px', color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{step.latency_ms}ms</span>
          )}
        </div>
        <div style={{ fontSize: '10px', color: 'var(--tm-text-muted)', marginBottom: expanded ? '10px' : 0 }}>iter {step.iteration} · {step.status}</div>
        {expanded && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
            {!!step.input?.message && (
              <div>
                <div style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '3px' }}>Input</div>
                <div style={{ fontSize: '11px', color: 'var(--tm-text)', background: 'var(--tm-surface-2)', borderRadius: '6px', padding: '6px 8px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: '120px', overflowY: 'auto' }}>
                  {String(step.input.message)}
                </div>
              </div>
            )}
            {step.output && (
              <div>
                <div style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '3px' }}>Output</div>
                <div style={{ fontSize: '11px', color: 'var(--tm-text)', background: 'var(--tm-surface-2)', borderRadius: '6px', padding: '6px 8px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: '120px', overflowY: 'auto' }}>
                  {step.output}
                </div>
              </div>
            )}
            {step.error && <div style={{ fontSize: '11px', color: '#f87171' }}>Error: {step.error}</div>}
            {node.artifacts.length > 0 && (
              <div style={{ fontSize: '10px', color: '#a78bfa' }}>{node.artifacts.length} artifact{node.artifacts.length !== 1 ? 's' : ''}</div>
            )}
          </div>
        )}
      </div>
    );
  }

  if (node.kind === 'summary') {
    const text = node.artifact.parts.find(p => p.text)?.text ?? '';
    return (
      <div onClick={onToggle} style={{ ...baseStyle, border: '1px solid rgba(251,191,36,.3)', background: 'rgba(251,191,36,.04)', padding: '12px 16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: expanded ? '8px' : 0 }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: '#fbbf24' }}>summarize</span>
          <span style={{ fontSize: '12px', fontWeight: 700, color: '#fbbf24' }}>Memory Summary</span>
        </div>
        {expanded && <div style={{ fontSize: '11px', color: 'var(--tm-text)', whiteSpace: 'pre-wrap', lineHeight: 1.5, maxHeight: '150px', overflowY: 'auto' }}>{text}</div>}
        {!expanded && <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{text.slice(0, 60)}…</div>}
      </div>
    );
  }

  if (node.kind === 'answer') {
    const text = node.artifact.parts.find(p => p.text)?.text ?? '';
    return (
      <div onClick={onToggle} style={{ ...baseStyle, border: '1px solid rgba(78,222,163,.4)', background: 'rgba(78,222,163,.06)', padding: '12px 16px', maxWidth: '340px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: expanded ? '8px' : 0 }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: '#4edea3' }}>check_circle</span>
          <span style={{ fontSize: '12px', fontWeight: 700, color: '#4edea3' }}>Final Answer</span>
        </div>
        {expanded
          ? <div style={{ fontSize: '12px', color: 'var(--tm-text)', whiteSpace: 'pre-wrap', lineHeight: 1.6, maxHeight: '200px', overflowY: 'auto' }}>{text}</div>
          : <div style={{ fontSize: '12px', color: 'var(--tm-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{text.slice(0, 80)}…</div>
        }
      </div>
    );
  }

  if (node.kind === 'iteration') {
    return (
      <div style={{ padding: '4px 12px', borderRadius: '20px', background: 'rgba(167,139,250,.1)', border: '1px solid rgba(167,139,250,.2)', fontSize: '11px', fontWeight: 700, color: '#a78bfa', pointerEvents: 'none' }}>
        Iteration {node.iteration}
      </div>
    );
  }

  return null;
}

// ── Connector SVG arrow ───────────────────────────────────────────────────────

function Arrow() {
  return (
    <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '32px' }}>
      <svg width="2" height="28" viewBox="0 0 2 28">
        <line x1="1" y1="0" x2="1" y2="22" stroke="var(--tm-border)" strokeWidth="2" strokeDasharray="4 2" />
        <polygon points="1,28 -3,20 5,20" fill="var(--tm-border)" />
      </svg>
    </div>
  );
}

// ── Node graph ────────────────────────────────────────────────────────────────

export function NodeGraph({ rows }: { rows: GraphRow[] }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set(['0-0', '1-0']));

  const toggle = (rowI: number, nodeI: number) => {
    const key = `${rowI}-${nodeI}`;
    setExpanded(prev => {
      const next = new Set(prev);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '8px 0 24px', minWidth: '600px' }}>
      {rows.map((row, rowI) => (
        <div key={rowI} style={{ width: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
          {rowI > 0 && <Arrow />}

          {row.parallel ? (
            <div style={{ display: 'flex', gap: '12px', alignItems: 'flex-start', justifyContent: 'center', flexWrap: 'wrap' }}>
              {row.nodes.map((node, nodeI) => (
                <div key={nodeI} style={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                  <div style={{ height: '16px', width: '2px', background: 'var(--tm-border)', marginBottom: '0' }} />
                  <NodeCard
                    node={node}
                    expanded={expanded.has(`${rowI}-${nodeI}`)}
                    onToggle={() => toggle(rowI, nodeI)}
                  />
                </div>
              ))}
            </div>
          ) : (
            <NodeCard
              node={row.nodes[0]}
              expanded={expanded.has(`${rowI}-0`)}
              onToggle={() => toggle(rowI, 0)}
            />
          )}
        </div>
      ))}
    </div>
  );
}
