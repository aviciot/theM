'use client';
import { useCallback, useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi } from '@/lib/api';
import type { TenantObservabilitySummary } from '@/lib/api';

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

function KpiTile({ label, value, sub }: { label: string; value: string | number; sub?: string }) {
  return (
    <div style={{ background: 'var(--tm-card)', border: '1px solid var(--tm-border)', borderRadius: 10, padding: '16px 20px', minWidth: 130 }}>
      <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>{label}</div>
      <div style={{ fontSize: 26, fontWeight: 700, color: 'var(--tm-text)', lineHeight: 1 }}>{value}</div>
      {sub && <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', marginTop: 4 }}>{sub}</div>}
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function ObservabilityPage() {
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

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1, padding: '32px 32px 64px' }}>
          {/* Header */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
            <div>
              <h2 style={{ margin: 0, fontSize: 20, fontWeight: 700, color: 'var(--tm-text)' }}>Observability</h2>
              <div style={{ fontSize: 13, color: 'var(--tm-text-muted)', marginTop: 2 }}>Per-tenant usage and quota summary — last 30 days</div>
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
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((r) => (
                        <tr key={r.tenant_id} style={{ borderTop: '1px solid var(--tm-border)' }}>
                          <td style={{ padding: '12px 16px' }}>
                            <div style={{ fontWeight: 600, color: 'var(--tm-text)' }}>{r.display_name}</div>
                            <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', marginTop: 2, fontFamily: 'monospace' }}>{r.tenant_id}</div>
                          </td>
                          <td style={{ padding: '12px 16px', textAlign: 'right', color: 'var(--tm-text)', fontVariantNumeric: 'tabular-nums' }}>
                            {fmtNum(r.run_count_30d)}
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
                        </tr>
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
