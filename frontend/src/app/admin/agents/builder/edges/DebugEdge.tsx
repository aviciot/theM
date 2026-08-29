'use client';
import { useState } from 'react';
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from '@xyflow/react';

const debugEdgeStyle = `
  @keyframes flowDash {
    from { stroke-dashoffset: 24; }
    to   { stroke-dashoffset: 0; }
  }
  @keyframes flowPulse {
    0%, 100% { opacity: 1; }
    50%       { opacity: 0.5; }
  }
`;

interface DebugEdgeData {
  debugState?: 'idle' | 'flowing' | 'done';
  label?: string;
}

export function DebugEdge({
  id, sourceX, sourceY, targetX, targetY,
  sourcePosition, targetPosition, data, markerEnd,
}: EdgeProps) {
  const d = (data ?? {}) as DebugEdgeData;
  const [edgePath, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });
  const [hovered, setHovered] = useState(false);

  const isFlowing = d.debugState === 'flowing';
  const isDone    = d.debugState === 'done';

  const fullValue = d.label ? d.label.replace(/^"|"$/g, '') : '';

  return (
    <>
      <style>{debugEdgeStyle}</style>
      <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} style={{ stroke: isDone ? '#00f0ff' : isFlowing ? '#7c3aed' : '#334155', strokeWidth: isDone ? 2 : isFlowing ? 2.5 : 1.5 }} />
      {isFlowing && (
        <path
          d={edgePath}
          fill="none"
          stroke="#a78bfa"
          strokeWidth={3}
          strokeDasharray="8 4"
          style={{ animation: 'flowDash 0.4s linear infinite', opacity: 0.9 }}
        />
      )}
      {isFlowing && (
        <circle r={5} fill="#a78bfa" style={{ animation: 'flowPulse 0.6s ease-in-out infinite' }}>
          <animateMotion dur="0.8s" repeatCount="indefinite">
            <mpath href={`#edge-path-${id}`} />
          </animateMotion>
        </circle>
      )}
      {isDone && d.label && (
        <EdgeLabelRenderer>
          <div
            onMouseEnter={() => setHovered(true)}
            onMouseLeave={() => setHovered(false)}
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              pointerEvents: 'all',
              background: 'rgba(0,15,30,0.85)',
              border: '1px solid #00f0ff',
              borderRadius: '4px',
              padding: '2px 6px',
              fontSize: '10px',
              fontFamily: 'monospace',
              color: '#00f0ff',
              whiteSpace: 'nowrap',
              maxWidth: '140px',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              cursor: 'default',
              zIndex: 10,
            }}
          >
            {d.label}
          </div>

          {hovered && fullValue && (
            <div
              onMouseEnter={() => setHovered(true)}
              onMouseLeave={() => setHovered(false)}
              style={{
                position: 'absolute',
                transform: `translate(-50%, 0) translate(${labelX}px, ${labelY + 16}px)`,
                pointerEvents: 'all',
                zIndex: 9999,
                background: 'rgba(0, 8, 20, 0.97)',
                border: '1px solid #00f0ff',
                borderRadius: '8px',
                padding: '10px 14px',
                maxWidth: '480px',
                minWidth: '200px',
                boxShadow: '0 0 24px rgba(0,240,255,0.2)',
              }}
            >
              <div style={{
                fontSize: '10px',
                fontWeight: 700,
                color: 'rgba(0,240,255,0.5)',
                letterSpacing: '0.08em',
                textTransform: 'uppercase',
                marginBottom: '6px',
                fontFamily: 'sans-serif',
              }}>
                Edge value
              </div>
              <div style={{
                fontFamily: 'monospace',
                fontSize: '12px',
                color: '#00f0ff',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                maxHeight: '320px',
                overflowY: 'auto',
                lineHeight: 1.6,
              }}>
                {fullValue}
              </div>
            </div>
          )}
        </EdgeLabelRenderer>
      )}
    </>
  );
}
