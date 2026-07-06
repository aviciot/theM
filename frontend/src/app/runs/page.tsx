'use client';
import { useEffect, useState, useCallback } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Run, type RunDetail, type RunStep, type TaskOut, type ArtifactOut } from '@/lib/api';

// ── Helpers ───────────────────────────────────────────────────────────────────

function formatDuration(ms: number | null) {
  if (ms == null) return '—';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function formatTs(iso: string) {
  return new Date(iso).toLocaleString();
}

const STATUS_COLOR: Record<string, string> = {
  completed: '#4edea3', failed: '#f87171', running: '#5b7fff',
  pending: '#fbbf24', working: '#a78bfa', submitted: '#60a5fa',
  canceled: '#94a3b8', rejected: '#94a3b8',
};

function statusColor(s: string) { return STATUS_COLOR[s] ?? '#94a3b8'; }

function StatusBadge({ status }: { status: string }) {
  const col = statusColor(status);
  return (
    <span style={{
      fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px',
      background: `${col}18`, color: col,
    }}>{status}</span>
  );
}

// ── Node graph types ──────────────────────────────────────────────────────────

type GraphNode =
  | { kind: 'user';         text: string }
  | { kind: 'orchestrator'; run: RunDetail }
  | { kind: 'iteration';    iteration: number }
  | { kind: 'agent';        step: RunStep; task?: TaskOut; artifacts: ArtifactOut[] }
  | { kind: 'summary';      artifact: ArtifactOut }
  | { kind: 'answer';       artifact: ArtifactOut };

type GraphRow = { nodes: GraphNode[]; parallel: boolean };

function buildGraph(detail: RunDetail, tasks: TaskOut[], artifacts: ArtifactOut[]): GraphRow[] {
  const rows: GraphRow[] = [];

  // Row 0: user message
  rows.push({ nodes: [{ kind: 'user', text: detail.goal || detail.user_message || '' }], parallel: false });

  // Row 1: orchestrator
  rows.push({ nodes: [{ kind: 'orchestrator', run: detail }], parallel: false });

  // Group steps by iteration — parallel agents share the same iteration
  const byIter = new Map<number, RunStep[]>();
  for (const step of detail.steps) {
    const arr = byIter.get(step.iteration) ?? [];
    arr.push(step);
    byIter.set(step.iteration, arr);
  }

  const taskBySlugIter = new Map<string, TaskOut>();
  for (const t of tasks) {
    if (t.kind === 'delegated' && t.agent_id) {
      const step = detail.steps.find(s => s.agent_slug && t.agent_id);
      if (step) taskBySlugIter.set(`${step.iteration}-${step.agent_slug}`, t);
    }
  }

  const artifactsByTaskId = new Map<string, ArtifactOut[]>();
  for (const a of artifacts) {
    const arr = artifactsByTaskId.get(a.task_id) ?? [];
    arr.push(a);
    artifactsByTaskId.set(a.task_id, arr);
  }

  // Find summary artifacts
  const summaryArtifacts = artifacts.filter(a => a.artifact_id?.startsWith('summary-'));
  const finalAnswer = artifacts.find(a => a.artifact_id === 'final-answer');

  for (const [iter, steps] of Array.from(byIter.entries()).sort((a, b) => a[0] - b[0])) {
    // Iteration label row (only if more than one iteration)
    if (byIter.size > 1) {
      rows.push({ nodes: [{ kind: 'iteration', iteration: iter }], parallel: false });
    }

    // Agent nodes — all steps in this iteration are parallel
    const agentNodes: GraphNode[] = steps.map(step => {
      const task = taskBySlugIter.get(`${iter}-${step.agent_slug}`);
      const taskArtifacts = task ? (artifactsByTaskId.get(task.id) ?? []) : [];
      return { kind: 'agent', step, task, artifacts: taskArtifacts };
    });
    rows.push({ nodes: agentNodes, parallel: agentNodes.length > 1 });

    // Summary after this iteration if exists
    const summary = summaryArtifacts.find(a => {
      // Match by created_at proximity to last step in this iteration
      const lastStep = steps[steps.length - 1];
      return lastStep && new Date(a.created_at) > new Date(lastStep.started_at);
    });
    if (summary && !rows.some(r => r.nodes.some(n => n.kind === 'summary' && (n as { kind: 'summary'; artifact: ArtifactOut }).artifact.id === summary.id))) {
      rows.push({ nodes: [{ kind: 'summary', artifact: summary }], parallel: false });
    }
  }

  // Final answer node
  if (finalAnswer) {
    rows.push({ nodes: [{ kind: 'answer', artifact: finalAnswer }], parallel: false });
  }

  return rows;
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
            {step.input?.message && (
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

function NodeGraph({ rows }: { rows: GraphRow[] }) {
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
          {/* Connector above every row except the first */}
          {rowI > 0 && <Arrow />}

          {/* Row: single node or parallel group */}
          {row.parallel ? (
            <div style={{ display: 'flex', gap: '12px', alignItems: 'flex-start', justifyContent: 'center', flexWrap: 'wrap' }}>
              {row.nodes.map((node, nodeI) => (
                <div key={nodeI} style={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                  {/* Horizontal connector line above parallel nodes */}
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

// ── Run detail modal ──────────────────────────────────────────────────────────

function RunModal({ run, onClose }: { run: Run; onClose: () => void }) {
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [tasks, setTasks] = useState<TaskOut[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactOut[]>([]);
  const [loading, setLoading] = useState(true);
  const [tab, setTab] = useState<'graph' | 'steps' | 'answer'>('graph');

  useEffect(() => {
    Promise.all([
      themApi.runDetail(run.id),
      themApi.runTasks(run.id),
      themApi.runArtifacts(run.id),
    ]).then(([d, t, a]) => {
      setDetail(d);
      setTasks(t);
      setArtifacts(a);
    }).finally(() => setLoading(false));
  }, [run.id]);

  const durationMs = run.duration_ms ?? (detail?.ended_at && detail?.started_at
    ? new Date(detail.ended_at).getTime() - new Date(detail.started_at).getTime()
    : null);

  const graph = detail ? buildGraph(detail, tasks, artifacts) : [];
  const finalAnswer = artifacts.find(a => a.artifact_id === 'final-answer');

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 200,
      background: 'rgba(0,0,0,0.65)', display: 'flex', alignItems: 'center', justifyContent: 'center',
      backdropFilter: 'blur(4px)',
    }} onClick={onClose}>
      <div style={{
        background: 'var(--tm-bg)', border: '1px solid var(--tm-border)',
        borderRadius: '20px', width: '860px', maxWidth: '95vw', maxHeight: '90vh',
        display: 'flex', flexDirection: 'column', overflow: 'hidden',
        boxShadow: '0 24px 80px rgba(0,0,0,0.5)',
      }} onClick={e => e.stopPropagation()}>

        {/* Header */}
        <div style={{
          padding: '20px 28px', borderBottom: '1px solid var(--tm-border)',
          display: 'flex', alignItems: 'flex-start', gap: '16px', flexShrink: 0,
        }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '6px' }}>
              <StatusBadge status={run.status} />
              <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>{run.id.slice(0, 16)}…</span>
            </div>
            <div style={{ fontSize: '16px', fontWeight: 700, color: 'var(--tm-text)', marginBottom: '4px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {run.user_message || run.goal || 'No message'}
            </div>
            <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>
              {run.orchestrator_name} · {formatTs(run.started_at)} · {formatDuration(durationMs)}
              {detail && ` · ${(detail.total_tokens_in ?? 0) + (detail.total_tokens_out ?? 0)} tokens`}
            </div>
          </div>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tm-text-muted)', fontSize: '22px', flexShrink: 0, lineHeight: 1, padding: '2px' }}>✕</button>
        </div>

        {/* Tabs */}
        <div style={{ display: 'flex', gap: '4px', padding: '12px 28px 0', borderBottom: '1px solid var(--tm-border)', flexShrink: 0 }}>
          {([['graph', 'account_tree', 'Flow'], ['steps', 'list', 'Steps'], ['answer', 'chat', 'Answer']] as const).map(([t, icon, label]) => (
            <button key={t} onClick={() => setTab(t)} style={{
              padding: '8px 16px', borderRadius: '8px 8px 0 0', border: 'none',
              background: tab === t ? 'var(--tm-surface)' : 'transparent',
              color: tab === t ? 'var(--tm-accent)' : 'var(--tm-text-muted)',
              fontWeight: tab === t ? 700 : 400, fontSize: '13px', cursor: 'pointer',
              display: 'flex', alignItems: 'center', gap: '6px',
              borderBottom: tab === t ? '2px solid var(--tm-accent)' : '2px solid transparent',
            }}>
              <span className="material-symbols-outlined" style={{ fontSize: '15px' }}>{icon}</span>
              {label}
            </button>
          ))}
        </div>

        {/* Content */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '24px 28px' }}>
          {loading ? (
            <div style={{ textAlign: 'center', color: 'var(--tm-text-muted)', padding: '60px', fontSize: '14px' }}>Loading run data…</div>
          ) : (
            <>
              {/* Graph tab */}
              {tab === 'graph' && (
                <div>
                  <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', textAlign: 'center', marginBottom: '16px' }}>
                    Click any node to expand · Parallel agents run at the same level
                  </div>
                  {graph.length > 0
                    ? <NodeGraph rows={graph} />
                    : <div style={{ textAlign: 'center', color: 'var(--tm-text-muted)', padding: '40px' }}>No step data available for this run</div>
                  }
                </div>
              )}

              {/* Steps tab */}
              {tab === 'steps' && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                  {detail?.steps.length === 0 && <div style={{ color: 'var(--tm-text-muted)', fontSize: '13px' }}>No steps recorded</div>}
                  {detail?.steps.map((step, i) => (
                    <div key={i} style={{
                      border: '1px solid var(--tm-border)', borderRadius: '10px',
                      background: 'var(--tm-surface)', overflow: 'hidden',
                    }}>
                      <div style={{ padding: '10px 14px', display: 'flex', alignItems: 'center', gap: '10px' }}>
                        <span style={{
                          fontSize: '10px', fontWeight: 700, padding: '2px 6px', borderRadius: '4px',
                          background: `${statusColor(step.status)}18`, color: statusColor(step.status),
                        }}>{step.status}</span>
                        <span style={{ fontSize: '13px', fontWeight: 600, color: 'var(--tm-text)' }}>{step.agent_slug}</span>
                        <span style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>iter {step.iteration}</span>
                        {step.latency_ms != null && (
                          <span style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{step.latency_ms}ms</span>
                        )}
                      </div>
                      {(step.input?.message || step.output) && (
                        <div style={{ padding: '0 14px 12px', display: 'flex', flexDirection: 'column', gap: '6px' }}>
                          {step.input?.message && (
                            <div>
                              <div style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '3px' }}>Input</div>
                              <pre style={{ fontSize: '11px', color: 'var(--tm-text)', background: 'var(--tm-surface-2)', borderRadius: '6px', padding: '8px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, maxHeight: '100px', overflowY: 'auto' }}>
                                {String(step.input.message)}
                              </pre>
                            </div>
                          )}
                          {step.output && (
                            <div>
                              <div style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '3px' }}>Output</div>
                              <pre style={{ fontSize: '11px', color: 'var(--tm-text)', background: 'var(--tm-surface-2)', borderRadius: '6px', padding: '8px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, maxHeight: '100px', overflowY: 'auto' }}>
                                {step.output}
                              </pre>
                            </div>
                          )}
                          {step.error && <div style={{ fontSize: '11px', color: '#f87171' }}>Error: {step.error}</div>}
                        </div>
                      )}
                    </div>
                  ))}
                  {/* Usage breakdown */}
                  {detail?.usage && detail.usage.length > 0 && (
                    <div style={{ marginTop: '16px', padding: '14px', borderRadius: '10px', background: 'var(--tm-surface)', border: '1px solid var(--tm-border)' }}>
                      <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '10px' }}>Token Usage</div>
                      {detail.usage.map((u, i) => (
                        <div key={i} style={{ display: 'flex', gap: '16px', fontSize: '12px', color: 'var(--tm-text)', marginBottom: '4px' }}>
                          <span style={{ color: 'var(--tm-text-muted)', minWidth: '120px' }}>{u.provider} / {u.model}</span>
                          <span>{u.tokens_input.toLocaleString()} in</span>
                          <span>{u.tokens_output.toLocaleString()} out</span>
                          <span style={{ color: '#4edea3', marginLeft: 'auto' }}>${Number(u.cost_usd).toFixed(6)}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}

              {/* Answer tab */}
              {tab === 'answer' && (
                <div>
                  {finalAnswer ? (
                    <div style={{ fontSize: '14px', color: 'var(--tm-text)', lineHeight: 1.7, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                      {finalAnswer.parts.find(p => p.text)?.text ?? ''}
                    </div>
                  ) : detail?.final_output ? (
                    <div style={{ fontSize: '14px', color: 'var(--tm-text)', lineHeight: 1.7, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                      {detail.final_output}
                    </div>
                  ) : (
                    <div style={{ color: 'var(--tm-text-muted)', fontSize: '13px' }}>No final answer recorded for this run</div>
                  )}
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function RunsPage() {
  const [runs, setRuns] = useState<Run[]>([]);
  const [stats, setStats] = useState<{ total: number; by_status: Record<string, number>; total_cost_usd: number } | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedRun, setSelectedRun] = useState<Run | null>(null);

  useEffect(() => {
    Promise.allSettled([themApi.runs(50), themApi.runStats()]).then(([r, s]) => {
      if (r.status === 'fulfilled') setRuns(Array.isArray(r.value) ? r.value : (r.value?.items ?? []));
      if (s.status === 'fulfilled') setStats(s.value);
      setLoading(false);
    });
  }, []);

  const closeModal = useCallback(() => setSelectedRun(null), []);

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1 }}>
          <header style={{
            position: 'sticky', top: 0, zIndex: 30, height: '56px',
            background: 'var(--tm-topbar)', borderBottom: '1px solid var(--tm-topbar-border)',
            display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 32px',
          }}>
            <div>
              <h2 style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-accent)' }}>Run History</h2>
              <p style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>Orchestration run log — click any row to inspect</p>
            </div>
            {stats && (
              <div style={{ display: 'flex', gap: '24px', alignItems: 'center' }}>
                <div style={{ textAlign: 'right' }}>
                  <p style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-text)' }}>{stats.total}</p>
                  <p style={{ fontSize: '10px', color: 'var(--tm-text-muted)', textTransform: 'uppercase' }}>Total runs</p>
                </div>
                <div style={{ textAlign: 'right' }}>
                  <p style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-text)' }}>${Number(stats.total_cost_usd).toFixed(4)}</p>
                  <p style={{ fontSize: '10px', color: 'var(--tm-text-muted)', textTransform: 'uppercase' }}>Total cost</p>
                </div>
              </div>
            )}
          </header>

          <div style={{ padding: '32px' }}>
            {stats && Object.keys(stats.by_status).length > 0 && (
              <div style={{ display: 'flex', gap: '8px', marginBottom: '24px' }}>
                {Object.entries(stats.by_status).map(([status, count]) => (
                  <div key={status} style={{
                    display: 'flex', alignItems: 'center', gap: '6px',
                    padding: '6px 12px', borderRadius: '20px',
                    background: 'var(--tm-surface)', border: '1px solid var(--tm-border)',
                  }}>
                    <StatusBadge status={status} />
                    <span style={{ fontSize: '13px', fontWeight: 600, color: 'var(--tm-text)' }}>{count as number}</span>
                  </div>
                ))}
              </div>
            )}

            <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: '12px', overflow: 'hidden' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--tm-border)' }}>
                    {['Message', 'Orchestrator', 'Status', 'Started', 'Duration', 'Tokens', 'Cost', ''].map((h) => (
                      <th key={h} style={{
                        padding: '10px 16px', textAlign: 'left',
                        fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)',
                        textTransform: 'uppercase', letterSpacing: '0.06em',
                        background: 'var(--tm-surface-2)',
                      }}>{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {loading && (
                    <tr><td colSpan={8} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>Loading…</td></tr>
                  )}
                  {!loading && runs.length === 0 && (
                    <tr><td colSpan={8} style={{ padding: '48px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>
                      <span className="material-symbols-outlined" style={{ fontSize: '40px', display: 'block', marginBottom: '8px', opacity: 0.3 }}>history</span>
                      No runs yet
                    </td></tr>
                  )}
                  {runs.map((run, i) => (
                    <tr key={run.id}
                      onClick={() => setSelectedRun(run)}
                      style={{ borderBottom: i < runs.length - 1 ? '1px solid var(--tm-border-subtle)' : 'none', cursor: 'pointer' }}
                      onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--tm-surface-2)')}
                      onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
                    >
                      <td style={{ padding: '12px 16px', maxWidth: '240px' }}>
                        <p style={{ fontSize: '13px', fontWeight: 500, color: 'var(--tm-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {run.user_message || run.goal || <em style={{ color: 'var(--tm-text-subtle)' }}>no message</em>}
                        </p>
                        <p style={{ fontSize: '10px', color: 'var(--tm-text-subtle)', fontFamily: 'monospace' }}>{run.id.slice(0, 8)}…</p>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{ fontSize: '13px', color: 'var(--tm-text-2)' }}>{run.orchestrator_name}</span>
                      </td>
                      <td style={{ padding: '12px 16px' }}><StatusBadge status={run.status} /></td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>{formatTs(run.started_at)}</span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{ fontSize: '13px', color: 'var(--tm-text-2)' }}>{formatDuration(run.duration_ms)}</span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{ fontSize: '13px', color: 'var(--tm-text-2)' }}>{run.total_tokens?.toLocaleString() ?? '—'}</span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{ fontSize: '13px', color: 'var(--tm-text-2)' }}>
                          {run.cost_usd != null ? `$${Number(run.cost_usd).toFixed(4)}` : '—'}
                        </span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: '16px', color: 'var(--tm-text-muted)' }}>chevron_right</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </main>

        {selectedRun && <RunModal run={selectedRun} onClose={closeModal} />}
      </div>
    </AuthGuard>
  );
}
