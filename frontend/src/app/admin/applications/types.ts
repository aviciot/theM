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

// ── App Canvas Debug Mode (docs/APP_CANVAS_DEBUG_PLAN.md Phase 5/6) ─────────
// Real WS/SSE-driven state, not a client-side simulator — see
// useAppFlowDebugSession.ts. Mirrors the agent builder's DebugNodeState shape
// so StepNode.tsx's border/glow styling constants can be reused as-is.
// 'paused' (Phase 6) is distinct from 'pending': pending means "not reached
// yet," paused means "the backend has genuinely stopped here, waiting for a
// Step click" — a real, observed backend state, not a UI guess.
export type AppFlowDebugNodeState = 'idle' | 'pending' | 'running' | 'paused' | 'done' | 'error';

export interface AppFlowNodeDebugInfo {
  state: AppFlowDebugNodeState;
  detail?: string;
  error?: string;
}

// ── AppFlow Runtime Params (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md) ────────────
// Generic declared-runtime-param scan: a node kind declares (via GET
// /admin/node-types' app_params, see lib/nodeRegistry.ts's AppParamDecl) what
// it needs; the debug panel scans the canvas and renders ONE field set PER
// NODE INSTANCE that declares one — never merged/deduped across nodes, unlike
// the agent builder's HTTP-node app_param_key dedup (two LLM nodes are two
// independent choices, not "the same secret used twice").
export interface AppFlowRuntimeParamSpec {
  // `${nodeId}:${paramKey}` — unique per node, so two nodes declaring the same
  // param key (e.g. two llm nodes both declaring "llm_key") never collide.
  specKey: string;
  key: string;
  label: string;
  description: string;
  type: string;
  required: boolean;
  nodeId: string;
  nodeLabel?: string;
}

// One LLM-credential picker's value — General mode (tenant's saved key) or
// Custom mode (one-off key for this debug run only, never persisted to the
// app's saved Runtime settings). model is optional in General mode — empty
// means "use the provider's own default_model," same fallback the backend
// (resolveLLMOverride) already applies; when set, it's validated server-side
// against the provider's own allowed_models list.
export type AppFlowLLMCredentialValue =
  | { mode: 'general'; provider: string; keyId: number | null; model: string }
  | { mode: 'custom'; provider: string; model: string; apiKey: string; baseUrl: string };

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
