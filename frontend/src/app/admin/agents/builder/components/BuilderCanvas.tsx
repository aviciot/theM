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

// nodeTypes MUST be defined outside the component for stable references.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const nodeTypes: NodeTypes = {
  agentRoot: AgentRootNode as any,
  skill:     SkillNode     as any,
  step:      StepNode      as any,
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const edgeTypes: EdgeTypes = { debugEdge: DebugEdge as any, dataEdge: DataEdge as any, bundleEdge: BundleEdge as any };

// ── Canvas Logo ───────────────────────────────────────────────────────────────
type LogoStateDef = { opacity: number; filter: string; animation: string; }
const LOGO_STATES: Record<LogoState, LogoStateDef> = {
  idle:     { opacity: 0.015, filter: 'none',   animation: 'none' },
  dirty:    { opacity: 0.015, filter: 'none',   animation: 'none' },
  warning:  { opacity: 0.45, filter: 'drop-shadow(0 0 18px rgba(255,120,120,0.4))',   animation: 'logo-warn-flash 1.2s ease-in-out 1 forwards' },
  error:    { opacity: 0.35, filter: 'drop-shadow(0 0 18px rgba(255,107,138,0.4))',   animation: 'logo-shake 0.5s ease-in-out' },
  success:  { opacity: 1.0,  filter: 'drop-shadow(0 0 40px rgba(74,222,128,0.9))',    animation: 'logo-burst 1.8s ease-out forwards' },
  thinking: { opacity: 1.0,  filter: 'none',                                           animation: 'none' },
};
const LOGO_KEYFRAMES = `
@keyframes logo-shake { 0%,100%{transform:translateX(0)} 15%{transform:translateX(-10px) rotate(-2deg)} 30%{transform:translateX(10px) rotate(2deg)} 45%{transform:translateX(-8px) rotate(-1deg)} 60%{transform:translateX(8px) rotate(1deg)} 75%{transform:translateX(-4px)} 90%{transform:translateX(4px)} }
@keyframes logo-burst { 0%{opacity:0.13;filter:drop-shadow(0 0 18px rgba(0,240,255,0.18))} 15%{opacity:1;filter:drop-shadow(0 0 80px rgba(74,222,128,1)) drop-shadow(0 0 40px rgba(255,255,255,0.8))} 100%{opacity:0.13;filter:drop-shadow(0 0 18px rgba(0,240,255,0.18))} }
@keyframes logo-explode { 0%{transform:translate(0,0) scale(1) rotate(0deg);opacity:1} 20%{transform:translate(calc(var(--ex)*60px),calc(var(--ey)*60px)) scale(1.15) rotate(var(--rot));opacity:1} 55%{transform:translate(calc(var(--ex)*140px),calc(var(--ey)*140px)) scale(0.7) rotate(calc(var(--rot)*2));opacity:0.6} 80%{transform:translate(calc(var(--ex)*180px),calc(var(--ey)*180px)) scale(0.3) rotate(calc(var(--rot)*3));opacity:0} 81%{transform:translate(0,0) scale(0) rotate(0deg);opacity:0} 100%{transform:translate(0,0) scale(1) rotate(0deg);opacity:1} }
@keyframes logo-polygon-flicker { 0%,100%{opacity:0.08;fill:#4ab8a0} 50%{opacity:0.55;fill:#00b8c8;filter:drop-shadow(0 0 6px rgba(0,180,200,0.6))} }
@keyframes logo-warn-flash { 0%{opacity:0.18;filter:drop-shadow(0 0 12px rgba(255,120,120,0.15))} 40%{opacity:0.48;filter:drop-shadow(0 0 22px rgba(255,120,120,0.5))} 100%{opacity:0.18;filter:drop-shadow(0 0 12px rgba(255,120,120,0.15))} }
`;
const LOGO_PATHS: Array<{ id: string; points: string; ex: number; ey: number }> = [
  { id: 'part-01', ex: -0.5, ey: -1.0, points: "88,77 184,146 244,191 281,217 336,259 355,272 358,272 367,267 372,266 379,262 391,258 433,239 440,237 473,222 513,206 520,202 546,192 555,187 558,187 446,102 433,91 421,83 403,68 397,65 392,60 331,15 318,4 274,19 264,21 246,28 239,29 217,37 214,37 211,39 201,41 189,46 186,46 154,57 151,57 148,59 141,60 138,62 104,73 101,73 98,75" },
  { id: 'part-02', ex:  0.5, ey: -1.0, points: "1323,77 1313,75 1292,67 1289,67 1239,50 1236,50 1233,48 1230,48 1189,34 1176,31 1094,4 1085,12 1074,19 1053,36 959,106 855,187 876,196 881,197 973,237 980,239 1034,263 1048,268 1055,272 1059,272 1139,213 1146,209 1177,185 1188,178 1208,162 1284,107" },
  { id: 'part-03', ex: -1.2, ey:  0.1, points: "70,97 70,334 71,335 72,350 76,365 104,429 108,435 180,486 184,490 245,534 345,609 339,293 305,269 281,250 252,230 182,177 179,176 153,156 150,155" },
  { id: 'part-04', ex:  1.2, ey:  0.1, points: "1342,97 1252,162 1248,166 1152,236 1148,240 1126,255 1122,259 1112,265 1103,273 1074,293 1073,296 1073,317 1072,318 1072,355 1071,356 1071,415 1070,416 1070,461 1069,462 1069,526 1068,527 1067,609 1306,433 1325,392 1336,365 1341,343 1341,331 1342,330" },
  { id: 'part-05', ex: -0.4, ey: -0.6, points: "682,361 576,210 381,292 532,410 577,395 580,395 586,392 595,390 613,384 616,382 622,381 664,367 667,365" },
  { id: 'part-06', ex:  0.4, ey: -0.6, points: "732,361 803,384 806,386 809,386 831,394 834,394 860,404 863,404 881,410 1033,291 837,210 764,315 760,319 759,322 740,348" },
  { id: 'part-07', ex: -0.8, ey:  0.0, points: "367,314 367,373 368,374 368,430 369,431 371,567 380,574 383,575 388,580 396,585 505,669 508,611 509,610 509,595 510,594 512,540 513,539 513,524 514,523 514,504 515,503 515,490 516,489 517,454 518,453 518,434 519,433 504,421 501,420 490,410 468,394 427,361 423,359 395,336 392,335" },
  { id: 'part-08', ex:  0.8, ey:  0.0, points: "1046,314 894,433 895,456 896,457 896,475 897,476 897,494 898,495 898,513 899,514 901,561 902,562 902,579 903,580 903,594 904,595 906,650 907,651 907,666 908,669 934,648 971,621 1041,567" },
  { id: 'part-09', ex: -0.3, ey: -0.3, points: "549,424 693,539 693,534 694,533 693,532 693,377 676,382 664,387 660,387 657,389 654,389 635,396 632,396" },
  { id: 'part-10', ex:  0.3, ey: -0.3, points: "864,424 815,407 812,407 791,400 779,395 776,395 721,377 721,539 732,529 736,527 752,513" },
  { id: 'part-11', ex: -0.4, ey:  0.5, points: "535,446 532,511 531,512 531,531 530,532 530,546 529,547 529,567 528,568 527,600 526,601 526,616 525,617 525,634 524,635 524,650 523,651 523,662 522,663 522,682 543,697 628,763 640,771 645,776 649,778 692,812 693,809 693,799 692,798 692,793 693,792 693,572 685,567 679,561 675,559 649,537 611,508 605,502 602,501" },
  { id: 'part-12', ex:  0.4, ey:  0.5, points: "878,446 721,572 721,594 720,595 720,775 721,776 720,780 721,781 721,812 752,787 756,785 816,738 892,681 891,669 890,668 890,650 889,649 889,633 888,632 888,619 887,618 885,567 884,566 884,554 883,553 883,532 882,531 882,513 881,512" },
  { id: 'part-13', ex: -1.3, ey:  0.8, points: "100,461 95,488 89,506 87,509 86,515 77,534 75,541 55,582 38,613 16,647 13,656 13,662 16,670 26,679 42,685 62,690 67,693 74,700 76,705 76,720 68,743 68,749 70,755 75,763 87,770 97,772 125,772 126,771 130,772 128,775 112,781 89,784 83,791 81,797 81,805 83,811 89,818 100,824 105,829 109,836 111,843 111,860 105,889 105,910 108,922 115,933 121,939 173,974 286,1057 326,1088 345,1105 345,641" },
  { id: 'part-14', ex:  1.3, ey:  0.8, points: "1312,462 1273,489 1230,522 1227,523 1143,586 1067,641 1067,1106 1080,1093 1135,1050 1138,1049 1172,1023 1235,978 1239,974 1249,968 1253,964 1256,963 1270,952 1292,938 1301,928 1305,920 1307,912 1308,897 1307,896 1307,888 1301,858 1302,839 1307,829 1312,824 1323,818 1328,813 1331,806 1331,796 1330,792 1324,784 1311,783 1297,780 1286,776 1282,773 1284,771 1287,772 1316,772 1328,769 1335,765 1340,760 1344,750 1344,742 1336,717 1336,706 1339,699 1344,694 1353,689 1371,685 1386,679 1394,673 1399,663 1399,655 1397,649 1372,610 1339,546 1321,500 1321,497 1316,484" },
];
const LOGO_COLOR = '#a0f0d0';
const THINK_DELAYS  = LOGO_PATHS.map((_, i) => +(((i * 2654435761) >>> 0) / 0xffffffff * 2.4).toFixed(2));
const THINK_DURATIONS = LOGO_PATHS.map((_, i) => +(0.9 + (((i + 7) * 2246822519) >>> 0) / 0xffffffff * 1.4).toFixed(2));

function CanvasLogo({ state }: { state: LogoState }) {
  const def = LOGO_STATES[state];
  const key = (state === 'idle' || state === 'dirty') ? 'calm' : state;
  const isExplode  = state === 'success';
  const isThinking = state === 'thinking';
  return (
    <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none', zIndex: 0 }}>
      <style>{LOGO_KEYFRAMES}</style>
      <svg key={key} xmlns="http://www.w3.org/2000/svg" width={720} height={572} viewBox="0 0 1407 1118" overflow="visible"
        style={{ opacity: def.opacity, animation: def.animation, filter: def.filter, overflow: 'visible' }}>
        {LOGO_PATHS.map(({ id, points, ex, ey }, i) => (
          <polygon key={id} points={points}
            style={isExplode ? {
              // @ts-ignore
              '--ex': ex, '--ey': ey, '--rot': `${(ex + ey) * 45}deg`,
              fill: LOGO_COLOR,
              animation: 'logo-explode 1.8s cubic-bezier(0.25,0.46,0.45,0.94) forwards',
              animationDelay: `${i * 0.06}s`,
              transformOrigin: 'center', transformBox: 'fill-box',
            } as React.CSSProperties : isThinking ? {
              animation: `logo-polygon-flicker ${THINK_DURATIONS[i]}s ease-in-out ${THINK_DELAYS[i]}s infinite`,
            } as React.CSSProperties : { fill: state === 'warning' ? '#ff8080' : LOGO_COLOR }}
          />
        ))}
      </svg>
    </div>
  );
}

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
  onNodeCtx, onEdgeCtx, onAgentNodeDoubleClick, onPipeNodeDoubleClick,
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
          onNodeClick={(_: MouseEvent, node: Node) => { setSelectedNode(node); closeCtx(); }}
          onNodeDoubleClick={onAgentNodeDoubleClick}
          onPaneClick={() => { setSelectedNode(null); closeCtx(); }}
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
          onNodeClick={(_: MouseEvent, node: Node) => { setSelectedNode(node); closeCtx(); }}
          onNodeDoubleClick={onPipeNodeDoubleClick}
          onPaneClick={() => { setSelectedNode(null); closeCtx(); }}
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
