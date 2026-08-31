'use client';
import { useState, useEffect, useRef, useCallback } from 'react';
import { C } from '../constants';
import type { Application, SessionInfo } from '@/lib/api';
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

// ── useRunFeed hook ───────────────────────────────────────────────────────────

function useRunFeed(token: string | null, runId: string | null): {
  entries: FeedEntry[];
  connected: boolean;
  done: boolean;
} {
  const [entries, setEntries] = useState<FeedEntry[]>([]);
  const [connected, setConnected] = useState(false);
  const [done, setDone] = useState(false);

  useEffect(() => {
    if (!token || !runId) return;
    setEntries([]);
    setDone(false);

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
        } catch { /* ignore malformed */ }
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
  }, [token, runId]);

  return { entries, connected, done };
}

// ── EventRow ─────────────────────────────────────────────────────────────────

function EventRow({ entry }: { entry: FeedEntry }) {
  const [open, setOpen] = useState(false);
  const ev = entry.event;

  if (ev.type === 'token') {
    return (
      <span style={{ color: 'rgba(203,213,225,0.9)', fontSize: 12, wordBreak: 'break-word' }}>
        {String(ev.content ?? '')}
      </span>
    );
  }

  if (ev.type === 'tool_call') {
    return (
      <div style={{ margin: '4px 0' }}>
        <button
          onClick={() => setOpen(o => !o)}
          style={{ display: 'flex', alignItems: 'center', gap: 6, background: 'rgba(99,102,241,0.1)', border: '1px solid rgba(99,102,241,0.3)', borderRadius: 6, padding: '3px 8px', cursor: 'pointer', color: '#a5b4fc', fontSize: 11 }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 13 }}>build</span>
          {String(ev.name ?? 'tool')}
          <span className="material-symbols-outlined" style={{ fontSize: 11, opacity: 0.6 }}>{open ? 'expand_less' : 'expand_more'}</span>
        </button>
        {open && (
          <pre style={{ margin: '4px 0 0 0', padding: '6px 8px', background: 'rgba(0,0,0,0.3)', borderRadius: 4, fontSize: 10, color: 'rgba(203,213,225,0.7)', overflowX: 'auto', maxHeight: 120 }}>
            {JSON.stringify(ev.input, null, 2)}
          </pre>
        )}
      </div>
    );
  }

  if (ev.type === 'tool_result') {
    return (
      <div style={{ margin: '2px 0 4px 16px' }}>
        <button
          onClick={() => setOpen(o => !o)}
          style={{ display: 'flex', alignItems: 'center', gap: 6, background: 'rgba(34,197,94,0.08)', border: '1px solid rgba(34,197,94,0.2)', borderRadius: 6, padding: '3px 8px', cursor: 'pointer', color: '#86efac', fontSize: 11 }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 13 }}>check_circle</span>
          {String(ev.name ?? 'result')}
          <span className="material-symbols-outlined" style={{ fontSize: 11, opacity: 0.6 }}>{open ? 'expand_less' : 'expand_more'}</span>
        </button>
        {open && (
          <pre style={{ margin: '4px 0 0 0', padding: '6px 8px', background: 'rgba(0,0,0,0.3)', borderRadius: 4, fontSize: 10, color: 'rgba(203,213,225,0.7)', overflowX: 'auto', maxHeight: 120 }}>
            {JSON.stringify(ev.output, null, 2)}
          </pre>
        )}
      </div>
    );
  }

  if (ev.type === 'done') {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, color: C.green, fontSize: 12, margin: '6px 0 2px' }}>
        <span className="material-symbols-outlined" style={{ fontSize: 14 }}>check_circle</span>
        Done
      </div>
    );
  }

  if (ev.type === 'error') {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, color: '#f87171', fontSize: 12, margin: '6px 0 2px' }}>
        <span className="material-symbols-outlined" style={{ fontSize: 14 }}>error</span>
        {String(ev.message ?? 'error')}
      </div>
    );
  }

  if (ev.type === 'iteration_start') {
    return (
      <div style={{ fontSize: 10, color: 'rgba(148,163,184,0.5)', borderTop: '1px solid rgba(255,255,255,0.05)', margin: '6px 0 4px', paddingTop: 4 }}>
        — iteration —
      </div>
    );
  }

  return null;
}

// ── RunFeedColumn ─────────────────────────────────────────────────────────────

function RunFeedColumn({ session, token, onUnpin }: {
  session: SessionInfo;
  token: string;
  onUnpin: () => void;
}) {
  const runId = (session as SessionInfo & { run_id?: string }).run_id ?? null;
  const { entries, connected, done } = useRunFeed(token, runId);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [entries.length]);

  // Group consecutive token events into text blocks
  const rendered: Array<{ key: string; isTokenBlock: boolean; text?: string; entry?: FeedEntry }> = [];
  let tokenBuf = '';
  let tokenKey = '';
  for (const e of entries) {
    if (e.event.type === 'token') {
      if (!tokenKey) tokenKey = e.id;
      tokenBuf += String(e.event.content ?? '');
    } else {
      if (tokenBuf) {
        rendered.push({ key: tokenKey, isTokenBlock: true, text: tokenBuf });
        tokenBuf = '';
        tokenKey = '';
      }
      rendered.push({ key: e.id, isTokenBlock: false, entry: e });
    }
  }
  if (tokenBuf) rendered.push({ key: tokenKey, isTokenBlock: true, text: tokenBuf });

  const epType = session.ep_slug ?? '—';
  const age = session.started_at
    ? Math.floor((Date.now() - new Date(session.started_at).getTime()) / 1000)
    : 0;
  const ageStr = age < 60 ? `${age}s` : `${Math.floor(age / 60)}m`;

  return (
    <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', borderLeft: '1px solid rgba(255,255,255,0.06)' }}>
      {/* Column header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderBottom: '1px solid rgba(255,255,255,0.06)', background: 'rgba(255,255,255,0.02)' }}>
        <span style={{ width: 6, height: 6, borderRadius: '50%', background: done ? C.green : connected ? '#60a5fa' : 'rgba(255,255,255,0.2)', flexShrink: 0 }} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 11, fontWeight: 600, color: 'rgba(203,213,225,0.9)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {epType}
          </div>
          <div style={{ fontSize: 10, color: 'rgba(148,163,184,0.5)' }}>
            {session.session_id.slice(0, 8)}… · {ageStr} ago
          </div>
        </div>
        <button
          onClick={onUnpin}
          title="Unpin"
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(148,163,184,0.4)', padding: 2, display: 'flex', alignItems: 'center' }}
          onMouseEnter={e => e.currentTarget.style.color = '#f87171'}
          onMouseLeave={e => e.currentTarget.style.color = 'rgba(148,163,184,0.4)'}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 16 }}>close</span>
        </button>
      </div>

      {/* Event stream */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '10px 12px', display: 'flex', flexDirection: 'column', gap: 1 }}>
        {!runId && (
          <div style={{ color: 'rgba(148,163,184,0.4)', fontSize: 12, textAlign: 'center', marginTop: 24 }}>
            No run ID — reconnect to pick up new run
          </div>
        )}
        {runId && entries.length === 0 && (
          <div style={{ color: 'rgba(148,163,184,0.4)', fontSize: 12, textAlign: 'center', marginTop: 24 }}>
            {connected ? 'Waiting for events…' : 'Connecting…'}
          </div>
        )}
        {rendered.map(r =>
          r.isTokenBlock ? (
            <span key={r.key} style={{ color: 'rgba(203,213,225,0.9)', fontSize: 12, wordBreak: 'break-word' }}>
              {r.text}
            </span>
          ) : r.entry ? (
            <EventRow key={r.key} entry={r.entry} />
          ) : null
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}

// ── SessionRow ────────────────────────────────────────────────────────────────

function SessionRow({ session, pinned, onPin }: {
  session: SessionInfo;
  pinned: boolean;
  onPin: () => void;
}) {
  const age = session.started_at
    ? Math.floor((Date.now() - new Date(session.started_at).getTime()) / 1000)
    : 0;
  const ageStr = age < 60 ? `${age}s` : `${Math.floor(age / 60)}m`;

  return (
    <button
      onClick={onPin}
      title={pinned ? 'Already watching' : 'Pin to monitor'}
      style={{
        width: '100%', textAlign: 'left', background: pinned ? 'rgba(99,102,241,0.12)' : 'none',
        border: pinned ? '1px solid rgba(99,102,241,0.35)' : '1px solid transparent',
        borderRadius: 7, padding: '8px 10px', cursor: pinned ? 'default' : 'pointer',
        display: 'flex', flexDirection: 'column', gap: 3,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ width: 6, height: 6, borderRadius: '50%', background: pinned ? '#818cf8' : C.green, flexShrink: 0 }} />
        <span style={{ fontSize: 12, fontWeight: 600, color: 'rgba(203,213,225,0.9)' }}>
          {session.ep_slug ?? 'unknown ep'}
        </span>
        <span style={{ marginLeft: 'auto', fontSize: 10, color: 'rgba(148,163,184,0.5)' }}>{ageStr}</span>
      </div>
      <div style={{ fontSize: 10, color: 'rgba(148,163,184,0.5)', paddingLeft: 12 }}>
        {session.session_id.slice(0, 16)}…
      </div>
      {session.active_agents && session.active_agents.length > 0 && (
        <div style={{ fontSize: 10, color: 'rgba(148,163,184,0.4)', paddingLeft: 12 }}>
          {session.active_agents.slice(0, 3).join(', ')}
        </div>
      )}
    </button>
  );
}

// ── MonitorView ───────────────────────────────────────────────────────────────

interface MonitorViewProps {
  app: Application;
  token: string | null;
  onBack: () => void;
}

export function MonitorView({ app, token, onBack }: MonitorViewProps) {
  const { sessions, connected } = useDashSessions(token, app.id);
  const [pinned, setPinned] = useState<SessionInfo[]>([]);

  const MAX_PINNED = 3;

  const pin = useCallback((s: SessionInfo) => {
    setPinned(prev => {
      if (prev.find(p => p.session_id === s.session_id)) return prev;
      if (prev.length >= MAX_PINNED) return prev;
      return [...prev, s];
    });
  }, []);

  const unpin = useCallback((sessionId: string) => {
    setPinned(prev => prev.filter(p => p.session_id !== sessionId));
  }, []);

  // Remove pinned sessions that ended
  useEffect(() => {
    setPinned(prev => prev.filter(p => sessions.find(s => s.session_id === p.session_id)));
  }, [sessions]);

  // Group sessions by EP slug
  const byEP = sessions.reduce<Record<string, SessionInfo[]>>((acc, s) => {
    const k = s.ep_slug ?? 'unknown';
    if (!acc[k]) acc[k] = [];
    acc[k].push(s);
    return acc;
  }, {});

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: C.bg }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '14px 24px', borderBottom: `1px solid rgba(255,255,255,0.06)`, flexShrink: 0 }}>
        <button
          onClick={onBack}
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(148,163,184,0.6)', display: 'flex', alignItems: 'center', gap: 4, fontSize: 13, padding: 0 }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 16 }}>arrow_back</span>
          Back
        </button>
        <span style={{ color: 'rgba(255,255,255,0.15)', fontSize: 16 }}>|</span>
        <span style={{ fontSize: 15, fontWeight: 700, color: 'rgba(203,213,225,0.95)' }}>{app.name}</span>
        <span style={{ fontSize: 13, color: 'rgba(148,163,184,0.5)' }}>— Live Monitor</span>
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 6 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: connected ? C.green : 'rgba(255,255,255,0.2)' }} />
          <span style={{ fontSize: 11, color: 'rgba(148,163,184,0.5)' }}>
            {connected ? `${sessions.length} active` : 'connecting…'}
          </span>
        </div>
      </div>

      {/* Body */}
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        {/* Left panel — session list */}
        <div style={{ width: 220, flexShrink: 0, borderRight: '1px solid rgba(255,255,255,0.06)', overflowY: 'auto', padding: '12px 8px', display: 'flex', flexDirection: 'column', gap: 16 }}>
          {sessions.length === 0 ? (
            <div style={{ color: 'rgba(148,163,184,0.4)', fontSize: 12, textAlign: 'center', marginTop: 32 }}>
              No active sessions
            </div>
          ) : (
            Object.entries(byEP).map(([ep, epSessions]) => (
              <div key={ep}>
                <div style={{ fontSize: 10, fontWeight: 600, color: 'rgba(148,163,184,0.45)', textTransform: 'uppercase', letterSpacing: '0.08em', marginBottom: 6, paddingLeft: 4 }}>
                  {ep}
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                  {epSessions.map(s => (
                    <SessionRow
                      key={s.session_id}
                      session={s}
                      pinned={!!pinned.find(p => p.session_id === s.session_id)}
                      onPin={() => pin(s)}
                    />
                  ))}
                </div>
              </div>
            ))
          )}
          {sessions.length > 0 && pinned.length < MAX_PINNED && (
            <div style={{ fontSize: 10, color: 'rgba(148,163,184,0.3)', textAlign: 'center', marginTop: 4 }}>
              Click a session to monitor it
            </div>
          )}
          {pinned.length >= MAX_PINNED && (
            <div style={{ fontSize: 10, color: 'rgba(251,146,60,0.6)', textAlign: 'center', marginTop: 4 }}>
              Max {MAX_PINNED} feeds open
            </div>
          )}
        </div>

        {/* Right panel — feed columns */}
        <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
          {pinned.length === 0 ? (
            <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column', gap: 12, color: 'rgba(148,163,184,0.3)' }}>
              <span className="material-symbols-outlined" style={{ fontSize: 40, opacity: 0.3 }}>monitor_heart</span>
              <span style={{ fontSize: 13 }}>Select a session from the left to start monitoring</span>
            </div>
          ) : (
            pinned.map(s => (
              <RunFeedColumn
                key={s.session_id}
                session={s}
                token={token!}
                onUnpin={() => unpin(s.session_id)}
              />
            ))
          )}
        </div>
      </div>
    </div>
  );
}
