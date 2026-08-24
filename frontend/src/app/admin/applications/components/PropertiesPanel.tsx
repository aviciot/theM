'use client';
import { useState } from 'react';
import { themApi } from '@/lib/api';
import type { Node, Edge } from '@xyflow/react';
import type {
  Application,
  OrchestratorData,
  EntryPointData,
  AgentData,
  MiddlewareData,
  ChainStatus,
  EntryPointType,
} from '../types';
import { C, glass, MODELS_BY_PROVIDER } from '../constants';
import { agentIconForLibrary } from './CanvasHelpers';

export function PropertiesPanel({
  selectedNode,
  onUpdateNode,
  slugLocked,
  onSlugManualEdit,
  appName,
  onAppNameChange,
  convTokenLimit,
  onConvTokenLimitChange,
  chain,
  app,
  epCount,
  nodes,
  edges,
}: {
  selectedNode: Node | null;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
  slugLocked: boolean;
  onSlugManualEdit: () => void;
  appName: string;
  onAppNameChange: (name: string) => void;
  convTokenLimit: string;
  onConvTokenLimitChange: (val: string) => void;
  chain: ChainStatus;
  app: Application | null;
  epCount: number;
  nodes: Node[];
  edges: Edge[];
}) {
  const [propTab, setPropTab] = useState<'properties' | 'configuration'>('properties');
  const [orchTestState, setOrchTestState] = useState<{ loading?: boolean; ok?: boolean; latency?: number; error?: string }>({});
  const [sttTestState,  setSttTestState]  = useState<{ loading?: boolean; ok?: boolean; latency?: number; error?: string }>({});
  const [ttsTestState,  setTtsTestState]  = useState<{ loading?: boolean; ok?: boolean; latency?: number; error?: string }>({});

  async function testOrchLlm(d: OrchestratorData) {
    if (!d.llmProvider || !d.llmModel || !d.appOrchestratorId || !app) return;
    setOrchTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchLlm(app.id, d.appOrchestratorId, { provider: d.llmProvider, model: d.llmModel, api_key: d.llmApiKey || undefined });
      setOrchTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setOrchTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function testStt(d: OrchestratorData) {
    if (!d.transcriptionProvider || !d.transcriptionModel || !d.appOrchestratorId || !app) return;
    setSttTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchVoice(app.id, d.appOrchestratorId, { provider: d.transcriptionProvider, model: d.transcriptionModel });
      setSttTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setSttTestState({ loading: false, ok: false, error: e.message });
    }
  }

  async function testTts(d: OrchestratorData) {
    if (!d.ttsProvider || !d.ttsVoice || !d.appOrchestratorId || !app) return;
    setTtsTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchTts(app.id, d.appOrchestratorId, { provider: d.ttsProvider, voice: d.ttsVoice });
      setTtsTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: any) {
      setTtsTestState({ loading: false, ok: false, error: e.message });
    }
  }

  function TabBtn({ id, label }: { id: 'properties' | 'configuration'; label: string }) {
    const active = propTab === id;
    return (
      <button onClick={() => setPropTab(id)} style={{
        padding: '6px 14px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 600,
        background: active ? 'rgba(0,240,255,0.15)' : 'transparent',
        color: active ? C.cyan : C.textMuted,
        transition: 'all 0.15s',
      }}>{label}</button>
    );
  }

  const labelStyle: React.CSSProperties = { fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' };
  const inputStyle: React.CSSProperties = {
    width: '100%', padding: '7px 10px', borderRadius: 6,
    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
    color: 'var(--tm-card-text)', fontSize: 13, boxSizing: 'border-box', outline: 'none',
  };
  const readOnlyStyle: React.CSSProperties = { ...inputStyle, color: 'var(--tm-card-text-hint)', background: 'rgba(10,18,32,0.6)', cursor: 'default' };
  const fieldWrap: React.CSSProperties = { marginBottom: 14 };

  return (
    <div onKeyDown={e => e.stopPropagation()} style={{
      width: 320, flexShrink: 0, height: '100%', overflowY: 'auto',
      ...glass, borderLeft: `1px solid ${C.glassBorder}`, padding: '16px 14px',
      display: 'flex', flexDirection: 'column',
    }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', paddingBottom: 8, borderBottom: `1px solid ${C.outlineVariant}`, marginBottom: 16 }}>
        {selectedNode ? 'Node Properties' : 'Application'}
      </div>

      {!selectedNode ? (
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {/* App header */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: 'rgba(0,209,255,0.06)', border: '1px solid rgba(0,209,255,0.18)' }}>
            <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.cyan }}>deployed_code</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {appName || 'Untitled Application'}
              </div>
              <div style={{ fontSize: 10, color: C.textMuted }}>
                {app ? `ID: ${app.id.slice(0, 8)}…` : 'Not yet saved'}
              </div>
            </div>
          </div>

          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' }}>Application Name</label>
            <input
              style={{
                width: '100%', padding: '7px 10px', borderRadius: 6,
                border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
                color: 'var(--tm-card-text)', fontSize: 13, boxSizing: 'border-box', outline: 'none',
              }}
              value={appName}
              onChange={e => onAppNameChange(e.target.value)}
              placeholder="My Application"
            />
          </div>

          {epCount <= 1 ? (
            <>
              <div style={{ marginBottom: 14 }}>
                <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' }}>
                  Conversation Token Limit
                  <span style={{ marginLeft: 6, fontSize: 10, color: '#64748b' }}>per session · blank = unlimited</span>
                </label>
                <input
                  type="number" min={1}
                  style={{
                    width: '100%', padding: '7px 10px', borderRadius: 6,
                    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
                    color: 'var(--tm-card-text)', fontSize: 13, boxSizing: 'border-box', outline: 'none',
                  }}
                  value={convTokenLimit}
                  onChange={e => onConvTokenLimitChange(e.target.value)}
                  placeholder="e.g. 50000"
                />
              </div>
            </>
          ) : (
            <div style={{ marginBottom: 14, padding: '8px 10px', borderRadius: 6, background: 'rgba(0,240,255,0.05)', border: '1px solid rgba(0,240,255,0.15)', fontSize: 11, color: C.textMuted, lineHeight: 1.5 }}>
              Multiple entry points — select each entry point node to edit its name and token limit individually.
            </div>
          )}

          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 6, display: 'block' }}>Canvas Status</label>
            <div style={{
              display: 'flex', alignItems: 'flex-start', gap: 8,
              padding: '8px 10px', borderRadius: 8,
              background: chain.ready ? 'rgba(74,222,128,0.06)' : 'rgba(255,180,171,0.06)',
              border: `1px solid ${chain.ready ? 'rgba(74,222,128,0.2)' : 'rgba(255,180,171,0.2)'}`,
            }}>
              <span style={{
                width: 7, height: 7, borderRadius: '50%', flexShrink: 0, marginTop: 4,
                background: chain.color, boxShadow: chain.ready ? `0 0 6px ${chain.color}` : 'none',
                display: 'inline-block',
              }} />
              <span style={{ fontSize: 12, color: chain.color, lineHeight: 1.5 }}>{chain.label}</span>
            </div>
          </div>

          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 6, display: 'block' }}>Canvas Info</label>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 6 }}>
              {[
                { label: 'Entry Points', value: String(chain.epNode ? 1 : 0) },
                { label: 'Orchestrator', value: chain.orchNode ? (chain.orchNode.data as OrchestratorData).displayName : '—' },
                { label: 'Agents', value: String(chain.agentCount) },
                { label: 'Status', value: app?.enabled ? 'Deployed' : 'Draft' },
              ].map(({ label, value }) => (
                <div key={label} style={{ padding: '7px 10px', borderRadius: 7, background: C.surfaceLow, border: `1px solid ${C.outlineVariant}` }}>
                  <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 2 }}>{label}</div>
                  <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{value}</div>
                </div>
              ))}
            </div>
          </div>

          {app && (
            <div style={{ marginBottom: 14 }}>
              <label style={{ fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' }}>Created</label>
              <div style={{ fontSize: 12, color: C.textMuted }}>
                {new Date(app.created_at).toLocaleString()}
              </div>
            </div>
          )}

          <div style={{ marginTop: 8, padding: '8px 0', borderTop: `1px solid ${C.outlineVariant}`, fontSize: 11, color: C.textMuted, lineHeight: 1.6 }}>
            Click any node to edit its properties.
          </div>
        </div>
      ) : (
        <>
          <div style={{ display: 'flex', gap: 4, marginBottom: 18 }}>
            <TabBtn id="properties" label="Properties" />
            <TabBtn id="configuration" label="Configuration" />
          </div>

          {/* EntryPoint properties */}
          {selectedNode.type === 'entryPoint' && propTab === 'properties' && (() => {
            const d = selectedNode.data as EntryPointData;
            return (
              <div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Display Name</label>
                  <input style={inputStyle} value={d.appName ?? d.label} onChange={e => onUpdateNode(selectedNode.id, { appName: e.target.value, label: e.target.value })} placeholder="e.g. Customer Support" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Token Limit <span style={{ fontSize: 10, color: '#64748b' }}>per session · blank = unlimited</span></label>
                  <input type="number" min={1} style={inputStyle} value={d.convTokenLimit ?? ''} onChange={e => onUpdateNode(selectedNode.id, { convTokenLimit: e.target.value })} placeholder="e.g. 50000" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Max Concurrent Sessions <span style={{ fontSize: 10, color: '#64748b' }}>blank = unlimited</span></label>
                  <input type="number" min={1} style={inputStyle} value={d.maxConcurrentSessions ?? ''} onChange={e => onUpdateNode(selectedNode.id, { maxConcurrentSessions: e.target.value })} placeholder="e.g. 10" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Queue Timeout (seconds) <span style={{ fontSize: 10, color: '#64748b' }}>blank = reject immediately</span></label>
                  <input type="number" min={1} style={inputStyle} value={d.queueTimeout ?? ''} onChange={e => onUpdateNode(selectedNode.id, { queueTimeout: e.target.value })} placeholder="e.g. 60" />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Queue Message <span style={{ fontSize: 10, color: '#64748b' }}>shown while waiting</span></label>
                  <input style={inputStyle} value={d.queueMessage ?? ''} onChange={e => onUpdateNode(selectedNode.id, { queueMessage: e.target.value })} placeholder="All agents are busy, please wait..." />
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Type</label>
                  <select style={{ ...inputStyle }} value={d.epType} onChange={e => onUpdateNode(selectedNode.id, { epType: e.target.value as EntryPointType })}>
                    <option value="websocket">WebSocket</option>
                    <option value="sse">SSE</option>
                    <option value="webrtc">WebRTC Voice</option>
                    <option value="voice">Voice (STT/TTS)</option>
                  </select>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Access Policy</label>
                  <select style={{ ...inputStyle }} value={d.accessMode} onChange={e => onUpdateNode(selectedNode.id, { accessMode: e.target.value as 'token' | 'public' })}>
                    <option value="token">Token required</option>
                    <option value="public">Public (no auth)</option>
                  </select>
                </div>
                <div style={fieldWrap}>
                  <label style={{ ...labelStyle, display: 'flex', alignItems: 'center', gap: 6 }}>
                    Slug
                    {!slugLocked && <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 10, background: 'rgba(0,240,255,0.1)', color: C.cyan, border: '1px solid rgba(0,240,255,0.3)', fontWeight: 600 }}>auto</span>}
                  </label>
                  <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace' }} value={d.slug} onChange={e => { onSlugManualEdit(); onUpdateNode(selectedNode.id, { slug: e.target.value }); }} placeholder="my-app-slug" />
                  {d.slug && (
                    <div style={{
                      fontSize: 11, color: C.textMuted, marginTop: 6, padding: '5px 8px',
                      background: C.surfaceLow, borderRadius: 5, fontFamily: 'JetBrains Mono, monospace',
                      wordBreak: 'break-all', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 6,
                    }}>
                      <span style={{ flex: 1 }}>
                        {d.epType === 'websocket' ? `ws://<host>:8088/apps/${d.slug}/ws`
                          : d.epType === 'webrtc' ? `http://<host>:8088/apps/${d.slug}/voice`
                          : d.epType === 'voice' ? `http://<host>:8088/apps/${d.slug}/voice/transcribe · /voice/tts`
                          : `http://<host>:8088/apps/${d.slug}/sse`}
                      </span>
                      <button
                        onClick={() => navigator.clipboard.writeText(
                          d.epType === 'websocket'
                            ? `ws://localhost:8088/apps/${d.slug}/ws`
                            : d.epType === 'webrtc'
                            ? `http://localhost:8088/apps/${d.slug}/voice`
                            : d.epType === 'voice'
                            ? `http://localhost:8088/apps/${d.slug}/voice/transcribe`
                            : `http://localhost:8088/apps/${d.slug}/sse`
                        )}
                        title="Copy endpoint URL"
                        style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.cyan, flexShrink: 0, padding: 0 }}
                      >
                        <span className="material-symbols-outlined" style={{ fontSize: 14 }}>content_copy</span>
                      </button>
                    </div>
                  )}
                </div>
                {(() => {
                  const orchEdge = edges.find((e: Edge) => e.source === selectedNode.id);
                  const orchNode = orchEdge ? nodes.find((nd: Node) => nd.id === orchEdge.target && nd.type === 'orchestrator') : undefined;
                  const orchName = orchNode ? (orchNode.data as OrchestratorData).name : '';
                  const isSaved = !!(app?.entry_points?.find((ep: { slug: string }) => ep.slug === d.slug));
                  const testUrl = d.epType === 'voice' || d.epType === 'webrtc'
                    ? `/apps/${d.slug}/voice`
                    : orchName ? `/admin/playground?orchestrator=${encodeURIComponent(orchName)}` : '/admin/playground';
                  return (
                    <div style={{ marginTop: 12 }}>
                      <button
                        disabled={!isSaved}
                        onClick={() => { if (isSaved) window.open(testUrl, '_blank', 'noopener'); }}
                        title={isSaved ? 'Open test interface' : 'Save the application first to enable testing'}
                        style={{
                          width: '100%', padding: '8px 0', borderRadius: 8, border: `1px solid ${isSaved ? C.green : C.outlineVariant}`,
                          background: 'transparent', color: isSaved ? C.green : C.textMuted,
                          cursor: isSaved ? 'pointer' : 'not-allowed', fontSize: 13, fontWeight: 600,
                          display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
                          opacity: isSaved ? 1 : 0.5,
                        }}
                      >
                        <span className="material-symbols-outlined" style={{ fontSize: 15 }}>
                          {d.epType === 'voice' ? 'mic' : d.epType === 'webrtc' ? 'videocam' : 'play_arrow'}
                        </span>
                        Test Entry Point
                      </button>
                      {!isSaved && (
                        <div style={{ fontSize: 10, color: C.textMuted, textAlign: 'center', marginTop: 4 }}>
                          Save the application first to enable testing
                        </div>
                      )}
                    </div>
                  );
                })()}
              </div>
            );
          })()}

          {/* Orchestrator properties */}
          {selectedNode.type === 'orchestrator' && propTab === 'properties' && (() => {
            const d = selectedNode.data as OrchestratorData;
            const connectedAgentCount = edges.filter(e => e.source === selectedNode.id && nodes.find(n => n.id === e.target && n.type === 'agent')).length;
            const ORCH_PROVIDERS: Record<string, string[]> = {
              anthropic: ['claude-opus-4-8', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
              openai:    ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo', 'o1', 'o3-mini'],
              groq:      ['openai/gpt-oss-120b', 'openai/gpt-oss-20b', 'qwen/qwen3.6-27b', 'groq/compound', 'groq/compound-mini'],
              gemini:    ['gemini-2.5-pro', 'gemini-2.0-flash', 'gemini-1.5-pro'],
            };
            const CUSTOM = '__custom__';
            const currentProvider = d.llmProvider || '';
            const knownModels = ORCH_PROVIDERS[currentProvider] ?? [];
            const isCustomModel = !!d.llmModel && knownModels.length > 0 && !knownModels.includes(d.llmModel);
            const selectVal = isCustomModel ? CUSTOM : (d.llmModel ?? '');
            const connectedEpTypes = new Set<string>(
              edges
                .filter(e => e.target === selectedNode.id)
                .map(e => nodes.find(n => n.id === e.source && n.type === 'entryPoint'))
                .filter((n): n is Node => !!n)
                .map(n => (n.data as EntryPointData).epType as string)
            );
            const hasVoice = connectedEpTypes.has('voice');
            const hasWebrtc = connectedEpTypes.has('webrtc');
            const hasLlmEp = hasVoice || connectedEpTypes.has('websocket') || connectedEpTypes.has('sse');
            const noEp = connectedEpTypes.size === 0;
            return (
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.purpleBg, border: '1px solid rgba(208,188,255,0.2)' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.purple }}>hub</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName || <span style={{ opacity: 0.4 }}>Unnamed</span>}</div>
                    <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.name || '—'}</div>
                  </div>
                </div>

                <div style={fieldWrap}>
                  <label style={labelStyle}>Display Name</label>
                  <input style={inputStyle} value={d.displayName} onChange={e => onUpdateNode(selectedNode.id, { displayName: e.target.value })} placeholder="Display name" />
                </div>

                {noEp && (
                  <div style={{ padding: '12px', borderRadius: 8, background: C.surfaceLow, border: `1px solid ${C.outlineVariant}`, color: C.textMuted, fontSize: 12, textAlign: 'center', marginTop: 8 }}>
                    Connect an entry point to start configuring
                  </div>
                )}

                {(hasLlmEp || hasWebrtc) && (
                  <>
                    <div style={{ padding: '8px 10px', borderRadius: 7, background: 'rgba(208,188,255,0.06)', border: '1px solid rgba(208,188,255,0.15)', fontSize: 11, color: C.textMuted, marginBottom: 4 }}>
                      LLM provider, model &amp; API key are configured in <strong style={{ color: C.purple }}>App Runtime → LLM Configuration</strong>.
                    </div>

                    <div style={fieldWrap}>
                      <label style={labelStyle}>System Prompt</label>
                      <textarea
                        style={{ ...inputStyle, resize: 'vertical', minHeight: 80, fontFamily: 'inherit', fontSize: 12 }}
                        value={d.systemPrompt ?? ''}
                        onChange={e => onUpdateNode(selectedNode.id, { systemPrompt: e.target.value })}
                        placeholder="You are a helpful assistant…"
                      />
                    </div>

                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Max Iterations</label>
                        <input type="number" min={1} max={100} style={inputStyle} value={d.maxIterations ?? ''} onChange={e => onUpdateNode(selectedNode.id, { maxIterations: parseInt(e.target.value, 10) || 10 })} placeholder="10" />
                      </div>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>History Window</label>
                        <input type="number" min={0} max={200} style={inputStyle} value={d.historyWindow ?? ''} onChange={e => onUpdateNode(selectedNode.id, { historyWindow: parseInt(e.target.value, 10) || 20 })} placeholder="20" />
                      </div>
                    </div>

                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Parallel Tools</label>
                        <input type="number" min={1} max={20} style={inputStyle} value={d.maxParallelTools ?? ''} onChange={e => onUpdateNode(selectedNode.id, { maxParallelTools: parseInt(e.target.value, 10) || 4 })} placeholder="4" />
                      </div>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Budget Tokens</label>
                        <input type="number" min={0} style={inputStyle} value={d.budgetTokens ?? ''} onChange={e => { const v = e.target.value; onUpdateNode(selectedNode.id, { budgetTokens: v === '' ? null : parseInt(v, 10) }); }} placeholder="unlimited" />
                      </div>
                    </div>

                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Kind</label>
                        <select style={{ ...inputStyle, cursor: 'pointer' }} value={d.kind || 'standard'} onChange={e => onUpdateNode(selectedNode.id, { kind: e.target.value })}>
                          <option value="standard">standard</option>
                          <option value="supervisor">supervisor</option>
                          <option value="delegator">delegator</option>
                        </select>
                      </div>
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Delegatable</label>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow, cursor: 'pointer' }} onClick={() => onUpdateNode(selectedNode.id, { delegatable: !d.delegatable })}>
                          <div style={{ width: 32, height: 18, borderRadius: 9, background: d.delegatable ? C.purple : 'rgba(255,255,255,0.12)', transition: 'background 200ms', position: 'relative', flexShrink: 0 }}>
                            <div style={{ position: 'absolute', top: 2, left: d.delegatable ? 16 : 2, width: 14, height: 14, borderRadius: '50%', background: '#fff', transition: 'left 200ms', boxShadow: '0 1px 3px rgba(0,0,0,0.4)' }} />
                          </div>
                          <span style={{ fontSize: 12, color: d.delegatable ? C.purple : C.textMuted }}>{d.delegatable ? 'Yes' : 'No'}</span>
                        </div>
                      </div>
                    </div>

                    <div style={fieldWrap}>
                      <label style={labelStyle}>Connected Agents</label>
                      <div style={{ fontSize: 12, color: C.textMuted, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                        {connectedAgentCount} agent{connectedAgentCount !== 1 ? 's' : ''} — connect via canvas
                      </div>
                    </div>
                  </>
                )}

                {/* STT section — voice EP only */}
                {hasVoice && (
                  <div style={{ marginTop: 16, borderTop: `1px solid ${C.outlineVariant}`, paddingTop: 14 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.amber }}>mic</span>
                      <span style={{ fontSize: 11, fontWeight: 700, color: C.amber, textTransform: 'uppercase', letterSpacing: '0.5px' }}>Speech-to-Text</span>
                      <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 9999, background: C.amberBg, border: `1px solid ${C.amberBorder}`, color: C.amber, fontWeight: 600 }}>Required</span>
                    </div>

                    <div style={fieldWrap}>
                      <label style={labelStyle}>Provider</label>
                      <select style={{ ...inputStyle, cursor: 'pointer' }}
                        value={d.transcriptionProvider || ''}
                        onChange={e => {
                          const p = e.target.value;
                          const model = p === 'openai' ? 'whisper-1' : p === 'groq' ? 'whisper-large-v3' : '';
                          onUpdateNode(selectedNode.id, { transcriptionProvider: p || null, transcriptionModel: model || null });
                        }}
                      >
                        <option value="">— select provider —</option>
                        <option value="openai">OpenAI Whisper</option>
                        <option value="groq">Groq whisper-large-v3</option>
                      </select>
                    </div>

                    {d.transcriptionProvider && (
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Model</label>
                        <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                          value={d.transcriptionModel ?? ''}
                          onChange={e => onUpdateNode(selectedNode.id, { transcriptionModel: e.target.value })}
                          placeholder="e.g. whisper-1"
                        />
                      </div>
                    )}

                    <div style={fieldWrap}>
                      <label style={labelStyle}>API Key</label>
                      <input type="password"
                        style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                        value={d.transcriptionApiKey ?? ''}
                        onChange={e => onUpdateNode(selectedNode.id, { transcriptionApiKey: e.target.value })}
                        placeholder={d.appOrchestratorId ? '••••••••  (leave blank to keep existing)' : 'Enter API key'}
                      />
                    </div>

                    {d.transcriptionProvider && d.transcriptionModel && (
                      <div style={{ marginTop: 4, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                        <button
                          onClick={() => testStt(d)}
                          disabled={sttTestState.loading || !d.appOrchestratorId}
                          title={!d.appOrchestratorId ? 'Save the application first to enable testing' : undefined}
                          style={{
                            display: 'flex', alignItems: 'center', gap: 6,
                            padding: '7px 14px', borderRadius: 8,
                            border: `1px solid ${C.amberBorder}`,
                            background: 'rgba(251,191,36,0.07)',
                            color: (!d.appOrchestratorId || sttTestState.loading) ? C.textMuted : C.amber,
                            cursor: (!d.appOrchestratorId || sttTestState.loading) ? 'not-allowed' : 'pointer',
                            fontSize: 12, fontWeight: 600, opacity: !d.appOrchestratorId ? 0.5 : 1,
                            transition: 'all 150ms',
                          }}
                        >
                          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>mic</span>
                          {sttTestState.loading ? 'Testing…' : 'Test STT'}
                        </button>
                        {!sttTestState.loading && sttTestState.ok !== undefined && (
                          sttTestState.ok
                            ? <span style={{ fontSize: 12, color: '#4edea3', fontWeight: 600 }}>✓ Connected ({sttTestState.latency}ms)</span>
                            : <span style={{ fontSize: 12, color: '#f87171' }}>✗ {sttTestState.error ?? 'Failed'}</span>
                        )}
                        {!d.appOrchestratorId && (
                          <span style={{ fontSize: 11, color: C.textMuted }}>Save first to enable testing</span>
                        )}
                      </div>
                    )}
                  </div>
                )}

                {/* TTS section — voice EP only */}
                {hasVoice && (
                  <div style={{ marginTop: 16, borderTop: `1px solid ${C.outlineVariant}`, paddingTop: 14 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.amber }}>volume_up</span>
                      <span style={{ fontSize: 11, fontWeight: 700, color: C.amber, textTransform: 'uppercase', letterSpacing: '0.5px' }}>Text-to-Speech</span>
                      <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 9999, background: C.amberBg, border: `1px solid ${C.amberBorder}`, color: C.amber, fontWeight: 600 }}>Required</span>
                    </div>

                    <div style={fieldWrap}>
                      <label style={labelStyle}>Provider</label>
                      <select style={{ ...inputStyle, cursor: 'pointer' }}
                        value={d.ttsProvider || ''}
                        onChange={e => onUpdateNode(selectedNode.id, { ttsProvider: e.target.value || null, ttsVoice: null })}
                      >
                        <option value="">— select provider —</option>
                        <option value="openai">OpenAI</option>
                        <option value="elevenlabs">ElevenLabs</option>
                      </select>
                    </div>

                    {d.ttsProvider === 'openai' && (
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Voice</label>
                        <select style={{ ...inputStyle, cursor: 'pointer' }}
                          value={d.ttsVoice || ''}
                          onChange={e => onUpdateNode(selectedNode.id, { ttsVoice: e.target.value || null })}
                        >
                          <option value="">— select voice —</option>
                          {['alloy', 'echo', 'fable', 'onyx', 'nova', 'shimmer'].map(v => <option key={v} value={v}>{v}</option>)}
                        </select>
                      </div>
                    )}
                    {d.ttsProvider === 'elevenlabs' && (
                      <div style={fieldWrap}>
                        <label style={labelStyle}>Voice ID</label>
                        <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                          value={d.ttsVoice ?? ''}
                          onChange={e => onUpdateNode(selectedNode.id, { ttsVoice: e.target.value || null })}
                          placeholder="ElevenLabs voice ID"
                        />
                      </div>
                    )}

                    <div style={fieldWrap}>
                      <label style={labelStyle}>API Key</label>
                      <input type="password"
                        style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                        value={d.ttsApiKey ?? ''}
                        onChange={e => onUpdateNode(selectedNode.id, { ttsApiKey: e.target.value })}
                        placeholder={d.appOrchestratorId ? '••••••••  (leave blank to keep existing)' : 'Enter API key'}
                      />
                    </div>

                    {d.ttsProvider && d.ttsVoice && (
                      <div style={{ marginTop: 4, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                        <button
                          onClick={() => testTts(d)}
                          disabled={ttsTestState.loading || !d.appOrchestratorId}
                          title={!d.appOrchestratorId ? 'Save the application first to enable testing' : undefined}
                          style={{
                            display: 'flex', alignItems: 'center', gap: 6,
                            padding: '7px 14px', borderRadius: 8,
                            border: `1px solid ${C.amberBorder}`,
                            background: 'rgba(251,191,36,0.07)',
                            color: (!d.appOrchestratorId || ttsTestState.loading) ? C.textMuted : C.amber,
                            cursor: (!d.appOrchestratorId || ttsTestState.loading) ? 'not-allowed' : 'pointer',
                            fontSize: 12, fontWeight: 600, opacity: !d.appOrchestratorId ? 0.5 : 1,
                            transition: 'all 150ms',
                          }}
                        >
                          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>volume_up</span>
                          {ttsTestState.loading ? 'Testing…' : 'Test TTS'}
                        </button>
                        {!ttsTestState.loading && ttsTestState.ok !== undefined && (
                          ttsTestState.ok
                            ? <span style={{ fontSize: 12, color: '#4edea3', fontWeight: 600 }}>✓ Connected ({ttsTestState.latency}ms)</span>
                            : <span style={{ fontSize: 12, color: '#f87171' }}>✗ {ttsTestState.error ?? 'Failed'}</span>
                        )}
                        {!d.appOrchestratorId && (
                          <span style={{ fontSize: 11, color: C.textMuted }}>Save first to enable testing</span>
                        )}
                      </div>
                    )}
                  </div>
                )}

                {/* Realtime section — webrtc EP only */}
                {hasWebrtc && (
                  <div style={{ marginTop: 16, borderTop: `1px solid ${C.outlineVariant}`, paddingTop: 14 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.cyan }}>sensors</span>
                      <span style={{ fontSize: 11, fontWeight: 700, color: C.cyan, textTransform: 'uppercase', letterSpacing: '0.5px' }}>Realtime Voice</span>
                    </div>
                    <div style={{ fontSize: 12, color: C.textMuted, padding: '10px 12px', borderRadius: 8, background: C.surfaceLow, border: `1px solid ${C.outlineVariant}` }}>
                      WebRTC entry points require a realtime-capable model (e.g. gpt-4o-realtime-preview). Configure the LLM model above accordingly.
                    </div>
                  </div>
                )}
              </div>
            );
          })()}

          {/* Agent properties */}
          {selectedNode.type === 'agent' && propTab === 'properties' && (() => {
            const d = selectedNode.data as AgentData;
            const icon = d.icon || agentIconForLibrary({ slug: d.name, icon: d.icon } as any);
            return (
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.greenBg, border: `1px solid ${C.greenBorder}` }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.green }}>{icon}</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName}</div>
                    <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.name}</div>
                  </div>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Description</label>
                  <div style={{ fontSize: 12, color: 'var(--tm-card-text-hint)', lineHeight: 1.55, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                    {d.description || <span style={{ opacity: 0.4 }}>No description</span>}
                  </div>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Transport</label>
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '3px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: C.greenBg, color: C.green, border: `1px solid ${C.greenBorder}` }}>
                    <span style={{ width: 5, height: 5, borderRadius: '50%', background: C.green, boxShadow: `0 0 5px ${C.green}` }} />
                    {d.transport}
                  </span>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Endpoint</label>
                  <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', wordBreak: 'break-all', padding: '6px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                    {d.endpointUrl}
                  </div>
                </div>
                <a href="/admin/agents" style={{ fontSize: 12, color: C.green, textDecoration: 'none', display: 'flex', alignItems: 'center', gap: 4, marginTop: 8, opacity: 0.8 }}
                  onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
                  onMouseLeave={e => (e.currentTarget.style.opacity = '0.8')}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
                  Configure in Agents
                </a>
              </div>
            );
          })()}

          {/* Middleware properties */}
          {selectedNode.type === 'middleware' && propTab === 'properties' && (() => {
            const mwNode = selectedNode;
            const d = mwNode.data as MiddlewareData;
            const icon = d.kind === 'guard' ? 'shield' : 'bolt';
            const kindBadge = (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '3px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: C.amberBg, color: C.amber, border: `1px solid ${C.amberBorder}` }}>
                <span style={{ width: 5, height: 5, borderRadius: '50%', background: C.amber, boxShadow: `0 0 5px ${C.amber}` }} />
                {d.kind}
              </span>
            );
            const co = (d.configOverride ?? {}) as Record<string, unknown>;
            function setOverride(patch: Record<string, unknown>) {
              onUpdateNode(mwNode.id, { configOverride: { ...co, ...patch } });
            }
            return (
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.amberBg, border: `1px solid ${C.amberBorder}` }}>
                  <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.amber }}>{icon}</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName}</div>
                    <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.slug}</div>
                  </div>
                </div>
                <div style={fieldWrap}>
                  <label style={labelStyle}>Kind</label>
                  {kindBadge}
                </div>
                {d.description && (
                  <div style={fieldWrap}>
                    <label style={labelStyle}>Description</label>
                    <div style={{ fontSize: 12, color: 'var(--tm-card-text-hint)', lineHeight: 1.55, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
                      {d.description}
                    </div>
                  </div>
                )}
                <div style={{ marginTop: 8, marginBottom: 8, paddingTop: 8, borderTop: `1px solid ${C.outlineVariant}`, fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase' }}>
                  Config Override
                </div>
                {d.kind === 'guard' && (
                  <>
                    <div style={fieldWrap}>
                      <label style={labelStyle}>Mode</label>
                      <select
                        style={{ ...inputStyle }}
                        value={(co.mode as string) ?? ''}
                        onChange={e => setOverride({ mode: e.target.value || undefined })}
                      >
                        <option value="">— default —</option>
                        <option value="block">block</option>
                        <option value="redact">redact</option>
                      </select>
                    </div>
                    <div style={{ ...fieldWrap, display: 'flex', flexDirection: 'column', gap: 8 }}>
                      <label style={{ ...labelStyle, marginBottom: 0 }}>Detection</label>
                      <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: C.text, cursor: 'pointer' }}>
                        <input
                          type="checkbox"
                          checked={co.pii_detection !== false}
                          onChange={e => setOverride({ pii_detection: e.target.checked })}
                          style={{ accentColor: C.amber }}
                        />
                        PII detection
                      </label>
                      <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: C.text, cursor: 'pointer' }}>
                        <input
                          type="checkbox"
                          checked={co.injection_detection !== false}
                          onChange={e => setOverride({ injection_detection: e.target.checked })}
                          style={{ accentColor: C.amber }}
                        />
                        Injection detection
                      </label>
                    </div>
                  </>
                )}
                {d.kind === 'cache' && (
                  <>
                    <div style={fieldWrap}>
                      <label style={labelStyle}>TTL (seconds)</label>
                      <input
                        type="number"
                        min={1}
                        style={inputStyle}
                        value={(co.ttl_seconds as number | undefined) ?? ''}
                        placeholder="e.g. 300"
                        onChange={e => setOverride({ ttl_seconds: e.target.value ? parseInt(e.target.value, 10) : undefined })}
                      />
                    </div>
                    <div style={fieldWrap}>
                      <label style={labelStyle}>Scope</label>
                      <select
                        style={{ ...inputStyle }}
                        value={(co.scope as string) ?? ''}
                        onChange={e => setOverride({ scope: e.target.value || undefined })}
                      >
                        <option value="">— default —</option>
                        <option value="global">global</option>
                        <option value="app">app</option>
                        <option value="session">session</option>
                        <option value="user">user</option>
                      </select>
                    </div>
                    <div style={fieldWrap}>
                      <label style={labelStyle}>Max result chars</label>
                      <input
                        type="number"
                        min={1}
                        style={inputStyle}
                        value={(co.max_result_chars as number | undefined) ?? ''}
                        placeholder="e.g. 8000"
                        onChange={e => setOverride({ max_result_chars: e.target.value ? parseInt(e.target.value, 10) : undefined })}
                      />
                    </div>
                  </>
                )}
              </div>
            );
          })()}

          {propTab === 'configuration' && (
            <div style={{ color: C.textMuted, fontSize: 13, padding: 10 }}>
              Configuration options for this node type are managed at the resource level.<br /><br />
              <span style={{ fontSize: 11, opacity: 0.7 }}>Use the Properties tab or navigate to the resource admin page.</span>
            </div>
          )}
        </>
      )}
    </div>
  );
}
