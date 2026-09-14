export interface Tenant {
  id: string;
  slug: string;
  display_name: string;
  enabled: boolean;
}

export interface App {
  id: string;
  slug: string;
  name: string;
  enabled: boolean;
}

export interface EP {
  id: string;
  slug: string;
  entry_point_type: string;
  access_policy: { mode: string };
  allowed_principals: string;
  enabled: boolean;
}

export interface Scenario {
  id: string;
  name: string;
  tenant_slug: string;
  app_id: string;
  app_slug: string;
  ep_slug: string;
  auth_mode: 'token' | 'public' | 'user_jwt' | 'external_jwt';
  n_users: number;
  messages: string[];
  created_at: string;
  updated_at: string;
}

export interface StepResult {
  sent: string;
  received: string;
  latency_ms: number;
  ok: boolean;
  error?: string;
}

export interface UserResult {
  user_index: number;
  token_id?: string;
  connected: boolean;
  steps: StepResult[];
  error?: string;
  duration_ms: number;
  status: 'running' | 'passed' | 'failed';
}

export interface RunSummary {
  run_id: string;
  scenario_id: string;
  scenario_name: string;
  n_users: number;
  passed: number;
  failed: number;
  started_at: string;
  ended_at?: string;
  results?: UserResult[];
}

export interface RunEvent {
  type: 'user_update' | 'run_complete';
  user_result?: UserResult;
  summary?: RunSummary;
}

export interface ConfigState {
  them_url: string;
  admin_user: string;
  admin_pass_set: boolean;
}
