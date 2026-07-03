const API_BASE = '/api/odin';
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
  total_tokens: number;
  cost_usd: number;
  started_at: string;
  completed_at: string | null;
  duration_ms: number | null;
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

export interface BridgeHealth {
  status: string;
  postgres: string;
  redis: string;
}

export const odinApi = {
  health: () => fetch(`${HEALTH_BASE}/health`)
    .then((r) => r.json())
    .catch(() => ({ status: 'error', postgres: 'unknown', redis: 'unknown' })),
  agents: () => api.get<Agent[]>('/admin/agents'),
  createAgent: (body: unknown) => api.post<Agent>('/admin/agents', body),
  updateAgent: (id: string, body: unknown) => api.patch<Agent>(`/admin/agents/${id}`, body),
  deleteAgent: (id: string) => api.delete<void>(`/admin/agents/${id}`),
  testAgent: (id: string) => api.post<{ ok: boolean; latency_ms: number; detail: string }>(`/admin/agents/${id}/test`, {}),
  orchestrators: () => api.get<OrchestratorFull[]>('/admin/orchestrators'),
  createOrchestrator: (body: unknown) => api.post<OrchestratorFull>('/admin/orchestrators', body),
  updateOrchestrator: (id: string, body: unknown) => api.patch<OrchestratorFull>(`/admin/orchestrators/${id}`, body),
  deleteOrchestrator: (id: string) => api.delete<void>(`/admin/orchestrators/${id}`),
  testLlm: (id: string, body: unknown) => api.post<{ ok: boolean; latency_ms?: number; error?: string }>(`/admin/orchestrators/${id}/test-llm`, body),
  getOrchestrator: async (name: string): Promise<OrchestratorFull | undefined> => {
    const list = await api.get<OrchestratorFull[]>('/admin/orchestrators');
    return list.find((o) => o.name === name);
  },
  transcribe: async (name: string, audio: Blob): Promise<{ text: string }> => {
    const form = new FormData();
    form.append('audio', audio, 'recording.webm');
    const res = await fetch(`${API_BASE}/api/v1/orchestrators/${name}/transcribe`, {
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
  runs: (limit = 20) => api.get<{ items: Run[]; total: number }>(`/runs?limit=${limit}`),
  runStats: () => api.get<RunStats>('/runs/stats'),
};
