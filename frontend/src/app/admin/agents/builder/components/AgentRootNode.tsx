'use client';
import { Handle, Position } from '@xyflow/react';
import { C } from '../constants';
import type { AgentRootData } from '../types';
import { useLayoutDir } from '../LayoutContext';

export function AgentRootNode({ data }: { data: AgentRootData; id: string }) {
  const layoutDir = useLayoutDir();
  return (
    <div style={{ background: 'transparent', border: 'none', padding: '8px', minWidth: '120px', textAlign: 'center' }}>
      <Handle type="source" position={layoutDir === 'LR' ? Position.Right : Position.Bottom} style={{ background: C.cyan }} />
      <div style={{ fontSize: '42px', textAlign: 'center', lineHeight: 1 }}>🤖</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '13px', textAlign: 'center', marginTop: '6px' }}>{data.display_name || 'Unnamed Agent'}</div>
    </div>
  );
}
