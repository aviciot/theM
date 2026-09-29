'use client';
import { useEffect, useState } from 'react';
import type { Node } from '@xyflow/react';
import { C } from '../../../constants';
import { fieldStyle, selectStyle, sectionHdrStyle } from './panelShared';
import { getCachedNodeTypesByFamily } from '@/lib/nodeRegistry';
import type { ConfigFieldDecl } from '@/lib/nodeRegistry';
import { themApi, type Agent, type MiddlewareWiring } from '@/lib/api';

// ── AgentGuardsSection — Guards panel for agent + llm nodes ─────────────────
//
// Started as Phase 3 of docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md (File Guard
// on agent nodes only); generalized in docs/APPFLOW_TEXT_GUARDS_PLAN.md
// Phase 4 to render every applicable guard def for a node, and to work for
// llm nodes too (an llm node has no agents row — middleware_wirings.agent_id
// is now nullable, db/114 — node_id is the only real identity a wiring
// needs).
//
// Which guards apply to which node kind:
// - agent: file-guard, pii_redact, prompt_inject (an agent's A2A response
//   can carry a file or text worth guarding).
// - llm: pii_redact, prompt_inject only — NOT file-guard. An llm node's
//   response can never carry a file today (confirmed:
//   docs/APPFLOW_LLM_FILE_OUTPUT_PLAN.md is a separate, not-yet-started
//   plan) — showing a File Guard toggle that can never trigger would
//   misrepresent what it does, same principle as PII/Prompt-Injection guard
//   being excluded before their real detection logic existed.
//
// PII/Prompt-Injection guard is deliberately NOT rendered for either node
// kind until this phase — confirmed pii_redact/prompt_inject now have real
// detection logic (docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 1-2/3), so this is
// no longer the "would silently lie about what it does" case that excluded
// the old combined guard_default def.

interface Props {
  appId: string;
  selectedNode: Node;
  agents: Agent[];
  showToast: (msg: string, ok: boolean) => void;
}

const AGENT_GUARD_SLUGS = ['file-guard', 'pii_redact', 'prompt_inject'] as const;
const LLM_GUARD_SLUGS = ['pii_redact', 'prompt_inject'] as const;

export const GUARD_LABELS: Record<string, string> = {
  'file-guard': 'File Guard',
  pii_redact: 'PII Guard',
  prompt_inject: 'Prompt-Injection Guard',
};

export function AgentGuardsSection({ appId, selectedNode, agents, showToast }: Props) {
  const nodeData = selectedNode.data as unknown as { definition_ref?: { name?: string }; node_type?: string };
  const isLLMNode = nodeData.node_type === 'llm';
  const agentSlug = nodeData.definition_ref?.name;
  const agent = agents.find(a => a.slug === agentSlug);

  // An llm node has no agent to resolve at all — that's expected, not a
  // loading/error state the way it is for an agent node whose definition
  // hasn't resolved yet.
  if (!isLLMNode && !agent) {
    return (
      <div>
        <div style={{ ...sectionHdrStyle, color: C.purple, marginBottom: 6 }}>Guards</div>
        <div style={{ fontSize: 11, color: C.textMuted }}>
          Agent definition not resolved yet — save and reload to configure guards.
        </div>
      </div>
    );
  }

  const guardSlugs = isLLMNode ? LLM_GUARD_SLUGS : AGENT_GUARD_SLUGS;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={sectionHdrStyle}>Guards</div>
      {guardSlugs.map(slug => (
        <GuardWiringForm
          key={slug}
          appId={appId}
          selectedNode={selectedNode}
          agent={agent}
          defSlug={slug}
          showToast={showToast}
        />
      ))}
    </div>
  );
}

interface GuardWiringFormProps {
  appId: string;
  selectedNode: Node;
  agent: Agent | undefined;
  defSlug: string;
  showToast: (msg: string, ok: boolean) => void;
}

function GuardWiringForm({ appId, selectedNode, agent, defSlug, showToast }: GuardWiringFormProps) {
  const [wiring, setWiring] = useState<MiddlewareWiring | null | undefined>(undefined); // undefined = loading
  const [configDraft, setConfigDraft] = useState<Record<string, unknown>>({});
  const [saving, setSaving] = useState(false);

  const guardDef = getCachedNodeTypesByFamily('middleware').find(d => d.type === defSlug);
  const configFields: ConfigFieldDecl[] = guardDef?.config_fields ?? [];

  useEffect(() => {
    let cancelled = false;
    setWiring(undefined);
    themApi.listMiddlewareWirings(appId).then(list => {
      if (cancelled) return;
      const match = list.find(w => w.node_id === selectedNode.id && w.def_slug === defSlug);
      setWiring(match ?? null);
      setConfigDraft((match?.config_override as Record<string, unknown>) ?? {});
    }).catch(() => { if (!cancelled) setWiring(null); });
    return () => { cancelled = true; };
  }, [appId, selectedNode.id, defSlug]);

  if (wiring === undefined) {
    return (
      <div>
        <div style={{ fontSize: 11, fontWeight: 600, color: C.textMuted, marginBottom: 4 }}>
          {GUARD_LABELS[defSlug] ?? defSlug}
        </div>
        <div style={{ fontSize: 11, color: C.textMuted }}>Loading…</div>
      </div>
    );
  }

  const enabled = wiring?.enabled ?? false;

  // This guard's own config_fields also declare an "enabled" key (the
  // wiring-independent "is this guard active" flag inside config_override).
  // That's redundant with the wiring-level `enabled` column this form
  // already renders as its own top-level checkbox — rather than show two
  // "Enabled" toggles, config_override.enabled is kept silently in sync with
  // the wiring's own enabled state and hidden from the rendered form (same
  // fix as File Guard's original duplicate-checkbox bug, applied generically
  // here so it can't recur for any future guard).
  function withEnabledSynced(nextEnabled: boolean) {
    return { ...configDraft, enabled: nextEnabled };
  }

  async function handleToggleEnabled(nextEnabled: boolean) {
    setSaving(true);
    try {
      const syncedConfig = withEnabledSynced(nextEnabled);
      if (wiring) {
        const updated = await themApi.updateMiddlewareWiring(appId, wiring.id, {
          enabled: nextEnabled,
          config_override: syncedConfig,
        });
        setWiring(updated);
      } else {
        const created = await themApi.createMiddlewareWiring(appId, {
          agent_id: agent?.id,
          def_slug: defSlug,
          node_id: selectedNode.id,
          enabled: nextEnabled,
          config_override: syncedConfig,
        });
        setWiring(created);
      }
      setConfigDraft(syncedConfig);
      showToast(`${GUARD_LABELS[defSlug] ?? defSlug} ${nextEnabled ? 'enabled' : 'disabled'}`, true);
    } catch (e: unknown) {
      showToast(e instanceof Error ? e.message : 'Failed to save guard setting', false);
    } finally {
      setSaving(false);
    }
  }

  async function handleSaveConfig() {
    setSaving(true);
    try {
      const syncedConfig = withEnabledSynced(enabled);
      if (wiring) {
        const updated = await themApi.updateMiddlewareWiring(appId, wiring.id, { config_override: syncedConfig });
        setWiring(updated);
      } else {
        const created = await themApi.createMiddlewareWiring(appId, {
          agent_id: agent?.id,
          def_slug: defSlug,
          node_id: selectedNode.id,
          enabled: true,
          config_override: syncedConfig,
        });
        setWiring(created);
      }
      setConfigDraft(syncedConfig);
      showToast('Guard config saved', true);
    } catch (e: unknown) {
      showToast(e instanceof Error ? e.message : 'Failed to save guard config', false);
    } finally {
      setSaving(false);
    }
  }

  function updateField(key: string, value: unknown) {
    setConfigDraft(prev => ({ ...prev, [key]: value }));
  }

  return (
    <div style={{ borderTop: '1px solid rgba(255,255,255,0.06)', paddingTop: 10 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: C.purple, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
          {GUARD_LABELS[defSlug] ?? defSlug}
        </div>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: C.textMuted, cursor: 'pointer' }}>
          <input
            type="checkbox"
            checked={enabled}
            disabled={saving}
            onChange={e => void handleToggleEnabled(e.target.checked)}
          />
          Enabled
        </label>
      </div>

      {!enabled && (
        <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 8 }}>
          This guard is off for this node. Turn on to configure it.
        </div>
      )}

      {enabled && configFields.length === 0 && (
        <div style={{ fontSize: 11, color: '#f87171' }}>
          {defSlug}&apos;s config fields are not available — reload the canvas to refetch node types.
        </div>
      )}

      {enabled && configFields.filter(field => field.key !== 'enabled').map(field => (
        <div key={field.key} style={{ marginBottom: 8 }}>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>
            {field.key}{field.required && ' *'}{field.placeholder && ' (placeholder)'}
          </label>
          {field.type === 'array' && field.options ? (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10 }}>
              {field.options.map(opt => {
                const selected = Array.isArray(configDraft[field.key]) ? (configDraft[field.key] as string[]) : [];
                const checked = selected.includes(opt);
                return (
                  <label key={opt} style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 12, color: C.text, cursor: 'pointer' }}>
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={e => updateField(
                        field.key,
                        e.target.checked ? [...selected, opt] : selected.filter(v => v !== opt),
                      )}
                    />
                    {opt}
                  </label>
                );
              })}
            </div>
          ) : field.options ? (
            <select
              style={selectStyle}
              value={typeof configDraft[field.key] === 'string' ? (configDraft[field.key] as string) : ''}
              onChange={e => updateField(field.key, e.target.value)}
            >
              <option value="" disabled>Select…</option>
              {field.options.map(opt => (
                <option key={opt} value={opt}>{opt}</option>
              ))}
            </select>
          ) : field.type === 'bool' ? (
            <input
              type="checkbox"
              checked={Boolean(configDraft[field.key])}
              onChange={e => updateField(field.key, e.target.checked)}
            />
          ) : field.type === 'array' ? (
            <input
              style={fieldStyle}
              placeholder={field.example}
              value={Array.isArray(configDraft[field.key]) ? (configDraft[field.key] as string[]).join(', ') : ''}
              onChange={e => updateField(field.key, e.target.value.split(',').map(s => s.trim()).filter(Boolean))}
            />
          ) : field.type === 'int' ? (
            <input
              type="number"
              style={fieldStyle}
              placeholder={field.example}
              value={typeof configDraft[field.key] === 'number' ? (configDraft[field.key] as number) : ''}
              onChange={e => updateField(field.key, e.target.value === '' ? undefined : Number(e.target.value))}
            />
          ) : (
            <input
              style={fieldStyle}
              placeholder={field.example}
              value={typeof configDraft[field.key] === 'string' ? (configDraft[field.key] as string) : ''}
              onChange={e => updateField(field.key, e.target.value)}
            />
          )}
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 2 }}>{field.description}</div>
        </div>
      ))}

      {enabled && (
        <button
          onClick={() => void handleSaveConfig()}
          disabled={saving}
          style={{ padding: '6px 14px', borderRadius: 6, border: 'none', background: C.purple, color: '#fff', fontSize: 12, fontWeight: 600, cursor: saving ? 'default' : 'pointer', opacity: saving ? 0.6 : 1 }}
        >
          {saving ? 'Saving…' : 'Save Guard Config'}
        </button>
      )}
    </div>
  );
}
