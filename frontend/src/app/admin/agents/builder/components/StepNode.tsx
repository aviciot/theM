import React from 'react';
import { Handle, Position } from '@xyflow/react';
import { getNodeDef } from '@/lib/nodeRegistry';
import type { PortDef } from '@/lib/nodeRegistry';
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

interface FunctionStep { fn: string; input_var: string; output_var: string; }

function computeFinalOutputs(fns: FunctionStep[]): string[] {
  const consumed = new Set(fns.map(s => s.input_var).filter(Boolean));
  const seen = new Set<string>();
  const finals: string[] = [];
  for (const s of fns) {
    if (s.output_var && !consumed.has(s.output_var) && !seen.has(s.output_var)) {
      seen.add(s.output_var);
      finals.push(s.output_var);
    }
  }
  return finals;
}

const H_DOT = 8;   // handle square size px
const PORT_MIN_GAP = 20; // minimum px between port centers
const PORT_MARGIN  = 16; // margin from edge of node to first/last port center
const NODE_MIN     = 80; // minimum node width/height px
const LABEL_GAP    = 4;  // gap between handle edge and label

/** Minimum edge length (px) needed to hold N ports evenly spaced. */
export function minEdgeForPorts(n: number): number {
  if (n === 0) return NODE_MIN;
  return Math.max(NODE_MIN, 2 * PORT_MARGIN + (n - 1) * PORT_MIN_GAP);
}

/**
 * Evenly spaced port positions (center of each handle) along an edge of `edgeSize` px.
 * Returns array of px offsets from the near edge of the node.
 */
function portPositions(n: number, edgeSize: number): number[] {
  if (n === 0) return [];
  const gap = edgeSize / (n + 1);
  return Array.from({ length: n }, (_, i) => gap * (i + 1));
}

export function StepNode({ data }: { data: StepNodeData; id: string }) {
  const nodeDef = getNodeDef(data.step_type);
  const meta = { bg: nodeDef.bg, border: nodeDef.border, emoji: nodeDef.emoji, label: nodeDef.label };
  const cfg = data.config ?? {};
  const isLR = useLayoutDir() === 'LR';
  const dbg = data._debug;
  const sub = nodeDef.summary(cfg);
  const state = dbg?.state ?? 'idle';

  // Border / shadow state
  let borderColor = state !== 'idle' ? debugBorder[state] : 'transparent';
  let boxShadow   = state !== 'idle' ? debugGlow[state]   : 'none';
  if (state === 'idle') {
    if (data._validation === 'error')                       { borderColor = '#f87171'; boxShadow = '0 0 8px 2px rgba(248,113,113,0.45)'; }
    else if (data._validation === 'warning' || data._stub) { borderColor = '#f59e0b'; boxShadow = '0 0 6px 1px rgba(245,158,11,0.35)'; }
  }
  const dragAccept = data._dragAccept;
  const ghostVar   = data._draggingVar;
  if (dragAccept === 'accept') { borderColor = '#4ade80'; boxShadow = '0 0 0 3px rgba(74,222,128,0.5), 0 0 16px 4px rgba(74,222,128,0.3)'; }
  else if (dragAccept === 'reject') { borderColor = '#f87171'; boxShadow = '0 0 0 3px rgba(248,113,113,0.5), 0 0 12px 3px rgba(248,113,113,0.3)'; }

  // Port lists
  const staticInputPorts:  PortDef[] = nodeDef.input_ports  ?? [];
  const staticOutputPorts: PortDef[] = nodeDef.output_ports ?? [];
  const dynamicInputPorts: string[]  = data.inputs ? Object.keys(data.inputs) : [];

  // Transform outputs use the same output-edge system
  const isTransform = data.step_type === 'transform';
  const transformOutputs = isTransform
    ? computeFinalOutputs((cfg.functions as FunctionStep[] | undefined) ?? [])
    : [];

  // Merge all input ports: dynamic (committed) + ghost + static registry
  const committedInputs = dynamicInputPorts;
  const ghostIsNew = ghostVar && dragAccept === 'accept' && !committedInputs.includes(ghostVar);
  const allInputPorts: Array<{ id: string; ghost?: boolean }> = [
    ...committedInputs.map(id => ({ id })),
    ...(ghostIsNew ? [{ id: ghostVar!, ghost: true }] : []),
    ...staticInputPorts.map(p => ({ id: p.id })),
  ];

  // Merge all output ports: transform named + static registry
  const allOutputPorts: Array<{ id: string; label: string }> = [
    ...transformOutputs.map(v => ({ id: v, label: v })),
    ...staticOutputPorts.map(p => ({ id: p.id, label: p.id })),
  ];

  // Branch keeps its own T/F output handles — skip output system for branch.
  const isBranch = data.step_type === 'branch';

  // ── Port edge positions ────────────────────────────────────────────────────
  // LR: inputs on TOP, outputs on BOTTOM, control flow left/right.
  // TB: inputs on LEFT, outputs on RIGHT, control flow top/bottom.
  //
  // We read the rendered node size via a ref — but since we can't in pure CSS
  // nodes, we use NODE_MIN as the baseline and let ports overflow if needed.
  // dagre is told the correct size via the exported minEdgeForPorts() helper.

  const inputEdgeSize  = Math.max(NODE_MIN, minEdgeForPorts(allInputPorts.length));
  const outputEdgeSize = Math.max(NODE_MIN, minEdgeForPorts(isBranch ? 2 : allOutputPorts.length));

  const inputPositions  = portPositions(allInputPorts.length, inputEdgeSize);
  const outputPositions = portPositions(isBranch ? 2 : allOutputPorts.length, outputEdgeSize);

  // Handle and label styles
  const dataInStyle  = { width: H_DOT, height: H_DOT, borderRadius: 2, background: '#f97316', border: '1px solid rgba(0,0,0,0.35)' };
  const dataOutStyle = { width: H_DOT, height: H_DOT, borderRadius: 2, background: '#818cf8', border: '1px solid rgba(0,0,0,0.35)' };
  const hOff = -(H_DOT / 2 + 1);

  // Input port handle position:  LR→top edge, TB→left edge
  function inHandlePos(px: number): React.CSSProperties {
    return isLR
      ? { position: 'absolute', left: px - H_DOT / 2, top: hOff }
      : { position: 'absolute', top: px - H_DOT / 2, left: hOff };
  }
  // Input label: above handle (LR) or to the right of handle (TB)
  function inLabelPos(px: number): React.CSSProperties {
    const base: React.CSSProperties = { position: 'absolute', fontSize: 7, color: '#f97316', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1 };
    return isLR
      ? { ...base, left: px - 20, top: H_DOT + LABEL_GAP, textAlign: 'center', width: 40 }
      : { ...base, top: px - 5, left: H_DOT + LABEL_GAP };
  }

  // Output port handle position: LR→bottom edge, TB→right edge
  function outHandlePos(px: number): React.CSSProperties {
    return isLR
      ? { position: 'absolute', left: px - H_DOT / 2, bottom: hOff }
      : { position: 'absolute', top: px - H_DOT / 2, right: hOff };
  }
  // Output label: below handle (LR) or to the left of handle (TB)
  function outLabelPos(px: number): React.CSSProperties {
    const base: React.CSSProperties = { position: 'absolute', fontSize: 7, color: '#818cf8', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1 };
    return isLR
      ? { ...base, left: px - 20, bottom: H_DOT + LABEL_GAP, textAlign: 'center', width: 40 }
      : { ...base, top: px - 5, right: H_DOT + LABEL_GAP, textAlign: 'right' };
  }

  // Node sizing: width driven by inputEdgeSize (LR: top edge), height constant.
  // In LR the top/bottom edges are the "wide" dimension.
  const nodeW = isLR ? Math.max(inputEdgeSize, outputEdgeSize, NODE_MIN) : NODE_MIN;
  const nodeH = isLR ? NODE_MIN : Math.max(inputEdgeSize, outputEdgeSize, NODE_MIN);

  // Padding clears space for labels outside the border.
  const labelClear = H_DOT + LABEL_GAP + 10;
  const hasInputPorts  = allInputPorts.length > 0;
  const hasOutputPorts = allOutputPorts.length > 0 && !isBranch;

  return (
    <div style={{
      textAlign: 'center', position: 'relative',
      width: nodeW, height: nodeH,
      paddingTop:    isLR ? (hasInputPorts ? labelClear : 8) : 8,
      paddingBottom: isLR ? (hasOutputPorts ? labelClear : 8) : 8,
      paddingLeft:   8,
      paddingRight:  8,
      display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
      border: `2px solid ${borderColor}`, borderRadius: '10px', boxShadow,
      transition: 'border-color 0.15s, box-shadow 0.15s',
      boxSizing: 'border-box',
    }}>

      {/* Control-flow handles — left/right (LR) or top/bottom (TB) */}
      <Handle type="target" position={isLR ? Position.Left  : Position.Top}    style={{ background: meta.border }} />
      <Handle type="source" position={isLR ? Position.Right : Position.Bottom}
        style={{ background: meta.border, ...(isBranch ? { display: 'none' } : {}) }} />

      {/* Branch: two named control outputs */}
      {isBranch && (
        <>
          <Handle id="source-true"  type="source" position={isLR ? Position.Right : Position.Bottom}
            style={{ background: '#4ade80', width: 10, height: 10,
              ...(isLR ? { top: `${outputPositions[0]}px`, right: -6 } : { left: `${outputPositions[0]}px`, bottom: -6 }) }} />
          <span style={{ position: 'absolute', fontSize: 9, color: '#4ade80', fontWeight: 700, pointerEvents: 'none',
            ...(isLR ? { right: -18, top: `${outputPositions[0] - 6}px` } : { bottom: -16, left: `${outputPositions[0] - 4}px` }) }}>T</span>
          <Handle id="source-false" type="source" position={isLR ? Position.Right : Position.Bottom}
            style={{ background: '#f87171', width: 10, height: 10,
              ...(isLR ? { top: `${outputPositions[1]}px`, right: -6 } : { left: `${outputPositions[1]}px`, bottom: -6 }) }} />
          <span style={{ position: 'absolute', fontSize: 9, color: '#f87171', fontWeight: 700, pointerEvents: 'none',
            ...(isLR ? { right: -18, top: `${outputPositions[1] - 6}px` } : { bottom: -16, left: `${outputPositions[1] - 4}px` }) }}>F</span>
        </>
      )}

      {/* Data input ports — top edge (LR) or left edge (TB) */}
      {allInputPorts.map((port, i) => (
        <React.Fragment key={`in-${port.id}`}>
          <Handle
            id={`data-in-${port.id}`}
            type="target"
            position={isLR ? Position.Top : Position.Left}
            style={{ ...dataInStyle, ...inHandlePos(inputPositions[i]), opacity: port.ghost ? 0.5 : 1 }}
            title={port.id}
          />
          <span style={{ ...inLabelPos(inputPositions[i]), opacity: port.ghost ? 0.6 : 1 }}>{port.id}</span>
        </React.Fragment>
      ))}

      {/* Data output ports — bottom edge (LR) or right edge (TB) */}
      {!isBranch && allOutputPorts.map((port, i) => (
        <React.Fragment key={`out-${port.id}`}>
          <Handle
            id={`data-out-${port.id}`}
            type="source"
            position={isLR ? Position.Bottom : Position.Right}
            style={{ ...dataOutStyle, ...outHandlePos(outputPositions[i]) }}
            title={port.label}
          />
          <span style={outLabelPos(outputPositions[i])}>{port.label}</span>
        </React.Fragment>
      ))}

      {/* Node content */}
      <div style={{ fontSize: '32px', lineHeight: 1 }}>{meta.emoji}</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '11px', marginTop: 4 }}>{data.label || meta.label}</div>
      {sub && <div style={{ fontSize: '10px', color: meta.border, opacity: 0.9, marginTop: 2 }}>{sub}</div>}
      {data._stub && state === 'idle' && <div style={{ marginTop: 3, fontSize: '9px', color: '#f59e0b', fontWeight: 700, letterSpacing: '0.05em' }}>STUB</div>}
      {dbg?.state === 'done'    && dbg.output && <div style={{ marginTop: 4, fontSize: '9px', color: '#4ade80',  maxWidth: '90px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{dbg.output.length > 30 ? dbg.output.slice(0, 30) + '…' : dbg.output}</div>}
      {dbg?.state === 'error'   && dbg.error  && <div style={{ marginTop: 4, fontSize: '9px', color: '#f87171',  maxWidth: '90px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{dbg.error.slice(0, 30)}</div>}
      {dbg?.state === 'running' && <div style={{ marginTop: 4, fontSize: '9px', color: '#60a5fa' }}>running…</div>}
      {dbg?.state === 'pending' && <div style={{ marginTop: 4, fontSize: '9px', color: '#f59e0b' }}>{isLR ? 'next →' : 'next ↓'}</div>}
    </div>
  );
}

export { stepMetaFromType as stepMeta };
