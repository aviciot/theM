'use client';
import { useEffect, useState } from 'react';
import { themApi, type MCPServer } from '@/lib/api';
import { ACCENT, ACCENT_BORDER, inputStyle, labelStyle, slugify } from './mcpConstants';

export function MCPCreateModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (s: MCPServer) => void;
}) {
  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [slugTouched, setSlugTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [transport, setTransport] = useState<MCPServer['transport']>('streamable-http');
  const [url, setUrl] = useState('');
  const [authType, setAuthType] = useState<MCPServer['auth_type']>('none');
  const [probeToken, setProbeToken] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!slugTouched) setSlug(slugify(name));
  }, [name, slugTouched]);

  async function handleCreate() {
    if (!name.trim() || !url.trim()) { setError('Name and URL are required.'); return; }
    setSaving(true);
    setError('');
    try {
      const body: Parameters<typeof themApi.createMCPServer>[0] = {
        name: name.trim(), slug: slug.trim() || slugify(name),
        description, transport, url: url.trim(), auth_type: authType,
      };
      if (probeToken) body.probe_token = probeToken;
      const created = await themApi.createMCPServer(body);
      onCreated(created);
    } catch (e) {
      setError((e as Error).message || 'Create failed');
    } finally {
      setSaving(false);
    }
  }

  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 200, background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center' }} onClick={e => { if (e.target === e.currentTarget) onClose(); }}>
      <div style={{ width: '480px', background: 'var(--tm-panel)', borderRadius: '16px', border: '1px solid rgba(255,255,255,0.09)', boxShadow: '0 24px 80px rgba(0,0,0,0.6)', padding: '28px', display: 'flex', flexDirection: 'column', gap: '16px' }}>
        <h2 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '18px', color: ACCENT }}>add_circle</span>
          Add MCP Server
        </h2>

        <div>
          <label style={labelStyle}>Name *</label>
          <input value={name} onChange={e => setName(e.target.value)} style={inputStyle} placeholder="GitHub MCP" autoFocus />
        </div>

        <div>
          <label style={labelStyle}>Slug</label>
          <input value={slug} onChange={e => { setSlug(e.target.value); setSlugTouched(true); }} style={{ ...inputStyle, fontFamily: 'monospace' }} placeholder="github-mcp" />
        </div>

        <div>
          <label style={labelStyle}>Description</label>
          <textarea value={description} onChange={e => setDescription(e.target.value)} rows={2} style={{ ...inputStyle, resize: 'vertical', fontFamily: 'inherit' }} />
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px' }}>
          <div>
            <label style={labelStyle}>Transport</label>
            <select value={transport} onChange={e => setTransport(e.target.value as MCPServer['transport'])} style={{ ...inputStyle }}>
              <option value="streamable-http">streamable-http (recommended)</option>
              <option value="http">http (legacy)</option>
              <option value="sse">sse (legacy)</option>
            </select>
          </div>
          <div>
            <label style={labelStyle}>Auth type</label>
            <select value={authType} onChange={e => setAuthType(e.target.value as MCPServer['auth_type'])} style={{ ...inputStyle }}>
              <option value="none">none</option>
              <option value="bearer">bearer token</option>
              <option value="header">custom header</option>
              <option value="oauth2" disabled>oauth2 (soon)</option>
            </select>
          </div>
        </div>

        <div>
          <label style={labelStyle}>URL *</label>
          <input value={url} onChange={e => setUrl(e.target.value)} style={inputStyle} placeholder="https://my-mcp-server.example.com" />
        </div>

        {authType !== 'none' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
            <div>
              <label style={labelStyle}>Probe token <span style={{ opacity: 0.55, fontWeight: 400 }}>(optional)</span></label>
              <input type="password" value={probeToken} onChange={e => setProbeToken(e.target.value)} style={inputStyle} placeholder={authType === 'bearer' ? 'Bearer token for health probe' : 'Auth header value for health probe'} autoComplete="new-password" />
              <p style={{ margin: '4px 0 0 0', fontSize: '11px', color: 'var(--tm-card-text-muted)' }}>
                Used by the platform health worker to probe this server. Encrypted at rest.
              </p>
            </div>
            <div style={{ padding: '10px 12px', borderRadius: '8px', background: `${ACCENT}0d`, border: `1px solid ${ACCENT_BORDER}`, fontSize: '12px', color: 'var(--tm-card-text-muted)', display: 'flex', gap: '8px', alignItems: 'flex-start' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '15px', color: ACCENT, flexShrink: 0, marginTop: '1px' }}>info</span>
              <span>Application-level credentials are set separately in <strong style={{ color: 'var(--tm-card-text)' }}>Applications → MCP Credentials</strong>.</span>
            </div>
          </div>
        )}

        {error && <div style={{ fontSize: '11px', padding: '6px 10px', borderRadius: '6px', background: 'rgba(220,38,38,0.08)', border: '1px solid rgba(220,38,38,0.2)', color: '#f87171' }}>{error}</div>}

        <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={{ padding: '9px 16px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: 'rgba(100,116,139,0.1)', border: '1px solid rgba(100,116,139,0.2)', color: 'var(--tm-card-text-muted)', cursor: 'pointer' }}>Cancel</button>
          <button onClick={handleCreate} disabled={saving} style={{ padding: '9px 20px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: saving ? 'not-allowed' : 'pointer' }}>
            {saving ? 'Creating…' : 'Add Server'}
          </button>
        </div>
      </div>
    </div>
  );
}
