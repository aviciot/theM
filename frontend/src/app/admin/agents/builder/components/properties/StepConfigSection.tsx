import { useState, useEffect } from 'react';
import type { Node, Edge } from '@xyflow/react';
import { getNodeDef } from '@/lib/nodeRegistry';
import type { MCPServer } from '@/lib/api';
import { themApi } from '@/lib/api';
import type { StepData, StepPolicyOverride } from '../../types';
import { C } from '../../constants';
import { ExecutionPolicySection } from './ExecutionPolicySection';
import {
  renderInputConfig,
  renderLlmConfig,
  renderHttpConfig,
  renderTransformConfig,
  renderResponseConfig,
  renderBranchConfig,
  renderLoopConfig,
} from './stepConfigs';

interface StepConfigSectionProps {
  selectedNode: Node;
  d: StepData;
  cfg: Record<string, unknown>;
  localPipeNodes: Node[];
  localPipeEdges: Edge[];
  updateStepConfig: (key: string, value: unknown) => void;
  updateStepPolicy: (policy: Record<string, unknown> | null) => void;
  nodeTypesReady?: boolean;
}

export function StepConfigSection({
  selectedNode, d, cfg, localPipeNodes, localPipeEdges, updateStepConfig, updateStepPolicy, nodeTypesReady,
}: StepConfigSectionProps) {
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  useEffect(() => {
    themApi.listMCPServers().then(s => setMcpServers(s ?? [])).catch(() => {});
  }, []);

  function cfgStr(key: string): string { return (cfg[key] as string) ?? ''; }
  function cfgNum(key: string, def = 0): number { return (cfg[key] as number) ?? def; }

  // nodeTypesReady triggers re-render when node types load (http app_params)
  void nodeTypesReady;

  const nodeDef = getNodeDef(d.step_type);
  const maxPolicy = nodeDef.max_policy;
  const policy = (d.policy ?? {}) as StepPolicyOverride;

  // For HTTP nodes, default max_attempts depends on the method: GET→3, mutating→1.
  // resolvePolicy in Go applies this same rule at compile time; mirror it here so
  // the UI shows the accurate resolved default before the user overrides anything.
  const defaultPolicy = (() => {
    const base = nodeDef.default_policy;
    if (!base) return base;
    if (d.step_type === 'http') {
      const method = ((cfg.method as string) ?? '').toUpperCase();
      const isMutating = method !== '' && method !== 'GET';
      return { ...base, max_attempts: isMutating ? 1 : 3 };
    }
    return base;
  })();

  function policyNum(key: keyof StepPolicyOverride, fallback: number): number {
    const v = policy[key];
    return typeof v === 'number' ? v : fallback;
  }
  void policyNum; // consumed by ExecutionPolicySection internally

  function setPolicyField(key: keyof StepPolicyOverride, value: number | undefined) {
    const next = { ...policy };
    if (value === undefined || value === 0) {
      delete next[key];
    } else {
      (next as Record<string, unknown>)[key] = value;
    }
    updateStepPolicy(Object.keys(next).length > 0 ? next as Record<string, unknown> : null);
  }

  return (
    <>
      {d.step_type === 'input' && renderInputConfig({ cfgStr, updateStepConfig, cfg })}

      {d.step_type === 'llm' && renderLlmConfig({ cfgStr, cfgNum, updateStepConfig, mcpServers, cfg })}

      {d.step_type === 'http' && renderHttpConfig({ cfgStr, cfgNum, cfg, updateStepConfig })}

      {d.step_type === 'transform' && renderTransformConfig({
        selectedNodeId: selectedNode.id,
        localPipeNodes,
        localPipeEdges,
        cfg,
        updateStepConfig,
      })}

      {d.step_type === 'response' && renderResponseConfig({ cfgStr, updateStepConfig })}

      {d.step_type === 'branch' && renderBranchConfig({ cfgStr, updateStepConfig })}

      {d.step_type === 'loop' && renderLoopConfig({ cfgStr, cfgNum, updateStepConfig })}

      {!['input', 'llm', 'http', 'transform', 'response', 'branch', 'loop'].includes(d.step_type) && (
        <div style={{ color: '#64748b', fontSize: '12px', padding: '12px', border: `1px dashed ${C.outline}`, borderRadius: '6px', textAlign: 'center' }}>
          Config for <strong style={{ color: C.text }}>{d.step_type}</strong> is not yet supported in the builder.
        </div>
      )}

      {defaultPolicy && (
        <ExecutionPolicySection
          policy={policy}
          defaultPolicy={defaultPolicy}
          maxPolicy={maxPolicy}
          setPolicyField={setPolicyField}
        />
      )}
    </>
  );
}
