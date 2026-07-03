'use client';
import { useEffect, useRef, useState, useCallback } from 'react';
import { useSearchParams } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { odinApi, type OrchestratorFull } from '@/lib/api';

function getBridgeWs(): string {
  if (typeof window === 'undefined') return 'ws://localhost:8001';
  // In production, NEXT_PUBLIC_BRIDGE_WS_URL is set at build time
  if (process.env.NEXT_PUBLIC_BRIDGE_WS_URL) return process.env.NEXT_PUBLIC_BRIDGE_WS_URL;
  // In dev, bridge is on port 8001 of the same host
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.hostname}:8001`;
}

// ── Types ──────────────────────────────────────────────────────────────────

type ChatMsg = { role: 'user' | 'assistant'; text: string; pending?: boolean };
type TraceEvent = { ts: number; type: string; [key: string]: unknown };
type RecordingState = 'idle' | 'recording' | 'transcribing';

// ── Helpers ────────────────────────────────────────────────────────────────

function traceLabel(ev: TraceEvent): string {
  switch (ev.type) {
    case 'run_start':     return `▶ Run started — ${ev.goal as string}`;
    case 'iteration_start': return `↻ Iteration ${ev.iteration}`;
    case 'tool_start':    return `⚙ ${ev.tool} called`;
    case 'tool_done':     return `✓ ${ev.tool} done`;
    case 'usage':         return `📊 Iter ${ev.iteration}: ${ev.input_tokens}↑ ${ev.output_tokens}↓ tokens`;
    case 'run_end':       return `■ Run ${ev.status} — ${ev.iterations} iterations`;
    case 'error':         return `✗ Error: ${ev.message}`;
    default:              return ev.type;
  }
}

function traceColor(type: string): string {
  if (type === 'error') return '#f87171';
  if (type === 'run_end') return '#4edea3';
  if (type.startsWith('tool')) return '#a78bfa';
  if (type === 'usage') return '#60a5fa';
  return 'var(--tm-text-muted)';
}

// ── Mic SVG icon ───────────────────────────────────────────────────────────
function MicIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
      <path d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.93V21h2v-3.07A7 7 0 0 0 19 11h-2z"/>
    </svg>
  );
}

// ── Spinner ────────────────────────────────────────────────────────────────
function Spinner() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
      <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" style={{ animation: 'spin 1s linear infinite', transformOrigin: 'center' }} />
    </svg>
  );
}

// ── Component ──────────────────────────────────────────────────────────────

export default function PlaygroundPage() {
  const searchParams = useSearchParams();
  const initialOrch = searchParams.get('orchestrator') || '';

  const [orchestrators, setOrchestrators] = useState<OrchestratorFull[]>([]);
  const [selectedOrch, setSelectedOrch] = useState(initialOrch);
  const [messages, setMessages] = useState<ChatMsg[]>([]);
  const [trace, setTrace] = useState<TraceEvent[]>([]);
  const [input, setInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [voiceEnabled, setVoiceEnabled] = useState(false);
  const [recordingState, setRecordingState] = useState<RecordingState>('idle');
  const [mediaRecorder, setMediaRecorder] = useState<MediaRecorder | null>(null);

  const chatWs = useRef<WebSocket | null>(null);
  const dashWs = useRef<WebSocket | null>(null);
  const runId = useRef<string | null>(null);
  const assistantBuf = useRef('');
  const chatBottom = useRef<HTMLDivElement>(null);
  const traceBottom = useRef<HTMLDivElement>(null);

  // Load orchestrators list, then check voice_enabled for the selected one
  useEffect(() => {
    odinApi.orchestrators().then(list => {
      const enabled = list.filter(o => o.enabled);
      setOrchestrators(enabled);
      const name = initialOrch || (enabled.length > 0 ? enabled[0].name : '');
      if (!initialOrch && enabled.length > 0) setSelectedOrch(enabled[0].name);
      const orch = enabled.find(o => o.name === name);
      setVoiceEnabled(orch?.voice_enabled ?? false);
    });
  }, [initialOrch]);

  // When selected orchestrator changes, update voice_enabled
  useEffect(() => {
    if (!selectedOrch) return;
    const orch = orchestrators.find(o => o.name === selectedOrch);
    setVoiceEnabled(orch?.voice_enabled ?? false);
  }, [selectedOrch, orchestrators]);

  useEffect(() => {
    chatBottom.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  useEffect(() => {
    traceBottom.current?.scrollIntoView({ behavior: 'smooth' });
  }, [trace]);

  // ── Dashboard WS for trace ───────────────────────────────────────────────
  const openDashWs = useCallback(async (rid: string) => {
    const r = await fetch('/api/auth/token');
    if (!r.ok) return;
    const { token } = await r.json();

    const ws = new WebSocket(`${getBridgeWs()}/ws/dashboard?token=${encodeURIComponent(token)}`);
    dashWs.current = ws;

    ws.onopen = () => {
      ws.send(JSON.stringify({ type: 'subscribe', channels: [`run:${rid}`] }));
    };

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'ping') return;
        if (msg.channel?.startsWith('run:')) {
          setTrace(prev => [...prev, { ts: Date.now(), ...msg.event }]);
        }
      } catch {}
    };

    ws.onerror = () => ws.close();
  }, []);

  // ── Send message ─────────────────────────────────────────────────────────
  const sendText = useCallback(async (text: string) => {
    if (!text.trim() || !selectedOrch || busy) return;
    setInput('');
    setBusy(true);
    setTrace([]);
    setMessages(prev => [...prev, { role: 'user', text }]);

    const r = await fetch('/api/auth/token');
    if (!r.ok) { setBusy(false); return; }
    const { token } = await r.json();

    const ws = new WebSocket(`${getBridgeWs()}/ws/orchestrate/${selectedOrch}?token=${encodeURIComponent(token)}`);
    chatWs.current = ws;
    assistantBuf.current = '';

    ws.onopen = () => {
      setStatus('Connected');
      ws.send(JSON.stringify({ type: 'message', content: text }));
    };

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'ready') {
          runId.current = msg.run_id;
          openDashWs(msg.run_id);
          // Add pending assistant message
          setMessages(prev => [...prev, { role: 'assistant', text: '', pending: true }]);
          setStatus(`Run ${msg.run_id.slice(0, 8)}…`);

        } else if (msg.type === 'token') {
          assistantBuf.current += msg.text || '';
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant') {
              copy[copy.length - 1] = { ...last, text: assistantBuf.current };
            }
            return copy;
          });

        } else if (msg.type === 'tool_start') {
          setStatus(`Calling ${msg.tool}…`);

        } else if (msg.type === 'tool_done') {
          setStatus(`${msg.tool} done`);

        } else if (msg.type === 'done') {
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant') copy[copy.length - 1] = { ...last, pending: false };
            return copy;
          });
          setStatus(`Done — ${msg.iterations} iteration(s)`);
          setBusy(false);
          ws.close();
          dashWs.current?.close();

        } else if (msg.type === 'error') {
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant' && last.pending) {
              copy[copy.length - 1] = { role: 'assistant', text: `Error: ${msg.message}`, pending: false };
            } else {
              copy.push({ role: 'assistant', text: `Error: ${msg.message}` });
            }
            return copy;
          });
          setStatus(`Error: ${msg.message}`);
          setBusy(false);
          ws.close();
        }
      } catch {}
    };

    ws.onerror = (err) => {
      console.error('Orchestrator WS error', err);
      setStatus('WebSocket error — check console');
      setBusy(false);
    };

    ws.onclose = (ev) => {
      console.log('Orchestrator WS closed', ev.code, ev.reason);
      if (busy) setBusy(false);
    };
  }, [selectedOrch, busy, openDashWs]);

  const send = useCallback(() => sendText(input), [input, sendText]);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  };

  const clearChat = () => {
    setMessages([]);
    setTrace([]);
    setStatus('');
  };

  // ── Voice recording ───────────────────────────────────────────────────────
  const startRecording = async () => {
    if (recordingState !== 'idle' || !selectedOrch) return;
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const recorder = new MediaRecorder(stream, { mimeType: 'audio/webm' });
      const chunks: Blob[] = [];
      recorder.ondataavailable = (e) => { if (e.data.size > 0) chunks.push(e.data); };
      recorder.onstop = async () => {
        stream.getTracks().forEach(t => t.stop());
        const blob = new Blob(chunks, { type: 'audio/webm' });
        setRecordingState('transcribing');
        try {
          const result = await odinApi.transcribe(selectedOrch, blob);
          if (result.text) {
            await sendText(result.text);
          }
        } catch (e) {
          console.error('Transcription error', e);
          setStatus('Transcription failed');
          setTimeout(() => setStatus(''), 3000);
        } finally {
          setRecordingState('idle');
        }
      };
      recorder.start();
      setMediaRecorder(recorder);
      setRecordingState('recording');
    } catch (e) {
      console.error('Mic access error', e);
      setStatus('Microphone access denied');
      setTimeout(() => setStatus(''), 3000);
    }
  };

  const stopRecording = () => {
    if (mediaRecorder && recordingState === 'recording') {
      mediaRecorder.stop();
      setMediaRecorder(null);
    }
  };

  // Mic button style
  const micBtnStyle = (): React.CSSProperties => {
    if (recordingState === 'recording') {
      return {
        padding: '10px 14px', borderRadius: 12, border: 'none',
        background: '#ef4444', color: '#fff', cursor: 'pointer',
        alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center',
        outline: '2px solid #f87171',
      };
    }
    if (recordingState === 'transcribing') {
      return {
        padding: '10px 14px', borderRadius: 12, border: 'none',
        background: 'var(--tm-surface)', color: 'var(--tm-text-muted)', cursor: 'not-allowed',
        alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center',
      };
    }
    return {
      padding: '10px 14px', borderRadius: 12, border: 'none',
      background: 'var(--tm-surface-2)', color: 'var(--tm-text-muted)', cursor: 'pointer',
      alignSelf: 'flex-end', display: 'flex', alignItems: 'center', justifyContent: 'center',
    };
  };

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <AuthGuard>
      <div style={{ display: 'flex', height: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', minWidth: 0 }}>
          {/* Header */}
          <div style={{ padding: '16px 24px', borderBottom: '1px solid var(--tm-border)', display: 'flex', alignItems: 'center', gap: 16 }}>
            <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--tm-text)' }}>Playground</div>
            <select
              value={selectedOrch}
              onChange={e => { setSelectedOrch(e.target.value); clearChat(); }}
              style={{ padding: '6px 12px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', color: 'var(--tm-text)', fontSize: 13, cursor: 'pointer' }}
            >
              {orchestrators.map(o => (
                <option key={o.id} value={o.name}>{o.display_name || o.name}</option>
              ))}
            </select>
            {status && (
              <span style={{ fontSize: 12, color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{status}</span>
            )}
            <button onClick={clearChat} style={{ marginLeft: status ? 0 : 'auto', padding: '4px 12px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: 12 }}>
              Clear
            </button>
          </div>

          {/* Split view */}
          <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
            {/* Chat pane */}
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', borderRight: '1px solid var(--tm-border)' }}>
              <div style={{ flex: 1, overflowY: 'auto', padding: '20px 24px', display: 'flex', flexDirection: 'column', gap: 16 }}>
                {messages.length === 0 && (
                  <div style={{ color: 'var(--tm-text-muted)', fontSize: 14, textAlign: 'center', marginTop: 60 }}>
                    Select an orchestrator and send a message to begin
                  </div>
                )}
                {messages.map((m, i) => (
                  <div key={i} style={{ display: 'flex', justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
                    <div style={{
                      maxWidth: '75%',
                      padding: '10px 14px',
                      borderRadius: m.role === 'user' ? '16px 16px 4px 16px' : '16px 16px 16px 4px',
                      background: m.role === 'user' ? '#7c3aed' : 'var(--tm-surface)',
                      color: m.role === 'user' ? '#fff' : 'var(--tm-text)',
                      fontSize: 14,
                      lineHeight: 1.5,
                      whiteSpace: 'pre-wrap',
                      wordBreak: 'break-word',
                    }}>
                      {m.text || (m.pending ? <span style={{ opacity: 0.5 }}>thinking…</span> : '')}
                    </div>
                  </div>
                ))}
                <div ref={chatBottom} />
              </div>

              {/* Input */}
              <div style={{ padding: '12px 16px', borderTop: '1px solid var(--tm-border)', display: 'flex', gap: 8 }}>
                {voiceEnabled && (
                  <button
                    style={micBtnStyle()}
                    onMouseDown={startRecording}
                    onMouseUp={stopRecording}
                    onTouchStart={startRecording}
                    onTouchEnd={stopRecording}
                    disabled={recordingState === 'transcribing' || busy || !selectedOrch}
                    title={recordingState === 'recording' ? 'Release to transcribe' : 'Hold to record'}
                  >
                    {recordingState === 'transcribing' ? <Spinner /> : <MicIcon />}
                  </button>
                )}
                <textarea
                  value={input}
                  onChange={e => setInput(e.target.value)}
                  onKeyDown={onKey}
                  disabled={busy || !selectedOrch}
                  placeholder="Send a message… (Enter to send, Shift+Enter for newline)"
                  rows={3}
                  style={{
                    flex: 1,
                    padding: '10px 14px',
                    borderRadius: 12,
                    border: '1px solid var(--tm-border)',
                    background: 'var(--tm-surface)',
                    color: 'var(--tm-text)',
                    fontSize: 13,
                    resize: 'none',
                    outline: 'none',
                    fontFamily: 'inherit',
                    lineHeight: 1.5,
                  }}
                />
                <button
                  onClick={send}
                  disabled={busy || !input.trim() || !selectedOrch}
                  style={{
                    padding: '10px 18px',
                    borderRadius: 12,
                    border: 'none',
                    background: busy ? 'var(--tm-surface)' : '#7c3aed',
                    color: busy ? 'var(--tm-text-muted)' : '#fff',
                    fontSize: 13,
                    fontWeight: 600,
                    cursor: busy ? 'not-allowed' : 'pointer',
                    alignSelf: 'flex-end',
                  }}
                >
                  {busy ? '…' : 'Send'}
                </button>
              </div>
            </div>

            {/* Trace pane */}
            <div style={{ width: 380, display: 'flex', flexDirection: 'column', background: 'var(--tm-bg)' }}>
              <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--tm-border)', fontSize: 11, fontWeight: 700, color: 'var(--tm-text-muted)', letterSpacing: '0.08em', textTransform: 'uppercase' }}>
                Internal Trace
              </div>
              <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
                {trace.length === 0 && (
                  <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, textAlign: 'center', marginTop: 40 }}>
                    Trace events appear here when a run starts
                  </div>
                )}
                {trace.map((ev, i) => (
                  <div key={i} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                    <div style={{ fontSize: 12, color: traceColor(ev.type), fontWeight: 500, fontFamily: 'monospace' }}>
                      {traceLabel(ev)}
                    </div>
                    {ev.type === 'tool_start' && ev.input && (
                      <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace', paddingLeft: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                        {JSON.stringify(ev.input, null, 2)}
                      </div>
                    )}
                    {ev.type === 'tool_done' && ev.output && (
                      <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace', paddingLeft: 12, maxHeight: 120, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                        {String(ev.output).slice(0, 300)}{String(ev.output).length > 300 ? '…' : ''}
                      </div>
                    )}
                  </div>
                ))}
                <div ref={traceBottom} />
              </div>
            </div>
          </div>
        </div>
      </div>
    </AuthGuard>
  );
}
