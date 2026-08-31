'use client';
import { useEffect, useState, useCallback } from 'react';
import { themApi, type MCPServer } from '@/lib/api';
import Sidebar from '@/components/Sidebar';
import { type HealthStatus, ACCENT, ACCENT_BORDER } from './mcpConstants';
import { MCPServerCard } from './MCPServerCard';
import { MCPPropertiesPanel } from './MCPPropertiesPanel';
import { MCPCreateModal } from './MCPCreateModal';

type FilterStatus = 'all' | HealthStatus;

export default function MCPServersPage() {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError]     = useState('');
  const [search, setSearch]   = useState('');
  const [filter, setFilter]   = useState<FilterStatus>('all');
  const [selected, setSelected] = useState<MCPServer | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const list = await themApi.listMCPServers();
      setServers(list ?? []);
    } catch (e) {
      setError((e as Error).message || 'Failed to load MCP servers');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  useEffect(() => {
    if (!selected) return;
    const updated = servers.find(s => s.id === selected.id);
    if (updated) setSelected(updated);
  }, [servers]);

  const filtered = servers.filter(s => {
    if (filter !== 'all' && s.health_status !== filter) return false;
    if (search) {
      const q = search.toLowerCase();
      return s.name.toLowerCase().includes(q) || s.slug.toLowerCase().includes(q) || (s.description || '').toLowerCase().includes(q);
    }
    return true;
  });

  function handleSaved(updated: MCPServer) {
    setServers(prev => prev.map(s => s.id === updated.id ? updated : s));
    setSelected(updated);
  }
  function handleDeleted(id: string) {
    setServers(prev => prev.filter(s => s.id !== id));
    setSelected(null);
  }
  function handleCreated(created: MCPServer) {
    setServers(prev => [created, ...prev]);
    setSelected(created);
    setShowCreate(false);
  }

  const filterPills: { label: string; value: FilterStatus }[] = [
    { label: 'All', value: 'all' },
    { label: 'Healthy', value: 'healthy' },
    { label: 'Degraded', value: 'degraded' },
    { label: 'Unreachable', value: 'unreachable' },
    { label: 'Unknown', value: 'unknown' },
  ];

  const inputStyle: React.CSSProperties = {
    padding: '8px 11px', borderRadius: '8px', fontSize: '13px',
    background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)',
    color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box',
    paddingLeft: '32px', width: '100%',
  };

  return (
    <>
      <Sidebar />
      <main style={{ marginLeft: '260px', height: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--tm-bg)' }}>
        <header style={{ padding: '24px 32px 16px 32px', flexShrink: 0, borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '16px' }}>
            <div>
              <h1 style={{ fontSize: '22px', fontWeight: 800, color: 'var(--tm-card-text)', margin: 0, display: 'flex', alignItems: 'center', gap: '10px' }}>
                <span className="material-symbols-outlined" style={{ fontSize: '22px', color: ACCENT }}>electrical_services</span>
                MCP Store
              </h1>
              <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: '4px 0 0 0' }}>
                Model Context Protocol servers — tools and resources for your agents
              </p>
            </div>
            <button onClick={() => setShowCreate(true)} style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '9px 18px', borderRadius: '10px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: 'pointer' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>add</span>
              Add Server
            </button>
          </div>

          <div style={{ display: 'flex', gap: '10px', alignItems: 'center', flexWrap: 'wrap' }}>
            <div style={{ position: 'relative', flex: '0 0 260px' }}>
              <span className="material-symbols-outlined" style={{ position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', fontSize: '16px', color: 'var(--tm-card-text-muted)', pointerEvents: 'none' }}>search</span>
              <input value={search} onChange={e => setSearch(e.target.value)} placeholder="Search servers…" style={inputStyle} />
            </div>
            <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap' }}>
              {filterPills.map(({ label, value }) => (
                <button key={value} onClick={() => setFilter(value)} className={filter === value ? 'filter-pill filter-pill-active' : 'filter-pill'} style={filter === value ? { borderColor: ACCENT, color: ACCENT, background: `${ACCENT}18` } : {}}>
                  {label}
                </button>
              ))}
            </div>
            <button onClick={load} title="Refresh" style={{ width: '34px', height: '34px', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '8px', background: 'var(--tm-btn-2-bg)', border: '1px solid var(--tm-filter-border)', color: 'var(--tm-card-text-muted)', cursor: 'pointer' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>refresh</span>
            </button>
            <span style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginLeft: '4px' }}>{filtered.length} of {servers.length}</span>
          </div>
        </header>

        <div style={{ flex: 1, overflowY: 'auto', padding: '24px 32px' }} className="custom-scrollbar">
          {loading && (
            <div style={{ textAlign: 'center', padding: '80px 0', color: 'var(--tm-card-text-muted)' }}>
              <span className="material-symbols-outlined spin" style={{ fontSize: '32px', display: 'block', marginBottom: '12px', color: ACCENT }}>sync</span>
              Loading MCP servers…
            </div>
          )}
          {!loading && error && (
            <div style={{ textAlign: 'center', padding: '80px 0' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '32px', color: '#f87171', display: 'block', marginBottom: '12px' }}>error</span>
              <p style={{ color: '#f87171', fontSize: '14px', margin: '0 0 16px 0' }}>{error}</p>
              <button onClick={load} style={{ padding: '8px 16px', borderRadius: '8px', background: 'rgba(248,113,113,0.1)', border: '1px solid rgba(248,113,113,0.3)', color: '#f87171', cursor: 'pointer', fontSize: '13px' }}>Retry</button>
            </div>
          )}
          {!loading && !error && filtered.length === 0 && (
            <div style={{ textAlign: 'center', padding: '80px 0', color: 'var(--tm-card-text-muted)' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '40px', display: 'block', marginBottom: '12px', opacity: 0.3, color: ACCENT }}>electrical_services</span>
              {servers.length === 0
                ? <><p style={{ fontSize: '15px', fontWeight: 600, margin: '0 0 6px 0' }}>No MCP servers yet</p><p style={{ fontSize: '13px', margin: 0 }}>Add your first MCP server to connect tools to your agents.</p></>
                : <><p style={{ fontSize: '15px', fontWeight: 600, margin: '0 0 6px 0' }}>No servers match your filters</p><p style={{ fontSize: '13px', margin: 0 }}>Try adjusting the search or health filter.</p></>
              }
            </div>
          )}
          {!loading && !error && filtered.length > 0 && (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))', gap: '16px', alignContent: 'start' }}>
              {filtered.map(server => (
                <MCPServerCard key={server.id} server={server} selected={selected?.id === server.id} onClick={() => setSelected(prev => prev?.id === server.id ? null : server)} />
              ))}
            </div>
          )}
        </div>

        {selected && <MCPPropertiesPanel server={selected} onClose={() => setSelected(null)} onSaved={handleSaved} onDeleted={handleDeleted} />}
        {showCreate && <MCPCreateModal onClose={() => setShowCreate(false)} onCreated={handleCreated} />}
      </main>
    </>
  );
}
