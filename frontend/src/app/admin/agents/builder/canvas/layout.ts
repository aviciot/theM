import type { Node, Edge } from '@xyflow/react';
import { Position } from '@xyflow/react';
import { getNodeDef, resolveInputPorts, resolveOutputPorts } from '@/lib/nodeRegistry';
import type { StepData, LayoutDir } from '../types';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const dagre: any = (typeof window !== 'undefined' ? require('dagre') : null);

export function applyDagreLayout(nodes: Node[], edges: Edge[], dir: LayoutDir = 'TB'): Node[] {
  if (!dagre) return nodes;
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: dir, nodesep: 60, ranksep: 100, marginx: 60, marginy: 60 });
  nodes.forEach(n => {
    let h = 80;
    if (n.type === 'step') {
      const stepd = n.data as unknown as StepData;
      const nodeDef = getNodeDef(stepd.step_type);
      const committedInputs = stepd.inputs ? Object.keys(stepd.inputs) : [];
      const inputPorts = resolveInputPorts(nodeDef, committedInputs);
      const outputPorts = resolveOutputPorts(nodeDef, (stepd.config ?? {}) as Record<string, unknown>);
      const dataPortCount = Math.max(
        inputPorts.filter(p => p.kind === 'data').length,
        outputPorts.filter(p => p.kind === 'data').length,
      );
      if (dataPortCount > 0) {
        h = Math.max(80, 20 + dataPortCount * 18 + 20);
      }
    }
    g.setNode(n.id, { width: 120, height: h });
  });
  edges.forEach(e => g.setEdge(e.source, e.target));
  dagre.layout(g);
  const sourcePos = dir === 'LR' ? Position.Right : Position.Bottom;
  const targetPos = dir === 'LR' ? Position.Left  : Position.Top;
  return nodes.map(n => {
    const pos = g.node(n.id);
    return { ...n, position: { x: pos.x - 60, y: pos.y - 40 }, sourcePosition: sourcePos, targetPosition: targetPos };
  });
}
