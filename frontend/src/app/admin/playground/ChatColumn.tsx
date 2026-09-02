'use client';
import { useEffect, useRef, useState, useCallback } from 'react';
import { themApi, type ContextSession } from '@/lib/api';
import {
  type ConnTarget, type AgentActivity, type RecordingState,
  targetId, targetStorageKey,
} from './playgroundTypes';
import { MicIcon, Spinner, MsgBubble } from './ChatBubbles';
import { TabBtn, TraceTab, TasksTab, ArtifactsTab, SessionsTab, type DebugTab } from './DebugPanel';
import { useChatConnection } from './useChatConnection';

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
  const [input, setInput] = useState('');
  const [voiceEnabled, setVoiceEnabled] = useState(false);
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [speaking, setSpeakingLocal] = useState(false);
  const [recordingState, setRecordingState] = useState<RecordingState>('idle');
  const recordingStateRef = useRef<RecordingState>('idle');
  const setRecState = (s: RecordingState) => { recordingStateRef.current = s; setRecordingState(s); };
  const [, setMediaRecorder] = useState<MediaRecorder | null>(null);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const voiceFetchAbortRef = useRef<AbortController | null>(null);
  const voiceAudioRef = useRef<HTMLAudioElement | null>(null);
  const [debugTab, setDebugTab] = useState<DebugTab>('trace');
  const [panelHeight, setPanelHeight] = useState(280);
  const [inputHeight, setInputHeight] = useState(90);
  const panelDragY = useRef<number | null>(null);
  const panelDragH = useRef<number>(280);
  const inputDragY = useRef<number | null>(null);
  const inputDragH = useRef<number>(90);

  const chatBottom = useRef<HTMLDivElement>(null);
  const traceBottom = useRef<HTMLDivElement | null>(null);

  const orchName = target.kind === 'orchestrator' ? target.name : target.orchName;

  const conn = useChatConnection({ target, ttsEnabled, orchName });
  const { messages, trace, busy, status, setStatus: connSetStatus, activities, contextId, restoredSession,
    setRestoredSession, runIdRef, sendText, stopRun, clearChat, resumeSession } = conn;

  const setSpeaking = useCallback((val: boolean | ((prev: boolean) => boolean)) => {
    setSpeakingLocal(val as boolean);
    conn.setSpeaking(val as boolean);
  }, [conn]);

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
    chatBottom.current?.scrollIntoView({
      behavior: document.visibilityState === 'visible' ? 'smooth' : 'instant',
    });
  }, [messages]);

  useEffect(() => {
    traceBottom.current?.scrollIntoView({ behavior: 'smooth' });
  }, [trace]);

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

  const prevSharedInput = useRef<string | null>(null);
  useEffect(() => {
    if (sharedInput == null || sharedInput === prevSharedInput.current) return;
    prevSharedInput.current = sharedInput;
    if (sharedInput.trim()) {
      sendText(sharedInput, contextId);
      onSharedSent?.();
    }
  }, [sharedInput, contextId, sendText, onSharedSent]);

  const send = useCallback(() => { setInput(''); sendText(input, contextId); }, [input, contextId, sendText]);
  const onKey = (e: React.KeyboardEvent) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } };

  const wrappedResumeSession = useCallback(async (s: ContextSession) => {
    setDebugTab('trace');
    await resumeSession(s);
  }, [resumeSession]);

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
            connSetStatus('Sending…');
            const fetchAbort = new AbortController();
            voiceFetchAbortRef.current = fetchAbort;
            try {
              let assistantAdded = false;
              let fullReply = '';
              for await (const ev of themApi.voiceStream(target.appSlug, target.slug, blob, fetchAbort.signal)) {
                const evType = ev.type as string;
                if (evType === 'transcript') {
                  const txt = ev.text as string;
                  if (txt) conn.setMessages(prev => [...prev, { role: 'user', text: txt }]);
                  connSetStatus('Thinking…');
                } else if (evType === 'token') {
                  fullReply += (ev.content as string) ?? '';
                  if (!assistantAdded) {
                    conn.setMessages(prev => [...prev, { role: 'assistant', text: fullReply, pending: true }]);
                    assistantAdded = true;
                  } else {
                    conn.setMessages(prev => {
                      const copy = [...prev];
                      const last = copy[copy.length - 1];
                      if (last?.role === 'assistant') copy[copy.length - 1] = { ...last, text: fullReply };
                      return copy;
                    });
                  }
                } else if (evType === 'done') {
                  conn.setMessages(prev => {
                    const copy = [...prev];
                    const last = copy[copy.length - 1];
                    if (last?.role === 'assistant' && last.pending) copy[copy.length - 1] = { ...last, pending: false };
                    return copy;
                  });
                  const replyText = (ev.text as string) || fullReply;
                  if (replyText && ev.tts_enabled !== false) {
                    connSetStatus('Speaking…');
                    themApi.voiceTTS(target.appSlug, target.slug, replyText, fetchAbort.signal)
                      .then(audioBlob => {
                        if (!audioBlob || audioBlob.size === 0) return;
                        setSpeakingLocal(true);
                        const url = URL.createObjectURL(audioBlob);
                        const audio = new Audio(url);
                        voiceAudioRef.current = audio;
                        const cleanup = () => {
                          if (voiceAudioRef.current === audio) voiceAudioRef.current = null;
                          setSpeakingLocal(false);
                          URL.revokeObjectURL(url);
                          connSetStatus('');
                        };
                        audio.onended = cleanup;
                        audio.onerror = cleanup;
                        audio.play().catch(cleanup);
                      })
                      .catch(() => { setSpeakingLocal(false); connSetStatus(''); });
                  }
                  connSetStatus('Done');
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
            connSetStatus(`Voice error: ${(e as Error).message}`);
            setTimeout(() => connSetStatus(''), 4000);
            setSpeakingLocal(false);
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
      connSetStatus(friendly);
      setTimeout(() => connSetStatus(''), 5000);
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
    setSpeakingLocal(false);
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
              <button onClick={() => wrappedResumeSession(restoredSession)} style={{ flex: 1, padding: '6px 0', borderRadius: 8, border: 'none', background: color, color: '#fff', fontSize: 12, fontWeight: 600, cursor: 'pointer' }}>Resume</button>
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
              <div style={{ maxWidth: '82%', borderRadius: '14px 14px 14px 4px', border: `1px solid ${m.file.blocked ? 'rgba(239,68,68,0.4)' : 'var(--tm-border)'}`, background: 'var(--tm-surface)', overflow: 'hidden' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '7px 12px', borderBottom: `1px solid ${m.file.blocked ? 'rgba(239,68,68,0.4)' : 'var(--tm-border)'}`, background: m.file.blocked ? 'rgba(239,68,68,0.08)' : 'rgba(124,58,237,0.08)' }}>
                  {m.file.scanning ? (
                    <span style={{ color: 'var(--tm-text-muted)', display: 'flex', alignItems: 'center' }}><Spinner /></span>
                  ) : m.file.blocked ? (
                    <span style={{ fontSize: 14 }}>🚫</span>
                  ) : (
                    <span style={{ fontSize: 14 }}>{m.file.media_type === 'text/html' ? '🌐' : m.file.media_type === 'text/markdown' ? '📝' : '📄'}</span>
                  )}
                  <span style={{ fontSize: 12, fontWeight: 600, color: m.file.blocked ? '#ef4444' : 'var(--tm-text)', flex: 1 }}>{m.file.filename}</span>
                  {m.file.scanning && <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', whiteSpace: 'nowrap' }}>Scanning…</span>}
                  {m.file.blocked && <span style={{ fontSize: 11, color: '#ef4444', whiteSpace: 'nowrap' }}>Blocked</span>}
                  {!m.file.scanning && !m.file.blocked && (
                    <button onClick={() => { const b = new Blob([m.file!.text], { type: m.file!.media_type }); const u = URL.createObjectURL(b); const a = document.createElement('a'); a.href = u; a.download = m.file!.filename; a.click(); URL.revokeObjectURL(u); }} style={{ padding: '3px 8px', borderRadius: 5, fontSize: 11, fontWeight: 600, background: color, color: '#fff', border: 'none', cursor: 'pointer' }}>Download</button>
                  )}
                </div>
                {m.file.blocked && m.file.threat && (
                  <div style={{ padding: '6px 12px', fontSize: 11, color: '#ef4444', background: 'rgba(239,68,68,0.05)' }}>
                    Threat detected: {m.file.threat}
                  </div>
                )}
                {m.file.blocked && !m.file.threat && (
                  <div style={{ padding: '6px 12px', fontSize: 11, color: '#ef4444', background: 'rgba(239,68,68,0.05)' }}>
                    File removed — security policy violation
                  </div>
                )}
                {!m.file.scanning && !m.file.blocked && m.file.media_type === 'text/html' && <iframe srcDoc={m.file.text} style={{ width: '100%', height: 340, border: 'none', display: 'block' }} sandbox="allow-scripts allow-same-origin" title={m.file.filename} />}
                {!m.file.scanning && !m.file.blocked && m.file.media_type === 'text/markdown' && <pre style={{ margin: 0, padding: '10px 12px', fontSize: 11, fontFamily: 'monospace', color: 'var(--tm-text)', whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: 260, overflowY: 'auto' }}>{m.file.text}</pre>}
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
            {debugTab === 'trace' && <TraceTab trace={trace} traceBottom={traceBottom} runId={runIdRef.current} contextId={contextId} />}
            {debugTab === 'tasks' && <TasksTab runId={runIdRef.current} busy={busy} />}
            {debugTab === 'artifacts' && <ArtifactsTab runId={runIdRef.current} busy={busy} />}
            {debugTab === 'sessions' && <SessionsTab currentContextId={contextId} onResume={wrappedResumeSession} />}
          </div>
        </div>
      )}
    </div>
  );
}
