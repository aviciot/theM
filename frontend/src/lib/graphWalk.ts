/**
 * Generic edge-based reachability helpers for canvas graphs (React Flow
 * `Node`/`Edge` shapes). No dependency on any node's `data` payload — these
 * only look at `source`/`target` on edges — so they work identically for the
 * agent builder (agentgen, via nodeVars.ts's re-export) and AppFlow
 * (docs/APPFLOW_NAMED_PORTS_PLAN.md).
 */

import type { Edge } from '@xyflow/react';

/**
 * Collect all node IDs that can reach `targetId` by walking edges backwards.
 * Returns the set of predecessor node IDs (not including targetId itself).
 */
export function reachablePredecessors(targetId: string, edges: Edge[]): Set<string> {
  const pred = new Set<string>();
  const queue = [targetId];
  while (queue.length > 0) {
    const cur = queue.shift()!;
    for (const e of edges) {
      if (e.target === cur && !pred.has(e.source)) {
        pred.add(e.source);
        queue.push(e.source);
      }
    }
  }
  return pred;
}

/**
 * Collect all node IDs reachable from `sourceId` by walking edges forwards.
 * Returns the set of successor node IDs (not including sourceId itself).
 */
export function reachableSuccessors(sourceId: string, edges: Edge[]): Set<string> {
  const succ = new Set<string>();
  const queue = [sourceId];
  while (queue.length > 0) {
    const cur = queue.shift()!;
    for (const e of edges) {
      if (e.source === cur && !succ.has(e.target)) {
        succ.add(e.target);
        queue.push(e.target);
      }
    }
  }
  return succ;
}
