'use client';
import React, { useCallback, useEffect, useRef, useState, type MouseEvent, type DragEvent } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
const dagre: any = (typeof window !== 'undefined' ? require('dagre') : null); // eslint-disable-line @typescript-eslint/no-explicit-any
import { getNodeDef, fetchNodeTypes, setCachedNodeTypes, canAddIncoming, canAddOutgoing, acceptsDynamicInputs, resolveInputPorts, resolveOutputPorts } from '@/lib/nodeRegistry';
import {
  themApi,
  getPreferences,
  setPreferences,
  type AgentDefinitionDoc,
  type AgentSkillDoc,
  type AgentStepDoc,
} from '@/lib/api';
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  Controls,
  SelectionMode,
  addEdge,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type Connection,
  type NodeTypes,
  type EdgeTypes,
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  type EdgeProps,
  Handle,
  Position,
  useReactFlow,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { C, inputStyle, INITIAL_DEBUG, INITIAL_VALIDATION, genUUID } from './constants';
import { PROVIDER_LIST, RUNTIME_MODELS } from '../../applications/constants';
import type { AgentRootData, SkillData, StepData, StepNodeData, DebugNodeState, DebugState, ValidationState, LogoState, LayoutDir } from './types';
import { StepNode, stepMeta } from './components/StepNode';
import { DebugPanel } from './components/DebugPanel';
import { NodeContextMenu } from './components/NodeContextMenu';
import type { CtxTarget } from './components/NodeContextMenu';
import { RightPanel } from './components/RightPanel';
import { LayoutDirContext, useLayoutDir } from './LayoutContext';
import { edgeRelevantVars } from './nodeVars';

// ── Node components (must be outside the render component) ───────────────────

function AgentRootNode({ data }: { data: AgentRootData; id: string }) {
  const layoutDir = useLayoutDir();
  return (
    <div style={{ background: 'transparent', border: 'none', padding: '8px', minWidth: '120px', textAlign: 'center' }}>
      <Handle type="source" position={layoutDir === 'LR' ? Position.Right : Position.Bottom} style={{ background: C.cyan }} />
      <div style={{ fontSize: '42px', textAlign: 'center', lineHeight: 1 }}>🤖</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '13px', textAlign: 'center', marginTop: '6px' }}>{data.display_name || 'Unnamed Agent'}</div>
    </div>
  );
}

function SkillNode({ data }: { data: SkillData; id: string }) {
  const layoutDir = useLayoutDir();
  return (
    <div style={{ background: 'transparent', border: 'none', padding: '8px', minWidth: '100px', textAlign: 'center' }}>
      <Handle type="target" position={layoutDir === 'LR' ? Position.Left  : Position.Top}    style={{ background: C.purple }} />
      <Handle type="source" position={layoutDir === 'LR' ? Position.Right : Position.Bottom} style={{ background: C.purple }} />
      <div style={{ fontSize: '36px', lineHeight: 1 }}>⚡</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '12px', marginTop: '6px' }}>{data.name || 'Skill'}</div>
    </div>
  );
}

// ── Animated debug edge ────────────────────────────────────────────────────────
const debugEdgeStyle = `
  @keyframes flowDash {
    from { stroke-dashoffset: 24; }
    to   { stroke-dashoffset: 0; }
  }
  @keyframes flowPulse {
    0%, 100% { opacity: 1; }
    50%       { opacity: 0.5; }
  }
`;

interface DebugEdgeData {
  debugState?: 'idle' | 'flowing' | 'done';
  label?: string;
}

function DebugEdge({
  id, sourceX, sourceY, targetX, targetY,
  sourcePosition, targetPosition, data, markerEnd,
}: EdgeProps) {
  const d = (data ?? {}) as DebugEdgeData;
  const [edgePath, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });
  const [hovered, setHovered] = useState(false);

  const isFlowing = d.debugState === 'flowing';
  const isDone    = d.debugState === 'done';

  // Full value to show in tooltip — strip surrounding quotes added by the label formatter.
  const fullValue = d.label ? d.label.replace(/^"|"$/g, '') : '';

  return (
    <>
      <style>{debugEdgeStyle}</style>
      {/* Base track */}
      <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} style={{ stroke: isDone ? '#00f0ff' : isFlowing ? '#7c3aed' : '#334155', strokeWidth: isDone ? 2 : isFlowing ? 2.5 : 1.5 }} />
      {/* Animated dash overlay when flowing */}
      {isFlowing && (
        <path
          d={edgePath}
          fill="none"
          stroke="#a78bfa"
          strokeWidth={3}
          strokeDasharray="8 4"
          style={{ animation: 'flowDash 0.4s linear infinite', opacity: 0.9 }}
        />
      )}
      {/* Glowing dot travelling the path when flowing */}
      {isFlowing && (
        <circle r={5} fill="#a78bfa" style={{ animation: 'flowPulse 0.6s ease-in-out infinite' }}>
          <animateMotion dur="0.8s" repeatCount="indefinite">
            <mpath href={`#edge-path-${id}`} />
          </animateMotion>
        </circle>
      )}
      {/* Value label when done */}
      {isDone && d.label && (
        <EdgeLabelRenderer>
          {/* Chip */}
          <div
            onMouseEnter={() => setHovered(true)}
            onMouseLeave={() => setHovered(false)}
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              pointerEvents: 'all',
              background: 'rgba(0,15,30,0.85)',
              border: '1px solid #00f0ff',
              borderRadius: '4px',
              padding: '2px 6px',
              fontSize: '10px',
              fontFamily: 'monospace',
              color: '#00f0ff',
              whiteSpace: 'nowrap',
              maxWidth: '140px',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              cursor: 'default',
              zIndex: 10,
            }}
          >
            {d.label}
          </div>

          {/* Hover tooltip — full value */}
          {hovered && fullValue && (
            <div
              onMouseEnter={() => setHovered(true)}
              onMouseLeave={() => setHovered(false)}
              style={{
                position: 'absolute',
                transform: `translate(-50%, 0) translate(${labelX}px, ${labelY + 16}px)`,
                pointerEvents: 'all',
                zIndex: 9999,
                background: 'rgba(0, 8, 20, 0.97)',
                border: '1px solid #00f0ff',
                borderRadius: '8px',
                padding: '10px 14px',
                maxWidth: '480px',
                minWidth: '200px',
                boxShadow: '0 0 24px rgba(0,240,255,0.2)',
              }}
            >
              <div style={{
                fontSize: '10px',
                fontWeight: 700,
                color: 'rgba(0,240,255,0.5)',
                letterSpacing: '0.08em',
                textTransform: 'uppercase',
                marginBottom: '6px',
                fontFamily: 'sans-serif',
              }}>
                Edge value
              </div>
              <div style={{
                fontFamily: 'monospace',
                fontSize: '12px',
                color: '#00f0ff',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                maxHeight: '320px',
                overflowY: 'auto',
                lineHeight: 1.6,
              }}>
                {fullValue}
              </div>
            </div>
          )}
        </EdgeLabelRenderer>
      )}
    </>
  );
}

// DataEdge — rendered for data bindings (kind:'data'). Dashed indigo wire.
function DataEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition }: EdgeProps) {
  const [edgePath] = getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });
  return (
    <BaseEdge
      id={id}
      path={edgePath}
      style={{ stroke: '#818cf8', strokeWidth: 1.5, strokeDasharray: '5 3', opacity: 0.8 }}
    />
  );
}

// nodeTypes MUST be defined outside the component for stable references.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const nodeTypes: NodeTypes = {
  agentRoot: AgentRootNode as any,
  skill:     SkillNode     as any,
  step:      StepNode      as any,
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const edgeTypes: EdgeTypes = { debugEdge: DebugEdge as any, dataEdge: DataEdge as any };

// Returns true for data-binding edges (kind:'data'). False for control edges.
function isDataEdge(e: Edge): boolean { return (e.data as Record<string, unknown> | undefined)?.kind === 'data'; }

// ── Canvas Logo (copied from applications/page.tsx) ───────────────────────────
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

// ── Canvas inner (uses ReactFlow hooks — must be inside ReactFlowProvider) ───

function CanvasInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const defId = searchParams.get('id');
  const { screenToFlowPosition, fitView } = useReactFlow();

  function applyDagreLayout(nodes: Node[], edges: Edge[], dir: LayoutDir = 'TB'): Node[] {
    if (!dagre) return nodes;
    const g = new dagre.graphlib.Graph();
    g.setDefaultEdgeLabel(() => ({}));
    g.setGraph({ rankdir: dir, nodesep: 60, ranksep: 100, marginx: 60, marginy: 60 });
    nodes.forEach(n => {
      // Estimate node height from port count so Dagre routes edges without collisions.
      // For step nodes: read port counts from the registry + committed inputs.
      let h = 80;
      if (n.type === 'step') {
        const stepd = n.data as unknown as StepData;
        const nodeDef = getNodeDef(stepd.step_type);
        const committedInputs = stepd.inputs ? Object.keys(stepd.inputs) : [];
        const inputPorts = resolveInputPorts(nodeDef, committedInputs);
        const outputPorts = resolveOutputPorts(nodeDef, (stepd.config ?? {}) as Record<string, unknown>);
        const dataPortCount = Math.max(
          inputPorts.filter(p => p.kind === 'data').length,
          outputPorts.filter(p => p.kind === 'data').length,
        );
        if (dataPortCount > 0) {
          h = Math.max(80, 20 + dataPortCount * 18 + 20);
        }
      }
      g.setNode(n.id, { width: 120, height: h });
    });
    edges.forEach(e => g.setEdge(e.source, e.target));
    dagre.layout(g);
    const sourcePos = dir === 'LR' ? Position.Right : Position.Bottom;
    const targetPos = dir === 'LR' ? Position.Left  : Position.Top;
    return nodes.map(n => {
      const pos = g.node(n.id);
      return { ...n, position: { x: pos.x - 60, y: pos.y - 40 }, sourcePosition: sourcePos, targetPosition: targetPos };
    });
  }

  // View state: 'agent' = top-level, 'skill' = pipeline for a skill
  const [activeView, setActiveView] = useState<'agent' | 'skill'>('agent');
  const [layoutDir, setLayoutDir] = useState<LayoutDir>('LR');
  const [activeSkillId, setActiveSkillId] = useState<string | null>(null);

  // Resizable panels
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

  // Agent-level nodes/edges — pre-seed AGENT ROOT for new drafts
  const initialAgentNodes: Node[] = defId ? [] : [{
    id: 'agent-root',
    type: 'agentRoot',
    position: { x: 300, y: 80 },
    data: { display_name: 'My Agent', description: '', version: '1.0.0' },
  }];
  const [agentNodes, setAgentNodes, onAgentNodesChange] = useNodesState<Node>(initialAgentNodes);
  const [agentEdges, setAgentEdges, onAgentEdgesChange] = useEdgesState<Edge>([]);

  // Per-skill pipeline state
  const [skillPipelines, setSkillPipelines] = useState<Record<string, { nodes: Node[]; edges: Edge[] }>>({});

  // Agent metadata
  const [agentSlug, setAgentSlug] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [description, setDescription] = useState('');
  const [version, setVersion] = useState('1.0.0');

  // UI state
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [validating, setValidating] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [publishError, setPublishError] = useState('');
  const [publishedRevision, setPublishedRevision] = useState<number | null>(null);
  const [dirty, setDirty] = useState(false);
  const [logoResult, setLogoResult] = useState<'none' | 'valid' | 'invalid' | 'warn'>('none');

  // Undo/redo — stack of serialised AgentDefinitionDoc snapshots
  const undoStack = useRef<string[]>([]);
  const redoStack = useRef<string[]>([]);
  const [canUndo, setCanUndo] = useState(false);
  const [canRedo, setCanRedo] = useState(false);

  // Debug mode
  const [debug, setDebug] = useState<DebugState>(INITIAL_DEBUG);

  // Tracks whether the node-type registry has been fetched.
  const [nodeTypesReady, setNodeTypesReady] = useState(false);

  // Stable refs — memoized to satisfy React Flow's identity check on first render.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stableNodeTypes = React.useMemo(() => nodeTypes, []);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stableEdgeTypes = React.useMemo(() => edgeTypes, []);

  // Validation state
  const [validation, setValidation] = useState<ValidationState>(INITIAL_VALIDATION);
  const validationTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Properties panel
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);

  // Pipeline nodes/edges for the active skill view
  const pipelineNodes = activeSkillId ? (skillPipelines[activeSkillId]?.nodes ?? []) : [];
  const pipelineEdges = activeSkillId ? (skillPipelines[activeSkillId]?.edges ?? []) : [];
  const [localPipeNodes, setLocalPipeNodes, onPipeNodesChange] = useNodesState<Node>(pipelineNodes);
  const [localPipeEdges, setLocalPipeEdges, onPipeEdgesChange] = useEdgesState<Edge>(pipelineEdges);

  useEffect(() => {
    fetchNodeTypes()
      .then(defs => {
        setCachedNodeTypes(defs);
        setAgentNodes(ns => ns.map(n => ({ ...n })));
        setLocalPipeNodes(ns => ns.map(n => ({ ...n })));
        setNodeTypesReady(true);
      })
      .catch(() => { setNodeTypesReady(true); });
  }, []);

  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!defId) return;

    if (validationTimerRef.current) clearTimeout(validationTimerRef.current);

    validationTimerRef.current = setTimeout(() => {
      if (abortRef.current) abortRef.current.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;

      const rootNodeData = agentNodes.find(n => n.id === 'agent-root')?.data as unknown as AgentRootData | undefined;
      const skills: AgentSkillDoc[] = agentNodes
        .filter(n => n.type === 'skill')
        .map(n => {
          const sd = n.data as unknown as SkillData;
          const pipeline = (sd.skill_id === activeSkillId
            ? { nodes: localPipeNodes, edges: localPipeEdges }
            : skillPipelines[sd.skill_id]) ?? { nodes: [], edges: [] };
          const steps: AgentStepDoc[] = pipeline.nodes.map(sn => {
            const stepd = sn.data as unknown as StepData;
            const ctrlOut = pipeline.edges.filter(e => e.source === sn.id && !isDataEdge(e));
            const defaultLabel = stepMeta(stepd.step_type).label;
            // Generic: nodes with named control output ports (e.g. branch) preserve
            // port order from the registry definition. Anonymous control outputs are ordered by edge insertion.
            const ctrlPortDefs = getNodeDef(stepd.step_type).control_output_ports ?? [];
            const next = ctrlPortDefs.length > 0
              ? ctrlPortDefs
                  .map(p => ctrlOut.find(e => e.sourceHandle === `ctrl-out-${p.id}`)?.target?.replace('step-', '') ?? '')
                  .filter(Boolean)
              : ctrlOut.map(e => (e.target as string).replace('step-', ''));
            const dataIn = pipeline.edges.filter(e => e.target === sn.id && isDataEdge(e));
            const inputs: Record<string, { from_step: string; from_port: string }> = {};
            for (const de of dataIn) {
              const portID = de.targetHandle?.replace('data-in-', '');
              const fromPort = de.sourceHandle?.replace('data-out-', '');
              const fromStepID = (de.source as string).replace('step-', '');
              if (portID && fromPort && fromStepID) inputs[portID] = { from_step: fromStepID, from_port: fromPort };
            }
            return {
              id: stepd.step_id,
              type: stepd.step_type as AgentStepDoc['type'],
              label: (stepd.label && stepd.label !== defaultLabel) ? stepd.label : undefined,
              config: stepd.config ?? {},
              next,
              ...(Object.keys(inputs).length > 0 ? { inputs } : {}),
              position: sn.position,
            };
          });
          return {
            skill_id: sd.skill_id, name: sd.name, description: sd.description ?? '',
            tags: sd.tags ?? [], input_modes: sd.input_modes ?? ['text/plain'],
            output_modes: sd.output_modes ?? ['text/plain'], examples: sd.examples ?? [],
            input_schema: {}, output_schema: {}, steps, position: n.position,
          };
        });

      const liveDefinition: AgentDefinitionDoc = {
        schema_version: 1,
        agent_slug: agentSlug,
        agent_root: {
          display_name: rootNodeData?.display_name ?? '',
          description: rootNodeData?.description ?? '',
          version: rootNodeData?.version ?? '1.0.0',
          capabilities: { streaming: false, push_notifications: false },
        },
        skills,
      };

      setValidation(prev => ({ ...prev, loading: true }));
      themApi.validateAgentDefinition(defId, liveDefinition, ctrl.signal)
        .then(result => {
          setValidation({ issues: result.issues ?? [], stepContracts: result.step_contracts ?? {}, loading: false, lastValidatedAt: Date.now() });
        })
        .catch(e => {
          if ((e as { name?: string }).name === 'AbortError') return;
          setValidation(prev => ({ ...prev, loading: false }));
        });
    }, 1200);

    return () => {
      if (validationTimerRef.current) clearTimeout(validationTimerRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [defId, agentNodes, agentEdges, agentSlug, skillPipelines, localPipeNodes, localPipeEdges]);

  useEffect(() => {
    if (activeSkillId) {
      const state = skillPipelines[activeSkillId] ?? { nodes: [], edges: [] };
      const arranged = applyDagreLayout(state.nodes, state.edges, 'LR');
      setLocalPipeNodes(arranged);
      setLocalPipeEdges(state.edges);
      setTimeout(() => fitView({ padding: 0.2 }), 50);
    }
  }, [activeSkillId]); // eslint-disable-line react-hooks/exhaustive-deps

  const pushHistory = useCallback((snapshot: string) => {
    undoStack.current.push(snapshot);
    if (undoStack.current.length > 100) undoStack.current.shift();
    redoStack.current = [];
    setCanUndo(true);
    setCanRedo(false);
  }, []);

  const markDirty = useCallback(() => {
    // Snapshot current doc before the change lands in state.
    // buildDefinitionDoc() reads current state so this captures the BEFORE state.
    try { pushHistory(JSON.stringify(buildDefinitionDoc())); } catch { /* ignore */ }
    setDirty(true);
  }, [pushHistory]); // eslint-disable-line react-hooks/exhaustive-deps

  function handleUndo() {
    const snap = undoStack.current.pop();
    if (!snap) return;
    // Push current state to redo before restoring
    try { redoStack.current.push(JSON.stringify(buildDefinitionDoc())); } catch { /* ignore */ }
    setCanRedo(true);
    setCanUndo(undoStack.current.length > 0);
    try { loadDefinitionDoc(JSON.parse(snap) as AgentDefinitionDoc); setDirty(true); } catch { /* ignore */ }
  }

  function handleRedo() {
    const snap = redoStack.current.pop();
    if (!snap) return;
    try { undoStack.current.push(JSON.stringify(buildDefinitionDoc())); } catch { /* ignore */ }
    setCanUndo(true);
    setCanRedo(redoStack.current.length > 0);
    try { loadDefinitionDoc(JSON.parse(snap) as AgentDefinitionDoc); setDirty(true); } catch { /* ignore */ }
  }

  function handleExport() {
    savePipelineState();
    const doc = buildDefinitionDoc();
    const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${agentSlug || 'agent'}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }

  const savePipelineState = useCallback(() => {
    if (activeSkillId) {
      setSkillPipelines(prev => ({
        ...prev,
        [activeSkillId]: { nodes: localPipeNodes, edges: localPipeEdges },
      }));
    }
  }, [activeSkillId, localPipeNodes, localPipeEdges]);

  useEffect(() => {
    if (!defId) return;
    themApi.getAgentDefinition(defId).then(resp => {
      const doc = resp.definition;
      if (!doc) return;
      setAgentSlug(doc.agent_slug ?? '');
      setDisplayName(doc.agent_root.display_name ?? '');
      setDescription(doc.agent_root.description ?? '');
      setVersion(doc.agent_root.version ?? '1.0.0');
      loadDefinitionDoc(doc);
    }).catch(e => {
      setLoadError('Failed to load definition: ' + String(e));
    });
  }, [defId]); // eslint-disable-line react-hooks/exhaustive-deps

  function loadDefinitionDoc(doc: AgentDefinitionDoc) {
    const rootNode: Node = {
      id: 'agent-root',
      type: 'agentRoot',
      position: { x: 300, y: 80 },
      data: {
        display_name: doc.agent_root.display_name,
        description: doc.agent_root.description ?? '',
        version: doc.agent_root.version ?? '1.0.0',
      },
    };
    const skillNodes: Node[] = doc.skills.map((sk, i) => ({
      id: `skill-${sk.skill_id}`,
      type: 'skill',
      position: sk.position ?? { x: 150 + i * 220, y: 250 },
      data: {
        skill_id: sk.skill_id,
        name: sk.name,
        description: sk.description ?? '',
        tags: sk.tags ?? [],
        input_modes: sk.input_modes ?? ['text/plain'],
        output_modes: sk.output_modes ?? ['text/plain'],
        examples: sk.examples ?? [],
      },
    }));
    const skillEdges: Edge[] = doc.skills.map(sk => ({
      id: `root-to-${sk.skill_id}`,
      source: 'agent-root',
      target: `skill-${sk.skill_id}`,
    }));
    setAgentNodes([rootNode, ...skillNodes]);
    setAgentEdges(skillEdges);

    const pipelines: Record<string, { nodes: Node[]; edges: Edge[] }> = {};
    for (const sk of doc.skills) {
      const stepNodes: Node[] = (sk.steps ?? []).map((step, si) => ({
        id: `step-${step.id}`,
        type: 'step',
        position: step.position ?? { x: 200, y: 80 + si * 120 },
        data: {
          step_id: step.id,
          step_type: step.type,
          label: (step as AgentStepDoc & { label?: string }).label || stepMeta(step.type).label,
          config: (step.config as Record<string, unknown>) ?? {},
          inputs: step.inputs
            ? Object.fromEntries(Object.entries(step.inputs).map(([portID, b]) => [portID, { from_step: b.from_step, from_port: b.from_port }]))
            : undefined,
        },
      }));
      const stepEdges: Edge[] = [];
      for (const step of (sk.steps ?? [])) {
        // Control edges from step.next.
        // Named control ports (e.g. branch true/false) are identified by registry
        // definition order — no node-name conditionals.
        const ctrlPortDefs = getNodeDef(step.type as string).control_output_ports ?? [];
        (step.next ?? []).forEach((nextId, idx) => {
          const sourceHandle = ctrlPortDefs.length > 0
            ? `ctrl-out-${ctrlPortDefs[idx]?.id ?? idx}`
            : undefined;
          stepEdges.push({
            id: `${step.id}-to-${nextId}`,
            source: `step-${step.id}`,
            target: `step-${nextId}`,
            ...(sourceHandle ? { sourceHandle } : {}),
          });
        });
        // Data binding edges from step.inputs
        if (step.inputs) {
          for (const [portID, binding] of Object.entries(step.inputs)) {
            stepEdges.push({
              id: `data-${binding.from_step}-${binding.from_port}-to-${step.id}-${portID}`,
              source: `step-${binding.from_step}`,
              target: `step-${step.id}`,
              sourceHandle: `data-out-${binding.from_port}`,
              targetHandle: `data-in-${portID}`,
              type: 'dataEdge',
              data: { kind: 'data' },
            });
          }
        }
      }
      pipelines[sk.skill_id] = { nodes: stepNodes, edges: stepEdges };
    }
    setSkillPipelines(pipelines);
  }

  function buildDefinitionDoc(): AgentDefinitionDoc {
    const rootNodeData = agentNodes.find(n => n.id === 'agent-root')?.data as unknown as AgentRootData | undefined;
    const dn = rootNodeData?.display_name ?? displayName;
    const desc = rootNodeData?.description ?? description;
    const ver = rootNodeData?.version ?? version;

    const skills: AgentSkillDoc[] = agentNodes
      .filter(n => n.type === 'skill')
      .map(n => {
        const sd = n.data as unknown as SkillData;
        const pipeline = skillPipelines[sd.skill_id] ?? { nodes: [], edges: [] };
        const steps: AgentStepDoc[] = pipeline.nodes.map(sn => {
          const stepd = sn.data as unknown as StepData;
          // Control edges only — data edges (kind:'data') carry no execution order.
          const ctrlOut = pipeline.edges.filter(e => e.source === sn.id && !isDataEdge(e));
          const defaultLabel = stepMeta(stepd.step_type).label;
          // Generic: nodes with named control output ports preserve definition order.
          const ctrlPortDefs = getNodeDef(stepd.step_type).control_output_ports ?? [];
          let next: string[];
          if (ctrlPortDefs.length > 0) {
            next = ctrlPortDefs
              .map(p => ctrlOut.find(e => e.sourceHandle === `ctrl-out-${p.id}`)?.target?.replace('step-', '') ?? '')
              .filter(Boolean);
          } else {
            next = ctrlOut.map(e => (e.target as string).replace('step-', ''));
          }
          // Derive inputs from incoming data edges — source of truth for bindings.
          const dataIn = pipeline.edges.filter(e => e.target === sn.id && isDataEdge(e));
          const inputs: Record<string, { from_step: string; from_port: string }> = {};
          for (const de of dataIn) {
            const portID = de.targetHandle?.replace('data-in-', '');
            const fromPort = de.sourceHandle?.replace('data-out-', '');
            const fromStepID = (de.source as string).replace('step-', '');
            if (portID && fromPort && fromStepID) inputs[portID] = { from_step: fromStepID, from_port: fromPort };
          }
          return {
            id: stepd.step_id,
            type: stepd.step_type as AgentStepDoc['type'],
            label: (stepd.label && stepd.label !== defaultLabel) ? stepd.label : undefined,
            config: stepd.config ?? {},
            next,
            ...(Object.keys(inputs).length > 0 ? { inputs } : {}),
            position: sn.position,
          };
        });
        return {
          skill_id: sd.skill_id,
          name: sd.name,
          description: sd.description ?? '',
          tags: sd.tags ?? [],
          input_modes: sd.input_modes ?? ['text/plain'],
          output_modes: sd.output_modes ?? ['text/plain'],
          examples: sd.examples ?? [],
          input_schema: {},
          output_schema: {},
          steps,
          position: n.position,
        };
      });

    return {
      schema_version: 1,
      agent_slug: agentSlug,
      agent_root: {
        display_name: dn,
        description: desc,
        version: ver,
        capabilities: { streaming: false, push_notifications: false },
      },
      skills,
    };
  }

  const importFileRef = useRef<HTMLInputElement>(null);

  function handleImportJSON() {
    importFileRef.current?.click();
  }

  function handleImportFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (ev) => {
      try {
        const doc = JSON.parse(ev.target?.result as string) as AgentDefinitionDoc;
        if (!doc.agent_root || !Array.isArray(doc.skills)) throw new Error('Missing agent_root or skills array');
        setAgentSlug(doc.agent_slug ?? '');
        loadDefinitionDoc(doc);
        markDirty();
        setSaveError('');
      } catch (err) {
        setSaveError(`Import failed: ${String(err)}`);
      }
      e.target.value = '';
    };
    reader.readAsText(file);
  }

  async function handleSave() {
    if (!agentSlug.trim()) {
      setSaveError('Agent slug is required — fill in the slug field in the toolbar before saving.');
      setLogoResult('invalid');
      setTimeout(() => setLogoResult('none'), 1800);
      return;
    }
    setSaving(true);
    setSaveError('');
    setLogoResult('none');
    savePipelineState();
    try {
      const doc = buildDefinitionDoc();
      if (defId) {
        await themApi.updateAgentDefinition(defId, { definition: doc });
        setDirty(false);
      } else {
        const result = await themApi.createAgentDefinition({ agent_slug: agentSlug, definition: doc });
        router.replace(`/admin/agents/builder?id=${result.id}`);
        setDirty(false);
      }
      setLogoResult('valid');
      setTimeout(() => setLogoResult('none'), 1800);
    } catch (e) {
      setSaveError(String(e));
      setLogoResult('invalid');
      setTimeout(() => setLogoResult('none'), 1800);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!defId || !confirm('Delete this draft agent definition?')) return;
    setDeleting(true);
    try {
      await themApi.deleteAgentDefinition(defId);
      router.push('/admin/agents');
    } catch (e) {
      setSaveError(String(e));
    } finally {
      setDeleting(false);
    }
  }

  async function handleValidate() {
    if (!defId) return;
    setValidating(true);
    setPublishError('');
    setLogoResult('none');
    setValidation(prev => ({ ...prev, loading: true }));
    savePipelineState();
    try {
      const result = await themApi.validateAgentDefinition(defId, buildDefinitionDoc());
      setValidation({ issues: result.issues ?? [], stepContracts: result.step_contracts ?? {}, loading: false, lastValidatedAt: Date.now() });
      const errors   = (result.issues ?? []).filter(i => i.severity === 'error').length;
      const warnings = (result.issues ?? []).filter(i => i.severity === 'warning').length;
      const r = errors > 0 ? 'invalid' : warnings > 0 ? 'warn' : 'valid';
      setLogoResult(r);
      setTimeout(() => setLogoResult('none'), 1800);
    } catch {
      setValidation(prev => ({ ...prev, loading: false }));
    } finally {
      setValidating(false);
    }
  }

  async function handlePublish() {
    if (!defId || !confirm('Publish this agent definition? This creates a runtime agent entry.')) return;
    setPublishing(true);
    setPublishError('');
    setLogoResult('none');
    try {
      const result = await themApi.publishAgentDefinition(defId);
      setPublishedRevision(result.revision);
      setDirty(false);
      setLogoResult('valid');
      setTimeout(() => setLogoResult('none'), 1800);
    } catch (e: unknown) {
      const refreshed = await themApi.validateAgentDefinition(defId);
      if (refreshed.issues && refreshed.issues.length > 0) {
        setValidation({ issues: refreshed.issues, stepContracts: refreshed.step_contracts ?? {}, loading: false, lastValidatedAt: Date.now() });
        setPublishError('Publish failed — fix errors before publishing.');
        setLogoResult('invalid');
      } else {
        setPublishError(String(e));
        setLogoResult('invalid');
      }
      setTimeout(() => setLogoResult('none'), 1800);
    } finally {
      setPublishing(false);
    }
  }

  function handleBack() {
    savePipelineState();
    if (activeView === 'skill') {
      setActiveView('agent');
      setActiveSkillId(null);
      setSelectedNode(null);
    } else {
      router.push('/admin/agents');
    }
  }

  function makeDefaultPipeline(): { nodes: Node[]; edges: Edge[] } {
    const inputId = genUUID();
    const responseId = genUUID();
    return {
      nodes: [
        { id: `step-${inputId}`,    type: 'step', position: { x: 160, y: 60  }, data: { step_id: inputId,    step_type: 'input',    label: 'Input',    config: {} } },
        { id: `step-${responseId}`, type: 'step', position: { x: 160, y: 280 }, data: { step_id: responseId, step_type: 'response', label: 'Response', config: {} } },
      ],
      edges: [],
    };
  }

  function addSkill() {
    const sid = genUUID();
    const newNode: Node = {
      id: `skill-${sid}`,
      type: 'skill',
      position: { x: 150 + agentNodes.filter(n => n.type === 'skill').length * 220, y: 250 },
      data: { skill_id: sid, name: 'New Skill', description: '', tags: [], input_modes: ['text/plain'], output_modes: ['text/plain'], examples: [] },
    };
    setAgentNodes(prev => [...prev, newNode]);
    if (agentNodes.find(n => n.id === 'agent-root')) {
      setAgentEdges(prev => [...prev, { id: `root-to-${sid}`, source: 'agent-root', target: `skill-${sid}` }]);
    }
    setSkillPipelines(prev => ({ ...prev, [sid]: makeDefaultPipeline() }));
    markDirty();
  }

  function addStepToActivePipeline(type: AgentStepDoc['type']) {
    if (!activeSkillId) return;
    const stepId = genUUID();
    const newNode: Node = {
      id: `step-${stepId}`,
      type: 'step',
      position: screenToFlowPosition({ x: 300, y: 200 }),
      data: { step_id: stepId, step_type: type, label: type, config: {} },
    };
    setLocalPipeNodes(prev => [...prev, newNode]);
    markDirty();
  }

  function onAgentNodeDoubleClick(_: MouseEvent, node: Node) {
    if (node.type === 'skill') {
      savePipelineState();
      const sd = node.data as unknown as SkillData;
      setActiveSkillId(sd.skill_id);
      setActiveView('skill');
      setSelectedNode(null);
    } else {
      setSelectedNode(node);
    }
  }

  function onPipeNodeDoubleClick(_: MouseEvent, node: Node) {
    setSelectedNode(node);
  }

  const onAgentConnect = useCallback((conn: Connection) => {
    setAgentEdges(prev => addEdge(conn, prev));
    markDirty();
  }, [setAgentEdges]);

  // ── Context menu ──────────────────────────────────────────────────────────────
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; target: CtxTarget } | null>(null);

  const closeCtx = useCallback(() => setCtxMenu(null), []);

  const onNodeCtx = useCallback((e: MouseEvent, node: Node) => {
    e.preventDefault();
    setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: 'node', node } });
    setSelectedNode(node);
  }, []);

  const onEdgeCtx = useCallback((e: MouseEvent, edge: Edge) => {
    e.preventDefault();
    setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: 'edge', edge } });
  }, []);

  const ctxDelete = useCallback(() => {
    if (!ctxMenu) return;
    if (ctxMenu.target.kind === 'node') {
      const id = ctxMenu.target.node.id;
      if (activeView === 'agent') {
        setAgentNodes(prev => prev.filter(n => n.id !== id));
        setAgentEdges(prev => prev.filter(e => e.source !== id && e.target !== id));
      } else {
        setLocalPipeNodes(prev => prev.filter(n => n.id !== id));
        setLocalPipeEdges(prev => prev.filter(e => e.source !== id && e.target !== id));
      }
    } else {
      const id = ctxMenu.target.edge.id;
      if (activeView === 'agent') {
        setAgentEdges(prev => prev.filter(e => e.id !== id));
      } else {
        setLocalPipeEdges(prev => prev.filter(e => e.id !== id));
      }
    }
    markDirty();
    closeCtx();
  }, [ctxMenu, activeView, setAgentNodes, setAgentEdges, setLocalPipeNodes, setLocalPipeEdges, closeCtx]);

  const ctxEditPipeline = useCallback(() => {
    if (!ctxMenu || ctxMenu.target.kind !== 'node') return;
    const node = ctxMenu.target.node;
    if (node.type === 'skill') {
      savePipelineState();
      const sd = node.data as unknown as SkillData;
      setActiveSkillId(sd.skill_id);
      setActiveView('skill');
      setSelectedNode(null);
    }
    closeCtx();
  }, [ctxMenu, savePipelineState, closeCtx]);

  const onPipeConnect = useCallback((conn: Connection) => {
    // Data binding connection — sourceHandle starts with 'data-out-'
    const isDataSrc = conn.sourceHandle?.startsWith('data-out-');
    const isData = isDataSrc && conn.targetHandle?.startsWith('data-in-');

    if (isData) {
      // The ghost port handle (data-in-{ghostVar}) is already rendered by StepNode during drag.
      // Commit the binding into data.inputs so it persists after drag state is cleared.
      const portID = conn.targetHandle!.replace('data-in-', '');
      const fromPort = conn.sourceHandle!.replace('data-out-', '');
      const srcStepID = (conn.source as string).replace('step-', '');
      setLocalPipeNodes(prev => prev.map(n => {
        if (n.id !== conn.target) return n;
        const existing = (n.data as unknown as StepData).inputs ?? {};
        return { ...n, data: { ...n.data, inputs: { ...existing, [portID]: { from_step: srcStepID, from_port: fromPort } } } };
      }));
      setLocalPipeEdges(prev => {
        const kept = prev.filter(e => !(isDataEdge(e) && e.target === conn.target && e.targetHandle === conn.targetHandle));
        return addEdge({ ...conn, type: 'dataEdge', data: { kind: 'data' } }, kept);
      });
      markDirty();
      return;
    }

    // Control edge — existing behavior. Single-incoming guard applies per targetHandle for branch.
    setLocalPipeEdges(prev => addEdge(conn, prev.filter(e => e.target !== conn.target || isDataEdge(e))));
    markDirty();

    // Auto-fill input_field on target node (heuristic, control edges only)
    setLocalPipeNodes(prev => {
      const sourceNode = prev.find(n => n.id === conn.source);
      const targetNode = prev.find(n => n.id === conn.target);
      if (!sourceNode || !targetNode) return prev;

      const srcData = sourceNode.data as unknown as StepData;
      const tgtData = targetNode.data as unknown as StepData;

      const sourceVar: string =
        srcData.step_type === 'input'
          ? ((srcData.config?.bindings as Record<string, string>)?.text || 'input')
          : ((srcData.config?.output_var as string) || 'output');

      const targetField = getNodeDef(tgtData.step_type).input_field;
      if (!targetField) return prev;

      const currentValue = tgtData.config?.[targetField] as string | undefined;
      if (targetField !== 'from_var' && currentValue && currentValue.trim() !== '') return prev;

      const fillValue = targetField === 'from_var' ? sourceVar : `{{${sourceVar}}}`;

      return prev.map(n =>
        n.id === conn.target
          ? { ...n, data: { ...n.data, config: { ...(n.data.config as Record<string, unknown>), [targetField]: fillValue } } }
          : n
      );
    });
  }, [setLocalPipeEdges, setLocalPipeNodes, markDirty]);

  // Delete a dynamic input port from a node: remove from data.inputs + remove the data edge.
  const onDeleteInput = useCallback((nodeId: string, portID: string) => {
    setLocalPipeNodes(prev => prev.map(n => {
      if (n.id !== nodeId) return n;
      const existing = { ...((n.data as unknown as import('./types').StepData).inputs ?? {}) };
      delete existing[portID];
      return { ...n, data: { ...n.data, inputs: existing } };
    }));
    setLocalPipeEdges(prev => prev.filter(e =>
      !(isDataEdge(e) && e.target === nodeId && e.targetHandle === `data-in-${portID}`)
    ));
    markDirty();
  }, [setLocalPipeNodes, setLocalPipeEdges, markDirty]);

  // Tracks whether onConnect fired during the current drag (to detect drop-on-nothing).
  const connectFiredRef = useRef(false);

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onPipeConnectStart = useCallback((_: any, params: { nodeId: string | null; handleId: string | null; handleType: string | null }) => {
    if (!params.handleId?.startsWith('data-out-')) return;
    const varName = params.handleId.replace('data-out-', '');
    connectFiredRef.current = false;
    // Inject _draggingVar + _dragAccept into every other step node so StepNode can show highlight + ghost port.
    setLocalPipeNodes(prev => prev.map(n => {
      if (n.type !== 'step' || n.id === params.nodeId) return n;
      const stepType = (n.data as unknown as StepData).step_type;
      const existingInputs = (n.data as unknown as StepData).inputs ?? {};
      const accept = acceptsDynamicInputs(stepType);
      // Deduplicate port name: if varName already exists, try varName_2, _3, etc.
      let ghostVar = varName;
      if (accept && ghostVar in existingInputs) {
        let i = 2;
        while (`${varName}_${i}` in existingInputs) i++;
        ghostVar = `${varName}_${i}`;
      }
      return { ...n, data: { ...n.data, _draggingVar: accept ? ghostVar : varName, _dragAccept: accept ? 'accept' : 'reject' } };
    }));
  }, [setLocalPipeNodes]);

  const onPipeConnectEnd = useCallback(() => {
    // Clear all drag highlight state regardless of whether onConnect fired.
    setLocalPipeNodes(prev => prev.map(n => {
      if (n.type !== 'step') return n;
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { _draggingVar, _dragAccept, ...rest } = n.data as unknown as StepNodeData;
      return { ...n, data: rest };
    }));
  }, [setLocalPipeNodes]);

  const isPipeConnectionValid = useCallback((conn: Connection | Edge) => {
    if (conn.source === conn.target) return false;

    // Data edges skip degree and cycle checks — they carry no execution order.
    const connIsData = (conn as Edge).data?.kind === 'data'
      || ((conn as Connection).sourceHandle?.startsWith('data-out-') && (conn as Connection).targetHandle?.startsWith('data-in-'));
    if (connIsData) return true;

    const srcNode = localPipeNodes.find(n => n.id === conn.source);
    const tgtNode = localPipeNodes.find(n => n.id === conn.target);

    // Degree checks apply to control edges only.
    const ctrlEdges = localPipeEdges.filter(e => !isDataEdge(e));

    if (srcNode) {
      const srcType = (srcNode.data as unknown as StepData).step_type;
      // For nodes with named control output ports, degree is per-handle (each port has its own cap).
      // For anonymous control output nodes, degree is across all outgoing control edges.
      const hasNamedCtrlPorts = (getNodeDef(srcType).control_output_ports ?? []).length > 0;
      const currentOut = hasNamedCtrlPorts
        ? ctrlEdges.filter(e => e.source === conn.source && e.sourceHandle === conn.sourceHandle).length
        : ctrlEdges.filter(e => e.source === conn.source).length;
      if (!canAddOutgoing(srcType, currentOut)) return false;
    }
    if (tgtNode) {
      const tgtType = (tgtNode.data as unknown as StepData).step_type;
      const currentIn = ctrlEdges.filter(e => e.target === conn.target).length;
      if (!canAddIncoming(tgtType, currentIn)) return false;
    }

    // Cycle check over control edges only.
    const hypothetical = [...ctrlEdges, { id: '__test__', source: conn.source!, target: conn.target! }];
    if (topoSort(localPipeNodes, hypothetical) === null) return false;

    return true;
  }, [localPipeNodes, localPipeEdges]);

  // ── Debug helpers ─────────────────────────────────────────────────────────────

  function ssKey(paramKey: string) { return `debug_param:${defId ?? 'new'}:${paramKey}`; }
  function ssGet(paramKey: string) { try { return sessionStorage.getItem(ssKey(paramKey)) ?? ''; } catch { return ''; } }
  function ssSet(paramKey: string, val: string) { try { sessionStorage.setItem(ssKey(paramKey), val); } catch { /* ignore */ } }

  function buildDebugParamSpecs(nodes: Node[]) {
    const specs: import('./types').DebugParamSpec[] = [];
    const seenParamKeys = new Set<string>();

    specs.push({
      key: '__test_input',
      label: 'Test message',
      description: 'The user message to send into the pipeline',
      isSecret: false,
      required: true,
    });

    const hasLLM = nodes.some(n => (n.data as unknown as StepData).step_type === 'llm');
    if (hasLLM) {
      specs.push({
        key: '__debug_provider',
        label: 'LLM Provider',
        description: 'Provider to use for all LLM nodes in this debug run',
        isSecret: false,
        required: true,
        options: [...PROVIDER_LIST],
      });
      specs.push({
        key: '__debug_model',
        label: 'Model',
        description: 'Model to use (must be valid for the chosen provider)',
        isSecret: false,
        required: true,
        options: Object.values(RUNTIME_MODELS).flat(),
      });
      specs.push({
        key: '__debug_api_key',
        label: 'API Key',
        description: 'API key for the chosen provider — stored in browser session only, never sent to the-M server',
        isSecret: true,
        required: true,
      });
    }

    for (const node of nodes) {
      const d = node.data as unknown as StepData;
      if (d.step_type !== 'http') continue;
      const paramKey = d.config?.app_param_key as string | undefined;
      if (!paramKey || seenParamKeys.has(paramKey)) continue;
      seenParamKeys.add(paramKey);

      const httpDef = getNodeDef('http');
      const decl = httpDef.app_params?.find(p => p.key === paramKey);
      specs.push({
        key: paramKey,
        label: decl?.label ?? paramKey,
        description: decl?.description ?? `Required by HTTP node "${d.label || d.step_id}"`,
        isSecret: decl?.type === 'secret',
        required: true,
        nodeLabel: d.label || d.step_id,
      });
    }

    return specs;
  }

  async function loadDebugPrefs(): Promise<{ testInput: string }> {
    try {
      const prefs = await getPreferences();
      const saved = (prefs as Record<string, unknown>).debugValues as Record<string, { testInput: string }> | undefined;
      return { testInput: saved?.[defId ?? 'new']?.testInput ?? '' };
    } catch { return { testInput: '' }; }
  }

  async function saveDebugPrefs(testInput: string) {
    try {
      const prefs = await getPreferences();
      const existing = (prefs as Record<string, unknown>).debugValues as Record<string, unknown> ?? {};
      await setPreferences({ ...prefs, debugValues: { ...existing, [defId ?? 'new']: { testInput } } });
    } catch { /* non-critical */ }
  }

  function renderTemplate(template: string, vars: Record<string, unknown>): string {
    // Support both {{varname}} and {{.varname}} (Go template) syntax.
    return template.replace(/\{\{\.?(\w+)\}\}/g, (_, key) => String(vars[key] ?? ''));
  }

  function topoSort(nodes: Node[], edges: Edge[]): string[] | null {
    const inDegree: Record<string, number> = {};
    const adj: Record<string, string[]> = {};
    for (const n of nodes) { inDegree[n.id] = 0; adj[n.id] = []; }
    for (const e of edges) {
      adj[e.source]?.push(e.target);
      if (inDegree[e.target] !== undefined) inDegree[e.target]++;
    }
    const queue = nodes.filter(n => inDegree[n.id] === 0).map(n => n.id);
    const order: string[] = [];
    while (queue.length) {
      const id = queue.shift()!;
      order.push(id);
      for (const next of (adj[id] ?? [])) {
        inDegree[next]--;
        if (inDegree[next] === 0) queue.push(next);
      }
    }
    return order.length === nodes.length ? order : null;
  }

  async function executeStep(
    nodeId: string,
    nodes: Node[],
    edges: Edge[],
    vars: Record<string, unknown>,
    debugParams: Record<string, string>,
  ): Promise<{ vars: Record<string, unknown>; output: string; edgeValues: Record<string, string> }> {
    const node = nodes.find(n => n.id === nodeId);
    if (!node) throw new Error(`Node ${nodeId} not found`);
    const d = node.data as unknown as StepData;
    const cfg = d.config ?? {};
    const newVars = { ...vars };
    let output = '';
    const edgeValues: Record<string, string> = {};

    const outEdgesForNode = edges.filter(e => e.source === nodeId);

    if (d.step_type === 'input') {
      const bindVar = (cfg.bindings as Record<string,string>)?.text || 'input';
      newVars[bindVar] = newVars[bindVar] ?? '';   // ensure it's in vars for downstream nodes
      output = String(newVars[bindVar]);
      for (const e of outEdgesForNode) edgeValues[e.id] = output;
    } else if (d.step_type === 'llm') {
      const model = (cfg.model as string) || 'claude-haiku-4-5-20251001';
      const maxTokens = (cfg.max_tokens as number) || 4096;
      const systemPrompt = (cfg.system_prompt as string) || '';
      const userPromptTemplate = (cfg.user_prompt as string) || '';

      // Find the variable carried by the incoming edge (what the upstream node produced).
      const inEdge = edges.find(e => e.target === nodeId);
      const inSourceNode = inEdge ? nodes.find(n => n.id === inEdge.source) : undefined;
      const inBindVar = inSourceNode
        ? ((inSourceNode.data as unknown as StepData).config?.bindings as Record<string,string>)?.text || 'input'
        : 'input';

      // Use user_prompt template if set, otherwise pass the incoming variable directly.
      const userPrompt = userPromptTemplate
        ? renderTemplate(userPromptTemplate, newVars)
        : String(newVars[inBindVar] ?? '');
      const outVar = (cfg.output_var as string) || 'output';

      const messages: { role: string; content: string }[] = [];
      if (userPrompt) messages.push({ role: 'user', content: userPrompt });
      if (messages.length === 0) throw new Error('LLM step: user prompt is empty — connect an Input node or set the user_prompt template.');

      // Use debug-session provider/model/key — overrides the node's compiled values
      const debugProvider = debugParams['__debug_provider'] ?? 'anthropic';
      const debugModel = debugParams['__debug_model'] || model;
      const resp = await fetch('/api/debug/llm', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'x-debug-provider': debugProvider,
          'x-debug-api-key': debugParams['__debug_api_key'] ?? '',
        },
        body: JSON.stringify({
          model: debugModel,
          max_tokens: maxTokens,
          ...(systemPrompt ? { system: systemPrompt } : {}),
          messages,
        }),
      });
      if (!resp.ok) {
        const errText = await resp.text();
        throw new Error(`Anthropic API error ${resp.status}: ${errText.slice(0, 200)}`);
      }
      const json = await resp.json() as { content: { type: string; text: string }[] };
      const text = json.content?.find(c => c.type === 'text')?.text ?? '';
      newVars[outVar] = text;
      output = text;
      for (const e of outEdgesForNode) edgeValues[e.id] = text;
    } else if (d.step_type === 'response') {
      const fromVar = (cfg.from_var as string) || 'output';
      output = String(newVars[fromVar] ?? '');
    } else if (d.step_type === 'transform') {
      // Server-side function pipeline (fn/input_var/output_var format)
      const functions = (cfg.functions as Array<{ fn: string; args?: Record<string, unknown>; input_var: string; output_var: string }>) ?? [];
      for (const f of functions) {
        const raw = newVars[f.input_var];
        const rawStr = raw === undefined ? '' : typeof raw === 'string' ? raw : JSON.stringify(raw);
        let result: unknown = rawStr;
        if (f.fn === 'strip_fences') {
          // Strip markdown code fences: ```json ... ``` or ``` ... ```
          result = rawStr.replace(/^```[a-z]*\n?/i, '').replace(/\n?```\s*$/i, '').trim();
        } else if (f.fn === 'json_path') {
          const path = String((f.args?.path as string) ?? '');
          let parsed: unknown = null;
          if (typeof raw === 'object' && raw !== null) parsed = raw;
          else { try { parsed = JSON.parse(rawStr); } catch { parsed = null; } }
          if (parsed !== null) {
            const parts = path.replace(/^\$\.?/, '').split('.').filter(Boolean);
            let cur: unknown = parsed;
            for (const p of parts) { if (typeof cur === 'object' && cur !== null) cur = (cur as Record<string, unknown>)[p]; else { cur = undefined; break; } }
            result = cur !== undefined ? cur : '';
          }
        } else if (f.fn === 'template') {
          result = renderTemplate(rawStr, newVars);
        }
        newVars[f.output_var] = result;
        output = typeof result === 'string' ? result : JSON.stringify(result);
      }
      // Legacy expression/extraction format
      const exprs = (cfg.expressions as Record<string, string>) ?? {};
      for (const [outKey, tmpl] of Object.entries(exprs)) {
        const val = renderTemplate(tmpl, newVars);
        newVars[outKey] = val;
        output = val;
      }
      const extractions = (cfg.extractions as Array<{ from_var: string; json_path: string; var: string }>) ?? [];
      for (const ext of extractions) {
        const raw = newVars[ext.from_var];
        if (raw === undefined) continue;
        let parsed: Record<string, unknown> | null = null;
        if (typeof raw === 'object' && raw !== null) parsed = raw as Record<string, unknown>;
        else if (typeof raw === 'string') { try { parsed = JSON.parse(raw); } catch { continue; } }
        if (!parsed) continue;
        const parts = ext.json_path.replace(/^\$\./, '').split('.');
        let cur: unknown = parsed;
        for (const p of parts) { if (typeof cur === 'object' && cur !== null) cur = (cur as Record<string, unknown>)[p]; else { cur = undefined; break; } }
        if (cur !== undefined) { newVars[ext.var] = String(cur); output = String(cur); }
      }
      for (const e of outEdgesForNode) edgeValues[e.id] = output;
    } else if (d.step_type === 'branch') {
      const expr = (cfg.expression as string) || '';
      const trueNext = (cfg.true_next as string) || '';
      const falseNext = (cfg.false_next as string) || '';
      const rendered = renderTemplate(expr, newVars);
      const truthy = rendered.trim() !== '' && rendered.trim() !== 'false' && rendered.trim() !== '0' && rendered.trim() !== '<no value>';
      output = truthy ? `→ ${trueNext} (true)` : `→ ${falseNext} (false)`;
      for (const e of outEdgesForNode) edgeValues[e.id] = truthy ? 'true' : 'false';
    } else if (d.step_type === 'http') {
      const method = (cfg.method as string) || 'GET';
      const urlTemplate = (cfg.url_template as string) || '';
      const bodyTemplate = (cfg.body_template as string) || '';
      const appParamKey = (cfg.app_param_key as string) || '';
      const injectMode = (cfg.inject_mode as string) || 'header';
      const injectHeaderName = (cfg.inject_header_name as string) || 'api_key';

      let url = renderTemplate(urlTemplate, newVars);
      const headers: Record<string, string> = { 'Accept': 'application/json' };

      if (appParamKey && debugParams[appParamKey]) {
        const paramVal = debugParams[appParamKey];
        if (injectMode === 'query') {
          const sep = url.includes('?') ? '&' : '?';
          url += `${sep}${encodeURIComponent(injectHeaderName)}=${encodeURIComponent(paramVal)}`;
        } else if (injectMode === 'basic') {
          headers['Authorization'] = 'Basic ' + btoa(paramVal);
        } else if (injectMode === 'custom_header') {
          headers[injectHeaderName] = paramVal;
        } else {
          headers['Authorization'] = 'Bearer ' + paramVal;
        }
      }

      const proxyBody: Record<string, unknown> = { method, url, headers };
      if (bodyTemplate && method !== 'GET') {
        proxyBody.body = renderTemplate(bodyTemplate, newVars);
        (headers as Record<string, string>)['Content-Type'] = 'application/json';
      }
      const resp = await fetch('/api/v1/admin/debug-proxy', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
        body: JSON.stringify(proxyBody),
      });
      if (!resp.ok) {
        const errText = await resp.text();
        throw new Error(errText.slice(0, 120) || `HTTP ${resp.status}`);
      }
      const text = await resp.text();
      try {
        const parsed = JSON.parse(text);
        newVars['http_response'] = parsed;
      } catch {
        newVars['http_response'] = text;
      }
      output = text;
      for (const e of outEdgesForNode) edgeValues[e.id] = text;
    } else {
      output = `[${d.step_type} not supported in debug mode]`;
    }

    // Rewrite edge labels: show only vars the target node actually reads (∩ source writes).
    // Empty intersection → keep prior fallback value rather than showing all source writes.
    for (const e of outEdgesForNode) {
      const targetNode = nodes.find(n => n.id === e.target);
      if (!targetNode) continue;
      const relevant = edgeRelevantVars(node, targetNode);
      if (relevant.length === 0) {
        // No statically matched vars — keep whatever the step set as its generic output
        continue;
      }
      const label = relevant
        .map(v => `${v}: ${String(newVars[v] ?? '').slice(0, 60)}`)
        .filter(s => !s.endsWith(': '))
        .join('\n');
      if (label) edgeValues[e.id] = label;
    }

    return { vars: newVars, output, edgeValues };
  }

  function debugReset() {
    setDebug(prev => ({
      ...INITIAL_DEBUG,
      active: prev.active,
      setupComplete: false,
      paramSpecs: prev.paramSpecs,
      debugParams: prev.debugParams,
    }));
  }

  async function debugStartSetup() {
    if (!activeSkillId) {
      setDebug(prev => ({
        ...prev,
        active: true,
        setupComplete: false,
        paramSpecs: [],
        debugParams: {},
        error: 'Open a skill first — double-click a skill node on the canvas, then click Debug.',
      }));
      return;
    }

    // Build the test-input + LLM provider/model/key specs (always present for LLM agents).
    const baseSpecs = buildDebugParamSpecs(localPipeNodes).filter(
      s => s.key === '__test_input' || s.key === '__debug_provider' || s.key === '__debug_model' || s.key === '__debug_api_key',
    );

    // For published agents, fetch required params from the API (canonical, same source
    // as application runtime settings). Fall back to raw node scan for unpublished drafts.
    let apiSpecs: import('@/lib/api').AgentParamMeta[] = [];
    if (defId) {
      try {
        const resp = await themApi.getDefinitionParams(defId);
        apiSpecs = resp.required_params ?? [];
      } catch {
        // Draft not yet published — fall back to raw node scan below.
      }
    }

    let paramSpecs: import('./types').DebugParamSpec[];
    if (apiSpecs.length > 0) {
      // Use API params — authoritative, matches what runtime injection expects.
      const extraSpecs: import('./types').DebugParamSpec[] = apiSpecs.map(p => ({
        key: p.key,
        label: p.label,
        description: p.description,
        isSecret: p.type === 'secret',
        required: p.required,
      }));
      paramSpecs = [...baseSpecs, ...extraSpecs];
    } else {
      // Fallback: raw node scan (draft or no published spec yet).
      paramSpecs = buildDebugParamSpecs(localPipeNodes);
    }

    const prefs = await loadDebugPrefs();
    const params: Record<string, string> = {};
    for (const spec of paramSpecs) {
      if (spec.key === '__test_input') params[spec.key] = prefs.testInput;
      else params[spec.key] = ssGet(spec.key);
    }
    setDebug(prev => ({
      ...prev,
      active: true,
      setupComplete: false,
      paramSpecs,
      debugParams: params,
      error: null,
    }));
  }

  async function debugCommitSetup() {
    const testInput = debug.debugParams['__test_input'] ?? '';
    if (!testInput.trim()) { setDebug(prev => ({ ...prev, error: 'Test message is required.' })); return; }
    const hasLLM = debug.paramSpecs.some(s => s.key === '__debug_provider');
    if (hasLLM) {
      if (!(debug.debugParams['__debug_provider'] ?? '').trim()) {
        setDebug(prev => ({ ...prev, error: 'Select an LLM provider for debug.' })); return;
      }
      if (!(debug.debugParams['__debug_model'] ?? '').trim()) {
        setDebug(prev => ({ ...prev, error: 'Select a model for debug.' })); return;
      }
      if (!(debug.debugParams['__debug_api_key'] ?? '').trim()) {
        setDebug(prev => ({ ...prev, error: 'Enter an API key for the chosen provider.' })); return;
      }
    }
    saveDebugPrefs(testInput);
    for (const spec of debug.paramSpecs) {
      if (spec.isSecret) ssSet(spec.key, debug.debugParams[spec.key] ?? '');
    }
    setDebug(prev => ({ ...prev, setupComplete: true, error: null }));
  }

  async function debugRunAll() {
    if (!debug.setupComplete) return;
    const testInput = debug.debugParams['__test_input'] ?? '';
    const order = topoSort(localPipeNodes, localPipeEdges);
    if (!order) { setDebug(prev => ({ ...prev, error: 'Pipeline has a cycle — cannot execute.' })); return; }

    setDebug(prev => ({
      ...prev, mode: 'run-all', error: null, executionOrder: order,
      nodeStates: Object.fromEntries(order.map(id => [id, 'pending' as DebugNodeState])),
      nodeOutputs: {}, nodeErrors: {}, edgeValues: {}, vars: {}, nodeInputVars: {},
    }));

    const inputNode = localPipeNodes.find(n => (n.data as unknown as StepData).step_type === 'input');
    let vars: Record<string, unknown> = {};
    if (inputNode) {
      const inputData = inputNode.data as unknown as StepData;
      const bindVar = (inputData.config?.bindings as Record<string,string>)?.text || 'input';
      vars[bindVar] = testInput;
    }

    for (const nodeId of order) {
      const inputSnapshot = { ...vars };
      setDebug(prev => ({ ...prev, vars, nodeStates: { ...prev.nodeStates, [nodeId]: 'running' }, nodeInputVars: { ...prev.nodeInputVars, [nodeId]: inputSnapshot } }));
      try {
        const result = await executeStep(nodeId, localPipeNodes, localPipeEdges, vars, debug.debugParams);
        vars = result.vars;
        setDebug(prev => ({
          ...prev, vars,
          edgeValues: { ...prev.edgeValues, ...result.edgeValues },
          nodeStates: { ...prev.nodeStates, [nodeId]: 'done' },
          nodeOutputs: { ...prev.nodeOutputs, [nodeId]: result.output },
        }));
      } catch (err) {
        const msg = String(err);
        setDebug(prev => ({
          ...prev,
          nodeStates: { ...prev.nodeStates, [nodeId]: 'error' },
          nodeErrors: { ...prev.nodeErrors, [nodeId]: msg },
          error: `Step failed: ${msg}`,
        }));
        return;
      }
    }
    setDebug(prev => ({ ...prev, currentStepIndex: order.length }));
  }

  async function debugStep() {
    if (!debug.setupComplete) return;
    const testInput = debug.debugParams['__test_input'] ?? '';

    if (!debug.mode) {
      const order = topoSort(localPipeNodes, localPipeEdges);
      if (!order) { setDebug(prev => ({ ...prev, error: 'Pipeline has a cycle.' })); return; }

      const inputNode = localPipeNodes.find(n => (n.data as unknown as StepData).step_type === 'input');
      let initVars: Record<string, unknown> = {};
      if (inputNode) {
        const inputData = inputNode.data as unknown as StepData;
        const bindVar = (inputData.config?.bindings as Record<string,string>)?.text || 'input';
        initVars[bindVar] = testInput;
      }

      const firstNodeId = order[0];
      setDebug(prev => ({
        ...prev, mode: 'step', error: null, executionOrder: order, currentStepIndex: 0,
        vars: initVars,
        nodeStates: { ...Object.fromEntries(order.map(id => [id, 'idle' as DebugNodeState])), [firstNodeId]: 'pending' },
        nodeOutputs: {}, nodeErrors: {}, edgeValues: {}, pendingVarOverrides: {}, nodeInputVars: {},
      }));
      return;
    }

    const { executionOrder, currentStepIndex, vars } = debug;
    if (currentStepIndex >= executionOrder.length) return;

    const nodeId = executionOrder[currentStepIndex];
    const mergedVars = { ...vars, ...debug.pendingVarOverrides };

    setDebug(prev => ({
      ...prev,
      nodeStates: { ...prev.nodeStates, [nodeId]: 'running' },
      nodeInputVars: { ...prev.nodeInputVars, [nodeId]: { ...mergedVars } },
      pendingVarOverrides: {},
    }));

    try {
      const result = await executeStep(nodeId, localPipeNodes, localPipeEdges, mergedVars, debug.debugParams);
      const nextIdx = currentStepIndex + 1;
      const nextNodeId = executionOrder[nextIdx];
      setDebug(prev => ({
        ...prev,
        vars: result.vars,
        edgeValues: { ...prev.edgeValues, ...result.edgeValues },
        nodeStates: {
          ...prev.nodeStates,
          [nodeId]: 'done',
          ...(nextNodeId ? { [nextNodeId]: 'pending' } : {}),
        },
        nodeOutputs: { ...prev.nodeOutputs, [nodeId]: result.output },
        currentStepIndex: nextIdx,
      }));
    } catch (err) {
      const msg = String(err);
      setDebug(prev => ({
        ...prev,
        nodeStates: { ...prev.nodeStates, [nodeId]: 'error' },
        nodeErrors: { ...prev.nodeErrors, [nodeId]: msg },
        error: `Step failed: ${msg}`,
      }));
    }
  }

  function updateSelectedNodeField(field: string, value: string) {
    if (!selectedNode) return;
    if (activeView === 'agent') {
      setAgentNodes(prev => prev.map(n =>
        n.id === selectedNode.id ? { ...n, data: { ...n.data, [field]: value } } : n
      ));
      if (field === 'display_name' && selectedNode.id === 'agent-root' && !defId) {
        const slug = value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
        setAgentSlug(slug);
      }
    } else {
      setLocalPipeNodes(prev => prev.map(n =>
        n.id === selectedNode.id ? { ...n, data: { ...n.data, [field]: value } } : n
      ));
    }
    markDirty();
  }

  function updateStepConfig(key: string, value: unknown) {
    if (!selectedNode || activeView !== 'skill') return;
    setLocalPipeNodes(prev => prev.map(n =>
      n.id === selectedNode.id
        ? { ...n, data: { ...n.data, config: { ...(n.data.config as Record<string, unknown>), [key]: value } } }
        : n
    ));
    markDirty();
  }

  const debugNodes = activeView === 'skill' && debug.active
    ? localPipeNodes.map(n => ({
        ...n,
        data: {
          ...n.data,
          _debug: {
            state: debug.nodeStates[n.id] ?? 'idle',
            output: debug.nodeOutputs[n.id],
            error: debug.nodeErrors[n.id],
          },
        },
      }))
    : localPipeNodes;

  const runningNodeId = Object.entries(debug.nodeStates).find(([, s]) => s === 'running')?.[0];

  const debugEdges = activeView === 'skill' && debug.active
    ? localPipeEdges.map(e => {
        const hasDoneValue = !!debug.edgeValues[e.id];
        const isFlowing = runningNodeId === e.source;
        const edgeState: 'idle' | 'flowing' | 'done' = isFlowing ? 'flowing' : hasDoneValue ? 'done' : 'idle';
        return {
          ...e,
          type: 'debugEdge',
          data: {
            ...((e.data ?? {}) as Record<string, unknown>),
            debugState: edgeState,
            label: hasDoneValue ? `"${debug.edgeValues[e.id]}"` : undefined,
          },
        };
      })
    : localPipeEdges;

  const nodeValidationMap = (() => {
    const m: Record<string, 'error' | 'warning'> = {};
    for (const iss of validation.issues) {
      if (!iss.node_id) continue;
      const current = m[iss.node_id];
      if (!current || (iss.severity === 'error' && current === 'warning')) {
        m[iss.node_id] = iss.severity;
      }
    }
    return m;
  })();

  const pipelineIssues = activeSkillId
    ? validation.issues.filter(iss => iss.skill_id === activeSkillId || !iss.skill_id)
    : validation.issues;

  const validatedPipeNodes = localPipeNodes.map(n => {
    const stepId = (n.data as unknown as StepData).step_id;
    const stepType = (n.data as unknown as StepData).step_type;
    const isStub = !getNodeDef(stepType).executable;
    const valSeverity = nodeValidationMap[stepId] ?? null;
    if (!valSeverity && !isStub) return n;
    return { ...n, data: { ...n.data, _validation: valSeverity, _stub: isStub } };
  });

  const errorCount   = validation.issues.filter(iss => iss.severity === 'error').length;
  const warningCount = validation.issues.filter(iss => iss.severity === 'warning').length;

  const debugRunning = Object.values(debug.nodeStates).some(s => s === 'running');

  const logoState: LogoState = (() => {
    if (saving || publishing || validation.loading || debugRunning) return 'thinking';
    if (logoResult === 'invalid') return 'error';
    if (logoResult === 'warn')    return 'warning';
    if (logoResult === 'valid')   return 'success';
    if (dirty) return 'dirty';
    return 'idle';
  })();

  const currentNodes = activeView === 'agent'
    ? agentNodes
    : debug.active ? debugNodes : validatedPipeNodes;
  const currentEdges = activeView === 'agent' ? agentEdges : (debug.active ? debugEdges : localPipeEdges);

  return (
    <LayoutDirContext.Provider value={layoutDir}>
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
          {activeView === 'skill' ? 'Back to Agent' : 'Back to Agents'}
        </button>

        <div style={{ flex: 1 }}>
          {activeView === 'agent' ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
              <input
                value={agentSlug}
                onChange={e => { setAgentSlug(e.target.value); markDirty(); }}
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
              Pipeline: {activeSkillId}
            </span>
          )}
        </div>

        {saveError && (
          <span style={{ color: '#f87171', fontSize: '12px', maxWidth: '300px' }}>{saveError}</span>
        )}
        {publishedRevision !== null && (
          <span style={{ color: '#34d399', fontSize: '12px' }}>Published rev {publishedRevision}</span>
        )}

        {/* Validation issues badge */}
        {defId && (validation.loading || errorCount > 0 || warningCount > 0) && (
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            {validation.loading && (
              <span style={{ color: '#64748b', fontSize: '11px', fontStyle: 'italic' }}>validating…</span>
            )}
            {!validation.loading && errorCount > 0 && (
              <span style={{
                background: 'rgba(248,113,113,0.15)', border: '1px solid rgba(248,113,113,0.4)',
                color: '#f87171', padding: '3px 8px', borderRadius: '20px', fontSize: '11px', fontWeight: 700,
              }}>
                ✗ {errorCount} error{errorCount !== 1 ? 's' : ''}
              </span>
            )}
            {!validation.loading && warningCount > 0 && (
              <span style={{
                background: 'rgba(245,158,11,0.15)', border: '1px solid rgba(245,158,11,0.4)',
                color: '#f59e0b', padding: '3px 8px', borderRadius: '20px', fontSize: '11px', fontWeight: 700,
              }}>
                ⚠ {warningCount} warning{warningCount !== 1 ? 's' : ''}
              </span>
            )}
            {!validation.loading && errorCount === 0 && warningCount === 0 && validation.lastValidatedAt && (
              <span style={{ color: '#34d399', fontSize: '11px' }}>✓ valid</span>
            )}
          </div>
        )}

        {defId && (
          <button onClick={handleDelete} disabled={deleting} style={{
            background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.4)',
            color: '#f87171', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {deleting ? 'Deleting...' : 'Delete Draft'}
          </button>
        )}
        {defId && (
          <button onClick={handleValidate} disabled={validating || validation.loading} style={{
            background: 'rgba(52,211,153,0.1)', border: '1px solid rgba(52,211,153,0.4)',
            color: '#34d399', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {validating || validation.loading ? 'Validating…' : 'Validate'}
          </button>
        )}
        {defId && (
          <button
            onClick={handlePublish}
            disabled={publishing || errorCount > 0}
            title={errorCount > 0 ? `Fix ${errorCount} error${errorCount !== 1 ? 's' : ''} before publishing` : undefined}
            style={{
              background: (publishing || errorCount > 0) ? 'rgba(0,240,255,0.05)' : 'rgba(0,240,255,0.15)',
              border: '1px solid rgba(0,240,255,0.4)',
              color: errorCount > 0 ? 'rgba(0,240,255,0.4)' : '#00f0ff',
              padding: '6px 14px', borderRadius: '6px', cursor: errorCount > 0 ? 'not-allowed' : 'pointer', fontSize: '13px',
            }}
          >
            {publishing ? 'Publishing…' : 'Publish'}
          </button>
        )}
        {activeView === 'skill' && (
          <button onClick={() => {
            if (debug.active) {
              setDebug(INITIAL_DEBUG);
            } else {
              debugStartSetup();
            }
          }} style={{
            background: debug.active ? 'rgba(245,158,11,0.2)' : 'rgba(100,116,139,0.1)',
            border: `1px solid ${debug.active ? C.amber : C.outline}`,
            color: debug.active ? C.amber : C.textMuted,
            padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {debug.active ? '🐛 Exit Debug' : '🐛 Debug'}
          </button>
        )}
        {activeView === 'agent' && !defId && (
          <>
            <input
              ref={importFileRef}
              type="file"
              accept=".json,application/json"
              style={{ display: 'none' }}
              onChange={handleImportFileChange}
            />
            <button onClick={handleImportJSON} style={{
              background: 'rgba(99,102,241,0.12)', border: `1px solid rgba(99,102,241,0.5)`,
              color: C.indigo, padding: '7px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
            }}>
              ↓ Import JSON
            </button>
          </>
        )}
        {/* Undo / Redo */}
        <button onClick={handleUndo} disabled={!canUndo} title="Undo" style={{
          background: 'transparent', border: `1px solid ${canUndo ? C.outline : 'transparent'}`,
          color: canUndo ? '#cbd5e1' : '#334155', padding: '6px 10px', borderRadius: '6px',
          cursor: canUndo ? 'pointer' : 'default', fontSize: '14px',
        }}>↩</button>
        <button onClick={handleRedo} disabled={!canRedo} title="Redo" style={{
          background: 'transparent', border: `1px solid ${canRedo ? C.outline : 'transparent'}`,
          color: canRedo ? '#cbd5e1' : '#334155', padding: '6px 10px', borderRadius: '6px',
          cursor: canRedo ? 'pointer' : 'default', fontSize: '14px',
        }}>↪</button>
        {/* Export */}
        <button onClick={handleExport} title="Export as JSON file" style={{
          background: 'rgba(99,102,241,0.12)', border: `1px solid rgba(99,102,241,0.5)`,
          color: C.indigo, padding: '7px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
        }}>↑ Export JSON</button>
        <button onClick={handleSave} disabled={saving} style={{
          background: dirty ? C.cyan : 'rgba(0,240,255,0.2)',
          border: 'none', color: '#000', fontWeight: 700,
          padding: '7px 20px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          opacity: saving ? 0.7 : 1,
        }}>
          {saving ? 'Saving...' : defId ? 'Save Changes' : 'Create Draft'}
        </button>
      </div>

      {loadError && (
        <div style={{ background: 'rgba(239,68,68,0.1)', padding: '10px 24px', color: '#f87171', fontSize: '13px' }}>
          {loadError}
        </div>
      )}

      {/* ── Debug bar (only in skill view when debug is active) ── */}
      {activeView === 'skill' && debug.active && (
        <DebugPanel
          debug={debug}
          setDebug={setDebug}
          debugRunning={debugRunning}
          debugCommitSetup={debugCommitSetup}
          debugRunAll={debugRunAll}
          debugStep={debugStep}
          debugReset={debugReset}
        />
      )}

      {/* ── Issues panel — shown when there are validation issues ── */}
      {activeView === 'skill' && pipelineIssues.length > 0 && !debug.active && (
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
        {/* ── Node Library (left panel) ── */}
        <div style={{
          width: libraryWidth, flexShrink: 0, borderRight: `1px solid ${C.outline}`,
          background: C.surface, overflowY: 'auto', display: 'flex', flexDirection: 'column',
          position: 'relative',
        }} className="dark-scrollbar">
          <div style={{ padding: '14px 14px 8px', fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1.5, textTransform: 'uppercase', borderBottom: `1px solid ${C.outline}` }}>
            {activeView === 'agent' ? 'Node Library' : 'Step Library'}
          </div>

          <div style={{ padding: '12px 10px', display: 'flex', flexDirection: 'column', gap: 6, flex: 1 }}>
            {activeView === 'agent' ? (
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
                          onClick={() => addStepToActivePipeline(type as AgentStepDoc['type'])}
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

          {/* Library resize handle */}
          <div
            onMouseDown={e => { e.preventDefault(); resizingRef.current = { side: 'library', startX: e.clientX, startW: libraryWidth }; document.body.style.cursor = 'col-resize'; document.body.style.userSelect = 'none'; }}
            style={{
              position: 'absolute', top: 0, right: -3, width: 6, height: '100%',
              cursor: 'col-resize', zIndex: 10,
            }}
          />
        </div>

        {/* Canvas */}
        <div style={{ flex: 1, position: 'relative' }}>
          <style>{`.react-flow__pane { cursor: default !important; } .react-flow__pane.dragging { cursor: default !important; } .react-flow__node.selected > div { box-shadow: 0 0 0 2px #00f0ff, 0 0 14px rgba(0,240,255,0.35) !important; } .palette-card { transition: filter 0.15s, box-shadow 0.15s; } .palette-card:hover { filter: brightness(1.7) saturate(1.2); box-shadow: 0 0 10px rgba(255,255,255,0.1), 0 2px 8px rgba(0,0,0,0.3); }`}</style>

          {/* Canvas toolbar — fit + auto-arrange */}
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
              onClick={() => {
                if (activeView === 'agent') {
                  setAgentNodes(ns => applyDagreLayout(ns, agentEdges, layoutDir));
                } else {
                  setLocalPipeNodes(ns => applyDagreLayout(ns, localPipeEdges, layoutDir));
                }
                setTimeout(() => fitView({ padding: 0.2 }), 50);
              }}
              title="Auto-arrange nodes"
              style={{ background: C.surface, border: `1px solid ${C.outline}`, color: C.textMuted, borderRadius: 6, padding: '5px 10px', cursor: 'pointer', fontSize: 18, lineHeight: 1 }}
            >⚏</button>
            <button
              onClick={() => {
                const next: LayoutDir = layoutDir === 'TB' ? 'LR' : 'TB';
                setLayoutDir(next);
                if (activeView === 'agent') {
                  setAgentNodes(ns => applyDagreLayout(ns, agentEdges, next));
                } else {
                  setLocalPipeNodes(ns => applyDagreLayout(ns, localPipeEdges, next));
                }
                setTimeout(() => fitView({ padding: 0.2 }), 50);
              }}
              title={layoutDir === 'TB' ? 'Switch to horizontal layout' : 'Switch to vertical layout'}
              style={{ background: C.surface, border: `1px solid ${C.outline}`, color: C.textMuted, borderRadius: 6, padding: '5px 10px', cursor: 'pointer', fontSize: 14, lineHeight: 1, fontWeight: 600 }}
            >{layoutDir === 'TB' ? '⇆' : '⇅'}</button>
          </div>

          {/* Context menu */}
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
              onDrop={(e: DragEvent) => {
                e.preventDefault();
                const nodeType = e.dataTransfer.getData('nodeType');
                if (nodeType === 'skill') {
                  const bounds = (e.currentTarget as HTMLElement).getBoundingClientRect();
                  const pos = screenToFlowPosition({ x: e.clientX - bounds.left, y: e.clientY - bounds.top });
                  const sid = genUUID();
                  const newNode: Node = { id: `skill-${sid}`, type: 'skill', position: pos, data: { skill_id: sid, name: 'New Skill', description: '', tags: [], input_modes: ['text/plain'], output_modes: ['text/plain'], examples: [] } };
                  setAgentNodes(prev => [...prev, newNode]);
                  setAgentEdges(prev => [...prev, { id: `root-to-${sid}`, source: 'agent-root', target: `skill-${sid}` }]);
                  setSkillPipelines(prev => ({ ...prev, [sid]: makeDefaultPipeline() }));
                  markDirty();
                }
              }}
              fitView
            >
              <Background variant={BackgroundVariant.Dots} gap={20} color="rgba(255,255,255,0.05)" />
              <Controls />
            </ReactFlow>
          ) : (
            <ReactFlow
              nodes={debug.active ? debugNodes : localPipeNodes}
              edges={debug.active ? debugEdges : localPipeEdges}
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
              onDrop={(e: DragEvent) => {
                e.preventDefault();
                const nodeType = e.dataTransfer.getData('nodeType');
                if (nodeType === 'step') {
                  const stepType = e.dataTransfer.getData('stepType') as AgentStepDoc['type'];
                  const bounds = (e.currentTarget as HTMLElement).getBoundingClientRect();
                  const pos = screenToFlowPosition({ x: e.clientX - bounds.left, y: e.clientY - bounds.top });
                  const stepId = genUUID();
                  const newNode: Node = { id: `step-${stepId}`, type: 'step', position: pos, data: { step_id: stepId, step_type: stepType, label: stepType.replace('_', ' '), config: {} } };
                  setLocalPipeNodes(prev => [...prev, newNode]);
                  markDirty();
                }
              }}
              fitView
            >
              <Background variant={BackgroundVariant.Dots} gap={20} color="rgba(255,255,255,0.05)" />
              <Controls />
            </ReactFlow>
          )}
        </div>

        {/* Properties panel */}
        {selectedNode && (
          <RightPanel
            selectedNode={selectedNode}
            setSelectedNode={setSelectedNode}
            propertiesWidth={propertiesWidth}
            onResizeStart={(e) => { e.preventDefault(); resizingRef.current = { side: 'properties', startX: e.clientX, startW: propertiesWidth }; document.body.style.cursor = 'col-resize'; document.body.style.userSelect = 'none'; }}
            activeView={activeView}
            agentNodes={agentNodes}
            localPipeNodes={localPipeNodes}
            localPipeEdges={localPipeEdges}
            validationIssues={validation.issues}
            stepContracts={validation.stepContracts}
            debug={debug}
            updateSelectedNodeField={updateSelectedNodeField}
            updateStepConfig={updateStepConfig}
            setAgentNodes={setAgentNodes}
            setDirty={setDirty}
            savePipelineState={savePipelineState}
            setActiveSkillId={setActiveSkillId}
            setActiveView={setActiveView}
            setDebug={setDebug}
            debugStep={debugStep}
            nodeTypesReady={nodeTypesReady}
            onDeleteInput={onDeleteInput}
          />
        )}
      </div>
    </div>
    </LayoutDirContext.Provider>
  );
}

// ── Page (top-level component) ────────────────────────────────────────────────

export default function AgentBuilderPage() {
  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
          <ReactFlowProvider>
            <CanvasInner />
          </ReactFlowProvider>
        </div>
      </div>
    </AuthGuard>
  );
}
