'use client';
import { useState, useEffect } from 'react';
import type { Node, Edge } from '@xyflow/react';
import type { OrchNodeData, EpNodeData } from '../../../types';
import { C } from '../../../constants';
import { themApi } from '@/lib/api';
import { SCENARIOS, resolveScenario, isDead } from '../../epScenario';
import { fieldStyle, selectStyle, chipStyle, sectionHdrStyle, isSectionOpen, SectionHeader } from './panelShared';

// ── EntryPointNodePanel ──────────────────────────────────────────────────────

interface Props {
  selectedNode: Node;
  nodes: Node[];
  edges: Edge[];
  openSections: Record<string, boolean>;
  setOpenSections: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
  setLogoResult: (v: 'none' | 'valid' | 'invalid' | 'warn') => void;
  setEpConfig: (instanceId: string, patch: Record<string, unknown>, remove?: string[]) => void;
}

export function EntryPointNodePanel({
  selectedNode, nodes, edges,
  openSections, setOpenSections,
  setNodes, setIsDirty, setLogoResult, setEpConfig,
}: Props) {
  const [hasRuntimeIdp, setHasRuntimeIdp] = useState<boolean | null | 'error'>(null);
  useEffect(() => {
    themApi.getRuntimeIDP().then(cfg => setHasRuntimeIdp(cfg.configured)).catch(() => setHasRuntimeIdp('error'));
  }, []);

  const liveEpNode = nodes.find(n => n.id === selectedNode.id);
  const d = (liveEpNode?.data ?? selectedNode.data) as unknown as EpNodeData;
  const cfg = d.config ?? {};
  const rootOrchNode = edges
    .filter(e => e.source === selectedNode.id)
    .map(e => nodes.find(n => n.id === e.target))
    .find(n => n?.type === 'orchestrator');
  const slugValid = /^[a-z0-9_-]{1,64}$/.test(d.slug);
  const rootLabel = rootOrchNode
    ? ((rootOrchNode.data as unknown as OrchNodeData).display_name ?? rootOrchNode.id)
    : 'Not connected';

  const sectionHdrStyleLocal: React.CSSProperties = sectionHdrStyle;

  return (
    <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
      <div style={{ fontSize: 12, fontWeight: 700, color: C.cyan, textTransform: 'uppercase', letterSpacing: '0.06em' }}>Entry Point</div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Instance ID</label>
        <div style={chipStyle}>{d.instance_id}</div>
      </div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Slug</label>
        <input
          style={{ ...fieldStyle, borderColor: d.slug && !slugValid ? C.amber : undefined }}
          value={d.slug}
          onChange={e => { setNodes(ns => ns.map(n => n.id === selectedNode.id ? { ...n, data: { ...n.data, slug: e.target.value } } : n)); setIsDirty(true); setLogoResult('none'); }}
          placeholder="e.g. my-endpoint"
        />
        {d.slug && !slugValid && <div style={{ fontSize: 11, color: C.amber, marginTop: 3 }}>Only a-z, 0-9, _, - (1-64 chars)</div>}
      </div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Protocol</label>
        <span style={{ fontSize: 12, padding: '3px 10px', borderRadius: 20, background: C.cyanBg, color: C.cyan, border: `1px solid ${C.cyanBorder}` }}>{d.protocol}</span>
      </div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Root Orchestrator</label>
        <div style={{ fontSize: 12, color: rootOrchNode ? C.purple : C.textMuted, fontStyle: rootOrchNode ? 'normal' : 'italic' }}>{rootLabel}</div>
      </div>

      {/* Section B — LLM */}
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
        <div style={{ ...sectionHdrStyleLocal, marginBottom: 8 }}>LLM</div>
        {rootOrchNode ? (() => {
          const orchConfig = (rootOrchNode.data as unknown as OrchNodeData).config;
          const epLlm = ((orchConfig.ep_llm ?? {}) as Record<string, { provider?: string; model?: string }>)[d.instance_id];
          const prov = epLlm?.provider;
          const mdl = epLlm?.model;
          return prov && mdl
            ? <div style={{ fontSize: 12, color: C.text }}><span style={{ color: C.purple }}>{prov}</span> / <span style={{ fontFamily: 'JetBrains Mono, monospace', color: C.cyan }}>{mdl}</span></div>
            : <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>Configure on the orchestrator panel</div>;
        })()
          : <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>Connect an orchestrator to configure LLM</div>
        }
      </div>

      {/* Section C — Access */}
      {d.protocol !== 'voice' && d.protocol !== 'webrtc' && (() => {
        const accessMode = (cfg.access_mode as string) || 'token';
        const allowedPrincipals = (cfg.allowed_principals as string) || 'internal';
        const scenarioKey = resolveScenario(accessMode, allowedPrincipals);
        const isDeadCombo = isDead(accessMode, allowedPrincipals);
        const isLegacy = scenarioKey !== null && scenarioKey !== undefined &&
          !SCENARIOS.find(s => s.accessMode === accessMode && s.allowedPrincipals === allowedPrincipals);
        const selectedScenario = SCENARIOS.find(s => s.key === scenarioKey);

        function handleScenarioChange(key: string) {
          const s = SCENARIOS.find(sc => sc.key === key);
          if (!s) return;
          setEpConfig(selectedNode.id, { access_mode: s.accessMode, allowed_principals: s.allowedPrincipals });
        }

        return (
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
            <div style={{ ...sectionHdrStyleLocal, marginBottom: 8 }}>Access</div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Who can connect?</label>
            <select
              style={{ ...selectStyle, borderColor: isDeadCombo ? '#ef4444' : undefined }}
              value={isDeadCombo ? '__invalid__' : (scenarioKey ?? '__unknown__')}
              onChange={e => handleScenarioChange(e.target.value)}
            >
              {isDeadCombo && (
                <option value="__invalid__" disabled>⚠ Unsupported combination — select a valid option</option>
              )}
              {!isDeadCombo && scenarioKey === undefined && (
                <option value="__unknown__" disabled>⚠ Unknown combination — select a valid option</option>
              )}
              {SCENARIOS.map(s => (
                <option
                  key={s.key}
                  value={s.key}
                  disabled={s.requiresRuntimeIdp && hasRuntimeIdp !== true}
                >
                  {s.label}{s.requiresRuntimeIdp && hasRuntimeIdp === false ? ' (Runtime Identity not configured)' : s.requiresRuntimeIdp && hasRuntimeIdp === 'error' ? ' (could not load status)' : ''}
                </option>
              ))}
            </select>
            {isDeadCombo && (
              <div style={{ fontSize: 10, color: '#ef4444', marginTop: 4, lineHeight: 1.5 }}>
                This combination rejects all callers. Select a valid option above.
              </div>
            )}
            {isLegacy && selectedScenario && (
              <div style={{ fontSize: 10, color: C.amber, marginTop: 4, lineHeight: 1.5 }}>
                Stored as {accessMode} + {allowedPrincipals} — displayed as nearest equivalent.
              </div>
            )}
            {selectedScenario && !isDeadCombo && (
              <div style={{ marginTop: 8, padding: '8px 10px', borderRadius: 7, background: 'rgba(124,58,237,0.08)', border: '1px solid rgba(124,58,237,0.2)', display: 'flex', flexDirection: 'column', gap: 5 }}>
                <div style={{ fontSize: 11, color: C.text, lineHeight: 1.5 }}>
                  {selectedScenario.description}
                </div>
                <div style={{ fontSize: 10, color: C.textMuted, lineHeight: 1.5 }}>
                  <span style={{ fontWeight: 600, color: C.text }}>Use when: </span>{selectedScenario.whenToUse}
                </div>
                <div style={{ fontSize: 10, color: C.textMuted, lineHeight: 1.5 }}>
                  <span style={{ fontWeight: 600, color: C.text }}>Requires: </span>{selectedScenario.prerequisite}
                </div>
                {selectedScenario.requiresRuntimeIdp && hasRuntimeIdp === false && (
                  <div style={{ fontSize: 10, color: '#ef4444', marginTop: 2 }}>
                    ⚠ Runtime Identity not configured — go to Tenant Settings → Runtime Identity.
                  </div>
                )}
                {selectedScenario.requiresRuntimeIdp && hasRuntimeIdp === 'error' && (
                  <div style={{ fontSize: 10, color: '#ef4444', marginTop: 2 }}>
                    ⚠ Could not load Runtime Identity status — reload and try again.
                  </div>
                )}
                {selectedScenario.requiresRuntimeIdp && hasRuntimeIdp === true && (
                  <div style={{ fontSize: 10, color: C.green, marginTop: 2 }}>
                    Runtime Identity is configured.
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })()}

      {/* Section D — Capacity */}
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
        <SectionHeader id="ep-capacity" label="Capacity" defaultOpen={false} openSections={openSections} setOpenSections={setOpenSections} />
        {isSectionOpen(openSections, 'ep-capacity', false) && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
            <div>
              <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Conversation Token Limit</label>
              <input
                type="number"
                style={fieldStyle}
                value={(cfg.conversation_token_limit as number) ?? ''}
                placeholder="unset"
                onChange={e => {
                  if (e.target.value === '') setEpConfig(selectedNode.id, {}, ['conversation_token_limit']);
                  else setEpConfig(selectedNode.id, { conversation_token_limit: Number(e.target.value) });
                }}
              />
            </div>
            <div>
              <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Queue Timeout (s)</label>
              <input
                type="number"
                style={fieldStyle}
                value={(cfg.queue_timeout_seconds as number) ?? ''}
                placeholder="unset"
                onChange={e => {
                  if (e.target.value === '') setEpConfig(selectedNode.id, {}, ['queue_timeout_seconds']);
                  else setEpConfig(selectedNode.id, { queue_timeout_seconds: Number(e.target.value) });
                }}
              />
            </div>
            <div>
              <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Queue Message</label>
              <input
                style={fieldStyle}
                value={(cfg.queue_message as string) ?? ''}
                placeholder="All agents are busy, please wait…"
                onChange={e => {
                  if (e.target.value === '') setEpConfig(selectedNode.id, {}, ['queue_message']);
                  else setEpConfig(selectedNode.id, { queue_message: e.target.value });
                }}
              />
            </div>
          </div>
        )}
      </div>

      {/* Section E — Protocol-specific */}
      {d.protocol === 'voice' && (
        <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
          <div style={{ ...sectionHdrStyleLocal, marginBottom: 8, color: C.amber }}>Voice</div>
          <div style={{ fontSize: 12, color: C.textMuted, padding: '8px 10px', background: C.amberBg, border: `1px solid ${C.amberBorder}`, borderRadius: 6 }}>
            STT/TTS is configured on the root orchestrator&rsquo;s Voice section.
          </div>
        </div>
      )}
      {d.protocol === 'a2a' && (
        <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
          <div style={{ ...sectionHdrStyleLocal, marginBottom: 8 }}>A2A</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div>
              <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 2 }}>Skill ID</label>
              <span style={{ fontSize: 12, fontFamily: 'JetBrains Mono, monospace', color: C.cyan }}>{d.slug}</span>
            </div>
            <div style={{ fontSize: 12, color: C.textMuted }}>
              budget_tokens from the root orchestrator applies to A2A calls.
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
