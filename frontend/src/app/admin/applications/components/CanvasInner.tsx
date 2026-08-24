'use client';
import { useState, useEffect, type DragEvent } from 'react';
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  MiniMap,
  addEdge,
  useReactFlow,
  type Node,
  type Edge,
  type Connection,
} from '@xyflow/react';
import { AppLayoutDirContext } from '../AppLayoutContext';
import type {
  OrchestratorData,
  EntryPointData,
  ChainStatus,
  EpPickerEntry,
  LogoState,
  CanvasRule,
} from '../types';
import { C, CANVAS_STYLES, EDGE_STYLE, CANVAS_RULES, NODE_PORTS, glass } from '../constants';
import { NODE_TYPES } from './CanvasNodes';
import { CanvasLogo } from './CanvasLogo';

// ── Connection validator ──────────────────────────────────────────────────────
export function validateConnection(
  sourceType: string,
  targetType: string,
  sourceId: string,
  targetId: string,
  edges: Edge[],
): string | null {
  const src = NODE_PORTS[sourceType];
  const tgt = NODE_PORTS[targetType];
  if (!src || !tgt) return `Unknown node type`;

  const compatible = src.emits.some(sig => tgt.accepts.includes(sig));
  if (!compatible) return `Cannot connect ${sourceType} → ${targetType}`;

  if (edges.some(e => e.source === sourceId && e.target === targetId)) {
    return `These nodes are already connected`;
  }

  if (src.maxOutgoing !== undefined) {
    const out = edges.filter(e => e.source === sourceId).length;
    if (out >= src.maxOutgoing) return `Entry point already has an orchestrator — remove it first`;
  }

  if (tgt.maxIncoming !== undefined) {
    const inc = edges.filter(e => e.target === targetId).length;
    if (inc >= tgt.maxIncoming) return `This node already has the maximum number of incoming connections`;
  }

  return null;
}

// ── Error node map ────────────────────────────────────────────────────────────
export function getErrorNodeMap(nodes: Node[], edges: Edge[]): Map<string, string> {
  const ctx = { nodes, edges };
  const result = new Map<string, string>();
  for (const rule of CANVAS_RULES) {
    const msg = rule.message(ctx);
    if (msg && rule.errorNodeIds) {
      const ids = rule.errorNodeIds(ctx);
      for (const id of ids) {
        if (!result.has(id)) result.set(id, msg);
      }
    }
  }
  return result;
}

// ── Rule runner ───────────────────────────────────────────────────────────────
export function runRules(nodes: Node[], edges: Edge[], mode: 'save' | 'deploy'): { ok: boolean; message: string | null; warnings: string[] } {
  const ctx = { nodes, edges };
  for (const rule of CANVAS_RULES) {
    if (rule.severity === 'block') {
      const msg = rule.message(ctx);
      if (msg) return { ok: false, message: msg, warnings: [] };
    }
  }
  const warnings: string[] = [];
  for (const rule of CANVAS_RULES) {
    if (rule.severity === 'warn') {
      const msg = rule.message(ctx);
      if (msg) {
        if (mode === 'deploy') return { ok: false, message: msg, warnings: [] };
        warnings.push(msg);
      }
    }
  }
  return { ok: true, message: null, warnings };
}

// ── Chain analysis ────────────────────────────────────────────────────────────
export function analyzeChain(nodes: Node[], edges: Edge[]): ChainStatus {
  const result = runRules(nodes, edges, 'save');
  if (!result.ok) return { ready: false, label: result.message!, color: C.error, agentCount: 0 };

  const epNodes = nodes.filter(n => n.type === 'entryPoint');
  const orchNodes = nodes.filter(n => n.type === 'orchestrator');
  const agentCount = nodes.filter(n => n.type === 'agent').length;
  const epNode = epNodes[0];
  const orchEdge = edges.find(e => e.source === epNode.id);
  const orchNode = orchEdge ? nodes.find(n => n.id === orchEdge.target) : undefined;

  const warnLabel = result.warnings.length > 0 ? ` · ${result.warnings[0]}` : '';
  return {
    ready: true,
    label: `Ready · ${epNodes.length} EP · ${orchNodes.length} Orch · ${agentCount} agent${agentCount !== 1 ? 's' : ''}${warnLabel}`,
    color: result.warnings.length > 0 ? C.amber : C.green,
    epNode,
    orchNode,
    agentCount,
  };
}

// ── Styled edges ──────────────────────────────────────────────────────────────
export function styledEdges(edges: Edge[], nodes: Node[]): Edge[] {
  const chainEdgeIds = new Set<string>();
  const epNodes = nodes.filter(n => n.type === 'entryPoint');
  for (const epNode of epNodes) {
    const orchEdge = edges.find(e => e.source === epNode.id && nodes.some(n => n.id === e.target && n.type === 'orchestrator'));
    if (!orchEdge) continue;
    chainEdgeIds.add(orchEdge.id);
    const orchNode = nodes.find(n => n.id === orchEdge.target)!;
    for (const downEdge of edges.filter(e => e.source === orchNode.id)) {
      chainEdgeIds.add(downEdge.id);
    }
  }
  return edges.map(e => ({
    ...e,
    animated: chainEdgeIds.has(e.id),
    style: chainEdgeIds.has(e.id)
      ? { stroke: C.cyan, strokeWidth: 2 }
      : { stroke: C.error, strokeWidth: 1.5, strokeDasharray: '5 4' },
  }));
}

export function toSlug(s: string) {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64);
}

// ── EpPickerModal ─────────────────────────────────────────────────────────────
export function EpPickerModal({ entries, onSelect, onClose }: { entries: EpPickerEntry[]; onSelect: (e: EpPickerEntry) => void; onClose: () => void; }) {
  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };
  return (
    <div style={{ position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', background: 'rgba(5,20,36,0.85)', zIndex: 9999, display: 'flex', alignItems: 'center', justifyContent: 'center' }} onClick={onClose}>
      <div style={{ ...glass, borderRadius: 16, padding: '28px 32px', minWidth: 360, maxWidth: 480 }} onClick={e => e.stopPropagation()}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
          <div style={{ fontSize: 14, fontWeight: 700, color: C.text }}>Choose Entry Point to Test</div>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center' }}>
            <span className="material-symbols-outlined" style={{ fontSize: 20 }}>close</span>
          </button>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {entries.map(entry => (
            <button key={entry.slug} onClick={() => onSelect(entry)} style={{
              padding: '12px 16px', borderRadius: 10, border: `1px solid ${C.outlineVariant}`,
              background: C.surfaceLow, color: C.text, cursor: 'pointer', textAlign: 'left',
              display: 'flex', alignItems: 'center', gap: 12, transition: 'border-color 0.15s, background 0.15s',
            }}
              onMouseEnter={e => { e.currentTarget.style.borderColor = C.cyan; e.currentTarget.style.background = 'rgba(0,240,255,0.05)'; }}
              onMouseLeave={e => { e.currentTarget.style.borderColor = C.outlineVariant; e.currentTarget.style.background = C.surfaceLow; }}
            >
              <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.cyan, flexShrink: 0 }}>{EP_MS_ICON[entry.epType] ?? 'bolt'}</span>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 600 }}>{entry.label || entry.slug}</div>
                <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', marginTop: 2 }}>{entry.slug}</div>
                {entry.orchName && <div style={{ fontSize: 11, color: C.purple, marginTop: 2 }}>{entry.orchName}</div>}
              </div>
              <span className="material-symbols-outlined" style={{ fontSize: 16, color: C.textMuted, marginLeft: 'auto', flexShrink: 0 }}>arrow_forward</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

// ── CanvasInner ───────────────────────────────────────────────────────────────
export function CanvasInner({
  nodes, edges, onNodesChange, onEdgesChange, onConnect, onDrop, onDragOver, selectedNode, setSelectedNode, onUpdateNode, onDeleteEdge, onAutoLayout, onToggleLayout, layoutDir, onNodesDelete, logoState, advisorOpen, onAdvisorOpen,
}: {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: any;
  onEdgesChange: any;
  onConnect: (c: Connection) => void;
  onDrop: (e: DragEvent<HTMLDivElement>) => void;
  onDragOver: (e: DragEvent<HTMLDivElement>) => void;
  selectedNode: Node | null;
  setSelectedNode: (n: Node | null) => void;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
  onDeleteEdge: (edgeId: string) => void;
  onAutoLayout: () => void;
  onToggleLayout?: () => void;
  layoutDir?: 'TB' | 'LR';
  onNodesDelete?: () => void;
  logoState: LogoState;
  advisorOpen: boolean;
  onAdvisorOpen: () => void;
}) {
  const { fitView, zoomIn, zoomOut, getZoom, setViewport, getViewport } = useReactFlow();
  const [zoom, setZoom] = useState(100);
  const visualEdges = styledEdges(edges, nodes);

  useEffect(() => {
    const id = setInterval(() => {
      setZoom(Math.round(getZoom() * 100));
    }, 250);
    return () => clearInterval(id);
  }, [getZoom]);

  function handleSliderChange(v: number) {
    setZoom(v);
    const vp = getViewport();
    setViewport({ ...vp, zoom: v / 100 });
  }

  const iconBtn: React.CSSProperties = {
    width: 30, height: 30, borderRadius: 6, border: 'none', cursor: 'pointer',
    background: 'transparent', color: C.textMuted, display: 'flex', alignItems: 'center',
    justifyContent: 'center', transition: 'all 0.15s', flexShrink: 0,
  };

  return (
    <AppLayoutDirContext.Provider value={layoutDir ?? 'TB'}>
    <div style={{ flex: 1, position: 'relative', height: '100%' }}>
      <style>{CANVAS_STYLES}</style>
      {/* Canvas toolbar */}
      <div style={{
        position: 'absolute', top: 14, left: '50%', transform: 'translateX(-50%)',
        zIndex: 10, display: 'flex', alignItems: 'center', gap: 4,
        ...glass, borderRadius: 10, padding: '5px 10px',
      }}>
        <button
          onClick={() => { zoomOut(); }}
          title="Zoom out"
          style={iconBtn}
          onMouseEnter={e => (e.currentTarget.style.color = C.text)}
          onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
        </button>
        <input
          type="range" min={10} max={200} step={10} value={zoom}
          onChange={e => handleSliderChange(Number(e.target.value))}
          title="Zoom level"
          style={{ width: 72, accentColor: C.cyan, cursor: 'pointer', margin: '0 2px' }}
        />
        <span style={{ fontSize: 11, color: C.textMuted, minWidth: 36, textAlign: 'center', fontFamily: 'JetBrains Mono, monospace' }}>
          {zoom}%
        </span>
        <button
          onClick={() => { zoomIn(); }}
          title="Zoom in"
          style={iconBtn}
          onMouseEnter={e => (e.currentTarget.style.color = C.text)}
          onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="12" y1="5" x2="12" y2="19"/>
            <line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
        </button>
        <div style={{ width: 1, height: 18, background: C.outlineVariant, margin: '0 4px' }} />
        <button
          onClick={() => fitView({ padding: 0.15 })}
          title="Fit to screen"
          style={iconBtn}
          onMouseEnter={e => (e.currentTarget.style.color = C.cyan)}
          onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="15 3 21 3 21 9"/>
            <polyline points="9 21 3 21 3 15"/>
            <line x1="21" y1="3" x2="14" y2="10"/>
            <line x1="3" y1="21" x2="10" y2="14"/>
          </svg>
        </button>
        <div style={{ width: 1, height: 18, background: C.outlineVariant, margin: '0 4px' }} />
        <button
          onClick={() => { onAutoLayout(); setTimeout(() => fitView({ padding: 0.2 }), 50); }}
          title="Auto-arrange nodes"
          style={iconBtn}
          onMouseEnter={e => (e.currentTarget.style.color = C.purple)}
          onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="2" y="3" width="6" height="6" rx="1"/>
            <rect x="9" y="3" width="6" height="6" rx="1"/>
            <rect x="16" y="3" width="6" height="6" rx="1"/>
            <line x1="5" y1="9" x2="5" y2="21"/>
            <line x1="12" y1="9" x2="12" y2="21"/>
            <line x1="19" y1="9" x2="19" y2="21"/>
          </svg>
        </button>
        {onToggleLayout && (
          <button
            onClick={() => { onToggleLayout(); setTimeout(() => fitView({ padding: 0.2 }), 50); }}
            title={layoutDir === 'LR' ? 'Switch to vertical layout' : 'Switch to horizontal layout'}
            style={{ ...iconBtn, fontSize: 14, fontWeight: 700 }}
            onMouseEnter={e => (e.currentTarget.style.color = C.cyan)}
            onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
          >
            {layoutDir === 'LR' ? '⇅' : '⇆'}
          </button>
        )}
        <div style={{ width: 1, height: 18, background: C.outlineVariant, margin: '0 4px' }} />
        <button
          onClick={onAdvisorOpen}
          title="AI Workflow Advisor"
          style={{
            ...iconBtn,
            width: 'auto', height: 30, padding: '0 10px', gap: 5,
            display: 'flex', alignItems: 'center', borderRadius: 6,
            border: advisorOpen ? `1px solid rgba(0,240,255,0.35)` : '1px solid transparent',
            background: advisorOpen ? 'rgba(0,240,255,0.08)' : 'transparent',
            color: advisorOpen ? C.cyan : C.textMuted,
          }}
          onMouseEnter={e => { if (!advisorOpen) { e.currentTarget.style.color = C.cyan; e.currentTarget.style.border = `1px solid rgba(0,240,255,0.2)`; } }}
          onMouseLeave={e => { if (!advisorOpen) { e.currentTarget.style.color = C.textMuted; e.currentTarget.style.border = '1px solid transparent'; } }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>assistant</span>
          <span style={{ fontSize: 11, fontWeight: 600 }}>AI Advisor</span>
        </button>
      </div>

      <ReactFlow
        nodes={nodes}
        edges={visualEdges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onDrop={onDrop}
        onDragOver={onDragOver}
        nodeTypes={NODE_TYPES}
        onNodeClick={(_evt: React.MouseEvent, node: Node) => setSelectedNode(node)}
        onPaneClick={() => setSelectedNode(null)}
        onEdgeDoubleClick={(_evt: React.MouseEvent, edge: Edge) => onDeleteEdge(edge.id)}
        onNodesDelete={() => onNodesDelete?.()}
        onEdgesDelete={() => onNodesDelete?.()}
        fitView
        fitViewOptions={{ padding: 0.15 }}
        style={{ background: C.bg }}
        defaultEdgeOptions={{ animated: true, style: EDGE_STYLE }}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} color="rgba(132,148,149,0.15)" gap={22} size={1} />
        <CanvasLogo state={logoState} />
        <MiniMap
          style={{ background: C.surfaceLow, border: `1px solid ${C.outlineVariant}`, borderRadius: 8 }}
          nodeColor={(n: Node) => n.type === 'entryPoint' ? C.cyan : n.type === 'orchestrator' ? C.purple : n.type === 'middleware' ? C.amber : C.green}
          maskColor="rgba(5,20,36,0.7)"
        />
      </ReactFlow>
    </div>
    </AppLayoutDirContext.Provider>
  );
}

// ── CanvasInnerWithDrop ───────────────────────────────────────────────────────
export function CanvasInnerWithDrop({
  onDropWithInstance,
  ...props
}: Omit<Parameters<typeof CanvasInner>[0], 'onDrop'> & {
  onDropWithInstance: (e: DragEvent<HTMLDivElement>, rfInstance: ReturnType<typeof useReactFlow>) => void;
}) {
  const rfInstance = useReactFlow();
  return (
    <CanvasInner
      {...props}
      onDrop={e => onDropWithInstance(e, rfInstance)}
    />
  );
}
