const API_BASE = '/api/them';
const HEALTH_BASE = '/api/bridge';

let _refreshing: Promise<boolean> | null = null;

async function tryRefresh(): Promise<boolean> {
  if (_refreshing) return _refreshing;
  _refreshing = fetch('/api/auth/refresh', { method: 'POST' })
    .then(r => r.ok)
    .catch(() => false)
    .finally(() => { _refreshing = null; });
  return _refreshing;
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  };

  let res = await fetch(`${API_BASE}${path}`, { ...options, headers });

  // On 401, attempt a silent token refresh and retry once.
  if (res.status === 401) {
    const refreshed = await tryRefresh();
    if (refreshed) {
      res = await fetch(`${API_BASE}${path}`, { ...options, headers });
    }
  }

  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.detail || `HTTP ${res.status}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body: unknown) => request<T>(path, { method: 'POST', body: JSON.stringify(body) }),
  put: <T>(path: string, body: unknown) => request<T>(path, { method: 'PUT', body: JSON.stringify(body) }),
  patch: <T>(path: string, body: unknown) => request<T>(path, { method: 'PATCH', body: JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
};

// ── User preferences (stored in auth service, persists across browsers) ──────
// Preferences is a generic JSON object — add new keys freely without schema changes.

export interface UserPreferences {
  agentFolders?: { folders: Array<{ id: string; name: string; agentIds: string[]; collapsed: boolean }> };
  [key: string]: unknown;
}

async function authFetch(path: string, init: RequestInit = {}): Promise<Response> {
  return fetch(`/api/auth${path}`, init);
}

export async function getPreferences(): Promise<UserPreferences> {
  const res = await authFetch('/me/preferences');
  if (!res.ok) return {};
  return res.json().catch(() => ({}));
}

export async function setPreferences(prefs: UserPreferences): Promise<void> {
  await authFetch('/me/preferences', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(prefs),
  });
}

// ── Typed API calls ──────────────────────────────────────────────────────────

export interface AgentSkill {
  id: string;
  name: string;
  description?: string;
  tags?: string[];
}

export interface DiscoverResult {
  ok: boolean;
  detail: string;
  suggested_slug: string;
  display_name: string;
  description: string;
  skills: AgentSkill[];
  supports_streaming: boolean;
  supports_push: boolean;
  icon: string | null;
  agent_card: Record<string, unknown> | null;
  agent_card_url: string;
  category?: string;
}

export interface SystemAgentRoleOut {
  enabled: boolean;
  provider: string | null;
  model: string | null;
  base_url: string | null;
  system_prompt: string | null;
  api_key_hint: string | null;
}

export interface SystemAgentsOut {
  roles: Record<string, SystemAgentRoleOut>;
}

export interface SystemAgentRoleIn {
  enabled?: boolean;
  provider?: string | null;
  model?: string | null;
  base_url?: string | null;
  system_prompt?: string | null;
  api_key?: string | null;
}

export interface Agent {
  id: string;
  slug: string;
  display_name: string;
  description: string;
  transport: string;
  endpoint_url: string;
  enabled: boolean;
  max_concurrency: number;
  timeout_seconds: number;
  auth_token_set?: boolean;
  auth_token_masked?: string | null;
  input_schema?: Record<string, unknown>;
  tags?: string[];
  skills?: AgentSkill[];
  supports_streaming?: boolean;
  supports_push?: boolean;
  icon?: string | null;
  category?: string | null;
  agent_card?: Record<string, unknown> | null;
  agent_card_url?: string | null;
  card_fetched_at?: string | null;
  last_scan_at?: string | null;
  last_scan_result?: ScanResult | null;
  runtime_definition_id?: string | null;
  created_by?: number | null;
  created_by_username?: string;
  created_at: string;
}

export interface ScanFinding {
  id: string;
  label: string;
  status: 'pass' | 'fail' | 'warn';
  risk: 'low' | 'medium' | 'high';
  detail: string;
  recommendation: string;
}

export interface ScanResult {
  score: number;
  risk: 'low' | 'medium' | 'high';
  summary: string;
  findings: ScanFinding[];
  http_probes: { tls: string; auth_required: string; reachable: boolean };
  scanned_at: string;
}

export interface Orchestrator {
  id: string;
  name: string;
  description: string;
  system_prompt: string;
  max_iterations: number;
  max_parallel_tools: number;
  enabled: boolean;
  created_at: string;
}

export interface Run {
  id: string;
  orchestrator_name: string;
  status: string;
  user_message: string;
  goal?: string;
  total_tokens: number;
  cost_usd: number;
  started_at: string;
  completed_at: string | null;
  ended_at?: string | null;
  duration_ms: number | null;
  iterations?: number;
  final_output?: string | null;
  error?: string | null;
  total_tokens_in?: number;
  total_tokens_out?: number;
  total_cost_usd?: string;
  parent_run_id?: string | null;
}

export interface RunStep {
  id: string;
  iteration: number;
  agent_slug: string;
  tool_call_id: string;
  input: Record<string, unknown>;
  output: string | null;
  status: string;
  error: string | null;
  latency_ms: number | null;
  started_at: string;
  ended_at: string | null;
}

export interface RunDetail extends Run {
  steps: RunStep[];
  usage: Array<{ provider: string; model: string; tokens_input: number; tokens_output: number; cost_usd: string }>;
}

export interface OrchestratorFull {
  id: string;
  name: string;
  display_name: string;
  system_prompt: string;
  allowed_agent_ids: string[];
  llm_provider: string | null;
  llm_model: string | null;
  llm_api_key_hint: string | null;
  llm_base_url: string | null;
  max_iterations: number;
  max_parallel_tools: number;
  rate_limit_rpm: number;
  daily_budget_usd: string;
  enabled: boolean;
  voice_enabled: boolean;
  transcription_provider: string | null;
  transcription_model: string | null;
  tts_enabled: boolean;
  tts_provider: string | null;
  tts_voice: string | null;
  memory_enabled: boolean;
  summarize_every_n_calls: number;
  memory_raw_fallback_n: number;
  summarizer_provider: string | null;
  summarizer_model: string | null;
  history_window?: number | null;
  delegatable?: boolean | null;
}

export interface EntryPoint {
  id: string;
  application_id: string;
  slug: string;
  entry_point_type: 'websocket' | 'sse' | 'webrtc' | 'a2a';
  access_policy: Record<string, unknown>;
  conversation_token_limit: number | null;
  max_concurrent_sessions: number | null;
  queue_timeout_seconds: number | null;
  queue_message: string | null;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  app_orchestrator_id?: string | null;
  app_orchestrator?: AppOrchestratorOut | null;
}

export interface CanvasLayout {
  nodes?: Array<{ id: string; type?: string; position?: { x: number; y: number }; data?: Record<string, unknown> }>;
  edges?: Array<{ id: string; source: string; target: string; [k: string]: unknown }>;
  [k: string]: unknown;
}

export interface AppRuntimeConfig {
  max_concurrent_sessions: number | null;
  rate_limit_rpm: number | null;
  blocked_tokens: string[];
  blocked_user_ids: number[];
  session_timeout_minutes: number | null;
}

export interface AppOrchestratorSummary {
  id: string;
  name: string;
  display_name: string;
  llm_provider?: string | null;
  llm_model?: string | null;
  mcp_servers?: MCPServerAttachment[];
}

export interface Application {
  id: string;
  name: string;
  slug?: string;
  presentation?: Record<string, unknown>;
  enabled: boolean;
  active_revision?: number | null;
  active_status?: string | null;
  canvas?: CanvasLayout | null;
  entry_points: EntryPoint[];
  app_orchestrators: AppOrchestratorSummary[];
  runtime_config?: AppRuntimeConfig;
  created_at: string;
  updated_at: string;
}

export interface MCPServerAttachment {
  slug: string;
  tools?: string[]; // allowlist; empty/absent = all tools
}

export interface AppOrchestratorOut {
  id: string;
  application_id: string;
  name: string;
  node_id?: string | null;
  display_name: string | null;
  system_prompt: string | null;
  llm_provider: string | null;
  llm_model: string | null;
  max_iterations: number;
  max_parallel_tools: number;
  history_window: number | null;
  delegatable: boolean;
  kind: string;
  budget_tokens: number | null;
  allowed_agent_ids: string[];
  mcp_servers: MCPServerAttachment[];
  transcription_provider: string | null;
  transcription_model: string | null;
  tts_provider: string | null;
  tts_voice: string | null;
  voice_enabled?: boolean;
  tts_enabled?: boolean;
}

export interface AppOrchestratorIn {
  id?: string;
  display_name?: string | null;
  system_prompt?: string | null;
  llm_provider?: string | null;
  llm_model?: string | null;
  max_iterations?: number | null;
  max_parallel_tools?: number | null;
  history_window?: number | null;
  delegatable?: boolean | null;
  budget_tokens?: number | null;
  allowed_agent_ids?: string[] | null;
}

export interface MiddlewareDef {
  id: string;
  slug: string;
  kind: 'guard' | 'cache';
  display_name: string;
  description: string;
  config: Record<string, unknown>;
  is_builtin: boolean;
  enabled: boolean;
}

export interface MiddlewareWiringIn {
  def_id: string;
  agent_id: string;
  position: number;
  config_override: Record<string, unknown>;
  node_id: string;
  enabled: boolean;
}

export interface AccessToken {
  id: string;
  label: string;
  user_id: number;
  orchestrator_id: string | null;
  enabled: boolean;
  expires_at: string | null;
  last_used_at: string | null;
  created_at: string;
  token?: string;
}

export interface RunStats {
  total: number;
  by_status: Record<string, number>;
  total_cost_usd: number;
}

export interface TaskOut {
  id: string;
  parent_task_id: string | null;
  agent_id: string | null;
  orchestrator_id: string | null;
  context_id: string;
  state: string;
  kind: string;
  remote_task_id: string | null;
  budget_tokens: number | null;
  tokens_used: number;
  error: string | null;
  created_at: string;
  updated_at: string;
}

export interface ArtifactPart {
  kind?: string;
  text?: string;
  /** Base64-encoded raw bytes — always present for file parts (images, PDFs, binaries). */
  data?: string;
  filename?: string;
  media_type?: string;   // snake_case (normalized by adapter from v1.1+)
  mediaType?: string;    // camelCase (older records in DB — wire format from A2A SDK)
  [key: string]: unknown;
}

export interface ArtifactOut {
  id: string;
  task_id: string;
  context_id: string;
  artifact_id: string;
  name: string | null;
  parts: ArtifactPart[];
  append_index: number;
  last_chunk: boolean;
  created_at: string;
}

export interface ContextSession {
  context_id: string;
  orchestrator_name: string;
  turn_count: number;
  title: string;
  last_active: string;
}

export interface AgentCard {
  name: string;
  description?: string;
  url?: string;
  version?: string;
  capabilities?: Record<string, unknown>;
  skills?: Array<{ id: string; name: string; description?: string }>;
}

export interface BridgeHealth {
  status: string;
  postgres: string;
  redis: string;
}

export interface MonitoringConfig {
  heatmap_low:           number;
  heatmap_medium:        number;
  heatmap_high:          number;
  edge_thin:             number;
  edge_medium:           number;
  edge_thick:            number;
  panel_max_sessions:    number;
  stats_window_seconds:  number;
}

export interface SessionInfo {
  session_id: string;
  instance_id: string;
  user_id: number;
  orchestrator_name: string;
  ep_slug: string | null;
  app_id: string | null;
  context_id: string;
  started_at: string; // ISO8601
  active_agents?: string[];
}

// ── Application Definition types (Phase D) ────────────────────────────────────

export interface ComponentDefinitionSummary {
  id: string;
  kind: 'orchestrator' | 'agent' | 'middleware' | 'entry_point' | 'tool';
  namespace: string;
  name: string;
  version: number;
  display_name: string;
  description?: string;
  implementation_type: string;
  scope: 'builtin' | 'tenant';
  status: string;
  enabled: boolean;
}

export interface DefinitionRef {
  kind: string;
  namespace: string;
  name: string;
  version: number;
}

export interface ComponentInstance {
  instance_id: string;
  name?: string;          // orchestrators only — immutable Temporal key
  definition_ref: DefinitionRef;
  definition_id?: string; // UUID fast-path
  config: Record<string, unknown>;
  secret_bindings?: Record<string, string>;
}

export interface EPInstance {
  instance_id: string;
  slug: string;
  protocol: 'websocket' | 'sse' | 'webrtc' | 'a2a' | 'voice';
  root: string;           // instance_id of root orchestrator
  config?: Record<string, unknown>;
}

export interface ConnectionDef {
  source: string;
  target: string;
  type: 'entry' | 'delegation' | 'tool';
}

export interface AppDefinitionDoc {
  schema_version: 2;
  name?: string;
  components: ComponentInstance[];
  entry_points: EPInstance[];
  connections: ConnectionDef[];
}

export interface AppDefinition {
  id: string;
  application_id: string;
  tenant_id: string;
  revision: number;
  status: 'draft' | 'published';
  definition: AppDefinitionDoc;
  definition_hash: string;
  created_at: string;
  published_at?: string;
}

export interface ValidationError {
  instance_id?: string;
  field?: string;
  code: string;
  message: string;
}

export interface ValidationReport {
  valid: boolean;
  errors?: ValidationError[];
}

export interface PublishResult {
  definition_id: string;
  revision: number;
  definition_hash: string;
}

// ── Canvas A2A Agent Builder (Phase 2) ───────────────────────────────────────

export interface AgentRootDoc {
  display_name: string;
  description: string;
  version: string;
  icon?: string;
  category?: string;
  capabilities: { streaming: boolean; push_notifications: boolean };
}

export interface AgentStepDoc {
  id: string;
  type:
    | 'input' | 'llm' | 'http' | 'transform' | 'response'
    | 'branch' | 'loop' | 'parallel' | 'a2a_call' | 'human_wait' | 'stream_out';
  label?: string;
  config: Record<string, unknown>;
  next: string[];
  next_handles?: string[]; // parallel to next — named sourceHandle per outgoing edge (transform, branch)
  position?: { x: number; y: number };
}

export interface AgentSkillDoc {
  skill_id: string;
  name: string;
  description: string;
  tags: string[];
  input_modes: string[];
  output_modes: string[];
  examples: string[];
  input_schema: Record<string, unknown>;
  output_schema: Record<string, unknown>;
  steps: AgentStepDoc[];
  position?: { x: number; y: number };
}

export interface AgentDefinitionDoc {
  schema_version: 1;
  agent_slug: string;
  agent_root: AgentRootDoc;
  skills: AgentSkillDoc[];
}

export interface AgentDefinition {
  id: string;
  tenant_id: string;
  agent_slug: string;
  display_name: string;
  revision: number;
  status: 'draft' | 'published';
  definition: AgentDefinitionDoc | null;
  definition_hash: string;
  owner_id: number | null;
  owner_username: string;
  created_at: string;
  updated_at: string;
}

export interface AgentIssue {
  severity: 'error' | 'warning';
  code: string;
  message: string;
  skill_id?: string;
  node_id?: string;
  field?: string;
}

/** @deprecated use AgentIssue */
export interface AgentCompileError {
  code: string;
  message: string;
  context?: string;
}

export interface AgentValidationResult {
  valid: boolean;
  issues?: AgentIssue[];
  errors?: AgentIssue[];
}

export interface AgentPublishResult {
  agent_id: string;
  definition_id: string;
  revision: number;
  spec_hash: string;
}

export interface AgentBindingSlotStatus {
  id: string;
  application_id: string;
  agent_id: string;
  definition_id?: string;
  credential_set: Record<string, boolean>;
  config_overrides?: Record<string, unknown>;
  policies?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface AgentBindingUpsertBody {
  definition_id?: string;
  credentials: Record<string, string>;
  config_overrides?: Record<string, unknown>;
  policies?: Record<string, unknown>;
}

export interface AgentParamMeta {
  key: string;
  label: string;
  description: string;
  type: 'secret' | 'string' | 'url' | 'int' | 'bool';
  required: boolean;
  default_value?: string;
  used_by_nodes: string[];
  is_set: boolean;
  hint: string;
}

export interface AgentParamsResponse {
  agent_id: string;
  agent_slug: string;
  required_params: AgentParamMeta[];
}

export interface AgentLLMNodeStatus {
  agent_id: string;
  agent_slug: string;
  node_id: string;
  label: string;
  compiled_provider: string;
  compiled_model: string;
  override_provider?: string;
  override_model?: string;
}

// ── MCP Store ──────────────────────────────────────────────────────────────

export interface MCPTool {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
}

export interface MCPServer {
  id: string;
  tenant_id: string;
  name: string;
  slug: string;
  description: string;
  transport: 'http' | 'sse' | 'streamable-http';
  url: string;
  auth_type: 'none' | 'bearer' | 'header' | 'oauth2';
  health_status: 'unknown' | 'healthy' | 'degraded' | 'unreachable';
  last_checked_at: string | null;
  last_error: string;
  tools_manifest: MCPTool[];
  tools_count: number;
  capabilities: Record<string, unknown>;
  enabled: boolean;
  probe_credential_set: boolean;
  created_at: string;
  updated_at: string;
}

export interface MCPServerPatch {
  name?: string;
  description?: string;
  transport?: MCPServer['transport'];
  url?: string;
  auth_type?: MCPServer['auth_type'];
  enabled?: boolean;
  probe_token?: string;
}

export interface MCPServerCreate {
  name: string;
  slug: string;
  description?: string;
  transport?: MCPServer['transport'];
  url: string;
  auth_type?: MCPServer['auth_type'];
  probe_token?: string;
}

export interface MCPCredentialMeta {
  mcp_server_id: string;
  slug: string;
  name: string;
  credential_set: boolean;
  auth_header_name: string;
}

export interface AppGlobalParam {
  name: string;
  type: string;
  is_set: boolean;
  value_hint?: string;
  value?: string;
}

export const themApi = {
  health: () => fetch(`${HEALTH_BASE}/health`)
    .then((r) => r.json())
    .catch(() => ({ status: 'error', postgres: 'unknown', redis: 'unknown' })),
  agents: () => api.get<Agent[]>('/admin/agents'),
  getAgent: (id: string) => api.get<Agent>(`/admin/agents/${id}`),
  createAgent: (body: unknown) => api.post<Agent>('/admin/agents', body),
  updateAgent: (id: string, body: unknown) => api.patch<Agent>(`/admin/agents/${id}`, body),
  deleteAgent: (id: string) => api.delete<void>(`/admin/agents/${id}`),
  testAgent: (id: string) => api.post<{ ok: boolean; latency_ms: number; detail: string }>(`/admin/agents/${id}/test`, {}),
  scanAgent: (id: string) => api.post<{ job_id: string; agent_id: string }>(`/admin/agents/${id}/security-scan`, {}),
  discoverAgent: (body: { endpoint_url: string; auth_token?: string; agent_id?: string }) => api.post<DiscoverResult>('/admin/agents/discover', body),
  orchestrators: () => api.get<OrchestratorFull[]>('/admin/orchestrators'),
  createOrchestrator: (body: unknown) => api.post<OrchestratorFull>('/admin/orchestrators', body),
  updateOrchestrator: (id: string, body: unknown) => api.patch<OrchestratorFull>(`/admin/orchestrators/${id}`, body),
  deleteOrchestrator: (id: string) => api.delete<void>(`/admin/orchestrators/${id}`),
  testLlm: (id: string, body: unknown) => api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/orchestrators/${id}/test-llm`, body),
  testVoice: (id: string, body: unknown) => api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/orchestrators/${id}/test-voice`, body),
  testTts: (id: string, body: unknown) => api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/orchestrators/${id}/test-tts`, body),
  tts: async (name: string, text: string): Promise<Response> => {
    const res = await fetch(`/api/them/orchestrators/${name}/tts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
    });
    if (!res.ok) throw new Error(await res.text());
    return res;
  },
  getOrchestrator: async (name: string): Promise<OrchestratorFull | undefined> => {
    const list = await api.get<OrchestratorFull[]>('/admin/orchestrators');
    return list.find((o) => o.name === name);
  },
  transcribe: async (name: string, audio: Blob): Promise<{ text: string }> => {
    const form = new FormData();
    form.append('audio', audio, 'recording.webm');
    const res = await fetch(`${API_BASE}/orchestrators/${name}/transcribe`, {
      method: 'POST',
      body: form,
    });
    if (!res.ok) throw new Error(await res.text());
    return res.json();
  },
  applications: () => api.get<Application[]>('/admin/applications'),
  getApplication: (id: string) => api.get<Application>(`/admin/applications/${id}`),
  createApplication: async (body: unknown): Promise<Application> => {
    const { id } = await api.post<{ id: string }>('/admin/applications', body);
    return api.get<Application>(`/admin/applications/${id}`);
  },
  updateApplication: (id: string, body: unknown) => api.patch<Application>(`/admin/applications/${id}`, body),
  deleteApplication: (id: string) => api.delete<void>(`/admin/applications/${id}`),
  listMiddlewareDefs: () => api.get<MiddlewareDef[]>('/admin/middleware-defs'),
  putMiddlewareWirings: (appId: string, wirings: MiddlewareWiringIn[]) =>
    api.put<void>(`/admin/applications/${appId}/middleware-wirings`, { wirings }),
  tokens: () => api.get<AccessToken[]>('/admin/tokens'),
  createToken: (body: unknown) => api.post<AccessToken>('/admin/tokens', body),
  updateToken: (id: string, body: unknown) => api.patch<AccessToken>(`/admin/tokens/${id}`, body),
  deleteToken: (id: string) => api.delete<void>(`/admin/tokens/${id}`),
  runs: (limit = 20) => api.get<Run[]>(`/runs?limit=${limit}`),
  cancelRun: (runId: string) => api.patch<Run>(`/runs/${runId}/cancel`, {}),
  runDetail: (runId: string) => api.get<RunDetail>(`/runs/${runId}`),
  runStats: () => api.get<RunStats>('/runs/stats'),
  runTasks: (runId: string) => api.get<TaskOut[]>(`/runs/${runId}/tasks`),
  runArtifacts: (runId: string) => api.get<ArtifactOut[]>(`/runs/${runId}/artifacts`),
  contextArtifacts: (contextId: string, limit = 100) =>
    api.get<ArtifactOut[]>(`/runs/context/${contextId}/artifacts?limit=${limit}`),
  contextMessages: (contextId: string, limit = 100) =>
    api.get<{ role: string; text: string }[]>(`/runs/context/${contextId}/messages?limit=${limit}`),
  contexts: (orchestrator?: string, limit = 50) =>
    api.get<ContextSession[]>(`/runs/contexts?limit=${limit}${orchestrator ? `&orchestrator=${orchestrator}` : ''}`),
  fetchAgentCard: async (endpointUrl: string): Promise<AgentCard> => {
    const base = endpointUrl.replace(/\/+$/, '');
    const res = await fetch(`${base}/.well-known/agent-card.json`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return res.json();
  },
  getSystemAgents: () => api.get<SystemAgentsOut>('/admin/system-agents'),
  putSystemAgents: (body: { roles: Record<string, SystemAgentRoleIn> }) => api.put<SystemAgentsOut>('/admin/system-agents', body),
  testSystemAgentLlm: (role: string, body: { provider: string; model: string; api_key?: string; base_url?: string }) =>
    api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/system-agents/${role}/test-llm`, body),
  testAppOrchLlm: (appId: string, aoId: string, body: { provider: string; model: string; api_key?: string; base_url?: string }) =>
    api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/applications/${appId}/orchestrators/${aoId}/test-llm`, body),
  testAppOrchVoice: (appId: string, aoId: string, body: { provider: string; model: string }) =>
    api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/applications/${appId}/orchestrators/${aoId}/test-voice`, body),
  testAppOrchTts: (appId: string, aoId: string, body: { provider: string; voice: string }) =>
    api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/applications/${appId}/orchestrators/${aoId}/test-tts`, body),
  deleteRun: (runId: string) => api.delete<void>(`/runs/${runId}`),
  bulkDeleteRuns: (runIds: string[]) => api.post<{ deleted: number }>('/runs/bulk-delete', { run_ids: runIds }),
  bulkDeleteApplications: (appIds: string[]) => api.post<{ deleted: number }>('/admin/applications/bulk-delete', { app_ids: appIds }),
  listSessions: (appId: string) => api.get<{ sessions: SessionInfo[]; count: number }>(`/admin/sessions?app_id=${appId}`),
  disconnectSession: (sessionId: string) => api.post<{ session_id: string; signal_delivered: boolean }>(`/admin/sessions/${sessionId}/disconnect`, {}),
  getAppRuntime: (appId: string) => api.get<Application>(`/admin/applications/${appId}`).then(a => a.runtime_config ?? { max_concurrent_sessions: null, rate_limit_rpm: null, blocked_tokens: [], blocked_user_ids: [], session_timeout_minutes: null }),
  putAppRuntime: (appId: string, config: AppRuntimeConfig) => api.put<AppRuntimeConfig>(`/admin/applications/${appId}/runtime`, config),
  getProviderKeys: (appId: string) => api.get<{ provider: string; key_set: boolean; key_hint?: string }[]>(`/admin/applications/${appId}/provider-keys`),
  setProviderKey: (appId: string, provider: string, key: string) => api.put<{ provider: string; updated: boolean }>(`/admin/applications/${appId}/provider-keys/${provider}`, { key }),
  deleteProviderKey: (appId: string, provider: string) => api.delete<{ provider: string; deleted: boolean }>(`/admin/applications/${appId}/provider-keys/${provider}`),
  getAppParams: (appId: string) => api.get<AppGlobalParam[]>(`/admin/applications/${appId}/app-params`),
  setAppParam: (appId: string, name: string, value: string, type: string) => api.put<{ name: string; updated: boolean }>(`/admin/applications/${appId}/app-params/${name}`, { value, type }),
  deleteAppParam: (appId: string, name: string) => api.delete<{ name: string; deleted: boolean }>(`/admin/applications/${appId}/app-params/${name}`),
  testAppLlm: (appId: string, provider: string, model: string) => api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/applications/${appId}/test-llm`, { provider, model }),
  patchOrchestratorLLM: (appId: string, orchId: string, provider: string, model: string) => api.patch<{ id: string; llm_provider: string; llm_model: string }>(`/admin/applications/${appId}/orchestrators/${orchId}/llm`, { provider, model }),
  patchOrchestratorMCPServers: (appId: string, orchId: string, mcpServers: MCPServerAttachment[]) => api.patch<{ id: string; mcp_servers: MCPServerAttachment[] }>(`/admin/applications/${appId}/orchestrators/${orchId}/mcp-servers`, { mcp_servers: mcpServers }),
  getMonitoringConfig: () => api.get<MonitoringConfig>('/admin/monitoring-config'),
  putMonitoringConfig: (body: MonitoringConfig) => api.put<MonitoringConfig>('/admin/monitoring-config', body),
  // Live reachability check for a deployed application slug
  pingApp: async (slug: string): Promise<boolean> => {
    try {
      const res = await fetch(`/api/apps/${slug}`, { method: 'GET' });
      return res.ok;
    } catch {
      return false;
    }
  },

  // Component Registry (Phase D)
  listComponentDefinitions: () =>
    api.get<ComponentDefinitionSummary[]>('/admin/component-definitions'),

  // Application Definitions (Phase D)
  listDefinitions: (appId: string) =>
    api.get<AppDefinition[]>(`/admin/applications/${appId}/definitions`),
  createDefinition: (appId: string, body: { definition: AppDefinitionDoc }) =>
    api.post<{ id: string; revision: number }>(`/admin/applications/${appId}/definitions`, body),
  updateDefinition: (appId: string, defId: string, body: { definition: AppDefinitionDoc }) =>
    api.put<{ id: string; updated: boolean }>(`/admin/applications/${appId}/definitions/${defId}`, body),
  deleteDefinition: (appId: string, defId: string) =>
    api.delete<void>(`/admin/applications/${appId}/definitions/${defId}`),
  validateDefinition: (appId: string, defId: string) =>
    api.post<ValidationReport>(`/admin/applications/${appId}/definitions/${defId}/validate`, {}),
  publishDefinition: (appId: string, defId: string) =>
    api.post<PublishResult>(`/admin/applications/${appId}/definitions/${defId}/publish`, {}),

  // Canvas A2A Agent Builder (Phase 2)
  listAgentDefinitions: () =>
    api.get<AgentDefinition[]>('/admin/agent-definitions'),
  getAgentDefinition: (id: string) =>
    api.get<AgentDefinition>(`/admin/agent-definitions/${id}`),
  createAgentDefinition: (body: { agent_slug: string; definition: AgentDefinitionDoc }) =>
    api.post<{ id: string; revision: number }>('/admin/agent-definitions', body),
  updateAgentDefinition: (id: string, body: { definition: AgentDefinitionDoc }) =>
    api.put<{ id: string; updated: boolean }>(`/admin/agent-definitions/${id}`, body),
  deleteAgentDefinition: (id: string) =>
    api.delete<void>(`/admin/agent-definitions/${id}`),
  cloneAgentDefinition: (id: string, agentSlug?: string) =>
    api.post<{ id: string; revision: number }>(`/admin/agent-definitions/${id}/clone`, { agent_slug: agentSlug ?? '' }),

  // Phase 3: validate + publish
  // Always resolves (never throws). Accepts an optional AbortSignal and an
  // optional definition object. When definition is provided it is sent in the
  // request body so the backend validates the live canvas state rather than the
  // last-saved DB copy. On 422 the structured errors are returned as issues.
  validateAgentDefinition: async (
    id: string,
    definition?: AgentDefinitionDoc,
    signal?: AbortSignal,
  ): Promise<AgentValidationResult> => {
    const body = definition ? JSON.stringify({ definition }) : '{}';
    let res: Response;
    try {
      res = await fetch(`/api/them/admin/agent-definitions/${id}/validate`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body,
        signal,
      });
    } catch (e) {
      if ((e as { name?: string }).name === 'AbortError') throw e;
      return { valid: false, issues: [] }; // network error — keep prior results
    }
    const parsed = await res.json().catch(() => ({})) as AgentValidationResult;
    // 200: {valid, issues}  422: {valid:false, errors:[...]}
    if (!res.ok) {
      const errors = (parsed as { errors?: AgentIssue[] }).errors ?? [];
      return { valid: false, issues: errors };
    }
    return parsed;
  },
  publishAgentDefinition: (id: string) =>
    api.post<AgentPublishResult>(`/admin/agent-definitions/${id}/publish`, {}),
  getDefinitionParams: (id: string) =>
    api.get<AgentParamsResponse>(`/admin/agent-definitions/${id}/params`),

  // Phase 3: application agent bindings
  listAgentBindings: (appId: string) =>
    api.get<AgentBindingSlotStatus[]>(`/admin/applications/${appId}/agent-bindings`),
  getAgentBinding: (appId: string, agentId: string) =>
    api.get<AgentBindingSlotStatus>(`/admin/applications/${appId}/agent-bindings/${agentId}`),
  upsertAgentBinding: (appId: string, agentId: string, body: AgentBindingUpsertBody) =>
    api.post<{ application_id: string; agent_id: string; updated: boolean }>(
      `/admin/applications/${appId}/agent-bindings/${agentId}`,
      body,
    ),
  deleteAgentBinding: (appId: string, agentId: string) =>
    api.delete<void>(`/admin/applications/${appId}/agent-bindings/${agentId}`),

  // Agent runtime params (Phase 1 — per-binding encrypted params)
  getAgentParams: (appId: string, agentId: string) =>
    api.get<AgentParamsResponse>(`/admin/applications/${appId}/agents/${agentId}/params`),
  putAgentParams: (appId: string, agentId: string, params: Record<string, string>) =>
    api.put<void>(`/admin/applications/${appId}/agents/${agentId}/params`, { params }),
  getAgentLLMNodes: (appId: string, agentId: string) =>
    api.get<AgentLLMNodeStatus[]>(`/admin/applications/${appId}/agents/${agentId}/llm-nodes`),
  putNodeLLMOverride: (appId: string, agentId: string, nodeId: string, provider: string, model: string) =>
    api.put<{ node_id: string; updated: boolean }>(`/admin/applications/${appId}/agents/${agentId}/llm-nodes/${nodeId}`, { provider, model }),

  // MCP Store — server registry + app credentials
  listMCPServers: () =>
    api.get<MCPServer[]>('/admin/mcp-servers'),
  createMCPServer: (body: MCPServerCreate) =>
    api.post<MCPServer>('/admin/mcp-servers', body),
  getMCPServer: (id: string) =>
    api.get<MCPServer>(`/admin/mcp-servers/${id}`),
  updateMCPServer: (id: string, body: MCPServerPatch) =>
    api.patch<MCPServer>(`/admin/mcp-servers/${id}`, body),
  deleteMCPServer: (id: string) =>
    api.delete<void>(`/admin/mcp-servers/${id}`),
  probeMCPServer: (id: string) =>
    api.post<{ health_status: string; tools_count: number; last_error?: string }>(`/admin/mcp-servers/${id}/probe`, {}),

  listAppMCPCredentials: (appId: string) =>
    api.get<MCPCredentialMeta[]>(`/admin/applications/${appId}/mcp-credentials`),
  setAppMCPCredential: (appId: string, serverId: string, body: { credential: string; auth_header_name?: string }) =>
    api.put<void>(`/admin/applications/${appId}/mcp-credentials/${serverId}`, body),
  deleteAppMCPCredential: (appId: string, serverId: string) =>
    api.delete<void>(`/admin/applications/${appId}/mcp-credentials/${serverId}`),
};
