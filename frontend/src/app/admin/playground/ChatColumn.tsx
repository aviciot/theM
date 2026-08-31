'use client';
import { useEffect, useRef, useState, useCallback } from 'react';
import { themApi, type ContextSession } from '@/lib/api';
import {
  type ConnTarget, type ChatMsg, type FileMsg, type AgentActivity, type TraceEvent, type RecordingState,
  targetId, targetStorageKey, targetWsUrl, getBridgeWs,
} from './playgroundTypes';
import { MarkdownText } from './MarkdownRenderer';
import { TabBtn, TraceTab, TasksTab, ArtifactsTab, SessionsTab, type DebugTab } from './DebugPanel';

// ── ActivityBar ───────────────────────────────────────────────────────────

export function ActivityBar({ activities }: { activities: AgentActivity[] }) {
  const [, setElapsedTick] = useState(0);

  useEffect(() => {
    if (activities.length === 0) return;
    const t = setInterval(() => setElapsedTick(n => n + 1), 1000);
    return () => clearInterval(t);
  }, [activities.length]);

  if (activities.length === 0) return null;

  return (
    <div style={{
      borderTop: '1px solid var(--tm-border)',
      background: 'var(--tm-surface)',
      padding: '5px 20px',
      display: 'flex',
      flexDirection: 'row',
      flexWrap: 'wrap',
      gap: '4px 20px',
      animation: 'fadeSlideUp 0.2s ease-out',
    }}>
      {activities.map(a => {
        const isTerminal = ['TASK_STATE_COMPLETED', 'completed'].includes(a.displayState);
        const isFailed = ['TASK_STATE_FAILED', 'failed', 'TASK_STATE_CANCELED', 'canceled'].includes(a.displayState);
        const isWorking = !isTerminal && !isFailed;
        const elapsedS = (a.elapsed_ms / 1000).toFixed(1);
        const stateLabel = a.displayState.replace(/^TASK_STATE_/, '').toLowerCase();

        return (
          <div key={a.agent} style={{
            display: 'flex', alignItems: 'center', gap: 6,
            animation: 'fadeSlideUp 0.15s ease-out',
          }}>
            {isWorking ? (
              <span style={{
                display: 'inline-block', width: 8, height: 8,
                border: '1.5px solid #7c3aed', borderTopColor: '#a78bfa',
                borderRadius: '50%', animation: 'spin 0.7s linear infinite', flexShrink: 0,
              }} />
            ) : (
              <span style={{ fontSize: 10, color: isFailed ? '#f87171' : '#4edea3', fontWeight: 700 }}>
                {isFailed ? '✗' : '✓'}
              </span>
            )}
            <span style={{ fontSize: 11, color: isWorking ? '#a78bfa' : isFailed ? '#f87171' : '#4edea3', fontFamily: 'monospace' }}>
              {a.agent}
            </span>
            <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>
              {isWorking ? `${stateLabel}…` : stateLabel}
            </span>
            <span style={{ fontSize: 10, color: '#6b7280', fontFamily: 'monospace' }}>
              {elapsedS}s
            </span>
          </div>
        );
      })}
    </div>
  );
}

// ── MicIcon ───────────────────────────────────────────────────────────────

function MicIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
      <path d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.93V21h2v-3.07A7 7 0 0 0 19 11h-2z"/>
    </svg>
  );
}

// ── Spinner ───────────────────────────────────────────────────────────────

function Spinner() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
      <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" style={{ animation: 'spin 1s linear infinite', transformOrigin: 'center' }} />
    </svg>
  );
}

// ── copyToClipboard ───────────────────────────────────────────────────────

function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(text);
  return new Promise((resolve, reject) => {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;top:-9999px;left:-9999px;opacity:0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    ok ? resolve() : reject(new Error('execCommand failed'));
  });
}

// ── MsgBubble ─────────────────────────────────────────────────────────────

function MsgBubble({ msg, color }: { msg: ChatMsg; color: string }) {
  const [copied, setCopied] = useState(false);
  const [hovered, setHovered] = useState(false);

  function handleCopy() {
    copyToClipboard(msg.text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }).catch(() => {});
  }

  const isUser = msg.role === 'user';
  const showActions = hovered && msg.text && !msg.pending;

  return (
    <div
      style={{ maxWidth: '78%', display: 'flex', flexDirection: 'column', alignItems: isUser ? 'flex-end' : 'flex-start' }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <div style={{ padding: '9px 13px', borderRadius: isUser ? '14px 14px 4px 14px' : '14px 14px 14px 4px', background: isUser ? color : 'var(--tm-surface)', color: isUser ? '#fff' : 'var(--tm-text)', fontSize: 13, lineHeight: 1.5, wordBreak: 'break-word' }}>
        {msg.pending && !msg.text ? <span style={{ opacity: 0.5 }}>thinking…</span> : isUser ? <span dir="auto" style={{ whiteSpace: 'pre-wrap' }}>{msg.text}</span> : <div dir="auto"><MarkdownText text={msg.text} /></div>}
      </div>
      <div style={{ height: 24, display: 'flex', alignItems: 'center', paddingTop: 2, opacity: showActions ? 1 : 0, transition: 'opacity 0.12s', pointerEvents: showActions ? 'auto' : 'none' }}>
        <button
          onClick={handleCopy}
          title={copied ? 'Copied!' : 'Copy'}
          style={{
            width: 24, height: 24, border: 'none', borderRadius: 6, background: 'transparent',
            color: copied ? '#10b981' : 'var(--tm-text-muted)', cursor: 'pointer',
            display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 0,
            transition: 'color 0.15s, background 0.15s',
          }}
          onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.background = 'var(--tm-surface)'; }}
          onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.background = 'transparent'; }}
        >
          {copied ? (
            <svg width="13" height="13" viewBox="0 0 12 12" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M2 6l3 3 5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          ) : (
            <svg width="13" height="13" viewBox="0 0 12 12" fill="none" xmlns="http://www.w3.org/2000/svg">
              <rect x="4" y="1" width="7" height="8" rx="1.5" stroke="currentColor" strokeWidth="1.2"/>
              <path d="M1 4h2v6a1 1 0 001 1h5v1.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
            </svg>
          )}
        </button>
      </div>
    </div>
  );
}

// ── ChatColumn ────────────────────────────────────────────────────────────

export interface ChatColumnProps {
  target: ConnTarget;
  color: string;
  sharedInput?: string | null;
  onSharedSent?: () => void;
  showHeader?: boolean;
  compact?: boolean;
}

export function ChatColumn({ target, color, sharedInput, onSharedSent, showHeader = true, compact = false }: ChatColumnProps) {
  const [messages, setMessages] = useState<ChatMsg[]>([]);
  const [trace, setTrace] = useState<TraceEvent[]>([]);
  const [input, setInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [voiceEnabled, setVoiceEnabled] = useState(false);
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [speaking, setSpeaking] = useState(false);
  const [recordingState, setRecordingState] = useState<RecordingState>('idle');
  const recordingStateRef = useRef<RecordingState>('idle');
  const setRecState = (s: RecordingState) => { recordingStateRef.current = s; setRecordingState(s); };
  const [, setMediaRecorder] = useState<MediaRecorder | null>(null);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const voiceFetchAbortRef = useRef<AbortController | null>(null);
  const voiceAudioRef = useRef<HTMLAudioElement | null>(null);
  const [debugTab, setDebugTab] = useState<DebugTab>('trace');
  const [contextId, setContextId] = useState<string | null>(null);
  const [restoredSession, setRestoredSession] = useState<ContextSession | null>(null);
  const [activities, setActivities] = useState<AgentActivity[]>([]);
  const activitiesRef = useRef<AgentActivity[]>([]);
  const [panelHeight, setPanelHeight] = useState(280);
  const [inputHeight, setInputHeight] = useState(90);
  const panelDragY = useRef<number | null>(null);
  const panelDragH = useRef<number>(280);
  const inputDragY = useRef<number | null>(null);
  const inputDragH = useRef<number>(90);

  const chatWs = useRef<WebSocket | null>(null);
  const dashWs = useRef<WebSocket | null>(null);
  const runId = useRef<string | null>(null);
  const assistantBuf = useRef('');
  const chatBottom = useRef<HTMLDivElement>(null);
  const traceBottom = useRef<HTMLDivElement | null>(null);
  const busyRef = useRef(false);
  const didRestoreRef = useRef(false);

  useEffect(() => { busyRef.current = busy; }, [busy]);

  const onPanelDragStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    panelDragY.current = e.clientY;
    panelDragH.current = panelHeight;
    const onMove = (me: MouseEvent) => {
      if (panelDragY.current === null) return;
      const delta = panelDragY.current - me.clientY;
      const next = Math.min(Math.max(panelDragH.current + delta, 120), Math.floor(window.innerHeight * 0.75));
      setPanelHeight(next);
    };
    const onUp = () => {
      panelDragY.current = null;
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, [panelHeight]);

  const onInputDragStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    inputDragY.current = e.clientY;
    inputDragH.current = inputHeight;
    const onMove = (me: MouseEvent) => {
      if (inputDragY.current === null) return;
      const delta = inputDragY.current - me.clientY;
      const next = Math.min(Math.max(inputDragH.current + delta, 56), 320);
      setInputHeight(next);
    };
    const onUp = () => {
      inputDragY.current = null;
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, [inputHeight]);

  const orchName = target.kind === 'orchestrator' ? target.name : target.orchName;

  useEffect(() => {
    if (target.kind === 'entrypoint' && target.epType === 'voice') {
      setVoiceEnabled(true);
      setTtsEnabled(true);
      return;
    }
    const name = target.kind === 'orchestrator' ? target.name : target.orchName;
    if (!name) return;
    themApi.orchestrators().then(list => {
      const o = list.find(o => o.name === name);
      setVoiceEnabled(o?.voice_enabled ?? false);
      setTtsEnabled(o?.tts_enabled ?? false);
    }).catch(() => {});
  }, [targetId(target)]);  // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (didRestoreRef.current) return;
    didRestoreRef.current = true;
    const storageKey = `them:playground:ctx:${targetStorageKey(target)}`;
    const saved = localStorage.getItem(storageKey);
    if (!saved) return;
    themApi.contexts().then(sessions => {
      const match = sessions.find(s => s.context_id === saved);
      if (!match) { localStorage.removeItem(storageKey); return; }
      setRestoredSession(match);
      setContextId(saved);
      themApi.contextMessages(saved, 200).then(msgs => {
        const chatMsgs: ChatMsg[] = msgs
          .filter(m => m.text)
          .map(m => ({ role: (m.role === 'user' ? 'user' : 'assistant') as 'user' | 'assistant', text: m.text }));
        if (chatMsgs.length > 0) setMessages(chatMsgs);
      }).catch(() => {});
    }).catch(() => {});
  }, []);  // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState !== 'visible') return;
      if (!contextId || chatWs.current) return;
      themApi.contextMessages(contextId, 200).then(msgs => {
        const chatMsgs: ChatMsg[] = msgs
          .filter(m => m.text)
          .map(m => ({ role: (m.role === 'user' ? 'user' : 'assistant') as 'user' | 'assistant', text: m.text }));
        if (chatMsgs.length > 0) setMessages(chatMsgs);
      }).catch(() => {});
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => document.removeEventListener('visibilitychange', onVisible);
  }, [contextId]);

  useEffect(() => {
    if (!contextId) return;
    const storageKey = `them:playground:ctx:${targetStorageKey(target)}`;
    localStorage.setItem(storageKey, contextId);
  }, [contextId]);  // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    chatBottom.current?.scrollIntoView({
      behavior: document.visibilityState === 'visible' ? 'smooth' : 'instant',
    });
  }, [messages]);

  useEffect(() => {
    traceBottom.current?.scrollIntoView({ behavior: 'smooth' });
  }, [trace]);

  // ── Dashboard WS ──────────────────────────────────────────────────────────
  const openDashWs = useCallback(async (rid: string) => {
    const r = await fetch('/api/auth/token');
    if (!r.ok) return;
    const { token } = await r.json();
    const ws = new WebSocket(`${getBridgeWs()}/ws/dashboard?token=${encodeURIComponent(token)}`);
    dashWs.current = ws;
    ws.onopen = () => { ws.send(JSON.stringify({ type: 'subscribe', channels: [`run:${rid}`] })); };
    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'ping') return;
        if (msg.channel?.startsWith('run:')) setTrace(prev => [...prev, { ts: Date.now(), ...msg.event }]);
      } catch {}
    };
    ws.onerror = () => ws.close();
  }, []);

  // ── Send ──────────────────────────────────────────────────────────────────
  const sendText = useCallback(async (text: string, currentContextId?: string | null) => {
    if (!text.trim() || busyRef.current) return;
    setInput('');
    setBusy(true);
    busyRef.current = true;
    setTrace([]);
    setMessages(prev => [...prev, { role: 'user', text }]);

    const r = await fetch('/api/auth/token');
    if (!r.ok) { setBusy(false); busyRef.current = false; return; }
    const { token } = await r.json();

    // ── A2A path ────────────────────────────────────────────────────────────
    if (target.kind === 'entrypoint' && target.epType === 'a2a') {
      setMessages(prev => [...prev, { role: 'assistant', text: '', pending: true }]);
      setStatus('Sending…');
      assistantBuf.current = '';

      try {
        for await (const ev of themApi.a2aStream(target.appSlug, target.slug, text, token)) {
          const kind = ev.kind as string;
          if (kind === 'run-started') {
            const rid = ev.taskId as string | undefined;
            const cid = ev.contextId as string | undefined;
            if (rid) {
              runId.current = rid;
              setStatus(`Run ${rid.slice(0, 8)}…`);
              openDashWs(rid);
            }
            if (cid) {
              setContextId(cid);
              const storageKey = `them:playground:ctx:${targetStorageKey(target)}`;
              localStorage.setItem(storageKey, cid);
            }
          } else if (kind === 'message-delta') {
            const parts = (ev.parts as Array<{ text?: string }>) ?? [];
            for (const p of parts) {
              if (p.text) {
                assistantBuf.current += p.text;
                setMessages(prev => {
                  const copy = [...prev];
                  const last = copy[copy.length - 1];
                  if (last?.role === 'assistant') copy[copy.length - 1] = { ...last, text: assistantBuf.current };
                  return copy;
                });
              }
            }
          } else if (kind === 'task-status-update') {
            const state = (ev.status as { state?: string } | undefined)?.state ?? '';
            if (state === 'completed') {
              setMessages(prev => {
                const copy = [...prev];
                const last = copy[copy.length - 1];
                if (last?.role === 'assistant') copy[copy.length - 1] = { ...last, pending: false };
                return copy;
              });
              setStatus('Done');
              setBusy(false); busyRef.current = false;
              dashWs.current?.close();
              break;
            } else if (state === 'failed') {
              const msg = (ev.status as { message?: string } | undefined)?.message ?? 'Run failed';
              setMessages(prev => {
                const copy = [...prev];
                const last = copy[copy.length - 1];
                if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, text: `Error: ${msg}`, pending: false };
                else copy.push({ role: 'assistant', text: `Error: ${msg}` });
                return copy;
              });
              setStatus(`Error: ${msg}`);
              setBusy(false); busyRef.current = false;
              dashWs.current?.close();
              break;
            }
          }
        }
      } catch (e) {
        const msg = (e as Error).message ?? 'A2A stream error';
        setMessages(prev => {
          const copy = [...prev];
          const last = copy[copy.length - 1];
          if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, text: `Error: ${msg}`, pending: false };
          else copy.push({ role: 'assistant', text: `Error: ${msg}` });
          return copy;
        });
        setStatus(`Error: ${msg}`);
        setBusy(false); busyRef.current = false;
        dashWs.current?.close();
      }
      return;
    }

    // ── WS path ─────────────────────────────────────────────────────────────
    const ws = new WebSocket(targetWsUrl(target, token));
    chatWs.current = ws;
    assistantBuf.current = '';

    ws.onopen = () => {
      setStatus('Connected');
      const payload: Record<string, string> = { type: 'message', content: text };
      if (currentContextId) payload.context_id = currentContextId;
      ws.send(JSON.stringify(payload));
    };

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type && msg.type !== 'token' && msg.type !== 'ping') {
          setTrace(prev => [...prev, { ts: Date.now(), ...msg }]);
        }
        if (msg.type === 'ready') {
          runId.current = msg.run_id;
          if (msg.context_id) setContextId(msg.context_id as string);
          setMessages(prev => [...prev, { role: 'assistant', text: '', pending: true }]);
          setStatus(`Run ${(msg.run_id as string).slice(0, 8)}…`);

        } else if (msg.type === 'token') {
          assistantBuf.current += msg.content || msg.text || '';
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant') copy[copy.length - 1] = { ...last, text: assistantBuf.current };
            return copy;
          });

        } else if (msg.type === 'agent_status') {
          const agent = msg.agent as string;
          const state = msg.state as string;
          const elapsed_ms = msg.elapsed_ms as number;
          const now = Date.now();
          const HOLD_MS = 2000;
          setActivities(prev => {
            const existing = prev.find(a => a.agent === agent);
            if (!existing) {
              const next = [...prev, { agent, state, elapsed_ms, displayState: state, visibleUntil: now + HOLD_MS }];
              activitiesRef.current = next; return next;
            }
            const displayState = now >= existing.visibleUntil ? state : existing.displayState;
            const visibleUntil = now >= existing.visibleUntil ? now + HOLD_MS : existing.visibleUntil;
            const next = prev.map(a => a.agent === agent ? { ...a, state, elapsed_ms, displayState, visibleUntil } : a);
            activitiesRef.current = next; return next;
          });

        } else if (msg.type === 'iteration_start') {
          const agents = (msg.agents as string[] | undefined) ?? [];
          setStatus(agents.length > 1 ? `Iter ${msg.iteration} — waiting for ${agents.join(', ')}…`
            : agents.length === 1 ? `Iter ${msg.iteration} — calling ${agents[0]}…`
            : `Iteration ${msg.iteration}…`);

        } else if (msg.type === 'tool_start') {
          const slug = (msg.tool as string).replace(/^agent__/, '');
          setStatus(`Calling ${slug}…`);

        } else if (msg.type === 'tool_done') {
          const slug = (msg.tool as string).replace(/^agent__/, '');
          setStatus(`${slug} done`);

        } else if (msg.type === 'file') {
          const fm: FileMsg = { filename: msg.filename as string, media_type: msg.media_type as string, text: msg.text as string ?? '' };
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, pending: false };
            return [...copy, { role: 'assistant', text: '', file: fm }];
          });

        } else if (msg.type === 'done') {
          setTimeout(() => { setActivities([]); activitiesRef.current = []; }, 1500);
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant' && !last.file) copy[copy.length - 1] = { ...last, pending: false };
            return copy;
          });
          if (ttsEnabled && assistantBuf.current) {
            const textToSpeak = assistantBuf.current;
            setSpeaking(true);
            themApi.tts(orchName, textToSpeak)
              .then(async res => {
                if (!res.body) throw new Error('no body');
                const ms = new MediaSource();
                const url = URL.createObjectURL(ms);
                const audio = new Audio(url);
                audio.onended = () => { setSpeaking(false); URL.revokeObjectURL(url); };
                audio.onerror = () => { setSpeaking(false); URL.revokeObjectURL(url); };
                await new Promise<void>(resolve => { ms.addEventListener('sourceopen', () => resolve(), { once: true }); });
                audio.play();
                const sb = ms.addSourceBuffer('audio/mpeg');
                const reader = res.body!.getReader();
                const pump = async () => {
                  while (true) {
                    const { done, value } = await reader.read();
                    if (done) { ms.endOfStream(); break; }
                    await new Promise<void>(r => { if (!sb.updating) { r(); return; } sb.addEventListener('updateend', () => r(), { once: true }); });
                    sb.appendBuffer(value);
                  }
                };
                pump().catch(() => { setSpeaking(false); ms.endOfStream(); });
              })
              .catch(() => setSpeaking(false));
          }
          setStatus(`Done — ${msg.iterations} iteration(s)`);
          setBusy(false); busyRef.current = false;
          ws.close(); dashWs.current?.close();

        } else if (msg.type === 'canceled') {
          setActivities([]); activitiesRef.current = [];
          setBusy(false); busyRef.current = false;
          setStatus('Canceled');
          ws.close(); dashWs.current?.close();

        } else if (msg.type === 'error') {
          setActivities([]); activitiesRef.current = [];
          const isTokenLimit = msg.code === 4029;
          const errText = isTokenLimit ? 'Conversation token limit reached' : `Error: ${msg.message}`;
          if ('context_id' in msg && msg.context_id === null) {
            setContextId(null);
            const storageKey = `them:playground:ctx:${target.kind === 'orchestrator' ? (target as {kind:'orchestrator';name:string}).name : (target as {kind:'entrypoint';slug:string}).slug}`;
            localStorage.removeItem(storageKey);
          }
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant' && last.pending) {
              copy[copy.length - 1] = { role: 'assistant', text: errText, pending: false };
            } else {
              copy.push({ role: 'assistant', text: errText });
            }
            return copy;
          });
          setStatus(isTokenLimit ? 'Token limit reached' : `Error: ${msg.message}`);
          setBusy(false); busyRef.current = false;
          ws.close();
        }
      } catch {}
    };

    ws.onerror = () => { setStatus('WebSocket error — check console'); setBusy(false); busyRef.current = false; };
    ws.onclose = (ev) => {
      if (ev.code === 4029) {
        setStatus('Token limit reached');
        setMessages(prev => {
          const copy = [...prev];
          const last = copy[copy.length - 1];
          if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, text: 'Conversation token limit reached', pending: false };
          return copy;
        });
      } else if (busyRef.current) {
        const closeMsg = ev.reason
          ? `Backend error: ${ev.reason}`
          : 'Connection closed unexpectedly — check the Trace tab for details';
        setMessages(prev => {
          const copy = [...prev];
          const last = copy[copy.length - 1];
          if (last?.role === 'assistant' && last.pending) {
            copy[copy.length - 1] = { ...last, text: last.text || closeMsg, pending: false };
          } else if (!last?.text) {
            copy.push({ role: 'assistant', text: closeMsg });
          }
          return copy;
        });
        setTrace(prev => [...prev, { ts: Date.now(), type: 'error', message: closeMsg }]);
        setStatus('Error — connection closed');
      }
      setBusy(false); busyRef.current = false;
    };
  }, [target, openDashWs, ttsEnabled, orchName]);

  const prevSharedInput = useRef<string | null>(null);
  useEffect(() => {
    if (sharedInput == null || sharedInput === prevSharedInput.current) return;
    prevSharedInput.current = sharedInput;
    if (sharedInput.trim()) {
      sendText(sharedInput, contextId);
      onSharedSent?.();
    }
  }, [sharedInput, contextId, sendText, onSharedSent]);

  const send = useCallback(() => sendText(input, contextId), [input, contextId, sendText]);

  const stopRun = useCallback(() => {
    if (chatWs.current?.readyState === WebSocket.OPEN) chatWs.current.send(JSON.stringify({ type: 'cancel' }));
    setBusy(false); busyRef.current = false;
    setStatus('Canceling…');
    setMessages(prev => {
      const copy = [...prev];
      const last = copy[copy.length - 1];
      if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, text: last.text || '(stopped)', pending: false };
      return copy;
    });
    setTimeout(() => { chatWs.current?.close(); dashWs.current?.close(); }, 3000);
  }, []);

  const onKey = (e: React.KeyboardEvent) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } };

  const clearChat = useCallback(() => {
    setMessages([]); setTrace([]); setStatus('');
    setActivities([]); activitiesRef.current = [];
    setContextId(null); setRestoredSession(null); runId.current = null;
    const storageKey = `them:playground:ctx:${targetStorageKey(target)}`;
    localStorage.removeItem(storageKey);
  }, [target]);

  const resumeSession = useCallback(async (s: ContextSession) => {
    setRestoredSession(null);
    setContextId(s.context_id);
    setDebugTab('trace');
    try {
      const msgs = await themApi.contextMessages(s.context_id, 200);
      const chatMsgs: ChatMsg[] = msgs.map(m => ({
        role: m.role === 'user' ? 'user' : 'assistant',
        text: m.text,
      }));
      if (chatMsgs.length > 0) {
        setMessages(chatMsgs);
      } else {
        setMessages([{ role: 'assistant', text: `↩ Resumed: **${s.title}** — no stored messages. Continue below.` }]);
      }
    } catch {
      setMessages([{ role: 'assistant', text: `↩ Resumed: **${s.title}** — ${s.turn_count} prior turn${s.turn_count !== 1 ? 's' : ''}. Continue below.` }]);
    }
  }, []);

  // ── Voice ─────────────────────────────────────────────────────────────────
  const isVoiceEP = target.kind === 'entrypoint' && target.epType === 'voice';

  const startRecording = async () => {
    if (recordingStateRef.current !== 'idle') return;
    try {
      if (!navigator.mediaDevices?.getUserMedia) {
        throw new Error('Microphone requires HTTPS or localhost');
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mimeType = ['audio/webm', 'audio/ogg;codecs=opus', 'audio/ogg', ''].find(
        m => !m || MediaRecorder.isTypeSupported(m)
      ) ?? '';
      const recorder = new MediaRecorder(stream, mimeType ? { mimeType } : {});
      const chunks: Blob[] = [];
      recorder.ondataavailable = (e) => { if (e.data.size > 0) chunks.push(e.data); };
      recorder.onstop = async () => {
        stream.getTracks().forEach(t => t.stop());
        const blob = new Blob(chunks, { type: recorder.mimeType || 'audio/webm' });
        setRecState('transcribing');
        try {
          if (isVoiceEP && target.kind === 'entrypoint') {
            setStatus('Sending…');
            const fetchAbort = new AbortController();
            voiceFetchAbortRef.current = fetchAbort;
            try {
              let assistantAdded = false;
              let fullReply = '';
              for await (const ev of themApi.voiceStream(target.appSlug, target.slug, blob, fetchAbort.signal)) {
                const evType = ev.type as string;
                if (evType === 'transcript') {
                  const txt = ev.text as string;
                  if (txt) setMessages(prev => [...prev, { role: 'user', text: txt }]);
                  setStatus('Thinking…');
                } else if (evType === 'token') {
                  fullReply += (ev.content as string) ?? '';
                  if (!assistantAdded) {
                    setMessages(prev => [...prev, { role: 'assistant', text: fullReply, pending: true }]);
                    assistantAdded = true;
                  } else {
                    setMessages(prev => {
                      const copy = [...prev];
                      const last = copy[copy.length - 1];
                      if (last?.role === 'assistant') copy[copy.length - 1] = { ...last, text: fullReply };
                      return copy;
                    });
                  }
                } else if (evType === 'done') {
                  setMessages(prev => {
                    const copy = [...prev];
                    const last = copy[copy.length - 1];
                    if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, pending: false };
                    return copy;
                  });
                  const replyText = (ev.text as string) || fullReply;
                  if (replyText && ev.tts_enabled !== false) {
                    setStatus('Speaking…');
                    themApi.voiceTTS(target.appSlug, target.slug, replyText, fetchAbort.signal)
                      .then(audioBlob => {
                        if (!audioBlob || audioBlob.size === 0) return;
                        setSpeaking(true);
                        const url = URL.createObjectURL(audioBlob);
                        const audio = new Audio(url);
                        voiceAudioRef.current = audio;
                        const cleanup = () => {
                          if (voiceAudioRef.current === audio) voiceAudioRef.current = null;
                          setSpeaking(false);
                          URL.revokeObjectURL(url);
                          setStatus('');
                        };
                        audio.onended = cleanup;
                        audio.onerror = cleanup;
                        audio.play().catch(cleanup);
                      })
                      .catch(() => { setStatus(''); });
                  }
                  setStatus('Done');
                } else if (evType === 'error') {
                  throw new Error((ev.message as string) ?? 'voice stream error');
                }
              }
            } finally {
              if (voiceFetchAbortRef.current === fetchAbort) voiceFetchAbortRef.current = null;
            }
          } else {
            const result = await themApi.transcribe(orchName, blob);
            if (result.text) await sendText(result.text, contextId);
          }
        } catch (e) {
          if ((e as Error).name !== 'AbortError') {
            setStatus(`Voice error: ${(e as Error).message}`);
            setTimeout(() => setStatus(''), 4000);
            setBusy(false); busyRef.current = false;
          }
        }
        finally {
          if (recordingStateRef.current === 'transcribing') setRecState('idle');
        }
      };
      recorder.start(250);
      mediaRecorderRef.current = recorder;
      setMediaRecorder(recorder);
      setRecState('recording');
      setTimeout(() => {
        if (mediaRecorderRef.current === recorder) {
          try { if (recorder.state !== 'inactive') recorder.stop(); } catch { /* ok */ }
          mediaRecorderRef.current = null;
        }
      }, 60000);
    } catch (e) {
      const msg = (e as Error).message || '';
      const friendly = msg.includes('HTTPS') || msg.includes('localhost')
        ? 'Microphone requires HTTPS or localhost'
        : msg.includes('Permission') || msg.includes('permission') || msg.includes('denied') || msg.includes('NotAllowed')
          ? 'Microphone permission denied — allow mic access in your browser'
          : `Mic error: ${msg}`;
      setStatus(friendly);
      setTimeout(() => setStatus(''), 5000);
    }
  };

  const stopRecording = () => {
    const rec = mediaRecorderRef.current;
    if (!rec) { setRecState('idle'); return; }
    try { if (rec.state !== 'inactive') rec.stop(); } catch { /* already stopped */ }
    mediaRecorderRef.current = null;
    setMediaRecorder(null);
    if (rec.state === 'inactive') setRecState('idle');
  };

  const interruptVoice = () => {
    if (voiceAudioRef.current) {
      voiceAudioRef.current.pause();
      voiceAudioRef.current = null;
    }
    if (voiceFetchAbortRef.current) {
      voiceFetchAbortRef.current.abort();
      voiceFetchAbortRef.current = null;
    }
    setSpeaking(false);
    setRecState('idle');
  };

  const toggleRecording = () => {
    const state = recordingStateRef.current;
    if (state === 'recording') { stopRecording(); return; }
    if (state === 'transcribing') { interruptVoice(); return; }
    startRecording();
  };

  const micBtnStyle = (): React.CSSProperties => {
    if (recordingState === 'recording') return { padding: '10px 14px', borderRadius: 12, border: 'none', background: '#ef4444', color: '#fff', cursor: 'pointer', alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center', outline: '2px solid #f87171' };
    if (recordingState === 'transcribing') return { padding: '10px 14px', borderRadius: 12, border: 'none', background: '#7c3aed', color: '#fff', cursor: 'pointer', alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center', outline: '2px solid #a78bfa' };
    return { padding: '10px 14px', borderRadius: 12, border: 'none', background: 'var(--tm-surface-2)', color: 'var(--tm-text-muted)', cursor: 'pointer', alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center' };
  };

  const pad = compact ? '8px 12px' : '20px 24px';

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, minHeight: 0, overflow: 'hidden', borderRight: '1px solid var(--tm-border)' }}>
      {showHeader && (
        <div style={{ padding: '8px 12px', borderBottom: '1px solid var(--tm-border)', display: 'flex', alignItems: 'center', gap: 8, background: 'var(--tm-surface)', flexShrink: 0 }}>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: color, display: 'inline-block', flexShrink: 0 }} />
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {target.kind === 'orchestrator' ? target.label : target.appName}
          </span>
          {target.kind === 'entrypoint' && (
            <>
              <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{target.slug}</span>
              <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(124,58,237,0.15)', color: '#a78bfa', fontWeight: 600, flexShrink: 0 }}>{target.epType}</span>
            </>
          )}
          {speaking && <span style={{ fontSize: 11, color: '#a78bfa', marginLeft: 'auto' }}>🔊</span>}
          {!speaking && status && <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', marginLeft: 'auto', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 160 }}>{status}</span>}
          <button onClick={clearChat} style={{ marginLeft: status || speaking ? 0 : 'auto', padding: '2px 8px', borderRadius: 6, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: 11, flexShrink: 0 }}>
            Clear
          </button>
        </div>
      )}

      <div className="dark-scrollbar" style={{ flex: 1, overflowY: 'auto', padding: pad, display: 'flex', flexDirection: 'column', gap: 12 }}>
        {restoredSession && !messages.some(m => m.role === 'assistant') && (
          <div style={{ margin: '40px auto', maxWidth: 360, padding: '14px 18px', borderRadius: 12, border: `1px solid ${color}`, background: 'rgba(124,58,237,0.06)', display: 'flex', flexDirection: 'column', gap: 8 }}>
            <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tm-text)' }}>↩ Resume last conversation?</div>
            <div style={{ fontSize: 12, color: 'var(--tm-text-muted)' }}>
              <span style={{ color, fontWeight: 600 }}>{restoredSession.orchestrator_name}</span>
              {' · '}{restoredSession.turn_count} turn{restoredSession.turn_count !== 1 ? 's' : ''}
            </div>
            <div style={{ fontSize: 12, color: 'var(--tm-text)', fontStyle: 'italic', opacity: 0.8 }}>"{restoredSession.title}"</div>
            <div style={{ display: 'flex', gap: 8 }}>
              <button onClick={() => resumeSession(restoredSession)} style={{ flex: 1, padding: '6px 0', borderRadius: 8, border: 'none', background: color, color: '#fff', fontSize: 12, fontWeight: 600, cursor: 'pointer' }}>Resume</button>
              <button onClick={() => { setRestoredSession(null); localStorage.removeItem(`them:playground:ctx:${targetStorageKey(target)}`); }} style={{ flex: 1, padding: '6px 0', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text-muted)', fontSize: 12, cursor: 'pointer' }}>Fresh start</button>
            </div>
          </div>
        )}
        {messages.length === 0 && !restoredSession && (
          <div style={{ color: 'var(--tm-text-muted)', fontSize: 13, textAlign: 'center', marginTop: 48 }}>
            Send a message to begin
          </div>
        )}
        {messages.map((m, i) => (
          <div key={i} style={{ display: 'flex', justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
            {m.file ? (
              <div style={{ maxWidth: '82%', borderRadius: '14px 14px 14px 4px', border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', overflow: 'hidden' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '7px 12px', borderBottom: '1px solid var(--tm-border)', background: 'rgba(124,58,237,0.08)' }}>
                  <span style={{ fontSize: 14 }}>{m.file.media_type === 'text/html' ? '🌐' : m.file.media_type === 'text/markdown' ? '📝' : '📄'}</span>
                  <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text)', flex: 1 }}>{m.file.filename}</span>
                  <button onClick={() => { const b = new Blob([m.file!.text], { type: m.file!.media_type }); const u = URL.createObjectURL(b); const a = document.createElement('a'); a.href = u; a.download = m.file!.filename; a.click(); URL.revokeObjectURL(u); }} style={{ padding: '3px 8px', borderRadius: 5, fontSize: 11, fontWeight: 600, background: color, color: '#fff', border: 'none', cursor: 'pointer' }}>Download</button>
                </div>
                {m.file.media_type === 'text/html' && <iframe srcDoc={m.file.text} style={{ width: '100%', height: 340, border: 'none', display: 'block' }} sandbox="allow-scripts allow-same-origin" title={m.file.filename} />}
                {m.file.media_type === 'text/markdown' && <pre style={{ margin: 0, padding: '10px 12px', fontSize: 11, fontFamily: 'monospace', color: 'var(--tm-text)', whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: 260, overflowY: 'auto' }}>{m.file.text}</pre>}
              </div>
            ) : (
              <MsgBubble msg={m} color={color} />
            )}
          </div>
        ))}
        <div ref={chatBottom} />
      </div>

      <ActivityBar activities={activities} />

      {sharedInput === undefined && (
        <div style={{ display: 'flex', flexDirection: 'column', flexShrink: 0 }}>
          <div
            onMouseDown={isVoiceEP ? undefined : onInputDragStart}
            style={{ height: 10, cursor: 'ns-resize', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', position: 'relative' }}
            onMouseEnter={e => { const el = e.currentTarget.querySelector<HTMLElement>('.grip-line'); if (el) el.style.background = '#7c3aed'; }}
            onMouseLeave={e => { const el = e.currentTarget.querySelector<HTMLElement>('.grip-line'); if (el) el.style.background = 'var(--tm-border)'; }}
          >
            <div className="grip-line" style={{ position: 'absolute', top: '50%', left: 0, right: 0, height: 1, background: 'var(--tm-border)', transition: 'background 0.15s' }} />
            <div style={{ position: 'relative', display: 'flex', gap: 3, zIndex: 1 }}>
              {[0,1,2].map(i => <div key={i} style={{ width: 3, height: 3, borderRadius: '50%', background: 'var(--tm-border)' }} />)}
            </div>
          </div>
          <div style={{ padding: '6px 14px 10px', display: 'flex', gap: 8, alignItems: 'center' }}>
            {(voiceEnabled || isVoiceEP) && (
              <button
                style={micBtnStyle()}
                onClick={(e) => { e.stopPropagation(); toggleRecording(); }}
                title={recordingState === 'recording' ? 'Click to stop & send' : recordingState === 'transcribing' ? 'Click to interrupt' : 'Click to start recording'}
              >
                {recordingState === 'transcribing' ? <Spinner /> : <MicIcon />}
              </button>
            )}
            {isVoiceEP ? (
              <div style={{ flex: 1, padding: '9px 12px', borderRadius: 10, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', color: 'var(--tm-text-muted)', fontSize: 13, display: 'flex', alignItems: 'center', fontStyle: 'italic' }}>
                {recordingState === 'recording' ? '🔴 Recording… click mic to stop' : recordingState === 'transcribing' ? '⏳ Processing… click mic to interrupt' : 'Click the mic to start speaking'}
              </div>
            ) : (
              <>
                <textarea value={input} onChange={e => setInput(e.target.value)} onKeyDown={onKey} disabled={busy} dir="auto" placeholder="Send a message… (Enter to send, Shift+Enter for newline)"
                  style={{ flex: 1, height: inputHeight, padding: '9px 12px', borderRadius: 10, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', color: 'var(--tm-text)', fontSize: 13, resize: 'none', outline: 'none', fontFamily: 'inherit', lineHeight: 1.5 }} />
                {busy ? (
                  <button onClick={stopRun} style={{ padding: '9px 16px', borderRadius: 10, border: `1.5px solid #ef4444`, background: 'transparent', color: '#ef4444', fontSize: 13, fontWeight: 600, cursor: 'pointer', alignSelf: 'flex-end' }}>Stop</button>
                ) : (
                  <button onClick={send} disabled={!input.trim()} style={{ padding: '9px 16px', borderRadius: 10, border: 'none', background: !input.trim() ? 'var(--tm-surface)' : color, color: !input.trim() ? 'var(--tm-text-muted)' : '#fff', fontSize: 13, fontWeight: 600, cursor: !input.trim() ? 'not-allowed' : 'pointer', alignSelf: 'flex-end' }}>Send</button>
                )}
              </>
            )}
          </div>
        </div>
      )}

      {sharedInput === undefined && (
        <div style={{ height: panelHeight, display: 'flex', flexDirection: 'column', flexShrink: 0 }}>
          <div
            onMouseDown={onPanelDragStart}
            style={{ height: 10, cursor: 'ns-resize', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', position: 'relative' }}
            onMouseEnter={e => { const el = e.currentTarget.querySelector<HTMLElement>('.grip-line'); if (el) el.style.background = '#7c3aed'; }}
            onMouseLeave={e => { const el = e.currentTarget.querySelector<HTMLElement>('.grip-line'); if (el) el.style.background = 'var(--tm-border)'; }}
          >
            <div className="grip-line" style={{ position: 'absolute', top: '50%', left: 0, right: 0, height: 1, background: 'var(--tm-border)', transition: 'background 0.15s' }} />
            <div style={{ position: 'relative', display: 'flex', gap: 3, zIndex: 1 }}>
              {[0,1,2].map(i => <div key={i} style={{ width: 3, height: 3, borderRadius: '50%', background: 'var(--tm-border)' }} />)}
            </div>
          </div>
          <div style={{ padding: '6px 10px', borderBottom: '1px solid var(--tm-border)', display: 'flex', gap: 4, alignItems: 'center', background: 'var(--tm-surface)' }}>
            <TabBtn label="Trace" active={debugTab === 'trace'} onClick={() => setDebugTab('trace')} />
            <TabBtn label="Tasks" active={debugTab === 'tasks'} onClick={() => setDebugTab('tasks')} />
            <TabBtn label="Artifacts" active={debugTab === 'artifacts'} onClick={() => setDebugTab('artifacts')} />
            <TabBtn label="Sessions" active={debugTab === 'sessions'} onClick={() => setDebugTab('sessions')} />
          </div>
          <div style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
            {debugTab === 'trace' && <TraceTab trace={trace} traceBottom={traceBottom} runId={runId.current} contextId={contextId} />}
            {debugTab === 'tasks' && <TasksTab runId={runId.current} busy={busy} />}
            {debugTab === 'artifacts' && <ArtifactsTab runId={runId.current} busy={busy} />}
            {debugTab === 'sessions' && <SessionsTab currentContextId={contextId} onResume={resumeSession} />}
          </div>
        </div>
      )}
    </div>
  );
}
