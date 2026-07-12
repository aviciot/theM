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

interface AdvisorMessage { role: 'user' | 'assistant'; text: string; streaming?: boolean; }

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
function EntryPointNode({ id, data, selected }: { id: string; data: EntryPointData & { _scanning?: boolean }; selected?: boolean }) {
  const slugMissing = !data.slug;
  return (
    <div
      style={{
        minWidth: 200, width: 'fit-content', padding: '14px 18px', borderRadius: 12,
        background: C.cyanBg,
        border: `1px solid ${data._scanning ? C.cyan : slugMissing ? 'rgba(255,180,100,0.6)' : selected ? C.cyan : C.cyanBorder}`,
        boxShadow: data._scanning ? '0 0 24px rgba(0,240,255,0.7)' : selected ? '0 0 20px rgba(0,240,255,0.3)' : C.cyanGlow,
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
function OrchestratorNode({ id, data, selected }: { id: string; data: OrchestratorData & { _scanning?: boolean }; selected?: boolean }) {
  return (
    <div
      style={{
        minWidth: 200, width: 'fit-content', padding: '16px 20px', borderRadius: 12,
        background: C.purpleBg,
        border: `2px solid ${data._scanning ? C.cyan : selected ? '#e8d5ff' : C.purpleBorder}`,
        boxShadow: data._scanning ? '0 0 24px rgba(0,240,255,0.7)' : selected ? '0 0 24px rgba(208,188,255,0.4)' : C.purpleGlow,
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
function AgentNode({ id, data, selected }: { id: string; data: AgentData & { _scanning?: boolean }; selected?: boolean }) {
  return (
    <div
      style={{
        minWidth: 160, maxWidth: 220, width: 'fit-content', padding: '12px 16px', borderRadius: 12,
        background: C.greenBg,
        border: `1px solid ${data._scanning ? C.cyan : selected ? C.green : C.greenBorder}`,
        boxShadow: data._scanning ? '0 0 24px rgba(0,240,255,0.7)' : selected ? '0 0 20px rgba(74,222,128,0.25)' : '0 0 10px rgba(74,222,128,0.08)',
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
              {orchestrators.filter(o => o.enabled && o.name !== 'workflow_advisor').map(o => (
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
              {agents.filter(a => a.enabled && !a.tags?.includes('internal')).map(a => {
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
  idle:     { opacity: 0.35, filter: 'drop-shadow(0 0 24px rgba(160,240,208,0.5))',   animation: 'logo-breathe 20s ease-in-out infinite' },
  dirty:    { opacity: 0.22, filter: 'drop-shadow(0 0 14px rgba(245,158,11,0.25))',   animation: 'logo-sway 2.5s ease-in-out infinite' },
  warning:  { opacity: 0.45, filter: 'drop-shadow(0 0 18px rgba(255,120,120,0.4))',    animation: 'logo-warn-flash 1.2s ease-in-out 1 forwards' },
  error:    { opacity: 0.35, filter: 'drop-shadow(0 0 18px rgba(255,107,138,0.4))',   animation: 'logo-shake 0.5s ease-in-out' },
  success:  { opacity: 1.0,  filter: 'drop-shadow(0 0 40px rgba(74,222,128,0.9))',    animation: 'logo-burst 1.8s ease-out forwards' },
  thinking: { opacity: 1.0,  filter: 'none',                                           animation: 'none' },
};

const LOGO_KEYFRAMES = `
@keyframes logo-breathe {
  0%   { opacity: 0.18; }
  50%  { opacity: 0.45; }
  100% { opacity: 0.18; }
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
        opacity={def.opacity}
        style={{ animation: def.animation, filter: def.filter, overflow: 'visible' }}
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
function AdvisorPanel({
  messages, busy, input, scanning,
  onInputChange, onSend, onClose, onRescan,
}: {
  messages: AdvisorMessage[];
  busy: boolean;
  input: string;
  scanning: boolean;
  onInputChange: (v: string) => void;
  onSend: (text: string) => void;
  onClose: () => void;
  onRescan: () => void;
}) {
  const bottomRef = useRef<HTMLDivElement>(null);
  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }); }, [messages]);

  return (
    <div style={{
      width: 360, flexShrink: 0, height: '100%', display: 'flex', flexDirection: 'column',
      background: 'rgba(10,14,23,0.97)', borderLeft: `1px solid rgba(0,240,255,0.15)`,
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
        {messages.map((m, i) => (
          <div key={i} style={{ display: 'flex', flexDirection: 'column', alignItems: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
            {m.role === 'assistant' && (
              <span style={{ fontSize: 10, color: C.textMuted, marginBottom: 3, paddingLeft: 2 }}>AI Advisor</span>
            )}
            <div style={{
              maxWidth: '92%', padding: '9px 12px',
              borderRadius: m.role === 'user' ? '12px 12px 2px 12px' : '2px 12px 12px 12px',
              background: m.role === 'user' ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.04)',
              border: `1px solid ${m.role === 'user' ? 'rgba(0,240,255,0.2)' : C.outlineVariant}`,
              fontSize: 13, color: m.role === 'user' ? C.text : '#d1d5db',
              lineHeight: 1.65, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            }}>
              {m.text}
              {m.streaming && <span style={{ opacity: 0.6, marginLeft: 2 }}>▋</span>}
            </div>
          </div>
        ))}
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
              borderRadius: 8, color: '#e2e8f0', fontSize: 13, padding: '7px 10px',
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

  // ── Advisor functions ────────────────────────────────────────────────────────

  function serializeWorkflow(): object {
    const nds = nodesRef.current;
    const eds = edgesRef.current;
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
          return {
            type: 'orchestrator', id: n.id, name: d.name, displayName: d.displayName,
            model: d.model, maxParallelTools: d.maxParallelTools,
            systemPrompt: rawPrompt.slice(0, 600) + (rawPrompt.length > 600 ? '…' : ''),
            allowedAgentIds: full?.allowed_agent_ids ?? [],
          };
        }
        if (n.type === 'agent') {
          const d = n.data as AgentData;
          const full = agents.find(a => a.id === d.agentId);
          return {
            type: 'agent', id: n.id, slug: d.name, displayName: d.displayName,
            description: d.description, transport: d.transport,
            hasAuthToken: full?.auth_token_set ?? false,
            lastScanResult: full?.last_scan_result
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
          const buf = advisorBufRef.current;
          setAdvisorMessages(prev => {
            const last = prev[prev.length - 1];
            if (last?.role === 'assistant') {
              return [...prev.slice(0, -1), { role: 'assistant', text: buf, streaming: true }];
            }
            return [...prev, { role: 'assistant', text: buf, streaming: true }];
          });
        } else if (msg.type === 'done') {
          setAdvisorMessages(prev => {
            const last = prev[prev.length - 1];
            if (last?.role === 'assistant') return [...prev.slice(0, -1), { ...last, streaming: false }];
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
    if (advisorOpen) { setAdvisorOpen(false); return; }

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
            onClose={() => setAdvisorOpen(false)}
            onRescan={handleAdvisorRescan}
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
