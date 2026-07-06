const API_BASE = '/api/them';
const HEALTH_BASE = '/api/bridge';

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  };

  const res = await fetch(`${API_BASE}${path}`, { ...options, headers });

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
  patch: <T>(path: string, body: unknown) => request<T>(path, { method: 'PATCH', body: JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
};

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
  agent_card: Record<string, unknown> | null;
  agent_card_url: string;
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
  agent_card?: Record<string, unknown> | null;
  agent_card_url?: string | null;
  card_fetched_at?: string | null;
  created_at: string;
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

export const themApi = {
  health: () => fetch(`${HEALTH_BASE}/health`)
    .then((r) => r.json())
    .catch(() => ({ status: 'error', postgres: 'unknown', redis: 'unknown' })),
  agents: () => api.get<Agent[]>('/admin/agents'),
  createAgent: (body: unknown) => api.post<Agent>('/admin/agents', body),
  updateAgent: (id: string, body: unknown) => api.patch<Agent>(`/admin/agents/${id}`, body),
  deleteAgent: (id: string) => api.delete<void>(`/admin/agents/${id}`),
  testAgent: (id: string) => api.post<{ ok: boolean; latency_ms: number; detail: string }>(`/admin/agents/${id}/test`, {}),
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
  tokens: () => api.get<AccessToken[]>('/admin/tokens'),
  createToken: (body: unknown) => api.post<AccessToken>('/admin/tokens', body),
  updateToken: (id: string, body: unknown) => api.patch<AccessToken>(`/admin/tokens/${id}`, body),
  deleteToken: (id: string) => api.delete<void>(`/admin/tokens/${id}`),
  runs: (limit = 20) => api.get<Run[]>(`/runs?limit=${limit}`),
  runDetail: (runId: string) => api.get<RunDetail>(`/runs/${runId}`),
  runStats: () => api.get<RunStats>('/runs/stats'),
  runTasks: (runId: string) => api.get<TaskOut[]>(`/runs/${runId}/tasks`),
  runArtifacts: (runId: string) => api.get<ArtifactOut[]>(`/runs/${runId}/artifacts`),
  contextArtifacts: (contextId: string, limit = 100) =>
    api.get<ArtifactOut[]>(`/runs/context/${contextId}/artifacts?limit=${limit}`),
  fetchAgentCard: async (endpointUrl: string): Promise<AgentCard> => {
    const base = endpointUrl.replace(/\/+$/, '');
    const res = await fetch(`${base}/.well-known/agent-card.json`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return res.json();
  },
};
