'use client';
import '@xyflow/react/dist/style.css';
import React, { type DragEvent, type MouseEvent } from 'react';
import {
  ReactFlow, Background, BackgroundVariant, Controls, SelectionMode,
  type Node, type Edge, type Connection, type NodeTypes, type EdgeTypes,
} from '@xyflow/react';
import { C } from '../constants';
import type { LogoState } from '../types';
import { StepNode } from './StepNode';
import { AgentRootNode } from './AgentRootNode';
import { SkillNode } from './SkillNode';
import { DebugEdge } from '../edges/DebugEdge';
import { DataEdge } from '../edges/DataEdge';
import { BundleEdge } from '../edges/BundleEdge';
import { NodeContextMenu } from './NodeContextMenu';
import type { CtxTarget } from './NodeContextMenu';
import { CanvasLogo } from './CanvasLogo';

// nodeTypes MUST be defined outside the component for stable references.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const nodeTypes: NodeTypes = {
  agentRoot: AgentRootNode as any,
  skill:     SkillNode     as any,
  step:      StepNode      as any,
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const edgeTypes: EdgeTypes = { debugEdge: DebugEdge as any, dataEdge: DataEdge as any, bundleEdge: BundleEdge as any };

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type OnDrop = (e: DragEvent<any>) => void;

interface BuilderCanvasProps {
  activeView: 'agent' | 'skill';
  logoState: LogoState;
  currentNodes: Node[];
  currentEdges: Edge[];
  onAgentNodesChange: ReturnType<typeof import('@xyflow/react').useNodesState>[2];
  onAgentEdgesChange: ReturnType<typeof import('@xyflow/react').useEdgesState>[2];
  onPipeNodesChange: ReturnType<typeof import('@xyflow/react').useNodesState>[2];
  onPipeEdgesChange: ReturnType<typeof import('@xyflow/react').useEdgesState>[2];
  debugNodes: Node[];
  debugEdges: Edge[];
  localPipeNodes: Node[];
  displayPipeEdges: Edge[];
  onAgentConnect: (conn: Connection) => void;
  onPipeConnect: (conn: Connection) => void;
  onPipeConnectStart: (e: unknown, params: { nodeId: string | null; handleId: string | null; handleType: string | null }) => void;
  onPipeConnectEnd: () => void;
  isPipeConnectionValid: (conn: Connection | Edge) => boolean;
  onClosePopovers: () => void;
  onNodeCtx: (e: MouseEvent, node: Node) => void;
  onEdgeCtx: (e: MouseEvent, edge: Edge) => void;
  onAgentNodeDoubleClick: (e: MouseEvent, node: Node) => void;
  onPipeNodeDoubleClick: (e: MouseEvent, node: Node) => void;
  setSelectedNode: (n: Node | null) => void;
  closeCtx: () => void;
  ctxMenu: { x: number; y: number; target: CtxTarget } | null;
  ctxDelete: () => void;
  ctxEditPipeline: () => void;
  fitView: (opts?: { padding?: number }) => void;
  applyDagreAndFit: () => void;
  toggleLayoutDir: () => void;
  layoutDir: 'LR' | 'TB';
  debugActive: boolean;
  onAgentDrop: OnDrop;
  onPipeDrop: OnDrop;
  stableNodeTypes: NodeTypes;
  stableEdgeTypes: EdgeTypes;
}

export function BuilderCanvas({
  activeView, logoState, currentNodes, currentEdges,
  onAgentNodesChange, onAgentEdgesChange, onPipeNodesChange, onPipeEdgesChange,
  debugNodes, debugEdges, localPipeNodes, displayPipeEdges,
  onAgentConnect, onPipeConnect, onPipeConnectStart, onPipeConnectEnd, isPipeConnectionValid,
  onClosePopovers, onNodeCtx, onEdgeCtx, onAgentNodeDoubleClick, onPipeNodeDoubleClick,
  setSelectedNode, closeCtx, ctxMenu, ctxDelete, ctxEditPipeline,
  fitView, applyDagreAndFit, toggleLayoutDir, layoutDir, debugActive,
  onAgentDrop, onPipeDrop, stableNodeTypes, stableEdgeTypes,
}: BuilderCanvasProps) {
  return (
    <div style={{ flex: 1, position: 'relative' }}>
      <style>{`.react-flow__pane { cursor: default !important; } .react-flow__pane.dragging { cursor: default !important; } .react-flow__node.selected > div { box-shadow: 0 0 0 2px #00f0ff, 0 0 14px rgba(0,240,255,0.35) !important; } .palette-card { transition: filter 0.15s, box-shadow 0.15s; } .palette-card:hover { filter: brightness(1.7) saturate(1.2); box-shadow: 0 0 10px rgba(255,255,255,0.1), 0 2px 8px rgba(0,0,0,0.3); }`}</style>

      <div style={{
        position: 'absolute', top: 12, right: 12, zIndex: 10,
        display: 'flex', gap: 6,
      }}>
        <button
          onClick={() => fitView({ padding: 0.15 })}
          title="Fit to screen"
          style={{ background: C.surface, border: `1px solid ${C.outline}`, color: C.textMuted, borderRadius: 6, padding: '5px 10px', cursor: 'pointer', fontSize: 18, lineHeight: 1 }}
        >⊡</button>
        <button
          onClick={applyDagreAndFit}
          title="Auto-arrange nodes"
          style={{ background: C.surface, border: `1px solid ${C.outline}`, color: C.textMuted, borderRadius: 6, padding: '5px 10px', cursor: 'pointer', fontSize: 18, lineHeight: 1 }}
        >⚏</button>
        <button
          onClick={toggleLayoutDir}
          title={layoutDir === 'TB' ? 'Switch to horizontal layout' : 'Switch to vertical layout'}
          style={{ background: C.surface, border: `1px solid ${C.outline}`, color: C.textMuted, borderRadius: 6, padding: '5px 10px', cursor: 'pointer', fontSize: 14, lineHeight: 1, fontWeight: 600 }}
        >{layoutDir === 'TB' ? '⇆' : '⇅'}</button>
      </div>

      {ctxMenu && (
        <NodeContextMenu
          ctxMenu={ctxMenu}
          closeCtx={closeCtx}
          ctxDelete={ctxDelete}
          ctxEditPipeline={ctxEditPipeline}
          setSelectedNode={setSelectedNode}
        />
      )}

      <CanvasLogo state={logoState} />

      {activeView === 'agent' ? (
        <ReactFlow
          nodes={currentNodes}
          edges={currentEdges}
          onNodesChange={onAgentNodesChange}
          onEdgesChange={onAgentEdgesChange}
          onConnect={onAgentConnect}
          onNodeContextMenu={onNodeCtx}
          onEdgeContextMenu={onEdgeCtx}
          onNodeClick={(_: MouseEvent, node: Node) => { setSelectedNode(node); closeCtx(); onClosePopovers(); }}
          onNodeDoubleClick={onAgentNodeDoubleClick}
          onPaneClick={() => { setSelectedNode(null); closeCtx(); onClosePopovers(); }}
          nodeTypes={stableNodeTypes}
          panOnDrag={[1]}
          selectionMode={SelectionMode.Partial}
          multiSelectionKeyCode={['Shift', 'Control']}
          onDragOver={(e: DragEvent) => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; }}
          onDrop={onAgentDrop}
          fitView
        >
          <Background variant={BackgroundVariant.Dots} gap={20} color="rgba(255,255,255,0.05)" />
          <Controls />
        </ReactFlow>
      ) : (
        <ReactFlow
          nodes={debugActive ? debugNodes : localPipeNodes}
          edges={debugActive ? debugEdges : displayPipeEdges}
          onNodesChange={onPipeNodesChange}
          onEdgesChange={onPipeEdgesChange}
          onConnect={onPipeConnect}
          onConnectStart={onPipeConnectStart}
          onConnectEnd={onPipeConnectEnd}
          isValidConnection={isPipeConnectionValid}
          onNodeContextMenu={onNodeCtx}
          onEdgeContextMenu={onEdgeCtx}
          onNodeClick={(_: MouseEvent, node: Node) => { setSelectedNode(node); closeCtx(); onClosePopovers(); }}
          onNodeDoubleClick={onPipeNodeDoubleClick}
          onPaneClick={() => { setSelectedNode(null); closeCtx(); onClosePopovers(); }}
          nodeTypes={stableNodeTypes}
          edgeTypes={stableEdgeTypes}
          panOnDrag={[1]}
          selectionMode={SelectionMode.Partial}
          multiSelectionKeyCode={['Shift', 'Control']}
          onDragOver={(e: DragEvent) => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; }}
          onDrop={onPipeDrop}
          fitView
        >
          <Background variant={BackgroundVariant.Dots} gap={20} color="rgba(255,255,255,0.05)" />
          <Controls />
        </ReactFlow>
      )}
    </div>
  );
}

export { nodeTypes, edgeTypes };
