const API_BASE = process.env.NEXT_PUBLIC_API_URL || '/api/odin';
const HEALTH_BASE = '/api/bridge';

function getToken() {
  if (typeof window === 'undefined') return null;
  return localStorage.getItem('odin_access_token');
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  };
  if (token) headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}${path}`, { ...options, headers });

  if (res.status === 401 && typeof window !== 'undefined') {
    localStorage.removeItem('odin_access_token');
    localStorage.removeItem('odin_refresh_token');
    window.location.href = '/login';
    throw new Error('Unauthorized');
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
  patch: <T>(path: string, body: unknown) => request<T>(path, { method: 'PATCH', body: JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
};

// ── Typed API calls ──────────────────────────────────────────────────────────

export interface Agent {
  id: string;
  slug: string;
  name: string;
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
  health: () => fetch(`${HEALTH_BASE}/health`, { headers: { Authorization: `Bearer ${getToken()}` } })
    .then((r) => r.json())
    .catch(() => ({ status: 'error', postgres: 'unknown', redis: 'unknown' })),
  agents: () => api.get<Agent[]>('/admin/agents'),
  orchestrators: () => api.get<Orchestrator[]>('/admin/orchestrators'),
  runs: (limit = 20) => api.get<{ items: Run[]; total: number }>(`/runs?limit=${limit}`),
  runStats: () => api.get<RunStats>('/runs/stats'),
};
