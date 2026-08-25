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
import { themApi, type Application, type Agent, type AppDefinition, type AppDefinitionDoc, type ComponentDefinitionSummary, type ValidationReport, type MCPServer, type MCPServerAttachment } from '@/lib/api';
import type { OrchNodeData, AgentNodeData, MwNodeData, EpNodeData, LogoState } from '../types';
import { C, EP_META, MODELS_BY_PROVIDER } from '../constants';
import { agentIconForLibrary, applyDagreLayout, canvasToDoc, docToCanvas, genInstanceId } from './CanvasHelpers';
import { computeLogoState } from './CanvasLogo';
import { CanvasInnerWithDrop, validateConnection, analyzeChain, styledEdges, EpPickerModal } from './CanvasInner';

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
    } catch {
      showToast('Publish failed', false);
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

  function renderPropertiesPanel() {
    // ── helpers ──────────────────────────────────────────────────────────────
    const sectionHdrStyle: React.CSSProperties = {
      fontSize: 11, fontWeight: 700, color: C.textMuted,
      textTransform: 'uppercase', letterSpacing: '0.06em',
    };
    const chipStyle: React.CSSProperties = {
      fontSize: 12, padding: '4px 10px', borderRadius: 20,
      background: 'rgba(255,255,255,0.06)', color: C.textMuted,
      fontFamily: 'JetBrains Mono, monospace', display: 'inline-block',
    };
    const selectStyle: React.CSSProperties = {
      ...fieldStyle, padding: '7px 10px', fontSize: 13, cursor: 'pointer',
    };

    function isSectionOpen(id: string, def: boolean) {
      return openSections[id] ?? def;
    }
    function SectionHeader({ id, label, defaultOpen }: { id: string; label: string; defaultOpen: boolean }) {
      const open = isSectionOpen(id, defaultOpen);
      return (
        <button
          onClick={() => setOpenSections(s => ({ ...s, [id]: !open }))}
          style={{ display: 'flex', alignItems: 'center', gap: 6, background: 'none', border: 'none', cursor: 'pointer', padding: '2px 0', width: '100%', textAlign: 'left' }}
        >
          <span style={{ ...sectionHdrStyle, flex: 1 }}>{label}</span>
          <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 0.15s' }}>expand_more</span>
        </button>
      );
    }

    if (!selectedNode) {
      return (
        <div style={{ padding: 20, color: C.textMuted, fontSize: 13, fontStyle: 'italic' }}>
          Select a node to configure properties
        </div>
      );
    }

    // ── orchestrator ─────────────────────────────────────────────────────────
    if (selectedNode.type === 'orchestrator') {
      const liveOrchNode = nodes.find(n => n.id === selectedNode.id);
      const d = (liveOrchNode?.data ?? selectedNode.data) as unknown as OrchNodeData;
      const cfg = d.config ?? {};
      const epLlm = (cfg.ep_llm ?? {}) as Record<string, { provider?: string; model?: string }>;
      const connectedEps = edges
        .filter(e => e.target === selectedNode.id)
        .map(e => nodes.find(n => n.id === e.source))
        .filter(n => n?.type === 'entryPoint');
      const hasVoice = connectedEps.some(n => (n?.data as unknown as EpNodeData)?.protocol === 'voice');
      const sttProvider = (cfg.stt_provider as string) ?? 'openai';
      const ttsProvider = (cfg.tts_provider as string) ?? 'openai';
      const ttsVoice = (cfg.tts_voice as string) ?? 'alloy';

      function setOrchConfig(patch: Record<string, unknown>) {
        setNodes(ns => ns.map(n => n.id === selectedNode!.id
          ? { ...n, data: { ...n.data, config: { ...(n.data as unknown as OrchNodeData).config, ...patch } } }
          : n));
        setIsDirty(true);
        setLogoResult('none');
      }

      function setEpLlm(epInstanceId: string, patch: { provider?: string; model?: string }) {
        const current = ((cfg.ep_llm ?? {}) as Record<string, { provider?: string; model?: string }>)[epInstanceId] ?? {};
        const updated = { ...current, ...patch };
        setOrchConfig({ ep_llm: { ...epLlm, [epInstanceId]: updated } });
      }

      return (
        <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 12, overflowY: 'auto' }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: C.purple, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>Orchestrator</div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Instance ID</label>
            <div style={chipStyle}>{d.instance_id}</div>
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Display Name</label>
            <input
              style={fieldStyle}
              value={d.display_name ?? ''}
              onChange={e => { setNodes(ns => ns.map(n => n.id === selectedNode!.id ? { ...n, data: { ...n.data, display_name: e.target.value } } : n)); setIsDirty(true); setLogoResult('none'); }}
            />
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>System Prompt</label>
            <textarea
              style={{ ...fieldStyle, minHeight: 80, resize: 'vertical' }}
              value={(cfg.system_prompt as string) ?? ''}
              onChange={e => setOrchConfig({ system_prompt: e.target.value })}
            />
          </div>

          {/* LLM per-EP config */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
            <SectionHeader id="orch-llm" label="LLM" defaultOpen={true} />
            {isSectionOpen('orch-llm', true) && connectedEps.length === 0 && (
              <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic', marginTop: 6 }}>Connect an entry point to configure LLM</div>
            )}
            {isSectionOpen('orch-llm', true) && connectedEps.map(epNode => {
              if (!epNode) return null;
              const ep = epNode.data as unknown as EpNodeData;
              const epCfg = epLlm[ep.instance_id] ?? {};
              const currentProvider = (epCfg.provider as string) ?? '';
              const currentModel = (epCfg.model as string) ?? '';
              const testKey = ep.instance_id;
              const testState = llmTestState[testKey] ?? {};
              const availableModels = currentProvider ? (MODELS_BY_PROVIDER[currentProvider] ?? []) : [];
              const keySet = providerKeyStatuses[currentProvider] ?? false;
              return (
                <div key={ep.instance_id} style={{ marginTop: 8, padding: '8px 10px', borderRadius: 8, border: '1px solid rgba(0,240,255,0.12)', background: 'rgba(0,240,255,0.03)' }}>
                  <div style={{ fontSize: 11, fontWeight: 700, color: C.cyan, marginBottom: 6 }}>{ep.slug || ep.instance_id} ({ep.protocol})</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                    <div>
                      <label style={{ fontSize: 10, color: C.textMuted, display: 'block', marginBottom: 3 }}>Provider</label>
                      <select style={{ ...selectStyle, fontSize: 12 }} value={currentProvider} onChange={e => { const p = e.target.value; const models = MODELS_BY_PROVIDER[p] ?? []; setEpLlm(ep.instance_id, { provider: p, model: models[0] ?? '' }); }}>
                        <option value="">— select —</option>
                        {Object.keys(MODELS_BY_PROVIDER).map(p => (
                          <option key={p} value={p}>{p}{providerKeyStatuses[p] ? ' ✓' : ''}</option>
                        ))}
                      </select>
                    </div>
                    {currentProvider && (
                      <div>
                        <label style={{ fontSize: 10, color: C.textMuted, display: 'block', marginBottom: 3 }}>Model</label>
                        <select style={{ ...selectStyle, fontSize: 12, fontFamily: 'JetBrains Mono, monospace' }} value={currentModel} onChange={e => setEpLlm(ep.instance_id, { model: e.target.value })}>
                          <option value="">— select —</option>
                          {availableModels.map(m => <option key={m} value={m}>{m}</option>)}
                        </select>
                      </div>
                    )}
                    {currentProvider && currentModel && (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        {!keySet && <span style={{ fontSize: 10, color: C.amber }}>⚠ No key set for {currentProvider}</span>}
                        <button
                          disabled={testState.loading}
                          onClick={() => testOrchLlm(testKey, currentProvider, currentModel)}
                          style={{ padding: '5px 10px', borderRadius: 6, border: '1px solid rgba(74,222,128,0.3)', background: 'rgba(74,222,128,0.05)', color: C.green, cursor: 'pointer', fontSize: 11, fontWeight: 600, opacity: testState.loading ? 0.5 : 1 }}
                        >
                          {testState.loading ? '…' : 'Test'}
                        </button>
                        {testState.ok === true && <span style={{ fontSize: 11, color: C.green }}>✓ {testState.latency}ms</span>}
                        {testState.ok === false && <span style={{ fontSize: 11, color: C.error }}>{testState.error ?? 'Failed'}</span>}
                      </div>
                    )}
                  </div>
                </div>
              );
            })}
          </div>

          {/* Advanced settings */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
            <SectionHeader id="orch-adv" label="Advanced" defaultOpen={false} />
            {isSectionOpen('orch-adv', false) && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
                <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Max Iterations</label>
                  <input type="number" style={fieldStyle} value={(cfg.max_iterations as number) ?? 10} onChange={e => setOrchConfig({ max_iterations: Number(e.target.value) || null })} /></div>
                <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>History Window</label>
                  <input type="number" style={fieldStyle} value={(cfg.history_window as number) ?? 20} onChange={e => setOrchConfig({ history_window: Number(e.target.value) || null })} /></div>
                <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Max Parallel Tools</label>
                  <input type="number" style={fieldStyle} value={(cfg.max_parallel_tools as number) ?? 5} onChange={e => setOrchConfig({ max_parallel_tools: Number(e.target.value) || null })} /></div>
                <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Budget Tokens</label>
                  <input type="number" style={fieldStyle} value={(cfg.budget_tokens as number) ?? ''} placeholder="none" onChange={e => setOrchConfig({ budget_tokens: e.target.value === '' ? null : Number(e.target.value) })} /></div>
              </div>
            )}
          </div>

          {/* Voice STT/TTS — only when a voice EP is connected */}
          {hasVoice && (
            <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
              <SectionHeader id="orch-voice" label="Voice (STT / TTS)" defaultOpen={true} />
              {isSectionOpen('orch-voice', true) && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
                  <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>STT Provider</label>
                    <select style={selectStyle} value={sttProvider} onChange={e => { const p = e.target.value; setOrchConfig({ stt_provider: p, stt_model: p === 'openai' ? 'whisper-1' : 'whisper-large-v3' }); }}>
                      <option value="openai">openai</option>
                      <option value="groq">groq</option>
                    </select>
                  </div>
                  <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>STT Model</label>
                    <input style={fieldStyle} value={(cfg.stt_model as string) ?? ''} onChange={e => setOrchConfig({ stt_model: e.target.value })} placeholder="e.g. whisper-1" />
                  </div>
                  <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>TTS Provider</label>
                    <select style={selectStyle} value={ttsProvider} onChange={e => setOrchConfig({ tts_provider: e.target.value, tts_voice: '' })}>
                      <option value="openai">openai</option>
                      <option value="elevenlabs">elevenlabs</option>
                    </select>
                  </div>
                  <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>TTS Voice</label>
                    {ttsProvider === 'openai'
                      ? <select style={selectStyle} value={ttsVoice} onChange={e => setOrchConfig({ tts_voice: e.target.value })}>{['alloy','echo','fable','onyx','nova','shimmer'].map(v => <option key={v} value={v}>{v}</option>)}</select>
                      : <input style={fieldStyle} value={ttsVoice} onChange={e => setOrchConfig({ tts_voice: e.target.value })} placeholder="ElevenLabs voice ID" />}
                  </div>
                  <div style={{ fontSize: 11, color: C.textMuted, fontStyle: 'italic' }}>API keys via Secret Bindings — not stored here</div>
                </div>
              )}
            </div>
          )}

          {/* MCP Servers — design-time config, saved to canvas on publish */}
          {(() => {
            const currentAttachments: MCPServerAttachment[] = (cfg.mcp_servers as MCPServerAttachment[] | undefined) ?? [];
            const enabledServers = availableMCPServers.filter(s => s.enabled);

            function toggleServer(slug: string) {
              const isAttached = currentAttachments.some(a => a.slug === slug);
              const next: MCPServerAttachment[] = isAttached
                ? currentAttachments.filter(a => a.slug !== slug)
                : [...currentAttachments, { slug, tools: [] }]; // empty = all tools
              setOrchConfig({ mcp_servers: next });
            }

            function toggleTool(slug: string, toolName: string) {
              const attachment = currentAttachments.find(a => a.slug === slug);
              if (!attachment) return;
              const current = attachment.tools ?? [];
              // empty means "all" — switching to explicit list when user first unchecks
              const allTools = availableMCPServers.find(s => s.slug === slug)?.tools_manifest?.map(t => t.name) ?? [];
              const base = current.length === 0 ? allTools : current;
              const next = base.includes(toolName)
                ? base.filter(t => t !== toolName)
                : [...base, toolName];
              // if user re-selected all tools, collapse back to empty (= all)
              const nextTools = next.length === allTools.length ? [] : next;
              const nextAttachments = currentAttachments.map(a =>
                a.slug === slug ? { ...a, tools: nextTools } : a
              );
              setOrchConfig({ mcp_servers: nextAttachments });
            }

            return (
              <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.purple }}>lan</span>
                  <span style={{ fontSize: 11, fontWeight: 700, color: C.purple, textTransform: 'uppercase', letterSpacing: '0.5px' }}>MCP Servers</span>
                </div>
                {enabledServers.length === 0 ? (
                  <div style={{ fontSize: 11, color: C.textMuted, fontStyle: 'italic' }}>No MCP servers configured — add one in MCP Store</div>
                ) : (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                    {enabledServers.map(server => {
                      const attachment = currentAttachments.find(a => a.slug === server.slug);
                      const isAttached = !!attachment;
                      const allowlist = attachment?.tools ?? [];
                      const allTools = server.tools_manifest ?? [];
                      const statusColor = server.health_status === 'healthy' ? '#4ade80' : server.health_status === 'degraded' ? C.amber : server.health_status === 'unreachable' ? '#f87171' : C.textMuted;
                      const activeCount = allowlist.length === 0 ? allTools.length : allowlist.length;
                      const expanded = !!mcpExpanded[server.slug];
                      return (
                        <div key={server.slug} style={{ borderRadius: 7, border: `1px solid ${isAttached ? 'rgba(208,188,255,0.25)' : 'rgba(255,255,255,0.08)'}`, overflow: 'hidden' }}>
                          {/* Server header row */}
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 9px', background: isAttached ? 'rgba(208,188,255,0.07)' : 'transparent' }}>
                            {/* Checkbox — toggles attachment */}
                            <div
                              onClick={e => {
                                e.stopPropagation();
                                if (!isAttached) setMcpExpanded(prev => ({ ...prev, [server.slug]: true }));
                                toggleServer(server.slug);
                              }}
                              style={{ width: 13, height: 13, borderRadius: 3, border: `1.5px solid ${isAttached ? C.purple : 'rgba(255,255,255,0.3)'}`, background: isAttached ? C.purple : 'transparent', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, cursor: 'pointer' }}>
                              {isAttached && <span className="material-symbols-outlined" style={{ fontSize: 9, color: '#fff' }}>check</span>}
                            </div>
                            {/* Name + slug — clicking expands */}
                            <div style={{ flex: 1, minWidth: 0, cursor: 'pointer' }} onClick={() => setMcpExpanded(prev => ({ ...prev, [server.slug]: !prev[server.slug] }))}>
                              <div style={{ fontSize: 12, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{server.name}</div>
                              <div style={{ fontSize: 10, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{server.slug}</div>
                            </div>
                            {/* Right side: health dot + tool count + chevron */}
                            <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0 }}>
                              <div style={{ width: 6, height: 6, borderRadius: '50%', background: statusColor }} title={server.health_status} />
                              {isAttached && allTools.length > 0 ? (
                                <span style={{ fontSize: 10, color: allowlist.length > 0 ? C.amber : C.textMuted }}>
                                  {activeCount}/{allTools.length}
                                </span>
                              ) : (
                                <span style={{ fontSize: 10, color: C.textMuted }}>{allTools.length}</span>
                              )}
                              {allTools.length > 0 && (
                                <span
                                  className="material-symbols-outlined"
                                  style={{ fontSize: 14, color: C.textMuted, cursor: 'pointer', transition: 'transform 150ms', transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)' }}
                                  onClick={e => { e.stopPropagation(); setMcpExpanded(prev => ({ ...prev, [server.slug]: !prev[server.slug] })); }}>
                                  expand_more
                                </span>
                              )}
                            </div>
                          </div>
                          {/* Collapsible tool list */}
                          {expanded && allTools.length > 0 && (
                            <div style={{ borderTop: '1px solid rgba(255,255,255,0.06)', padding: '6px 9px', display: 'flex', flexDirection: 'column', gap: 3, background: 'rgba(0,0,0,0.15)' }}>
                              {allTools.map(tool => {
                                const toolEnabled = allowlist.length === 0 || allowlist.includes(tool.name);
                                return (
                                  <div key={tool.name}
                                    onClick={e => { e.stopPropagation(); if (isAttached) toggleTool(server.slug, tool.name); }}
                                    style={{ display: 'flex', alignItems: 'flex-start', gap: 7, padding: '3px 2px', cursor: isAttached ? 'pointer' : 'default', borderRadius: 4, opacity: isAttached ? 1 : 0.45 }}>
                                    <div style={{ width: 11, height: 11, marginTop: 1, borderRadius: 2, border: `1.5px solid ${toolEnabled ? C.purple : 'rgba(255,255,255,0.2)'}`, background: toolEnabled ? C.purple : 'transparent', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                                      {toolEnabled && <span className="material-symbols-outlined" style={{ fontSize: 8, color: '#fff' }}>check</span>}
                                    </div>
                                    <div style={{ flex: 1, minWidth: 0 }}>
                                      <div style={{ fontSize: 11, color: toolEnabled ? C.text : C.textMuted, fontFamily: 'JetBrains Mono, monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{tool.name}</div>
                                      {tool.description && <div style={{ fontSize: 10, color: C.textMuted, lineHeight: 1.3, marginTop: 1 }}>{tool.description}</div>}
                                    </div>
                                  </div>
                                );
                              })}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            );
          })()}
        </div>
      );
    }

    // ── agent ────────────────────────────────────────────────────────────────
    if (selectedNode.type === 'agent') {
      const liveAgentNode = nodes.find(n => n.id === selectedNode.id);
      const d = (liveAgentNode?.data ?? selectedNode.data) as unknown as AgentNodeData;
      return (
        <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 12, overflowY: 'auto' }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: C.green, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>Agent</div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Instance ID</label>
            <div style={chipStyle}>{d.instance_id}</div>
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Display Name</label>
            <div style={{ fontSize: 13, color: C.text, padding: '6px 0' }}>{d.display_name}</div>
          </div>
          {d.description && <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Description</label>
            <div style={{ fontSize: 12, color: C.textMuted }}>{d.description}</div>
          </div>}
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Config (JSON)</label>
            <textarea
              style={{ ...fieldStyle, minHeight: 100, resize: 'vertical', fontFamily: 'JetBrains Mono, monospace', fontSize: 12, borderColor: configPanelErr ? C.error : undefined }}
              value={configPanelText}
              onChange={e => setConfigPanelText(e.target.value)}
              onBlur={() => {
                try {
                  const parsed = JSON.parse(configPanelText);
                  setNodes(ns => ns.map(n => n.id === selectedNode!.id ? { ...n, data: { ...n.data, config: parsed } } : n));
                  setIsDirty(true);
                  setConfigPanelErr(false);
                } catch { setConfigPanelErr(true); showToast('Invalid JSON', false); }
              }}
            />
          </div>
        </div>
      );
    }

    // ── entry point ──────────────────────────────────────────────────────────
    if (selectedNode.type === 'entryPoint') {
      const liveEpNode = nodes.find(n => n.id === selectedNode.id);
      const d = (liveEpNode?.data ?? selectedNode.data) as unknown as EpNodeData;
      const cfg = d.config ?? {};
      const rootOrchNode = edges
        .filter(e => e.source === selectedNode.id)
        .map(e => nodes.find(n => n.id === e.target))
        .find(n => n?.type === 'orchestrator');
      const slugValid = /^[a-z0-9_-]{1,64}$/.test(d.slug);
      const rootLabel = rootOrchNode
        ? ((rootOrchNode.data as unknown as OrchNodeData).display_name ?? rootOrchNode.id)
        : 'Not connected';

      return (
        <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
          {/* Section A — Identity */}
          <div style={{ fontSize: 12, fontWeight: 700, color: C.cyan, textTransform: 'uppercase', letterSpacing: '0.06em' }}>Entry Point</div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Instance ID</label>
            <div style={chipStyle}>{d.instance_id}</div>
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Slug</label>
            <input
              style={{ ...fieldStyle, borderColor: d.slug && !slugValid ? C.amber : undefined }}
              value={d.slug}
              onChange={e => { setNodes(ns => ns.map(n => n.id === selectedNode!.id ? { ...n, data: { ...n.data, slug: e.target.value } } : n)); setIsDirty(true); setLogoResult('none'); }}
              placeholder="e.g. my-endpoint"
            />
            {d.slug && !slugValid && <div style={{ fontSize: 11, color: C.amber, marginTop: 3 }}>Only a-z, 0-9, _, - (1-64 chars)</div>}
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Protocol</label>
            <span style={{ fontSize: 12, padding: '3px 10px', borderRadius: 20, background: C.cyanBg, color: C.cyan, border: `1px solid ${C.cyanBorder}` }}>{d.protocol}</span>
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Root Orchestrator</label>
            <div style={{ fontSize: 12, color: rootOrchNode ? C.purple : C.textMuted, fontStyle: rootOrchNode ? 'normal' : 'italic' }}>{rootLabel}</div>
          </div>

          {/* Section B — LLM (read-only: shows what orchestrator has configured for this EP) */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
            <div style={{ ...sectionHdrStyle, marginBottom: 8 }}>LLM</div>
            {rootOrchNode ? (() => {
              const orchConfig = (rootOrchNode.data as unknown as OrchNodeData).config;
              const epLlm = ((orchConfig.ep_llm ?? {}) as Record<string, { provider?: string; model?: string }>)[d.instance_id];
              const prov = epLlm?.provider;
              const mdl = epLlm?.model;
              return prov && mdl
                ? <div style={{ fontSize: 12, color: C.text }}><span style={{ color: C.purple }}>{prov}</span> / <span style={{ fontFamily: 'JetBrains Mono, monospace', color: C.cyan }}>{mdl}</span></div>
                : <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>Configure on the orchestrator panel</div>;
            })()
              : <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>Connect an orchestrator to configure LLM</div>
            }
          </div>

          {/* Section C — Access */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
            <div style={{ ...sectionHdrStyle, marginBottom: 8 }}>Access</div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Access Mode</label>
            <select
              style={selectStyle}
              value={(cfg.access_mode as string) || 'token'}
              onChange={e => setEpConfig(selectedNode!.id, { access_mode: e.target.value })}
            >
              <option value="token">token</option>
              <option value="public">public</option>
            </select>
          </div>

          {/* Section D — Capacity (collapsible, default collapsed) */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
            <SectionHeader id="ep-capacity" label="Capacity" defaultOpen={false} />
            {isSectionOpen('ep-capacity', false) && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
                <div>
                  <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Conversation Token Limit</label>
                  <input
                    type="number"
                    style={fieldStyle}
                    value={(cfg.conversation_token_limit as number) ?? ''}
                    placeholder="unset"
                    onChange={e => {
                      if (e.target.value === '') setEpConfig(selectedNode!.id, {}, ['conversation_token_limit']);
                      else setEpConfig(selectedNode!.id, { conversation_token_limit: Number(e.target.value) });
                    }}
                  />
                </div>
                <div>
                  <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Queue Timeout (s)</label>
                  <input
                    type="number"
                    style={fieldStyle}
                    value={(cfg.queue_timeout_seconds as number) ?? ''}
                    placeholder="unset"
                    onChange={e => {
                      if (e.target.value === '') setEpConfig(selectedNode!.id, {}, ['queue_timeout_seconds']);
                      else setEpConfig(selectedNode!.id, { queue_timeout_seconds: Number(e.target.value) });
                    }}
                  />
                </div>
                <div>
                  <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Queue Message</label>
                  <input
                    style={fieldStyle}
                    value={(cfg.queue_message as string) ?? ''}
                    placeholder="All agents are busy, please wait…"
                    onChange={e => {
                      if (e.target.value === '') setEpConfig(selectedNode!.id, {}, ['queue_message']);
                      else setEpConfig(selectedNode!.id, { queue_message: e.target.value });
                    }}
                  />
                </div>
              </div>
            )}
          </div>

          {/* Section E — Protocol-specific */}
          {d.protocol === 'voice' && (
            <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
              <div style={{ ...sectionHdrStyle, marginBottom: 8, color: C.amber }}>Voice</div>
              <div style={{ fontSize: 12, color: C.textMuted, padding: '8px 10px', background: C.amberBg, border: `1px solid ${C.amberBorder}`, borderRadius: 6 }}>
                STT/TTS is configured on the root orchestrator&rsquo;s Voice section.
              </div>
            </div>
          )}
          {d.protocol === 'a2a' && (
            <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
              <div style={{ ...sectionHdrStyle, marginBottom: 8 }}>A2A</div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <div>
                  <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 2 }}>Skill ID</label>
                  <span style={{ fontSize: 12, fontFamily: 'JetBrains Mono, monospace', color: C.cyan }}>{d.slug}</span>
                </div>
                <div style={{ fontSize: 12, color: C.textMuted }}>
                  budget_tokens from the root orchestrator applies to A2A calls.
                </div>
              </div>
            </div>
          )}
        </div>
      );
    }

    // ── middleware ───────────────────────────────────────────────────────────
    if (selectedNode.type === 'middleware') {
      const liveMwNode = nodes.find(n => n.id === selectedNode.id);
      const d = (liveMwNode?.data ?? selectedNode.data) as unknown as MwNodeData;
      return (
        <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 12, overflowY: 'auto' }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: C.amber, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>Middleware</div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Instance ID</label>
            <div style={chipStyle}>{d.instance_id}</div>
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Display Name</label>
            <div style={{ fontSize: 13, color: C.text }}>{d.display_name}</div>
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Config (JSON)</label>
            <textarea
              style={{ ...fieldStyle, minHeight: 100, resize: 'vertical', fontFamily: 'JetBrains Mono, monospace', fontSize: 12, borderColor: configPanelErr ? C.error : undefined }}
              value={configPanelText}
              onChange={e => setConfigPanelText(e.target.value)}
              onBlur={() => {
                try {
                  const parsed = JSON.parse(configPanelText);
                  setNodes(ns => ns.map(n => n.id === selectedNode!.id ? { ...n, data: { ...n.data, config: parsed } } : n));
                  setIsDirty(true);
                  setConfigPanelErr(false);
                } catch { setConfigPanelErr(true); showToast('Invalid JSON', false); }
              }}
            />
          </div>
        </div>
      );
    }

    return (
      <div style={{ padding: 20, color: C.textMuted, fontSize: 13, fontStyle: 'italic' }}>
        Select a node to configure properties
      </div>
    );
  }

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
            {renderPropertiesPanel()}
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
