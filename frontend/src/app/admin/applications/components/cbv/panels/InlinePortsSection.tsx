'use client';
import type { Node, Edge } from '@xyflow/react';
import { C } from '../../../constants';
import { getNodeDef } from '@/lib/nodeRegistry';
import { extractInlineNodeVars, upstreamAppFlowVarSources, resolveAlias } from '../appFlowVars';
import { renameInlinePortAlias, deleteInlinePortAlias } from '../useInlinePortWiring';
import { PortAliasField } from './PortAliasField';

// ── InlinePortsSection — "READS" panel for llm/condition ─────────────────────
//
// Phase 3 of docs/APPFLOW_NAMED_PORTS_PLAN.md built this read-only, mirroring
// the agent builder's StepDataFlowSection. Phase 5 adds rename/delete for any
// var that's a drag-created `input_aliases` entry — vars typed directly into
// the prompt/expression text (never dragged in) stay read-only, matching the
// agent builder's own is-it-dynamic distinction. AppFlow has no compiled
// contract like agentgen's StepContract, so "unresolved" here always means
// "no upstream node writes this var" via a graph walk, never a
// compiler-reported error.

interface Props {
  selectedNode: Node;
  nodes: Node[];
  edges: Edge[];
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
}

export function InlinePortsSection({ selectedNode, nodes, edges, setNodes }: Props) {
  const thisNode = nodes.find(n => n.id === selectedNode.id) ?? selectedNode;
  const { reads } = extractInlineNodeVars(thisNode);
  const varSrcMap = upstreamAppFlowVarSources(selectedNode.id, nodes, edges);
  const inputAliases = ((thisNode.data as unknown as { config?: Record<string, unknown> }).config?.input_aliases as Record<string, string>) ?? {};

  const nodeType = (thisNode.data as unknown as { node_type: string }).node_type;
  const emptyHint = nodeType === 'condition'
    ? 'fill in the expression to see consumed vars'
    : 'no variables consumed';

  return (
    <div>
      <div style={{ fontSize: 11, fontWeight: 700, letterSpacing: '0.08em', color: C.cyan, marginBottom: 6, textTransform: 'uppercase' }}>
        Reads {reads.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400, textTransform: 'none' }}>— {emptyHint}</span>}
      </div>
      {[...new Set(reads)].map(v => {
        const src = varSrcMap.get(resolveAlias(thisNode, v));
        const unresolved = !src;
        const isAlias = v in inputAliases;
        return (
          <div
            key={v}
            style={{
              borderRadius: 6, marginBottom: 4,
              background: unresolved ? 'rgba(248,113,113,0.06)' : 'rgba(0,240,255,0.05)',
              border: `1px solid ${unresolved ? 'rgba(248,113,113,0.3)' : 'rgba(0,240,255,0.15)'}`,
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px' }}>
              <code style={{ color: unresolved ? '#f87171' : C.cyan, fontSize: 11, fontFamily: 'monospace', flexShrink: 0 }}>{`{{.${v}}}`}</code>
              {src && (
                <>
                  <span style={{ color: C.textMuted, fontSize: 10 }}>from</span>
                  <span style={{ color: '#94a3b8', fontSize: 10 }}>
                    {getNodeDef(src.node_type, 'appflow').emoji} {src.label}
                  </span>
                </>
              )}
              {unresolved && <span style={{ color: '#f87171', fontSize: 10 }}>— not written by any upstream node</span>}
              {isAlias && (
                <button
                  onClick={() => deleteInlinePortAlias(thisNode.id, v, setNodes)}
                  title={`Remove ${v} binding`}
                  style={{ marginLeft: 'auto', background: 'none', border: 'none', cursor: 'pointer', color: '#64748b', fontSize: 12, lineHeight: 1, padding: '0 2px', display: 'flex', alignItems: 'center' }}
                  onMouseEnter={e => (e.currentTarget.style.color = '#f87171')}
                  onMouseLeave={e => (e.currentTarget.style.color = '#64748b')}
                >✕</button>
              )}
            </div>
            {isAlias && (
              <PortAliasField
                nodeId={thisNode.id}
                alias={v}
                onRename={(nodeId, oldAlias, newAlias) => renameInlinePortAlias(nodeId, oldAlias, newAlias, setNodes)}
              />
            )}
          </div>
        );
      })}
    </div>
  );
}
