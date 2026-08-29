'use client';
import React, { useCallback, useEffect, useRef, useState, type DragEvent, type MouseEvent } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { useReactFlow, type Node, type Edge, type NodeTypes, type EdgeTypes } from '@xyflow/react';
import { LayoutDirContext } from '../LayoutContext';
import { PortsPanelContext } from '../PortsPanelContext';
import { C, INITIAL_DEBUG, genUUID } from '../constants';
import type { SkillData } from '../types';
import type { AgentStepDoc } from '@/lib/api';
import { useAgentGraph } from '../hooks/useAgentGraph';
import { useSkillPipeline } from '../hooks/useSkillPipeline';
import { useDefinitionLifecycle } from '../hooks/useDefinitionLifecycle';
import { useBuilderHistory } from '../hooks/useBuilderHistory';
import { useDebugSession } from '../hooks/useDebugSession';
import { useResizablePanels } from '../hooks/useResizablePanels';
import { useBuilderDerivedState } from '../hooks/useBuilderDerivedState';
import { applyDagreLayout } from '../canvas/dagre';
import { BuilderCanvas, nodeTypes, edgeTypes } from './BuilderCanvas';
import { BuilderTopBar } from './BuilderTopBar';
import { NodeLibraryPanel } from './NodeLibraryPanel';
import { ValidationIssuesPanel } from './ValidationIssuesPanel';
import { DebugPanel } from './DebugPanel';
import { RightPanel } from './RightPanel';
import type { CtxTarget } from './NodeContextMenu';

export function BuilderWorkspace() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const defId = searchParams.get('id');
  const { screenToFlowPosition, fitView } = useReactFlow();

  // ── markDirty bootstrap — resolves circular: pipeline needs markDirty before history is created ──
  const markDirtyRef = useRef<() => void>(() => {});
  const stableMarkDirty = useCallback(() => markDirtyRef.current(), []);

  // ── Hooks ─────────────────────────────────────────────────────────────────────
  const panels = useResizablePanels();

  const graph = useAgentGraph({ markDirty: stableMarkDirty });

  // Pre-seed AGENT ROOT for new drafts
  useEffect(() => {
    if (!defId && graph.agentNodes.length === 0) {
      graph.setAgentNodes(() => [{
        id: 'agent-root',
        type: 'agentRoot',
        position: { x: 300, y: 80 },
        data: { display_name: 'My Agent', description: '', version: '1.0.0' },
      }]);
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const pipeline = useSkillPipeline({
    activeSkillId: graph.activeSkillId,
    layoutDir: graph.layoutDir,
    markDirty: stableMarkDirty,
    screenToFlowPosition,
    fitView,
  });

  const lifecycle = useDefinitionLifecycle({
    defId,
    router,
    agentSlug: graph.agentSlug,
    setAgentSlug: graph.setAgentSlug,
    agentNodes: graph.agentNodes,
    setAgentNodes: graph.setAgentNodes,
    agentEdges: graph.agentEdges,
    setAgentEdges: graph.setAgentEdges,
    displayName: graph.displayName,
    setDisplayName: graph.setDisplayName,
    description: graph.description,
    setDescription: graph.setDescription,
    version: graph.version,
    setVersion: graph.setVersion,
    activeSkillId: graph.activeSkillId,
    skillPipelines: pipeline.skillPipelines,
    setSkillPipelines: pipeline.setSkillPipelines,
    localPipeNodes: pipeline.localPipeNodes,
    setLocalPipeNodes: pipeline.setLocalPipeNodes,
    localPipeEdges: pipeline.localPipeEdges,
    savePipelineState: pipeline.savePipelineState,
    setSelectedNode: graph.setSelectedNode,
    markDirty: stableMarkDirty,
    setDirty: (v) => { /* managed by history */ },
  });

  const history = useBuilderHistory({
    buildDefinitionDoc: lifecycle.buildDefinitionDoc,
    loadDefinitionDoc: lifecycle.loadDefinitionDoc,
    setDirty: lifecycle.setDirty,
  });

  useEffect(() => { markDirtyRef.current = history.markDirty; }, [history.markDirty]);

  const debugSession = useDebugSession({
    defId,
    activeSkillId: graph.activeSkillId,
    localPipeNodes: pipeline.localPipeNodes,
    localPipeEdges: pipeline.localPipeEdges,
  });

  const derived = useBuilderDerivedState({ graph, pipeline, lifecycle, debugSession });

  // ── Stable node/edge type refs ────────────────────────────────────────────────
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stableNodeTypes = React.useMemo<NodeTypes>(() => nodeTypes, []);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stableEdgeTypes = React.useMemo<EdgeTypes>(() => edgeTypes, []);

  // ── Ports panel close broadcast ───────────────────────────────────────────────
  const [portsPanelCloseToken, setPortsPanelCloseToken] = useState(0);
  const closeAllPopovers = useCallback(() => setPortsPanelCloseToken(t => t + 1), []);

  // ── Context menu ──────────────────────────────────────────────────────────────
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; target: CtxTarget } | null>(null);
  const closeCtx = useCallback(() => setCtxMenu(null), []);

  const onNodeCtx = useCallback((e: MouseEvent, node: Node) => {
    e.preventDefault();
    setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: 'node', node } });
    graph.setSelectedNode(node);
  }, [graph]);

  const onEdgeCtx = useCallback((e: MouseEvent, edge: Edge) => {
    e.preventDefault();
    setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: 'edge', edge } });
  }, []);

  const ctxDelete = useCallback(() => {
    if (!ctxMenu) return;
    if (ctxMenu.target.kind === 'node') {
      const id = ctxMenu.target.node.id;
      if (graph.activeView === 'agent') {
        graph.setAgentNodes(prev => prev.filter(n => n.id !== id));
        graph.setAgentEdges(prev => prev.filter(e => e.source !== id && e.target !== id));
      } else {
        pipeline.setLocalPipeNodes(prev => prev.filter(n => n.id !== id));
        pipeline.setLocalPipeEdges(prev => prev.filter(e => e.source !== id && e.target !== id));
      }
    } else {
      const id = ctxMenu.target.edge.id;
      if (graph.activeView === 'agent') {
        graph.setAgentEdges(prev => prev.filter(e => e.id !== id));
      } else {
        pipeline.setLocalPipeEdges(prev => prev.filter(e => e.id !== id));
      }
    }
    history.markDirty();
    closeCtx();
  }, [ctxMenu, graph, pipeline, history, closeCtx]);

  const ctxEditPipeline = useCallback(() => {
    if (!ctxMenu || ctxMenu.target.kind !== 'node') return;
    const node = ctxMenu.target.node;
    if (node.type === 'skill') {
      pipeline.savePipelineState();
      const sd = node.data as unknown as SkillData;
      graph.setActiveSkillId(sd.skill_id);
      graph.setActiveView('skill');
      graph.setSelectedNode(null);
    }
    closeCtx();
  }, [ctxMenu, pipeline, graph, closeCtx]);

  // ── Navigation ────────────────────────────────────────────────────────────────
  function handleBack() {
    pipeline.savePipelineState();
    if (graph.activeView === 'skill') {
      graph.setActiveView('agent');
      graph.setActiveSkillId(null);
      graph.setSelectedNode(null);
    } else {
      router.push('/admin/agents');
    }
  }

  // ── Node double-click ─────────────────────────────────────────────────────────
  function onAgentNodeDoubleClick(_: MouseEvent, node: Node) {
    if (node.type === 'skill') {
      pipeline.savePipelineState();
      const sd = node.data as unknown as SkillData;
      graph.setActiveSkillId(sd.skill_id);
      graph.setActiveView('skill');
      graph.setSelectedNode(null);
    } else {
      graph.setSelectedNode(node);
    }
  }

  function onPipeNodeDoubleClick(_: MouseEvent, node: Node) {
    graph.setSelectedNode(node);
  }

  // ── addSkill (needs both graph and pipeline setters) ──────────────────────────
  function addSkill() {
    const sid = genUUID();
    const newNode: Node = {
      id: `skill-${sid}`,
      type: 'skill',
      position: { x: 150 + graph.agentNodes.filter(n => n.type === 'skill').length * 220, y: 250 },
      data: { skill_id: sid, name: 'New Skill', description: '', tags: [], input_modes: ['text/plain'], output_modes: ['text/plain'], examples: [] },
    };
    graph.setAgentNodes(prev => [...prev, newNode]);
    if (graph.agentNodes.find(n => n.id === 'agent-root')) {
      graph.setAgentEdges(prev => [...prev, { id: `root-to-${sid}`, source: 'agent-root', target: `skill-${sid}` }]);
    }
    pipeline.setSkillPipelines(prev => ({ ...prev, [sid]: pipeline.makeDefaultPipeline() }));
    history.markDirty();
  }

  // ── Canvas layout helpers ─────────────────────────────────────────────────────
  function applyDagreAndFit() {
    if (graph.activeView === 'agent') {
      graph.setAgentNodes(ns => applyDagreLayout(ns, graph.agentEdges, graph.layoutDir));
    } else {
      pipeline.setLocalPipeNodes(ns => applyDagreLayout(ns, pipeline.localPipeEdges, graph.layoutDir));
    }
    setTimeout(() => fitView({ padding: 0.2 }), 50);
  }

  function toggleLayoutDir() {
    const next = graph.layoutDir === 'TB' ? 'LR' : 'TB';
    graph.setLayoutDir(next);
    if (graph.activeView === 'agent') {
      graph.setAgentNodes(ns => applyDagreLayout(ns, graph.agentEdges, next));
    } else {
      pipeline.setLocalPipeNodes(ns => applyDagreLayout(ns, pipeline.localPipeEdges, next));
    }
    setTimeout(() => fitView({ padding: 0.2 }), 50);
  }

  // ── Drop handlers ─────────────────────────────────────────────────────────────
  const onAgentDrop = useCallback((e: DragEvent) => {
    e.preventDefault();
    const nodeType = e.dataTransfer.getData('nodeType');
    if (nodeType === 'skill') {
      const bounds = (e.currentTarget as HTMLElement).getBoundingClientRect();
      const pos = screenToFlowPosition({ x: e.clientX - bounds.left, y: e.clientY - bounds.top });
      const sid = genUUID();
      const newNode: Node = { id: `skill-${sid}`, type: 'skill', position: pos, data: { skill_id: sid, name: 'New Skill', description: '', tags: [], input_modes: ['text/plain'], output_modes: ['text/plain'], examples: [] } };
      graph.setAgentNodes(prev => [...prev, newNode]);
      graph.setAgentEdges(prev => [...prev, { id: `root-to-${sid}`, source: 'agent-root', target: `skill-${sid}` }]);
      pipeline.setSkillPipelines(prev => ({ ...prev, [sid]: pipeline.makeDefaultPipeline() }));
      history.markDirty();
    }
  }, [screenToFlowPosition, graph, pipeline, history]);

  const onPipeDrop = useCallback((e: DragEvent) => {
    e.preventDefault();
    const nodeType = e.dataTransfer.getData('nodeType');
    if (nodeType === 'step') {
      const stepType = e.dataTransfer.getData('stepType') as AgentStepDoc['type'];
      const bounds = (e.currentTarget as HTMLElement).getBoundingClientRect();
      const pos = screenToFlowPosition({ x: e.clientX - bounds.left, y: e.clientY - bounds.top });
      const stepId = genUUID();
      const newNode: Node = { id: `step-${stepId}`, type: 'step', position: pos, data: { step_id: stepId, step_type: stepType, label: stepType.replace('_', ' '), config: {} } };
      pipeline.setLocalPipeNodes(prev => [...prev, newNode]);
      history.markDirty();
    }
  }, [screenToFlowPosition, pipeline, history]);

  // ── Node field updates ────────────────────────────────────────────────────────
  function updateSelectedNodeField(field: string, value: string) {
    if (!graph.selectedNode) return;
    if (graph.activeView === 'agent') {
      graph.setAgentNodes(prev => prev.map(n =>
        n.id === graph.selectedNode!.id ? { ...n, data: { ...n.data, [field]: value } } : n
      ));
      if (field === 'display_name' && graph.selectedNode.id === 'agent-root' && !defId) {
        const slug = value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
        graph.setAgentSlug(slug);
      }
    } else {
      pipeline.setLocalPipeNodes(prev => prev.map(n =>
        n.id === graph.selectedNode!.id ? { ...n, data: { ...n.data, [field]: value } } : n
      ));
    }
    history.markDirty();
  }

  function updateStepConfig(key: string, value: unknown) {
    if (!graph.selectedNode || graph.activeView !== 'skill') return;
    pipeline.setLocalPipeNodes(prev => prev.map(n =>
      n.id === graph.selectedNode!.id
        ? { ...n, data: { ...n.data, config: { ...(n.data.config as Record<string, unknown>), [key]: value } } }
        : n
    ));
    history.markDirty();
  }

  function updateStepPolicy(policy: Record<string, unknown> | null) {
    if (!graph.selectedNode || graph.activeView !== 'skill') return;
    pipeline.setLocalPipeNodes(prev => prev.map(n =>
      n.id === graph.selectedNode!.id
        ? { ...n, data: { ...n.data, policy: policy ?? undefined } }
        : n
    ));
    history.markDirty();
  }

  return (
    <PortsPanelContext.Provider value={portsPanelCloseToken}>
    <LayoutDirContext.Provider value={graph.layoutDir}>
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg }}>
      <BuilderTopBar
        activeView={graph.activeView}
        activeSkillId={graph.activeSkillId}
        defId={defId}
        agentSlug={graph.agentSlug}
        onSlugChange={v => { graph.setAgentSlug(v); history.markDirty(); }}
        dirty={lifecycle.dirty}
        saving={lifecycle.saving}
        deleting={lifecycle.deleting}
        validating={lifecycle.validating}
        publishing={lifecycle.publishing}
        saveError={lifecycle.saveError}
        publishedRevision={lifecycle.publishedRevision}
        errorCount={derived.errorCount}
        warningCount={derived.warningCount}
        validationLoading={lifecycle.validation.loading}
        lastValidatedAt={lifecycle.validation.lastValidatedAt}
        debugActive={debugSession.debug.active}
        canUndo={history.canUndo}
        canRedo={history.canRedo}
        importFileRef={lifecycle.importFileRef}
        onBack={handleBack}
        onSave={lifecycle.handleSave}
        onDelete={lifecycle.handleDelete}
        onValidate={lifecycle.handleValidate}
        onPublish={lifecycle.handlePublish}
        onExport={lifecycle.handleExport}
        onImportJSON={lifecycle.handleImportJSON}
        onImportFileChange={lifecycle.handleImportFileChange}
        onUndo={history.handleUndo}
        onRedo={history.handleRedo}
        onDebugToggle={() => {
          if (debugSession.debug.active) {
            debugSession.setDebug(INITIAL_DEBUG);
          } else {
            debugSession.debugStartSetup();
          }
        }}
      />

      {lifecycle.loadError && (
        <div style={{ background: 'rgba(239,68,68,0.1)', padding: '10px 24px', color: '#f87171', fontSize: '13px' }}>
          {lifecycle.loadError}
        </div>
      )}

      {graph.activeView === 'skill' && debugSession.debug.active && (
        <DebugPanel
          debug={debugSession.debug}
          setDebug={debugSession.setDebug}
          debugRunning={derived.debugRunning}
          debugCommitSetup={debugSession.debugCommitSetup}
          debugRunAll={debugSession.debugRunAll}
          debugStep={debugSession.debugStep}
          debugReset={debugSession.debugReset}
        />
      )}

      <ValidationIssuesPanel
        issues={derived.pipelineIssues}
        errorCount={derived.errorCount}
        show={graph.activeView === 'skill' && derived.pipelineIssues.length > 0 && !debugSession.debug.active}
      />

      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
        <NodeLibraryPanel
          activeView={graph.activeView}
          libraryWidth={panels.libraryWidth}
          onAddStep={pipeline.addStepToActivePipeline}
          onResizeStart={panels.startResizeLibrary}
        />

        <BuilderCanvas
          activeView={graph.activeView}
          logoState={derived.logoState}
          currentNodes={derived.currentNodes}
          currentEdges={derived.currentEdges}
          onAgentNodesChange={graph.onAgentNodesChange}
          onAgentEdgesChange={graph.onAgentEdgesChange}
          onPipeNodesChange={pipeline.onPipeNodesChange}
          onPipeEdgesChange={pipeline.onPipeEdgesChange}
          debugNodes={derived.debugNodes}
          debugEdges={derived.debugEdges}
          localPipeNodes={pipeline.localPipeNodes}
          displayPipeEdges={pipeline.displayPipeEdges}
          onAgentConnect={graph.onAgentConnect}
          onPipeConnect={pipeline.onPipeConnect}
          onPipeConnectStart={pipeline.onPipeConnectStart}
          onPipeConnectEnd={pipeline.onPipeConnectEnd}
          isPipeConnectionValid={pipeline.isPipeConnectionValid}
          onClosePopovers={closeAllPopovers}
          onNodeCtx={onNodeCtx}
          onEdgeCtx={onEdgeCtx}
          onAgentNodeDoubleClick={onAgentNodeDoubleClick}
          onPipeNodeDoubleClick={onPipeNodeDoubleClick}
          setSelectedNode={graph.setSelectedNode}
          closeCtx={closeCtx}
          ctxMenu={ctxMenu}
          ctxDelete={ctxDelete}
          ctxEditPipeline={ctxEditPipeline}
          fitView={fitView}
          applyDagreAndFit={applyDagreAndFit}
          toggleLayoutDir={toggleLayoutDir}
          layoutDir={graph.layoutDir}
          debugActive={debugSession.debug.active}
          onAgentDrop={onAgentDrop}
          onPipeDrop={onPipeDrop}
          stableNodeTypes={stableNodeTypes}
          stableEdgeTypes={stableEdgeTypes}
        />

        {graph.selectedNode && (
          <RightPanel
            selectedNode={graph.selectedNode}
            setSelectedNode={graph.setSelectedNode}
            propertiesWidth={panels.propertiesWidth}
            onResizeStart={panels.startResizeProperties}
            activeView={graph.activeView}
            agentNodes={graph.agentNodes}
            localPipeNodes={pipeline.localPipeNodes}
            localPipeEdges={pipeline.localPipeEdges}
            validationIssues={lifecycle.validation.issues}
            stepContracts={lifecycle.validation.stepContracts}
            debug={debugSession.debug}
            updateSelectedNodeField={updateSelectedNodeField}
            updateStepConfig={updateStepConfig}
            updateStepPolicy={updateStepPolicy}
            setAgentNodes={graph.setAgentNodes}
            setDirty={lifecycle.setDirty}
            savePipelineState={pipeline.savePipelineState}
            setActiveSkillId={graph.setActiveSkillId}
            setActiveView={graph.setActiveView}
            setDebug={debugSession.setDebug}
            debugStep={debugSession.debugStep}
            nodeTypesReady={lifecycle.nodeTypesReady}
            onDeleteInput={pipeline.onDeleteInput}
            onRenameInput={pipeline.onRenameInput}
          />
        )}
      </div>
    </div>
    </LayoutDirContext.Provider>
    </PortsPanelContext.Provider>
  );
}
