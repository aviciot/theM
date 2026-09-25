'use client';
import type { Node, Edge } from '@xyflow/react';
import { C } from '../../../constants';
import { getNodeDef } from '@/lib/nodeRegistry';
import { extractInlineNodeVars, upstreamAppFlowVarSources } from '../appFlowVars';

// ── InlinePortsSection — read-only "READS" panel for llm/condition ──────────
//
// Phase 3 of docs/APPFLOW_NAMED_PORTS_PLAN.md: surfaces which FlowVars a node
// reads and where each one is written upstream, mirroring the agent builder's
// StepDataFlowSection — but read-only (no rename/delete; that's Phase 5's
// drag-to-connect wiring). AppFlow has no compiled contract like agentgen's
// StepContract, so "unresolved" here always means "no upstream node writes
// this var" via a graph walk, never a compiler-reported error.

interface Props {
  selectedNode: Node;
  nodes: Node[];
  edges: Edge[];
}

export function InlinePortsSection({ selectedNode, nodes, edges }: Props) {
  const thisNode = nodes.find(n => n.id === selectedNode.id) ?? selectedNode;
  const { reads } = extractInlineNodeVars(thisNode);
  const varSrcMap = upstreamAppFlowVarSources(selectedNode.id, nodes, edges);

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
        const src = varSrcMap.get(v);
        const unresolved = !src;
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
            </div>
          </div>
        );
      })}
    </div>
  );
}
