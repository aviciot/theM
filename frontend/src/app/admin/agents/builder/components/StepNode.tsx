import { Handle, Position } from '@xyflow/react';
import { getNodeDef } from '@/lib/nodeRegistry';
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

  return (
    <div style={{
      background: 'transparent', padding: '8px', minWidth: '80px', textAlign: 'center',
      border: `2px solid ${borderColor}`, borderRadius: '10px', boxShadow,
      transition: 'border-color 0.2s, box-shadow 0.2s',
      position: 'relative',
      paddingRight: isTransform && transformOutputs.length > 0 ? '72px' : '8px',
      ...(transformMinHeight > 0 ? { minHeight: transformMinHeight } : {}),
    }}>
      <Handle type="target" position={targetPos} style={{ background: meta.border }} />
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
        <>
          {transformOutputs.map((varName, idx) => {
            // Place handles at fixed pixel intervals from the top, centred in the usable height.
            const topPx = 8 + idx * PX_PER_ROW + PX_PER_ROW / 2;
            return (
              <span key={varName}>
                <Handle
                  id={`out-${varName}`}
                  type="source"
                  position={Position.Right}
                  style={{
                    background: meta.border,
                    top: topPx,
                    right: -6,
                    width: 9,
                    height: 9,
                  }}
                />
                <span style={{
                  position: 'absolute',
                  top: topPx - 5,
                  right: 8,
                  fontSize: 8,
                  color: meta.border,
                  fontFamily: 'JetBrains Mono, monospace',
                  pointerEvents: 'none',
                  whiteSpace: 'nowrap',
                  textAlign: 'right',
                  lineHeight: 1,
                }}>{varName}</span>
              </span>
            );
          })}
        </>
      ) : (
        <Handle type="source" position={sourcePos} style={{ background: meta.border }} />
      )}
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
