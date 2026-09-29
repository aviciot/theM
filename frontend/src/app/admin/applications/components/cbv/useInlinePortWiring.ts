/**
 * Phase 5 of docs/APPFLOW_NAMED_PORTS_PLAN.md: drag-to-connect named data-port
 * wiring for `llm`/`condition` nodes.
 *
 * There is no separate "data" handle — llm/condition nodes have the same 2
 * control handles every AppFlow node kind has (see CanvasNodes.tsx's
 * InlineNode). A wire dragged from an `llm` node's single output is always
 * both a control-flow edge AND (when the target is llm/condition) a candidate
 * data binding, since llm is the only kind that ever writes a FlowVar.
 *
 * Binding storage: `input_aliases: {alias: {source_node_id, source_var}}`
 * inside the target node's already-opaque `config` object (confirmed zero
 * serialization-layer changes needed — CanvasHelpers.ts passes `config`
 * through whole; node IDs are stable across export/import, confirmed by
 * reading CanvasHelpers.ts/CanvasExportImport.ts directly — instance_id
 * round-trips unchanged).
 *
 * REVISED 2026-09-26, live user feedback on the first cut: the original
 * version stored `{alias: sourceOutputVarName}` — a copied string, not a
 * reference. That broke two things at once: (1) renaming the source's
 * `output_var` never propagated anywhere, since nothing remembered *which
 * node* the copied name came from; (2) two different `llm` nodes both
 * outputting a var literally named "output" were indistinguishable except by
 * numeric suffix. Storing the source node's id fixes both — the source's
 * *current* output_var is always looked up live (see appFlowVars.ts's
 * `resolveBinding`), and the default alias can be derived from the source's
 * own display name instead of a bare suffix.
 */

import type { Node } from '@xyflow/react';
import type { MiddlewareWiring } from '@/lib/api';
import { guardWriteVarsForNode } from './guardWriteVars';

export interface NameableField {
  key: string;    // config key the alias's {{.alias}} reference gets appended into
  label: string;  // shown in the popover
}

/** One of a node's available outputs — its own primary var, or a guard var. */
export interface DragSource {
  varName: string;  // the real FlowVar key (== NameableField.key's role, but source-side)
  label: string;    // shown in the popover, e.g. "output" or "PII Guard: email.status"
}

export interface PortBinding {
  source_node_id: string;
  source_var: string; // which field on the source this pointed at when bound (llm only ever has one: output_var)
}

interface InlineLikeData {
  node_type: string;
  display_name?: string;
  config?: Record<string, unknown>;
}

/** Fields on a node kind a dropped data port can be bound into. */
export function nameableFields(nodeType: string): NameableField[] {
  if (nodeType === 'llm') {
    return [
      { key: 'system_prompt', label: 'System Prompt' },
      { key: 'user_prompt', label: 'User Prompt' },
    ];
  }
  if (nodeType === 'condition') {
    return [{ key: 'expression', label: 'Expression' }];
  }
  return [];
}

/**
 * Every output a node can be dragged FROM: its own primary var (`llm` only
 * — `output_var`, defaulting to "output") plus one entry per FlowVar an
 * enabled guard wiring on it writes (any node kind — agent or llm today,
 * any future kind guards apply to tomorrow, with zero new code here).
 */
export function resolveDragSource(node: Node | undefined, wirings: MiddlewareWiring[]): DragSource[] {
  if (!node) return [];
  const d = node.data as unknown as InlineLikeData;
  const out: DragSource[] = [];
  if (d.node_type === 'llm') {
    const outputVar = (d.config?.output_var as string) || 'output';
    out.push({ varName: outputVar, label: outputVar });
  }
  for (const g of guardWriteVarsForNode(node.id, wirings)) {
    out.push({ varName: g.flatVar, label: `${g.guardLabel}: ${g.displayRef}` });
  }
  return out;
}

/** Any node with at least one draggable output (own var, or a guard var). */
export function isBindableSource(node: Node | undefined, wirings: MiddlewareWiring[]): boolean {
  return resolveDragSource(node, wirings).length > 0;
}

export type DropResolution =
  | { kind: 'none' }             // target isn't llm/condition — plain control edge only
  | { kind: 'auto'; field: NameableField }
  | { kind: 'ambiguous'; fields: NameableField[] };

/** Decide whether a drop onto `targetNode` should auto-bind or need a popover. */
export function resolveDropTarget(targetNode: Node | undefined): DropResolution {
  if (!targetNode) return { kind: 'none' };
  const nodeType = (targetNode.data as unknown as InlineLikeData).node_type;
  const fields = nameableFields(nodeType);
  if (fields.length === 0) return { kind: 'none' };
  if (fields.length === 1) return { kind: 'auto', field: fields[0] };
  return { kind: 'ambiguous', fields };
}

export function getInputAliases(node: Node): Record<string, PortBinding> {
  const cfg = (node.data as unknown as InlineLikeData).config ?? {};
  return (cfg.input_aliases as Record<string, PortBinding>) ?? {};
}

/**
 * `{{.aliasName}}` is executed by Go's real text/template (confirmed by
 * reading go/internal/appflow/inline.go directly) against FlowVars, a flat
 * map[string]string. A literal `.` inside the alias would NOT mean "the map
 * key containing a dot" — Go's template dotted-field syntax reads it as
 * chained field access ({{.A.B}} = "field B of field A"), which fails against
 * a flat string map. So a `.` must never appear in an alias — loosened from
 * an earlier [a-z0-9_]-only rule, but a dot specifically must still become
 * `_`, alongside whitespace/braces/quotes.
 */
function sanitizeAliasChars(s: string): string {
  return s.trim().replace(/[\s{}"'`.]+/g, '_');
}

/**
 * `<SourceDisplayName>_output` — falls back to a numeric suffix only if two
 * source nodes genuinely share the same display name (rare, already an
 * existing ambiguity elsewhere on the canvas).
 *
 * A guard var already embeds its own source node's id as a prefix
 * (`nodeID_defSlug_status`, `guardWriteVarsForNode`'s convention) — naively
 * prepending the display name again would double up the node identity
 * (`Agent1_agent1_pii_redact_status`, redundant but valid). Stripped back
 * down to just the guard-specific suffix (`pii_redact_status`) before the
 * label is re-added, so the alias reads as `Agent1_pii_redact_status`
 * instead.
 */
function defaultAliasFor(sourceNode: Node, sourceVar: string, existing: Record<string, PortBinding>): string {
  const label = (sourceNode.data as unknown as InlineLikeData).display_name || sourceNode.id;
  const nodeIdPrefix = `${sourceNode.id}_`;
  const varSuffix = sourceVar.startsWith(nodeIdPrefix) ? sourceVar.slice(nodeIdPrefix.length) : sourceVar;
  const base = sanitizeAliasChars(`${label}_${varSuffix}`);
  if (!(base in existing)) return base;
  let i = 2;
  while (`${base}_${i}` in existing) i++;
  return `${base}_${i}`;
}

function appendTemplateRef(text: string, alias: string): string {
  const ref = `{{.${alias}}}`;
  const trimmed = text.trim();
  return trimmed ? `${text} ${ref}` : ref;
}

/**
 * Commit a data-port binding: a reference to the source node (not a copy of
 * the var name itself) becomes a new alias on the target, appended into
 * `field`'s text. `sourceVar` is the chosen output — the source's own
 * primary var (`output_var`, the pre-existing default) or a guard var
 * (`resolveDragSource`'s new multi-output case).
 */
export function commitInlinePortBinding(
  sourceNode: Node,
  targetNodeId: string,
  field: NameableField,
  setNodes: (updater: (ns: Node[]) => Node[]) => void,
  sourceVar?: string,
) {
  const sourceCfg = (sourceNode.data as unknown as InlineLikeData).config ?? {};
  const resolvedSourceVar = sourceVar ?? (sourceCfg.output_var as string) ?? 'output';

  setNodes(ns => ns.map(n => {
    if (n.id !== targetNodeId) return n;
    const nd = n.data as unknown as InlineLikeData;
    const cfg = nd.config ?? {};
    const existingAliases = (cfg.input_aliases as Record<string, PortBinding>) ?? {};
    const alias = defaultAliasFor(sourceNode, resolvedSourceVar, existingAliases);
    const currentText = (cfg[field.key] as string) || '';
    return {
      ...n,
      data: {
        ...nd,
        config: {
          ...cfg,
          input_aliases: { ...existingAliases, [alias]: { source_node_id: sourceNode.id, source_var: resolvedSourceVar } },
          [field.key]: appendTemplateRef(currentText, alias),
        },
      } as unknown as Record<string, unknown>,
    };
  }));
}

/** Rewrite every `{{.oldAlias}}` occurrence across a node's text fields to `{{.newAlias}}`. */
export function renameInlinePortAlias(
  nodeId: string,
  oldAlias: string,
  newAlias: string,
  setNodes: (updater: (ns: Node[]) => Node[]) => void,
) {
  if (oldAlias === newAlias || !newAlias) return;
  setNodes(ns => ns.map(n => {
    if (n.id !== nodeId) return n;
    const nd = n.data as unknown as InlineLikeData;
    const cfg = nd.config ?? {};
    const aliases = getInputAliases(n);
    if (!(oldAlias in aliases)) return n;

    const nextAliases = { ...aliases };
    nextAliases[newAlias] = nextAliases[oldAlias];
    delete nextAliases[oldAlias];

    const nextCfg: Record<string, unknown> = { ...cfg, input_aliases: nextAliases };
    const oldRef = new RegExp(`\\{\\{\\.${oldAlias}\\}\\}`, 'g');
    for (const field of nameableFields(nd.node_type)) {
      const text = cfg[field.key];
      if (typeof text === 'string' && text.includes(`{{.${oldAlias}}}`)) {
        nextCfg[field.key] = text.replace(oldRef, `{{.${newAlias}}}`);
      }
    }
    return { ...n, data: { ...nd, config: nextCfg } as unknown as Record<string, unknown> };
  }));
}

/** Remove an alias binding and every `{{.alias}}` occurrence referencing it. */
export function deleteInlinePortAlias(
  nodeId: string,
  alias: string,
  setNodes: (updater: (ns: Node[]) => Node[]) => void,
) {
  setNodes(ns => ns.map(n => {
    if (n.id !== nodeId) return n;
    const nd = n.data as unknown as InlineLikeData;
    const cfg = nd.config ?? {};
    const aliases = getInputAliases(n);
    if (!(alias in aliases)) return n;

    const nextAliases = { ...aliases };
    delete nextAliases[alias];

    const nextCfg: Record<string, unknown> = { ...cfg, input_aliases: nextAliases };
    const ref = new RegExp(`\\s?\\{\\{\\.${alias}\\}\\}`, 'g');
    for (const field of nameableFields(nd.node_type)) {
      const text = cfg[field.key];
      if (typeof text === 'string' && text.includes(`{{.${alias}}}`)) {
        nextCfg[field.key] = text.replace(ref, '');
      }
    }
    return { ...n, data: { ...nd, config: nextCfg } as unknown as Record<string, unknown> };
  }));
}
