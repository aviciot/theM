import type { App, ConfigState, EP, RunSummary, Scenario, Tenant } from './types';

const BASE = '/api/backend';

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error ?? res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

export const api = {
  // Config
  getConfig: () => req<ConfigState>('GET', '/config'),
  putConfig: (them_url: string, admin_user: string, admin_pass: string) =>
    req('PUT', '/config', { them_url, admin_user, admin_pass }),
  testConfig: () => req<{ ok: boolean; error?: string; tenants_found: number }>('POST', '/config/test'),

  // Catalog
  listTenants: () => req<Tenant[]>('GET', '/tenants'),
  listApps: (slug: string) => req<App[]>('GET', `/tenants/${slug}/apps`),
  listEPs: (appID: string) => req<EP[]>('GET', `/apps/${appID}/eps`),

  // Scenarios
  listScenarios: () => req<Scenario[]>('GET', '/scenarios'),
  createScenario: (sc: Partial<Scenario>) => req<Scenario>('POST', '/scenarios', sc),
  updateScenario: (id: string, sc: Partial<Scenario>) => req<Scenario>('PUT', `/scenarios/${id}`, sc),
  deleteScenario: (id: string) => req('DELETE', `/scenarios/${id}`),

  // Runs
  startRun: (scenario_id: string, n_users?: number, messages?: string[]) =>
    req<{ run_id: string }>('POST', '/run', { scenario_id, n_users, messages }),
  cancelRun: (runId: string) => req('DELETE', `/run/${runId}`),

  // History
  listHistory: () => req<RunSummary[]>('GET', '/history'),
  getHistory: (runId: string) => req<RunSummary>('GET', `/history/${runId}`),
  deleteHistory: (runId: string) => req('DELETE', `/history/${runId}`),
};
