import type { Node } from '@xyflow/react';
import type { AgentRootData } from '../../types';
import { C } from '../../constants';

interface AgentRootPropertiesProps {
  selectedNode: Node;
  agentNodes: Node[];
  updateSelectedNodeField: (field: string, value: string) => void;
}

export function AgentRootProperties({ selectedNode, agentNodes, updateSelectedNodeField }: AgentRootPropertiesProps) {
  const d = (agentNodes.find(n => n.id === selectedNode.id)?.data ?? selectedNode.data) as unknown as AgentRootData;
  return (
    <>
      <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginBottom: '4px' }}>Display Name</label>
      <input
        value={d.display_name}
        onChange={e => updateSelectedNodeField('display_name', e.target.value)}
        style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
      />
      <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Description</label>
      <textarea
        value={d.description}
        onChange={e => updateSelectedNodeField('description', e.target.value)}
        rows={3}
        style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', resize: 'vertical', boxSizing: 'border-box' }}
      />
      <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Version</label>
      <input
        value={d.version}
        onChange={e => updateSelectedNodeField('version', e.target.value)}
        style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
      />
    </>
  );
}
