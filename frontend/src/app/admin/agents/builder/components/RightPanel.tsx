import type { Node, Edge } from '@xyflow/react';
import type { AgentIssue, StepContract } from '@/lib/api';
import type { DebugState } from '../types';
import { C } from '../constants';
import { AgentRootProperties } from './properties/AgentRootProperties';
import { SkillProperties } from './properties/SkillProperties';
import { StepProperties } from './properties/StepProperties';

interface RightPanelProps {
  selectedNode: Node;
  setSelectedNode: (node: Node | null) => void;
  propertiesWidth: number;
  onResizeStart: (e: React.MouseEvent) => void;
  activeView: 'agent' | 'skill';
  agentNodes: Node[];
  localPipeNodes: Node[];
  localPipeEdges: Edge[];
  validationIssues: AgentIssue[];
  stepContracts: Record<string, StepContract>;
  debug: DebugState;
  updateSelectedNodeField: (field: string, value: string) => void;
  updateStepConfig: (key: string, value: unknown) => void;
  updateStepPolicy: (policy: Record<string, unknown> | null) => void;
  setAgentNodes: (updater: (prev: Node[]) => Node[]) => void;
  setDirty: (dirty: boolean) => void;
  savePipelineState: () => void;
  setActiveSkillId: (id: string | null) => void;
  setActiveView: (view: 'agent' | 'skill') => void;
  setDebug: React.Dispatch<React.SetStateAction<DebugState>>;
  debugStep: () => void;
  nodeTypesReady?: boolean;
  onDeleteInput?: (nodeId: string, portID: string) => void;
  onRenameInput?: (nodeId: string, oldPortID: string, newPortID: string) => void;
}

export function RightPanel({
  selectedNode,
  setSelectedNode,
  propertiesWidth,
  onResizeStart,
  activeView,
  agentNodes,
  localPipeNodes,
  localPipeEdges,
  validationIssues,
  stepContracts,
  debug,
  updateSelectedNodeField,
  updateStepConfig,
  updateStepPolicy,
  setAgentNodes,
  setDirty,
  savePipelineState,
  setActiveSkillId,
  setActiveView,
  setDebug,
  debugStep,
  nodeTypesReady,
  onDeleteInput,
  onRenameInput,
}: RightPanelProps) {
  return (
    <div onKeyDown={e => e.stopPropagation()} style={{
      width: propertiesWidth, flexShrink: 0, borderLeft: `1px solid ${C.outline}`,
      background: C.surface, padding: '16px', overflowY: 'auto', position: 'relative',
    }} className="dark-scrollbar">
      <div
        onMouseDown={onResizeStart}
        style={{
          position: 'absolute', top: 0, left: -3, width: 6, height: '100%',
          cursor: 'col-resize', zIndex: 10,
        }}
      />
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '16px' }}>
        <span style={{ color: C.text, fontWeight: 700, fontSize: '13px' }}>Properties</span>
        <button onClick={() => setSelectedNode(null)} style={{ background: 'transparent', border: 'none', color: C.textMuted, cursor: 'pointer', fontSize: '16px' }}>x</button>
      </div>

      {selectedNode.type === 'agentRoot' && (
        <AgentRootProperties
          selectedNode={selectedNode}
          agentNodes={agentNodes}
          updateSelectedNodeField={updateSelectedNodeField}
        />
      )}

      {selectedNode.type === 'skill' && (
        <SkillProperties
          selectedNode={selectedNode}
          agentNodes={agentNodes}
          activeView={activeView}
          setAgentNodes={setAgentNodes}
          setDirty={setDirty}
          savePipelineState={savePipelineState}
          setActiveSkillId={setActiveSkillId}
          setActiveView={setActiveView}
          setSelectedNode={setSelectedNode}
          updateSelectedNodeField={updateSelectedNodeField}
        />
      )}

      {selectedNode.type === 'step' && (
        <StepProperties
          selectedNode={selectedNode}
          localPipeNodes={localPipeNodes}
          localPipeEdges={localPipeEdges}
          validationIssues={validationIssues}
          stepContracts={stepContracts}
          debug={debug}
          updateSelectedNodeField={updateSelectedNodeField}
          updateStepConfig={updateStepConfig}
          updateStepPolicy={updateStepPolicy}
          setDebug={setDebug}
          debugStep={debugStep}
          nodeTypesReady={nodeTypesReady}
          onDeleteInput={onDeleteInput}
          onRenameInput={onRenameInput}
        />
      )}
    </div>
  );
}
