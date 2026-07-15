'use client';
import { useEffect, useState, useCallback, useRef, DragEvent } from 'react';
// eslint-disable-next-line @typescript-eslint/no-require-imports, @typescript-eslint/no-explicit-any
const dagre: any = (typeof window !== 'undefined' ? require('dagre') : null);
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Application, type OrchestratorFull, type Agent, type MiddlewareDef, type AppOrchestratorOut } from '@/lib/api';
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  addEdge,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type Connection,
  type NodeTypes,
  Handle,
  Position,
  useReactFlow,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';

// ── Design tokens ────────────────────────────────────────────────────────────
const C = {
  bg: 'var(--tm-bg)',
  surface: 'var(--tm-panel)',
  surfaceContainer: 'var(--tm-canvas-container)',
  surfaceLow: 'var(--tm-canvas-inset)',
  cyan: '#00f0ff',
  cyanBg: 'rgba(0,240,255,0.05)',
  cyanBorder: 'rgba(0,240,255,0.4)',
  cyanGlow: '0 0 15px rgba(0,240,255,0.15)',
  purple: '#d0bcff',
  purpleBg: 'rgba(87,27,193,0.1)',
  purpleBorder: '#d0bcff',
  purpleGlow: '0 0 15px rgba(208,188,255,0.15)',
  green: '#4ade80',
  greenBg: 'rgba(74,222,128,0.05)',
  greenBorder: 'rgba(74,222,128,0.3)',
  amber: '#f59e0b',
  amberBg: 'rgba(245,158,11,0.05)',
  amberBorder: 'rgba(245,158,11,0.3)',
  amberGlow: '0 0 15px rgba(245,158,11,0.15)',
  text: 'var(--tm-card-text)',
  textMuted: 'var(--tm-card-text-muted)',
  outline: 'var(--tm-canvas-border)',
  outlineVariant: 'var(--tm-canvas-border)',
  error: '#ffb4ab',
  errorBg: 'rgba(255,180,171,0.1)',
  glass: 'var(--tm-panel)',
  glassBorder: 'var(--tm-canvas-glass-border)',
};

const glass = {
  background: C.glass,
  backdropFilter: 'blur(12px)',
  WebkitBackdropFilter: 'blur(12px)',
  border: `1px solid ${C.glassBorder}`,
};

const deleteNodeRef = { current: (_id: string) => {} };

// ── Types ────────────────────────────────────────────────────────────────────
const ENTRY_POINT_TYPES = ['websocket', 'sse', 'webrtc'] as const;
type EntryPointType = typeof ENTRY_POINT_TYPES[number];

interface EntryPointData { label: string; epType: EntryPointType; accessMode: 'token' | 'public'; slug: string; appName?: string; convTokenLimit?: string; _epId?: string; [key: string]: unknown; }
interface OrchestratorData {
  orchestratorId: string;          // template global orch id (for library seeding)
  name: string;
  displayName: string;
  model: string | null;            // alias of llmModel — kept for compat
  maxParallelTools: number;
  // app_orchestrators fields:
  appOrchestratorId: string | null;  // app_orchestrators.id; null = unsaved new instance
  systemPrompt: string | null;
  allowedAgentIds: string[];
  llmProvider: string | null;
  llmModel: string | null;
  maxIterations: number;
  historyWindow: number;
  delegatable: boolean;
  kind: string;
  budgetTokens: number | null;
  [key: string]: unknown;
}
interface AgentData { agentId: string; name: string; displayName: string; description: string; transport: string; endpointUrl: string; tags?: string[]; icon?: string | null; [key: string]: unknown; }
interface MiddlewareData { defId: string; slug: string; kind: 'guard' | 'cache'; displayName: string; description: string; config: Record<string, unknown>; configOverride: Record<string, unknown>; nodeId: string; [key: string]: unknown; }

type ProposalStatus = 'pending' | 'applying' | 'applied' | 'failed' | 'stale';
const PROPOSAL_ALLOWED_FIELDS = new Set([
  'system_prompt', 'description', 'display_name',
  'max_iterations', 'history_window', 'max_parallel_tools',
]);
interface Proposal {
  id: string; type: string;
  targetType: 'orchestrator' | 'agent';
  targetId: string; targetName: string; field: string;
  current: string | number; suggested: string | number; reason: string;
  status: ProposalStatus; error?: string;
}
interface AdvisorMessage { role: 'user' | 'assistant'; text: string; streaming?: boolean; proposals?: Proposal[]; }

function parseAdvisorBuffer(buf: string): { text: string; proposals: Proposal[] } {
  const OPEN = '```them-proposal';
  const CLOSE = '```';
  const proposals: Proposal[] = [];
  let text = buf;
  let searchFrom = 0;
  while (true) {
    const openIdx = text.indexOf(OPEN, searchFrom);
    if (openIdx === -1) break;
    const afterOpen = text.indexOf('\n', openIdx);
    if (afterOpen === -1) break; // opening fence not yet fully received
    const closeIdx = text.indexOf('\n' + CLOSE, afterOpen);
    if (closeIdx === -1) {
      // Block not closed yet — hide everything from opening fence onward
      text = text.slice(0, openIdx).trimEnd() + (text.slice(0, openIdx).trim() ? '\n\n_Preparing suggestion…_' : '');
      break;
    }
    const jsonStr = text.slice(afterOpen + 1, closeIdx).trim();
    const blockEnd = closeIdx + 1 + CLOSE.length;
    try {
      const obj = JSON.parse(jsonStr);
      if (obj.id && obj.targetId && obj.targetType && PROPOSAL_ALLOWED_FIELDS.has(obj.field)) {
        proposals.push({
          id: String(obj.id), type: String(obj.type ?? ''),
          targetType: obj.targetType, targetId: String(obj.targetId),
          targetName: String(obj.targetName ?? obj.targetId), field: String(obj.field),
          current: obj.current ?? '', suggested: obj.suggested ?? '',
          reason: String(obj.reason ?? ''), status: 'pending',
        });
      }
    } catch { /* malformed — silently drop */ }
    // Remove the fenced block from display text
    text = text.slice(0, openIdx).trimEnd() + text.slice(blockEnd);
    // Don't advance searchFrom — new text may have shifted
  }
  return { text, proposals };
}

function mergeProposals(existing: Proposal[] | undefined, incoming: Proposal[]): Proposal[] {
  if (!existing || existing.length === 0) return incoming;
  const statusMap = new Map(existing.map(p => [p.id, p.status]));
  const errorMap = new Map(existing.map(p => [p.id, p.error]));
  return incoming.map(p => ({
    ...p,
    status: statusMap.get(p.id) ?? p.status,
    error: errorMap.get(p.id),
  }));
}

function getBridgeWs(): string {
  if (typeof window === 'undefined') return '';
  if (process.env.NEXT_PUBLIC_BRIDGE_WS_URL) return process.env.NEXT_PUBLIC_BRIDGE_WS_URL;
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.host}`;
}

// ── Change 1: CSS animations + font inheritance ───────────────────────────────
const CANVAS_STYLES = `
  /* Force all text bright — builder lives on a dark bg, globals.css light-mode vars bleed in */
  .builder-root, .builder-root * {
    color: inherit;
  }
  .builder-root input,
  .builder-root select,
  .builder-root textarea {
    color: var(--tm-card-text) !important;
    background-color: var(--tm-canvas-inset) !important;
    -webkit-text-fill-color: var(--tm-card-text) !important;
  }
  .builder-root input::placeholder,
  .builder-root textarea::placeholder {
    color: var(--tm-card-text-muted) !important;
    -webkit-text-fill-color: var(--tm-card-text-muted) !important;
  }
  .builder-root input[style*="color: #f59e0b"],
  .builder-root input[style*="color:#f59e0b"] {
    color: #f59e0b !important;
    -webkit-text-fill-color: #f59e0b !important;
  }
  /* Slug input on entry-point node */
  .ep-slug-set {
    color: var(--tm-card-text) !important;
    -webkit-text-fill-color: var(--tm-card-text) !important;
  }
  .ep-slug-missing {
    color: #f59e0b !important;
    -webkit-text-fill-color: #f59e0b !important;
  }
  .ep-slug-missing::placeholder {
    color: rgba(245,158,11,0.5) !important;
    -webkit-text-fill-color: rgba(245,158,11,0.5) !important;
  }
  @keyframes handlePulse {
    0%, 100% { box-shadow: 0 0 0 0 rgba(0,240,255,0.4); }
    50% { box-shadow: 0 0 0 5px rgba(0,240,255,0); }
  }
  .react-flow__node.selected .react-flow__handle {
    animation: handlePulse 1.2s ease-in-out infinite;
  }
  .react-flow__node.selected {
    outline: none !important;
    box-shadow: none !important;
  }
  .react-flow__node *:not(.material-symbols-outlined):not(.material-icons) {
    font-family: inherit;
    box-sizing: border-box;
  }
  .react-flow__node .material-symbols-outlined {
    font-family: 'Material Symbols Outlined';
    box-sizing: border-box;
  }
  .react-flow__edge:hover .react-flow__edge-path {
    stroke-width: 3;
    filter: drop-shadow(0 0 4px rgba(248,113,113,0.6));
    cursor: pointer;
  }
  .react-flow__edge-path {
    cursor: pointer;
  }
  /* Tooltip */
  .nl-tooltip {
    position: relative;
  }
  .nl-tooltip .nl-tip {
    display: none;
    position: absolute;
    left: calc(100% + 10px);
    top: 50%;
    transform: translateY(-50%);
    background: var(--tm-card-chrome);
    border: 1px solid rgba(0,240,255,0.22);
    border-radius: 8px;
    padding: 8px 11px;
    width: 220px;
    z-index: 999;
    pointer-events: none;
    box-shadow: 0 8px 24px rgba(0,0,0,0.5);
  }
  .nl-tooltip:hover .nl-tip {
    display: block;
  }
  /* Section subscroll */
  .nl-section-list {
    max-height: 430px; /* ~10 items × 43px */
    overflow-y: auto;
    overflow-x: hidden;
    scrollbar-width: thin;
    scrollbar-color: rgba(0,240,255,0.2) transparent;
  }
  .nl-section-list::-webkit-scrollbar { width: 4px; }
  .nl-section-list::-webkit-scrollbar-track { background: transparent; }
  .nl-section-list::-webkit-scrollbar-thumb { background: rgba(0,240,255,0.2); border-radius: 4px; }
  .nl-section-list::-webkit-scrollbar-thumb:hover { background: rgba(0,240,255,0.4); }
  /* Resize handle */
  .nl-resize-handle {
    width: 4px;
    flex-shrink: 0;
    cursor: col-resize;
    background: transparent;
    transition: background 150ms ease;
    position: relative;
    z-index: 10;
  }
  .nl-resize-handle:hover,
  .nl-resize-handle.dragging {
    background: rgba(0,240,255,0.35);
  }
`;

// ── Node Components ──────────────────────────────────────────────────────────
// Tiny the-M logo badge for internal nodes — sits in top-left corner
function InternalMBadge() {
  return (
    <div title="Internal the-M system component" style={{
      position: 'absolute', top: 6, left: 8,
      display: 'flex', alignItems: 'center', gap: 4,
      pointerEvents: 'none',
    }}>
      <svg width="14" height="11" viewBox="0 0 1407 1118" style={{ opacity: 0.55, flexShrink: 0 }}>
        <polygon points="88,77 184,146 244,191 281,217 336,259 355,272 358,272 367,267 372,266 379,262 391,258 433,239 440,237 473,222 513,206 520,202 546,192 555,187 558,187 446,102 433,91 421,83 403,68 397,65 392,60 331,15 318,4 274,19 264,21 246,28 239,29 217,37 214,37 211,39 201,41 189,46 186,46 154,57 151,57 148,59 141,60 138,62 104,73 101,73 98,75" fill="#a0f0d0"/>
        <polygon points="1323,77 1313,75 1292,67 1289,67 1239,50 1236,50 1233,48 1230,48 1189,34 1176,31 1094,4 1085,12 1074,19 1053,36 959,106 855,187 876,196 881,197 973,237 980,239 1034,263 1048,268 1055,272 1059,272 1139,213 1146,209 1177,185 1188,178 1208,162 1284,107" fill="#a0f0d0"/>
        <polygon points="70,97 70,334 71,335 72,350 76,365 104,429 108,435 180,486 184,490 245,534 345,609 339,293 305,269 281,250 252,230 182,177 179,176 153,156 150,155" fill="#a0f0d0"/>
        <polygon points="1342,97 1252,162 1248,166 1152,236 1148,240 1126,255 1122,259 1112,265 1103,273 1074,293 1073,296 1073,317 1072,318 1072,355 1071,356 1071,415 1070,416 1070,461 1069,462 1069,526 1068,527 1067,609 1306,433 1325,392 1336,365 1341,343 1341,331 1342,330" fill="#a0f0d0"/>
        <polygon points="682,361 576,210 381,292 532,410 577,395 580,395 586,392 595,390 613,384 616,382 622,381 664,367 667,365" fill="#a0f0d0"/>
        <polygon points="732,361 803,384 806,386 809,386 831,394 834,394 860,404 863,404 881,410 1033,291 837,210 764,315 760,319 759,322 740,348" fill="#a0f0d0"/>
        <polygon points="367,314 367,373 368,374 368,430 369,431 371,567 380,574 383,575 388,580 396,585 505,669 508,611 509,610 509,595 510,594 512,540 513,539 513,524 514,523 514,504 515,503 515,490 516,489 517,454 518,453 518,434 519,433 504,421 501,420 490,410 468,394 427,361 423,359 395,336 392,335" fill="#a0f0d0"/>
        <polygon points="1046,314 894,433 895,456 896,457 896,475 897,476 897,494 898,495 898,513 899,514 901,561 902,562 902,579 903,580 903,594 904,595 906,650 907,651 907,666 908,669 934,648 971,621 1041,567" fill="#a0f0d0"/>
        <polygon points="549,424 693,539 693,534 694,533 693,532 693,377 676,382 664,387 660,387 657,389 654,389 635,396 632,396" fill="#a0f0d0"/>
        <polygon points="864,424 815,407 812,407 791,400 779,395 776,395 721,377 721,539 732,529 736,527 752,513" fill="#a0f0d0"/>
        <polygon points="535,446 532,511 531,512 531,531 530,532 530,546 529,547 529,567 528,568 527,600 526,601 526,616 525,617 525,634 524,635 524,650 523,651 523,662 522,663 522,682 543,697 628,763 640,771 645,776 649,778 692,812 693,809 693,799 692,798 692,793 693,792 693,572 685,567 679,561 675,559 649,537 611,508 605,502 602,501" fill="#a0f0d0"/>
        <polygon points="878,446 721,572 721,594 720,595 720,775 721,776 720,780 721,781 721,812 752,787 756,785 816,738 892,681 891,669 890,668 890,650 889,649 889,633 888,632 888,619 887,618 885,567 884,566 884,554 883,553 883,532 882,531 882,513 881,512" fill="#a0f0d0"/>
        <polygon points="100,461 95,488 89,506 87,509 86,515 77,534 75,541 55,582 38,613 16,647 13,656 13,662 16,670 26,679 42,685 62,690 67,693 74,700 76,705 76,720 68,743 68,749 70,755 75,763 87,770 97,772 125,772 126,771 130,772 128,775 112,781 89,784 83,791 81,797 81,805 83,811 89,818 100,824 105,829 109,836 111,843 111,860 105,889 105,910 108,922 115,933 121,939 173,974 286,1057 326,1088 345,1105 345,641" fill="#a0f0d0"/>
        <polygon points="1312,462 1273,489 1230,522 1227,523 1143,586 1067,641 1067,1106 1080,1093 1135,1050 1138,1049 1172,1023 1235,978 1239,974 1249,968 1253,964 1256,963 1270,952 1292,938 1301,928 1305,920 1307,912 1308,897 1307,896 1307,888 1301,858 1302,839 1307,829 1312,824 1323,818 1328,813 1331,806 1331,796 1330,792 1324,784 1311,783 1297,780 1286,776 1282,773 1284,771 1287,772 1316,772 1328,769 1335,765 1340,760 1344,750 1344,742 1336,717 1336,706 1339,699 1344,694 1353,689 1371,685 1386,679 1394,673 1399,663 1399,655 1397,649 1372,610 1339,546 1321,500 1321,497 1316,484" fill="#a0f0d0"/>
      </svg>
      <span style={{ fontSize: 8, fontWeight: 700, color: '#a0f0d0', letterSpacing: 0.8, textTransform: 'uppercase', opacity: 0.7 }}>internal</span>
    </div>
  );
}

// EntryPointNode — icon-only, transparent, name below
function EntryPointNode({ id, data, selected }: { id: string; data: EntryPointData & { _scanning?: boolean }; selected?: boolean }) {
  const slugMissing = !data.slug;
  const accent = slugMissing ? '#f59e0b' : C.cyan;
  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam' };
  const msIcon = EP_MS_ICON[data.epType] ?? 'bolt';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteNodeRef.current(id); }}
          style={{
            position: 'absolute', top: -8, right: -8,
            width: 18, height: 18, borderRadius: '50%',
            background: '#f87171', border: '2px solid #051424',
            color: '#fff', fontSize: 10, fontWeight: 700,
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            lineHeight: 1, padding: 0, zIndex: 10,
          }}
          title="Delete node (or press Delete key)"
        >✕</button>
      )}
      <div style={{
        width: 56, height: 56, borderRadius: '50%',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: selected ? `rgba(0,240,255,0.10)` : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
        border: selected ? `2px solid ${accent}` : '2px solid transparent',
        boxShadow: selected ? `0 0 14px rgba(0,240,255,0.35), inset 0 0 8px rgba(0,240,255,0.08)` : data._scanning ? '0 0 20px rgba(0,240,255,0.5)' : 'none',
        transition: 'all 0.18s ease',
      }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>{msIcon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center' }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, transition: 'color 0.18s' }}>
          {data.label || (data.epType === 'sse' ? 'SSE' : 'WebSocket')}
        </div>
        {data.slug ? (
          <div style={{ fontSize: 10, color: C.cyan, fontFamily: 'JetBrains Mono, monospace', opacity: 0.8, marginTop: 1 }}>{data.slug}</div>
        ) : (
          <div style={{ fontSize: 10, color: '#f59e0b', fontWeight: 600, marginTop: 1 }}>⚠ slug required</div>
        )}
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

const INTERNAL_ORCHESTRATOR_NAMES = new Set(['workflow_advisor']);

// OrchestratorNode — icon-only, transparent, name below
function OrchestratorNode({ id, data, selected }: { id: string; data: OrchestratorData & { _scanning?: boolean }; selected?: boolean }) {
  const isInternal = INTERNAL_ORCHESTRATOR_NAMES.has(data.name);
  const accent = isInternal ? '#a0f0d0' : C.purple;
  const selGlow = isInternal ? 'rgba(160,240,208,0.35)' : 'rgba(208,188,255,0.35)';
  const selBg   = isInternal ? 'rgba(160,240,208,0.10)' : 'rgba(208,188,255,0.10)';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteNodeRef.current(id); }}
          style={{
            position: 'absolute', top: -8, right: -8,
            width: 18, height: 18, borderRadius: '50%',
            background: '#f87171', border: '2px solid #051424',
            color: '#fff', fontSize: 10, fontWeight: 700,
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            lineHeight: 1, padding: 0, zIndex: 10,
          }}
          title="Delete node (or press Delete key)"
        >✕</button>
      )}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div style={{
        width: 56, height: 56, borderRadius: '50%',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: selected ? selBg : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
        border: selected ? `2px solid ${accent}` : '2px solid transparent',
        boxShadow: selected ? `0 0 14px ${selGlow}, inset 0 0 8px ${selGlow}` : data._scanning ? '0 0 20px rgba(0,240,255,0.5)' : 'none',
        transition: 'all 0.18s ease',
      }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>hub</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 120 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', transition: 'color 0.18s' }}>
          {data.displayName}
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

// AgentNode — icon-only, transparent, name below; uses actual agent icon field first
function AgentNode({ id, data, selected }: { id: string; data: AgentData & { _scanning?: boolean }; selected?: boolean }) {
  const isInternal = data.tags?.includes('internal') ?? false;
  const accent = isInternal ? '#a0f0d0' : C.green;
  const selGlow = isInternal ? 'rgba(160,240,208,0.35)' : 'rgba(74,222,128,0.35)';
  const selBg   = isInternal ? 'rgba(160,240,208,0.10)' : 'rgba(74,222,128,0.10)';
  const icon = data.icon || agentIconForLibrary({ slug: data.name, icon: data.icon } as any);
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteNodeRef.current(id); }}
          style={{
            position: 'absolute', top: -8, right: -8,
            width: 18, height: 18, borderRadius: '50%',
            background: '#f87171', border: '2px solid #051424',
            color: '#fff', fontSize: 10, fontWeight: 700,
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            lineHeight: 1, padding: 0, zIndex: 10,
          }}
          title="Delete node (or press Delete key)"
        >✕</button>
      )}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div style={{
        width: 56, height: 56, borderRadius: '50%',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: selected ? selBg : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
        border: selected ? `2px solid ${accent}` : '2px solid transparent',
        boxShadow: selected ? `0 0 14px ${selGlow}, inset 0 0 8px ${selGlow}` : data._scanning ? '0 0 20px rgba(0,240,255,0.5)' : 'none',
        transition: 'all 0.18s ease',
      }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>{icon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 110 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', transition: 'color 0.18s' }}>
          {data.displayName}
        </div>
      </div>
    </div>
  );
}

// MiddlewareNode — amber-colored, shield for guard / bolt for cache
function MiddlewareNode({ id, data, selected }: { id: string; data: MiddlewareData & { _scanning?: boolean }; selected?: boolean }) {
  const accent = C.amber;
  const selGlow = 'rgba(245,158,11,0.35)';
  const selBg   = 'rgba(245,158,11,0.10)';
  const icon = data.kind === 'guard' ? 'shield' : 'bolt';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteNodeRef.current(id); }}
          style={{
            position: 'absolute', top: -8, right: -8,
            width: 18, height: 18, borderRadius: '50%',
            background: '#f87171', border: '2px solid #051424',
            color: '#fff', fontSize: 10, fontWeight: 700,
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            lineHeight: 1, padding: 0, zIndex: 10,
          }}
          title="Delete node (or press Delete key)"
        >✕</button>
      )}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div style={{
        width: 56, height: 56, borderRadius: '50%',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: selected ? selBg : data._scanning ? 'rgba(245,158,11,0.08)' : 'transparent',
        border: selected ? `2px solid ${accent}` : '2px solid transparent',
        boxShadow: selected ? `0 0 14px ${selGlow}, inset 0 0 8px ${selGlow}` : data._scanning ? C.amberGlow : 'none',
        transition: 'all 0.18s ease',
      }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>{icon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 110 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', transition: 'color 0.18s' }}>
          {data.displayName}
        </div>
        <div style={{ fontSize: 9, color: accent, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.8, opacity: 0.8 }}>
          {data.kind}
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

const NODE_TYPES: NodeTypes = {
  entryPoint: EntryPointNode as any,
  orchestrator: OrchestratorNode as any,
  agent: AgentNode as any,
  middleware: MiddlewareNode as any,
};

// ── Custom animated edge ──────────────────────────────────────────────────────
const EDGE_STYLE = {
  stroke: C.cyan,
  strokeWidth: 1.5,
  strokeDasharray: '5,3',
};

// ── Helpers ──────────────────────────────────────────────────────────────────
function makeId() { return `node_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`; }

// ── Dagre auto-layout ─────────────────────────────────────────────────────────
const NODE_WIDTH  = 240;
const NODE_HEIGHT = 80;

function applyDagreLayout(nodes: Node[], edges: Edge[]): Node[] {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: 'TB', nodesep: 60, ranksep: 100, marginx: 60, marginy: 60 });

  nodes.forEach(n => g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHT }));
  edges.forEach(e => g.setEdge(e.source, e.target));

  dagre.layout(g);

  return nodes.map(n => {
    const pos = g.node(n.id);
    return { ...n, position: { x: pos.x - NODE_WIDTH / 2, y: pos.y - NODE_HEIGHT / 2 } };
  });
}

function buildNodesFromApp(
  app: Application,
  agents: Agent[],
): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  // Build a lookup from app_orchestrator id → AppOrchestratorOut
  const aoById = new Map<string, AppOrchestratorOut>();
  (app.app_orchestrators ?? []).forEach(ao => aoById.set(ao.id, ao));
  // Also pick up inline app_orchestrator objects from entry_points
  app.entry_points.forEach(ep => {
    if (ep.app_orchestrator) aoById.set(ep.app_orchestrator.id, ep.app_orchestrator);
  });

  // Track which app_orchestrator node ids have already been emitted
  const emittedOrchIds = new Set<string>();
  // Track which agent node ids have been emitted (agent_{agentId}_{aoId})
  const emittedAgentNodeIds = new Set<string>();

  // One EP node per entry-point row
  app.entry_points.forEach((ep, idx) => {
    const epId = `ep_${idx}`;
    nodes.push({
      id: epId, type: 'entryPoint',
      position: { x: 150 + idx * 240, y: 60 },
      data: {
        label: app.name,
        epType: (ep.entry_point_type as EntryPointType) ?? 'websocket',
        accessMode: ((ep.access_policy as any)?.mode ?? 'token') as 'token' | 'public',
        slug: ep.slug,
        appName: app.name,
        convTokenLimit: ep.conversation_token_limit != null ? String(ep.conversation_token_limit) : '',
        _epId: ep.id,
      } satisfies EntryPointData,
    });

    const aoId = ep.app_orchestrator_id ?? ep.app_orchestrator?.id;
    if (aoId) {
      const orchNodeId = `orch_${aoId}`;
      edges.push({ id: `e_ep_orch_${idx}`, source: epId, target: orchNodeId, animated: true, style: EDGE_STYLE });

      if (!emittedOrchIds.has(aoId)) {
        emittedOrchIds.add(aoId);
        const ao = aoById.get(aoId);
        if (ao) {
          nodes.push({
            id: orchNodeId, type: 'orchestrator',
            position: { x: 250, y: 220 },
            data: {
              appOrchestratorId: ao.id,
              orchestratorId: ao.id,
              name: ao.name,
              displayName: ao.display_name || ao.name,
              model: ao.llm_model,
              maxParallelTools: ao.max_parallel_tools,
              systemPrompt: ao.system_prompt,
              allowedAgentIds: ao.allowed_agent_ids,
              llmProvider: ao.llm_provider,
              llmModel: ao.llm_model,
              maxIterations: ao.max_iterations,
              historyWindow: ao.history_window ?? 20,
              delegatable: ao.delegatable,
              kind: ao.kind,
              budgetTokens: ao.budget_tokens,
            } as OrchestratorData,
          });

          // Emit agent nodes + orch→agent edges
          const allowedAgents = agents.filter(a => ao.allowed_agent_ids.includes(a.id));
          const spread = Math.max(allowedAgents.length * 180, 400);
          const startX = 300 - spread / 2 + 90;
          allowedAgents.forEach((agent, i) => {
            const agentNodeId = `agent_${agent.id}_${aoId}`;
            if (!emittedAgentNodeIds.has(agentNodeId)) {
              emittedAgentNodeIds.add(agentNodeId);
              nodes.push({
                id: agentNodeId, type: 'agent',
                position: { x: startX + i * 190, y: 420 },
                data: {
                  agentId: agent.id,
                  name: agent.slug,
                  displayName: agent.display_name,
                  description: agent.description,
                  transport: agent.transport,
                  endpointUrl: agent.endpoint_url,
                  tags: agent.tags ?? [],
                  icon: agent.icon || agentIconForLibrary(agent),
                } satisfies AgentData,
              });
            }
            edges.push({ id: `e_orch_agent_${aoId}_${i}`, source: orchNodeId, target: agentNodeId, animated: true, style: EDGE_STYLE });
          });
        }
      }
    }
  });

  const laid = applyDagreLayout(nodes, edges);
  return { nodes: laid, edges };
}

// ── Helpers ───────────────────────────────────────────────────────────────────
function agentIconForLibrary(a: Agent): string {
  return a.icon || 'smart_toy';
}

const EP_META: Record<string, { emoji: string; title: string; desc: string; color?: string }> = {
  websocket: { emoji: '⚡', title: 'WebSocket', desc: 'Full-duplex, persistent connection. Client and server can send messages at any time. Best for chat, real-time collaboration, and interactive agents.' },
  sse:       { emoji: '📡', title: 'Server-Sent Events', desc: 'One-way server→client stream over HTTP. Lightweight, works through proxies. Best for dashboards, notifications, and read-only agent output.' },
  webrtc:    { emoji: '🎙️', title: 'WebRTC Voice', desc: 'Real-time voice via LiveKit WebRTC. Low-latency bidirectional audio with automatic voice activity detection. Best for voice assistants and spoken-word agents.', color: '#a78bfa' },
};

function trunc(s: string | null | undefined, n = 120) {
  if (!s) return '—';
  return s.length > n ? s.slice(0, n) + '…' : s;
}

// ── Node Library panel ────────────────────────────────────────────────────────
function NodeLibrary({ orchestrators, agents, middlewareDefs, width, onWidthChange }: {
  orchestrators: OrchestratorFull[];
  agents: Agent[];
  middlewareDefs: MiddlewareDef[];
  width: number;
  onWidthChange: (w: number) => void;
}) {
  const [openEP, setOpenEP] = useState(true);
  const [openOrch, setOpenOrch] = useState(true);
  const [openAgents, setOpenAgents] = useState(true);
  const [openMW, setOpenMW] = useState(true);
  const dragging = useRef(false);
  const startX = useRef(0);
  const startW = useRef(0);

  function onResizeMouseDown(e: React.MouseEvent) {
    e.preventDefault();
    dragging.current = true;
    startX.current = e.clientX;
    startW.current = width;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    function onMove(ev: MouseEvent) {
      if (!dragging.current) return;
      const delta = ev.clientX - startX.current;
      const next = Math.min(480, Math.max(200, startW.current + delta));
      onWidthChange(next);
    }
    function onUp() {
      dragging.current = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  function dragItem(e: DragEvent, nodeType: string, nodeData: object) {
    e.dataTransfer.setData('nodeType', nodeType);
    e.dataTransfer.setData('nodeData', JSON.stringify(nodeData));
    e.dataTransfer.effectAllowed = 'move';
  }

  const itemStyle: React.CSSProperties = {
    display: 'flex', alignItems: 'center', gap: 10, padding: '9px 12px',
    borderRadius: 8, cursor: 'grab', userSelect: 'none',
    border: `1px solid transparent`, transition: 'all 0.15s', marginBottom: 4,
  };

  function SectionHeader({ label, open, onToggle }: { label: string; open: boolean; onToggle: () => void }) {
    return (
      <button onClick={onToggle} style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        width: '100%', padding: '6px 0', background: 'none', border: 'none', cursor: 'pointer',
        fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1.5, textTransform: 'uppercase',
        marginBottom: open ? 8 : 0,
      }}>
        {label}
        <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, transition: 'transform 0.15s', transform: open ? 'rotate(180deg)' : 'none' }}>expand_more</span>
      </button>
    );
  }

  return (
    <div style={{ display: 'flex', height: '100%', flexShrink: 0 }}>
      {/* Panel body */}
      <div style={{
        width, height: '100%', overflowY: 'auto',
        ...glass, borderRight: 'none', padding: '16px 14px',
        display: 'flex', flexDirection: 'column', gap: 20,
      }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', paddingBottom: 8, borderBottom: `1px solid ${C.outlineVariant}` }}>
          Node Library
        </div>

        {/* Entry Points */}
        <div>
          <SectionHeader label="Entry Points" open={openEP} onToggle={() => setOpenEP(v => !v)} />
          {openEP && (
            <div className="nl-section-list">
              {(['websocket', 'sse', 'webrtc'] as const).map(ep => {
                const meta = EP_META[ep];
                return (
                  <div key={ep} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                    <div
                      draggable
                      onDragStart={e => dragItem(e, 'entryPoint', { epType: ep, label: meta.title, accessMode: 'token', slug: '' })}
                      style={{ ...itemStyle, background: C.cyanBg, borderColor: C.cyanBorder, marginBottom: 0 }}
                      onMouseEnter={e => (e.currentTarget.style.background = 'rgba(0,240,255,0.1)')}
                      onMouseLeave={e => (e.currentTarget.style.background = C.cyanBg)}
                    >
                      <span style={{ fontSize: 20, lineHeight: 1, flexShrink: 0 }}>{meta.emoji}</span>
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 13, fontWeight: 600, color: C.text }}>{meta.title}</div>
                        <div style={{ fontSize: 10, color: C.textMuted }}>Entry point</div>
                      </div>
                      <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', opacity: 0.5 }}>drag_indicator</span>
                    </div>
                    <div className="nl-tip">
                      <div style={{ fontSize: 11, fontWeight: 700, color: C.cyan, marginBottom: 4 }}>{meta.title}</div>
                      <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>{meta.desc}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Orchestrators */}
        <div>
          <SectionHeader label="Orchestrators" open={openOrch} onToggle={() => setOpenOrch(v => !v)} />
          {openOrch && (
            <div className="nl-section-list">
              {orchestrators.filter(o => o.enabled && !INTERNAL_ORCHESTRATOR_NAMES.has(o.name)).map(o => (
                <div key={o.id} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                  <div
                    draggable
                    onDragStart={e => dragItem(e, 'orchestrator', {
                      orchestratorId: o.id, appOrchestratorId: null, name: o.name, displayName: o.display_name,
                      model: o.llm_model, maxParallelTools: o.max_parallel_tools,
                      systemPrompt: o.system_prompt, allowedAgentIds: o.allowed_agent_ids ?? [],
                      llmProvider: o.llm_provider, llmModel: o.llm_model, maxIterations: o.max_iterations,
                      historyWindow: o.history_window ?? 20, delegatable: o.delegatable ?? false,
                      kind: 'standard', budgetTokens: null,
                    })}
                    style={{ ...itemStyle, background: C.purpleBg, borderColor: 'rgba(208,188,255,0.2)', marginBottom: 0 }}
                    onMouseEnter={e => (e.currentTarget.style.background = 'rgba(87,27,193,0.2)')}
                    onMouseLeave={e => (e.currentTarget.style.background = C.purpleBg)}
                  >
                    <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.purple, flexShrink: 0 }}>hub</span>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontSize: 13, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{o.display_name}</div>
                      <div style={{ fontSize: 10, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {o.llm_model ?? o.name}
                      </div>
                    </div>
                    <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
                  </div>
                  <div className="nl-tip">
                    <div style={{ fontSize: 11, fontWeight: 700, color: C.purple, marginBottom: 4 }}>{o.display_name}</div>
                    {o.llm_model && <div style={{ fontSize: 10, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', marginBottom: 6 }}>{o.llm_model}</div>}
                    <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>{trunc(o.system_prompt ?? o.name)}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Agents */}
        <div>
          <SectionHeader label="Agents" open={openAgents} onToggle={() => setOpenAgents(v => !v)} />
          {openAgents && (
            <div className="nl-section-list">
              {agents.filter(a => a.enabled && !a.tags?.includes('internal')).map(a => {
                const icon = a.icon || agentIconForLibrary(a);
                return (
                  <div key={a.id} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                    <div
                      draggable
                      onDragStart={e => dragItem(e, 'agent', { agentId: a.id, name: a.slug, displayName: a.display_name, description: a.description, transport: a.transport, endpointUrl: a.endpoint_url, icon: a.icon || agentIconForLibrary(a) })}
                      style={{ ...itemStyle, background: C.greenBg, borderColor: C.greenBorder, marginBottom: 0 }}
                      onMouseEnter={e => (e.currentTarget.style.background = 'rgba(74,222,128,0.1)')}
                      onMouseLeave={e => (e.currentTarget.style.background = C.greenBg)}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.green, flexShrink: 0 }}>{icon}</span>
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 13, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.display_name}</div>
                        <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.transport}</div>
                      </div>
                      <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
                    </div>
                    <div className="nl-tip">
                      <div style={{ fontSize: 11, fontWeight: 700, color: C.green, marginBottom: 4 }}>{a.display_name}</div>
                      <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 6 }}>{a.transport} · {a.slug}</div>
                      <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>{trunc(a.description)}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Middleware */}
        {middlewareDefs.length > 0 && (
          <div>
            <SectionHeader label="Middleware" open={openMW} onToggle={() => setOpenMW(v => !v)} />
            {openMW && (
              <div className="nl-section-list">
                {middlewareDefs.filter(m => m.enabled).map(m => {
                  const icon = m.kind === 'guard' ? 'shield' : 'bolt';
                  return (
                    <div key={m.id} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                      <div
                        draggable
                        onDragStart={e => dragItem(e, 'middleware', {
                          defId: m.id, slug: m.slug, kind: m.kind,
                          displayName: m.display_name, description: m.description,
                          config: m.config, configOverride: {}, nodeId: '',
                        } satisfies MiddlewareData)}
                        style={{ ...itemStyle, background: C.amberBg, borderColor: C.amberBorder, marginBottom: 0 }}
                        onMouseEnter={e => (e.currentTarget.style.background = 'rgba(245,158,11,0.1)')}
                        onMouseLeave={e => (e.currentTarget.style.background = C.amberBg)}
                      >
                        <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.amber, flexShrink: 0 }}>{icon}</span>
                        <div style={{ minWidth: 0 }}>
                          <div style={{ fontSize: 13, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.display_name}</div>
                          <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.kind}</div>
                        </div>
                        <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
                      </div>
                      <div className="nl-tip">
                        <div style={{ fontSize: 11, fontWeight: 700, color: C.amber, marginBottom: 4 }}>{m.display_name}</div>
                        <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 6 }}>{m.kind} · {m.slug}</div>
                        <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>{trunc(m.description)}</div>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Resize handle */}
      <div
        className="nl-resize-handle"
        onMouseDown={onResizeMouseDown}
        style={{ borderRight: `1px solid ${C.glassBorder}` }}
      />
    </div>
  );
}

// ── Properties Panel ──────────────────────────────────────────────────────────
function PropertiesPanel({
  selectedNode,
  onUpdateNode,
  slugLocked,
  onSlugManualEdit,
  appName,
  onAppNameChange,
  convTokenLimit,
  onConvTokenLimitChange,
  chain,
  app,
  epCount,
  nodes,
  edges,
}: {
  selectedNode: Node | null;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
  slugLocked: boolean;
  onSlugManualEdit: () => void;
  appName: string;
  onAppNameChange: (name: string) => void;
  convTokenLimit: string;
  onConvTokenLimitChange: (val: string) => void;
  chain: ChainStatus;
  app: Application | null;
  epCount: number;
  nodes: Node[];
  edges: Edge[];
}) {
  const [propTab, setPropTab] = useState<'properties' | 'configuration'>('properties');

  function TabBtn({ id, label }: { id: 'properties' | 'configuration'; label: string }) {
    const active = propTab === id;
    return (
      <button onClick={() => setPropTab(id)} style={{
        padding: '6px 14px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 600,
        background: active ? 'rgba(0,240,255,0.15)' : 'transparent',
        color: active ? C.cyan : C.textMuted,
        transition: 'all 0.15s',
      }}>{label}</button>
    );
  }

  const labelStyle: React.CSSProperties = { fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' };
  const inputStyle: React.CSSProperties = {
    width: '100%', padding: '7px 10px', borderRadius: 6,
    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
    color: 'var(--tm-card-text)', fontSize: 13, boxSizing: 'border-box', outline: 'none',
  };
  const readOnlyStyle: React.CSSProperties = { ...inputStyle, color: 'var(--tm-card-text-hint)', background: 'rgba(10,18,32,0.6)', cursor: 'default' };
  const fieldWrap: React.CSSProperties = { marginBottom: 14 };

  return (
    <div style={{
      width: 320, flexShrink: 0, height: '100%', overflowY: 'auto',
      ...glass, borderLeft: `1px solid ${C.glassBorder}`, padding: '16px 14px',
      display: 'flex', flexDirection: 'column',
    }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', paddingBottom: 8, borderBottom: `1px solid ${C.outlineVariant}`, marginBottom: 16 }}>
        {selectedNode ? 'Node Properties' : 'Application'}
      </div>

      {!selectedNode ? (
        /* ── App-level properties (shown when canvas background is clicked) ── */
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {/* App header */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: 'rgba(0,209,255,0.06)', border: '1px solid rgba(0,209,255,0.18)' }}>
            <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.cyan }}>deployed_code</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {appName || 'Untitled Application'}
              </div>
              <div style={{ fontSize: 10, color: C.textMuted }}>
                {app ? `ID: ${app.id.slice(0, 8)}…` : 'Not yet saved'}
              </div>
            </div>
          </div>

          {epCount <= 1 ? (
            <>
              {/* Name field — single EP only; multi-EP uses per-node appName */}
              <div style={{ marginBottom: 14 }}>
                <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' }}>Application Name</label>
                <input
                  style={{
                    width: '100%', padding: '7px 10px', borderRadius: 6,
                    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
                    color: 'var(--tm-card-text)', fontSize: 13, boxSizing: 'border-box', outline: 'none',
                  }}
                  value={appName}
                  onChange={e => onAppNameChange(e.target.value)}
                  placeholder="My Application"
                />
              </div>
              <div style={{ marginBottom: 14 }}>
                <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' }}>
                  Conversation Token Limit
                  <span style={{ marginLeft: 6, fontSize: 10, color: '#64748b' }}>per session · blank = unlimited</span>
                </label>
                <input
                  type="number" min={1}
                  style={{
                    width: '100%', padding: '7px 10px', borderRadius: 6,
                    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
                    color: 'var(--tm-card-text)', fontSize: 13, boxSizing: 'border-box', outline: 'none',
                  }}
                  value={convTokenLimit}
                  onChange={e => onConvTokenLimitChange(e.target.value)}
                  placeholder="e.g. 50000"
                />
              </div>
            </>
          ) : (
            <div style={{ marginBottom: 14, padding: '8px 10px', borderRadius: 6, background: 'rgba(0,240,255,0.05)', border: '1px solid rgba(0,240,255,0.15)', fontSize: 11, color: C.textMuted, lineHeight: 1.5 }}>
              Multiple entry points — select each entry point node to edit its name and token limit individually.
            </div>
          )}

          {/* Chain status */}
          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 6, display: 'block' }}>Canvas Status</label>
            <div style={{
              display: 'flex', alignItems: 'flex-start', gap: 8,
              padding: '8px 10px', borderRadius: 8,
              background: chain.ready ? 'rgba(74,222,128,0.06)' : 'rgba(255,180,171,0.06)',
              border: `1px solid ${chain.ready ? 'rgba(74,222,128,0.2)' : 'rgba(255,180,171,0.2)'}`,
            }}>
              <span style={{
                width: 7, height: 7, borderRadius: '50%', flexShrink: 0, marginTop: 4,
                background: chain.color, boxShadow: chain.ready ? `0 0 6px ${chain.color}` : 'none',
                display: 'inline-block',
              }} />
              <span style={{ fontSize: 12, color: chain.color, lineHeight: 1.5 }}>{chain.label}</span>
            </div>
          </div>

          {/* Stats */}
          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 6, display: 'block' }}>Canvas Info</label>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 6 }}>
              {[
                { label: 'Entry Points', value: String(chain.epNode ? 1 : 0) },
                { label: 'Orchestrator', value: chain.orchNode ? (chain.orchNode.data as OrchestratorData).displayName : '—' },
                { label: 'Agents', value: String(chain.agentCount) },
                { label: 'Status', value: app?.enabled ? 'Deployed' : 'Draft' },
              ].map(({ label, value }) => (
                <div key={label} style={{ padding: '7px 10px', borderRadius: 7, background: C.surfaceLow, border: `1px solid ${C.outlineVariant}` }}>
                  <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 2 }}>{label}</div>
                  <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{value}</div>
                </div>
              ))}
            </div>
          </div>

          {app && (
            <div style={{ marginBottom: 14 }}>
              <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' }}>Created</label>
              <div style={{ fontSize: 12, color: C.textMuted }}>
                {new Date(app.created_at).toLocaleString()}
              </div>
            </div>
          )}

          <div style={{ marginTop: 8, padding: '8px 0', borderTop: `1px solid ${C.outlineVariant}`, fontSize: 11, color: C.textMuted, lineHeight: 1.6 }}>
            Click any node to edit its properties.
          </div>
        </div>
      ) : (
        <>
          <div style={{ display: 'flex', gap: 4, marginBottom: 18 }}>
            <TabBtn id="properties" label="Properties" />
            <TabBtn id="configuration" label="Configuration" />
          </div>

          {/* EntryPoint properties */}
          {selectedNode.type === 'entryPoint' && propTab === 'properties' && (() => {
            const d = selectedNode.data as EntryPointData;
            return (
              <div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>App Name</label>
                  <input style={inputStyle} value={d.appName ?? d.label} onChange={e => onUpdateNode(selectedNode.id, { appName: e.target.value, label: e.target.value })} placeholder="My Application" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Token Limit <span style={{ fontSize: 10, color: '#64748b' }}>per session · blank = unlimited</span></label>
                  <input type="number" min={1} style={inputStyle} value={d.convTokenLimit ?? ''} onChange={e => onUpdateNode(selectedNode.id, { convTokenLimit: e.target.value })} placeholder="e.g. 50000" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Type</label>
                  <select style={{ ...inputStyle }} value={d.epType} onChange={e => onUpdateNode(selectedNode.id, { epType: e.target.value as EntryPointType })}>
                    <option value="websocket">WebSocket</option>
                    <option value="sse">SSE</option>
                    <option value="webrtc">WebRTC Voice</option>
                  </select>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Access Policy</label>
                  <select style={{ ...inputStyle }} value={d.accessMode} onChange={e => onUpdateNode(selectedNode.id, { accessMode: e.target.value as 'token' | 'public' })}>
                    <option value="token">Token required</option>
                    <option value="public">Public (no auth)</option>
                  </select>
                </div>
                <div style={fieldWrap}>
                  <label style={{ ...labelStyle, display: 'flex', alignItems: 'center', gap: 6 }}>
                    Slug
                    {!slugLocked && <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 10, background: 'rgba(0,240,255,0.1)', color: C.cyan, border: '1px solid rgba(0,240,255,0.3)', fontWeight: 600 }}>auto</span>}
                  </label>
                  <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace' }} value={d.slug} onChange={e => { onSlugManualEdit(); onUpdateNode(selectedNode.id, { slug: e.target.value }); }} placeholder="my-app-slug" />
                  {d.slug && (
                    <div style={{
                      fontSize: 11, color: C.textMuted, marginTop: 6, padding: '5px 8px',
                      background: C.surfaceLow, borderRadius: 5, fontFamily: 'JetBrains Mono, monospace',
                      wordBreak: 'break-all', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 6,
                    }}>
                      <span style={{ flex: 1 }}>
                        {d.epType === 'websocket' ? `ws://<host>:8088/apps/${d.slug}/ws`
                          : d.epType === 'webrtc' ? `http://<host>:8088/apps/${d.slug}/voice`
                          : `http://<host>:8088/apps/${d.slug}/sse`}
                      </span>
                      <button
                        onClick={() => navigator.clipboard.writeText(
                          d.epType === 'websocket'
                            ? `ws://localhost:8088/apps/${d.slug}/ws`
                            : d.epType === 'webrtc'
                            ? `http://localhost:8088/apps/${d.slug}/voice`
                            : `http://localhost:8088/apps/${d.slug}/sse`
                        )}
                        title="Copy endpoint URL"
                        style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.cyan, flexShrink: 0, padding: 0 }}
                      >
                        <span className="material-symbols-outlined" style={{ fontSize: 14 }}>content_copy</span>
                      </button>
                    </div>
                  )}
                </div>
              </div>
            );
          })()}

          {/* Orchestrator properties */}
          {selectedNode.type === 'orchestrator' && propTab === 'properties' && (() => {
            const d = selectedNode.data as OrchestratorData;
            // Count outgoing Orch→Agent edges for read-only display
            const connectedAgentCount = chain.epNode
              ? edges.filter(e => e.source === selectedNode.id && nodes.find(n => n.id === e.target && n.type === 'agent')).length
              : 0;
            return (
              <div>
                {/* Header tile */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.purpleBg, border: '1px solid rgba(208,188,255,0.2)' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.purple }}>hub</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName}</div>
                    <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.name}</div>
                  </div>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Display Name</label>
                  <input style={inputStyle} value={d.displayName} onChange={e => onUpdateNode(selectedNode.id, { displayName: e.target.value })} placeholder="Display name" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>LLM Model</label>
                  <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace' }} value={d.llmModel ?? ''} onChange={e => onUpdateNode(selectedNode.id, { llmModel: e.target.value, model: e.target.value })} placeholder="e.g. claude-sonnet-4-5" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>System Prompt</label>
                  <textarea
                    style={{ ...inputStyle, resize: 'vertical', minHeight: 80, fontFamily: 'inherit', fontSize: 12 }}
                    value={d.systemPrompt ?? ''}
                    onChange={e => onUpdateNode(selectedNode.id, { systemPrompt: e.target.value })}
                    placeholder="You are a helpful assistant…"
                  />
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                  <div style={fieldWrap}>
                    <label style={labelStyle}>Max Iterations</label>
                    <input type="number" min={1} max={100} style={inputStyle} value={d.maxIterations} onChange={e => onUpdateNode(selectedNode.id, { maxIterations: parseInt(e.target.value, 10) || 10 })} />
                  </div>
                  <div style={fieldWrap}>
                    <label style={labelStyle}>History Window</label>
                    <input type="number" min={0} max={200} style={inputStyle} value={d.historyWindow} onChange={e => onUpdateNode(selectedNode.id, { historyWindow: parseInt(e.target.value, 10) || 20 })} />
                  </div>
                </div>
                <div style={{ ...fieldWrap, display: 'flex', alignItems: 'center', gap: 8 }}>
                  <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: C.text, cursor: 'pointer' }}>
                    <input
                      type="checkbox"
                      checked={!!d.delegatable}
                      onChange={e => onUpdateNode(selectedNode.id, { delegatable: e.target.checked })}
                      style={{ accentColor: C.purple }}
                    />
                    Delegatable (A2A)
                  </label>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Connected Agents</label>
                  <div style={{ fontSize: 12, color: C.textMuted, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                    {connectedAgentCount} agent{connectedAgentCount !== 1 ? 's' : ''} — connect via canvas
                  </div>
                </div>
              </div>
            );
          })()}

          {/* Agent properties */}
          {selectedNode.type === 'agent' && propTab === 'properties' && (() => {
            const d = selectedNode.data as AgentData;
            const icon = d.icon || agentIconForLibrary({ slug: d.name, icon: d.icon } as any);
            return (
              <div>
                {/* Header tile */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.greenBg, border: `1px solid ${C.greenBorder}` }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.green }}>{icon}</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName}</div>
                    <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.name}</div>
                  </div>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Description</label>
                  <div style={{ fontSize: 12, color: 'var(--tm-card-text-hint)', lineHeight: 1.55, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                    {d.description || <span style={{ opacity: 0.4 }}>No description</span>}
                  </div>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Transport</label>
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '3px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: C.greenBg, color: C.green, border: `1px solid ${C.greenBorder}` }}>
                    <span style={{ width: 5, height: 5, borderRadius: '50%', background: C.green, boxShadow: `0 0 5px ${C.green}` }} />
                    {d.transport}
                  </span>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Endpoint</label>
                  <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', wordBreak: 'break-all', padding: '6px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                    {d.endpointUrl}
                  </div>
                </div>
                <a href="/admin/agents" style={{ fontSize: 12, color: C.green, textDecoration: 'none', display: 'flex', alignItems: 'center', gap: 4, marginTop: 8, opacity: 0.8 }}
                  onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
                  onMouseLeave={e => (e.currentTarget.style.opacity = '0.8')}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
                  Configure in Agents
                </a>
              </div>
            );
          })()}

          {/* Middleware properties */}
          {selectedNode.type === 'middleware' && propTab === 'properties' && (() => {
            const mwNode = selectedNode;
            const d = mwNode.data as MiddlewareData;
            const icon = d.kind === 'guard' ? 'shield' : 'bolt';
            const kindBadge = (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '3px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: C.amberBg, color: C.amber, border: `1px solid ${C.amberBorder}` }}>
                <span style={{ width: 5, height: 5, borderRadius: '50%', background: C.amber, boxShadow: `0 0 5px ${C.amber}` }} />
                {d.kind}
              </span>
            );
            const co = (d.configOverride ?? {}) as Record<string, unknown>;
            function setOverride(patch: Record<string, unknown>) {
              onUpdateNode(mwNode.id, { configOverride: { ...co, ...patch } });
            }
            return (
              <div>
                {/* Header tile */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.amberBg, border: `1px solid ${C.amberBorder}` }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.amber }}>{icon}</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName}</div>
                    <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.slug}</div>
                  </div>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Kind</label>
                  {kindBadge}
                </div>
                {d.description && (
                  <div style={fieldWrap}>
                    <label style={labelStyle}>Description</label>
                    <div style={{ fontSize: 12, color: 'var(--tm-card-text-hint)', lineHeight: 1.55, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                      {d.description}
                    </div>
                  </div>
                )}
                <div style={{ marginTop: 8, marginBottom: 8, paddingTop: 8, borderTop: `1px solid ${C.outlineVariant}`, fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase' }}>
                  Config Override
                </div>
                {d.kind === 'guard' && (
                  <>
                    <div style={fieldWrap}>
                      <label style={labelStyle}>Mode</label>
                      <select
                        style={{ ...inputStyle }}
                        value={(co.mode as string) ?? ''}
                        onChange={e => setOverride({ mode: e.target.value || undefined })}
                      >
                        <option value="">— default —</option>
                        <option value="block">block</option>
                        <option value="redact">redact</option>
                      </select>
                    </div>
                    <div style={{ ...fieldWrap, display: 'flex', flexDirection: 'column', gap: 8 }}>
                      <label style={{ ...labelStyle, marginBottom: 0 }}>Detection</label>
                      <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: C.text, cursor: 'pointer' }}>
                        <input
                          type="checkbox"
                          checked={co.pii_detection !== false}
                          onChange={e => setOverride({ pii_detection: e.target.checked })}
                          style={{ accentColor: C.amber }}
                        />
                        PII detection
                      </label>
                      <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: C.text, cursor: 'pointer' }}>
                        <input
                          type="checkbox"
                          checked={co.injection_detection !== false}
                          onChange={e => setOverride({ injection_detection: e.target.checked })}
                          style={{ accentColor: C.amber }}
                        />
                        Injection detection
                      </label>
                    </div>
                  </>
                )}
                {d.kind === 'cache' && (
                  <>
                    <div style={fieldWrap}>
                      <label style={labelStyle}>TTL (seconds)</label>
                      <input
                        type="number"
                        min={1}
                        style={inputStyle}
                        value={(co.ttl_seconds as number | undefined) ?? ''}
                        placeholder="e.g. 300"
                        onChange={e => setOverride({ ttl_seconds: e.target.value ? parseInt(e.target.value, 10) : undefined })}
                      />
                    </div>
                    <div style={fieldWrap}>
                      <label style={labelStyle}>Scope</label>
                      <select
                        style={{ ...inputStyle }}
                        value={(co.scope as string) ?? ''}
                        onChange={e => setOverride({ scope: e.target.value || undefined })}
                      >
                        <option value="">— default —</option>
                        <option value="global">global</option>
                        <option value="app">app</option>
                        <option value="session">session</option>
                        <option value="user">user</option>
                      </select>
                    </div>
                    <div style={fieldWrap}>
                      <label style={labelStyle}>Max result chars</label>
                      <input
                        type="number"
                        min={1}
                        style={inputStyle}
                        value={(co.max_result_chars as number | undefined) ?? ''}
                        placeholder="e.g. 8000"
                        onChange={e => setOverride({ max_result_chars: e.target.value ? parseInt(e.target.value, 10) : undefined })}
                      />
                    </div>
                  </>
                )}
              </div>
            );
          })()}

          {propTab === 'configuration' && (
            <div style={{ color: C.textMuted, fontSize: 13, padding: 10 }}>
              Configuration options for this node type are managed at the resource level.<br /><br />
              <span style={{ fontSize: 11, opacity: 0.7 }}>Use the Properties tab or navigate to the resource admin page.</span>
            </div>
          )}
        </>
      )}
    </div>
  );
}

// ── Canvas Logo ───────────────────────────────────────────────────────────────
// Extensible state-driven logo. Add new states by adding one entry to LOGO_STATES
// and (optionally) a @keyframes block in LOGO_KEYFRAMES.
export type LogoState = 'idle' | 'dirty' | 'error' | 'success' | 'thinking' | 'warning';

interface LogoStateDef {
  opacity: number;
  filter: string;
  animation: string;
}

const LOGO_STATES: Record<LogoState, LogoStateDef> = {
  idle:     { opacity: 0.015, filter: 'none',   animation: 'none' },
  dirty:    { opacity: 0.015, filter: 'none',   animation: 'none' },
  warning:  { opacity: 0.45, filter: 'drop-shadow(0 0 18px rgba(255,120,120,0.4))',    animation: 'logo-warn-flash 1.2s ease-in-out 1 forwards' },
  error:    { opacity: 0.35, filter: 'drop-shadow(0 0 18px rgba(255,107,138,0.4))',   animation: 'logo-shake 0.5s ease-in-out' },
  success:  { opacity: 1.0,  filter: 'drop-shadow(0 0 40px rgba(74,222,128,0.9))',    animation: 'logo-burst 1.8s ease-out forwards' },
  thinking: { opacity: 1.0,  filter: 'none',                                           animation: 'none' },
};

const LOGO_KEYFRAMES = `
@keyframes logo-breathe {
  0%   { opacity: 0.012; }
  50%  { opacity: 0.028; }
  100% { opacity: 0.012; }
}
@keyframes logo-breathe-v3 {
  0%   { opacity: 0.007; }
  50%  { opacity: 0.015; }
  100% { opacity: 0.007; }
}
@keyframes logo-sway {
  0%, 100% { transform: rotate3d(0,1,0,0deg); }
  25%       { transform: rotate3d(0,1,0,6deg); }
  75%       { transform: rotate3d(0,1,0,-6deg); }
}
@keyframes logo-shake {
  0%,100% { transform: translateX(0); }
  15%     { transform: translateX(-10px) rotate(-2deg); }
  30%     { transform: translateX(10px)  rotate(2deg); }
  45%     { transform: translateX(-8px)  rotate(-1deg); }
  60%     { transform: translateX(8px)   rotate(1deg); }
  75%     { transform: translateX(-4px); }
  90%     { transform: translateX(4px); }
}
@keyframes logo-burst {
  0%   { opacity: 0.13; filter: drop-shadow(0 0 18px rgba(0,240,255,0.18)); }
  15%  { opacity: 1;    filter: drop-shadow(0 0 80px rgba(74,222,128,1)) drop-shadow(0 0 40px rgba(255,255,255,0.8)); }
  100% { opacity: 0.13; filter: drop-shadow(0 0 18px rgba(0,240,255,0.18)); }
}
@keyframes logo-explode {
  0%   { transform: translate(0,0) scale(1) rotate(0deg);                                              opacity: 1; }
  20%  { transform: translate(calc(var(--ex)*60px), calc(var(--ey)*60px)) scale(1.15) rotate(var(--rot)); opacity: 1; }
  55%  { transform: translate(calc(var(--ex)*140px), calc(var(--ey)*140px)) scale(0.7) rotate(calc(var(--rot)*2)); opacity: 0.6; }
  80%  { transform: translate(calc(var(--ex)*180px), calc(var(--ey)*180px)) scale(0.3) rotate(calc(var(--rot)*3)); opacity: 0.0; }
  81%  { transform: translate(0,0) scale(0) rotate(0deg);                                              opacity: 0; }
  100% { transform: translate(0,0) scale(1) rotate(0deg);                                              opacity: 1; }
}
@keyframes logo-flip {
  0%   { transform: perspective(600px) rotateY(0deg); }
  100% { transform: perspective(600px) rotateY(360deg); }
}
@keyframes logo-polygon-flicker {
  0%,100% { opacity: 0.08; fill: #4ab8a0; }
  50%     { opacity: 0.55; fill: #00b8c8; filter: drop-shadow(0 0 6px rgba(0,180,200,0.6)); }
}
@keyframes logo-warn-flash {
  0%   { opacity: 0.18; filter: drop-shadow(0 0 12px rgba(255,120,120,0.15)); }
  40%  { opacity: 0.48; filter: drop-shadow(0 0 22px rgba(255,120,120,0.5)); }
  100% { opacity: 0.18; filter: drop-shadow(0 0 12px rgba(255,120,120,0.15)); }
}
`;

// 14 polygons from the_m_smiling_14_polygons.svg — explode vectors computed from centroid vs center (703,559)
// 14 polygons from the_m_smiling_14_polygons.svg — center ~(703,559), explode vectors from centroid
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

// Stable per-polygon random delays for the thinking flicker — generated once at module load
const THINK_DELAYS = LOGO_PATHS.map((_, i) => {
  // cheap deterministic pseudo-random from index
  const r = ((i * 2654435761) >>> 0) / 0xffffffff;
  return +(r * 2.4).toFixed(2); // 0–2.4s spread
});
const THINK_DURATIONS = LOGO_PATHS.map((_, i) => {
  const r = (((i + 7) * 2246822519) >>> 0) / 0xffffffff;
  return +(0.9 + r * 1.4).toFixed(2); // 0.9–2.3s per polygon
});

function CanvasLogo({ state }: { state: LogoState }) {
  const def = LOGO_STATES[state];
  const key = (state === 'idle' || state === 'dirty') ? 'calm' : state;
  const isExplode = state === 'success';
  const isThinking = state === 'thinking';

  return (
    <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none', zIndex: 0 }}>
      <style>{LOGO_KEYFRAMES}</style>
      <svg
        key={key}
        xmlns="http://www.w3.org/2000/svg"
        width={720} height={572}
        viewBox="0 0 1407 1118"
        overflow="visible"
        style={{ opacity: def.opacity, animation: def.animation, filter: def.filter, overflow: 'visible' }}
      >
        {LOGO_PATHS.map(({ id, points, ex, ey }, i) => (
          <polygon
            key={id}
            points={points}
            style={isExplode ? {
              // @ts-ignore
              '--ex': ex,
              '--ey': ey,
              '--rot': `${(ex + ey) * 45}deg`,
              fill: LOGO_COLOR,
              animation: 'logo-explode 1.8s cubic-bezier(0.25,0.46,0.45,0.94) forwards',
              animationDelay: `${i * 0.06}s`,
              transformOrigin: 'center',
              transformBox: 'fill-box',
            } as React.CSSProperties : isThinking ? {
              animation: `logo-polygon-flicker ${THINK_DURATIONS[i]}s ease-in-out ${THINK_DELAYS[i]}s infinite`,
            } as React.CSSProperties : { fill: state === 'warning' ? '#ff8080' : LOGO_COLOR }}
          />
        ))}
      </svg>
    </div>
  );
}

// ── Advisor panel ─────────────────────────────────────────────────────────────
const FIELD_LABEL: Record<string, string> = {
  system_prompt: 'System prompt', description: 'Description',
  display_name: 'Display name', max_iterations: 'Max iterations',
  history_window: 'History window', max_parallel_tools: 'Max parallel tools',
};
const FIELD_ICON: Record<string, string> = {
  system_prompt: 'edit_note', description: 'description', display_name: 'label',
  max_iterations: 'repeat', history_window: 'history', max_parallel_tools: 'fork_right',
};

function ProposalCard({ proposal, msgIndex, onApply }: {
  proposal: Proposal; msgIndex: number; onApply: (msgIndex: number, p: Proposal) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const st = proposal.status;
  const isText = typeof proposal.suggested === 'string' && (proposal.suggested as string).length > 60;

  const btnBg = st === 'applied' ? 'rgba(16,185,129,0.2)'
    : st === 'failed' ? 'rgba(239,68,68,0.2)'
    : st === 'stale' ? 'rgba(251,191,36,0.15)'
    : 'rgba(0,240,255,0.12)';
  const btnColor = st === 'applied' ? '#34d399'
    : st === 'failed' ? '#f87171'
    : st === 'stale' ? '#fbbf24'
    : C.cyan;
  const btnLabel = st === 'applying' ? '…' : st === 'applied' ? 'Applied ✓' : st === 'failed' ? 'Retry' : st === 'stale' ? 'Apply anyway' : 'Apply';

  return (
    <div style={{
      marginTop: 8, borderRadius: 8, border: `1px solid rgba(0,240,255,0.18)`,
      background: 'rgba(0,240,255,0.04)', overflow: 'hidden',
    }}>
      {/* Card header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 10px' }}>
        <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.cyan, flexShrink: 0 }}>
          {FIELD_ICON[proposal.field] ?? 'tune'}
        </span>
        <span style={{ fontSize: 11, fontWeight: 700, color: C.cyan, flex: 1 }}>
          {FIELD_LABEL[proposal.field] ?? proposal.field}
          <span style={{ fontWeight: 400, color: C.textMuted }}> · {proposal.targetName}</span>
        </span>
        <button
          onClick={() => setExpanded(e => !e)}
          style={{ border: 'none', background: 'transparent', cursor: 'pointer', color: C.textMuted, padding: 0, lineHeight: 1 }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>{expanded ? 'expand_less' : 'expand_more'}</span>
        </button>
      </div>

      {/* Reason */}
      <div style={{ padding: '0 10px 7px', fontSize: 11, color: 'var(--tm-card-text-subtle)', lineHeight: 1.5 }}>{proposal.reason}</div>

      {/* Diff preview (expandable) */}
      {expanded && (
        <div style={{ borderTop: `1px solid rgba(0,240,255,0.1)`, padding: '8px 10px', display: 'flex', flexDirection: 'column', gap: 6 }}>
          <div>
            <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 2 }}>Current</div>
            <div style={{
              fontSize: 11, color: 'var(--tm-card-text-subtle)', background: 'rgba(255,255,255,0.03)', borderRadius: 4,
              padding: '5px 7px', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              maxHeight: isText ? 80 : 'none', overflowY: isText ? 'auto' : 'visible',
            }}>{String(proposal.current) || '(empty)'}</div>
          </div>
          <div>
            <div style={{ fontSize: 10, color: '#34d399', marginBottom: 2 }}>Suggested</div>
            <div style={{
              fontSize: 11, color: '#d1fae5', background: 'rgba(16,185,129,0.06)', borderRadius: 4,
              padding: '5px 7px', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              maxHeight: isText ? 120 : 'none', overflowY: isText ? 'auto' : 'visible',
            }}>{String(proposal.suggested)}</div>
          </div>
        </div>
      )}

      {/* Apply button */}
      <div style={{ padding: '6px 10px 8px', display: 'flex', alignItems: 'center', gap: 8 }}>
        <button
          disabled={st === 'applying' || st === 'applied'}
          onClick={() => onApply(msgIndex, proposal)}
          style={{
            padding: '5px 12px', borderRadius: 6, border: `1px solid ${btnColor}`,
            background: btnBg, color: btnColor, fontSize: 11, fontWeight: 700,
            cursor: st === 'applying' || st === 'applied' ? 'not-allowed' : 'pointer',
            opacity: st === 'applying' ? 0.7 : 1,
          }}
        >{btnLabel}</button>
        {proposal.error && <span style={{ fontSize: 10, color: '#f87171', flex: 1 }}>{proposal.error}</span>}
        {st === 'stale' && !proposal.error && (
          <span style={{ fontSize: 10, color: '#fbbf24' }}>Canvas changed since analysis</span>
        )}
      </div>
    </div>
  );
}

function AdvisorPanel({
  messages, busy, input, scanning,
  onInputChange, onSend, onClose, onRescan,
  onApplyProposal, onApplyAll,
}: {
  messages: AdvisorMessage[];
  busy: boolean;
  input: string;
  scanning: boolean;
  onInputChange: (v: string) => void;
  onSend: (text: string) => void;
  onClose: () => void;
  onRescan: () => void;
  onApplyProposal: (msgIndex: number, p: Proposal) => void;
  onApplyAll: (msgIndex: number) => void;
}) {
  const bottomRef = useRef<HTMLDivElement>(null);
  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }); }, [messages]);

  return (
    <div style={{
      width: 380, flexShrink: 0, height: '100%', display: 'flex', flexDirection: 'column',
      background: 'var(--tm-card-chrome)', borderLeft: `1px solid rgba(0,240,255,0.15)`,
      boxShadow: '-4px 0 24px rgba(0,0,0,0.4)',
    }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '11px 14px',
        borderBottom: `1px solid ${C.glassBorder}`, flexShrink: 0,
      }}>
        <span className="material-symbols-outlined" style={{ fontSize: 17, color: C.cyan }}>assistant</span>
        <span style={{ fontWeight: 700, fontSize: 13, color: C.text, flex: 1 }}>AI Workflow Advisor</span>
        {scanning && (
          <span style={{ fontSize: 11, color: C.cyan, fontStyle: 'italic' }}>Scanning…</span>
        )}
        <button
          onClick={onRescan}
          title="Re-analyze workflow"
          disabled={busy || scanning}
          style={{ width: 26, height: 26, borderRadius: 5, border: 'none', background: 'transparent',
            color: busy || scanning ? C.outlineVariant : C.textMuted, cursor: busy || scanning ? 'not-allowed' : 'pointer',
            display: 'flex', alignItems: 'center', justifyContent: 'center' }}
          onMouseEnter={e => { if (!busy && !scanning) e.currentTarget.style.color = C.cyan; }}
          onMouseLeave={e => { e.currentTarget.style.color = C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>refresh</span>
        </button>
        <button
          onClick={onClose}
          style={{ width: 26, height: 26, borderRadius: 5, border: 'none', background: 'transparent',
            color: C.textMuted, cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
          onMouseEnter={e => (e.currentTarget.style.color = C.text)}
          onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>close</span>
        </button>
      </div>

      {/* Messages */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '14px 14px', display: 'flex', flexDirection: 'column', gap: 10 }}>
        {messages.length === 0 && !busy && !scanning && (
          <div style={{ fontSize: 13, color: C.textMuted, fontStyle: 'italic', textAlign: 'center', marginTop: 40 }}>
            Scanning your workflow…
          </div>
        )}
        {messages.map((m, i) => {
          const pendingCount = (m.proposals ?? []).filter(p => p.status === 'pending' || p.status === 'stale').length;
          return (
            <div key={i} style={{ display: 'flex', flexDirection: 'column', alignItems: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
              {m.role === 'assistant' && (
                <span style={{ fontSize: 10, color: C.textMuted, marginBottom: 3, paddingLeft: 2 }}>AI Advisor</span>
              )}
              <div style={{
                maxWidth: '96%', padding: '9px 12px',
                borderRadius: m.role === 'user' ? '12px 12px 2px 12px' : '2px 12px 12px 12px',
                background: m.role === 'user' ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.04)',
                border: `1px solid ${m.role === 'user' ? 'rgba(0,240,255,0.2)' : C.outlineVariant}`,
                fontSize: 13, color: m.role === 'user' ? C.text : 'var(--tm-card-text-hint)',
                lineHeight: 1.65, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              }}>
                {m.text}
                {m.streaming && <span style={{ opacity: 0.6, marginLeft: 2 }}>▋</span>}
              </div>
              {/* Proposal cards */}
              {(m.proposals ?? []).length > 0 && (
                <div style={{ width: '96%', display: 'flex', flexDirection: 'column', gap: 0 }}>
                  {m.proposals!.map(p => (
                    <ProposalCard key={`${i}-${p.id}`} proposal={p} msgIndex={i} onApply={onApplyProposal} />
                  ))}
                  {pendingCount >= 2 && (
                    <button
                      onClick={() => onApplyAll(i)}
                      style={{
                        marginTop: 8, padding: '6px 0', borderRadius: 7,
                        border: `1px solid rgba(0,240,255,0.3)`,
                        background: 'rgba(0,240,255,0.08)', color: C.cyan,
                        fontSize: 11, fontWeight: 700, cursor: 'pointer', width: '100%',
                      }}
                    >Apply all ({pendingCount})</button>
                  )}
                </div>
              )}
            </div>
          );
        })}
        {busy && messages[messages.length - 1]?.role !== 'assistant' && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, paddingLeft: 2 }}>
            <span style={{ fontSize: 11, color: C.cyan, fontStyle: 'italic' }}>Thinking…</span>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <div style={{ padding: '10px 14px', borderTop: `1px solid ${C.glassBorder}`, flexShrink: 0 }}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
          <textarea
            value={input}
            onChange={e => onInputChange(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                if (!busy && !scanning && input.trim()) { onSend(input.trim()); onInputChange(''); }
              }
            }}
            placeholder="Ask a follow-up question…"
            disabled={busy || scanning}
            rows={2}
            style={{
              flex: 1, background: 'rgba(255,255,255,0.04)', border: `1px solid ${C.outlineVariant}`,
              borderRadius: 8, color: 'var(--tm-card-text)', fontSize: 13, padding: '7px 10px',
              resize: 'none', outline: 'none', fontFamily: 'inherit',
              opacity: (busy || scanning) ? 0.5 : 1,
            }}
          />
          <button
            onClick={() => { if (!busy && !scanning && input.trim()) { onSend(input.trim()); onInputChange(''); } }}
            disabled={busy || scanning || !input.trim()}
            style={{
              padding: '8px 12px', borderRadius: 8, border: 'none', flexShrink: 0,
              background: (!busy && !scanning && input.trim()) ? C.cyan : C.outlineVariant,
              color: (!busy && !scanning && input.trim()) ? '#00363a' : C.textMuted,
              cursor: (!busy && !scanning && input.trim()) ? 'pointer' : 'not-allowed',
              fontWeight: 700, fontSize: 12,
            }}
          >
            Send
          </button>
        </div>
        <div style={{ fontSize: 11, color: C.textMuted, marginTop: 5, paddingLeft: 2 }}>
          Shift+Enter for newline · Enter to send
        </div>
      </div>
    </div>
  );
}

// ── Canvas inner (needs ReactFlow context) ────────────────────────────────────
function CanvasInner({
  nodes, edges, onNodesChange, onEdgesChange, onConnect, onDrop, onDragOver, selectedNode, setSelectedNode, onUpdateNode, onDeleteEdge, onAutoLayout, logoState, advisorOpen, onAdvisorOpen,
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
  );
}

const toolBtnStyle: React.CSSProperties = {
  padding: '4px 8px', borderRadius: 6, border: 'none', cursor: 'pointer',
  background: 'transparent', color: C.textMuted, display: 'flex', alignItems: 'center',
  transition: 'all 0.1s',
};

// ── Connection rule system ────────────────────────────────────────────────────
// To add a new node type: add one entry here. The validator needs no changes.
interface NodePortDef {
  accepts: string[];       // signal types this node can receive
  emits: string[];         // signal types this node produces
  maxOutgoing?: number;    // undefined = unlimited
  maxIncoming?: number;    // undefined = unlimited
}

const NODE_PORTS: Record<string, NodePortDef> = {
  entryPoint:   { accepts: [],                           emits: ['request'] },  // multiple allowed, unique by slug
  orchestrator: { accepts: ['request', 'signal'],         emits: ['task', 'signal'] },
  agent:        { accepts: ['task', 'mw_task'],           emits: ['result'] },
  middleware:   { accepts: ['task', 'mw_task'],           emits: ['mw_task'] },
  // future: router, condition, webhook, llm, transform …
};

function validateConnection(
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

  // Prevent duplicate edge before cardinality check
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

interface ChainStatus {
  ready: boolean;
  label: string;
  color: string;
  epNode?: Node;
  orchNode?: Node;
  agentCount: number;
}

// ── Canvas rule engine ────────────────────────────────────────────────────────
type RuleSeverity = 'block' | 'warn';
interface CanvasRule {
  id: string;
  severity: RuleSeverity;
  message: (ctx: { nodes: Node[]; edges: Edge[] }) => string | null; // null = rule passes
}

const CANVAS_RULES: CanvasRule[] = [
  {
    id: 'AT_LEAST_ONE_EP',
    severity: 'block',
    message: ({ nodes }) => nodes.filter(n => n.type === 'entryPoint').length === 0
      ? 'Drop an Entry Point to start' : null,
  },
  {
    id: 'EP_SLUG_NONEMPTY',
    severity: 'block',
    message: ({ nodes }) => {
      const bad = nodes.filter(n => n.type === 'entryPoint' && !(n.data as EntryPointData).slug);
      return bad.length > 0 ? 'Every entry point needs a slug' : null;
    },
  },
  {
    id: 'EP_SLUG_UNIQUE',
    severity: 'block',
    message: ({ nodes }) => {
      const slugs = nodes.filter(n => n.type === 'entryPoint').map(n => (n.data as EntryPointData).slug ?? '');
      return new Set(slugs).size !== slugs.length ? 'Duplicate entry point slug — each slug must be unique' : null;
    },
  },
  {
    id: 'EP_SLUG_FORMAT',
    severity: 'block',
    message: ({ nodes }) => {
      const bad = nodes.filter(n => n.type === 'entryPoint' && !(n.data as EntryPointData).slug?.match(/^[a-z0-9_-]{1,64}$/));
      return bad.length > 0 ? `Slug "${(bad[0].data as EntryPointData).slug}": lowercase letters, numbers, _ or - only` : null;
    },
  },
  {
    id: 'EP_HAS_ORCH',
    severity: 'block',
    message: ({ nodes, edges }) => {
      const epNodes = nodes.filter(n => n.type === 'entryPoint');
      const unconnected = epNodes.filter(ep => !edges.some(e => e.source === ep.id && nodes.find(n => n.id === e.target && n.type === 'orchestrator')));
      return unconnected.length > 0 ? 'Every entry point must connect to an orchestrator' : null;
    },
  },
  {
    id: 'ORCH_HAS_AGENT',
    severity: 'warn',
    message: ({ nodes, edges }) => {
      const orchNodes = nodes.filter(n => n.type === 'orchestrator');
      const empty = orchNodes.filter(o => !edges.some(e => e.source === o.id && nodes.find(n => n.id === e.target && n.type === 'agent')));
      return empty.length > 0 ? `${empty.length} orchestrator${empty.length > 1 ? 's have' : ' has'} no agents` : null;
    },
  },
];

function runRules(nodes: Node[], edges: Edge[], mode: 'save' | 'deploy'): { ok: boolean; message: string | null; warnings: string[] } {
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
function analyzeChain(nodes: Node[], edges: Edge[]): ChainStatus {
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

// ── Compute styled edges based on chain validity ──────────────────────────────
function styledEdges(edges: Edge[], nodes: Node[]): Edge[] {
  // Compute set of all edges that are part of a valid EP→Orch→(MW→)*Agent chain
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

function toSlug(s: string) {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64);
}

// ── Entry Point Picker modal ──────────────────────────────────────────────────
interface EpPickerEntry { epNode: Node; orchName: string; slug: string; label: string; epType: string; }

function EpPickerModal({ entries, onSelect, onClose }: { entries: EpPickerEntry[]; onSelect: (e: EpPickerEntry) => void; onClose: () => void; }) {
  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam' };
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

// ── Builder view ──────────────────────────────────────────────────────────────
function BuilderView({
  app,
  orchestrators,
  agents,
  onBack,
  onSaved,
  onOrchestratorsChange,
  onAgentsChange,
}: {
  app: Application | null;
  orchestrators: OrchestratorFull[];
  agents: Agent[];
  onBack: () => void;
  onSaved: () => void;
  onOrchestratorsChange: (update: (prev: OrchestratorFull[]) => OrchestratorFull[]) => void;
  onAgentsChange: (update: (prev: Agent[]) => Agent[]) => void;
}) {
  const initial = app
    ? buildNodesFromApp(app, agents)
    : {
        nodes: [],
        edges: [],
      };

  const [nodes, setNodes, onNodesChange] = useNodesState(initial.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initial.edges);
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);
  const [currentApp, setCurrentApp] = useState<Application | null>(app);
  const [saving, setSaving] = useState(false);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
  const [libWidth, setLibWidth] = useState(280);
  const [middlewareDefs, setMiddlewareDefs] = useState<MiddlewareDef[]>([]);
  const [appName, setAppName] = useState(app?.name ?? '');
  const [convTokenLimit, setConvTokenLimit] = useState<string>(
    app?.entry_points?.[0]?.conversation_token_limit != null ? String(app.entry_points[0].conversation_token_limit) : ''
  );
  const [slugLocked, setSlugLocked] = useState(!!(app?.entry_points?.[0]?.slug));
  const [isDirty, setIsDirty] = useState(false);
  const [testPickerOpen, setTestPickerOpen] = useState(false);
  const [logoState, setLogoState] = useState<LogoState>('idle');
  const logoTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const rfWrapper = useRef<HTMLDivElement>(null);

  // ── Advisor state ────────────────────────────────────────────────────────────
  const [advisorOpen, setAdvisorOpen] = useState(false);
  const [advisorMessages, setAdvisorMessages] = useState<AdvisorMessage[]>([]);
  const [advisorBusy, setAdvisorBusy] = useState(false);
  const [advisorInput, setAdvisorInput] = useState('');
  const [advisorContextId, setAdvisorContextId] = useState<string | null>(null);
  const [advisorScanning, setAdvisorScanning] = useState(false);
  const advisorWsRef = useRef<WebSocket | null>(null);
  const advisorBufRef = useRef('');
  const advisorScanningRef = useRef(false);

  function triggerLogo(state: LogoState, duration = 2000) {
    if (logoTimerRef.current) clearTimeout(logoTimerRef.current);
    setLogoState(state);
    logoTimerRef.current = setTimeout(() => setLogoState('idle'), duration);
  }
  const nodesRef = useRef<Node[]>(initial.nodes);
  const edgesRef = useRef<Edge[]>(initial.edges);
  const { screenToFlowPosition } = useReactFlow();

  const epNode = nodes.find((n: Node) => n.type === 'entryPoint');

  deleteNodeRef.current = (id: string) => {
    // Deleting an EP node just removes it from the canvas; the diff is applied on next Save.
    setNodes((nds: Node[]) => nds.filter(n => n.id !== id));
    setEdges((eds: Edge[]) => eds.filter(e => e.source !== id && e.target !== id));
    setSelectedNode((prev: Node | null) => prev?.id === id ? null : prev);
  };

  // Fetch middleware defs on mount
  useEffect(() => {
    themApi.listMiddlewareDefs().then(setMiddlewareDefs).catch(() => {/* non-critical */});
  }, []);

  // Keep refs in sync so onConnect can read current state synchronously
  useEffect(() => { nodesRef.current = nodes; }, [nodes]);
  useEffect(() => { edgesRef.current = edges; }, [edges]);

  // Dirty tracking — set on any change after mount
  const mountedRef = useRef(false);
  useEffect(() => {
    if (!mountedRef.current) { mountedRef.current = true; return; }
    setIsDirty(true);
    // Only switch to dirty state if not mid-animation
    setLogoState(prev => (prev === 'idle' || prev === 'dirty') ? 'dirty' : prev);
  }, [nodes, edges, appName]);

  // Return logo to idle when canvas becomes clean
  useEffect(() => {
    if (!isDirty) setLogoState(prev => prev === 'dirty' ? 'idle' : prev);
  }, [isDirty]);

  // Warn before leaving when dirty
  useEffect(() => {
    function onBeforeUnload(e: BeforeUnloadEvent) {
      if (!isDirty) return;
      e.preventDefault();
      e.returnValue = '';
    }
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [isDirty]);

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.key === 'Delete' || e.key === 'Backspace') && selectedNode) {
        const tag = (document.activeElement as HTMLElement)?.tagName;
        if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
        setNodes((nds: Node[]) => nds.filter(n => n.id !== selectedNode.id));
        setEdges((eds: Edge[]) => eds.filter(e => e.source !== selectedNode.id && e.target !== selectedNode.id));
        setSelectedNode(null);
      }
    }
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [selectedNode, setNodes, setEdges]);

  function showToast(msg: string, ok: boolean) {
    setToast({ msg, ok });
    setTimeout(() => setToast(null), 3000);
  }

  // ── Advisor functions ────────────────────────────────────────────────────────

  function serializeWorkflow(): object {
    const nds = nodesRef.current;
    const eds = edgesRef.current;

    // Build agent id→slug lookup so orchestrators can name their assigned agents
    const agentIdToSlug: Record<string, string> = {};
    for (const a of agents) agentIdToSlug[a.id] = a.slug || a.id;

    return {
      nodes: nds.map(n => {
        if (n.type === 'entryPoint') {
          const d = n.data as EntryPointData;
          return { type: 'entry_point', id: n.id, epType: d.epType, accessMode: d.accessMode, slug: d.slug };
        }
        if (n.type === 'orchestrator') {
          const d = n.data as OrchestratorData;
          const full = orchestrators.find(o => o.id === d.orchestratorId);
          const rawPrompt = full?.system_prompt ?? '';
          const assignedAgentIds = full?.allowed_agent_ids ?? [];
          return {
            type: 'orchestrator',
            id: n.id,
            orchestratorId: full?.id,
            name: d.name,
            displayName: d.displayName,
            model: d.model,
            maxParallelTools: d.maxParallelTools,
            maxIterations: full?.max_iterations ?? 10,
            historyWindow: full?.history_window ?? null,
            memoryEnabled: full?.memory_enabled ?? false,
            systemPrompt: rawPrompt.slice(0, 800) + (rawPrompt.length > 800 ? '…[truncated]' : ''),
            assignedAgents: assignedAgentIds.map(aid => ({
              id: aid,
              slug: agentIdToSlug[aid] ?? aid,
            })),
          };
        }
        if (n.type === 'agent') {
          const d = n.data as AgentData;
          const full = agents.find(a => a.id === d.agentId);
          return {
            type: 'agent',
            id: n.id,
            agentId: full?.id,
            slug: d.name,
            displayName: d.displayName,
            description: d.description,
            transport: d.transport,
            hasAuthToken: full?.auth_token_set ?? false,
            scanResult: full?.last_scan_result
              ? { score: full.last_scan_result.score, risk: full.last_scan_result.risk, summary: full.last_scan_result.summary }
              : null,
          };
        }
        return { type: n.type, id: n.id };
      }),
      edges: eds.map(e => ({ source: e.source, target: e.target })),
    };
  }

  async function advisorSend(text: string | null, isInitial = false) {
    if (advisorBusy) return;
    setAdvisorBusy(true);
    advisorBufRef.current = '';
    triggerLogo('thinking', 120000); // hold until done

    const content = isInitial
      ? `Analyze this workflow:\n\n${JSON.stringify(serializeWorkflow(), null, 2)}`
      : (text ?? '').trim();

    if (!isInitial && !content) { setAdvisorBusy(false); return; }

    setAdvisorMessages(prev => [
      ...prev,
      { role: 'user', text: isInitial ? '🔍 Analyzing your workflow…' : content },
    ]);

    let token: string;
    try {
      const r = await fetch('/api/auth/token');
      if (!r.ok) throw new Error('auth');
      ({ token } = await r.json());
    } catch {
      setAdvisorMessages(prev => [...prev, { role: 'assistant', text: 'Could not connect — please refresh and try again.' }]);
      setAdvisorBusy(false);
      return;
    }

    const ws = new WebSocket(`${getBridgeWs()}/ws/orchestrate/workflow_advisor?token=${encodeURIComponent(token)}`);
    advisorWsRef.current = ws;

    ws.onopen = () => {
      const payload: Record<string, string> = { type: 'message', content };
      if (advisorContextId) payload.context_id = advisorContextId;
      ws.send(JSON.stringify(payload));
    };

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data as string);
        if (msg.type === 'ready') {
          if (msg.context_id) setAdvisorContextId(msg.context_id as string);
          setAdvisorMessages(prev => [...prev, { role: 'assistant', text: '', streaming: true }]);
        } else if (msg.type === 'token') {
          advisorBufRef.current += (msg.text ?? '') as string;
          const { text: parsed, proposals } = parseAdvisorBuffer(advisorBufRef.current);
          setAdvisorMessages(prev => {
            const last = prev[prev.length - 1];
            const merged = mergeProposals(last?.proposals, proposals);
            const next: AdvisorMessage = { role: 'assistant', text: parsed, streaming: true, proposals: merged };
            if (last?.role === 'assistant') return [...prev.slice(0, -1), next];
            return [...prev, next];
          });
        } else if (msg.type === 'done') {
          const { text: parsed, proposals } = parseAdvisorBuffer(advisorBufRef.current);
          setAdvisorMessages(prev => {
            const last = prev[prev.length - 1];
            if (last?.role === 'assistant') {
              const merged = mergeProposals(last.proposals, proposals);
              return [...prev.slice(0, -1), { ...last, text: parsed, proposals: merged, streaming: false }];
            }
            return prev;
          });
          setAdvisorBusy(false);
          triggerLogo('idle', 1);
          ws.close();
        } else if (msg.type === 'error') {
          setAdvisorMessages(prev => [...prev, { role: 'assistant', text: `⚠️ ${msg.message ?? 'Something went wrong.'}` }]);
          setAdvisorBusy(false);
          triggerLogo('idle', 1);
          ws.close();
        }
      } catch { /* ignore parse errors */ }
    };

    ws.onerror = () => { setAdvisorBusy(false); triggerLogo('idle', 1); };
    ws.onclose = () => { setAdvisorBusy(false); };
  }

  async function handleAdvisorOpen() {
    if (advisorScanningRef.current) return;

    // If already open, just re-focus (no re-scan)
    if (advisorOpen) { setAdvisorOpen(false); triggerLogo('idle', 1); return; }

    advisorScanningRef.current = true;
    setAdvisorScanning(true);
    setAdvisorOpen(true);

    // Scan animation — nodes light up sequentially
    const nodeIds = nodesRef.current.map(n => n.id);
    if (nodeIds.length > 0) {
      const delay = Math.min(200, Math.floor(1200 / nodeIds.length));
      for (let i = 0; i < nodeIds.length; i++) {
        setNodes(nds => nds.map(n => ({
          ...n,
          data: { ...n.data, _scanning: n.id === nodeIds[i] },
        })));
        await new Promise(r => setTimeout(r, delay));
      }
      // Clear scan highlight
      setNodes(nds => nds.map(n => ({ ...n, data: { ...n.data, _scanning: false } })));
      await new Promise(r => setTimeout(r, 200));
    }

    advisorScanningRef.current = false;
    setAdvisorScanning(false);

    // Only send initial analysis if fresh session
    if (advisorMessages.length === 0) {
      await advisorSend(null, true);
    }
  }

  function handleAdvisorRescan() {
    setAdvisorMessages([]);
    setAdvisorContextId(null);
    advisorBufRef.current = '';
    handleAdvisorOpen();
  }

  function setProposalStatus(msgIndex: number, proposalId: string, status: ProposalStatus, error?: string) {
    setAdvisorMessages(prev => prev.map((m, i) => {
      if (i !== msgIndex || !m.proposals) return m;
      return {
        ...m,
        proposals: m.proposals.map(p =>
          p.id === proposalId ? { ...p, status, error } : p
        ),
      };
    }));
  }

  function reflectProposalOnCanvas(proposal: Proposal, updated: OrchestratorFull | Agent) {
    // Update the full lists in parent so re-serialization sees the new value
    if (proposal.targetType === 'orchestrator') {
      onOrchestratorsChange(prev => prev.map(o => o.id === proposal.targetId ? updated as OrchestratorFull : o));
    } else {
      onAgentsChange(prev => prev.map(a => a.id === proposal.targetId ? updated as Agent : a));
    }
    // Update canvas node data for fields that live there
    const nodeId = nodesRef.current.find(n => {
      const d = n.data as Record<string, unknown>;
      return d.orchestratorId === proposal.targetId || d.agentId === proposal.targetId;
    })?.id;
    if (!nodeId) return;
    if (proposal.field === 'display_name') updateNodeData(nodeId, { displayName: proposal.suggested });
    if (proposal.field === 'description') updateNodeData(nodeId, { description: proposal.suggested });
    if (proposal.field === 'max_parallel_tools') updateNodeData(nodeId, { maxParallelTools: proposal.suggested });
  }

  async function applyProposal(msgIndex: number, proposal: Proposal) {
    if (proposal.status === 'applying' || proposal.status === 'applied') return;
    setProposalStatus(msgIndex, proposal.id, 'applying');
    try {
      const body: Record<string, unknown> = { [proposal.field]: proposal.suggested };
      let updated: OrchestratorFull | Agent;
      if (proposal.targetType === 'orchestrator') {
        updated = await themApi.updateOrchestrator(proposal.targetId, body);
      } else {
        updated = await themApi.updateAgent(proposal.targetId, body);
      }
      reflectProposalOnCanvas(proposal, updated);
      setProposalStatus(msgIndex, proposal.id, 'applied');
      showToast(`Applied: ${FIELD_LABEL[proposal.field] ?? proposal.field} on ${proposal.targetName}`, true);
    } catch (e) {
      setProposalStatus(msgIndex, proposal.id, 'failed', String(e));
      showToast(`Failed to apply ${FIELD_LABEL[proposal.field] ?? proposal.field}`, false);
    }
  }

  async function applyAll(msgIndex: number) {
    const msg = advisorMessages[msgIndex];
    if (!msg?.proposals) return;
    const pending = msg.proposals.filter(p => p.status === 'pending' || p.status === 'stale' || p.status === 'failed');
    for (const p of pending) {
      await applyProposal(msgIndex, p);
    }
  }

  const onConnect = useCallback((c: Connection) => {
    const nds = nodesRef.current;
    const eds = edgesRef.current;
    const srcNode = nds.find(n => n.id === c.source);
    const tgtNode = nds.find(n => n.id === c.target);
    if (!srcNode || !tgtNode) return;
    const err = validateConnection(srcNode.type!, tgtNode.type!, c.source!, c.target!, eds);
    if (err) { showToast(err, false); triggerLogo('warning', 1800); return; }
    setEdges(eds => addEdge({ ...c, animated: true, style: { stroke: C.cyan, strokeWidth: 2 } }, eds));
  }, [setEdges]); // eslint-disable-line react-hooks/exhaustive-deps

  function onDragOver(e: DragEvent<HTMLDivElement>) {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
  }

  function onDrop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault();
    const nodeType = e.dataTransfer.getData('nodeType');
    const rawData = e.dataTransfer.getData('nodeData');
    if (!nodeType || !rawData) return;
    let nodeData: Record<string, unknown>;
    try { nodeData = JSON.parse(rawData) as Record<string, unknown>; } catch { return; }

    if (nodeType === 'entryPoint') {
      nodeData = { ...nodeData, slug: '' };
    }

    const position = screenToFlowPosition({ x: e.clientX, y: e.clientY });
    const orchId = makeId();
    const newNode: Node = { id: orchId, type: nodeType, position, data: nodeData };

    if (nodeType === 'orchestrator') {
      const full = orchestrators.find(o => o.id === (nodeData.orchestratorId as string));
      const connectedAgents = full ? agents.filter(a => full.allowed_agent_ids.includes(a.id)) : [];

      if (connectedAgents.length > 0) {
        const spread = Math.max(connectedAgents.length * 140, 300);
        const startX = position.x - spread / 2 + 70;
        const agentNodes: Node[] = connectedAgents.map((agent, i) => ({
          id: makeId(),
          type: 'agent',
          position: { x: startX + i * 140, y: position.y + 160 },
          data: {
            agentId: agent.id,
            name: agent.slug,
            displayName: agent.display_name,
            description: agent.description,
            transport: agent.transport,
            endpointUrl: agent.endpoint_url,
            tags: agent.tags ?? [],
            icon: agent.icon || agentIconForLibrary(agent),
          } satisfies AgentData,
        }));
        const agentEdges: Edge[] = agentNodes.map((an, i) => ({
          id: `e_${orchId}_agent_${i}`,
          source: orchId,
          target: an.id,
          animated: true,
          style: EDGE_STYLE,
        }));
        setNodes(nds => [...nds, newNode, ...agentNodes]);
        setEdges(eds => [...eds, ...agentEdges]);
        return;
      }
    }

    setNodes((nds: Node[]) => [...nds, newNode]);
  }

  function deleteEdge(edgeId: string) {
    setEdges(eds => eds.filter(e => e.id !== edgeId));
  }

  function autoLayout() {
    setNodes(nds => applyDagreLayout(nds, edgesRef.current));
  }

  function updateNodeData(id: string, partialData: Record<string, unknown>) {
    setNodes((nds: Node[]) => nds.map((n: Node) => n.id === id ? { ...n, data: { ...n.data, ...partialData } } : n));
    setSelectedNode((prev: Node | null) => prev && prev.id === id ? { ...prev, data: { ...prev.data, ...partialData } } : prev);
  }

  async function handleSave(deploy = false) {
    const validation = runRules(nodes, edges, deploy ? 'deploy' : 'save');
    if (!validation.ok) { showToast(validation.message!, false); triggerLogo('error', 1800); return; }

    // Collect all EP nodes + their connected orch nodes
    const epPairs = nodes
      .filter((n: Node) => n.type === 'entryPoint')
      .map((epNode: Node) => {
        const orchEdge = edges.find((e: Edge) => e.source === epNode.id && nodes.some(n => n.id === e.target && n.type === 'orchestrator'));
        const orchNode = orchEdge ? nodes.find((n: Node) => n.id === orchEdge.target) : undefined;
        return orchNode ? { epNode, orchNode } : null;
      })
      .filter(Boolean) as { epNode: Node; orchNode: Node }[];

    if (epPairs.length === 0) { showToast('Connect entry points to orchestrators', false); return; }

    setSaving(true);
    setLogoState('thinking');
    try {
      // Build entry_points array with inline orchestrator config per EP
      const entryPoints = epPairs.map(({ epNode, orchNode }) => {
        const d = epNode.data as EntryPointData;
        const od = orchNode.data as OrchestratorData;
        const limit = d.convTokenLimit !== undefined && d.convTokenLimit !== '' ? parseInt(d.convTokenLimit, 10) : null;

        // Collect agent ids from outgoing Orch→Agent edges
        const agentIds = edges
          .filter((e: Edge) => e.source === orchNode.id)
          .map((e: Edge) => nodes.find((n: Node) => n.id === e.target && n.type === 'agent'))
          .filter((n: Node | undefined): n is Node => Boolean(n))
          .map((n: Node) => (n.data as AgentData).agentId);

        return {
          ...(d._epId ? { id: d._epId } : {}),
          slug: d.slug,
          entry_point_type: d.epType,
          access_policy: { mode: d.accessMode },
          conversation_token_limit: limit,
          enabled: true,
          orchestrator: {
            ...(od.appOrchestratorId ? { id: od.appOrchestratorId } : {}),
            allowed_agent_ids: agentIds,
            display_name: od.displayName,
            system_prompt: od.systemPrompt,
            llm_provider: od.llmProvider,
            llm_model: od.llmModel,
            max_iterations: od.maxIterations,
            history_window: od.historyWindow,
            delegatable: od.delegatable,
          },
        };
      });

      const resolvedName = appName.trim() || epPairs[0].epNode.data.appName || epPairs[0].epNode.data.slug;

      const body: Record<string, unknown> = {
        name: resolvedName,
        enabled: deploy ? true : (currentApp?.enabled ?? false),
        entry_points: entryPoints,
      };

      let saved: Application;
      if (currentApp?.id) {
        saved = await themApi.updateApplication(currentApp.id, body);
      } else {
        saved = await themApi.createApplication(body);
      }
      setCurrentApp(saved);

      // Write back DB ids to EP nodes and orch nodes
      saved.entry_points.forEach(ep => {
        const matchingEpNode = nodes.find((n: Node) => n.type === 'entryPoint' && (n.data as EntryPointData).slug === ep.slug);
        if (matchingEpNode) {
          updateNodeData(matchingEpNode.id, { _epId: ep.id });
          // Also write back the AppOrchestrator id to the connected orch node
          const orchEdge = edges.find((e: Edge) => e.source === matchingEpNode.id);
          const orchNode = orchEdge ? nodes.find((n: Node) => n.id === orchEdge.target) : undefined;
          if (orchNode && ep.app_orchestrator?.id) {
            updateNodeData(orchNode.id, { appOrchestratorId: ep.app_orchestrator.id });
          }
        }
      });

      // Sync middleware wirings — iterate all orch nodes
      try {
        const wirings: Array<{ def_id: string; agent_id: string; position: number; config_override: Record<string, unknown>; node_id: string; enabled: boolean }> = [];
        const orchNodes = [...new Set(epPairs.map(p => p.orchNode))];
        for (const orchNodeForMW of orchNodes) {
          for (const orchEdge of edges.filter((e: Edge) => e.source === orchNodeForMW.id)) {
            const firstTarget = nodes.find((n: Node) => n.id === orchEdge.target);
            if (!firstTarget || firstTarget.type !== 'middleware') continue;
            const mwChain: Node[] = [];
            let current: Node | undefined = firstTarget;
            while (current && current.type === 'middleware') {
              mwChain.push(current);
              const nextEdge = edges.find((e: Edge) => e.source === current!.id);
              current = nextEdge ? nodes.find((n: Node) => n.id === nextEdge.target) : undefined;
            }
            const agentNode = current && current.type === 'agent' ? current : undefined;
            if (agentNode) {
              const agentId = (agentNode.data as AgentData).agentId;
              mwChain.forEach((mwNode, idx) => {
                const md = mwNode.data as MiddlewareData;
                wirings.push({ def_id: md.defId, agent_id: agentId, position: idx, config_override: md.configOverride ?? {}, node_id: mwNode.id, enabled: true });
              });
            }
          }
        }
        await themApi.putMiddlewareWirings(saved.id, wirings);
      } catch { /* non-fatal */ }

      setIsDirty(false);
      triggerLogo('success', deploy ? 2500 : 1800);
      showToast(deploy ? '🚀 Application deployed!' : 'Saved successfully', true);
      onSaved();
    } catch (err: any) {
      triggerLogo('error', 1800);
      showToast(err?.message ?? 'Save failed', false);
    } finally {
      setSaving(false);
      setLogoState('idle');
    }
  }

  function handleTest() {
    const anyEnabled = currentApp?.enabled || currentApp?.entry_points?.some(ep => ep.enabled);
    if (!anyEnabled) { showToast('Deploy the application first', false); return; }
    const epNodes = nodes.filter((n: Node) => n.type === 'entryPoint' && (n.data as EntryPointData).slug);
    if (epNodes.length === 0) { showToast('No entry points configured', false); return; }
    const buildEntry = (n: Node): EpPickerEntry => {
      const d = n.data as EntryPointData;
      const orchEdge = edges.find((e: Edge) => e.source === n.id);
      const orchNode = orchEdge ? nodes.find((nd: Node) => nd.id === orchEdge.target && nd.type === 'orchestrator') : undefined;
      return { epNode: n, orchName: orchNode ? (orchNode.data as OrchestratorData).name : '', slug: d.slug, label: d.label, epType: d.epType };
    };
    if (epNodes.length === 1) {
      const entry = buildEntry(epNodes[0]);
      const url = entry.orchName ? `/admin/playground?orchestrator=${encodeURIComponent(entry.orchName)}` : '/admin/playground';
      window.open(url, '_blank', 'noopener');
    } else {
      setTestPickerOpen(true);
    }
  }

  const chain = analyzeChain(nodes, edges);
  const epPickerEntries: EpPickerEntry[] = nodes
    .filter((n: Node) => n.type === 'entryPoint' && (n.data as EntryPointData).slug)
    .map((n: Node) => {
      const d = n.data as EntryPointData;
      const orchEdge = edges.find((e: Edge) => e.source === n.id);
      const orchNode = orchEdge ? nodes.find((nd: Node) => nd.id === orchEdge.target && nd.type === 'orchestrator') : undefined;
      return { epNode: n, orchName: orchNode ? (orchNode.data as OrchestratorData).name : '', slug: d.slug, label: d.label, epType: d.epType };
    });

  return (
    <div className="builder-root" style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg, overflow: 'hidden' }}>
      {/* Top bar */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 16, padding: '0 24px', height: 56, flexShrink: 0,
        ...glass, borderBottom: `1px solid ${C.glassBorder}`, zIndex: 20,
      }}>
        <button
          onClick={() => {
            if (isDirty && !confirm('You have unsaved changes. Leave anyway?')) return;
            onBack();
          }}
          style={{ ...toolBtnStyle, padding: '6px 10px', color: C.textMuted }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 18 }}>arrow_back</span>
        </button>
        <div style={{ width: 1, height: 20, background: C.outlineVariant }} />
        <div style={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: 10 }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <input
              className="nodrag"
              value={appName}
              onChange={e => {
                setAppName(e.target.value);
                if (!slugLocked && epNode) {
                  updateNodeData(epNode.id, { slug: toSlug(e.target.value) });
                }
              }}
              placeholder="Application name…"
              style={{
                background: 'transparent', border: 'none', outline: 'none',
                fontSize: 15, fontWeight: 700, color: 'var(--tm-card-text)',
                fontFamily: 'Geist, sans-serif', width: '100%', padding: 0,
              }}
            />
            {epNode && (
              <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>
                {(epNode.data as EntryPointData).slug || (app?.entry_points?.[0]?.slug ?? '')}
              </div>
            )}
          </div>
          {isDirty && (
            <div title="Unsaved changes" style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '3px 8px', borderRadius: 6, background: 'rgba(245,158,11,0.1)', border: '1px solid rgba(245,158,11,0.3)' }}>
              <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#f59e0b', boxShadow: '0 0 6px #f59e0b', display: 'inline-block' }} />
              <span style={{ fontSize: 11, color: '#f59e0b', fontWeight: 600 }}>Unsaved</span>
            </div>
          )}
        </div>

        <div style={{ display: 'flex', gap: 8 }}>
          <button
            onClick={() => handleSave(false)}
            disabled={saving}
            style={{
              padding: '7px 18px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`,
              background: 'transparent', color: C.text, cursor: 'pointer', fontSize: 13, fontWeight: 600,
              opacity: saving ? 0.6 : 1,
            }}
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
          <button
            onClick={handleTest}
            disabled={saving}
            title={!currentApp?.enabled && !currentApp?.entry_points?.some(ep => ep.enabled) ? 'Deploy first to test' : 'Open in playground'}
            style={{
              padding: '7px 18px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`,
              background: 'transparent', color: (currentApp?.enabled || currentApp?.entry_points?.some(ep => ep.enabled)) ? C.green : C.textMuted,
              cursor: saving ? 'not-allowed' : 'pointer', fontSize: 13, fontWeight: 600,
              opacity: saving ? 0.6 : 1, display: 'flex', alignItems: 'center', gap: 6,
              transition: 'all 0.2s',
            }}
          >
            <span className="material-symbols-outlined" style={{ fontSize: 15 }}>play_arrow</span>
            Test
          </button>
          <button
            onClick={() => handleSave(true)}
            disabled={saving || !chain.ready}
            style={{
              padding: '7px 18px', borderRadius: 8, border: 'none',
              background: chain.ready ? C.cyan : C.outlineVariant,
              color: chain.ready ? '#00363a' : C.textMuted,
              cursor: saving || !chain.ready ? 'not-allowed' : 'pointer',
              fontSize: 13, fontWeight: 700,
              opacity: saving ? 0.6 : 1,
              boxShadow: chain.ready ? `0 0 12px rgba(0,240,255,0.25)` : 'none',
              transition: 'all 0.2s ease',
            }}
          >
            Deploy
          </button>
        </div>
      </div>

      {/* Builder area */}
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }} ref={rfWrapper}>
        <NodeLibrary orchestrators={orchestrators} agents={agents} middlewareDefs={middlewareDefs} width={libWidth} onWidthChange={setLibWidth} />

        {/* Canvas */}
        <div style={{ flex: 1, position: 'relative', height: 'calc(100vh - 56px)' }}>
          <CanvasInner
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onDrop={onDrop}
            onDragOver={onDragOver}
            selectedNode={selectedNode}
            setSelectedNode={setSelectedNode}
            onUpdateNode={updateNodeData}
            onDeleteEdge={deleteEdge}
            onAutoLayout={autoLayout}
            logoState={logoState}
            advisorOpen={advisorOpen}
            onAdvisorOpen={handleAdvisorOpen}
          />
        </div>

        {advisorOpen && (
          <AdvisorPanel
            messages={advisorMessages}
            busy={advisorBusy}
            input={advisorInput}
            scanning={advisorScanning}
            onInputChange={setAdvisorInput}
            onSend={text => advisorSend(text)}
            onClose={() => { setAdvisorOpen(false); triggerLogo('idle', 1); }}
            onRescan={handleAdvisorRescan}
            onApplyProposal={applyProposal}
            onApplyAll={applyAll}
          />
        )}

        <PropertiesPanel
          selectedNode={selectedNode}
          onUpdateNode={updateNodeData}
          slugLocked={slugLocked}
          onSlugManualEdit={() => setSlugLocked(true)}
          appName={appName}
          onAppNameChange={name => {
            setAppName(name);
            if (!slugLocked && epNode) updateNodeData(epNode.id, { slug: toSlug(name) });
          }}
          convTokenLimit={convTokenLimit}
          onConvTokenLimitChange={val => { setConvTokenLimit(val); setIsDirty(true); }}
          chain={chain}
          app={currentApp}
          epCount={nodes.filter((n: Node) => n.type === 'entryPoint').length}
          nodes={nodes}
          edges={edges}
        />
      </div>

      {/* Status bar */}
      <div style={{
        height: 28, display: 'flex', alignItems: 'center', gap: 12, padding: '0 16px',
        ...glass, borderTop: `1px solid ${C.glassBorder}`, fontSize: 11, color: C.textMuted, flexShrink: 0,
      }}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          <span style={{
            width: 6, height: 6, borderRadius: '50%', display: 'inline-block',
            background: chain.color,
            boxShadow: chain.ready ? `0 0 6px ${chain.color}` : 'none',
          }} />
          <span style={{ color: chain.color, fontWeight: 600 }}>{chain.label}</span>
        </span>
        <span style={{ color: C.outlineVariant }}>·</span>
        <span>Nodes: {nodes.length}</span>
        <span style={{ color: C.outlineVariant }}>·</span>
        <span>Edges: {edges.length}</span>
      </div>

      {/* Entry Point Picker */}
      {testPickerOpen && (
        <EpPickerModal
          entries={epPickerEntries}
          onSelect={entry => {
            setTestPickerOpen(false);
            const url = entry.orchName ? `/admin/playground?orchestrator=${encodeURIComponent(entry.orchName)}` : '/admin/playground';
            window.open(url, '_blank', 'noopener');
          }}
          onClose={() => setTestPickerOpen(false)}
        />
      )}

      {/* Toast */}
      {toast && (
        <div style={{
          position: 'fixed', bottom: 40, left: '50%', transform: 'translateX(-50%)',
          padding: '10px 20px', borderRadius: 8, fontSize: 13, fontWeight: 600, zIndex: 9999,
          background: toast.ok ? 'rgba(74,222,128,0.15)' : C.errorBg,
          border: `1px solid ${toast.ok ? C.greenBorder : 'rgba(255,180,171,0.3)'}`,
          color: toast.ok ? C.green : C.error,
          boxShadow: `0 4px 20px rgba(0,0,0,0.4)`,
          backdropFilter: 'blur(8px)',
        }}>
          {toast.msg}
        </div>
      )}
    </div>
  );
}

// ── List view ─────────────────────────────────────────────────────────────────
const APP_CARD_STYLES = `
.app-glass-card {
  background:
    linear-gradient(160deg, rgba(255,255,255,0.032) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%),
    var(--tm-card);
  border: 1px solid var(--tm-card-border);
  backdrop-filter: blur(12px);
  box-shadow:
    0 8px 32px rgba(0,0,0,0.4),
    0 2px 8px rgba(0,0,0,0.25),
    inset 0 1px 0 rgba(255,255,255,0.04);
  transition: border-color 240ms ease, box-shadow 240ms ease;
}
.app-glass-card:hover {
  border-color: rgba(0,209,255,0.28);
  box-shadow:
    0 8px 32px rgba(0,0,0,0.5),
    0 2px 8px rgba(0,0,0,0.28),
    0 0 0 1px rgba(0,209,255,0.1),
    0 0 32px rgba(0,209,255,0.08),
    inset 0 1px 0 rgba(255,255,255,0.055);
}
.app-glass-card:active {
  box-shadow:
    0 4px 16px rgba(0,0,0,0.5),
    inset 0 1px 0 rgba(255,255,255,0.03);
  border-color: rgba(0,209,255,0.4);
  transition: border-color 80ms ease, box-shadow 80ms ease;
}
.app-card-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  padding: 9px 4px;
  border-radius: 8px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.01em;
  cursor: pointer;
  transition: border-color 180ms ease, background 180ms ease,
              box-shadow 180ms ease, transform 180ms ease;
  white-space: nowrap;
}
.app-card-btn--open {
  background: #00d1ff;
  color: #021520;
  border: none;
  box-shadow: 0 0 14px rgba(0,209,255,0.38);
}
.app-card-btn--open:hover {
  background: #22dcff;
  box-shadow: 0 0 22px rgba(0,209,255,0.55);
}
.app-card-btn--open:active {
  background: #00b8e0;
  box-shadow: 0 0 10px rgba(0,209,255,0.3);
}
.app-card-btn--urls {
  background: var(--tm-btn-2-bg);
  color: var(--tm-card-text-subtle);
  border: 1px solid var(--tm-btn-2-border);
}
.app-card-btn--urls:hover {
  border-color: rgba(129,140,248,0.45);
  color: #818cf8;
  background: rgba(99,102,241,0.1);
}
.app-card-btn--toggle-on {
  background: var(--tm-btn-2-bg);
  color: #f87171;
  border: 1px solid rgba(248,113,113,0.2);
}
.app-card-btn--toggle-on:hover {
  border-color: rgba(248,113,113,0.5);
  background: rgba(248,113,113,0.08);
}
.app-card-btn--toggle-off {
  background: var(--tm-btn-2-bg);
  color: #34d399;
  border: 1px solid rgba(52,211,153,0.2);
}
.app-card-btn--toggle-off:hover {
  border-color: rgba(52,211,153,0.5);
  background: rgba(52,211,153,0.08);
}
.app-deploy-card:hover {
  border-color: rgba(99,102,241,0.7) !important;
  background: rgba(99,102,241,0.04) !important;
}
`;

// EP metadata
const EP_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam' };
const EP_LABEL: Record<string, string> = { websocket: 'WebSocket', sse: 'SSE', webrtc: 'WebRTC' };

function epIconColor(type: string): { color: string; glow: string; border: string } {
  if (type === 'websocket') return { color: '#00d1ff', glow: 'rgba(0,209,255,0.25)', border: 'rgba(0,209,255,0.45)' };
  if (type === 'sse')       return { color: '#a78bfa', glow: 'rgba(167,139,250,0.22)', border: 'rgba(167,139,250,0.42)' };
  if (type === 'webrtc')    return { color: '#a78bfa', glow: 'rgba(167,139,250,0.22)', border: 'rgba(167,139,250,0.42)' };
  return { color: '#94a3b8', glow: 'rgba(148,163,184,0.15)', border: 'rgba(148,163,184,0.3)' };
}

// ── AppCard sub-component ─────────────────────────────────────────────────────
function AppCard({
  app,
  liveness,
  onEdit,
  onToggle,
  onDelete,
  onUrls,
}: {
  app: Application;
  liveness: { reachable: boolean; latency_ms: number | null } | null;
  onEdit: (a: Application) => void;
  onToggle: (a: Application) => void;
  onDelete: (a: Application) => void;
  onUrls: (a: Application) => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!menuOpen) return;
    function handler(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as unknown as globalThis.Node)) setMenuOpen(false);
    }
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [menuOpen]);

  const firstEp = app.entry_points?.[0];
  const ep = epIconColor(firstEp?.entry_point_type ?? 'websocket');
  const accessMode = (firstEp?.access_policy as any)?.mode ?? 'token';

  // Liveness derived from multiplexed WS push (no per-card polling)
  const reachable = app.enabled ? (liveness?.reachable ?? null) : false;
  const latencyMs = liveness?.latency_ms ?? null;

  const statusColor = !app.enabled ? C.error : reachable === null ? C.textMuted : reachable ? C.green : '#f59e0b';
  const statusLabel = !app.enabled ? 'disabled' : reachable === null ? 'checking…' : reachable ? 'live' : 'unreachable';
  const statusBg    = !app.enabled ? 'rgba(255,180,171,0.1)' : reachable === null ? 'rgba(255,255,255,0.04)' : reachable ? 'rgba(74,222,128,0.08)' : 'rgba(245,158,11,0.08)';
  const statusBorder = !app.enabled ? 'rgba(255,180,171,0.3)' : reachable === null ? 'rgba(255,255,255,0.1)' : reachable ? C.greenBorder : 'rgba(245,158,11,0.4)';

  return (
    <div
      className="app-glass-card"
      style={{ borderRadius: 16, overflow: 'visible', display: 'flex', flexDirection: 'column', position: 'relative' }}
    >
      {/* Top section */}
      <div style={{ padding: '20px 20px 0', display: 'flex', flexDirection: 'column', gap: 14 }}>

        {/* Icon + name + menu row */}
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 14 }}>
          {/* Icon tile */}
          <div style={{
            width: 52, height: 52, borderRadius: 12, flexShrink: 0,
            background: `radial-gradient(circle at 30% 30%, ${ep.glow}, transparent 70%)`,
            border: `1px solid ${ep.border}`,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            position: 'relative',
          }}>
            <span className="material-symbols-outlined" style={{ fontSize: 24, color: ep.color }}>
              {EP_ICON[firstEp?.entry_point_type ?? ''] ?? 'extension'}
            </span>
            {app.entry_points.length > 1 && (
              <span style={{
                position: 'absolute', top: -6, right: -6,
                minWidth: 18, height: 18, borderRadius: 9,
                background: '#00d1ff', color: '#021520',
                fontSize: 10, fontWeight: 700,
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                padding: '0 4px',
              }}>{app.entry_points.length}</span>
            )}
          </div>

          {/* Name + slugs + type badge */}
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontWeight: 700, fontSize: 15, color: C.text, fontFamily: 'Geist, sans-serif', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', marginBottom: 3 }}>
              {app.name}
            </div>
            <div style={{ marginBottom: 5 }}>
              {app.entry_points.map(epRow => (
                <div key={epRow.id} style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'flex', alignItems: 'center', gap: 4 }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 11, color: epIconColor(epRow.entry_point_type).color, flexShrink: 0 }}>{EP_ICON[epRow.entry_point_type] ?? 'bolt'}</span>
                  {epRow.slug}
                </div>
              ))}
            </div>
            <span style={{
              display: 'inline-flex', alignItems: 'center', gap: 4,
              padding: '2px 7px', borderRadius: 20, fontSize: 10, fontWeight: 700,
              background: 'var(--tm-filter-bg)', color: ep.color,
              border: `1px solid ${ep.border}`,
            }}>
              {EP_LABEL[firstEp?.entry_point_type ?? ''] ?? firstEp?.entry_point_type ?? '—'}
            </span>
          </div>

          {/* Three-dot menu */}
          <div ref={menuRef} style={{ position: 'relative', flexShrink: 0 }} onClick={e => e.stopPropagation()}>
            <button
              onClick={() => setMenuOpen(v => !v)}
              style={{ width: 30, height: 30, borderRadius: 7, cursor: 'pointer', background: 'var(--tm-btn-2-bg)', border: '1px solid var(--tm-btn-2-border)', color: 'var(--tm-card-text-muted)', display: 'flex', alignItems: 'center', justifyContent: 'center', transition: 'color 150ms ease' }}
            >
              <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
                <circle cx="8" cy="3" r="1.5"/><circle cx="8" cy="8" r="1.5"/><circle cx="8" cy="13" r="1.5"/>
              </svg>
            </button>
            {menuOpen && (
              <div style={{
                position: 'absolute', top: 34, right: 0, zIndex: 50, minWidth: 130,
                background: 'var(--tm-menu-bg)', border: '1px solid var(--tm-menu-border)',
                borderRadius: 10, boxShadow: '0 8px 32px rgba(0,0,0,0.35)', overflow: 'hidden',
              }}>
                <button
                  onClick={() => { setMenuOpen(false); onDelete(app); }}
                  style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', padding: '10px 14px', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13, color: C.error, fontWeight: 600 }}
                  onMouseEnter={e => (e.currentTarget.style.background = 'rgba(255,180,171,0.08)')}
                  onMouseLeave={e => (e.currentTarget.style.background = 'none')}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                  Delete
                </button>
              </div>
            )}
          </div>
        </div>

        {/* Live status bar */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '8px 12px', borderRadius: 10,
          background: statusBg, border: `1px solid ${statusBorder}`,
        }}>
          {app.enabled && (
            <span style={{
              width: 7, height: 7, borderRadius: '50%', flexShrink: 0,
              background: statusColor,
              boxShadow: reachable ? `0 0 7px ${statusColor}` : 'none',
            }} />
          )}
          <span style={{ fontSize: 12, fontWeight: 700, color: statusColor }}>{statusLabel}</span>
          {reachable && latencyMs != null && (
            <span style={{ fontSize: 11, color: C.textMuted, marginLeft: 'auto' }}>{latencyMs}ms</span>
          )}
        </div>

        {/* Info tiles: orchestrator + access */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, paddingBottom: 16 }}>
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'var(--tm-filter-bg)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16, color: '#a78bfa', flexShrink: 0 }}>hub</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Orchestrator</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {app.orchestrator_name ?? <span style={{ color: C.textMuted, fontStyle: 'italic' }}>none</span>}
              </div>
            </div>
          </div>
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'var(--tm-filter-bg)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16, color: accessMode === 'public' ? C.green : '#f59e0b', flexShrink: 0 }}>
              {accessMode === 'public' ? 'lock_open' : 'lock'}
            </span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Access</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600 }}>{accessMode === 'public' ? 'Public' : 'Token'}</div>
            </div>
          </div>
        </div>
      </div>

      {/* Action buttons */}
      {(() => {
        const webrtcEp = app.entry_points.find(e => e.entry_point_type === 'webrtc');
        return (
          <div style={{
            borderTop: '1px solid var(--tm-divider)', padding: '10px 14px', display: 'grid',
            gridTemplateColumns: webrtcEp ? '2fr 1fr 1fr 1fr' : '2fr 1fr 1fr',
            gap: 8,
          }}>
            {/* Open builder — primary, full-width feel */}
            <button className="app-card-btn app-card-btn--open" onClick={() => onEdit(app)}
              style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 15 }}>open_in_new</span>
              Open Builder
            </button>
            {webrtcEp && (
              <button
                className="app-card-btn"
                onClick={() => window.open(`/apps/${webrtcEp.slug}/voice`, '_blank', 'noopener')}
                title="Open voice room"
                style={{
                  background: 'rgba(167,139,250,0.1)',
                  color: '#a78bfa',
                  border: '1px solid rgba(167,139,250,0.3)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
                }}
                onMouseEnter={e => { e.currentTarget.style.background = 'rgba(167,139,250,0.2)'; e.currentTarget.style.borderColor = 'rgba(167,139,250,0.5)'; }}
                onMouseLeave={e => { e.currentTarget.style.background = 'rgba(167,139,250,0.1)'; e.currentTarget.style.borderColor = 'rgba(167,139,250,0.3)'; }}
              >
                <span className="material-symbols-outlined" style={{ fontSize: 14 }}>mic</span>
                Voice
              </button>
            )}
            <button className="app-card-btn app-card-btn--urls" onClick={() => onUrls(app)}>
              URLs
            </button>
            {app.enabled ? (
              <button className="app-card-btn app-card-btn--toggle-on" onClick={() => onToggle(app)}>Disable</button>
            ) : (
              <button className="app-card-btn app-card-btn--toggle-off" onClick={() => onToggle(app)}>Enable</button>
            )}
          </div>
        );
      })()}
    </div>
  );
}

type AppLiveness = { reachable: boolean; latency_ms: number | null };

function useDashAppStatuses(token: string | null): Record<string, AppLiveness> {
  const [statuses, setStatuses] = useState<Record<string, AppLiveness>>({});

  useEffect(() => {
    if (!token) return;
    const wsUrl = `${window.location.origin.replace(/^http/, 'ws').replace(/^https/, 'wss')}/ws/dashboard?token=${token}`;
    let ws: WebSocket;
    let dead = false;

    function connect() {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        ws.send(JSON.stringify({ type: 'subscribe', channels: ['apps'] }));
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.channel === 'apps' && msg.event?.type === 'app_status') {
            setStatuses(prev => ({ ...prev, ...msg.event.statuses }));
          }
        } catch {}
      };
      ws.onclose = () => {
        if (!dead) setTimeout(connect, 4000);
      };
      ws.onerror = () => ws.close();
    }

    connect();
    return () => {
      dead = true;
      ws?.close();
    };
  }, [token]);

  return statuses;
}

function ListView({
  list, orchestrators, loading, onNew, onEdit, onToggle, onDelete,
}: {
  list: Application[];
  orchestrators: OrchestratorFull[];
  loading: boolean;
  onNew: () => void;
  onEdit: (app: Application) => void;
  onToggle: (app: Application) => void;
  onDelete: (app: Application) => void;
}) {
  const [urlModalApp, setUrlModalApp] = useState<Application | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);

  // Read JWT for WS auth — same cookie the rest of the app uses
  const [token, setToken] = useState<string | null>(null);
  useEffect(() => {
    fetch('/api/auth/token').then(r => r.ok ? r.json() : null).then(d => {
      if (d?.token) setToken(d.token);
    }).catch(() => {});
  }, []);

  const appStatuses = useDashAppStatuses(token);

  function copy(val: string, id: string) {
    navigator.clipboard?.writeText(val).catch(() => {});
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 1800);
  }

  return (
    <div style={{ marginLeft: 260, flex: 1, background: C.bg, minHeight: '100vh' }}>
      <style>{APP_CARD_STYLES}</style>

      {/* Page header */}
      <div style={{ padding: '40px 32px 24px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div>
          <h2 style={{ fontSize: 40, fontWeight: 800, color: C.text, margin: '0 0 6px 0', letterSpacing: '-0.03em', lineHeight: 1.1 }}>
            Applications
          </h2>
          <p style={{ fontSize: 14, color: C.textMuted, margin: 0 }}>
            Compose orchestrators and entry points into deployable agentic applications.
          </p>
        </div>
        <button
          onClick={onNew}
          style={{
            display: 'flex', alignItems: 'center', gap: 8,
            padding: '12px 24px', borderRadius: 8, border: 'none', cursor: 'pointer',
            background: '#00d1ff', color: '#000', fontSize: 14, fontWeight: 700,
            boxShadow: '0 0 20px rgba(0,209,255,0.4)',
          }}
        >
          <span style={{ fontSize: 18, lineHeight: 1 }}>+</span>
          New Application
        </button>
      </div>

      {/* Card grid */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 24, padding: '0 32px 48px' }}>
        {loading && (
          <div style={{ gridColumn: '1 / -1', padding: 80, textAlign: 'center', color: C.textMuted, fontSize: 14 }}>
            Loading…
          </div>
        )}

        {!loading && list.length === 0 && (
          <div
            className="app-deploy-card"
            onClick={onNew}
            style={{
              borderRadius: 16, border: '2px dashed rgba(99,102,241,0.35)',
              background: 'rgba(99,102,241,0.02)',
              display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
              gap: 14, cursor: 'pointer', minHeight: 220, transition: 'border-color 200ms ease, background 200ms ease',
            }}
          >
            <div style={{ width: 52, height: 52, borderRadius: 14, border: '2px dashed rgba(99,102,241,0.5)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <span className="material-icons" style={{ fontSize: 26, color: '#818cf8' }}>add</span>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, color: '#818cf8' }}>New Application</div>
          </div>
        )}

        {!loading && list.map((app) => (
          <AppCard
            key={app.id}
            app={app}
            liveness={appStatuses[app.entry_points?.[0]?.slug ?? ''] ?? null}
            onEdit={onEdit}
            onToggle={onToggle}
            onDelete={onDelete}
            onUrls={setUrlModalApp}
          />
        ))}

        {/* Deploy / New card — always last */}
        {!loading && list.length > 0 && (
          <div
            className="app-deploy-card"
            onClick={onNew}
            style={{
              borderRadius: 16, border: '2px dashed rgba(99,102,241,0.35)',
              background: 'rgba(99,102,241,0.02)',
              display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
              gap: 14, cursor: 'pointer', minHeight: 220, transition: 'border-color 200ms ease, background 200ms ease',
            }}
          >
            <div style={{ width: 52, height: 52, borderRadius: 14, border: '2px dashed rgba(99,102,241,0.5)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <span className="material-icons" style={{ fontSize: 26, color: '#818cf8' }}>add</span>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, color: '#818cf8' }}>New Application</div>
          </div>
        )}
      </div>

      {/* URL Modal */}
      {urlModalApp && (
        <div
          style={{ position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', background: 'rgba(5,20,36,0.85)', zIndex: 200, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
          onClick={() => setUrlModalApp(null)}
        >
          <div
            style={{ ...glass, borderRadius: 16, padding: '28px 32px', minWidth: 480, maxWidth: 600, position: 'relative' }}
            onClick={e => e.stopPropagation()}
          >
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
              <div style={{ fontSize: 14, fontWeight: 700, color: C.text, fontFamily: 'Geist, sans-serif' }}>
                Entry Point URLs — {urlModalApp.name}
              </div>
              <button onClick={() => setUrlModalApp(null)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center' }}>
                <span className="material-icons" style={{ fontSize: 20 }}>close</span>
              </button>
            </div>
            {urlModalApp.entry_points.map((epRow, epIdx) => {
              const urls: Array<{ label: string; val: string }> = [];
              if (epRow.entry_point_type === 'websocket') urls.push({ label: 'WebSocket', val: `ws://<host>:8088/apps/${epRow.slug}/ws` });
              if (epRow.entry_point_type === 'sse') urls.push({ label: 'SSE', val: `http://<host>:8088/apps/${epRow.slug}/sse` }, { label: 'REST', val: `http://<host>:8088/apps/${epRow.slug}` });
              if (epRow.entry_point_type === 'webrtc') urls.push(
                { label: 'Voice Page', val: `http://<host>:8088/apps/${epRow.slug}/voice` },
                { label: 'Token API', val: `http://<host>:8088/apps/${epRow.slug}/webrtc/token` },
              );
              const epColor = epIconColor(epRow.entry_point_type);
              return (
                <div key={epRow.id} style={{ marginBottom: epIdx < urlModalApp.entry_points.length - 1 ? 18 : 0 }}>
                  {urlModalApp.entry_points.length > 1 && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 14, color: epColor.color }}>{EP_ICON[epRow.entry_point_type] ?? 'bolt'}</span>
                      <span style={{ fontSize: 11, fontWeight: 700, color: epColor.color, fontFamily: 'JetBrains Mono, monospace' }}>{epRow.slug}</span>
                      <span style={{ fontSize: 10, color: C.textMuted }}>· {(epRow.access_policy as any)?.mode === 'public' ? 'public' : 'token'}</span>
                    </div>
                  )}
                  {urls.map(({ label, val }) => {
                    const cid = `modal_${epRow.id}_${label}`;
                    return (
                      <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
                        <span style={{ fontSize: 11, color: C.textMuted, minWidth: 100, fontFamily: 'Inter, sans-serif' }}>{label}</span>
                        <code style={{ flex: 1, fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: C.text, background: C.surfaceContainer, padding: '5px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{val}</code>
                        <button
                          onClick={() => copy(val, cid)}
                          style={{ display: 'inline-flex', alignItems: 'center', gap: 5, padding: '4px 12px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`, background: 'transparent', cursor: 'pointer', fontSize: 12, fontWeight: 600, color: copiedId === cid ? C.green : C.textMuted, transition: 'all 0.15s' }}
                        >
                          {copiedId === cid ? 'Copied!' : 'Copy'}
                        </button>
                      </div>
                    );
                  })}
                </div>
              );
            })}
            <div style={{ marginTop: 14, fontSize: 11, color: C.textMuted, fontFamily: 'Inter, sans-serif' }}>
              {urlModalApp.entry_points.every(ep => (ep.access_policy as any)?.mode === 'public')
                ? 'No auth required — public access'
                : 'Bearer token required — use /admin/tokens to create one'}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ── Page root ─────────────────────────────────────────────────────────────────
export default function ApplicationsPage() {
  const [list, setList] = useState<Application[]>([]);
  const [orchestrators, setOrchestrators] = useState<OrchestratorFull[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [view, setView] = useState<'list' | 'builder'>('list');
  const [editApp, setEditApp] = useState<Application | null>(null);

  async function load() {
    setLoading(true);
    Promise.all([themApi.applications(), themApi.orchestrators(), themApi.agents()])
      .then(([apps, orchs, ags]) => { setList(apps); setOrchestrators(orchs); setAgents(ags); })
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(); }, []);

  async function handleToggle(app: Application) {
    try { await themApi.updateApplication(app.id, { enabled: !app.enabled }); await load(); } catch {/* ignore */}
  }

  async function handleDelete(app: Application) {
    if (!confirm(`Delete application "${app.name}"?`)) return;
    try { await themApi.deleteApplication(app.id); await load(); } catch {/* ignore */}
  }

  function openBuilder(app: Application | null) {
    setEditApp(app);
    setView('builder');
  }

  function backToList() {
    setView('list');
    setEditApp(null);
  }

  async function onBuilderSaved() {
    await load();
    // Stay in builder — let user navigate back manually if they want
  }

  if (view === 'builder') {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <ReactFlowProvider>
              <BuilderView
                app={editApp}
                orchestrators={orchestrators}
                agents={agents}
                onBack={backToList}
                onSaved={onBuilderSaved}
                onOrchestratorsChange={setOrchestrators}
                onAgentsChange={setAgents}
              />
            </ReactFlowProvider>
          </div>
        </div>
      </AuthGuard>
    );
  }

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
        <Sidebar />
        <ListView
          list={list}
          orchestrators={orchestrators}
          loading={loading}
          onNew={() => openBuilder(null)}
          onEdit={(app) => openBuilder(app)}
          onToggle={handleToggle}
          onDelete={handleDelete}
        />
      </div>
    </AuthGuard>
  );
}
