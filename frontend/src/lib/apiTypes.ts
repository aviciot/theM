// All shared API types for the-M frontend.
// Imported by lib/api.ts and re-exported from there — callers should use '@/lib/api'.

export interface UserPreferences {
  agentFolders?: { folders: Array<{ id: string; name: string; agentIds: string[]; collapsed: boolean }> };
  [key: string]: unknown;
}

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
  entry_point_type: 'websocket' | 'sse' | 'webrtc' | 'a2a' | 'voice';
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
  memory_enabled?: boolean;
  summarize_every_n_calls?: number;
  memory_raw_fallback_n?: number;
  summarizer_provider?: string | null;
  summarizer_model?: string | null;
  llm_provider?: string | null;
  llm_model?: string | null;
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
  transcription_provider?: string | null;
  transcription_model?: string | null;
  tts_provider?: string | null;
  tts_voice?: string | null;
  voice_enabled?: boolean;
  tts_enabled?: boolean;
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
  run_id?: string;
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
  /** DAG execution backend. "" or "local" → in-process goroutines. "temporal" → CanvasAgentWorkflow. */
  execution_backend?: 'local' | 'temporal';
}

export interface AgentStepDoc {
  id: string;
  type:
    | 'input' | 'llm' | 'http' | 'transform' | 'response'
    | 'branch' | 'loop' | 'parallel' | 'a2a_call' | 'human_wait' | 'stream_out' | 'mcp_call';
  label?: string;
  config: Record<string, unknown>;
  next: string[];
  next_handles?: string[]; // parallel to next — named sourceHandle per outgoing edge (transform, branch)
  /** Explicit data bindings: input port ID → {from_step, from_port}. Absent means heuristic path. */
  inputs?: Record<string, Binding>;
  /** Optional per-node execution policy override. Absent means use NodeDef defaults. */
  policy?: { max_attempts?: number; timeout_seconds?: number; initial_interval_seconds?: number; backoff_coefficient?: number; max_interval_seconds?: number; };
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

export interface VarRef {
  name: string;
  required: boolean;
  port_id?: string;    // stable port handle; empty on heuristic path
  source_step?: string; // step ID that produces this value (from explicit binding)
  source_port?: string; // output port on the source step
}

/** One explicit data binding declared on a canvas step's input port. */
export interface Binding {
  from_step: string; // step ID of the producing step
  from_port: string; // output port ID on that step
}

export interface StepContract {
  inputs: VarRef[];
  outputs: VarRef[];
}

export interface AgentValidationResult {
  valid: boolean;
  issues?: AgentIssue[];
  errors?: AgentIssue[];
  step_contracts?: Record<string, StepContract>;
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

export interface AVScanConfig {
  enabled: boolean;
  max_file_mb: number;
  block_on_error: boolean;
}

export interface SecurityConfig {
  enabled: boolean;
  processors?: Record<string, AVScanConfig | Record<string, unknown>>;
}

export interface ArtifactScanEvent {
  type: 'artifact_scan';
  artifact_id: string;
  scan_status: 'pending' | 'scanning' | 'clean' | 'infected' | 'error' | 'disabled';
  processor?: string;
  detail?: string;
}

export interface DailyTrendRow { day: string; total: number; clean: number; infected: number; error: number; }
export interface AppScanRow { app_id: string; app_slug: string; scanned: number; clean: number; error: number; }
export interface RecentJobRow { job_id: string; artifact_id: string; status: string; processor: string; outcome: string | null; duration_ms: number | null; created_at: string; }

export interface SecurityScanStats {
  total_artifacts: number;
  scanned: number;
  clean: number;
  infected: number;
  error: number;
  pending: number;
  disabled: number;
  success_rate: number;
  avg_latency_ms: number;
  p95_latency_ms: number;
  quarantine_total: number;
  quarantine_expired: number;
  daily_trend: DailyTrendRow[];
  app_breakdown: AppScanRow[];
  recent_jobs: RecentJobRow[];
}

export interface ServicesStats {
  security: SecurityScanStats;
  worker_up: boolean;
}

// ── Tenant types ─────────────────────────────────────────────────────────────

export interface IDPConfig {
  discovery_url: string;
  client_id: string;
  redirect_uri: string;
  client_secret?: string; // write-only — sent on save, never returned by the API
}

export interface TenantRecord {
  id: string;
  slug: string;
  display_name: string;
  enabled: boolean;
  idp_configured: boolean;
  created_at: string;
  updated_at: string;
}

export interface TenantPatch {
  display_name?: string;
  enabled?: boolean;
  idp_config?: IDPConfig | null; // null = clear IdP config
}

// ── Managed App types ─────────────────────────────────────────────────────────

export interface ManagedApp {
  id: string;
  name: string;
  slug: string;
  version: string;
  changelog?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ManagedAppParam {
  id: string;
  app_id: string;
  key: string;
  label: string;
  description?: string;
  param_type: string;
  enum_values?: string[];
  required: boolean;
  default_value?: string;
  sort_order: number;
}

export interface ManagedAppDetail extends ManagedApp {
  params: ManagedAppParam[];
}

export interface ManagedAppBinding {
  id: string;
  app_id: string;
  tenant_id: string;
  enabled: boolean;
  config: Record<string, string>;
  app_version: string;
  created_at: string;
  updated_at: string;
}

export interface ManagedAppBindingInput {
  config: Record<string, string>;
  app_version?: string;
  enabled?: boolean;
}
