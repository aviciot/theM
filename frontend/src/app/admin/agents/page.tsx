'use client';
import { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import ChromaGrid from '@/components/ChromaGrid';
import { themApi, getPreferences, setPreferences, type Agent, type AgentDefinition, type DiscoverResult, type OrchestratorFull, type ScanResult } from '@/lib/api';
import { type AgentFolder, type FolderState, loadFoldersLocal, saveFoldersLocal, genId, EMPTY_FORM, type CardDiff, buildDiff } from './agentTypes';
import { agentCategory } from './agentUtils';
import { AgentCard, _inFlightScans } from './AgentCard';
import { FolderHeader } from './FolderHeader';
import { AgentModals } from './AgentModals';

type FormState = typeof EMPTY_FORM;

function DeployCard({ onClick }: { onClick: () => void }) {
  return (
    <article
      onClick={onClick}
      style={{ borderRadius: '24px', border: '1px dashed rgba(99,102,241,0.5)', background: 'rgba(15,23,42,0.2)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: '16px', padding: '48px 24px', cursor: 'pointer', minHeight: '280px', transition: 'border-color 200ms ease, background 200ms ease' }}
      className="deploy-card"
    >
      <div style={{ width: '56px', height: '56px', borderRadius: '16px', background: 'rgba(99,102,241,0.1)', border: '1px dashed rgba(99,102,241,0.4)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: '28px', color: '#6366f1' }}>+</div>
      <div style={{ textAlign: 'center' }}>
        <p style={{ fontSize: '16px', fontWeight: 600, color: 'var(--tm-card-text)', margin: '0 0 6px 0' }}>Deploy a new agent</p>
        <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: 0 }}>Connect an A2A agent endpoint</p>
      </div>
      <button style={{ padding: '10px 24px', borderRadius: '8px', border: 'none', cursor: 'pointer', background: '#6366f1', color: '#fff', fontSize: '13px', fontWeight: 600, boxShadow: '0 0 15px rgba(99,102,241,0.3)' }}>
        Deploy New Agent
      </button>
    </article>
  );
}

export default function AdminAgentsPage() {
  const router = useRouter();
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
  const [rowDiscoverError, setRowDiscoverError] = useState<Record<string, string>>({});
  const [rowDiscoverSuccess, setRowDiscoverSuccess] = useState<Record<string, boolean>>({});
  const [discoverPopup, setDiscoverPopup] = useState<{ agent: Agent; result: DiscoverResult; diff: CardDiff } | null>(null);
  const [applyingDiscover, setApplyingDiscover] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [discoverError, setDiscoverError] = useState('');
  const [scanResults, setScanResults] = useState<Record<string, ScanResult | 'scanning'>>({});
  const [scanSteps, setScanSteps] = useState<Record<string, string>>({});
  const [scanModal, setScanModal] = useState<{ agent: Agent; result: ScanResult } | null>(null);
  const dashWsRef = useRef<WebSocket | null>(null);
  const agentIdsKeyRef = useRef<string>('');
  const wsAliveRef = useRef(false);
  const [searchTerm, setSearchTerm] = useState('');
  const [activeCategory, setActiveCategory] = useState('All');

  const [folderState, setFolderState] = useState<FolderState>(() => loadFoldersLocal());
  const [dragOverId, setDragOverId] = useState<string | null>(null);
  const [pendingFolder, setPendingFolder] = useState<{ agentA: Agent; agentB: Agent } | null>(null);
  const [folderNameInput, setFolderNameInput] = useState('');

  useEffect(() => {
    getPreferences().then(prefs => {
      if (prefs.agentFolders && Array.isArray(prefs.agentFolders.folders)) {
        setFolderState(prefs.agentFolders);
        saveFoldersLocal(prefs.agentFolders);
      }
    }).catch(() => { /* keep localStorage state on network error */ });
  }, []);

  function updateFolders(next: FolderState) {
    setFolderState(next);
    saveFoldersLocal(next);
    getPreferences().then(prefs =>
      setPreferences({ ...prefs, agentFolders: next })
    ).catch(() => { /* silently ignore */ });
  }

  function handleAgentDrop(draggedAgentId: string, targetAgentId: string) {
    if (draggedAgentId === targetAgentId) return;
    const allFolders = folderState.folders;
    const targetFolder = allFolders.find(f => f.agentIds.includes(targetAgentId));
    if (targetFolder) {
      if (!targetFolder.agentIds.includes(draggedAgentId)) {
        updateFolders({
          folders: allFolders.map(f =>
            f.id === targetFolder.id
              ? { ...f, agentIds: [...f.agentIds.filter(id => id !== draggedAgentId), draggedAgentId] }
              : { ...f, agentIds: f.agentIds.filter(id => id !== draggedAgentId) }
          ).filter(f => f.agentIds.length > 0),
        });
      }
    } else {
      const agentA = agents.find(a => a.id === draggedAgentId);
      const agentB = agents.find(a => a.id === targetAgentId);
      if (agentA && agentB) {
        setFolderNameInput(agentA.display_name.split(' ')[0] + ' & ' + agentB.display_name.split(' ')[0]);
        setPendingFolder({ agentA, agentB });
      }
    }
  }

  function handleDropOntoFolder(draggedAgentId: string, folderId: string) {
    const allFolders = folderState.folders;
    const targetFolder = allFolders.find(f => f.id === folderId);
    if (!targetFolder || targetFolder.agentIds.includes(draggedAgentId)) return;
    updateFolders({
      folders: allFolders.map(f => {
        if (f.id === folderId) return { ...f, agentIds: [...f.agentIds, draggedAgentId] };
        return { ...f, agentIds: f.agentIds.filter(id => id !== draggedAgentId) };
      }).filter(f => f.agentIds.length > 0),
    });
  }

  function confirmCreateFolder() {
    if (!pendingFolder) return;
    const name = folderNameInput.trim() || 'Folder';
    const cleaned = folderState.folders.map(f => ({
      ...f,
      agentIds: f.agentIds.filter(id => id !== pendingFolder.agentA.id && id !== pendingFolder.agentB.id),
    })).filter(f => f.agentIds.length > 0);
    updateFolders({ folders: [...cleaned, { id: genId(), name, agentIds: [pendingFolder.agentA.id, pendingFolder.agentB.id], collapsed: false }] });
    setPendingFolder(null);
    setFolderNameInput('');
  }

  function removeAgentFromFolder(agentId: string) {
    updateFolders({ folders: folderState.folders.map(f => ({ ...f, agentIds: f.agentIds.filter(id => id !== agentId) })).filter(f => f.agentIds.length > 0) });
  }

  function toggleFolderCollapse(folderId: string) {
    updateFolders({ folders: folderState.folders.map(f => f.id === folderId ? { ...f, collapsed: !f.collapsed } : f) });
  }

  function renameFolderInline(folderId: string, newName: string) {
    updateFolders({ folders: folderState.folders.map(f => f.id === folderId ? { ...f, name: newName } : f) });
  }

  useEffect(() => {
    if (agents.length === 0) return;
    const liveIds = new Set(agents.map(a => a.id));
    const pruned: FolderState = {
      folders: folderState.folders.map(f => ({ ...f, agentIds: f.agentIds.filter(id => liveIds.has(id)) })).filter(f => f.agentIds.length > 0),
    };
    if (JSON.stringify(pruned) !== JSON.stringify(folderState)) updateFolders(pruned);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents]);

  const [draftDefinitions, setDraftDefinitions] = useState<AgentDefinition[]>([]);

  const reload = () => {
    Promise.all([themApi.agents(), themApi.orchestrators(), themApi.listAgentDefinitions()])
      .then(([a, o, defs]) => {
        setAgents(a);
        setOrchestrators(o);
        setDraftDefinitions(defs.filter(d => d.status === 'draft'));
        setScanResults((prev) => {
          const next = { ...prev };
          for (const agent of a) {
            if (_inFlightScans.has(agent.id)) {
              if (agent.last_scan_result) { next[agent.id] = agent.last_scan_result; _inFlightScans.delete(agent.id); }
              else { next[agent.id] = 'scanning'; }
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

  useEffect(() => {
    if (agents.length === 0) return;
    const key = agents.map((a) => a.id).sort().join(',');
    if (key === agentIdsKeyRef.current) return;
    agentIdsKeyRef.current = key;
    wsAliveRef.current = true;

    const channels = agents.map((a) => `agent:${a.id}`);
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const handleEvent = (event: Record<string, unknown>) => {
      const agentId = (event.agent_id as string) ?? '';
      if (!agentId) return;
      if (event.type === 'scan_started') {
        _inFlightScans.add(agentId);
        setScanResults((r) => ({ ...r, [agentId]: 'scanning' }));
        setScanSteps((s) => ({ ...s, [agentId]: 'Starting…' }));
      } else if (event.type === 'scan_step') {
        setScanResults((r) => ({ ...r, [agentId]: 'scanning' }));
        setScanSteps((s) => ({ ...s, [agentId]: (event.step as string) ?? '' }));
      } else if (event.type === 'scan_complete') {
        _inFlightScans.delete(agentId);
        setScanSteps((s) => { const n = { ...s }; delete n[agentId]; return n; });
        const rawFindings = event.findings;
        const parsedFindings: ScanResult['findings'] = Array.isArray(rawFindings)
          ? rawFindings as ScanResult['findings']
          : typeof rawFindings === 'string' ? (() => { try { return JSON.parse(rawFindings); } catch { return []; } })()
          : [];
        const rawProbes = event.http_probes;
        const parsedProbes: ScanResult['http_probes'] = (rawProbes && typeof rawProbes === 'object' && !Array.isArray(rawProbes))
          ? rawProbes as ScanResult['http_probes']
          : typeof rawProbes === 'string' ? (() => { try { return JSON.parse(rawProbes); } catch { return { tls: '', auth_required: '', reachable: false }; } })()
          : { tls: '', auth_required: '', reachable: false };
        setScanResults((r) => ({ ...r, [agentId]: { score: event.score as number, risk: event.risk as string, summary: event.summary as string, findings: parsedFindings, http_probes: parsedProbes, scanned_at: (event.scanned_at as string) ?? new Date().toISOString() } as ScanResult }));
        reload();
      } else if (event.type === 'scan_failed') {
        _inFlightScans.delete(agentId);
        setScanSteps((s) => { const n = { ...s }; delete n[agentId]; return n; });
        setScanResults((r) => { const n = { ...r }; delete n[agentId]; return n; });
        alert(`Scan failed: ${(event.error as string) ?? 'unknown error'}`);
      }
    };

    const connect = () => {
      if (!wsAliveRef.current) return;
      if (dashWsRef.current) { dashWsRef.current.close(); dashWsRef.current = null; }
      fetch('/api/auth/token')
        .then((r) => r.json())
        .then((data: { token?: string }) => {
          if (!wsAliveRef.current || !data.token) return;
          const wsBase = window.location.origin.replace('http://', 'ws://').replace('https://', 'wss://');
          const ws = new WebSocket(`${wsBase}/ws/dashboard?token=${data.token}`);
          dashWsRef.current = ws;
          ws.onopen = () => { ws.send(JSON.stringify({ type: 'subscribe', channels })); };
          ws.onmessage = (e) => {
            try {
              const msg = JSON.parse(e.data);
              if (msg.type === 'ping') return;
              const ch: string = msg.channel ?? '';
              if (!ch.startsWith('agent:')) return;
              handleEvent(msg.event ?? {});
            } catch { /* ignore parse errors */ }
          };
          ws.onerror = () => { /* handled by onclose */ };
          ws.onclose = () => {
            if (dashWsRef.current === ws) dashWsRef.current = null;
            if (!wsAliveRef.current) return;
            if (reconnectTimer) clearTimeout(reconnectTimer);
            reconnectTimer = setTimeout(connect, 1000);
          };
        })
        .catch(() => {
          if (!wsAliveRef.current) return;
          if (reconnectTimer) clearTimeout(reconnectTimer);
          reconnectTimer = setTimeout(connect, 1000);
        });
    };

    connect();
    return () => {
      wsAliveRef.current = false;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (dashWsRef.current) { dashWsRef.current.close(); dashWsRef.current = null; }
      agentIdsKeyRef.current = '';
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
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
      slug: agent.slug, display_name: agent.display_name, description: agent.description || '',
      transport: agent.transport, endpoint_url: agent.endpoint_url, auth_token: '',
      max_concurrency: agent.max_concurrency, timeout_seconds: agent.timeout_seconds,
      enabled: agent.enabled, skills: agent.skills || [],
      supports_streaming: agent.supports_streaming || false, supports_push: agent.supports_push || false,
      icon: agent.icon || '', agent_card: agent.agent_card || null,
      agent_card_url: agent.agent_card_url || '', tags: agent.tags || [],
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
      setForm((f) => ({ ...f, display_name: result.display_name || f.display_name, slug: editing ? f.slug : (result.suggested_slug || f.slug), description: result.description || f.description, skills: result.skills, supports_streaming: result.supports_streaming, supports_push: result.supports_push, ...(result.icon ? { icon: result.icon } : {}), ...(result.category ? { category: result.category } : {}), agent_card: result.agent_card, agent_card_url: result.agent_card_url }));
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
      if (!body.icon) body.icon = null;
      if (!Array.isArray(body.tags)) body.tags = [];
      if (editing) { delete body.slug; await themApi.updateAgent(editing.id, body); }
      else { await themApi.createAgent(body); }
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
    setScanSteps((s) => ({ ...s, [agent.id]: 'Starting…' }));
    setScanModal(null);
    try {
      await themApi.scanAgent(agent.id);
    } catch (e: unknown) {
      _inFlightScans.delete(agent.id);
      setScanResults((r) => { const n = { ...r }; delete n[agent.id]; return n; });
      setScanSteps((s) => { const n = { ...s }; delete n[agent.id]; return n; });
      alert(e instanceof Error ? e.message : 'Scan failed');
    }
  }

  async function handleRowDiscover(agent: Agent) {
    setRowDiscoverState((r) => ({ ...r, [agent.id]: 'discovering' }));
    setRowDiscoverError((r) => { const n = { ...r }; delete n[agent.id]; return n; });
    try {
      const result = await themApi.discoverAgent({ endpoint_url: agent.endpoint_url, agent_id: agent.id });
      if (!result.ok) { setRowDiscoverError((r) => ({ ...r, [agent.id]: result.detail ?? 'Discovery failed' })); return; }
      setDiscoverPopup({ agent, result, diff: buildDiff(agent, result) });
    } catch (e: unknown) {
      setRowDiscoverError((r) => ({ ...r, [agent.id]: e instanceof Error ? e.message : 'Discovery failed' }));
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
      if (!window.confirm(`This agent is used by ${affected.length} orchestrator${affected.length > 1 ? 's' : ''}: ${names}\n\nTheir tool descriptions will update on the next run. Continue?`)) return;
    }
    setApplyingDiscover(true);
    try {
      await themApi.updateAgent(agent.id, { display_name: result.display_name || agent.display_name, description: result.description || agent.description, skills: result.skills, supports_streaming: result.supports_streaming, supports_push: result.supports_push, ...(result.icon ? { icon: result.icon } : {}), ...(result.category ? { category: result.category } : {}), agent_card: result.agent_card, agent_card_url: result.agent_card_url });
      setDiscoverPopup(null);
      setRowDiscoverSuccess((r) => ({ ...r, [agent.id]: true }));
      setTimeout(() => setRowDiscoverSuccess((r) => { const n = { ...r }; delete n[agent.id]; return n; }), 3000);
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

  const CATEGORY_PILLS = ['All', 'Enabled', 'A2A', 'Vision', 'Coding', 'Research'];

  const filteredAgents = agents.filter((a) => {
    const q = searchTerm.toLowerCase();
    const matchSearch = !q || a.display_name.toLowerCase().includes(q) || a.slug.toLowerCase().includes(q) || (a.description ?? '').toLowerCase().includes(q) || (a.endpoint_url ?? '').toLowerCase().includes(q);
    const cat = agentCategory(a);
    const matchCategory = activeCategory === 'All' ? true : activeCategory === 'Enabled' ? a.enabled : cat === activeCategory;
    return matchSearch && matchCategory;
  });

  function renderAgentCard(agent: Agent, extraProps?: { isDragOver?: boolean; onDragStart?: (e: React.DragEvent) => void; onDragOver?: (e: React.DragEvent) => void; onDrop?: (e: React.DragEvent) => void; onRemoveFromFolder?: () => void }) {
    return (
      <AgentCard
        key={agent.id}
        agent={agent}
        scanResult={scanResults[agent.id]}
        scanStep={scanSteps[agent.id]}
        testResult={testResults[agent.id]}
        isDiscovering={!!rowDiscoverState[agent.id]}
        discoverError={rowDiscoverError[agent.id]}
        discoverSuccess={!!rowDiscoverSuccess[agent.id]}
        onTest={() => handleTest(agent)}
        onScan={() => handleScan(agent)}
        onDiscover={() => handleRowDiscover(agent)}
        onEdit={() => openEdit(agent)}
        onDelete={() => setDeleteTarget(agent)}
        onOpenScanModal={() => {
          const sr = scanResults[agent.id];
          if (sr && sr !== 'scanning') setScanModal({ agent, result: sr });
        }}
        {...extraProps}
      />
    );
  }

  return (
    <AuthGuard>
      <style>{`
/* ── Glass card ─────────────────────────────────────── */
        .glass-card {
          background: linear-gradient(160deg, rgba(255,255,255,0.032) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%), var(--tm-card);
          border: 1px solid var(--tm-card-border);
          backdrop-filter: blur(12px);
          box-shadow: 0 8px 32px rgba(0,0,0,0.4), 0 2px 8px rgba(0,0,0,0.25), inset 0 1px 0 rgba(255,255,255,0.04);
          transition: border-color 240ms ease, box-shadow 240ms ease;
        }
        .glass-card:hover { border-color: rgba(0,209,255,0.28); box-shadow: 0 8px 32px rgba(0,0,0,0.5), 0 2px 8px var(--tm-inset-deep), 0 0 0 1px rgba(0,209,255,0.1), 0 0 32px rgba(0,209,255,0.08), inset 0 1px 0 rgba(255,255,255,0.055); }
        .glass-card:active { box-shadow: 0 4px 16px rgba(0,0,0,0.5), inset 0 1px 0 var(--tm-filter-bg); border-color: rgba(0,209,255,0.4); transition: border-color 80ms ease, box-shadow 80ms ease; }
        .deploy-card:hover { border-color: rgba(99,102,241,0.7) !important; background: rgba(99,102,241,0.04) !important; }
        .card-action-btn { display: flex; align-items: center; justify-content: center; gap: 5px; padding: 9px 4px; border-radius: 8px; font-size: 11px; font-weight: 700; letter-spacing: 0.01em; cursor: pointer; transition: border-color 180ms ease, background 180ms ease, box-shadow 180ms ease, transform 180ms ease; white-space: nowrap; }
        .card-action-btn:disabled { opacity: 0.45; cursor: not-allowed; }
        .card-action-btn--primary { background: #00d1ff; color: #021520; border: none; box-shadow: 0 0 14px rgba(0,209,255,0.38); }
        .card-action-btn--primary:hover:not(:disabled) { background: #22dcff; box-shadow: 0 0 22px rgba(0,209,255,0.55); }
        .card-action-btn--primary:active:not(:disabled) { background: #00b8e0; box-shadow: 0 0 10px rgba(0,209,255,0.3); }
        .card-action-btn--secondary { background: var(--tm-btn-2-bg); color: var(--tm-card-text-muted); border: 1px solid rgba(255,255,255,0.08); }
        .card-action-btn--secondary:hover:not(:disabled) { border-color: rgba(129,140,248,0.45); color: #818cf8; background: rgba(99,102,241,0.1); }
        .card-action-btn--secondary.is-loading { border-color: rgba(129,140,248,0.45); color: #818cf8; background: rgba(99,102,241,0.1); }
        @keyframes spin { to { transform: rotate(360deg); } }
        .spin { animation: spin 0.8s linear infinite; display: inline-block; }
        .card-action-btn--scan { background: var(--tm-btn-2-bg); color: var(--tm-card-text-muted); border: 1px solid rgba(255,255,255,0.08); }
        .card-action-btn--scan:hover:not(:disabled) { border-color: rgba(0,209,255,0.42); color: #00d1ff; background: rgba(0,209,255,0.08); }
        @keyframes pulse-border { 0%, 100% { box-shadow: 0 0 0 0 rgba(124,58,237,0.5); } 50% { box-shadow: 0 0 0 6px rgba(124,58,237,0); } }
        .save-pulse { animation: pulse-border 1.4s ease-in-out infinite; }
        @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.45; } }
        @keyframes laser-sweep { 0% { top: -4px; opacity: 0; } 5% { opacity: 1; } 95% { opacity: 1; } 100% { top: 100%; opacity: 0; } }
        @keyframes laser-trail { 0% { top: -20px; opacity: 0; } 5% { opacity: 0.25; } 95% { opacity: 0.25; } 100% { top: 100%; opacity: 0; } }
        @keyframes scan-glow { 0%, 100% { box-shadow: var(--glass-shadow, 0 8px 32px rgba(0,0,0,0.4)), 0 0 0 1px rgba(0,255,80,0.15); } 50% { box-shadow: var(--glass-shadow, 0 8px 32px rgba(0,0,0,0.4)), 0 0 0 1.5px rgba(0,255,80,0.5), 0 0 24px rgba(0,255,80,0.18); } }
        .scanning-card { animation: scan-glow 1.8s ease-in-out infinite; overflow: hidden; }
        .laser-beam { position: absolute; left: 0; right: 0; top: -4px; height: 2px; pointer-events: none; z-index: 5; border-radius: 1px; background: linear-gradient(90deg, rgba(0,255,80,0) 0%, rgba(0,255,80,0.5) 20%, rgba(180,255,200,1) 50%, rgba(0,255,80,0.5) 80%, rgba(0,255,80,0) 100%); box-shadow: 0 0 6px 2px rgba(0,255,80,0.8), 0 0 18px rgba(0,255,80,0.4); animation: laser-sweep 1.6s linear infinite; }
        .laser-beam::after { content: ''; position: absolute; left: 0; right: 0; top: 2px; height: 16px; background: linear-gradient(180deg, rgba(0,255,80,0.18) 0%, transparent 100%); pointer-events: none; }
        .filter-pill { padding: 6px 16px; border-radius: 9999px; border: 1px solid rgba(255,255,255,0.08); background: var(--tm-filter-bg); color: var(--tm-card-text-muted); font-size: 12px; font-weight: 600; cursor: pointer; white-space: nowrap; transition: border-color 150ms ease, color 150ms ease, background 150ms ease; }
        .filter-pill:hover { border-color: rgba(255,255,255,0.14); color: #94a3b8; background: rgba(255,255,255,0.07); }
        .filter-pill-active { padding: 6px 16px; border-radius: 9999px; border: 1px solid rgba(0,209,255,0.45); background: rgba(0,209,255,0.12); color: #00d1ff; font-size: 12px; font-weight: 700; cursor: pointer; white-space: nowrap; transition: border-color 150ms ease; }
        .ghost-btn { padding: 5px 11px; border-radius: 7px; border: 1px solid var(--tm-modal-border); background: linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), var(--tm-inset); box-shadow: inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.18); cursor: pointer; font-size: 12px; color: var(--tm-text-muted); transition: border-color 160ms ease, color 160ms ease; }
        .ghost-btn:hover { border-color: var(--tm-input-border); color: var(--tm-text); }
        .delete-btn { padding: 5px 11px; border-radius: 7px; border: 1px solid rgba(220,38,38,.22); background: linear-gradient(145deg, rgba(220,38,38,.06) 0%, rgba(185,28,28,.02) 100%), var(--tm-inset); box-shadow: inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.18); cursor: pointer; font-size: 12px; color: #f87171; transition: border-color 160ms ease, box-shadow 160ms ease; }
        .delete-btn:hover { border-color: rgba(220,38,38,.42); box-shadow: 0 3px 10px rgba(220,38,38,.1), inset 0 1px 0 rgba(255,255,255,.03), inset 0 -1px 0 rgba(0,0,0,.2); }
        .discover-btn { padding: 5px 11px; border-radius: 7px; border: 1px solid rgba(167,139,250,.28); background: linear-gradient(145deg, rgba(167,139,250,.08) 0%, rgba(124,58,237,.04) 100%), var(--tm-inset); box-shadow: inset 0 1px 0 rgba(255,255,255,.05), inset 0 -1px 0 rgba(0,0,0,.2); cursor: pointer; font-size: 12px; font-weight: 600; color: #a78bfa; transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease; }
        .discover-btn:hover:not(:disabled) { border-color: rgba(167,139,250,.45); box-shadow: 0 4px 12px rgba(167,139,250,.1), inset 0 1px 0 rgba(255,255,255,.07), inset 0 -1px 0 rgba(0,0,0,.25); transform: translateY(-1px); }
        .discover-btn:disabled { opacity: 0.5; cursor: not-allowed; }
        .score-badge { display: inline-flex; align-items: center; gap: 5px; padding: 3px 9px; border-radius: 5px; font-size: 10px; font-weight: 700; letter-spacing: 0.04em; cursor: pointer; border: 1px solid; transition: filter 160ms ease, transform 160ms ease; box-shadow: inset 0 1px 0 rgba(255,255,255,.04), inset 0 -1px 0 rgba(0,0,0,.18); }
        .score-badge:hover { filter: brightness(1.15); transform: translateY(-1px); }
        .scanning-pill { display: inline-flex; align-items: center; gap: 5px; padding: 3px 9px; border-radius: 5px; font-size: 10px; font-weight: 700; letter-spacing: 0.04em; background: linear-gradient(145deg, rgba(167,139,250,.14), rgba(124,58,237,.06)); border: 1px solid rgba(167,139,250,.28); color: #c4b5fd; box-shadow: inset 0 1px 0 rgba(255,255,255,.04); animation: pulse 1.6s ease-in-out infinite; }
        .score-ring { position: relative; width: 80px; height: 80px; border-radius: 50%; display: flex; flex-direction: column; align-items: center; justify-content: center; flex-shrink: 0; box-shadow: 0 6px 20px var(--tm-inset-deep), inset 0 1px 0 rgba(255,255,255,.06), inset 0 -1px 0 rgba(0,0,0,.25); }
        .probe-row { display: flex; align-items: center; justify-content: space-between; padding: 7px 12px; border-radius: 8px; margin-bottom: 6px; }
        .finding-card { padding: 11px 13px; border-radius: 10px; margin-bottom: 7px; transition: border-color 160ms ease; }
        .finding-card:hover { border-color: rgba(132,158,190,.22) !important; }
        .btn-primary-scan { padding: 9px 22px; border-radius: 9px; border: 1px solid rgba(40,215,238,.48); background: linear-gradient(180deg, rgba(40,215,238,.22) 0%, rgba(24,197,223,.12) 100%), var(--tm-inset); box-shadow: 0 6px 18px rgba(40,215,238,.12), inset 0 1px 0 rgba(255,255,255,.10), inset 0 -1px 0 rgba(0,0,0,.24); color: #28d7ee; font-size: 14px; font-weight: 700; cursor: pointer; letter-spacing: 0.01em; transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease; display: flex; align-items: center; gap: 7px; }
        .btn-primary-scan:hover:not(:disabled) { border-color: rgba(40,215,238,.70); box-shadow: 0 8px 24px rgba(40,215,238,.18), inset 0 1px 0 rgba(255,255,255,.14), inset 0 -1px 0 var(--tm-inset-deep); transform: translateY(-1px); }
        .btn-primary-scan:disabled { opacity: 0.5; cursor: not-allowed; }
        @media (prefers-reduced-motion: reduce) { .glass-card { transition: none !important; } .scanning-pill { animation: none !important; } }
      `}</style>

      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1, background: 'var(--tm-bg)' }}>

          {/* Page header */}
          <div style={{ padding: '40px 32px 24px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div>
              <h2 style={{ fontSize: '40px', fontWeight: 800, color: '#fff', margin: '0 0 6px 0', letterSpacing: '-0.03em', lineHeight: 1.1 }}>Agents</h2>
              <p style={{ fontSize: '14px', color: 'var(--tm-card-text-muted)', margin: 0 }}>Manage A2A (Agent-to-Agent) orchestrators and node connectors.</p>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
              <button onClick={() => router.push('/admin/agents/builder')} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '12px 24px', borderRadius: '8px', border: '1px solid rgba(99,102,241,0.5)', cursor: 'pointer', background: 'rgba(99,102,241,0.1)', color: '#a5b4fc', fontSize: '14px', fontWeight: 700, transition: 'box-shadow 200ms ease, transform 200ms ease' }}>
                Build Visually
              </button>
              <button onClick={openCreate} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '12px 24px', borderRadius: '8px', border: 'none', cursor: 'pointer', background: '#00d1ff', color: '#000', fontSize: '14px', fontWeight: 700, boxShadow: '0 0 20px rgba(0,209,255,0.4)', transition: 'box-shadow 200ms ease, transform 200ms ease' }}>
                <span style={{ fontSize: '18px', lineHeight: 1 }}>+</span>
                Deploy New Agent
              </button>
            </div>
          </div>

          {/* Draft definitions section */}
          {draftDefinitions.length > 0 && (
            <div style={{ padding: '0 32px 28px' }}>
              <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-card-text-muted)', letterSpacing: '0.1em', textTransform: 'uppercase', marginBottom: '12px' }}>Drafts in builder</div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: '12px' }}>
                {draftDefinitions.map(d => (
                  <div key={d.id} style={{ display: 'flex', alignItems: 'center', gap: '12px', background: 'rgba(99,102,241,0.07)', border: '1px solid rgba(99,102,241,0.25)', borderRadius: '10px', padding: '10px 16px' }}>
                    <span style={{ fontSize: '24px' }}>🤖</span>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ color: '#fff', fontWeight: 600, fontSize: '13px' }}>{d.definition?.agent_root?.display_name || d.agent_slug}</div>
                      <div style={{ color: 'var(--tm-card-text-muted)', fontSize: '11px' }}>{d.agent_slug} · rev {d.revision}</div>
                    </div>
                    <button onClick={() => router.push(`/admin/agents/builder?id=${d.id}`)} style={{ background: 'rgba(99,102,241,0.15)', border: '1px solid rgba(99,102,241,0.4)', color: '#a5b4fc', borderRadius: '6px', padding: '5px 12px', fontSize: '12px', fontWeight: 600, cursor: 'pointer' }}>
                      Open Builder
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Filter bar */}
          <div style={{ padding: '0 32px 28px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', background: 'var(--tm-filter-bg)', border: '1px solid var(--tm-filter-border)', borderRadius: '12px', padding: '10px 16px' }}>
              <div style={{ position: 'relative', flex: '0 0 240px' }}>
                <span style={{ position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', color: 'var(--tm-card-text-muted)', fontSize: '14px', pointerEvents: 'none' }}>🔍</span>
                <input type="text" placeholder="Search agents…" value={searchTerm} onChange={(e) => setSearchTerm(e.target.value)} style={{ width: '100%', padding: '7px 12px 7px 32px', borderRadius: '8px', boxSizing: 'border-box', background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-card-border)', color: 'var(--tm-card-text)', fontSize: '13px', outline: 'none' }} />
              </div>
              <div style={{ width: '1px', height: '20px', background: 'rgba(255,255,255,0.08)', flexShrink: 0 }} />
              {CATEGORY_PILLS.map((cat) => (
                <button key={cat} onClick={() => setActiveCategory(cat)} className={activeCategory === cat ? 'filter-pill-active' : 'filter-pill'}>{cat}</button>
              ))}
            </div>
          </div>

          {/* Card grid */}
          {(() => {
            const isFiltering = searchTerm.trim() !== '' || activeCategory !== 'All';
            if (isFiltering) {
              return (
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '24px', padding: '0 32px 48px' }}>
                  {loading && <div style={{ gridColumn: '1 / -1', padding: '80px', textAlign: 'center', color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>Loading agents…</div>}
                  {!loading && filteredAgents.length === 0 && <div style={{ gridColumn: '1 / -1', padding: '60px', textAlign: 'center', color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>No agents match your filter</div>}
                  {!loading && filteredAgents.map((agent) => renderAgentCard(agent))}
                  {!loading && agents.length > 0 && <DeployCard onClick={openCreate} />}
                </div>
              );
            }

            return (
              <div style={{ padding: '0 32px 48px' }}>
                {loading && <div style={{ padding: '80px', textAlign: 'center', color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>Loading agents…</div>}
                {!loading && agents.length === 0 && <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '24px' }}><DeployCard onClick={openCreate} /></div>}
                {!loading && agents.length > 0 && (() => {
                  const folderedAgentIds = new Set(folderState.folders.flatMap(f => f.agentIds));
                  const ungroupedAgents = agents.filter(a => !folderedAgentIds.has(a.id));
                  const collapsedFolders = folderState.folders.filter(f => f.collapsed);
                  const expandedFolders = folderState.folders.filter(f => !f.collapsed);

                  return (
                    <>
                      {collapsedFolders.length > 0 && (
                        <ChromaGrid radius={420} damping={0.09} fadeOutMs={800} style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '16px', marginBottom: '24px' }}>
                          {collapsedFolders.map(folder => {
                            const folderAgents = folder.agentIds.map(id => agents.find(a => a.id === id)).filter(Boolean) as Agent[];
                            return (
                              <FolderHeader key={folder.id} folder={folder} folderAgents={folderAgents} count={folderAgents.length} isDragOver={dragOverId === `folder:${folder.id}`} onToggleCollapse={() => toggleFolderCollapse(folder.id)} onRename={(name) => renameFolderInline(folder.id, name)} onDragOver={(e) => { e.preventDefault(); setDragOverId(`folder:${folder.id}`); }} onDragLeave={() => setDragOverId(null)} onDrop={(e) => { e.preventDefault(); setDragOverId(null); const id = e.dataTransfer.getData('agentId'); if (id) handleDropOntoFolder(id, folder.id); }} />
                            );
                          })}
                        </ChromaGrid>
                      )}

                      {expandedFolders.map(folder => {
                        const folderAgents = folder.agentIds.map(id => agents.find(a => a.id === id)).filter(Boolean) as Agent[];
                        return (
                          <div key={folder.id} style={{ marginBottom: '24px' }}>
                            <FolderHeader folder={folder} folderAgents={folderAgents} count={folderAgents.length} isDragOver={dragOverId === `folder:${folder.id}`} onToggleCollapse={() => toggleFolderCollapse(folder.id)} onRename={(name) => renameFolderInline(folder.id, name)} onDragOver={(e) => { e.preventDefault(); setDragOverId(`folder:${folder.id}`); }} onDragLeave={() => setDragOverId(null)} onDrop={(e) => { e.preventDefault(); setDragOverId(null); const id = e.dataTransfer.getData('agentId'); if (id) handleDropOntoFolder(id, folder.id); }} />
                            <ChromaGrid radius={420} damping={0.09} fadeOutMs={800} style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '16px', padding: '16px', background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.06)', borderTop: 'none', borderRadius: '0 0 12px 12px' }}>
                              {folderAgents.map(agent => renderAgentCard(agent, {
                                isDragOver: dragOverId === agent.id,
                                onDragStart: (e) => { e.dataTransfer.setData('agentId', agent.id); setDragOverId(null); },
                                onDragOver: (e) => { e.preventDefault(); setDragOverId(agent.id); },
                                onDrop: (e) => { e.preventDefault(); setDragOverId(null); const id = e.dataTransfer.getData('agentId'); if (id) handleAgentDrop(id, agent.id); },
                                onRemoveFromFolder: () => removeAgentFromFolder(agent.id),
                              }))}
                            </ChromaGrid>
                          </div>
                        );
                      })}

                      <ChromaGrid radius={420} damping={0.09} fadeOutMs={800}
                        style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '24px', transition: 'background 150ms ease, box-shadow 150ms ease', ...(dragOverId === 'ungrouped' ? { background: 'rgba(0,209,255,0.04)', boxShadow: 'inset 0 0 0 2px rgba(0,209,255,0.25)', borderRadius: '12px' } : {}) }}
                        onDragOver={(e: React.DragEvent) => { e.preventDefault(); setDragOverId('ungrouped'); }}
                        onDragLeave={(e: React.DragEvent) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragOverId(null); }}
                        onDrop={(e: React.DragEvent) => { e.preventDefault(); setDragOverId(null); const id = e.dataTransfer.getData('agentId'); if (id && folderedAgentIds.has(id)) removeAgentFromFolder(id); }}
                      >
                        {ungroupedAgents.map(agent => renderAgentCard(agent, {
                          isDragOver: dragOverId === agent.id,
                          onDragStart: (e) => { e.dataTransfer.setData('agentId', agent.id); setDragOverId(null); },
                          onDragOver: (e) => { e.preventDefault(); setDragOverId(agent.id); },
                          onDrop: (e) => { e.preventDefault(); setDragOverId(null); const id = e.dataTransfer.getData('agentId'); if (id) handleAgentDrop(id, agent.id); },
                        }))}
                        <DeployCard onClick={openCreate} />
                      </ChromaGrid>
                    </>
                  );
                })()}
              </div>
            );
          })()}
        </main>

        <AgentModals
          showModal={showModal}
          editing={editing}
          form={form}
          saving={saving}
          error={error}
          discovering={discovering}
          discoverError={discoverError}
          onCloseModal={() => setShowModal(false)}
          onFieldChange={(k, v) => setForm((f) => ({ ...f, [k]: v }))}
          onDiscover={handleDiscover}
          onSave={handleSave}
          discoverPopup={discoverPopup}
          orchestrators={orchestrators}
          applyingDiscover={applyingDiscover}
          onCloseDiscoverPopup={() => setDiscoverPopup(null)}
          onApplyDiscover={handleApplyDiscover}
          scanModal={scanModal}
          scanResults={scanResults}
          onCloseScanModal={() => setScanModal(null)}
          onRescan={handleScan}
          deleteTarget={deleteTarget}
          onCloseDelete={() => setDeleteTarget(null)}
          onConfirmDelete={handleDelete}
          pendingFolder={pendingFolder}
          folderNameInput={folderNameInput}
          onFolderNameChange={setFolderNameInput}
          onCloseFolderPrompt={() => { setPendingFolder(null); setFolderNameInput(''); }}
          onConfirmCreateFolder={confirmCreateFolder}
        />
      </div>
    </AuthGuard>
  );
}
