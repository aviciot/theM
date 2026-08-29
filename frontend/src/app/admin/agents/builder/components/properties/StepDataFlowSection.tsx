import { useState, useEffect, useRef, type ReactNode } from 'react';
import type { Node, Edge } from '@xyflow/react';
import type { AgentIssue, StepContract } from '@/lib/api';
import type { StepData } from '../../types';
import { C } from '../../constants';
import { stepMeta } from '../StepNode';
import { extractNodeVars, upstreamVarSources, downstreamReadVars } from '../../nodeVars';

function PortAliasField({
  nodeId, portID, onRename,
}: { nodeId: string; portID: string; onRename: (nodeId: string, oldID: string, newID: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(portID);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => { setDraft(portID); }, [portID]);
  useEffect(() => { if (editing) inputRef.current?.select(); }, [editing]);

  function commit() {
    setEditing(false);
    const clean = draft.trim().replace(/[^a-z0-9_]/gi, '_').replace(/^[0-9]/, '_$&');
    if (clean && clean !== portID) onRename(nodeId, portID, clean);
    else setDraft(portID);
  }

  if (!editing) {
    return (
      <div
        onClick={() => setEditing(true)}
        title="Click to rename port alias"
        style={{ padding: '3px 8px 5px', fontSize: 10, color: '#475569', fontFamily: 'monospace', cursor: 'text', borderTop: '1px dashed rgba(0,240,255,0.1)', display: 'flex', alignItems: 'center', gap: 4 }}
      >
        <span style={{ color: '#334155', fontSize: 9 }}>alias:</span>
        <span style={{ color: '#7dd3fc' }}>{portID}</span>
        <span style={{ color: '#334155', fontSize: 9, marginLeft: 2 }}>✎</span>
      </div>
    );
  }
  return (
    <div style={{ padding: '3px 8px 5px', borderTop: '1px dashed rgba(0,240,255,0.1)' }}>
      <input
        ref={inputRef}
        value={draft}
        onChange={e => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={e => { if (e.key === 'Enter') commit(); if (e.key === 'Escape') { setEditing(false); setDraft(portID); } }}
        style={{ width: '100%', background: 'rgba(0,240,255,0.06)', border: '1px solid rgba(0,240,255,0.3)', color: '#7dd3fc', fontSize: 10, fontFamily: 'monospace', padding: '2px 4px', borderRadius: 3, outline: 'none', boxSizing: 'border-box' }}
      />
    </div>
  );
}

interface StepDataFlowSectionProps {
  selectedNode: Node;
  localPipeNodes: Node[];
  localPipeEdges: Edge[];
  validationIssues: AgentIssue[];
  stepContracts: Record<string, StepContract>;
  d: StepData;
  onDeleteInput?: (nodeId: string, portID: string) => void;
  onRenameInput?: (nodeId: string, oldPortID: string, newPortID: string) => void;
}

export function StepDataFlowSection({
  selectedNode, localPipeNodes, localPipeEdges, validationIssues, stepContracts,
  d, onDeleteInput, onRenameInput,
}: StepDataFlowSectionProps) {
  const thisNode = localPipeNodes.find(n => n.id === selectedNode.id);
  if (!thisNode) return null;

  const stepId = (thisNode.data as unknown as StepData).step_id as string | undefined;
  const dynamicInputs = (thisNode.data as unknown as StepData).inputs ?? {};
  const contract = stepId ? stepContracts[stepId] : undefined;

  const reads: string[] = contract ? contract.inputs.map(r => r.name) : extractNodeVars(thisNode).reads;
  const writes: string[] = contract ? contract.outputs.map(w => w.name) : extractNodeVars(thisNode).writes;
  const isAuthoritative = !!contract;

  const varSrcMap = isAuthoritative ? null : upstreamVarSources(selectedNode.id, localPipeNodes, localPipeEdges);
  const downstreamReads = isAuthoritative ? null : downstreamReadVars(selectedNode.id, localPipeNodes, localPipeEdges);

  const unresolvedFromCompiler = new Set(
    validationIssues
      .filter(iss => iss.node_id === stepId && iss.code === 'UNRESOLVED_INPUT')
      .map(iss => iss.field ?? '')
  );

  const outVar = d.step_type === 'input'
    ? ((d.config?.bindings as Record<string, string>)?.text || 'input')
    : ((d.config?.output_var as string) || 'output');
  const outEdges = localPipeEdges.filter(e => e.source === selectedNode.id);

  return (
    <div style={{ marginBottom: '16px', display: 'flex', flexDirection: 'column', gap: 10 }}>
      {isAuthoritative && (
        <div style={{ fontSize: '9px', color: C.textMuted, letterSpacing: '0.06em', marginBottom: '-4px' }}>
          ✓ compiled contract
        </div>
      )}

      {/* READS */}
      <div>
        <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: C.cyan, marginBottom: '6px' }}>
          READS {reads.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400 }}>— no variables consumed</span>}
        </div>
        {[...new Set(reads)].map(v => {
          const unresolved = isAuthoritative ? unresolvedFromCompiler.has(v) : !varSrcMap?.get(v);
          const src = !isAuthoritative ? varSrcMap?.get(v) : undefined;
          const isDynamic = v in dynamicInputs;
          return (
            <div key={v} style={{ borderRadius: '6px', marginBottom: '4px', background: unresolved ? 'rgba(248,113,113,0.06)' : 'rgba(0,240,255,0.05)', border: `1px solid ${unresolved ? 'rgba(248,113,113,0.3)' : 'rgba(0,240,255,0.15)'}` }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '5px 8px' }}>
                <code style={{ color: unresolved ? '#f87171' : C.cyan, fontSize: '11px', fontFamily: 'monospace', flexShrink: 0 }}>{`{{.${v}}}`}</code>
                {src && <><span style={{ color: C.textMuted, fontSize: '10px' }}>from</span><span style={{ color: '#94a3b8', fontSize: '10px' }}>{stepMeta(src.step_type).emoji} {src.label}</span></>}
                {unresolved && <span style={{ color: '#f87171', fontSize: '10px' }}>— not guaranteed on all paths</span>}
                {isDynamic && onDeleteInput && (
                  <button
                    onClick={() => onDeleteInput(selectedNode.id, v)}
                    title={`Remove ${v} input port`}
                    style={{ marginLeft: 'auto', background: 'none', border: 'none', cursor: 'pointer', color: '#64748b', fontSize: '12px', lineHeight: 1, padding: '0 2px', display: 'flex', alignItems: 'center' }}
                    onMouseEnter={e => (e.currentTarget.style.color = '#f87171')}
                    onMouseLeave={e => (e.currentTarget.style.color = '#64748b')}
                  >✕</button>
                )}
              </div>
              {isDynamic && onRenameInput && (
                <PortAliasField nodeId={selectedNode.id} portID={v} onRename={onRenameInput} />
              )}
            </div>
          );
        })}
      </div>

      {/* WRITES */}
      <div>
        <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: '#a78bfa', marginBottom: '6px' }}>
          WRITES {writes.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400 }}>— no variables produced</span>}
        </div>
        {writes.map(v => {
          const consumed = isAuthoritative ? true : downstreamReads?.has(v);
          return (
            <div key={v} style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '5px 8px', borderRadius: '6px', marginBottom: '4px', background: 'rgba(167,139,250,0.05)', border: '1px solid rgba(167,139,250,0.2)' }}>
              <code style={{ color: '#a78bfa', fontSize: '11px', fontFamily: 'monospace', flexShrink: 0 }}>{`{{.${v}}}`}</code>
              {!isAuthoritative && !consumed && <span style={{ color: C.textMuted, fontSize: '10px' }}>— not read downstream</span>}
            </div>
          );
        })}
      </div>

      {/* OUTPUTS */}
      {d.step_type !== 'response' && (
        <div>
          <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: C.green, marginBottom: '6px' }}>
            OUTPUTS {outEdges.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400 }}>— nothing connected</span>}
          </div>
          {outEdges.map(e => {
            const tgt = localPipeNodes.find(n => n.id === e.target);
            const tgtData = tgt?.data as unknown as StepData | undefined;
            const tgtMeta = tgtData ? stepMeta(tgtData.step_type) : { emoji: '?', label: 'unknown' };

            const isBranch = d.step_type === 'branch';
            let routeLabel: ReactNode;
            if (isBranch) {
              const handle = (e.sourceHandle ?? '').replace('ctrl-out-', '');
              const isTrue = handle === 'true';
              routeLabel = (
                <code style={{ color: isTrue ? '#4ade80' : '#f87171', fontSize: '11px', fontFamily: 'monospace' }}>
                  {isTrue ? 'true' : 'false'}
                </code>
              );
            } else {
              routeLabel = (
                <code style={{ color: C.green, fontSize: '11px', fontFamily: 'monospace' }}>{`{{${outVar}}}`}</code>
              );
            }

            return (
              <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '6px 8px', borderRadius: '6px', marginBottom: '4px', background: 'rgba(74,222,128,0.05)', border: '1px solid rgba(74,222,128,0.2)' }}>
                {routeLabel}
                <span style={{ color: C.textMuted, fontSize: '11px' }}>→</span>
                <span style={{ fontSize: '14px' }}>{tgtMeta.emoji}</span>
                <span style={{ color: '#e2e8f0', fontSize: '11px', fontWeight: 600 }}>{tgtData?.label || tgtMeta.label}</span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
