'use client';
import { type AgentParamsResponse, type AgentLLMNodeStatus } from '@/lib/api';
import { C, PROVIDER_LIST, RUNTIME_MODELS } from '../constants';
import { Section, AgentSection, AgentSubLabel, sharedField, badge, type SaveBtn } from './RuntimeShared';

type NodeLLMDraft = { provider: string; model: string };

export function CanvasAgentsSection({
  agentIds, agentLLMNodes, agentParamsList,
  nodeLLMDrafts, setNodeLLMDrafts, nodeLLMSaving, nodeLLMMsg,
  agentParamInputs, setAgentParamInputs, agentParamSaving, agentParamMsg,
  setProviders, saveBtn,
  onSaveNodeLLM, onSaveAgentParams,
}: {
  agentIds: string[];
  agentLLMNodes: AgentLLMNodeStatus[];
  agentParamsList: AgentParamsResponse[];
  nodeLLMDrafts: Record<string, NodeLLMDraft>;
  setNodeLLMDrafts: React.Dispatch<React.SetStateAction<Record<string, NodeLLMDraft>>>;
  nodeLLMSaving: string | null;
  nodeLLMMsg: Record<string, string>;
  agentParamInputs: Record<string, Record<string, string>>;
  setAgentParamInputs: React.Dispatch<React.SetStateAction<Record<string, Record<string, string>>>>;
  agentParamSaving: string | null;
  agentParamMsg: Record<string, string>;
  setProviders: string[];
  saveBtn: SaveBtn;
  onSaveNodeLLM: (agentId: string, nodeId: string) => void;
  onSaveAgentParams: (agentId: string) => void;
}) {
  if (agentIds.length === 0) return null;
  const f = sharedField;

  return (
    <Section title="Canvas Agents" icon="smart_toy" accent="#a78bfa" defaultOpen={false}
      subtitle={`${agentIds.length} agent${agentIds.length !== 1 ? 's' : ''} — LLM node overrides and parameters`}>
      {agentIds.map(agentId => {
        const nodes = agentLLMNodes.filter(n => n.agent_id === agentId);
        const agentParams = agentParamsList.find(a => a.agent_id === agentId);
        const slug = nodes[0]?.agent_slug ?? agentParams?.agent_slug ?? agentId;
        const paramsBusy = agentParamSaving === agentId;
        const paramsMsg = agentParamMsg[agentId] ?? '';
        const paramsErr = paramsMsg && paramsMsg !== 'Saved';
        const paramInputs = agentParamInputs[agentId] ?? {};
        const hasParamInput = Object.values(paramInputs).some(v => v.trim() !== '');

        const httpParams = agentParams?.required_params.filter(p =>
          p.used_by_nodes?.some(n => n.toLowerCase().includes('http')) || p.type === 'url'
        ) ?? [];
        const otherParams = agentParams?.required_params.filter(p => !httpParams.includes(p)) ?? [];

        return (
          <AgentSection key={agentId} slug={slug}>
            {nodes.length > 0 && (
              <div>
                <AgentSubLabel icon="psychology" label="LLM Nodes" />
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {nodes.map(node => {
                    const key = `${agentId}::${node.node_id}`;
                    const isBusy = nodeLLMSaving === key;
                    const msg = nodeLLMMsg[node.node_id] ?? '';
                    const isErr = msg && msg !== 'Saved';
                    const draft = nodeLLMDrafts[node.node_id] ?? { provider: '', model: '' };
                    const canSave = draft.provider && draft.model;
                    const isOverridden = !!(node.override_provider && node.override_model);
                    return (
                      <div key={node.node_id} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: `1px solid ${isOverridden ? 'rgba(251,146,60,0.2)' : 'rgba(255,255,255,0.07)'}` }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                          <span style={{ flex: 1, fontSize: 12, fontWeight: 600, color: C.text }}>{node.label || node.node_id}</span>
                          <span style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>default: {node.compiled_provider}/{node.compiled_model}</span>
                          {isOverridden && badge('#fb923c', 'rgba(251,146,60,0.1)', 'rgba(251,146,60,0.3)', 'overridden')}
                        </div>
                        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                          <select value={draft.provider}
                            onChange={e => { const p = e.target.value; setNodeLLMDrafts(prev => { const models = RUNTIME_MODELS[p] ?? []; const prevModel = prev[node.node_id]?.model ?? ''; return { ...prev, [node.node_id]: { provider: p, model: models.includes(prevModel) ? prevModel : (models[0] ?? '') } }; }); }}
                            style={{ ...f, width: 150, flexShrink: 0 }}>
                            <option value="">— provider —</option>
                            {(setProviders.length > 0 ? setProviders : PROVIDER_LIST).map(p => <option key={p} value={p}>{p}</option>)}
                          </select>
                          <select value={draft.model} disabled={!draft.provider}
                            onChange={e => setNodeLLMDrafts(prev => ({ ...prev, [node.node_id]: { ...draft, model: e.target.value } }))}
                            style={{ ...f, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                            <option value="">— model —</option>
                            {(RUNTIME_MODELS[draft.provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
                          </select>
                          {saveBtn(() => onSaveNodeLLM(agentId, node.node_id), isBusy, !canSave)}
                        </div>
                        {msg && <div style={{ marginTop: 6, fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{msg}</div>}
                      </div>
                    );
                  })}
                </div>
              </div>
            )}

            {httpParams.length > 0 && (
              <div>
                <AgentSubLabel icon="http" label="HTTP Nodes" />
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {httpParams.map(param => {
                    const isSecret = param.type === 'secret';
                    const currentVal = paramInputs[param.key] ?? '';
                    return (
                      <div key={param.key} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.07)' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                          <span style={{ fontSize: 12, fontWeight: 700, color: C.text, flex: 1 }}>{param.label}</span>
                          {param.required && !param.is_set && badge('#f87171', 'rgba(248,113,113,0.1)', 'rgba(248,113,113,0.3)', 'required')}
                          {param.is_set && badge(C.green, 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', `set ···${param.hint}`)}
                        </div>
                        {param.description && <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 6 }}>{param.description}</div>}
                        <input type={isSecret ? 'password' : 'text'}
                          placeholder={param.is_set ? 'Replace…' : (param.default_value ? `default: ${param.default_value}` : 'Enter value…')}
                          value={currentVal}
                          onChange={e => setAgentParamInputs(prev => ({ ...prev, [agentId]: { ...(prev[agentId] ?? {}), [param.key]: e.target.value } }))}
                          style={f} />
                      </div>
                    );
                  })}
                </div>
              </div>
            )}

            {otherParams.length > 0 && (
              <div>
                <AgentSubLabel icon="tune" label="Parameters" />
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {otherParams.map(param => {
                    const isSecret = param.type === 'secret';
                    const currentVal = paramInputs[param.key] ?? '';
                    return (
                      <div key={param.key} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.07)' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                          <span style={{ fontSize: 12, fontWeight: 700, color: C.text, flex: 1 }}>{param.label}</span>
                          {param.required && !param.is_set && badge('#f87171', 'rgba(248,113,113,0.1)', 'rgba(248,113,113,0.3)', 'required')}
                          {param.is_set && badge(C.green, 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', `set ···${param.hint}`)}
                        </div>
                        {param.description && <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 6 }}>{param.description}</div>}
                        <input type={isSecret ? 'password' : 'text'}
                          placeholder={param.is_set ? 'Replace…' : (param.default_value ? `default: ${param.default_value}` : 'Enter value…')}
                          value={currentVal}
                          onChange={e => setAgentParamInputs(prev => ({ ...prev, [agentId]: { ...(prev[agentId] ?? {}), [param.key]: e.target.value } }))}
                          style={f} />
                      </div>
                    );
                  })}
                </div>
              </div>
            )}

            {agentParams && agentParams.required_params.length > 0 && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                {saveBtn(() => onSaveAgentParams(agentId), paramsBusy, !hasParamInput, 'Save Parameters')}
                {paramsMsg && <span style={{ fontSize: 12, color: paramsErr ? C.error : C.green, fontWeight: 600 }}>{paramsMsg}</span>}
              </div>
            )}
          </AgentSection>
        );
      })}
    </Section>
  );
}
