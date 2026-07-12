'use client';
import { useEffect, useState, useCallback, useRef, DragEvent } from 'react';
// eslint-disable-next-line @typescript-eslint/no-require-imports, @typescript-eslint/no-explicit-any
const dagre: any = (typeof window !== 'undefined' ? require('dagre') : null);
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Application, type OrchestratorFull, type Agent } from '@/lib/api';
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
  bg: '#051424',
  surface: 'rgba(15,23,42,0.7)',
  surfaceContainer: '#122131',
  surfaceLow: '#0d1c2d',
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
  text: '#d4e4fa',
  textMuted: '#b9cacb',
  outline: '#849495',
  outlineVariant: '#3b494b',
  error: '#ffb4ab',
  errorBg: 'rgba(255,180,171,0.1)',
  glass: 'rgba(15,23,42,0.7)',
  glassBorder: 'rgba(30,41,59,0.5)',
};

const glass = {
  background: C.glass,
  backdropFilter: 'blur(12px)',
  WebkitBackdropFilter: 'blur(12px)',
  border: `1px solid ${C.glassBorder}`,
};

const deleteNodeRef = { current: (_id: string) => {} };

// ── Types ────────────────────────────────────────────────────────────────────
const ENTRY_POINT_TYPES = ['websocket', 'sse'] as const;
type EntryPointType = typeof ENTRY_POINT_TYPES[number];

interface EntryPointData { label: string; epType: EntryPointType; accessMode: 'token' | 'public'; slug: string; [key: string]: unknown; }
interface OrchestratorData { orchestratorId: string; name: string; displayName: string; model: string | null; maxParallelTools: number; [key: string]: unknown; }
interface AgentData { agentId: string; name: string; displayName: string; description: string; transport: string; endpointUrl: string; [key: string]: unknown; }

// ── Change 1: CSS animations + font inheritance ───────────────────────────────
const CANVAS_STYLES = `
  /* Force all text bright — builder lives on a dark bg, globals.css light-mode vars bleed in */
  .builder-root, .builder-root * {
    color: inherit;
  }
  .builder-root input,
  .builder-root select,
  .builder-root textarea {
    color: #e2e8f0 !important;
    background-color: #0d1c2d !important;
    -webkit-text-fill-color: #e2e8f0 !important;
  }
  .builder-root input::placeholder,
  .builder-root textarea::placeholder {
    color: #4a6580 !important;
    -webkit-text-fill-color: #4a6580 !important;
  }
  .builder-root input[style*="color: #f59e0b"],
  .builder-root input[style*="color:#f59e0b"] {
    color: #f59e0b !important;
    -webkit-text-fill-color: #f59e0b !important;
  }
  /* Slug input on entry-point node */
  .ep-slug-set {
    color: #e2e8f0 !important;
    -webkit-text-fill-color: #e2e8f0 !important;
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
  .react-flow__node * {
    font-family: inherit;
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
    background: rgba(8,18,36,0.97);
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
// Change 2: EntryPointNode with inline SVG icon + font fixes + hover animation
function EntryPointNode({ id, data, selected }: { id: string; data: EntryPointData; selected?: boolean }) {
  const slugMissing = !data.slug;
  return (
    <div
      style={{
        minWidth: 200, width: 'fit-content', padding: '14px 18px', borderRadius: 12,
        background: C.cyanBg,
        border: `1px solid ${slugMissing ? 'rgba(255,180,100,0.6)' : selected ? C.cyan : C.cyanBorder}`,
        boxShadow: selected ? '0 0 20px rgba(0,240,255,0.3)' : C.cyanGlow,
        fontFamily: 'Inter, sans-serif', cursor: 'default', transition: 'all 0.2s',
        transformOrigin: 'center', position: 'relative',
      }}
    >
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
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan, border: `2px solid ${C.bg}`, width: 10, height: 10 }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
        <div style={{ width: 32, height: 32, borderRadius: 8, background: 'rgba(0,240,255,0.15)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
          {data.epType === 'sse' ? (
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={C.cyan} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M22 12h-4l-3 9L9 3l-3 9H2"/>
            </svg>
          ) : (
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={C.cyan} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z"/>
            </svg>
          )}
        </div>
        <div>
          <div style={{ fontSize: 14, fontWeight: 600, color: C.text, fontFamily: 'Inter, sans-serif' }}>
            {data.label || (data.epType === 'sse' ? 'SSE' : 'WebSocket')}
          </div>
          <div style={{ fontSize: 10, fontWeight: 700, color: C.cyan, letterSpacing: 1, textTransform: 'uppercase', marginTop: 2, fontFamily: 'Inter, sans-serif' }}>
            Entry Point
          </div>
        </div>
      </div>
      {/* Inline slug input — always visible on node */}
      <div style={{ borderTop: '1px solid rgba(0,240,255,0.12)', paddingTop: 8 }}>
        <div style={{ fontSize: 9, fontWeight: 700, color: slugMissing ? '#f59e0b' : C.cyan, textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4 }}>
          {slugMissing ? '⚠ Slug required' : 'Slug'}
        </div>
        <input
          className={`nodrag ${slugMissing ? 'ep-slug-missing' : 'ep-slug-set'}`}
          value={data.slug}
          onChange={() => {/* updated via Properties panel */}}
          placeholder="my-app"
          readOnly
          style={{
            width: '100%', padding: '4px 8px', borderRadius: 5, fontSize: 12,
            fontFamily: 'JetBrains Mono, monospace', boxSizing: 'border-box',
            background: 'rgba(0,0,0,0.25)',
            border: `1px solid ${slugMissing ? 'rgba(245,158,11,0.5)' : 'rgba(0,240,255,0.2)'}`,
            cursor: 'default',
          }}
          title="Click the node then set slug in the Properties panel →"
        />
        {!slugMissing && (
          <div
            className="nodrag"
            title="Click to copy endpoint URL"
            onClick={() => navigator.clipboard.writeText(
              data.epType === 'websocket'
                ? `ws://localhost:8088/apps/${data.slug}/ws`
                : `http://localhost:8088/apps/${data.slug}/sse`
            )}
            style={{
              marginTop: 5, padding: '3px 6px', borderRadius: 4,
              background: 'rgba(0,240,255,0.06)', border: '1px solid rgba(0,240,255,0.15)',
              fontSize: 10, color: 'rgba(0,240,255,0.7)', fontFamily: 'JetBrains Mono, monospace',
              cursor: 'pointer', wordBreak: 'break-all', lineHeight: 1.4,
              display: 'flex', alignItems: 'center', gap: 4,
            }}
          >
            <span style={{ flex: 1 }}>
              {data.epType === 'websocket' ? `ws://<host>/apps/${data.slug}/ws` : `http://<host>/apps/${data.slug}/sse`}
            </span>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
              <rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
            </svg>
          </div>
        )}
      </div>
    </div>
  );
}

// Change 3: OrchestratorNode with inline SVG icon + font fixes + hover animation
function OrchestratorNode({ id, data, selected }: { id: string; data: OrchestratorData; selected?: boolean }) {
  return (
    <div
      style={{
        minWidth: 200, width: 'fit-content', padding: '16px 20px', borderRadius: 12,
        background: C.purpleBg, border: `2px solid ${selected ? '#e8d5ff' : C.purpleBorder}`,
        boxShadow: selected ? '0 0 24px rgba(208,188,255,0.4)' : C.purpleGlow,
        fontFamily: 'Inter, sans-serif', cursor: 'default', transition: 'all 0.2s',
        transformOrigin: 'center', position: 'relative',
      }}
      onMouseEnter={e => {
        e.currentTarget.style.transform = 'scale(1.03)';
        e.currentTarget.style.boxShadow = '0 0 32px rgba(208,188,255,0.45)';
      }}
      onMouseLeave={e => {
        e.currentTarget.style.transform = 'scale(1)';
        e.currentTarget.style.boxShadow = selected ? '0 0 24px rgba(208,188,255,0.4)' : C.purpleGlow;
      }}
    >
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
      <Handle type="target" position={Position.Top} style={{ background: C.purple, border: `2px solid ${C.bg}`, width: 10, height: 10 }} />
      <Handle type="source" position={Position.Bottom} style={{ background: C.purple, border: `2px solid ${C.bg}`, width: 10, height: 10 }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{ width: 32, height: 32, borderRadius: 8, background: 'rgba(208,188,255,0.15)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={C.purple} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M2 20h20"/>
            <path d="M5 20l2-8 5 4 5-4 2 8"/>
            <circle cx="12" cy="4" r="2" fill={C.purple}/>
          </svg>
        </div>
        <div>
          <div style={{ fontSize: 14, fontWeight: 600, color: C.text, fontFamily: 'Inter, sans-serif' }}>{data.displayName}</div>
          <div style={{ fontSize: 10, fontWeight: 700, color: C.purple, letterSpacing: 1, textTransform: 'uppercase', marginTop: 2, fontFamily: 'Inter, sans-serif' }}>
            Orchestrator
          </div>
        </div>
      </div>
      {data.model && (
        <div style={{ marginTop: 10, padding: '4px 8px', borderRadius: 6, background: 'rgba(208,188,255,0.08)', fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>
          {data.model}
        </div>
      )}
    </div>
  );
}

// Change 4: AgentNode with inline SVG icon + font fixes + hover animation
function AgentNode({ id, data, selected }: { id: string; data: AgentData; selected?: boolean }) {
  return (
    <div
      style={{
        minWidth: 160, maxWidth: 220, width: 'fit-content', padding: '12px 16px', borderRadius: 12,
        background: C.greenBg, border: `1px solid ${selected ? C.green : C.greenBorder}`,
        boxShadow: selected ? '0 0 20px rgba(74,222,128,0.25)' : '0 0 10px rgba(74,222,128,0.08)',
        fontFamily: 'Inter, sans-serif', cursor: 'default', transition: 'all 0.2s',
        transformOrigin: 'center', position: 'relative',
      }}
      onMouseEnter={e => {
        e.currentTarget.style.transform = 'scale(1.03)';
        e.currentTarget.style.boxShadow = '0 0 24px rgba(74,222,128,0.3)';
      }}
      onMouseLeave={e => {
        e.currentTarget.style.transform = 'scale(1)';
        e.currentTarget.style.boxShadow = selected ? '0 0 20px rgba(74,222,128,0.25)' : '0 0 10px rgba(74,222,128,0.08)';
      }}
    >
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
      <Handle type="target" position={Position.Top} style={{ background: C.green, border: `2px solid ${C.bg}`, width: 10, height: 10 }} />
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
        <div style={{ width: 28, height: 28, borderRadius: 7, background: 'rgba(74,222,128,0.12)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, marginTop: 1 }}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={C.green} strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
            <rect x="7" y="7" width="10" height="12" rx="2"/>
            <path d="M10 7V5a2 2 0 0 1 4 0v2"/>
            <circle cx="10.5" cy="12" r="1" fill={C.green}/>
            <circle cx="13.5" cy="12" r="1" fill={C.green}/>
            <path d="M10 15.5h4"/>
          </svg>
        </div>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: C.text, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', fontFamily: 'Inter, sans-serif' }}>
            {data.displayName}
          </div>
          <div style={{ fontSize: 10, fontWeight: 700, color: C.green, letterSpacing: 1, textTransform: 'uppercase', marginTop: 1, fontFamily: 'Inter, sans-serif' }}>
            Agent
          </div>
          {data.description && (
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4, overflow: 'hidden', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', fontFamily: 'Inter, sans-serif' }}>
              {data.description}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

const NODE_TYPES: NodeTypes = {
  entryPoint: EntryPointNode as any,
  orchestrator: OrchestratorNode as any,
  agent: AgentNode as any,
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
  orch: OrchestratorFull | undefined,
  agents: Agent[],
): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  const epId = 'ep_0';
  nodes.push({
    id: epId, type: 'entryPoint',
    position: { x: 300, y: 60 },
    data: {
      label: app.name,
      epType: (app.entry_point_type as EntryPointType) ?? 'websocket',
      accessMode: ((app.access_policy as any)?.mode ?? 'token') as 'token' | 'public',
      slug: app.slug,
    } satisfies EntryPointData,
  });

  if (orch) {
    const orchId = `orch_${orch.id}`;
    nodes.push({
      id: orchId, type: 'orchestrator',
      position: { x: 250, y: 220 },
      data: {
        orchestratorId: orch.id,
        name: orch.name,
        displayName: orch.display_name,
        model: orch.llm_model,
        maxParallelTools: orch.max_parallel_tools,
      } satisfies OrchestratorData,
    });
    edges.push({ id: `e_ep_orch`, source: epId, target: orchId, animated: true, style: EDGE_STYLE });

    const allowedAgents = agents.filter(a => orch.allowed_agent_ids.includes(a.id));
    const spread = Math.max(allowedAgents.length * 180, 400);
    const startX = 300 - spread / 2 + 90;
    allowedAgents.forEach((agent, i) => {
      const aId = `agent_${agent.id}`;
      nodes.push({
        id: aId, type: 'agent',
        position: { x: startX + i * 190, y: 420 },
        data: {
          agentId: agent.id,
          name: agent.slug,
          displayName: agent.display_name,
          description: agent.description,
          transport: agent.transport,
          endpointUrl: agent.endpoint_url,
        } satisfies AgentData,
      });
      edges.push({ id: `e_orch_agent_${i}`, source: orchId, target: aId, animated: true, style: EDGE_STYLE });
    });
  }

  const laid = applyDagreLayout(nodes, edges);
  return { nodes: laid, edges };
}

// ── Helpers ───────────────────────────────────────────────────────────────────
function agentIconForLibrary(a: Agent): string {
  const s = (a.icon || a.slug || '').toLowerCase();
  if (s.includes('vision') || s.includes('map'))  return 'visibility';
  if (s.includes('code') || s.includes('coder'))  return 'code';
  if (s.includes('doc') || s.includes('write'))   return 'description';
  if (s.includes('search') || s.includes('research')) return 'search';
  if (s.includes('security') || s.includes('scan')) return 'security';
  if (s.includes('echo'))  return 'record_voice_over';
  if (s.includes('slow'))  return 'hourglass_bottom';
  if (s.includes('stream')) return 'stream';
  return 'smart_toy';
}

const EP_META: Record<string, { emoji: string; title: string; desc: string }> = {
  websocket: { emoji: '⚡', title: 'WebSocket', desc: 'Full-duplex, persistent connection. Client and server can send messages at any time. Best for chat, real-time collaboration, and interactive agents.' },
  sse:       { emoji: '📡', title: 'Server-Sent Events', desc: 'One-way server→client stream over HTTP. Lightweight, works through proxies. Best for dashboards, notifications, and read-only agent output.' },
};

function trunc(s: string | null | undefined, n = 120) {
  if (!s) return '—';
  return s.length > n ? s.slice(0, n) + '…' : s;
}

// ── Node Library panel ────────────────────────────────────────────────────────
function NodeLibrary({ orchestrators, agents, width, onWidthChange }: {
  orchestrators: OrchestratorFull[];
  agents: Agent[];
  width: number;
  onWidthChange: (w: number) => void;
}) {
  const [openEP, setOpenEP] = useState(true);
  const [openOrch, setOpenOrch] = useState(true);
  const [openAgents, setOpenAgents] = useState(true);
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
              {(['websocket', 'sse'] as const).map(ep => {
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
                      <div style={{ fontSize: 11, color: '#cbd5e1', lineHeight: 1.5 }}>{meta.desc}</div>
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
              {orchestrators.filter(o => o.enabled).map(o => (
                <div key={o.id} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                  <div
                    draggable
                    onDragStart={e => dragItem(e, 'orchestrator', { orchestratorId: o.id, name: o.name, displayName: o.display_name, model: o.llm_model, maxParallelTools: o.max_parallel_tools })}
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
                    <div style={{ fontSize: 11, color: '#cbd5e1', lineHeight: 1.5 }}>{trunc(o.system_prompt ?? o.name)}</div>
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
              {agents.filter(a => a.enabled).map(a => {
                const icon = a.icon || agentIconForLibrary(a);
                return (
                  <div key={a.id} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                    <div
                      draggable
                      onDragStart={e => dragItem(e, 'agent', { agentId: a.id, name: a.slug, displayName: a.display_name, description: a.description, transport: a.transport, endpointUrl: a.endpoint_url })}
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
                      <div style={{ fontSize: 11, color: '#cbd5e1', lineHeight: 1.5 }}>{trunc(a.description)}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
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
  chain,
  app,
}: {
  selectedNode: Node | null;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
  slugLocked: boolean;
  onSlugManualEdit: () => void;
  appName: string;
  onAppNameChange: (name: string) => void;
  chain: ChainStatus;
  app: Application | null;
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

  const labelStyle: React.CSSProperties = { fontSize: 12, color: '#94a3b8', marginBottom: 4, display: 'block' };
  const inputStyle: React.CSSProperties = {
    width: '100%', padding: '7px 10px', borderRadius: 6,
    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
    color: '#e2e8f0', fontSize: 13, boxSizing: 'border-box', outline: 'none',
  };
  const readOnlyStyle: React.CSSProperties = { ...inputStyle, color: '#cbd5e1', background: 'rgba(10,18,32,0.6)', cursor: 'default' };
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

          {/* Name field */}
          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 12, color: '#94a3b8', marginBottom: 4, display: 'block' }}>Application Name</label>
            <input
              style={{
                width: '100%', padding: '7px 10px', borderRadius: 6,
                border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
                color: '#e2e8f0', fontSize: 13, boxSizing: 'border-box', outline: 'none',
              }}
              value={appName}
              onChange={e => onAppNameChange(e.target.value)}
              placeholder="My Application"
            />
          </div>

          {/* Chain status */}
          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 12, color: '#94a3b8', marginBottom: 6, display: 'block' }}>Canvas Status</label>
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
            <label style={{ fontSize: 12, color: '#94a3b8', marginBottom: 6, display: 'block' }}>Canvas Info</label>
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
              <label style={{ fontSize: 12, color: '#94a3b8', marginBottom: 4, display: 'block' }}>Created</label>
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
                  <label style={labelStyle}>Name</label>
                  <input style={inputStyle} value={d.label} onChange={e => onUpdateNode(selectedNode.id, { label: e.target.value })} />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Type</label>
                  <select style={{ ...inputStyle }} value={d.epType} onChange={e => onUpdateNode(selectedNode.id, { epType: e.target.value as EntryPointType })}>
                    <option value="websocket">WebSocket</option>
                    <option value="sse">SSE</option>
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
                    <div style={{ fontSize: 11, color: C.textMuted, marginTop: 6, padding: '5px 8px', background: C.surfaceLow, borderRadius: 5, fontFamily: 'JetBrains Mono, monospace', wordBreak: 'break-all' }}>
                      {d.epType === 'websocket' ? `ws://<host>:8088/apps/${d.slug}/ws` : `http://<host>:8088/apps/${d.slug}/sse`}
                    </div>
                  )}
                </div>
              </div>
            );
          })()}

          {/* Orchestrator properties */}
          {selectedNode.type === 'orchestrator' && propTab === 'properties' && (() => {
            const d = selectedNode.data as OrchestratorData;
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
                {[
                  { label: 'Model', value: d.model ?? '—', mono: true },
                  { label: 'Max Parallel Tools', value: String(d.maxParallelTools), mono: false },
                ].map(({ label, value, mono }) => (
                  <div key={label} style={fieldWrap}>
                    <label style={labelStyle}>{label}</label>
                    <div style={{ ...readOnlyStyle, fontFamily: mono ? 'JetBrains Mono, monospace' : 'inherit', fontSize: mono ? 12 : 13, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow, color: C.text }}>
                      {value}
                    </div>
                  </div>
                ))}
                <div style={fieldWrap}>
                  <label style={labelStyle}>A2A Endpoint</label>
                  <div style={{
                    fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace',
                    wordBreak: 'break-all', padding: '7px 10px', borderRadius: 6,
                    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
                    display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 6,
                  }}>
                    <span>http://&lt;host&gt;:8088/a2a/{d.name}</span>
                    <button
                      onClick={() => navigator.clipboard.writeText(`http://localhost:8088/a2a/${d.name}`)}
                      title="Copy A2A URL"
                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.purple, flexShrink: 0, padding: 0 }}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 14 }}>content_copy</span>
                    </button>
                  </div>
                </div>
                <a href="/admin/orchestrators" style={{ fontSize: 12, color: C.purple, textDecoration: 'none', display: 'flex', alignItems: 'center', gap: 4, marginTop: 8, opacity: 0.8 }}
                  onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
                  onMouseLeave={e => (e.currentTarget.style.opacity = '0.8')}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
                  Configure in Orchestrators
                </a>
              </div>
            );
          })()}

          {/* Agent properties */}
          {selectedNode.type === 'agent' && propTab === 'properties' && (() => {
            const d = selectedNode.data as AgentData;
            const icon = agentIconForLibrary({ slug: d.name, icon: null } as any);
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
                  <div style={{ fontSize: 12, color: '#cbd5e1', lineHeight: 1.55, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
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
  idle:     { opacity: 0.13, filter: 'drop-shadow(0 0 18px rgba(0,240,255,0.18))',    animation: 'logo-breathe 20s ease-in-out infinite' },
  dirty:    { opacity: 0.22, filter: 'drop-shadow(0 0 14px rgba(245,158,11,0.25))',   animation: 'logo-sway 2.5s ease-in-out infinite' },
  warning:  { opacity: 0.30, filter: 'drop-shadow(0 0 16px rgba(245,158,11,0.35))',   animation: 'logo-breathe 1.8s ease-in-out infinite' },
  error:    { opacity: 0.35, filter: 'drop-shadow(0 0 18px rgba(255,107,138,0.4))',   animation: 'logo-shake 0.5s ease-in-out' },
  success:  { opacity: 1.0,  filter: 'drop-shadow(0 0 40px rgba(74,222,128,0.9))',    animation: 'logo-burst 1.8s ease-out forwards' },
  thinking: { opacity: 0.28, filter: 'drop-shadow(0 0 16px rgba(208,188,255,0.3))',   animation: 'logo-flip 1.4s linear infinite' },
};

const LOGO_KEYFRAMES = `
@keyframes logo-breathe {
  0%   { opacity: 0.08; }
  50%  { opacity: 0.25; }
  100% { opacity: 0.08; }
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
`;

// Explode vectors per path index — matches the_m_logo_white_fully_separated.svg order
const LOGO_PATHS: Array<{ id: string; d: string; ex: number; ey: number }> = [
  { id: 'left-profile-upper',  ex: -1.2,  ey: -0.2, d: "M 520.00 198.00 L 519.00 199.00 L 519.00 200.00 L 519.00 201.00 L 518.00 202.00 L 518.00 203.00 L 518.00 204.00 L 517.00 205.00 L 517.00 206.00 L 516.00 207.00 L 516.00 208.00 L 516.00 209.00 L 515.00 210.00 L 515.00 211.00 L 514.00 212.00 L 514.00 213.00 L 513.00 214.00 L 513.00 215.00 L 513.00 216.00 L 512.00 217.00 L 512.00 218.00 L 511.00 219.00 L 511.00 220.00 L 510.00 221.00 L 510.00 222.00 L 509.00 223.00 L 509.00 224.00 L 508.00 225.00 L 508.00 226.00 L 507.00 227.00 L 507.00 228.00 L 506.00 229.00 L 506.00 230.00 L 505.00 231.00 L 505.00 232.00 L 504.00 233.00 L 504.00 234.00 L 503.00 235.00 L 503.00 236.00 L 502.00 237.00 L 502.00 238.00 L 501.00 239.00 L 500.00 240.00 L 500.00 241.00 L 499.00 242.00 L 499.00 243.00 L 498.00 244.00 L 497.00 245.00 L 497.00 246.00 L 496.00 247.00 L 496.00 248.00 L 592.00 248.00 L 591.00 247.00 L 590.00 246.00 L 589.00 246.00 L 588.00 245.00 L 587.00 244.00 L 586.00 243.00 L 585.00 243.00 L 584.00 242.00 L 583.00 241.00 L 582.00 240.00 L 581.00 240.00 L 580.00 239.00 L 579.00 238.00 L 578.00 238.00 L 577.00 237.00 L 576.00 236.00 L 575.00 235.00 L 574.00 235.00 L 573.00 234.00 L 572.00 233.00 L 571.00 232.00 L 570.00 232.00 L 569.00 231.00 L 568.00 230.00 L 567.00 230.00 L 566.00 229.00 L 565.00 228.00 L 564.00 227.00 L 563.00 227.00 L 562.00 226.00 L 561.00 225.00 L 560.00 225.00 L 559.00 224.00 L 558.00 223.00 L 557.00 222.00 L 556.00 222.00 L 555.00 221.00 L 554.00 220.00 L 553.00 219.00 L 552.00 219.00 L 551.00 218.00 L 550.00 217.00 L 549.00 216.00 L 548.00 216.00 L 547.00 215.00 L 546.00 214.00 L 545.00 213.00 L 544.00 213.00 L 543.00 212.00 L 542.00 211.00 L 541.00 211.00 L 540.00 210.00 L 539.00 209.00 L 538.00 208.00 L 537.00 208.00 L 536.00 207.00 L 535.00 206.00 L 534.00 206.00 L 533.00 205.00 L 532.00 204.00 L 531.00 204.00 L 530.00 203.00 L 529.00 202.00 L 528.00 201.00 L 527.00 201.00 L 526.00 200.00 L 525.00 199.00 L 524.00 199.00 L 523.00 198.00 L 522.00 197.00 L 521.00 197.00 L 520.00 197.00 Z" },
  { id: 'left-profile-middle', ex: -1.3,  ey:  0.0, d: "M 495.00 249.00 L 494.00 250.00 L 494.00 251.00 L 493.00 252.00 L 493.00 253.00 L 493.00 254.00 L 493.00 255.00 L 493.00 256.00 L 493.00 257.00 L 493.00 258.00 L 494.00 259.00 L 495.00 260.00 L 496.00 260.00 L 497.00 261.00 L 498.00 261.00 L 499.00 262.00 L 500.00 262.00 L 501.00 262.00 L 502.00 263.00 L 503.00 263.00 L 504.00 263.00 L 505.00 264.00 L 506.00 264.00 L 507.00 265.00 L 508.00 266.00 L 509.00 267.00 L 510.00 268.00 L 510.00 269.00 L 510.00 270.00 L 510.00 271.00 L 510.00 272.00 L 509.00 273.00 L 509.00 274.00 L 509.00 275.00 L 508.00 276.00 L 508.00 277.00 L 507.00 278.00 L 507.00 279.00 L 506.00 280.00 L 506.00 281.00 L 506.00 282.00 L 506.00 283.00 L 506.00 284.00 L 507.00 285.00 L 507.00 286.00 L 508.00 287.00 L 509.00 288.00 L 510.00 288.00 L 511.00 289.00 L 512.00 289.00 L 513.00 290.00 L 514.00 291.00 L 513.00 292.00 L 512.00 293.00 L 511.00 294.00 L 511.00 295.00 L 510.00 296.00 L 509.00 297.00 L 509.00 298.00 L 509.00 299.00 L 509.00 300.00 L 510.00 301.00 L 510.00 302.00 L 511.00 303.00 L 512.00 303.00 L 513.00 304.00 L 514.00 305.00 L 515.00 305.00 L 516.00 306.00 L 516.00 307.00 L 517.00 308.00 L 518.00 309.00 L 518.00 310.00 L 519.00 311.00 L 519.00 312.00 L 519.00 313.00 L 519.00 314.00 L 519.00 315.00 L 592.00 315.00 L 592.00 248.00 L 496.00 248.00 Z" },
  { id: 'left-profile-lower',  ex: -1.1,  ey:  0.8, d: "M 519.00 316.00 L 519.00 335.00 L 520.00 336.00 L 521.00 337.00 L 522.00 338.00 L 523.00 339.00 L 524.00 340.00 L 525.00 341.00 L 526.00 342.00 L 527.00 342.00 L 528.00 343.00 L 529.00 344.00 L 530.00 344.00 L 531.00 345.00 L 532.00 346.00 L 533.00 347.00 L 534.00 347.00 L 535.00 348.00 L 536.00 349.00 L 537.00 349.00 L 538.00 350.00 L 539.00 351.00 L 540.00 351.00 L 541.00 352.00 L 542.00 353.00 L 543.00 353.00 L 544.00 354.00 L 545.00 355.00 L 546.00 356.00 L 547.00 356.00 L 548.00 357.00 L 549.00 358.00 L 550.00 358.00 L 551.00 359.00 L 552.00 360.00 L 553.00 361.00 L 554.00 361.00 L 555.00 362.00 L 556.00 363.00 L 557.00 363.00 L 558.00 364.00 L 559.00 365.00 L 560.00 365.00 L 561.00 366.00 L 562.00 367.00 L 563.00 368.00 L 564.00 368.00 L 565.00 369.00 L 566.00 370.00 L 567.00 371.00 L 568.00 371.00 L 569.00 372.00 L 570.00 373.00 L 571.00 373.00 L 572.00 374.00 L 573.00 375.00 L 574.00 376.00 L 575.00 376.00 L 576.00 377.00 L 577.00 378.00 L 578.00 378.00 L 579.00 379.00 L 580.00 380.00 L 581.00 381.00 L 582.00 381.00 L 583.00 382.00 L 584.00 383.00 L 585.00 383.00 L 586.00 384.00 L 587.00 385.00 L 588.00 386.00 L 589.00 386.00 L 590.00 387.00 L 591.00 388.00 L 592.00 388.00 L 593.00 389.00 L 593.00 316.00 L 592.00 315.00 L 519.00 315.00 Z" },
  { id: 'right-profile-upper', ex:  1.2,  ey: -0.2, d: "M 876.00 197.00 L 875.00 197.00 L 874.00 198.00 L 873.00 199.00 L 872.00 200.00 L 871.00 200.00 L 870.00 201.00 L 869.00 202.00 L 868.00 202.00 L 867.00 203.00 L 866.00 204.00 L 865.00 205.00 L 864.00 205.00 L 863.00 206.00 L 862.00 207.00 L 861.00 207.00 L 860.00 208.00 L 859.00 209.00 L 858.00 210.00 L 857.00 210.00 L 856.00 211.00 L 855.00 212.00 L 854.00 212.00 L 853.00 213.00 L 852.00 214.00 L 851.00 215.00 L 850.00 215.00 L 849.00 216.00 L 848.00 217.00 L 847.00 217.00 L 846.00 218.00 L 845.00 219.00 L 844.00 220.00 L 843.00 221.00 L 842.00 221.00 L 841.00 222.00 L 840.00 223.00 L 839.00 223.00 L 838.00 224.00 L 837.00 225.00 L 836.00 226.00 L 835.00 226.00 L 834.00 227.00 L 833.00 228.00 L 832.00 229.00 L 831.00 229.00 L 830.00 230.00 L 829.00 231.00 L 828.00 231.00 L 827.00 232.00 L 826.00 233.00 L 825.00 234.00 L 824.00 234.00 L 823.00 235.00 L 822.00 236.00 L 821.00 236.00 L 820.00 237.00 L 819.00 238.00 L 818.00 239.00 L 817.00 239.00 L 816.00 240.00 L 815.00 241.00 L 814.00 241.00 L 813.00 242.00 L 812.00 243.00 L 811.00 244.00 L 810.00 245.00 L 809.00 245.00 L 808.00 246.00 L 807.00 247.00 L 806.00 247.00 L 805.00 248.00 L 902.00 248.00 L 877.00 196.00 Z" },
  { id: 'right-profile-middle',ex:  1.3,  ey:  0.0, d: "M 805.00 249.00 L 805.00 315.00 L 878.00 315.00 L 902.00 248.00 L 805.00 248.00 Z" },
  { id: 'right-profile-lower', ex:  1.1,  ey:  0.8, d: "M 806.00 316.00 L 806.00 315.00 L 805.00 315.00 L 805.00 389.00 L 806.00 388.00 L 807.00 388.00 L 808.00 387.00 L 809.00 386.00 L 810.00 385.00 L 811.00 385.00 L 812.00 384.00 L 813.00 383.00 L 814.00 382.00 L 815.00 382.00 L 816.00 381.00 L 817.00 380.00 L 818.00 380.00 L 819.00 379.00 L 820.00 378.00 L 821.00 377.00 L 822.00 377.00 L 823.00 376.00 L 824.00 375.00 L 825.00 375.00 L 826.00 374.00 L 827.00 373.00 L 828.00 372.00 L 829.00 372.00 L 830.00 371.00 L 831.00 370.00 L 832.00 370.00 L 833.00 369.00 L 834.00 368.00 L 835.00 367.00 L 836.00 367.00 L 837.00 366.00 L 838.00 365.00 L 839.00 365.00 L 840.00 364.00 L 841.00 363.00 L 842.00 363.00 L 843.00 362.00 L 844.00 361.00 L 845.00 361.00 L 846.00 360.00 L 847.00 359.00 L 848.00 358.00 L 849.00 358.00 L 850.00 357.00 L 851.00 356.00 L 852.00 356.00 L 853.00 355.00 L 854.00 354.00 L 855.00 353.00 L 856.00 353.00 L 857.00 352.00 L 858.00 351.00 L 859.00 351.00 L 860.00 350.00 L 861.00 349.00 L 862.00 348.00 L 863.00 348.00 L 864.00 347.00 L 865.00 346.00 L 866.00 345.00 L 867.00 345.00 L 868.00 344.00 L 869.00 343.00 L 870.00 343.00 L 871.00 342.00 L 872.00 341.00 L 873.00 341.00 L 874.00 340.00 L 875.00 339.00 L 876.00 339.00 L 877.00 338.00 L 878.00 337.00 L 879.00 336.00 L 879.00 316.00 L 878.00 315.00 L 806.00 315.00 Z" },
  { id: 'left-top-wing',       ex: -0.5,  ey: -1.3, d: "M 581 62 L 580 63 L 579 63 L 578 64 L 577 64 L 576 64 L 575 64 L 574 65 L 573 65 L 572 66 L 571 66 L 570 66 L 569 66 L 568 67 L 567 67 L 566 67 L 565 68 L 564 68 L 563 69 L 562 69 L 561 69 L 560 69 L 559 70 L 558 70 L 557 70 L 556 71 L 555 71 L 554 71 L 553 72 L 552 72 L 551 72 L 550 73 L 549 73 L 548 73 L 547 74 L 546 74 L 545 74 L 544 75 L 543 75 L 542 75 L 541 76 L 540 76 L 539 76 L 538 77 L 537 77 L 536 77 L 535 78 L 534 78 L 533 78 L 532 79 L 531 79 L 530 79 L 529 80 L 528 80 L 527 80 L 526 81 L 525 81 L 524 81 L 523 82 L 522 82 L 521 82 L 520 83 L 519 83 L 518 83 L 517 83 L 517 84 L 518 85 L 519 85 L 520 86 L 521 87 L 522 88 L 523 88 L 524 89 L 525 90 L 526 90 L 527 91 L 528 92 L 529 93 L 530 93 L 531 94 L 532 95 L 533 96 L 534 96 L 535 97 L 536 98 L 537 99 L 538 99 L 539 100 L 540 101 L 541 102 L 542 102 L 543 103 L 544 104 L 545 104 L 546 105 L 547 106 L 548 107 L 549 107 L 550 108 L 551 109 L 552 109 L 553 110 L 554 111 L 555 112 L 556 112 L 557 113 L 558 114 L 559 115 L 560 115 L 561 116 L 562 117 L 563 118 L 564 118 L 565 119 L 566 120 L 567 120 L 568 121 L 569 122 L 570 123 L 571 124 L 572 124 L 573 125 L 574 126 L 575 126 L 576 127 L 577 128 L 578 129 L 579 129 L 580 130 L 581 131 L 582 131 L 583 132 L 584 133 L 585 134 L 586 134 L 587 135 L 588 136 L 589 137 L 590 137 L 591 138 L 592 139 L 593 139 L 594 140 L 595 141 L 596 141 L 597 140 L 598 140 L 599 139 L 600 139 L 601 139 L 602 138 L 603 138 L 604 137 L 605 137 L 606 137 L 607 136 L 608 136 L 609 135 L 610 135 L 611 134 L 612 134 L 613 134 L 614 133 L 615 133 L 616 132 L 617 132 L 618 131 L 619 131 L 620 131 L 621 130 L 622 130 L 623 129 L 624 129 L 625 129 L 626 128 L 627 128 L 628 127 L 629 127 L 630 126 L 631 126 L 632 126 L 633 125 L 634 125 L 635 124 L 636 124 L 637 124 L 638 123 L 639 123 L 640 122 L 641 122 L 642 121 L 643 121 L 644 120 L 645 120 L 646 120 L 647 119 L 648 119 L 649 118 L 650 118 L 651 117 L 652 117 L 653 117 L 654 116 L 655 116 L 655 115 L 654 114 L 653 114 L 652 113 L 651 112 L 650 111 L 649 110 L 648 110 L 647 109 L 646 108 L 645 108 L 644 107 L 643 106 L 642 105 L 641 104 L 640 104 L 639 103 L 638 102 L 637 101 L 636 101 L 635 100 L 634 99 L 633 98 L 632 98 L 631 97 L 630 96 L 629 95 L 628 95 L 627 94 L 626 93 L 625 92 L 624 92 L 623 91 L 622 90 L 621 89 L 620 89 L 619 88 L 618 87 L 617 86 L 616 86 L 615 85 L 614 84 L 613 83 L 612 83 L 611 82 L 610 81 L 609 80 L 608 80 L 607 79 L 606 78 L 605 77 L 604 77 L 603 76 L 602 75 L 601 74 L 600 74 L 599 73 L 598 72 L 597 71 L 596 71 L 595 70 L 594 69 L 593 68 L 592 68 L 591 67 L 590 66 L 589 66 L 588 65 L 587 64 L 586 63 L 585 63 L 584 62 L 583 62 L 582 62 Z" },
  { id: 'left-outer-upper',    ex: -1.0,  ey: -0.5, d: "M 512 89 L 511 90 L 511 160 L 512 164 L 513 168 L 514 172 L 515 174 L 516 176 L 517 178 L 518 181 L 519 183 L 520 185 L 521 187 L 522 188 L 523 189 L 524 190 L 525 190 L 526 191 L 527 192 L 528 193 L 529 193 L 530 194 L 531 195 L 532 196 L 533 196 L 534 197 L 535 198 L 536 198 L 537 199 L 538 200 L 539 201 L 540 201 L 541 202 L 542 203 L 543 203 L 544 204 L 545 205 L 546 206 L 547 206 L 548 207 L 549 208 L 550 208 L 551 209 L 552 210 L 553 211 L 554 211 L 555 212 L 556 213 L 557 214 L 558 214 L 559 215 L 560 216 L 561 216 L 562 217 L 563 218 L 564 219 L 565 219 L 566 220 L 567 221 L 568 222 L 569 222 L 570 223 L 571 224 L 572 225 L 573 225 L 574 226 L 575 227 L 576 228 L 577 228 L 578 229 L 579 230 L 580 230 L 581 231 L 582 232 L 583 233 L 584 233 L 585 234 L 586 235 L 587 236 L 588 236 L 589 237 L 590 238 L 591 239 L 592 239 L 593 240 L 593 89 L 592 89 L 591 88 L 590 87 L 589 87 L 588 86 L 587 85 L 586 85 L 585 84 L 584 83 L 583 82 L 582 92 L 581 91 L 580 90 L 579 90 L 578 89 L 577 88 L 576 88 L 575 87 L 574 86 L 573 86 L 572 85 L 571 85 L 570 84 L 569 83 L 568 83 L 567 82 L 566 82 L 565 91 L 564 90 L 563 90 L 562 89 L 561 89 L 560 88 L 559 88 L 558 87 L 557 87 L 556 86 L 555 86 L 554 85 L 553 85 L 552 84 L 551 84 L 550 83 L 549 83 L 548 82 L 547 82 L 546 81 L 545 81 L 544 80 L 543 80 L 542 80 L 541 79 L 540 79 L 539 79 L 538 78 L 537 78 L 536 78 L 535 77 L 534 77 L 533 77 L 532 76 L 531 76 L 530 76 L 529 75 L 528 75 L 527 75 L 526 74 L 525 74 L 524 74 L 523 73 L 522 73 L 521 73 L 520 72 L 519 72 L 518 72 L 517 71 L 516 71 L 515 71 L 514 70 L 513 70 L 512 70 Z" },
  { id: 'left-upper-inner',    ex: -0.3,  ey: -1.0, d: "M 659 122 L 658 123 L 657 123 L 656 124 L 655 124 L 654 125 L 653 125 L 652 125 L 651 126 L 650 126 L 649 127 L 648 127 L 647 127 L 646 128 L 645 128 L 644 129 L 643 129 L 642 130 L 641 130 L 640 130 L 639 131 L 638 131 L 637 132 L 636 132 L 635 132 L 634 133 L 633 133 L 632 134 L 631 134 L 630 135 L 629 135 L 628 135 L 627 136 L 626 136 L 625 137 L 624 137 L 623 137 L 622 138 L 621 138 L 620 139 L 619 139 L 618 140 L 617 140 L 616 140 L 615 141 L 614 141 L 613 142 L 612 142 L 611 143 L 610 143 L 609 143 L 608 144 L 607 144 L 606 145 L 605 145 L 604 146 L 603 146 L 603 147 L 604 148 L 605 149 L 606 149 L 607 150 L 608 151 L 609 152 L 610 153 L 611 153 L 612 154 L 613 155 L 614 156 L 615 156 L 616 157 L 617 158 L 618 159 L 619 160 L 620 160 L 621 161 L 622 162 L 623 163 L 624 163 L 625 164 L 626 165 L 627 166 L 628 167 L 629 167 L 630 168 L 631 169 L 632 170 L 633 171 L 634 171 L 635 172 L 636 173 L 637 174 L 638 174 L 639 175 L 640 176 L 641 177 L 642 178 L 643 178 L 644 179 L 645 180 L 646 181 L 647 181 L 648 181 L 649 181 L 650 181 L 651 180 L 652 180 L 653 180 L 654 179 L 655 179 L 656 179 L 657 178 L 658 178 L 659 178 L 660 177 L 661 177 L 662 177 L 663 176 L 664 176 L 665 176 L 666 175 L 667 175 L 668 175 L 669 174 L 670 174 L 671 174 L 672 173 L 673 173 L 674 173 L 675 172 L 676 172 L 677 172 L 678 171 L 679 171 L 680 171 L 681 170 L 682 170 L 683 170 L 684 169 L 685 169 L 686 169 L 687 168 L 688 168 L 689 168 L 690 167 L 691 167 L 692 167 L 691 166 L 691 165 L 690 164 L 689 163 L 688 162 L 688 161 L 687 160 L 686 159 L 686 158 L 685 157 L 684 156 L 684 155 L 683 154 L 682 153 L 682 152 L 681 151 L 680 150 L 679 149 L 679 148 L 678 147 L 677 146 L 677 145 L 676 144 L 675 143 L 674 142 L 674 141 L 673 140 L 672 139 L 672 138 L 671 137 L 670 136 L 670 135 L 669 134 L 668 133 L 667 132 L 667 131 L 666 130 L 665 129 L 665 128 L 664 127 L 663 126 L 662 125 L 662 124 L 661 123 L 660 122 Z" },
  { id: 'left-mid-inner',      ex: -0.6,  ey:  0.3, d: "M 598 153 L 598 227 L 601 228 L 602 229 L 603 229 L 604 230 L 605 231 L 606 232 L 607 232 L 608 233 L 609 234 L 610 235 L 611 235 L 612 236 L 613 237 L 614 238 L 615 238 L 616 239 L 617 240 L 618 241 L 619 241 L 620 242 L 621 243 L 622 244 L 623 244 L 624 245 L 625 246 L 626 247 L 627 248 L 628 248 L 629 249 L 630 250 L 631 251 L 632 251 L 633 252 L 634 253 L 635 254 L 636 254 L 637 255 L 638 256 L 639 257 L 640 256 L 640 153 Z" },
  { id: 'left-inner-column',   ex: -0.2,  ey:  0.0, d: "M 693 171 L 692 172 L 691 172 L 690 172 L 689 173 L 688 173 L 687 174 L 686 174 L 685 174 L 684 174 L 683 175 L 682 175 L 681 175 L 680 176 L 679 176 L 678 176 L 677 177 L 676 177 L 675 177 L 674 178 L 673 178 L 672 178 L 671 179 L 670 179 L 669 179 L 668 180 L 667 180 L 666 181 L 665 181 L 664 181 L 663 181 L 662 182 L 661 182 L 660 182 L 659 183 L 658 183 L 657 183 L 656 184 L 655 184 L 654 184 L 653 185 L 652 185 L 653 186 L 654 187 L 655 188 L 656 188 L 657 189 L 658 190 L 659 191 L 660 192 L 661 192 L 662 193 L 663 194 L 664 195 L 665 196 L 666 196 L 667 197 L 668 198 L 669 199 L 670 200 L 671 200 L 672 201 L 673 202 L 674 203 L 675 204 L 676 204 L 677 205 L 678 206 L 679 207 L 680 207 L 681 208 L 682 209 L 683 210 L 684 211 L 685 211 L 686 212 L 687 213 L 688 214 L 689 215 L 690 215 L 691 216 L 692 217 L 693 218 L 694 219 L 695 219 L 695 171 L 694 171 Z" },
  { id: 'left-center-wedge',   ex: -0.4,  ey:  0.6, d: "M 648 191 L 648 232 L 645 240 L 645 260 L 644 261 L 646 262 L 647 263 L 648 263 L 649 264 L 650 265 L 651 265 L 652 266 L 653 267 L 654 268 L 655 269 L 656 269 L 657 270 L 658 271 L 659 272 L 660 273 L 661 273 L 662 274 L 663 275 L 664 276 L 665 276 L 666 277 L 667 278 L 668 279 L 669 279 L 670 280 L 671 281 L 672 282 L 673 282 L 674 283 L 675 284 L 676 285 L 677 286 L 678 286 L 679 287 L 680 288 L 681 289 L 682 289 L 683 290 L 684 291 L 685 292 L 686 293 L 687 293 L 688 294 L 689 295 L 690 296 L 691 296 L 692 297 L 693 298 L 694 299 L 695 299 L 695 191 Z" },
  { id: 'right-top-wing',      ex:  0.5,  ey: -1.3, d: "M 813 62 L 812 63 L 811 64 L 810 64 L 809 65 L 808 66 L 807 66 L 806 67 L 805 68 L 804 69 L 803 69 L 802 70 L 801 71 L 800 72 L 799 72 L 798 73 L 797 74 L 796 75 L 795 75 L 794 76 L 793 77 L 792 78 L 791 78 L 790 79 L 789 80 L 788 81 L 787 81 L 786 82 L 785 83 L 784 84 L 783 84 L 782 85 L 781 86 L 780 87 L 779 87 L 778 88 L 777 89 L 776 90 L 775 91 L 774 91 L 773 92 L 772 93 L 771 94 L 770 94 L 769 95 L 768 96 L 767 96 L 766 97 L 765 98 L 764 99 L 763 100 L 762 100 L 761 101 L 760 102 L 759 103 L 758 103 L 757 104 L 756 105 L 755 106 L 754 106 L 753 107 L 752 108 L 751 109 L 750 109 L 749 110 L 748 111 L 747 112 L 746 112 L 745 113 L 744 114 L 743 115 L 742 115 L 742 116 L 743 116 L 744 117 L 745 117 L 746 117 L 747 118 L 748 118 L 749 119 L 750 119 L 751 119 L 752 120 L 753 120 L 754 121 L 755 121 L 756 122 L 757 122 L 758 122 L 759 123 L 760 123 L 761 124 L 762 124 L 763 125 L 764 125 L 765 125 L 766 126 L 767 126 L 768 127 L 769 127 L 770 127 L 771 128 L 772 128 L 773 129 L 774 129 L 775 130 L 776 130 L 777 130 L 778 131 L 779 131 L 780 132 L 781 132 L 782 132 L 783 133 L 784 133 L 785 134 L 786 134 L 787 135 L 788 135 L 789 136 L 790 136 L 791 136 L 792 137 L 793 137 L 794 138 L 795 138 L 796 138 L 797 139 L 798 139 L 799 140 L 800 140 L 801 141 L 802 141 L 803 140 L 804 140 L 805 139 L 806 138 L 807 138 L 808 137 L 809 136 L 810 135 L 811 135 L 812 134 L 813 133 L 814 132 L 815 132 L 816 131 L 817 130 L 818 130 L 819 129 L 820 128 L 821 127 L 822 127 L 823 126 L 824 125 L 825 124 L 826 124 L 827 123 L 828 122 L 829 122 L 830 121 L 831 120 L 832 119 L 833 119 L 834 118 L 835 117 L 836 117 L 837 116 L 838 115 L 839 114 L 840 114 L 841 113 L 842 112 L 843 111 L 844 111 L 845 110 L 846 109 L 847 108 L 848 108 L 849 107 L 850 106 L 851 105 L 852 105 L 853 104 L 854 103 L 855 103 L 856 102 L 857 101 L 858 100 L 859 100 L 860 99 L 861 98 L 862 97 L 863 97 L 864 96 L 865 95 L 866 95 L 867 94 L 868 93 L 869 92 L 870 92 L 871 91 L 872 90 L 873 89 L 874 89 L 875 88 L 876 87 L 877 86 L 878 86 L 879 85 L 880 84 L 881 84 L 880 83 L 816 62 L 815 62 L 814 62 Z" },
  { id: 'right-outer-upper',   ex:  1.0,  ey: -0.5, d: "M 886 89 L 885 90 L 807 147 L 807 239 L 806 239 L 807 238 L 808 237 L 809 237 L 810 236 L 811 235 L 812 234 L 813 234 L 814 233 L 815 232 L 816 231 L 817 231 L 818 230 L 819 229 L 820 229 L 821 228 L 822 227 L 823 226 L 824 226 L 825 225 L 826 224 L 827 223 L 828 223 L 829 222 L 830 221 L 831 221 L 832 220 L 833 219 L 834 218 L 835 218 L 836 217 L 837 216 L 838 215 L 839 215 L 840 214 L 841 213 L 842 213 L 843 212 L 844 211 L 845 210 L 846 210 L 847 209 L 848 208 L 849 207 L 850 207 L 851 206 L 852 205 L 853 205 L 854 204 L 855 203 L 856 202 L 857 202 L 858 201 L 859 200 L 860 200 L 861 199 L 862 198 L 863 197 L 864 197 L 865 196 L 866 195 L 867 194 L 868 194 L 869 193 L 870 192 L 871 191 L 872 191 L 873 190 L 874 189 L 875 188 L 876 187 L 877 186 L 877 184 L 878 183 L 879 180 L 880 178 L 881 176 L 882 174 L 883 171 L 884 169 L 885 167 L 886 164 L 887 161 L 887 89 Z" },
  { id: 'right-upper-inner',   ex:  0.3,  ey: -1.0, d: "M 737 122 L 736 123 L 706 167 L 707 167 L 708 168 L 709 168 L 710 168 L 711 169 L 712 169 L 713 169 L 714 170 L 715 170 L 716 170 L 717 171 L 718 171 L 719 171 L 720 172 L 721 172 L 722 172 L 723 173 L 724 173 L 725 173 L 726 174 L 727 174 L 728 174 L 729 175 L 730 175 L 731 175 L 732 175 L 733 176 L 734 176 L 735 177 L 736 177 L 737 177 L 738 178 L 739 178 L 740 178 L 741 178 L 742 179 L 743 179 L 744 180 L 745 180 L 746 180 L 747 180 L 748 181 L 749 181 L 750 181 L 751 181 L 752 180 L 753 179 L 754 179 L 755 178 L 756 177 L 757 176 L 758 175 L 759 175 L 760 174 L 761 173 L 762 172 L 763 172 L 764 171 L 765 170 L 766 169 L 767 168 L 768 167 L 769 167 L 770 166 L 771 165 L 772 164 L 773 164 L 774 163 L 775 162 L 776 161 L 777 160 L 778 160 L 779 159 L 780 158 L 781 157 L 782 157 L 783 156 L 784 155 L 785 154 L 786 153 L 787 153 L 788 152 L 789 151 L 790 150 L 791 149 L 792 149 L 793 148 L 794 147 L 795 146 L 794 145 L 793 145 L 792 145 L 791 144 L 790 144 L 789 143 L 788 143 L 787 143 L 786 142 L 785 142 L 784 141 L 783 141 L 782 140 L 781 140 L 780 140 L 779 139 L 778 139 L 777 138 L 776 138 L 775 138 L 774 137 L 773 137 L 772 136 L 771 136 L 770 136 L 769 135 L 768 135 L 767 134 L 766 134 L 765 133 L 764 133 L 763 133 L 762 132 L 761 132 L 760 131 L 759 131 L 758 131 L 757 130 L 756 130 L 755 129 L 754 129 L 753 129 L 752 128 L 751 128 L 750 127 L 749 127 L 748 126 L 747 126 L 746 126 L 745 125 L 744 125 L 743 124 L 742 124 L 741 124 L 740 123 L 739 123 L 738 122 Z" },
  { id: 'right-mid-inner',     ex:  0.6,  ey:  0.3, d: "M 798 153 L 797 154 L 754 188 L 754 258 L 753 257 L 757 248 L 757 153 Z" },
  { id: 'right-inner-column',  ex:  0.2,  ey:  0.0, d: "M 703 171 L 703 219 L 704 218 L 705 217 L 706 216 L 707 216 L 708 215 L 709 214 L 710 213 L 711 212 L 712 212 L 713 211 L 714 210 L 715 209 L 716 208 L 717 208 L 718 207 L 719 206 L 720 205 L 721 205 L 722 204 L 723 203 L 724 202 L 725 201 L 726 201 L 727 200 L 728 199 L 729 198 L 730 197 L 731 197 L 732 196 L 733 195 L 734 194 L 735 194 L 736 193 L 737 192 L 738 191 L 739 190 L 740 190 L 741 189 L 742 188 L 743 187 L 744 186 L 745 186 L 746 185 L 745 185 L 744 184 L 743 184 L 742 184 L 741 183 L 740 183 L 739 183 L 738 182 L 737 182 L 736 182 L 735 181 L 734 181 L 733 181 L 732 180 L 731 180 L 730 180 L 729 179 L 728 179 L 727 179 L 726 178 L 725 178 L 724 178 L 723 177 L 722 177 L 721 177 L 720 176 L 719 176 L 718 176 L 717 175 L 716 175 L 715 175 L 714 174 L 713 174 L 712 174 L 711 174 L 710 173 L 709 173 L 708 173 L 707 172 L 706 172 L 705 171 L 704 171 Z" },
  { id: 'right-center-wedge',  ex:  0.4,  ey:  0.6, d: "M 748 192 L 747 193 L 703 228 L 703 299 L 704 298 L 705 297 L 706 297 L 707 296 L 708 295 L 709 294 L 710 294 L 711 293 L 712 292 L 713 291 L 714 291 L 715 290 L 716 289 L 717 288 L 718 287 L 719 287 L 720 286 L 721 285 L 722 284 L 723 284 L 724 283 L 725 282 L 726 281 L 727 281 L 728 280 L 729 279 L 730 278 L 731 277 L 732 277 L 733 276 L 734 275 L 735 274 L 736 274 L 737 273 L 738 272 L 739 271 L 740 271 L 741 270 L 742 269 L 743 268 L 744 268 L 745 267 L 746 266 L 747 265 L 748 265 L 749 264 L 750 263 L 751 262 L 752 262 L 753 261 L 754 260 L 754 192 L 749 192 Z" },
];

function CanvasLogo({ state }: { state: LogoState }) {
  const def = LOGO_STATES[state];
  const key = (state === 'idle' || state === 'dirty') ? 'calm' : state;
  const isExplode = state === 'success';

  return (
    <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none', zIndex: 0 }}>
      <style>{LOGO_KEYFRAMES}</style>
      <svg
        key={key}
        xmlns="http://www.w3.org/2000/svg"
        width={720} height={572}
        viewBox="0 0 412 327"
        fill={def.filter ? '#ffffff' : '#ffffff'}
        opacity={def.opacity}
        style={{ animation: def.animation, filter: def.filter }}
      >
        <g transform="translate(-493 -62)">
          {LOGO_PATHS.map(({ id, d, ex, ey }, i) => (
            <path
              key={id}
              d={d}
              fill="#ffffff"
              fillRule="evenodd"
              style={isExplode ? {
                // @ts-ignore
                '--ex': ex,
                '--ey': ey,
                '--rot': `${(ex + ey) * 45}deg`,
                animation: 'logo-explode 1.8s cubic-bezier(0.25,0.46,0.45,0.94) forwards',
                animationDelay: `${i * 0.06}s`,
                transformOrigin: 'center',
                transformBox: 'fill-box',
              } as React.CSSProperties : undefined}
            />
          ))}
        </g>
      </svg>
    </div>
  );
}

// ── Canvas inner (needs ReactFlow context) ────────────────────────────────────
function CanvasInner({
  nodes, edges, onNodesChange, onEdgesChange, onConnect, onDrop, onDragOver, selectedNode, setSelectedNode, onUpdateNode, onDeleteEdge, onAutoLayout, logoState,
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
          nodeColor={(n: Node) => n.type === 'entryPoint' ? C.cyan : n.type === 'orchestrator' ? C.purple : C.green}
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
  entryPoint:   { accepts: [],                     emits: ['request'] },  // multiple allowed, unique by slug
  orchestrator: { accepts: ['request', 'signal'],   emits: ['task', 'signal'] },
  agent:        { accepts: ['task'],                emits: ['result'] },
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

// ── Chain analysis ────────────────────────────────────────────────────────────
interface ChainStatus {
  ready: boolean;
  label: string;
  color: string;
  epNode?: Node;
  orchNode?: Node;
  agentCount: number;
}

function analyzeChain(nodes: Node[], edges: Edge[]): ChainStatus {
  const miss = (msg: string) => ({ ready: false, label: msg, color: C.error, agentCount: 0 });

  const epNodes = nodes.filter(n => n.type === 'entryPoint');
  if (epNodes.length === 0) return miss('Drop an Entry Point to start');

  // All entry points must have unique non-empty slugs
  const epSlugs = epNodes.map(n => (n.data as EntryPointData).slug?.trim() ?? '');
  const missingSlug = epNodes.find(n => !(n.data as EntryPointData).slug);
  if (missingSlug) return miss('Every entry point needs a unique slug');
  const slugSet = new Set(epSlugs);
  if (slugSet.size !== epSlugs.length) return miss('Duplicate entry point slug — each slug must be unique');
  const badSlug = epSlugs.find(s => !/^[a-z0-9_-]{1,64}$/.test(s));
  if (badSlug) return miss(`Slug "${badSlug}": lowercase letters, numbers, _ or - only`);

  // For chain analysis use first EP that connects to an orchestrator
  const epNode = epNodes.find(n => edges.some(e => e.source === n.id && nodes.find(t => t.id === e.target && t.type === 'orchestrator'))) ?? epNodes[0];
  const epData = epNode.data as EntryPointData;

  const orchEdge = edges.find(e => e.source === epNode.id);
  const orchNode = orchEdge ? nodes.find(n => n.id === orchEdge.target && n.type === 'orchestrator') : undefined;
  if (!orchNode) return miss('Connect an Orchestrator to the Entry Point');

  const agentCount = edges
    .filter(e => e.source === orchNode.id)
    .map(e => nodes.find(n => n.id === e.target && n.type === 'agent'))
    .filter(Boolean).length;

  return {
    ready: true,
    label: `Ready · ${epData.epType.toUpperCase()} · ${(orchNode.data as OrchestratorData).displayName} · ${agentCount} agent${agentCount !== 1 ? 's' : ''}`,
    color: C.green,
    epNode,
    orchNode,
    agentCount,
  };
}

// ── Compute styled edges based on chain validity ──────────────────────────────
function styledEdges(edges: Edge[], nodes: Node[]): Edge[] {
  const epNode = nodes.find(n => n.type === 'entryPoint');
  const orchEdge = epNode ? edges.find(e => e.source === epNode.id) : undefined;
  const orchNode = orchEdge ? nodes.find(n => n.id === orchEdge.target && n.type === 'orchestrator') : undefined;

  return edges.map(e => {
    const isChain =
      (epNode && orchNode && e.source === epNode.id && e.target === orchNode.id) ||
      (orchNode && e.source === orchNode.id);
    return {
      ...e,
      animated: !!isChain,
      style: isChain
        ? { stroke: C.cyan, strokeWidth: 2 }
        : { stroke: C.error, strokeWidth: 1.5, strokeDasharray: '5 4' },
    };
  });
}

function toSlug(s: string) {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64);
}

// ── Builder view ──────────────────────────────────────────────────────────────
function BuilderView({
  app,
  orchestrators,
  agents,
  onBack,
  onSaved,
}: {
  app: Application | null;
  orchestrators: OrchestratorFull[];
  agents: Agent[];
  onBack: () => void;
  onSaved: () => void;
}) {
  const initial = app
    ? buildNodesFromApp(app, orchestrators.find(o => o.id === app.orchestrator_id), agents)
    : {
        nodes: [],
        edges: [],
      };

  const [nodes, setNodes, onNodesChange] = useNodesState(initial.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initial.edges);
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);
  const [saving, setSaving] = useState(false);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
  const [libWidth, setLibWidth] = useState(280);
  const [appName, setAppName] = useState(app?.name ?? '');
  const [slugLocked, setSlugLocked] = useState(!!app?.slug);
  const [isDirty, setIsDirty] = useState(false);
  const [logoState, setLogoState] = useState<LogoState>('idle');
  const logoTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const rfWrapper = useRef<HTMLDivElement>(null);

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
    setNodes((nds: Node[]) => nds.filter(n => n.id !== id));
    setEdges((eds: Edge[]) => eds.filter(e => e.source !== id && e.target !== id));
    setSelectedNode((prev: Node | null) => prev?.id === id ? null : prev);
  };

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

  const onConnect = useCallback((c: Connection) => {
    const nds = nodesRef.current;
    const eds = edgesRef.current;
    const srcNode = nds.find(n => n.id === c.source);
    const tgtNode = nds.find(n => n.id === c.target);
    if (!srcNode || !tgtNode) return;
    const err = validateConnection(srcNode.type!, tgtNode.type!, c.source!, c.target!, eds);
    if (err) { showToast(err, false); return; }
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

    // Entry points: allow multiple, but auto-clear slug to force uniqueness
    if (nodeType === 'entryPoint') {
      nodeData = { ...nodeData, slug: '' };
    }

    const position = screenToFlowPosition({ x: e.clientX, y: e.clientY });
    const newNode: Node = { id: makeId(), type: nodeType, position, data: nodeData };
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
    const chain = analyzeChain(nodes, edges);
    if (!chain.ready) { showToast(chain.label, false); triggerLogo('error', 1800); return; }

    const epData = chain.epNode!.data as EntryPointData;
    const orchData = chain.orchNode!.data as OrchestratorData;

    // Collect agent IDs from orchestrator edges (full replace — empty array clears them)
    const agentIds = edges
      .filter((e: Edge) => e.source === chain.orchNode!.id)
      .map((e: Edge) => nodes.find((n: Node) => n.id === e.target && n.type === 'agent'))
      .filter((n: Node | undefined): n is Node => Boolean(n))
      .map((n: Node) => (n.data as AgentData).agentId);

    setSaving(true);
    setLogoState('thinking');
    try {
      // Always update orchestrator agent list (full replace)
      await themApi.updateOrchestrator(orchData.orchestratorId, { allowed_agent_ids: agentIds });

      const body = {
        name: appName || epData.label || epData.slug,
        slug: epData.slug,
        entry_point_type: epData.epType,
        orchestrator_id: orchData.orchestratorId,
        access_policy: { mode: epData.accessMode },
        enabled: deploy ? true : (app?.enabled ?? false),
      };

      if (app?.id) {
        await themApi.updateApplication(app.id, body);
      } else {
        await themApi.createApplication(body);
      }
      setIsDirty(false);
      triggerLogo('success', deploy ? 2500 : 1800);
      showToast(deploy ? '🚀 Application deployed!' : 'Saved successfully', true);
      onSaved();
    } catch (err: any) {
      triggerLogo('error', 1800);
      showToast(err?.message ?? 'Save failed', false);
    } finally {
      setSaving(false);
    }
  }

  const chain = analyzeChain(nodes, edges);

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
                fontSize: 15, fontWeight: 700, color: '#e2e8f0',
                fontFamily: 'Geist, sans-serif', width: '100%', padding: 0,
              }}
            />
            {epNode && (
              <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>
                {(epNode.data as EntryPointData).slug || (app?.slug ?? '')}
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
        <NodeLibrary orchestrators={orchestrators} agents={agents} width={libWidth} onWidthChange={setLibWidth} />

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
          />
        </div>

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
          chain={chain}
          app={app}
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
    rgba(10,18,32,0.92);
  border: 1px solid rgba(255,255,255,0.07);
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
  background: rgba(30,41,59,0.55);
  color: #94a3b8;
  border: 1px solid rgba(255,255,255,0.08);
}
.app-card-btn--urls:hover {
  border-color: rgba(129,140,248,0.45);
  color: #818cf8;
  background: rgba(99,102,241,0.1);
}
.app-card-btn--toggle-on {
  background: rgba(30,41,59,0.55);
  color: #f87171;
  border: 1px solid rgba(248,113,113,0.2);
}
.app-card-btn--toggle-on:hover {
  border-color: rgba(248,113,113,0.5);
  background: rgba(248,113,113,0.08);
}
.app-card-btn--toggle-off {
  background: rgba(30,41,59,0.55);
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
const EP_ICON: Record<string, string> = { websocket: 'settings_input_component', sse: 'stream', webrtc: 'videocam' };
const EP_LABEL: Record<string, string> = { websocket: 'WebSocket', sse: 'SSE', webrtc: 'WebRTC' };

function epIconColor(type: string): { color: string; glow: string; border: string } {
  if (type === 'websocket') return { color: '#00d1ff', glow: 'rgba(0,209,255,0.25)', border: 'rgba(0,209,255,0.45)' };
  if (type === 'sse')       return { color: '#a78bfa', glow: 'rgba(167,139,250,0.22)', border: 'rgba(167,139,250,0.42)' };
  return { color: '#94a3b8', glow: 'rgba(148,163,184,0.15)', border: 'rgba(148,163,184,0.3)' };
}

// ── AppCard sub-component ─────────────────────────────────────────────────────
function AppCard({
  app,
  onEdit,
  onToggle,
  onDelete,
  onUrls,
  onCopy,
  copiedId,
}: {
  app: Application;
  onEdit: (a: Application) => void;
  onToggle: (a: Application) => void;
  onDelete: (a: Application) => void;
  onUrls: (a: Application) => void;
  onCopy: (val: string, id: string) => void;
  copiedId: string | null;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const [reachable, setReachable] = useState<boolean | null>(null);

  useEffect(() => {
    if (!menuOpen) return;
    function handler(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as unknown as globalThis.Node)) setMenuOpen(false);
    }
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [menuOpen]);

  // Liveness probe — only run for enabled apps, once on mount
  useEffect(() => {
    if (!app.enabled) { setReachable(null); return; }
    let cancelled = false;
    themApi.pingApp(app.slug).then(ok => { if (!cancelled) setReachable(ok); });
    return () => { cancelled = true; };
  }, [app.enabled, app.slug]);

  const ep = epIconColor(app.entry_point_type);
  const accessMode = (app.access_policy as any)?.mode ?? 'token';

  return (
    <div
      className="app-glass-card"
      style={{ borderRadius: 16, overflow: 'visible', display: 'flex', flexDirection: 'column', position: 'relative' }}
    >
      {/* Clickable top section → opens builder */}
      <div
        style={{ padding: '20px 20px 16px', flex: 1, display: 'flex', flexDirection: 'column', gap: 14, cursor: 'pointer' }}
        onClick={() => onEdit(app)}
      >
        {/* Icon + name row */}
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 14 }}>
          {/* Icon tile 56×56 */}
          <div style={{
            width: 56, height: 56, borderRadius: 12, flexShrink: 0,
            background: `radial-gradient(circle at 30% 30%, ${ep.glow}, transparent 70%)`,
            border: `1px solid ${ep.border}`,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <span className="material-symbols-outlined" style={{ fontSize: 26, color: ep.color }}>
              {EP_ICON[app.entry_point_type] ?? 'extension'}
            </span>
          </div>

          {/* Name + badges */}
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontWeight: 700, fontSize: 16, color: C.text, fontFamily: 'Geist, sans-serif', marginBottom: 6, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {app.name}
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center' }}>
              {/* Enabled + reachability pill */}
              <span
                title={app.enabled ? (reachable === null ? 'Checking reachability…' : reachable ? 'Endpoint reachable' : 'Endpoint unreachable — check routing') : 'Disabled'}
                style={{
                  display: 'inline-flex', alignItems: 'center', gap: 5,
                  padding: '2px 8px', borderRadius: 20, fontSize: 11, fontWeight: 700,
                  background: app.enabled
                    ? reachable === false ? 'rgba(245,158,11,0.1)' : 'rgba(74,222,128,0.1)'
                    : 'rgba(255,180,171,0.1)',
                  color: app.enabled
                    ? reachable === false ? '#f59e0b' : C.green
                    : C.error,
                  border: `1px solid ${app.enabled
                    ? reachable === false ? 'rgba(245,158,11,0.4)' : C.greenBorder
                    : 'rgba(255,180,171,0.3)'}`,
                }}
              >
                {app.enabled && (
                  <span style={{
                    width: 6, height: 6, borderRadius: '50%', display: 'inline-block',
                    background: reachable === null ? C.textMuted : reachable ? C.green : '#f59e0b',
                    boxShadow: reachable ? '0 0 6px rgba(74,222,128,0.8)' : reachable === false ? '0 0 6px rgba(245,158,11,0.8)' : 'none',
                  }} />
                )}
                {app.enabled ? (reachable === null ? 'live' : reachable ? 'live ✓' : 'unreachable') : 'disabled'}
              </span>
              {/* Entry-point type badge */}
              <span style={{
                display: 'inline-flex', alignItems: 'center', gap: 4,
                padding: '2px 8px', borderRadius: 20, fontSize: 11, fontWeight: 700,
                background: 'rgba(255,255,255,0.04)', color: ep.color,
                border: `1px solid ${ep.border}`,
              }}>
                {EP_LABEL[app.entry_point_type] ?? app.entry_point_type}
              </span>
            </div>
            {/* Slug subtext */}
            <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', marginTop: 5 }}>
              {app.slug}
            </div>
          </div>

          {/* Three-dot menu (top-right) */}
          <div ref={menuRef} style={{ position: 'relative', flexShrink: 0 }} onClick={e => e.stopPropagation()}>
            <button
              onClick={() => setMenuOpen(v => !v)}
              style={{ width: 32, height: 32, borderRadius: 8, cursor: 'pointer', background: 'rgba(30,41,59,0.5)', border: '1px solid rgba(255,255,255,0.06)', color: '#64748b', display: 'flex', alignItems: 'center', justifyContent: 'center', transition: 'color 150ms ease, border-color 150ms ease' }}
            >
              <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
                <circle cx="8" cy="3" r="1.5"/><circle cx="8" cy="8" r="1.5"/><circle cx="8" cy="13" r="1.5"/>
              </svg>
            </button>
            {menuOpen && (
              <div style={{
                position: 'absolute', top: 36, right: 0, zIndex: 50, minWidth: 130,
                background: 'rgba(10,18,32,0.97)', border: '1px solid rgba(255,255,255,0.1)',
                borderRadius: 10, boxShadow: '0 8px 32px rgba(0,0,0,0.5)', overflow: 'hidden',
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

        {/* Two stat tiles */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          {/* Orchestrator tile */}
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.06)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 18, color: '#a78bfa', flexShrink: 0 }}>hub</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Orchestrator</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {app.orchestrator_name ?? <span style={{ color: C.textMuted, fontStyle: 'italic' }}>none</span>}
              </div>
            </div>
          </div>
          {/* Access policy tile */}
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.06)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 18, color: accessMode === 'public' ? C.green : '#f59e0b', flexShrink: 0 }}>
              {accessMode === 'public' ? 'lock_open' : 'lock'}
            </span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Access</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600 }}>{accessMode === 'public' ? 'Public' : 'Token'}</div>
            </div>
          </div>
        </div>
      </div>

      {/* Action buttons (3-column grid) */}
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.06)', padding: '12px 16px', display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 8 }}>
        <button className="app-card-btn app-card-btn--open" onClick={() => onEdit(app)}>
          🖥️ Open
        </button>
        <button className="app-card-btn app-card-btn--urls" onClick={() => onUrls(app)}>
          🔗 URLs
        </button>
        {app.enabled ? (
          <button className="app-card-btn app-card-btn--toggle-on" onClick={() => onToggle(app)}>
            🔴 Disable
          </button>
        ) : (
          <button className="app-card-btn app-card-btn--toggle-off" onClick={() => onToggle(app)}>
            🟢 Enable
          </button>
        )}
      </div>
    </div>
  );
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

  function copy(val: string, id: string) {
    navigator.clipboard?.writeText(val).catch(() => {});
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 1800);
  }

  return (
    <div style={{ marginLeft: 260, minHeight: '100vh', background: C.bg, padding: '36px 48px' }}>
      <style>{APP_CARD_STYLES}</style>

      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 32 }}>
        <div>
          <h1 style={{ fontSize: 26, fontWeight: 700, color: C.text, margin: 0, fontFamily: 'Geist, sans-serif', letterSpacing: -0.5 }}>
            Applications
          </h1>
          <p style={{ fontSize: 13, color: C.textMuted, margin: '6px 0 0', fontFamily: 'Inter, sans-serif' }}>
            Compose orchestrators and entry points into deployable agentic applications.
          </p>
        </div>
      </div>

      {!loading && list.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '80px 0', color: C.textMuted }}>
          <span className="material-icons" style={{ fontSize: 56, marginBottom: 16, opacity: 0.25, display: 'block' }}>apps</span>
          <div style={{ fontSize: 16, fontWeight: 600, marginBottom: 8, color: C.text, fontFamily: 'Geist, sans-serif' }}>No applications yet</div>
          <div style={{ fontSize: 13, fontFamily: 'Inter, sans-serif' }}>Create one to expose an orchestrator as a shareable endpoint.</div>
          <button onClick={onNew} style={{ marginTop: 24, padding: '10px 22px', borderRadius: 10, border: 'none', cursor: 'pointer', background: C.cyan, color: '#00363a', fontWeight: 700, fontSize: 14, boxShadow: '0 0 14px rgba(0,240,255,0.3)', fontFamily: 'Inter, sans-serif' }}>
            + New Application
          </button>
        </div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: 20 }}>
          {list.map(app => (
            <AppCard
              key={app.id}
              app={app}
              onEdit={onEdit}
              onToggle={onToggle}
              onDelete={onDelete}
              onUrls={setUrlModalApp}
              onCopy={copy}
              copiedId={copiedId}
            />
          ))}
          {/* Deploy / New card */}
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
            <div style={{
              width: 52, height: 52, borderRadius: 14, border: '2px dashed rgba(99,102,241,0.5)',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
            }}>
              <span className="material-icons" style={{ fontSize: 26, color: '#818cf8' }}>add</span>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, color: '#818cf8', fontFamily: 'Geist, sans-serif' }}>New Application</div>
          </div>
        </div>
      )}

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
            {(() => {
              const app = urlModalApp;
              const urls: Array<{ label: string; val: string }> = [];
              if (app.entry_point_type === 'websocket') urls.push({ label: 'WebSocket', val: `ws://<host>:8088/apps/${app.slug}/ws` });
              if (app.entry_point_type === 'sse') urls.push({ label: 'SSE', val: `http://<host>:8088/apps/${app.slug}/sse` }, { label: 'REST', val: `http://<host>:8088/apps/${app.slug}` });
              if (app.entry_point_type === 'webrtc') urls.push({ label: 'WebRTC (soon)', val: `ws://<host>:8088/apps/${app.slug}/ws` });
              return urls.map(({ label, val }) => {
                const cid = `modal_${app.id}_${label}`;
                return (
                  <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
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
              });
            })()}
            <div style={{ marginTop: 14, fontSize: 11, color: C.textMuted, fontFamily: 'Inter, sans-serif' }}>
              {(urlModalApp.access_policy as any)?.mode === 'public'
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
