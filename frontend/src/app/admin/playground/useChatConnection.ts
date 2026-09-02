'use client';
import { useEffect, useRef, useState, useCallback } from 'react';
import { themApi, type ContextSession } from '@/lib/api';
import {
  type ConnTarget, type ChatMsg, type FileMsg, type AgentActivity, type TraceEvent,
  targetStorageKey, targetWsUrl, getBridgeWs,
} from './playgroundTypes';

export interface UseChatConnectionProps {
  target: ConnTarget;
  ttsEnabled: boolean;
  orchName: string | undefined;
}

export interface UseChatConnectionResult {
  messages: ChatMsg[];
  setMessages: React.Dispatch<React.SetStateAction<ChatMsg[]>>;
  trace: TraceEvent[];
  busy: boolean;
  status: string;
  setStatus: React.Dispatch<React.SetStateAction<string>>;
  activities: AgentActivity[];
  contextId: string | null;
  restoredSession: ContextSession | null;
  setRestoredSession: React.Dispatch<React.SetStateAction<ContextSession | null>>;
  runId: string | null;
  runIdRef: React.MutableRefObject<string | null>;
  chatWsRef: React.MutableRefObject<WebSocket | null>;
  dashWsRef: React.MutableRefObject<WebSocket | null>;
  setSpeaking: React.Dispatch<React.SetStateAction<boolean>>;
  sendText: (text: string, currentContextId?: string | null) => Promise<void>;
  stopRun: () => void;
  clearChat: () => void;
  resumeSession: (s: ContextSession) => Promise<void>;
  openDashWs: (rid: string) => Promise<void>;
}

export function useChatConnection({ target, ttsEnabled, orchName }: UseChatConnectionProps): UseChatConnectionResult {
  const [messages, setMessages] = useState<ChatMsg[]>([]);
  const [trace, setTrace] = useState<TraceEvent[]>([]);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [contextId, setContextId] = useState<string | null>(null);
  const [restoredSession, setRestoredSession] = useState<ContextSession | null>(null);
  const [activities, setActivities] = useState<AgentActivity[]>([]);
  const [, setSpeakingState] = useState(false);

  const [runId, setRunId] = useState<string | null>(null);
  const activitiesRef = useRef<AgentActivity[]>([]);
  const chatWsRef = useRef<WebSocket | null>(null);
  const dashWsRef = useRef<WebSocket | null>(null);
  const runIdRef = useRef<string | null>(null);
  const assistantBuf = useRef('');
  const busyRef = useRef(false);
  const didRestoreRef = useRef(false);

  const setSpeaking: React.Dispatch<React.SetStateAction<boolean>> = useCallback((val) => {
    setSpeakingState(val);
  }, []);

  useEffect(() => { busyRef.current = busy; }, [busy]);

  // Session restore on mount
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

  // Reload messages when tab becomes visible again (if WS is closed)
  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState !== 'visible') return;
      if (!contextId || chatWsRef.current) return;
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

  // Persist contextId to localStorage
  useEffect(() => {
    if (!contextId) return;
    const storageKey = `them:playground:ctx:${targetStorageKey(target)}`;
    localStorage.setItem(storageKey, contextId);
  }, [contextId]);  // eslint-disable-line react-hooks/exhaustive-deps

  // ── Dashboard WS ──────────────────────────────────────────────────────────
  const openDashWs = useCallback(async (rid: string) => {
    const r = await fetch('/api/auth/token');
    if (!r.ok) return;
    const { token } = await r.json();
    const ws = new WebSocket(`${getBridgeWs()}/ws/dashboard?token=${encodeURIComponent(token)}`);
    dashWsRef.current = ws;
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
            const rid = (ev.runId ?? ev.taskId) as string | undefined;
            const cid = ev.contextId as string | undefined;
            if (rid) {
              runIdRef.current = rid;
              setRunId(rid);
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
              dashWsRef.current?.close();
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
              dashWsRef.current?.close();
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
        dashWsRef.current?.close();
      }
      return;
    }

    // ── WS path ─────────────────────────────────────────────────────────────
    const ws = new WebSocket(targetWsUrl(target, token));
    chatWsRef.current = ws;
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
          runIdRef.current = msg.run_id;
          setRunId(msg.run_id as string);
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

        } else if (msg.type === 'file_scanning') {
          // Security gate: scan in progress — show spinner bubble, no download link yet.
          const fm: FileMsg = {
            filename: msg.filename as string,
            media_type: msg.media_type as string ?? '',
            text: '',
            artifact_id: msg.artifact_id as string | undefined,
            scanning: true,
          };
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, pending: false };
            return [...copy, { role: 'assistant', text: '', file: fm }];
          });

        } else if (msg.type === 'file_blocked') {
          // Security gate: scan found a threat — update the scanning bubble to blocked state.
          const artifactId = msg.artifact_id as string | undefined;
          setMessages(prev => {
            const idx = artifactId
              ? prev.findLastIndex(m => m.file?.artifact_id === artifactId && m.file?.scanning)
              : -1;
            if (idx >= 0) {
              const copy = [...prev];
              copy[idx] = { ...copy[idx], file: { ...copy[idx].file!, scanning: false, blocked: true, threat: msg.threat as string | undefined } };
              return copy;
            }
            // No matching scanning bubble — add a new blocked bubble.
            const fm: FileMsg = {
              filename: msg.filename as string,
              media_type: msg.media_type as string ?? '',
              text: '',
              artifact_id: artifactId,
              blocked: true,
              threat: msg.threat as string | undefined,
            };
            return [...prev, { role: 'assistant', text: '', file: fm }];
          });

        } else if (msg.type === 'file') {
          const rawUrl = msg.download_url as string | undefined;
          // Rewrite /api/v1/... to go through the Next.js proxy at /api/them/...
          const download_url = rawUrl?.startsWith('/api/v1/')
            ? '/api/them/' + rawUrl.slice('/api/v1/'.length)
            : rawUrl;
          const artifactId = msg.artifact_id as string | undefined;
          const fm: FileMsg = { filename: msg.filename as string, media_type: msg.media_type as string, text: msg.text as string ?? '', download_url, artifact_id: artifactId };
          setMessages(prev => {
            // Replace an existing scanning bubble for the same artifact (clean scan result).
            const idx = artifactId
              ? prev.findLastIndex(m => m.file?.artifact_id === artifactId && m.file?.scanning)
              : -1;
            if (idx >= 0) {
              const copy = [...prev];
              copy[idx] = { ...copy[idx], file: fm };
              return copy;
            }
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
          if (ttsEnabled && orchName && assistantBuf.current) {
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
          ws.close(); dashWsRef.current?.close();

        } else if (msg.type === 'canceled') {
          setActivities([]); activitiesRef.current = [];
          setBusy(false); busyRef.current = false;
          setStatus('Canceled');
          ws.close(); dashWsRef.current?.close();

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
  }, [target, openDashWs, ttsEnabled, orchName]);  // eslint-disable-line react-hooks/exhaustive-deps

  const stopRun = useCallback(() => {
    if (chatWsRef.current?.readyState === WebSocket.OPEN) chatWsRef.current.send(JSON.stringify({ type: 'cancel' }));
    setBusy(false); busyRef.current = false;
    setStatus('Canceling…');
    setMessages(prev => {
      const copy = [...prev];
      const last = copy[copy.length - 1];
      if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, text: last.text || '(stopped)', pending: false };
      return copy;
    });
    setTimeout(() => { chatWsRef.current?.close(); dashWsRef.current?.close(); }, 3000);
  }, []);

  const clearChat = useCallback(() => {
    setMessages([]); setTrace([]); setStatus('');
    setActivities([]); activitiesRef.current = [];
    setContextId(null); setRestoredSession(null); runIdRef.current = null; setRunId(null);
    const storageKey = `them:playground:ctx:${targetStorageKey(target)}`;
    localStorage.removeItem(storageKey);
  }, [target]);

  const resumeSession = useCallback(async (s: ContextSession) => {
    setRestoredSession(null);
    setContextId(s.context_id);
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

  return {
    messages, setMessages,
    trace,
    busy,
    status, setStatus,
    activities,
    contextId,
    restoredSession, setRestoredSession,
    runId,
    runIdRef,
    chatWsRef,
    dashWsRef,
    setSpeaking,
    sendText,
    stopRun,
    clearChat,
    resumeSession,
    openDashWs,
  };
}
