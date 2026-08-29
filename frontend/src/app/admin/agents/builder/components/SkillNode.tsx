'use client';
import { Handle, Position } from '@xyflow/react';
import { C } from '../constants';
import type { SkillData } from '../types';
import { useLayoutDir } from '../LayoutContext';

export function SkillNode({ data }: { data: SkillData; id: string }) {
  const layoutDir = useLayoutDir();
  return (
    <div style={{ background: 'transparent', border: 'none', padding: '8px', minWidth: '100px', textAlign: 'center' }}>
      <Handle type="target" position={layoutDir === 'LR' ? Position.Left  : Position.Top}    style={{ background: C.purple }} />
      <Handle type="source" position={layoutDir === 'LR' ? Position.Right : Position.Bottom} style={{ background: C.purple }} />
      <div style={{ fontSize: '36px', lineHeight: 1 }}>⚡</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '12px', marginTop: '6px' }}>{data.name || 'Skill'}</div>
    </div>
  );
}
