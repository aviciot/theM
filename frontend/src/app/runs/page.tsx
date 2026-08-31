'use client';
import { useEffect, useState, useCallback } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Run, type RunDetail, type RunStep, type TaskOut, type ArtifactOut } from '@/lib/api';
import { formatDuration, formatTs, buildGraph, statusColor } from './runsTypes';
import { StatusBadge, NodeGraph } from './RunGraph';

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
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '6px', flexWrap: 'wrap' }}>
              <StatusBadge status={run.status} />
              <span title={run.id} style={{ fontSize: '11px', color: 'var(--tm-text-muted)', fontFamily: 'monospace', cursor: 'default' }}>
                run·{run.id.slice(0, 8)}
              </span>
              {(() => {
                const rootTask = tasks.find(t => t.kind === 'root');
                if (!rootTask?.context_id) return null;
                const wfID = `ctx-${rootTask.context_id}`;
                const temporalURL = '/temporal/namespaces/default/workflows?query=' + encodeURIComponent('WorkflowId="' + wfID + '"');
                return (
                  <a
                    href={temporalURL}
                    target="_blank"
                    rel="noopener noreferrer"
                    title={`Open in Temporal UI: ${wfID}`}
                    style={{ fontSize: '11px', color: '#5b7fff', fontFamily: 'monospace', textDecoration: 'none', display: 'flex', alignItems: 'center', gap: '3px' }}
                  >
                    <span className="material-symbols-outlined" style={{ fontSize: '13px' }}>open_in_new</span>
                    {wfID.slice(0, 20)}…
                  </a>
                );
              })()}
            </div>
            <div style={{ fontSize: '16px', fontWeight: 700, color: 'var(--tm-text)', marginBottom: '4px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {run.user_message || run.goal || 'No message'}
            </div>
            <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>
              <span style={{ fontWeight: 600, color: 'var(--tm-text-2)' }}>{run.orchestrator_name}</span>
              {run.parent_run_id && (
                <span style={{ color: '#a78bfa', marginLeft: '6px' }}>↳ child of {run.parent_run_id.slice(0, 8)}…</span>
              )}
              {' · '}{formatTs(run.started_at)} · {formatDuration(durationMs)}
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
                      {(!!step.input?.message || step.output) && (
                        <div style={{ padding: '0 14px 12px', display: 'flex', flexDirection: 'column', gap: '6px' }}>
                          {!!step.input?.message && (
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
  const [canceling, setCanceling] = useState<Set<string>>(new Set());
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [selectAll, setSelectAll] = useState(false);
  const [bulkDeleting, setBulkDeleting] = useState(false);

  const load = useCallback(() => {
    Promise.allSettled([themApi.runs(50), themApi.runStats()]).then(([r, s]) => {
      if (r.status === 'fulfilled') setRuns(r.value ?? []);
      if (s.status === 'fulfilled') setStats(s.value);
      setLoading(false);
    });
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleCancel = useCallback(async (e: React.MouseEvent, runId: string) => {
    e.stopPropagation();
    setCanceling(prev => new Set(prev).add(runId));
    try {
      const updated = await themApi.cancelRun(runId);
      setRuns(prev => prev.map(r => r.id === runId ? { ...r, ...updated } : r));
    } catch {
      // ignore — run may have already finished
    } finally {
      setCanceling(prev => { const s = new Set(prev); s.delete(runId); return s; });
    }
  }, []);

  const handleToggleSelect = useCallback((runId: string, checked: boolean) => {
    setSelected(prev => {
      const next = new Set(prev);
      checked ? next.add(runId) : next.delete(runId);
      return next;
    });
  }, []);

  const handleSelectAll = useCallback((checked: boolean) => {
    setSelectAll(checked);
    if (checked) {
      setSelected(new Set(runs.map(r => r.id)));
    } else {
      setSelected(new Set());
    }
  }, [runs]);

  const handleBulkDelete = useCallback(async () => {
    if (selected.size === 0) return;
    if (!window.confirm(`Delete ${selected.size} run${selected.size !== 1 ? 's' : ''}? This cannot be undone.`)) return;
    setBulkDeleting(true);
    try {
      await themApi.bulkDeleteRuns(Array.from(selected));
      setSelected(new Set());
      setSelectAll(false);
      load();
    } catch (err) {
      alert(`Bulk delete failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBulkDeleting(false);
    }
  }, [selected, load]);

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
            {/* Status filter pills + bulk-delete button row */}
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '24px', flexWrap: 'wrap' }}>
              {stats && Object.keys(stats.by_status).length > 0 && (
                Object.entries(stats.by_status).map(([s, count]) => (
                  <div key={s} style={{
                    display: 'flex', alignItems: 'center', gap: '6px',
                    padding: '6px 12px', borderRadius: '20px',
                    background: 'var(--tm-surface)', border: '1px solid var(--tm-border)',
                  }}>
                    <StatusBadge status={s} />
                    <span style={{ fontSize: '13px', fontWeight: 600, color: 'var(--tm-text)' }}>{count as number}</span>
                  </div>
                ))
              )}
              {selected.size > 0 && (
                <button
                  onClick={handleBulkDelete}
                  disabled={bulkDeleting}
                  style={{
                    marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '6px',
                    padding: '6px 16px', borderRadius: '8px', fontSize: '13px', fontWeight: 600,
                    border: '1px solid #f87171', background: 'rgba(248,113,113,0.12)',
                    color: '#f87171', cursor: bulkDeleting ? 'not-allowed' : 'pointer',
                    opacity: bulkDeleting ? 0.6 : 1, transition: 'opacity 0.15s',
                  }}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: '15px' }}>delete</span>
                  {bulkDeleting ? 'Deleting…' : `Delete selected (${selected.size})`}
                </button>
              )}
            </div>

            <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: '12px', overflow: 'hidden' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--tm-border)' }}>
                    <th style={{
                      padding: '10px 12px', width: '40px',
                      background: 'var(--tm-surface-2)',
                    }}>
                      <input
                        type="checkbox"
                        checked={selectAll}
                        onChange={e => handleSelectAll(e.target.checked)}
                        title="Select all"
                        style={{ cursor: 'pointer' }}
                      />
                    </th>
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
                    <tr><td colSpan={9} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>Loading…</td></tr>
                  )}
                  {!loading && runs.length === 0 && (
                    <tr><td colSpan={9} style={{ padding: '48px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>
                      <span className="material-symbols-outlined" style={{ fontSize: '40px', display: 'block', marginBottom: '8px', opacity: 0.3 }}>history</span>
                      No runs yet
                    </td></tr>
                  )}
                  {runs.map((run, i) => (
                    <tr key={run.id}
                      onClick={() => setSelectedRun(run)}
                      style={{
                        borderBottom: i < runs.length - 1 ? '1px solid var(--tm-border-subtle)' : 'none',
                        cursor: 'pointer',
                        background: selected.has(run.id) ? 'rgba(248,113,113,0.06)' : 'transparent',
                      }}
                      onMouseEnter={(e) => { if (!selected.has(run.id)) e.currentTarget.style.background = 'var(--tm-surface-2)'; }}
                      onMouseLeave={(e) => { e.currentTarget.style.background = selected.has(run.id) ? 'rgba(248,113,113,0.06)' : 'transparent'; }}
                    >
                      <td style={{ padding: '12px 12px', width: '40px' }} onClick={e => e.stopPropagation()}>
                        <input
                          type="checkbox"
                          checked={selected.has(run.id)}
                          onChange={e => handleToggleSelect(run.id, e.target.checked)}
                          style={{ cursor: 'pointer' }}
                        />
                      </td>
                      <td style={{ padding: '12px 16px', maxWidth: '240px' }}>
                        <div style={{ display: 'flex', alignItems: 'flex-start', gap: '6px' }}>
                          {run.parent_run_id && (
                            <span title={`Child of ${run.parent_run_id.slice(0, 8)}…`} style={{ fontSize: '11px', color: '#a78bfa', marginTop: '2px', flexShrink: 0 }}>↳</span>
                          )}
                          <div style={{ minWidth: 0 }}>
                            <p style={{ fontSize: '13px', fontWeight: 500, color: 'var(--tm-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                              {run.user_message || run.goal || <em style={{ color: 'var(--tm-text-subtle)' }}>no message</em>}
                            </p>
                            <p style={{ fontSize: '10px', color: 'var(--tm-text-subtle)', fontFamily: 'monospace' }}>{run.id.slice(0, 8)}…</p>
                          </div>
                        </div>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <div>
                          <span style={{ fontSize: '13px', color: 'var(--tm-text-2)' }}>{run.orchestrator_name}</span>
                          {run.parent_run_id && (
                            <div style={{ fontSize: '10px', color: '#a78bfa', marginTop: '2px' }}>
                              child · {run.parent_run_id.slice(0, 8)}…
                            </div>
                          )}
                        </div>
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
                      <td style={{ padding: '12px 16px', display: 'flex', alignItems: 'center', gap: 8 }}>
                        {run.status === 'running' && (
                          <button
                            onClick={(e) => handleCancel(e, run.id)}
                            disabled={canceling.has(run.id)}
                            title="Force cancel this run"
                            style={{
                              padding: '3px 8px', borderRadius: 5, fontSize: 11, fontWeight: 600,
                              border: '1px solid #f87171', background: 'rgba(248,113,113,0.1)',
                              color: '#f87171', cursor: 'pointer', opacity: canceling.has(run.id) ? 0.5 : 1,
                            }}
                          >
                            {canceling.has(run.id) ? '…' : 'Cancel'}
                          </button>
                        )}
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
