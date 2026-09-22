'use client';
import { useState } from 'react';
import type { AppDefinition, ComponentDefinitionSummary } from '@/lib/api';
import type { NodeDef } from '@/lib/nodeRegistry';
import { C } from '../constants';

// ── CanvasPalette ─────────────────────────────────────────────────────────────
// Extracted from CanvasBuilderView.tsx (which was over the 400-line guideline)
// per docs/APP_CANVAS_EXPORT_IMPORT_PLAN.md's file-size cleanup. Pure
// presentation + drag-start wiring — all state (componentDefs, palette lists,
// panel width) is owned by CanvasBuilderView and passed down as props.

const EP_MS_ICON_MAP: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };

export function CanvasPalette({
  compPanelWidth,
  onStartResize,
  componentDefs,
  agentIconBySlug,
  flowControlPalette,
  inlinePalette,
  activeDef,
  onNewDraft,
}: {
  compPanelWidth: number;
  onStartResize: (e: React.MouseEvent) => void;
  componentDefs: ComponentDefinitionSummary[];
  agentIconBySlug: Map<string, string>;
  flowControlPalette: NodeDef[];
  inlinePalette: NodeDef[];
  activeDef: AppDefinition | null;
  onNewDraft: () => void;
}) {
  const [agentSearch, setAgentSearch] = useState('');
  return (
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

        {/* Flow Control nodes — driven by GET /admin/node-types (appflow family) */}
        <div style={{ padding: '0 8px 12px' }}>
          <div style={{ fontSize: 11, color: C.textMuted, padding: '4px 8px', fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.5 }}>Flow Control</div>
          {flowControlPalette.map(fc => (
            <div
              key={fc.type}
              draggable
              onDragStart={e => { e.dataTransfer.setData('nodeType', 'flow_control'); e.dataTransfer.setData('nodeData', JSON.stringify({ node_type: fc.type })); e.dataTransfer.effectAllowed = 'move'; }}
              style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: 8, cursor: 'grab', marginBottom: 2, background: `${fc.border}0a`, border: `1px solid ${fc.border}1f` }}
            >
              <div style={{ fontSize: 16, lineHeight: 1, flexShrink: 0 }}>{fc.emoji}</div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 12, color: C.text, fontWeight: 500 }}>{fc.label}</div>
                <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{fc.description}</div>
              </div>
            </div>
          ))}
        </div>

        {/* Inline / Logic nodes — driven by GET /admin/node-types (appflow family) */}
        <div style={{ padding: '0 8px 12px' }}>
          <div style={{ fontSize: 11, color: C.textMuted, padding: '4px 8px', fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.5 }}>Inline / Logic</div>
          {inlinePalette.map(fc => (
            <div
              key={fc.type}
              draggable
              onDragStart={e => { e.dataTransfer.setData('nodeType', 'inline'); e.dataTransfer.setData('nodeData', JSON.stringify({ node_type: fc.type })); e.dataTransfer.effectAllowed = 'move'; }}
              style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: 8, cursor: 'grab', marginBottom: 2, background: `${fc.border}0a`, border: `1px solid ${fc.border}1f` }}
            >
              <div style={{ fontSize: 16, lineHeight: 1, flexShrink: 0 }}>{fc.emoji}</div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 12, color: C.text, fontWeight: 500 }}>{fc.label}</div>
                <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{fc.description}</div>
              </div>
            </div>
          ))}
        </div>

        {/* Middleware — component kind */}
        {(['middleware'] as const).map(kind => {
          const items = componentDefs.filter(cd => cd.kind === kind);
          if (items.length === 0) return null;
          const kindColor = '245,158,11';
          const kindIconColor = '#f59e0b';
          const defaultKindIcon = 'shield';
          return (
            <div key={kind} style={{ padding: '0 8px 12px' }}>
              <div style={{ fontSize: 11, color: C.textMuted, padding: '4px 8px', fontWeight: 600, textTransform: 'capitalize' }}>{kind}s</div>
              {items.map(cd => {
                const itemIcon = cd.name.includes('guard') ? 'shield' : 'bolt';
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

        {/* Orchestrators — component kind */}
        {(['orchestrator'] as const).map(kind => {
          const items = componentDefs.filter(cd => cd.kind === kind);
          if (items.length === 0) return null;
          const kindColor = '99,102,241';
          const kindIconColor = '#818cf8';
          const defaultKindIcon = 'hub';
          return (
            <div key={kind} style={{ padding: '0 8px 12px' }}>
              <div style={{ fontSize: 11, color: C.textMuted, padding: '4px 8px', fontWeight: 600, textTransform: 'capitalize' }}>{kind}s</div>
              {items.map(cd => (
                <div
                  key={cd.id}
                  draggable
                  onDragStart={e => { e.dataTransfer.setData('nodeType', kind); e.dataTransfer.setData('nodeData', JSON.stringify({ cd })); e.dataTransfer.effectAllowed = 'move'; }}
                  style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: 8, cursor: 'grab', marginBottom: 2, background: `rgba(${kindColor},0.04)`, border: `1px solid rgba(${kindColor},0.12)` }}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 16, color: kindIconColor }}>{defaultKindIcon}</span>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 12, color: C.text, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cd.display_name}</div>
                    {cd.description && <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cd.description}</div>}
                  </div>
                </div>
              ))}
            </div>
          );
        })}

        {/* Agents — searchable + scrollable since this list can grow large */}
        {(() => {
          const agents = componentDefs.filter(cd => cd.kind === 'agent');
          if (agents.length === 0) return null;
          const filtered = agentSearch.trim()
            ? agents.filter(cd =>
                cd.display_name.toLowerCase().includes(agentSearch.trim().toLowerCase()) ||
                cd.name.toLowerCase().includes(agentSearch.trim().toLowerCase()))
            : agents;
          return (
            <div style={{ padding: '0 8px 12px' }}>
              <div style={{ fontSize: 11, color: C.textMuted, padding: '4px 8px', fontWeight: 600, textTransform: 'capitalize' }}>Agents</div>
              <div style={{ padding: '0 8px 6px' }}>
                <input
                  type="text"
                  value={agentSearch}
                  onChange={e => setAgentSearch(e.target.value)}
                  placeholder="Search agents…"
                  style={{
                    width: '100%', boxSizing: 'border-box', padding: '6px 8px', borderRadius: 6,
                    border: '1px solid rgba(255,255,255,0.1)', background: 'rgba(0,0,0,0.25)',
                    color: C.text, fontSize: 12, outline: 'none',
                  }}
                />
              </div>
              <div style={{ maxHeight: 260, overflowY: 'auto' }}>
                {filtered.length === 0 && (
                  <div style={{ fontSize: 11, color: C.textMuted, padding: '4px 10px' }}>No agents match &quot;{agentSearch}&quot;</div>
                )}
                {filtered.map(cd => {
                  const itemIcon = agentIconBySlug.get(cd.name) ?? 'smart_toy';
                  return (
                    <div
                      key={cd.id}
                      draggable
                      onDragStart={e => { e.dataTransfer.setData('nodeType', 'agent'); e.dataTransfer.setData('nodeData', JSON.stringify({ cd })); e.dataTransfer.effectAllowed = 'move'; }}
                      style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: 8, cursor: 'grab', marginBottom: 2, background: 'rgba(74,222,128,0.04)', border: '1px solid rgba(74,222,128,0.12)' }}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 16, color: C.green }}>{itemIcon}</span>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: C.text, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cd.display_name}</div>
                        {cd.description && <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cd.description}</div>}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })()}

        {!activeDef && (
          <div style={{ padding: '20px 16px', textAlign: 'center' }}>
            <div style={{ fontSize: 12, color: C.textMuted, marginBottom: 10 }}>No definition loaded</div>
            <button onClick={onNewDraft} style={{ padding: '8px 14px', borderRadius: 8, border: 'none', background: C.cyan, color: '#021520', fontWeight: 700, cursor: 'pointer', fontSize: 12 }}>
              Create First Definition
            </button>
          </div>
        )}
      </div>
      {/* Resize grip */}
      <div
        onMouseDown={onStartResize}
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
  );
}
