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

  const chatWs = useRef<WebSocket | null>(null);
  const dashWs = useRef<WebSocket | null>(null);
  const runId = useRef<string | null>(null);
  const assistantBuf = useRef('');
  const chatBottom = useRef<HTMLDivElement>(null);
  const traceBottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    odinApi.orchestrators().then(list => {
      setOrchestrators(list.filter(o => o.enabled));
      if (!initialOrch && list.length > 0) setSelectedOrch(list[0].name);
    });
  }, [initialOrch]);

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
  const send = useCallback(async () => {
    if (!input.trim() || !selectedOrch || busy) return;
    const text = input.trim();
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
  }, [input, selectedOrch, busy, openDashWs]);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  };

  const clearChat = () => {
    setMessages([]);
    setTrace([]);
    setStatus('');
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
