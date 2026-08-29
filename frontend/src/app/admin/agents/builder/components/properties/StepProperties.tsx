import type { Node, Edge } from '@xyflow/react';
import type { AgentIssue, StepContract } from '@/lib/api';
import type { DebugState, StepData } from '../../types';
import { C, labelStyle, inputStyle, fieldGap } from '../../constants';
import { StepDataFlowSection } from './StepDataFlowSection';
import { StepConfigSection } from './StepConfigSection';
import { StepDebugSection } from './StepDebugSection';

interface StepPropertiesProps {
  selectedNode: Node;
  localPipeNodes: Node[];
  localPipeEdges: Edge[];
  validationIssues: AgentIssue[];
  stepContracts: Record<string, StepContract>;
  debug: DebugState;
  updateSelectedNodeField: (field: string, value: string) => void;
  updateStepConfig: (key: string, value: unknown) => void;
  setDebug: React.Dispatch<React.SetStateAction<DebugState>>;
  debugStep: () => void;
  nodeTypesReady?: boolean;
  onDeleteInput?: (nodeId: string, portID: string) => void;
  onRenameInput?: (nodeId: string, oldPortID: string, newPortID: string) => void;
}

export function StepProperties({
  selectedNode, localPipeNodes, localPipeEdges, validationIssues, stepContracts,
  debug, updateSelectedNodeField, updateStepConfig, setDebug, debugStep,
  nodeTypesReady, onDeleteInput, onRenameInput,
}: StepPropertiesProps) {
  const d = (localPipeNodes.find(n => n.id === selectedNode.id)?.data ?? selectedNode.data) as unknown as StepData;
  const cfg = d.config ?? {};

  const nodeIssues = validationIssues.filter(iss => iss.node_id === d.step_id);
  const fieldIssues: Record<string, 'error' | 'warning'> = {};
  for (const iss of nodeIssues) {
    if (!iss.field) continue;
    if (!fieldIssues[iss.field] || (iss.severity === 'error' && fieldIssues[iss.field] === 'warning')) {
      fieldIssues[iss.field] = iss.severity;
    }
  }

  return (
    <>
      {nodeIssues.length > 0 && (
        <div style={{ marginBottom: '12px', padding: '8px 10px', borderRadius: '6px', background: nodeIssues.some(i => i.severity === 'error') ? 'rgba(248,113,113,0.08)' : 'rgba(245,158,11,0.08)', border: `1px solid ${nodeIssues.some(i => i.severity === 'error') ? 'rgba(248,113,113,0.3)' : 'rgba(245,158,11,0.3)'}` }}>
          {nodeIssues.map((iss, i) => (
            <div key={i} style={{ fontSize: '11px', color: iss.severity === 'error' ? '#f87171' : '#f59e0b', display: 'flex', gap: '6px', marginBottom: i < nodeIssues.length - 1 ? '4px' : 0 }}>
              <span style={{ flexShrink: 0 }}>{iss.severity === 'error' ? '✗' : '⚠'}</span>
              <span>{iss.message}{iss.field && <span style={{ color: '#64748b' }}> · <code style={{ color: iss.severity === 'error' ? '#f87171' : '#f59e0b' }}>{iss.field}</code></span>}</span>
            </div>
          ))}
        </div>
      )}

      <div style={{ marginBottom: '2px' }}>
        <label style={labelStyle}>Label</label>
        <input value={d.label} onChange={e => updateSelectedNodeField('label', e.target.value)} style={inputStyle} />
      </div>
      <div style={fieldGap}>
        <label style={labelStyle}>Step ID <span style={{ fontWeight: 400, color: '#475569' }}>(auto-generated)</span></label>
        <div style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: '10px', color: '#475569', userSelect: 'all', cursor: 'text', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {d.step_id}
        </div>
      </div>
      <div style={{ ...fieldGap, marginBottom: '16px' }}>
        <label style={labelStyle}>Type</label>
        <div style={{ ...inputStyle, color: C.textMuted, cursor: 'default' }}>{d.step_type}</div>
      </div>

      <StepDataFlowSection
        selectedNode={selectedNode}
        localPipeNodes={localPipeNodes}
        localPipeEdges={localPipeEdges}
        validationIssues={validationIssues}
        stepContracts={stepContracts}
        d={d}
        onDeleteInput={onDeleteInput}
        onRenameInput={onRenameInput}
      />

      <StepConfigSection
        selectedNode={selectedNode}
        d={d}
        cfg={cfg}
        localPipeNodes={localPipeNodes}
        localPipeEdges={localPipeEdges}
        updateStepConfig={updateStepConfig}
        nodeTypesReady={nodeTypesReady}
      />

      <StepDebugSection
        selectedNode={selectedNode}
        localPipeNodes={localPipeNodes}
        localPipeEdges={localPipeEdges}
        debug={debug}
        setDebug={setDebug}
        debugStep={debugStep}
      />
    </>
  );
}
