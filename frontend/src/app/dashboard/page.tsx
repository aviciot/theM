'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Agent, type Run } from '@/lib/api';

function StatCard({ icon, label, value, sub, color }: { icon: string; label: string; value: string | number; sub?: string; color: string }) {
  return (
    <div style={{
      background: 'var(--tm-surface)', border: '1px solid var(--tm-border)',
      borderRadius: '12px', padding: '20px 24px',
      display: 'flex', alignItems: 'flex-start', gap: '16px',
    }}>
      <div style={{
        width: '44px', height: '44px', borderRadius: '10px', flexShrink: 0,
        background: `${color}18`, display: 'flex', alignItems: 'center', justifyContent: 'center',
      }}>
        <span className="material-symbols-outlined" style={{ color, fontSize: '22px' }}>{icon}</span>
      </div>
      <div>
        <div style={{ fontSize: '13px', color: 'var(--tm-text-muted)', marginBottom: '4px' }}>{label}</div>
        <div style={{ fontSize: '28px', fontWeight: 700, color: 'var(--tm-text)', lineHeight: 1 }}>{value}</div>
        {sub && <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)', marginTop: '4px' }}>{sub}</div>}
      </div>
    </div>
  );
}

function RunRow({ run }: { run: Run }) {
  const statusColor: Record<string, string> = {
    completed: '#4edea3', running: '#5b7fff', failed: '#f87171', pending: '#fbbf24',
  };
  const col = statusColor[run.status] ?? '#94a3b8';
  const ts = new Date(run.started_at).toLocaleString();
  return (
    <div style={{
      display: 'grid', gridTemplateColumns: '1fr 140px 100px 160px',
      alignItems: 'center', padding: '12px 20px', gap: '16px',
      borderBottom: '1px solid var(--tm-border)',
    }}>
      <div>
        <div style={{ fontWeight: 500, color: 'var(--tm-text)', fontSize: '13px', marginBottom: '2px' }}>
          {run.orchestrator_name ?? 'Unnamed run'}
        </div>
        <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>
          #{run.id}
        </div>
      </div>
      <div style={{
        display: 'inline-flex', alignItems: 'center', gap: '6px',
        padding: '3px 10px', borderRadius: '20px', fontSize: '12px', fontWeight: 600,
        background: `${col}18`, color: col, width: 'fit-content',
      }}>
        <div style={{ width: '6px', height: '6px', borderRadius: '50%', background: col }} />
        {run.status}
      </div>
      <div style={{ fontSize: '13px', color: 'var(--tm-text-muted)' }}>
        {run.total_tokens ? run.total_tokens.toLocaleString() : '—'} tok
      </div>
      <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>{ts}</div>
    </div>
  );
}

export default function DashboardPage() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [runs, setRuns] = useState<Run[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    Promise.all([
      themApi.agents().catch(() => [] as Agent[]),
      themApi.runs().catch(() => [] as Run[]),
    ])
      .then(([a, r]) => { setAgents(a); setRuns((r as any).items ?? r); })
      .finally(() => setLoading(false));
  }, []);

  const enabledAgents = agents.filter((a) => a.enabled).length;
  const completedRuns = runs.filter((r) => r.status === 'completed').length;
  const failedRuns = runs.filter((r) => r.status === 'failed').length;
  const totalTokens = runs.reduce((s, r) => s + (r.total_tokens ?? 0), 0);

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1 }}>
          {/* Top bar */}
          <header style={{
            position: 'sticky', top: 0, zIndex: 30, height: '56px',
            background: 'var(--tm-topbar)', borderBottom: '1px solid var(--tm-topbar-border)',
            display: 'flex', alignItems: 'center', padding: '0 28px',
            backdropFilter: 'blur(10px)',
          }}>
            <span className="material-symbols-outlined" style={{ color: 'var(--tm-accent)', marginRight: '10px', fontSize: '20px' }}>dashboard</span>
            <h1 style={{ fontSize: '15px', fontWeight: 600, color: 'var(--tm-text)' }}>Command Center</h1>
          </header>

          <div style={{ padding: '28px' }}>
            {/* Stats */}
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: '16px', marginBottom: '28px' }}>
              <StatCard icon="smart_toy" label="Active Agents" value={loading ? '…' : enabledAgents} sub={`${agents.length} total`} color="#5b7fff" />
              <StatCard icon="check_circle" label="Completed Runs" value={loading ? '…' : completedRuns} sub={`${runs.length} total`} color="#4edea3" />
              <StatCard icon="error" label="Failed Runs" value={loading ? '…' : failedRuns} color="#f87171" />
              <StatCard icon="bolt" label="Tokens Used" value={loading ? '…' : totalTokens > 999 ? `${(totalTokens/1000).toFixed(1)}k` : totalTokens} color="#fbbf24" />
            </div>

            {/* Recent runs */}
            <div style={{
              background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: '12px', overflow: 'hidden',
            }}>
              <div style={{ padding: '16px 20px', borderBottom: '1px solid var(--tm-border)', display: 'flex', alignItems: 'center', gap: '8px' }}>
                <span className="material-symbols-outlined" style={{ color: 'var(--tm-accent)', fontSize: '18px' }}>history</span>
                <span style={{ fontWeight: 600, fontSize: '14px', color: 'var(--tm-text)' }}>Recent Runs</span>
              </div>

              {/* Table header */}
              <div style={{
                display: 'grid', gridTemplateColumns: '1fr 140px 100px 160px',
                padding: '8px 20px', gap: '16px',
                background: 'rgba(255,255,255,.02)',
                borderBottom: '1px solid var(--tm-border)',
              }}>
                {['Run', 'Status', 'Tokens', 'Started'].map((h) => (
                  <div key={h} style={{ fontSize: '11px', fontWeight: 600, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>{h}</div>
                ))}
              </div>

              {loading ? (
                <div style={{ padding: '40px', textAlign: 'center', color: 'var(--tm-text-muted)', fontSize: '13px' }}>Loading…</div>
              ) : runs.length === 0 ? (
                <div style={{ padding: '60px', textAlign: 'center' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: '40px', color: 'var(--tm-text-muted)', display: 'block', marginBottom: '12px' }}>history</span>
                  <div style={{ color: 'var(--tm-text-muted)', fontSize: '14px' }}>No runs yet</div>
                </div>
              ) : (
                runs.slice(0, 10).map((r) => <RunRow key={r.id} run={r} />)
              )}
            </div>
          </div>
        </main>
      </div>
    </AuthGuard>
  );
}
