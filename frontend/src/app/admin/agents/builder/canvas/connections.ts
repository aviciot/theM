import type { Edge, Node } from '@xyflow/react';

export function isDataEdge(e: Edge): boolean {
  return (e.data as Record<string, unknown> | undefined)?.kind === 'data';
}

export function topoSort(nodes: Node[], edges: Edge[]): string[] | null {
  const inDegree: Record<string, number> = {};
  const adj: Record<string, string[]> = {};
  for (const n of nodes) { inDegree[n.id] = 0; adj[n.id] = []; }
  for (const e of edges) {
    adj[e.source]?.push(e.target);
    if (inDegree[e.target] !== undefined) inDegree[e.target]++;
  }
  const queue = nodes.filter(n => inDegree[n.id] === 0).map(n => n.id);
  const order: string[] = [];
  while (queue.length) {
    const id = queue.shift()!;
    order.push(id);
    for (const next of (adj[id] ?? [])) {
      inDegree[next]--;
      if (inDegree[next] === 0) queue.push(next);
    }
  }
  return order.length === nodes.length ? order : null;
}

export function applyBundleGroups(edges: Edge[], layoutDir: 'LR' | 'TB'): Edge[] {
  const dataEdges = edges.filter(isDataEdge);
  const ctrlEdges = edges.filter(e => !isDataEdge(e));

  const groups = new Map<string, Edge[]>();
  for (const e of dataEdges) {
    const key = `${e.source}::${e.target}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key)!.push(e);
  }

  const result: Edge[] = [...ctrlEdges];
  for (const group of groups.values()) {
    if (group.length === 1) {
      result.push(group[0]);
      continue;
    }
    const portLabels = group.map(e => {
      const sh = (e.sourceHandle ?? '').replace('data-out-', '');
      return sh || (e.targetHandle ?? '').replace('data-in-', '') || '?';
    });
    group.forEach((e, i) => {
      result.push({
        ...e,
        type: 'bundleEdge',
        data: {
          ...(e.data as object ?? {}),
          kind: 'data',
          portLabel: portLabels[i],
          bundleIndex: i,
          bundleTotal: group.length,
          bundlePorts: portLabels,
          isLeader: i === 0,
          layoutDir,
        },
      });
    });
  }
  return result;
}
