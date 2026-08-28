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
  idle:    'none',
  pending: '0 0 8px 2px rgba(245,158,11,0.5)',
  running: '0 0 8px 2px rgba(96,165,250,0.5)',
  done:    '0 0 8px 2px rgba(74,222,128,0.4)',
  error:   '0 0 8px 2px rgba(248,113,113,0.5)',
};

// Port geometry — PORT_STEP must match WIRE_STEP in page.tsx so BundleEdge wires align with dots
const PORT_STEP  = 22;
const PORT_DOT   = 9;
const PORT_START = 28;
const PORT_COLORS = ['#818cf8', '#a78bfa', '#7dd3fc', '#6ee7b7', '#fcd34d', '#f9a8d4'];

// Solid dark card — overrides near-transparent registry bg_color values
const CARD_BG = 'rgba(13,13,28,0.95)';

// Breathing animation keyframes injected once
const BREATHE_STYLE = `
@keyframes breathe {
  0%,100% { box-shadow: 0 0 6px 2px var(--breathe-color); }
  50%      { box-shadow: 0 0 18px 6px var(--breathe-color); }
}
`;
if (typeof document !== 'undefined' && !document.getElementById('sn-breathe-style')) {
  const s = document.createElement('style');
  s.id = 'sn-breathe-style';
  s.textContent = BREATHE_STYLE;
  document.head.appendChild(s);
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
  const onMouseEnter = useCallback(() => setHovered(true),  []);
  const onMouseLeave = useCallback(() => setHovered(false), []);

  // Live edges — tells us which ports are already wired
  const edges = useStore(s => s.edges);

  const hasCtrlIn  = edges.some(e => e.target === id && (!e.sourceHandle || e.sourceHandle.startsWith('ctrl')));
  const hasCtrlOut = edges.some(e => e.source === id && (!e.sourceHandle || e.sourceHandle.startsWith('ctrl')));

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

  // Resolve ports — dynamic outputs always show (transform output_vars from config)
  const inputPorts  = resolveInputPorts(nodeDef, [...allInputIDs, ...wiredInIDs]);
  const outputPorts = resolveOutputPorts(nodeDef, cfg as Record<string, unknown>);

  const ctrlOutputPorts = outputPorts.filter(p => p.kind === 'control');
  const dataInputPorts  = inputPorts.filter(p => p.kind === 'data');
  const dataOutputPorts = outputPorts.filter(p => p.kind === 'data');

  // Notify RF when handle list changes
  const portKey = [...inputPorts.map(p => p.id), ...outputPorts.map(p => p.id)].join(',');
  useEffect(() => { updateNodeInternals(id); }, [portKey, id, updateNodeInternals]);

  // ── Unsatisfied mandatory ports → breathing glow ──────────────────────────────
  // min_in > 0 means needs at least one incoming control edge
  // min_out > 0 means needs at least one outgoing control edge
  const needsIn  = nodeDef.edges.min_in  > 0 && !hasCtrlIn  && !nodeDef.is_source;
  const needsOut = nodeDef.edges.min_out > 0 && !hasCtrlOut && !nodeDef.is_sink;
  const breathes = (needsIn || needsOut) && data._debug?.state === undefined;

  // ── Visual state ──────────────────────────────────────────────────────────────
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
  } else if (breathes) {
    // Breathing glow — amber if needs both, indigo-ish per side
    const breatheColor = needsIn && needsOut
      ? 'rgba(245,158,11,0.55)'   // amber — needs wiring on both sides
      : needsIn
        ? 'rgba(99,102,241,0.55)' // indigo — needs incoming
        : 'rgba(74,222,128,0.45)'; // green — needs outgoing
    animation = `breathe 2.2s ease-in-out infinite`;
    boxShadow = `0 0 6px 2px ${breatheColor}`;
    // Pass breathe-color as CSS var for the keyframe
    borderColor = breatheColor.replace('0.55', '0.8').replace('0.45', '0.7');
  } else if (hovered) {
    boxShadow = `0 0 0 1px ${meta.border}, 0 0 12px 3px ${meta.border}44`;
  }

  // ── Geometry helpers ──────────────────────────────────────────────────────────
  function dotOffset(idx: number) { return PORT_START + idx * PORT_STEP; }

  // Invisible 1×1 RF handle — connection geometry only
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

  // Control handle style — tiny, subtle at rest; slightly brighter on hover
  function ctrlHandleStyle(wired: boolean): React.CSSProperties {
    const visible = hovered || wired;
    return {
      width: 8, height: 8,
      background: wired ? meta.border : `${meta.border}99`,
      border: `1px solid ${meta.border}`,
      opacity: visible ? 1 : 0,
      transition: 'opacity 0.15s ease, box-shadow 0.15s ease',
      boxShadow: hovered && !wired ? `0 0 6px 2px ${meta.border}66` : 'none',
    };
  }

  // Named control output positions — spread 20%–80%
  function ctrlOutHandleStyle(idx: number, total: number): React.CSSProperties {
    const frac = total === 1 ? 0.5 : 0.2 + (idx / (total - 1)) * 0.6;
    const base = ctrlHandleStyle(hasCtrlOut);
    return isLR
      ? { ...base, position: 'absolute', top: `${frac * 100}%`, right: -5, width: 10, height: 10 }
      : { ...base, position: 'absolute', left: `${frac * 100}%`, bottom: -5, width: 10, height: 10 };
  }

  // ── Port dot renderer ─────────────────────────────────────────────────────────
  function PortDot({ port, idx, side }: { port: ResolvedPort; idx: number; side: 'in' | 'out' }) {
    const off     = dotOffset(idx);
    const color   = port.color || PORT_COLORS[idx % PORT_COLORS.length];
    const isWired = side === 'out'
      ? wiredOutIDs.includes(port.id.replace('data-out-', ''))
      : wiredInIDs.includes(port.id.replace('data-in-', ''));
    const visible = hovered || isWired;

    const dotBase: React.CSSProperties = {
      position: 'absolute',
      width: PORT_DOT, height: PORT_DOT,
      borderRadius: '50%',
      background: color,
      boxShadow: isWired ? `0 0 8px 3px ${color}99` : `0 0 4px 1px ${color}66`,
      zIndex: 3,
      transition: 'transform 0.18s ease, opacity 0.18s ease',
      transform:  visible ? 'scale(1)'   : 'scale(0.2)',
      opacity:    visible ? 1 : 0,
      pointerEvents: 'none',
    };

    const dotStyle: React.CSSProperties = isLR
      ? side === 'in'
        ? { ...dotBase, top: off - PORT_DOT / 2, left:  -(PORT_DOT / 2 + 1) }
        : { ...dotBase, top: off - PORT_DOT / 2, right: -(PORT_DOT / 2 + 1) }
      : side === 'in'
        ? { ...dotBase, left: off - PORT_DOT / 2, top:    -(PORT_DOT / 2 + 1) }
        : { ...dotBase, left: off - PORT_DOT / 2, bottom: -(PORT_DOT / 2 + 1) };

    const labelBase: React.CSSProperties = {
      position: 'absolute',
      fontSize: 8,
      color,
      fontFamily: 'JetBrains Mono, monospace',
      whiteSpace: 'nowrap',
      pointerEvents: 'none',
      lineHeight: 1,
      transition: 'opacity 0.18s ease',
      opacity: visible ? 1 : 0,
    };

    const labelStyle: React.CSSProperties = isLR
      ? side === 'in'
        ? { ...labelBase, top: off - 4, left:  PORT_DOT + 5, maxWidth: 60, overflow: 'hidden', textOverflow: 'ellipsis' }
        : { ...labelBase, top: off - 4, right: PORT_DOT + 5, maxWidth: 60, overflow: 'hidden', textOverflow: 'ellipsis', textAlign: 'right' }
      : side === 'in'
        ? { ...labelBase, left: off - 2, top:    PORT_DOT + 4 }
        : { ...labelBase, left: off - 2, bottom: PORT_DOT + 4 };

    return (
      <>
        <div style={dotStyle} />
        {port.label && <div style={labelStyle}>{port.label}</div>}
      </>
    );
  }

  // Card sizing — stable height regardless of hover (no layout shift)
  const maxDataPorts = Math.max(dataInputPorts.length, dataOutputPorts.length);
  const railHeight   = maxDataPorts > 0 ? PORT_START + maxDataPorts * PORT_STEP + PORT_START : 0;
  const cardHeight   = Math.max(90, railHeight);

  // Padding — reserve label space only when data ports exist (card stays stable on hover)
  const hasDataIn  = dataInputPorts.length  > 0;
  const hasDataOut = dataOutputPorts.length > 0;
  const leftPad  = isLR  && hasDataIn  ? PORT_DOT + 62 : 14;
  const rightPad = isLR  && hasDataOut ? PORT_DOT + 62 : 14;
  const topPad   = !isLR && hasDataIn  ? PORT_DOT + 28 : 12;
  const botPad   = !isLR && hasDataOut ? PORT_DOT + 28 : 12;

  // CSS var for breathe keyframe color
  const breatheColor = needsIn && needsOut
    ? 'rgba(245,158,11,0.55)'
    : needsIn ? 'rgba(99,102,241,0.55)' : 'rgba(74,222,128,0.45)';

  return (
    <div
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
      style={{
        background: 'transparent',
        minWidth: 100,
        minHeight: cardHeight,
        position: 'relative',
        paddingTop:    topPad,
        paddingBottom: botPad,
        paddingLeft:   leftPad,
        paddingRight:  rightPad,
        ['--breathe-color' as string]: breatheColor,
      }}
    >
      {/* ── Control INPUT handle — invisible at rest, fades in on hover/wired ── */}
      {!nodeDef.is_source && (
        <Handle
          id="ctrl-in"
          type="target"
          position={targetPos}
          style={ctrlHandleStyle(hasCtrlIn)}
        />
      )}

      {/* ── Data input handles (invisible geometry) + animated dots ── */}
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

      {/* ── Named control OUTPUT handles (branch true/false) ── */}
      {ctrlOutputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="source"
            position={sourcePos}
            style={ctrlOutHandleStyle(idx, ctrlOutputPorts.length)}
          />
          {port.label && hovered && (
            <div style={{
              position: 'absolute',
              fontSize: 8, fontWeight: 700, color: port.color, pointerEvents: 'none',
              opacity: hovered ? 1 : 0,
              transition: 'opacity 0.15s',
              ...(isLR
                ? { right: -22, top: `calc(${(ctrlOutputPorts.length === 1 ? 0.5 : 0.2 + (idx / (ctrlOutputPorts.length - 1)) * 0.6) * 100}% - 5px)` }
                : { bottom: -16, left: `calc(${(ctrlOutputPorts.length === 1 ? 0.5 : 0.2 + (idx / (ctrlOutputPorts.length - 1)) * 0.6) * 100}% - 4px)` }),
            }}>
              {port.label.slice(0, 1).toUpperCase()}
            </div>
          )}
        </React.Fragment>
      ))}

      {/* ── Anonymous single control OUTPUT handle ── */}
      {ctrlOutputPorts.length === 0 && !nodeDef.is_sink && (
        <Handle
          id="ctrl-out"
          type="source"
          position={sourcePos}
          style={ctrlHandleStyle(hasCtrlOut)}
        />
      )}

      {/* ── Data output handles (invisible geometry) + animated dots ── */}
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

      {/* ── Node card content — bordered pill, transparent fill ── */}
      <div style={{
        position: 'relative', zIndex: 1, textAlign: 'center',
        border: `2px solid ${borderColor}`,
        borderRadius: '12px',
        padding: '10px 14px 8px',
        background: 'transparent',
        boxShadow,
        animation,
        transition: 'border-color 0.15s, box-shadow 0.2s',
      }}>
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
      </div>{/* end content wrapper */}
    </div>
  );
}

export { stepMetaFromType as stepMeta };
