/**
 * Static data-flow analysis for AppFlow's `llm`/`condition` node kinds.
 *
 * IMPORTANT: this is a heuristic display aid, NOT the authoritative wiring
 * model. The Go runtime (go/internal/appflow) uses a shared FlowVars store —
 * any node can read any variable written by any prior node regardless of
 * edges. Edges on the canvas encode execution order only, not data pipes.
 * Mirrors the agent builder's nodeVars.ts (same shared-var-bag model), but
 * only `llm`/`condition` touch FlowVars at runtime — every other AppFlow kind
 * (router, hil, fork, join, agent, orchestrator, middleware, entryPoint) only
 * ever reads/writes `accumulated`, never FlowVars, so this module correctly
 * reports empty reads/writes for them (see docs/APPFLOW_NAMED_PORTS_PLAN.md
 * scope-narrowing decision).
 *
 * Semantics derived from go/internal/appflow/workflow.go's `case "llm"` /
 * `case "condition"`. Keep in sync when runtime behavior changes.
 */

import type { Node, Edge } from '@xyflow/react';
import { extractTemplateVars } from '@/lib/templateVars';
import { reachablePredecessors } from '@/lib/graphWalk';

export interface AppFlowNodeVars {
  reads: string[];
  writes: string[];
}

interface InlineLikeData {
  node_type: string;
  display_name?: string;
  config?: Record<string, unknown>;
}

/**
 * Statically derive the FlowVars a node reads from / writes to.
 * Only `llm` and `condition` ever touch FlowVars — every other kind returns
 * empty reads/writes, matching the Go runtime exactly.
 */
export function extractInlineNodeVars(node: Node): AppFlowNodeVars {
  const d = node.data as unknown as InlineLikeData;
  const cfg = d.config ?? {};

  if (d.node_type === 'llm') {
    const userPrompt = (cfg.user_prompt as string) || '';
    const systemPrompt = (cfg.system_prompt as string) || '';
    const outputVar = (cfg.output_var as string) || 'output';

    const reads = [...new Set([
      ...extractTemplateVars(userPrompt),
      ...extractTemplateVars(systemPrompt),
    ])];

    return { reads, writes: [outputVar] };
  }

  if (d.node_type === 'condition') {
    const expression = (cfg.expression as string) || '';
    return { reads: extractTemplateVars(expression), writes: [] };
  }

  return { reads: [], writes: [] };
}

/**
 * Build a map of var → source node label for all FlowVars reachable upstream
 * of `nodeId` (graph-aware — walks edges backwards through any number of
 * hops, not just direct incoming edges).
 */
export function upstreamAppFlowVarSources(
  nodeId: string,
  allNodes: Node[],
  edges: Edge[],
): Map<string, { label: string; node_type: string }> {
  const predIds = reachablePredecessors(nodeId, edges);
  const result = new Map<string, { label: string; node_type: string }>();
  for (const n of allNodes) {
    if (!predIds.has(n.id)) continue;
    const d = n.data as unknown as InlineLikeData;
    const { writes } = extractInlineNodeVars(n);
    for (const v of writes) {
      // Earlier writers are overwritten by later ones (topo order not
      // guaranteed here, so last-write wins — good enough for display hints).
      result.set(v, { label: d.display_name || n.id, node_type: d.node_type });
    }
  }
  return result;
}
