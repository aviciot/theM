// Re-export API types
export type {
  Application,
  Agent,
  EntryPoint,
  AppDefinition,
  AppDefinitionDoc,
  ComponentDefinitionSummary,
  ValidationReport,
  AgentParamsResponse,
  SessionInfo,
  MonitoringConfig,
  MiddlewareDef,
  AppOrchestratorOut,
  AppOrchestratorSummary,
} from '@/lib/api';

// ── Local types ──────────────────────────────────────────────────────────────

export const ENTRY_POINT_TYPES = ['websocket', 'sse', 'webrtc', 'a2a', 'voice'] as const;
export type EntryPointType = typeof ENTRY_POINT_TYPES[number];

export interface EntryPointData {
  label: string;
  epType: EntryPointType;
  accessMode: 'token' | 'public';
  slug: string;
  appName?: string;
  convTokenLimit?: string;
  maxConcurrentSessions?: string;
  queueTimeout?: string;
  queueMessage?: string;
  _epId?: string;
  [key: string]: unknown;
}

export interface OrchestratorData {
  orchestratorId: string;
  name: string;
  displayName: string;
  model: string | null;
  maxParallelTools: number;
  appOrchestratorId: string | null;
  systemPrompt: string | null;
  allowedAgentIds: string[];
  llmProvider: string | null;
  llmModel: string | null;
  llmApiKey: string | null;
  maxIterations: number;
  historyWindow: number;
  delegatable: boolean;
  kind: string;
  budgetTokens: number | null;
  transcriptionProvider: string | null;
  transcriptionModel: string | null;
  transcriptionApiKey: string | null;
  ttsProvider: string | null;
  ttsVoice: string | null;
  ttsApiKey: string | null;
  [key: string]: unknown;
}

export interface AgentData {
  agentId: string;
  name: string;
  displayName: string;
  description: string;
  transport: string;
  endpointUrl: string;
  tags?: string[];
  icon?: string | null;
  [key: string]: unknown;
}

export interface MiddlewareData {
  defId: string;
  slug: string;
  kind: 'guard' | 'cache';
  displayName: string;
  description: string;
  config: Record<string, unknown>;
  configOverride: Record<string, unknown>;
  nodeId: string;
  [key: string]: unknown;
}

export type ProposalStatus = 'pending' | 'applying' | 'applied' | 'failed' | 'stale';

export interface Proposal {
  id: string;
  type: string;
  targetType: 'orchestrator' | 'agent';
  targetId: string;
  targetName: string;
  field: string;
  current: string | number;
  suggested: string | number;
  reason: string;
  status: ProposalStatus;
  error?: string;
}

export interface AdvisorMessage {
  role: 'user' | 'assistant';
  text: string;
  streaming?: boolean;
  proposals?: Proposal[];
}

// ── Canvas V2 node data interfaces ───────────────────────────────────────────
export interface OrchNodeData {
  _kind: 'orchestrator';
  instance_id: string;
  display_name: string;
  definition_ref: import('@/lib/api').DefinitionRef;
  definition_id?: string;
  config: Record<string, unknown>;
  _error?: boolean;
  _shake?: boolean;
  _errorMsg?: string;
}

export interface AgentNodeData {
  _kind: 'agent';
  instance_id: string;
  display_name: string;
  description: string;
  definition_ref: import('@/lib/api').DefinitionRef;
  definition_id?: string;
  config: Record<string, unknown>;
  secret_bindings?: Record<string, string>;
  icon?: string;
  _error?: boolean;
  _shake?: boolean;
  _errorMsg?: string;
}

export interface MwNodeData {
  _kind: 'middleware';
  instance_id: string;
  display_name: string;
  definition_ref: import('@/lib/api').DefinitionRef;
  definition_id?: string;
  config: Record<string, unknown>;
  _error?: boolean;
  _shake?: boolean;
  _errorMsg?: string;
}

export interface EpNodeData {
  _kind: 'ep';
  instance_id: string;
  slug: string;
  protocol: 'websocket' | 'sse' | 'webrtc' | 'a2a' | 'voice';
  label: string;
  config: Record<string, unknown>;
  _error?: boolean;
  _shake?: boolean;
  _errorMsg?: string;
}

export type CanvasNodeData = OrchNodeData | AgentNodeData | MwNodeData | EpNodeData;

// ── Chain/validation types ───────────────────────────────────────────────────
export interface ChainStatus {
  ready: boolean;
  label: string;
  color: string;
  epNode?: import('@xyflow/react').Node;
  orchNode?: import('@xyflow/react').Node;
  agentCount: number;
}

export type RuleSeverity = 'block' | 'warn';

export interface CanvasRule {
  id: string;
  severity: RuleSeverity;
  message: (ctx: { nodes: import('@xyflow/react').Node[]; edges: import('@xyflow/react').Edge[] }) => string | null;
  errorNodeIds?: (ctx: { nodes: import('@xyflow/react').Node[]; edges: import('@xyflow/react').Edge[] }) => string[];
}

// ── Node port definitions ────────────────────────────────────────────────────
export interface NodePortDef {
  accepts: string[];
  emits: string[];
  maxOutgoing?: number;
  maxIncoming?: number;
}

// ── Entry Point picker ───────────────────────────────────────────────────────
export interface EpPickerEntry {
  epNode: import('@xyflow/react').Node;
  orchName: string;
  slug: string;
  label: string;
  epType: string;
}

// ── Logo state ───────────────────────────────────────────────────────────────
export type LogoState = 'idle' | 'dirty' | 'error' | 'success' | 'thinking' | 'warning';

export interface LogoStateDef {
  opacity: number;
  filter: string;
  animation: string;
}

// ── App liveness ─────────────────────────────────────────────────────────────
export type AppLiveness = { reachable: boolean; latency_ms: number | null };
