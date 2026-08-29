import type { Node, Edge } from '@xyflow/react';
import type { DebugState, StepData } from '../../types';
import { C, labelStyle, textareaStyle } from '../../constants';

interface StepDebugSectionProps {
  selectedNode: Node;
  localPipeNodes: Node[];
  localPipeEdges: Edge[];
  debug: DebugState;
  setDebug: React.Dispatch<React.SetStateAction<DebugState>>;
  debugStep: () => void;
}

export function StepDebugSection({
  selectedNode, localPipeNodes, localPipeEdges, debug, setDebug, debugStep,
}: StepDebugSectionProps) {
  if (!debug.active) return null;

  const nodeDebugState = debug.nodeStates[selectedNode.id];
  const nodeOutput = debug.nodeOutputs[selectedNode.id];
  const nodeError = debug.nodeErrors[selectedNode.id];
  const nodeInputVars = debug.nodeInputVars[selectedNode.id];

  if (debug.mode === 'step' && nodeDebugState === 'pending') {
    const inEdges = localPipeEdges.filter(e => e.target === selectedNode.id);
    const pendingVars = inEdges.map(e => {
      const src = localPipeNodes.find(n => n.id === e.source);
      const srcData = src?.data as unknown as StepData | undefined;
      const varName = srcData?.step_type === 'input'
        ? ((srcData?.config?.bindings as Record<string, string>)?.text || 'input')
        : ((srcData?.config?.output_var as string) || 'output');
      return { varName, currentVal: String(debug.vars[varName] ?? '') };
    });
    if (pendingVars.length > 0) {
      return (
        <div style={{ marginTop: '16px', padding: '10px', background: 'rgba(245,158,11,0.08)', border: `1px solid ${C.amberBorder}`, borderRadius: '8px' }}>
          <div style={{ fontSize: '10px', fontWeight: 700, color: C.amber, marginBottom: '8px', letterSpacing: '0.08em' }}>
            STEP OVERRIDE — edit values before step runs
          </div>
          {pendingVars.map(({ varName, currentVal }) => (
            <div key={varName} style={{ marginBottom: '8px' }}>
              <label style={{ ...labelStyle, color: C.amber }}>{`{{${varName}}}`}</label>
              <textarea
                rows={2}
                value={debug.pendingVarOverrides[varName] ?? currentVal}
                onChange={e => setDebug(prev => ({
                  ...prev,
                  pendingVarOverrides: { ...prev.pendingVarOverrides, [varName]: e.target.value },
                }))}
                style={{ ...textareaStyle, borderColor: C.amberBorder, fontSize: '11px' }}
              />
            </div>
          ))}
          <button onClick={debugStep} style={{
            width: '100%', background: 'rgba(96,165,250,0.1)', border: `1px solid rgba(96,165,250,0.4)`,
            color: '#60a5fa', padding: '6px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px', fontWeight: 600,
          }}>
            ⏭ Execute this step
          </button>
        </div>
      );
    }
  }

  const VarsInBlock = nodeInputVars && Object.keys(nodeInputVars).length > 0 ? (
    <details style={{ background: 'rgba(96,165,250,0.06)', border: '1px solid rgba(96,165,250,0.25)', borderRadius: '8px', padding: '8px 10px' }}>
      <summary style={{ fontSize: '10px', fontWeight: 700, color: '#60a5fa', letterSpacing: '0.08em', cursor: 'pointer', userSelect: 'none' }}>
        VARS IN ({Object.keys(nodeInputVars).length})
      </summary>
      <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
        {Object.entries(nodeInputVars).map(([k, v]) => {
          const str = typeof v === 'object' ? JSON.stringify(v, null, 2) : String(v);
          const preview = str.length > 120 ? str.slice(0, 120) + '…' : str;
          return (
            <div key={k}>
              <div style={{ fontSize: '10px', color: '#60a5fa', fontFamily: 'monospace', fontWeight: 600 }}>{`{{.${k}}}`}</div>
              <pre style={{ color: '#94a3b8', fontSize: '10px', whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0, fontFamily: 'monospace' }}>{preview}</pre>
            </div>
          );
        })}
      </div>
    </details>
  ) : null;

  if (nodeDebugState === 'done' && nodeOutput !== undefined) {
    return (
      <div style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
        {VarsInBlock}
        <div style={{ padding: '10px', background: 'rgba(74,222,128,0.06)', border: `1px solid rgba(74,222,128,0.3)`, borderRadius: '8px' }}>
          <div style={{ fontSize: '10px', fontWeight: 700, color: C.green, marginBottom: '6px', letterSpacing: '0.08em' }}>OUTPUT</div>
          <pre style={{ color: '#e2e8f0', fontSize: '11px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontFamily: 'monospace' }}>
            {nodeOutput || '(empty)'}
          </pre>
        </div>
      </div>
    );
  }

  if (nodeDebugState === 'error' && nodeError) {
    return (
      <div style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
        {VarsInBlock}
        <div style={{ padding: '10px', background: 'rgba(248,113,113,0.06)', border: `1px solid rgba(248,113,113,0.3)`, borderRadius: '8px' }}>
          <div style={{ fontSize: '10px', fontWeight: 700, color: '#f87171', marginBottom: '6px', letterSpacing: '0.08em' }}>ERROR</div>
          <pre style={{ color: '#f87171', fontSize: '11px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontFamily: 'monospace' }}>
            {nodeError}
          </pre>
        </div>
      </div>
    );
  }

  return null;
}
