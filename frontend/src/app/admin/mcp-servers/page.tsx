'use client';

import { useEffect, useState, useRef, useCallback } from 'react';
import { themApi, MCPServer, MCPTool } from '@/lib/api';
import Sidebar from '@/components/Sidebar';

// ── accent / theme ────────────────────────────────────────────────────────────
const ACCENT = '#818cf8';
const ACCENT_GLOW = 'rgba(129,140,248,0.25)';
const ACCENT_BORDER = 'rgba(129,140,248,0.35)';

// ── helpers ───────────────────────────────────────────────────────────────────

function timeAgo(iso: string | null): string {
  if (!iso) return '—';
  const diff = Date.now() - new Date(iso).getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function slugify(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
}

// ── health badge ──────────────────────────────────────────────────────────────

type HealthStatus = 'healthy' | 'degraded' | 'unreachable' | 'unknown';

const HEALTH_COLORS: Record<HealthStatus, { dot: string; bg: string; border: string; label: string }> = {
  healthy:     { dot: '#34d399', bg: 'rgba(16,185,129,0.12)',  border: 'rgba(16,185,129,0.28)',  label: 'Healthy' },
  degraded:    { dot: '#fbbf24', bg: 'rgba(251,191,36,0.12)',  border: 'rgba(251,191,36,0.28)',  label: 'Degraded' },
  unreachable: { dot: '#f87171', bg: 'rgba(220,38,38,0.12)',   border: 'rgba(220,38,38,0.25)',   label: 'Unreachable' },
  unknown:     { dot: '#64748b', bg: 'rgba(100,116,139,0.12)', border: 'rgba(100,116,139,0.22)', label: 'Unknown' },
};

function HealthBadge({ status, pulse }: { status: HealthStatus; pulse?: boolean }) {
  const c = HEALTH_COLORS[status] ?? HEALTH_COLORS.unknown;
  return (
    <span style={{
      fontSize: '9px', fontWeight: 700, letterSpacing: '0.07em', textTransform: 'uppercase',
      padding: '2px 8px', borderRadius: '9999px', display: 'inline-flex', alignItems: 'center', gap: '5px',
      background: c.bg, border: `1px solid ${c.border}`, color: c.dot,
    }}>
      <span style={{
        width: '5px', height: '5px', borderRadius: '50%', background: c.dot, display: 'inline-block',
        boxShadow: `0 0 6px ${c.dot}`,
        ...(pulse ? { animation: 'pulse 1.6s ease-in-out infinite' } : {}),
      }} />
      {c.label}
    </span>
  );
}

// ── transport / auth badges ───────────────────────────────────────────────────

function TransportBadge({ t }: { t: string }) {
  const color = t === 'http' ? '#38bdf8' : t === 'sse' ? '#a78bfa' : '#94a3b8';
  return (
    <span style={{
      fontSize: '9px', fontWeight: 700, letterSpacing: '0.06em', textTransform: 'uppercase',
      padding: '2px 6px', borderRadius: '6px',
      background: `${color}18`, border: `1px solid ${color}40`, color,
    }}>{t}</span>
  );
}

function AuthBadge({ a }: { a: string }) {
  const color = a === 'none' ? '#64748b' : a === 'bearer' ? '#34d399' : a === 'header' ? '#fbbf24' : '#a78bfa';
  return (
    <span style={{
      fontSize: '9px', fontWeight: 700, letterSpacing: '0.06em', textTransform: 'uppercase',
      padding: '2px 6px', borderRadius: '6px',
      background: `${color}18`, border: `1px solid ${color}40`, color,
    }}>{a}</span>
  );
}

// ── CSS-in-JS style constants ─────────────────────────────────────────────────

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 11px', borderRadius: '8px', fontSize: '13px',
  background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)',
  color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box',
};

const labelStyle: React.CSSProperties = {
  fontSize: '11px', fontWeight: 600, color: 'var(--tm-card-text-muted)',
  display: 'block', marginBottom: '5px',
};

const sectionLabel: React.CSSProperties = {
  fontSize: '9px', fontWeight: 700, letterSpacing: '0.1em', textTransform: 'uppercase',
  color: 'rgba(255,255,255,0.2)', margin: '18px 0 10px 0',
};

const nestedSurface: React.CSSProperties = {
  background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)',
  borderRadius: '10px', padding: '12px',
};

// ── MCPServerCard ─────────────────────────────────────────────────────────────

function MCPServerCard({
  server,
  selected,
  onClick,
}: {
  server: MCPServer;
  selected: boolean;
  onClick: () => void;
}) {
  const hs = (server.health_status as HealthStatus) || 'unknown';
  const chromaGrad = selected
    ? `linear-gradient(145deg, rgba(129,140,248,0.18) 0%, rgba(99,102,241,0.10) 40%, #0a0d14 100%)`
    : `linear-gradient(145deg, rgba(129,140,248,0.07) 0%, rgba(0,0,0,0) 50%, #0a0d14 100%)`;

  return (
    <article
      className="glass-card chroma-card"
      onClick={onClick}
      style={{
        padding: '20px', display: 'flex', flexDirection: 'column', gap: '12px',
        borderRadius: '20px', position: 'relative', cursor: 'pointer',
        '--card-border': selected ? ACCENT : ACCENT_BORDER,
        '--card-gradient': chromaGrad,
        boxShadow: selected ? `0 0 0 2px ${ACCENT}, 0 8px 32px rgba(0,0,0,0.4)` : undefined,
      } as React.CSSProperties}
    >
      {/* Header: icon + name + health badge */}
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: '10px' }}>
        {/* Icon tile */}
        <div style={{
          width: '48px', height: '48px', flexShrink: 0, borderRadius: '12px',
          background: `radial-gradient(circle at 30% 25%, ${ACCENT_GLOW}, transparent 65%),
                       linear-gradient(145deg, var(--tm-inset), var(--tm-inset-deep))`,
          border: `1px solid ${ACCENT_BORDER}`,
          boxShadow: `0 0 16px ${ACCENT_GLOW}`,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: '22px', color: ACCENT }}>electrical_services</span>
        </div>

        {/* Name + slug */}
        <div style={{ flex: 1, minWidth: 0, paddingTop: '2px' }}>
          <h3 style={{
            fontSize: '15px', fontWeight: 700, color: 'var(--tm-card-text)', margin: '0 0 3px 0',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>{server.name}</h3>
          <p style={{
            fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: 0, fontFamily: 'monospace',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>{server.slug}</p>
        </div>

        <HealthBadge status={hs} />
      </div>

      {/* Description */}
      <p style={{
        fontSize: '12px', color: 'var(--tm-card-text-muted)', lineHeight: 1.5, margin: 0,
        display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden',
        minHeight: '36px',
      }}>
        {server.description || <span style={{ opacity: 0.35 }}>No description</span>}
      </p>

      {/* Stats row */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
        <div style={{ ...nestedSurface, padding: '8px 10px', display: 'flex', alignItems: 'center', gap: '7px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '14px', color: 'var(--tm-card-text-muted)' }}>build</span>
          <div>
            <p style={{ fontSize: '14px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, lineHeight: 1 }}>
              {server.tools_count ?? server.tools_manifest?.length ?? 0}
            </p>
            <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>tools</p>
          </div>
        </div>
        <div style={{ ...nestedSurface, padding: '8px 10px', display: 'flex', alignItems: 'center', gap: '7px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '14px', color: 'var(--tm-card-text-muted)' }}>schedule</span>
          <div>
            <p style={{ fontSize: '12px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, lineHeight: 1, whiteSpace: 'nowrap' }}>
              {timeAgo(server.last_checked_at)}
            </p>
            <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>checked</p>
          </div>
        </div>
      </div>

      {/* Transport + Auth type row */}
      <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap', alignItems: 'center' }}>
        <TransportBadge t={server.transport} />
        <AuthBadge a={server.auth_type} />
        {!server.enabled && (
          <span style={{
            fontSize: '9px', fontWeight: 700, letterSpacing: '0.07em', textTransform: 'uppercase',
            padding: '2px 6px', borderRadius: '6px',
            background: 'rgba(100,116,139,0.12)', border: '1px solid rgba(100,116,139,0.22)', color: '#64748b',
          }}>Disabled</span>
        )}
      </div>
    </article>
  );
}

// ── Tool item ─────────────────────────────────────────────────────────────────

function ToolRow({ tool }: { tool: MCPTool }) {
  const [open, setOpen] = useState(false);
  const hasSchema = tool.inputSchema && Object.keys(tool.inputSchema).length > 0;
  return (
    <div style={{
      borderRadius: '8px', background: 'var(--tm-inset-deep)',
      border: '1px solid var(--tm-divider)', overflow: 'hidden',
    }}>
      <button
        onClick={() => setOpen(v => !v)}
        style={{
          width: '100%', textAlign: 'left', padding: '9px 12px',
          background: 'none', border: 'none', cursor: 'pointer',
          display: 'flex', alignItems: 'center', gap: '8px',
        }}
      >
        <span className="material-symbols-outlined" style={{ fontSize: '14px', color: ACCENT, flexShrink: 0 }}>
          {open ? 'expand_less' : 'expand_more'}
        </span>
        <span style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text)', fontFamily: 'monospace' }}>
          {tool.name}
        </span>
        {tool.description && (
          <span style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>
            — {tool.description}
          </span>
        )}
        {hasSchema && (
          <span style={{
            fontSize: '9px', fontWeight: 700, padding: '1px 5px', borderRadius: '4px',
            background: `${ACCENT}20`, border: `1px solid ${ACCENT}40`, color: ACCENT,
            flexShrink: 0,
          }}>schema</span>
        )}
      </button>
      {open && hasSchema && (
        <div style={{ padding: '0 12px 10px 34px' }}>
          <pre style={{
            fontSize: '10px', color: 'var(--tm-card-text-hint)', margin: 0,
            background: 'rgba(0,0,0,0.3)', borderRadius: '6px', padding: '8px',
            overflowX: 'auto', maxHeight: '160px',
          }}>
            {JSON.stringify(tool.inputSchema, null, 2)}
          </pre>
        </div>
      )}
    </div>
  );
}

// ── ProbeButton ───────────────────────────────────────────────────────────────

function ProbeButton({ serverId, onDone }: { serverId: string; onDone?: (s: MCPServer) => void }) {
  const [state, setState] = useState<'idle' | 'loading' | 'ok' | 'err'>('idle');
  const [msg, setMsg] = useState('');

  async function run() {
    setState('loading');
    setMsg('');
    try {
      const res = await themApi.probeMCPServer(serverId);
      setMsg(`${res.health_status} · ${res.tools_count} tools`);
      setState('ok');
    } catch (e) {
      const err = e as Error;
      if (err.message.includes('404') || err.message.includes('501') || err.message.includes('503')) {
        setMsg('Probe endpoint not yet available — coming in MCP-2');
      } else {
        setMsg(err.message || 'Probe failed');
      }
      setState('err');
    }
  }

  return (
    <div>
      <button
        onClick={run}
        disabled={state === 'loading'}
        style={{
          display: 'inline-flex', alignItems: 'center', gap: '6px',
          padding: '8px 16px', borderRadius: '8px', cursor: state === 'loading' ? 'not-allowed' : 'pointer',
          background: `${ACCENT}22`, border: `1px solid ${ACCENT}55`, color: ACCENT,
          fontSize: '12px', fontWeight: 600, transition: 'all 150ms ease',
        }}
      >
        <span className={`material-symbols-outlined${state === 'loading' ? ' spin' : ''}`} style={{ fontSize: '14px' }}>
          {state === 'loading' ? 'sync' : 'play_arrow'}
        </span>
        {state === 'loading' ? 'Probing…' : 'Test connection'}
      </button>

      {msg && (
        <div style={{
          marginTop: '8px', fontSize: '11px', padding: '6px 10px', borderRadius: '6px',
          background: state === 'ok' ? 'rgba(16,185,129,0.08)' : 'rgba(220,38,38,0.08)',
          border: `1px solid ${state === 'ok' ? 'rgba(16,185,129,0.2)' : 'rgba(220,38,38,0.2)'}`,
          color: state === 'ok' ? '#34d399' : '#f87171',
        }}>
          {state === 'ok' ? '✓ ' : '✗ '}{msg}
        </div>
      )}
    </div>
  );
}

// ── Properties Panel (slide-in) ───────────────────────────────────────────────

type PanelTab = 'general' | 'status';

function PropertiesPanel({
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

  // Editable fields
  const [name, setName] = useState(server.name);
  const [description, setDescription] = useState(server.description || '');
  const [transport, setTransport] = useState(server.transport);
  const [url, setUrl] = useState(server.url);
  const [authType, setAuthType] = useState(server.auth_type);
  const [enabled, setEnabled] = useState(server.enabled);

  // Reset when server changes
  useEffect(() => {
    setName(server.name);
    setDescription(server.description || '');
    setTransport(server.transport);
    setUrl(server.url);
    setAuthType(server.auth_type);
    setEnabled(server.enabled);
    setError('');
    setConfirmDel(false);
    setSaving(false);
  }, [server.id]);

  async function handleSave() {
    setSaving(true);
    setError('');
    try {
      const updated = await themApi.updateMCPServer(server.id, {
        name, description, transport, url, auth_type: authType, enabled,
      });
      onSaved(updated);
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
  const tools: MCPTool[] = server.tools_manifest ?? [];

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 300,
      background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
    }} onClick={onClose}>
    <div style={{
      position: 'relative',
      background: 'var(--tm-panel)',
      border: '1px solid var(--tm-modal-border)',
      borderRadius: '18px',
      width: '600px',
      maxHeight: '90vh',
      display: 'flex', flexDirection: 'column',
      boxShadow: '0 24px 64px rgba(0,0,0,.55), 0 6px 18px rgba(0,0,0,0.3)',
    }} onClick={e => e.stopPropagation()}>

      {/* Modal header */}
      <div style={{
        padding: '24px 24px 0 24px', display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between',
        borderBottom: '1px solid rgba(255,255,255,0.06)', paddingBottom: '16px',
      }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
            <span className="material-symbols-outlined" style={{ fontSize: '18px', color: ACCENT }}>electrical_services</span>
            <h2 style={{ fontSize: '16px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0,
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {server.name}
            </h2>
          </div>
          <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: 0, fontFamily: 'monospace' }}>
            {server.slug}
          </p>
        </div>
        <button onClick={onClose} style={{
          background: 'none', border: 'none', cursor: 'pointer',
          color: 'var(--tm-card-text-muted)', padding: '4px', flexShrink: 0,
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: '18px' }}>close</span>
        </button>
      </div>

      {/* Tabs */}
      <div style={{ display: 'flex', gap: '4px', padding: '12px 24px 0 24px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
        {(['general', 'status'] as PanelTab[]).map(t => (
          <button key={t} onClick={() => setTab(t)} style={{
            padding: '6px 14px', borderRadius: '8px 8px 0 0', fontSize: '12px', fontWeight: tab === t ? 700 : 400,
            background: tab === t ? `${ACCENT}18` : 'transparent',
            border: `1px solid ${tab === t ? ACCENT_BORDER : 'transparent'}`,
            borderBottom: 'none', color: tab === t ? ACCENT : 'var(--tm-card-text-muted)', cursor: 'pointer',
            textTransform: 'capitalize',
          }}>{t === 'general' ? 'General' : 'Status & Tools'}</button>
        ))}
      </div>

      {/* Tab content */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '16px 24px 24px 24px' }} className="custom-scrollbar">

        {tab === 'general' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
            {/* Name */}
            <div>
              <label style={labelStyle}>Name</label>
              <input value={name} onChange={e => setName(e.target.value)} style={inputStyle} />
            </div>
            {/* Slug — read-only after create */}
            <div>
              <label style={labelStyle}>Slug <span style={{ opacity: 0.5 }}>(immutable)</span></label>
              <div style={{
                ...inputStyle, display: 'inline-block', fontFamily: 'monospace',
                color: 'var(--tm-card-text-muted)', background: 'rgba(0,0,0,0.2)',
              }}>{server.slug}</div>
            </div>
            {/* Description */}
            <div>
              <label style={labelStyle}>Description</label>
              <textarea value={description} onChange={e => setDescription(e.target.value)}
                rows={3} style={{ ...inputStyle, resize: 'vertical', fontFamily: 'inherit' }} />
            </div>
            {/* Transport */}
            <div>
              <label style={labelStyle}>Transport</label>
              <select value={transport} onChange={e => setTransport(e.target.value as MCPServer['transport'])}
                style={{ ...inputStyle }}>
                <option value="streamable-http">streamable-http (recommended)</option>
                <option value="http">http (legacy)</option>
                <option value="sse">sse (legacy)</option>
                </select>
            </div>
            {/* URL */}
            <div>
              <label style={labelStyle}>URL</label>
              <input value={url} onChange={e => setUrl(e.target.value)} style={inputStyle}
                placeholder="https://my-mcp-server.example.com" />
            </div>
            {/* Auth type */}
            <div>
              <label style={labelStyle}>Auth type</label>
              <select value={authType} onChange={e => setAuthType(e.target.value as MCPServer['auth_type'])}
                style={{ ...inputStyle }}>
                <option value="none">none</option>
                <option value="bearer">bearer token</option>
                <option value="header">custom header</option>
                <option value="oauth2" disabled>oauth2 (coming soon)</option>
              </select>
            </div>
            {/* Enabled */}
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
              <label style={{ ...labelStyle, margin: 0, cursor: 'pointer', userSelect: 'none', display: 'flex', alignItems: 'center', gap: '8px' }}>
                <input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)}
                  style={{ accentColor: ACCENT, width: '14px', height: '14px' }} />
                Enabled
              </label>
            </div>

            {error && (
              <div style={{ fontSize: '11px', padding: '6px 10px', borderRadius: '6px', background: 'rgba(220,38,38,0.08)', border: '1px solid rgba(220,38,38,0.2)', color: '#f87171' }}>
                {error}
              </div>
            )}

            {/* Actions */}
            <div style={{ display: 'flex', gap: '8px', marginTop: '4px' }}>
              <button onClick={handleSave} disabled={saving} style={{
                flex: 1, padding: '9px', borderRadius: '8px', fontWeight: 600, fontSize: '13px',
                background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT,
                cursor: saving ? 'not-allowed' : 'pointer', transition: 'all 150ms ease',
              }}>
                {saving ? 'Saving…' : 'Save'}
              </button>
              <button onClick={handleDelete} disabled={deleting} style={{
                padding: '9px 14px', borderRadius: '8px', fontWeight: 600, fontSize: '13px',
                background: confirmDel ? 'rgba(220,38,38,0.18)' : 'rgba(100,116,139,0.1)',
                border: `1px solid ${confirmDel ? 'rgba(220,38,38,0.4)' : 'rgba(100,116,139,0.2)'}`,
                color: confirmDel ? '#f87171' : 'var(--tm-card-text-muted)',
                cursor: deleting ? 'not-allowed' : 'pointer', transition: 'all 150ms ease',
                whiteSpace: 'nowrap',
              }}>
                {deleting ? 'Deleting…' : confirmDel ? 'Confirm delete?' : 'Delete'}
              </button>
            </div>
          </div>
        )}

        {tab === 'status' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
            {/* Health summary */}
            <div style={nestedSurface}>
              <p style={sectionLabel}>Health</p>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '10px' }}>
                <HealthBadge status={hs} />
                <span style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)' }}>
                  {timeAgo(server.last_checked_at)}
                </span>
              </div>
              {server.last_error && (
                <div style={{
                  fontSize: '11px', padding: '6px 10px', borderRadius: '6px',
                  background: 'rgba(220,38,38,0.08)', border: '1px solid rgba(220,38,38,0.2)',
                  color: '#f87171', fontFamily: 'monospace', wordBreak: 'break-word',
                }}>
                  {server.last_error}
                </div>
              )}
            </div>

            {/* Stats */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
              <div style={{ ...nestedSurface, padding: '10px 12px' }}>
                <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '0 0 4px 0' }}>Tools</p>
                <p style={{ fontSize: '20px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0 }}>
                  {server.tools_count ?? tools.length}
                </p>
              </div>
              <div style={{ ...nestedSurface, padding: '10px 12px' }}>
                <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '0 0 4px 0' }}>Transport</p>
                <p style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, fontFamily: 'monospace' }}>
                  {server.transport}
                </p>
              </div>
            </div>

            {/* Probe */}
            <div>
              <p style={sectionLabel}>Test connection</p>
              <ProbeButton serverId={server.id} />
            </div>

            {/* Capabilities */}
            {server.capabilities && Object.keys(server.capabilities).length > 0 && (
              <div>
                <p style={sectionLabel}>Capabilities</p>
                <pre style={{
                  fontSize: '10px', color: 'var(--tm-card-text-hint)', margin: 0,
                  background: 'rgba(0,0,0,0.3)', borderRadius: '6px', padding: '8px',
                  overflowX: 'auto', maxHeight: '120px',
                }}>
                  {JSON.stringify(server.capabilities, null, 2)}
                </pre>
              </div>
            )}

            {/* Tools manifest */}
            <div>
              <p style={sectionLabel}>Tools manifest ({tools.length})</p>
              {tools.length === 0 ? (
                <div style={{
                  ...nestedSurface, textAlign: 'center', padding: '20px',
                  color: 'var(--tm-card-text-muted)', fontSize: '12px',
                }}>
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

// ── Create Modal ──────────────────────────────────────────────────────────────

function CreateModal({
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
      const created = await themApi.createMCPServer({ name: name.trim(), slug: slug.trim() || slugify(name), description, transport, url: url.trim(), auth_type: authType });
      onCreated(created);
    } catch (e) {
      setError((e as Error).message || 'Create failed');
    } finally {
      setSaving(false);
    }
  }

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 200,
      background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
    }} onClick={e => { if (e.target === e.currentTarget) onClose(); }}>
      <div style={{
        width: '480px', background: 'var(--tm-panel)', borderRadius: '16px',
        border: '1px solid rgba(255,255,255,0.09)', boxShadow: '0 24px 80px rgba(0,0,0,0.6)',
        padding: '28px', display: 'flex', flexDirection: 'column', gap: '16px',
      }}>
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
          <input
            value={slug}
            onChange={e => { setSlug(e.target.value); setSlugTouched(true); }}
            style={{ ...inputStyle, fontFamily: 'monospace' }}
            placeholder="github-mcp"
          />
        </div>

        <div>
          <label style={labelStyle}>Description</label>
          <textarea value={description} onChange={e => setDescription(e.target.value)}
            rows={2} style={{ ...inputStyle, resize: 'vertical', fontFamily: 'inherit' }} />
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
          <input value={url} onChange={e => setUrl(e.target.value)} style={inputStyle}
            placeholder="https://my-mcp-server.example.com" />
        </div>

        {authType !== 'none' && (
          <div style={{
            padding: '10px 12px', borderRadius: '8px',
            background: `${ACCENT}0d`, border: `1px solid ${ACCENT_BORDER}`,
            fontSize: '12px', color: 'var(--tm-card-text-muted)', display: 'flex', gap: '8px', alignItems: 'flex-start',
          }}>
            <span className="material-symbols-outlined" style={{ fontSize: '15px', color: ACCENT, flexShrink: 0, marginTop: '1px' }}>info</span>
            <span>
              Credentials are set per-application in <strong style={{ color: 'var(--tm-card-text)' }}>Applications → MCP Credentials</strong>.
              After adding this server, open the application and set the {authType === 'bearer' ? 'bearer token' : 'header value'} there.
            </span>
          </div>
        )}

        {error && (
          <div style={{ fontSize: '11px', padding: '6px 10px', borderRadius: '6px', background: 'rgba(220,38,38,0.08)', border: '1px solid rgba(220,38,38,0.2)', color: '#f87171' }}>
            {error}
          </div>
        )}

        <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={{
            padding: '9px 16px', borderRadius: '8px', fontSize: '13px', fontWeight: 600,
            background: 'rgba(100,116,139,0.1)', border: '1px solid rgba(100,116,139,0.2)',
            color: 'var(--tm-card-text-muted)', cursor: 'pointer',
          }}>Cancel</button>
          <button onClick={handleCreate} disabled={saving} style={{
            padding: '9px 20px', borderRadius: '8px', fontSize: '13px', fontWeight: 600,
            background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT,
            cursor: saving ? 'not-allowed' : 'pointer',
          }}>
            {saving ? 'Creating…' : 'Add Server'}
          </button>
        </div>
      </div>
    </div>
  );
}

// ── Main Page ─────────────────────────────────────────────────────────────────

type FilterStatus = 'all' | HealthStatus;

export default function MCPServersPage() {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<FilterStatus>('all');
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

  // keep selected in sync after saves
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

  return (
    <>
    <Sidebar />
    <main style={{ marginLeft: '260px', height: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--tm-bg)' }}>
      {/* Header */}
      <header style={{
        padding: '24px 32px 0 32px', flexShrink: 0,
        borderBottom: '1px solid rgba(255,255,255,0.06)', paddingBottom: '16px',
      }}>
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
          <button
            onClick={() => setShowCreate(true)}
            style={{
              display: 'flex', alignItems: 'center', gap: '6px',
              padding: '9px 18px', borderRadius: '10px', fontSize: '13px', fontWeight: 600,
              background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT,
              cursor: 'pointer', transition: 'all 150ms ease',
            }}
          >
            <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>add</span>
            Add Server
          </button>
        </div>

        {/* Search + filters */}
        <div style={{ display: 'flex', gap: '10px', alignItems: 'center', flexWrap: 'wrap' }}>
          <div style={{ position: 'relative', flex: '0 0 260px' }}>
            <span className="material-symbols-outlined" style={{
              position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)',
              fontSize: '16px', color: 'var(--tm-card-text-muted)', pointerEvents: 'none',
            }}>search</span>
            <input
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="Search servers…"
              style={{
                ...inputStyle, paddingLeft: '32px', width: '100%',
                background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)',
              }}
            />
          </div>
          <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap' }}>
            {filterPills.map(({ label, value }) => (
              <button
                key={value}
                onClick={() => setFilter(value)}
                className={filter === value ? 'filter-pill filter-pill-active' : 'filter-pill'}
                style={filter === value ? { borderColor: ACCENT, color: ACCENT, background: `${ACCENT}18` } : {}}
              >
                {label}
              </button>
            ))}
          </div>
          <button onClick={load} title="Refresh" style={{
            width: '34px', height: '34px', display: 'flex', alignItems: 'center', justifyContent: 'center',
            borderRadius: '8px', background: 'var(--tm-btn-2-bg)', border: '1px solid var(--tm-filter-border)',
            color: 'var(--tm-card-text-muted)', cursor: 'pointer',
          }}>
            <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>refresh</span>
          </button>
          <span style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginLeft: '4px' }}>
            {filtered.length} of {servers.length}
          </span>
        </div>
      </header>

      {/* Content area */}
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
              <button onClick={load} style={{
                padding: '8px 16px', borderRadius: '8px', background: 'rgba(248,113,113,0.1)',
                border: '1px solid rgba(248,113,113,0.3)', color: '#f87171', cursor: 'pointer', fontSize: '13px',
              }}>Retry</button>
            </div>
          )}

          {!loading && !error && filtered.length === 0 && (
            <div style={{ textAlign: 'center', padding: '80px 0', color: 'var(--tm-card-text-muted)' }}>
              <span className="material-symbols-outlined" style={{ fontSize: '40px', display: 'block', marginBottom: '12px', opacity: 0.3, color: ACCENT }}>electrical_services</span>
              {servers.length === 0
                ? <><p style={{ fontSize: '15px', fontWeight: 600, margin: '0 0 6px 0' }}>No MCP servers yet</p>
                    <p style={{ fontSize: '13px', margin: 0 }}>Add your first MCP server to connect tools to your agents.</p></>
                : <><p style={{ fontSize: '15px', fontWeight: 600, margin: '0 0 6px 0' }}>No servers match your filters</p>
                    <p style={{ fontSize: '13px', margin: 0 }}>Try adjusting the search or health filter.</p></>
              }
            </div>
          )}

          {!loading && !error && filtered.length > 0 && (
            <div style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))',
              gap: '16px',
              alignContent: 'start',
            }}>
              {filtered.map(server => (
                <MCPServerCard
                  key={server.id}
                  server={server}
                  selected={selected?.id === server.id}
                  onClick={() => setSelected(prev => prev?.id === server.id ? null : server)}
                />
              ))}
            </div>
          )}
      </div>

      {/* Properties modal */}
      {selected && (
        <PropertiesPanel
          server={selected}
          onClose={() => setSelected(null)}
          onSaved={handleSaved}
          onDeleted={handleDeleted}
        />
      )}

      {/* Create modal */}
      {showCreate && (
        <CreateModal onClose={() => setShowCreate(false)} onCreated={handleCreated} />
      )}
    </main>
    </>
  );
}
