'use client';
import { useEffect, useRef, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Agent, type AgentSkill, type DiscoverResult, type OrchestratorFull, type ScanResult } from '@/lib/api';

const EMPTY_FORM = {
  slug: '',
  display_name: '',
  description: '',
  transport: 'a2a_async',
  endpoint_url: '',
  auth_token: '',
  max_concurrency: 3,
  timeout_seconds: 60,
  enabled: true,
  skills: [] as AgentSkill[],
  supports_streaming: false,
  supports_push: false,
  agent_card: null as Record<string, unknown> | null,
  agent_card_url: '',
};

type FormState = typeof EMPTY_FORM;

// ── Diff helpers ──────────────────────────────────────────────────────────────

type CardDiff = {
  hasChanges: boolean;
  displayName: { old: string; new: string; changed: boolean };
  description: { old: string; new: string; changed: boolean };
  skills: { old: AgentSkill[]; new: AgentSkill[]; changed: boolean };
  streaming: { old: boolean; new: boolean; changed: boolean };
  push: { old: boolean; new: boolean; changed: boolean };
  version: { old: string; new: string; changed: boolean };
  provider: { old: string; new: string; changed: boolean };
};

function buildDiff(agent: Agent, result: DiscoverResult): CardDiff {
  const oldCard = (agent.agent_card ?? {}) as Record<string, unknown>;
  const newCard = (result.agent_card ?? {}) as Record<string, unknown>;

  const oldVersion = String(oldCard.version ?? '');
  const newVersion = String(newCard.version ?? '');

  const oldProvider = typeof oldCard.provider === 'object' && oldCard.provider
    ? String((oldCard.provider as Record<string, unknown>).organization ?? '')
    : '';
  const newProvider = typeof newCard.provider === 'object' && newCard.provider
    ? String((newCard.provider as Record<string, unknown>).organization ?? '')
    : '';

  const oldSkillsJson = JSON.stringify((agent.skills ?? []).map(s => ({ id: s.id, name: s.name, description: s.description ?? '', tags: (s.tags ?? []).sort() })));
  const newSkillsJson = JSON.stringify((result.skills ?? []).map(s => ({ id: s.id, name: s.name, description: s.description ?? '', tags: (s.tags ?? []).sort() })));

  const fields = {
    displayName: { old: agent.display_name, new: result.display_name, changed: agent.display_name !== result.display_name },
    description: { old: agent.description, new: result.description, changed: agent.description !== result.description },
    skills: { old: agent.skills ?? [], new: result.skills, changed: oldSkillsJson !== newSkillsJson },
    streaming: { old: !!agent.supports_streaming, new: result.supports_streaming, changed: !!agent.supports_streaming !== result.supports_streaming },
    push: { old: !!agent.supports_push, new: result.supports_push, changed: !!agent.supports_push !== result.supports_push },
    version: { old: oldVersion, new: newVersion, changed: oldVersion !== newVersion },
    provider: { old: oldProvider, new: newProvider, changed: oldProvider !== newProvider },
  };

  const hasChanges = Object.values(fields).some(f => f.changed);
  return { hasChanges, ...fields };
}

// ── Utilities ─────────────────────────────────────────────────────────────────

function timeAgo(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diffMs / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

function cardVersion(card: Record<string, unknown> | null | undefined): string {
  return card ? String(card.version ?? '') : '';
}
function cardProvider(card: Record<string, unknown> | null | undefined): string {
  if (!card?.provider || typeof card.provider !== 'object') return '';
  return String((card.provider as Record<string, unknown>).organization ?? '');
}
function cardDocUrl(card: Record<string, unknown> | null | undefined): string {
  return card ? String(card.documentationUrl ?? '') : '';
}
function cardAuth(card: Record<string, unknown> | null | undefined): string[] {
  if (!card?.authentication || !Array.isArray(card.authentication)) return [];
  return (card.authentication as unknown[]).map(a => typeof a === 'string' ? a : String((a as Record<string, unknown>).scheme ?? a));
}

// ── Design tokens ─────────────────────────────────────────────────────────────

// Risk colors — embossed dark-panel style matching the card design language
function riskColors(risk: 'low' | 'medium' | 'high') {
  if (risk === 'low') return {
    bg: 'linear-gradient(145deg, rgba(66,217,139,.14) 0%, rgba(42,181,109,.08) 100%)',
    border: 'rgba(66,217,139,.28)',
    color: '#4edea3',
    glow: 'rgba(42,181,109,.18)',
  };
  if (risk === 'medium') return {
    bg: 'linear-gradient(145deg, rgba(230,184,92,.14) 0%, rgba(180,131,9,.08) 100%)',
    border: 'rgba(230,184,92,.28)',
    color: '#e6b85c',
    glow: 'rgba(180,131,9,.18)',
  };
  return {
    bg: 'linear-gradient(145deg, rgba(220,38,38,.14) 0%, rgba(185,28,28,.08) 100%)',
    border: 'rgba(220,38,38,.28)',
    color: '#f87171',
    glow: 'rgba(220,38,38,.18)',
  };
}

function statusIcon(status: 'pass' | 'fail' | 'warn') {
  if (status === 'pass') return { icon: '✓', color: '#4edea3' };
  if (status === 'warn') return { icon: '⚠', color: '#e6b85c' };
  return { icon: '✗', color: '#f87171' };
}

// Score ring color
function scoreRingColor(score: number) {
  if (score >= 75) return '#4edea3';
  if (score >= 45) return '#e6b85c';
  return '#f87171';
}

// ── Sub-components ─────────────────────────────────────────────────────────────

function Modal({ title, onClose, wide, children }: { title: string; onClose: () => void; wide?: boolean; children: React.ReactNode }) {
  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 100,
      background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
    }} onClick={onClose}>
      <div style={{
        position: 'relative',
        background: 'linear-gradient(145deg, rgba(255,255,255,.028) 0%, rgba(255,255,255,.008) 36%, rgba(0,0,0,.045) 100%), rgba(10,22,38,.97)',
        border: '1px solid rgba(132,158,190,.2)',
        borderRadius: '18px',
        padding: '32px',
        width: wide ? '760px' : '580px',
        maxHeight: '90vh',
        overflowY: 'auto',
        boxShadow: '0 24px 64px rgba(0,0,0,.55), 0 6px 18px rgba(0,0,0,.3), inset 0 1px 0 rgba(255,255,255,.06), inset 0 -1px 0 rgba(0,0,0,.3)',
      }} onClick={(e) => e.stopPropagation()}>
        {/* Inner inset highlight ring */}
        <div style={{
          position: 'absolute', inset: '1px', borderRadius: '17px', pointerEvents: 'none',
          boxShadow: 'inset 0 0 0 1px rgba(255,255,255,.018)',
        }} />
        {/* Top accent line */}
        <div style={{
          position: 'absolute', top: 0, left: '24px', right: '24px', height: '1px', pointerEvents: 'none',
          background: 'linear-gradient(90deg, transparent, rgba(40,215,238,.38), transparent)',
          borderRadius: '1px',
        }} />
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <h3 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-text)', letterSpacing: '-0.01em' }}>{title}</h3>
          <button onClick={onClose} style={{
            background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.5)',
            border: '1px solid rgba(132,157,188,.14)',
            boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
            borderRadius: '8px', cursor: 'pointer', color: 'var(--tm-text-muted)',
            fontSize: '16px', width: '32px', height: '32px', display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>✕</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '16px' }}>
      <label style={{ display: 'block', fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', marginBottom: '6px', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        {label}
      </label>
      {children}
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: '8px' }}>
      {children}
    </div>
  );
}

function ChangedBadge({ old: oldVal, next: newVal }: { old: string; next: string }) {
  return (
    <span style={{ fontSize: '11px' }}>
      <span style={{ color: '#94a3b8', textDecoration: 'line-through', marginRight: '6px' }}>{oldVal || '—'}</span>
      <span style={{ color: '#4edea3', fontWeight: 600 }}>{newVal || '—'}</span>
    </span>
  );
}

function DiffRow({ label, changed, oldVal, newVal }: { label: string; changed: boolean; oldVal: string; newVal: string }) {
  if (!changed && !newVal) return null;
  return (
    <div style={{ display: 'flex', gap: '8px', alignItems: 'flex-start', padding: '6px 0', borderBottom: '1px solid var(--tm-border)' }}>
      <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px', flexShrink: 0 }}>{label}</span>
      {changed
        ? <ChangedBadge old={oldVal} next={newVal} />
        : <span style={{ fontSize: '12px', color: 'var(--tm-text)' }}>{newVal}</span>
      }
    </div>
  );
}

// Nested surface — used for stat boxes, probe rows, skill rows
const nestedSurface: React.CSSProperties = {
  background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.50)',
  border: '1px solid rgba(132,157,188,.12)',
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
};

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: '8px',
  border: '1px solid rgba(132,157,188,.18)',
  background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.50)',
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
  fontSize: '14px', color: 'var(--tm-text)', boxSizing: 'border-box',
};

// Module-level set — survives tab switches (component unmount/remount)
const _inFlightScans = new Set<string>();

// ── Main page ─────────────────────────────────────────────────────────────────

export default function AdminAgentsPage() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [orchestrators, setOrchestrators] = useState<OrchestratorFull[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editing, setEditing] = useState<Agent | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<Agent | null>(null);
  const [testResults, setTestResults] = useState<Record<string, { ok: boolean; latency_ms: number; detail: string } | 'testing'>>({});
  const [rowDiscoverState, setRowDiscoverState] = useState<Record<string, 'discovering'>>({});
  const [discoverPopup, setDiscoverPopup] = useState<{ agent: Agent; result: DiscoverResult; diff: CardDiff } | null>(null);
  const [applyingDiscover, setApplyingDiscover] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [discoverError, setDiscoverError] = useState('');
  const [scanResults, setScanResults] = useState<Record<string, ScanResult | 'scanning'>>({});
  const [scanModal, setScanModal] = useState<{ agent: Agent; result: ScanResult } | null>(null);
  const dashWsRef = useRef<WebSocket | null>(null);
  const agentIdsKeyRef = useRef<string>('');

  const reload = () => {
    Promise.all([themApi.agents(), themApi.orchestrators()])
      .then(([a, o]) => {
        setAgents(a);
        setOrchestrators(o);
        setScanResults((prev) => {
          const next = { ...prev };
          for (const agent of a) {
            if (_inFlightScans.has(agent.id)) {
              if (agent.last_scan_result) {
                next[agent.id] = agent.last_scan_result;
                _inFlightScans.delete(agent.id);
              } else {
                next[agent.id] = 'scanning';
              }
            } else if (agent.last_scan_result && !next[agent.id]) {
              next[agent.id] = agent.last_scan_result;
            } else if (agent.last_scan_result && next[agent.id] === 'scanning') {
              next[agent.id] = agent.last_scan_result;
            }
          }
          return next;
        });
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => { reload(); }, []);

  // Dashboard WS — subscribe to agent:<id> channels for live scan events
  useEffect(() => {
    if (agents.length === 0) return;
    const key = agents.map((a) => a.id).sort().join(',');
    if (key === agentIdsKeyRef.current) return;
    agentIdsKeyRef.current = key;

    if (dashWsRef.current) {
      dashWsRef.current.close();
      dashWsRef.current = null;
    }

    let ws: WebSocket;
    fetch('/api/auth/token')
      .then((r) => r.json())
      .then((data: { access_token?: string }) => {
        if (!data.access_token) return;
        const wsBase = window.location.origin
          .replace('http://', 'ws://')
          .replace('https://', 'wss://');
        ws = new WebSocket(`${wsBase}/ws/dashboard?token=${data.access_token}`);
        dashWsRef.current = ws;

        ws.onopen = () => {
          ws.send(JSON.stringify({
            type: 'subscribe',
            channels: agents.map((a) => `agent:${a.id}`),
          }));
        };

        ws.onmessage = (e) => {
          try {
            const msg = JSON.parse(e.data);
            if (msg.type === 'ping') return;
            const ch: string = msg.channel ?? '';
            if (!ch.startsWith('agent:')) return;
            const event = msg.event ?? {};
            const agentId: string = event.agent_id ?? '';
            if (event.type === 'scan_started') {
              _inFlightScans.add(agentId);
              setScanResults((r) => ({ ...r, [agentId]: 'scanning' }));
            } else if (event.type === 'scan_complete') {
              _inFlightScans.delete(agentId);
              const partial: ScanResult = {
                score: event.score,
                risk: event.risk,
                summary: event.summary,
                findings: event.findings ?? [],
                http_probes: event.http_probes ?? { tls: '', auth_required: '', reachable: false },
                scanned_at: event.scanned_at ?? new Date().toISOString(),
              };
              setScanResults((r) => ({ ...r, [agentId]: partial }));
              reload();
            } else if (event.type === 'scan_failed') {
              _inFlightScans.delete(agentId);
              setScanResults((r) => {
                const n = { ...r };
                delete n[agentId];
                return n;
              });
              alert(`Scan failed: ${event.error ?? 'unknown error'}`);
            }
          } catch { /* ignore parse errors */ }
        };

        ws.onerror = () => { /* silent */ };
      })
      .catch(() => { /* auth fetch failed */ });

    return () => {
      if (dashWsRef.current) {
        dashWsRef.current.close();
        dashWsRef.current = null;
      }
      agentIdsKeyRef.current = '';
    };
  }, [agents]);

  function openCreate() {
    setEditing(null);
    setForm(EMPTY_FORM);
    setError('');
    setDiscoverError('');
    setShowModal(true);
  }

  function openEdit(agent: Agent) {
    setEditing(agent);
    setForm({
      slug: agent.slug,
      display_name: agent.display_name,
      description: agent.description || '',
      transport: agent.transport,
      endpoint_url: agent.endpoint_url,
      auth_token: '',
      max_concurrency: agent.max_concurrency,
      timeout_seconds: agent.timeout_seconds,
      enabled: agent.enabled,
      skills: agent.skills || [],
      supports_streaming: agent.supports_streaming || false,
      supports_push: agent.supports_push || false,
      agent_card: agent.agent_card || null,
      agent_card_url: agent.agent_card_url || '',
    });
    setError('');
    setDiscoverError('');
    setShowModal(true);
  }

  async function handleDiscover() {
    if (!form.endpoint_url.trim()) { setDiscoverError('Enter an endpoint URL first'); return; }
    setDiscovering(true);
    setDiscoverError('');
    try {
      const result = await themApi.discoverAgent({ endpoint_url: form.endpoint_url.trim(), auth_token: form.auth_token || undefined });
      if (!result.ok) { setDiscoverError(result.detail || 'Discovery failed'); return; }
      setForm((f) => ({
        ...f,
        display_name: result.display_name || f.display_name,
        slug: editing ? f.slug : (result.suggested_slug || f.slug),
        description: result.description || f.description,
        skills: result.skills,
        supports_streaming: result.supports_streaming,
        supports_push: result.supports_push,
        agent_card: result.agent_card,
        agent_card_url: result.agent_card_url,
      }));
    } catch (e: unknown) {
      setDiscoverError(e instanceof Error ? e.message : 'Discovery failed');
    } finally {
      setDiscovering(false);
    }
  }

  async function handleSave() {
    setSaving(true);
    setError('');
    try {
      const body: Record<string, unknown> = { ...form };
      if (!body.auth_token) delete body.auth_token;
      if (editing) {
        delete body.slug;
        await themApi.updateAgent(editing.id, body);
      } else {
        await themApi.createAgent(body);
      }
      setShowModal(false);
      reload();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }

  async function handleTest(agent: Agent) {
    setTestResults((r) => ({ ...r, [agent.id]: 'testing' }));
    try {
      const result = await themApi.testAgent(agent.id);
      setTestResults((r) => ({ ...r, [agent.id]: result }));
    } catch (e: unknown) {
      setTestResults((r) => ({ ...r, [agent.id]: { ok: false, latency_ms: 0, detail: e instanceof Error ? e.message : 'Test failed' } }));
    }
  }

  async function handleScan(agent: Agent) {
    _inFlightScans.add(agent.id);
    setScanResults((r) => ({ ...r, [agent.id]: 'scanning' }));
    setScanModal(null);
    try {
      await themApi.scanAgent(agent.id);
    } catch (e: unknown) {
      _inFlightScans.delete(agent.id);
      setScanResults((r) => { const n = { ...r }; delete n[agent.id]; return n; });
      alert(e instanceof Error ? e.message : 'Scan failed');
    }
  }

  async function handleRowDiscover(agent: Agent) {
    setRowDiscoverState((r) => ({ ...r, [agent.id]: 'discovering' }));
    try {
      const result = await themApi.discoverAgent({ endpoint_url: agent.endpoint_url, agent_id: agent.id });
      if (!result.ok) { alert(`Discovery failed: ${result.detail}`); return; }
      const diff = buildDiff(agent, result);
      setDiscoverPopup({ agent, result, diff });
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Discovery failed');
    } finally {
      setRowDiscoverState((r) => { const n = { ...r }; delete n[agent.id]; return n; });
    }
  }

  async function handleApplyDiscover() {
    if (!discoverPopup) return;
    const { agent, result, diff } = discoverPopup;

    const affected = orchestrators.filter(o => o.allowed_agent_ids.includes(agent.id));
    if (affected.length > 0 && diff.hasChanges) {
      const names = affected.map(o => o.display_name || o.name).join(', ');
      const confirmed = window.confirm(
        `This agent is used by ${affected.length} orchestrator${affected.length > 1 ? 's' : ''}: ${names}\n\nTheir tool descriptions will update on the next run. Continue?`
      );
      if (!confirmed) return;
    }

    setApplyingDiscover(true);
    try {
      await themApi.updateAgent(agent.id, {
        display_name: result.display_name || agent.display_name,
        description: result.description || agent.description,
        skills: result.skills,
        supports_streaming: result.supports_streaming,
        supports_push: result.supports_push,
        agent_card: result.agent_card,
        agent_card_url: result.agent_card_url,
      });
      setDiscoverPopup(null);
      reload();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Apply failed');
    } finally {
      setApplyingDiscover(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    try {
      await themApi.deleteAgent(deleteTarget.id);
      setDeleteTarget(null);
      reload();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Delete failed');
    }
  }

  const set = (k: keyof FormState, v: unknown) => setForm((f) => ({ ...f, [k]: v }));

  return (
    <AuthGuard>
      <style>{`
        @keyframes pulse-border {
          0%, 100% { box-shadow: 0 0 0 0 rgba(124,58,237,0.5); }
          50% { box-shadow: 0 0 0 6px rgba(124,58,237,0); }
        }
        .save-pulse { animation: pulse-border 1.4s ease-in-out infinite; }

        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.45; }
        }

        @keyframes scan-shimmer {
          0% { background-position: -200% center; }
          100% { background-position: 200% center; }
        }

        @media (prefers-reduced-motion: reduce) {
          .agent-row, .agent-row * { transition: none !important; }
          .scan-btn-shimmer { animation: none !important; }
        }

        .agent-row {
          transition: background 160ms ease, box-shadow 160ms ease;
        }
        .agent-row:hover {
          background: rgba(255,255,255,.022) !important;
        }

        /* Scan button — embossed shield style */
        .scan-btn {
          position: relative;
          overflow: hidden;
          padding: 5px 11px;
          border-radius: 7px;
          border: 1px solid rgba(40,215,238,.32);
          background: linear-gradient(145deg, rgba(40,215,238,.10) 0%, rgba(24,197,223,.04) 100%), rgba(5,15,28,.60);
          box-shadow: 0 4px 12px rgba(40,215,238,.08), inset 0 1px 0 rgba(255,255,255,.06), inset 0 -1px 0 rgba(0,0,0,.22);
          cursor: pointer;
          font-size: 12px;
          font-weight: 600;
          color: #28d7ee;
          letter-spacing: 0.01em;
          transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease;
        }
        .scan-btn:hover:not(:disabled) {
          border-color: rgba(40,215,238,.52);
          box-shadow: 0 6px 18px rgba(40,215,238,.14), inset 0 1px 0 rgba(255,255,255,.08), inset 0 -1px 0 rgba(0,0,0,.28);
          transform: translateY(-1px);
        }
        .scan-btn:disabled {
          opacity: 0.5;
          cursor: not-allowed;
        }

        /* Discover button */
        .discover-btn {
          padding: 5px 11px;
          border-radius: 7px;
          border: 1px solid rgba(167,139,250,.28);
          background: linear-gradient(145deg, rgba(167,139,250,.08) 0%, rgba(124,58,237,.04) 100%), rgba(5,15,28,.50);
          box-shadow: inset 0 1px 0 rgba(255,255,255,.05), inset 0 -1px 0 rgba(0,0,0,.2);
          cursor: pointer;
          font-size: 12px;
          font-weight: 600;
          color: #a78bfa;
          transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease;
        }
        .discover-btn:hover:not(:disabled) {
          border-color: rgba(167,139,250,.45);
          box-shadow: 0 4px 12px rgba(167,139,250,.1), inset 0 1px 0 rgba(255,255,255,.07), inset 0 -1px 0 rgba(0,0,0,.25);
          transform: translateY(-1px);
        }
        .discover-btn:disabled { opacity: 0.5; cursor: not-allowed; }

        /* Test button */
        .test-btn {
          padding: 5px 11px;
          border-radius: 7px;
          border: 1px solid rgba(96,165,250,.28);
          background: linear-gradient(145deg, rgba(96,165,250,.08) 0%, rgba(59,130,246,.04) 100%), rgba(5,15,28,.50);
          box-shadow: inset 0 1px 0 rgba(255,255,255,.05), inset 0 -1px 0 rgba(0,0,0,.2);
          cursor: pointer;
          font-size: 12px;
          font-weight: 600;
          color: #60a5fa;
          transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease;
        }
        .test-btn:hover:not(:disabled) {
          border-color: rgba(96,165,250,.44);
          box-shadow: 0 4px 12px rgba(96,165,250,.1), inset 0 1px 0 rgba(255,255,255,.07), inset 0 -1px 0 rgba(0,0,0,.25);
          transform: translateY(-1px);
        }
        .test-btn:disabled { opacity: 0.6; cursor: not-allowed; }

        /* Edit / neutral button */
        .ghost-btn {
          padding: 5px 11px;
          border-radius: 7px;
          border: 1px solid rgba(132,158,190,.18);
          background: linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.40);
          box-shadow: inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.18);
          cursor: pointer;
          font-size: 12px;
          color: var(--tm-text-muted);
          transition: border-color 160ms ease, color 160ms ease;
        }
        .ghost-btn:hover { border-color: rgba(132,158,190,.32); color: var(--tm-text); }

        /* Delete button */
        .delete-btn {
          padding: 5px 11px;
          border-radius: 7px;
          border: 1px solid rgba(220,38,38,.22);
          background: linear-gradient(145deg, rgba(220,38,38,.06) 0%, rgba(185,28,28,.02) 100%), rgba(5,15,28,.40);
          box-shadow: inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.18);
          cursor: pointer;
          font-size: 12px;
          color: #f87171;
          transition: border-color 160ms ease, box-shadow 160ms ease;
        }
        .delete-btn:hover {
          border-color: rgba(220,38,38,.42);
          box-shadow: 0 3px 10px rgba(220,38,38,.1), inset 0 1px 0 rgba(255,255,255,.03), inset 0 -1px 0 rgba(0,0,0,.2);
        }

        /* Score badge — clickable pill */
        .score-badge {
          display: inline-flex;
          align-items: center;
          gap: 5px;
          padding: 3px 9px;
          border-radius: 5px;
          font-size: 10px;
          font-weight: 700;
          letter-spacing: 0.04em;
          cursor: pointer;
          border: 1px solid;
          transition: filter 160ms ease, transform 160ms ease;
          box-shadow: inset 0 1px 0 rgba(255,255,255,.04), inset 0 -1px 0 rgba(0,0,0,.18);
        }
        .score-badge:hover { filter: brightness(1.15); transform: translateY(-1px); }

        /* Scanning pill */
        .scanning-pill {
          display: inline-flex;
          align-items: center;
          gap: 5px;
          padding: 3px 9px;
          border-radius: 5px;
          font-size: 10px;
          font-weight: 700;
          letter-spacing: 0.04em;
          background: linear-gradient(145deg, rgba(167,139,250,.14), rgba(124,58,237,.06));
          border: 1px solid rgba(167,139,250,.28);
          color: #c4b5fd;
          box-shadow: inset 0 1px 0 rgba(255,255,255,.04);
          animation: pulse 1.6s ease-in-out infinite;
        }

        /* Score ring in modal */
        .score-ring {
          position: relative;
          width: 80px;
          height: 80px;
          border-radius: 50%;
          display: flex;
          flex-direction: column;
          align-items: center;
          justify-content: center;
          flex-shrink: 0;
          box-shadow: 0 6px 20px rgba(0,0,0,.3), inset 0 1px 0 rgba(255,255,255,.06), inset 0 -1px 0 rgba(0,0,0,.25);
        }

        /* Probe row */
        .probe-row {
          display: flex;
          align-items: center;
          justify-content: space-between;
          padding: 7px 12px;
          border-radius: 8px;
          margin-bottom: 6px;
        }

        /* Finding card */
        .finding-card {
          padding: 11px 13px;
          border-radius: 10px;
          margin-bottom: 7px;
          transition: border-color 160ms ease;
        }
        .finding-card:hover { border-color: rgba(132,158,190,.22) !important; }

        /* Primary modal button — scan / rescan */
        .btn-primary-scan {
          padding: 9px 22px;
          border-radius: 9px;
          border: 1px solid rgba(40,215,238,.48);
          background: linear-gradient(180deg, rgba(40,215,238,.22) 0%, rgba(24,197,223,.12) 100%), rgba(5,15,28,.80);
          box-shadow: 0 6px 18px rgba(40,215,238,.12), inset 0 1px 0 rgba(255,255,255,.10), inset 0 -1px 0 rgba(0,0,0,.24);
          color: #28d7ee;
          font-size: 14px;
          font-weight: 700;
          cursor: pointer;
          letter-spacing: 0.01em;
          transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease;
          display: flex;
          align-items: center;
          gap: 7px;
        }
        .btn-primary-scan:hover:not(:disabled) {
          border-color: rgba(40,215,238,.70);
          box-shadow: 0 8px 24px rgba(40,215,238,.18), inset 0 1px 0 rgba(255,255,255,.14), inset 0 -1px 0 rgba(0,0,0,.28);
          transform: translateY(-1px);
        }
        .btn-primary-scan:disabled { opacity: 0.5; cursor: not-allowed; }
      `}</style>

      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1 }}>
          <header style={{
            position: 'sticky', top: 0, zIndex: 30, height: '56px',
            background: 'var(--tm-topbar)', borderBottom: '1px solid var(--tm-topbar-border)',
            display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 32px',
          }}>
            <div>
              <h2 style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-accent)' }}>Agents</h2>
              <p style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>Manage A2A agent connectors</p>
            </div>
            <button onClick={openCreate} style={{
              display: 'flex', alignItems: 'center', gap: '6px',
              padding: '8px 16px', borderRadius: '8px', border: 'none', cursor: 'pointer',
              background: 'var(--tm-accent)', color: '#fff', fontSize: '13px', fontWeight: 600,
            }}>
              <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>add</span>
              New Agent
            </button>
          </header>

          <div style={{ padding: '32px' }}>
            {/* Embossed table container */}
            <div style={{
              position: 'relative',
              background: 'linear-gradient(145deg, rgba(255,255,255,.022) 0%, rgba(255,255,255,.006) 36%, rgba(0,0,0,.04) 100%), rgba(10,21,36,.96)',
              border: '1px solid rgba(132,158,190,.16)',
              borderRadius: '16px',
              overflow: 'hidden',
              boxShadow: '0 14px 34px rgba(0,0,0,.28), 0 3px 10px rgba(0,0,0,.16), inset 0 1px 0 rgba(255,255,255,.045), inset 0 -1px 0 rgba(0,0,0,.28)',
            }}>
              {/* Inner inset ring */}
              <div style={{
                position: 'absolute', inset: '1px', borderRadius: '15px', pointerEvents: 'none', zIndex: 0,
                boxShadow: 'inset 0 0 0 1px rgba(255,255,255,.014)',
              }} />
              {/* Top accent */}
              <div style={{
                position: 'absolute', top: 0, left: '20px', right: '20px', height: '1px', pointerEvents: 'none', zIndex: 1,
                background: 'linear-gradient(90deg, transparent, rgba(40,215,238,.28), transparent)',
              }} />
              <table style={{ width: '100%', borderCollapse: 'collapse', position: 'relative', zIndex: 1 }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid rgba(132,158,190,.12)' }}>
                    {['Name', 'Slug', 'Skills', 'Endpoint', 'Synced', 'Status', ''].map((h) => (
                      <th key={h} style={{
                        padding: '11px 16px', textAlign: 'left',
                        fontSize: '10px', fontWeight: 700, color: 'var(--tm-text-muted)',
                        textTransform: 'uppercase', letterSpacing: '0.07em',
                        background: 'rgba(5,15,28,.40)',
                        borderBottom: '1px solid rgba(132,158,190,.10)',
                      }}>{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {loading && (
                    <tr><td colSpan={7} style={{ padding: '40px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>Loading…</td></tr>
                  )}
                  {!loading && agents.length === 0 && (
                    <tr><td colSpan={7} style={{ padding: '40px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>No agents yet — click New Agent to add one</td></tr>
                  )}
                  {agents.map((agent, i) => (
                    <tr key={agent.id}
                      className="agent-row"
                      style={{
                        borderBottom: i < agents.length - 1 ? '1px solid rgba(132,158,190,.08)' : 'none',
                        background: 'transparent',
                      }}
                    >
                      {/* Name */}
                      <td style={{ padding: '13px 16px' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                          {/* Icon tile — nested surface */}
                          <div style={{
                            width: '34px', height: '34px', borderRadius: '9px', flexShrink: 0,
                            background: 'radial-gradient(circle at 30% 20%, rgba(40,215,238,.15), transparent 60%), linear-gradient(145deg, rgba(27,47,68,.96), rgba(8,20,35,.96))',
                            border: '1px solid rgba(40,215,238,.30)',
                            boxShadow: '0 5px 14px rgba(0,0,0,.24), 0 0 10px rgba(40,215,238,.08), inset 0 1px 0 rgba(255,255,255,.06), inset 0 -1px 0 rgba(0,0,0,.22)',
                            color: '#28d7ee',
                            display: 'flex', alignItems: 'center', justifyContent: 'center',
                          }}>
                            <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>smart_toy</span>
                          </div>
                          <div>
                            <div style={{ fontSize: '14px', fontWeight: 600, color: 'var(--tm-text)' }}>{agent.display_name}</div>
                            {agent.description && (
                              <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '1px' }}>
                                {agent.description.split('\n')[0].slice(0, 60)}{agent.description.split('\n')[0].length > 60 ? '…' : ''}
                              </div>
                            )}
                          </div>
                        </div>
                      </td>

                      {/* Slug */}
                      <td style={{ padding: '13px 16px' }}>
                        <code style={{
                          fontSize: '11px', color: 'var(--tm-text-muted)',
                          background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.50)',
                          border: '1px solid rgba(132,157,188,.12)',
                          boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025)',
                          padding: '2px 7px', borderRadius: '5px',
                        }}>
                          {agent.slug}
                        </code>
                      </td>

                      {/* Skills */}
                      <td style={{ padding: '13px 16px' }}>
                        {agent.skills && agent.skills.length > 0
                          ? <span style={{
                              fontSize: '11px', color: 'var(--tm-text-muted)',
                              background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.40)',
                              border: '1px solid rgba(132,157,188,.10)',
                              padding: '2px 7px', borderRadius: '5px',
                            }} title={agent.skills.map((s) => s.name).join(', ')}>
                              {agent.skills.length} skill{agent.skills.length !== 1 ? 's' : ''}
                            </span>
                          : <span style={{ fontSize: '11px', color: 'var(--tm-text-subtle)' }}>—</span>
                        }
                      </td>

                      {/* Endpoint */}
                      <td style={{ padding: '13px 16px', maxWidth: '180px' }}>
                        <span style={{
                          fontSize: '11px', color: 'var(--tm-text-muted)',
                          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'block',
                          fontFamily: 'monospace',
                        }}>
                          {agent.endpoint_url}
                        </span>
                      </td>

                      {/* Synced */}
                      <td style={{ padding: '13px 16px' }}>
                        <span style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>
                          {agent.card_fetched_at ? timeAgo(agent.card_fetched_at) : '—'}
                        </span>
                      </td>

                      {/* Status + score */}
                      <td style={{ padding: '13px 16px' }}>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', alignItems: 'flex-start' }}>
                          {/* Enabled badge — nested surface pill */}
                          <span style={{
                            fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '5px', letterSpacing: '0.04em',
                            background: agent.enabled
                              ? 'linear-gradient(145deg, rgba(66,217,139,.14), rgba(42,181,109,.06))'
                              : 'linear-gradient(145deg, rgba(107,114,128,.10), rgba(75,80,90,.04))',
                            border: `1px solid ${agent.enabled ? 'rgba(66,217,139,.24)' : 'rgba(107,114,128,.18)'}`,
                            boxShadow: 'inset 0 1px 0 rgba(255,255,255,.03)',
                            color: agent.enabled ? '#4edea3' : '#6b7280',
                          }}>
                            {agent.enabled ? 'Enabled' : 'Disabled'}
                          </span>

                          {/* Security score badge */}
                          {(() => {
                            const sr = scanResults[agent.id];
                            if (!sr) return null;
                            if (sr === 'scanning') return (
                              <span className="scanning-pill">
                                <span style={{ fontSize: '11px' }}>🛡</span> Scanning…
                              </span>
                            );
                            const rc = riskColors(sr.risk);
                            return (
                              <button
                                className="score-badge"
                                onClick={() => setScanModal({ agent, result: sr })}
                                style={{
                                  background: rc.bg,
                                  borderColor: rc.border,
                                  color: rc.color,
                                }}
                                title="Click to view security report"
                              >
                                <span style={{ fontSize: '11px' }}>🛡</span>
                                {sr.score} · {sr.risk}
                              </button>
                            );
                          })()}
                        </div>
                      </td>

                      {/* Actions */}
                      <td style={{ padding: '13px 16px' }}>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '7px', alignItems: 'flex-end' }}>
                          <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap', justifyContent: 'flex-end' }}>
                            <button
                              className="discover-btn"
                              onClick={() => handleRowDiscover(agent)}
                              disabled={!!rowDiscoverState[agent.id]}
                            >
                              {rowDiscoverState[agent.id] ? 'Discovering…' : 'Discover'}
                            </button>
                            <button
                              className="scan-btn"
                              onClick={() => handleScan(agent)}
                              disabled={scanResults[agent.id] === 'scanning'}
                            >
                              <span style={{ fontSize: '11px' }}>🛡</span>
                              {scanResults[agent.id] === 'scanning' ? 'Scanning…' : 'Scan'}
                            </button>
                            <button
                              className="test-btn"
                              onClick={() => handleTest(agent)}
                              disabled={testResults[agent.id] === 'testing'}
                            >
                              {testResults[agent.id] === 'testing' ? 'Testing…' : 'Test'}
                            </button>
                            <button className="ghost-btn" onClick={() => openEdit(agent)}>Edit</button>
                            <button className="delete-btn" onClick={() => setDeleteTarget(agent)}>Delete</button>
                          </div>

                          {/* Test result inline */}
                          {testResults[agent.id] && testResults[agent.id] !== 'testing' && (() => {
                            const r = testResults[agent.id] as { ok: boolean; latency_ms: number; detail: string };
                            return (
                              <div style={{
                                fontSize: '11px', padding: '3px 9px', borderRadius: '5px', maxWidth: '280px', textAlign: 'right',
                                background: r.ok
                                  ? 'linear-gradient(145deg, rgba(66,217,139,.10), rgba(42,181,109,.04))'
                                  : 'linear-gradient(145deg, rgba(220,38,38,.10), rgba(185,28,28,.04))',
                                border: `1px solid ${r.ok ? 'rgba(66,217,139,.20)' : 'rgba(220,38,38,.20)'}`,
                                boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025)',
                                color: r.ok ? '#4edea3' : '#f87171',
                              }}>
                                {r.ok ? `✓ ${r.latency_ms}ms — ` : '✗ '}{r.detail}
                              </div>
                            );
                          })()}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </main>

        {/* ── Create / Edit Modal ── */}
        {showModal && (
          <Modal title={editing ? `Edit — ${editing.display_name}` : 'New Agent'} onClose={() => setShowModal(false)}>
            <Field label="Endpoint URL">
              <div style={{ display: 'flex', gap: '8px' }}>
                <input style={{ ...inputStyle, flex: 1 }} value={form.endpoint_url} onChange={(e) => set('endpoint_url', e.target.value)} placeholder="http://host:port" />
                <button onClick={handleDiscover} disabled={discovering} className="discover-btn" style={{ whiteSpace: 'nowrap', flexShrink: 0, padding: '8px 14px', fontSize: '13px' }}>
                  {discovering ? 'Discovering…' : 'Discover'}
                </button>
              </div>
              {discoverError && <div style={{ fontSize: '11px', color: '#f87171', marginTop: '4px' }}>{discoverError}</div>}
              {form.agent_card_url && !discoverError && (
                <div style={{ fontSize: '11px', color: '#4edea3', marginTop: '4px' }}>
                  Card fetched — {form.skills.length} skill{form.skills.length !== 1 ? 's' : ''} discovered
                  {form.supports_streaming && ' · streaming'}{form.supports_push && ' · push'}
                </div>
              )}
            </Field>
            <Field label="Display Name">
              <input style={inputStyle} value={form.display_name} onChange={(e) => set('display_name', e.target.value)} placeholder="Echo Agent" />
            </Field>
            {!editing && (
              <Field label="Slug">
                <input style={inputStyle} value={form.slug} onChange={(e) => set('slug', e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, '_'))} placeholder="echo_agent" />
                <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '4px' }}>lowercase letters, numbers, underscores only</div>
              </Field>
            )}
            <Field label="Description (shown to LLM)">
              <textarea style={{ ...inputStyle, minHeight: '72px', resize: 'vertical', fontFamily: 'inherit' }} value={form.description} onChange={(e) => set('description', e.target.value)} placeholder="Echoes back any message it receives" />
            </Field>
            {form.skills.length > 0 && (
              <Field label={`Skills (${form.skills.length})`}>
                <div style={{ ...nestedSurface, borderRadius: '8px', padding: '8px 12px', maxHeight: '120px', overflowY: 'auto' }}>
                  {form.skills.map((s, i) => (
                    <div key={i} style={{ fontSize: '12px', color: 'var(--tm-text)', marginBottom: i < form.skills.length - 1 ? '6px' : 0 }}>
                      <span style={{ fontWeight: 600 }}>{s.name}</span>
                      {s.description && <span style={{ color: 'var(--tm-text-muted)' }}> — {s.description}</span>}
                    </div>
                  ))}
                </div>
              </Field>
            )}
            <Field label={editing ? 'Auth Token (leave blank to keep existing)' : 'Auth Token (optional)'}>
              <input style={inputStyle} type="password" value={form.auth_token} onChange={(e) => set('auth_token', e.target.value)} placeholder="Bearer token for the agent endpoint" />
            </Field>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px' }}>
              <Field label="Max Concurrency">
                <input style={inputStyle} type="number" min={1} max={20} value={form.max_concurrency} onChange={(e) => set('max_concurrency', Number(e.target.value))} />
              </Field>
              <Field label="Timeout (seconds)">
                <input style={inputStyle} type="number" min={5} max={300} value={form.timeout_seconds} onChange={(e) => set('timeout_seconds', Number(e.target.value))} />
              </Field>
            </div>
            <Field label="Status">
              <label style={{ display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer' }}>
                <input type="checkbox" checked={form.enabled} onChange={(e) => set('enabled', e.target.checked)} />
                <span style={{ fontSize: '14px', color: 'var(--tm-text)' }}>Enabled</span>
              </label>
            </Field>
            {error && <div style={{ padding: '10px 12px', borderRadius: '8px', background: 'rgba(220,38,38,.08)', border: '1px solid rgba(220,38,38,.2)', color: '#f87171', fontSize: '13px', marginBottom: '16px' }}>{error}</div>}
            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '8px' }}>
              <button className="ghost-btn" onClick={() => setShowModal(false)} style={{ padding: '8px 20px', fontSize: '14px' }}>Cancel</button>
              <button onClick={handleSave} disabled={saving} style={{
                padding: '8px 22px', borderRadius: '9px', border: 'none',
                background: saving ? 'rgba(99,102,241,.5)' : 'var(--tm-accent)',
                color: '#fff', cursor: saving ? 'not-allowed' : 'pointer', fontSize: '14px', fontWeight: 600, opacity: saving ? 0.7 : 1,
              }}>{saving ? 'Saving…' : (editing ? 'Save Changes' : 'Create Agent')}</button>
            </div>
          </Modal>
        )}

        {/* ── Discover popup ── */}
        {discoverPopup && (() => {
          const { agent, result, diff } = discoverPopup;
          const newCard = (result.agent_card ?? {}) as Record<string, unknown>;
          const version = cardVersion(result.agent_card);
          const provider = cardProvider(result.agent_card);
          const docUrl = cardDocUrl(result.agent_card);
          const authSchemes = cardAuth(result.agent_card);
          const affectedOrchestrators = orchestrators.filter(o => o.allowed_agent_ids.includes(agent.id));

          return (
            <Modal wide title={`Agent Card — ${result.display_name || agent.display_name}`} onClose={() => setDiscoverPopup(null)}>
              {/* Status banner */}
              <div style={{
                padding: '10px 14px', borderRadius: '9px', marginBottom: '20px',
                background: diff.hasChanges
                  ? 'linear-gradient(145deg, rgba(230,184,92,.10), rgba(180,131,9,.04))'
                  : 'linear-gradient(145deg, rgba(66,217,139,.10), rgba(42,181,109,.04))',
                border: `1px solid ${diff.hasChanges ? 'rgba(230,184,92,.28)' : 'rgba(66,217,139,.28)'}`,
                boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025)',
                display: 'flex', alignItems: 'center', gap: '8px',
              }}>
                <span style={{ fontSize: '16px' }}>{diff.hasChanges ? '⚠️' : '✓'}</span>
                <span style={{ fontSize: '13px', fontWeight: 600, color: diff.hasChanges ? '#e6b85c' : '#4edea3' }}>
                  {diff.hasChanges ? 'Changes detected — review and save to update this agent' : 'Up to date — no changes since last sync'}
                </span>
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>
                <div>
                  <SectionLabel>Agent Info</SectionLabel>
                  <div style={{ ...nestedSurface, borderRadius: '8px', overflow: 'hidden', marginBottom: '16px' }}>
                    <DiffRow label="Name" changed={diff.displayName.changed} oldVal={diff.displayName.old} newVal={diff.displayName.new} />
                    {version && <DiffRow label="Version" changed={diff.version.changed} oldVal={diff.version.old} newVal={version} />}
                    {provider && <DiffRow label="Provider" changed={diff.provider.changed} oldVal={diff.provider.old} newVal={provider} />}
                    {docUrl && (
                      <div style={{ display: 'flex', gap: '8px', alignItems: 'center', padding: '6px 0 6px 0', borderBottom: '1px solid var(--tm-border)' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px' }}>Docs</span>
                        <a href={docUrl} target="_blank" rel="noreferrer" style={{ fontSize: '12px', color: '#60a5fa' }}>{docUrl}</a>
                      </div>
                    )}
                    <DiffRow label="Streaming" changed={diff.streaming.changed} oldVal={diff.streaming.old ? 'yes' : 'no'} newVal={diff.streaming.new ? 'yes' : 'no'} />
                    <DiffRow label="Push" changed={diff.push.changed} oldVal={diff.push.old ? 'yes' : 'no'} newVal={diff.push.new ? 'yes' : 'no'} />
                    {authSchemes.length > 0 && (
                      <div style={{ display: 'flex', gap: '8px', alignItems: 'center', padding: '6px 0' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px' }}>Auth</span>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text)' }}>{authSchemes.join(', ')}</span>
                      </div>
                    )}
                  </div>

                  <SectionLabel>Card URL</SectionLabel>
                  <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', fontFamily: 'monospace', wordBreak: 'break-all', marginBottom: '16px' }}>
                    {result.agent_card_url}
                  </div>

                  {affectedOrchestrators.length > 0 && (
                    <>
                      <SectionLabel>Used by orchestrators</SectionLabel>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                        {affectedOrchestrators.map(o => (
                          <div key={o.id} style={{ fontSize: '12px', padding: '4px 8px', borderRadius: '6px', background: 'rgba(230,184,92,.08)', color: '#e6b85c', border: '1px solid rgba(230,184,92,.2)' }}>
                            {o.display_name || o.name}
                          </div>
                        ))}
                      </div>
                    </>
                  )}
                </div>

                <div>
                  <SectionLabel>Description</SectionLabel>
                  <div style={{
                    ...nestedSurface,
                    padding: '10px 12px', borderRadius: '8px', marginBottom: '16px',
                    border: `1px solid ${diff.description.changed ? 'rgba(66,217,139,.28)' : 'rgba(132,157,188,.12)'}`,
                  }}>
                    {diff.description.changed && (
                      <div style={{ fontSize: '11px', color: '#94a3b8', textDecoration: 'line-through', marginBottom: '6px', whiteSpace: 'pre-wrap' }}>{diff.description.old || '—'}</div>
                    )}
                    <div style={{ fontSize: '12px', color: diff.description.changed ? '#4edea3' : 'var(--tm-text)', whiteSpace: 'pre-wrap', lineHeight: 1.5 }}>
                      {result.description || '—'}
                    </div>
                  </div>

                  <SectionLabel>Skills ({result.skills.length}){diff.skills.changed && <span style={{ color: '#e6b85c', marginLeft: '6px', textTransform: 'none', fontSize: '10px' }}>changed</span>}</SectionLabel>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', maxHeight: '280px', overflowY: 'auto' }}>
                    {result.skills.length === 0 && <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>No skills declared</div>}
                    {result.skills.map((s, i) => {
                      const skillCard = ((newCard.skills ?? []) as Record<string, unknown>[])[i] ?? {};
                      const inputModes = Array.isArray(skillCard.inputModes) ? (skillCard.inputModes as string[]) : [];
                      const outputModes = Array.isArray(skillCard.outputModes) ? (skillCard.outputModes as string[]) : [];
                      return (
                        <div key={i} style={{ ...nestedSurface, padding: '8px 10px', borderRadius: '8px' }}>
                          <div style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-text)', marginBottom: '2px' }}>{s.name}</div>
                          {s.description && <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', lineHeight: 1.4, marginBottom: '4px' }}>{s.description}</div>}
                          <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
                            {(s.tags ?? []).map((t, ti) => (
                              <span key={ti} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(167,139,250,.12)', color: '#a78bfa', border: '1px solid rgba(167,139,250,.18)' }}>{t}</span>
                            ))}
                            {inputModes.map((m, mi) => (
                              <span key={`in-${mi}`} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(96,165,250,.10)', color: '#60a5fa', border: '1px solid rgba(96,165,250,.18)' }}>in:{m}</span>
                            ))}
                            {outputModes.map((m, mi) => (
                              <span key={`out-${mi}`} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(78,222,163,.10)', color: '#4edea3', border: '1px solid rgba(78,222,163,.18)' }}>out:{m}</span>
                            ))}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              </div>

              <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '24px', paddingTop: '16px', borderTop: '1px solid rgba(132,158,190,.12)' }}>
                <button className="ghost-btn" onClick={() => setDiscoverPopup(null)} style={{ padding: '8px 20px', fontSize: '14px' }}>Close</button>
                {diff.hasChanges && (
                  <button
                    onClick={handleApplyDiscover}
                    disabled={applyingDiscover}
                    className="save-pulse"
                    style={{
                      padding: '8px 24px', borderRadius: '9px',
                      border: '1px solid rgba(167,139,250,.48)',
                      background: 'linear-gradient(145deg, rgba(167,139,250,.18), rgba(124,58,237,.10)), rgba(5,15,28,.80)',
                      boxShadow: '0 6px 18px rgba(124,58,237,.14), inset 0 1px 0 rgba(255,255,255,.08)',
                      color: '#c4b5fd', cursor: applyingDiscover ? 'not-allowed' : 'pointer',
                      fontSize: '14px', fontWeight: 700, opacity: applyingDiscover ? 0.7 : 1,
                    }}
                  >
                    {applyingDiscover ? 'Saving…' : 'Save Changes'}
                  </button>
                )}
              </div>
            </Modal>
          );
        })()}

        {/* ── Security scan detail modal ── */}
        {scanModal && (() => {
          const { agent, result } = scanModal;
          const rc = riskColors(result.risk);
          const ringColor = scoreRingColor(result.score);
          return (
            <Modal wide title="" onClose={() => setScanModal(null)}>
              {/* Modal header with shield icon + agent name */}
              <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '24px', marginTop: '-8px' }}>
                <div style={{
                  width: '40px', height: '40px', borderRadius: '11px', flexShrink: 0,
                  background: 'radial-gradient(circle at 30% 20%, rgba(40,215,238,.20), transparent 60%), linear-gradient(145deg, rgba(27,47,68,.96), rgba(8,20,35,.96))',
                  border: '1px solid rgba(40,215,238,.35)',
                  boxShadow: '0 5px 14px rgba(0,0,0,.28), 0 0 12px rgba(40,215,238,.10), inset 0 1px 0 rgba(255,255,255,.07)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  fontSize: '20px',
                }}>🛡</div>
                <div>
                  <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: '2px' }}>Security Report</div>
                  <div style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-text)', letterSpacing: '-0.01em' }}>{agent.display_name}</div>
                </div>
              </div>

              {/* Score + risk header */}
              <div style={{
                display: 'flex', alignItems: 'center', gap: '16px', marginBottom: '24px',
                padding: '16px 18px', borderRadius: '12px',
                background: `linear-gradient(145deg, rgba(255,255,255,.020) 0%, rgba(255,255,255,.006) 36%, rgba(0,0,0,.04) 100%), rgba(8,18,32,.80)`,
                border: `1px solid ${rc.border}`,
                boxShadow: `0 6px 18px rgba(0,0,0,.22), 0 0 24px ${rc.glow}, inset 0 1px 0 rgba(255,255,255,.04)`,
              }}>
                {/* Score ring */}
                <div className="score-ring" style={{
                  background: `radial-gradient(circle at 40% 30%, ${ringColor}22, transparent 60%), linear-gradient(145deg, rgba(20,38,60,.96), rgba(8,18,32,.96))`,
                  border: `2px solid ${ringColor}55`,
                }}>
                  <span style={{ fontSize: '24px', fontWeight: 800, color: ringColor, lineHeight: 1 }}>{result.score}</span>
                  <span style={{ fontSize: '9px', fontWeight: 700, color: `${ringColor}99`, textTransform: 'uppercase', letterSpacing: '0.04em' }}>/100</span>
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' }}>
                    <span style={{
                      fontSize: '10px', fontWeight: 700, padding: '3px 10px', borderRadius: '5px', letterSpacing: '0.06em',
                      textTransform: 'uppercase',
                      background: rc.bg,
                      border: `1px solid ${rc.border}`,
                      boxShadow: 'inset 0 1px 0 rgba(255,255,255,.04)',
                      color: rc.color,
                    }}>
                      {result.risk} risk
                    </span>
                  </div>
                  <p style={{ fontSize: '14px', color: 'var(--tm-text)', lineHeight: 1.55, fontWeight: 500, margin: 0 }}>
                    {result.summary}
                  </p>
                </div>
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>
                {/* Left: findings */}
                <div>
                  <SectionLabel>Findings</SectionLabel>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '0' }}>
                    {result.findings.map((f, i) => {
                      const si = statusIcon(f.status);
                      const frc = riskColors(f.risk);
                      return (
                        <div
                          key={i}
                          className="finding-card"
                          style={{
                            ...nestedSurface,
                            borderRadius: '10px',
                            padding: '11px 13px',
                            marginBottom: '7px',
                          }}
                        >
                          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
                            <span style={{
                              width: '22px', height: '22px', borderRadius: '6px', flexShrink: 0,
                              background: `${si.color}18`,
                              border: `1px solid ${si.color}30`,
                              display: 'flex', alignItems: 'center', justifyContent: 'center',
                              fontSize: '12px', color: si.color, fontWeight: 700,
                            }}>{si.icon}</span>
                            <span style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-text)', flex: 1 }}>{f.label}</span>
                            <span style={{
                              fontSize: '9px', fontWeight: 700, padding: '2px 7px', borderRadius: '4px',
                              background: frc.bg, border: `1px solid ${frc.border}`, color: frc.color,
                              textTransform: 'uppercase', letterSpacing: '0.05em',
                            }}>{f.risk}</span>
                          </div>
                          <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', margin: '0 0 4px 30px', lineHeight: 1.5 }}>{f.detail}</p>
                          {f.recommendation !== 'No action needed.' && (
                            <p style={{ fontSize: '11px', color: '#60a5fa', margin: '0 0 0 30px', lineHeight: 1.4 }}>
                              <span style={{ opacity: 0.6 }}>→</span> {f.recommendation}
                            </p>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>

                {/* Right: HTTP probes + meta */}
                <div>
                  <SectionLabel>HTTP Probes</SectionLabel>
                  <div style={{ marginBottom: '20px' }}>
                    {[
                      { label: 'TLS', value: result.http_probes.tls },
                      { label: 'Auth Required', value: result.http_probes.auth_required },
                      { label: 'Reachable', value: result.http_probes.reachable ? 'pass' : 'fail' },
                    ].map(({ label, value }) => {
                      const pass = value === 'pass';
                      return (
                        <div key={label} className="probe-row" style={{
                          ...nestedSurface,
                          borderRadius: '8px',
                          marginBottom: '6px',
                        }}>
                          <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>{label}</span>
                          <span style={{
                            fontSize: '10px', fontWeight: 700, padding: '2px 9px', borderRadius: '4px', letterSpacing: '0.04em',
                            background: pass
                              ? 'linear-gradient(145deg, rgba(66,217,139,.14), rgba(42,181,109,.06))'
                              : 'linear-gradient(145deg, rgba(220,38,38,.14), rgba(185,28,28,.06))',
                            border: `1px solid ${pass ? 'rgba(66,217,139,.24)' : 'rgba(220,38,38,.24)'}`,
                            boxShadow: 'inset 0 1px 0 rgba(255,255,255,.03)',
                            color: pass ? '#4edea3' : '#f87171',
                          }}>
                            {pass ? '✓ pass' : '✗ fail'}
                          </span>
                        </div>
                      );
                    })}
                  </div>

                  <SectionLabel>Scanned</SectionLabel>
                  <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', marginBottom: '20px', lineHeight: 1.5 }}>
                    {timeAgo(result.scanned_at)}<br />
                    <span style={{ fontSize: '11px', opacity: 0.7 }}>{new Date(result.scanned_at).toLocaleString()}</span>
                  </p>

                  <SectionLabel>Agent</SectionLabel>
                  <div style={{ ...nestedSurface, borderRadius: '8px', padding: '8px 12px' }}>
                    <div style={{ fontSize: '12px', color: 'var(--tm-text)', fontWeight: 600, marginBottom: '3px' }}>
                      <code style={{ background: 'rgba(40,215,238,.08)', border: '1px solid rgba(40,215,238,.15)', padding: '1px 6px', borderRadius: '4px', color: '#28d7ee', fontSize: '11px' }}>{agent.slug}</code>
                    </div>
                    <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', wordBreak: 'break-all', lineHeight: 1.4 }}>{agent.endpoint_url}</div>
                  </div>
                </div>
              </div>

              {/* Actions */}
              <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '24px', paddingTop: '16px', borderTop: '1px solid rgba(132,158,190,.10)' }}>
                <button className="ghost-btn" onClick={() => setScanModal(null)} style={{ padding: '8px 20px', fontSize: '14px' }}>Close</button>
                <button
                  className="btn-primary-scan"
                  onClick={() => handleScan(agent)}
                  disabled={scanResults[agent.id] === 'scanning'}
                >
                  <span style={{ fontSize: '15px' }}>🛡</span>
                  {scanResults[agent.id] === 'scanning' ? 'Scanning…' : 'Re-scan'}
                </button>
              </div>
            </Modal>
          );
        })()}

        {/* ── Delete confirm ── */}
        {deleteTarget && (
          <Modal title="Delete Agent" onClose={() => setDeleteTarget(null)}>
            <p style={{ color: 'var(--tm-text)', marginBottom: '24px', lineHeight: 1.6 }}>
              Delete <strong style={{ color: '#f87171' }}>{deleteTarget.display_name}</strong>? This cannot be undone and will remove it from any orchestrators that use it.
            </p>
            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
              <button className="ghost-btn" onClick={() => setDeleteTarget(null)} style={{ padding: '8px 20px', fontSize: '14px' }}>Cancel</button>
              <button className="delete-btn" onClick={handleDelete} style={{ padding: '8px 20px', fontSize: '14px', fontWeight: 700 }}>Delete</button>
            </div>
          </Modal>
        )}
      </div>
    </AuthGuard>
  );
}
