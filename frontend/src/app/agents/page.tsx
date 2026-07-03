'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { odinApi, type Agent } from '@/lib/api';

export default function AgentsPage() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');

  useEffect(() => {
    odinApi.agents().then(setAgents).finally(() => setLoading(false));
  }, []);

  const filtered = agents.filter((a) =>
    (a.display_name ?? '').toLowerCase().includes(search.toLowerCase()) ||
    a.slug.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1 }}>
          {/* Top bar */}
          <header style={{
            position: 'sticky', top: 0, zIndex: 30, height: '56px',
            background: 'var(--tm-topbar)', borderBottom: '1px solid var(--tm-topbar-border)',
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            padding: '0 32px',
          }}>
            <div>
              <h2 style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-accent)' }}>Agents</h2>
              <p style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>Registered agent transport connectors</p>
            </div>
          </header>

          <div style={{ padding: '32px' }}>
            {/* Search bar */}
            <div style={{ marginBottom: '24px', display: 'flex', gap: '12px' }}>
              <div style={{ position: 'relative', flex: 1, maxWidth: '400px' }}>
                <span className="material-symbols-outlined" style={{
                  position: 'absolute', left: '12px', top: '50%', transform: 'translateY(-50%)',
                  fontSize: '18px', color: 'var(--tm-text-muted)',
                }}>search</span>
                <input
                  type="search" placeholder="Search agents…" value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  style={{
                    width: '100%', padding: '8px 12px 8px 40px', borderRadius: '8px',
                    border: '1px solid var(--tm-border)', background: 'var(--tm-surface)',
                    fontSize: '14px', color: 'var(--tm-text)',
                  }}
                />
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <span style={{ fontSize: '13px', color: 'var(--tm-text-muted)' }}>{filtered.length} agents</span>
              </div>
            </div>

            {/* Table */}
            <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: '12px', overflow: 'hidden' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--tm-border)' }}>
                    {['Name', 'Slug', 'Transport', 'Endpoint', 'Concurrency', 'Status'].map((h) => (
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
                    <tr><td colSpan={6} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>Loading…</td></tr>
                  )}
                  {!loading && filtered.length === 0 && (
                    <tr><td colSpan={6} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>No agents found</td></tr>
                  )}
                  {filtered.map((agent, i) => (
                    <tr key={agent.id}
                      style={{ borderBottom: i < filtered.length - 1 ? '1px solid var(--tm-border-subtle)' : 'none' }}
                      onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--tm-surface-2)')}
                      onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
                    >
                      <td style={{ padding: '12px 16px' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                          <div style={{
                            width: '32px', height: '32px', borderRadius: '8px', flexShrink: 0,
                            background: 'var(--tm-accent-bg)', color: 'var(--tm-accent)',
                            display: 'flex', alignItems: 'center', justifyContent: 'center',
                          }}>
                            <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>smart_toy</span>
                          </div>
                          <span style={{ fontSize: '14px', fontWeight: 600, color: 'var(--tm-text)' }}>{agent.display_name}</span>
                        </div>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <code style={{ fontSize: '12px', color: 'var(--tm-text-muted)', background: 'var(--tm-surface-2)', padding: '2px 6px', borderRadius: '4px' }}>
                          {agent.slug}
                        </code>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{
                          fontSize: '10px', fontWeight: 700, padding: '2px 8px', borderRadius: '4px',
                          background: 'var(--tm-accent-bg)', color: 'var(--tm-accent)',
                          textTransform: 'uppercase',
                        }}>{agent.transport}</span>
                      </td>
                      <td style={{ padding: '12px 16px', maxWidth: '180px' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'block' }}>
                          {agent.endpoint_url}
                        </span>
                      </td>
                      <td style={{ padding: '12px 16px', textAlign: 'center' }}>
                        <span style={{ fontSize: '13px', color: 'var(--tm-text-2)' }}>{agent.max_concurrency}</span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{
                          fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px',
                          background: agent.enabled ? 'rgba(0,118,80,.12)' : 'rgba(107,114,128,.1)',
                          color: agent.enabled ? '#005b3d' : '#6b7280',
                        }}>
                          {agent.enabled ? 'Enabled' : 'Disabled'}
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
