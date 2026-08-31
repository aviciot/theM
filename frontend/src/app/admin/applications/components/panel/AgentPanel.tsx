'use client';
import type { Node } from '@xyflow/react';
import type { AgentData } from '../../types';
import { C } from '../../constants';
import { agentIconForLibrary } from '../CanvasHelpers';
import { labelStyle, fieldWrap } from './panelStyles';

interface Props {
  selectedNode: Node;
}

export function AgentPanel({ selectedNode }: Props) {
  const d = selectedNode.data as AgentData;
  const icon = d.icon || agentIconForLibrary({ slug: d.name, icon: d.icon } as Parameters<typeof agentIconForLibrary>[0]);

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.greenBg, border: `1px solid ${C.greenBorder}` }}>
        <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.green }}>{icon}</span>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName}</div>
          <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.name}</div>
        </div>
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Description</label>
        <div style={{ fontSize: 12, color: 'var(--tm-card-text-hint)', lineHeight: 1.55, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
          {d.description || <span style={{ opacity: 0.4 }}>No description</span>}
        </div>
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Transport</label>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '3px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: C.greenBg, color: C.green, border: `1px solid ${C.greenBorder}` }}>
          <span style={{ width: 5, height: 5, borderRadius: '50%', background: C.green, boxShadow: `0 0 5px ${C.green}` }} />
          {d.transport}
        </span>
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Endpoint</label>
        <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', wordBreak: 'break-all', padding: '6px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
          {d.endpointUrl}
        </div>
      </div>
      <a href="/admin/agents" style={{ fontSize: 12, color: C.green, textDecoration: 'none', display: 'flex', alignItems: 'center', gap: 4, marginTop: 8, opacity: 0.8 }}
        onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
        onMouseLeave={e => (e.currentTarget.style.opacity = '0.8')}
      >
        <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
        Configure in Agents
      </a>
    </div>
  );
}
