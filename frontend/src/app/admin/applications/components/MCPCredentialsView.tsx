'use client';
import { useState, useEffect } from 'react';
import { themApi, type Application, type MCPServer, type MCPCredentialMeta } from '@/lib/api';
import { C, glass } from '../constants';

const ACCENT = '#818cf8';
const ACCENT_BG = 'rgba(129,140,248,0.1)';
const ACCENT_BORDER = 'rgba(129,140,248,0.35)';

const AUTH_COLOR: Record<string, string> = {
  bearer: '#34d399',
  header: '#fbbf24',
  oauth2: '#a78bfa',
  none:   '#64748b',
};

export function MCPCredentialsView({ app, onBack }: { app: Application; onBack: () => void }) {
  const [servers, setServers]     = useState<MCPServer[]>([]);
  const [creds, setCreds]         = useState<MCPCredentialMeta[]>([]);
  const [loading, setLoading]     = useState(true);
  const [error, setError]         = useState('');

  // per-server credential input state
  const [inputs, setInputs]       = useState<Record<string, string>>({});
  const [headerNames, setHeaderNames] = useState<Record<string, string>>({});
  const [saving, setSaving]       = useState<string | null>(null);
  const [deleting, setDeleting]   = useState<string | null>(null);
  const [msgs, setMsgs]           = useState<Record<string, { text: string; ok: boolean }>>({});

  useEffect(() => {
    setLoading(true);
    Promise.all([
      themApi.listMCPServers(),
      themApi.listAppMCPCredentials(app.id),
    ]).then(([srvs, cs]) => {
      setServers((srvs ?? []).filter(s => s.auth_type !== 'none'));
      setCreds(cs ?? []);
    }).catch(e => setError(e instanceof Error ? e.message : 'Failed to load')).finally(() => setLoading(false));
  }, [app.id]);

  function credFor(serverId: string): MCPCredentialMeta | undefined {
    return creds.find(c => c.mcp_server_id === serverId);
  }

  function flash(serverId: string, text: string, ok: boolean) {
    setMsgs(m => ({ ...m, [serverId]: { text, ok } }));
    setTimeout(() => setMsgs(m => { const n = { ...m }; delete n[serverId]; return n; }), 3000);
  }

  async function handleSave(server: MCPServer) {
    const val = (inputs[server.id] ?? '').trim();
    if (!val) return;
    setSaving(server.id);
    try {
      await themApi.setAppMCPCredential(app.id, server.id, {
        credential: val,
        auth_header_name: (headerNames[server.id] ?? '').trim() || 'Authorization',
      });
      const updated = await themApi.listAppMCPCredentials(app.id);
      setCreds(updated ?? []);
      setInputs(p => { const n = { ...p }; delete n[server.id]; return n; });
      flash(server.id, 'Saved', true);
    } catch (e) {
      flash(server.id, e instanceof Error ? e.message : 'Failed', false);
    } finally {
      setSaving(null);
    }
  }

  async function handleDelete(serverId: string) {
    setDeleting(serverId);
    try {
      await themApi.deleteAppMCPCredential(app.id, serverId);
      setCreds(p => p.filter(c => c.mcp_server_id !== serverId));
      flash(serverId, 'Removed', true);
    } catch (e) {
      flash(serverId, e instanceof Error ? e.message : 'Failed', false);
    } finally {
      setDeleting(null);
    }
  }

  const fieldStyle: React.CSSProperties = {
    width: '100%', padding: '9px 12px', borderRadius: 8,
    border: '1px solid rgba(255,255,255,0.12)', background: 'rgba(255,255,255,0.05)',
    color: C.text, fontSize: 13, outline: 'none', boxSizing: 'border-box',
    fontFamily: 'monospace',
  };
  const labelStyle: React.CSSProperties = {
    fontSize: 11, fontWeight: 600, color: C.textMuted,
    letterSpacing: '0.06em', textTransform: 'uppercase', marginBottom: 5, display: 'block',
  };
  const sectionStyle: React.CSSProperties = {
    ...glass, borderRadius: 12, padding: '20px 24px',
    display: 'flex', flexDirection: 'column', gap: 14, marginBottom: 16,
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '40px 40px 60px', background: C.bg }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 32 }}>
        <button onClick={onBack} style={{ background: 'none', border: 'none', color: C.textMuted, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 18 }}>arrow_back</span>
          Applications
        </button>
        <span style={{ color: 'rgba(255,255,255,0.2)', fontSize: 18 }}>/</span>
        <span style={{ fontSize: 14, color: C.text, fontWeight: 600 }}>{app.name}</span>
        <span style={{ color: 'rgba(255,255,255,0.2)', fontSize: 18 }}>/</span>
        <span style={{ fontSize: 14, color: ACCENT, fontWeight: 700 }}>MCP Credentials</span>
      </div>

      <div style={{ maxWidth: 660 }}>
        <p style={{ fontSize: 13, color: C.textMuted, marginBottom: 24, lineHeight: 1.6 }}>
          Set per-application credentials for connected MCP servers. Credentials are encrypted
          at rest and never returned by the API — only whether a key is set is shown.
          Servers with <code style={{ fontFamily: 'monospace', fontSize: 12, color: ACCENT }}>auth_type: none</code> are excluded.
        </p>

        {loading && (
          <div style={{ textAlign: 'center', padding: '60px 0', color: C.textMuted }}>
            <span className="material-symbols-outlined spin" style={{ fontSize: 28, display: 'block', marginBottom: 10, color: ACCENT }}>sync</span>
            Loading…
          </div>
        )}

        {!loading && error && (
          <div style={{ padding: '12px 16px', borderRadius: 10, background: 'rgba(248,113,113,0.08)', border: '1px solid rgba(248,113,113,0.2)', color: '#f87171', fontSize: 13 }}>
            {error}
          </div>
        )}

        {!loading && !error && servers.length === 0 && (
          <div style={{ textAlign: 'center', padding: '60px 0', color: C.textMuted }}>
            <span className="material-symbols-outlined" style={{ fontSize: 36, display: 'block', marginBottom: 10, opacity: 0.3, color: ACCENT }}>electrical_services</span>
            <p style={{ fontSize: 14, fontWeight: 600, marginBottom: 6 }}>No MCP servers require credentials</p>
            <p style={{ fontSize: 13 }}>Add MCP servers with bearer or header auth in MCP Store first.</p>
          </div>
        )}

        {!loading && !error && servers.map(server => {
          const cred = credFor(server.id);
          const input = inputs[server.id] ?? '';
          const headerName = headerNames[server.id] ?? '';
          const isSaving = saving === server.id;
          const isDeleting = deleting === server.id;
          const msg = msgs[server.id];
          const authColor = AUTH_COLOR[server.auth_type] ?? '#64748b';

          return (
            <div key={server.id} style={sectionStyle}>
              {/* Server name + badges */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                <span className="material-symbols-outlined" style={{ fontSize: 16, color: ACCENT }}>electrical_services</span>
                <span style={{ fontSize: 14, fontWeight: 700, color: C.text }}>{server.name}</span>
                <code style={{ fontSize: 11, fontFamily: 'monospace', color: C.textMuted }}>{server.slug}</code>
                <span style={{
                  fontSize: 9, fontWeight: 700, letterSpacing: '0.07em', textTransform: 'uppercase',
                  padding: '2px 7px', borderRadius: 6,
                  background: `${authColor}18`, border: `1px solid ${authColor}40`, color: authColor,
                }}>{server.auth_type}</span>
                {/* Credential status badge */}
                {cred?.credential_set
                  ? <span style={{ fontSize: 9, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.07em', padding: '2px 7px', borderRadius: 6, background: 'rgba(52,211,153,0.1)', border: '1px solid rgba(52,211,153,0.3)', color: '#34d399' }}>● key set</span>
                  : <span style={{ fontSize: 9, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.07em', padding: '2px 7px', borderRadius: 6, background: 'rgba(100,116,139,0.1)', border: '1px solid rgba(100,116,139,0.2)', color: '#64748b' }}>○ no key</span>
                }
              </div>

              {/* URL hint */}
              {server.url && (
                <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {server.url}
                </div>
              )}

              {/* Credential input */}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                <div>
                  <label style={labelStyle}>
                    {server.auth_type === 'bearer' ? 'Bearer token' : 'Header value'}
                    {cred?.credential_set && <span style={{ marginLeft: 6, color: '#34d399', fontWeight: 400 }}>(replaces existing)</span>}
                  </label>
                  <input
                    type="password"
                    value={input}
                    onChange={e => setInputs(p => ({ ...p, [server.id]: e.target.value }))}
                    style={fieldStyle}
                    placeholder={cred?.credential_set ? '●●●●●●●●●●●●●●●● (enter new value to replace)' : server.auth_type === 'bearer' ? 'sk-…' : 'header value'}
                    autoComplete="off"
                  />
                </div>

                {server.auth_type === 'header' && (
                  <div>
                    <label style={labelStyle}>Header name <span style={{ opacity: 0.5, fontWeight: 400 }}>(default: Authorization)</span></label>
                    <input
                      type="text"
                      value={headerName}
                      onChange={e => setHeaderNames(p => ({ ...p, [server.id]: e.target.value }))}
                      style={{ ...fieldStyle, fontFamily: 'inherit' }}
                      placeholder={cred?.auth_header_name || 'Authorization'}
                    />
                  </div>
                )}

                {/* Actions */}
                <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                  <button
                    onClick={() => handleSave(server)}
                    disabled={isSaving || !input}
                    style={{
                      padding: '8px 18px', borderRadius: 8, fontSize: 12, fontWeight: 600,
                      background: input ? ACCENT_BG : 'rgba(255,255,255,0.04)',
                      border: `1px solid ${input ? ACCENT_BORDER : 'rgba(255,255,255,0.08)'}`,
                      color: input ? ACCENT : C.textMuted,
                      cursor: isSaving || !input ? 'not-allowed' : 'pointer',
                      transition: 'all 150ms ease',
                    }}
                  >
                    {isSaving ? 'Saving…' : cred?.credential_set ? 'Update' : 'Save credential'}
                  </button>

                  {cred?.credential_set && (
                    <button
                      onClick={() => handleDelete(server.id)}
                      disabled={isDeleting}
                      style={{
                        padding: '8px 14px', borderRadius: 8, fontSize: 12, fontWeight: 600,
                        background: 'rgba(248,113,113,0.06)', border: '1px solid rgba(248,113,113,0.2)',
                        color: '#f87171', cursor: isDeleting ? 'not-allowed' : 'pointer',
                        transition: 'all 150ms ease',
                      }}
                    >
                      {isDeleting ? 'Removing…' : 'Remove'}
                    </button>
                  )}

                  {msg && (
                    <span style={{ fontSize: 12, color: msg.ok ? '#34d399' : '#f87171', fontWeight: 600 }}>
                      {msg.ok ? '✓ ' : '✗ '}{msg.text}
                    </span>
                  )}
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
