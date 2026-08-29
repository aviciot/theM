'use client';
import React, { useCallback, useEffect, useRef, useState, type DragEvent, type MouseEvent } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { useReactFlow, type Node, type Edge, type NodeTypes, type EdgeTypes } from '@xyflow/react';
import { LayoutDirContext } from '../LayoutContext';
import { C, INITIAL_DEBUG, genUUID } from '../constants';
import { getNodeDef } from '@/lib/nodeRegistry';
import type { AgentRootData, DebugNodeState, SkillData, StepData } from '../types';
import type { AgentStepDoc } from '@/lib/api';
import { useAgentGraph } from '../hooks/useAgentGraph';
import { useSkillPipeline } from '../hooks/useSkillPipeline';
import { useDefinitionLifecycle } from '../hooks/useDefinitionLifecycle';
import { useBuilderHistory } from '../hooks/useBuilderHistory';
import { useDebugSession } from '../hooks/useDebugSession';
import { applyDagreLayout } from '../canvas/layout';
import { isDataEdge } from '../canvas/connections';
import { stepMeta } from './StepNode';
import { BuilderCanvas, nodeTypes, edgeTypes } from './BuilderCanvas';
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

  // ── Resizable panels ──────────────────────────────────────────────────────────
  const [libraryWidth, setLibraryWidth] = useState(220);
  const [propertiesWidth, setPropertiesWidth] = useState(300);
  const resizingRef = useRef<{ side: 'library' | 'properties'; startX: number; startW: number } | null>(null);

  useEffect(() => {
    function onMouseMove(e: globalThis.MouseEvent) {
      if (!resizingRef.current) return;
      const { side, startX, startW } = resizingRef.current;
      const delta = e.clientX - startX;
      if (side === 'library') {
        setLibraryWidth(Math.max(160, Math.min(480, startW + delta)));
      } else {
        setPropertiesWidth(Math.max(220, Math.min(600, startW - delta)));
      }
    }
    function onMouseUp() { resizingRef.current = null; document.body.style.cursor = ''; document.body.style.userSelect = ''; }
    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
    return () => { document.removeEventListener('mousemove', onMouseMove); document.removeEventListener('mouseup', onMouseUp); };
  }, []);

  // ── Hooks ─────────────────────────────────────────────────────────────────────
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

  // ── Stable node/edge type refs ────────────────────────────────────────────────
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stableNodeTypes = React.useMemo<NodeTypes>(() => nodeTypes, []);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stableEdgeTypes = React.useMemo<EdgeTypes>(() => edgeTypes, []);

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

  // ── Derived display values ────────────────────────────────────────────────────
  const debugNodes = graph.activeView === 'skill' && debugSession.debug.active
    ? pipeline.localPipeNodes.map(n => ({
        ...n,
        data: {
          ...n.data,
          _debug: {
            state: debugSession.debug.nodeStates[n.id] ?? 'idle',
            output: debugSession.debug.nodeOutputs[n.id],
            error: debugSession.debug.nodeErrors[n.id],
          },
        },
      }))
    : pipeline.localPipeNodes;

  const runningNodeId = Object.entries(debugSession.debug.nodeStates).find(([, s]) => s === 'running')?.[0];

  const debugEdges = graph.activeView === 'skill' && debugSession.debug.active
    ? pipeline.localPipeEdges.map(e => {
        const hasDoneValue = !!debugSession.debug.edgeValues[e.id];
        const isFlowing = runningNodeId === e.source;
        const edgeState: 'idle' | 'flowing' | 'done' = isFlowing ? 'flowing' : hasDoneValue ? 'done' : 'idle';
        return {
          ...e,
          type: 'debugEdge',
          data: {
            ...((e.data ?? {}) as Record<string, unknown>),
            debugState: edgeState,
            label: hasDoneValue ? `"${debugSession.debug.edgeValues[e.id]}"` : undefined,
          },
        };
      })
    : pipeline.localPipeEdges;

  const nodeValidationMap = (() => {
    const m: Record<string, 'error' | 'warning'> = {};
    for (const iss of lifecycle.validation.issues) {
      if (!iss.node_id) continue;
      const current = m[iss.node_id];
      if (!current || (iss.severity === 'error' && current === 'warning')) {
        m[iss.node_id] = iss.severity;
      }
    }
    return m;
  })();

  const pipelineIssues = graph.activeSkillId
    ? lifecycle.validation.issues.filter(iss => iss.skill_id === graph.activeSkillId || !iss.skill_id)
    : lifecycle.validation.issues;

  const validatedPipeNodes = pipeline.localPipeNodes.map(n => {
    const stepId = (n.data as unknown as StepData).step_id;
    const stepType = (n.data as unknown as StepData).step_type;
    const isStub = !getNodeDef(stepType).executable;
    const valSeverity = nodeValidationMap[stepId] ?? null;
    if (!valSeverity && !isStub) return n;
    return { ...n, data: { ...n.data, _validation: valSeverity, _stub: isStub } };
  });

  const errorCount   = lifecycle.validation.issues.filter(iss => iss.severity === 'error').length;
  const warningCount = lifecycle.validation.issues.filter(iss => iss.severity === 'warning').length;
  const debugRunning = Object.values(debugSession.debug.nodeStates).some(s => s === 'running');

  const logoState = (() => {
    if (lifecycle.saving || lifecycle.publishing || lifecycle.validation.loading || debugRunning) return 'thinking' as const;
    if (lifecycle.logoResult === 'invalid') return 'error' as const;
    if (lifecycle.logoResult === 'warn')    return 'warning' as const;
    if (lifecycle.logoResult === 'valid')   return 'success' as const;
    if (lifecycle.dirty) return 'dirty' as const;
    return 'idle' as const;
  })();

  const currentNodes = graph.activeView === 'agent'
    ? graph.agentNodes
    : debugSession.debug.active ? debugNodes : validatedPipeNodes;
  const currentEdges = graph.activeView === 'agent' ? graph.agentEdges : (debugSession.debug.active ? debugEdges : pipeline.localPipeEdges);

  return (
    <LayoutDirContext.Provider value={graph.layoutDir}>
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg }}>
      {/* Toolbar */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: '12px',
        padding: '12px 24px', borderBottom: `1px solid ${C.outline}`,
        background: C.surface, flexShrink: 0,
      }}>
        <button onClick={handleBack} style={{
          background: 'transparent', border: `1px solid ${C.outline}`, color: C.textMuted,
          padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
        }}>
          {graph.activeView === 'skill' ? 'Back to Agent' : 'Back to Agents'}
        </button>

        <div style={{ flex: 1 }}>
          {graph.activeView === 'agent' ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
              <input
                value={graph.agentSlug}
                onChange={e => { graph.setAgentSlug(e.target.value); history.markDirty(); }}
                placeholder="agent-slug (kebab-case)"
                style={{
                  background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff',
                  padding: '6px 12px', borderRadius: '6px', fontSize: '13px', width: '220px',
                }}
              />
              <span style={{ color: C.textMuted, fontSize: '12px' }}>Agent Builder</span>
            </div>
          ) : (
            <span style={{ color: C.purple, fontWeight: 600, fontSize: '14px' }}>
              Pipeline: {graph.activeSkillId}
            </span>
          )}
        </div>

        {lifecycle.saveError && (
          <span style={{ color: '#f87171', fontSize: '12px', maxWidth: '300px' }}>{lifecycle.saveError}</span>
        )}
        {lifecycle.publishedRevision !== null && (
          <span style={{ color: '#34d399', fontSize: '12px' }}>Published rev {lifecycle.publishedRevision}</span>
        )}

        {defId && (lifecycle.validation.loading || errorCount > 0 || warningCount > 0) && (
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            {lifecycle.validation.loading && (
              <span style={{ color: '#64748b', fontSize: '11px', fontStyle: 'italic' }}>validating…</span>
            )}
            {!lifecycle.validation.loading && errorCount > 0 && (
              <span style={{
                background: 'rgba(248,113,113,0.15)', border: '1px solid rgba(248,113,113,0.4)',
                color: '#f87171', padding: '3px 8px', borderRadius: '20px', fontSize: '11px', fontWeight: 700,
              }}>
                ✗ {errorCount} error{errorCount !== 1 ? 's' : ''}
              </span>
            )}
            {!lifecycle.validation.loading && warningCount > 0 && (
              <span style={{
                background: 'rgba(245,158,11,0.15)', border: '1px solid rgba(245,158,11,0.4)',
                color: '#f59e0b', padding: '3px 8px', borderRadius: '20px', fontSize: '11px', fontWeight: 700,
              }}>
                ⚠ {warningCount} warning{warningCount !== 1 ? 's' : ''}
              </span>
            )}
            {!lifecycle.validation.loading && errorCount === 0 && warningCount === 0 && lifecycle.validation.lastValidatedAt && (
              <span style={{ color: '#34d399', fontSize: '11px' }}>✓ valid</span>
            )}
          </div>
        )}

        {defId && (
          <button onClick={lifecycle.handleDelete} disabled={lifecycle.deleting} style={{
            background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.4)',
            color: '#f87171', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {lifecycle.deleting ? 'Deleting...' : 'Delete Draft'}
          </button>
        )}
        {defId && (
          <button onClick={lifecycle.handleValidate} disabled={lifecycle.validating || lifecycle.validation.loading} style={{
            background: 'rgba(52,211,153,0.1)', border: '1px solid rgba(52,211,153,0.4)',
            color: '#34d399', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {lifecycle.validating || lifecycle.validation.loading ? 'Validating…' : 'Validate'}
          </button>
        )}
        {defId && (
          <button
            onClick={lifecycle.handlePublish}
            disabled={lifecycle.publishing || errorCount > 0}
            title={errorCount > 0 ? `Fix ${errorCount} error${errorCount !== 1 ? 's' : ''} before publishing` : undefined}
            style={{
              background: (lifecycle.publishing || errorCount > 0) ? 'rgba(0,240,255,0.05)' : 'rgba(0,240,255,0.15)',
              border: '1px solid rgba(0,240,255,0.4)',
              color: errorCount > 0 ? 'rgba(0,240,255,0.4)' : '#00f0ff',
              padding: '6px 14px', borderRadius: '6px', cursor: errorCount > 0 ? 'not-allowed' : 'pointer', fontSize: '13px',
            }}
          >
            {lifecycle.publishing ? 'Publishing…' : 'Publish'}
          </button>
        )}
        {graph.activeView === 'skill' && (
          <button onClick={() => {
            if (debugSession.debug.active) {
              debugSession.setDebug(INITIAL_DEBUG);
            } else {
              debugSession.debugStartSetup();
            }
          }} style={{
            background: debugSession.debug.active ? 'rgba(245,158,11,0.2)' : 'rgba(100,116,139,0.1)',
            border: `1px solid ${debugSession.debug.active ? C.amber : C.outline}`,
            color: debugSession.debug.active ? C.amber : C.textMuted,
            padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {debugSession.debug.active ? '🐛 Exit Debug' : '🐛 Debug'}
          </button>
        )}
        {graph.activeView === 'agent' && !defId && (
          <>
            <input
              ref={lifecycle.importFileRef}
              type="file"
              accept=".json,application/json"
              style={{ display: 'none' }}
              onChange={lifecycle.handleImportFileChange}
            />
            <button onClick={lifecycle.handleImportJSON} style={{
              background: 'rgba(99,102,241,0.12)', border: `1px solid rgba(99,102,241,0.5)`,
              color: C.indigo, padding: '7px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
            }}>
              ↓ Import JSON
            </button>
          </>
        )}
        <button onClick={history.handleUndo} disabled={!history.canUndo} title="Undo" style={{
          background: 'transparent', border: `1px solid ${history.canUndo ? C.outline : 'transparent'}`,
          color: history.canUndo ? '#cbd5e1' : '#334155', padding: '6px 10px', borderRadius: '6px',
          cursor: history.canUndo ? 'pointer' : 'default', fontSize: '14px',
        }}>↩</button>
        <button onClick={history.handleRedo} disabled={!history.canRedo} title="Redo" style={{
          background: 'transparent', border: `1px solid ${history.canRedo ? C.outline : 'transparent'}`,
          color: history.canRedo ? '#cbd5e1' : '#334155', padding: '6px 10px', borderRadius: '6px',
          cursor: history.canRedo ? 'pointer' : 'default', fontSize: '14px',
        }}>↪</button>
        <button onClick={lifecycle.handleExport} title="Export as JSON file" style={{
          background: 'rgba(99,102,241,0.12)', border: `1px solid rgba(99,102,241,0.5)`,
          color: C.indigo, padding: '7px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
        }}>↑ Export JSON</button>
        <button onClick={lifecycle.handleSave} disabled={lifecycle.saving} style={{
          background: lifecycle.dirty ? C.cyan : 'rgba(0,240,255,0.2)',
          border: 'none', color: '#000', fontWeight: 700,
          padding: '7px 20px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          opacity: lifecycle.saving ? 0.7 : 1,
        }}>
          {lifecycle.saving ? 'Saving...' : defId ? 'Save Changes' : 'Create Draft'}
        </button>
      </div>

      {lifecycle.loadError && (
        <div style={{ background: 'rgba(239,68,68,0.1)', padding: '10px 24px', color: '#f87171', fontSize: '13px' }}>
          {lifecycle.loadError}
        </div>
      )}

      {graph.activeView === 'skill' && debugSession.debug.active && (
        <DebugPanel
          debug={debugSession.debug}
          setDebug={debugSession.setDebug}
          debugRunning={debugRunning}
          debugCommitSetup={debugSession.debugCommitSetup}
          debugRunAll={debugSession.debugRunAll}
          debugStep={debugSession.debugStep}
          debugReset={debugSession.debugReset}
        />
      )}

      {graph.activeView === 'skill' && pipelineIssues.length > 0 && !debugSession.debug.active && (
        <div style={{
          flexShrink: 0, maxHeight: '130px', overflowY: 'auto',
          borderBottom: `1px solid ${errorCount > 0 ? 'rgba(248,113,113,0.3)' : 'rgba(245,158,11,0.3)'}`,
          background: errorCount > 0 ? 'rgba(248,113,113,0.04)' : 'rgba(245,158,11,0.04)',
          padding: '6px 16px',
        }}>
          {pipelineIssues.map((iss, i) => (
            <div key={i} style={{ display: 'flex', alignItems: 'flex-start', gap: '8px', padding: '3px 0', borderBottom: i < pipelineIssues.length - 1 ? '1px solid rgba(255,255,255,0.04)' : 'none' }}>
              <span style={{ fontSize: '11px', color: iss.severity === 'error' ? '#f87171' : '#f59e0b', flexShrink: 0, marginTop: '1px' }}>
                {iss.severity === 'error' ? '✗' : '⚠'}
              </span>
              <span style={{ fontSize: '11px', color: '#e2e8f0', flex: 1 }}>
                <span style={{ fontFamily: 'monospace', color: '#94a3b8', marginRight: '6px' }}>[{iss.code}]</span>
                {iss.message}
                {iss.field && <span style={{ marginLeft: '6px', color: '#64748b' }}>· field: <code style={{ color: '#f59e0b' }}>{iss.field}</code></span>}
              </span>
            </div>
          ))}
        </div>
      )}

      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
        {/* Node Library */}
        <div style={{
          width: libraryWidth, flexShrink: 0, borderRight: `1px solid ${C.outline}`,
          background: C.surface, overflowY: 'auto', display: 'flex', flexDirection: 'column',
          position: 'relative',
        }} className="dark-scrollbar">
          <div style={{ padding: '14px 14px 8px', fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1.5, textTransform: 'uppercase', borderBottom: `1px solid ${C.outline}` }}>
            {graph.activeView === 'agent' ? 'Node Library' : 'Step Library'}
          </div>

          <div style={{ padding: '12px 10px', display: 'flex', flexDirection: 'column', gap: 6, flex: 1 }}>
            {graph.activeView === 'agent' ? (
              <>
                <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', marginBottom: 4 }}>Skills</div>
                <div
                  draggable
                  onDragStart={e => { e.dataTransfer.setData('nodeType', 'skill'); e.dataTransfer.effectAllowed = 'move'; }}
                  className="palette-card"
                  style={{
                    display: 'flex', alignItems: 'center', gap: 10, padding: '9px 12px',
                    borderRadius: 8, cursor: 'grab', userSelect: 'none',
                    background: C.purpleBg, border: `1px solid ${C.purpleBorder}`,
                  }}
                >
                  <span style={{ fontSize: 18 }}>⚡</span>
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 600, color: C.purple }}>Skill</div>
                    <div style={{ fontSize: 10, color: C.textMuted }}>Named capability</div>
                  </div>
                </div>
              </>
            ) : (
              <>
                {[
                  { label: 'Data Flow',  items: ['input', 'response'] },
                  { label: 'Processing', items: ['llm', 'transform', 'http', 'branch'] },
                  { label: 'Advanced',   items: ['loop', 'parallel', 'a2a_call', 'human_wait', 'stream_out'] },
                ].map(group => (
                  <div key={group.label}>
                    <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', margin: '8px 0 4px' }}>{group.label}</div>
                    {group.items.map(type => {
                      const def  = getNodeDef(type);
                      const meta = stepMeta(type);
                      return (
                        <div
                          key={type}
                          draggable
                          title={def.description}
                          onDragStart={e => { e.dataTransfer.setData('nodeType', 'step'); e.dataTransfer.setData('stepType', type); e.dataTransfer.effectAllowed = 'move'; }}
                          onClick={() => pipeline.addStepToActivePipeline(type as AgentStepDoc['type'])}
                          className="palette-card"
                          style={{
                            display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px',
                            borderRadius: 7, cursor: 'grab', userSelect: 'none', marginBottom: 3,
                            background: `${meta.border}18`, border: `1px solid ${meta.border}`,
                          }}
                        >
                          <span style={{ fontSize: 18, width: 22, textAlign: 'center', flexShrink: 0 }}>{meta.emoji}</span>
                          <div style={{ minWidth: 0 }}>
                            <div style={{ fontSize: 12, fontWeight: 600, color: meta.border }}>{meta.label}</div>
                            <div style={{ fontSize: 10, color: C.textMuted, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{def.description}</div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ))}
              </>
            )}
          </div>

          <div
            onMouseDown={e => { e.preventDefault(); resizingRef.current = { side: 'library', startX: e.clientX, startW: libraryWidth }; document.body.style.cursor = 'col-resize'; document.body.style.userSelect = 'none'; }}
            style={{
              position: 'absolute', top: 0, right: -3, width: 6, height: '100%',
              cursor: 'col-resize', zIndex: 10,
            }}
          />
        </div>

        <BuilderCanvas
          activeView={graph.activeView}
          logoState={logoState}
          currentNodes={currentNodes}
          currentEdges={currentEdges}
          onAgentNodesChange={graph.onAgentNodesChange}
          onAgentEdgesChange={graph.onAgentEdgesChange}
          onPipeNodesChange={pipeline.onPipeNodesChange}
          onPipeEdgesChange={pipeline.onPipeEdgesChange}
          debugNodes={debugNodes}
          debugEdges={debugEdges}
          localPipeNodes={pipeline.localPipeNodes}
          displayPipeEdges={pipeline.displayPipeEdges}
          onAgentConnect={graph.onAgentConnect}
          onPipeConnect={pipeline.onPipeConnect}
          onPipeConnectStart={pipeline.onPipeConnectStart}
          onPipeConnectEnd={pipeline.onPipeConnectEnd}
          isPipeConnectionValid={pipeline.isPipeConnectionValid}
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
            propertiesWidth={propertiesWidth}
            onResizeStart={(e) => { e.preventDefault(); resizingRef.current = { side: 'properties', startX: e.clientX, startW: propertiesWidth }; document.body.style.cursor = 'col-resize'; document.body.style.userSelect = 'none'; }}
            activeView={graph.activeView}
            agentNodes={graph.agentNodes}
            localPipeNodes={pipeline.localPipeNodes}
            localPipeEdges={pipeline.localPipeEdges}
            validationIssues={lifecycle.validation.issues}
            stepContracts={lifecycle.validation.stepContracts}
            debug={debugSession.debug}
            updateSelectedNodeField={updateSelectedNodeField}
            updateStepConfig={updateStepConfig}
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
  );
}
