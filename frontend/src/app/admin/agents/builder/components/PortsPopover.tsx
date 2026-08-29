'use client';
import React from 'react';
import { Handle, Position, useConnection } from '@xyflow/react';
import { resolveInputPorts, resolveOutputPorts } from '@/lib/nodeRegistry';
import type { NodeDef } from '@/lib/nodeRegistry';

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
  nodeId,
  nodeDef,
  cfg,
  committedInputPortIDs,
  wiredOutputPortIDs,
  isLR,
  sourcePos,
  targetPos,
}: PortsPopoverProps) {
  const connection = useConnection();
  const isDragging = connection.inProgress;

  const inPorts  = resolveInputPorts(nodeDef, committedInputPortIDs);
  const outPorts = resolveOutputPorts(nodeDef, cfg, wiredOutputPortIDs)
    .filter(p => p.kind === 'data');

  const opacity      = isDragging ? 0.45 : 1;
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

  return (
    <div className="nodrag nowheel nopan" style={panelStyle}>
      {/* IN ports — status only, no handles here */}
      {inPorts.length > 0 && (
        <div style={{ marginBottom: outPorts.length > 0 ? 10 : 0 }}>
          {sectionLabel('IN')}
          {inPorts.map((p, i) => (
            <div key={p.id} style={{
              display: 'flex', alignItems: 'center', gap: 6, marginBottom: i < inPorts.length - 1 ? 4 : 0,
            }}>
              <div style={{
                width: 8, height: 8, borderRadius: '50%',
                background: p.color || DATA_COLORS[i % DATA_COLORS.length],
                flexShrink: 0,
              }} />
              <span style={{ fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: '#cbd5e1' }}>
                {p.label}
              </span>
            </div>
          ))}
        </div>
      )}

      {/* OUT ports — real draggable Handle elements */}
      {outPorts.length > 0 && (
        <div>
          {sectionLabel('OUT')}
          {outPorts.map((p, i) => {
            const color = p.color || DATA_COLORS[i % DATA_COLORS.length];
            return (
              <div key={p.id} style={{
                display: 'flex', alignItems: 'center', gap: 6,
                marginBottom: i < outPorts.length - 1 ? 4 : 0,
                position: 'relative',
              }}>
                <span style={{ fontSize: 11, fontFamily: 'JetBrains Mono, monospace', color: '#cbd5e1', flex: 1 }}>
                  {p.label}
                </span>
                {/* Drag grip with real Handle */}
                <div style={{
                  position: 'relative', width: 20, height: 20,
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  flexShrink: 0,
                }}>
                  <div style={{
                    width: 12, height: 12, borderRadius: '50%',
                    background: color,
                    boxShadow: `0 0 6px 2px ${color}88`,
                    cursor: 'crosshair',
                    pointerEvents: 'none',
                  }} />
                  <Handle
                    id={p.id}
                    type="source"
                    position={sourcePos}
                    style={{
                      position: 'absolute',
                      inset: 0, margin: 'auto',
                      width: 20, height: 20,
                      opacity: 0,
                      background: 'transparent',
                      border: 'none',
                      cursor: 'crosshair',
                    }}
                  />
                </div>
              </div>
            );
          })}
        </div>
      )}

      {inPorts.length === 0 && outPorts.length === 0 && (
        <div style={{ fontSize: 11, color: '#475569', fontStyle: 'italic' }}>No data ports</div>
      )}

      {/* Target handles for IN ports — invisible, positioned at the node edge for drop */}
      {inPorts.map(p => (
        <Handle
          key={`t-${p.id}`}
          id={p.id}
          type="target"
          position={targetPos}
          style={{
            position: 'absolute',
            width: 1, height: 1,
            opacity: 0, background: 'transparent', border: 'none',
          }}
        />
      ))}
    </div>
  );
}

export type { PortsPopoverProps };
