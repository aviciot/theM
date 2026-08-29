'use client';
import { BaseEdge, getSmoothStepPath, type EdgeProps } from '@xyflow/react';

export function DataEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition }: EdgeProps) {
  const [edgePath] = getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });
  return (
    <BaseEdge
      id={id}
      path={edgePath}
      style={{ stroke: '#818cf8', strokeWidth: 1.5, strokeDasharray: '5 3', opacity: 0.8 }}
    />
  );
}
