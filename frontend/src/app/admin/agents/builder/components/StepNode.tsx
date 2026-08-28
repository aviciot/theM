import React, { useEffect } from 'react';
import { Handle, Position, useUpdateNodeInternals, useStore } from '@xyflow/react';
import { getNodeDef, resolveInputPorts, resolveOutputPorts } from '@/lib/nodeRegistry';
import type { StepNodeData, DebugNodeState } from '../types';
import { useLayoutDir } from '../LayoutContext';

function stepMetaFromType(type: string): { bg: string; border: string; emoji: string; label: string } {
  const def = getNodeDef(type);
  return { bg: def.bg, border: def.border, emoji: def.emoji, label: def.label };
}

const debugBorder: Record<DebugNodeState, string> = {
  idle: 'transparent',
  pending: '#f59e0b',
  running: '#60a5fa',
  done: '#4ade80',
  error: '#f87171',
};
const debugGlow: Record<DebugNodeState, string> = {
  idle: 'none',
  pending: '0 0 8px 2px rgba(245,158,11,0.5)',
  running: '0 0 8px 2px rgba(96,165,250,0.5)',
  done: '0 0 8px 2px rgba(74,222,128,0.4)',
  error: '0 0 8px 2px rgba(248,113,113,0.5)',
};

// Port visual constants — must match BundleEdge constants in page.tsx
const PORT_STEP   = 22;   // px between port rows (matches RAIL_STEP)
const PORT_DOT    = 7;    // dot diameter px (matches RAIL_DOT)
const PORT_START  = 24;   // px from node top/left where first data port dot sits

// Colors for data port dots — cycled by index
const PORT_COLORS = ['#818cf8','#a78bfa','#7dd3fc','#6ee7b7','#fcd34d','#f9a8d4'];

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

  // Read live edges to know which static ports are wired.
  const edges = useStore(s => s.edges);
  const wiredOutPortIDs = edges
    .filter(e => e.source === id && e.sourceHandle?.startsWith('data-out-'))
    .map(e => e.sourceHandle!.replace('data-out-', ''));
  const wiredInPortIDs = edges
    .filter(e => e.target === id && e.targetHandle?.startsWith('data-in-'))
    .map(e => e.targetHandle!.replace('data-in-', ''));

  // Ghost port during drag-hover.
  const dynamicInputPortIDs: string[] = data.inputs ? Object.keys(data.inputs) : [];
  const ghostVar   = data._draggingVar;
  const dragAccept = data._dragAccept;
  const allInputPortIDs = ghostVar && dragAccept === 'accept'
    ? [...dynamicInputPortIDs, ghostVar]
    : dynamicInputPortIDs;

  // Resolve all ports from backend definition.
  const inputPorts  = resolveInputPorts(nodeDef, [...allInputPortIDs, ...wiredInPortIDs]);
  const outputPorts = resolveOutputPorts(nodeDef, cfg as Record<string, unknown>, wiredOutPortIDs);

  const ctrlOutputPorts = outputPorts.filter(p => p.kind === 'control');
  const dataInputPorts  = inputPorts.filter(p => p.kind === 'data');
  const dataOutputPorts = outputPorts.filter(p => p.kind === 'data');

  // Notify React Flow when handle list changes.
  const portKey = [...inputPorts.map(p => p.id), ...outputPorts.map(p => p.id)].join(',');
  useEffect(() => { updateNodeInternals(id); }, [portKey, id, updateNodeInternals]);

  // Node card sizing — tall enough to fit the port rail.
  const maxDataPorts = Math.max(dataInputPorts.length, dataOutputPorts.length);
  const railHeight   = maxDataPorts > 0 ? PORT_START + maxDataPorts * PORT_STEP + PORT_START : 0;
  const cardHeight   = Math.max(90, railHeight);

  // Visual state.
  const state = dbg?.state ?? 'idle';
  let borderColor = state !== 'idle' ? debugBorder[state] : meta.border;
  let boxShadow   = state !== 'idle' ? debugGlow[state]   : 'none';
  if (state === 'idle') {
    if (data._validation === 'error') {
      borderColor = '#f87171';
      boxShadow   = '0 0 8px 2px rgba(248,113,113,0.45)';
    } else if (data._validation === 'warning' || data._stub) {
      borderColor = '#f59e0b';
      boxShadow   = '0 0 6px 1px rgba(245,158,11,0.35)';
    }
  }
  if (dragAccept === 'accept') {
    borderColor = '#4ade80';
    boxShadow   = '0 0 0 3px rgba(74,222,128,0.5), 0 0 16px 4px rgba(74,222,128,0.3)';
  } else if (dragAccept === 'reject') {
    borderColor = '#f87171';
    boxShadow   = '0 0 0 3px rgba(248,113,113,0.5), 0 0 12px 3px rgba(248,113,113,0.3)';
  }

  // ── Port rail geometry ────────────────────────────────────────────────────────
  // Port dots are rendered as visible divs ON the node card border.
  // The invisible RF Handle sits at the same position — RF uses it for connection geometry.
  // This matches the reference image: dots flush with node edge, labels in a sidebar rail.

  function dotOffset(idx: number): number { return PORT_START + idx * PORT_STEP; }

  // Invisible handle style — just a 1×1 connection point, no visual.
  const invisHandle: React.CSSProperties = { width: 1, height: 1, opacity: 0, border: 'none', background: 'transparent' };

  // Position of invisible handle (matches dot position so RF geometry is accurate).
  function inputHandleStyle(idx: number): React.CSSProperties {
    const off = dotOffset(idx);
    return isLR
      ? { ...invisHandle, top: off, left: -(PORT_DOT / 2) }
      : { ...invisHandle, left: off, top: -(PORT_DOT / 2) };
  }
  function outputHandleStyle(idx: number): React.CSSProperties {
    const off = dotOffset(idx);
    return isLR
      ? { ...invisHandle, top: off, right: -(PORT_DOT / 2) }
      : { ...invisHandle, left: off, bottom: -(PORT_DOT / 2) };
  }

  // Named control output port positions — spread across 20%–80%.
  function ctrlOutHandleStyle(idx: number, total: number): React.CSSProperties {
    const fraction = total === 1 ? 0.5 : 0.2 + (idx / (total - 1)) * 0.6;
    return isLR
      ? { top: `${fraction * 100}%`, right: -6, width: 10, height: 10 }
      : { left: `${fraction * 100}%`, bottom: -6, width: 10, height: 10 };
  }
  function ctrlOutLabelStyle(idx: number, total: number, color: string): React.CSSProperties {
    const fraction = total === 1 ? 0.5 : 0.2 + (idx / (total - 1)) * 0.6;
    return isLR
      ? { position: 'absolute', right: -18, top: `calc(${fraction * 100}% - 6px)`, fontSize: 9, color, fontWeight: 700, pointerEvents: 'none' }
      : { position: 'absolute', bottom: -18, left: `calc(${fraction * 100}% - 6px)`, fontSize: 9, color, fontWeight: 700, pointerEvents: 'none' };
  }

  // ── Visible dot + label for a data port ──────────────────────────────────────
  // Input dots: flush with LEFT edge (LR) or TOP edge (TB), label to the right/below inside card
  // Output dots: flush with RIGHT edge (LR) or BOTTOM edge (TB), label to the left/above inside card
  function InputPortDot({ port, idx }: { port: { id: string; label: string }, idx: number }) {
    const off   = dotOffset(idx);
    const color = PORT_COLORS[idx % PORT_COLORS.length];
    const dotStyle: React.CSSProperties = isLR ? {
      position: 'absolute', top: off - PORT_DOT / 2, left: -(PORT_DOT / 2 + 1),
      width: PORT_DOT, height: PORT_DOT, borderRadius: '50%',
      background: color, flexShrink: 0, zIndex: 2,
    } : {
      position: 'absolute', left: off - PORT_DOT / 2, top: -(PORT_DOT / 2 + 1),
      width: PORT_DOT, height: PORT_DOT, borderRadius: '50%',
      background: color, flexShrink: 0, zIndex: 2,
    };
    const labelStyle: React.CSSProperties = isLR ? {
      position: 'absolute', top: off - 5, left: PORT_DOT + 4,
      fontSize: 8, color, fontFamily: 'JetBrains Mono, monospace',
      whiteSpace: 'nowrap', pointerEvents: 'none', lineHeight: 1,
      maxWidth: 52, overflow: 'hidden', textOverflow: 'ellipsis',
    } : {
      position: 'absolute', left: off - 2, top: PORT_DOT + 3,
      fontSize: 8, color, fontFamily: 'JetBrains Mono, monospace',
      whiteSpace: 'nowrap', pointerEvents: 'none', lineHeight: 1,
    };
    return (
      <>
        <div style={dotStyle} />
        {port.label && <div style={labelStyle}>{port.label}</div>}
      </>
    );
  }

  function OutputPortDot({ port, idx }: { port: { id: string; label: string }, idx: number }) {
    const off   = dotOffset(idx);
    const color = PORT_COLORS[idx % PORT_COLORS.length];
    const dotStyle: React.CSSProperties = isLR ? {
      position: 'absolute', top: off - PORT_DOT / 2, right: -(PORT_DOT / 2 + 1),
      width: PORT_DOT, height: PORT_DOT, borderRadius: '50%',
      background: color, flexShrink: 0, zIndex: 2,
    } : {
      position: 'absolute', left: off - PORT_DOT / 2, bottom: -(PORT_DOT / 2 + 1),
      width: PORT_DOT, height: PORT_DOT, borderRadius: '50%',
      background: color, flexShrink: 0, zIndex: 2,
    };
    const labelStyle: React.CSSProperties = isLR ? {
      position: 'absolute', top: off - 5, right: PORT_DOT + 4,
      fontSize: 8, color, fontFamily: 'JetBrains Mono, monospace',
      whiteSpace: 'nowrap', pointerEvents: 'none', lineHeight: 1,
      textAlign: 'right', maxWidth: 52, overflow: 'hidden', textOverflow: 'ellipsis',
    } : {
      position: 'absolute', left: off - 2, bottom: PORT_DOT + 3,
      fontSize: 8, color, fontFamily: 'JetBrains Mono, monospace',
      whiteSpace: 'nowrap', pointerEvents: 'none', lineHeight: 1,
    };
    return (
      <>
        <div style={dotStyle} />
        {port.label && <div style={labelStyle}>{port.label}</div>}
      </>
    );
  }

  // Padding so node card content doesn't overlap the port dot + label area.
  const leftPad  = isLR && dataInputPorts.length  > 0 ? PORT_DOT + 56 : 12;
  const rightPad = isLR && dataOutputPorts.length > 0 ? PORT_DOT + 56 : 12;
  const topPad   = !isLR && dataInputPorts.length  > 0 ? PORT_DOT + 24 : 10;
  const botPad   = !isLR && dataOutputPorts.length > 0 ? PORT_DOT + 24 : 10;

  return (
    <div style={{
      background: meta.bg,
      minWidth: 100,
      minHeight: cardHeight,
      textAlign: 'center',
      paddingTop:    topPad,
      paddingBottom: botPad,
      paddingLeft:   leftPad,
      paddingRight:  rightPad,
      border: `2px solid ${borderColor}`,
      borderRadius: '10px',
      boxShadow,
      transition: 'border-color 0.15s, box-shadow 0.15s',
      position: 'relative',
    }}>

      {/* ── Control target handle (anonymous, centered) ── */}
      {!nodeDef.is_source && (
        <Handle id="ctrl-in" type="target" position={targetPos}
          style={{ background: meta.border }} />
      )}

      {/* ── Data input port handles (invisible geometry) + visible dots ── */}
      {dataInputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="target"
            position={targetPos}
            style={inputHandleStyle(idx)}
          />
          <InputPortDot port={port} idx={idx} />
        </React.Fragment>
      ))}

      {/* ── Named control output handles (e.g. branch true/false) ── */}
      {ctrlOutputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="source"
            position={sourcePos}
            style={{ background: port.color, ...ctrlOutHandleStyle(idx, ctrlOutputPorts.length) }}
          />
          {port.label && (
            <div style={ctrlOutLabelStyle(idx, ctrlOutputPorts.length, port.color)}>
              {port.label.slice(0, 1).toUpperCase()}
            </div>
          )}
        </React.Fragment>
      ))}

      {/* ── Anonymous single control output ── */}
      {ctrlOutputPorts.length === 0 && !nodeDef.is_sink && (
        <Handle id="ctrl-out" type="source" position={sourcePos}
          style={{ background: meta.border }} />
      )}

      {/* ── Data output port handles (invisible geometry) + visible dots ── */}
      {dataOutputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="source"
            position={sourcePos}
            style={outputHandleStyle(idx)}
          />
          <OutputPortDot port={port} idx={idx} />
        </React.Fragment>
      ))}

      {/* ── Node card content ── */}
      <div style={{ fontSize: '28px', lineHeight: 1 }}>{meta.emoji}</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '11px', marginTop: 4 }}>
        {data.label || meta.label}
      </div>
      {sub && (
        <div style={{ fontSize: '9px', color: meta.border, opacity: 0.85, marginTop: 2, maxWidth: 80, margin: '2px auto 0' }}>
          {sub}
        </div>
      )}
      {data._stub && state === 'idle' && (
        <div style={{ marginTop: 3, fontSize: '9px', color: '#f59e0b', fontWeight: 700, letterSpacing: '0.05em' }}>STUB</div>
      )}
      {dbg?.state === 'done' && dbg.output && (
        <div style={{ marginTop: 4, fontSize: '9px', color: '#4ade80', maxWidth: '80px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {dbg.output.length > 30 ? dbg.output.slice(0, 30) + '…' : dbg.output}
        </div>
      )}
      {dbg?.state === 'error' && dbg.error && (
        <div style={{ marginTop: 4, fontSize: '9px', color: '#f87171', maxWidth: '80px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {dbg.error.slice(0, 30)}
        </div>
      )}
      {dbg?.state === 'running' && (
        <div style={{ marginTop: 4, fontSize: '9px', color: '#60a5fa' }}>running…</div>
      )}
      {dbg?.state === 'pending' && (
        <div style={{ marginTop: 4, fontSize: '9px', color: '#f59e0b' }}>{isLR ? 'next →' : 'next ↓'}</div>
      )}
    </div>
  );
}

export { stepMetaFromType as stepMeta };
