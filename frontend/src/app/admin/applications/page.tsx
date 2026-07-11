'use client';
import { useEffect, useState, useCallback, useRef, DragEvent } from 'react';
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

// ── Types ────────────────────────────────────────────────────────────────────
const ENTRY_POINT_TYPES = ['websocket', 'sse'] as const;
type EntryPointType = typeof ENTRY_POINT_TYPES[number];

interface EntryPointData { label: string; epType: EntryPointType; accessMode: 'token' | 'public'; slug: string; [key: string]: unknown; }
interface OrchestratorData { orchestratorId: string; name: string; displayName: string; model: string | null; maxParallelTools: number; [key: string]: unknown; }
interface AgentData { agentId: string; name: string; displayName: string; description: string; transport: string; endpointUrl: string; [key: string]: unknown; }

// ── Node Components ──────────────────────────────────────────────────────────
function EntryPointNode({ data, selected }: { data: EntryPointData; selected?: boolean }) {
  const icon = data.epType === 'sse' ? 'stream' : 'settings_input_component';
  return (
    <div style={{
      minWidth: 160, padding: '14px 18px', borderRadius: 12,
      background: C.cyanBg, border: `1px solid ${selected ? C.cyan : C.cyanBorder}`,
      boxShadow: selected ? `0 0 20px rgba(0,240,255,0.3)` : C.cyanGlow,
      fontFamily: 'Inter, sans-serif', cursor: 'default', transition: 'all 0.15s',
    }}>
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan, border: `2px solid ${C.bg}`, width: 10, height: 10 }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <span className="material-icons" style={{ fontSize: 22, color: C.cyan }}>{icon}</span>
        <div>
          <div style={{ fontSize: 14, fontWeight: 600, color: C.text }}>{data.label || (data.epType === 'sse' ? 'SSE' : 'WebSocket')}</div>
          <div style={{ fontSize: 10, fontWeight: 700, color: C.cyan, letterSpacing: 1, textTransform: 'uppercase', marginTop: 2 }}>Entry Point</div>
        </div>
      </div>
    </div>
  );
}

function OrchestratorNode({ data, selected }: { data: OrchestratorData; selected?: boolean }) {
  return (
    <div style={{
      minWidth: 200, padding: '16px 20px', borderRadius: 12,
      background: C.purpleBg, border: `2px solid ${selected ? '#e8d5ff' : C.purpleBorder}`,
      boxShadow: selected ? `0 0 24px rgba(208,188,255,0.4)` : C.purpleGlow,
      fontFamily: 'Inter, sans-serif', cursor: 'default', transition: 'all 0.15s',
    }}>
      <Handle type="target" position={Position.Top} style={{ background: C.purple, border: `2px solid ${C.bg}`, width: 10, height: 10 }} />
      <Handle type="source" position={Position.Bottom} style={{ background: C.purple, border: `2px solid ${C.bg}`, width: 10, height: 10 }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <span className="material-icons" style={{ fontSize: 22, color: C.purple }}>hub</span>
        <div>
          <div style={{ fontSize: 14, fontWeight: 600, color: C.text }}>{data.displayName}</div>
          <div style={{ fontSize: 10, fontWeight: 700, color: C.purple, letterSpacing: 1, textTransform: 'uppercase', marginTop: 2 }}>Orchestrator</div>
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

function AgentNode({ data, selected }: { data: AgentData; selected?: boolean }) {
  return (
    <div style={{
      minWidth: 160, maxWidth: 220, padding: '12px 16px', borderRadius: 12,
      background: C.greenBg, border: `1px solid ${selected ? C.green : C.greenBorder}`,
      boxShadow: selected ? `0 0 20px rgba(74,222,128,0.25)` : `0 0 10px rgba(74,222,128,0.08)`,
      fontFamily: 'Inter, sans-serif', cursor: 'default', transition: 'all 0.15s',
    }}>
      <Handle type="target" position={Position.Top} style={{ background: C.green, border: `2px solid ${C.bg}`, width: 10, height: 10 }} />
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
        <span className="material-icons" style={{ fontSize: 18, color: C.green, marginTop: 1, flexShrink: 0 }}>smart_toy</span>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: C.text, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{data.displayName}</div>
          <div style={{ fontSize: 10, fontWeight: 700, color: C.green, letterSpacing: 1, textTransform: 'uppercase', marginTop: 1 }}>Agent</div>
          {data.description && (
            <div style={{ fontSize: 11, color: C.textMuted, marginTop: 4, overflow: 'hidden', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical' }}>
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

  return { nodes, edges };
}

// ── Node Library panel ────────────────────────────────────────────────────────
function NodeLibrary({ orchestrators, agents }: { orchestrators: OrchestratorFull[]; agents: Agent[] }) {
  const [openEP, setOpenEP] = useState(true);
  const [openOrch, setOpenOrch] = useState(true);
  const [openAgents, setOpenAgents] = useState(true);

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
        <span className="material-icons" style={{ fontSize: 14, color: C.textMuted, transition: 'transform 0.15s', transform: open ? 'rotate(180deg)' : 'none' }}>expand_more</span>
      </button>
    );
  }

  return (
    <div style={{
      width: 280, flexShrink: 0, height: '100%', overflowY: 'auto',
      ...glass, borderRight: `1px solid ${C.glassBorder}`, padding: '16px 14px',
      display: 'flex', flexDirection: 'column', gap: 20,
    }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', paddingBottom: 8, borderBottom: `1px solid ${C.outlineVariant}` }}>
        Node Library
      </div>

      {/* Entry Points */}
      <div>
        <SectionHeader label="Entry Points" open={openEP} onToggle={() => setOpenEP(v => !v)} />
        {openEP && (
          <div>
            {(['websocket', 'sse'] as const).map(ep => (
              <div key={ep}
                draggable
                onDragStart={e => dragItem(e, 'entryPoint', { epType: ep, label: ep === 'websocket' ? 'WebSocket' : 'SSE', accessMode: 'token', slug: '' })}
                style={{ ...itemStyle, background: C.cyanBg, borderColor: C.cyanBorder }}
                onMouseEnter={e => (e.currentTarget.style.background = 'rgba(0,240,255,0.1)')}
                onMouseLeave={e => (e.currentTarget.style.background = C.cyanBg)}
              >
                <span className="material-icons" style={{ fontSize: 18, color: C.cyan, flexShrink: 0 }}>
                  {ep === 'sse' ? 'stream' : 'settings_input_component'}
                </span>
                <div>
                  <div style={{ fontSize: 13, fontWeight: 600, color: C.text }}>{ep === 'websocket' ? 'WebSocket' : 'SSE'}</div>
                  <div style={{ fontSize: 11, color: C.textMuted }}>Entry point</div>
                </div>
                <span className="material-icons" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', opacity: 0.5 }}>drag_indicator</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Orchestrators */}
      <div>
        <SectionHeader label="Orchestrators" open={openOrch} onToggle={() => setOpenOrch(v => !v)} />
        {openOrch && (
          <div>
            {orchestrators.filter(o => o.enabled).map(o => (
              <div key={o.id}
                draggable
                onDragStart={e => dragItem(e, 'orchestrator', { orchestratorId: o.id, name: o.name, displayName: o.display_name, model: o.llm_model, maxParallelTools: o.max_parallel_tools })}
                style={{ ...itemStyle, background: C.purpleBg, borderColor: 'rgba(208,188,255,0.2)' }}
                onMouseEnter={e => (e.currentTarget.style.background = 'rgba(87,27,193,0.2)')}
                onMouseLeave={e => (e.currentTarget.style.background = C.purpleBg)}
              >
                <span className="material-icons" style={{ fontSize: 18, color: C.purple, flexShrink: 0 }}>hub</span>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{o.display_name}</div>
                  <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{o.name}</div>
                </div>
                <span className="material-icons" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Agents */}
      <div>
        <SectionHeader label="Agents" open={openAgents} onToggle={() => setOpenAgents(v => !v)} />
        {openAgents && (
          <div>
            {agents.filter(a => a.enabled).map(a => (
              <div key={a.id}
                draggable
                onDragStart={e => dragItem(e, 'agent', { agentId: a.id, name: a.slug, displayName: a.display_name, description: a.description, transport: a.transport, endpointUrl: a.endpoint_url })}
                style={{ ...itemStyle, background: C.greenBg, borderColor: C.greenBorder }}
                onMouseEnter={e => (e.currentTarget.style.background = 'rgba(74,222,128,0.1)')}
                onMouseLeave={e => (e.currentTarget.style.background = C.greenBg)}
              >
                <span className="material-icons" style={{ fontSize: 18, color: C.green, flexShrink: 0 }}>smart_toy</span>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.display_name}</div>
                  <div style={{ fontSize: 11, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.transport}</div>
                </div>
                <span className="material-icons" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// ── Properties Panel ──────────────────────────────────────────────────────────
function PropertiesPanel({
  selectedNode,
  onUpdateNode,
}: {
  selectedNode: Node | null;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
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

  const labelStyle: React.CSSProperties = { fontSize: 12, color: C.textMuted, marginBottom: 4, display: 'block' };
  const inputStyle: React.CSSProperties = {
    width: '100%', padding: '7px 10px', borderRadius: 6,
    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
    color: C.text, fontSize: 13, boxSizing: 'border-box', outline: 'none',
  };
  const readOnlyStyle: React.CSSProperties = { ...inputStyle, opacity: 0.6, cursor: 'default' };
  const fieldWrap: React.CSSProperties = { marginBottom: 14 };

  return (
    <div style={{
      width: 320, flexShrink: 0, height: '100%', overflowY: 'auto',
      ...glass, borderLeft: `1px solid ${C.glassBorder}`, padding: '16px 14px',
      display: 'flex', flexDirection: 'column',
    }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', paddingBottom: 8, borderBottom: `1px solid ${C.outlineVariant}`, marginBottom: 16 }}>
        Properties
      </div>

      {!selectedNode ? (
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: C.textMuted, padding: 20, textAlign: 'center' }}>
          <span className="material-icons" style={{ fontSize: 36, opacity: 0.3 }}>touch_app</span>
          <div style={{ fontSize: 13 }}>Select a node to configure it</div>
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
                  <label style={labelStyle}>Slug</label>
                  <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace' }} value={d.slug} onChange={e => onUpdateNode(selectedNode.id, { slug: e.target.value })} placeholder="my-app-slug" />
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
                <div style={fieldWrap}>
                  <label style={labelStyle}>Display Name</label>
                  <input style={readOnlyStyle} value={d.displayName} readOnly />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Slug</label>
                  <input style={{ ...readOnlyStyle, fontFamily: 'JetBrains Mono, monospace' }} value={d.name} readOnly />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Model</label>
                  <input style={{ ...readOnlyStyle, fontFamily: 'JetBrains Mono, monospace' }} value={d.model ?? '—'} readOnly />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Max Parallel Tools</label>
                  <input style={readOnlyStyle} value={d.maxParallelTools} readOnly />
                </div>
                <a href="/admin/orchestrators" style={{ fontSize: 12, color: C.cyan, textDecoration: 'none', display: 'flex', alignItems: 'center', gap: 4, marginTop: 8 }}>
                  Configure in Orchestrators <span className="material-icons" style={{ fontSize: 14 }}>arrow_forward</span>
                </a>
              </div>
            );
          })()}

          {/* Agent properties */}
          {selectedNode.type === 'agent' && propTab === 'properties' && (() => {
            const d = selectedNode.data as AgentData;
            return (
              <div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Display Name</label>
                  <input style={readOnlyStyle} value={d.displayName} readOnly />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Description</label>
                  <textarea style={{ ...readOnlyStyle, resize: 'none', height: 70 }} value={d.description} readOnly />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Endpoint URL</label>
                  <input style={{ ...readOnlyStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }} value={d.endpointUrl} readOnly />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Transport</label>
                  <span style={{ display: 'inline-block', padding: '3px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: C.greenBg, color: C.green, border: `1px solid ${C.greenBorder}` }}>{d.transport}</span>
                </div>
                <a href="/admin/agents" style={{ fontSize: 12, color: C.green, textDecoration: 'none', display: 'flex', alignItems: 'center', gap: 4, marginTop: 8 }}>
                  Configure in Agents <span className="material-icons" style={{ fontSize: 14 }}>arrow_forward</span>
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

// ── Canvas inner (needs ReactFlow context) ────────────────────────────────────
function CanvasInner({
  nodes, edges, onNodesChange, onEdgesChange, onConnect, onDrop, onDragOver, selectedNode, setSelectedNode, onUpdateNode,
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
}) {
  const { fitView, zoomIn, zoomOut, getZoom } = useReactFlow();

  return (
    <div style={{ flex: 1, position: 'relative', height: '100%' }}>
      {/* Canvas toolbar */}
      <div style={{
        position: 'absolute', top: 14, left: '50%', transform: 'translateX(-50%)',
        zIndex: 10, display: 'flex', alignItems: 'center', gap: 4,
        ...glass, borderRadius: 10, padding: '5px 10px',
      }}>
        <button onClick={() => zoomOut()} style={{ ...toolBtnStyle }}>
          <span className="material-icons" style={{ fontSize: 16 }}>remove</span>
        </button>
        <span style={{ fontSize: 12, color: C.textMuted, minWidth: 44, textAlign: 'center', fontFamily: 'JetBrains Mono, monospace' }}>
          {Math.round(getZoom() * 100)}%
        </span>
        <button onClick={() => zoomIn()} style={{ ...toolBtnStyle }}>
          <span className="material-icons" style={{ fontSize: 16 }}>add</span>
        </button>
        <div style={{ width: 1, height: 18, background: C.outlineVariant, margin: '0 4px' }} />
        <button onClick={() => fitView({ padding: 0.15 })} style={{ ...toolBtnStyle }}>
          <span className="material-icons" style={{ fontSize: 16 }}>fit_screen</span>
        </button>
      </div>

      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onDrop={onDrop}
        onDragOver={onDragOver}
        nodeTypes={NODE_TYPES}
        onNodeClick={(_evt: React.MouseEvent, node: Node) => setSelectedNode(node)}
        onPaneClick={() => setSelectedNode(null)}
        fitView
        fitViewOptions={{ padding: 0.15 }}
        style={{ background: C.bg }}
        defaultEdgeOptions={{ animated: true, style: EDGE_STYLE }}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} color="rgba(132,148,149,0.15)" gap={22} size={1} />
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
  const rfWrapper = useRef<HTMLDivElement>(null);
  const { screenToFlowPosition } = useReactFlow();

  function showToast(msg: string, ok: boolean) {
    setToast({ msg, ok });
    setTimeout(() => setToast(null), 3000);
  }

  const onConnect = useCallback((c: Connection) => {
    setEdges((eds: Edge[]) => addEdge({ ...c, animated: true, style: EDGE_STYLE }, eds));
  }, [setEdges]);

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

    const position = screenToFlowPosition({ x: e.clientX, y: e.clientY });
    const newNode: Node = { id: makeId(), type: nodeType, position, data: nodeData };
    setNodes((nds: Node[]) => [...nds, newNode]);
  }

  function updateNodeData(id: string, partialData: Record<string, unknown>) {
    setNodes((nds: Node[]) => nds.map((n: Node) => n.id === id ? { ...n, data: { ...n.data, ...partialData } } : n));
    setSelectedNode((prev: Node | null) => prev && prev.id === id ? { ...prev, data: { ...prev.data, ...partialData } } : prev);
  }

  async function handleSave(deploy = false) {
    // Find entry point
    const epNode = nodes.find((n: Node) => n.type === 'entryPoint');
    if (!epNode) { showToast('Add an Entry Point node first', false); return; }
    const epData = epNode.data as EntryPointData;
    if (!epData.slug) { showToast('Entry point needs a slug', false); return; }

    // Find orchestrator connected to entry point
    const orchEdge = edges.find((e: Edge) => e.source === epNode.id);
    const orchNode = orchEdge ? nodes.find((n: Node) => n.id === orchEdge.target && n.type === 'orchestrator') : undefined;
    if (!orchNode) { showToast('Connect an Orchestrator to the Entry Point', false); return; }
    const orchData = orchNode.data as OrchestratorData;

    // Find agents connected to orchestrator
    const agentEdges = edges.filter((e: Edge) => e.source === orchNode.id);
    const agentIds = agentEdges
      .map((e: Edge) => nodes.find((n: Node) => n.id === e.target && n.type === 'agent'))
      .filter((n: Node | undefined): n is Node => Boolean(n))
      .map((n: Node) => (n.data as AgentData).agentId);

    setSaving(true);
    try {
      // Update orchestrator's allowed_agent_ids
      if (agentIds.length > 0) {
        await themApi.updateOrchestrator(orchData.orchestratorId, { allowed_agent_ids: agentIds });
      }

      const body = {
        name: epData.label || epData.slug,
        slug: epData.slug,
        entry_point_type: epData.epType,
        orchestrator_id: orchData.orchestratorId,
        access_policy: { mode: epData.accessMode },
        enabled: true,
      };

      if (app?.id) {
        await themApi.updateApplication(app.id, body);
      } else {
        await themApi.createApplication(body);
      }
      showToast(deploy ? 'Application deployed!' : 'Saved successfully', true);
      onSaved();
    } catch (err: any) {
      showToast(err?.message ?? 'Save failed', false);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg }}>
      {/* Top bar */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 16, padding: '0 24px', height: 56, flexShrink: 0,
        ...glass, borderBottom: `1px solid ${C.glassBorder}`, zIndex: 20,
      }}>
        <button onClick={onBack} style={{ ...toolBtnStyle, padding: '6px 10px', color: C.textMuted }}>
          <span className="material-icons" style={{ fontSize: 18 }}>arrow_back</span>
        </button>
        <div style={{ width: 1, height: 20, background: C.outlineVariant }} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 700, color: C.text, fontFamily: 'Geist, sans-serif' }}>
            {app ? app.name : 'New Application'}
          </div>
          {app && <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{app.slug}</div>}
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
            disabled={saving}
            style={{
              padding: '7px 18px', borderRadius: 8, border: 'none',
              background: C.cyan, color: '#00363a', cursor: 'pointer', fontSize: 13, fontWeight: 700,
              opacity: saving ? 0.6 : 1, boxShadow: `0 0 12px rgba(0,240,255,0.25)`,
            }}
          >
            Deploy
          </button>
        </div>
      </div>

      {/* Builder area */}
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }} ref={rfWrapper}>
        <NodeLibrary orchestrators={orchestrators} agents={agents} />

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
          />
        </div>

        <PropertiesPanel selectedNode={selectedNode} onUpdateNode={updateNodeData} />
      </div>

      {/* Status bar */}
      <div style={{
        height: 28, display: 'flex', alignItems: 'center', gap: 16, padding: '0 16px',
        ...glass, borderTop: `1px solid ${C.glassBorder}`, fontSize: 11, color: C.textMuted, flexShrink: 0,
      }}>
        <span>Nodes: {nodes.length}</span>
        <span>·</span>
        <span>Edges: {edges.length}</span>
        {nodes.some((n: Node) => n.type === 'entryPoint') && <><span>·</span><span style={{ color: C.cyan }}>Entry point wired</span></>}
        {nodes.some((n: Node) => n.type === 'orchestrator') && <><span>·</span><span style={{ color: C.purple }}>Orchestrator connected</span></>}
        {nodes.filter((n: Node) => n.type === 'agent').length > 0 && <><span>·</span><span style={{ color: C.green }}>{nodes.filter((n: Node) => n.type === 'agent').length} agent(s)</span></>}
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
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);

  function copy(val: string, id: string) {
    navigator.clipboard?.writeText(val).catch(() => {});
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 1800);
  }

  const EP_ICON: Record<string, string> = { websocket: 'settings_input_component', sse: 'stream', webrtc: 'videocam' };
  const EP_LABEL: Record<string, string> = { websocket: 'WebSocket', sse: 'SSE', webrtc: 'WebRTC' };

  return (
    <div style={{ marginLeft: 260, minHeight: '100vh', background: C.bg, padding: '36px 48px' }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 32 }}>
        <div>
          <h1 style={{ fontSize: 26, fontWeight: 700, color: C.text, margin: 0, fontFamily: 'Geist, sans-serif', letterSpacing: -0.5 }}>
            Applications
          </h1>
          <p style={{ fontSize: 13, color: C.textMuted, margin: '6px 0 0' }}>
            Compose orchestrators and entry points into deployable agentic applications.
          </p>
        </div>
        <button
          onClick={onNew}
          style={{
            padding: '10px 22px', borderRadius: 10, border: 'none', cursor: 'pointer',
            background: C.cyan, color: '#00363a', fontWeight: 700, fontSize: 14,
            boxShadow: `0 0 14px rgba(0,240,255,0.3)`, display: 'flex', alignItems: 'center', gap: 6,
          }}
        >
          <span className="material-icons" style={{ fontSize: 18 }}>add</span>
          New Application
        </button>
      </div>

      {loading ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, color: C.textMuted, padding: '40px 0' }}>
          <span className="material-icons" style={{ fontSize: 18, animation: 'spin 1s linear infinite' }}>autorenew</span>
          Loading…
        </div>
      ) : list.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '80px 0', color: C.textMuted }}>
          <span className="material-icons" style={{ fontSize: 56, marginBottom: 16, opacity: 0.25, display: 'block' }}>apps</span>
          <div style={{ fontSize: 16, fontWeight: 600, marginBottom: 8, color: C.text }}>No applications yet</div>
          <div style={{ fontSize: 13 }}>Create one to expose an orchestrator as a shareable endpoint.</div>
          <button onClick={onNew} style={{ marginTop: 24, padding: '10px 22px', borderRadius: 10, border: 'none', cursor: 'pointer', background: C.cyan, color: '#00363a', fontWeight: 700, fontSize: 14, boxShadow: `0 0 14px rgba(0,240,255,0.3)` }}>
            + New Application
          </button>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          {list.map(app => (
            <div key={app.id} style={{
              ...glass, borderRadius: 14, overflow: 'hidden',
              borderLeft: `3px solid ${app.enabled ? C.cyan : C.outlineVariant}`,
              transition: 'box-shadow 0.2s',
            }}>
              {/* Main row */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 18, padding: '18px 22px' }}>
                <div style={{
                  width: 42, height: 42, borderRadius: 10, flexShrink: 0,
                  background: `rgba(0,240,255,0.08)`, border: `1px solid ${C.cyanBorder}`,
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                }}>
                  <span className="material-icons" style={{ fontSize: 20, color: C.cyan }}>
                    {EP_ICON[app.entry_point_type] ?? 'extension'}
                  </span>
                </div>

                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                    <span style={{ fontWeight: 700, fontSize: 16, color: C.text, fontFamily: 'Geist, sans-serif' }}>{app.name}</span>
                    <code style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', padding: '2px 7px', background: C.surfaceLow, borderRadius: 5 }}>{app.slug}</code>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 9px', borderRadius: 20, fontSize: 11, fontWeight: 700, background: 'rgba(0,240,255,0.08)', color: C.cyan, border: `1px solid rgba(0,240,255,0.2)` }}>
                      <span className="material-icons" style={{ fontSize: 12 }}>{EP_ICON[app.entry_point_type] ?? 'extension'}</span>
                      {EP_LABEL[app.entry_point_type] ?? app.entry_point_type}
                    </span>
                    <span style={{
                      padding: '2px 9px', borderRadius: 20, fontSize: 11, fontWeight: 700,
                      background: app.enabled ? 'rgba(74,222,128,0.1)' : 'rgba(255,180,171,0.1)',
                      color: app.enabled ? C.green : C.error,
                      border: `1px solid ${app.enabled ? C.greenBorder : 'rgba(255,180,171,0.3)'}`,
                      boxShadow: app.enabled ? `0 0 8px rgba(74,222,128,0.15)` : 'none',
                    }}>
                      {app.enabled ? 'enabled' : 'disabled'}
                    </span>
                  </div>
                  {app.orchestrator_name && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 6 }}>
                      <span className="material-icons" style={{ fontSize: 13, color: C.purple }}>hub</span>
                      <span style={{ fontSize: 12, color: C.textMuted }}>{app.orchestrator_name}</span>
                      <span style={{ fontSize: 12, color: C.outlineVariant }}>·</span>
                      <span style={{ fontSize: 12, color: C.textMuted }}>access: {(app.access_policy as any)?.mode ?? 'token'}</span>
                    </div>
                  )}
                </div>

                <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
                  <button
                    onClick={() => setExpandedId(expandedId === app.id ? null : app.id)}
                    style={{ ...ghostBtn, color: C.textMuted }}
                  >
                    <span className="material-icons" style={{ fontSize: 14 }}>link</span>
                    URLs
                  </button>
                  <button
                    onClick={() => onToggle(app)}
                    style={{ ...ghostBtn, color: app.enabled ? C.error : C.green }}
                  >
                    {app.enabled ? 'Disable' : 'Enable'}
                  </button>
                  <button
                    onClick={() => onEdit(app)}
                    style={{ ...ghostBtn, color: C.cyan, borderColor: C.cyanBorder, background: 'rgba(0,240,255,0.05)' }}
                  >
                    <span className="material-icons" style={{ fontSize: 14 }}>edit</span>
                    Open Builder
                  </button>
                  <button
                    onClick={() => onDelete(app)}
                    style={{ ...ghostBtn, color: C.error, borderColor: 'rgba(255,180,171,0.3)', background: C.errorBg }}
                  >
                    <span className="material-icons" style={{ fontSize: 14 }}>delete</span>
                  </button>
                </div>
              </div>

              {/* Expanded URLs */}
              {expandedId === app.id && (
                <div style={{ borderTop: `1px solid ${C.glassBorder}`, padding: '16px 22px', background: C.surfaceLow }}>
                  <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 12 }}>
                    Entry Point URLs
                  </div>
                  {(() => {
                    const urls: Array<{ label: string; val: string }> = [];
                    if (app.entry_point_type === 'websocket') urls.push({ label: 'WebSocket', val: `ws://<host>:8088/apps/${app.slug}/ws` });
                    if (app.entry_point_type === 'sse') urls.push({ label: 'SSE', val: `http://<host>:8088/apps/${app.slug}/sse` }, { label: 'REST', val: `http://<host>:8088/apps/${app.slug}` });
                    if (app.entry_point_type === 'webrtc') urls.push({ label: 'WebRTC (coming soon)', val: `ws://<host>:8088/apps/${app.slug}/ws` });
                    return urls.map(({ label, val }) => {
                      const cid = `${app.id}_${label}`;
                      return (
                        <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
                          <span style={{ fontSize: 11, color: C.textMuted, minWidth: 100 }}>{label}</span>
                          <code style={{ flex: 1, fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: C.text, background: C.surfaceContainer, padding: '4px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}` }}>{val}</code>
                          <button onClick={() => copy(val, cid)} style={{ ...ghostBtn, fontSize: 11, padding: '4px 12px', color: copiedId === cid ? C.green : C.textMuted }}>
                            {copiedId === cid ? 'Copied!' : 'Copy'}
                          </button>
                        </div>
                      );
                    });
                  })()}
                  <div style={{ marginTop: 10, fontSize: 11, color: C.textMuted }}>
                    {(app.access_policy as any)?.mode === 'public'
                      ? 'No auth required — public access'
                      : 'Bearer token required — use /admin/tokens to create one'}
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

const ghostBtn: React.CSSProperties = {
  display: 'inline-flex', alignItems: 'center', gap: 5,
  padding: '6px 14px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`,
  background: 'transparent', cursor: 'pointer', fontSize: 12, fontWeight: 600,
  color: C.textMuted, transition: 'all 0.15s',
};

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
          <div style={{ flex: 1 }}>
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
