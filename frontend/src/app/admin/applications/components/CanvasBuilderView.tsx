'use client';
import '@xyflow/react/dist/style.css';
import { useState, useEffect, useMemo, type DragEvent } from 'react';
import {
  ReactFlowProvider,
  addEdge,
  useNodesState,
  useEdgesState,
  useReactFlow,
  type Node,
  type Edge,
  type Connection,
} from '@xyflow/react';
import { themApi, type Application, type Agent, type AppDefinition, type AppDefinitionDoc, type ComponentDefinitionSummary, type ValidationReport, type MCPServer } from '@/lib/api';
import type { OrchNodeData, AgentNodeData, MwNodeData, EpNodeData, LogoState } from '../types';
import { C, EP_META } from '../constants';
import { agentIconForLibrary, applyDagreLayout, canvasToDoc, docToCanvas, genInstanceId } from './CanvasHelpers';
import { computeLogoState } from './CanvasLogo';
import { CanvasInnerWithDrop, validateConnection } from './CanvasInner';
import { CanvasNodePropertiesPanel } from './cbv/CanvasNodePropertiesPanel';

// ── CanvasBuilderView (V2) ────────────────────────────────────────────────────
export function CanvasBuilderView({
  app,
  agents,
  onBack,
  onAppUpdated,
}: {
  app: Application;
  agents: Agent[];
  onBack: () => void;
  onAppUpdated?: (updated: Application) => void;
}) {
  // Map agent slug → real icon (used for palette and canvas nodes)
  const agentIconBySlug = useMemo(() => {
    const m = new Map<string, string>();
    agents.forEach(a => { m.set(a.slug, a.icon || agentIconForLibrary(a)); });
    return m;
  }, [agents]);
  // State (mirroring DefinitionView)
  const [defs, setDefs] = useState<AppDefinition[]>([]);
  const [activeDef, setActiveDef] = useState<AppDefinition | null>(null);
  const [draft, setDraft] = useState<AppDefinitionDoc | null>(null);
  const [isDirty, setIsDirty] = useState(false);
  const [componentDefs, setComponentDefs] = useState<ComponentDefinitionSummary[]>([]);
  const [validating, setValidating] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [validationReport, setValidationReport] = useState<ValidationReport | null>(null);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
  const [logoResult, setLogoResult] = useState<'none' | 'valid' | 'invalid' | 'warn'>('none');
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);
  const [configPanelText, setConfigPanelText] = useState('{}');
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({});
  const [configPanelErr, setConfigPanelErr] = useState(false);
  const [llmTestState, setLlmTestState] = useState<Record<string, { loading: boolean; ok?: boolean; latency?: number; error?: string }>>({});
  const [providerKeyStatuses, setProviderKeyStatuses] = useState<Record<string, boolean>>({});
  const [propsPanelWidth, setPropsPanelWidth] = useState(280);
  const [compPanelWidth, setCompPanelWidth] = useState(260);
  const [showRepublishModal, setShowRepublishModal] = useState(false);
  const [availableMCPServers, setAvailableMCPServers] = useState<MCPServer[]>([]);
  const [mcpExpanded, setMcpExpanded] = useState<Record<string, boolean>>({});

  function startCompPanelResize(e: React.MouseEvent) {
    e.preventDefault();
    const startX = e.clientX;
    const startW = compPanelWidth;
    function onMove(ev: MouseEvent) {
      setCompPanelWidth(Math.min(500, Math.max(180, startW + (ev.clientX - startX))));
    }
    function onUp() {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  function startPropsPanelResize(e: React.MouseEvent) {
    e.preventDefault();
    const startX = e.clientX;
    const startW = propsPanelWidth;
    function onMove(ev: MouseEvent) {
      const delta = startX - ev.clientX;
      setPropsPanelWidth(Math.min(600, Math.max(200, startW + delta)));
    }
    function onUp() {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  const refreshProviderKeys = () => {
    themApi.getProviderKeys(app.id)
      .then(keys => {
        const m: Record<string, boolean> = {};
        keys.forEach(k => { m[k.provider] = k.key_set; });
        setProviderKeyStatuses(m);
      })
      .catch(() => {});
  };
  useEffect(() => { refreshProviderKeys(); }, [app.id]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { themApi.listMCPServers().then(setAvailableMCPServers).catch(() => {}); }, []);

  // Canvas state
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [layoutDir, setLayoutDir] = useState<'TB' | 'LR'>('TB');


  useEffect(() => {
    if (selectedNode?.type === 'agent' || selectedNode?.type === 'middleware') {
      setConfigPanelText(JSON.stringify((selectedNode.data as unknown as AgentNodeData | MwNodeData).config, null, 2));
      setConfigPanelErr(false);
    }
    setLlmTestState({});
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedNode?.id, selectedNode?.type]);

  const fieldStyle: React.CSSProperties = {
    width: '100%', padding: '10px 12px', borderRadius: 8,
    borderWidth: '1px', borderStyle: 'solid', borderColor: 'rgba(255,255,255,0.12)',
    background: 'rgba(255,255,255,0.05)',
    color: C.text, fontSize: 14, outline: 'none', boxSizing: 'border-box',
  };

  function showToast(msg: string, ok: boolean) {
    setToast({ msg, ok });
    setTimeout(() => setToast(null), 3000);
  }

  function setEpConfig(instanceId: string, patch: Record<string, unknown>, remove: string[] = []) {
    setNodes(ns => ns.map(n => {
      if (n.id !== instanceId) return n;
      const cfg = { ...(n.data as unknown as EpNodeData).config, ...patch };
      for (const k of remove) delete cfg[k];
      return { ...n, data: { ...n.data, config: cfg } };
    }));
    setIsDirty(true);
    setLogoResult('none');
  }

  async function testOrchLlm(epId: string, provider: string, model: string) {
    if (!provider || !model) return;
    setLlmTestState(s => ({ ...s, [epId]: { loading: true } }));
    try {
      const res = await themApi.testAppLlm(app.id, provider, model);
      setLlmTestState(s => ({ ...s, [epId]: { loading: false, ok: res.ok, latency: res.latency_ms, error: res.error } }));
    } catch (e: unknown) {
      setLlmTestState(s => ({ ...s, [epId]: { loading: false, ok: false, error: e instanceof Error ? e.message : 'Request failed' } }));
    }
  }

  function loadDef(def: AppDefinition) {
    setActiveDef(def);
    setDraft(JSON.parse(JSON.stringify(def.definition)));
    setIsDirty(false);
    setValidationReport(null);
    setSelectedNode(null);
    setLogoResult('none');
    const { nodes: n, edges: e } = docToCanvas(def.definition, componentDefs, {}, agentIconBySlug);
    setNodes(n);
    setEdges(e);
  }

  async function reloadDefs(selectId?: string) {
    try {
      const list = await themApi.listDefinitions(app.id);
      setDefs(list);
      if (selectId) {
        const found = list.find(d => d.id === selectId);
        if (found) { loadDef(found); return; }
      }
      const drafts = list.filter(d => d.status === 'draft');
      if (drafts.length > 0) {
        loadDef(drafts[0]);
      } else if (list.length > 0) {
        // Only published defs exist — load the latest to seed a new draft
        // list is ORDER BY revision DESC so index 0 is the newest
        const latest = list[0];
        loadDef(latest);
        // Auto-create a working draft seeded from the published definition
        const seedDoc: AppDefinitionDoc = JSON.parse(JSON.stringify(latest.definition));
        const seedWithName: AppDefinitionDoc = { ...seedDoc, name: app.name };
        try {
          const res = await themApi.createDefinition(app.id, { definition: seedWithName });
          const updated = await themApi.listDefinitions(app.id);
          setDefs(updated);
          const newDef = updated.find(d => d.id === res.id);
          if (newDef) loadDef(newDef);
        } catch { /* keep showing published def if draft creation fails */ }
      } else {
        setActiveDef(null); setDraft(null); setNodes([]); setEdges([]);
      }
    } catch {
      showToast('Failed to load definitions', false);
    }
  }

  useEffect(() => {
    reloadDefs();
    themApi.listComponentDefinitions().then(setComponentDefs).catch(() => {});
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app.id]);

  async function newDraft() {
    // Seed from current canvas so edits continue from the published state.
    // Fall back to empty doc only if there is no existing canvas.
    const seedDoc: AppDefinitionDoc = draft ?? {
      schema_version: 2, name: app.name,
      components: [], entry_points: [], connections: [],
    };
    const seedWithName: AppDefinitionDoc = { ...seedDoc, name: app.name };
    try {
      const res = await themApi.createDefinition(app.id, { definition: seedWithName });
      await reloadDefs(res.id);
      showToast('New draft created', true);
    } catch {
      showToast('Failed to create draft', false);
    }
  }

  async function saveDraft() {
    if (!activeDef) return;
    setSaving(true);
    try {
      const doc = canvasToDoc(nodes, edges, draft?.name ?? app.name);
      await themApi.updateDefinition(app.id, activeDef.id, { definition: doc });
      setDraft(doc);
      setIsDirty(false);
      showToast('Saved', true);
    } catch {
      showToast('Save failed', false);
    } finally {
      setSaving(false);
    }
  }

  async function validate() {
    if (!activeDef) return;
    if (isDirty) await saveDraft();
    setValidating(true);
    setLogoResult('none');
    try {
      const report = await themApi.validateDefinition(app.id, activeDef.id);
      setValidationReport(report);
      const errorIds = new Set((report.errors ?? []).map(e => e.instance_id).filter((x): x is string => !!x));
      setNodes(ns => ns.map(n => ({
        ...n,
        data: { ...n.data, _error: errorIds.has(n.id), _errorMsg: (report.errors ?? []).find(e => e.instance_id === n.id)?.message }
      })));
      const result = report.valid ? 'valid' : 'invalid';
      setLogoResult(result);
      showToast(report.valid ? 'Valid ✓' : `${report.errors?.length ?? 0} error(s)`, report.valid);
      if (report.valid) setTimeout(() => setLogoResult('none'), 1800);
    } catch {
      showToast('Validation failed', false);
      setLogoResult('none');
    } finally {
      setValidating(false);
    }
  }

  async function publish() {
    if (!activeDef) return;
    setPublishing(true);
    try {
      if (isDirty) await saveDraft();
      const report = await themApi.validateDefinition(app.id, activeDef.id);
      setValidationReport(report);
      const errorIds = new Set((report.errors ?? []).map(e => e.instance_id).filter((x): x is string => !!x));
      setNodes(ns => ns.map(n => ({
        ...n,
        data: { ...n.data, _error: errorIds.has(n.id), _errorMsg: (report.errors ?? []).find(e => e.instance_id === n.id)?.message }
      })));
      setLogoResult(report.valid ? 'valid' : 'invalid');
      if (!report.valid) {
        showToast(`${report.errors?.length ?? 1} validation error(s)`, false);
        return;
      }
      setTimeout(() => setLogoResult('none'), 1800);
      const res = await themApi.publishDefinition(app.id, activeDef.id);
      showToast(`Published revision ${res.revision}`, true);
      await reloadDefs();
      try {
        const freshApp = await themApi.getApplication(app.id);
        onAppUpdated?.(freshApp);
      } catch { onAppUpdated?.({ ...app }); }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Publish failed';
      showToast(msg, false);
    } finally {
      setPublishing(false);
    }
  }

  function handlePublishClick() {
    // If the app already has a published revision, warn before re-publishing
    if (app.active_revision != null) {
      setShowRepublishModal(true);
    } else {
      void publish();
    }
  }

  function handleConnect(conn: Connection) {
    const srcNode = nodes.find(n => n.id === conn.source);
    const tgtNode = nodes.find(n => n.id === conn.target);
    if (!srcNode || !tgtNode) return;
    const err = validateConnection(srcNode.type ?? '', tgtNode.type ?? '', conn.source ?? '', conn.target ?? '', edges);
    if (err) return;
    setEdges(es => addEdge({ ...conn, type: 'default' }, es));
    setIsDirty(true);
    setLogoResult('none');
  }

  function handleDropOnCanvas(e: DragEvent<HTMLDivElement>, rfInstance: ReturnType<typeof useReactFlow>) {
    e.preventDefault();
    const nodeType = e.dataTransfer.getData('nodeType');
    const rawData = e.dataTransfer.getData('nodeData');
    if (!nodeType || !rawData) return;
    let payload: { cd?: ComponentDefinitionSummary; protocol?: string };
    try { payload = JSON.parse(rawData); } catch { return; }
    const pos = rfInstance.screenToFlowPosition({ x: e.clientX, y: e.clientY });
    const existingIds = new Set(nodes.map(n => n.id));

    if (nodeType === 'orchestrator' && payload.cd) {
      const cd = payload.cd;
      const id = genInstanceId('orchestrator', cd.name, existingIds);
      const newNode: Node = { id, type: 'orchestrator', position: pos, data: { _kind: 'orchestrator', instance_id: id, display_name: cd.display_name, definition_ref: { kind: cd.kind, namespace: cd.namespace, name: cd.name, version: cd.version }, definition_id: cd.id, config: { max_iterations: 10, max_parallel_tools: 5, history_window: 20 } } as unknown as Record<string, unknown> };
      setNodes(ns => [...ns, newNode]);
    } else if (nodeType === 'agent' && payload.cd) {
      const cd = payload.cd;
      const id = genInstanceId('agent', cd.name, existingIds);
      const agentIcon = agentIconBySlug.get(cd.name);
      const newNode: Node = { id, type: 'agent', position: pos, data: { _kind: 'agent', instance_id: id, display_name: cd.display_name, description: cd.description ?? '', definition_ref: { kind: cd.kind, namespace: cd.namespace, name: cd.name, version: cd.version }, definition_id: cd.id, config: {}, icon: agentIcon } as unknown as Record<string, unknown> };
      setNodes(ns => [...ns, newNode]);
    } else if (nodeType === 'middleware' && payload.cd) {
      const cd = payload.cd;
      const id = genInstanceId('middleware', cd.name, existingIds);
      const newNode: Node = { id, type: 'middleware', position: pos, data: { _kind: 'middleware', instance_id: id, display_name: cd.display_name, definition_ref: { kind: cd.kind, namespace: cd.namespace, name: cd.name, version: cd.version }, definition_id: cd.id, config: {} } as unknown as Record<string, unknown> };
      setNodes(ns => [...ns, newNode]);
    } else if (nodeType === 'entryPoint' && payload.protocol) {
      const protocol = payload.protocol as EpNodeData['protocol'];
      const id = genInstanceId('ep', protocol, existingIds);
      const autoSlug = id.replace(/_/g, '-');
      const newNode: Node = { id, type: 'entryPoint', position: pos, data: { _kind: 'ep', instance_id: id, slug: autoSlug, protocol, label: EP_META[protocol]?.title ?? protocol, config: {} } as unknown as Record<string, unknown> };
      setNodes(ns => [...ns, newNode]);
    }
    setIsDirty(true);
    setLogoResult('none');
  }

  // Auto-save: trigger 3s after last canvas change, only when a draft is loaded
  const isLive = app.active_revision != null;
  const logoState = computeLogoState({ loaded: !!activeDef, isDirty, busy: validating || saving || publishing, lastResult: logoResult });

  const EP_MS_ICON_MAP: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg, overflow: 'hidden' }}>
      {/* Top bar */}
      <div style={{
        height: 56, flexShrink: 0, display: 'flex', alignItems: 'center', gap: 10,
        padding: '0 20px', borderBottom: `1px solid ${C.glassBorder}`,
        background: C.surface, position: 'sticky', top: 0, zIndex: 20,
      }}>
        <button onClick={onBack} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center', gap: 4, fontSize: 13 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 20 }}>arrow_back</span>
        </button>
        <span style={{ fontSize: 15, fontWeight: 700, color: C.text }}>{app.name}</span>
        {activeDef && (
          <span style={{
            padding: '2px 10px', borderRadius: 20, fontSize: 11, fontWeight: 700,
            background: isLive ? C.greenBg : 'rgba(208,188,255,0.12)',
            color: isLive ? C.green : C.purple,
            border: `1px solid ${isLive ? C.greenBorder : 'rgba(208,188,255,0.3)'}`,
          }}>
            {isLive ? `Rev ${app.active_revision} • live` : 'draft'}
          </span>
        )}
        <div style={{ flex: 1 }} />
        {activeDef && isDirty && (
          <button onClick={saveDraft} disabled={saving} style={{ padding: '7px 14px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 700, background: 'rgba(255,255,255,0.08)', color: C.text }}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        )}
        {activeDef && (
          <button
            onClick={handlePublishClick}
            disabled={publishing || saving || validating}
            style={{
              padding: '7px 16px', borderRadius: 8, cursor: publishing || saving || validating ? 'not-allowed' : 'pointer',
              fontSize: 12, fontWeight: 700,
              background: isLive ? 'rgba(245,158,11,0.15)' : C.greenBg,
              color: isLive ? '#f59e0b' : C.green,
              border: `1px solid ${isLive ? 'rgba(245,158,11,0.4)' : C.greenBorder}`,
              opacity: publishing || saving || validating ? 0.6 : 1,
            }}
          >
            {publishing ? 'Publishing…' : isLive ? 'Re-publish' : 'Publish'}
          </button>
        )}
      </div>

      {/* Validation errors banner */}
      {validationReport && !validationReport.valid && (
        <div style={{ background: C.errorBg, borderBottom: `1px solid rgba(255,180,171,0.3)`, padding: '10px 20px', flexShrink: 0 }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: C.error, marginBottom: 4 }}>Validation errors:</div>
          {(validationReport.errors ?? []).map((err, i) => (
            <div key={i} style={{ fontSize: 12, color: C.error }}>
              {err.instance_id ? `[${err.instance_id}] ` : ''}{err.message}
            </div>
          ))}
        </div>
      )}

      {/* Three-column canvas area */}
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>

        {/* Left: Component Palette */}
        <div style={{ width: compPanelWidth, flexShrink: 0, display: 'flex', position: 'relative' }}>
          <div className="comp-panel" style={{ flex: 1, background: 'rgba(0,0,0,0.2)', overflowY: 'auto', display: 'flex', flexDirection: 'column' }}>
          <div style={{ padding: '14px 16px 8px', fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: '0.08em', textTransform: 'uppercase' }}>Components</div>

          {/* Entry Points */}
          <div style={{ padding: '0 8px 12px' }}>
            <div style={{ fontSize: 11, color: C.textMuted, padding: '4px 8px', fontWeight: 600 }}>Entry Points</div>
            {(['websocket', 'sse', 'webrtc', 'a2a', 'voice'] as const).map(protocol => (
              <div
                key={protocol}
                draggable
                onDragStart={e => { e.dataTransfer.setData('nodeType', 'entryPoint'); e.dataTransfer.setData('nodeData', JSON.stringify({ protocol })); e.dataTransfer.effectAllowed = 'move'; }}
                style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: 8, cursor: 'grab', marginBottom: 2, background: 'rgba(0,209,255,0.04)', border: '1px solid rgba(0,209,255,0.12)' }}
              >
                <span className="material-symbols-outlined" style={{ fontSize: 16, color: '#00d1ff' }}>{EP_MS_ICON_MAP[protocol] ?? 'bolt'}</span>
                <span style={{ fontSize: 12, color: C.text, fontWeight: 500 }}>{protocol.charAt(0).toUpperCase() + protocol.slice(1)}</span>
              </div>
            ))}
          </div>

          {/* Component kinds */}
          {(['orchestrator', 'agent', 'middleware'] as const).map(kind => {
            const items = componentDefs.filter(cd => cd.kind === kind);
            if (items.length === 0) return null;
            const kindColor = kind === 'orchestrator' ? '99,102,241' : kind === 'agent' ? '74,222,128' : '245,158,11';
            const kindIconColor = kind === 'orchestrator' ? '#818cf8' : kind === 'agent' ? C.green : '#f59e0b';
            const defaultKindIcon = kind === 'orchestrator' ? 'hub' : kind === 'agent' ? 'smart_toy' : 'shield';
            return (
              <div key={kind} style={{ padding: '0 8px 12px' }}>
                <div style={{ fontSize: 11, color: C.textMuted, padding: '4px 8px', fontWeight: 600, textTransform: 'capitalize' }}>{kind}s</div>
                {items.map(cd => {
                  const itemIcon = kind === 'agent' ? (agentIconBySlug.get(cd.name) ?? defaultKindIcon) : kind === 'middleware' ? (cd.name.includes('guard') ? 'shield' : 'bolt') : defaultKindIcon;
                  return (
                    <div
                      key={cd.id}
                      draggable
                      onDragStart={e => { e.dataTransfer.setData('nodeType', kind); e.dataTransfer.setData('nodeData', JSON.stringify({ cd })); e.dataTransfer.effectAllowed = 'move'; }}
                      style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: 8, cursor: 'grab', marginBottom: 2, background: `rgba(${kindColor},0.04)`, border: `1px solid rgba(${kindColor},0.12)` }}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 16, color: kindIconColor }}>{itemIcon}</span>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: C.text, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cd.display_name}</div>
                        {cd.description && <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cd.description}</div>}
                      </div>
                    </div>
                  );
                })}
              </div>
            );
          })}

          {!activeDef && (
            <div style={{ padding: '20px 16px', textAlign: 'center' }}>
              <div style={{ fontSize: 12, color: C.textMuted, marginBottom: 10 }}>No definition loaded</div>
              <button onClick={newDraft} style={{ padding: '8px 14px', borderRadius: 8, border: 'none', background: C.cyan, color: '#021520', fontWeight: 700, cursor: 'pointer', fontSize: 12 }}>
                Create First Definition
              </button>
            </div>
          )}
          </div>
          {/* Resize grip */}
          <div
            onMouseDown={startCompPanelResize}
            style={{
              width: 10, flexShrink: 0, cursor: 'col-resize',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              background: 'rgba(0,0,0,0.25)', borderRight: '1px solid rgba(255,255,255,0.05)',
            }}
            onMouseEnter={e => { e.currentTarget.style.background = 'rgba(0,0,0,0.45)'; }}
            onMouseLeave={e => { e.currentTarget.style.background = 'rgba(0,0,0,0.25)'; }}
          >
            <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
              {[0,1,2,3].map(i => <div key={i} style={{ width: 2, height: 2, borderRadius: '50%', background: 'rgba(255,255,255,0.2)' }} />)}
            </div>
          </div>
        </div>

        {/* Center: ReactFlow canvas */}
        <div style={{ flex: 1, position: 'relative', height: 'calc(100vh - 56px)', overflow: 'hidden' }}>
          {activeDef ? (
            <ReactFlowProvider>
              <CanvasInnerWithDrop
                nodes={nodes}
                edges={edges}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onConnect={handleConnect}
                onDropWithInstance={handleDropOnCanvas}
                onDragOver={e => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; }}
                selectedNode={selectedNode}
                setSelectedNode={setSelectedNode}
                onUpdateNode={(id, patch) => { setNodes(ns => ns.map(n => n.id === id ? { ...n, data: { ...n.data, ...patch } } : n)); setIsDirty(true); setLogoResult('none'); }}
                onDeleteEdge={edgeId => { setEdges(es => es.filter(e => e.id !== edgeId)); setIsDirty(true); }}
                onAutoLayout={() => { setNodes(ns => applyDagreLayout([...ns], edges, layoutDir)); }}
                onToggleLayout={() => {
                  const next: 'TB' | 'LR' = layoutDir === 'TB' ? 'LR' : 'TB';
                  setLayoutDir(next);
                  setNodes(ns => applyDagreLayout([...ns], edges, next));
                }}
                layoutDir={layoutDir}
                onNodesDelete={() => { setIsDirty(true); setLogoResult('none'); setSelectedNode(null); }}
                logoState={logoState}
                advisorOpen={false}
                onAdvisorOpen={() => {}}
              />
            </ReactFlowProvider>
          ) : (
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%', color: C.textMuted }}>
              <div style={{ textAlign: 'center' }}>
                <span className="material-symbols-outlined" style={{ fontSize: 48, display: 'block', marginBottom: 12, opacity: 0.4 }}>account_tree</span>
                <div style={{ fontSize: 15, marginBottom: 16 }}>No definition loaded</div>
                <button onClick={newDraft} style={{ padding: '10px 20px', borderRadius: 8, border: 'none', background: C.cyan, color: '#021520', fontWeight: 700, cursor: 'pointer' }}>
                  Create First Definition
                </button>
              </div>
            </div>
          )}
        </div>

        {/* Right: Properties panel */}
        <div style={{ width: propsPanelWidth, flexShrink: 0, display: 'flex', flexDirection: 'row' }}>
          {/* Drag handle */}
          <div
            onMouseDown={startPropsPanelResize}
            style={{ width: 4, flexShrink: 0, cursor: 'col-resize', background: 'rgba(255,255,255,0.06)', transition: 'background 0.15s' }}
            onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.background = 'rgba(0,209,255,0.35)'; }}
            onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = 'rgba(255,255,255,0.06)'; }}
          />
          <div style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column', background: 'rgba(0,0,0,0.15)' }} onKeyDown={e => e.stopPropagation()}>
            <div style={{ padding: '14px 16px 8px', fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: '0.08em', textTransform: 'uppercase', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>Properties</div>
            <CanvasNodePropertiesPanel
              selectedNode={selectedNode}
              nodes={nodes}
              edges={edges}
              openSections={openSections}
              setOpenSections={setOpenSections}
              availableMCPServers={availableMCPServers}
              mcpExpanded={mcpExpanded}
              setMcpExpanded={setMcpExpanded}
              configPanelText={configPanelText}
              setConfigPanelText={setConfigPanelText}
              configPanelErr={configPanelErr}
              setConfigPanelErr={setConfigPanelErr}
              setNodes={setNodes}
              setIsDirty={setIsDirty}
              setLogoResult={setLogoResult}
              showToast={showToast}
              setEpConfig={setEpConfig}
            />
          </div>
        </div>
      </div>

      {/* Toast */}
      {toast && (
        <div style={{
          position: 'fixed', bottom: 24, left: '50%', transform: 'translateX(-50%)',
          background: toast.ok ? C.greenBg : C.errorBg, border: `1px solid ${toast.ok ? C.greenBorder : 'rgba(255,180,171,0.3)'}`,
          color: toast.ok ? C.green : C.error, borderRadius: 10, padding: '10px 20px', fontSize: 13, fontWeight: 600,
          zIndex: 9999, boxShadow: '0 8px 32px rgba(0,0,0,0.4)',
        }}>
          {toast.msg}
        </div>
      )}

      {/* Re-publish warning modal */}
      {showRepublishModal && (
        <div style={{
          position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.65)', display: 'flex',
          alignItems: 'center', justifyContent: 'center', zIndex: 10000,
        }}>
          <div style={{
            background: C.surface, border: `1px solid rgba(245,158,11,0.4)`, borderRadius: 14,
            padding: '28px 32px', maxWidth: 420, width: '90%', boxShadow: '0 24px 64px rgba(0,0,0,0.6)',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 24, color: '#f59e0b' }}>warning</span>
              <span style={{ fontSize: 16, fontWeight: 700, color: C.text }}>Re-publish live app?</span>
            </div>
            <p style={{ fontSize: 13, color: C.textMuted, lineHeight: 1.6, margin: '0 0 22px' }}>
              This app is currently live (revision {app.active_revision}). Re-publishing will apply your changes immediately and may affect active users.
            </p>
            <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
              <button
                onClick={() => setShowRepublishModal(false)}
                style={{ padding: '8px 18px', borderRadius: 8, border: '1px solid rgba(255,255,255,0.12)', background: 'rgba(255,255,255,0.06)', color: C.text, cursor: 'pointer', fontSize: 13, fontWeight: 600 }}
              >
                Cancel
              </button>
              <button
                onClick={() => { setShowRepublishModal(false); void publish(); }}
                style={{ padding: '8px 18px', borderRadius: 8, border: '1px solid rgba(245,158,11,0.4)', background: 'rgba(245,158,11,0.15)', color: '#f59e0b', cursor: 'pointer', fontSize: 13, fontWeight: 700 }}
              >
                Re-publish
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
