import { useCallback, useState } from 'react';
import { addEdge, useEdgesState, useNodesState, type Connection, type Edge, type Node } from '@xyflow/react';
import type { LayoutDir } from '../types';

export function useAgentGraph({ markDirty }: { markDirty: () => void }) {
  const [agentNodes, setAgentNodes, onAgentNodesChange] = useNodesState<Node>([]);
  const [agentEdges, setAgentEdges, onAgentEdgesChange] = useEdgesState<Edge>([]);
  const [agentSlug, setAgentSlug] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [description, setDescription] = useState('');
  const [version, setVersion] = useState('1.0.0');
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);
  const [activeView, setActiveView] = useState<'agent' | 'skill'>('agent');
  const [activeSkillId, setActiveSkillId] = useState<string | null>(null);
  const [layoutDir, setLayoutDir] = useState<LayoutDir>('LR');

  const onAgentConnect = useCallback((conn: Connection) => {
    setAgentEdges(prev => addEdge(conn, prev));
    markDirty();
  }, [setAgentEdges, markDirty]);

  return {
    agentNodes, setAgentNodes, onAgentNodesChange,
    agentEdges, setAgentEdges, onAgentEdgesChange,
    agentSlug, setAgentSlug,
    displayName, setDisplayName,
    description, setDescription,
    version, setVersion,
    selectedNode, setSelectedNode,
    activeView, setActiveView,
    activeSkillId, setActiveSkillId,
    layoutDir, setLayoutDir,
    onAgentConnect,
  };
}
