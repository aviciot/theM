'use client';
import { useEffect, useRef, useState, useCallback } from 'react';
import { useSearchParams } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type OrchestratorFull, type TaskOut, type ArtifactOut, type AgentCard, type ContextSession } from '@/lib/api';

function getBridgeWs(): string {
  if (typeof window === 'undefined') return '';
  if (process.env.NEXT_PUBLIC_BRIDGE_WS_URL) return process.env.NEXT_PUBLIC_BRIDGE_WS_URL;
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.host}`;
}

// ── Types ──────────────────────────────────────────────────────────────────

type ChatMsg = { role: 'user' | 'assistant'; text: string; pending?: boolean };
type TraceEvent = { ts: number; type: string; [key: string]: unknown };
type RecordingState = 'idle' | 'recording' | 'transcribing';
type DebugTab = 'trace' | 'tasks' | 'artifacts' | 'memory' | 'sessions';

type AgentInvocation = {
  slug: string;
  tool: string;
  startedAt: number;
  endedAt?: number;
  latencyMs?: number;
  endpointUrl?: string;
  agentCard?: AgentCard | null;
  fetchingCard?: boolean;
};

// ── Helpers ────────────────────────────────────────────────────────────────

function traceLabel(ev: TraceEvent): string {
  switch (ev.type) {
    case 'run_start':       return `Run started — ${ev.goal as string}`;
    case 'iteration_start': return `Iteration ${ev.iteration}`;
    case 'tool_start':      return `${ev.tool} called`;
    case 'tool_done':       return `${ev.tool} done`;
    case 'usage':           return `Iter ${ev.iteration}: ${ev.input_tokens}+ ${ev.output_tokens}- tokens`;
    case 'run_end':         return `Run ${ev.status} — ${ev.iterations} iterations`;
    case 'error':           return `Error: ${ev.message}`;
    default:                return ev.type;
  }
}

function traceColor(type: string): string {
  if (type === 'error') return '#f87171';
  if (type === 'run_end') return '#4edea3';
  if (type.startsWith('tool')) return '#a78bfa';
  if (type === 'usage') return '#60a5fa';
  return 'var(--tm-text-muted)';
}

function stateColor(state: string): string {
  if (state === 'completed') return '#4edea3';
  if (state === 'failed') return '#f87171';
  if (state === 'working') return '#a78bfa';
  if (state === 'submitted') return '#60a5fa';
  if (state === 'canceled' || state === 'rejected') return '#94a3b8';
  return 'var(--tm-text-muted)';
}

// ── Debug Panel: right tray ────────────────────────────────────────────────

function TabBtn({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      style={{
        padding: '6px 12px',
        borderRadius: 6,
        border: 'none',
        background: active ? '#7c3aed' : 'transparent',
        color: active ? '#fff' : 'var(--tm-text-muted)',
        fontSize: 11,
        fontWeight: 600,
        cursor: 'pointer',
        letterSpacing: '0.05em',
      }}
    >
      {label}
    </button>
  );
}

function TraceTab({ trace, traceBottom, runId, contextId }: { trace: TraceEvent[]; traceBottom: React.RefObject<HTMLDivElement | null>; runId?: string | null; contextId?: string | null }) {
  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
      {(runId || contextId) && (
        <div style={{ fontSize: 10, fontFamily: 'monospace', color: 'var(--tm-text-muted)', background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: 4, padding: '6px 8px', display: 'flex', flexDirection: 'column', gap: 2, marginBottom: 4 }}>
          {runId && <span title={runId}><span style={{ opacity: 0.6 }}>run_id: </span>{runId}</span>}
          {contextId && <span title={contextId}><span style={{ opacity: 0.6 }}>ctx_id: </span>{contextId}</span>}
        </div>
      )}
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
          {ev.type === 'tool_start' && ev.input != null && (
            <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace', paddingLeft: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
              {JSON.stringify(ev.input, null, 2)}
            </div>
          )}
          {ev.type === 'tool_done' && ev.output != null && (
            <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace', paddingLeft: 12, maxHeight: 120, overflow: 'hidden', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
              {String(ev.output).slice(0, 300)}{String(ev.output).length > 300 ? '…' : ''}
            </div>
          )}
        </div>
      ))}
      <div ref={traceBottom} />
    </div>
  );
}

function TasksTab({ runId }: { runId: string | null }) {
  const [tasks, setTasks] = useState<TaskOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    if (!runId) { setTasks([]); return; }
    setLoading(true);
    setErr('');
    themApi.runTasks(runId)
      .then(setTasks)
      .catch(e => setErr(e.message))
      .finally(() => setLoading(false));
  }, [runId]);

  if (!runId) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, textAlign: 'center', marginTop: 40, padding: 16 }}>Start a run to see the task graph</div>;
  if (loading) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>Loading…</div>;
  if (err) return <div style={{ color: '#f87171', fontSize: 12, padding: 16 }}>{err}</div>;
  if (tasks.length === 0) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>No tasks yet</div>;

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
      {tasks.map(t => (
        <div key={t.id} style={{
          padding: '10px 12px',
          borderRadius: 8,
          border: '1px solid var(--tm-border)',
          background: 'var(--tm-surface)',
          marginLeft: t.parent_task_id ? 16 : 0,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <span style={{ fontSize: 10, padding: '2px 6px', borderRadius: 4, background: stateColor(t.state), color: '#fff', fontWeight: 700 }}>
              {t.state}
            </span>
            <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>
              {t.kind}
            </span>
            {t.tokens_used > 0 && (
              <span style={{ fontSize: 11, color: '#60a5fa', marginLeft: 'auto' }}>{t.tokens_used} tok</span>
            )}
          </div>
          <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>
            {t.id.slice(0, 8)}…
          </div>
          {t.error && (
            <div style={{ fontSize: 11, color: '#f87171', marginTop: 4, wordBreak: 'break-word' }}>{t.error}</div>
          )}
          {t.remote_task_id && (
            <div style={{ fontSize: 10, color: 'var(--tm-text-muted)', marginTop: 2 }}>remote: {t.remote_task_id}</div>
          )}
        </div>
      ))}
    </div>
  );
}

function ArtifactsTab({ runId }: { runId: string | null }) {
  const [artifacts, setArtifacts] = useState<ArtifactOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (!runId) { setArtifacts([]); return; }
    setLoading(true);
    setErr('');
    themApi.runArtifacts(runId)
      .then(setArtifacts)
      .catch(e => setErr(e.message))
      .finally(() => setLoading(false));
  }, [runId]);

  const toggle = (id: string) => setExpanded(prev => {
    const next = new Set(prev);
    next.has(id) ? next.delete(id) : next.add(id);
    return next;
  });

  if (!runId) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, textAlign: 'center', marginTop: 40, padding: 16 }}>Start a run to see artifacts</div>;
  if (loading) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>Loading…</div>;
  if (err) return <div style={{ color: '#f87171', fontSize: 12, padding: 16 }}>{err}</div>;
  if (artifacts.length === 0) return <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: 16 }}>No artifacts yet</div>;

  const downloadPart = (text: string, filename: string, mediaType: string) => {
    const blob = new Blob([text], { type: mediaType });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
      {artifacts.map(a => {
        const isOpen = expanded.has(a.id);
        const filePart = a.parts.find(p => p.filename || p.media_type);
        const isHtml = filePart?.media_type === 'text/html';
        const isMarkdown = filePart?.media_type === 'text/markdown';
        const textParts = a.parts.filter(p => !p.filename && !p.media_type && p.text !== undefined);

        return (
          <div key={a.id} style={{ border: '1px solid var(--tm-border)', borderRadius: 8, background: 'var(--tm-surface)', overflow: 'hidden' }}>
            <button
              onClick={() => toggle(a.id)}
              style={{ width: '100%', padding: '8px 12px', background: 'transparent', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8, textAlign: 'left' }}
            >
              <span style={{ fontSize: 11, color: '#a78bfa', fontWeight: 600 }}>{a.artifact_id}</span>
              {filePart?.filename
                ? <span style={{ fontSize: 11, color: '#4edea3', fontWeight: 600 }}>{filePart.filename}</span>
                : a.name && <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>{a.name}</span>
              }
              {filePart?.media_type && (
                <span style={{ fontSize: 10, color: 'var(--tm-text-muted)', padding: '1px 6px', border: '1px solid var(--tm-border)', borderRadius: 4 }}>{filePart.media_type}</span>
              )}
              <span style={{ fontSize: 10, color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{isOpen ? '▲' : '▼'}</span>
            </button>

            {isOpen && (
              <div style={{ borderTop: '1px solid var(--tm-border)' }}>
                {/* HTML artifact — inline iframe */}
                {isHtml && filePart?.text && (
                  <div>
                    <div style={{ padding: '6px 12px', display: 'flex', justifyContent: 'flex-end', borderBottom: '1px solid var(--tm-border)' }}>
                      <button
                        onClick={() => downloadPart(filePart.text!, filePart.filename!, filePart.media_type!)}
                        style={{ fontSize: 11, padding: '3px 10px', borderRadius: 5, border: '1px solid var(--tm-border)', background: 'transparent', color: '#a78bfa', cursor: 'pointer' }}
                      >
                        Download
                      </button>
                    </div>
                    <iframe
                      srcDoc={filePart.text}
                      style={{ width: '100%', height: 500, border: 'none', display: 'block' }}
                      sandbox="allow-scripts"
                      title={filePart.filename || 'preview'}
                    />
                  </div>
                )}

                {/* Markdown / slides — styled pre with download */}
                {isMarkdown && filePart?.text && (
                  <div style={{ padding: '0 12px 10px' }}>
                    <div style={{ display: 'flex', justifyContent: 'flex-end', padding: '6px 0' }}>
                      <button
                        onClick={() => downloadPart(filePart.text!, filePart.filename!, filePart.media_type!)}
                        style={{ fontSize: 11, padding: '3px 10px', borderRadius: 5, border: '1px solid var(--tm-border)', background: 'transparent', color: '#a78bfa', cursor: 'pointer' }}
                      >
                        Download
                      </button>
                    </div>
                    <pre style={{ fontSize: 12, color: 'var(--tm-text)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, maxHeight: 400, overflowY: 'auto' }}>
                      {filePart.text}
                    </pre>
                  </div>
                )}

                {/* Plain text parts */}
                {!filePart && (
                  <div style={{ padding: '0 12px 10px' }}>
                    {textParts.map((p, i) => (
                      <pre key={i} style={{ fontSize: 11, color: 'var(--tm-text)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: '8px 0 0' }}>
                        {p.text}
                      </pre>
                    ))}
                    {textParts.length === 0 && (
                      <pre style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', margin: '8px 0 0' }}>
                        {JSON.stringify(a.parts, null, 2)}
                      </pre>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function MemoryTab({ contextId, agentInvocations, allAgents }: {
  contextId: string | null;
  agentInvocations: AgentInvocation[];
  allAgents: OrchestratorFull['allowed_agent_ids'] | null;
}) {
  const [artifacts, setArtifacts] = useState<ArtifactOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');
  const [agentCards, setAgentCards] = useState<Record<string, { card?: AgentCard; fetching?: boolean; error?: string }>>({});

  useEffect(() => {
    if (!contextId) { setArtifacts([]); return; }
    setLoading(true);
    setErr('');
    themApi.contextArtifacts(contextId)
      .then(setArtifacts)
      .catch(e => setErr(e.message))
      .finally(() => setLoading(false));
  }, [contextId]);

  const fetchCard = async (inv: AgentInvocation) => {
    if (!inv.endpointUrl) return;
    const key = inv.slug;
    setAgentCards(prev => ({ ...prev, [key]: { fetching: true } }));
    try {
      const card = await themApi.fetchAgentCard(inv.endpointUrl!);
      setAgentCards(prev => ({ ...prev, [key]: { card } }));
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      setAgentCards(prev => ({ ...prev, [key]: { error: msg } }));
    }
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 12 }}>
      {/* Agent invocations with Fetch Agent Card button */}
      {agentInvocations.length > 0 && (
        <div>
          <div style={{ fontSize: 10, fontWeight: 700, color: 'var(--tm-text-muted)', letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 8 }}>Agents Used</div>
          {agentInvocations.map((inv, i) => {
            const cardState = agentCards[inv.slug];
            return (
              <div key={i} style={{ padding: '10px 12px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', marginBottom: 8 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                  <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text)' }}>{inv.slug}</span>
                  {inv.latencyMs != null && (
                    <span style={{ fontSize: 10, color: 'var(--tm-text-muted)' }}>{inv.latencyMs}ms</span>
                  )}
                  <button
                    onClick={() => fetchCard(inv)}
                    disabled={cardState?.fetching}
                    style={{
                      marginLeft: 'auto', padding: '3px 8px', borderRadius: 5, border: '1px solid var(--tm-border)',
                      background: 'transparent', color: '#a78bfa', fontSize: 10, cursor: 'pointer', fontWeight: 600,
                    }}
                  >
                    {cardState?.fetching ? 'Fetching…' : 'Fetch Agent Card'}
                  </button>
                </div>
                {cardState?.error && (
                  <div style={{ fontSize: 11, color: '#f87171' }}>{cardState.error}</div>
                )}
                {cardState?.card && (
                  <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', fontFamily: 'monospace' }}>
                    <div style={{ color: 'var(--tm-text)', fontWeight: 600, marginBottom: 4 }}>{cardState.card.name} {cardState.card.version && `v${cardState.card.version}`}</div>
                    {cardState.card.description && <div style={{ marginBottom: 4 }}>{cardState.card.description}</div>}
                    {cardState.card.capabilities && (
                      <div style={{ marginBottom: 4 }}>
                        streaming: {cardState.card.capabilities.streaming ? 'yes' : 'no'}
                        {' · '}
                        push: {cardState.card.capabilities.pushNotifications ? 'yes' : 'no'}
                      </div>
                    )}
                    {cardState.card.skills && cardState.card.skills.length > 0 && (
                      <div>Skills: {cardState.card.skills.map(s => s.name).join(', ')}</div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* Context artifacts */}
      <div>
        <div style={{ fontSize: 10, fontWeight: 700, color: 'var(--tm-text-muted)', letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 8 }}>
          Context Memory {contextId && <span style={{ fontWeight: 400, textTransform: 'none', marginLeft: 4 }}>{contextId.slice(0, 8)}…</span>}
        </div>
        {!contextId && <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>Start a run to inspect memory</div>}
        {loading && <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>Loading…</div>}
        {err && <div style={{ color: '#f87171', fontSize: 12 }}>{err}</div>}
        {artifacts.map(a => (
          <div key={a.id} style={{ padding: '8px 10px', borderRadius: 6, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', marginBottom: 6, fontSize: 11 }}>
            <div style={{ color: '#a78bfa', fontWeight: 600, marginBottom: 4 }}>{a.name || a.artifact_id}</div>
            {a.parts.filter(p => p.text).map((p, i) => (
              <div key={i} style={{ color: 'var(--tm-text)', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                {String(p.text).slice(0, 400)}{String(p.text).length > 400 ? '…' : ''}
              </div>
            ))}
          </div>
        ))}
        {contextId && !loading && artifacts.length === 0 && !err && (
          <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>No context artifacts yet</div>
        )}
      </div>
    </div>
  );
}

// ── Sessions tab ──────────────────────────────────────────────────────────
function SessionsTab({ onResume, currentContextId }: {
  onResume: (session: ContextSession) => void;
  currentContextId: string | null;
}) {
  const [sessions, setSessions] = useState<ContextSession[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    themApi.contexts().then(s => { setSessions(s); setLoading(false); }).catch(() => setLoading(false));
  }, [currentContextId]);

  const fmt = (iso: string) => {
    const d = new Date(iso);
    const now = new Date();
    const diff = now.getTime() - d.getTime();
    if (diff < 60000) return 'just now';
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
    return d.toLocaleDateString();
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '10px 12px' }}>
      {loading && <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>Loading…</div>}
      {!loading && sessions.length === 0 && (
        <div style={{ color: 'var(--tm-text-muted)', fontSize: 12 }}>No past sessions yet</div>
      )}
      {sessions.map(s => {
        const isCurrent = s.context_id === currentContextId;
        return (
          <div key={s.context_id} style={{
            padding: '8px 10px', borderRadius: 8, marginBottom: 6,
            border: `1px solid ${isCurrent ? '#7c3aed' : 'var(--tm-border)'}`,
            background: isCurrent ? 'rgba(124,58,237,0.08)' : 'var(--tm-surface)',
            cursor: 'pointer',
          }} onClick={() => !isCurrent && onResume(s)}>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--tm-text)', marginBottom: 3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {s.title}
            </div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span style={{ fontSize: 11, color: '#a78bfa' }}>{s.orchestrator_name}</span>
              <span style={{ fontSize: 11, color: 'var(--tm-text-muted)' }}>{s.turn_count} turn{s.turn_count !== 1 ? 's' : ''}</span>
              <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', marginLeft: 'auto' }}>{fmt(s.last_active)}</span>
            </div>
            {isCurrent && (
              <div style={{ fontSize: 10, color: '#7c3aed', marginTop: 3 }}>current session</div>
            )}
          </div>
        );
      })}
    </div>
  );
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
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [speaking, setSpeaking] = useState(false);
  const [recordingState, setRecordingState] = useState<RecordingState>('idle');
  const [mediaRecorder, setMediaRecorder] = useState<MediaRecorder | null>(null);
  const [debugTab, setDebugTab] = useState<DebugTab>('trace');
  const [agentInvocations, setAgentInvocations] = useState<AgentInvocation[]>([]);
  const [contextId, setContextId] = useState<string | null>(null);

  const chatWs = useRef<WebSocket | null>(null);
  const dashWs = useRef<WebSocket | null>(null);
  const runId = useRef<string | null>(null);
  const assistantBuf = useRef('');
  const chatBottom = useRef<HTMLDivElement>(null);
  const traceBottom = useRef<HTMLDivElement | null>(null);

  // Load orchestrators list, then check voice_enabled for the selected one
  useEffect(() => {
    themApi.orchestrators().then(list => {
      const enabled = list.filter(o => o.enabled);
      setOrchestrators(enabled);
      const name = initialOrch || (enabled.length > 0 ? enabled[0].name : '');
      if (!initialOrch && enabled.length > 0) setSelectedOrch(enabled[0].name);
      const orch = enabled.find(o => o.name === name);
      setVoiceEnabled(orch?.voice_enabled ?? false);
      setTtsEnabled(orch?.tts_enabled ?? false);
    });
  }, [initialOrch]);

  // When selected orchestrator changes, update voice_enabled + tts_enabled
  useEffect(() => {
    if (!selectedOrch) return;
    const orch = orchestrators.find(o => o.name === selectedOrch);
    setVoiceEnabled(orch?.voice_enabled ?? false);
    setTtsEnabled(orch?.tts_enabled ?? false);
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
  const sendText = useCallback(async (text: string, currentContextId?: string | null) => {
    if (!text.trim() || !selectedOrch || busy) return;
    setInput('');
    setBusy(true);
    setTrace([]);
    setAgentInvocations([]);
    setMessages(prev => [...prev, { role: 'user', text }]);

    const r = await fetch('/api/auth/token');
    if (!r.ok) { setBusy(false); return; }
    const { token } = await r.json();

    const ws = new WebSocket(`${getBridgeWs()}/ws/orchestrate/${selectedOrch}?token=${encodeURIComponent(token)}`);
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
        if (msg.type === 'ready') {
          runId.current = msg.run_id;
          if (msg.context_id) setContextId(msg.context_id as string);
          openDashWs(msg.run_id);
          setMessages(prev => [...prev, { role: 'assistant', text: '', pending: true }]);
          setStatus(`Run ${(msg.run_id as string).slice(0, 8)}…`);

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
          const slug = (msg.tool as string).replace(/^agent__/, '');
          setStatus(`Calling ${slug}…`);
          setAgentInvocations(prev => {
            const existing = prev.find(a => a.tool === msg.tool);
            if (existing) return prev;
            return [...prev, { slug, tool: msg.tool as string, startedAt: Date.now() }];
          });

        } else if (msg.type === 'tool_done') {
          const slug = (msg.tool as string).replace(/^agent__/, '');
          setStatus(`${slug} done`);
          setAgentInvocations(prev => prev.map(a =>
            a.tool === msg.tool
              ? { ...a, endedAt: Date.now(), latencyMs: msg.latency_ms as number ?? Date.now() - a.startedAt }
              : a
          ));

        } else if (msg.type === 'done') {
          setMessages(prev => {
            const copy = [...prev];
            const last = copy[copy.length - 1];
            if (last?.role === 'assistant') copy[copy.length - 1] = { ...last, pending: false };
            return copy;
          });
          // TTS — stream audio chunks via MediaSource for low-latency playback
          if (ttsEnabled && assistantBuf.current) {
            const textToSpeak = assistantBuf.current;
            const orchName = selectedOrch;
            setSpeaking(true);
            themApi.tts(orchName, textToSpeak)
              .then(async res => {
                if (!res.body) throw new Error('no body');
                // MediaSource lets us start playing before all bytes arrive
                const ms = new MediaSource();
                const url = URL.createObjectURL(ms);
                const audio = new Audio(url);
                audio.onended = () => { setSpeaking(false); URL.revokeObjectURL(url); };
                audio.onerror = () => { setSpeaking(false); URL.revokeObjectURL(url); };

                await new Promise<void>((resolve) => { ms.addEventListener('sourceopen', () => resolve(), { once: true }); });
                audio.play();

                const sb = ms.addSourceBuffer('audio/mpeg');
                const reader = res.body.getReader();

                const pump = async () => {
                  while (true) {
                    const { done, value } = await reader.read();
                    if (done) { ms.endOfStream(); break; }
                    // Wait for sourceBuffer to be ready before appending
                    await new Promise<void>(r => {
                      if (!sb.updating) { r(); return; }
                      sb.addEventListener('updateend', () => r(), { once: true });
                    });
                    sb.appendBuffer(value);
                  }
                };
                pump().catch(() => { setSpeaking(false); ms.endOfStream(); });
              })
              .catch(() => setSpeaking(false));
          }
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

  const send = useCallback(() => sendText(input, contextId), [input, contextId, sendText]);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  };

  const clearChat = () => {
    setMessages([]);
    setTrace([]);
    setStatus('');
    setAgentInvocations([]);
    setContextId(null);
    runId.current = null;
  };

  // ── Voice recording ───────────────────────────────────────────────────────
  const startRecording = async () => {
    if (recordingState !== 'idle' || !selectedOrch) return;
    try {
      const mediaDevices = navigator.mediaDevices ??
        // HTTP non-localhost: mediaDevices is undefined; nothing we can do
        (() => { throw new Error('Microphone requires HTTPS or localhost'); })();
      const stream = await mediaDevices.getUserMedia({ audio: true });
      const recorder = new MediaRecorder(stream, { mimeType: 'audio/webm' });
      const chunks: Blob[] = [];
      recorder.ondataavailable = (e) => { if (e.data.size > 0) chunks.push(e.data); };
      recorder.onstop = async () => {
        stream.getTracks().forEach(t => t.stop());
        const blob = new Blob(chunks, { type: 'audio/webm' });
        setRecordingState('transcribing');
        try {
          const result = await themApi.transcribe(selectedOrch, blob);
          if (result.text) {
            await sendText(result.text, contextId);
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
            {speaking && (
              <span style={{ fontSize: 12, color: '#a78bfa', marginLeft: 'auto' }}>🔊 Speaking…</span>
            )}
            {!speaking && status && (
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

            {/* Debug panel — tabbed right tray */}
            <div style={{ width: 400, display: 'flex', flexDirection: 'column', background: 'var(--tm-bg)', borderLeft: '1px solid var(--tm-border)' }}>
              {/* Tab bar */}
              <div style={{ padding: '8px 12px', borderBottom: '1px solid var(--tm-border)', display: 'flex', gap: 4, alignItems: 'center' }}>
                <TabBtn label="Trace" active={debugTab === 'trace'} onClick={() => setDebugTab('trace')} />
                <TabBtn label="Tasks" active={debugTab === 'tasks'} onClick={() => setDebugTab('tasks')} />
                <TabBtn label="Artifacts" active={debugTab === 'artifacts'} onClick={() => setDebugTab('artifacts')} />
                <TabBtn label="Memory" active={debugTab === 'memory'} onClick={() => setDebugTab('memory')} />
                <TabBtn label="Sessions" active={debugTab === 'sessions'} onClick={() => setDebugTab('sessions')} />
              </div>
              {/* Tab content */}
              <div style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
                {debugTab === 'trace' && <TraceTab trace={trace} traceBottom={traceBottom} runId={runId.current} contextId={contextId} />}
                {debugTab === 'tasks' && <TasksTab runId={runId.current} />}
                {debugTab === 'artifacts' && <ArtifactsTab runId={runId.current} />}
                {debugTab === 'memory' && (
                  <MemoryTab
                    contextId={contextId}
                    agentInvocations={agentInvocations}
                    allAgents={null}
                  />
                )}
                {debugTab === 'sessions' && (
                  <SessionsTab
                    currentContextId={contextId}
                    onResume={s => {
                      setContextId(s.context_id);
                      if (s.orchestrator_name !== selectedOrch) setSelectedOrch(s.orchestrator_name);
                      setMessages([{ role: 'assistant', text: `↩ Resumed session — ${s.turn_count} prior turn${s.turn_count !== 1 ? 's' : ''}` }]);
                      setDebugTab('trace');
                    }}
                  />
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </AuthGuard>
  );
}
