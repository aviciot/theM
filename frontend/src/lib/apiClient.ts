// Base HTTP client for the-M frontend — request(), tryRefresh(), api object, auth helpers.
// Import from '@/lib/api' in application code, not directly from here.

import type { UserPreferences } from './apiTypes';

export const API_BASE = '/api/them';
export const HEALTH_BASE = '/api/bridge';

let _refreshing: Promise<boolean> | null = null;

export async function tryRefresh(): Promise<boolean> {
  if (_refreshing) return _refreshing;
  _refreshing = fetch('/api/auth/refresh', { method: 'POST' })
    .then(r => r.ok)
    .catch(() => false)
    .finally(() => { _refreshing = null; });
  return _refreshing;
}

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
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
    throw new Error(err.detail || err.error || err.message || `HTTP ${res.status}`);
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
