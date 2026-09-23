import type { MCPServerAttachment } from '@/lib/api';

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
  MCPServerAttachment,
} from '@/lib/api';

// ── Local types ──────────────────────────────────────────────────────────────

export const ENTRY_POINT_TYPES = ['websocket', 'sse', 'webrtc', 'a2a', 'voice'] as const;
export type EntryPointType = typeof ENTRY_POINT_TYPES[number];

export interface EntryPointData {
  label: string;
  epType: EntryPointType;
  accessMode: 'token' | 'public' | 'user_jwt' | 'external_jwt';
  allowedPrincipals: 'internal' | 'external' | 'both';
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
  mcpServers: MCPServerAttachment[];
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
  wiringEnabled?: boolean;
  emoji?: string;
  color?: string;
  bg_color?: string;
  [key: string]: unknown;
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
  emoji?: string;
  color?: string;
  bg_color?: string;
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

// ── App Canvas Debug Mode (docs/APP_CANVAS_DEBUG_PLAN.md Phase 5) ───────────
// Real WS/SSE-driven state, not a client-side simulator — see
// useAppFlowDebugSession.ts. Mirrors the agent builder's DebugNodeState shape
// so StepNode.tsx's border/glow styling constants can be reused as-is.
export type AppFlowDebugNodeState = 'idle' | 'pending' | 'running' | 'done' | 'error';

export interface AppFlowNodeDebugInfo {
  state: AppFlowDebugNodeState;
  detail?: string;
  error?: string;
}

// node_type is a string, not a literal union — new appflow node kinds
// (see go/internal/appflow/noderegistry.go) need zero frontend type changes,
// per docs/NODE_REGISTRY_PLAN.md Phase 4.
export interface FlowControlNodeData {
  _kind: 'flow_control';
  instance_id: string;
  node_type: string;
  display_name: string;
  config: Record<string, unknown>;
  _error?: boolean;
  _shake?: boolean;
  _errorMsg?: string;
  _debug?: AppFlowNodeDebugInfo;
}

export interface InlineNodeData {
  _kind: 'inline';
  instance_id: string;
  node_type: string;
  display_name: string;
  config: Record<string, unknown>;
  _error?: boolean;
  _shake?: boolean;
  _errorMsg?: string;
  _debug?: AppFlowNodeDebugInfo;
}

export type CanvasNodeData = OrchNodeData | AgentNodeData | MwNodeData | EpNodeData | FlowControlNodeData | InlineNodeData;

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
