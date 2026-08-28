import React, { useEffect, useState, useCallback } from 'react';
import { Handle, Position, useUpdateNodeInternals, useStore } from '@xyflow/react';
import { getNodeDef, resolveInputPorts, resolveOutputPorts } from '@/lib/nodeRegistry';
import type { ResolvedPort } from '@/lib/nodeRegistry';
import type { StepNodeData, DebugNodeState } from '../types';
import { useLayoutDir } from '../LayoutContext';

function stepMetaFromType(type: string): { bg: string; border: string; emoji: string; label: string } {
  const def = getNodeDef(type);
  return { bg: def.bg, border: def.border, emoji: def.emoji, label: def.label };
}

const debugBorder: Record<DebugNodeState, string> = {
  idle: 'transparent', pending: '#f59e0b', running: '#60a5fa', done: '#4ade80', error: '#f87171',
};
const debugGlow: Record<DebugNodeState, string> = {
  idle: 'none',
  pending: '0 0 8px 2px rgba(245,158,11,0.5)',
  running: '0 0 8px 2px rgba(96,165,250,0.5)',
  done:    '0 0 8px 2px rgba(74,222,128,0.4)',
  error:   '0 0 8px 2px rgba(248,113,113,0.5)',
};

// Port geometry — must stay in sync with WIRE_STEP in page.tsx (BundleEdge uses same spacing)
const PORT_STEP  = 22;   // px between port rows
const PORT_DOT   = 9;    // visible dot diameter
const PORT_START = 28;   // px from node top/left to first port dot center

// Port dot colors — cycled by index, matching BundleEdge WIRE_COLORS
const PORT_COLORS = ['#818cf8', '#a78bfa', '#7dd3fc', '#6ee7b7', '#fcd34d', '#f9a8d4'];

// Card background — solid dark so node is always readable regardless of meta.bg opacity
const CARD_BG = 'rgba(15,15,30,0.92)';

export function StepNode({ id, data }: { id: string; data: StepNodeData }) {
  const nodeDef    = getNodeDef(data.step_type);
  const meta       = { bg: nodeDef.bg, border: nodeDef.border, emoji: nodeDef.emoji, label: nodeDef.label };
  const cfg        = data.config ?? {};
  const layoutDir  = useLayoutDir();
  const isLR       = layoutDir === 'LR';
  const targetPos  = isLR ? Position.Left  : Position.Top;
  const sourcePos  = isLR ? Position.Right : Position.Bottom;
  const dbg        = data._debug;
  const sub        = nodeDef.summary(cfg);
  const updateNodeInternals = useUpdateNodeInternals();

  const [hovered, setHovered] = useState(false);

  // Read live edges — needed to know which wired ports to always show (even when not hovering)
  const edges = useStore(s => s.edges);
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

  // Resolve ALL ports — dynamic outputs always included (transform shows output_vars from config)
  const inputPorts  = resolveInputPorts(nodeDef, [...allInputIDs, ...wiredInIDs]);
  const outputPorts = resolveOutputPorts(nodeDef, cfg as Record<string, unknown>);

  const ctrlOutputPorts = outputPorts.filter(p => p.kind === 'control');
  const dataInputPorts  = inputPorts.filter(p => p.kind === 'data');
  const dataOutputPorts = outputPorts.filter(p => p.kind === 'data');

  // Which data ports to SHOW visually:
  // - Always show wired ports (their wire already comes from them)
  // - Show all data ports on hover (so user can drag from any)
  const wiredOutSet = new Set(wiredOutIDs.map(id => `data-out-${id}`));
  const wiredInSet  = new Set(wiredInIDs.map(id => `data-in-${id}`));

  const visibleOutPorts = dataOutputPorts.filter(p => hovered || wiredOutSet.has(p.id));
  const visibleInPorts  = dataInputPorts.filter(p => hovered || wiredInSet.has(p.id));

  // RF handles must always be registered (invisible) even when not visually shown —
  // so connection geometry works for wired ports even when not hovered.
  // We render invisible handles for ALL resolved data ports at all times.
  const portKey = [...inputPorts.map(p => p.id), ...outputPorts.map(p => p.id)].join(',');
  useEffect(() => { updateNodeInternals(id); }, [portKey, id, updateNodeInternals]);

  // Node card min-height: enough for the tallest visible rail (hover state = all ports)
  const maxDataPorts = Math.max(dataInputPorts.length, dataOutputPorts.length);
  const railHeight   = maxDataPorts > 0 ? PORT_START + maxDataPorts * PORT_STEP + PORT_START : 0;
  const cardHeight   = Math.max(90, railHeight);

  // ── Visual state ──────────────────────────────────────────────────────────────
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

  // On hover add a subtle glow to the border so user knows the node is interactive
  if (hovered && state === 'idle' && dragAccept !== 'accept' && dragAccept !== 'reject') {
    boxShadow = `0 0 0 1px ${meta.border}, 0 0 14px 3px ${meta.border}55`;
  }

  // ── Geometry helpers ──────────────────────────────────────────────────────────
  function dotOffset(idx: number) { return PORT_START + idx * PORT_STEP; }

  // Invisible RF handle — just a connection point, no visual
  const invisHandle: React.CSSProperties = {
    width: 1, height: 1, opacity: 0, border: 'none', background: 'transparent',
  };

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

  // Named control output handle positions — spread 20%–80%
  function ctrlOutHandleStyle(idx: number, total: number): React.CSSProperties {
    const frac = total === 1 ? 0.5 : 0.2 + (idx / (total - 1)) * 0.6;
    return isLR
      ? { top: `${frac * 100}%`, right: -6, width: 11, height: 11 }
      : { left: `${frac * 100}%`, bottom: -6, width: 11, height: 11 };
  }

  // ── Port dot + label renderers ────────────────────────────────────────────────
  // Dots sit flush with the node border edge, labels inside the card.
  // Transition: scale + opacity so they animate in on hover.

  const onMouseEnter = useCallback(() => setHovered(true),  []);
  const onMouseLeave = useCallback(() => setHovered(false), []);

  function PortDot({ port, idx, side }: { port: ResolvedPort; idx: number; side: 'in' | 'out' }) {
    const off     = dotOffset(idx);
    const color   = port.color || PORT_COLORS[idx % PORT_COLORS.length];
    const isWired = side === 'out' ? wiredOutSet.has(port.id) : wiredInSet.has(port.id);
    const visible = hovered || isWired;

    // Dot position: flush with card border edge
    const dotBase: React.CSSProperties = {
      position: 'absolute',
      width:  PORT_DOT, height: PORT_DOT,
      borderRadius: '50%',
      background: color,
      boxShadow: `0 0 6px 2px ${color}88`,
      zIndex: 3,
      transition: 'transform 0.15s ease, opacity 0.15s ease',
      transform:  visible ? 'scale(1)'   : 'scale(0.3)',
      opacity:    visible ? (isWired ? 1 : 0.85) : 0,
      pointerEvents: 'none',
    };

    const dotStyle: React.CSSProperties = isLR
      ? side === 'in'
        ? { ...dotBase, top: off - PORT_DOT / 2, left: -(PORT_DOT / 2 + 1) }
        : { ...dotBase, top: off - PORT_DOT / 2, right: -(PORT_DOT / 2 + 1) }
      : side === 'in'
        ? { ...dotBase, left: off - PORT_DOT / 2, top: -(PORT_DOT / 2 + 1) }
        : { ...dotBase, left: off - PORT_DOT / 2, bottom: -(PORT_DOT / 2 + 1) };

    // Label: inside card, next to dot
    const labelBase: React.CSSProperties = {
      position: 'absolute',
      fontSize: 8,
      color,
      fontFamily: 'JetBrains Mono, monospace',
      whiteSpace: 'nowrap',
      pointerEvents: 'none',
      lineHeight: 1,
      transition: 'opacity 0.15s ease',
      opacity: visible ? 1 : 0,
    };

    const labelStyle: React.CSSProperties = isLR
      ? side === 'in'
        ? { ...labelBase, top: off - 4, left: PORT_DOT + 5, maxWidth: 58, overflow: 'hidden', textOverflow: 'ellipsis' }
        : { ...labelBase, top: off - 4, right: PORT_DOT + 5, maxWidth: 58, overflow: 'hidden', textOverflow: 'ellipsis', textAlign: 'right' }
      : side === 'in'
        ? { ...labelBase, left: off - 2, top: PORT_DOT + 4 }
        : { ...labelBase, left: off - 2, bottom: PORT_DOT + 4 };

    return (
      <>
        <div style={dotStyle} />
        {port.label && <div style={labelStyle}>{port.label}</div>}
      </>
    );
  }

  // ── Card padding — only reserve space when hovering (so resting card is compact) ──
  // We always reserve space for the max port count so the card doesn't resize on hover,
  // which would cause jarring layout shifts. Card height is stable.
  const hasDataIn  = dataInputPorts.length  > 0;
  const hasDataOut = dataOutputPorts.length > 0;
  const leftPad  = isLR  && hasDataIn  ? PORT_DOT + 60 : 14;
  const rightPad = isLR  && hasDataOut ? PORT_DOT + 60 : 14;
  const topPad   = !isLR && hasDataIn  ? PORT_DOT + 26 : 12;
  const botPad   = !isLR && hasDataOut ? PORT_DOT + 26 : 12;

  return (
    <div
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
      style={{
        background: CARD_BG,
        minWidth: 100,
        minHeight: cardHeight,
        textAlign: 'center',
        paddingTop:    topPad,
        paddingBottom: botPad,
        paddingLeft:   leftPad,
        paddingRight:  rightPad,
        border: `2px solid ${borderColor}`,
        borderRadius: '12px',
        boxShadow,
        transition: 'border-color 0.15s, box-shadow 0.2s',
        position: 'relative',
      }}
    >
      {/* ── Control target handle (always visible, centered) ── */}
      {!nodeDef.is_source && (
        <Handle id="ctrl-in" type="target" position={targetPos}
          style={{ background: meta.border, width: 10, height: 10 }} />
      )}

      {/* ── Data input handles — invisible geometry for ALL ports, dots shown conditionally ── */}
      {dataInputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="target"
            position={targetPos}
            style={inputHandleStyle(idx)}
          />
          <PortDot port={port} idx={idx} side="in" />
        </React.Fragment>
      ))}

      {/* ── Named control output handles (branch true/false) ── */}
      {ctrlOutputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="source"
            position={sourcePos}
            style={{ background: port.color, ...ctrlOutHandleStyle(idx, ctrlOutputPorts.length) }}
          />
          {port.label && (
            <div style={{
              position: 'absolute',
              fontSize: 8, fontWeight: 700, color: port.color, pointerEvents: 'none',
              ...(isLR
                ? { right: -20, top: `calc(${(ctrlOutputPorts.length === 1 ? 0.5 : 0.2 + (idx / (ctrlOutputPorts.length - 1)) * 0.6) * 100}% - 5px)` }
                : { bottom: -16, left: `calc(${(ctrlOutputPorts.length === 1 ? 0.5 : 0.2 + (idx / (ctrlOutputPorts.length - 1)) * 0.6) * 100}% - 4px)` }),
            }}>
              {port.label.slice(0, 1).toUpperCase()}
            </div>
          )}
        </React.Fragment>
      ))}

      {/* ── Anonymous single control output (all non-sink nodes without named ctrl ports) ── */}
      {ctrlOutputPorts.length === 0 && !nodeDef.is_sink && (
        <Handle id="ctrl-out" type="source" position={sourcePos}
          style={{ background: meta.border, width: 10, height: 10 }} />
      )}

      {/* ── Data output handles — invisible geometry for ALL ports, dots shown conditionally ── */}
      {dataOutputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="source"
            position={sourcePos}
            style={outputHandleStyle(idx)}
          />
          <PortDot port={port} idx={idx} side="out" />
        </React.Fragment>
      ))}

      {/* ── Node card content ── */}
      <div style={{ fontSize: '26px', lineHeight: 1 }}>{meta.emoji}</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '11px', marginTop: 5 }}>
        {data.label || meta.label}
      </div>
      {sub && (
        <div style={{ fontSize: '9px', color: meta.border, opacity: 0.9, marginTop: 2, maxWidth: 80, margin: '2px auto 0' }}>
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
