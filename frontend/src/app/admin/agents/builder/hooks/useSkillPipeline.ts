import React, { useCallback, useEffect, useRef, useState } from 'react';
import { addEdge, useEdgesState, useNodesState, type Connection, type Edge, type Node } from '@xyflow/react';
import { acceptsDynamicInputs, canAddIncoming, canAddOutgoing, getNodeDef } from '@/lib/nodeRegistry';
import { genUUID } from '../constants';
import type { AgentStepDoc } from '@/lib/api';
import type { LayoutDir, StepData, StepNodeData } from '../types';
import { applyBundleGroups, isDataEdge, topoSort } from '../canvas/connections';
import { applyDagreLayout } from '../canvas/dagre';

interface UseSkillPipelineParams {
  activeSkillId: string | null;
  layoutDir: LayoutDir;
  markDirty: () => void;
  screenToFlowPosition: (pos: { x: number; y: number }) => { x: number; y: number };
  fitView: (opts?: { padding?: number }) => void;
}

export function useSkillPipeline({
  activeSkillId,
  layoutDir,
  markDirty,
  screenToFlowPosition,
  fitView,
}: UseSkillPipelineParams) {
  const [skillPipelines, setSkillPipelines] = useState<Record<string, { nodes: Node[]; edges: Edge[] }>>({});
  const [localPipeNodes, setLocalPipeNodes, onPipeNodesChange] = useNodesState<Node>([]);
  const [localPipeEdges, setLocalPipeEdges, onPipeEdgesChange] = useEdgesState<Edge>([]);

  const displayPipeEdges = React.useMemo(
    () => applyBundleGroups(localPipeEdges, layoutDir),
    [localPipeEdges, layoutDir],
  );

  useEffect(() => {
    if (activeSkillId) {
      const state = skillPipelines[activeSkillId] ?? { nodes: [], edges: [] };
      const arranged = applyDagreLayout(state.nodes, state.edges, 'LR');
      setLocalPipeNodes(arranged);
      setLocalPipeEdges(state.edges);
      setTimeout(() => fitView({ padding: 0.2 }), 50);
    }
  }, [activeSkillId]); // eslint-disable-line react-hooks/exhaustive-deps

  const savePipelineState = useCallback(() => {
    if (activeSkillId) {
      setSkillPipelines(prev => ({
        ...prev,
        [activeSkillId]: { nodes: localPipeNodes, edges: localPipeEdges },
      }));
    }
  }, [activeSkillId, localPipeNodes, localPipeEdges]);

  function makeDefaultPipeline(): { nodes: Node[]; edges: Edge[] } {
    const inputId = genUUID();
    const responseId = genUUID();
    return {
      nodes: [
        { id: `step-${inputId}`,    type: 'step', position: { x: 160, y: 60  }, data: { step_id: inputId,    step_type: 'input',    label: 'Input',    config: {} } },
        { id: `step-${responseId}`, type: 'step', position: { x: 160, y: 280 }, data: { step_id: responseId, step_type: 'response', label: 'Response', config: {} } },
      ],
      edges: [],
    };
  }

  function addStepToActivePipeline(type: AgentStepDoc['type']) {
    if (!activeSkillId) return;
    const stepId = genUUID();
    const newNode: Node = {
      id: `step-${stepId}`,
      type: 'step',
      position: screenToFlowPosition({ x: 300, y: 200 }),
      data: { step_id: stepId, step_type: type, label: type, config: {} },
    };
    setLocalPipeNodes(prev => [...prev, newNode]);
    markDirty();
  }

  const onPipeConnect = useCallback((conn: Connection) => {
    const isDataSrc = conn.sourceHandle?.startsWith('data-out-');
    const isData = isDataSrc && conn.targetHandle?.startsWith('data-in-');

    if (isData) {
      const portID = conn.targetHandle!.replace('data-in-', '');
      const fromPort = conn.sourceHandle!.replace('data-out-', '');
      const srcStepID = (conn.source as string).replace('step-', '');
      setLocalPipeNodes(prev => prev.map(n => {
        if (n.id !== conn.target) return n;
        const existing = (n.data as unknown as StepData).inputs ?? {};
        return { ...n, data: { ...n.data, inputs: { ...existing, [portID]: { from_step: srcStepID, from_port: fromPort } } } };
      }));
      setLocalPipeEdges(prev => {
        const kept = prev.filter(e => !(isDataEdge(e) && e.target === conn.target && e.targetHandle === conn.targetHandle));
        return addEdge({ ...conn, type: 'dataEdge', data: { kind: 'data' } }, kept);
      });
      markDirty();
      return;
    }

    setLocalPipeEdges(prev => addEdge(conn, prev.filter(e => e.target !== conn.target || isDataEdge(e))));
    markDirty();

    setLocalPipeNodes(prev => {
      const sourceNode = prev.find(n => n.id === conn.source);
      const targetNode = prev.find(n => n.id === conn.target);
      if (!sourceNode || !targetNode) return prev;

      const srcData = sourceNode.data as unknown as StepData;
      const tgtData = targetNode.data as unknown as StepData;

      const sourceVar: string =
        srcData.step_type === 'input'
          ? ((srcData.config?.bindings as Record<string, string>)?.text || 'input')
          : ((srcData.config?.output_var as string) || 'output');

      const targetField = getNodeDef(tgtData.step_type).input_field;
      if (!targetField) return prev;

      const currentValue = tgtData.config?.[targetField] as string | undefined;
      if (targetField !== 'from_var' && currentValue && currentValue.trim() !== '') return prev;

      const fillValue = targetField === 'from_var' ? sourceVar : `{{${sourceVar}}}`;

      return prev.map(n =>
        n.id === conn.target
          ? { ...n, data: { ...n.data, config: { ...(n.data.config as Record<string, unknown>), [targetField]: fillValue } } }
          : n
      );
    });
  }, [setLocalPipeEdges, setLocalPipeNodes, markDirty]);

  const onDeleteInput = useCallback((nodeId: string, portID: string) => {
    setLocalPipeNodes(prev => prev.map(n => {
      if (n.id !== nodeId) return n;
      const existing = { ...((n.data as unknown as StepData).inputs ?? {}) };
      delete existing[portID];
      return { ...n, data: { ...n.data, inputs: existing } };
    }));
    setLocalPipeEdges(prev => prev.filter(e =>
      !(isDataEdge(e) && e.target === nodeId && e.targetHandle === `data-in-${portID}`)
    ));
    markDirty();
  }, [setLocalPipeNodes, setLocalPipeEdges, markDirty]);

  const onRenameInput = useCallback((nodeId: string, oldPortID: string, newPortID: string) => {
    if (oldPortID === newPortID) return;
    setLocalPipeNodes(prev => prev.map(n => {
      if (n.id !== nodeId) return n;
      const existing = { ...((n.data as unknown as StepData).inputs ?? {}) };
      if (!(oldPortID in existing)) return n;
      existing[newPortID] = existing[oldPortID];
      delete existing[oldPortID];
      return { ...n, data: { ...n.data, inputs: existing } };
    }));
    setLocalPipeEdges(prev => prev.map(e => {
      if (!isDataEdge(e) || e.target !== nodeId || e.targetHandle !== `data-in-${oldPortID}`) return e;
      return {
        ...e,
        id: e.id.replace(`-${oldPortID}`, `-${newPortID}`),
        targetHandle: `data-in-${newPortID}`,
      };
    }));
    markDirty();
  }, [setLocalPipeNodes, setLocalPipeEdges, markDirty]);

  const connectFiredRef = useRef(false);

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onPipeConnectStart = useCallback((_: any, params: { nodeId: string | null; handleId: string | null; handleType: string | null }) => {
    if (!params.handleId?.startsWith('data-out-')) return;
    const varName = params.handleId.replace('data-out-', '');
    connectFiredRef.current = false;
    setLocalPipeNodes(prev => prev.map(n => {
      if (n.type !== 'step' || n.id === params.nodeId) return n;
      const stepType = (n.data as unknown as StepData).step_type;
      const existingInputs = (n.data as unknown as StepData).inputs ?? {};
      const accept = acceptsDynamicInputs(stepType);
      let ghostVar = varName;
      if (accept && ghostVar in existingInputs) {
        let i = 2;
        while (`${varName}_${i}` in existingInputs) i++;
        ghostVar = `${varName}_${i}`;
      }
      return { ...n, data: { ...n.data, _draggingVar: accept ? ghostVar : varName, _dragAccept: accept ? 'accept' : 'reject' } };
    }));
  }, [setLocalPipeNodes]);

  const onPipeConnectEnd = useCallback(() => {
    setLocalPipeNodes(prev => prev.map(n => {
      if (n.type !== 'step') return n;
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { _draggingVar, _dragAccept, ...rest } = n.data as unknown as StepNodeData;
      return { ...n, data: rest };
    }));
  }, [setLocalPipeNodes]);

  const isPipeConnectionValid = useCallback((conn: Connection | Edge) => {
    if (conn.source === conn.target) return false;

    const connIsData = (conn as Edge).data?.kind === 'data'
      || ((conn as Connection).sourceHandle?.startsWith('data-out-') && (conn as Connection).targetHandle?.startsWith('data-in-'));
    if (connIsData) return true;

    const srcNode = localPipeNodes.find(n => n.id === conn.source);
    const tgtNode = localPipeNodes.find(n => n.id === conn.target);

    const ctrlEdges = localPipeEdges.filter(e => !isDataEdge(e));

    if (srcNode) {
      const srcType = (srcNode.data as unknown as StepData).step_type;
      const hasNamedCtrlPorts = (getNodeDef(srcType).control_output_ports ?? []).length > 0;
      const currentOut = hasNamedCtrlPorts
        ? ctrlEdges.filter(e => e.source === conn.source && e.sourceHandle === conn.sourceHandle).length
        : ctrlEdges.filter(e => e.source === conn.source).length;
      if (!canAddOutgoing(srcType, currentOut)) return false;
    }
    if (tgtNode) {
      const tgtType = (tgtNode.data as unknown as StepData).step_type;
      const currentIn = ctrlEdges.filter(e => e.target === conn.target).length;
      if (!canAddIncoming(tgtType, currentIn)) return false;
    }

    const hypothetical = [...ctrlEdges, { id: '__test__', source: conn.source!, target: conn.target! }];
    if (topoSort(localPipeNodes, hypothetical) === null) return false;

    return true;
  }, [localPipeNodes, localPipeEdges]);

  return {
    skillPipelines, setSkillPipelines,
    localPipeNodes, setLocalPipeNodes, onPipeNodesChange,
    localPipeEdges, setLocalPipeEdges, onPipeEdgesChange,
    displayPipeEdges,
    savePipelineState,
    makeDefaultPipeline,
    addStepToActivePipeline,
    onPipeConnect,
    onPipeConnectStart,
    onPipeConnectEnd,
    isPipeConnectionValid,
    onDeleteInput,
    onRenameInput,
  };
}
