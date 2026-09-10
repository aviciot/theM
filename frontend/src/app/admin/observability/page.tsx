'use client';
import { useCallback, useEffect, useState, type ReactNode } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi } from '@/lib/api';
import type { TenantObservabilitySummary, AppObservabilitySummary } from '@/lib/api';
import { useRequireSuperAdmin } from '@/hooks/useRequireSuperAdmin';

// ── helpers ───────────────────────────────────────────────────────────────────

function fmtNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

function quota(used: number, max: number | null): string {
  if (max == null) return `${fmtNum(used)} / ∞`;
  return `${fmtNum(used)} / ${fmtNum(max)}`;
}

function quotaColor(used: number, max: number | null): string {
  if (max == null || max === 0) return 'var(--tm-text)';
  const pct = used / max;
  if (pct >= 0.9) return '#f87171';
  if (pct >= 0.7) return '#f59e0b';
  return 'var(--tm-text)';
}

// ── KPI tile ──────────────────────────────────────────────────────────────────

function KpiTile({ label, value, sub }: { label: ReactNode; value: string | number; sub?: string }) {
  return (
    <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: 10, padding: '16px 20px', minWidth: 130 }}>
      <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>{label}</div>
      <div style={{ fontSize: 26, fontWeight: 700, color: 'var(--tm-text)', lineHeight: 1 }}>{value}</div>
      {sub && <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', marginTop: 4 }}>{sub}</div>}
    </div>
  );
}

// ── Live badge ────────────────────────────────────────────────────────────────

function LiveBadge() {
  return (
    <span style={{ fontSize: 10, fontWeight: 700, color: '#22c55e', background: 'rgba(34,197,94,0.1)', border: '1px solid rgba(34,197,94,0.3)', borderRadius: 4, padding: '1px 5px', marginLeft: 5, verticalAlign: 'middle', letterSpacing: '0.04em' }}>
      LIVE
    </span>
  );
}

// ── Per-app breakdown row ─────────────────────────────────────────────────────

function AppBreakdownTable({ apps, loading, error }: { apps: AppObservabilitySummary[]; loading: boolean; error: string }) {
  if (loading) {
    return <div style={{ padding: '12px 16px 12px 40px', color: 'var(--tm-text-muted)', fontSize: 13 }}>Loading app breakdown…</div>;
  }
  if (error) {
    return <div style={{ padding: '12px 16px 12px 40px', color: '#f87171', fontSize: 13 }}>{error}</div>;
  }
  if (apps.length === 0) {
    return <div style={{ padding: '12px 16px 12px 40px', color: 'var(--tm-text-muted)', fontSize: 13 }}>No applications.</div>;
  }

  const isLive = apps.some(a => a.is_live);

  return (
    <tr>
      <td colSpan={6} style={{ padding: 0 }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12, background: 'var(--tm-bg)' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid var(--tm-border)' }}>
              <th style={{ textAlign: 'left', padding: '8px 16px 8px 40px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>App</th>
              <th style={{ textAlign: 'right', padding: '8px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>Runs (30d)</th>
              <th style={{ textAlign: 'right', padding: '8px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>Tokens in (30d)</th>
              <th style={{ textAlign: 'right', padding: '8px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>Tokens out (30d)</th>
              <th style={{ textAlign: 'right', padding: '8px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>
                MCP (30d)
              </th>
              <th style={{ textAlign: 'right', padding: '8px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>
                Users today{isLive && <LiveBadge />}
              </th>
            </tr>
          </thead>
          <tbody>
            {apps.map((a) => (
              <tr key={a.application_id} style={{ borderTop: '1px solid var(--tm-border)' }}>
                <td style={{ padding: '8px 16px 8px 40px' }}>
                  <div style={{ fontWeight: 500, color: 'var(--tm-text)' }}>{a.app_name}</div>
                  <div style={{ fontSize: 10, color: 'var(--tm-text-muted)', fontFamily: 'monospace', marginTop: 1 }}>{a.application_id}</div>
                </td>
                <td style={{ padding: '8px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
                  {fmtNum(a.run_count_30d)}
                  {a.is_live && a.runs_today > 0 && (
                    <div style={{ fontSize: 10, color: '#22c55e' }}>+{fmtNum(a.runs_today)} today</div>
                  )}
                </td>
                <td style={{ padding: '8px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
                  {fmtNum(a.tokens_in_30d)}
                  {a.is_live && a.tokens_in_today > 0 && (
                    <div style={{ fontSize: 10, color: '#22c55e' }}>+{fmtNum(a.tokens_in_today)} today</div>
                  )}
                </td>
                <td style={{ padding: '8px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
                  {fmtNum(a.tokens_out_30d)}
                  {a.is_live && a.tokens_out_today > 0 && (
                    <div style={{ fontSize: 10, color: '#22c55e' }}>+{fmtNum(a.tokens_out_today)} today</div>
                  )}
                </td>
                <td style={{ padding: '8px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
                  {fmtNum(a.mcp_calls_30d)}
                  {a.is_live && a.mcp_calls_today > 0 && (
                    <div style={{ fontSize: 10, color: '#22c55e' }}>+{fmtNum(a.mcp_calls_today)} today</div>
                  )}
                </td>
                <td style={{ padding: '8px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
                  {a.is_live ? fmtNum(a.active_users_today) : '—'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </td>
    </tr>
  );
}

// ── Expandable tenant row ─────────────────────────────────────────────────────

function TenantRow({ r }: { r: TenantObservabilitySummary }) {
  const [expanded, setExpanded] = useState(false);
  const [apps, setApps] = useState<AppObservabilitySummary[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const expand = useCallback(async () => {
    if (expanded) {
      setExpanded(false);
      return;
    }
    setExpanded(true);
    if (apps.length > 0) return; // already loaded
    setLoading(true);
    setError('');
    try {
      const data = await themApi.getAppObservabilityBreakdown(r.tenant_id);
      setApps(data ?? []);
    } catch (e) {
      setError((e as Error).message ?? 'Failed to load app breakdown');
    } finally {
      setLoading(false);
    }
  }, [expanded, apps.length, r.tenant_id]);

  return (
    <>
      <tr
        key={r.tenant_id}
        style={{ borderTop: '1px solid var(--tm-border)', cursor: 'pointer' }}
        onClick={expand}
        title="Click to expand app breakdown"
      >
        <td style={{ padding: '12px 16px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span
              className="material-symbols-outlined"
              style={{ fontSize: 14, color: 'var(--tm-text-muted)', transition: 'transform 0.15s', transform: expanded ? 'rotate(90deg)' : 'none' }}
            >
              chevron_right
            </span>
            <div>
              <div style={{ fontWeight: 600, color: 'var(--tm-text)' }}>{r.display_name}</div>
              <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', marginTop: 2, fontFamily: 'monospace' }}>{r.tenant_id}</div>
            </div>
          </div>
        </td>
        <td style={{ padding: '12px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
          {fmtNum(r.run_count_30d)}
          {r.is_live && r.runs_today > 0 && (
            <div style={{ fontSize: 10, color: '#22c55e' }}>+{fmtNum(r.runs_today)} today</div>
          )}
        </td>
        <td style={{ padding: '12px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
          {fmtNum(r.total_llm_tokens_30d)}
        </td>
        <td style={{ padding: '12px 16px', textAlign: 'right', fontVariantNumeric: 'tabular-nums', color: quotaColor(r.agent_count, r.max_agents) }}>
          {quota(r.agent_count, r.max_agents)}
        </td>
        <td style={{ padding: '12px 16px', textAlign: 'right', fontVariantNumeric: 'tabular-nums', color: quotaColor(r.app_count, r.max_apps) }}>
          {quota(r.app_count, r.max_apps)}
        </td>
        <td style={{ padding: '12px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
          {r.is_live ? fmtNum(r.active_users_today) : '—'}
        </td>
      </tr>
      {expanded && (
        loading || error || apps.length >= 0
          ? <AppBreakdownTable apps={apps} loading={loading} error={error} />
          : null
      )}
    </>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function ObservabilityPage() {
  useRequireSuperAdmin();
  const [rows, setRows] = useState<TenantObservabilitySummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await themApi.getObservabilitySummary();
      setRows(data ?? []);
    } catch (e) {
      setError((e as Error).message ?? 'Failed to load');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const totalRuns = rows.reduce((s, r) => s + r.run_count_30d, 0);
  const totalTokens = rows.reduce((s, r) => s + r.total_llm_tokens_30d, 0);
  const totalRunsToday = rows.reduce((s, r) => s + r.runs_today, 0);
  const totalUsersToday = rows.reduce((s, r) => s + r.active_users_today, 0);
  const anyLive = rows.some(r => r.is_live);

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1, padding: '32px 32px 64px' }}>
          {/* Header */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
            <div>
              <h2 style={{ margin: 0, fontSize: 20, fontWeight: 700, color: 'var(--tm-text)' }}>Observability</h2>
              <div style={{ fontSize: 13, color: 'var(--tm-text-muted)', marginTop: 2 }}>Per-tenant usage and quota summary — last 30 days. Click a row to see per-app breakdown.</div>
            </div>
            <button onClick={load} title="Refresh" style={{ padding: '6px 10px', borderRadius: 7, border: '1px solid var(--tm-border)', background: 'var(--tm-card)', color: 'var(--tm-text-muted)', cursor: 'pointer', display: 'flex', alignItems: 'center' }}>
              <span className="material-symbols-outlined" style={{ fontSize: 18 }}>refresh</span>
            </button>
          </div>

          {/* Platform KPI row */}
          {!loading && !error && rows.length > 0 && (
            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 28 }}>
              <KpiTile label="Tenants" value={rows.length} />
              <KpiTile label="Runs (30d)" value={fmtNum(totalRuns)} sub="across all tenants" />
              <KpiTile label="LLM tokens (30d)" value={fmtNum(totalTokens)} sub="across all tenants" />
              {anyLive && (
                <>
                  <KpiTile label={<>Runs today{<LiveBadge />}</>} value={fmtNum(totalRunsToday)} sub="across all tenants" />
                  <KpiTile label={<>Active users today{<LiveBadge />}</>} value={fmtNum(totalUsersToday)} sub="across all tenants" />
                </>
              )}
            </div>
          )}

          {/* Status */}
          {loading && <div style={{ color: 'var(--tm-text-muted)', fontSize: 14 }}>Loading…</div>}
          {error && <div style={{ color: '#f87171', fontSize: 14 }}>{error}</div>}

          {/* Table */}
          {!loading && !error && (
            rows.length === 0
              ? <div style={{ color: 'var(--tm-text-muted)', fontSize: 14 }}>No tenants found.</div>
              : (
                <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: 10, overflow: 'hidden' }}>
                  <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                    <thead>
                      <tr style={{ background: 'var(--tm-bg)', borderBottom: '1px solid var(--tm-border)' }}>
                        <th style={{ textAlign: 'left', padding: '10px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>Tenant</th>
                        <th style={{ textAlign: 'right', padding: '10px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>Runs (30d)</th>
                        <th style={{ textAlign: 'right', padding: '10px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>LLM Tokens (30d)</th>
                        <th style={{ textAlign: 'right', padding: '10px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>Agents</th>
                        <th style={{ textAlign: 'right', padding: '10px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>Apps</th>
                        <th style={{ textAlign: 'right', padding: '10px 16px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>
                          Users today{anyLive && <LiveBadge />}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((r) => (
                        <TenantRow key={r.tenant_id} r={r} />
                      ))}
                    </tbody>
                  </table>
                </div>
              )
          )}
        </main>
      </div>
    </AuthGuard>
  );
}
