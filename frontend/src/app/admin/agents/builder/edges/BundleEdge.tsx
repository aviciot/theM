'use client';
import { useState } from 'react';
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from '@xyflow/react';

interface BundleEdgeData {
  portLabel?: string;
  bundleIndex?: number;
  bundleTotal?: number;
  bundlePorts?: string[];
  isLeader?: boolean;
  layoutDir?: 'LR' | 'TB';
}

const WIRE_STEP   = 22;
const WIRE_COLORS = ['#818cf8','#a78bfa','#7dd3fc','#6ee7b7','#fcd34d','#f9a8d4'];

export function BundleEdge({
  id, sourceX, sourceY, targetX, targetY,
  sourcePosition, targetPosition, data,
}: EdgeProps) {
  const [hovered, setHovered] = useState(false);
  const d         = (data ?? {}) as BundleEdgeData;
  const ports     = d.bundlePorts ?? (d.portLabel ? [d.portLabel] : ['']);
  const total     = ports.length;
  const isLeader  = d.isLeader ?? true;
  const layoutDir = d.layoutDir ?? 'LR';
  const isLR      = layoutDir === 'LR';

  if (total <= 1) {
    const [edgePath] = getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });
    return <BaseEdge id={id} path={edgePath} style={{ stroke: '#818cf8', strokeWidth: 1.5, strokeDasharray: '5 3', opacity: 0.8 }} />;
  }

  if (!isLeader) {
    return <BaseEdge id={id} path={`M ${sourceX} ${sourceY} L ${targetX} ${targetY}`}
      style={{ stroke: 'transparent', strokeWidth: 0 }} />;
  }

  const midX = (sourceX + targetX) / 2;
  const midY = (sourceY + targetY) / 2;

  function portSrcPos(i: number) {
    return isLR
      ? { x: sourceX, y: sourceY + i * WIRE_STEP }
      : { x: sourceX + i * WIRE_STEP, y: sourceY };
  }
  function portTgtPos(i: number) {
    return isLR
      ? { x: targetX, y: targetY + i * WIRE_STEP }
      : { x: targetX + i * WIRE_STEP, y: targetY };
  }

  function wirePath(src: {x:number;y:number}, tgt: {x:number;y:number}): string {
    const cx1 = isLR ? midX : src.x;
    const cy1 = isLR ? src.y : midY;
    const cx2 = isLR ? midX : tgt.x;
    const cy2 = isLR ? tgt.y : midY;
    return `M ${src.x} ${src.y} C ${cx1} ${cy1}, ${cx2} ${cy2}, ${tgt.x} ${tgt.y}`;
  }

  const wireOpacity = hovered ? 0.9 : 0.55;
  const wireWidth   = hovered ? 2.0 : 1.4;

  const midWire = Math.floor(total / 2);
  const badgeX = (portSrcPos(midWire).x + portTgtPos(midWire).x) / 2;
  const badgeY = (portSrcPos(midWire).y + portTgtPos(midWire).y) / 2;

  return (
    <>
      {ports.map((_, i) => (
        <path
          key={i}
          d={wirePath(portSrcPos(i), portTgtPos(i))}
          fill="none"
          stroke={WIRE_COLORS[i % WIRE_COLORS.length]}
          strokeWidth={wireWidth}
          opacity={wireOpacity}
          style={{ transition: 'opacity 0.15s, stroke-width 0.15s' }}
        />
      ))}

      <EdgeLabelRenderer>
        <div
          onMouseEnter={() => setHovered(true)}
          onMouseLeave={() => setHovered(false)}
          style={{
            position: 'absolute',
            transform: `translate(-50%,-50%) translate(${badgeX}px,${badgeY}px)`,
            pointerEvents: 'all',
            width: 26, height: 26, borderRadius: '50%',
            background: hovered ? 'rgba(99,102,241,0.95)' : 'rgba(20,20,50,0.88)',
            border: `2px solid ${hovered ? '#818cf8' : '#475569'}`,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            fontSize: 10, fontWeight: 700, color: hovered ? '#fff' : '#94a3b8',
            boxShadow: hovered ? '0 0 12px 4px rgba(99,102,241,0.5)' : 'none',
            transition: 'all 0.15s',
            zIndex: 10,
            cursor: 'default',
          }}
        >
          {total}
        </div>
      </EdgeLabelRenderer>
    </>
  );
}
