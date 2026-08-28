import React, { useEffect } from 'react';
import { Handle, Position, useUpdateNodeInternals } from '@xyflow/react';
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

// Port layout constants — axis-agnostic pixel values.
// In LR mode: applied to top/right offsets. In TB mode: applied to left/bottom offsets.
const PORT_START  = 20;  // px from node edge where first port sits
const PORT_STEP   = 18;  // px between ports
const HANDLE_SZ   = 8;   // handle dot side length
const HANDLE_OFF  = -(HANDLE_SZ / 2 + 1); // flush with node border

export function StepNode({ id, data }: { id: string; data: StepNodeData }) {
  const nodeDef  = getNodeDef(data.step_type);
  const meta     = { bg: nodeDef.bg, border: nodeDef.border, emoji: nodeDef.emoji, label: nodeDef.label };
  const cfg      = data.config ?? {};
  const layoutDir = useLayoutDir();
  const isLR     = layoutDir === 'LR';
  const targetPos = isLR ? Position.Left   : Position.Top;
  const sourcePos = isLR ? Position.Right  : Position.Bottom;
  const dbg      = data._debug;
  const sub      = nodeDef.summary(cfg);
  const updateNodeInternals = useUpdateNodeInternals();

  // Ghost port during drag-hover.
  const dynamicInputPortIDs: string[] = data.inputs ? Object.keys(data.inputs) : [];
  const ghostVar  = data._draggingVar;
  const dragAccept = data._dragAccept;
  const allInputPortIDs = ghostVar && dragAccept === 'accept'
    ? [...dynamicInputPortIDs, ghostVar]
    : dynamicInputPortIDs;

  // Resolve ports generically from backend definition.
  const inputPorts  = resolveInputPorts(nodeDef, allInputPortIDs);
  const outputPorts = resolveOutputPorts(nodeDef, cfg as Record<string, unknown>);

  // Control output ports are those with kind:'control' in outputPorts.
  const ctrlOutputPorts = outputPorts.filter(p => p.kind === 'control');
  const dataOutputPorts = outputPorts.filter(p => p.kind === 'data');

  // Notify React Flow when the handle list changes so it re-measures geometry.
  const portKey = [
    ...inputPorts.map(p => p.id),
    ...outputPorts.map(p => p.id),
  ].join(',');
  useEffect(() => {
    updateNodeInternals(id);
  }, [portKey, id, updateNodeInternals]);

  // Node min-height: enough to accommodate the tallest port rail.
  const maxDataPorts = Math.max(inputPorts.filter(p => p.kind === 'data').length, dataOutputPorts.length);
  const minHeight    = maxDataPorts > 0
    ? Math.max(70, PORT_START + maxDataPorts * PORT_STEP + 20)
    : 0;

  // Visual state.
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
  if (dragAccept === 'accept') {
    borderColor = '#4ade80';
    boxShadow   = '0 0 0 3px rgba(74,222,128,0.5), 0 0 16px 4px rgba(74,222,128,0.3)';
  } else if (dragAccept === 'reject') {
    borderColor = '#f87171';
    boxShadow   = '0 0 0 3px rgba(248,113,113,0.5), 0 0 12px 3px rgba(248,113,113,0.3)';
  }

  // Padding to clear port labels.
  const dataInputCount  = inputPorts.filter(p => p.kind === 'data').length;
  const inputPortPad    = dataInputCount > 0 ? HANDLE_SZ + 24 : 0;
  const dataOutputCount = dataOutputPorts.length;
  const outputPortPad   = dataOutputCount > 0 ? HANDLE_SZ + 24 : 0;

  // Position helpers — axis-aware.
  function inputPortStyle(idx: number): React.CSSProperties {
    const offset = PORT_START + idx * PORT_STEP;
    return isLR
      ? { position: 'absolute', top: offset, left: HANDLE_OFF }
      : { position: 'absolute', left: offset, top: HANDLE_OFF };
  }
  function inputLabelStyle(idx: number): React.CSSProperties {
    const offset = PORT_START + idx * PORT_STEP;
    return isLR
      ? { position: 'absolute', top: offset - 3, left: HANDLE_SZ + 4, fontSize: 7, color: '#f97316', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1 }
      : { position: 'absolute', left: offset - 2, top: HANDLE_SZ + 3, fontSize: 7, color: '#f97316', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1 };
  }
  function outputPortStyle(idx: number): React.CSSProperties {
    const offset = PORT_START + idx * PORT_STEP;
    return isLR
      ? { position: 'absolute', top: offset, right: HANDLE_OFF }
      : { position: 'absolute', left: offset, bottom: HANDLE_OFF };
  }
  function outputLabelStyle(idx: number): React.CSSProperties {
    const offset = PORT_START + idx * PORT_STEP;
    return isLR
      ? { position: 'absolute', top: offset - 3, right: HANDLE_SZ + 4, fontSize: 7, color: '#818cf8', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1, textAlign: 'right' }
      : { position: 'absolute', left: offset - 2, bottom: HANDLE_SZ + 3, fontSize: 7, color: '#818cf8', fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap', lineHeight: 1 };
  }

  // Named control output port positions — spread across 20%–80% of the node dimension.
  function ctrlOutPortStyle(idx: number, total: number): React.CSSProperties {
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

  const dataHandleBase = { width: HANDLE_SZ, height: HANDLE_SZ, borderRadius: 2, border: '1px solid rgba(0,0,0,0.4)' };

  return (
    <div style={{
      background: 'transparent',
      minWidth: '80px',
      textAlign: 'center',
      paddingTop:    isLR ? 8 : (inputPortPad  > 0 ? inputPortPad  : 8),
      paddingBottom: isLR ? 8 : (outputPortPad > 0 ? outputPortPad : 8),
      paddingLeft:   isLR ? (inputPortPad  > 0 ? inputPortPad  : 8) : 8,
      paddingRight:  isLR ? (outputPortPad > 0 ? outputPortPad : 8) : 8,
      border: `2px solid ${borderColor}`,
      borderRadius: '10px',
      boxShadow,
      transition: 'border-color 0.15s, box-shadow 0.15s',
      position: 'relative',
      ...(minHeight > 0 ? { minHeight } : {}),
    }}>

      {/* ── Control target handle (anonymous, center of target edge) ── */}
      {!nodeDef.is_source && (
        <Handle
          id="ctrl-in"
          type="target"
          position={targetPos}
          style={{ background: meta.border }}
        />
      )}

      {/* ── Data input port handles (generic, from resolveInputPorts) ── */}
      {inputPorts.filter(p => p.kind === 'data').map((port, idx) => {
        const isGhost = port.id === `data-in-${ghostVar}` && dragAccept === 'accept' && !dynamicInputPortIDs.includes(ghostVar ?? '');
        return (
          <React.Fragment key={port.id}>
            <Handle
              id={port.id}
              type="target"
              position={targetPos}
              style={{ ...dataHandleBase, background: port.color, ...inputPortStyle(idx), opacity: isGhost ? 0.55 : 1 }}
              title={port.label}
            />
            <span style={{ ...inputLabelStyle(idx), opacity: isGhost ? 0.6 : 1 }}>{port.label}</span>
          </React.Fragment>
        );
      })}

      {/* ── Named control output handles (e.g. branch true/false) ── */}
      {ctrlOutputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="source"
            position={sourcePos}
            style={{ background: port.color, ...ctrlOutPortStyle(idx, ctrlOutputPorts.length) }}
          />
          {port.label && (
            <div style={ctrlOutLabelStyle(idx, ctrlOutputPorts.length, port.color)}>
              {port.label.slice(0, 1).toUpperCase()}
            </div>
          )}
        </React.Fragment>
      ))}

      {/* ── Anonymous single control output (all nodes without named ctrl ports, except sinks) ── */}
      {ctrlOutputPorts.length === 0 && !nodeDef.is_sink && (
        <Handle
          id="ctrl-out"
          type="source"
          position={sourcePos}
          style={{ background: meta.border }}
        />
      )}

      {/* ── Data output port handles (generic, from resolveOutputPorts) ── */}
      {dataOutputPorts.map((port, idx) => (
        <React.Fragment key={port.id}>
          <Handle
            id={port.id}
            type="source"
            position={sourcePos}
            style={{ ...dataHandleBase, background: port.color, ...outputPortStyle(idx) }}
            title={port.label}
          />
          <span style={outputLabelStyle(idx)}>{port.label}</span>
        </React.Fragment>
      ))}

      {/* ── Node card content ── */}
      <div style={{ fontSize: '32px', lineHeight: 1 }}>{meta.emoji}</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '11px', marginTop: '5px' }}>
        {data.label || meta.label}
      </div>
      {sub && (
        <div style={{ fontSize: '10px', color: meta.border, opacity: 0.9, marginTop: 2 }}>
          {sub}
        </div>
      )}
      {data._stub && state === 'idle' && (
        <div style={{ marginTop: 3, fontSize: '9px', color: '#f59e0b', fontWeight: 700, letterSpacing: '0.05em' }}>
          STUB
        </div>
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
