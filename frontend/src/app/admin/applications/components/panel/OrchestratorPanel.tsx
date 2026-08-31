'use client';
import { useState, useEffect } from 'react';
import { themApi } from '@/lib/api';
import type { MCPServer } from '@/lib/api';
import type { Node, Edge } from '@xyflow/react';
import type { Application, OrchestratorData, EntryPointData, MCPServerAttachment } from '../../types';
import { C } from '../../constants';
import { labelStyle, inputStyle, fieldWrap } from './panelStyles';

interface Props {
  selectedNode: Node;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
  app: Application | null;
  nodes: Node[];
  edges: Edge[];
}

type TestState = { loading?: boolean; ok?: boolean; latency?: number; error?: string };

export function OrchestratorPanel({ selectedNode, onUpdateNode, app, nodes, edges }: Props) {
  const d = selectedNode.data as OrchestratorData;

  const [orchTestState, setOrchTestState] = useState<TestState>({});
  const [sttTestState,  setSttTestState]  = useState<TestState>({});
  const [ttsTestState,  setTtsTestState]  = useState<TestState>({});
  const [availableMCPServers, setAvailableMCPServers] = useState<MCPServer[]>([]);
  const [mcpSaving, setMcpSaving] = useState(false);
  const [mcpExpanded, setMcpExpanded] = useState<Record<string, boolean>>({});
  const [attachedServers, setAttachedServers] = useState<MCPServerAttachment[]>(
    () => d.mcpServers ?? []
  );

  useEffect(() => {
    setAttachedServers(d.mcpServers ?? []);
    setMcpExpanded({});
  }, [selectedNode.id]);

  useEffect(() => {
    themApi.listMCPServers().then(setAvailableMCPServers).catch(() => {});
  }, []);

  async function testOrchLlm() {
    if (!d.llmProvider || !d.llmModel || !d.appOrchestratorId || !app) return;
    setOrchTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchLlm(app.id, d.appOrchestratorId, { provider: d.llmProvider, model: d.llmModel, api_key: d.llmApiKey || undefined });
      setOrchTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: unknown) {
      setOrchTestState({ loading: false, ok: false, error: (e as Error).message });
    }
  }

  async function saveMCPServers(servers: MCPServerAttachment[]) {
    if (!d.appOrchestratorId || !app) return;
    const prev = d.mcpServers ?? [];
    setAttachedServers(servers);
    onUpdateNode(selectedNode.id, { mcpServers: servers });
    setMcpSaving(true);
    try {
      await themApi.patchOrchestratorMCPServers(app.id, d.appOrchestratorId, servers);
    } catch {
      setAttachedServers(prev);
      onUpdateNode(selectedNode.id, { mcpServers: prev });
    } finally {
      setMcpSaving(false);
    }
  }

  async function testStt() {
    if (!d.transcriptionProvider || !d.transcriptionModel || !d.appOrchestratorId || !app) return;
    setSttTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchVoice(app.id, d.appOrchestratorId, { provider: d.transcriptionProvider, model: d.transcriptionModel });
      setSttTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: unknown) {
      setSttTestState({ loading: false, ok: false, error: (e as Error).message });
    }
  }

  async function testTts() {
    if (!d.ttsProvider || !d.ttsVoice || !d.appOrchestratorId || !app) return;
    setTtsTestState({ loading: true });
    try {
      const res = await themApi.testAppOrchTts(app.id, d.appOrchestratorId, { provider: d.ttsProvider, voice: d.ttsVoice });
      setTtsTestState({ loading: false, ok: res.ok, latency: res.latency_ms, error: res.error });
    } catch (e: unknown) {
      setTtsTestState({ loading: false, ok: false, error: (e as Error).message });
    }
  }

  const connectedAgentCount = edges.filter(e => e.source === selectedNode.id && nodes.find(n => n.id === e.target && n.type === 'agent')).length;

  const connectedEpTypes = new Set<string>(
    edges
      .filter(e => e.target === selectedNode.id)
      .map(e => nodes.find(n => n.id === e.source && n.type === 'entryPoint'))
      .filter((n): n is Node => !!n)
      .map(n => (n.data as EntryPointData).epType as string)
  );
  const hasVoice   = connectedEpTypes.has('voice');
  const hasWebrtc  = connectedEpTypes.has('webrtc');
  const hasLlmEp   = hasVoice || connectedEpTypes.has('websocket') || connectedEpTypes.has('sse');
  const noEp       = connectedEpTypes.size === 0;

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

      {/* MCP Servers */}
      <div style={{ marginTop: 4, borderTop: `1px solid ${C.outlineVariant}`, paddingTop: 12 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.purple }}>lan</span>
          <span style={{ fontSize: 11, fontWeight: 700, color: C.purple, textTransform: 'uppercase', letterSpacing: '0.5px' }}>MCP Servers</span>
          {mcpSaving && <span style={{ fontSize: 10, color: C.textMuted }}>saving…</span>}
        </div>
        {availableMCPServers.filter(s => s.enabled).length === 0 ? (
          <div style={{ fontSize: 11, color: C.textMuted, padding: '6px 10px', borderRadius: 6, background: C.surfaceLow, border: `1px solid ${C.outlineVariant}` }}>
            No MCP servers configured — add one in MCP Store
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {availableMCPServers.filter(s => s.enabled).map(server => {
              const attachment = attachedServers.find(a => a.slug === server.slug);
              const isAttached = !!attachment;
              const allTools = server.tools_manifest ?? [];
              const allowlist = attachment?.tools ?? [];
              const expanded = !!mcpExpanded[server.slug];
              const statusColor = server.health_status === 'healthy' ? '#4ade80' : server.health_status === 'degraded' ? C.amber : server.health_status === 'unreachable' ? '#f87171' : C.textMuted;
              const activeCount = allowlist.length === 0 ? allTools.length : allowlist.length;

              function toggleAttach() {
                const next = isAttached
                  ? attachedServers.filter(a => a.slug !== server.slug)
                  : [...attachedServers, { slug: server.slug, tools: [] }];
                if (!isAttached) setMcpExpanded(prev => ({ ...prev, [server.slug]: true }));
                saveMCPServers(next);
              }

              function toggleTool(toolName: string) {
                const base = allowlist.length === 0 ? allTools.map(t => t.name) : [...allowlist];
                const next = base.includes(toolName) ? base.filter(t => t !== toolName) : [...base, toolName];
                const tools = next.length === allTools.length ? [] : next;
                saveMCPServers(attachedServers.map(a => a.slug === server.slug ? { ...a, tools } : a));
              }

              return (
                <div key={server.slug} style={{ borderRadius: 6, border: `1px solid ${isAttached ? 'rgba(208,188,255,0.3)' : C.outlineVariant}`, background: isAttached ? 'rgba(208,188,255,0.06)' : C.surfaceLow, overflow: 'hidden' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 10px' }}>
                    <div onClick={e => { e.stopPropagation(); toggleAttach(); }} style={{ width: 14, height: 14, borderRadius: 3, border: `1.5px solid ${isAttached ? C.purple : C.outlineVariant}`, background: isAttached ? C.purple : 'transparent', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, cursor: 'pointer' }}>
                      {isAttached && <span className="material-symbols-outlined" style={{ fontSize: 10, color: '#fff', fontWeight: 700 }}>check</span>}
                    </div>
                    <div style={{ flex: 1, minWidth: 0, cursor: 'pointer' }} onClick={() => setMcpExpanded(prev => ({ ...prev, [server.slug]: !prev[server.slug] }))}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{server.name}</div>
                      <div style={{ fontSize: 10, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{server.slug}</div>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexShrink: 0 }}>
                      <div style={{ width: 6, height: 6, borderRadius: '50%', background: statusColor }} title={server.health_status} />
                      <span style={{ fontSize: 10, color: isAttached && allowlist.length > 0 ? C.amber : C.textMuted }}>
                        {isAttached ? `${activeCount}/${allTools.length}` : allTools.length}
                      </span>
                      {allTools.length > 0 && (
                        <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, cursor: 'pointer', transition: 'transform 150ms', transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)' }}>
                          expand_more
                        </span>
                      )}
                    </div>
                  </div>
                  {expanded && allTools.length > 0 && (
                    <div style={{ borderTop: `1px solid ${C.outlineVariant}`, padding: '6px 10px', display: 'flex', flexDirection: 'column', gap: 3, background: 'rgba(0,0,0,0.15)' }}>
                      {allTools.map(tool => {
                        const enabled = isAttached && (allowlist.length === 0 || allowlist.includes(tool.name));
                        return (
                          <div key={tool.name} onClick={() => isAttached && toggleTool(tool.name)}
                            style={{ display: 'flex', alignItems: 'center', gap: 7, padding: '3px 2px', borderRadius: 4, cursor: isAttached ? 'pointer' : 'default', opacity: isAttached ? 1 : 0.4 }}>
                            <div style={{ width: 11, height: 11, borderRadius: 2, border: `1.5px solid ${enabled ? C.purple : 'rgba(255,255,255,0.2)'}`, background: enabled ? C.purple : 'transparent', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                              {enabled && <span className="material-symbols-outlined" style={{ fontSize: 8, color: '#fff' }}>check</span>}
                            </div>
                            <span style={{ fontSize: 11, color: enabled ? C.text : C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{tool.name}</span>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>

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
                onClick={testStt}
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
                onClick={testTts}
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

      {/* LLM test — shown when a provider/model is selected */}
      {d.llmProvider && d.llmModel && (
        <div style={{ marginTop: 12, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <button
            onClick={testOrchLlm}
            disabled={orchTestState.loading || !d.appOrchestratorId}
            title={!d.appOrchestratorId ? 'Save the application first to enable testing' : undefined}
            style={{
              display: 'flex', alignItems: 'center', gap: 6,
              padding: '7px 14px', borderRadius: 8,
              border: `1px solid rgba(208,188,255,0.3)`,
              background: 'rgba(208,188,255,0.07)',
              color: (!d.appOrchestratorId || orchTestState.loading) ? C.textMuted : C.purple,
              cursor: (!d.appOrchestratorId || orchTestState.loading) ? 'not-allowed' : 'pointer',
              fontSize: 12, fontWeight: 600, opacity: !d.appOrchestratorId ? 0.5 : 1,
              transition: 'all 150ms',
            }}
          >
            <span className="material-symbols-outlined" style={{ fontSize: 15 }}>bolt</span>
            {orchTestState.loading ? 'Testing…' : 'Test LLM'}
          </button>
          {!orchTestState.loading && orchTestState.ok !== undefined && (
            orchTestState.ok
              ? <span style={{ fontSize: 12, color: '#4edea3', fontWeight: 600 }}>✓ Connected ({orchTestState.latency}ms)</span>
              : <span style={{ fontSize: 12, color: '#f87171' }}>✗ {orchTestState.error ?? 'Failed'}</span>
          )}
        </div>
      )}
    </div>
  );
}
