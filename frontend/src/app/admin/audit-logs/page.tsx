'use client';
import { useEffect, useState, useCallback } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi } from '@/lib/api';
import type { AuditLog } from '@/lib/api';

const PAGE_SIZE = 50;

function timeAgo(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const s = Math.floor(diffMs / 1000);
  if (s < 5)  return 'just now';
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function actionBadgeColor(action: string): string {
  if (action.endsWith('.create')) return '#22c55e';
  if (action.endsWith('.delete')) return '#ef4444';
  if (action.endsWith('.update') || action.endsWith('.patch')) return '#f59e0b';
  return '#6366f1';
}

export default function AuditLogsPage() {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [offset, setOffset] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const [expanded, setExpanded] = useState<number | null>(null);

  const load = useCallback(async (off: number) => {
    setLoading(true);
    setError(null);
    try {
      const data = await themApi.getAuditLogs(PAGE_SIZE, off);
      setLogs(data);
      setHasMore(data.length === PAGE_SIZE);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load audit logs');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(offset); }, [load, offset]);

  function prev() { setOffset(o => Math.max(0, o - PAGE_SIZE)); }
  function next() { setOffset(o => o + PAGE_SIZE); }

  const card: React.CSSProperties = {
    background: 'var(--tm-card)', border: '1px solid var(--tm-border)',
    borderRadius: 10, overflow: 'hidden',
  };

  const th: React.CSSProperties = {
    padding: '10px 16px', textAlign: 'left', fontSize: 11,
    fontWeight: 600, color: 'var(--tm-text-muted)', textTransform: 'uppercase',
    letterSpacing: '0.06em', borderBottom: '1px solid var(--tm-border)',
    background: 'var(--tm-sidebar)',
  };

  const td: React.CSSProperties = {
    padding: '10px 16px', fontSize: 13, color: 'var(--tm-text)',
    borderBottom: '1px solid var(--tm-border)', verticalAlign: 'top',
  };

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: 260, flex: 1, padding: '32px 40px', maxWidth: 1200 }}>
          <div style={{ marginBottom: 24 }}>
            <h1 style={{ fontSize: 22, fontWeight: 700, color: 'var(--tm-text)', margin: 0 }}>Audit Logs</h1>
            <p style={{ fontSize: 13, color: 'var(--tm-text-muted)', margin: '4px 0 0' }}>
              Tenant-scoped activity log — admin actions on agents, apps, and configuration.
            </p>
          </div>

          {error && (
            <div style={{ background: '#fee2e2', border: '1px solid #fca5a5', borderRadius: 8, padding: '12px 16px', marginBottom: 20, color: '#991b1b', fontSize: 13 }}>
              {error}
            </div>
          )}

          <div style={card}>
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr>
                  <th style={th}>Time</th>
                  <th style={th}>Action</th>
                  <th style={th}>Entity Type</th>
                  <th style={th}>Entity ID</th>
                  <th style={th}>User</th>
                  <th style={th}>Details</th>
                </tr>
              </thead>
              <tbody>
                {loading && (
                  <tr>
                    <td colSpan={6} style={{ ...td, textAlign: 'center', color: 'var(--tm-text-muted)', padding: '32px' }}>
                      Loading…
                    </td>
                  </tr>
                )}
                {!loading && logs.length === 0 && (
                  <tr>
                    <td colSpan={6} style={{ ...td, textAlign: 'center', color: 'var(--tm-text-muted)', padding: '32px' }}>
                      No audit log entries found.
                    </td>
                  </tr>
                )}
                {!loading && logs.map(log => {
                  const isExpanded = expanded === log.id;
                  return (
                    <tr key={log.id} style={{ cursor: 'default' }}>
                      <td style={{ ...td, whiteSpace: 'nowrap', color: 'var(--tm-text-muted)' }}>
                        <span title={log.created_at}>{timeAgo(log.created_at)}</span>
                      </td>
                      <td style={td}>
                        <span style={{
                          display: 'inline-block', padding: '2px 8px', borderRadius: 4,
                          fontSize: 11, fontWeight: 600, fontFamily: 'monospace',
                          background: actionBadgeColor(log.action) + '22',
                          color: actionBadgeColor(log.action),
                        }}>
                          {log.action}
                        </span>
                      </td>
                      <td style={{ ...td, color: 'var(--tm-text-muted)' }}>{log.entity_type}</td>
                      <td style={{ ...td, fontFamily: 'monospace', fontSize: 11 }}>
                        {log.entity_id ?? '—'}
                      </td>
                      <td style={{ ...td, color: 'var(--tm-text-muted)' }}>
                        {log.user_id ?? '—'}
                      </td>
                      <td style={td}>
                        {Object.keys(log.details ?? {}).length === 0 ? (
                          <span style={{ color: 'var(--tm-text-muted)' }}>—</span>
                        ) : (
                          <button
                            onClick={() => setExpanded(isExpanded ? null : log.id)}
                            style={{
                              background: 'none', border: '1px solid var(--tm-border)',
                              borderRadius: 4, padding: '2px 8px', fontSize: 11,
                              color: 'var(--tm-text-muted)', cursor: 'pointer',
                            }}
                          >
                            {isExpanded ? 'hide' : 'show'}
                          </button>
                        )}
                        {isExpanded && (
                          <pre style={{
                            marginTop: 8, padding: 8, background: 'var(--tm-bg)',
                            borderRadius: 4, fontSize: 11, maxWidth: 400,
                            overflowX: 'auto', color: 'var(--tm-text)',
                          }}>
                            {JSON.stringify(log.details, null, 2)}
                          </pre>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          <div style={{ display: 'flex', gap: 12, alignItems: 'center', marginTop: 16 }}>
            <button
              onClick={prev}
              disabled={offset === 0}
              style={{
                padding: '6px 16px', borderRadius: 6, fontSize: 13,
                background: offset === 0 ? 'var(--tm-border)' : 'var(--tm-accent)',
                color: offset === 0 ? 'var(--tm-text-muted)' : '#fff',
                border: 'none', cursor: offset === 0 ? 'not-allowed' : 'pointer',
              }}
            >
              ← Prev
            </button>
            <span style={{ fontSize: 12, color: 'var(--tm-text-muted)' }}>
              Showing {offset + 1}–{offset + logs.length}
            </span>
            <button
              onClick={next}
              disabled={!hasMore}
              style={{
                padding: '6px 16px', borderRadius: 6, fontSize: 13,
                background: !hasMore ? 'var(--tm-border)' : 'var(--tm-accent)',
                color: !hasMore ? 'var(--tm-text-muted)' : '#fff',
                border: 'none', cursor: !hasMore ? 'not-allowed' : 'pointer',
              }}
            >
              Next →
            </button>
          </div>
        </main>
      </div>
    </AuthGuard>
  );
}
