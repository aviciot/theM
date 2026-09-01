// Public surface for all API calls — import from '@/lib/api' everywhere.
// Types live in apiTypes.ts; base client lives in apiClient.ts.

export type {
  UserPreferences,
  AgentSkill,
  DiscoverResult,
  SystemAgentRoleOut,
  SystemAgentsOut,
  SystemAgentRoleIn,
  Agent,
  ScanFinding,
  ScanResult,
  Orchestrator,
  Run,
  RunStep,
  RunDetail,
  OrchestratorFull,
  EntryPoint,
  CanvasLayout,
  AppRuntimeConfig,
  AppOrchestratorSummary,
  Application,
  MCPServerAttachment,
  AppOrchestratorOut,
  AppOrchestratorIn,
  MiddlewareDef,
  MiddlewareWiringIn,
  AccessToken,
  RunStats,
  TaskOut,
  ArtifactPart,
  ArtifactOut,
  ContextSession,
  AgentCard,
  BridgeHealth,
  MonitoringConfig,
  SessionInfo,
  ComponentDefinitionSummary,
  DefinitionRef,
  ComponentInstance,
  EPInstance,
  ConnectionDef,
  AppDefinitionDoc,
  AppDefinition,
  ValidationError,
  ValidationReport,
  PublishResult,
  AgentRootDoc,
  AgentStepDoc,
  AgentSkillDoc,
  AgentDefinitionDoc,
  AgentDefinition,
  AgentIssue,
  AgentCompileError,
  VarRef,
  Binding,
  StepContract,
  AgentValidationResult,
  AgentPublishResult,
  AgentBindingSlotStatus,
  AgentBindingUpsertBody,
  AgentParamMeta,
  AgentParamsResponse,
  AgentLLMNodeStatus,
  MCPTool,
  MCPServer,
  MCPServerPatch,
  MCPServerCreate,
  MCPCredentialMeta,
  AppGlobalParam,
} from './apiTypes';

export { api, getPreferences, setPreferences } from './apiClient';

import { api } from './apiClient';
import { API_BASE, HEALTH_BASE } from './apiClient';
import type {
  Agent,
  OrchestratorFull,
  DiscoverResult,
  SystemAgentsOut,
  SystemAgentRoleIn,
  Application,
  AppRuntimeConfig,
  MiddlewareWiringIn,
  AccessToken,
  Run,
  RunDetail,
  RunStats,
  TaskOut,
  ArtifactOut,
  ContextSession,
  AgentCard,
  MonitoringConfig,
  SessionInfo,
  ComponentDefinitionSummary,
  AppDefinitionDoc,
  AppDefinition,
  ValidationReport,
  PublishResult,
  AgentDefinitionDoc,
  AgentDefinition,
  AgentIssue,
  AgentValidationResult,
  AgentPublishResult,
  AgentBindingSlotStatus,
  AgentBindingUpsertBody,
  AgentParamsResponse,
  AgentLLMNodeStatus,
  MCPServer,
  MCPServerPatch,
  MCPServerCreate,
  MCPCredentialMeta,
  MCPServerAttachment,
  AppGlobalParam,
  EntryPoint,
} from './apiTypes';

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
  voiceChat: async (appSlug: string, slug: string, audio: Blob, signal?: AbortSignal): Promise<{ transcript: string; reply: string; audioBlob: Blob }> => {
    const form = new FormData();
    form.append('audio', audio, 'recording.webm');
    const timeoutCtrl = new AbortController();
    const timer = setTimeout(() => timeoutCtrl.abort(), 30000);
    // Combine the caller's abort signal with the internal 30s timeout signal
    const combined = signal
      ? AbortSignal.any([signal, timeoutCtrl.signal])
      : timeoutCtrl.signal;
    try {
      const res = await fetch(`/api/them/apps/${appSlug}/${slug}/voice/chat`, {
        method: 'POST',
        body: form,
        signal: combined,
      });
      if (!res.ok) {
        const errText = await res.text().catch(() => `HTTP ${res.status}`);
        throw new Error(errText);
      }
      const transcript = res.headers.get('X-Transcript') ?? '';
      const reply = res.headers.get('X-Reply') ?? '';
      const audioBlob = await res.blob();
      return { transcript, reply, audioBlob };
    } finally {
      clearTimeout(timer);
    }
  },
  // voiceTTS POSTs text to the voice EP's TTS endpoint and returns the audio Blob.
  voiceTTS: async (appSlug: string, slug: string, text: string, signal?: AbortSignal): Promise<Blob> => {
    const combined = signal ?? undefined;
    const res = await fetch(`/api/them/apps/${appSlug}/${slug}/voice/tts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
      signal: combined,
    });
    if (!res.ok) throw new Error(`TTS failed: HTTP ${res.status}`);
    return res.blob();
  },
  // voiceStream POSTs audio and returns an async iterator of parsed SSE events:
  //   { type: 'transcript', text: string }
  //   { type: 'token', content: string }
  //   { type: 'done', text: string }      — full reply
  //   { type: 'error', message: string }
  voiceStream: async function* (appSlug: string, slug: string, audio: Blob, signal?: AbortSignal): AsyncGenerator<Record<string, unknown>> {
    const form = new FormData();
    form.append('audio', audio, 'recording.webm');
    const timeoutCtrl = new AbortController();
    const timer = setTimeout(() => timeoutCtrl.abort(), 90000);
    const combined = signal ? AbortSignal.any([signal, timeoutCtrl.signal]) : timeoutCtrl.signal;
    try {
      const res = await fetch(`/api/them/apps/${appSlug}/${slug}/voice/stream`, {
        method: 'POST',
        body: form,
        signal: combined,
      });
      if (!res.ok) {
        const errText = await res.text().catch(() => `HTTP ${res.status}`);
        throw new Error(errText);
      }
      const reader = res.body!.getReader();
      const decoder = new TextDecoder();
      let buf = '';
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const parts = buf.split('\n\n');
        buf = parts.pop() ?? '';
        for (const part of parts) {
          const line = part.trim();
          if (!line.startsWith('data:')) continue;
          try { yield JSON.parse(line.slice(5).trim()); } catch { /* skip malformed */ }
        }
      }
    } finally {
      clearTimeout(timer);
    }
  },
  // a2aStream: POSTs a message/stream JSON-RPC request to an A2A entry point and
  // yields parsed SSE event bodies: { kind, parts?, status?, taskId? }
  // Auth is handled by the Next.js proxy via the them_access_token session cookie.
  a2aStream: async function* (appSlug: string, slug: string, text: string, _bearerToken: string, signal?: AbortSignal): AsyncGenerator<Record<string, unknown>> {
    const body = JSON.stringify({
      jsonrpc: '2.0',
      id: `pg-${Date.now()}`,
      method: 'SendStreamingMessage',
      params: {
        message: { messageId: `msg-${Date.now()}`, role: 'user', parts: [{ text }] },
      },
    });
    const res = await fetch(`/api/them/a2a/${appSlug}/${slug}`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Accept': 'text/event-stream',
      },
      body,
      signal,
    });
    if (!res.ok) {
      const errText = await res.text().catch(() => `HTTP ${res.status}`);
      throw new Error(errText);
    }
    const reader = res.body!.getReader();
    const decoder = new TextDecoder();
    let buf = '';
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      const parts = buf.split('\n\n');
      buf = parts.pop() ?? '';
      for (const part of parts) {
        const line = part.trim();
        if (!line.startsWith('data:')) continue;
        try {
          const frame = JSON.parse(line.slice(5).trim()) as { result?: { event?: Record<string, unknown> } };
          const event = frame?.result?.event;
          if (event) yield event;
        } catch { /* skip malformed */ }
      }
    }
  },
  applications: () => api.get<Application[]>('/admin/applications'),
  getApplication: (id: string) => api.get<Application>(`/admin/applications/${id}`),
  createApplication: async (body: unknown): Promise<Application> => {
    const { id } = await api.post<{ id: string }>('/admin/applications', body);
    return api.get<Application>(`/admin/applications/${id}`);
  },
  updateApplication: (id: string, body: unknown) => api.patch<Application>(`/admin/applications/${id}`, body),
  deleteApplication: (id: string) => api.delete<void>(`/admin/applications/${id}`),
  listMiddlewareDefs: () => api.get<{ id: string; slug: string; kind: string; display_name: string; description: string; config: Record<string, unknown>; is_builtin: boolean; enabled: boolean }[]>('/admin/middleware-defs'),
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
  patchOrchestratorVoice: (appId: string, aoId: string, body: { stt_provider: string; stt_model: string; tts_provider: string; tts_voice: string; voice_enabled: boolean; tts_enabled: boolean }) =>
    api.patch<{ id: string }>(`/admin/applications/${appId}/orchestrators/${aoId}/voice`, body),
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
  listEntryPoints: (appId: string) => api.get<EntryPoint[]>(`/admin/applications/${appId}/entry-points`),
  patchEntryPoint: (appId: string, epId: string, payload: { slug?: string; entry_point_type?: string; enabled?: boolean }) => api.patch<{ id: string }>(`/admin/applications/${appId}/entry-points/${epId}`, payload),
  discoverEP: (appId: string, epId: string) => api.post<{ ok: boolean; card?: Record<string, unknown>; detail?: string }>(`/admin/applications/${appId}/entry-points/${epId}/discover`, {}),
  patchEntryPointSummarizer: (appId: string, epId: string, payload: { memory_enabled: boolean; summarize_every_n_calls: number; memory_raw_fallback_n: number; summarizer_provider: string | null; summarizer_model: string | null }) => api.patch<{ id: string }>(`/admin/applications/${appId}/entry-points/${epId}/summarizer`, payload),
  patchEntryPointLLM: (appId: string, epId: string, payload: { llm_provider: string | null; llm_model: string | null }) => api.patch<{ id: string }>(`/admin/applications/${appId}/entry-points/${epId}/llm`, payload),
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
