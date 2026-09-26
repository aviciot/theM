'use client';
import { useEffect, useState } from 'react';
import type { Node } from '@xyflow/react';
import { C } from '../../../constants';
import { fieldStyle, sectionHdrStyle } from './panelShared';
import { getCachedNodeTypesByFamily } from '@/lib/nodeRegistry';
import type { ConfigFieldDecl } from '@/lib/nodeRegistry';
import { themApi, type Agent, type MiddlewareWiring } from '@/lib/api';

// ── AgentGuardsSection — Phase 3 of docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md ──
//
// Lets a canvas agent node carry its own File Guard config, scoped by this
// exact node instance (node_id) — not just the agent globally — per Phase
// 2's node_id scoping fix. Renders file-guard's config_fields (fetched live
// via GET /admin/node-types, cached by CanvasBuilderView on load) instead of
// a hardcoded field list, matching this session's confirmed
// self-documenting-registry pattern.
//
// PII/Prompt-Injection guard is deliberately NOT rendered here — confirmed
// this session that no real detection logic exists for it anywhere in the
// codebase (empty config structs only); showing a working-looking form for
// it would silently lie about what it does.

interface Props {
  appId: string;
  selectedNode: Node;
  agents: Agent[];
  showToast: (msg: string, ok: boolean) => void;
}

const FILE_GUARD_SLUG = 'file-guard';

export function AgentGuardsSection({ appId, selectedNode, agents, showToast }: Props) {
  const nodeData = selectedNode.data as unknown as { definition_ref?: { name?: string } };
  const agentSlug = nodeData.definition_ref?.name;
  const agent = agents.find(a => a.slug === agentSlug);

  const [wiring, setWiring] = useState<MiddlewareWiring | null | undefined>(undefined); // undefined = loading
  const [configDraft, setConfigDraft] = useState<Record<string, unknown>>({});
  const [saving, setSaving] = useState(false);

  const fileGuardDef = getCachedNodeTypesByFamily('middleware').find(d => d.type === FILE_GUARD_SLUG);
  const configFields: ConfigFieldDecl[] = fileGuardDef?.config_fields ?? [];

  useEffect(() => {
    let cancelled = false;
    setWiring(undefined);
    if (!agent) return;
    themApi.listMiddlewareWirings(appId).then(list => {
      if (cancelled) return;
      const match = list.find(w => w.node_id === selectedNode.id && w.def_slug === FILE_GUARD_SLUG);
      setWiring(match ?? null);
      setConfigDraft((match?.config_override as Record<string, unknown>) ?? {});
    }).catch(() => { if (!cancelled) setWiring(null); });
    return () => { cancelled = true; };
  }, [appId, selectedNode.id, agent]);

  if (!agent) {
    return (
      <div>
        <div style={{ ...sectionHdrStyle, color: C.purple, marginBottom: 6 }}>Guards</div>
        <div style={{ fontSize: 11, color: C.textMuted }}>
          Agent definition not resolved yet — save and reload to configure guards.
        </div>
      </div>
    );
  }

  async function handleToggleEnabled(nextEnabled: boolean) {
    if (!agent) return;
    setSaving(true);
    try {
      if (wiring) {
        const updated = await themApi.updateMiddlewareWiring(appId, wiring.id, { enabled: nextEnabled });
        setWiring(updated);
      } else {
        const created = await themApi.createMiddlewareWiring(appId, {
          agent_id: agent.id,
          def_slug: FILE_GUARD_SLUG,
          node_id: selectedNode.id,
          enabled: nextEnabled,
          config_override: configDraft,
        });
        setWiring(created);
      }
      showToast(nextEnabled ? 'File Guard enabled' : 'File Guard disabled', true);
    } catch (e: unknown) {
      showToast(e instanceof Error ? e.message : 'Failed to save guard setting', false);
    } finally {
      setSaving(false);
    }
  }

  async function handleSaveConfig() {
    if (!agent) return;
    setSaving(true);
    try {
      if (wiring) {
        const updated = await themApi.updateMiddlewareWiring(appId, wiring.id, { config_override: configDraft });
        setWiring(updated);
      } else {
        const created = await themApi.createMiddlewareWiring(appId, {
          agent_id: agent.id,
          def_slug: FILE_GUARD_SLUG,
          node_id: selectedNode.id,
          enabled: true,
          config_override: configDraft,
        });
        setWiring(created);
      }
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

  if (wiring === undefined) {
    return (
      <div>
        <div style={{ ...sectionHdrStyle, color: C.purple, marginBottom: 6 }}>Guards</div>
        <div style={{ fontSize: 11, color: C.textMuted }}>Loading…</div>
      </div>
    );
  }

  const enabled = wiring?.enabled ?? false;

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
        <div style={{ ...sectionHdrStyle, color: C.purple }}>Guards — File Guard</div>
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
          Files this agent returns are not scanned. Turn on to quarantine and scan them before delivery.
        </div>
      )}

      {enabled && configFields.length === 0 && (
        <div style={{ fontSize: 11, color: '#f87171' }}>
          file-guard's config fields are not available — reload the canvas to refetch node types.
        </div>
      )}

      {enabled && configFields.map(field => (
        <div key={field.key} style={{ marginBottom: 8 }}>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>
            {field.key}{field.required && ' *'}
          </label>
          {field.type === 'bool' ? (
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
