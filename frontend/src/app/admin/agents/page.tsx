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

// ── Sub-components ─────────────────────────────────────────────────────────────

function Modal({ title, onClose, wide, children }: { title: string; onClose: () => void; wide?: boolean; children: React.ReactNode }) {
  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 100,
      background: 'rgba(0,0,0,0.55)', display: 'flex', alignItems: 'center', justifyContent: 'center',
    }} onClick={onClose}>
      <div style={{
        background: 'var(--tm-surface)', border: '1px solid var(--tm-border)',
        borderRadius: '16px', padding: '32px', width: wide ? '720px' : '560px', maxHeight: '90vh',
        overflowY: 'auto',
      }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <h3 style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-text)' }}>{title}</h3>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tm-text-muted)', fontSize: '20px' }}>✕</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '16px' }}>
      <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, color: 'var(--tm-text-muted)', marginBottom: '6px', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
        {label}
      </label>
      {children}
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '8px' }}>
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

function riskColors(risk: 'low' | 'medium' | 'high') {
  if (risk === 'low') return { bg: 'rgba(0,118,80,.12)', color: '#005b3d' };
  if (risk === 'medium') return { bg: 'rgba(251,191,36,.14)', color: '#b45309' };
  return { bg: 'rgba(220,38,38,.12)', color: '#dc2626' };
}

function statusIcon(status: 'pass' | 'fail' | 'warn') {
  if (status === 'pass') return { icon: '✓', color: '#005b3d' };
  if (status === 'warn') return { icon: '⚠', color: '#b45309' };
  return { icon: '✗', color: '#dc2626' };
}

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: '8px',
  border: '1px solid var(--tm-border)', background: 'var(--tm-surface-2)',
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
              // Scan was in-flight when we left — check if it completed while away
              if (agent.last_scan_result) {
                next[agent.id] = agent.last_scan_result;
                _inFlightScans.delete(agent.id);
              } else {
                // Still running — restore the scanning badge
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

    // Close existing connection before opening a new one
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

        ws.onerror = () => { /* silent — scan still works, just no live update */ };
      })
      .catch(() => { /* auth fetch failed — scan still works */ });

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
      // Result arrives via WS — no further action needed here
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

    // Warn if agent is part of orchestrators
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
        @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }
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
            <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: '12px', overflow: 'hidden' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--tm-border)' }}>
                    {['Name', 'Slug', 'Skills', 'Endpoint', 'Synced', 'Status', ''].map((h) => (
                      <th key={h} style={{
                        padding: '10px 16px', textAlign: 'left',
                        fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)',
                        textTransform: 'uppercase', letterSpacing: '0.06em',
                        background: 'var(--tm-surface-2)',
                      }}>{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {loading && (
                    <tr><td colSpan={7} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>Loading…</td></tr>
                  )}
                  {!loading && agents.length === 0 && (
                    <tr><td colSpan={7} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>No agents yet — click New Agent to add one</td></tr>
                  )}
                  {agents.map((agent, i) => (
                    <tr key={agent.id}
                      style={{ borderBottom: i < agents.length - 1 ? '1px solid var(--tm-border-subtle)' : 'none' }}
                      onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--tm-surface-2)')}
                      onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
                    >
                      <td style={{ padding: '12px 16px' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                          <div style={{
                            width: '32px', height: '32px', borderRadius: '8px', flexShrink: 0,
                            background: 'var(--tm-accent-bg)', color: 'var(--tm-accent)',
                            display: 'flex', alignItems: 'center', justifyContent: 'center',
                          }}>
                            <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>smart_toy</span>
                          </div>
                          <div>
                            <div style={{ fontSize: '14px', fontWeight: 600, color: 'var(--tm-text)' }}>{agent.display_name}</div>
                            {agent.description && <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '1px' }}>{agent.description.split('\n')[0].slice(0, 60)}{agent.description.split('\n')[0].length > 60 ? '…' : ''}</div>}
                          </div>
                        </div>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <code style={{ fontSize: '12px', color: 'var(--tm-text-muted)', background: 'var(--tm-surface-2)', padding: '2px 6px', borderRadius: '4px' }}>
                          {agent.slug}
                        </code>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        {agent.skills && agent.skills.length > 0
                          ? <span style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }} title={agent.skills.map((s) => s.name).join(', ')}>{agent.skills.length} skill{agent.skills.length !== 1 ? 's' : ''}</span>
                          : <span style={{ fontSize: '11px', color: 'var(--tm-text-subtle)' }}>—</span>
                        }
                      </td>
                      <td style={{ padding: '12px 16px', maxWidth: '180px' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'block' }}>
                          {agent.endpoint_url}
                        </span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>
                          {agent.card_fetched_at ? timeAgo(agent.card_fetched_at) : '—'}
                        </span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', alignItems: 'flex-start' }}>
                          <span style={{
                            fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px',
                            background: agent.enabled ? 'rgba(0,118,80,.12)' : 'rgba(107,114,128,.1)',
                            color: agent.enabled ? '#005b3d' : '#6b7280',
                          }}>
                            {agent.enabled ? 'Enabled' : 'Disabled'}
                          </span>
                          {/* Security score badge */}
                          {(() => {
                            const sr = scanResults[agent.id];
                            if (!sr) return null;
                            if (sr === 'scanning') return (
                              <span style={{ fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px', background: 'rgba(167,139,250,.1)', color: '#a78bfa', animation: 'pulse 1.5s ease-in-out infinite' }}>
                                🛡️ Scanning…
                              </span>
                            );
                            const { bg, color } = riskColors(sr.risk);
                            return (
                              <button
                                onClick={() => setScanModal({ agent, result: sr })}
                                style={{ fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px', background: bg, color, border: 'none', cursor: 'pointer' }}
                                title="Click to view security report"
                              >
                                🛡️ {sr.score} · {sr.risk}
                              </button>
                            );
                          })()}
                        </div>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', alignItems: 'flex-end' }}>
                          <div style={{ display: 'flex', gap: '8px' }}>
                            <button onClick={() => handleRowDiscover(agent)} disabled={!!rowDiscoverState[agent.id]} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid rgba(167,139,250,.4)',
                              background: 'transparent', cursor: rowDiscoverState[agent.id] ? 'not-allowed' : 'pointer',
                              fontSize: '12px', color: '#a78bfa', opacity: rowDiscoverState[agent.id] ? 0.6 : 1,
                            }}>
                              {rowDiscoverState[agent.id] ? 'Discovering…' : 'Discover'}
                            </button>
                            <button onClick={() => handleScan(agent)} disabled={scanResults[agent.id] === 'scanning'} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid rgba(34,197,94,.4)',
                              background: 'transparent', cursor: scanResults[agent.id] === 'scanning' ? 'not-allowed' : 'pointer',
                              fontSize: '12px', color: '#16a34a', opacity: scanResults[agent.id] === 'scanning' ? 0.6 : 1,
                            }}>
                              {scanResults[agent.id] === 'scanning' ? '🛡️ Scanning…' : '🛡️ Scan'}
                            </button>
                            <button onClick={() => handleTest(agent)} disabled={testResults[agent.id] === 'testing'} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid var(--tm-border)',
                              background: 'transparent', cursor: 'pointer', fontSize: '12px', color: 'var(--tm-accent)',
                              opacity: testResults[agent.id] === 'testing' ? 0.6 : 1,
                            }}>
                              {testResults[agent.id] === 'testing' ? 'Testing…' : 'Test'}
                            </button>
                            <button onClick={() => openEdit(agent)} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid var(--tm-border)',
                              background: 'transparent', cursor: 'pointer', fontSize: '12px', color: 'var(--tm-text)',
                            }}>Edit</button>
                            <button onClick={() => setDeleteTarget(agent)} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid rgba(220,38,38,.3)',
                              background: 'transparent', cursor: 'pointer', fontSize: '12px', color: '#dc2626',
                            }}>Delete</button>
                          </div>
                          {testResults[agent.id] && testResults[agent.id] !== 'testing' && (() => {
                            const r = testResults[agent.id] as { ok: boolean; latency_ms: number; detail: string };
                            return (
                              <div style={{
                                fontSize: '11px', padding: '3px 8px', borderRadius: '4px', maxWidth: '280px', textAlign: 'right',
                                background: r.ok ? 'rgba(0,118,80,.08)' : 'rgba(220,38,38,.08)',
                                color: r.ok ? '#005b3d' : '#dc2626',
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

        {/* Create / Edit Modal */}
        {showModal && (
          <Modal title={editing ? `Edit — ${editing.display_name}` : 'New Agent'} onClose={() => setShowModal(false)}>
            <Field label="Endpoint URL">
              <div style={{ display: 'flex', gap: '8px' }}>
                <input style={{ ...inputStyle, flex: 1 }} value={form.endpoint_url} onChange={(e) => set('endpoint_url', e.target.value)} placeholder="http://host:port" />
                <button onClick={handleDiscover} disabled={discovering} style={{
                  padding: '8px 14px', borderRadius: '8px', border: '1px solid var(--tm-border)',
                  background: 'var(--tm-surface-2)', cursor: discovering ? 'not-allowed' : 'pointer',
                  fontSize: '12px', fontWeight: 600, color: 'var(--tm-accent)',
                  whiteSpace: 'nowrap', opacity: discovering ? 0.6 : 1, flexShrink: 0,
                }}>{discovering ? 'Discovering…' : 'Discover'}</button>
              </div>
              {discoverError && <div style={{ fontSize: '11px', color: '#dc2626', marginTop: '4px' }}>{discoverError}</div>}
              {form.agent_card_url && !discoverError && (
                <div style={{ fontSize: '11px', color: '#005b3d', marginTop: '4px' }}>
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
                <div style={{ border: '1px solid var(--tm-border)', borderRadius: '8px', background: 'var(--tm-surface-2)', padding: '8px 12px', maxHeight: '120px', overflowY: 'auto' }}>
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
            {error && <div style={{ padding: '10px 12px', borderRadius: '8px', background: 'rgba(220,38,38,.08)', color: '#dc2626', fontSize: '13px', marginBottom: '16px' }}>{error}</div>}
            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '8px' }}>
              <button onClick={() => setShowModal(false)} style={{ padding: '8px 20px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', cursor: 'pointer', fontSize: '14px', color: 'var(--tm-text)' }}>Cancel</button>
              <button onClick={handleSave} disabled={saving} style={{ padding: '8px 20px', borderRadius: '8px', border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: saving ? 'not-allowed' : 'pointer', fontSize: '14px', fontWeight: 600, opacity: saving ? 0.7 : 1 }}>{saving ? 'Saving…' : (editing ? 'Save Changes' : 'Create Agent')}</button>
            </div>
          </Modal>
        )}

        {/* Discover popup */}
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
                padding: '10px 14px', borderRadius: '8px', marginBottom: '20px',
                background: diff.hasChanges ? 'rgba(251,191,36,.08)' : 'rgba(78,222,163,.08)',
                border: `1px solid ${diff.hasChanges ? 'rgba(251,191,36,.3)' : 'rgba(78,222,163,.3)'}`,
                display: 'flex', alignItems: 'center', gap: '8px',
              }}>
                <span style={{ fontSize: '16px' }}>{diff.hasChanges ? '⚠️' : '✓'}</span>
                <span style={{ fontSize: '13px', fontWeight: 600, color: diff.hasChanges ? '#fbbf24' : '#4edea3' }}>
                  {diff.hasChanges ? 'Changes detected — review and save to update this agent' : 'Up to date — no changes since last sync'}
                </span>
              </div>

              {/* Two-column layout */}
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>

                {/* Left: card info */}
                <div>
                  <SectionLabel>Agent Info</SectionLabel>
                  <div style={{ border: '1px solid var(--tm-border)', borderRadius: '8px', overflow: 'hidden', marginBottom: '16px' }}>
                    <DiffRow label="Name" changed={diff.displayName.changed} oldVal={diff.displayName.old} newVal={diff.displayName.new} />
                    {version && <DiffRow label="Version" changed={diff.version.changed} oldVal={diff.version.old} newVal={version} />}
                    {provider && <DiffRow label="Provider" changed={diff.provider.changed} oldVal={diff.provider.old} newVal={provider} />}
                    {docUrl && (
                      <div style={{ display: 'flex', gap: '8px', alignItems: 'center', padding: '6px 0 6px 0', borderBottom: '1px solid var(--tm-border)' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px' }}>Docs</span>
                        <a href={docUrl} target="_blank" rel="noreferrer" style={{ fontSize: '12px', color: '#5b7fff' }}>{docUrl}</a>
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

                  {/* Card URL */}
                  <SectionLabel>Card URL</SectionLabel>
                  <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', fontFamily: 'monospace', wordBreak: 'break-all', marginBottom: '16px' }}>
                    {result.agent_card_url}
                  </div>

                  {/* Orchestrators using this agent */}
                  {affectedOrchestrators.length > 0 && (
                    <>
                      <SectionLabel>Used by orchestrators</SectionLabel>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                        {affectedOrchestrators.map(o => (
                          <div key={o.id} style={{ fontSize: '12px', padding: '4px 8px', borderRadius: '6px', background: 'rgba(251,191,36,.08)', color: '#fbbf24', border: '1px solid rgba(251,191,36,.2)' }}>
                            {o.display_name || o.name}
                          </div>
                        ))}
                      </div>
                    </>
                  )}
                </div>

                {/* Right: description + skills */}
                <div>
                  {/* Description */}
                  <SectionLabel>Description</SectionLabel>
                  <div style={{
                    padding: '10px 12px', borderRadius: '8px', marginBottom: '16px',
                    border: `1px solid ${diff.description.changed ? 'rgba(78,222,163,.4)' : 'var(--tm-border)'}`,
                    background: diff.description.changed ? 'rgba(78,222,163,.04)' : 'var(--tm-surface-2)',
                  }}>
                    {diff.description.changed && (
                      <div style={{ fontSize: '11px', color: '#94a3b8', textDecoration: 'line-through', marginBottom: '6px', whiteSpace: 'pre-wrap' }}>{diff.description.old || '—'}</div>
                    )}
                    <div style={{ fontSize: '12px', color: diff.description.changed ? '#4edea3' : 'var(--tm-text)', whiteSpace: 'pre-wrap', lineHeight: 1.5 }}>
                      {result.description || '—'}
                    </div>
                  </div>

                  {/* Skills */}
                  <SectionLabel>Skills ({result.skills.length}){diff.skills.changed && <span style={{ color: '#fbbf24', marginLeft: '6px', textTransform: 'none', fontSize: '10px' }}>changed</span>}</SectionLabel>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', maxHeight: '280px', overflowY: 'auto' }}>
                    {result.skills.length === 0 && <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>No skills declared</div>}
                    {result.skills.map((s, i) => {
                      const skillCard = ((newCard.skills ?? []) as Record<string, unknown>[])[i] ?? {};
                      const inputModes = Array.isArray(skillCard.inputModes) ? (skillCard.inputModes as string[]) : [];
                      const outputModes = Array.isArray(skillCard.outputModes) ? (skillCard.outputModes as string[]) : [];
                      return (
                        <div key={i} style={{ padding: '8px 10px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'var(--tm-surface-2)' }}>
                          <div style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-text)', marginBottom: '2px' }}>{s.name}</div>
                          {s.description && <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', lineHeight: 1.4, marginBottom: '4px' }}>{s.description}</div>}
                          <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
                            {(s.tags ?? []).map((t, ti) => (
                              <span key={ti} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(167,139,250,.1)', color: '#a78bfa' }}>{t}</span>
                            ))}
                            {inputModes.map((m, mi) => (
                              <span key={`in-${mi}`} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(96,165,250,.1)', color: '#60a5fa' }}>in:{m}</span>
                            ))}
                            {outputModes.map((m, mi) => (
                              <span key={`out-${mi}`} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(78,222,163,.1)', color: '#4edea3' }}>out:{m}</span>
                            ))}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              </div>

              {/* Actions */}
              <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '24px', paddingTop: '16px', borderTop: '1px solid var(--tm-border)' }}>
                <button onClick={() => setDiscoverPopup(null)} style={{ padding: '8px 20px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', cursor: 'pointer', fontSize: '14px', color: 'var(--tm-text)' }}>Close</button>
                {diff.hasChanges && (
                  <button
                    onClick={handleApplyDiscover}
                    disabled={applyingDiscover}
                    className="save-pulse"
                    style={{
                      padding: '8px 24px', borderRadius: '8px', border: 'none',
                      background: '#7c3aed', color: '#fff',
                      cursor: applyingDiscover ? 'not-allowed' : 'pointer',
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

        {/* Security scan detail modal */}
        {scanModal && (() => {
          const { agent, result } = scanModal;
          const { bg, color } = riskColors(result.risk);
          return (
            <Modal wide title={`Security Report — ${agent.display_name}`} onClose={() => setScanModal(null)}>
              {/* Score + risk header */}
              <div style={{ display: 'flex', alignItems: 'center', gap: '16px', marginBottom: '20px' }}>
                <div style={{
                  width: '72px', height: '72px', borderRadius: '50%', flexShrink: 0,
                  background: bg, color, display: 'flex', flexDirection: 'column',
                  alignItems: 'center', justifyContent: 'center',
                }}>
                  <span style={{ fontSize: '22px', fontWeight: 800, lineHeight: 1 }}>{result.score}</span>
                  <span style={{ fontSize: '9px', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.05em' }}>/ 100</span>
                </div>
                <div>
                  <span style={{ fontSize: '11px', fontWeight: 700, padding: '3px 10px', borderRadius: '4px', background: bg, color, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
                    {result.risk} risk
                  </span>
                  <p style={{ fontSize: '15px', color: 'var(--tm-text)', marginTop: '8px', lineHeight: 1.5, fontWeight: 500 }}>
                    {result.summary}
                  </p>
                </div>
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>
                {/* Left: findings */}
                <div>
                  <SectionLabel>Findings</SectionLabel>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                    {result.findings.map((f, i) => {
                      const si = statusIcon(f.status);
                      const rc = riskColors(f.risk);
                      return (
                        <div key={i} style={{ padding: '10px 12px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'var(--tm-surface-2)' }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
                            <span style={{ fontSize: '14px', color: si.color, fontWeight: 700 }}>{si.icon}</span>
                            <span style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-text)', flex: 1 }}>{f.label}</span>
                            <span style={{ fontSize: '9px', fontWeight: 700, padding: '1px 6px', borderRadius: '3px', background: rc.bg, color: rc.color, textTransform: 'uppercase' }}>{f.risk}</span>
                          </div>
                          <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', margin: '0 0 4px 22px', lineHeight: 1.4 }}>{f.detail}</p>
                          {f.recommendation !== 'No action needed.' && (
                            <p style={{ fontSize: '11px', color: '#60a5fa', margin: '0 0 0 22px', lineHeight: 1.4 }}>→ {f.recommendation}</p>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>

                {/* Right: HTTP probes + meta */}
                <div>
                  <SectionLabel>HTTP Probes</SectionLabel>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', marginBottom: '20px' }}>
                    {[
                      { label: 'TLS', value: result.http_probes.tls },
                      { label: 'Auth Required', value: result.http_probes.auth_required },
                      { label: 'Reachable', value: result.http_probes.reachable ? 'pass' : 'fail' },
                    ].map(({ label, value }) => {
                      const pass = value === 'pass' || value === true;
                      return (
                        <div key={label} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '6px 10px', borderRadius: '6px', background: 'var(--tm-surface-2)', border: '1px solid var(--tm-border)' }}>
                          <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>{label}</span>
                          <span style={{ fontSize: '11px', fontWeight: 700, padding: '2px 8px', borderRadius: '3px', background: pass ? 'rgba(0,118,80,.12)' : 'rgba(220,38,38,.12)', color: pass ? '#005b3d' : '#dc2626' }}>
                            {pass ? '✓ pass' : '✗ fail'}
                          </span>
                        </div>
                      );
                    })}
                  </div>

                  <SectionLabel>Scanned</SectionLabel>
                  <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', marginBottom: '20px' }}>
                    {timeAgo(result.scanned_at)} — {new Date(result.scanned_at).toLocaleString()}
                  </p>

                  <SectionLabel>Agent</SectionLabel>
                  <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>
                    <code style={{ background: 'var(--tm-surface-2)', padding: '2px 6px', borderRadius: '4px' }}>{agent.slug}</code>
                    <br />
                    <span style={{ wordBreak: 'break-all' }}>{agent.endpoint_url}</span>
                  </p>
                </div>
              </div>

              <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '24px', paddingTop: '16px', borderTop: '1px solid var(--tm-border)' }}>
                <button onClick={() => setScanModal(null)} style={{ padding: '8px 20px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', cursor: 'pointer', fontSize: '14px', color: 'var(--tm-text)' }}>Close</button>
                <button onClick={() => handleScan(agent)} disabled={scanResults[agent.id] === 'scanning'} style={{ padding: '8px 20px', borderRadius: '8px', border: 'none', background: '#16a34a', color: '#fff', cursor: 'pointer', fontSize: '14px', fontWeight: 600, opacity: scanResults[agent.id] === 'scanning' ? 0.6 : 1 }}>
                  🛡️ Re-scan
                </button>
              </div>
            </Modal>
          );
        })()}

        {/* Delete confirm */}
        {deleteTarget && (
          <Modal title="Delete Agent" onClose={() => setDeleteTarget(null)}>
            <p style={{ color: 'var(--tm-text)', marginBottom: '24px' }}>
              Delete <strong>{deleteTarget.display_name}</strong>? This cannot be undone and will remove it from any orchestrators that use it.
            </p>
            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
              <button onClick={() => setDeleteTarget(null)} style={{ padding: '8px 20px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'transparent', cursor: 'pointer', fontSize: '14px', color: 'var(--tm-text)' }}>Cancel</button>
              <button onClick={handleDelete} style={{ padding: '8px 20px', borderRadius: '8px', border: 'none', background: '#dc2626', color: '#fff', cursor: 'pointer', fontSize: '14px', fontWeight: 600 }}>Delete</button>
            </div>
          </Modal>
        )}
      </div>
    </AuthGuard>
  );
}
