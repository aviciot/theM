import React, { useEffect, useState, useCallback } from 'react';
import { Handle, Position, useUpdateNodeInternals, useStore } from '@xyflow/react';
import { getNodeDef, resolveOutputPorts } from '@/lib/nodeRegistry';
import type { ResolvedPort } from '@/lib/nodeRegistry';
import type { StepNodeData, DebugNodeState } from '../types';
import { useLayoutDir } from '../LayoutContext';
import { PortsPopover } from './PortsPopover';

function stepMetaFromType(type: string): { bg: string; border: string; emoji: string; label: string } {
  const def = getNodeDef(type);
  return { bg: def.bg, border: def.border, emoji: def.emoji, label: def.label };
}

const debugBorder: Record<DebugNodeState, string> = {
  idle: 'transparent', pending: '#f59e0b', running: '#60a5fa', done: '#4ade80', error: '#f87171',
};
const debugGlow: Record<DebugNodeState, string> = {
  idle:    'none',
  pending: '0 0 8px 2px rgba(245,158,11,0.5)',
  running: '0 0 8px 2px rgba(96,165,250,0.5)',
  done:    '0 0 8px 2px rgba(74,222,128,0.4)',
  error:   '0 0 8px 2px rgba(248,113,113,0.5)',
};

const PORT_STEP  = 22;
const PORT_START = 28;

// Flow port — gray arrow, always first in list
const FLOW_COLOR  = '#64748b';
// Data port colors — cycled by index
const DATA_COLORS = ['#818cf8', '#a78bfa', '#7dd3fc', '#6ee7b7', '#fcd34d', '#f9a8d4'];

// Inject global styles once — kills RF default node background
const GLOBAL_CSS = `
.react-flow__node { background: transparent !important; border: none !important; padding: 0 !important; box-shadow: none !important; }
`;
if (typeof document !== 'undefined' && !document.getElementById('sn-global-css')) {
  const s = document.createElement('style');
  s.id = 'sn-global-css';
  s.textContent = GLOBAL_CSS;
  document.head.appendChild(s);
}

// ── Unified port model ────────────────────────────────────────────────────────
// All ports — control flow AND data — are in one list per side.
// Flow port is always first, labeled "→", gray.
// Data ports follow, colored, labeled with var name.

interface UnifiedPort extends ResolvedPort {
  handleID: string; // the RF Handle id
}

function buildInputPorts(
  nodeDef: ReturnType<typeof getNodeDef>,
  allInputIDs: string[],   // committed + ghost
  wiredInIDs: string[],    // for dedup
): UnifiedPort[] {
  const ports: UnifiedPort[] = [];

  // Flow input port — present on every non-source node
  if (!nodeDef.is_source) {
    ports.push({
      handleID: 'ctrl-in',
      id: 'ctrl-in',
      label: '→',
      kind: 'control',
      direction: 'in',
      color: FLOW_COLOR,
      required: nodeDef.edges.min_in > 0,
      maxConnections: nodeDef.edges.max_in || 0,
    });
  }

  // Data input ports — deduplicated
  const emitted = new Set<string>();

  // Static registry ports first (better metadata), only if wired
  const wiredSet = new Set(wiredInIDs);
  for (const port of nodeDef.input_ports ?? []) {
    if (!wiredSet.has(port.id)) continue;
    const hid = `data-in-${port.id}`;
    emitted.add(hid);
    ports.push({
      handleID: hid,
      id: hid,
      label: port.label || port.id,
      kind: 'data',
      direction: 'in',
      color: port.color || DATA_COLORS[emitted.size % DATA_COLORS.length],
      required: port.required ?? false,
      maxConnections: port.max_connections ?? 1,
    });
  }

  // Dynamic user-dragged ports
  for (const portID of allInputIDs) {
    const hid = `data-in-${portID}`;
    if (emitted.has(hid)) continue;
    emitted.add(hid);
    const colorIdx = emitted.size - 1;
    ports.push({
      handleID: hid,
      id: hid,
      label: portID,
      kind: 'data',
      direction: 'in',
      color: DATA_COLORS[colorIdx % DATA_COLORS.length],
      required: false,
      maxConnections: 1,
    });
  }

  return ports;
}

function buildOutputPorts(
  nodeDef: ReturnType<typeof getNodeDef>,
  cfg: Record<string, unknown>,
): UnifiedPort[] {
  const ports: UnifiedPort[] = [];

  // Named control outputs (branch: true/false) — these ARE the flow ports
  const ctrlPorts = nodeDef.control_output_ports ?? [];
  if (ctrlPorts.length > 0) {
    for (const p of ctrlPorts) {
      ports.push({
        handleID: `ctrl-out-${p.id}`,
        id: `ctrl-out-${p.id}`,
        label: p.label || p.id,
        kind: 'control',
        direction: 'out',
        color: p.color || FLOW_COLOR,
        required: p.required ?? false,
        maxConnections: p.max_connections ?? 1,
      });
    }
  } else if (!nodeDef.is_sink) {
    // Single anonymous flow output
    ports.push({
      handleID: 'ctrl-out',
      id: 'ctrl-out',
      label: '→',
      kind: 'control',
      direction: 'out',
      color: FLOW_COLOR,
      required: nodeDef.edges.min_out > 0,
      maxConnections: 0,
    });
  }

  // Dynamic data outputs (transform: functions[].output_var) — always show
  const resolvedOut = resolveOutputPorts(nodeDef, cfg);
  for (const p of resolvedOut) {
    if (p.kind !== 'data') continue;
    ports.push({ ...p, handleID: p.id });
  }

  return ports;
}

export function StepNode({ id, data }: { id: string; data: StepNodeData }) {
  const nodeDef   = getNodeDef(data.step_type);
  const meta      = { bg: nodeDef.bg, border: nodeDef.border, emoji: nodeDef.emoji, label: nodeDef.label };
  const cfg       = data.config ?? {};
  const layoutDir = useLayoutDir();
  const isLR      = layoutDir === 'LR';
  const targetPos = isLR ? Position.Left  : Position.Top;
  const sourcePos = isLR ? Position.Right : Position.Bottom;
  const dbg       = data._debug;
  const sub       = nodeDef.summary(cfg);
  const updateNodeInternals = useUpdateNodeInternals();

  const [hovered, setHovered] = useState(false);
  const [showPortsPanel, setShowPortsPanel] = useState(false);
  const onMouseEnter = useCallback(() => setHovered(true),  []);
  const onMouseLeave = useCallback(() => setHovered(false), []);

  // Live edges from RF store
  const edges = useStore(s => s.edges);

  const hasCtrlIn  = edges.some(e => e.target === id &&
    (e.targetHandle === 'ctrl-in' || !e.targetHandle?.startsWith('data')));
  const hasCtrlOut = edges.some(e => e.source === id &&
    (e.sourceHandle === 'ctrl-out' || e.sourceHandle?.startsWith('ctrl-out')));

  const wiredOutIDs = edges
    .filter(e => e.source === id && e.sourceHandle?.startsWith('data-out-'))
    .map(e => e.sourceHandle!.replace('data-out-', ''));
  const wiredInIDs = edges
    .filter(e => e.target === id && e.targetHandle?.startsWith('data-in-'))
    .map(e => e.targetHandle!.replace('data-in-', ''));

  // Ghost port during drag-hover
  const dynamicInputIDs: string[] = data.inputs ? Object.keys(data.inputs) : [];
  const ghostVar   = data._draggingVar;
  const dragAccept = data._dragAccept;
  const allInputIDs = ghostVar && dragAccept === 'accept'
    ? [...dynamicInputIDs, ghostVar]
    : dynamicInputIDs;

  // Build unified port lists
  const inputPorts  = buildInputPorts(nodeDef, allInputIDs, wiredInIDs);
  const outputPorts = buildOutputPorts(nodeDef, cfg as Record<string, unknown>);

  // Notify RF when handle list changes
  const portKey = [...inputPorts.map(p => p.handleID), ...outputPorts.map(p => p.handleID)].join(',');
  useEffect(() => { updateNodeInternals(id); }, [portKey, id, updateNodeInternals]);

  // ── Visual state ──────────────────────────────────────────────────────────
  const state = dbg?.state ?? 'idle';
  let borderColor = meta.border;
  let boxShadow   = 'none';
  let animation   = 'none';

  if (state !== 'idle') {
    borderColor = debugBorder[state];
    boxShadow   = debugGlow[state];
  } else if (data._validation === 'error') {
    borderColor = '#f87171';
    boxShadow   = '0 0 8px 2px rgba(248,113,113,0.45)';
  } else if (data._validation === 'warning' || data._stub) {
    borderColor = '#f59e0b';
    boxShadow   = '0 0 6px 1px rgba(245,158,11,0.35)';
  } else if (dragAccept === 'accept') {
    borderColor = '#4ade80';
    boxShadow   = '0 0 0 3px rgba(74,222,128,0.5), 0 0 16px 4px rgba(74,222,128,0.3)';
  } else if (dragAccept === 'reject') {
    borderColor = '#f87171';
    boxShadow   = '0 0 0 3px rgba(248,113,113,0.5), 0 0 12px 3px rgba(248,113,113,0.3)';
  } else if (hovered) {
    boxShadow = `0 0 0 1px ${meta.border}, 0 0 12px 3px ${meta.border}44`;
  }

  // ── Geometry ──────────────────────────────────────────────────────────────
  function dotOffset(idx: number) { return PORT_START + idx * PORT_STEP; }

  const invisHandle: React.CSSProperties = {
    width: 1, height: 1, opacity: 0, border: 'none', background: 'transparent',
  };

  // Flow (ctrl) handles — always-visible chevron arrows on card edge
  function ctrlHandleStyle(side: 'in' | 'out'): React.CSSProperties {
    const size = 18;
    const base: React.CSSProperties = {
      width: size, height: size,
      borderRadius: '50%',
      background: hovered ? '#334155' : '#1e293b',
      border: `2px solid ${hovered ? '#64748b' : '#334155'}`,
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      cursor: side === 'out' ? 'crosshair' : 'default',
      zIndex: 5,
      transition: 'background 0.15s, border-color 0.15s',
      boxSizing: 'border-box',
    };
    const half = size / 2;
    if (isLR) {
      return side === 'in'
        ? { ...base, left: -half, top: '50%', transform: 'translateY(-50%)' }
        : { ...base, right: -half, top: '50%', transform: 'translateY(-50%)' };
    }
    return side === 'in'
      ? { ...base, top: -half, left: '50%', transform: 'translateX(-50%)' }
      : { ...base, bottom: -half, left: '50%', transform: 'translateX(-50%)' };
  }

  function inputHandleStyle(idx: number): React.CSSProperties {
    const off = dotOffset(idx);
    return isLR
      ? { ...invisHandle, top: off, left: 0 }
      : { ...invisHandle, left: off, top: 0 };
  }
  function outputHandleStyle(idx: number): React.CSSProperties {
    const off = dotOffset(idx);
    return isLR
      ? { ...invisHandle, top: off, right: 0 }
      : { ...invisHandle, left: off, bottom: 0 };
  }

  // Card sizing — stable, based on max port count
  const maxPorts  = Math.max(inputPorts.length, outputPorts.length);
  const railH     = maxPorts > 0 ? PORT_START + maxPorts * PORT_STEP + PORT_START : 0;
  const cardH     = Math.max(80, railH);

  const leftPad  = isLR  ? 21 : 14;
  const rightPad = isLR  ? 21 : 14;
  const topPad   = !isLR ? 21 : 12;
  const botPad   = !isLR ? 21 : 12;

  return (
    <div
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
      style={{
        background: 'transparent',
        minWidth: 90,
        minHeight: cardH,
        position: 'relative',
        paddingTop:    topPad,
        paddingBottom: botPad,
        paddingLeft:   leftPad,
        paddingRight:  rightPad,
        overflow: 'visible',
        zIndex: showPortsPanel ? 10 : undefined,
      }}
    >
      {/* ── All INPUT handles ── */}
      {inputPorts.map((port, idx) => (
        <React.Fragment key={port.handleID}>
          <Handle
            id={port.handleID}
            type="target"
            position={targetPos}
            style={port.kind === 'control' ? ctrlHandleStyle('in') : inputHandleStyle(idx)}
          />
          {port.kind === 'control' && (
            <div style={{
              ...ctrlHandleStyle('in'),
              position: 'absolute',
              pointerEvents: 'none',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontSize: 10, color: '#64748b', fontWeight: 700, lineHeight: 1,
              userSelect: 'none',
            }}>
              {isLR ? '‹' : '∧'}
            </div>
          )}
        </React.Fragment>
      ))}

      {/* ── All OUTPUT handles ── */}
      {(() => {
        const ctrlOutPorts = outputPorts.filter(p => p.kind === 'control');
        const dataOutPorts = outputPorts.filter(p => p.kind === 'data');
        const multiCtrl = ctrlOutPorts.length > 1;

        return (
          <>
            {ctrlOutPorts.map((port, idx) => {
              const baseStyle = ctrlHandleStyle('out');
              // For multi ctrl-out: build a clean style without duplicate top/transform keys
              const portStyle: React.CSSProperties = multiCtrl
                ? (() => {
                    const { top: _t, transform: _tr, ...rest } = baseStyle;
                    void _t; void _tr;
                    return isLR
                      ? { ...rest, top: `${33 + idx * 34}%`, transform: 'translateY(-50%)' }
                      : { ...rest, left: `${33 + idx * 34}%`, transform: 'translateX(-50%)' };
                  })()
                : baseStyle;

              const portLabel = multiCtrl
                ? port.label.charAt(0).toUpperCase()
                : (isLR ? '›' : '∨');

              const labelColor = multiCtrl
                ? (port.color || (hovered ? '#94a3b8' : '#64748b'))
                : (hovered ? '#94a3b8' : '#64748b');

              return (
                <React.Fragment key={port.handleID}>
                  <Handle
                    id={port.handleID}
                    type="source"
                    position={sourcePos}
                    style={portStyle}
                  />
                  <div style={{
                    ...portStyle,
                    position: 'absolute',
                    pointerEvents: 'none',
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    fontSize: multiCtrl ? 9 : 10,
                    color: labelColor,
                    fontWeight: 700, lineHeight: 1,
                    userSelect: 'none',
                  }}>
                    {portLabel}
                  </div>
                </React.Fragment>
              );
            })}

            {dataOutPorts.map((port, idx) => (
              <React.Fragment key={port.handleID}>
                <Handle
                  id={port.handleID}
                  type="source"
                  position={sourcePos}
                  style={outputHandleStyle(idx)}
                />
              </React.Fragment>
            ))}
          </>
        );
      })()}

      {/* ── Card content ── */}
      <div style={{
        position: 'relative', zIndex: 1, textAlign: 'center',
        border: `2px solid ${borderColor}`,
        borderRadius: '12px',
        padding: '10px 12px 8px',
        background: 'transparent',
        boxShadow,
        animation,
        transition: 'border-color 0.15s, box-shadow 0.2s',
      }}>
        <div style={{ fontSize: '26px', lineHeight: 1 }}>{meta.emoji}</div>
        <div style={{ color: '#fff', fontWeight: 700, fontSize: '11px', marginTop: 4 }}>
          {data.label || meta.label}
        </div>
        {sub && (
          <div style={{ fontSize: '9px', color: meta.border, opacity: 0.85, marginTop: 2, maxWidth: 72, margin: '2px auto 0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {sub}
          </div>
        )}
        {data._stub && state === 'idle' && (
          <div style={{ marginTop: 3, fontSize: '9px', color: '#f59e0b', fontWeight: 700, letterSpacing: '0.05em' }}>STUB</div>
        )}
        {dbg?.state === 'done' && dbg.output && (
          <div style={{ marginTop: 4, fontSize: '9px', color: '#4ade80', maxWidth: '72px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {dbg.output.length > 30 ? dbg.output.slice(0, 30) + '…' : dbg.output}
          </div>
        )}
        {dbg?.state === 'error' && dbg.error && (
          <div style={{ marginTop: 4, fontSize: '9px', color: '#f87171', maxWidth: '72px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {dbg.error.slice(0, 30)}
          </div>
        )}
        {dbg?.state === 'running' && (
          <div style={{ marginTop: 4, fontSize: '9px', color: '#60a5fa' }}>running…</div>
        )}
        {dbg?.state === 'pending' && (
          <div style={{ marginTop: 4, fontSize: '9px', color: '#f59e0b' }}>{isLR ? 'next →' : 'next ↓'}</div>
        )}

        {/* Ports button — hidden for branch (no data ports, only named ctrl-flow ports) */}
        {nodeDef.type !== 'branch' && (
          <button
            className="nodrag nowheel nopan"
            onClick={e => { e.stopPropagation(); setShowPortsPanel(v => !v); }}
            style={{
              marginTop: 6,
              background: showPortsPanel ? 'rgba(99,102,241,0.25)' : 'rgba(255,255,255,0.06)',
              border: `1px solid ${showPortsPanel ? '#818cf8' : '#334155'}`,
              borderRadius: 6,
              color: showPortsPanel ? '#a5b4fc' : '#64748b',
              fontSize: 13, cursor: 'pointer',
              padding: '2px 7px', lineHeight: 1.4,
              transition: 'all 0.15s',
            }}
            title="Show data ports"
          >⊕</button>
        )}
      </div>

      {nodeDef.type !== 'branch' && showPortsPanel && (
        <PortsPopover
          nodeId={id}
          nodeDef={nodeDef}
          cfg={cfg as Record<string, unknown>}
          committedInputPortIDs={dynamicInputIDs}
          wiredOutputPortIDs={wiredOutIDs}
          isLR={isLR}
          sourcePos={sourcePos}
          targetPos={targetPos}
        />
      )}
    </div>
  );
}

export { stepMetaFromType as stepMeta };
