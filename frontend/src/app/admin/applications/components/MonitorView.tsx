'use client';
import { useState, useEffect, useRef, useCallback } from 'react';
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

// ── Types ─────────────────────────────────────────────────────────────────────

interface RunEvent {
  type: string;
  content?: string;
  name?: string;
  input?: unknown;
  output?: unknown;
  run_id?: string;
  message?: string;
  [key: string]: unknown;
}

interface FeedEntry {
  id: string;
  event: RunEvent;
}

// Extended session that may have ended but is still within TTL
interface TrackedSession extends SessionInfo {
  _ended?: boolean;
  _endedAt?: number; // ms timestamp
}

const SESSION_TTL_MS = 2 * 60 * 1000; // 2 minutes

// ── Topology node components (read-only) ──────────────────────────────────────

const EP_MS_ICON: Record<string, string> = {
  websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic',
};

function EPNodeRO({ data }: { data: { label?: string; slug?: string; epType?: string; _sessCount?: number; _heatStyle?: React.CSSProperties } }) {
  const msIcon = EP_MS_ICON[data.epType ?? 'websocket'] ?? 'bolt';
  const count = data._sessCount ?? 0;
  const accent = C.cyan;
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {count > 0 && <div className="sess-badge active" title={`${count} sessions`}>{count}</div>}
      <div style={{ width: 46, height: 46, borderRadius: '50%', display: 'flex', alignItems: 'center', justifyContent: 'center', border: `2px solid ${count > 0 ? accent : 'rgba(0,240,255,0.25)'}`, transition: 'all 0.3s ease', ...(count > 0 && data._heatStyle ? data._heatStyle : {}) }}>
        <span className="material-symbols-outlined" style={{ fontSize: 22, color: accent }}>{msIcon}</span>
      </div>
      <div style={{ marginTop: 4, textAlign: 'center' }}>
        <div style={{ fontSize: 11, fontWeight: 600, color: C.text, lineHeight: 1.3 }}>{data.label || 'EP'}</div>
        {data.slug && <div style={{ fontSize: 9, color: C.cyan, fontFamily: 'JetBrains Mono, monospace', opacity: 0.8 }}>{data.slug}</div>}
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan, border: `2px solid ${C.bg}`, width: 7, height: 7 }} />
    </div>
  );
}

function OrchNodeRO({ data }: { data: { displayName?: string; _sessCount?: number; _heatStyle?: React.CSSProperties } }) {
  const count = data._sessCount ?? 0;
  const accent = C.purple;
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {count > 0 && <div className="sess-badge active" style={{ background: 'rgba(208,188,255,0.15)', border: '1.5px solid rgba(208,188,255,0.55)', color: C.purple, boxShadow: '0 0 8px rgba(208,188,255,0.3)' }}>{count}</div>}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 7, height: 7 }} />
      <div style={{ width: 46, height: 46, borderRadius: '50%', display: 'flex', alignItems: 'center', justifyContent: 'center', border: `2px solid ${count > 0 ? accent : 'rgba(208,188,255,0.25)'}`, transition: 'all 0.3s ease', ...(count > 0 && data._heatStyle ? data._heatStyle : {}) }}>
        <span className="material-symbols-outlined" style={{ fontSize: 22, color: accent }}>hub</span>
      </div>
      <div style={{ marginTop: 4, textAlign: 'center', maxWidth: 100 }}>
        <div style={{ fontSize: 11, fontWeight: 600, color: C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{data.displayName}</div>
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: accent, border: `2px solid ${C.bg}`, width: 7, height: 7 }} />
    </div>
  );
}

function AgentNodeRO({ data }: { data: { displayName?: string; icon?: string; tags?: string[] } }) {
  const isInternal = data.tags?.includes('internal') ?? false;
  const accent = isInternal ? '#a0f0d0' : C.green;
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 7, height: 7 }} />
      <div style={{ width: 46, height: 46, borderRadius: '50%', display: 'flex', alignItems: 'center', justifyContent: 'center', border: '2px solid rgba(74,222,128,0.25)' }}>
        <span className="material-symbols-outlined" style={{ fontSize: 22, color: accent }}>{data.icon || 'smart_toy'}</span>
      </div>
      <div style={{ marginTop: 4, textAlign: 'center', maxWidth: 90 }}>
        <div style={{ fontSize: 11, fontWeight: 600, color: C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{data.displayName}</div>
      </div>
    </div>
  );
}

const RO_NODE_TYPES: NodeTypes = {
  entryPoint: EPNodeRO as any,
  orchestrator: OrchNodeRO as any,
  agent: AgentNodeRO as any,
};

// ── Heatmap helpers ───────────────────────────────────────────────────────────

function heatmapStyle(count: number, cfg: MonitoringConfig, type: 'ep' | 'orch'): React.CSSProperties {
  if (count <= 0) return {};
  const accent = type === 'ep' ? C.cyan : C.purple;
  const low   = type === 'ep' ? 'rgba(0,240,255,0.10)'  : 'rgba(208,188,255,0.10)';
  const mid   = type === 'ep' ? 'rgba(0,240,255,0.20)'  : 'rgba(208,188,255,0.20)';
  const high  = type === 'ep' ? 'rgba(0,240,255,0.35)'  : 'rgba(208,188,255,0.35)';
  const lowG  = type === 'ep' ? '0 0 10px rgba(0,240,255,0.25)'  : '0 0 10px rgba(208,188,255,0.25)';
  const midG  = type === 'ep' ? '0 0 18px rgba(0,240,255,0.5)'   : '0 0 18px rgba(208,188,255,0.5)';
  const highG = type === 'ep' ? '0 0 28px rgba(0,240,255,0.85)'  : '0 0 28px rgba(208,188,255,0.85)';
  const bw    = count >= cfg.heatmap_high ? 3 : count >= cfg.heatmap_medium ? 2.5 : 2;
  const bg    = count >= cfg.heatmap_high ? high : count >= cfg.heatmap_medium ? mid : low;
  const glow  = count >= cfg.heatmap_high ? highG : count >= cfg.heatmap_medium ? midG : lowG;
  return { background: bg, border: `${bw}px solid ${accent}`, boxShadow: glow };
}

function edgeStrokeWidth(count: number, cfg: MonitoringConfig): number {
  if (count >= cfg.edge_thick)  return 5;
  if (count >= cfg.edge_medium) return 3;
  if (count >= cfg.edge_thin)   return 1.5;
  return 1;
}

function elapsed(iso: string): string {
  const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (secs < 60)   return `${secs}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`;
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`;
}

// ── useRunFeed hook ───────────────────────────────────────────────────────────

function useRunFeed(token: string | null, runId: string | null): {
  entries: FeedEntry[];
  connected: boolean;
  done: boolean;
} {
  const [entries, setEntries] = useState<FeedEntry[]>([]);
  const [connected, setConnected] = useState(false);
  const [done, setDone] = useState(false);
  // Track which runId we're currently subscribed to — only clear entries when
  // the runId changes to a *different* non-null value (not on reconnects).
  const subscribedRunId = useRef<string | null>(null);

  useEffect(() => {
    if (!token || !runId) return;
    // Only clear accumulated events when switching to a different run
    if (subscribedRunId.current !== null && subscribedRunId.current !== runId) {
      setEntries([]);
      setDone(false);
    }
    subscribedRunId.current = runId;

    const wsBase = window.location.origin.replace(/^http/, 'ws').replace(/^https/, 'wss');
    const wsUrl = `${wsBase}/ws/dashboard?token=${token}`;
    let ws: WebSocket;
    let dead = false;
    let seq = 0;

    function connect() {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        ws.send(JSON.stringify({ type: 'subscribe', channels: [`run:${runId}`] }));
        setConnected(true);
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.channel !== `run:${runId}`) return;
          const evt = msg.event as RunEvent;
          if (!evt?.type) return;
          setEntries(prev => [...prev, { id: `${Date.now()}-${seq++}`, event: evt }]);
          if (evt.type === 'done' || evt.type === 'error' || evt.type === 'canceled' || evt.type === 'terminated') {
            setDone(true);
          }
        } catch { /* ignore */ }
      };
      ws.onclose = () => {
        setConnected(false);
        if (!dead) setTimeout(connect, 4000);
      };
      ws.onerror = () => ws.close();
    }

    connect();
    return () => { dead = true; ws?.close(); };
  }, [token, runId]);

  return { entries, connected, done };
}

// ── EventRow ─────────────────────────────────────────────────────────────────

function EventRow({ entry }: { entry: FeedEntry }) {
  const [open, setOpen] = useState(false);
  const ev = entry.event;

  if (ev.type === 'token') {
    return <span style={{ color: 'rgba(203,213,225,0.9)', fontSize: 12, wordBreak: 'break-word' }}>{String(ev.content ?? '')}</span>;
  }

  if (ev.type === 'tool_call') {
    return (
      <div style={{ margin: '4px 0' }}>
        <button onClick={() => setOpen(o => !o)} style={{ display: 'flex', alignItems: 'center', gap: 6, background: 'rgba(99,102,241,0.1)', border: '1px solid rgba(99,102,241,0.3)', borderRadius: 6, padding: '3px 8px', cursor: 'pointer', color: '#a5b4fc', fontSize: 11 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 13 }}>build</span>
          {String(ev.name ?? 'tool')}
          <span className="material-symbols-outlined" style={{ fontSize: 11, opacity: 0.6 }}>{open ? 'expand_less' : 'expand_more'}</span>
        </button>
        {open && <pre style={{ margin: '4px 0 0 0', padding: '6px 8px', background: 'rgba(0,0,0,0.3)', borderRadius: 4, fontSize: 10, color: 'rgba(203,213,225,0.7)', overflowX: 'auto', maxHeight: 120 }}>{JSON.stringify(ev.input, null, 2)}</pre>}
      </div>
    );
  }

  if (ev.type === 'tool_result') {
    return (
      <div style={{ margin: '2px 0 4px 16px' }}>
        <button onClick={() => setOpen(o => !o)} style={{ display: 'flex', alignItems: 'center', gap: 6, background: 'rgba(34,197,94,0.08)', border: '1px solid rgba(34,197,94,0.2)', borderRadius: 6, padding: '3px 8px', cursor: 'pointer', color: '#86efac', fontSize: 11 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 13 }}>check_circle</span>
          {String(ev.name ?? 'result')}
          <span className="material-symbols-outlined" style={{ fontSize: 11, opacity: 0.6 }}>{open ? 'expand_less' : 'expand_more'}</span>
        </button>
        {open && <pre style={{ margin: '4px 0 0 0', padding: '6px 8px', background: 'rgba(0,0,0,0.3)', borderRadius: 4, fontSize: 10, color: 'rgba(203,213,225,0.7)', overflowX: 'auto', maxHeight: 120 }}>{JSON.stringify(ev.output, null, 2)}</pre>}
      </div>
    );
  }

  if (ev.type === 'done') {
    return <div style={{ display: 'flex', alignItems: 'center', gap: 6, color: C.green, fontSize: 12, margin: '6px 0 2px' }}><span className="material-symbols-outlined" style={{ fontSize: 14 }}>check_circle</span>Done</div>;
  }

  if (ev.type === 'error') {
    return <div style={{ display: 'flex', alignItems: 'center', gap: 6, color: '#f87171', fontSize: 12, margin: '6px 0 2px' }}><span className="material-symbols-outlined" style={{ fontSize: 14 }}>error</span>{String(ev.message ?? 'error')}</div>;
  }

  if (ev.type === 'iteration_start') {
    return <div style={{ fontSize: 10, color: 'rgba(148,163,184,0.5)', borderTop: '1px solid rgba(255,255,255,0.05)', margin: '6px 0 4px', paddingTop: 4 }}>— iteration —</div>;
  }

  return null;
}

// ── RunFeedColumn ─────────────────────────────────────────────────────────────

function RunFeedColumn({ session, token, onUnpin }: {
  session: TrackedSession;
  token: string;
  onUnpin: () => void;
}) {
  const runId = (session as TrackedSession & { run_id?: string }).run_id ?? null;
  const { entries, connected, done } = useRunFeed(token, runId);
  const bottomRef = useRef<HTMLDivElement>(null);
  const isEnded = !!session._ended;

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [entries.length]);

  // TTL countdown display
  const [ttlSecs, setTtlSecs] = useState<number | null>(null);
  useEffect(() => {
    if (!session._endedAt) { setTtlSecs(null); return; }
    function update() {
      const remaining = Math.max(0, Math.ceil((session._endedAt! + SESSION_TTL_MS - Date.now()) / 1000));
      setTtlSecs(remaining);
    }
    update();
    const iv = setInterval(update, 1000);
    return () => clearInterval(iv);
  }, [session._endedAt]);

  // Group consecutive token events into text blocks
  const rendered: Array<{ key: string; isTokenBlock: boolean; text?: string; entry?: FeedEntry }> = [];
  let tokenBuf = '';
  let tokenKey = '';
  for (const e of entries) {
    if (e.event.type === 'token') {
      if (!tokenKey) tokenKey = e.id;
      tokenBuf += String(e.event.content ?? '');
    } else {
      if (tokenBuf) { rendered.push({ key: tokenKey, isTokenBlock: true, text: tokenBuf }); tokenBuf = ''; tokenKey = ''; }
      rendered.push({ key: e.id, isTokenBlock: false, entry: e });
    }
  }
  if (tokenBuf) rendered.push({ key: tokenKey, isTokenBlock: true, text: tokenBuf });

  const age = session.started_at ? Math.floor((Date.now() - new Date(session.started_at).getTime()) / 1000) : 0;
  const ageStr = age < 60 ? `${age}s` : `${Math.floor(age / 60)}m`;
  const dotColor = isEnded ? 'rgba(148,163,184,0.3)' : done ? C.green : connected ? '#60a5fa' : 'rgba(255,255,255,0.2)';

  return (
    <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', borderLeft: '1px solid rgba(255,255,255,0.06)', opacity: isEnded ? 0.6 : 1, transition: 'opacity 0.5s' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderBottom: '1px solid rgba(255,255,255,0.06)', background: 'rgba(255,255,255,0.02)' }}>
        <span style={{ width: 6, height: 6, borderRadius: '50%', background: dotColor, flexShrink: 0 }} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 11, fontWeight: 600, color: 'rgba(203,213,225,0.9)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {session.ep_slug ?? '—'}
            {isEnded && <span style={{ marginLeft: 6, fontSize: 9, color: 'rgba(148,163,184,0.5)', fontWeight: 400 }}>ended</span>}
          </div>
          <div style={{ fontSize: 10, color: 'rgba(148,163,184,0.5)' }}>
            {session.session_id.slice(0, 8)}… · {ageStr} ago
            {ttlSecs !== null && <span style={{ marginLeft: 6, color: 'rgba(245,158,11,0.6)' }}>expires {ttlSecs}s</span>}
          </div>
        </div>
        <button onClick={onUnpin} title="Unpin" style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(148,163,184,0.4)', padding: 2, display: 'flex', alignItems: 'center' }} onMouseEnter={e => e.currentTarget.style.color = '#f87171'} onMouseLeave={e => e.currentTarget.style.color = 'rgba(148,163,184,0.4)'}>
          <span className="material-symbols-outlined" style={{ fontSize: 16 }}>close</span>
        </button>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: '10px 12px', display: 'flex', flexDirection: 'column', gap: 1 }}>
        {!runId && <div style={{ color: 'rgba(148,163,184,0.4)', fontSize: 12, textAlign: 'center', marginTop: 24 }}>No run ID — reconnect to pick up new run</div>}
        {runId && entries.length === 0 && <div style={{ color: 'rgba(148,163,184,0.4)', fontSize: 12, textAlign: 'center', marginTop: 24 }}>{connected ? 'Waiting for events…' : 'Connecting…'}</div>}
        {rendered.map(r =>
          r.isTokenBlock
            ? <span key={r.key} style={{ color: 'rgba(203,213,225,0.9)', fontSize: 12, wordBreak: 'break-word' }}>{r.text}</span>
            : r.entry ? <EventRow key={r.key} entry={r.entry} /> : null
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}

// ── MonitorView ───────────────────────────────────────────────────────────────

export interface MonitorViewProps {
  app: Application;
  agents: Agent[];
  token: string | null;
  onBack: () => void;
}

export function MonitorView({ app: initialApp, agents, token, onBack }: MonitorViewProps) {
  const [app] = useState(initialApp);
  const { sessions: liveSessions, connected } = useDashSessions(token, app.id);
  const [trackedSessions, setTrackedSessions] = useState<TrackedSession[]>([]);
  const [pinned, setPinned] = useState<TrackedSession[]>([]);
  const [selectedSession, setSelectedSession] = useState<TrackedSession | null>(null);
  const [monCfg, setMonCfg] = useState<MonitoringConfig>(MON_DEFAULTS);
  const [canvasOpen, setCanvasOpen] = useState(true);
  const [tick, setTick] = useState(0);
  const [hiddenSessions, setHiddenSessions] = useState<Set<string>>(new Set());
  const MAX_PINNED = 3;

  // Load monitoring config once
  useEffect(() => {
    themApi.getMonitoringConfig().then(setMonCfg).catch(() => {});
  }, []);

  // Re-render elapsed times every 5s
  useEffect(() => {
    const iv = setInterval(() => setTick(t => t + 1), 5000);
    return () => clearInterval(iv);
  }, []);
  void tick;

  // Single atomic effect: merge live sessions into tracked state, mark ended,
  // preserve TTL entries, and auto-pin new sessions when there's a free slot.
  useEffect(() => {
    const now = Date.now();
    const liveIds = new Set(liveSessions.map(s => s.session_id));

    setTrackedSessions(prev => {
      const next: TrackedSession[] = [];
      const newSessions: TrackedSession[] = [];

      // Update or expire existing tracked sessions
      for (const p of prev) {
        if (liveIds.has(p.session_id)) {
          // Still live — update fields, clear ended state
          const live = liveSessions.find(s => s.session_id === p.session_id)!;
          next.push({ ...live, _ended: false, _endedAt: undefined });
        } else if (!p._ended) {
          // Just ended — start TTL
          next.push({ ...p, _ended: true, _endedAt: now });
        } else if (p._endedAt && now - p._endedAt < SESSION_TTL_MS) {
          // Within TTL — keep
          next.push(p);
        }
        // else: expired — drop
      }

      // Add brand-new sessions not yet tracked
      const trackedIds = new Set(next.map(n => n.session_id));
      for (const s of liveSessions) {
        if (!trackedIds.has(s.session_id)) {
          const ts: TrackedSession = { ...s, _ended: false };
          next.push(ts);
          newSessions.push(ts);
        }
      }

      // Auto-pin new sessions if there's room (run outside setTrackedSessions
      // to avoid nested state updates — schedule via setTimeout)
      if (newSessions.length > 0) {
        setTimeout(() => {
          setPinned(prev => {
            let updated = prev;
            for (const s of newSessions) {
              if (updated.length >= MAX_PINNED) break;
              if (!updated.find(p => p.session_id === s.session_id)) {
                updated = [...updated, s];
              }
            }
            return updated;
          });
        }, 0);
      }

      return next;
    });
  }, [liveSessions]); // eslint-disable-line react-hooks/exhaustive-deps

  // Expire TTL entries every 10s
  useEffect(() => {
    const iv = setInterval(() => {
      const now = Date.now();
      setTrackedSessions(prev => {
        const next = prev.filter(p => !p._ended || !p._endedAt || now - p._endedAt < SESSION_TTL_MS);
        return next.length === prev.length ? prev : next;
      });
      setPinned(prev => {
        const next = prev.filter(p => !p._ended || !p._endedAt || Date.now() - p._endedAt < SESSION_TTL_MS);
        return next.length === prev.length ? prev : next;
      });
    }, 10000);
    return () => clearInterval(iv);
  }, []);

  const pin = useCallback((s: TrackedSession) => {
    setPinned(prev => {
      if (prev.find(p => p.session_id === s.session_id)) return prev;
      if (prev.length >= MAX_PINNED) return prev;
      return [...prev, s];
    });
  }, []);

  const unpin = useCallback((sessionId: string) => {
    setPinned(prev => prev.filter(p => p.session_id !== sessionId));
  }, []);

  // Keep pinned entries in sync with latest tracked state (_ended, run_id updates)
  useEffect(() => {
    setPinned(prev => {
      const next = prev.map(p => trackedSessions.find(t => t.session_id === p.session_id) ?? p);
      return next.every((n, i) => n === prev[i]) ? prev : next;
    });
  }, [trackedSessions]);

  async function handleTerminate(sid: string) {
    setHiddenSessions(h => new Set(h).add(sid));
    try {
      await themApi.disconnectSession(sid);
    } catch {
      setHiddenSessions(h => { const n = new Set(h); n.delete(sid); return n; });
    }
  }

  // Visible sessions: tracked (including TTL) minus hidden
  const visibleSessions = trackedSessions.filter(s => !hiddenSessions.has(s.session_id));
  const activeSessions = visibleSessions.filter(s => !s._ended);

  // ── Topology canvas data ───────────────────────────────────────────────────
  const epCountBySlug = new Map<string, number>();
  activeSessions.forEach(s => {
    if (s.ep_slug) epCountBySlug.set(s.ep_slug, (epCountBySlug.get(s.ep_slug) ?? 0) + 1);
  });

  const { nodes: baseNodes, edges: baseEdges } = buildNodesFromApp(app, agents);
  const activeEpNodeIds = new Set<string>();
  const activeOrchNodeIds = new Set<string>();

  const canvasNodes = baseNodes.map(n => {
    if (n.type === 'entryPoint' && n.data?.slug) {
      const count = epCountBySlug.get(n.data.slug as string) ?? 0;
      if (count > 0) activeEpNodeIds.add(n.id);
      return { ...n, data: { ...n.data, _sessCount: count, _heatStyle: heatmapStyle(count, monCfg, 'ep') } };
    }
    if (n.type === 'orchestrator') {
      const orchName = (n.data as any)?.name ?? '';
      const orchCount = activeSessions.filter(s => s.orchestrator_name === orchName).length;
      if (orchCount > 0) activeOrchNodeIds.add(n.id);
      return { ...n, data: { ...n.data, _sessCount: orchCount, _heatStyle: heatmapStyle(orchCount, monCfg, 'orch') } };
    }
    return n;
  });

  const activeAgentSlugs = new Set(activeSessions.flatMap(s => s.active_agents ?? []));

  const canvasEdges = baseEdges.map(e => {
    const isEpOrch    = activeEpNodeIds.has(e.source) && activeOrchNodeIds.has(e.target);
    const targetSlug  = (baseNodes.find(n => n.id === e.target)?.data as any)?.name ?? '';
    const isOrchAgent = activeOrchNodeIds.has(e.source) && activeAgentSlugs.has(targetSlug);

    if (isEpOrch) {
      return { ...e, animated: false, className: 'active-ep-orch', style: { stroke: '#00f0ff', strokeWidth: edgeStrokeWidth(activeSessions.length, monCfg) } };
    }
    if (isOrchAgent) {
      const orchCount = activeSessions.filter(s => s.active_agents?.includes(targetSlug)).length;
      return { ...e, animated: false, className: 'active-orch-agent', style: { stroke: C.purple, strokeWidth: edgeStrokeWidth(orchCount, monCfg) } };
    }
    return { ...e, animated: false, style: { stroke: 'rgba(148,163,184,0.18)', strokeWidth: 1, strokeDasharray: '4,4' } };
  });

  // Group sessions by EP for the sidebar
  const byEP = visibleSessions.reduce<Record<string, TrackedSession[]>>((acc, s) => {
    const k = s.ep_slug ?? 'unknown';
    if (!acc[k]) acc[k] = [];
    acc[k].push(s);
    return acc;
  }, {});

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg, overflow: 'hidden' }}>
      <style>{CANVAS_STYLES}{SESSIONS_STYLES}{MONITOR_STYLES}</style>

      {/* Top bar */}
      <div style={{ height: 52, flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12, padding: '0 20px', background: C.surfaceContainer, borderBottom: `1px solid ${C.outline}` }}>
        <button onClick={onBack} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '6px 12px', borderRadius: 8, border: `1px solid ${C.outline}`, background: 'transparent', color: C.textMuted, fontSize: 13, fontWeight: 500, cursor: 'pointer' }} onMouseEnter={e => { e.currentTarget.style.background = 'rgba(255,255,255,0.05)'; e.currentTarget.style.color = C.text; }} onMouseLeave={e => { e.currentTarget.style.background = 'transparent'; e.currentTarget.style.color = C.textMuted; }}>
          <span className="material-symbols-outlined" style={{ fontSize: 16 }}>arrow_back</span>
          Back
        </button>
        <div style={{ width: 1, height: 24, background: C.outline, flexShrink: 0 }} />
        <span className="material-symbols-outlined" style={{ fontSize: 17, color: C.cyan }}>monitor_heart</span>
        <span style={{ fontSize: 15, fontWeight: 700, color: C.text }}>{app.name}</span>
        <span style={{ fontSize: 12, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>/{app.slug}</span>
        <span style={{ fontSize: 12, color: C.textMuted }}>— Monitor</span>
        <div style={{ flex: 1 }} />
        {/* Canvas toggle */}
        <button onClick={() => setCanvasOpen(o => !o)} style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '5px 10px', borderRadius: 7, border: `1px solid ${C.outline}`, background: canvasOpen ? 'rgba(0,240,255,0.06)' : 'transparent', color: canvasOpen ? C.cyan : C.textMuted, fontSize: 12, cursor: 'pointer' }} title={canvasOpen ? 'Hide topology' : 'Show topology'}>
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>hub</span>
          Topology
        </button>
        {/* Live indicator */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '4px 10px', borderRadius: 20, background: connected ? 'rgba(74,222,128,0.08)' : 'rgba(255,255,255,0.04)', border: `1px solid ${connected ? C.greenBorder : 'rgba(255,255,255,0.1)'}` }}>
          <div style={{ width: 7, height: 7, borderRadius: '50%', background: connected ? C.green : C.textMuted, boxShadow: connected ? '0 0 6px rgba(74,222,128,0.8)' : 'none', animation: connected ? 'sess-pulse 2s ease-in-out infinite' : 'none' }} />
          <span style={{ fontSize: 11, color: connected ? C.green : C.textMuted, fontWeight: 600 }}>{connected ? 'Live' : 'Connecting…'}</span>
        </div>
        {/* Session count */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '4px 12px', borderRadius: 20, background: activeSessions.length > 0 ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.03)', border: `1px solid ${activeSessions.length > 0 ? C.cyanBorder : 'rgba(255,255,255,0.08)'}` }}>
          <span className="material-symbols-outlined" style={{ fontSize: 13, color: activeSessions.length > 0 ? C.cyan : C.textMuted }}>person</span>
          <span style={{ fontSize: 13, fontWeight: 700, color: activeSessions.length > 0 ? C.cyan : C.textMuted }}>{activeSessions.length} active</span>
          {visibleSessions.length > activeSessions.length && (
            <span style={{ fontSize: 11, color: 'rgba(245,158,11,0.7)', marginLeft: 2 }}>+{visibleSessions.length - activeSessions.length} recent</span>
          )}
        </div>
      </div>

      {/* Topology canvas (collapsible) */}
      {canvasOpen && (
        <div style={{ height: 220, flexShrink: 0, borderBottom: `1px solid ${C.outline}`, position: 'relative' }}>
          <ReactFlowProvider>
            <ReactFlow nodes={canvasNodes} edges={canvasEdges} nodeTypes={RO_NODE_TYPES} fitView fitViewOptions={{ padding: 0.3 }} nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} panOnDrag zoomOnScroll style={{ background: C.surfaceLow }}>
              <Background variant={BackgroundVariant.Dots} gap={24} size={1} color="rgba(148,163,184,0.12)" />
              <Controls showInteractive={false} style={{ background: C.surface, border: `1px solid ${C.outline}` }} />
            </ReactFlow>
          </ReactFlowProvider>
          {activeSessions.length === 0 && connected && (
            <div style={{ position: 'absolute', top: '50%', left: '50%', transform: 'translate(-50%,-50%)', pointerEvents: 'none', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 32, color: 'rgba(148,163,184,0.2)' }}>person_off</span>
              <span style={{ fontSize: 12, color: 'rgba(148,163,184,0.35)' }}>No active sessions</span>
            </div>
          )}
        </div>
      )}

      {/* Main body: session sidebar + feed columns */}
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        {/* Left sidebar — session list */}
        <div style={{ width: 240, flexShrink: 0, background: C.surfaceContainer, borderRight: `1px solid ${C.outline}`, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          <div style={{ padding: '10px 12px 8px', borderBottom: `1px solid ${C.outline}`, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: 11, fontWeight: 700, color: C.text, letterSpacing: 0.2 }}>Sessions</span>
            <span style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, background: 'rgba(255,255,255,0.05)', borderRadius: 8, padding: '1px 7px', border: `1px solid ${C.outline}` }}>{visibleSessions.length}</span>
          </div>
          <div style={{ flex: 1, overflowY: 'auto', padding: '8px 8px 0' }}>
            {visibleSessions.length === 0 && connected && (
              <div style={{ padding: '28px 12px', textAlign: 'center', color: C.textMuted, fontSize: 12 }}>Waiting for sessions…</div>
            )}
            {!connected && <div style={{ padding: '28px 12px', textAlign: 'center', color: C.textMuted, fontSize: 12 }}>Connecting…</div>}
            {Object.entries(byEP).map(([ep, epSessions]) => (
              <div key={ep} style={{ marginBottom: 12 }}>
                <div style={{ fontSize: 9, fontWeight: 700, color: 'rgba(148,163,184,0.45)', textTransform: 'uppercase', letterSpacing: '0.08em', marginBottom: 4, paddingLeft: 4 }}>{ep}</div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                  {epSessions.map(s => {
                    const isPinned = !!pinned.find(p => p.session_id === s.session_id);
                    const isSelected = selectedSession?.session_id === s.session_id;
                    const epType = app.entry_points?.find(ep => ep.slug === s.ep_slug)?.entry_point_type ?? 'websocket';
                    const epColor = epType === 'sse' ? '#a78bfa' : s._ended ? 'rgba(148,163,184,0.4)' : C.cyan;
                    return (
                      <div
                        key={s.session_id}
                        className={`sess-row${isSelected ? ' selected' : ''}`}
                        style={{ opacity: s._ended ? 0.55 : 1 }}
                        onClick={() => setSelectedSession(isSelected ? null : s)}
                      >
                        <div style={{ width: 28, height: 28, borderRadius: '50%', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', background: `${epColor}18`, border: `1.5px solid ${epColor}44` }}>
                          <span className="material-symbols-outlined" style={{ fontSize: 13, color: epColor }}>{EP_MS_ICON[epType] ?? 'bolt'}</span>
                        </div>
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 1 }}>
                            <span style={{ fontSize: 11, fontWeight: 600, color: s._ended ? C.textMuted : C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.ep_slug ?? 'direct'}</span>
                            {s._ended && <span style={{ fontSize: 9, color: 'rgba(245,158,11,0.65)', background: 'rgba(245,158,11,0.1)', borderRadius: 3, padding: '0 4px', flexShrink: 0 }}>ended</span>}
                          </div>
                          <div style={{ fontSize: 10, color: C.textMuted }}>{elapsed(s.started_at)}</div>
                        </div>
                        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 2, flexShrink: 0 }}>
                          {!s._ended && (
                            <button
                              title={isPinned ? 'Already monitoring' : 'Pin to monitor'}
                              onClick={e => { e.stopPropagation(); pin(s); }}
                              style={{ width: 22, height: 22, borderRadius: 5, border: isPinned ? '1px solid rgba(99,102,241,0.5)' : `1px solid ${C.outline}`, background: isPinned ? 'rgba(99,102,241,0.15)' : 'transparent', cursor: isPinned ? 'default' : 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                            >
                              <span className="material-symbols-outlined" style={{ fontSize: 12, color: isPinned ? '#818cf8' : C.textMuted }}>monitor_heart</span>
                            </button>
                          )}
                          {!s._ended && (
                            <button
                              title="Terminate session"
                              onClick={e => { e.stopPropagation(); handleTerminate(s.session_id); }}
                              style={{ width: 22, height: 22, borderRadius: 5, border: '1px solid rgba(239,68,68,0.3)', background: 'transparent', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                              onMouseEnter={e => e.currentTarget.style.background = 'rgba(239,68,68,0.1)'}
                              onMouseLeave={e => e.currentTarget.style.background = 'transparent'}
                            >
                              <span className="material-symbols-outlined" style={{ fontSize: 12, color: '#ef4444' }}>power_settings_new</span>
                            </button>
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}
            {visibleSessions.length > 0 && pinned.length < MAX_PINNED && activeSessions.length > 0 && (
              <div style={{ fontSize: 10, color: 'rgba(148,163,184,0.3)', textAlign: 'center', marginTop: 4, paddingBottom: 8 }}>
                Click <span className="material-symbols-outlined" style={{ fontSize: 10, verticalAlign: 'middle' }}>monitor_heart</span> to monitor
              </div>
            )}
            {pinned.length >= MAX_PINNED && (
              <div style={{ fontSize: 10, color: 'rgba(251,146,60,0.6)', textAlign: 'center', marginTop: 4, paddingBottom: 8 }}>Max {MAX_PINNED} feeds open</div>
            )}
          </div>

          {/* Session detail drawer */}
          {selectedSession && (() => {
            const s = selectedSession;
            const epType = app.entry_points?.find(ep => ep.slug === s.ep_slug)?.entry_point_type ?? 'websocket';
            return (
              <div style={{ borderTop: `1px solid ${C.outline}`, padding: '12px 14px', background: 'rgba(0,240,255,0.02)', flexShrink: 0, maxHeight: 220, overflowY: 'auto' }}>
                <div style={{ fontSize: 11, fontWeight: 700, color: C.cyan, marginBottom: 8, letterSpacing: 0.3 }}>SESSION DETAIL</div>
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
                  <div key={label} style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4, gap: 8 }}>
                    <span style={{ fontSize: 10, color: C.textMuted, flexShrink: 0 }}>{label}</span>
                    <span style={{ fontSize: 10, color: C.text, fontFamily: ['Session ID', 'Context ID', 'Pod'].includes(label) ? 'JetBrains Mono, monospace' : 'inherit', textAlign: 'right', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={value}>{value}</span>
                  </div>
                ))}
                <div style={{ display: 'flex', gap: 5, marginTop: 6 }}>
                  {!s._ended && (
                    <button onClick={() => { handleTerminate(s.session_id); setSelectedSession(null); }} style={{ flex: 1, padding: '5px 0', borderRadius: 5, border: '1px solid rgba(239,68,68,0.45)', background: 'rgba(239,68,68,0.06)', color: '#ef4444', fontSize: 11, cursor: 'pointer' }} onMouseEnter={e => e.currentTarget.style.background = 'rgba(239,68,68,0.14)'} onMouseLeave={e => e.currentTarget.style.background = 'rgba(239,68,68,0.06)'}>
                      Terminate
                    </button>
                  )}
                  <button onClick={() => setSelectedSession(null)} style={{ flex: 1, padding: '5px 0', borderRadius: 5, border: `1px solid ${C.outline}`, background: 'transparent', color: C.textMuted, fontSize: 11, cursor: 'pointer' }} onMouseEnter={e => e.currentTarget.style.background = 'rgba(255,255,255,0.04)'} onMouseLeave={e => e.currentTarget.style.background = 'transparent'}>
                    Close
                  </button>
                </div>
              </div>
            );
          })()}
        </div>

        {/* Right panel — feed columns */}
        <div style={{ flex: 1, display: 'flex', overflow: 'hidden', background: C.bg }}>
          {pinned.length === 0 ? (
            <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column', gap: 12, color: 'rgba(148,163,184,0.3)' }}>
              <span className="material-symbols-outlined" style={{ fontSize: 40, opacity: 0.3 }}>monitor_heart</span>
              <span style={{ fontSize: 13 }}>
                {activeSessions.length > 0 ? 'Click the monitor icon on a session to watch its events' : 'No active sessions — events will appear here when sessions start'}
              </span>
            </div>
          ) : (
            pinned.map(s => (
              <RunFeedColumn key={s.session_id} session={s} token={token!} onUnpin={() => unpin(s.session_id)} />
            ))
          )}
        </div>
      </div>
    </div>
  );
}

// ── Extra monitor styles ──────────────────────────────────────────────────────

const MONITOR_STYLES = `
  .monitor-sess-list::-webkit-scrollbar { width: 4px; }
  .monitor-sess-list::-webkit-scrollbar-track { background: transparent; }
  .monitor-sess-list::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.1); border-radius: 4px; }
`;
