'use client';
import type { Node } from '@xyflow/react';
import type { CycleNodeData } from '../../../types';
import { C } from '../../../constants';
import { fieldStyle } from './panelShared';

// ── CycleNodePanel ────────────────────────────────────────────────────────────

interface Props {
  selectedNode: Node;
  nodes: Node[];
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
}

const OPS = [
  { value: 'eq',     label: '= equals' },
  { value: 'neq',    label: '≠ not equals' },
  { value: 'truthy', label: 'is truthy' },
];

export function CycleNodePanel({ selectedNode, nodes, setNodes, setIsDirty }: Props) {
  const live = nodes.find(n => n.id === selectedNode.id);
  const d = (live?.data ?? selectedNode.data) as unknown as CycleNodeData;

  function patch(updates: Partial<CycleNodeData>) {
    setNodes(ns => ns.map(n => {
      if (n.id !== selectedNode.id) return n;
      return { ...n, data: { ...n.data, ...updates } as unknown as Record<string, unknown> };
    }));
    setIsDirty(true);
  }

  const isTruthy = (d.break_when_op ?? 'eq') === 'truthy';

  return (
    <div style={{ padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ fontSize: 13, fontWeight: 700, color: C.text }}>↻ Cycle</div>

      {/* Display name */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <label style={{ fontSize: 11, color: C.textMuted, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.07em' }}>Name</label>
        <input
          style={fieldStyle}
          value={d.display_name ?? ''}
          placeholder="e.g. Approval Retry Loop"
          onChange={e => patch({ display_name: e.target.value })}
        />
      </div>

      {/* Break condition */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <div style={{ fontSize: 11, color: C.textMuted, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.07em' }}>Break Condition</div>

        <label style={{ fontSize: 12, color: C.textMuted }}>Variable name</label>
        <input
          style={fieldStyle}
          value={d.break_when_var ?? ''}
          placeholder="e.g. done"
          onChange={e => patch({ break_when_var: e.target.value })}
        />

        <label style={{ fontSize: 12, color: C.textMuted }}>Operator</label>
        <select
          style={{ ...fieldStyle, cursor: 'pointer' }}
          value={d.break_when_op ?? 'eq'}
          onChange={e => patch({ break_when_op: e.target.value })}
        >
          {OPS.map(op => (
            <option key={op.value} value={op.value}>{op.label}</option>
          ))}
        </select>

        {!isTruthy && (
          <>
            <label style={{ fontSize: 12, color: C.textMuted }}>Value</label>
            <input
              style={fieldStyle}
              value={d.break_when_val ?? ''}
              placeholder="e.g. true"
              onChange={e => patch({ break_when_val: e.target.value })}
            />
          </>
        )}
      </div>

      {/* Max iterations */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <div style={{ fontSize: 11, color: C.textMuted, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.07em' }}>Safety Cap</div>
        <label style={{ fontSize: 12, color: C.textMuted }}>Max iterations (1–100)</label>
        <input
          type="number"
          style={fieldStyle}
          min={1} max={100}
          value={d.max_iterations ?? 10}
          onChange={e => {
            const v = parseInt(e.target.value, 10);
            if (!isNaN(v)) patch({ max_iterations: Math.min(100, Math.max(1, v)) });
          }}
        />
      </div>

      <div style={{ fontSize: 11, color: C.textMuted, fontStyle: 'italic', lineHeight: 1.5 }}>
        Drag nodes into the Cycle frame to include them in the loop body.
      </div>
    </div>
  );
}
