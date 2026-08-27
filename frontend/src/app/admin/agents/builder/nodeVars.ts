/**
 * Static data-flow analysis for canvas nodes.
 *
 * IMPORTANT: this is a heuristic display aid, NOT the authoritative wiring
 * model. The Go runtime uses a shared PipelineVars store — any node can read
 * any variable written by any prior node regardless of edges. Edges on the
 * canvas encode execution order only, not data pipes.
 *
 * Semantics are derived from the Go interpreter in
 * go/internal/agentgen/interpreter.go and the transform package.
 * Keep in sync when runtime behavior changes.
 */

import type { Node, Edge } from '@xyflow/react';
import type { StepData } from './types';

export interface NodeVars {
  reads: string[];
  writes: string[];
}

// ── Template var extraction ───────────────────────────────────────────────────

export function extractTemplateVars(tmpl: string): string[] {
  const matches: string[] = [];
  const re = /\{\{\.?(\w+)\}\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(tmpl)) !== null) matches.push(m[1]);
  return [...new Set(matches)];
}

// ── Per-node static analysis ──────────────────────────────────────────────────

/**
 * Statically derive the variables a node reads from and writes to PipelineVars.
 *
 * Matches Go interpreter semantics (interpreter.go execHTTP / execLLM /
 * execTransform). Dead config fields that the interpreter ignores are excluded:
 *   - transform: `expressions`, `extractions` — not in Go TransformStepConfig
 *   - llm: `effort`, `stream`                 — declared but never read by execLLM
 */
export function extractNodeVars(node: Node): NodeVars {
  const d = node.data as unknown as StepData;
  const cfg = d.config ?? {};
  const st = d.step_type;

  // ── input ──────────────────────────────────────────────────────────────────
  if (st === 'input') {
    const bindVar = (cfg.bindings as Record<string, string>)?.text || 'input';
    return { reads: [], writes: [bindVar] };
  }

  // ── llm ───────────────────────────────────────────────────────────────────
  if (st === 'llm') {
    const userPrompt = (cfg.user_prompt as string) || '';
    const systemPrompt = (cfg.system_prompt as string) || '';
    const outVar = (cfg.output_var as string) || 'output';

    const reads = [...new Set([
      ...extractTemplateVars(userPrompt),
      ...extractTemplateVars(systemPrompt),
      // Go runtime fallback: when user_prompt renders to "" it reads vars["input"]
      ...(userPrompt === '' ? ['input'] : []),
    ])];

    return { reads, writes: [outVar] };
  }

  // ── transform ─────────────────────────────────────────────────────────────
  // Go TransformStepConfig has only `functions` and `exposed_vars`.
  // `expressions` and `extractions` are frontend-only legacy shapes that the
  // Go interpreter never executes — exclude them from analysis.
  if (st === 'transform') {
    const functions = (cfg.functions as Array<{
      fn: string;
      input_var: string;
      output_var: string;
      args?: Record<string, unknown>;
    }>) ?? [];

    const reads: string[] = [];
    const writes: string[] = [];
    for (const f of functions) {
      if (f.input_var) reads.push(f.input_var);
      if (f.output_var) writes.push(f.output_var);
      // 'template' fn renders a Go template string stored in args.template
      if (f.fn === 'template' && f.args?.template) {
        reads.push(...extractTemplateVars(String(f.args.template)));
      }
    }
    return { reads: [...new Set(reads)], writes: [...new Set(writes)] };
  }

  // ── http ──────────────────────────────────────────────────────────────────
  // Go runtime writes http_response AND any cfg.Extractions[].Var.
  if (st === 'http') {
    const urlTemplate = (cfg.url_template as string) || '';
    const bodyTemplate = (cfg.body_template as string) || '';
    const reads = [...new Set([
      ...extractTemplateVars(urlTemplate),
      ...extractTemplateVars(bodyTemplate),
    ])];

    // Extractions written to PipelineVars alongside http_response
    const extractions = (cfg.extractions as Array<{ var: string; json_path: string }>) ?? [];
    const writes = ['http_response', ...extractions.map(e => e.var).filter(Boolean)];

    return { reads, writes: [...new Set(writes)] };
  }

  // ── response ──────────────────────────────────────────────────────────────
  if (st === 'response') {
    const fromVar = (cfg.from_var as string) || 'output';
    return { reads: [fromVar], writes: [] };
  }

  // ── branch ────────────────────────────────────────────────────────────────
  if (st === 'branch') {
    const expr = (cfg.expression as string) || '';
    return { reads: extractTemplateVars(expr), writes: [] };
  }

  return { reads: [], writes: [] };
}

// ── Graph-aware helpers ───────────────────────────────────────────────────────

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

/**
 * Build a map of var → source node label for all variables reachable upstream
 * of `nodeId` (graph-aware, not just direct incoming edges).
 */
export function upstreamVarSources(
  nodeId: string,
  allNodes: Node[],
  edges: Edge[],
): Map<string, { label: string; step_type: string }> {
  const predIds = reachablePredecessors(nodeId, edges);
  const result = new Map<string, { label: string; step_type: string }>();
  for (const n of allNodes) {
    if (!predIds.has(n.id)) continue;
    const d = n.data as unknown as StepData;
    const { writes } = extractNodeVars(n);
    for (const v of writes) {
      // earlier writers are overwritten by later ones (topo order not guaranteed
      // here, so last-write wins — good enough for display hints)
      result.set(v, { label: d.label || n.id, step_type: d.step_type });
    }
  }
  return result;
}

/**
 * All var names written anywhere downstream of `nodeId` (graph-aware).
 */
export function downstreamReadVars(
  nodeId: string,
  allNodes: Node[],
  edges: Edge[],
): Set<string> {
  const succIds = reachableSuccessors(nodeId, edges);
  const result = new Set<string>();
  for (const n of allNodes) {
    if (!succIds.has(n.id)) continue;
    const { reads } = extractNodeVars(n);
    for (const v of reads) result.add(v);
  }
  return result;
}

// ── Edge label helper ─────────────────────────────────────────────────────────

/**
 * Returns the variables that cross a specific edge: intersection of what the
 * source writes and what the target reads.
 *
 * Returns [] (empty) when there is no statically matched intersection — callers
 * should treat this as "no matched vars" and fall back to a generic label,
 * rather than showing all source writes (which is misleading).
 *
 * Note: because the runtime uses a shared store, the target node may still
 * receive variables via the shared store that aren't in this intersection.
 */
export function edgeRelevantVars(sourceNode: Node, targetNode: Node): string[] {
  const { writes } = extractNodeVars(sourceNode);
  const { reads } = extractNodeVars(targetNode);
  if (reads.length === 0 || writes.length === 0) return [];
  return writes.filter(v => reads.includes(v));
}
