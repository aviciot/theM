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
 * Binding storage: `input_aliases: {alias: underlying_flowvars_key}` inside
 * the target node's already-opaque `config` object (confirmed zero
 * serialization-layer changes needed — CanvasHelpers.ts passes `config`
 * through whole). Renaming/deleting an alias also rewrites `{{.alias}}`
 * occurrences in the node's own text fields so the alias and the template
 * text never drift apart.
 */

import type { Node } from '@xyflow/react';

export interface NameableField {
  key: string;    // config key the alias's {{.alias}} reference gets appended into
  label: string;  // shown in the popover
}

interface InlineLikeData {
  node_type: string;
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

/** Only `llm` ever writes a FlowVar — it's the only kind with a bindable output. */
export function isBindableSource(node: Node | undefined): boolean {
  return !!node && (node.data as unknown as InlineLikeData).node_type === 'llm';
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

function getInputAliases(node: Node): Record<string, string> {
  const cfg = (node.data as unknown as InlineLikeData).config ?? {};
  return (cfg.input_aliases as Record<string, string>) ?? {};
}

/** `output`, `output_2`, `output_3`... — same suffix scheme the agent builder's onPipeConnectStart uses. */
function uniqueAlias(desired: string, existing: Record<string, string>): string {
  if (!(desired in existing)) return desired;
  let i = 2;
  while (`${desired}_${i}` in existing) i++;
  return `${desired}_${i}`;
}

function appendTemplateRef(text: string, alias: string): string {
  const ref = `{{.${alias}}}`;
  const trimmed = text.trim();
  return trimmed ? `${text} ${ref}` : ref;
}

/**
 * Commit a data-port binding: source's output_var becomes a new alias on the
 * target, appended into `field`'s text. Idempotent against re-dragging the
 * same source→target→field combination is not attempted — each drop creates
 * a fresh alias, matching the agent builder's own behavior.
 */
export function commitInlinePortBinding(
  sourceNode: Node,
  targetNodeId: string,
  field: NameableField,
  setNodes: (updater: (ns: Node[]) => Node[]) => void,
) {
  const sourceCfg = (sourceNode.data as unknown as InlineLikeData).config ?? {};
  const sourceVar = (sourceCfg.output_var as string) || 'output';

  setNodes(ns => ns.map(n => {
    if (n.id !== targetNodeId) return n;
    const nd = n.data as unknown as InlineLikeData;
    const cfg = nd.config ?? {};
    const existingAliases = (cfg.input_aliases as Record<string, string>) ?? {};
    const alias = uniqueAlias(sourceVar, existingAliases);
    const currentText = (cfg[field.key] as string) || '';
    return {
      ...n,
      data: {
        ...nd,
        config: {
          ...cfg,
          input_aliases: { ...existingAliases, [alias]: sourceVar },
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
