'use client';
import { type AppFlowLLMNodeStatus } from '@/lib/api';
import { C, PROVIDER_LIST, RUNTIME_MODELS } from '../constants';
import { Section, sharedField, badge, type SaveBtn } from './RuntimeShared';

type NodeLLMDraft = { provider: string; model: string };

// Node Registry Phase 1 — provider/model for app-canvas inline LLM nodes live
// here, not on the canvas. Mirrors CanvasAgentsSection's LLM-node block but for
// nodes that sit directly on the app canvas rather than inside an agent.
export function RuntimeAppFlowLLMSection({
  nodes, drafts, setDrafts, saving, msg, setProviders, saveBtn, onSave,
}: {
  nodes: AppFlowLLMNodeStatus[];
  drafts: Record<string, NodeLLMDraft>;
  setDrafts: React.Dispatch<React.SetStateAction<Record<string, NodeLLMDraft>>>;
  saving: string | null;
  msg: Record<string, string>;
  setProviders: string[];
  saveBtn: SaveBtn;
  onSave: (nodeId: string) => void;
}) {
  if (nodes.length === 0) return null;
  const f = sharedField;

  return (
    <Section title="App Canvas — Inline LLM Nodes" icon="bolt" accent="#d0bcff" defaultOpen={false}
      subtitle={`${nodes.length} node${nodes.length !== 1 ? 's' : ''} — provider/model, no re-publish needed`}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {nodes.map(node => {
          const isBusy = saving === node.node_id;
          const nodeMsg = msg[node.node_id] ?? '';
          const isErr = nodeMsg && nodeMsg !== 'Saved';
          const draft = drafts[node.node_id] ?? { provider: '', model: '' };
          const canSave = draft.provider && draft.model;
          const isOverridden = !!(node.override_provider && node.override_model);
          return (
            <div key={node.node_id} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: `1px solid ${isOverridden ? 'rgba(251,146,60,0.2)' : 'rgba(255,255,255,0.07)'}` }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <span style={{ flex: 1, fontSize: 12, fontWeight: 600, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{node.node_id}</span>
                <span style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>
                  default: {node.compiled_provider || 'inherit'}/{node.compiled_model || 'inherit'}
                </span>
                {isOverridden && badge('#fb923c', 'rgba(251,146,60,0.1)', 'rgba(251,146,60,0.3)', 'overridden')}
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <select value={draft.provider}
                  onChange={e => { const p = e.target.value; setDrafts(prev => { const models = RUNTIME_MODELS[p] ?? []; const prevModel = prev[node.node_id]?.model ?? ''; return { ...prev, [node.node_id]: { provider: p, model: models.includes(prevModel) ? prevModel : (models[0] ?? '') } }; }); }}
                  style={{ ...f, width: 150, flexShrink: 0 }}>
                  <option value="">— provider —</option>
                  {(setProviders.length > 0 ? setProviders : PROVIDER_LIST).map(p => <option key={p} value={p}>{p}</option>)}
                </select>
                <select value={draft.model} disabled={!draft.provider}
                  onChange={e => setDrafts(prev => ({ ...prev, [node.node_id]: { ...draft, model: e.target.value } }))}
                  style={{ ...f, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                  <option value="">— model —</option>
                  {(RUNTIME_MODELS[draft.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                </select>
                {saveBtn(() => onSave(node.node_id), isBusy, !canSave)}
              </div>
              {nodeMsg && <div style={{ marginTop: 6, fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{nodeMsg}</div>}
            </div>
          );
        })}
      </div>
    </Section>
  );
}
