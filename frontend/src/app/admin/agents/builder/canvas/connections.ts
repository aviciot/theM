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

/** One data-flow mapping represented in a bundle edge. */
export interface MappingRecord {
  edgeId: string;
  sourceHandle: string;
  targetHandle: string;
  portLabel: string;
}

/**
 * All data edges between the same source→target pair are collapsed into a
 * single BundleEdge (leader) with a `mappings` array. Non-leader edges get
 * `hidden: true` so React Flow still tracks their handles but doesn't draw them.
 *
 * Even a single data mapping renders as a BundleEdge — gives consistent UX
 * (badge always visible, MappingSheet always openable).
 */
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
    const mappings: MappingRecord[] = group.map(e => {
      const sh = (e.sourceHandle ?? '').replace('data-out-', '');
      const th = (e.targetHandle ?? '').replace('data-in-', '');
      return {
        edgeId: e.id,
        sourceHandle: e.sourceHandle ?? '',
        targetHandle: e.targetHandle ?? '',
        portLabel: sh || th || '?',
      };
    });

    group.forEach((e, i) => {
      if (i === 0) {
        result.push({
          ...e,
          type: 'bundleEdge',
          data: {
            ...(e.data as object ?? {}),
            kind: 'data',
            mappings,
            isLeader: true,
            layoutDir,
          },
        });
      } else {
        result.push({ ...e, hidden: true });
      }
    });
  }
  return result;
}

// ── Module-level callback registry for BundleEdge → useSkillPipeline ─────────
// BundleEdge is a pure React component with no access to pipeline callbacks.
// The delete callback is registered here and called by edgeId.

type DeleteMappingFn = (edgeId: string) => void;
let _deleteMappingFn: DeleteMappingFn | null = null;

export function registerDeleteMapping(fn: DeleteMappingFn): void {
  _deleteMappingFn = fn;
}

export function callDeleteMapping(edgeId: string): void {
  _deleteMappingFn?.(edgeId);
}
