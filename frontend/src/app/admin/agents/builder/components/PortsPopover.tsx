'use client';
import React from 'react';
import { useConnection, Position } from '@xyflow/react';
import { resolveInputPorts, resolveOutputPorts } from '@/lib/nodeRegistry';
import type { NodeDef } from '@/lib/nodeRegistry';

// PortsPopover is display-only — it shows which data ports exist on this node.
// All Handle elements live exclusively in StepNode (the permanent invisible handles
// at rail offsets). Duplicating Handles here caused silent ReactFlow edge misrouting.

const DATA_COLORS = ['#818cf8', '#a78bfa', '#7dd3fc', '#6ee7b7', '#fcd34d', '#f9a8d4'];

interface PortsPopoverProps {
  nodeId: string;
  nodeDef: NodeDef;
  cfg: Record<string, unknown>;
  committedInputPortIDs: string[];
  wiredOutputPortIDs: string[];
  isLR: boolean;
  sourcePos: Position;
  targetPos: Position;
}

export function PortsPopover({
  nodeDef,
  cfg,
  committedInputPortIDs,
  wiredOutputPortIDs,
  isLR,
}: PortsPopoverProps) {
  const connection = useConnection();
  const isDragging = connection.inProgress;

  const inPorts  = resolveInputPorts(nodeDef, committedInputPortIDs);
  const outPorts = resolveOutputPorts(nodeDef, cfg, wiredOutputPortIDs)
    .filter(p => p.kind === 'data');

  const opacity       = isDragging ? 0.45 : 1;
  const pointerEvents = isDragging ? 'none' : 'all';

  const panelStyle: React.CSSProperties = {
    position: 'absolute',
    ...(isLR
      ? { left: 'calc(100% + 16px)', top: '50%', transform: 'translateY(-50%)' }
      : { top: 'calc(100% + 16px)', left: '50%', transform: 'translateX(-50%)' }),
    background: 'rgba(12,12,28,0.97)',
    border: '1px solid #334155',
    borderRadius: 10,
    padding: '10px 12px',
    minWidth: 160,
    zIndex: 1000,
    boxShadow: '0 8px 32px rgba(0,0,0,0.7)',
    opacity,
    pointerEvents,
    transition: 'opacity 0.18s',
  };

  function sectionLabel(text: string) {
    return (
      <div style={{ fontSize: 9, fontWeight: 700, color: '#64748b', letterSpacing: '0.08em', marginBottom: 6 }}>
        {text}
      </div>
    );
  }

  function portDot(color: string) {
    return (
      <div style={{
        width: 8, height: 8, borderRadius: '50%',
        background: color, flexShrink: 0,
      }} />
    );
  }

  return (
    <div className="nodrag nowheel nopan" style={panelStyle}>
      {/* IN ports — display only */}
      {inPorts.length > 0 && (
        <div style={{ marginBottom: outPorts.length > 0 ? 10 : 0 }}>
          {sectionLabel('IN')}
          {inPorts.map((p, i) => (
            <div key={p.id} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: i < inPorts.length - 1 ? 4 : 0 }}>
              {portDot(p.color || DATA_COLORS[i % DATA_COLORS.length])}
              <span style={{ fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: '#cbd5e1' }}>
                {p.label}
              </span>
            </div>
          ))}
        </div>
      )}

      {/* OUT ports — display only; drag from the invisible handles on the node card */}
      {outPorts.length > 0 && (
        <div>
          {sectionLabel('OUT')}
          {outPorts.map((p, i) => {
            const color = p.color || DATA_COLORS[i % DATA_COLORS.length];
            return (
              <div key={p.id} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: i < outPorts.length - 1 ? 4 : 0 }}>
                <span style={{ fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: '#cbd5e1', flex: 1 }}>
                  {p.label}
                </span>
                <div style={{
                  width: 12, height: 12, borderRadius: '50%',
                  background: color,
                  boxShadow: `0 0 6px 2px ${color}88`,
                  flexShrink: 0,
                }} />
              </div>
            );
          })}
        </div>
      )}

      {inPorts.length === 0 && outPorts.length === 0 && (
        <div style={{ fontSize: 11, color: '#475569', fontStyle: 'italic' }}>No data ports</div>
      )}
    </div>
  );
}

export type { PortsPopoverProps };
