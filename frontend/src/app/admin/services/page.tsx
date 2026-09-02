'use client';
import { useEffect, useRef, useState, useCallback } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi } from '@/lib/api';
import type { ServicesStats, SecurityScanStats, DailyTrendRow, AppScanRow, RecentJobRow } from '@/lib/api';

// ── helpers ───────────────────────────────────────────────────────────────────

function timeAgo(isoUtc: string): string {
  const diffMs = Date.now() - new Date(isoUtc).getTime();
  const s = Math.floor(diffMs / 1000);
  if (s < 5)  return 'just now';
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function pct(n: number, total: number) {
  if (!total) return '0%';
  return `${((n / total) * 100).toFixed(1)}%`;
}

function fmt(n: number | null | undefined, unit = '') {
  if (n == null) return '—';
  return n.toFixed(1) + unit;
}

// ── KPI tile ──────────────────────────────────────────────────────────────────

function KpiTile({ label, value, sub, color }: { label: string; value: string | number; sub?: string; color?: string }) {
  return (
    <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: 10, padding: '16px 20px', minWidth: 130 }}>
      <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>{label}</div>
      <div style={{ fontSize: 26, fontWeight: 700, color: color ?? 'var(--tm-text)', lineHeight: 1 }}>{value}</div>
      {sub && <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', marginTop: 4 }}>{sub}</div>}
    </div>
  );
}

// ── Bar chart (inline SVG) ────────────────────────────────────────────────────

function TrendBar({ rows }: { rows: DailyTrendRow[] }) {
  if (!rows.length) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 13, padding: 20 }}>No data</div>;
  const maxVal = Math.max(...rows.map(r => r.total), 1);
  const W = 48, GAP = 6, H = 80;
  const totalW = rows.length * (W + GAP) - GAP;
  return (
    <svg width={totalW} height={H + 24} style={{ overflow: 'visible' }}>
      {rows.map((r, i) => {
        const x = i * (W + GAP);
        const cleanH = Math.round((r.clean / maxVal) * H);
        const infH = Math.round((r.infected / maxVal) * H);
        const errH = Math.round((r.error / maxVal) * H);
        const otherH = Math.round(((r.total - r.clean - r.infected - r.error) / maxVal) * H);
        const day = r.day.slice(5); // MM-DD
        let y = H;
        return (
          <g key={r.day}>
            {cleanH > 0 && (() => { y -= cleanH; return <rect x={x} y={y} width={W} height={cleanH} rx={2} fill="#10b981" />; })()}
            {infH > 0 && (() => { y -= infH; return <rect x={x} y={y} width={W} height={infH} rx={2} fill="#f87171" />; })()}
            {errH > 0 && (() => { y -= errH; return <rect x={x} y={y} width={W} height={errH} rx={2} fill="#f59e0b" />; })()}
            {otherH > 0 && (() => { y -= otherH; return <rect x={x} y={y} width={W} height={otherH} rx={2} fill="#475569" />; })()}
            <text x={x + W / 2} y={H + 16} textAnchor="middle" fontSize={10} fill="var(--tm-text-muted)">{day}</text>
          </g>
        );
      })}
    </svg>
  );
}

// ── Scan status badge ─────────────────────────────────────────────────────────

function ScanBadge({ status }: { status: string }) {
  const map: Record<string, [string, string]> = {
    clean:    ['#10b981', 'clean'],
    infected: ['#f87171', 'infected'],
    error:    ['#f59e0b', 'error'],
    pending:  ['#60a5fa', 'pending'],
    disabled: ['#475569', 'disabled'],
    done:     ['#10b981', 'done'],
    failed:   ['#f87171', 'failed'],
  };
  const [color, label] = map[status] ?? ['#94a3b8', status];
  return (
    <span style={{ display: 'inline-block', padding: '2px 8px', borderRadius: 4, fontSize: 11, fontWeight: 600, background: color + '22', color }}>{label}</span>
  );
}

// ── Security tab ──────────────────────────────────────────────────────────────

function SecurityTab({ stats }: { stats: SecurityScanStats }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      {/* KPI row */}
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
        <KpiTile label="Scanned" value={stats.scanned} sub={`of ${stats.total_artifacts} artifacts`} />
        <KpiTile label="Success rate" value={`${stats.success_rate.toFixed(1)}%`} color="#10b981" />
        <KpiTile label="Clean" value={stats.clean} color="#10b981" />
        <KpiTile label="Infected" value={stats.infected} color={stats.infected > 0 ? '#f87171' : undefined} />
        <KpiTile label="Error" value={stats.error} color={stats.error > 0 ? '#f59e0b' : undefined} />
        <KpiTile label="Pending" value={stats.pending} />
        <KpiTile label="Avg latency" value={fmt(stats.avg_latency_ms, ' ms')} />
        <KpiTile label="p95 latency" value={fmt(stats.p95_latency_ms, ' ms')} />
      </div>

      {/* Quarantine health */}
      <div style={{ background: 'var(--tm-card)', border: `1px solid ${(stats.quarantine_expired ?? 0) > 0 ? 'rgba(245,158,11,0.4)' : 'var(--tm-border)'}`, borderRadius: 10, padding: '14px 20px' }}>
        <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 10, color: 'var(--tm-text)' }}>Quarantine</div>
        <div style={{ display: 'flex', gap: 24, flexWrap: 'wrap', alignItems: 'center' }}>
          <div>
            <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>In quarantine</span>
            <div style={{ fontSize: 22, fontWeight: 700, color: 'var(--tm-text)', marginTop: 2 }}>{stats.quarantine_total ?? 0}</div>
            <div style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>objects (scan in progress)</div>
          </div>
          <div>
            <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>Expired (pending reap)</span>
            <div style={{ fontSize: 22, fontWeight: 700, color: (stats.quarantine_expired ?? 0) > 0 ? '#f59e0b' : 'var(--tm-text)', marginTop: 2 }}>{stats.quarantine_expired ?? 0}</div>
            <div style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>cleaned up every 15 min</div>
          </div>
          {(stats.quarantine_expired ?? 0) === 0 && (stats.quarantine_total ?? 0) === 0 && (
            <div style={{ fontSize: 12, color: '#10b981', display: 'flex', alignItems: 'center', gap: 6 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 16 }}>check_circle</span>
              Quarantine clean
            </div>
          )}
          {(stats.quarantine_expired ?? 0) > 0 && (
            <div style={{ fontSize: 12, color: '#f59e0b', display: 'flex', alignItems: 'center', gap: 6 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 16 }}>warning</span>
              Expired objects awaiting next reaper run
            </div>
          )}
        </div>
      </div>

      {/* Daily trend */}
      <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: 10, padding: 20 }}>
        <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 16, color: 'var(--tm-text)' }}>Daily trend</div>
        <div style={{ overflowX: 'auto' }}>
          <TrendBar rows={stats.daily_trend} />
        </div>
        <div style={{ display: 'flex', gap: 16, marginTop: 12 }}>
          {[['#10b981', 'clean'], ['#f87171', 'infected'], ['#f59e0b', 'error'], ['#475569', 'other']].map(([c, l]) => (
            <div key={l} style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 11, color: 'var(--tm-text-muted)' }}>
              <div style={{ width: 10, height: 10, borderRadius: 2, background: c }} />
              {l}
            </div>
          ))}
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        {/* Per-app breakdown */}
        <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: 10, padding: 20 }}>
          <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 14, color: 'var(--tm-text)' }}>Per-app breakdown</div>
          {stats.app_breakdown.length === 0
            ? <div style={{ color: 'var(--tm-text-muted)', fontSize: 13 }}>No scanned files yet</div>
            : (
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                <thead>
                  <tr style={{ color: 'var(--tm-text-muted)' }}>
                    <th style={{ textAlign: 'left', paddingBottom: 8, fontWeight: 500 }}>App</th>
                    <th style={{ textAlign: 'right', paddingBottom: 8, fontWeight: 500 }}>Scanned</th>
                    <th style={{ textAlign: 'right', paddingBottom: 8, fontWeight: 500 }}>Clean</th>
                    <th style={{ textAlign: 'right', paddingBottom: 8, fontWeight: 500 }}>Error</th>
                  </tr>
                </thead>
                <tbody>
                  {stats.app_breakdown.map((r: AppScanRow) => (
                    <tr key={r.app_id} style={{ borderTop: '1px solid var(--tm-border)' }}>
                      <td style={{ padding: '7px 0', color: 'var(--tm-text)' }}>{r.app_slug}</td>
                      <td style={{ padding: '7px 0', textAlign: 'right', color: 'var(--tm-text-muted)' }}>{r.scanned}</td>
                      <td style={{ padding: '7px 0', textAlign: 'right', color: '#10b981' }}>{r.clean}</td>
                      <td style={{ padding: '7px 0', textAlign: 'right', color: r.error > 0 ? '#f59e0b' : 'var(--tm-text-muted)' }}>{r.error}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
        </div>

        {/* Recent jobs */}
        <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: 10, padding: 20 }}>
          <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 14, color: 'var(--tm-text)' }}>Recent scan jobs</div>
          {stats.recent_jobs.length === 0
            ? <div style={{ color: 'var(--tm-text-muted)', fontSize: 13 }}>No jobs yet</div>
            : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {stats.recent_jobs.map((j: RecentJobRow) => (
                  <div key={j.job_id} style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 12, padding: '6px 0', borderBottom: '1px solid var(--tm-border)' }}>
                    <ScanBadge status={j.outcome ?? j.status} />
                    <span style={{ flex: 1, color: 'var(--tm-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={j.artifact_id}>{j.artifact_id.slice(0, 8)}…</span>
                    <span style={{ color: 'var(--tm-text-muted)' }}>{j.processor}</span>
                    {j.duration_ms != null && <span style={{ color: 'var(--tm-text-muted)' }}>{j.duration_ms}ms</span>}
                    <span style={{ color: 'var(--tm-text-muted)', flexShrink: 0 }}>{new Date(j.created_at).toLocaleString()}</span>
                  </div>
                ))}
              </div>
            )}
        </div>
      </div>
    </div>
  );
}

// ── Window selector ───────────────────────────────────────────────────────────

type Window = '24h' | '7d' | '30d';

function WindowPicker({ value, onChange }: { value: Window; onChange: (w: Window) => void }) {
  return (
    <div style={{ display: 'flex', gap: 4 }}>
      {(['24h', '7d', '30d'] as Window[]).map(w => (
        <button key={w} onClick={() => onChange(w)} style={{
          padding: '4px 12px', borderRadius: 6, border: '1px solid var(--tm-border)',
          background: value === w ? 'var(--tm-accent)' : 'var(--tm-card)',
          color: value === w ? '#fff' : 'var(--tm-text-muted)',
          fontSize: 12, cursor: 'pointer', fontWeight: value === w ? 600 : 400,
        }}>{w}</button>
      ))}
    </div>
  );
}

// ── Service tabs ──────────────────────────────────────────────────────────────
// Add new services here — each tab renders its own stats component.

const SERVICE_TABS = [
  { id: 'security', label: 'Security Scanner', icon: 'security' },
  // future: { id: 'llm', label: 'LLM Gateway', icon: 'psychology' },
  // future: { id: 'mcp', label: 'MCP Servers', icon: 'electrical_services' },
];

// ── Page ──────────────────────────────────────────────────────────────────────

export default function ServicesPage() {
  const [tab, setTab] = useState('security');
  const [timeWindow, setTimeWindow] = useState<Window>('7d');
  const [stats, setStats] = useState<ServicesStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async (showSpinner = true) => {
    if (showSpinner) setLoading(true);
    setError('');
    try {
      const data = await themApi.getServicesStats(timeWindow);
      setStats(data);
    } catch (e) {
      setError((e as Error).message ?? 'Failed to load stats');
    } finally {
      if (showSpinner) setLoading(false);
    }
  }, [timeWindow]);

  useEffect(() => { load(); }, [load]);

  // Live stats invalidation via the dashboard WebSocket.
  // The middleware worker publishes to them:dash:services:stats after each scan
  // job completes. We re-fetch only the stats data — no full page reload.
  const wsRef = useRef<WebSocket | null>(null);
  const wsAliveRef = useRef(true);
  useEffect(() => {
    wsAliveRef.current = true;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const connect = () => {
      if (!wsAliveRef.current) return;
      if (wsRef.current) { wsRef.current.close(); wsRef.current = null; }
      fetch('/api/auth/token')
        .then((r) => r.json())
        .then((data: { token?: string }) => {
          if (!wsAliveRef.current || !data.token) return;
          const wsBase = window.location.origin.replace('http://', 'ws://').replace('https://', 'wss://');
          const ws = new WebSocket(`${wsBase}/ws/dashboard?token=${data.token}`);
          wsRef.current = ws;
          ws.onopen = () => { ws.send(JSON.stringify({ type: 'subscribe', channels: ['services:stats'] })); };
          ws.onmessage = (e) => {
            try {
              const msg = JSON.parse(e.data as string);
              if (msg.type === 'ping' || msg.type === 'subscribed') return;
              if (msg.channel === 'services:stats') {
                if (msg.event?.type === 'services_health') {
                  // Snapshot on subscribe — update badge only, no re-fetch
                  setStats(prev => prev ? { ...prev, worker_up: msg.event.worker_up } : prev);
                } else {
                  // Scan job completed — re-fetch stats silently
                  load(false);
                }
              }
            } catch { /* ignore parse errors */ }
          };
          ws.onerror = () => { /* handled by onclose */ };
          ws.onclose = () => {
            if (wsRef.current === ws) wsRef.current = null;
            if (!wsAliveRef.current) return;
            if (reconnectTimer) clearTimeout(reconnectTimer);
            reconnectTimer = setTimeout(connect, 3000);
          };
        })
        .catch(() => {
          if (!wsAliveRef.current) return;
          if (reconnectTimer) clearTimeout(reconnectTimer);
          reconnectTimer = setTimeout(connect, 3000);
        });
    };

    connect();
    return () => {
      wsAliveRef.current = false;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (wsRef.current) { wsRef.current.close(); wsRef.current = null; }
    };
  // load is stable (useCallback with [timeWindow]), reconnect when timeWindow changes
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1, padding: '32px 32px 64px' }}>
          {/* Header */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
            <div>
              <h2 style={{ margin: 0, fontSize: 20, fontWeight: 700, color: 'var(--tm-text)' }}>Services</h2>
              <div style={{ fontSize: 13, color: 'var(--tm-text-muted)', marginTop: 2 }}>Runtime statistics for platform services</div>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              {/* Worker health badge — derived from them:dash:services:health Redis key (30s TTL) */}
              {!loading && stats && (
                <div style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  padding: '4px 12px', borderRadius: 20,
                  background: stats.worker_up ? 'rgba(16,185,129,0.12)' : 'rgba(248,113,113,0.12)',
                  border: `1px solid ${stats.worker_up ? 'rgba(16,185,129,0.3)' : 'rgba(248,113,113,0.3)'}`,
                }}>
                  <div style={{
                    width: 7, height: 7, borderRadius: '50%',
                    background: stats.worker_up ? '#10b981' : '#f87171',
                    boxShadow: stats.worker_up ? '0 0 0 2px rgba(16,185,129,0.3)' : undefined,
                  }} />
                  <span style={{ fontSize: 12, fontWeight: 600, color: stats.worker_up ? '#10b981' : '#f87171' }}>
                    {stats.worker_up ? 'Scanner online' : 'Scanner offline'}
                  </span>
                </div>
              )}
              <WindowPicker value={timeWindow} onChange={w => setTimeWindow(w)} />
              <button onClick={load} title="Refresh" style={{ padding: '6px 10px', borderRadius: 7, border: '1px solid var(--tm-border)', background: 'var(--tm-card)', color: 'var(--tm-text-muted)', cursor: 'pointer', display: 'flex', alignItems: 'center' }}>
                <span className="material-symbols-outlined" style={{ fontSize: 18 }}>refresh</span>
              </button>
            </div>
          </div>

          {/* Service tabs */}
          <div style={{ display: 'flex', gap: 2, marginBottom: 24, borderBottom: '1px solid var(--tm-border)', paddingBottom: 0 }}>
            {SERVICE_TABS.map(t => (
              <button key={t.id} onClick={() => setTab(t.id)} style={{
                display: 'flex', alignItems: 'center', gap: 6,
                padding: '8px 16px', border: 'none', background: 'none', cursor: 'pointer',
                fontSize: 13, fontWeight: tab === t.id ? 600 : 400,
                color: tab === t.id ? 'var(--tm-accent)' : 'var(--tm-text-muted)',
                borderBottom: tab === t.id ? '2px solid var(--tm-accent)' : '2px solid transparent',
                marginBottom: -1,
              }}>
                <span className="material-symbols-outlined" style={{ fontSize: 16 }}>{t.icon}</span>
                {t.label}
              </button>
            ))}
          </div>

          {/* Content */}
          {loading && <div style={{ color: 'var(--tm-text-muted)', fontSize: 14 }}>Loading…</div>}
          {error && <div style={{ color: '#f87171', fontSize: 14 }}>{error}</div>}
          {!loading && !error && stats && tab === 'security' && <SecurityTab stats={stats.security} />}
        </main>
      </div>
    </AuthGuard>
  );
}
