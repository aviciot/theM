'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { odinApi, type Run, type RunStats } from '@/lib/api';

function StatusBadge({ status }: { status: string }) {
  const map: Record<string, { bg: string; color: string }> = {
    completed: { bg: 'rgba(0,118,80,.12)', color: '#005b3d' },
    failed:    { bg: 'rgba(220,38,38,.1)',  color: '#dc2626' },
    running:   { bg: 'rgba(22,43,232,.1)',   color: 'var(--tm-accent)' },
    pending:   { bg: 'rgba(245,158,11,.1)',  color: '#d97706' },
  };
  const s = map[status] || { bg: 'rgba(107,114,128,.1)', color: '#6b7280' };
  return (
    <span style={{ fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px', background: s.bg, color: s.color }}>
      {status}
    </span>
  );
}

export default function RunsPage() {
  const [runs, setRuns] = useState<Run[]>([]);
  const [stats, setStats] = useState<RunStats | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    Promise.allSettled([odinApi.runs(50), odinApi.runStats()]).then(([r, s]) => {
      if (r.status === 'fulfilled') setRuns(r.value?.items ?? []);
      if (s.status === 'fulfilled') setStats(s.value);
      setLoading(false);
    });
  }, []);

  function formatDuration(ms: number | null) {
    if (ms == null) return '—';
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(1)}s`;
  }

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1 }}>
          <header style={{
            position: 'sticky', top: 0, zIndex: 30, height: '56px',
            background: 'var(--tm-topbar)', borderBottom: '1px solid var(--tm-topbar-border)',
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            padding: '0 32px',
          }}>
            <div>
              <h2 style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-accent)' }}>Run History</h2>
              <p style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>Orchestration run log</p>
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
            {/* Status summary chips */}
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

            {/* Table */}
            <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: '12px', overflow: 'hidden' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--tm-border)' }}>
                    {['Message', 'Orchestrator', 'Status', 'Started', 'Duration', 'Tokens', 'Cost'].map((h) => (
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
                    <tr><td colSpan={7} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>Loading…</td></tr>
                  )}
                  {!loading && runs.length === 0 && (
                    <tr><td colSpan={7} style={{ padding: '48px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>
                      <span className="material-symbols-outlined" style={{ fontSize: '40px', display: 'block', marginBottom: '8px', opacity: 0.3 }}>history</span>
                      No runs yet
                    </td></tr>
                  )}
                  {runs.map((run, i) => (
                    <tr key={run.id}
                      style={{ borderBottom: i < runs.length - 1 ? '1px solid var(--tm-border-subtle)' : 'none' }}
                      onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--tm-surface-2)')}
                      onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
                    >
                      <td style={{ padding: '12px 16px', maxWidth: '240px' }}>
                        <p style={{ fontSize: '13px', fontWeight: 500, color: 'var(--tm-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {run.user_message || <em style={{ color: 'var(--tm-text-subtle)' }}>no message</em>}
                        </p>
                        <p style={{ fontSize: '10px', color: 'var(--tm-text-subtle)', fontFamily: 'monospace' }}>{run.id.slice(0, 8)}…</p>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{ fontSize: '13px', color: 'var(--tm-text-2)' }}>{run.orchestrator_name}</span>
                      </td>
                      <td style={{ padding: '12px 16px' }}><StatusBadge status={run.status} /></td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>
                          {new Date(run.started_at).toLocaleString()}
                        </span>
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
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </main>
      </div>
    </AuthGuard>
  );
}
