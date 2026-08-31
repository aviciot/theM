'use client';
import { useState, useEffect, useRef } from 'react';
import { themApi, type Application, type EntryPoint } from '@/lib/api';
import type { AppLiveness } from '../types';
import { C, APP_CARD_STYLES } from '../constants';
import { fallbackCopy } from './CanvasHelpers';

// EP metadata (AppCard-local)
const EP_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };
const EP_LABEL: Record<string, string> = { websocket: 'WebSocket', sse: 'SSE', webrtc: 'WebRTC', a2a: 'A2A', voice: 'Voice' };

function epIconColor(type: string): { color: string; glow: string; border: string } {
  if (type === 'websocket') return { color: '#00d1ff', glow: 'rgba(0,209,255,0.25)', border: 'rgba(0,209,255,0.45)' };
  if (type === 'sse')       return { color: '#a78bfa', glow: 'rgba(167,139,250,0.22)', border: 'rgba(167,139,250,0.42)' };
  if (type === 'webrtc')    return { color: '#a78bfa', glow: 'rgba(167,139,250,0.22)', border: 'rgba(167,139,250,0.42)' };
  if (type === 'a2a')       return { color: '#f59e0b', glow: 'rgba(245,158,11,0.22)', border: 'rgba(245,158,11,0.42)' };
  if (type === 'voice')     return { color: '#4ade80', glow: 'rgba(74,222,128,0.22)', border: 'rgba(74,222,128,0.42)' };
  return { color: '#94a3b8', glow: 'rgba(148,163,184,0.15)', border: 'rgba(148,163,184,0.3)' };
}

// ── AppCard sub-component ─────────────────────────────────────────────────────
export function AppCard({
  app,
  liveness,
  sessionCount,
  selected,
  onToggleSelect,
  onEdit,
  onSessions,
  onRuntime,
  onMCPCredentials,
  onToggle,
  onDelete,
  onRename,
}: {
  app: Application;
  liveness: AppLiveness | null;
  sessionCount: number;
  selected?: boolean;
  onToggleSelect?: (id: string, checked: boolean) => void;
  onEdit: (a: Application) => void;
  onSessions: (a: Application) => void;
  onRuntime: (a: Application) => void;
  onMCPCredentials: (a: Application) => void;
  onToggle: (a: Application) => void;
  onDelete: (a: Application) => void;
  onRename: (a: Application) => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [toggling, setToggling] = useState(false);
  const [synthesizing, setSynthesizing] = useState<string | null>(null); // ep id
  const [synthToast, setSynthToast] = useState<string | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  async function handleSynthesizeCard(epId: string) {
    setSynthesizing(epId);
    try {
      const res = await themApi.discoverEP(app.id, epId);
      setSynthToast(res.ok ? 'Card synthesized' : (res.detail ?? 'Synthesis failed'));
    } catch {
      setSynthToast('Synthesis failed');
    } finally {
      setSynthesizing(null);
      setTimeout(() => setSynthToast(null), 3500);
    }
  }

  useEffect(() => {
    if (!menuOpen) return;
    function handler(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as unknown as globalThis.Node)) setMenuOpen(false);
    }
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [menuOpen]);

  const enabledEps = (app.entry_points ?? []).filter(e => e.enabled);
  const firstEp = enabledEps[0] ?? app.entry_points?.[0];
  const ep = epIconColor(firstEp?.entry_point_type ?? 'websocket');

  // Liveness derived from multiplexed WS push (no per-card polling)
  const hasEPs = (app.entry_points ?? []).length > 0;
  const reachable = !app.enabled ? false : !hasEPs ? false : (liveness?.reachable ?? null);
  const latencyMs = liveness?.latency_ms ?? null;

  const statusColor  = !app.enabled ? C.error : !hasEPs ? C.textMuted : reachable === null ? C.textMuted : reachable ? C.green : '#f59e0b';
  const statusLabel  = !app.enabled ? 'disabled' : !hasEPs ? 'no entry points' : reachable === null ? 'checking…' : reachable ? 'live' : 'unreachable';
  const statusBg     = !app.enabled ? 'rgba(255,180,171,0.1)' : !hasEPs ? 'rgba(255,255,255,0.04)' : reachable === null ? 'rgba(255,255,255,0.04)' : reachable ? 'rgba(74,222,128,0.08)' : 'rgba(245,158,11,0.08)';
  const statusBorder = !app.enabled ? 'rgba(255,180,171,0.3)' : !hasEPs ? 'rgba(255,255,255,0.1)' : reachable === null ? 'rgba(255,255,255,0.1)' : reachable ? C.greenBorder : 'rgba(245,158,11,0.4)';

  const chromaAccent = !app.enabled ? '#64748b' : reachable === false ? '#f59e0b' : '#6366f1';
  const chromaGrad   = `linear-gradient(145deg, ${chromaAccent}1a 0%, ${chromaAccent}08 40%, #07090f 100%)`;

  // Publish badge
  const publishBadge = app.active_revision != null
    ? { label: `Rev ${app.active_revision} · live`, color: C.green, bg: 'rgba(74,222,128,0.10)', border: C.greenBorder }
    : { label: 'Draft · not live', color: '#f59e0b', bg: 'rgba(245,158,11,0.08)', border: 'rgba(245,158,11,0.3)' };

  // Orchestrator summary
  const orch = (app.app_orchestrators ?? [])[0];
  const orchLabel = orch?.display_name || orch?.name;
  const orchModel = orch?.llm_model ?? null;

  // Inline EP URLs (resolve host from window)
  // URLs use two-segment paths: /apps/{app_slug}/{ep_slug}/... (migration 048)
  function epUrls(epRow: EntryPoint): Array<{ label: string; val: string; icon: string }> {
    const host = typeof window !== 'undefined' ? window.location.hostname : 'localhost';
    const port = typeof window !== 'undefined' ? (window.location.port || (window.location.protocol === 'https:' ? '443' : '80')) : '8088';
    const portSuffix = (port === '80' || port === '443') ? '' : `:${port}`;
    const http = window.location.protocol === 'https:' ? 'https' : 'http';
    const ws   = window.location.protocol === 'https:' ? 'wss'  : 'ws';
    const base = `${host}${portSuffix}`;
    const appSlug = app.slug ?? app.id;
    const t = epRow.entry_point_type;
    if (t === 'websocket') return [{ label: 'WS', val: `${ws}://${base}/apps/${appSlug}/${epRow.slug}/ws`, icon: 'electrical_services' }];
    if (t === 'sse')       return [
      { label: 'SSE', val: `${http}://${base}/apps/${appSlug}/${epRow.slug}/sse`, icon: 'stream' },
    ];
    if (t === 'webrtc')    return [
      { label: 'Voice', val: `${http}://${base}/apps/${appSlug}/${epRow.slug}/voice/chat`, icon: 'mic' },
    ];
    if (t === 'a2a')       return [
      { label: 'A2A', val: `${http}://${base}/a2a/${appSlug}/${epRow.slug}`, icon: 'smart_toy' },
      { label: 'Card', val: `${http}://${base}/a2a/${appSlug}/${epRow.slug}/.well-known/agent.json`, icon: 'badge' },
    ];
    if (t === 'voice')     return [
      { label: 'STT', val: `${http}://${base}/apps/${appSlug}/${epRow.slug}/voice/transcribe`, icon: 'mic' },
      { label: 'TTS', val: `${http}://${base}/apps/${appSlug}/${epRow.slug}/voice/tts`, icon: 'volume_up' },
    ];
    return [];
  }

  const hasRuntime = app.runtime_config && Object.values(app.runtime_config).some(v => v !== null && !(Array.isArray(v) && v.length === 0));

  return (
    <div
      className="app-glass-card chroma-card"
      style={{
        borderRadius: 16, overflow: 'visible', display: 'flex', flexDirection: 'column', position: 'relative',
        outline: selected ? '2px solid #00d1ff' : undefined,
        '--card-border': chromaAccent,
        '--card-gradient': chromaGrad,
      } as React.CSSProperties}
    >
      {/* Bulk-select checkbox */}
      {onToggleSelect && (
        <input
          type="checkbox"
          checked={!!selected}
          onChange={(e) => { e.stopPropagation(); onToggleSelect(app.id, e.target.checked); }}
          title="Select for bulk delete"
          style={{ position: 'absolute', top: 10, left: 10, width: 16, height: 16, accentColor: '#00d1ff', cursor: 'pointer', zIndex: 10 }}
        />
      )}

      {/* ── Top section ── */}
      <div style={{ padding: '20px 20px 0', display: 'flex', flexDirection: 'column', gap: 12 }}>

        {/* Row 1: icon + name + publish badge + menu */}
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
          {/* App icon */}
          <div style={{
            width: 48, height: 48, borderRadius: 12, flexShrink: 0,
            background: `radial-gradient(circle at 30% 30%, ${ep.glow}, transparent 70%)`,
            border: `1px solid ${ep.border}`,
            display: 'flex', alignItems: 'center', justifyContent: 'center', position: 'relative',
          }}>
            <span className="material-symbols-outlined" style={{ fontSize: 22, color: ep.color }}>
              {EP_ICON[firstEp?.entry_point_type ?? ''] ?? 'extension'}
            </span>
            {enabledEps.length > 1 && (
              <span style={{
                position: 'absolute', top: -6, right: -6,
                minWidth: 18, height: 18, borderRadius: 9,
                background: '#00d1ff', color: '#021520',
                fontSize: 10, fontWeight: 700,
                display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '0 4px',
              }}>{enabledEps.length}</span>
            )}
          </div>

          {/* Name + subtitle */}
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontWeight: 700, fontSize: 15, color: C.text, fontFamily: 'Geist, sans-serif', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {app.name}
            </div>
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 2 }}>
              {enabledEps.length} entry point{enabledEps.length !== 1 ? 's' : ''}
              {' · '}
              <span style={{ color: publishBadge.color }}>{publishBadge.label}</span>
            </div>
          </div>

          {/* Three-dot menu */}
          <div ref={menuRef} style={{ position: 'relative', flexShrink: 0 }} onClick={e => e.stopPropagation()}>
            <button
              onClick={() => setMenuOpen(v => !v)}
              style={{ width: 30, height: 30, borderRadius: 7, cursor: 'pointer', background: 'var(--tm-btn-2-bg)', border: '1px solid var(--tm-btn-2-border)', color: 'var(--tm-card-text-muted)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
            >
              <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
                <circle cx="8" cy="3" r="1.5"/><circle cx="8" cy="8" r="1.5"/><circle cx="8" cy="13" r="1.5"/>
              </svg>
            </button>
            {menuOpen && (
              <div style={{
                position: 'absolute', top: 34, right: 0, zIndex: 50, minWidth: 140,
                background: 'var(--tm-menu-bg)', border: '1px solid var(--tm-menu-border)',
                borderRadius: 10, boxShadow: '0 8px 32px rgba(0,0,0,0.35)', overflow: 'hidden',
              }}>
                <button
                  onClick={() => { setMenuOpen(false); onRename(app); }}
                  style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', padding: '10px 14px', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13, color: C.text, fontWeight: 500 }}
                  onMouseEnter={e => (e.currentTarget.style.background = 'rgba(255,255,255,0.06)')}
                  onMouseLeave={e => (e.currentTarget.style.background = 'none')}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 16 }}>edit</span>
                  Rename
                </button>
                <div style={{ height: 1, background: 'var(--tm-divider)', margin: '0 10px' }} />
                <button
                  onClick={() => { setMenuOpen(false); onDelete(app); }}
                  style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', padding: '10px 14px', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13, color: C.error, fontWeight: 600 }}
                  onMouseEnter={e => (e.currentTarget.style.background = 'rgba(255,180,171,0.08)')}
                  onMouseLeave={e => (e.currentTarget.style.background = 'none')}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                  Delete
                </button>
              </div>
            )}
          </div>
        </div>

        {/* Row 2: live status bar */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 12px', borderRadius: 10, background: statusBg, border: `1px solid ${statusBorder}` }}>
          {app.enabled && (
            <span style={{ width: 7, height: 7, borderRadius: '50%', flexShrink: 0, background: statusColor, boxShadow: reachable ? `0 0 7px ${statusColor}` : 'none' }} />
          )}
          <span style={{ fontSize: 12, fontWeight: 700, color: statusColor }}>{statusLabel}</span>
          {reachable && latencyMs != null && (
            <span style={{ fontSize: 11, color: C.textMuted, marginLeft: 'auto' }}>{latencyMs}ms</span>
          )}
        </div>

        {/* Row 3: orchestrator + access tiles */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'var(--tm-filter-bg)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16, color: '#a78bfa', flexShrink: 0 }}>hub</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Orchestrator</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {orchLabel
                  ? <>{orchLabel}{orchModel && <span style={{ color: C.textMuted, fontWeight: 400, marginLeft: 4 }}>· {orchModel.split('/').pop()?.split('-').slice(0,2).join('-')}</span>}</>
                  : <span style={{ color: C.textMuted, fontStyle: 'italic' }}>none — publish to activate</span>
                }
              </div>
            </div>
          </div>
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'var(--tm-filter-bg)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16, color: '#f59e0b', flexShrink: 0 }}>lock</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Access</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600 }}>
                Bearer token
                <span style={{ fontSize: 10, color: C.textMuted, fontWeight: 400, display: 'block' }}>Authorization: Bearer …</span>
              </div>
            </div>
          </div>
        </div>

        {/* Row 4: entry point URL rows — all EPs, disabled ones dimmed */}
        {(app.entry_points ?? []).length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, paddingBottom: 4 }}>
            <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 2 }}>Entry Points</div>
            {(app.entry_points ?? []).map(epRow => {
              const urls = epUrls(epRow);
              const epC = epIconColor(epRow.entry_point_type);
              const primaryUrl = urls[0];
              return (
                <div key={epRow.id} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.06)' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 13, color: epC.color, flexShrink: 0 }}>{EP_ICON[epRow.entry_point_type] ?? 'bolt'}</span>
                  <code style={{ fontSize: 10, fontFamily: 'JetBrains Mono, monospace', color: C.textMuted, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {primaryUrl ? primaryUrl.val : epRow.slug}
                  </code>
                  {primaryUrl && (
                    <button
                      onClick={() => {
                        const val = primaryUrl.val;
                        if (navigator.clipboard) {
                          navigator.clipboard.writeText(val).catch(() => fallbackCopy(val));
                        } else {
                          fallbackCopy(val);
                        }
                      }}
                      title="Copy URL"
                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center', padding: 2, flexShrink: 0 }}
                      onMouseEnter={e => (e.currentTarget.style.color = C.text)}
                      onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 13 }}>content_copy</span>
                    </button>
                  )}
                  {epRow.entry_point_type === 'a2a' && (
                    <button
                      onClick={() => handleSynthesizeCard(epRow.id)}
                      disabled={synthesizing === epRow.id}
                      title="Synthesize A2A agent card from orchestrator + sub-agents"
                      style={{ background: 'none', border: 'none', cursor: synthesizing === epRow.id ? 'wait' : 'pointer', color: '#f59e0b', display: 'flex', alignItems: 'center', padding: 2, flexShrink: 0, opacity: synthesizing === epRow.id ? 0.5 : 1 }}
                      onMouseEnter={e => (e.currentTarget.style.opacity = synthesizing === epRow.id ? '0.5' : '0.75')}
                      onMouseLeave={e => (e.currentTarget.style.opacity = synthesizing === epRow.id ? '0.5' : '1')}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 13 }}>{synthesizing === epRow.id ? 'hourglass_empty' : 'auto_awesome'}</span>
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        )}
        {synthToast && (
          <div style={{ fontSize: 11, color: synthToast.startsWith('Card') ? '#4ade80' : '#f87171', padding: '4px 8px', borderRadius: 6, background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.08)', marginTop: 4 }}>
            {synthToast}
          </div>
        )}
      </div>

      {/* ── Action buttons ── */}
      <div style={{ borderTop: '1px solid var(--tm-divider)', padding: '10px 14px', display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        {/* Sessions */}
        <button
          className="app-card-btn"
          onClick={() => onSessions(app)}
          style={{
            flex: '2 1 80px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
            background: sessionCount > 0 ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.03)',
            color: sessionCount > 0 ? '#00f0ff' : C.textMuted,
            border: `1px solid ${sessionCount > 0 ? 'rgba(0,240,255,0.35)' : 'rgba(255,255,255,0.1)'}`,
          }}
          onMouseEnter={e => { e.currentTarget.style.background = 'rgba(0,240,255,0.12)'; e.currentTarget.style.color = '#00f0ff'; }}
          onMouseLeave={e => { e.currentTarget.style.background = sessionCount > 0 ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.03)'; e.currentTarget.style.color = sessionCount > 0 ? '#00f0ff' : C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>person</span>
          Sessions
          {sessionCount > 0 && (
            <span style={{ background: '#00f0ff', color: '#000', fontSize: 10, fontWeight: 800, borderRadius: 8, padding: '0px 5px', lineHeight: '16px', minWidth: 16, textAlign: 'center' }}>{sessionCount}</span>
          )}
        </button>

        {/* Builder (was "Definition") */}
        <button className="app-card-btn app-card-btn--open" onClick={() => onEdit(app)}
          style={{ flex: '1 1 60px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>hub</span>
          Builder
        </button>

        {/* Runtime */}
        <button
          className="app-card-btn"
          onClick={() => onRuntime(app)}
          title="Runtime policy"
          style={{
            flex: '1 1 60px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
            background: hasRuntime ? 'rgba(251,146,60,0.1)' : 'rgba(255,255,255,0.03)',
            color: hasRuntime ? '#fb923c' : C.textMuted,
            border: `1px solid ${hasRuntime ? 'rgba(251,146,60,0.4)' : 'rgba(255,255,255,0.1)'}`,
          }}
          onMouseEnter={e => { e.currentTarget.style.background = 'rgba(251,146,60,0.15)'; e.currentTarget.style.color = '#fb923c'; }}
          onMouseLeave={e => { e.currentTarget.style.background = hasRuntime ? 'rgba(251,146,60,0.1)' : 'rgba(255,255,255,0.03)'; e.currentTarget.style.color = hasRuntime ? '#fb923c' : C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>tune</span>
          Runtime
        </button>

        {/* MCP Credentials */}
        <button
          className="app-card-btn"
          onClick={() => onMCPCredentials(app)}
          title="MCP Credentials"
          style={{
            flex: '1 1 60px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
            background: 'rgba(255,255,255,0.03)', color: C.textMuted,
            border: '1px solid rgba(255,255,255,0.1)',
          }}
          onMouseEnter={e => { e.currentTarget.style.background = 'rgba(129,140,248,0.1)'; e.currentTarget.style.color = '#818cf8'; }}
          onMouseLeave={e => { e.currentTarget.style.background = 'rgba(255,255,255,0.03)'; e.currentTarget.style.color = C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>electrical_services</span>
          MCP
        </button>

        {/* Enable / Disable toggle with feedback */}
        <button
          className={`app-card-btn ${app.enabled ? 'app-card-btn--toggle-on' : 'app-card-btn--toggle-off'}`}
          onClick={async () => {
            if (toggling) return;
            setToggling(true);
            try { await (onToggle(app) as unknown as Promise<void>); } finally { setToggling(false); }
          }}
          disabled={toggling}
          title={app.enabled ? 'Disable this application' : 'Enable this application'}
          style={{ flex: '1 1 60px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5, opacity: toggling ? 0.6 : 1 }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>
            {toggling ? 'hourglass_empty' : app.enabled ? 'toggle_on' : 'toggle_off'}
          </span>
          {toggling ? '…' : app.enabled ? 'Disable' : 'Enable'}
        </button>
      </div>
    </div>
  );
}

// ── Dashboard WS hooks ────────────────────────────────────────────────────────
export function useDashAppStatuses(token: string | null): Record<string, AppLiveness> {
  const [statuses, setStatuses] = useState<Record<string, AppLiveness>>({});

  useEffect(() => {
    if (!token) return;
    const wsUrl = `${window.location.origin.replace(/^http/, 'ws').replace(/^https/, 'wss')}/ws/dashboard?token=${token}`;
    let ws: WebSocket;
    let dead = false;

    function connect() {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        ws.send(JSON.stringify({ type: 'subscribe', channels: ['apps'] }));
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.channel === 'apps' && msg.event?.type === 'app_status') {
            setStatuses(prev => ({ ...prev, ...msg.event.statuses }));
          }
        } catch {}
      };
      ws.onclose = () => {
        if (!dead) setTimeout(connect, 4000);
      };
      ws.onerror = () => ws.close();
    }

    connect();
    return () => {
      dead = true;
      ws?.close();
    };
  }, [token]);

  return statuses;
}

// ── Sessions live hook ────────────────────────────────────────────────────────
import type { SessionInfo } from '@/lib/api';

export function useDashSessions(token: string | null, appId: string | null): {
  sessions: SessionInfo[];
  connected: boolean;
} {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!token || !appId) return;
    const wsBase = window.location.origin.replace(/^http/, 'ws').replace(/^https/, 'wss');
    const wsUrl = `${wsBase}/ws/dashboard?token=${token}`;
    let ws: WebSocket;
    let dead = false;

    function connect() {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        ws.send(JSON.stringify({ type: 'subscribe', channels: [`sessions:${appId}`] }));
        setConnected(true);
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          const ch = `sessions:${appId}`;
          if (msg.channel !== ch) return;
          const evt = msg.event;
          if (evt?.type === 'session_snapshot') {
            setSessions(evt.sessions ?? []);
          } else if (evt?.type === 'session_start' && evt.session_info) {
            setSessions(prev => {
              if (prev.find(s => s.session_id === evt.session_id)) return prev;
              return [...prev, evt.session_info as SessionInfo];
            });
          } else if (evt?.type === 'session_end') {
            setSessions(prev => prev.filter(s => s.session_id !== evt.session_id));
          } else if (evt?.type === 'session_update' && evt.session_id) {
            setSessions(prev => prev.map(s =>
              s.session_id === evt.session_id
                ? { ...s, ...evt.session_info }
                : s
            ));
          }
        } catch {}
      };
      ws.onclose = () => {
        setConnected(false);
        if (!dead) setTimeout(connect, 4000);
      };
      ws.onerror = () => ws.close();
    }

    connect();
    return () => {
      dead = true;
      ws?.close();
    };
  }, [token, appId]);

  return { sessions, connected };
}
