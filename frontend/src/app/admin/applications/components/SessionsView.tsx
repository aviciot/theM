'use client';
import { useState, useEffect } from 'react';
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  Controls,
  type NodeTypes,
  Handle,
  Position,
} from '@xyflow/react';
import { themApi, type Application, type Agent, type SessionInfo, type MonitoringConfig } from '@/lib/api';
import { C, CANVAS_STYLES, SESSIONS_STYLES, MON_DEFAULTS } from '../constants';
import { buildNodesFromApp } from './CanvasHelpers';
import { useDashSessions } from './AppCard';

// Read-only canvas node wrappers — same visuals as builder, but with session count badge
function EPNodeRO({ data }: { data: { label?: string; slug?: string; epType?: string; _sessCount?: number; _heatStyle?: React.CSSProperties } }) {
  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };
  const msIcon = EP_MS_ICON[data.epType ?? 'websocket'] ?? 'bolt';
  const count = data._sessCount ?? 0;
  const accent = C.cyan;
  const badgeTitle = `${count} sessions`;
  const baseStyle: React.CSSProperties = {
    width: 56, height: 56, borderRadius: '50%',
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    border: `2px solid ${count > 0 ? accent : 'rgba(0,240,255,0.25)'}`,
    transition: 'all 0.3s ease',
  };
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {count > 0 && <div className={`sess-badge${count > 0 ? ' active' : ''}`} title={badgeTitle}>{count}</div>}
      <div style={{ ...baseStyle, ...(count > 0 && data._heatStyle ? data._heatStyle : {}) }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent }}>{msIcon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center' }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: C.text, lineHeight: 1.3 }}>
          {data.label || 'EP'}
        </div>
        {data.slug && <div style={{ fontSize: 10, color: C.cyan, fontFamily: 'JetBrains Mono, monospace', opacity: 0.8, marginTop: 1 }}>{data.slug}</div>}
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

function OrchNodeRO({ data }: { data: { displayName?: string; _sessCount?: number; _heatStyle?: React.CSSProperties } }) {
  const count = data._sessCount ?? 0;
  const accent = C.purple;
  const baseStyle: React.CSSProperties = {
    width: 56, height: 56, borderRadius: '50%',
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    border: `2px solid ${count > 0 ? accent : 'rgba(208,188,255,0.25)'}`,
    transition: 'all 0.3s ease',
  };
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {count > 0 && <div className={`sess-badge${count > 0 ? ' active' : ''}`} style={{ background: 'rgba(208,188,255,0.15)', border: '1.5px solid rgba(208,188,255,0.55)', color: C.purple, boxShadow: '0 0 8px rgba(208,188,255,0.3)' }}>{count}</div>}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div style={{ ...baseStyle, ...(count > 0 && data._heatStyle ? data._heatStyle : {}) }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent }}>hub</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 120 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {data.displayName}
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

function AgentNodeRO({ data }: { data: { displayName?: string; icon?: string; tags?: string[] } }) {
  const isInternal = data.tags?.includes('internal') ?? false;
  const accent = isInternal ? '#a0f0d0' : C.green;
  const icon = data.icon || 'smart_toy';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div style={{
        width: 56, height: 56, borderRadius: '50%',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: 'transparent', border: `2px solid rgba(74,222,128,0.25)`,
        transition: 'all 0.3s ease',
      }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent }}>{icon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 110 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {data.displayName}
        </div>
      </div>
    </div>
  );
}

const RO_NODE_TYPES: NodeTypes = {
  entryPoint: EPNodeRO as any,
  orchestrator: OrchNodeRO as any,
  agent: AgentNodeRO as any,
};

function elapsed(iso: string): string {
  const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`;
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`;
}

function heatmapStyle(count: number, cfg: MonitoringConfig, type: 'ep' | 'orch'): React.CSSProperties {
  if (count <= 0) return {};
  const accent = type === 'ep' ? C.cyan : C.purple;
  const lowColor  = type === 'ep' ? 'rgba(0,240,255,0.10)'     : 'rgba(208,188,255,0.10)';
  const midColor  = type === 'ep' ? 'rgba(0,240,255,0.20)'     : 'rgba(208,188,255,0.20)';
  const highColor = type === 'ep' ? 'rgba(0,240,255,0.35)'     : 'rgba(208,188,255,0.35)';
  const lowGlow   = type === 'ep' ? '0 0 10px rgba(0,240,255,0.25)'     : '0 0 10px rgba(208,188,255,0.25)';
  const midGlow   = type === 'ep' ? '0 0 18px rgba(0,240,255,0.5)'      : '0 0 18px rgba(208,188,255,0.5)';
  const highGlow  = type === 'ep' ? '0 0 28px rgba(0,240,255,0.85)'     : '0 0 28px rgba(208,188,255,0.85)';
  const borderW   = count >= cfg.heatmap_high ? 3 : count >= cfg.heatmap_medium ? 2.5 : 2;
  const bg    = count >= cfg.heatmap_high ? highColor : count >= cfg.heatmap_medium ? midColor : lowColor;
  const glow  = count >= cfg.heatmap_high ? highGlow  : count >= cfg.heatmap_medium ? midGlow  : lowGlow;
  return { background: bg, border: `${borderW}px solid ${accent}`, boxShadow: glow };
}

function edgeStrokeWidth(count: number, cfg: MonitoringConfig): number {
  if (count >= cfg.edge_thick)  return 5;
  if (count >= cfg.edge_medium) return 3;
  if (count >= cfg.edge_thin)   return 1.5;
  return 1;
}

export function SessionsView({
  app: initialApp,
  agents,
  onBack,
  token,
}: {
  app: Application;
  agents: Agent[];
  onBack: () => void;
  token: string | null;
}) {
  const [app, setApp] = useState(initialApp);
  const { sessions, connected } = useDashSessions(token, app.id);
  const [selectedSession, setSelectedSession] = useState<SessionInfo | null>(null);
  const [tick, setTick] = useState(0);
  const [monCfg, setMonCfg] = useState<MonitoringConfig>(MON_DEFAULTS);

  // Optimistic terminate: sessions hidden pending WS confirmation of session_end
  // hiddenSessions doubles as "terminating" — once hidden the row is gone from the list
  const [hiddenSessions, setHiddenSessions] = useState<Set<string>>(new Set());

  // When session_end arrives via WS, useDashSessions removes it from sessions[];
  // no further action needed — the hidden entry is simply never un-hidden for dead sessions.

  async function handleTerminate(sid: string) {
    setHiddenSessions(h => new Set(h).add(sid));
    try {
      await themApi.disconnectSession(sid);
    } catch {
      // Signal failed — un-hide so user can retry
      setHiddenSessions(h => { const n = new Set(h); n.delete(sid); return n; });
    }
  }

  // Load monitoring config once
  useEffect(() => {
    themApi.getMonitoringConfig().then(setMonCfg).catch(() => {});
  }, []);

  // Re-render elapsed times every 5s
  useEffect(() => {
    const iv = setInterval(() => setTick(t => t + 1), 5000);
    return () => clearInterval(iv);
  }, []);

  // Build read-only nodes/edges from app, with session counts overlaid
  const epCountBySlug = new Map<string, number>();
  const visibleSessions = sessions.filter(s => !hiddenSessions.has(s.session_id));
  visibleSessions.forEach(s => {
    if (s.ep_slug) epCountBySlug.set(s.ep_slug, (epCountBySlug.get(s.ep_slug) ?? 0) + 1);
  });

  const { nodes: baseNodes, edges: baseEdges } = buildNodesFromApp(app, agents);

  // Build active node id sets for edge coloring
  const activeEpNodeIds = new Set<string>();
  const activeOrchNodeIds = new Set<string>();

  const nodes = baseNodes.map(n => {
    if (n.type === 'entryPoint' && n.data?.slug) {
      const slug = n.data.slug as string;
      const count = epCountBySlug.get(slug) ?? 0;
      if (count > 0) activeEpNodeIds.add(n.id);
      return { ...n, data: { ...n.data, _sessCount: count, _heatStyle: heatmapStyle(count, monCfg, 'ep') } };
    }
    if (n.type === 'orchestrator') {
      const orchName = (n.data as any)?.name ?? '';
      const orchCount = visibleSessions.filter(s => s.orchestrator_name === orchName).length;
      if (orchCount > 0) activeOrchNodeIds.add(n.id);
      return { ...n, data: { ...n.data, _sessCount: orchCount, _heatStyle: heatmapStyle(orchCount, monCfg, 'orch') } };
    }
    return n;
  });

  // Which agent slugs are actively being called right now (across all sessions, parallel-safe)
  const activeAgentSlugs = new Set(
    visibleSessions.flatMap(s => s.active_agents ?? [])
  );

  // Count sessions flowing through each edge path for thickness scaling
  const epOrchSessionCount = sessions.length; // total sessions = load on ep→orch path

  // Style edges: EP→orch always active when sessions exist; orch→agent only when that agent is being called
  const edges = baseEdges.map(e => {
    const isEpOrch    = activeEpNodeIds.has(e.source) && activeOrchNodeIds.has(e.target);
    const targetSlug  = (baseNodes.find(n => n.id === e.target)?.data as any)?.name ?? '';
    const isOrchAgent = activeOrchNodeIds.has(e.source) && activeAgentSlugs.has(targetSlug);

    if (isEpOrch) {
      const sw = edgeStrokeWidth(epOrchSessionCount, monCfg);
      return {
        ...e,
        animated: false,
        className: 'active-ep-orch',
        style: { stroke: '#00f0ff', strokeWidth: sw },
      };
    }
    if (isOrchAgent) {
      const orchCount = visibleSessions.filter(s => s.active_agents?.includes(targetSlug)).length;
      const sw = edgeStrokeWidth(orchCount, monCfg);
      return {
        ...e,
        animated: false,
        className: 'active-orch-agent',
        style: { stroke: C.purple, strokeWidth: sw },
      };
    }
    return {
      ...e,
      animated: false,
      style: { stroke: 'rgba(148,163,184,0.18)', strokeWidth: 1, strokeDasharray: '4,4' },
    };
  });

  // Cap session list for UI performance
  const displaySessions = visibleSessions.slice(0, monCfg.panel_max_sessions);
  void tick; // used indirectly by elapsed() rerender

  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg, overflow: 'hidden' }}>
      <style>{CANVAS_STYLES}{SESSIONS_STYLES}</style>

      {/* Top bar */}
      <div style={{
        height: 56, flexShrink: 0,
        display: 'flex', alignItems: 'center', gap: 12,
        padding: '0 20px',
        background: C.surfaceContainer,
        borderBottom: `1px solid ${C.outline}`,
        backdropFilter: 'blur(12px)',
      }}>
        <button
          onClick={onBack}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '7px 14px', borderRadius: 8,
            border: `1px solid ${C.outline}`, background: 'transparent',
            color: C.textMuted, fontSize: 13, fontWeight: 500, cursor: 'pointer',
          }}
          onMouseEnter={e => { e.currentTarget.style.background = 'rgba(255,255,255,0.05)'; e.currentTarget.style.color = C.text; }}
          onMouseLeave={e => { e.currentTarget.style.background = 'transparent'; e.currentTarget.style.color = C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 16 }}>arrow_back</span>
          Back
        </button>

        <div style={{ width: 1, height: 24, background: C.outline, flexShrink: 0 }} />

        <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.cyan }}>hub</span>
        <span style={{ fontSize: 15, fontWeight: 700, color: C.text }}>{app.name}</span>
        <span style={{ fontSize: 12, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>/{app.slug}</span>

        <div style={{ flex: 1 }} />

        {/* Live indicator */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '5px 12px', borderRadius: 20,
          background: connected ? 'rgba(74,222,128,0.08)' : 'rgba(255,255,255,0.04)',
          border: `1px solid ${connected ? C.greenBorder : 'rgba(255,255,255,0.1)'}`,
        }}>
          <div style={{
            width: 7, height: 7, borderRadius: '50%',
            background: connected ? C.green : C.textMuted,
            boxShadow: connected ? '0 0 6px rgba(74,222,128,0.8)' : 'none',
            animation: connected ? 'sess-pulse 2s ease-in-out infinite' : 'none',
          }} />
          <span style={{ fontSize: 12, color: connected ? C.green : C.textMuted, fontWeight: 600 }}>
            {connected ? 'Live' : 'Connecting…'}
          </span>
        </div>

        {/* Session count pill */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '5px 14px', borderRadius: 20,
          background: visibleSessions.length > 0 ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.03)',
          border: `1px solid ${visibleSessions.length > 0 ? C.cyanBorder : 'rgba(255,255,255,0.08)'}`,
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: 14, color: visibleSessions.length > 0 ? C.cyan : C.textMuted }}>person</span>
          <span style={{ fontSize: 13, fontWeight: 700, color: visibleSessions.length > 0 ? C.cyan : C.textMuted }}>
            {visibleSessions.length} active
          </span>
        </div>
      </div>

      {/* Main body: canvas + right panel */}
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>

        {/* Canvas — read-only, no drag, no editor */}
        <div style={{ flex: 1, position: 'relative' }}>
          <ReactFlowProvider>
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={RO_NODE_TYPES}
              fitView
              fitViewOptions={{ padding: 0.25 }}
              nodesDraggable={false}
              nodesConnectable={false}
              elementsSelectable={false}
              panOnDrag={true}
              zoomOnScroll={true}
              style={{ background: C.surfaceLow }}
            >
              <Background variant={BackgroundVariant.Dots} gap={24} size={1} color="rgba(148,163,184,0.12)" />
              <Controls showInteractive={false} style={{ background: C.surface, border: `1px solid ${C.outline}` }} />
            </ReactFlow>
          </ReactFlowProvider>

          {/* Empty state overlay */}
          {visibleSessions.length === 0 && connected && (
            <div style={{
              position: 'absolute', top: '50%', left: '50%',
              transform: 'translate(-50%, -50%)',
              pointerEvents: 'none',
              display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 10,
            }}>
              <span className="material-symbols-outlined" style={{ fontSize: 40, color: 'rgba(148,163,184,0.25)' }}>person_off</span>
              <span style={{ fontSize: 13, color: 'rgba(148,163,184,0.4)', fontWeight: 500 }}>No active sessions</span>
            </div>
          )}
        </div>

        {/* Right panel — session list + detail */}
        <div style={{
          width: 340, flexShrink: 0,
          background: C.surfaceContainer,
          borderLeft: `1px solid ${C.outline}`,
          display: 'flex', flexDirection: 'column',
          overflow: 'hidden',
        }}>
          {/* Panel header */}
          <div style={{
            padding: '14px 16px 10px',
            borderBottom: `1px solid ${C.outline}`,
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          }}>
            <span style={{ fontSize: 13, fontWeight: 700, color: C.text, letterSpacing: 0.2 }}>Active Sessions</span>
            <span style={{
              fontSize: 11, fontWeight: 700, color: C.textMuted,
              background: 'rgba(255,255,255,0.05)', borderRadius: 10,
              padding: '2px 8px', border: `1px solid ${C.outline}`,
            }}>{visibleSessions.length}</span>
          </div>

          {/* Session list */}
          <div style={{ flex: 1, overflowY: 'auto', padding: '8px 8px 0' }}>
            {visibleSessions.length === 0 && connected && (
              <div style={{ padding: '32px 16px', textAlign: 'center', color: C.textMuted, fontSize: 13 }}>
                Waiting for sessions…
              </div>
            )}
            {!connected && (
              <div style={{ padding: '32px 16px', textAlign: 'center', color: C.textMuted, fontSize: 13 }}>
                Connecting…
              </div>
            )}
            {visibleSessions.length > monCfg.panel_max_sessions && (
              <div style={{ margin: '4px 8px 6px', padding: '6px 10px', borderRadius: 8, background: 'rgba(245,158,11,0.08)', border: '1px solid rgba(245,158,11,0.22)', fontSize: 11, color: '#f59e0b' }}>
                Showing {monCfg.panel_max_sessions} of {visibleSessions.length} sessions
              </div>
            )}
            {displaySessions.map(s => {
              const isSelected = selectedSession?.session_id === s.session_id;
              const epType = app.entry_points?.find(ep => ep.slug === s.ep_slug)?.entry_point_type ?? 'websocket';
              const epIcon = EP_MS_ICON[epType] ?? 'bolt';
              const epColor = epType === 'sse' ? '#a78bfa' : C.cyan;
              return (
                <div
                  key={s.session_id}
                  className={`sess-row${isSelected ? ' selected' : ''}`}
                  onClick={() => setSelectedSession(isSelected ? null : s)}
                >
                  {/* EP type icon */}
                  <div style={{
                    width: 32, height: 32, borderRadius: '50%', flexShrink: 0,
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    background: `${epColor}18`, border: `1.5px solid ${epColor}44`,
                  }}>
                    <span className="material-symbols-outlined" style={{ fontSize: 16, color: epColor }}>{epIcon}</span>
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
                      <span style={{ fontSize: 12, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {s.ep_slug ?? 'direct'}
                      </span>
                      <span style={{
                        fontSize: 10, color: C.textMuted, flexShrink: 0,
                        background: 'rgba(255,255,255,0.05)', borderRadius: 4, padding: '1px 5px',
                        border: `1px solid ${C.outline}`,
                      }}>{epType}</span>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 11, color: C.textMuted }}>
                        user {s.user_id}
                      </span>
                      <span style={{ fontSize: 11, color: 'rgba(74,222,128,0.7)', fontFamily: 'JetBrains Mono, monospace' }}>
                        {elapsed(s.started_at)}
                      </span>
                    </div>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0 }}>
                    <button
                      title="Terminate session"
                      onClick={e => { e.stopPropagation(); handleTerminate(s.session_id); }}
                      style={{
                        width: 26, height: 26, borderRadius: 6, border: '1px solid rgba(239,68,68,0.35)',
                        background: 'transparent', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
                        transition: 'all 0.15s',
                      }}
                      onMouseEnter={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.12)'; }}
                      onMouseLeave={e => { e.currentTarget.style.background = 'transparent'; }}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 13, color: '#ef4444' }}>power_settings_new</span>
                    </button>
                    <span className="material-symbols-outlined" style={{
                      fontSize: 14, color: isSelected ? C.cyan : C.textMuted,
                      transition: 'color 0.15s',
                      transform: isSelected ? 'rotate(90deg)' : 'rotate(0)',
                    }}>chevron_right</span>
                  </div>
                </div>
              );
            })}
          </div>

          {/* Session detail drawer */}
          {selectedSession && (() => {
            const s = selectedSession;
            const epType = app.entry_points?.find(ep => ep.slug === s.ep_slug)?.entry_point_type ?? 'websocket';
            return (
              <div style={{
                borderTop: `1px solid ${C.outline}`,
                padding: '14px 16px',
                background: 'rgba(0,240,255,0.03)',
                flexShrink: 0,
              }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: C.cyan, marginBottom: 10, letterSpacing: 0.3 }}>
                  SESSION DETAIL
                </div>
                {[
                  ['Session ID', s.session_id.slice(0, 16) + '…'],
                  ['Entry Point', s.ep_slug ?? '—'],
                  ['EP Type', epType],
                  ['Orchestrator', s.orchestrator_name],
                  ['User ID', String(s.user_id)],
                  ['Context ID', s.context_id.slice(0, 16) + '…'],
                  ['Started', new Date(s.started_at).toLocaleTimeString()],
                  ['Elapsed', elapsed(s.started_at)],
                  ['Pod', s.instance_id.slice(0, 12) + '…'],
                ].map(([label, value]) => (
                  <div key={label} style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 5, gap: 8 }}>
                    <span style={{ fontSize: 11, color: C.textMuted, flexShrink: 0 }}>{label}</span>
                    <span style={{
                      fontSize: 11, color: C.text, fontFamily: label === 'Session ID' || label === 'Context ID' || label === 'Pod' ? 'JetBrains Mono, monospace' : 'inherit',
                      textAlign: 'right', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    }} title={value}>{value}</span>
                  </div>
                ))}
                <div style={{ display: 'flex', gap: 6, marginTop: 8 }}>
                  <button
                    onClick={() => { handleTerminate(s.session_id); setSelectedSession(null); }}
                    style={{
                      flex: 1, padding: '6px 0', borderRadius: 6,
                      border: '1px solid rgba(239,68,68,0.45)', background: 'rgba(239,68,68,0.06)',
                      color: '#ef4444', fontSize: 12, cursor: 'pointer',
                    }}
                    onMouseEnter={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.14)'; }}
                    onMouseLeave={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.06)'; }}
                  >
                    Terminate
                  </button>
                  <button
                    onClick={() => setSelectedSession(null)}
                    style={{
                      flex: 1, padding: '6px 0', borderRadius: 6,
                      border: `1px solid ${C.outline}`, background: 'transparent',
                      color: C.textMuted, fontSize: 12, cursor: 'pointer',
                    }}
                    onMouseEnter={e => e.currentTarget.style.background = 'rgba(255,255,255,0.04)'}
                    onMouseLeave={e => e.currentTarget.style.background = 'transparent'}
                  >
                    Close
                  </button>
                </div>
              </div>
            );
          })()}
        </div>
      </div>
    </div>
  );
}
