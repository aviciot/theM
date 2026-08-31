'use client';
import { useEffect, useState } from 'react';
import { themApi, type MCPServer } from '@/lib/api';
import { type HealthStatus, ACCENT, ACCENT_BORDER, inputStyle, labelStyle, sectionLabel, nestedSurface, timeAgo } from './mcpConstants';
import { HealthBadge } from './MCPBadges';
import { ToolRow, ProbeButton } from './MCPToolRow';

type PanelTab = 'general' | 'status';

export function MCPPropertiesPanel({
  server,
  onClose,
  onSaved,
  onDeleted,
}: {
  server: MCPServer;
  onClose: () => void;
  onSaved: (s: MCPServer) => void;
  onDeleted: (id: string) => void;
}) {
  const [tab, setTab] = useState<PanelTab>('general');
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDel, setConfirmDel] = useState(false);
  const [error, setError] = useState('');

  const [name, setName] = useState(server.name);
  const [description, setDescription] = useState(server.description || '');
  const [transport, setTransport] = useState(server.transport);
  const [url, setUrl] = useState(server.url);
  const [authType, setAuthType] = useState(server.auth_type);
  const [enabled, setEnabled] = useState(server.enabled);
  const [probeToken, setProbeToken] = useState('');

  useEffect(() => {
    setName(server.name);
    setDescription(server.description || '');
    setTransport(server.transport);
    setUrl(server.url);
    setAuthType(server.auth_type);
    setEnabled(server.enabled);
    setProbeToken('');
    setError('');
    setConfirmDel(false);
    setSaving(false);
  }, [server.id]);

  async function handleSave() {
    setSaving(true);
    setError('');
    try {
      const patch: Parameters<typeof themApi.updateMCPServer>[1] = {
        name, description, transport, url, auth_type: authType, enabled,
      };
      if (probeToken !== '') patch.probe_token = probeToken;
      const updated = await themApi.updateMCPServer(server.id, patch);
      onSaved(updated);
      setProbeToken('');
    } catch (e) {
      setError((e as Error).message || 'Save failed');
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!confirmDel) { setConfirmDel(true); return; }
    setDeleting(true);
    try {
      await themApi.deleteMCPServer(server.id);
      onDeleted(server.id);
    } catch (e) {
      setError((e as Error).message || 'Delete failed');
      setDeleting(false);
    }
  }

  const hs = (server.health_status as HealthStatus) || 'unknown';
  const tools = server.tools_manifest ?? [];

  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 300, background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center' }} onClick={onClose}>
      <div style={{ position: 'relative', background: 'var(--tm-panel)', border: '1px solid var(--tm-modal-border)', borderRadius: '18px', width: '600px', maxHeight: '90vh', display: 'flex', flexDirection: 'column', boxShadow: '0 24px 64px rgba(0,0,0,.55), 0 6px 18px rgba(0,0,0,0.3)' }} onClick={e => e.stopPropagation()}>

        {/* Header */}
        <div style={{ padding: '24px 24px 16px 24px', display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '18px', color: ACCENT }}>electrical_services</span>
              <h2 style={{ fontSize: '16px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{server.name}</h2>
            </div>
            <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: 0, fontFamily: 'monospace' }}>{server.slug}</p>
          </div>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tm-card-text-muted)', padding: '4px', flexShrink: 0 }}>
            <span className="material-symbols-outlined" style={{ fontSize: '18px' }}>close</span>
          </button>
        </div>

        {/* Tabs */}
        <div style={{ display: 'flex', gap: '4px', padding: '12px 24px 0 24px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
          {(['general', 'status'] as PanelTab[]).map(t => (
            <button key={t} onClick={() => setTab(t)} style={{ padding: '6px 14px', borderRadius: '8px 8px 0 0', fontSize: '12px', fontWeight: tab === t ? 700 : 400, background: tab === t ? `${ACCENT}18` : 'transparent', borderTop: `1px solid ${tab === t ? ACCENT_BORDER : 'transparent'}`, borderLeft: `1px solid ${tab === t ? ACCENT_BORDER : 'transparent'}`, borderRight: `1px solid ${tab === t ? ACCENT_BORDER : 'transparent'}`, borderBottom: 'none', color: tab === t ? ACCENT : 'var(--tm-card-text-muted)', cursor: 'pointer', textTransform: 'capitalize' }}>
              {t === 'general' ? 'General' : 'Status & Tools'}
            </button>
          ))}
        </div>

        {/* Tab content */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '16px 24px 24px 24px' }} className="custom-scrollbar">

          {tab === 'general' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={labelStyle}>Name</label>
                <input value={name} onChange={e => setName(e.target.value)} style={inputStyle} />
              </div>
              <div>
                <label style={labelStyle}>Slug <span style={{ opacity: 0.5 }}>(immutable)</span></label>
                <div style={{ ...inputStyle, display: 'inline-block', fontFamily: 'monospace', color: 'var(--tm-card-text-muted)', background: 'rgba(0,0,0,0.2)' }}>{server.slug}</div>
              </div>
              <div>
                <label style={labelStyle}>Description</label>
                <textarea value={description} onChange={e => setDescription(e.target.value)} rows={3} style={{ ...inputStyle, resize: 'vertical', fontFamily: 'inherit' }} />
              </div>
              <div>
                <label style={labelStyle}>Transport</label>
                <select value={transport} onChange={e => setTransport(e.target.value as MCPServer['transport'])} style={{ ...inputStyle }}>
                  <option value="streamable-http">streamable-http (recommended)</option>
                  <option value="http">http (legacy)</option>
                  <option value="sse">sse (legacy)</option>
                </select>
              </div>
              <div>
                <label style={labelStyle}>URL</label>
                <input value={url} onChange={e => setUrl(e.target.value)} style={inputStyle} placeholder="https://my-mcp-server.example.com" />
              </div>
              <div>
                <label style={labelStyle}>Auth type</label>
                <select value={authType} onChange={e => setAuthType(e.target.value as MCPServer['auth_type'])} style={{ ...inputStyle }}>
                  <option value="none">none</option>
                  <option value="bearer">bearer token</option>
                  <option value="header">custom header</option>
                  <option value="oauth2" disabled>oauth2 (coming soon)</option>
                </select>
              </div>
              {authType !== 'none' && (
                <div>
                  <label style={labelStyle}>
                    Probe token
                    {server.probe_credential_set && (
                      <span style={{ marginLeft: '8px', fontSize: '10px', padding: '2px 7px', borderRadius: '4px', background: 'rgba(16,185,129,0.12)', border: '1px solid rgba(16,185,129,0.25)', color: '#34d399', fontWeight: 500 }}>set</span>
                    )}
                  </label>
                  <input type="password" value={probeToken} onChange={e => setProbeToken(e.target.value)} style={inputStyle} placeholder={server.probe_credential_set ? '••••••••  (leave blank to keep current)' : 'Bearer token for health probe'} autoComplete="new-password" />
                  <p style={{ margin: '4px 0 0 0', fontSize: '11px', color: 'var(--tm-card-text-muted)' }}>
                    Used by the platform health worker to authenticate when probing this server. Not shared with application-level credentials.
                  </p>
                </div>
              )}
              <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                <label style={{ ...labelStyle, margin: 0, cursor: 'pointer', userSelect: 'none', display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} style={{ accentColor: ACCENT, width: '14px', height: '14px' }} />
                  Enabled
                </label>
              </div>

              {error && <div style={{ fontSize: '11px', padding: '6px 10px', borderRadius: '6px', background: 'rgba(220,38,38,0.08)', border: '1px solid rgba(220,38,38,0.2)', color: '#f87171' }}>{error}</div>}

              <div style={{ display: 'flex', gap: '8px', marginTop: '4px' }}>
                <button onClick={handleSave} disabled={saving} style={{ flex: 1, padding: '9px', borderRadius: '8px', fontWeight: 600, fontSize: '13px', background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: saving ? 'not-allowed' : 'pointer' }}>
                  {saving ? 'Saving…' : 'Save'}
                </button>
                <button onClick={handleDelete} disabled={deleting} style={{ padding: '9px 14px', borderRadius: '8px', fontWeight: 600, fontSize: '13px', background: confirmDel ? 'rgba(220,38,38,0.18)' : 'rgba(100,116,139,0.1)', border: `1px solid ${confirmDel ? 'rgba(220,38,38,0.4)' : 'rgba(100,116,139,0.2)'}`, color: confirmDel ? '#f87171' : 'var(--tm-card-text-muted)', cursor: deleting ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap' }}>
                  {deleting ? 'Deleting…' : confirmDel ? 'Confirm delete?' : 'Delete'}
                </button>
              </div>
            </div>
          )}

          {tab === 'status' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
              <div style={nestedSurface}>
                <p style={sectionLabel}>Health</p>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '10px' }}>
                  <HealthBadge status={hs} />
                  <span style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)' }}>{timeAgo(server.last_checked_at)}</span>
                </div>
                {server.last_error && (
                  <div style={{ fontSize: '11px', padding: '6px 10px', borderRadius: '6px', background: 'rgba(220,38,38,0.08)', border: '1px solid rgba(220,38,38,0.2)', color: '#f87171', fontFamily: 'monospace', wordBreak: 'break-word' }}>
                    {server.last_error}
                  </div>
                )}
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
                <div style={{ ...nestedSurface, padding: '10px 12px' }}>
                  <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '0 0 4px 0' }}>Tools</p>
                  <p style={{ fontSize: '20px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0 }}>{server.tools_count ?? tools.length}</p>
                </div>
                <div style={{ ...nestedSurface, padding: '10px 12px' }}>
                  <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '0 0 4px 0' }}>Transport</p>
                  <p style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, fontFamily: 'monospace' }}>{server.transport}</p>
                </div>
              </div>

              <div>
                <p style={sectionLabel}>Test connection</p>
                <ProbeButton serverId={server.id} onDone={onSaved} />
              </div>

              {server.capabilities && Object.keys(server.capabilities).length > 0 && (
                <div>
                  <p style={sectionLabel}>Capabilities</p>
                  <pre style={{ fontSize: '10px', color: 'var(--tm-card-text-hint)', margin: 0, background: 'rgba(0,0,0,0.3)', borderRadius: '6px', padding: '8px', overflowX: 'auto', maxHeight: '120px' }}>
                    {JSON.stringify(server.capabilities, null, 2)}
                  </pre>
                </div>
              )}

              <div>
                <p style={sectionLabel}>Tools manifest ({tools.length})</p>
                {tools.length === 0 ? (
                  <div style={{ ...nestedSurface, textAlign: 'center', padding: '20px', color: 'var(--tm-card-text-muted)', fontSize: '12px' }}>
                    No tools discovered yet — run "Test connection" to fetch the manifest.
                  </div>
                ) : (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                    {tools.map(tool => <ToolRow key={tool.name} tool={tool} />)}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
