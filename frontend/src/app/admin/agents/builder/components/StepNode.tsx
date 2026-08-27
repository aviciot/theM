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

export function StepNode({ data }: { data: StepNodeData; id: string }) {
  const nodeDef = getNodeDef(data.step_type);
  const meta = { bg: nodeDef.bg, border: nodeDef.border, emoji: nodeDef.emoji, label: nodeDef.label };
  const cfg = data.config ?? {};
  const layoutDir = useLayoutDir();
  const targetPos = layoutDir === 'LR' ? Position.Left  : Position.Top;
  const sourcePos = layoutDir === 'LR' ? Position.Right : Position.Bottom;
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

  const isTransform = data.step_type === 'transform';
  const transformOutputs = isTransform
    ? computeFinalOutputs((cfg.functions as FunctionStep[] | undefined) ?? [])
    : [];

  // Each output row needs 18px; header (emoji + label) needs ~70px minimum.
  const PX_PER_ROW = 18;
  const HEADER_PX = 70;
  const transformMinHeight = isTransform && transformOutputs.length > 0
    ? Math.max(HEADER_PX, transformOutputs.length * PX_PER_ROW + 16)
    : 0;

  // Static data ports from the node registry (LLM input/output, Response input).
  // Transform and HTTP have dynamic ports — not rendered here.
  const inputPorts: PortDef[] = nodeDef.input_ports ?? [];
  const outputPorts: PortDef[] = nodeDef.output_ports ?? [];

  // Dynamic input ports — created when user drags a data-out handle onto this node body.
  // Stored in data.inputs as { portID: { from_step, from_port } }.
  const dynamicInputPorts: string[] = data.inputs ? Object.keys(data.inputs) : [];
  const hasDataPorts = inputPorts.length > 0 || outputPorts.length > 0;

  // Data-port handle style — small square, distinct color
  const dataInStyle  = { background: '#f97316', width: 7, height: 7, borderRadius: 2, border: '1px solid rgba(0,0,0,0.4)' };
  const dataOutStyle = { background: '#818cf8', width: 7, height: 7, borderRadius: 2, border: '1px solid rgba(0,0,0,0.4)' };

  // Drop-zone offset: left side (LR layout) or top side (TB layout) where drag-to-create lands.
  const dropZoneOffset = dynamicInputPorts.length * 16 + (inputPorts.length > 0 ? inputPorts.length * 16 + 8 : 0);

  return (
    <div style={{
      background: 'transparent', padding: '8px', minWidth: '80px', textAlign: 'center',
      border: `2px solid ${borderColor}`, borderRadius: '10px', boxShadow,
      transition: 'border-color 0.2s, box-shadow 0.2s',
      position: 'relative',
      paddingRight: isTransform && transformOutputs.length > 0 ? '72px' : '8px',
      paddingLeft: hasDataPorts && inputPorts.length > 0 ? '8px' : '8px',
      ...(transformMinHeight > 0 ? { minHeight: transformMinHeight } : {}),
    }}>
      {/* Control target handle — execution flow in */}
      <Handle type="target" position={targetPos} style={{ background: meta.border }} />
      {/* Drop-zone handle — invisible, wide target that catches data-out drags onto the node body.
          Positioned below existing dynamic+static input ports so it doesn't overlap them. */}
      <Handle
        id="data-drop-zone"
        type="target"
        position={targetPos}
        style={{
          background: 'transparent',
          border: '2px dashed rgba(249,115,22,0.4)',
          width: layoutDir === 'LR' ? 10 : 40,
          height: layoutDir === 'LR' ? 40 : 10,
          borderRadius: 4,
          opacity: 0,
          ...(layoutDir === 'LR'
            ? { top: `calc(50% + ${dropZoneOffset}px)`, left: -6 }
            : { left: `calc(50% + ${dropZoneOffset}px)`, top: -6 }),
        }}
        title="Drop variable here to create input port"
      />
      {/* Dynamic input port handles — created when user drags a data-out onto this node */}
      {dynamicInputPorts.map((portID, idx) => {
        const posStyle: React.CSSProperties = layoutDir === 'LR'
          ? { top: `calc(25% + ${idx * 16}px)`, left: -5 }
          : { left: `calc(25% + ${idx * 16}px)`, top: -5 };
        return (
          <span key={`dyn-${portID}`}>
            <Handle
              id={`data-in-${portID}`}
              type="target"
              position={targetPos}
              style={{ ...dataInStyle, ...posStyle }}
              title={portID}
            />
            <span style={{
              position: 'absolute', fontSize: 7, color: '#f97316',
              fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap',
              ...(layoutDir === 'LR' ? { left: 6, top: `calc(25% + ${idx * 16}px - 4px)` } : { top: 6, left: `calc(25% + ${idx * 16}px)` }),
            }}>{portID}</span>
          </span>
        );
      })}
      {/* Data input port handles — one per static input port */}
      {inputPorts.map((port, idx) => {
        const posStyle: React.CSSProperties = layoutDir === 'LR'
          ? { top: `calc(30% + ${idx * 16}px)`, left: -5 }
          : { left: `calc(30% + ${idx * 16}px)`, top: -5 };
        return (
          <span key={port.id}>
            <Handle
              id={`data-in-${port.id}`}
              type="target"
              position={targetPos}
              style={{ ...dataInStyle, ...posStyle }}
              title={port.label}
            />
            <span style={{
              position: 'absolute', fontSize: 7, color: '#f97316',
              fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap',
              ...(layoutDir === 'LR' ? { left: 6, top: `calc(30% + ${idx * 16}px - 4px)` } : { top: 6, left: `calc(30% + ${idx * 16}px)` }),
            }}>{port.id}</span>
          </span>
        );
      })}
      {data.step_type === 'branch' ? (
        <>
          <Handle
            id="source-true"
            type="source"
            position={sourcePos}
            style={{ background: '#4ade80', width: 10, height: 10, ...(layoutDir === 'LR' ? { top: '30%', right: -6 } : { left: '30%', bottom: -6 }) }}
          />
          <div style={{ position: 'absolute', fontSize: 9, color: '#4ade80', fontWeight: 700, pointerEvents: 'none', ...(layoutDir === 'LR' ? { right: -18, top: 'calc(30% - 6px)' } : { bottom: -18, left: 'calc(30% - 6px)' }) }}>T</div>
          <Handle
            id="source-false"
            type="source"
            position={sourcePos}
            style={{ background: '#f87171', width: 10, height: 10, ...(layoutDir === 'LR' ? { top: '70%', right: -6 } : { left: '70%', bottom: -6 }) }}
          />
          <div style={{ position: 'absolute', fontSize: 9, color: '#f87171', fontWeight: 700, pointerEvents: 'none', ...(layoutDir === 'LR' ? { right: -18, top: 'calc(70% - 6px)' } : { bottom: -18, left: 'calc(70% - 6px)' }) }}>F</div>
        </>
      ) : isTransform && transformOutputs.length > 0 ? (
        // Dynamic named output handles — fixed 18px per row, node grows to fit
        // Use data-out- prefix so onPipeConnect recognises them as data edges
        <>
          {transformOutputs.map((varName, idx) => {
            const topPx = 8 + idx * PX_PER_ROW + PX_PER_ROW / 2;
            return (
              <span key={varName}>
                <Handle
                  id={`data-out-${varName}`}
                  type="source"
                  position={Position.Right}
                  style={{ ...dataOutStyle, top: topPx, right: -5, width: 9, height: 9 }}
                  title={varName}
                />
                <span style={{
                  position: 'absolute', top: topPx - 5, right: 8,
                  fontSize: 8, color: '#818cf8',
                  fontFamily: 'JetBrains Mono, monospace',
                  pointerEvents: 'none', whiteSpace: 'nowrap', textAlign: 'right', lineHeight: 1,
                }}>{varName}</span>
              </span>
            );
          })}
        </>
      ) : (
        <Handle type="source" position={sourcePos} style={{ background: meta.border }} />
      )}
      {/* Data output port handles — one per static output port */}
      {outputPorts.map((port, idx) => {
        const posStyle: React.CSSProperties = layoutDir === 'LR'
          ? { top: `calc(70% + ${idx * 16}px)`, right: -5 }
          : { right: `calc(30% + ${idx * 16}px)`, bottom: -5 };
        return (
          <span key={port.id}>
            <Handle
              id={`data-out-${port.id}`}
              type="source"
              position={sourcePos}
              style={{ ...dataOutStyle, ...posStyle }}
              title={port.label}
            />
            <span style={{
              position: 'absolute', fontSize: 7, color: '#818cf8',
              fontFamily: 'JetBrains Mono, monospace', pointerEvents: 'none', whiteSpace: 'nowrap',
              ...(layoutDir === 'LR' ? { right: 6, top: `calc(70% + ${idx * 16}px - 4px)` } : { bottom: 6, right: `calc(30% + ${idx * 16}px)` }),
            }}>{port.id}</span>
          </span>
        );
      })}
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
        <div style={{ marginTop: 4, fontSize: '9px', color: '#f59e0b' }}>{layoutDir === 'LR' ? 'next →' : 'next ↓'}</div>
      )}
    </div>
  );
}

export { stepMetaFromType as stepMeta };
