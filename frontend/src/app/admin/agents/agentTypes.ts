import type { AgentSkill, DiscoverResult, Agent } from '@/lib/api';

export interface AgentFolder {
  id: string;
  name: string;
  agentIds: string[];
  collapsed: boolean;
}

export interface FolderState {
  folders: AgentFolder[];
}

export const FOLDER_KEY = 'them:agents:folders';

export function loadFoldersLocal(): FolderState {
  if (typeof window === 'undefined') return { folders: [] };
  try {
    const raw = localStorage.getItem(FOLDER_KEY);
    if (!raw) return { folders: [] };
    const parsed = JSON.parse(raw);
    if (parsed && Array.isArray(parsed.folders)) return parsed as FolderState;
  } catch { /* ignore */ }
  return { folders: [] };
}

export function saveFoldersLocal(state: FolderState): void {
  try { localStorage.setItem(FOLDER_KEY, JSON.stringify(state)); } catch { /* ignore */ }
}

export function genId(): string {
  return Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
}

export const EMPTY_FORM = {
  slug: '',
  display_name: '',
  description: '',
  transport: 'a2a_async',
  endpoint_url: '',
  auth_token: '',
  max_concurrency: 3,
  timeout_seconds: 60,
  enabled: true,
  skills: [] as AgentSkill[],
  supports_streaming: false,
  supports_push: false,
  icon: '',
  agent_card: null as Record<string, unknown> | null,
  agent_card_url: '',
  tags: [] as string[],
};

export type FormState = typeof EMPTY_FORM;

export type CardDiff = {
  hasChanges: boolean;
  displayName: { old: string; new: string; changed: boolean };
  description: { old: string; new: string; changed: boolean };
  skills: { old: AgentSkill[]; new: AgentSkill[]; changed: boolean };
  streaming: { old: boolean; new: boolean; changed: boolean };
  push: { old: boolean; new: boolean; changed: boolean };
  version: { old: string; new: string; changed: boolean };
  provider: { old: string; new: string; changed: boolean };
};

export function buildDiff(agent: Agent, result: DiscoverResult): CardDiff {
  const oldCard = (agent.agent_card ?? {}) as Record<string, unknown>;
  const newCard = (result.agent_card ?? {}) as Record<string, unknown>;

  const oldVersion = String(oldCard.version ?? '');
  const newVersion = String(newCard.version ?? '');

  const oldProvider = typeof oldCard.provider === 'object' && oldCard.provider
    ? String((oldCard.provider as Record<string, unknown>).organization ?? '') : '';
  const newProvider = typeof newCard.provider === 'object' && newCard.provider
    ? String((newCard.provider as Record<string, unknown>).organization ?? '') : '';

  const oldSkillsJson = JSON.stringify((agent.skills ?? []).map(s => ({ id: s.id, name: s.name, description: s.description ?? '', tags: (s.tags ?? []).sort() })));
  const newSkillsJson = JSON.stringify((result.skills ?? []).map(s => ({ id: s.id, name: s.name, description: s.description ?? '', tags: (s.tags ?? []).sort() })));

  const fields = {
    displayName: { old: agent.display_name, new: result.display_name, changed: agent.display_name !== result.display_name },
    description: { old: agent.description, new: result.description, changed: agent.description !== result.description },
    skills: { old: agent.skills ?? [], new: result.skills, changed: oldSkillsJson !== newSkillsJson },
    streaming: { old: !!agent.supports_streaming, new: result.supports_streaming, changed: !!agent.supports_streaming !== result.supports_streaming },
    push: { old: !!agent.supports_push, new: result.supports_push, changed: !!agent.supports_push !== result.supports_push },
    version: { old: oldVersion, new: newVersion, changed: oldVersion !== newVersion },
    provider: { old: oldProvider, new: newProvider, changed: oldProvider !== newProvider },
  };

  const iconChanged = !!(result.icon && result.icon !== (agent.icon ?? ''));
  const categoryChanged = !!(result.category && result.category !== (agent.category ?? ''));
  const hasChanges = Object.values(fields).some(f => f.changed) || iconChanged || categoryChanged;
  return { hasChanges, ...fields };
}
