import { getNodeDef } from '@/lib/nodeRegistry';
import type { Node, Edge } from '@xyflow/react';
import type { StepData, LogoState } from '../types';
import type { useAgentGraph } from './useAgentGraph';
import type { useSkillPipeline } from './useSkillPipeline';
import type { useDefinitionLifecycle } from './useDefinitionLifecycle';
import type { useDebugSession } from './useDebugSession';

interface Params {
  graph: ReturnType<typeof useAgentGraph>;
  pipeline: ReturnType<typeof useSkillPipeline>;
  lifecycle: ReturnType<typeof useDefinitionLifecycle>;
  debugSession: ReturnType<typeof useDebugSession>;
}

export function useBuilderDerivedState({ graph, pipeline, lifecycle, debugSession }: Params) {
  const debugNodes: Node[] = graph.activeView === 'skill' && debugSession.debug.active
    ? pipeline.localPipeNodes.map(n => ({
        ...n,
        data: {
          ...n.data,
          _debug: {
            state: debugSession.debug.nodeStates[n.id] ?? 'idle',
            output: debugSession.debug.nodeOutputs[n.id],
            error: debugSession.debug.nodeErrors[n.id],
          },
        },
      }))
    : pipeline.localPipeNodes;

  const runningNodeId = Object.entries(debugSession.debug.nodeStates).find(([, s]) => s === 'running')?.[0];

  const debugEdges: Edge[] = graph.activeView === 'skill' && debugSession.debug.active
    ? pipeline.localPipeEdges.map(e => {
        const hasDoneValue = !!debugSession.debug.edgeValues[e.id];
        const isFlowing = runningNodeId === e.source;
        const edgeState: 'idle' | 'flowing' | 'done' = isFlowing ? 'flowing' : hasDoneValue ? 'done' : 'idle';
        return {
          ...e,
          type: 'debugEdge',
          data: {
            ...((e.data ?? {}) as Record<string, unknown>),
            debugState: edgeState,
            label: hasDoneValue ? `"${debugSession.debug.edgeValues[e.id]}"` : undefined,
          },
        };
      })
    : pipeline.localPipeEdges;

  const nodeValidationMap = (() => {
    const m: Record<string, 'error' | 'warning'> = {};
    for (const iss of lifecycle.validation.issues) {
      if (!iss.node_id) continue;
      const current = m[iss.node_id];
      if (!current || (iss.severity === 'error' && current === 'warning')) {
        m[iss.node_id] = iss.severity;
      }
    }
    return m;
  })();

  const pipelineIssues = graph.activeSkillId
    ? lifecycle.validation.issues.filter(iss => iss.skill_id === graph.activeSkillId || !iss.skill_id)
    : lifecycle.validation.issues;

  const validatedPipeNodes = pipeline.localPipeNodes.map(n => {
    const stepId = (n.data as unknown as StepData).step_id;
    const stepType = (n.data as unknown as StepData).step_type;
    const isStub = !getNodeDef(stepType).executable;
    const valSeverity = nodeValidationMap[stepId] ?? null;
    if (!valSeverity && !isStub) return n;
    return { ...n, data: { ...n.data, _validation: valSeverity, _stub: isStub } };
  });

  const errorCount   = lifecycle.validation.issues.filter(iss => iss.severity === 'error').length;
  const warningCount = lifecycle.validation.issues.filter(iss => iss.severity === 'warning').length;
  const debugRunning = Object.values(debugSession.debug.nodeStates).some(s => s === 'running');

  const logoState: LogoState = (() => {
    if (lifecycle.saving || lifecycle.publishing || lifecycle.validation.loading || debugRunning) return 'thinking';
    if (lifecycle.logoResult === 'invalid') return 'error';
    if (lifecycle.logoResult === 'warn')    return 'warning';
    if (lifecycle.logoResult === 'valid')   return 'success';
    if (lifecycle.dirty) return 'dirty';
    return 'idle';
  })();

  const currentNodes: Node[] = graph.activeView === 'agent'
    ? graph.agentNodes
    : debugSession.debug.active ? debugNodes : validatedPipeNodes;
  const currentEdges: Edge[] = graph.activeView === 'agent' ? graph.agentEdges : (debugSession.debug.active ? debugEdges : pipeline.localPipeEdges);

  return {
    debugNodes, debugEdges, nodeValidationMap, pipelineIssues,
    validatedPipeNodes, errorCount, warningCount, debugRunning,
    logoState, currentNodes, currentEdges,
  };
}
