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

// Port layout constants — pixel-based, same for both layouts.
const PORT_START = 20;  // px from the node edge where first data port sits
const PORT_STEP  = 18;  // px between ports
const HANDLE_SZ  = 8;   // handle square side length
const HANDLE_OFF = -(HANDLE_SZ / 2 + 1); // flush with node border

export function StepNode({ data }: { data: StepNodeData; id: string }) {
  const nodeDef = getNodeDef(data.step_type);
  const meta = { bg: nodeDef.bg, border: nodeDef.border, emoji: nodeDef.emoji, label: nodeDef.label };
  const cfg = data.config ?? {};
  const layoutDir = useLayoutDir();
  const isLR = layoutDir === 'LR';
  const targetPos = isLR ? Position.Left  : Position.Top;
  const sourcePos = isLR ? Position.Right : Position.Bottom;
  const dbg = data._debug;
  const sub = nodeDef.summary(cfg);

  const state = dbg?.state ?? 'idle';

  let borderColor = state !== 'idle' ? debugBorder[state] : 'transparent';
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

  // Drag highlight overrides border/shadow.
  const dragAccept = data._dragAccept;
  const ghostVar   = data._draggingVar;
  if (dragAccept === 'accept') {
    borderColor = '#4ade80';
    boxShadow   = '0 0 0 3px rgba(74,222,128,0.5), 0 0 16px 4px rgba(74,222,128,0.3)';
  } else if (dragAccept === 'reject') {
    borderColor = '#f87171';
    boxShadow   = '0 0 0 3px rgba(248,113,113,0.5), 0 0 12px 3px rgba(248,113,113,0.3)';
  }

  const isTransform = data.step_type === 'transform';
  const transformOutputs = isTransform
    ? computeFinalOutputs((cfg.functions as FunctionStep[] | undefined) ?? [])
    : [];

  // Each transform output row needs 18px; header needs ~70px minimum.
  const PX_PER_ROW = 18;
  const HEADER_PX = 70;
  const transformMinHeight = isTransform && transformOutputs.length > 0
    ? Math.max(HEADER_PX, transformOutputs.length * PX_PER_ROW + 16)
    : 0;

  // Static data ports from the node registry.
  const inputPorts: PortDef[]  = nodeDef.input_ports  ?? [];
  const outputPorts: PortDef[] = nodeDef.output_ports ?? [];

  // All dynamic input ports (committed) + ghost (during drag).
  const dynamicInputPorts: string[] = data.inputs ? Object.keys(data.inputs) : [];
  const allInputPorts = ghostVar && dragAccept === 'accept'
    ? [...dynamicInputPorts, ghostVar]
    : dynamicInputPorts;

  // Data-port handle base styles.
  const dataInStyle  = { background: '#f97316', width: HANDLE_SZ, height: HANDLE_SZ, borderRadius: 2, border: '1px solid rgba(0,0,0,0.4)' };
  const dataOutStyle = { background: '#818cf8', width: HANDLE_SZ, height: HANDLE_SZ, borderRadius: 2, border: '1px solid rgba(0,0,0,0.4)' };

  // Returns absolute position style for the Nth data input port on the target edge.
  // LR: ports stack down the left edge. TB: ports stack right along the top edge.
  function inputPortPos(idx: number): React.CSSProperties {
    const offset = PORT_START + idx * PORT_STEP;
    return isLR
      ? { position: 'absolute', top: offset, left: HANDLE_OFF }
      : { position: 'absolute', left: offset, top: HANDLE_OFF };
  }

  // Label position: sits inside the node, adjacent to the handle.
  // LR: label to the right of the left-edge handle.
  // TB: label below the top-edge handle, centered on handle.
  function inputLabelPos(idx: number): React.CSSProperties {
    const offset = PORT_START + idx * PORT_STEP;
    return isLR
      ? { position: 'absolute', top: offset - 3, left: HANDLE_SZ + 4, fontSize: 7, color: '#f97316', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1 }
      : { position: 'absolute', left: offset - 2, top: HANDLE_SZ + 3, fontSize: 7, color: '#f97316', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1 };
  }

  // Output port position — mirrors input but on the opposite edge.
  function outputPortPos(idx: number): React.CSSProperties {
    const offset = PORT_START + idx * PORT_STEP;
    return isLR
      ? { position: 'absolute', top: offset, right: HANDLE_OFF }
      : { position: 'absolute', left: offset, bottom: HANDLE_OFF };
  }

  function outputLabelPos(idx: number): React.CSSProperties {
    const offset = PORT_START + idx * PORT_STEP;
    return isLR
      ? { position: 'absolute', top: offset - 3, right: HANDLE_SZ + 4, fontSize: 7, color: '#818cf8', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1, textAlign: 'right' }
      : { position: 'absolute', left: offset - 2, bottom: HANDLE_SZ + 3, fontSize: 7, color: '#818cf8', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1 };
  }

  // Extra padding so port labels don't clip into node content.
  const inputPortPad  = allInputPorts.length  > 0 ? HANDLE_SZ + 24 : 0;
  const outputPortPad = outputPorts.length > 0 ? HANDLE_SZ + 24 : 0;
  const transformRightPad = isTransform && transformOutputs.length > 0 ? 72 : 0;

  return (
    <div style={{
      background: 'transparent', minWidth: '80px', textAlign: 'center',
      paddingTop:    isLR ? 8 : (inputPortPad  > 0 ? inputPortPad  : 8),
      paddingBottom: isLR ? 8 : ((transformRightPad || outputPortPad) > 0 ? Math.max(transformRightPad, outputPortPad) : 8),
      paddingLeft:   isLR ? (inputPortPad > 0 ? inputPortPad : 8) : 8,
      paddingRight:  isLR ? ((transformRightPad || outputPortPad) > 0 ? Math.max(transformRightPad, outputPortPad) : 8) : 8,
      border: `2px solid ${borderColor}`, borderRadius: '10px', boxShadow,
      transition: 'border-color 0.15s, box-shadow 0.15s',
      position: 'relative',
      ...(transformMinHeight > 0 ? { minHeight: transformMinHeight } : {}),
    }}>
      {/* Control target handle — execution flow in */}
      <Handle type="target" position={targetPos} style={{ background: meta.border }} />

      {/* Dynamic input ports: committed + ghost (semi-transparent during drag) */}
      {allInputPorts.map((portID, idx) => {
        const isGhost = portID === ghostVar && dragAccept === 'accept' && !dynamicInputPorts.includes(portID);
        return (
          <React.Fragment key={`in-${portID}`}>
            <Handle
              id={`data-in-${portID}`}
              type="target"
              position={targetPos}
              style={{ ...dataInStyle, ...inputPortPos(idx), opacity: isGhost ? 0.55 : 1 }}
              title={portID}
            />
            <span style={{ ...inputLabelPos(idx), opacity: isGhost ? 0.6 : 1 }}>{portID}</span>
          </React.Fragment>
        );
      })}

      {/* Static registry input ports — rendered after dynamic ones */}
      {inputPorts.map((port, idx) => {
        const portIdx = allInputPorts.length + idx;
        return (
          <React.Fragment key={`sin-${port.id}`}>
            <Handle
              id={`data-in-${port.id}`}
              type="target"
              position={targetPos}
              style={{ ...dataInStyle, ...inputPortPos(portIdx) }}
              title={port.label}
            />
            <span style={inputLabelPos(portIdx)}>{port.id}</span>
          </React.Fragment>
        );
      })}

      {/* Source handle(s) — branch has two named, transform has per-var, others have one */}
      {data.step_type === 'branch' ? (
        <>
          <Handle id="source-true"  type="source" position={sourcePos}
            style={{ background: '#4ade80', width: 10, height: 10,
              ...(isLR ? { top: '30%', right: -6 } : { left: '30%', bottom: -6 }) }} />
          <div style={{ position: 'absolute', fontSize: 9, color: '#4ade80', fontWeight: 700, pointerEvents: 'none',
            ...(isLR ? { right: -18, top: 'calc(30% - 6px)' } : { bottom: -18, left: 'calc(30% - 6px)' }) }}>T</div>
          <Handle id="source-false" type="source" position={sourcePos}
            style={{ background: '#f87171', width: 10, height: 10,
              ...(isLR ? { top: '70%', right: -6 } : { left: '70%', bottom: -6 }) }} />
          <div style={{ position: 'absolute', fontSize: 9, color: '#f87171', fontWeight: 700, pointerEvents: 'none',
            ...(isLR ? { right: -18, top: 'calc(70% - 6px)' } : { bottom: -18, left: 'calc(70% - 6px)' }) }}>F</div>
        </>
      ) : isTransform && transformOutputs.length > 0 ? (
        // Named transform output handles — pixel-based top, node height set by transformMinHeight.
        <>
          {transformOutputs.map((varName, idx) => {
            const topPx = 8 + idx * PX_PER_ROW + PX_PER_ROW / 2;
            return (
              <React.Fragment key={varName}>
                <Handle
                  id={`data-out-${varName}`}
                  type="source"
                  position={Position.Right}
                  style={{ ...dataOutStyle, top: topPx, right: HANDLE_OFF, width: 9, height: 9 }}
                  title={varName}
                />
                <span style={{
                  position: 'absolute', top: topPx - 4, right: HANDLE_SZ + 4,
                  fontSize: 8, color: '#818cf8', fontFamily: 'JetBrains Mono, monospace',
                  pointerEvents: 'none', whiteSpace: 'nowrap', textAlign: 'right', lineHeight: 1,
                }}>{varName}</span>
              </React.Fragment>
            );
          })}
        </>
      ) : (
        <Handle type="source" position={sourcePos} style={{ background: meta.border }} />
      )}

      {/* Static registry output ports */}
      {outputPorts.map((port, idx) => (
        <React.Fragment key={`sout-${port.id}`}>
          <Handle
            id={`data-out-${port.id}`}
            type="source"
            position={sourcePos}
            style={{ ...dataOutStyle, ...outputPortPos(idx) }}
            title={port.label}
          />
          <span style={outputLabelPos(idx)}>{port.id}</span>
        </React.Fragment>
      ))}

      <div style={{ fontSize: '32px', lineHeight: 1 }}>{meta.emoji}</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '11px', marginTop: '5px' }}>{data.label || meta.label}</div>
      {sub && <div style={{ fontSize: '10px', color: meta.border, opacity: 0.9, marginTop: 2 }}>{sub}</div>}
      {data._stub && state === 'idle' && (
        <div style={{ marginTop: 3, fontSize: '9px', color: '#f59e0b', fontWeight: 700, letterSpacing: '0.05em' }}>STUB</div>
      )}
      {dbg?.state === 'done' && dbg.output && (
        <div style={{ marginTop: 4, fontSize: '9px', color: '#4ade80', maxWidth: '90px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {dbg.output.length > 30 ? dbg.output.slice(0, 30) + '…' : dbg.output}
        </div>
      )}
      {dbg?.state === 'error' && dbg.error && (
        <div style={{ marginTop: 4, fontSize: '9px', color: '#f87171', maxWidth: '90px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
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
