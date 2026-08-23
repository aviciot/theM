// Local TypeScript interfaces for the agent builder

export type DebugNodeState = 'idle' | 'pending' | 'running' | 'done' | 'error';

export interface AgentRootData {
  display_name: string;
  description: string;
  version: string;
}

export interface SkillData {
  skill_id: string;
  name: string;
  description: string;
  tags: string[];
  input_modes: string[];
  output_modes: string[];
  examples: string[];
}

export interface StepData {
  step_id: string;
  step_type: string;
  label: string;
  config: Record<string, unknown>;
}

export interface StepNodeData extends StepData {
  _debug?: {
    state: DebugNodeState;
    output?: string;
    error?: string;
  };
  _validation?: 'error' | 'warning' | null;
  _stub?: boolean;
}

export interface DebugParamSpec {
  key: string;
  label: string;
  description: string;
  isSecret: boolean;
  required: boolean;
  nodeLabel?: string;
}

export interface DebugState {
  active: boolean;
  setupComplete: boolean;
  paramSpecs: DebugParamSpec[];
  debugParams: Record<string, string>;
  mode: 'run-all' | 'step' | null;
  vars: Record<string, unknown>;
  nodeStates: Record<string, DebugNodeState>;
  nodeInputVars: Record<string, Record<string, unknown>>;
  nodeOutputs: Record<string, string>;
  nodeErrors: Record<string, string>;
  edgeValues: Record<string, string>;
  executionOrder: string[];
  currentStepIndex: number;
  pendingVarOverrides: Record<string, string>;
  error: string | null;
}

export interface ValidationState {
  issues: import('@/lib/api').AgentIssue[];
  loading: boolean;
  lastValidatedAt: number | null;
}

export type LogoState = 'idle' | 'dirty' | 'error' | 'success' | 'thinking' | 'warning';
