'use client';
import { useEffect, useState, useCallback, useRef, useMemo, DragEvent } from 'react';
// eslint-disable-next-line @typescript-eslint/no-require-imports, @typescript-eslint/no-explicit-any
const dagre: any = (typeof window !== 'undefined' ? require('dagre') : null);
import Sidebar from '@/components/Sidebar';
import ChromaGrid from '@/components/ChromaGrid';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Application, type Agent, type EntryPoint, type MiddlewareDef, type AppOrchestratorOut, type AppOrchestratorSummary, type SessionInfo, type MonitoringConfig, type AppDefinition, type AppDefinitionDoc, type ComponentDefinitionSummary, type ValidationReport } from '@/lib/api';
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

// deleteNodeRef removed — nodes use useReactFlow().deleteElements() directly

// ── Types ────────────────────────────────────────────────────────────────────
const ENTRY_POINT_TYPES = ['websocket', 'sse', 'webrtc', 'a2a', 'voice'] as const;
type EntryPointType = typeof ENTRY_POINT_TYPES[number];

interface EntryPointData { label: string; epType: EntryPointType; accessMode: 'token' | 'public'; slug: string; appName?: string; convTokenLimit?: string; maxConcurrentSessions?: string; queueTimeout?: string; queueMessage?: string; _epId?: string; [key: string]: unknown; }
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
  llmApiKey: string | null;
  maxIterations: number;
  historyWindow: number;
  delegatable: boolean;
  kind: string;
  budgetTokens: number | null;
  transcriptionProvider: string | null;
  transcriptionModel: string | null;
  transcriptionApiKey: string | null;
  ttsProvider: string | null;
  ttsVoice: string | null;
  ttsApiKey: string | null;
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
  @keyframes nodeShake {
    0%, 100% { transform: translateX(0); }
    15% { transform: translateX(-6px); }
    30% { transform: translateX(6px); }
    45% { transform: translateX(-4px); }
    60% { transform: translateX(4px); }
    75% { transform: translateX(-2px); }
    90% { transform: translateX(2px); }
  }
  .node-error-ring {
    outline: 2px solid rgba(248,113,113,0.85) !important;
    outline-offset: 3px;
    border-radius: 50% !important;
    box-shadow: 0 0 14px rgba(248,113,113,0.45) !important;
  }
  .node-shake {
    animation: nodeShake 0.6s ease-in-out;
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
  .comp-panel { scrollbar-width: thin; scrollbar-color: rgba(255,255,255,0.1) transparent; }
  .comp-panel::-webkit-scrollbar { width: 4px; }
  .comp-panel::-webkit-scrollbar-track { background: transparent; }
  .comp-panel::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.1); border-radius: 4px; }
  .comp-panel::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,0.22); }
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
function EntryPointNode({ id, data, selected }: { id: string; data: EntryPointData & { _scanning?: boolean; _error?: boolean; _shake?: boolean; _errorMsg?: string }; selected?: boolean }) {
  const { deleteElements } = useReactFlow();
  const epKind = data.epType || (data as unknown as Record<string, unknown>).protocol as string | undefined;
  const slugMissing = !data.slug;
  const hasError = data._error || data._shake;
  const isVoice = epKind === 'voice';
  const accent = hasError ? '#f87171' : slugMissing ? '#f59e0b' : isVoice ? C.amber : C.cyan;
  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };
  const msIcon = EP_MS_ICON[epKind ?? ''] ?? 'bolt';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}
      title={data._errorMsg || undefined}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteElements({ nodes: [{ id }] }); }}
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
      <div
        className={`${hasError ? 'node-error-ring' : ''} ${data._shake ? 'node-shake' : ''}`}
        style={{
          width: 56, height: 56, borderRadius: '50%',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: selected ? `rgba(0,240,255,0.10)` : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
          border: selected ? `2px solid ${accent}` : hasError ? '2px solid #f87171' : '2px solid transparent',
          boxShadow: selected ? `0 0 14px rgba(0,240,255,0.35), inset 0 0 8px rgba(0,240,255,0.08)` : data._scanning ? '0 0 20px rgba(0,240,255,0.5)' : 'none',
          transition: 'all 0.18s ease',
        }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>{msIcon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center' }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, transition: 'color 0.18s' }}>
          {data.label || (epKind === 'sse' ? 'SSE' : epKind === 'voice' ? 'Voice' : epKind === 'webrtc' ? 'WebRTC' : epKind === 'a2a' ? 'A2A' : 'WebSocket')}
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
function OrchestratorNode({ id, data, selected }: { id: string; data: OrchestratorData & { _scanning?: boolean; _error?: boolean; _shake?: boolean; _errorMsg?: string }; selected?: boolean }) {
  const { deleteElements } = useReactFlow();
  const isInternal = INTERNAL_ORCHESTRATOR_NAMES.has(data.name);
  const hasError = data._error || data._shake;
  const accent = hasError ? '#f87171' : isInternal ? '#a0f0d0' : C.purple;
  const selGlow = isInternal ? 'rgba(160,240,208,0.35)' : 'rgba(208,188,255,0.35)';
  const selBg   = isInternal ? 'rgba(160,240,208,0.10)' : 'rgba(208,188,255,0.10)';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}
      title={data._errorMsg || undefined}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteElements({ nodes: [{ id }] }); }}
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
      <div
        className={`${hasError ? 'node-error-ring' : ''} ${data._shake ? 'node-shake' : ''}`}
        style={{
          width: 56, height: 56, borderRadius: '50%',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: selected ? selBg : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
          border: selected ? `2px solid ${accent}` : hasError ? '2px solid #f87171' : '2px solid transparent',
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
function AgentNode({ id, data, selected }: { id: string; data: AgentData & { _scanning?: boolean; _error?: boolean; _shake?: boolean; _errorMsg?: string }; selected?: boolean }) {
  const { deleteElements } = useReactFlow();
  const isInternal = data.tags?.includes('internal') ?? false;
  const hasError = data._error || data._shake;
  const accent = hasError ? '#f87171' : isInternal ? '#a0f0d0' : C.green;
  const selGlow = isInternal ? 'rgba(160,240,208,0.35)' : 'rgba(74,222,128,0.35)';
  const selBg   = isInternal ? 'rgba(160,240,208,0.10)' : 'rgba(74,222,128,0.10)';
  const displayName = (data as unknown as Record<string, unknown>).display_name as string | undefined || data.displayName;
  const icon = data.icon || agentIconForLibrary({ slug: data.name, icon: data.icon } as any);
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}
      title={data._errorMsg || undefined}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteElements({ nodes: [{ id }] }); }}
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
      <div
        className={`${hasError ? 'node-error-ring' : ''} ${data._shake ? 'node-shake' : ''}`}
        style={{
          width: 56, height: 56, borderRadius: '50%',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: selected ? selBg : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
          border: selected ? `2px solid ${accent}` : hasError ? '2px solid #f87171' : '2px solid transparent',
          boxShadow: selected ? `0 0 14px ${selGlow}, inset 0 0 8px ${selGlow}` : data._scanning ? '0 0 20px rgba(0,240,255,0.5)' : 'none',
          transition: 'all 0.18s ease',
        }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>{icon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 110 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', transition: 'color 0.18s' }}>
          {displayName}
        </div>
      </div>
    </div>
  );
}

// MiddlewareNode — amber-colored, shield for guard / bolt for cache
function MiddlewareNode({ id, data, selected }: { id: string; data: MiddlewareData & { _scanning?: boolean; _error?: boolean; _shake?: boolean; _errorMsg?: string }; selected?: boolean }) {
  const { deleteElements } = useReactFlow();
  const hasError = data._error || data._shake;
  const accent = hasError ? '#f87171' : C.amber;
  const selGlow = 'rgba(245,158,11,0.35)';
  const selBg   = 'rgba(245,158,11,0.10)';
  const icon = data.kind === 'guard' ? 'shield' : 'bolt';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}
      title={data._errorMsg || undefined}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteElements({ nodes: [{ id }] }); }}
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
      <div
        className={`${hasError ? 'node-error-ring' : ''} ${data._shake ? 'node-shake' : ''}`}
        style={{
          width: 56, height: 56, borderRadius: '50%',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: selected ? selBg : data._scanning ? 'rgba(245,158,11,0.08)' : 'transparent',
          border: selected ? `2px solid ${accent}` : hasError ? '2px solid #f87171' : '2px solid transparent',
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

function fallbackCopy(text: string) {
  const el = document.createElement('textarea');
  el.value = text;
  el.style.cssText = 'position:fixed;top:-9999px;left:-9999px;opacity:0';
  document.body.appendChild(el);
  el.select();
  document.execCommand('copy');
  document.body.removeChild(el);
}

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

// ── Canvas V2 node data interfaces ───────────────────────────────────────────
interface OrchNodeData { _kind: 'orchestrator'; instance_id: string; display_name: string; definition_ref: import('@/lib/api').DefinitionRef; definition_id?: string; config: Record<string, unknown>; _error?: boolean; _shake?: boolean; _errorMsg?: string; }
interface AgentNodeData { _kind: 'agent'; instance_id: string; display_name: string; description: string; definition_ref: import('@/lib/api').DefinitionRef; definition_id?: string; config: Record<string, unknown>; secret_bindings?: Record<string, string>; icon?: string; _error?: boolean; _shake?: boolean; _errorMsg?: string; }
interface MwNodeData { _kind: 'middleware'; instance_id: string; display_name: string; definition_ref: import('@/lib/api').DefinitionRef; definition_id?: string; config: Record<string, unknown>; _error?: boolean; _shake?: boolean; _errorMsg?: string; }
interface EpNodeData { _kind: 'ep'; instance_id: string; slug: string; protocol: 'websocket' | 'sse' | 'webrtc' | 'a2a' | 'voice'; label: string; config: Record<string, unknown>; _error?: boolean; _shake?: boolean; _errorMsg?: string; }
type CanvasNodeData = OrchNodeData | AgentNodeData | MwNodeData | EpNodeData;

// ── Canvas V2 serialization helpers ──────────────────────────────────────────
function sanitize(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/_+/g, '_').replace(/^_|_$/g, '').slice(0, 20);
}

function genInstanceId(kind: 'orchestrator' | 'agent' | 'middleware' | 'ep', defName: string | undefined, existing: Set<string>): string {
  let base: string;
  if (kind === 'orchestrator') base = 'orch';
  else if (kind === 'ep') base = 'ep_' + sanitize(defName ?? 'ep');
  else if (kind === 'agent') base = 'agent_' + sanitize(defName ?? 'agent');
  else base = 'mw_' + sanitize(defName ?? 'mw');
  let n = 1;
  while (existing.has(`${base}_${n}`)) n++;
  return `${base}_${n}`;
}

function canvasToDoc(nodes: Node[], edges: Edge[], name?: string): import('@/lib/api').AppDefinitionDoc {
  const nodeTypeById = new Map(nodes.map(n => [n.id, n.type]));
  const rootByEp = new Map<string, string>();
  edges.forEach(e => {
    if (nodeTypeById.get(e.source) === 'entryPoint' && nodeTypeById.get(e.target) === 'orchestrator') {
      rootByEp.set(e.source, e.target);
    }
  });
  const components: import('@/lib/api').ComponentInstance[] = [];
  const entry_points: import('@/lib/api').EPInstance[] = [];
  const connections: import('@/lib/api').ConnectionDef[] = [];
  nodes.forEach(n => {
    if (n.type === 'orchestrator') {
      const d = n.data as unknown as OrchNodeData;
      components.push({ instance_id: n.id, name: n.id, definition_ref: d.definition_ref, definition_id: d.definition_id, config: { ...d.config, display_name: d.display_name } });
    } else if (n.type === 'agent') {
      const d = n.data as unknown as AgentNodeData;
      const comp: import('@/lib/api').ComponentInstance = { instance_id: n.id, definition_ref: d.definition_ref, definition_id: d.definition_id, config: d.config };
      if (d.secret_bindings && Object.keys(d.secret_bindings).length) comp.secret_bindings = d.secret_bindings;
      components.push(comp);
    } else if (n.type === 'middleware') {
      const d = n.data as unknown as MwNodeData;
      components.push({ instance_id: n.id, definition_ref: d.definition_ref, definition_id: d.definition_id, config: d.config });
    } else if (n.type === 'entryPoint') {
      const d = n.data as unknown as EpNodeData;
      entry_points.push({ instance_id: n.id, slug: d.slug, protocol: d.protocol, root: rootByEp.get(n.id) ?? '', config: d.config ?? {} });
    }
  });
  edges.forEach(e => {
    const srcType = nodeTypeById.get(e.source);
    const tgtType = nodeTypeById.get(e.target);
    if (srcType === 'entryPoint' && tgtType === 'orchestrator') return;
    if (srcType === 'orchestrator' && tgtType === 'agent') connections.push({ source: e.source, target: e.target, type: 'tool' });
    if (srcType === 'orchestrator' && tgtType === 'orchestrator') connections.push({ source: e.source, target: e.target, type: 'delegation' });
  });
  return { schema_version: 2 as const, name, components, entry_points, connections };
}

function docToCanvas(doc: import('@/lib/api').AppDefinitionDoc, componentDefs: ComponentDefinitionSummary[], layout: Record<string, { x: number; y: number }>, agentIconBySlug?: Map<string, string>): { nodes: Node[]; edges: Edge[] } {
  const defById = new Map(componentDefs.map(cd => [cd.id, cd]));
  const refKey = (r: import('@/lib/api').DefinitionRef) => `${r.kind}:${r.namespace}:${r.name}:${r.version}`;
  const defByRef = new Map(componentDefs.map(cd => [refKey({ kind: cd.kind, namespace: cd.namespace, name: cd.name, version: cd.version }), cd]));
  const nodes: Node[] = [];
  const edges: Edge[] = [];
  (doc.components ?? []).forEach(c => {
    const cd = defById.get(c.definition_id ?? '') ?? defByRef.get(refKey(c.definition_ref));
    const pos = layout[c.instance_id] ?? { x: 0, y: 0 };
    if (c.definition_ref.kind === 'orchestrator') {
      nodes.push({ id: c.instance_id, type: 'orchestrator', position: pos, data: { _kind: 'orchestrator', instance_id: c.instance_id, display_name: (c.config.display_name as string) ?? cd?.display_name ?? c.instance_id, definition_ref: c.definition_ref, definition_id: c.definition_id, config: c.config } as unknown as Record<string, unknown> });
    } else if (c.definition_ref.kind === 'agent') {
      const agentIcon = agentIconBySlug?.get(c.definition_ref.name);
      nodes.push({ id: c.instance_id, type: 'agent', position: pos, data: { _kind: 'agent', instance_id: c.instance_id, display_name: cd?.display_name ?? c.instance_id, description: cd?.description ?? '', definition_ref: c.definition_ref, definition_id: c.definition_id, config: c.config, secret_bindings: c.secret_bindings, icon: agentIcon } as unknown as Record<string, unknown> });
    } else if (c.definition_ref.kind === 'middleware') {
      nodes.push({ id: c.instance_id, type: 'middleware', position: pos, data: { _kind: 'middleware', instance_id: c.instance_id, display_name: cd?.display_name ?? c.instance_id, definition_ref: c.definition_ref, definition_id: c.definition_id, config: c.config } as unknown as Record<string, unknown> });
    }
  });
  (doc.entry_points ?? []).forEach(ep => {
    const pos = layout[ep.instance_id] ?? { x: 0, y: 0 };
    nodes.push({ id: ep.instance_id, type: 'entryPoint', position: pos, data: { _kind: 'ep', instance_id: ep.instance_id, slug: ep.slug, protocol: ep.protocol, label: EP_META[ep.protocol]?.title ?? ep.protocol, config: ep.config ?? {} } as unknown as Record<string, unknown> });
    if (ep.root) edges.push({ id: `e_${ep.instance_id}_${ep.root}`, source: ep.instance_id, target: ep.root, type: 'default' });
  });
  (doc.connections ?? []).forEach(conn => {
    if (conn.type === 'tool' || conn.type === 'delegation') {
      edges.push({ id: `e_${conn.source}_${conn.target}`, source: conn.source, target: conn.target, type: 'default' });
    }
  });
  if (Object.keys(layout).length === 0 && nodes.length > 0) {
    const laid = applyDagreLayout(nodes, edges);
    laid.forEach((ln, i) => { nodes[i].position = ln.position; });
  }
  return { nodes, edges };
}

function computeLogoState(s: { loaded: boolean; isDirty: boolean; busy: boolean; lastResult: 'none' | 'valid' | 'invalid' | 'warn' }): LogoState {
  if (s.busy) return 'thinking';
  if (s.lastResult === 'invalid') return 'error';
  if (s.lastResult === 'warn') return 'warning';
  if (s.lastResult === 'valid') return 'success';
  if (!s.loaded) return 'idle';
  if (s.isDirty) return 'dirty';
  return 'idle';
}

function buildNodesFromApp(
  app: Application,
  agents: Agent[],
): { nodes: Node[]; edges: Edge[] } {
  // Canvas layout is a ref-keyed position map: {"ep:<slug>": {x,y}, "orch:<ao_id>": {x,y}, ...}
  // Reconstruct logical graph from typed tables, then apply saved positions (if any).
  const layout = (app.canvas?.layout ?? {}) as Record<string, { x: number; y: number }>;

  const nodes: Node[] = [];
  const edges: Edge[] = [];

  // Build a lookup from app_orchestrator id → AppOrchestratorOut
  // app_orchestrators from the list API returns a summary; the canvas builder
  // only needs the id/name to find nodes — full fields come from entry_points.
  const aoById = new Map<string, AppOrchestratorOut>();
  (app.app_orchestrators ?? []).forEach(ao => aoById.set(ao.id, ao as unknown as AppOrchestratorOut));
  // Also pick up inline app_orchestrator objects from entry_points
  (app.entry_points ?? []).forEach(ep => {
    if (ep.app_orchestrator) aoById.set(ep.app_orchestrator.id, ep.app_orchestrator);
  });

  // Track which app_orchestrator node ids have already been emitted
  const emittedOrchIds = new Set<string>();
  // Track which agent node ids have been emitted (agent_{agentId}_{aoId})
  const emittedAgentNodeIds = new Set<string>();

  // Helper: get saved position or null (dagre will fill in missing ones).
  // Checks plain node-id key first (new format), then legacy prefixed key.
  const pos = (nodeId: string, legacyKey?: string) =>
    layout[nodeId] ?? (legacyKey ? layout[legacyKey] : null) ?? null;

  // One EP node per entry-point row
  (app.entry_points ?? []).forEach((ep, idx) => {
    const epId = `ep_${ep.slug}`;
    nodes.push({
      id: epId, type: 'entryPoint',
      position: pos(epId, `ep:${ep.slug}`) ?? { x: 150 + idx * 240, y: 60 },
      data: {
        label: app.name,
        epType: (ep.entry_point_type as EntryPointType) ?? 'websocket',
        accessMode: ((ep.access_policy as any)?.mode ?? 'token') as 'token' | 'public',
        slug: ep.slug,
        appName: app.name,
        convTokenLimit: ep.conversation_token_limit != null ? String(ep.conversation_token_limit) : '',
        maxConcurrentSessions: ep.max_concurrent_sessions != null ? String(ep.max_concurrent_sessions) : '',
        queueTimeout: ep.queue_timeout_seconds != null ? String(ep.queue_timeout_seconds) : '',
        queueMessage: ep.queue_message ?? '',
        _epId: ep.id,
      } satisfies EntryPointData,
    });

    const aoId = ep.app_orchestrator_id ?? ep.app_orchestrator?.id;
    if (aoId) {
      const orchNodeId = `orch_${aoId}`;
      edges.push({ id: `e_ep_orch_${ep.slug}`, source: epId, target: orchNodeId, animated: true, style: EDGE_STYLE });

      if (!emittedOrchIds.has(aoId)) {
        emittedOrchIds.add(aoId);
        const ao = aoById.get(aoId);
        if (ao) {
          nodes.push({
            id: orchNodeId, type: 'orchestrator',
            position: pos(orchNodeId, `orch:${aoId}`) ?? (ao.node_id ? pos(ao.node_id, `orch:${ao.node_id}`) : null) ?? { x: 250, y: 220 },
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
              transcriptionProvider: ao.transcription_provider ?? null,
              transcriptionModel: ao.transcription_model ?? null,
              transcriptionApiKey: null,
              ttsProvider: ao.tts_provider ?? null,
              ttsVoice: ao.tts_voice ?? null,
              ttsApiKey: null,
            } as OrchestratorData,
          });

          // Emit agent nodes + orch→agent edges
          const allowedAgents = agents.filter(a => ao.allowed_agent_ids.includes(a.id));
          const spread = Math.max(allowedAgents.length * 180, 400);
          const startX = 300 - spread / 2 + 90;
          allowedAgents.forEach((agent, i) => {
            const agentNodeId = `agent_${agent.id}`;
            if (!emittedAgentNodeIds.has(agentNodeId)) {
              emittedAgentNodeIds.add(agentNodeId);
              nodes.push({
                id: agentNodeId, type: 'agent',
                position: pos(agentNodeId, `agent:${agent.id}`) ?? { x: startX + i * 190, y: 420 },
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
            edges.push({ id: `e_orch_agent_${aoId}_${agent.id}`, source: orchNodeId, target: agentNodeId, animated: true, style: EDGE_STYLE });
          });
        }
      }
    }
  });

  // Only apply dagre auto-layout if we have NO saved positions (new app or import without canvas)
  const hasLayout = Object.keys(layout).length > 0;
  if (!hasLayout) {
    const laid = applyDagreLayout(nodes, edges);
    return { nodes: laid, edges };
  }
  return { nodes, edges };
}

// ── Helpers ───────────────────────────────────────────────────────────────────
function agentIconForLibrary(a: { slug?: string; icon?: string | null }): string {
  if (a.icon) return a.icon;
  const slug = (a.slug ?? '').toLowerCase();
  if (slug.includes('vision') || slug.includes('image') || slug.includes('ocr') || slug.includes('photo')) return 'image_search';
  if (slug.includes('security') || slug.includes('scan') || slug.includes('audit')) return 'security';
  if (slug.includes('code') || slug.includes('dev') || slug.includes('engineer')) return 'code';
  if (slug.includes('search') || slug.includes('web') || slug.includes('browse')) return 'travel_explore';
  if (slug.includes('doc') || slug.includes('write') || slug.includes('text') || slug.includes('summar')) return 'description';
  if (slug.includes('data') || slug.includes('analyt') || slug.includes('sql') || slug.includes('db')) return 'table_chart';
  if (slug.includes('email') || slug.includes('mail') || slug.includes('gmail')) return 'email';
  if (slug.includes('slack') || slug.includes('chat') || slug.includes('message')) return 'chat';
  if (slug.includes('judge') || slug.includes('eval') || slug.includes('review')) return 'rate_review';
  if (slug.includes('logic') || slug.includes('reason') || slug.includes('think')) return 'psychology';
  if (slug.includes('creat') || slug.includes('design') || slug.includes('art')) return 'palette';
  if (slug.includes('voice') || slug.includes('audio') || slug.includes('speech') || slug.includes('tts')) return 'record_voice_over';
  if (slug.includes('echo') || slug.includes('test') || slug.includes('mock')) return 'bug_report';
  if (slug.includes('slow') || slug.includes('queue') || slug.includes('batch')) return 'hourglass_top';
  if (slug.includes('stream')) return 'stream';
  if (slug.includes('a2a') || slug.includes('robot')) return 'robot_2';
  if (slug.includes('evidence') || slug.includes('fact') || slug.includes('verify')) return 'fact_check';
  return 'smart_toy';
}

const EP_META: Record<string, { emoji: string; title: string; desc: string; color?: string }> = {
  websocket: { emoji: '⚡', title: 'WebSocket', desc: 'Full-duplex, persistent connection. Client and server can send messages at any time. Best for chat, real-time collaboration, and interactive agents.' },
  sse:       { emoji: '📡', title: 'Server-Sent Events', desc: 'One-way server→client stream over HTTP. Lightweight, works through proxies. Best for dashboards, notifications, and read-only agent output.' },
  webrtc:    { emoji: '🎙️', title: 'WebRTC Voice', desc: 'Real-time voice via LiveKit WebRTC. Low-latency bidirectional audio with automatic voice activity detection. Best for voice assistants and spoken-word agents.', color: '#a78bfa' },
  a2a:       { emoji: '🤖', title: 'A2A External', desc: 'Expose this orchestrator as an A2A agent for external callers. The A2A skill id is the entry point slug. Best for machine-to-machine orchestration.', color: '#f59e0b' },
  voice:     { emoji: '🎤', title: 'Voice (STT/TTS)', desc: 'Speech-to-speech over HTTP. Browser sends audio → STT → orchestrator → TTS → audio reply. Requires STT + TTS config on the connected orchestrator.', color: '#f59e0b' },
};

// ── Canvas v2 LLM provider/model map ─────────────────────────────────────────
const MODELS_BY_PROVIDER: Record<string, string[]> = {
  anthropic: ['claude-opus-4-8', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
  openai:    ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo', 'o1', 'o3-mini'],
  groq:      ['openai/gpt-oss-120b', 'openai/gpt-oss-20b', 'qwen/qwen3.6-27b', 'groq/compound', 'groq/compound-mini'],
  gemini:    ['gemini-2.5-pro', 'gemini-2.0-flash', 'gemini-1.5-pro'],
};
const PROVIDER_OPTIONS = ['anthropic', 'openai', 'groq', 'gemini'];

function trunc(s: string | null | undefined, n = 120) {
  if (!s) return '—';
  return s.length > n ? s.slice(0, n) + '…' : s;
}

// ── Node Library panel ────────────────────────────────────────────────────────
function NodeLibrary({ agents, middlewareDefs, width, onWidthChange }: {
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
              {(['websocket', 'sse', 'webrtc', 'a2a', 'voice'] as const).map(ep => {
                const meta = EP_META[ep];
                const isAmber = ep === 'a2a' || ep === 'voice';
                return (
                  <div key={ep} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                    <div
                      draggable
                      onDragStart={e => dragItem(e, 'entryPoint', { epType: ep, label: meta.title, accessMode: 'token', slug: '' })}
                      style={{ ...itemStyle, background: isAmber ? C.amberBg : C.cyanBg, borderColor: isAmber ? C.amberBorder : C.cyanBorder, marginBottom: 0 }}
                      onMouseEnter={e => (e.currentTarget.style.background = isAmber ? 'rgba(245,158,11,0.1)' : 'rgba(0,240,255,0.1)')}
                      onMouseLeave={e => (e.currentTarget.style.background = isAmber ? C.amberBg : C.cyanBg)}
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
              <div className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                <div
                  draggable
                  onDragStart={e => dragItem(e, 'orchestrator', {
                    orchestratorId: null, appOrchestratorId: null,
                    name: '', displayName: '',
                    systemPrompt: '', allowedAgentIds: [],
                    llmProvider: '', llmModel: '', llmApiKey: '',
                    maxIterations: null, historyWindow: null,
                    maxParallelTools: null,
                    delegatable: false, kind: 'standard', budgetTokens: null,
                    transcriptionProvider: null, transcriptionModel: null, transcriptionApiKey: null,
                    ttsProvider: null, ttsVoice: null, ttsApiKey: null,
                  })}
                  style={{ ...itemStyle, background: C.purpleBg, borderColor: 'rgba(208,188,255,0.2)', marginBottom: 0 }}
                  onMouseEnter={e => (e.currentTarget.style.background = 'rgba(87,27,193,0.2)')}
                  onMouseLeave={e => (e.currentTarget.style.background = C.purpleBg)}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.purple, flexShrink: 0 }}>hub</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: C.text }}>Orchestrator</div>
                    <div style={{ fontSize: 10, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>claude-sonnet-4-6</div>
                  </div>
                  <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
                </div>
                <div className="nl-tip">
                  <div style={{ fontSize: 11, fontWeight: 700, color: C.purple, marginBottom: 4 }}>Orchestrator</div>
                  <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>Drop onto canvas to create a new orchestrator instance. Configure model, system prompt, and agents in the inspector.</div>
                </div>
              </div>
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

// ── Agent Credential Panel (canvas A2A agents) ───────────────────────────────
function AgentCredentialPanel({ appId, agentId, definitionId }: { appId: string; agentId: string; definitionId?: string }) {
  const [slots, setSlots] = useState<import('@/lib/api').AgentCredentialSlot[]>([]);
  const [filled, setFilled] = useState<Record<string, boolean>>({});
  const [values, setValues] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [err, setErr] = useState('');
  const [bindingId, setBindingId] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setErr('');
    const tasks: Promise<unknown>[] = [];
    let slotsFromDef: import('@/lib/api').AgentCredentialSlot[] = [];
    let filledFromBinding: Record<string, boolean> = {};
    let bId: string | null = null;

    if (definitionId) {
      tasks.push(
        themApi.getAgentDefinition(definitionId)
          .then(def => { slotsFromDef = def.definition.agent_root.credential_slots ?? []; })
          .catch(() => {})
      );
    }
    tasks.push(
      themApi.getAgentBinding(appId, agentId)
        .then(b => { filledFromBinding = b.credential_set; bId = b.id; })
        .catch(() => {})
    );
    Promise.all(tasks).then(() => {
      if (!alive) return;
      setSlots(slotsFromDef);
      setFilled(filledFromBinding);
      setBindingId(bId);
      const init: Record<string, string> = {};
      slotsFromDef.forEach(s => { init[s.name] = ''; });
      setValues(init);
      setLoading(false);
    });
    return () => { alive = false; };
  }, [appId, agentId, definitionId]);

  async function handleSave() {
    setSaving(true);
    setErr('');
    try {
      const creds: Record<string, string> = {};
      Object.entries(values).forEach(([k, v]) => { if (v) creds[k] = v; });
      await themApi.upsertAgentBinding(appId, agentId, { credentials: creds, definition_id: definitionId });
      const b = await themApi.getAgentBinding(appId, agentId);
      setFilled(b.credential_set);
      setBindingId(b.id);
      setValues(prev => { const r = { ...prev }; Object.keys(r).forEach(k => { r[k] = ''; }); return r; });
    } catch (e) { setErr(String(e)); }
    finally { setSaving(false); }
  }

  async function handleDelete() {
    if (!bindingId || !confirm('Remove all credential bindings for this agent in this application?')) return;
    setDeleting(true);
    setErr('');
    try {
      await themApi.deleteAgentBinding(appId, agentId);
      setFilled({});
      setBindingId(null);
    } catch (e) { setErr(String(e)); }
    finally { setDeleting(false); }
  }

  if (loading) return <div style={{ fontSize: 12, color: '#888', padding: '8px 0' }}>Loading credentials…</div>;
  if (slots.length === 0 && !bindingId) return null;

  const C2 = {
    cyan: '#00f0ff', cyanBg: 'rgba(0,240,255,0.08)', cyanBorder: 'rgba(0,240,255,0.25)',
    green: '#4ade80', greenBg: 'rgba(74,222,128,0.08)', greenBorder: 'rgba(74,222,128,0.25)',
  };

  return (
    <div style={{ marginTop: 20, paddingTop: 16, borderTop: '1px solid rgba(255,255,255,0.08)' }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: C2.cyan, textTransform: 'uppercase', letterSpacing: '0.08em', marginBottom: 12 }}>
        Credential Bindings
      </div>
      {slots.map(slot => (
        <div key={slot.name} style={{ marginBottom: 10 }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: '#aaa', marginBottom: 4 }}>
            {slot.name}
            {slot.required && <span style={{ color: '#f87171', fontSize: 10 }}>required</span>}
            {filled[slot.name] && (
              <span style={{ marginLeft: 'auto', fontSize: 10, color: C2.green, background: C2.greenBg, border: `1px solid ${C2.greenBorder}`, borderRadius: 10, padding: '1px 7px' }}>set</span>
            )}
          </label>
          {slot.description && <div style={{ fontSize: 11, color: '#666', marginBottom: 4 }}>{slot.description}</div>}
          <input
            type="password"
            value={values[slot.name] ?? ''}
            onChange={e => setValues(prev => ({ ...prev, [slot.name]: e.target.value }))}
            placeholder={filled[slot.name] ? '••••••• (leave blank to keep)' : 'Enter value…'}
            style={{ width: '100%', background: 'rgba(0,0,0,0.3)', border: `1px solid rgba(255,255,255,0.12)`, color: '#fff', borderRadius: 6, padding: '6px 10px', fontSize: 12, boxSizing: 'border-box' }}
          />
        </div>
      ))}
      {slots.length === 0 && bindingId && (
        <div style={{ fontSize: 12, color: '#888' }}>Binding exists (no slot metadata — refetch after republish).</div>
      )}
      {err && <div style={{ fontSize: 12, color: '#f87171', marginTop: 6 }}>{err}</div>}
      <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
        <button onClick={handleSave} disabled={saving} style={{ flex: 1, background: C2.cyanBg, border: `1px solid ${C2.cyanBorder}`, color: C2.cyan, padding: '6px 12px', borderRadius: 6, cursor: 'pointer', fontSize: 12 }}>
          {saving ? 'Saving…' : 'Save Credentials'}
        </button>
        {bindingId && (
          <button onClick={handleDelete} disabled={deleting} style={{ background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.3)', color: '#f87171', padding: '6px 12px', borderRadius: 6, cursor: 'pointer', fontSize: 12 }}>
            {deleting ? '…' : 'Remove'}
          </button>
        )}
      </div>
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
  const [orchTestState, setOrchTestState] = useState<{ loading?: boolean; ok?: boolean; latency?: number; error?: string }>({});
  const [sttTestState,  setSttTestState]  = useState<{ loading?: boolean; ok?: boolean; latency?: number; error?: string }>({});
  const [ttsTestState,  setTtsTestState]  = useState<{ loading?: boolean; ok?: boolean; latency?: number; error?: string }>({});

  async function testOrchLlm(d: OrchestratorData) {
    if (!d.llmProvider || !d.llmModel || !d.appOrchestratorId || !app) return;
    setOrchTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchLlm(app.id, d.appOrchestratorId, { provider: d.llmProvider, model: d.llmModel, api_key: d.llmApiKey || undefined });
      setOrchTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setOrchTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function testStt(d: OrchestratorData) {
    if (!d.transcriptionProvider || !d.transcriptionModel || !d.appOrchestratorId || !app) return;
    setSttTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchVoice(app.id, d.appOrchestratorId, { provider: d.transcriptionProvider, model: d.transcriptionModel });
      setSttTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setSttTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function testTts(d: OrchestratorData) {
    if (!d.ttsProvider || !d.ttsVoice || !d.appOrchestratorId || !app) return;
    setTtsTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchTts(app.id, d.appOrchestratorId, { provider: d.ttsProvider, voice: d.ttsVoice });
      setTtsTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setTtsTestState({ loading: false, ok: false, error: e.message });
    }
  }

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

          {/* App name — always visible */}
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

          {epCount <= 1 ? (
            <>
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
                  <label style={labelStyle}>Display Name</label>
                  <input style={inputStyle} value={d.appName ?? d.label} onChange={e => onUpdateNode(selectedNode.id, { appName: e.target.value, label: e.target.value })} placeholder="e.g. Customer Support" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Token Limit <span style={{ fontSize: 10, color: '#64748b' }}>per session · blank = unlimited</span></label>
                  <input type="number" min={1} style={inputStyle} value={d.convTokenLimit ?? ''} onChange={e => onUpdateNode(selectedNode.id, { convTokenLimit: e.target.value })} placeholder="e.g. 50000" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Max Concurrent Sessions <span style={{ fontSize: 10, color: '#64748b' }}>blank = unlimited</span></label>
                  <input type="number" min={1} style={inputStyle} value={d.maxConcurrentSessions ?? ''} onChange={e => onUpdateNode(selectedNode.id, { maxConcurrentSessions: e.target.value })} placeholder="e.g. 10" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Queue Timeout (seconds) <span style={{ fontSize: 10, color: '#64748b' }}>blank = reject immediately</span></label>
                  <input type="number" min={1} style={inputStyle} value={d.queueTimeout ?? ''} onChange={e => onUpdateNode(selectedNode.id, { queueTimeout: e.target.value })} placeholder="e.g. 60" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Queue Message <span style={{ fontSize: 10, color: '#64748b' }}>shown while waiting</span></label>
                  <input style={inputStyle} value={d.queueMessage ?? ''} onChange={e => onUpdateNode(selectedNode.id, { queueMessage: e.target.value })} placeholder="All agents are busy, please wait..." />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Type</label>
                  <select style={{ ...inputStyle }} value={d.epType} onChange={e => onUpdateNode(selectedNode.id, { epType: e.target.value as EntryPointType })}>
                    <option value="websocket">WebSocket</option>
                    <option value="sse">SSE</option>
                    <option value="webrtc">WebRTC Voice</option>
                    <option value="voice">Voice (STT/TTS)</option>
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
                          : d.epType === 'voice' ? `http://<host>:8088/apps/${d.slug}/voice/transcribe · /voice/tts`
                          : `http://<host>:8088/apps/${d.slug}/sse`}
                      </span>
                      <button
                        onClick={() => navigator.clipboard.writeText(
                          d.epType === 'websocket'
                            ? `ws://localhost:8088/apps/${d.slug}/ws`
                            : d.epType === 'webrtc'
                            ? `http://localhost:8088/apps/${d.slug}/voice`
                            : d.epType === 'voice'
                            ? `http://localhost:8088/apps/${d.slug}/voice/transcribe`
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
                {/* Test EP button */}
                {(() => {
                  const orchEdge = edges.find((e: Edge) => e.source === selectedNode.id);
                  const orchNode = orchEdge ? nodes.find((nd: Node) => nd.id === orchEdge.target && nd.type === 'orchestrator') : undefined;
                  const orchName = orchNode ? (orchNode.data as OrchestratorData).name : '';
                  const isSaved = !!(app?.entry_points?.find((ep: { slug: string }) => ep.slug === d.slug));
                  const testUrl = d.epType === 'voice' || d.epType === 'webrtc'
                    ? `/apps/${d.slug}/voice`
                    : orchName ? `/admin/playground?orchestrator=${encodeURIComponent(orchName)}` : '/admin/playground';
                  return (
                    <div style={{ marginTop: 12 }}>
                      <button
                        disabled={!isSaved}
                        onClick={() => { if (isSaved) window.open(testUrl, '_blank', 'noopener'); }}
                        title={isSaved ? 'Open test interface' : 'Save the application first to enable testing'}
                        style={{
                          width: '100%', padding: '8px 0', borderRadius: 8, border: `1px solid ${isSaved ? C.green : C.outlineVariant}`,
                          background: 'transparent', color: isSaved ? C.green : C.textMuted,
                          cursor: isSaved ? 'pointer' : 'not-allowed', fontSize: 13, fontWeight: 600,
                          display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
                          opacity: isSaved ? 1 : 0.5,
                        }}
                      >
                        <span className="material-symbols-outlined" style={{ fontSize: 15 }}>
                          {d.epType === 'voice' ? 'mic' : d.epType === 'webrtc' ? 'videocam' : 'play_arrow'}
                        </span>
                        Test Entry Point
                      </button>
                      {!isSaved && (
                        <div style={{ fontSize: 10, color: C.textMuted, textAlign: 'center', marginTop: 4 }}>
                          Save the application first to enable testing
                        </div>
                      )}
                    </div>
                  );
                })()}
              </div>
            );
          })()}

          {/* Orchestrator properties */}
          {selectedNode.type === 'orchestrator' && propTab === 'properties' && (() => {
            const d = selectedNode.data as OrchestratorData;
            const connectedAgentCount = edges.filter(e => e.source === selectedNode.id && nodes.find(n => n.id === e.target && n.type === 'agent')).length;
            const ORCH_PROVIDERS: Record<string, string[]> = {
              anthropic: ['claude-opus-4-8', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
              openai:    ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo', 'o1', 'o3-mini'],
              groq:      ['openai/gpt-oss-120b', 'openai/gpt-oss-20b', 'qwen/qwen3.6-27b', 'groq/compound', 'groq/compound-mini'],
              gemini:    ['gemini-2.5-pro', 'gemini-2.0-flash', 'gemini-1.5-pro'],
            };
            const CUSTOM = '__custom__';
            const currentProvider = d.llmProvider || '';
            const knownModels = ORCH_PROVIDERS[currentProvider] ?? [];
            const isCustomModel = !!d.llmModel && knownModels.length > 0 && !knownModels.includes(d.llmModel);
            const selectVal = isCustomModel ? CUSTOM : (d.llmModel ?? '');
            const connectedEpTypes = new Set<string>(
              edges
                .filter(e => e.target === selectedNode.id)
                .map(e => nodes.find(n => n.id === e.source && n.type === 'entryPoint'))
                .filter((n): n is Node => !!n)
                .map(n => (n.data as EntryPointData).epType as string)
            );
            const hasVoice = connectedEpTypes.has('voice');
            const hasWebrtc = connectedEpTypes.has('webrtc');
            const hasLlmEp = hasVoice || connectedEpTypes.has('websocket') || connectedEpTypes.has('sse');
            const noEp = connectedEpTypes.size === 0;
            return (
              <div>
                {/* Header tile */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.purpleBg, border: '1px solid rgba(208,188,255,0.2)' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.purple }}>hub</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName || <span style={{ opacity: 0.4 }}>Unnamed</span>}</div>
                    <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.name || '—'}</div>
                  </div>
                </div>

                {/* Display Name */}
                <div style={fieldWrap}>
                  <label style={labelStyle}>Display Name</label>
                  <input style={inputStyle} value={d.displayName} onChange={e => onUpdateNode(selectedNode.id, { displayName: e.target.value })} placeholder="Display name" />
                </div>

                {/* No EP placeholder */}
                {noEp && (
                  <div style={{ padding: '12px', borderRadius: 8, background: C.surfaceLow, border: `1px solid ${C.outlineVariant}`, color: C.textMuted, fontSize: 12, textAlign: 'center', marginTop: 8 }}>
                    Connect an entry point to start configuring
                  </div>
                )}

                {/* LLM section — shown when at least one non-webrtc EP is connected, or webrtc */}
                {(hasLlmEp || hasWebrtc) && (
                  <>
                    {/* LLM Provider */}
                    <div style={fieldWrap}>
                      <label style={labelStyle}>LLM Provider</label>
                      <select
                        style={{ ...inputStyle, cursor: 'pointer' }}
                        value={currentProvider}
                        onChange={e => {
                          const p = e.target.value;
                          const firstModel = ORCH_PROVIDERS[p]?.[0] ?? '';
                          onUpdateNode(selectedNode.id, { llmProvider: p || null, llmModel: firstModel || null, model: firstModel || null });
                        }}
                      >
                        <option value="">— inherit default —</option>
                        {Object.keys(ORCH_PROVIDERS).map(p => <option key={p} value={p}>{p}</option>)}
                      </select>
                    </div>

                    {/* LLM Model */}
                    <div style={fieldWrap}>
                      <label style={labelStyle}>LLM Model</label>
                      {knownModels.length > 0 ? (
                        <>
                          <select
                            style={{ ...inputStyle, cursor: 'pointer', fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                            value={selectVal}
                            onChange={e => {
                              const v = e.target.value;
                              if (v !== CUSTOM) onUpdateNode(selectedNode.id, { llmModel: v, model: v });
                              else onUpdateNode(selectedNode.id, { llmModel: '', model: '' });
                            }}
                          >
                            {knownModels.map(m => <option key={m} value={m}>{m}</option>)}
                            <option value={CUSTOM}>Custom…</option>
                          </select>
                          {(selectVal === CUSTOM || isCustomModel) && (
                            <input
                              style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11, marginTop: 6 }}
                              value={d.llmModel ?? ''}
                              onChange={e => onUpdateNode(selectedNode.id, { llmModel: e.target.value, model: e.target.value })}
                              placeholder="Enter model ID"
                              autoFocus
                            />
                          )}
                        </>
                      ) : (
                        <input
                          style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                          value={d.llmModel ?? ''}
                          onChange={e => onUpdateNode(selectedNode.id, { llmModel: e.target.value, model: e.target.value })}
                          placeholder="e.g. claude-sonnet-4-6"
                        />
                      )}
                    </div>

                    {/* API Key */}
                    <div style={fieldWrap}>
                      <label style={labelStyle}>API Key</label>
                      <input
                        type="password"
                        style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                        value={d.llmApiKey ?? ''}
                        onChange={e => onUpdateNode(selectedNode.id, { llmApiKey: e.target.value })}
                        placeholder={d.appOrchestratorId ? '••••••••  (leave blank to keep existing)' : 'Enter API key'}
                      />
                    </div>

                    {/* System Prompt */}
                    <div style={fieldWrap}>
                      <label style={labelStyle}>System Prompt</label>
                      <textarea
                        style={{ ...inputStyle, resize: 'vertical', minHeight: 80, fontFamily: 'inherit', fontSize: 12 }}
                        value={d.systemPrompt ?? ''}
                        onChange={e => onUpdateNode(selectedNode.id, { systemPrompt: e.target.value })}
                        placeholder="You are a helpful assistant…"
                      />
                    </div>

                    {/* Numeric row 1: Max Iterations + History Window */}
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Max Iterations</label>
                        <input type="number" min={1} max={100} style={inputStyle} value={d.maxIterations ?? ''} onChange={e => onUpdateNode(selectedNode.id, { maxIterations: parseInt(e.target.value, 10) || 10 })} placeholder="10" />
                      </div>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>History Window</label>
                        <input type="number" min={0} max={200} style={inputStyle} value={d.historyWindow ?? ''} onChange={e => onUpdateNode(selectedNode.id, { historyWindow: parseInt(e.target.value, 10) || 20 })} placeholder="20" />
                      </div>
                    </div>

                    {/* Numeric row 2: Max Parallel Tools + Budget Tokens */}
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Parallel Tools</label>
                        <input type="number" min={1} max={20} style={inputStyle} value={d.maxParallelTools ?? ''} onChange={e => onUpdateNode(selectedNode.id, { maxParallelTools: parseInt(e.target.value, 10) || 4 })} placeholder="4" />
                      </div>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Budget Tokens</label>
                        <input type="number" min={0} style={inputStyle} value={d.budgetTokens ?? ''} onChange={e => { const v = e.target.value; onUpdateNode(selectedNode.id, { budgetTokens: v === '' ? null : parseInt(v, 10) }); }} placeholder="unlimited" />
                      </div>
                    </div>

                    {/* Kind + Delegatable row */}
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Kind</label>
                        <select style={{ ...inputStyle, cursor: 'pointer' }} value={d.kind || 'standard'} onChange={e => onUpdateNode(selectedNode.id, { kind: e.target.value })}>
                          <option value="standard">standard</option>
                          <option value="supervisor">supervisor</option>
                          <option value="delegator">delegator</option>
                        </select>
                      </div>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Delegatable</label>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow, cursor: 'pointer' }} onClick={() => onUpdateNode(selectedNode.id, { delegatable: !d.delegatable })}>
                          <div style={{ width: 32, height: 18, borderRadius: 9, background: d.delegatable ? C.purple : 'rgba(255,255,255,0.12)', transition: 'background 200ms', position: 'relative', flexShrink: 0 }}>
                            <div style={{ position: 'absolute', top: 2, left: d.delegatable ? 16 : 2, width: 14, height: 14, borderRadius: '50%', background: '#fff', transition: 'left 200ms', boxShadow: '0 1px 3px rgba(0,0,0,0.4)' }} />
                          </div>
                          <span style={{ fontSize: 12, color: d.delegatable ? C.purple : C.textMuted }}>{d.delegatable ? 'Yes' : 'No'}</span>
                        </div>
                      </div>
                    </div>

                    {/* Test LLM connection */}
                    {d.llmProvider && d.llmModel && (
                      <div style={{ marginTop: 4, marginBottom: 2, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                        <button
                          onClick={() => testOrchLlm(d)}
                          disabled={orchTestState.loading || !d.appOrchestratorId}
                          title={!d.appOrchestratorId ? 'Save the application first to enable testing' : undefined}
                          style={{
                            display: 'flex', alignItems: 'center', gap: 6,
                            padding: '7px 14px', borderRadius: 8,
                            border: `1px solid ${C.purpleBorder}`,
                            background: 'rgba(208,188,255,0.07)',
                            color: (!d.appOrchestratorId || orchTestState.loading) ? C.textMuted : C.purple,
                            cursor: (!d.appOrchestratorId || orchTestState.loading) ? 'not-allowed' : 'pointer',
                            fontSize: 12, fontWeight: 600, opacity: !d.appOrchestratorId ? 0.5 : 1,
                            transition: 'all 150ms',
                          }}
                        >
                          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>bolt</span>
                          {orchTestState.loading ? 'Testing…' : 'Test connection'}
                        </button>
                        {!orchTestState.loading && orchTestState.ok !== undefined && (
                          orchTestState.ok
                            ? <span style={{ fontSize: 12, color: '#4edea3', fontWeight: 600 }}>✓ Connected ({orchTestState.latency}ms)</span>
                            : <span style={{ fontSize: 12, color: '#f87171' }}>✗ {orchTestState.error ?? 'Failed'}</span>
                        )}
                        {!d.appOrchestratorId && (
                          <span style={{ fontSize: 11, color: C.textMuted }}>
                            Save the application first to enable testing
                          </span>
                        )}
                      </div>
                    )}

                    {/* Connected Agents read-only */}
                    <div style={fieldWrap}>
                      <label style={labelStyle}>Connected Agents</label>
                      <div style={{ fontSize: 12, color: C.textMuted, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                        {connectedAgentCount} agent{connectedAgentCount !== 1 ? 's' : ''} — connect via canvas
                      </div>
                    </div>
                  </>
                )}

                {/* STT section — voice EP only */}
                {hasVoice && (
                  <div style={{ marginTop: 16, borderTop: `1px solid ${C.outlineVariant}`, paddingTop: 14 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.amber }}>mic</span>
                      <span style={{ fontSize: 11, fontWeight: 700, color: C.amber, textTransform: 'uppercase', letterSpacing: '0.5px' }}>Speech-to-Text</span>
                      <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 9999, background: C.amberBg, border: `1px solid ${C.amberBorder}`, color: C.amber, fontWeight: 600 }}>Required</span>
                    </div>

                    <div style={fieldWrap}>
                      <label style={labelStyle}>Provider</label>
                      <select style={{ ...inputStyle, cursor: 'pointer' }}
                        value={d.transcriptionProvider || ''}
                        onChange={e => {
                          const p = e.target.value;
                          const model = p === 'openai' ? 'whisper-1' : p === 'groq' ? 'whisper-large-v3' : '';
                          onUpdateNode(selectedNode.id, { transcriptionProvider: p || null, transcriptionModel: model || null });
                        }}
                      >
                        <option value="">— select provider —</option>
                        <option value="openai">OpenAI Whisper</option>
                        <option value="groq">Groq whisper-large-v3</option>
                      </select>
                    </div>

                    {d.transcriptionProvider && (
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Model</label>
                        <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                          value={d.transcriptionModel ?? ''}
                          onChange={e => onUpdateNode(selectedNode.id, { transcriptionModel: e.target.value })}
                          placeholder="e.g. whisper-1"
                        />
                      </div>
                    )}

                    <div style={fieldWrap}>
                      <label style={labelStyle}>API Key</label>
                      <input type="password"
                        style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                        value={d.transcriptionApiKey ?? ''}
                        onChange={e => onUpdateNode(selectedNode.id, { transcriptionApiKey: e.target.value })}
                        placeholder={d.appOrchestratorId ? '••••••••  (leave blank to keep existing)' : 'Enter API key'}
                      />
                    </div>

                    {d.transcriptionProvider && d.transcriptionModel && (
                      <div style={{ marginTop: 4, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                        <button
                          onClick={() => testStt(d)}
                          disabled={sttTestState.loading || !d.appOrchestratorId}
                          title={!d.appOrchestratorId ? 'Save the application first to enable testing' : undefined}
                          style={{
                            display: 'flex', alignItems: 'center', gap: 6,
                            padding: '7px 14px', borderRadius: 8,
                            border: `1px solid ${C.amberBorder}`,
                            background: 'rgba(251,191,36,0.07)',
                            color: (!d.appOrchestratorId || sttTestState.loading) ? C.textMuted : C.amber,
                            cursor: (!d.appOrchestratorId || sttTestState.loading) ? 'not-allowed' : 'pointer',
                            fontSize: 12, fontWeight: 600, opacity: !d.appOrchestratorId ? 0.5 : 1,
                            transition: 'all 150ms',
                          }}
                        >
                          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>mic</span>
                          {sttTestState.loading ? 'Testing…' : 'Test STT'}
                        </button>
                        {!sttTestState.loading && sttTestState.ok !== undefined && (
                          sttTestState.ok
                            ? <span style={{ fontSize: 12, color: '#4edea3', fontWeight: 600 }}>✓ Connected ({sttTestState.latency}ms)</span>
                            : <span style={{ fontSize: 12, color: '#f87171' }}>✗ {sttTestState.error ?? 'Failed'}</span>
                        )}
                        {!d.appOrchestratorId && (
                          <span style={{ fontSize: 11, color: C.textMuted }}>Save first to enable testing</span>
                        )}
                      </div>
                    )}
                  </div>
                )}

                {/* TTS section — voice EP only */}
                {hasVoice && (
                  <div style={{ marginTop: 16, borderTop: `1px solid ${C.outlineVariant}`, paddingTop: 14 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.amber }}>volume_up</span>
                      <span style={{ fontSize: 11, fontWeight: 700, color: C.amber, textTransform: 'uppercase', letterSpacing: '0.5px' }}>Text-to-Speech</span>
                      <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 9999, background: C.amberBg, border: `1px solid ${C.amberBorder}`, color: C.amber, fontWeight: 600 }}>Required</span>
                    </div>

                    <div style={fieldWrap}>
                      <label style={labelStyle}>Provider</label>
                      <select style={{ ...inputStyle, cursor: 'pointer' }}
                        value={d.ttsProvider || ''}
                        onChange={e => onUpdateNode(selectedNode.id, { ttsProvider: e.target.value || null, ttsVoice: null })}
                      >
                        <option value="">— select provider —</option>
                        <option value="openai">OpenAI</option>
                        <option value="elevenlabs">ElevenLabs</option>
                      </select>
                    </div>

                    {d.ttsProvider === 'openai' && (
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Voice</label>
                        <select style={{ ...inputStyle, cursor: 'pointer' }}
                          value={d.ttsVoice || ''}
                          onChange={e => onUpdateNode(selectedNode.id, { ttsVoice: e.target.value || null })}
                        >
                          <option value="">— select voice —</option>
                          {['alloy', 'echo', 'fable', 'onyx', 'nova', 'shimmer'].map(v => <option key={v} value={v}>{v}</option>)}
                        </select>
                      </div>
                    )}
                    {d.ttsProvider === 'elevenlabs' && (
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Voice ID</label>
                        <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                          value={d.ttsVoice ?? ''}
                          onChange={e => onUpdateNode(selectedNode.id, { ttsVoice: e.target.value || null })}
                          placeholder="ElevenLabs voice ID"
                        />
                      </div>
                    )}

                    <div style={fieldWrap}>
                      <label style={labelStyle}>API Key</label>
                      <input type="password"
                        style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                        value={d.ttsApiKey ?? ''}
                        onChange={e => onUpdateNode(selectedNode.id, { ttsApiKey: e.target.value })}
                        placeholder={d.appOrchestratorId ? '••••••••  (leave blank to keep existing)' : 'Enter API key'}
                      />
                    </div>

                    {d.ttsProvider && d.ttsVoice && (
                      <div style={{ marginTop: 4, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                        <button
                          onClick={() => testTts(d)}
                          disabled={ttsTestState.loading || !d.appOrchestratorId}
                          title={!d.appOrchestratorId ? 'Save the application first to enable testing' : undefined}
                          style={{
                            display: 'flex', alignItems: 'center', gap: 6,
                            padding: '7px 14px', borderRadius: 8,
                            border: `1px solid ${C.amberBorder}`,
                            background: 'rgba(251,191,36,0.07)',
                            color: (!d.appOrchestratorId || ttsTestState.loading) ? C.textMuted : C.amber,
                            cursor: (!d.appOrchestratorId || ttsTestState.loading) ? 'not-allowed' : 'pointer',
                            fontSize: 12, fontWeight: 600, opacity: !d.appOrchestratorId ? 0.5 : 1,
                            transition: 'all 150ms',
                          }}
                        >
                          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>volume_up</span>
                          {ttsTestState.loading ? 'Testing…' : 'Test TTS'}
                        </button>
                        {!ttsTestState.loading && ttsTestState.ok !== undefined && (
                          ttsTestState.ok
                            ? <span style={{ fontSize: 12, color: '#4edea3', fontWeight: 600 }}>✓ Connected ({ttsTestState.latency}ms)</span>
                            : <span style={{ fontSize: 12, color: '#f87171' }}>✗ {ttsTestState.error ?? 'Failed'}</span>
                        )}
                        {!d.appOrchestratorId && (
                          <span style={{ fontSize: 11, color: C.textMuted }}>Save first to enable testing</span>
                        )}
                      </div>
                    )}
                  </div>
                )}

                {/* Realtime section — webrtc EP only */}
                {hasWebrtc && (
                  <div style={{ marginTop: 16, borderTop: `1px solid ${C.outlineVariant}`, paddingTop: 14 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.cyan }}>sensors</span>
                      <span style={{ fontSize: 11, fontWeight: 700, color: C.cyan, textTransform: 'uppercase', letterSpacing: '0.5px' }}>Realtime Voice</span>
                    </div>
                    <div style={{ fontSize: 12, color: C.textMuted, padding: '10px 12px', borderRadius: 8, background: C.surfaceLow, border: `1px solid ${C.outlineVariant}` }}>
                      WebRTC entry points require a realtime-capable model (e.g. gpt-4o-realtime-preview). Configure the LLM model above accordingly.
                    </div>
                  </div>
                )}
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
                {app && d.transport === 'a2a' && (d as unknown as { definition_id?: string }).definition_id && (
                  <AgentCredentialPanel
                    appId={app.id}
                    agentId={d.agentId}
                    definitionId={(d as unknown as { definition_id?: string }).definition_id}
                  />
                )}
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
  nodes, edges, onNodesChange, onEdgesChange, onConnect, onDrop, onDragOver, selectedNode, setSelectedNode, onUpdateNode, onDeleteEdge, onAutoLayout, onNodesDelete, logoState, advisorOpen, onAdvisorOpen,
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
  // Returns the IDs of nodes that violate this rule (for red-ring highlighting)
  errorNodeIds?: (ctx: { nodes: Node[]; edges: Edge[] }) => string[];
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
    errorNodeIds: ({ nodes }) =>
      nodes.filter(n => n.type === 'entryPoint' && !(n.data as EntryPointData).slug).map(n => n.id),
  },
  {
    id: 'EP_SLUG_UNIQUE',
    severity: 'block',
    message: ({ nodes }) => {
      const slugs = nodes.filter(n => n.type === 'entryPoint').map(n => (n.data as EntryPointData).slug ?? '').filter(s => s !== '');
      return new Set(slugs).size !== slugs.length ? 'Duplicate entry point slug — each slug must be unique' : null;
    },
    errorNodeIds: ({ nodes }) => {
      const seen = new Set<string>(); const dupes = new Set<string>();
      nodes.filter(n => n.type === 'entryPoint').forEach(n => {
        const s = (n.data as EntryPointData).slug ?? '';
        if (s) { if (seen.has(s)) dupes.add(s); else seen.add(s); }
      });
      return nodes.filter(n => n.type === 'entryPoint' && dupes.has((n.data as EntryPointData).slug ?? '')).map(n => n.id);
    },
  },
  {
    id: 'EP_SLUG_FORMAT',
    severity: 'block',
    message: ({ nodes }) => {
      const bad = nodes.filter(n => n.type === 'entryPoint' && !(n.data as EntryPointData).slug?.match(/^[a-z0-9_-]{1,64}$/));
      return bad.length > 0 ? `Slug "${(bad[0].data as EntryPointData).slug}": lowercase letters, numbers, _ or - only` : null;
    },
    errorNodeIds: ({ nodes }) =>
      nodes.filter(n => n.type === 'entryPoint' && !(n.data as EntryPointData).slug?.match(/^[a-z0-9_-]{1,64}$/)).map(n => n.id),
  },
  {
    id: 'EP_HAS_ORCH',
    severity: 'block',
    message: ({ nodes, edges }) => {
      const epNodes = nodes.filter(n => n.type === 'entryPoint');
      const unconnected = epNodes.filter(ep => !edges.some(e => e.source === ep.id && nodes.find(n => n.id === e.target && n.type === 'orchestrator')));
      return unconnected.length > 0 ? 'Every entry point must connect to an orchestrator' : null;
    },
    errorNodeIds: ({ nodes, edges }) =>
      nodes.filter(n => n.type === 'entryPoint' && !edges.some(e => e.source === n.id && nodes.find(m => m.id === e.target && m.type === 'orchestrator'))).map(n => n.id),
  },
  {
    id: 'ORCH_HAS_AGENT',
    severity: 'warn',
    message: ({ nodes, edges }) => {
      const orchNodes = nodes.filter(n => n.type === 'orchestrator');
      const empty = orchNodes.filter(o => !edges.some(e => e.source === o.id && nodes.find(n => n.id === e.target && n.type === 'agent')));
      return empty.length > 0 ? `${empty.length} orchestrator${empty.length > 1 ? 's have' : ' has'} no agents` : null;
    },
    errorNodeIds: ({ nodes, edges }) =>
      nodes.filter(n => n.type === 'orchestrator' && !edges.some(e => e.source === n.id && nodes.find(m => m.id === e.target && m.type === 'agent'))).map(n => n.id),
  },
  {
    id: 'VOICE_EP_NEEDS_STT_TTS',
    severity: 'warn',
    message: ({ nodes, edges }) => {
      const voiceEps = nodes.filter(n => n.type === 'entryPoint' && (n.data as EntryPointData).epType === 'voice');
      for (const ep of voiceEps) {
        const orchEdge = edges.find(e => e.source === ep.id);
        if (!orchEdge) continue;
        const orch = nodes.find(n => n.id === orchEdge.target && n.type === 'orchestrator');
        if (!orch) continue;
        const d = orch.data as OrchestratorData;
        if (!d.transcriptionProvider || !d.ttsProvider) {
          return `Voice entry point requires STT and TTS providers configured on its orchestrator`;
        }
      }
      return null;
    },
    errorNodeIds: ({ nodes, edges }) => {
      const bad: string[] = [];
      const voiceEps = nodes.filter(n => n.type === 'entryPoint' && (n.data as EntryPointData).epType === 'voice');
      for (const ep of voiceEps) {
        const orchEdge = edges.find(e => e.source === ep.id);
        if (!orchEdge) continue;
        const orch = nodes.find(n => n.id === orchEdge.target && n.type === 'orchestrator');
        if (!orch) continue;
        const d = orch.data as OrchestratorData;
        if (!d.transcriptionProvider || !d.ttsProvider) bad.push(ep.id, orch.id);
      }
      return bad;
    },
  },
];

// Returns a map of nodeId → error message for all currently violated rules
function getErrorNodeMap(nodes: Node[], edges: Edge[]): Map<string, string> {
  const ctx = { nodes, edges };
  const result = new Map<string, string>();
  for (const rule of CANVAS_RULES) {
    const msg = rule.message(ctx);
    if (msg && rule.errorNodeIds) {
      const ids = rule.errorNodeIds(ctx);
      for (const id of ids) {
        if (!result.has(id)) result.set(id, msg); // first rule wins for tooltip
      }
    }
  }
  return result;
}

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

function CanvasInnerWithDrop({
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

// ── Canvas Builder View (V2) ──────────────────────────────────────────────────
function CanvasBuilderView({
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

  // Canvas state
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);


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
    border: '1px solid rgba(255,255,255,0.12)', background: 'rgba(255,255,255,0.05)',
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
      letterSpacing: '0.06em', textTransform: 'uppercase',
    };
    const chipStyle: React.CSSProperties = {
      fontSize: 12, fontFamily: 'JetBrains Mono, monospace', color: C.textMuted,
      padding: '6px 10px', background: 'rgba(255,255,255,0.03)',
      borderRadius: 6, border: '1px solid rgba(255,255,255,0.08)',
    };
    const selectStyle: React.CSSProperties = {
      ...fieldStyle, cursor: 'pointer', appearance: 'none' as const,
    };

    function SectionHeader({ id, label, defaultOpen = true }: { id: string; label: string; defaultOpen?: boolean }) {
      const isOpen = id in openSections ? openSections[id] : defaultOpen;
      return (
        <button
          onClick={() => setOpenSections(prev => ({ ...prev, [id]: !(id in prev ? prev[id] : defaultOpen) }))}
          style={{ display: 'flex', alignItems: 'center', gap: 4, background: 'none', border: 'none', cursor: 'pointer', padding: 0, width: '100%' }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 16, color: C.textMuted }}>
            {isOpen ? 'expand_more' : 'chevron_right'}
          </span>
          <span style={sectionHdrStyle}>{label}</span>
        </button>
      );
    }

    function isSectionOpen(id: string, defaultOpen = true): boolean {
      return id in openSections ? openSections[id] : defaultOpen;
    }

    // ── no selection ─────────────────────────────────────────────────────────
    if (!selectedNode) {
      return (
        <div style={{ padding: 20, color: C.textMuted, fontSize: 13, fontStyle: 'italic' }}>
          Select a node to configure properties
        </div>
      );
    }

    // ── orchestrator ─────────────────────────────────────────────────────────
    if (selectedNode.type === 'orchestrator') {
      // Read from live nodes state — selectedNode.data is stale after setNodes
      const liveNode = nodes.find(n => n.id === selectedNode.id);
      const d = (liveNode?.data ?? selectedNode.data) as unknown as OrchNodeData;
      const isDelegatable = edges.some(e => e.target === selectedNode.id && nodes.find(n => n.id === e.source)?.type === 'orchestrator');

      // Connected EPs — one section per EP, keyed by instance_id
      const connectedEps = edges
        .filter(e => e.target === selectedNode.id)
        .map(e => nodes.find(n => n.id === e.source))
        .filter((n): n is Node => !!n && n.type === 'entryPoint')
        .map(n => n.data as unknown as EpNodeData);
      const hasVoice = connectedEps.some(ep => ep.protocol === 'voice');

      function setOrchConfig(patch: Record<string, unknown>) {
        setNodes(ns => ns.map(n => n.id === selectedNode!.id
          ? { ...n, data: { ...n.data, config: { ...(n.data as unknown as OrchNodeData).config, ...patch } } }
          : n));
        setIsDirty(true);
        setLogoResult('none');
      }

      // Per-EP LLM config stored under config.ep_llm.<instance_id>.{provider,model}
      function getEpLlm(epId: string) {
        const epLlm = ((d.config.ep_llm ?? {}) as Record<string, { provider?: string; model?: string }>)[epId] ?? {};
        return { provider: epLlm.provider || 'anthropic', model: epLlm.model || '' };
      }
      function setEpLlm(epId: string, patch: { provider?: string; model?: string }) {
        const current = (d.config.ep_llm ?? {}) as Record<string, unknown>;
        const existing = (current[epId] ?? {}) as Record<string, unknown>;
        setOrchConfig({ ep_llm: { ...current, [epId]: { ...existing, ...patch } } });
        setLlmTestState(s => { const n = { ...s }; delete n[epId]; return n; });
      }

      // Per-EP memory config stored under config.ep_memory.<instance_id>
      type EpMemory = { memory_enabled?: boolean; history_window?: number; summarize_every_n_calls?: number; memory_raw_fallback_n?: number; summarizer_provider?: string; summarizer_model?: string };
      function getEpMemory(epId: string): EpMemory {
        return ((d.config.ep_memory ?? {}) as Record<string, EpMemory>)[epId] ?? {};
      }
      function setEpMemory(epId: string, patch: EpMemory) {
        const current = (d.config.ep_memory ?? {}) as Record<string, unknown>;
        const existing = (current[epId] ?? {}) as Record<string, unknown>;
        setOrchConfig({ ep_memory: { ...current, [epId]: { ...existing, ...patch } } });
      }

      // EP icon + accent per protocol
      const EP_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };
      const EP_COLOR: Record<string, string> = { websocket: C.cyan, sse: C.cyan, webrtc: C.amber, a2a: '#f59e0b', voice: C.amber };

      const sttProvider = (d.config.stt_provider as string) || 'openai';
      const ttsProvider = (d.config.tts_provider as string) || 'openai';
      const ttsVoice = (d.config.tts_voice as string) || '';

      return (
        <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
          {/* Identity */}
          <div style={{ fontSize: 12, fontWeight: 700, color: C.purple, textTransform: 'uppercase', letterSpacing: '0.06em' }}>Orchestrator</div>
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Display Name</label>
            <input style={fieldStyle} value={d.display_name} onChange={e => { setNodes(ns => ns.map(n => n.id === selectedNode!.id ? { ...n, data: { ...n.data, display_name: e.target.value } } : n)); setIsDirty(true); setLogoResult('none'); }} />
          </div>
          {isDelegatable && (
            <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 20, background: C.purpleBg, color: C.purple, border: `1px solid ${C.purpleBorder}`, alignSelf: 'flex-start' }}>Delegation target</span>
          )}

          {/* Brain — system prompt, always visible */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
            <div style={{ ...sectionHdrStyle, marginBottom: 8 }}>Brain</div>
            <textarea
              style={{ ...fieldStyle, minHeight: 90, resize: 'vertical', fontFamily: 'inherit' }}
              value={(d.config.system_prompt as string) ?? ''}
              onChange={e => setOrchConfig({ system_prompt: e.target.value })}
              placeholder="System prompt…"
            />
          </div>

          {/* Per-EP LLM config — one section per connected entry point */}
          {connectedEps.length === 0 ? (
            <div style={{ padding: '12px 14px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', color: C.textMuted, fontSize: 12, textAlign: 'center' }}>
              Connect an entry point to configure LLM settings
            </div>
          ) : connectedEps.map(ep => {
            const epColor = EP_COLOR[ep.protocol] ?? C.cyan;
            const epIcon = EP_ICON[ep.protocol] ?? 'bolt';
            const { provider: epProv, model: epModel } = getEpLlm(ep.instance_id);
            const epKnownModels = MODELS_BY_PROVIDER[epProv] ?? [];
            const epIsCustom = epModel !== '' && !epKnownModels.includes(epModel);
            return (
              <div key={ep.instance_id} style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
                {/* EP header */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginBottom: 10 }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 14, color: epColor }}>{epIcon}</span>
                  <span style={{ fontSize: 11, fontWeight: 700, color: epColor, textTransform: 'uppercase', letterSpacing: '0.06em' }}>{ep.protocol}</span>
                  <span style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', marginLeft: 2 }}>{ep.slug}</span>
                </div>
                {/* Provider */}
                <div style={{ marginBottom: 8 }}>
                  <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Provider</label>
                  <select style={selectStyle} value={epProv}
                    onChange={e => setEpLlm(ep.instance_id, { provider: e.target.value, model: MODELS_BY_PROVIDER[e.target.value]?.[0] ?? '' })}>
                    {PROVIDER_OPTIONS.map(p => <option key={p} value={p}>{p}{providerKeyStatuses[p] ? ' ✓' : ''}</option>)}
                  </select>
                  {!providerKeyStatuses[epProv] && (
                    <div style={{ fontSize: 11, color: '#fb923c', marginTop: 4, display: 'flex', alignItems: 'center', gap: 8 }}>
                      No API key set for {epProv} — configure in Runtime settings
                      <button onClick={refreshProviderKeys} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, border: '1px solid rgba(251,146,60,0.4)', background: 'transparent', color: '#fb923c', cursor: 'pointer' }}>Refresh</button>
                    </div>
                  )}
                </div>
                {/* Model */}
                <div style={{ marginBottom: 10 }}>
                  <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Model</label>
                  <select style={selectStyle} value={epIsCustom ? 'custom' : epModel}
                    onChange={e => setEpLlm(ep.instance_id, { model: e.target.value === 'custom' ? '' : e.target.value })}>
                    {epKnownModels.map(m => <option key={m} value={m}>{m}</option>)}
                    <option value="custom">Custom…</option>
                  </select>
                  {epIsCustom && (
                    <input style={{ ...fieldStyle, marginTop: 6 }} value={epModel}
                      onChange={e => setEpLlm(ep.instance_id, { model: e.target.value })}
                      placeholder="Enter model id" />
                  )}
                </div>
                {/* Test button — per-EP isolated state */}
                {(() => {
                  const ts = llmTestState[ep.instance_id] ?? { loading: false };
                  return (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                      <button
                        onClick={() => testOrchLlm(ep.instance_id, epProv, epModel)}
                        disabled={ts.loading || !epProv || !epModel}
                        style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '6px 12px', borderRadius: 8, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.06)', color: (!epProv || !epModel || ts.loading) ? C.textMuted : C.purple, cursor: (!epProv || !epModel || ts.loading) ? 'not-allowed' : 'pointer', fontSize: 12, fontWeight: 600, transition: 'all 150ms' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 14 }}>bolt</span>
                        {ts.loading ? 'Testing…' : 'Test'}
                      </button>
                      {!ts.loading && ts.ok === true && <span style={{ fontSize: 12, color: '#4edea3', fontWeight: 600 }}>✓ {ts.latency}ms</span>}
                      {!ts.loading && ts.ok === false && <span style={{ fontSize: 12, color: '#f87171' }}>✗ {ts.error ?? 'Failed'}</span>}
                    </div>
                  );
                })()}

                {/* Memory — collapsible, per-EP */}
                {(() => {
                  const mem = getEpMemory(ep.instance_id);
                  const memEnabled = mem.memory_enabled ?? false;
                  const sumProv = mem.summarizer_provider ?? '';
                  const sumKnownModels = MODELS_BY_PROVIDER[sumProv] ?? [];
                  const sumModel = mem.summarizer_model ?? '';
                  const sumIsCustom = sumModel !== '' && !sumKnownModels.includes(sumModel);
                  const memSectionId = `ep-memory-${ep.instance_id}`;
                  return (
                    <div style={{ marginTop: 10, borderTop: '1px solid rgba(255,255,255,0.05)', paddingTop: 8 }}>
                      <SectionHeader id={memSectionId} label="Memory" defaultOpen={false} />
                      {isSectionOpen(memSectionId, false) && (
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
                          {/* Enable toggle */}
                          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
                            <input type="checkbox" checked={memEnabled} onChange={e => setEpMemory(ep.instance_id, { memory_enabled: e.target.checked })} />
                            <span style={{ fontSize: 12, color: memEnabled ? C.text : C.textMuted }}>Enable history</span>
                          </label>
                          {memEnabled && (<>
                            {/* History depth */}
                            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                              <div>
                                <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>History Window</label>
                                <input type="number" style={fieldStyle} value={mem.history_window ?? 20} min={1} onChange={e => setEpMemory(ep.instance_id, { history_window: Number(e.target.value) || 20 })} />
                              </div>
                              <div>
                                <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Summarize After</label>
                                <input type="number" style={fieldStyle} value={mem.summarize_every_n_calls ?? 0} min={0} placeholder="0 = off" onChange={e => setEpMemory(ep.instance_id, { summarize_every_n_calls: Number(e.target.value) })} />
                              </div>
                              <div>
                                <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Keep Recent</label>
                                <input type="number" style={fieldStyle} value={mem.memory_raw_fallback_n ?? 3} min={1} onChange={e => setEpMemory(ep.instance_id, { memory_raw_fallback_n: Number(e.target.value) || 3 })} />
                              </div>
                            </div>
                            {/* Summarizer LLM */}
                            <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.05em', marginTop: 4 }}>Summarizer LLM</div>
                            <div>
                              <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Provider</label>
                              <select style={selectStyle} value={sumProv}
                                onChange={e => setEpMemory(ep.instance_id, { summarizer_provider: e.target.value, summarizer_model: MODELS_BY_PROVIDER[e.target.value]?.[0] ?? '' })}>
                                <option value="">none (no summarization)</option>
                                {PROVIDER_OPTIONS.map(p => <option key={p} value={p}>{p}{providerKeyStatuses[p] ? ' ✓' : ''}</option>)}
                              </select>
                            </div>
                            {sumProv && (
                              <div>
                                <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Model</label>
                                <select style={selectStyle} value={sumIsCustom ? 'custom' : sumModel}
                                  onChange={e => setEpMemory(ep.instance_id, { summarizer_model: e.target.value === 'custom' ? '' : e.target.value })}>
                                  {sumKnownModels.map(m => <option key={m} value={m}>{m}</option>)}
                                  <option value="custom">Custom…</option>
                                </select>
                                {sumIsCustom && (
                                  <input style={{ ...fieldStyle, marginTop: 6 }} value={sumModel}
                                    onChange={e => setEpMemory(ep.instance_id, { summarizer_model: e.target.value })}
                                    placeholder="Enter model id" />
                                )}
                              </div>
                            )}
                            <div style={{ fontSize: 11, color: C.textMuted, fontStyle: 'italic' }}>Summarizer uses provider key from Runtime settings</div>
                          </>)}
                        </div>
                      )}
                    </div>
                  );
                })()}
              </div>
            );
          })}

          {/* Loop Tuning — collapsible */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
            <SectionHeader id="orch-loop" label="Loop Tuning" defaultOpen={false} />
            {isSectionOpen('orch-loop', false) && (
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginTop: 8 }}>
                <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Max Iterations</label>
                  <input type="number" style={fieldStyle} value={(d.config.max_iterations as number) ?? 10} onChange={e => setOrchConfig({ max_iterations: Number(e.target.value) || null })} /></div>
                <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Parallel Tools</label>
                  <input type="number" style={fieldStyle} value={(d.config.max_parallel_tools as number) ?? 5} onChange={e => setOrchConfig({ max_parallel_tools: Number(e.target.value) || null })} /></div>
                <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Budget Tokens</label>
                  <input type="number" style={fieldStyle} value={(d.config.budget_tokens as number) ?? ''} placeholder="none" onChange={e => setOrchConfig({ budget_tokens: e.target.value === '' ? null : Number(e.target.value) })} /></div>
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
                    <input style={fieldStyle} value={(d.config.stt_model as string) ?? ''} onChange={e => setOrchConfig({ stt_model: e.target.value })} placeholder="e.g. whisper-1" />
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
        </div>
      );
    }

    // ── agent ────────────────────────────────────────────────────────────────
    if (selectedNode.type === 'agent') {
      const d = selectedNode.data as unknown as AgentNodeData;
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
      const d = selectedNode.data as unknown as MwNodeData;
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
                onAutoLayout={() => { setNodes(ns => applyDagreLayout([...ns], edges)); }}
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
          <div style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column', background: 'rgba(0,0,0,0.15)' }}>
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

// ── DefinitionView ────────────────────────────────────────────────────────────
// Phase D: Application Definition editor — draft, validate, publish
// Canonical model: components[] + connections[] + entry_points[]
// Old builder (nodes/edges graph canvas) is retired.
function DefinitionView({
  app,
  onBack,
  onAppUpdated,
}: {
  app: Application;
  onBack: () => void;
  onAppUpdated?: (updated: Application) => void;
}) {
  // ── DefinitionView state ──────────────────────────────────────────────────────
  const [defs, setDefs] = useState<AppDefinition[]>([]);
  const [activeDef, setActiveDef] = useState<AppDefinition | null>(null);
  const [draft, setDraft] = useState<AppDefinitionDoc | null>(null);
  const [isDirty, setIsDirty] = useState(false);
  const [componentDefs, setComponentDefs] = useState<ComponentDefinitionSummary[]>([]);
  const [selectedItem, setSelectedItem] = useState<{ type: 'component' | 'ep'; id: string } | null>(null);
  const [validating, setValidating] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [validationReport, setValidationReport] = useState<ValidationReport | null>(null);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
  const [showRepublishModal, setShowRepublishModal] = useState(false);

  const fieldStyle: React.CSSProperties = {
    width: '100%', padding: '10px 12px', borderRadius: 8,
    border: '1px solid rgba(255,255,255,0.12)', background: 'rgba(255,255,255,0.05)',
    color: C.text, fontSize: 14, outline: 'none', boxSizing: 'border-box',
  };

  function showToast(msg: string, ok: boolean) {
    setToast({ msg, ok });
    setTimeout(() => setToast(null), 3000);
  }

  function loadDef(def: AppDefinition) {
    setActiveDef(def);
    setDraft(JSON.parse(JSON.stringify(def.definition)));
    setIsDirty(false);
    setValidationReport(null);
    setSelectedItem(null);
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
        // list is ORDER BY revision DESC so index 0 is the newest
        const latest = list[0];
        loadDef(latest);
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
        setActiveDef(null); setDraft(null);
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
    if (!activeDef || !draft) return;
    setSaving(true);
    try {
      await themApi.updateDefinition(app.id, activeDef.id, { definition: draft });
      setIsDirty(false);
      showToast('Saved', true);
    } catch {
      showToast('Save failed', false);
    } finally {
      setSaving(false);
    }
  }

  async function validate() {
    if (!activeDef || !draft) return;
    if (isDirty) await saveDraft();
    setValidating(true);
    try {
      const report = await themApi.validateDefinition(app.id, activeDef.id);
      setValidationReport(report);
      showToast(report.valid ? 'Valid ✓' : `${report.errors?.length ?? 0} error(s)`, report.valid);
    } catch {
      showToast('Validation failed', false);
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
      if (!report.valid) {
        showToast(`${report.errors?.length ?? 1} validation error(s)`, false);
        return;
      }
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
    if (app.active_revision != null) {
      setShowRepublishModal(true);
    } else {
      void publish();
    }
  }

  function addComponent(cd: ComponentDefinitionSummary) {
    if (!draft) return;
    const newComp = {
      instance_id: `${cd.kind}_${Date.now()}`,
      definition_ref: { kind: cd.kind, namespace: cd.namespace, name: cd.name, version: cd.version },
      definition_id: cd.id,
      config: {} as Record<string, unknown>,
    };
    setDraft(prev => prev ? { ...prev, components: [...prev.components, newComp] } : prev);
    setIsDirty(true);
  }

  function addEntryPoint(protocol: string) {
    if (!draft) return;
    const slug = `ep-${Date.now()}`;
    const newEP = {
      instance_id: `ep_${Date.now()}`,
      slug,
      protocol: protocol as AppDefinitionDoc['entry_points'][0]['protocol'],
      root: '',
    };
    setDraft(prev => prev ? { ...prev, entry_points: [...prev.entry_points, newEP] } : prev);
    setIsDirty(true);
  }

  function removeComponent(instanceId: string) {
    setDraft(prev => prev ? {
      ...prev,
      components: prev.components.filter(c => c.instance_id !== instanceId),
      connections: prev.connections.filter(c => c.source !== instanceId && c.target !== instanceId),
    } : prev);
    setIsDirty(true);
    if (selectedItem?.id === instanceId) setSelectedItem(null);
  }

  function removeEntryPoint(instanceId: string) {
    setDraft(prev => prev ? {
      ...prev,
      entry_points: prev.entry_points.filter(ep => ep.instance_id !== instanceId),
      connections: prev.connections.filter(c => c.source !== instanceId && c.target !== instanceId),
    } : prev);
    setIsDirty(true);
    if (selectedItem?.id === instanceId) setSelectedItem(null);
  }

  function removeConnection(idx: number) {
    setDraft(prev => prev ? {
      ...prev,
      connections: prev.connections.filter((_, i) => i !== idx),
    } : prev);
    setIsDirty(true);
  }

  function updateComponent(instanceId: string, patch: Partial<import('@/lib/api').ComponentInstance>) {
    setDraft(prev => prev ? {
      ...prev,
      components: prev.components.map(c => c.instance_id === instanceId ? { ...c, ...patch } : c),
    } : prev);
    setIsDirty(true);
  }

  function updateEntryPoint(instanceId: string, patch: Partial<import('@/lib/api').EPInstance>) {
    setDraft(prev => prev ? {
      ...prev,
      entry_points: prev.entry_points.map(ep => ep.instance_id === instanceId ? { ...ep, ...patch } : ep),
    } : prev);
    setIsDirty(true);
  }

  const selectedComp = selectedItem?.type === 'component'
    ? draft?.components.find(c => c.instance_id === selectedItem.id) ?? null
    : null;
  const selectedEP = selectedItem?.type === 'ep'
    ? draft?.entry_points.find(ep => ep.instance_id === selectedItem.id) ?? null
    : null;

  const kindColors: Record<string, string> = {
    orchestrator: C.purple, agent: C.green, middleware: C.amber, entry_point: C.cyan, tool: '#f59e0b',
  };

  const protocolOptions = ['websocket', 'sse', 'webrtc', 'a2a', 'voice'];
  const componentKinds = ['orchestrator', 'agent', 'middleware', 'entry_point', 'tool'];

  const isLive = app.active_revision != null;

  // Auto-save: trigger 3s after last change, only when a draft is loaded
  // Validation error index by instance_id
  const errorsByInstance: Record<string, string[]> = {};
  for (const err of validationReport?.errors ?? []) {
    if (err.instance_id) {
      errorsByInstance[err.instance_id] = errorsByInstance[err.instance_id] ?? [];
      errorsByInstance[err.instance_id].push(err.message);
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg, overflow: 'hidden' }}>
      {/* Top bar */}
      <div style={{
        height: 56, flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12,
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
        <div style={{
          background: C.errorBg, borderBottom: `1px solid rgba(255,180,171,0.3)`,
          padding: '10px 20px', flexShrink: 0,
        }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: C.error, marginBottom: 4 }}>Validation errors:</div>
          {(validationReport.errors ?? []).map((err, i) => (
            <div key={i} style={{ fontSize: 12, color: C.error }}>
              {err.instance_id ? `[${err.instance_id}] ` : ''}{err.message}
            </div>
          ))}
        </div>
      )}

      {/* Body — three columns */}
      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>

        {/* Left panel: Component Palette (260px) */}
        <div style={{ width: 260, flexShrink: 0, borderRight: `1px solid ${C.glassBorder}`, overflowY: 'auto', padding: 16, ...glass }}>
          <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 12 }}>Component Palette</div>
          {componentKinds.map(kind => {
            const items = componentDefs.filter(cd => cd.kind === kind);
            if (items.length === 0) return null;
            return (
              <div key={kind} style={{ marginBottom: 16 }}>
                <div style={{ fontSize: 10, fontWeight: 700, color: kindColors[kind] ?? C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>{kind}</div>
                {items.map(cd => (
                  <button
                    key={cd.id}
                    onClick={() => addComponent(cd)}
                    disabled={!activeDef}
                    style={{
                      width: '100%', textAlign: 'left', padding: '8px 10px', borderRadius: 8, border: `1px solid rgba(255,255,255,0.08)`,
                      background: 'rgba(255,255,255,0.03)', color: C.text, cursor: !activeDef ? 'not-allowed' : 'pointer',
                      display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4, fontSize: 12,
                    }}
                    onMouseEnter={e => { if (activeDef) { e.currentTarget.style.background = 'rgba(255,255,255,0.07)'; e.currentTarget.style.borderColor = kindColors[kind] ?? 'rgba(255,255,255,0.2)'; } }}
                    onMouseLeave={e => { e.currentTarget.style.background = 'rgba(255,255,255,0.03)'; e.currentTarget.style.borderColor = 'rgba(255,255,255,0.08)'; }}
                  >
                    <span style={{ fontSize: 10, fontWeight: 700, padding: '1px 5px', borderRadius: 4, background: kindColors[kind] ? `${kindColors[kind]}22` : 'rgba(255,255,255,0.06)', color: kindColors[kind] ?? C.textMuted }}>
                      {cd.kind[0].toUpperCase()}
                    </span>
                    <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cd.display_name}</span>
                    <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, flexShrink: 0 }}>add</span>
                  </button>
                ))}
              </div>
            );
          })}
          {componentDefs.length === 0 && (
            <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>No components available</div>
          )}
          <div style={{ marginTop: 16, borderTop: `1px solid ${C.glassBorder}`, paddingTop: 12 }}>
            <div style={{ fontSize: 10, fontWeight: 700, color: C.cyan, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>Entry Points</div>
            {protocolOptions.map(p => (
              <button
                key={p}
                onClick={() => addEntryPoint(p)}
                disabled={!activeDef}
                style={{
                  width: '100%', textAlign: 'left', padding: '6px 10px', borderRadius: 8, border: '1px solid rgba(0,240,255,0.15)',
                  background: 'rgba(0,240,255,0.03)', color: C.cyan, cursor: !activeDef ? 'not-allowed' : 'pointer',
                  display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, fontSize: 12,
                }}
              >
                <span className="material-symbols-outlined" style={{ fontSize: 14 }}>add</span>
                {p}
              </button>
            ))}
          </div>
        </div>

        {/* Center panel: Definition Editor */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
          {!activeDef && (
            <div style={{ textAlign: 'center', padding: '60px 20px', color: C.textMuted }}>
              <span className="material-symbols-outlined" style={{ fontSize: 48, display: 'block', marginBottom: 12, opacity: 0.4 }}>description</span>
              <div style={{ fontSize: 15, marginBottom: 16 }}>No definitions yet</div>
              <button onClick={newDraft} style={{ padding: '10px 20px', borderRadius: 8, border: 'none', background: C.cyan, color: '#021520', fontWeight: 700, cursor: 'pointer' }}>
                Create First Definition
              </button>
            </div>
          )}

          {activeDef && draft && (
            <>
              {/* Definition name */}
              <div style={{ ...glass, borderRadius: 10, padding: '14px 16px' }}>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 6 }}>Definition Name</label>
                <input
                  style={fieldStyle} value={draft.name ?? ''}
                  onChange={e => { setDraft(prev => prev ? { ...prev, name: e.target.value } : prev); setIsDirty(true); }}
                  placeholder="e.g. My Orchestration"
                />
              </div>

              {/* Components */}
              <div style={{ ...glass, borderRadius: 10, padding: '14px 16px' }}>
                <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 10 }}>
                  Components ({draft.components.length})
                </div>
                {draft.components.length === 0 && (
                  <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>No components. Add from the palette.</div>
                )}
                {draft.components.map(comp => {
                  const hasErrors = !!(errorsByInstance[comp.instance_id]?.length);
                  const isSelected = selectedItem?.id === comp.instance_id;
                  return (
                    <div
                      key={comp.instance_id}
                      style={{
                        padding: '10px 12px', borderRadius: 8, marginBottom: 6,
                        border: `1px solid ${hasErrors ? 'rgba(255,180,171,0.4)' : isSelected ? 'rgba(0,240,255,0.4)' : 'rgba(255,255,255,0.08)'}`,
                        background: isSelected ? 'rgba(0,240,255,0.04)' : 'rgba(255,255,255,0.02)',
                        display: 'flex', alignItems: 'center', gap: 10,
                      }}
                    >
                      <span style={{
                        fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 4,
                        background: kindColors[comp.definition_ref.kind] ? `${kindColors[comp.definition_ref.kind]}22` : 'rgba(255,255,255,0.08)',
                        color: kindColors[comp.definition_ref.kind] ?? C.textMuted,
                      }}>{comp.definition_ref.kind}</span>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{comp.instance_id}</div>
                        <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{comp.definition_ref.namespace}/{comp.definition_ref.name}@{comp.definition_ref.version}</div>
                      </div>
                      {hasErrors && <span className="material-symbols-outlined" style={{ fontSize: 16, color: C.error }} title={(errorsByInstance[comp.instance_id] ?? []).join('; ')}>error</span>}
                      <button onClick={() => setSelectedItem({ type: 'component', id: comp.instance_id })} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 16 }}>settings</span>
                      </button>
                      <button onClick={() => removeComponent(comp.instance_id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.error, display: 'flex', alignItems: 'center' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                      </button>
                    </div>
                  );
                })}
              </div>

              {/* Entry Points */}
              <div style={{ ...glass, borderRadius: 10, padding: '14px 16px' }}>
                <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 10 }}>
                  Entry Points ({draft.entry_points.length})
                </div>
                {draft.entry_points.length === 0 && (
                  <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>No entry points. Add from the palette.</div>
                )}
                {draft.entry_points.map(ep => {
                  const isSelected = selectedItem?.id === ep.instance_id;
                  return (
                    <div
                      key={ep.instance_id}
                      style={{
                        padding: '10px 12px', borderRadius: 8, marginBottom: 6,
                        border: `1px solid ${isSelected ? C.cyanBorder : 'rgba(0,240,255,0.15)'}`,
                        background: isSelected ? 'rgba(0,240,255,0.04)' : 'rgba(0,240,255,0.02)',
                        display: 'flex', alignItems: 'center', gap: 10,
                      }}
                    >
                      <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 4, background: 'rgba(0,240,255,0.12)', color: C.cyan }}>{ep.protocol}</span>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: C.text, fontWeight: 600 }}>{ep.slug || ep.instance_id}</div>
                        {ep.root && <div style={{ fontSize: 11, color: C.textMuted }}>→ {ep.root}</div>}
                      </div>
                      <button onClick={() => setSelectedItem({ type: 'ep', id: ep.instance_id })} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 16 }}>settings</span>
                      </button>
                      <button onClick={() => removeEntryPoint(ep.instance_id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.error, display: 'flex', alignItems: 'center' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                      </button>
                    </div>
                  );
                })}
              </div>

              {/* Connections */}
              <div style={{ ...glass, borderRadius: 10, padding: '14px 16px' }}>
                <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 10 }}>
                  Connections ({draft.connections.length})
                </div>
                {draft.connections.length === 0 && (
                  <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>No explicit connections. Entry→Orchestrator connections derive from entry point root bindings.</div>
                )}
                {draft.connections.map((conn, i) => (
                  <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 10px', borderRadius: 8, marginBottom: 4, border: '1px solid rgba(255,255,255,0.06)', background: 'rgba(255,255,255,0.02)' }}>
                    <span style={{ fontSize: 12, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{conn.source}</span>
                    <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted }}>arrow_forward</span>
                    <span style={{ fontSize: 12, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{conn.target}</span>
                    <span style={{ marginLeft: 4, fontSize: 10, fontWeight: 700, padding: '1px 5px', borderRadius: 4, background: 'rgba(255,255,255,0.08)', color: C.textMuted }}>{conn.type}</span>
                    <div style={{ flex: 1 }} />
                    <button onClick={() => removeConnection(i)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.error, display: 'flex', alignItems: 'center' }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                    </button>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>

        {/* Right panel: Properties (320px) */}
        <div style={{ width: 320, flexShrink: 0, borderLeft: `1px solid ${C.glassBorder}`, overflowY: 'auto', padding: 16, ...glass }}>
          <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 12 }}>Properties</div>

          {!selectedItem && (
            <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>Select a component or entry point to configure.</div>
          )}

          {selectedComp && draft && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Instance ID</label>
                <input
                  style={fieldStyle} value={selectedComp.instance_id}
                  onChange={e => updateComponent(selectedComp.instance_id, { instance_id: e.target.value })}
                />
              </div>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Definition</label>
                <div style={{ fontSize: 12, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', padding: '8px 10px', borderRadius: 6, background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.08)' }}>
                  {selectedComp.definition_ref.namespace}/{selectedComp.definition_ref.name}@{selectedComp.definition_ref.version}
                </div>
              </div>
              {selectedComp.definition_ref.kind === 'orchestrator' && (
                <>
                  <div>
                    <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Name (Temporal key — immutable)</label>
                    <input
                      style={fieldStyle} value={(selectedComp.config as any)?.name ?? selectedComp.name ?? ''}
                      onChange={e => updateComponent(selectedComp.instance_id, { config: { ...selectedComp.config, name: e.target.value } })}
                    />
                  </div>
                  <div>
                    <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>System Prompt</label>
                    <textarea
                      style={{ ...fieldStyle, height: 100, resize: 'vertical' }} value={(selectedComp.config as any)?.system_prompt ?? ''}
                      onChange={e => updateComponent(selectedComp.instance_id, { config: { ...selectedComp.config, system_prompt: e.target.value } })}
                    />
                  </div>
                  <div>
                    <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>LLM Provider</label>
                    <select style={fieldStyle} value={(selectedComp.config as any)?.llm_provider ?? ''}
                      onChange={e => updateComponent(selectedComp.instance_id, { config: { ...selectedComp.config, llm_provider: e.target.value } })}>
                      <option value="">— inherit —</option>
                      <option value="anthropic">anthropic</option>
                      <option value="openai">openai</option>
                      <option value="gemini">gemini</option>
                    </select>
                  </div>
                  <div>
                    <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>LLM Model</label>
                    <input style={fieldStyle} value={(selectedComp.config as any)?.llm_model ?? ''}
                      onChange={e => updateComponent(selectedComp.instance_id, { config: { ...selectedComp.config, llm_model: e.target.value } })} />
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                    {(['max_iterations', 'history_window', 'max_parallel_tools', 'budget_tokens'] as const).map(field => (
                      <div key={field}>
                        <label style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.05em', display: 'block', marginBottom: 4 }}>{field.replace(/_/g, ' ')}</label>
                        <input type="number" style={fieldStyle} value={(selectedComp.config as any)?.[field] ?? ''}
                          onChange={e => updateComponent(selectedComp.instance_id, { config: { ...selectedComp.config, [field]: e.target.value === '' ? null : Number(e.target.value) } })} />
                      </div>
                    ))}
                  </div>
                </>
              )}
              {selectedComp.definition_ref.kind !== 'orchestrator' && (
                <div>
                  <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Config (JSON)</label>
                  <textarea
                    style={{ ...fieldStyle, height: 180, resize: 'vertical', fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}
                    value={JSON.stringify(selectedComp.config, null, 2)}
                    onChange={e => {
                      try { updateComponent(selectedComp.instance_id, { config: JSON.parse(e.target.value) }); } catch { /* invalid JSON — ignore */ }
                    }}
                  />
                </div>
              )}
            </div>
          )}

          {selectedEP && draft && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Slug</label>
                <input
                  style={fieldStyle} value={selectedEP.slug}
                  onChange={e => updateEntryPoint(selectedEP.instance_id, { slug: e.target.value })}
                />
              </div>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Protocol</label>
                <select style={fieldStyle} value={selectedEP.protocol}
                  onChange={e => updateEntryPoint(selectedEP.instance_id, { protocol: e.target.value as any })}>
                  {protocolOptions.map(p => <option key={p} value={p}>{p}</option>)}
                </select>
              </div>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Root Orchestrator</label>
                <select style={fieldStyle} value={selectedEP.root}
                  onChange={e => updateEntryPoint(selectedEP.instance_id, { root: e.target.value })}>
                  <option value="">— none —</option>
                  {draft.components.filter(c => c.definition_ref.kind === 'orchestrator').map(c => (
                    <option key={c.instance_id} value={c.instance_id}>{c.instance_id}</option>
                  ))}
                </select>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Toast */}
      {toast && (
        <div style={{
          position: 'fixed', bottom: 40, left: '50%', transform: 'translateX(-50%)',
          padding: '10px 20px', borderRadius: 8, fontSize: 13, fontWeight: 600, zIndex: 9999,
          background: toast.ok ? 'rgba(74,222,128,0.15)' : C.errorBg,
          border: `1px solid ${toast.ok ? C.greenBorder : 'rgba(255,180,171,0.3)'}`,
          color: toast.ok ? C.green : C.error,
          boxShadow: '0 4px 20px rgba(0,0,0,0.4)',
          backdropFilter: 'blur(8px)',
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
const EP_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2' };
const EP_LABEL: Record<string, string> = { websocket: 'WebSocket', sse: 'SSE', webrtc: 'WebRTC', a2a: 'A2A' };

function epIconColor(type: string): { color: string; glow: string; border: string } {
  if (type === 'websocket') return { color: '#00d1ff', glow: 'rgba(0,209,255,0.25)', border: 'rgba(0,209,255,0.45)' };
  if (type === 'sse')       return { color: '#a78bfa', glow: 'rgba(167,139,250,0.22)', border: 'rgba(167,139,250,0.42)' };
  if (type === 'webrtc')    return { color: '#a78bfa', glow: 'rgba(167,139,250,0.22)', border: 'rgba(167,139,250,0.42)' };
  if (type === 'a2a')       return { color: '#f59e0b', glow: 'rgba(245,158,11,0.22)', border: 'rgba(245,158,11,0.42)' };
  return { color: '#94a3b8', glow: 'rgba(148,163,184,0.15)', border: 'rgba(148,163,184,0.3)' };
}

// ── AppCard sub-component ─────────────────────────────────────────────────────
function AppCard({
  app,
  liveness,
  sessionCount,
  selected,
  onToggleSelect,
  onEdit,
  onSessions,
  onRuntime,
  onToggle,
  onDelete,
  onRename,
}: {
  app: Application;
  liveness: { reachable: boolean; latency_ms: number | null } | null;
  sessionCount: number;
  selected?: boolean;
  onToggleSelect?: (id: string, checked: boolean) => void;
  onEdit: (a: Application) => void;
  onSessions: (a: Application) => void;
  onRuntime: (a: Application) => void;
  onToggle: (a: Application) => void;
  onDelete: (a: Application) => void;
  onRename: (a: Application) => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [toggling, setToggling] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!menuOpen) return;
    function handler(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as unknown as globalThis.Node)) setMenuOpen(false);
    }
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [menuOpen]);

  const enabledEps = (app.entry_points ?? []).filter(e => e.enabled);
  const firstEp = enabledEps[0] ?? app.entry_points?.[0];
  const ep = epIconColor(firstEp?.entry_point_type ?? 'websocket');

  // Liveness derived from multiplexed WS push (no per-card polling)
  const reachable = app.enabled ? (liveness?.reachable ?? null) : false;
  const latencyMs = liveness?.latency_ms ?? null;

  const statusColor  = !app.enabled ? C.error : reachable === null ? C.textMuted : reachable ? C.green : '#f59e0b';
  const statusLabel  = !app.enabled ? 'disabled' : reachable === null ? 'checking…' : reachable ? 'live' : 'unreachable';
  const statusBg     = !app.enabled ? 'rgba(255,180,171,0.1)' : reachable === null ? 'rgba(255,255,255,0.04)' : reachable ? 'rgba(74,222,128,0.08)' : 'rgba(245,158,11,0.08)';
  const statusBorder = !app.enabled ? 'rgba(255,180,171,0.3)' : reachable === null ? 'rgba(255,255,255,0.1)' : reachable ? C.greenBorder : 'rgba(245,158,11,0.4)';

  const chromaAccent = !app.enabled ? '#64748b' : reachable === false ? '#f59e0b' : '#6366f1';
  const chromaGrad   = `linear-gradient(145deg, ${chromaAccent}1a 0%, ${chromaAccent}08 40%, #07090f 100%)`;

  // Publish badge
  const publishBadge = app.active_revision != null
    ? { label: `Rev ${app.active_revision} · live`, color: C.green, bg: 'rgba(74,222,128,0.10)', border: C.greenBorder }
    : { label: 'Draft · not live', color: '#f59e0b', bg: 'rgba(245,158,11,0.08)', border: 'rgba(245,158,11,0.3)' };

  // Orchestrator summary
  const orch = (app.app_orchestrators ?? [])[0];
  const orchLabel = orch?.display_name || orch?.name;
  const orchModel = orch?.llm_model ?? null;

  // Inline EP URLs (resolve host from window)
  function epUrls(epRow: EntryPoint): Array<{ label: string; val: string; icon: string }> {
    const host = typeof window !== 'undefined' ? window.location.hostname : 'localhost';
    const port = typeof window !== 'undefined' ? (window.location.port || (window.location.protocol === 'https:' ? '443' : '80')) : '8088';
    const portSuffix = (port === '80' || port === '443') ? '' : `:${port}`;
    const http = window.location.protocol === 'https:' ? 'https' : 'http';
    const ws   = window.location.protocol === 'https:' ? 'wss'  : 'ws';
    const base = `${host}${portSuffix}`;
    const t = epRow.entry_point_type;
    if (t === 'websocket') return [{ label: 'WS', val: `${ws}://${base}/apps/${epRow.slug}/ws`, icon: 'electrical_services' }];
    if (t === 'sse')       return [
      { label: 'SSE', val: `${http}://${base}/apps/${epRow.slug}/sse`, icon: 'stream' },
      { label: 'REST', val: `${http}://${base}/apps/${epRow.slug}`, icon: 'api' },
    ];
    if (t === 'webrtc')    return [
      { label: 'Voice', val: `${http}://${base}/apps/${epRow.slug}/voice`, icon: 'mic' },
      { label: 'Token', val: `${http}://${base}/apps/${epRow.slug}/webrtc/token`, icon: 'token' },
    ];
    if (t === 'a2a')       return [
      { label: 'A2A', val: `${http}://${base}/a2a/${epRow.slug}`, icon: 'smart_toy' },
      { label: 'Card', val: `${http}://${base}/a2a/${epRow.slug}/.well-known/agent.json`, icon: 'badge' },
    ];
    return [];
  }

  const hasRuntime = app.runtime_config && Object.values(app.runtime_config).some(v => v !== null && !(Array.isArray(v) && v.length === 0));

  return (
    <div
      className="app-glass-card chroma-card"
      style={{
        borderRadius: 16, overflow: 'visible', display: 'flex', flexDirection: 'column', position: 'relative',
        outline: selected ? '2px solid #00d1ff' : undefined,
        '--card-border': chromaAccent,
        '--card-gradient': chromaGrad,
      } as React.CSSProperties}
    >
      {/* Bulk-select checkbox */}
      {onToggleSelect && (
        <input
          type="checkbox"
          checked={!!selected}
          onChange={(e) => { e.stopPropagation(); onToggleSelect(app.id, e.target.checked); }}
          title="Select for bulk delete"
          style={{ position: 'absolute', top: 10, left: 10, width: 16, height: 16, accentColor: '#00d1ff', cursor: 'pointer', zIndex: 10 }}
        />
      )}

      {/* ── Top section ── */}
      <div style={{ padding: '20px 20px 0', display: 'flex', flexDirection: 'column', gap: 12 }}>

        {/* Row 1: icon + name + publish badge + menu */}
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
          {/* App icon */}
          <div style={{
            width: 48, height: 48, borderRadius: 12, flexShrink: 0,
            background: `radial-gradient(circle at 30% 30%, ${ep.glow}, transparent 70%)`,
            border: `1px solid ${ep.border}`,
            display: 'flex', alignItems: 'center', justifyContent: 'center', position: 'relative',
          }}>
            <span className="material-symbols-outlined" style={{ fontSize: 22, color: ep.color }}>
              {EP_ICON[firstEp?.entry_point_type ?? ''] ?? 'extension'}
            </span>
            {enabledEps.length > 1 && (
              <span style={{
                position: 'absolute', top: -6, right: -6,
                minWidth: 18, height: 18, borderRadius: 9,
                background: '#00d1ff', color: '#021520',
                fontSize: 10, fontWeight: 700,
                display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '0 4px',
              }}>{enabledEps.length}</span>
            )}
          </div>

          {/* Name + subtitle */}
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontWeight: 700, fontSize: 15, color: C.text, fontFamily: 'Geist, sans-serif', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {app.name}
            </div>
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 2 }}>
              {enabledEps.length} entry point{enabledEps.length !== 1 ? 's' : ''}
              {' · '}
              <span style={{ color: publishBadge.color }}>{publishBadge.label}</span>
            </div>
          </div>

          {/* Three-dot menu */}
          <div ref={menuRef} style={{ position: 'relative', flexShrink: 0 }} onClick={e => e.stopPropagation()}>
            <button
              onClick={() => setMenuOpen(v => !v)}
              style={{ width: 30, height: 30, borderRadius: 7, cursor: 'pointer', background: 'var(--tm-btn-2-bg)', border: '1px solid var(--tm-btn-2-border)', color: 'var(--tm-card-text-muted)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
            >
              <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
                <circle cx="8" cy="3" r="1.5"/><circle cx="8" cy="8" r="1.5"/><circle cx="8" cy="13" r="1.5"/>
              </svg>
            </button>
            {menuOpen && (
              <div style={{
                position: 'absolute', top: 34, right: 0, zIndex: 50, minWidth: 140,
                background: 'var(--tm-menu-bg)', border: '1px solid var(--tm-menu-border)',
                borderRadius: 10, boxShadow: '0 8px 32px rgba(0,0,0,0.35)', overflow: 'hidden',
              }}>
                <button
                  onClick={() => { setMenuOpen(false); onRename(app); }}
                  style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', padding: '10px 14px', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13, color: C.text, fontWeight: 500 }}
                  onMouseEnter={e => (e.currentTarget.style.background = 'rgba(255,255,255,0.06)')}
                  onMouseLeave={e => (e.currentTarget.style.background = 'none')}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 16 }}>edit</span>
                  Rename
                </button>
                <div style={{ height: 1, background: 'var(--tm-divider)', margin: '0 10px' }} />
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

        {/* Row 2: live status bar */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 12px', borderRadius: 10, background: statusBg, border: `1px solid ${statusBorder}` }}>
          {app.enabled && (
            <span style={{ width: 7, height: 7, borderRadius: '50%', flexShrink: 0, background: statusColor, boxShadow: reachable ? `0 0 7px ${statusColor}` : 'none' }} />
          )}
          <span style={{ fontSize: 12, fontWeight: 700, color: statusColor }}>{statusLabel}</span>
          {reachable && latencyMs != null && (
            <span style={{ fontSize: 11, color: C.textMuted, marginLeft: 'auto' }}>{latencyMs}ms</span>
          )}
        </div>

        {/* Row 3: orchestrator + access tiles */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'var(--tm-filter-bg)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16, color: '#a78bfa', flexShrink: 0 }}>hub</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Orchestrator</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {orchLabel
                  ? <>{orchLabel}{orchModel && <span style={{ color: C.textMuted, fontWeight: 400, marginLeft: 4 }}>· {orchModel.split('/').pop()?.split('-').slice(0,2).join('-')}</span>}</>
                  : <span style={{ color: C.textMuted, fontStyle: 'italic' }}>none — publish to activate</span>
                }
              </div>
            </div>
          </div>
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'var(--tm-filter-bg)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16, color: '#f59e0b', flexShrink: 0 }}>lock</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Access</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600 }}>
                Bearer token
                <span style={{ fontSize: 10, color: C.textMuted, fontWeight: 400, display: 'block' }}>Authorization: Bearer …</span>
              </div>
            </div>
          </div>
        </div>

        {/* Row 4: entry point URL rows — only enabled EPs */}
        {enabledEps.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, paddingBottom: 4 }}>
            <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 2 }}>Entry Points</div>
            {enabledEps.map(epRow => {
              const urls = epUrls(epRow);
              const epC = epIconColor(epRow.entry_point_type);
              const primaryUrl = urls[0];
              return (
                <div key={epRow.id} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.06)' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 13, color: epC.color, flexShrink: 0 }}>{EP_ICON[epRow.entry_point_type] ?? 'bolt'}</span>
                  <code style={{ fontSize: 10, fontFamily: 'JetBrains Mono, monospace', color: C.textMuted, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {primaryUrl ? primaryUrl.val : epRow.slug}
                  </code>
                  {primaryUrl && (
                    <button
                      onClick={() => {
                        const val = primaryUrl.val;
                        if (navigator.clipboard) {
                          navigator.clipboard.writeText(val).catch(() => fallbackCopy(val));
                        } else {
                          fallbackCopy(val);
                        }
                      }}
                      title="Copy URL"
                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center', padding: 2, flexShrink: 0 }}
                      onMouseEnter={e => (e.currentTarget.style.color = C.text)}
                      onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 13 }}>content_copy</span>
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* ── Action buttons ── */}
      <div style={{ borderTop: '1px solid var(--tm-divider)', padding: '10px 14px', display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        {/* Sessions */}
        <button
          className="app-card-btn"
          onClick={() => onSessions(app)}
          style={{
            flex: '2 1 80px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
            background: sessionCount > 0 ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.03)',
            color: sessionCount > 0 ? '#00f0ff' : C.textMuted,
            border: `1px solid ${sessionCount > 0 ? 'rgba(0,240,255,0.35)' : 'rgba(255,255,255,0.1)'}`,
          }}
          onMouseEnter={e => { e.currentTarget.style.background = 'rgba(0,240,255,0.12)'; e.currentTarget.style.color = '#00f0ff'; }}
          onMouseLeave={e => { e.currentTarget.style.background = sessionCount > 0 ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.03)'; e.currentTarget.style.color = sessionCount > 0 ? '#00f0ff' : C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>person</span>
          Sessions
          {sessionCount > 0 && (
            <span style={{ background: '#00f0ff', color: '#000', fontSize: 10, fontWeight: 800, borderRadius: 8, padding: '0px 5px', lineHeight: '16px', minWidth: 16, textAlign: 'center' }}>{sessionCount}</span>
          )}
        </button>

        {/* Builder (was "Definition") */}
        <button className="app-card-btn app-card-btn--open" onClick={() => onEdit(app)}
          style={{ flex: '1 1 60px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>hub</span>
          Builder
        </button>

        {/* Runtime */}
        <button
          className="app-card-btn"
          onClick={() => onRuntime(app)}
          title="Runtime policy"
          style={{
            flex: '1 1 60px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
            background: hasRuntime ? 'rgba(251,146,60,0.1)' : 'rgba(255,255,255,0.03)',
            color: hasRuntime ? '#fb923c' : C.textMuted,
            border: `1px solid ${hasRuntime ? 'rgba(251,146,60,0.4)' : 'rgba(255,255,255,0.1)'}`,
          }}
          onMouseEnter={e => { e.currentTarget.style.background = 'rgba(251,146,60,0.15)'; e.currentTarget.style.color = '#fb923c'; }}
          onMouseLeave={e => { e.currentTarget.style.background = hasRuntime ? 'rgba(251,146,60,0.1)' : 'rgba(255,255,255,0.03)'; e.currentTarget.style.color = hasRuntime ? '#fb923c' : C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>tune</span>
          Runtime
        </button>

        {/* Enable / Disable toggle with feedback */}
        <button
          className={`app-card-btn ${app.enabled ? 'app-card-btn--toggle-on' : 'app-card-btn--toggle-off'}`}
          onClick={async () => {
            if (toggling) return;
            setToggling(true);
            try { await (onToggle(app) as unknown as Promise<void>); } finally { setToggling(false); }
          }}
          disabled={toggling}
          title={app.enabled ? 'Disable this application' : 'Enable this application'}
          style={{ flex: '1 1 60px', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5, opacity: toggling ? 0.6 : 1 }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>
            {toggling ? 'hourglass_empty' : app.enabled ? 'toggle_on' : 'toggle_off'}
          </span>
          {toggling ? '…' : app.enabled ? 'Disable' : 'Enable'}
        </button>
      </div>
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

// ── Sessions live hook ────────────────────────────────────────────────────────
function useDashSessions(token: string | null, appId: string | null): {
  sessions: SessionInfo[];
  connected: boolean;
} {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!token || !appId) return;
    const wsBase = window.location.origin.replace(/^http/, 'ws').replace(/^https/, 'wss');
    const wsUrl = `${wsBase}/ws/dashboard?token=${token}`;
    let ws: WebSocket;
    let dead = false;

    function connect() {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        ws.send(JSON.stringify({ type: 'subscribe', channels: [`sessions:${appId}`] }));
        setConnected(true);
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          const ch = `sessions:${appId}`;
          if (msg.channel !== ch) return;
          const evt = msg.event;
          if (evt?.type === 'session_snapshot') {
            setSessions(evt.sessions ?? []);
          } else if (evt?.type === 'session_start' && evt.session_info) {
            setSessions(prev => {
              if (prev.find(s => s.session_id === evt.session_id)) return prev;
              return [...prev, evt.session_info as SessionInfo];
            });
          } else if (evt?.type === 'session_end') {
            setSessions(prev => prev.filter(s => s.session_id !== evt.session_id));
          } else if (evt?.type === 'session_update' && evt.session_id) {
            setSessions(prev => prev.map(s =>
              s.session_id === evt.session_id
                ? { ...s, ...evt.session_info }
                : s
            ));
          }
        } catch {}
      };
      ws.onclose = () => {
        setConnected(false);
        if (!dead) setTimeout(connect, 4000);
      };
      ws.onerror = () => ws.close();
    }

    connect();
    return () => {
      dead = true;
      ws?.close();
    };
  }, [token, appId]);

  return { sessions, connected };
}

// ── RuntimeView ───────────────────────────────────────────────────────────────
const PROVIDER_LIST = ['anthropic', 'openai', 'groq', 'gemini'] as const;

function RuntimeView({ app, onBack }: { app: Application; onBack: () => void }) {
  const emptyRuntime = { max_concurrent_sessions: null, rate_limit_rpm: null, blocked_tokens: [], blocked_user_ids: [], session_timeout_minutes: null };
  const [cfg, setCfg] = useState<import('@/lib/api').AppRuntimeConfig>(app.runtime_config ?? emptyRuntime);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Tag input helpers
  const [tokensInput, setTokensInput] = useState((app.runtime_config?.blocked_tokens ?? []).join('\n'));
  const [usersInput, setUsersInput] = useState((app.runtime_config?.blocked_user_ids ?? []).join(', '));

  // Provider keys state
  type KeyStatus = { provider: string; key_set: boolean; key_hint?: string };
  const [keyStatuses, setKeyStatuses] = useState<KeyStatus[]>([]);
  const [keyInputs, setKeyInputs] = useState<Record<string, string>>({});
  const [keySaving, setKeySaving] = useState<string | null>(null);
  const [keyMsg, setKeyMsg] = useState<Record<string, string>>({});

  useEffect(() => {
    themApi.getProviderKeys(app.id)
      .then(keys => setKeyStatuses(keys))
      .catch(() => {});
  }, [app.id]);

  function getKeyStatus(provider: string): KeyStatus {
    return keyStatuses.find(k => k.provider === provider) ?? { provider, key_set: false };
  }

  async function handleSaveKey(provider: string) {
    const key = (keyInputs[provider] ?? '').trim();
    if (!key) return;
    setKeySaving(provider);
    try {
      await themApi.setProviderKey(app.id, provider, key);
      const keys = await themApi.getProviderKeys(app.id);
      setKeyStatuses(keys);
      setKeyInputs(ki => ({ ...ki, [provider]: '' }));
      setKeyMsg(m => ({ ...m, [provider]: 'Saved' }));
      setTimeout(() => setKeyMsg(m => ({ ...m, [provider]: '' })), 2500);
    } catch (e: unknown) {
      setKeyMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setKeySaving(null);
    }
  }

  async function handleDeleteKey(provider: string) {
    setKeySaving(provider);
    try {
      await themApi.deleteProviderKey(app.id, provider);
      const keys = await themApi.getProviderKeys(app.id);
      setKeyStatuses(keys);
      setKeyMsg(m => ({ ...m, [provider]: 'Removed' }));
      setTimeout(() => setKeyMsg(m => ({ ...m, [provider]: '' })), 2500);
    } catch (e: unknown) {
      setKeyMsg(m => ({ ...m, [provider]: e instanceof Error ? e.message : 'Failed' }));
    } finally {
      setKeySaving(null);
    }
  }

  async function handleSave() {
    setSaving(true);
    setError(null);
    try {
      const parsedUsers = usersInput.split(/[\s,]+/).map(s => s.trim()).filter(Boolean).map(Number).filter(n => !isNaN(n));
      const parsedTokens = tokensInput.split(/\n/).map(s => s.trim()).filter(Boolean);
      const payload = { ...cfg, blocked_tokens: parsedTokens, blocked_user_ids: parsedUsers };
      await themApi.putAppRuntime(app.id, payload);
      setCfg(payload);
      setSaved(true);
      setTimeout(() => setSaved(false), 2500);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }

  const fieldStyle: React.CSSProperties = {
    width: '100%', padding: '10px 12px', borderRadius: 8,
    border: '1px solid rgba(255,255,255,0.12)', background: 'rgba(255,255,255,0.05)',
    color: C.text, fontSize: 14, outline: 'none', boxSizing: 'border-box',
  };
  const labelStyle: React.CSSProperties = { fontSize: 12, fontWeight: 600, color: C.textMuted, letterSpacing: '0.06em', textTransform: 'uppercase', marginBottom: 6, display: 'block' };
  const sectionStyle: React.CSSProperties = { ...glass, borderRadius: 12, padding: '20px 24px', display: 'flex', flexDirection: 'column', gap: 16, marginBottom: 20 };

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: '40px 40px 60px', background: C.bg }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 32 }}>
        <button onClick={onBack} style={{ background: 'none', border: 'none', color: C.textMuted, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 18 }}>arrow_back</span>
          Applications
        </button>
        <span style={{ color: 'rgba(255,255,255,0.2)', fontSize: 18 }}>/</span>
        <span style={{ fontSize: 14, color: C.text, fontWeight: 600 }}>{app.name}</span>
        <span style={{ color: 'rgba(255,255,255,0.2)', fontSize: 18 }}>/</span>
        <span style={{ fontSize: 14, color: '#fb923c', fontWeight: 700 }}>Runtime Policy</span>
      </div>

      <div style={{ maxWidth: 640 }}>
        {/* Session Limits */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>Session Limits</div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
            <div>
              <label style={labelStyle}>Max Concurrent Sessions</label>
              <input type="number" min={1} placeholder="Unlimited"
                value={cfg.max_concurrent_sessions ?? ''} style={fieldStyle}
                onChange={e => setCfg(c => ({ ...c, max_concurrent_sessions: e.target.value === '' ? null : parseInt(e.target.value) }))} />
              <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>App-wide soft cap. Empty = unlimited.</div>
            </div>
            <div>
              <label style={labelStyle}>Session Timeout (minutes)</label>
              <input type="number" min={1} placeholder="No timeout"
                value={cfg.session_timeout_minutes ?? ''} style={fieldStyle}
                onChange={e => setCfg(c => ({ ...c, session_timeout_minutes: e.target.value === '' ? null : parseInt(e.target.value) }))} />
              <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Advisory. Empty = no timeout.</div>
            </div>
          </div>
        </div>

        {/* Rate Limiting */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>Rate Limiting</div>
          <div>
            <label style={labelStyle}>App Rate Limit (requests per minute)</label>
            <input type="number" min={1} placeholder="Unlimited"
              value={cfg.rate_limit_rpm ?? ''} style={fieldStyle}
              onChange={e => setCfg(c => ({ ...c, rate_limit_rpm: e.target.value === '' ? null : parseInt(e.target.value) }))} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Applied across all entry points of this app. Separate from per-orchestrator rate limits.</div>
          </div>
        </div>

        {/* Access Control */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>Access Control</div>
          <div>
            <label style={labelStyle}>Blocked User IDs (comma-separated)</label>
            <input type="text" placeholder="e.g. 42, 107, 889"
              value={usersInput} style={fieldStyle}
              onChange={e => setUsersInput(e.target.value)} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Connections from these user IDs are rejected before any processing.</div>
          </div>
          <div>
            <label style={labelStyle}>Blocked Token Hashes (one per line)</label>
            <textarea placeholder="sha256 hash of each blocked access token"
              value={tokensInput} rows={4}
              style={{ ...fieldStyle, resize: 'vertical', fontFamily: 'monospace', fontSize: 12 }}
              onChange={e => setTokensInput(e.target.value)} />
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4 }}>Paste the SHA-256 hash of the token (not the raw token). One hash per line.</div>
          </div>
        </div>

        {/* Provider Keys */}
        <div style={sectionStyle}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 4 }}>LLM Provider Keys</div>
          <div style={{ fontSize: 12, color: C.textMuted, marginBottom: 8 }}>
            One API key per provider for this application. Keys are stored encrypted and never exported with the canvas definition.
          </div>
          {PROVIDER_LIST.map(provider => {
            const status = getKeyStatus(provider);
            const isBusy = keySaving === provider;
            const msg = keyMsg[provider] ?? '';
            const isError = msg && msg !== 'Saved' && msg !== 'Removed';
            return (
              <div key={provider} style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                {/* Provider label + key-set badge */}
                <div style={{ width: 90, flexShrink: 0 }}>
                  <span style={{ fontSize: 13, fontWeight: 600, color: C.text }}>{provider}</span>
                  <span style={{
                    marginLeft: 8, fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 20,
                    background: status.key_set ? 'rgba(74,222,128,0.12)' : 'rgba(251,146,60,0.12)',
                    color: status.key_set ? C.green : '#fb923c',
                    border: `1px solid ${status.key_set ? 'rgba(74,222,128,0.3)' : 'rgba(251,146,60,0.3)'}`,
                  }}>
                    {status.key_set ? `set ···${status.key_hint ?? ''}` : 'not set'}
                  </span>
                </div>
                {/* Key input */}
                <input
                  type="password"
                  placeholder={status.key_set ? 'Enter new key to replace…' : 'Paste API key…'}
                  value={keyInputs[provider] ?? ''}
                  onChange={e => setKeyInputs(ki => ({ ...ki, [provider]: e.target.value }))}
                  style={{ ...fieldStyle, flex: 1, minWidth: 180, fontSize: 13 }}
                  onKeyDown={e => { if (e.key === 'Enter') handleSaveKey(provider); }}
                />
                {/* Save key button */}
                <button
                  onClick={() => handleSaveKey(provider)}
                  disabled={isBusy || !(keyInputs[provider] ?? '').trim()}
                  style={{ padding: '8px 14px', borderRadius: 8, border: `1px solid ${C.purpleBorder}`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap', opacity: isBusy || !(keyInputs[provider] ?? '').trim() ? 0.5 : 1 }}
                >
                  {isBusy ? '…' : 'Save'}
                </button>
                {/* Remove button — only shown when a key is set */}
                {status.key_set && (
                  <button
                    onClick={() => handleDeleteKey(provider)}
                    disabled={isBusy}
                    style={{ padding: '8px 10px', borderRadius: 8, border: '1px solid rgba(248,113,113,0.3)', background: 'rgba(248,113,113,0.07)', color: '#f87171', cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: isBusy ? 0.5 : 1 }}
                  >
                    Remove
                  </button>
                )}
                {msg && <span style={{ fontSize: 12, color: isError ? C.error : C.green, fontWeight: 600 }}>{msg}</span>}
              </div>
            );
          })}
        </div>

        {/* Save */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <button
            onClick={handleSave}
            disabled={saving}
            style={{
              padding: '11px 28px', borderRadius: 8, border: 'none', cursor: saving ? 'not-allowed' : 'pointer',
              background: '#fb923c', color: '#000', fontSize: 14, fontWeight: 700,
              opacity: saving ? 0.6 : 1,
            }}
          >
            {saving ? 'Saving…' : 'Save Runtime Config'}
          </button>
          {saved && <span style={{ fontSize: 13, color: C.green }}>Saved</span>}
          {error && <span style={{ fontSize: 13, color: C.error }}>{error}</span>}
        </div>
      </div>
    </div>
  );
}

// ── SessionsView ──────────────────────────────────────────────────────────────
const SESSIONS_STYLES = `
@keyframes sess-pulse {
  0%,100% { opacity:1; transform:scale(1); }
  50%      { opacity:0.7; transform:scale(1.08); }
}
@keyframes edge-dash-cyan {
  to { stroke-dashoffset: -24; }
}
@keyframes edge-dash-purple {
  to { stroke-dashoffset: -20; }
}
@keyframes edge-glow-cyan {
  0%,100% { filter: drop-shadow(0 0 3px rgba(0,240,255,0.55)) drop-shadow(0 0 7px rgba(0,240,255,0.28)); }
  50%     { filter: drop-shadow(0 0 7px rgba(0,240,255,1.0)) drop-shadow(0 0 16px rgba(0,240,255,0.55)); }
}
@keyframes edge-glow-purple {
  0%,100% { filter: drop-shadow(0 0 3px rgba(208,188,255,0.5)) drop-shadow(0 0 7px rgba(208,188,255,0.25)); }
  50%     { filter: drop-shadow(0 0 7px rgba(208,188,255,0.95)) drop-shadow(0 0 16px rgba(208,188,255,0.5)); }
}
.react-flow__edge.active-ep-orch path.react-flow__edge-path {
  stroke-dasharray: 8 4;
  animation: edge-dash-cyan 0.5s linear infinite, edge-glow-cyan 1.8s ease-in-out infinite;
}
.react-flow__edge.active-orch-agent path.react-flow__edge-path {
  stroke-dasharray: 6 4;
  animation: edge-dash-purple 0.65s linear infinite, edge-glow-purple 1.8s ease-in-out infinite;
}
.sess-badge {
  position:absolute; top:-8px; right:-8px;
  min-width:20px; height:20px; border-radius:10px;
  background:rgba(0,240,255,0.15); border:1.5px solid rgba(0,240,255,0.55);
  color:#00f0ff; font-size:10px; font-weight:700;
  display:flex; align-items:center; justify-content:center; padding:0 4px;
  font-family:Inter,sans-serif; pointer-events:none; z-index:20;
  box-shadow:0 0 8px rgba(0,240,255,0.3);
}
.sess-badge.active { animation:sess-pulse 1.8s ease-in-out infinite; }
.sess-row {
  display:flex; align-items:flex-start; gap:10px;
  padding:10px 14px; border-radius:10px; cursor:pointer;
  border:1px solid transparent; transition:all 0.15s ease;
}
.sess-row:hover { background:rgba(0,240,255,0.04); border-color:rgba(0,240,255,0.18); }
.sess-row.selected { background:rgba(0,240,255,0.07); border-color:rgba(0,240,255,0.35); }
`;

// Read-only canvas node wrappers — same visuals as builder, but with session count badge
function EPNodeRO({ data }: { data: { label?: string; slug?: string; epType?: string; _sessCount?: number; _heatStyle?: React.CSSProperties } }) {
  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };
  const msIcon = EP_MS_ICON[data.epType ?? 'websocket'] ?? 'bolt';
  const count = data._sessCount ?? 0;
  const accent = C.cyan;
  const badgeTitle = `${count} sessions`;
  const baseStyle: React.CSSProperties = {
    width: 56, height: 56, borderRadius: '50%',
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    border: `2px solid ${count > 0 ? accent : 'rgba(0,240,255,0.25)'}`,
    transition: 'all 0.3s ease',
  };
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {count > 0 && <div className={`sess-badge${count > 0 ? ' active' : ''}`} title={badgeTitle}>{count}</div>}
      <div style={{ ...baseStyle, ...(count > 0 && data._heatStyle ? data._heatStyle : {}) }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent }}>{msIcon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center' }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: C.text, lineHeight: 1.3 }}>
          {data.label || 'EP'}
        </div>
        {data.slug && <div style={{ fontSize: 10, color: C.cyan, fontFamily: 'JetBrains Mono, monospace', opacity: 0.8, marginTop: 1 }}>{data.slug}</div>}
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

function OrchNodeRO({ data }: { data: { displayName?: string; _sessCount?: number; _heatStyle?: React.CSSProperties } }) {
  const count = data._sessCount ?? 0;
  const accent = C.purple;
  const baseStyle: React.CSSProperties = {
    width: 56, height: 56, borderRadius: '50%',
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    border: `2px solid ${count > 0 ? accent : 'rgba(208,188,255,0.25)'}`,
    transition: 'all 0.3s ease',
  };
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      {count > 0 && <div className={`sess-badge${count > 0 ? ' active' : ''}`} style={{ background: 'rgba(208,188,255,0.15)', border: '1.5px solid rgba(208,188,255,0.55)', color: C.purple, boxShadow: '0 0 8px rgba(208,188,255,0.3)' }}>{count}</div>}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div style={{ ...baseStyle, ...(count > 0 && data._heatStyle ? data._heatStyle : {}) }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent }}>hub</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 120 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {data.displayName}
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

function AgentNodeRO({ data }: { data: { displayName?: string; icon?: string; tags?: string[] } }) {
  const isInternal = data.tags?.includes('internal') ?? false;
  const accent = isInternal ? '#a0f0d0' : C.green;
  const icon = data.icon || 'smart_toy';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}>
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div style={{
        width: 56, height: 56, borderRadius: '50%',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: 'transparent', border: `2px solid rgba(74,222,128,0.25)`,
        transition: 'all 0.3s ease',
      }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent }}>{icon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 110 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {data.displayName}
        </div>
      </div>
    </div>
  );
}

const RO_NODE_TYPES: NodeTypes = {
  entryPoint: EPNodeRO as any,
  orchestrator: OrchNodeRO as any,
  agent: AgentNodeRO as any,
};

function elapsed(iso: string): string {
  const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`;
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`;
}

const MON_DEFAULTS: MonitoringConfig = {
  heatmap_low: 1, heatmap_medium: 10, heatmap_high: 50,
  edge_thin: 1, edge_medium: 10, edge_thick: 50,
  panel_max_sessions: 50, stats_window_seconds: 300,
};

function heatmapStyle(count: number, cfg: MonitoringConfig, type: 'ep' | 'orch'): React.CSSProperties {
  if (count <= 0) return {};
  const accent = type === 'ep' ? C.cyan : C.purple;
  const lowColor  = type === 'ep' ? 'rgba(0,240,255,0.10)'     : 'rgba(208,188,255,0.10)';
  const midColor  = type === 'ep' ? 'rgba(0,240,255,0.20)'     : 'rgba(208,188,255,0.20)';
  const highColor = type === 'ep' ? 'rgba(0,240,255,0.35)'     : 'rgba(208,188,255,0.35)';
  const lowGlow   = type === 'ep' ? '0 0 10px rgba(0,240,255,0.25)'     : '0 0 10px rgba(208,188,255,0.25)';
  const midGlow   = type === 'ep' ? '0 0 18px rgba(0,240,255,0.5)'      : '0 0 18px rgba(208,188,255,0.5)';
  const highGlow  = type === 'ep' ? '0 0 28px rgba(0,240,255,0.85)'     : '0 0 28px rgba(208,188,255,0.85)';
  const borderW   = count >= cfg.heatmap_high ? 3 : count >= cfg.heatmap_medium ? 2.5 : 2;
  const bg    = count >= cfg.heatmap_high ? highColor : count >= cfg.heatmap_medium ? midColor : lowColor;
  const glow  = count >= cfg.heatmap_high ? highGlow  : count >= cfg.heatmap_medium ? midGlow  : lowGlow;
  return { background: bg, border: `${borderW}px solid ${accent}`, boxShadow: glow };
}

function edgeStrokeWidth(count: number, cfg: MonitoringConfig): number {
  if (count >= cfg.edge_thick)  return 5;
  if (count >= cfg.edge_medium) return 3;
  if (count >= cfg.edge_thin)   return 1.5;
  return 1;
}

function SessionsView({
  app: initialApp,
  agents,
  onBack,
  token,
}: {
  app: Application;
  agents: Agent[];
  onBack: () => void;
  token: string | null;
}) {
  const [app, setApp] = useState(initialApp);
  const { sessions, connected } = useDashSessions(token, app.id);
  const [selectedSession, setSelectedSession] = useState<SessionInfo | null>(null);
  const [tick, setTick] = useState(0);
  const [monCfg, setMonCfg] = useState<MonitoringConfig>(MON_DEFAULTS);

  // Optimistic terminate: sessions hidden pending WS confirmation of session_end
  // hiddenSessions doubles as "terminating" — once hidden the row is gone from the list
  const [hiddenSessions, setHiddenSessions] = useState<Set<string>>(new Set());

  // When session_end arrives via WS, useDashSessions removes it from sessions[];
  // no further action needed — the hidden entry is simply never un-hidden for dead sessions.

  async function handleTerminate(sid: string) {
    setHiddenSessions(h => new Set(h).add(sid));
    try {
      await themApi.disconnectSession(sid);
    } catch {
      // Signal failed — un-hide so user can retry
      setHiddenSessions(h => { const n = new Set(h); n.delete(sid); return n; });
    }
  }

  // Load monitoring config once
  useEffect(() => {
    themApi.getMonitoringConfig().then(setMonCfg).catch(() => {});
  }, []);

  // Re-render elapsed times every 5s
  useEffect(() => {
    const iv = setInterval(() => setTick(t => t + 1), 5000);
    return () => clearInterval(iv);
  }, []);

  // Build read-only nodes/edges from app, with session counts overlaid
  const epCountBySlug = new Map<string, number>();
  const visibleSessions = sessions.filter(s => !hiddenSessions.has(s.session_id));
  visibleSessions.forEach(s => {
    if (s.ep_slug) epCountBySlug.set(s.ep_slug, (epCountBySlug.get(s.ep_slug) ?? 0) + 1);
  });

  const { nodes: baseNodes, edges: baseEdges } = buildNodesFromApp(app, agents);

  // Build active node id sets for edge coloring
  const activeEpNodeIds = new Set<string>();
  const activeOrchNodeIds = new Set<string>();

  const nodes = baseNodes.map(n => {
    if (n.type === 'entryPoint' && n.data?.slug) {
      const slug = n.data.slug as string;
      const count = epCountBySlug.get(slug) ?? 0;
      if (count > 0) activeEpNodeIds.add(n.id);
      return { ...n, data: { ...n.data, _sessCount: count, _heatStyle: heatmapStyle(count, monCfg, 'ep') } };
    }
    if (n.type === 'orchestrator') {
      const orchName = (n.data as any)?.name ?? '';
      const orchCount = visibleSessions.filter(s => s.orchestrator_name === orchName).length;
      if (orchCount > 0) activeOrchNodeIds.add(n.id);
      return { ...n, data: { ...n.data, _sessCount: orchCount, _heatStyle: heatmapStyle(orchCount, monCfg, 'orch') } };
    }
    return n;
  });

  // Which agent slugs are actively being called right now (across all sessions, parallel-safe)
  const activeAgentSlugs = new Set(
    visibleSessions.flatMap(s => s.active_agents ?? [])
  );

  // Count sessions flowing through each edge path for thickness scaling
  const epOrchSessionCount = sessions.length; // total sessions = load on ep→orch path

  // Style edges: EP→orch always active when sessions exist; orch→agent only when that agent is being called
  const edges = baseEdges.map(e => {
    const isEpOrch    = activeEpNodeIds.has(e.source) && activeOrchNodeIds.has(e.target);
    const targetSlug  = (baseNodes.find(n => n.id === e.target)?.data as any)?.name ?? '';
    const isOrchAgent = activeOrchNodeIds.has(e.source) && activeAgentSlugs.has(targetSlug);

    if (isEpOrch) {
      const sw = edgeStrokeWidth(epOrchSessionCount, monCfg);
      return {
        ...e,
        animated: false,
        className: 'active-ep-orch',
        style: { stroke: '#00f0ff', strokeWidth: sw },
      };
    }
    if (isOrchAgent) {
      const orchCount = visibleSessions.filter(s => s.active_agents?.includes(targetSlug)).length;
      const sw = edgeStrokeWidth(orchCount, monCfg);
      return {
        ...e,
        animated: false,
        className: 'active-orch-agent',
        style: { stroke: C.purple, strokeWidth: sw },
      };
    }
    return {
      ...e,
      animated: false,
      style: { stroke: 'rgba(148,163,184,0.18)', strokeWidth: 1, strokeDasharray: '4,4' },
    };
  });

  // Cap session list for UI performance
  const displaySessions = visibleSessions.slice(0, monCfg.panel_max_sessions);
  void tick; // used indirectly by elapsed() rerender

  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg, overflow: 'hidden' }}>
      <style>{CANVAS_STYLES}{SESSIONS_STYLES}</style>

      {/* Top bar */}
      <div style={{
        height: 56, flexShrink: 0,
        display: 'flex', alignItems: 'center', gap: 12,
        padding: '0 20px',
        background: C.surfaceContainer,
        borderBottom: `1px solid ${C.outline}`,
        backdropFilter: 'blur(12px)',
      }}>
        <button
          onClick={onBack}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '7px 14px', borderRadius: 8,
            border: `1px solid ${C.outline}`, background: 'transparent',
            color: C.textMuted, fontSize: 13, fontWeight: 500, cursor: 'pointer',
          }}
          onMouseEnter={e => { e.currentTarget.style.background = 'rgba(255,255,255,0.05)'; e.currentTarget.style.color = C.text; }}
          onMouseLeave={e => { e.currentTarget.style.background = 'transparent'; e.currentTarget.style.color = C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 16 }}>arrow_back</span>
          Back
        </button>

        <div style={{ width: 1, height: 24, background: C.outline, flexShrink: 0 }} />

        <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.cyan }}>hub</span>
        <span style={{ fontSize: 15, fontWeight: 700, color: C.text }}>{app.name}</span>
        <span style={{ fontSize: 12, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>/{app.slug}</span>

        <div style={{ flex: 1 }} />

        {/* Live indicator */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '5px 12px', borderRadius: 20,
          background: connected ? 'rgba(74,222,128,0.08)' : 'rgba(255,255,255,0.04)',
          border: `1px solid ${connected ? C.greenBorder : 'rgba(255,255,255,0.1)'}`,
        }}>
          <div style={{
            width: 7, height: 7, borderRadius: '50%',
            background: connected ? C.green : C.textMuted,
            boxShadow: connected ? '0 0 6px rgba(74,222,128,0.8)' : 'none',
            animation: connected ? 'sess-pulse 2s ease-in-out infinite' : 'none',
          }} />
          <span style={{ fontSize: 12, color: connected ? C.green : C.textMuted, fontWeight: 600 }}>
            {connected ? 'Live' : 'Connecting…'}
          </span>
        </div>

        {/* Session count pill */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '5px 14px', borderRadius: 20,
          background: visibleSessions.length > 0 ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.03)',
          border: `1px solid ${visibleSessions.length > 0 ? C.cyanBorder : 'rgba(255,255,255,0.08)'}`,
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: 14, color: visibleSessions.length > 0 ? C.cyan : C.textMuted }}>person</span>
          <span style={{ fontSize: 13, fontWeight: 700, color: visibleSessions.length > 0 ? C.cyan : C.textMuted }}>
            {visibleSessions.length} active
          </span>
        </div>
      </div>

      {/* Main body: canvas + right panel */}
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>

        {/* Canvas — read-only, no drag, no editor */}
        <div style={{ flex: 1, position: 'relative' }}>
          <ReactFlowProvider>
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={RO_NODE_TYPES}
              fitView
              fitViewOptions={{ padding: 0.25 }}
              nodesDraggable={false}
              nodesConnectable={false}
              elementsSelectable={false}
              panOnDrag={true}
              zoomOnScroll={true}
              style={{ background: C.surfaceLow }}
            >
              <Background variant={BackgroundVariant.Dots} gap={24} size={1} color="rgba(148,163,184,0.12)" />
              <Controls showInteractive={false} style={{ background: C.surface, border: `1px solid ${C.outline}` }} />
            </ReactFlow>
          </ReactFlowProvider>

          {/* Empty state overlay */}
          {visibleSessions.length === 0 && connected && (
            <div style={{
              position: 'absolute', top: '50%', left: '50%',
              transform: 'translate(-50%, -50%)',
              pointerEvents: 'none',
              display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 10,
            }}>
              <span className="material-symbols-outlined" style={{ fontSize: 40, color: 'rgba(148,163,184,0.25)' }}>person_off</span>
              <span style={{ fontSize: 13, color: 'rgba(148,163,184,0.4)', fontWeight: 500 }}>No active sessions</span>
            </div>
          )}
        </div>

        {/* Right panel — session list + detail */}
        <div style={{
          width: 340, flexShrink: 0,
          background: C.surfaceContainer,
          borderLeft: `1px solid ${C.outline}`,
          display: 'flex', flexDirection: 'column',
          overflow: 'hidden',
        }}>
          {/* Panel header */}
          <div style={{
            padding: '14px 16px 10px',
            borderBottom: `1px solid ${C.outline}`,
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          }}>
            <span style={{ fontSize: 13, fontWeight: 700, color: C.text, letterSpacing: 0.2 }}>Active Sessions</span>
            <span style={{
              fontSize: 11, fontWeight: 700, color: C.textMuted,
              background: 'rgba(255,255,255,0.05)', borderRadius: 10,
              padding: '2px 8px', border: `1px solid ${C.outline}`,
            }}>{visibleSessions.length}</span>
          </div>

          {/* Session list */}
          <div style={{ flex: 1, overflowY: 'auto', padding: '8px 8px 0' }}>
            {visibleSessions.length === 0 && connected && (
              <div style={{ padding: '32px 16px', textAlign: 'center', color: C.textMuted, fontSize: 13 }}>
                Waiting for sessions…
              </div>
            )}
            {!connected && (
              <div style={{ padding: '32px 16px', textAlign: 'center', color: C.textMuted, fontSize: 13 }}>
                Connecting…
              </div>
            )}
            {visibleSessions.length > monCfg.panel_max_sessions && (
              <div style={{ margin: '4px 8px 6px', padding: '6px 10px', borderRadius: 8, background: 'rgba(245,158,11,0.08)', border: '1px solid rgba(245,158,11,0.22)', fontSize: 11, color: '#f59e0b' }}>
                Showing {monCfg.panel_max_sessions} of {visibleSessions.length} sessions
              </div>
            )}
            {displaySessions.map(s => {
              const isSelected = selectedSession?.session_id === s.session_id;
              const epType = app.entry_points?.find(ep => ep.slug === s.ep_slug)?.entry_point_type ?? 'websocket';
              const epIcon = EP_MS_ICON[epType] ?? 'bolt';
              const epColor = epType === 'sse' ? '#a78bfa' : C.cyan;
              return (
                <div
                  key={s.session_id}
                  className={`sess-row${isSelected ? ' selected' : ''}`}
                  onClick={() => setSelectedSession(isSelected ? null : s)}
                >
                  {/* EP type icon */}
                  <div style={{
                    width: 32, height: 32, borderRadius: '50%', flexShrink: 0,
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    background: `${epColor}18`, border: `1.5px solid ${epColor}44`,
                  }}>
                    <span className="material-symbols-outlined" style={{ fontSize: 16, color: epColor }}>{epIcon}</span>
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
                      <span style={{ fontSize: 12, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {s.ep_slug ?? 'direct'}
                      </span>
                      <span style={{
                        fontSize: 10, color: C.textMuted, flexShrink: 0,
                        background: 'rgba(255,255,255,0.05)', borderRadius: 4, padding: '1px 5px',
                        border: `1px solid ${C.outline}`,
                      }}>{epType}</span>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 11, color: C.textMuted }}>
                        user {s.user_id}
                      </span>
                      <span style={{ fontSize: 11, color: 'rgba(74,222,128,0.7)', fontFamily: 'JetBrains Mono, monospace' }}>
                        {elapsed(s.started_at)}
                      </span>
                    </div>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0 }}>
                    <button
                      title="Terminate session"
                      onClick={e => { e.stopPropagation(); handleTerminate(s.session_id); }}
                      style={{
                        width: 26, height: 26, borderRadius: 6, border: '1px solid rgba(239,68,68,0.35)',
                        background: 'transparent', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
                        transition: 'all 0.15s',
                      }}
                      onMouseEnter={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.12)'; }}
                      onMouseLeave={e => { e.currentTarget.style.background = 'transparent'; }}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 13, color: '#ef4444' }}>power_settings_new</span>
                    </button>
                    <span className="material-symbols-outlined" style={{
                      fontSize: 14, color: isSelected ? C.cyan : C.textMuted,
                      transition: 'color 0.15s',
                      transform: isSelected ? 'rotate(90deg)' : 'rotate(0)',
                    }}>chevron_right</span>
                  </div>
                </div>
              );
            })}
          </div>

          {/* Session detail drawer */}
          {selectedSession && (() => {
            const s = selectedSession;
            const epType = app.entry_points?.find(ep => ep.slug === s.ep_slug)?.entry_point_type ?? 'websocket';
            return (
              <div style={{
                borderTop: `1px solid ${C.outline}`,
                padding: '14px 16px',
                background: 'rgba(0,240,255,0.03)',
                flexShrink: 0,
              }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: C.cyan, marginBottom: 10, letterSpacing: 0.3 }}>
                  SESSION DETAIL
                </div>
                {[
                  ['Session ID', s.session_id.slice(0, 16) + '…'],
                  ['Entry Point', s.ep_slug ?? '—'],
                  ['EP Type', epType],
                  ['Orchestrator', s.orchestrator_name],
                  ['User ID', String(s.user_id)],
                  ['Context ID', s.context_id.slice(0, 16) + '…'],
                  ['Started', new Date(s.started_at).toLocaleTimeString()],
                  ['Elapsed', elapsed(s.started_at)],
                  ['Pod', s.instance_id.slice(0, 12) + '…'],
                ].map(([label, value]) => (
                  <div key={label} style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 5, gap: 8 }}>
                    <span style={{ fontSize: 11, color: C.textMuted, flexShrink: 0 }}>{label}</span>
                    <span style={{
                      fontSize: 11, color: C.text, fontFamily: label === 'Session ID' || label === 'Context ID' || label === 'Pod' ? 'JetBrains Mono, monospace' : 'inherit',
                      textAlign: 'right', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    }} title={value}>{value}</span>
                  </div>
                ))}
                <div style={{ display: 'flex', gap: 6, marginTop: 8 }}>
                  <button
                    onClick={() => { handleTerminate(s.session_id); setSelectedSession(null); }}
                    style={{
                      flex: 1, padding: '6px 0', borderRadius: 6,
                      border: '1px solid rgba(239,68,68,0.45)', background: 'rgba(239,68,68,0.06)',
                      color: '#ef4444', fontSize: 12, cursor: 'pointer',
                    }}
                    onMouseEnter={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.14)'; }}
                    onMouseLeave={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.06)'; }}
                  >
                    Terminate
                  </button>
                  <button
                    onClick={() => setSelectedSession(null)}
                    style={{
                      flex: 1, padding: '6px 0', borderRadius: 6,
                      border: `1px solid ${C.outline}`, background: 'transparent',
                      color: C.textMuted, fontSize: 12, cursor: 'pointer',
                    }}
                    onMouseEnter={e => e.currentTarget.style.background = 'rgba(255,255,255,0.04)'}
                    onMouseLeave={e => e.currentTarget.style.background = 'transparent'}
                  >
                    Close
                  </button>
                </div>
              </div>
            );
          })()}
        </div>
      </div>
    </div>
  );
}

function ListView({
  list, loading, onNew, onEdit, onSessions, onRuntime, onToggle, onDelete, onReload,
  selectedApps, onToggleSelect, onSelectAll, onBulkDelete, bulkDeleting,
}: {
  list: Application[];
  loading: boolean;
  onNew: () => void;
  onEdit: (app: Application) => void;
  onSessions: (app: Application) => void;
  onRuntime: (app: Application) => void;
  onToggle: (app: Application) => void;
  onDelete: (app: Application) => void;
  onReload: () => void;
  selectedApps: Set<string>;
  onToggleSelect: (id: string, checked: boolean) => void;
  onSelectAll: (checked: boolean) => void;
  onBulkDelete: () => void;
  bulkDeleting: boolean;
}) {
  const [renameApp, setRenameApp] = useState<Application | null>(null);
  const [renameName, setRenameName] = useState('');
  const [renaming, setRenaming] = useState(false);
  const [listToast, setListToast] = useState<{ msg: string; ok: boolean } | null>(null);

  function showListToast(msg: string, ok: boolean) {
    setListToast({ msg, ok });
    setTimeout(() => setListToast(null), 3000);
  }

  function openRename(app: Application) {
    setRenameApp(app);
    setRenameName(app.name);
  }

  async function commitRename() {
    if (!renameApp || !renameName.trim()) return;
    setRenaming(true);
    try {
      await themApi.updateApplication(renameApp.id, { name: renameName.trim(), enabled: renameApp.enabled });
      setRenameApp(null);
      showListToast('Renamed', true);
      onReload();
    } catch {
      showListToast('Rename failed', false);
    } finally {
      setRenaming(false);
    }
  }

  // Read JWT for WS auth — same cookie the rest of the app uses
  const [token, setToken] = useState<string | null>(null);
  useEffect(() => {
    fetch('/api/auth/token').then(r => r.ok ? r.json() : null).then(d => {
      if (d?.token) setToken(d.token);
    }).catch(() => {});
  }, []);

  const appStatuses = useDashAppStatuses(token);

  // Track session counts per app via individual session WS subscriptions
  // We subscribe to each app's sessions channel and count
  const [sessionCounts, setSessionCounts] = useState<Record<string, number>>({});
  useEffect(() => {
    if (!token || list.length === 0) return;
    const wsBase = window.location.origin.replace(/^http/, 'ws').replace(/^https/, 'wss');
    const wsUrl = `${wsBase}/ws/dashboard?token=${token}`;
    let ws: WebSocket;
    let dead = false;

    function connect() {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        const channels = list.map(a => `sessions:${a.id}`);
        ws.send(JSON.stringify({ type: 'subscribe', channels }));
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (!msg.channel?.startsWith('sessions:')) return;
          const appId = msg.channel.slice('sessions:'.length);
          const evt = msg.event;
          if (evt?.type === 'session_snapshot') {
            setSessionCounts(prev => ({ ...prev, [appId]: (evt.sessions ?? []).length }));
          } else if (evt?.type === 'session_start') {
            setSessionCounts(prev => ({ ...prev, [appId]: (prev[appId] ?? 0) + 1 }));
          } else if (evt?.type === 'session_end') {
            setSessionCounts(prev => ({ ...prev, [appId]: Math.max(0, (prev[appId] ?? 1) - 1) }));
          }
        } catch {}
      };
      ws.onclose = () => { if (!dead) setTimeout(connect, 4000); };
      ws.onerror = () => ws.close();
    }
    connect();
    return () => { dead = true; ws?.close(); };
  }, [token, list]);

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
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          {list.length > 0 && (
            <label style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 13, color: C.textMuted }}>
              <input
                type="checkbox"
                checked={selectedApps.size > 0 && selectedApps.size === list.length}
                ref={el => { if (el) el.indeterminate = selectedApps.size > 0 && selectedApps.size < list.length; }}
                onChange={e => onSelectAll(e.target.checked)}
                style={{ accentColor: '#00d1ff' }}
              />
              All
            </label>
          )}
          {selectedApps.size > 0 && (
            <button
              onClick={onBulkDelete}
              disabled={bulkDeleting}
              style={{
                display: 'flex', alignItems: 'center', gap: 6,
                padding: '10px 18px', borderRadius: 8, border: '1px solid rgba(248,113,113,0.4)',
                background: 'rgba(248,113,113,0.08)', color: '#f87171',
                fontSize: 13, fontWeight: 600, cursor: bulkDeleting ? 'not-allowed' : 'pointer',
                opacity: bulkDeleting ? 0.6 : 1, transition: 'opacity 0.15s',
              }}
            >
              {bulkDeleting ? 'Deleting…' : `Delete selected (${selectedApps.size})`}
            </button>
          )}
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
      </div>

      {/* Card grid */}
      <ChromaGrid radius={420} damping={0.09} fadeOutMs={800} style={{ padding: '0 32px 48px' }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 24 }}>
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

        {!loading && list.map((app) => {
          // Aggregate liveness across all EPs: live if ANY is reachable, best latency wins.
          const epStatuses = (app.entry_points ?? [])
            .map(ep => appStatuses[ep.slug])
            .filter(Boolean) as AppLiveness[];
          const anyReachable = epStatuses.some(s => s.reachable);
          const allChecked = epStatuses.length > 0;
          const bestLatency = epStatuses
            .filter(s => s.reachable && s.latency_ms != null)
            .reduce((min, s) => (s.latency_ms! < min ? s.latency_ms! : min), Infinity);
          const aggLiveness: AppLiveness | null = allChecked
            ? { reachable: anyReachable, latency_ms: isFinite(bestLatency) ? bestLatency : null }
            : null;
          return (
          <AppCard
            key={app.id}
            app={app}
            liveness={aggLiveness}
            sessionCount={sessionCounts[app.id] ?? 0}
            selected={selectedApps.has(app.id)}
            onToggleSelect={onToggleSelect}
            onEdit={onEdit}
            onSessions={onSessions}
            onRuntime={onRuntime}
            onToggle={onToggle}
            onDelete={onDelete}
            onRename={openRename}
          />
          );
        })}

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
      </ChromaGrid>

      {/* Rename Modal */}
      {renameApp && (
        <div
          style={{ position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', background: 'rgba(5,20,36,0.85)', zIndex: 200, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
          onClick={() => !renaming && setRenameApp(null)}
        >
          <div
            style={{ ...glass, borderRadius: 16, padding: '28px 32px', minWidth: 360, maxWidth: 480, position: 'relative' }}
            onClick={e => e.stopPropagation()}
          >
            <div style={{ fontSize: 14, fontWeight: 700, color: C.text, marginBottom: 16 }}>Rename Application</div>
            <input
              autoFocus
              value={renameName}
              onChange={e => setRenameName(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') setRenameApp(null); }}
              placeholder="Application name"
              style={{ width: '100%', padding: '10px 14px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`, background: C.surfaceContainer, color: C.text, fontSize: 14, outline: 'none', boxSizing: 'border-box', marginBottom: 16 }}
            />
            <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
              <button onClick={() => setRenameApp(null)} disabled={renaming} style={{ padding: '8px 18px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`, background: 'none', color: C.textMuted, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
              <button onClick={commitRename} disabled={renaming || !renameName.trim()} style={{ padding: '8px 18px', borderRadius: 8, border: 'none', background: '#6366f1', color: '#fff', cursor: renaming ? 'default' : 'pointer', fontSize: 13, fontWeight: 700, opacity: renaming ? 0.7 : 1 }}>
                {renaming ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* List-level toast */}
      {listToast && (
        <div style={{
          position: 'fixed', bottom: 32, right: 32, zIndex: 9999,
          background: listToast.ok ? C.greenBg : C.errorBg,
          border: `1px solid ${listToast.ok ? C.greenBorder : 'rgba(255,180,171,0.3)'}`,
          color: listToast.ok ? C.green : C.error,
          borderRadius: 10, padding: '10px 20px', fontSize: 13, fontWeight: 600,
        }}>
          {listToast.msg}
        </div>
      )}
    </div>
  );
}

// ── Page root ─────────────────────────────────────────────────────────────────
export default function ApplicationsPage() {
  const [list, setList] = useState<Application[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [view, setView] = useState<'list' | 'definition' | 'sessions' | 'runtime'>('list');
  const [definitionApp, setDefinitionApp] = useState<Application | null>(null);
  const [sessionsApp, setSessionsApp] = useState<Application | null>(null);
  const [runtimeApp, setRuntimeApp] = useState<Application | null>(null);
  const [token, setToken] = useState<string | null>(null);
  useEffect(() => {
    fetch('/api/auth/token').then(r => r.ok ? r.json() : null).then(d => { if (d?.token) setToken(d.token); }).catch(() => {});
  }, []);
  const [selectedApps, setSelectedApps] = useState<Set<string>>(new Set());
  const [bulkDeleting, setBulkDeleting] = useState(false);

  async function load() {
    setLoading(true);
    try {
      const [apps, ags] = await Promise.all([themApi.applications(), themApi.agents()]);
      setList(apps);
      setAgents(ags);
    } catch {
      // Transient auth race on first mount (token not yet in cookie) — retry once
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  async function handleToggle(app: Application) {
    try {
      await themApi.updateApplication(app.id, { name: app.name, enabled: !app.enabled });
      await load();
    } catch {/* ignore — AppCard shows toggling state, failure resets on next load */}
  }

  async function handleDelete(app: Application) {
    try {
      await themApi.deleteApplication(app.id);
      await load();
      showListToast(`"${app.name}" deleted`, true);
    } catch (e) {
      showListToast(e instanceof Error ? e.message : 'Delete failed', false);
    }
  }

  function handleToggleSelect(id: string, checked: boolean) {
    setSelectedApps(prev => {
      const next = new Set(prev);
      checked ? next.add(id) : next.delete(id);
      return next;
    });
  }

  function handleSelectAll(checked: boolean) {
    setSelectedApps(checked ? new Set(list.map(a => a.id)) : new Set());
  }

  async function handleBulkDelete() {
    if (selectedApps.size === 0) return;
    setBulkDeleting(true);
    try {
      await themApi.bulkDeleteApplications(Array.from(selectedApps));
      setSelectedApps(new Set());
      await load();
    } catch (e) {
      showListToast(e instanceof Error ? e.message : 'Bulk delete failed', false);
    } finally {
      setBulkDeleting(false);
    }
  }

  function openDefinition(app: Application) {
    setDefinitionApp(app);
    setView('definition');
  }

  function backToList() {
    setView('list');
    setDefinitionApp(null);
    setSessionsApp(null);
    setRuntimeApp(null);
  }

  function openSessions(app: Application) {
    setSessionsApp(app);
    setView('sessions');
  }

  function openRuntime(app: Application) {
    setRuntimeApp(app);
    setView('runtime');
  }

  if (view === 'runtime' && runtimeApp) {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <RuntimeView
              app={runtimeApp}
              onBack={backToList}
            />
          </div>
        </div>
      </AuthGuard>
    );
  }

  if (view === 'sessions' && sessionsApp) {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <SessionsView
              app={sessionsApp}
              agents={agents}
              onBack={backToList}
              token={token}
            />
          </div>
        </div>
      </AuthGuard>
    );
  }

  if (view === 'definition' && definitionApp) {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <CanvasBuilderView
              app={definitionApp}
              agents={agents}
              onBack={() => { setView('list'); setDefinitionApp(null); }}
              onAppUpdated={(updated) => {
                setList(prev => prev.map(a => a.id === updated.id ? updated : a));
                setDefinitionApp(updated);
              }}
            />
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
          loading={loading}
          onNew={async () => {
            try {
              const app = await themApi.createApplication({ name: 'New Application', enabled: false });
              await load();
              openDefinition(app);
            } catch {/* ignore */}
          }}
          onEdit={(app) => openDefinition(app)}
          onSessions={openSessions}
          onRuntime={openRuntime}
          onToggle={handleToggle}
          onDelete={handleDelete}
          onReload={load}
          selectedApps={selectedApps}
          onToggleSelect={handleToggleSelect}
          onSelectAll={handleSelectAll}
          onBulkDelete={handleBulkDelete}
          bulkDeleting={bulkDeleting}
        />
      </div>
    </AuthGuard>
  );
}
