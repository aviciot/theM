'use client';
import React, { useRef, useState, useEffect } from 'react';
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from '@xyflow/react';
import type { MappingRecord } from '../canvas/connections';
import { callDeleteMapping } from '../canvas/connections';

interface BundleEdgeData {
  mappings?: MappingRecord[];
  isLeader?: boolean;
  layoutDir?: 'LR' | 'TB';
  // V1 legacy fields (kept for graceful fallback)
  portLabel?: string;
  bundleIndex?: number;
  bundleTotal?: number;
  bundlePorts?: string[];
}

export function BundleEdge({
  id, sourceX, sourceY, targetX, targetY,
  sourcePosition, targetPosition, data,
}: EdgeProps) {
  const d = (data ?? {}) as BundleEdgeData;
  const mappings: MappingRecord[] = d.mappings ?? (
    d.portLabel ? [{ edgeId: id, sourceHandle: '', targetHandle: '', portLabel: d.portLabel }] : []
  );
  const count = mappings.length;

  const [sheetOpen, setSheetOpen] = useState(false);
  const sheetRef = useRef<HTMLDivElement>(null);

  // Close MappingSheet when clicking outside
  useEffect(() => {
    if (!sheetOpen) return;
    function handleClick(e: MouseEvent) {
      if (sheetRef.current && !sheetRef.current.contains(e.target as Node)) {
        setSheetOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [sheetOpen]);

  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX, sourceY, sourcePosition,
    targetX, targetY, targetPosition,
  });

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        style={{ stroke: '#818cf8', strokeWidth: 1.8, strokeDasharray: '6 3', opacity: 0.75 }}
      />

      <EdgeLabelRenderer>
        <div
          className="nodrag nowheel nopan"
          style={{
            position: 'absolute',
            transform: `translate(-50%,-50%) translate(${labelX}px,${labelY}px)`,
            pointerEvents: 'all',
            zIndex: 20,
          }}
        >
          {/* Badge */}
          <button
            onClick={() => setSheetOpen(v => !v)}
            style={{
              width: 28, height: 28, borderRadius: '50%',
              background: sheetOpen ? 'rgba(99,102,241,0.95)' : 'rgba(20,20,50,0.88)',
              border: `2px solid ${sheetOpen ? '#818cf8' : '#475569'}`,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontSize: 10, fontWeight: 700,
              color: sheetOpen ? '#fff' : '#94a3b8',
              boxShadow: sheetOpen ? '0 0 12px 4px rgba(99,102,241,0.5)' : 'none',
              cursor: 'pointer',
              transition: 'all 0.15s',
            }}
            title={`${count} data port${count !== 1 ? 's' : ''} — click to inspect`}
          >
            {count}
          </button>

          {/* MappingSheet */}
          {sheetOpen && (
            <div
              ref={sheetRef}
              style={{
                position: 'absolute',
                top: 36, left: '50%',
                transform: 'translateX(-50%)',
                background: 'rgba(15,15,35,0.97)',
                border: '1px solid #334155',
                borderRadius: 10,
                padding: '10px 12px',
                minWidth: 180,
                boxShadow: '0 8px 32px rgba(0,0,0,0.6)',
                zIndex: 100,
              }}
            >
              <div style={{ fontSize: 10, fontWeight: 700, color: '#64748b', marginBottom: 8, letterSpacing: '0.06em' }}>
                DATA MAPPINGS
              </div>
              {mappings.map((m, i) => (
                <div key={m.edgeId} style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  marginBottom: i < mappings.length - 1 ? 6 : 0,
                }}>
                  <span style={{
                    flex: 1, fontSize: 11, fontFamily: 'JetBrains Mono, monospace',
                    color: '#e2e8f0', background: 'rgba(99,102,241,0.12)',
                    borderRadius: 4, padding: '2px 6px',
                    overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                  }}>
                    {m.portLabel}
                  </span>
                  <button
                    onClick={() => {
                      callDeleteMapping(m.edgeId);
                      if (mappings.length <= 1) setSheetOpen(false);
                    }}
                    style={{
                      background: 'transparent', border: 'none',
                      color: '#f87171', cursor: 'pointer',
                      fontSize: 14, lineHeight: 1, padding: '2px 4px',
                      borderRadius: 4,
                    }}
                    title="Remove mapping"
                  >×</button>
                </div>
              ))}
            </div>
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  );
}
