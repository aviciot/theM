'use client';
import type { Node, Edge } from '@xyflow/react';
import type { OrchNodeData, EpNodeData } from '../../../types';
import { C } from '../../../constants';
import type { MCPServer, MCPServerAttachment } from '@/lib/api';
import { fieldStyle, selectStyle, chipStyle, isSectionOpen, SectionHeader } from './panelShared';

// ── OrchestratorNodePanel ────────────────────────────────────────────────────

interface Props {
  selectedNode: Node;
  nodes: Node[];
  edges: Edge[];
  openSections: Record<string, boolean>;
  setOpenSections: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  availableMCPServers: MCPServer[];
  mcpExpanded: Record<string, boolean>;
  setMcpExpanded: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
  setLogoResult: (v: 'none' | 'valid' | 'invalid' | 'warn') => void;
}

export function OrchestratorNodePanel({
  selectedNode, nodes, edges,
  openSections, setOpenSections,
  availableMCPServers, mcpExpanded, setMcpExpanded,
  setNodes, setIsDirty, setLogoResult,
}: Props) {
  const liveOrchNode = nodes.find(n => n.id === selectedNode.id);
  const d = (liveOrchNode?.data ?? selectedNode.data) as unknown as OrchNodeData;
  const cfg = d.config ?? {};
  const connectedEps = edges
    .filter(e => e.target === selectedNode.id)
    .map(e => nodes.find(n => n.id === e.source))
    .filter(n => n?.type === 'entryPoint');
  const hasVoice = connectedEps.some(n => (n?.data as unknown as EpNodeData)?.protocol === 'voice');
  const sttProvider = (cfg.stt_provider as string) ?? 'openai';
  const ttsProvider = (cfg.tts_provider as string) ?? 'openai';
  const ttsVoice = (cfg.tts_voice as string) ?? 'alloy';

  function setOrchConfig(patch: Record<string, unknown>) {
    setNodes(ns => ns.map(n => n.id === selectedNode.id
      ? { ...n, data: { ...n.data, config: { ...(n.data as unknown as OrchNodeData).config, ...patch } } }
      : n));
    setIsDirty(true);
    setLogoResult('none');
  }

  const currentAttachments: MCPServerAttachment[] = (cfg.mcp_servers as MCPServerAttachment[] | undefined) ?? [];
  const enabledServers = availableMCPServers.filter(s => s.enabled);

  function toggleServer(slug: string) {
    const isAttached = currentAttachments.some(a => a.slug === slug);
    const next: MCPServerAttachment[] = isAttached
      ? currentAttachments.filter(a => a.slug !== slug)
      : [...currentAttachments, { slug, tools: [] }];
    setOrchConfig({ mcp_servers: next });
  }

  function toggleTool(slug: string, toolName: string) {
    const attachment = currentAttachments.find(a => a.slug === slug);
    if (!attachment) return;
    const current = attachment.tools ?? [];
    const server = enabledServers.find(s => s.slug === slug);
    const allTools = server?.tools_manifest?.map(t => t.name) ?? [];
    const base = current.length === 0 ? allTools : current;
    const next = base.includes(toolName)
      ? base.filter(t => t !== toolName)
      : [...base, toolName];
    const nextTools = next.length === allTools.length ? [] : next;
    const nextAttachments = currentAttachments.map(a =>
      a.slug === slug ? { ...a, tools: nextTools } : a
    );
    setOrchConfig({ mcp_servers: nextAttachments });
  }

  return (
    <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 12, overflowY: 'auto' }}>
      <div style={{ fontSize: 12, fontWeight: 700, color: C.purple, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>Orchestrator</div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Instance ID</label>
        <div style={chipStyle}>{d.instance_id}</div>
      </div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Display Name</label>
        <input
          style={fieldStyle}
          value={d.display_name ?? ''}
          onChange={e => { setNodes(ns => ns.map(n => n.id === selectedNode.id ? { ...n, data: { ...n.data, display_name: e.target.value } } : n)); setIsDirty(true); setLogoResult('none'); }}
        />
      </div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>System Prompt</label>
        <textarea
          style={{ ...fieldStyle, minHeight: 80, resize: 'vertical' }}
          value={(cfg.system_prompt as string) ?? ''}
          onChange={e => setOrchConfig({ system_prompt: e.target.value })}
        />
      </div>

      {/* LLM per-EP config */}
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
        <SectionHeader id="orch-llm" label="Entry Points" defaultOpen={true} openSections={openSections} setOpenSections={setOpenSections} />
        {isSectionOpen(openSections, 'orch-llm', true) && connectedEps.length === 0 && (
          <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic', marginTop: 6 }}>No entry points connected</div>
        )}
        {isSectionOpen(openSections, 'orch-llm', true) && connectedEps.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 8 }}>
            {connectedEps.map(epNode => {
              if (!epNode) return null;
              const ep = epNode.data as unknown as EpNodeData;
              return (
                <div key={ep.instance_id} style={{ padding: '7px 10px', borderRadius: 8, border: '1px solid rgba(0,240,255,0.12)', background: 'rgba(0,240,255,0.03)', display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontSize: 13, color: C.cyan }}>⬡</span>
                  <span style={{ fontSize: 12, fontWeight: 600, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{ep.slug || ep.instance_id}</span>
                  <span style={{ fontSize: 10, color: C.textMuted, marginLeft: 'auto' }}>{ep.protocol}</span>
                </div>
              );
            })}
            <div style={{ fontSize: 11, color: C.textMuted, fontStyle: 'italic', marginTop: 2 }}>
              LLM &amp; summarizer configured per entry point in Application Runtime.
            </div>
          </div>
        )}
      </div>

      {/* Advanced settings */}
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
        <SectionHeader id="orch-adv" label="Advanced" defaultOpen={false} openSections={openSections} setOpenSections={setOpenSections} />
        {isSectionOpen(openSections, 'orch-adv', false) && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
            <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Max Iterations</label>
              <input type="number" style={fieldStyle} value={(cfg.max_iterations as number) ?? 10} onChange={e => setOrchConfig({ max_iterations: Number(e.target.value) || null })} /></div>
            <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>History Window</label>
              <input type="number" style={fieldStyle} value={(cfg.history_window as number) ?? 20} onChange={e => setOrchConfig({ history_window: Number(e.target.value) || null })} /></div>
            <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Max Parallel Tools</label>
              <input type="number" style={fieldStyle} value={(cfg.max_parallel_tools as number) ?? 5} onChange={e => setOrchConfig({ max_parallel_tools: Number(e.target.value) || null })} /></div>
            <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Budget Tokens</label>
              <input type="number" style={fieldStyle} value={(cfg.budget_tokens as number) ?? ''} placeholder="none" onChange={e => setOrchConfig({ budget_tokens: e.target.value === '' ? null : Number(e.target.value) })} /></div>
          </div>
        )}
      </div>

      {/* Memory & Summarizer */}
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
        <SectionHeader id="orch-memory" label="Memory & Summarizer" defaultOpen={false} openSections={openSections} setOpenSections={setOpenSections} />
        {isSectionOpen(openSections, 'orch-memory', false) && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <label style={{ fontSize: 11, color: C.textMuted }}>Enable Summarizer</label>
              <div onClick={() => setOrchConfig({ memory_enabled: !(cfg.memory_enabled as boolean) })}
                style={{ width: 32, height: 18, borderRadius: 9, background: (cfg.memory_enabled as boolean) ? C.purple : 'rgba(255,255,255,0.12)', cursor: 'pointer', position: 'relative', transition: 'background 150ms' }}>
                <div style={{ position: 'absolute', top: 2, left: (cfg.memory_enabled as boolean) ? 16 : 2, width: 14, height: 14, borderRadius: '50%', background: '#fff', transition: 'left 150ms' }} />
              </div>
            </div>
            <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Summarize Every N Turns</label>
              <input type="number" style={fieldStyle} value={(cfg.summarize_every_n_calls as number) ?? 3} onChange={e => setOrchConfig({ summarize_every_n_calls: Number(e.target.value) || 3 })} /></div>
            <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Keep Last N Turns Verbatim</label>
              <input type="number" style={fieldStyle} value={(cfg.memory_raw_fallback_n as number) ?? 5} onChange={e => setOrchConfig({ memory_raw_fallback_n: Number(e.target.value) || 5 })} /></div>
            <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Summarizer Provider</label>
              <select style={selectStyle} value={(cfg.summarizer_provider as string) ?? ''} onChange={e => setOrchConfig({ summarizer_provider: e.target.value || null })}>
                <option value="">same as orchestrator</option>
                <option value="anthropic">anthropic</option>
                <option value="openai">openai</option>
                <option value="groq">groq</option>
                <option value="gemini">gemini</option>
              </select>
            </div>
            <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Summarizer Model</label>
              <input style={fieldStyle} value={(cfg.summarizer_model as string) ?? ''} onChange={e => setOrchConfig({ summarizer_model: e.target.value || null })} placeholder="e.g. claude-haiku-4-5-20251001" /></div>
          </div>
        )}
      </div>

      {/* Voice STT/TTS — only when a voice EP is connected */}
      {hasVoice && (
        <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
          <SectionHeader id="orch-voice" label="Voice (STT / TTS)" defaultOpen={true} openSections={openSections} setOpenSections={setOpenSections} />
          {isSectionOpen(openSections, 'orch-voice', true) && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
              <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>STT Provider</label>
                <select style={selectStyle} value={sttProvider} onChange={e => { const p = e.target.value; setOrchConfig({ stt_provider: p, stt_model: p === 'openai' ? 'whisper-1' : 'whisper-large-v3' }); }}>
                  <option value="openai">openai</option>
                  <option value="groq">groq</option>
                </select>
              </div>
              <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>STT Model</label>
                <input style={fieldStyle} value={(cfg.stt_model as string) ?? ''} onChange={e => setOrchConfig({ stt_model: e.target.value })} placeholder="e.g. whisper-1" />
              </div>
              <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>TTS Provider</label>
                <select style={selectStyle} value={ttsProvider} onChange={e => setOrchConfig({ tts_provider: e.target.value, tts_voice: '' })}>
                  <option value="openai">openai</option>
                  <option value="elevenlabs">elevenlabs</option>
                </select>
              </div>
              <div><label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>TTS Voice</label>
                {ttsProvider === 'openai'
                  ? <select style={selectStyle} value={ttsVoice} onChange={e => setOrchConfig({ tts_voice: e.target.value })}>{['alloy','echo','fable','onyx','nova','shimmer'].map(v => <option key={v} value={v}>{v}</option>)}</select>
                  : <input style={fieldStyle} value={ttsVoice} onChange={e => setOrchConfig({ tts_voice: e.target.value })} placeholder="ElevenLabs voice ID" />}
              </div>
              <div style={{ fontSize: 11, color: C.textMuted, fontStyle: 'italic' }}>API keys via Secret Bindings — not stored here</div>
            </div>
          )}
        </div>
      )}

      {/* MCP Servers */}
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.07)', paddingTop: 10 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.purple }}>lan</span>
          <span style={{ fontSize: 11, fontWeight: 700, color: C.purple, textTransform: 'uppercase', letterSpacing: '0.5px' }}>MCP Servers</span>
        </div>
        {enabledServers.length === 0 ? (
          <div style={{ fontSize: 11, color: C.textMuted, fontStyle: 'italic' }}>No MCP servers configured — add one in MCP Store</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {enabledServers.map(server => {
              const attachment = currentAttachments.find(a => a.slug === server.slug);
              const isAttached = !!attachment;
              const allowlist = attachment?.tools ?? [];
              const allTools = server.tools_manifest ?? [];
              const statusColor = server.health_status === 'healthy' ? '#4ade80' : server.health_status === 'degraded' ? C.amber : server.health_status === 'unreachable' ? '#f87171' : C.textMuted;
              const activeCount = allowlist.length === 0 ? allTools.length : allowlist.length;
              const expanded = !!mcpExpanded[server.slug];
              return (
                <div key={server.slug} style={{ borderRadius: 7, border: `1px solid ${isAttached ? 'rgba(208,188,255,0.25)' : 'rgba(255,255,255,0.08)'}`, overflow: 'hidden' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 9px', background: isAttached ? 'rgba(208,188,255,0.07)' : 'transparent' }}>
                    <div
                      onClick={e => {
                        e.stopPropagation();
                        if (!isAttached) setMcpExpanded(prev => ({ ...prev, [server.slug]: true }));
                        toggleServer(server.slug);
                      }}
                      style={{ width: 13, height: 13, borderRadius: 3, border: `1.5px solid ${isAttached ? C.purple : 'rgba(255,255,255,0.3)'}`, background: isAttached ? C.purple : 'transparent', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, cursor: 'pointer' }}>
                      {isAttached && <span className="material-symbols-outlined" style={{ fontSize: 9, color: '#fff' }}>check</span>}
                    </div>
                    <div style={{ flex: 1, minWidth: 0, cursor: 'pointer' }} onClick={() => setMcpExpanded(prev => ({ ...prev, [server.slug]: !prev[server.slug] }))}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{server.name}</div>
                      <div style={{ fontSize: 10, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{server.slug}</div>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0 }}>
                      <div style={{ width: 6, height: 6, borderRadius: '50%', background: statusColor }} title={server.health_status} />
                      {isAttached && allTools.length > 0 ? (
                        <span style={{ fontSize: 10, color: allowlist.length > 0 ? C.amber : C.textMuted }}>
                          {activeCount}/{allTools.length}
                        </span>
                      ) : (
                        <span style={{ fontSize: 10, color: C.textMuted }}>{allTools.length}</span>
                      )}
                      {allTools.length > 0 && (
                        <span
                          className="material-symbols-outlined"
                          style={{ fontSize: 14, color: C.textMuted, cursor: 'pointer', transition: 'transform 150ms', transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)' }}
                          onClick={e => { e.stopPropagation(); setMcpExpanded(prev => ({ ...prev, [server.slug]: !prev[server.slug] })); }}>
                          expand_more
                        </span>
                      )}
                    </div>
                  </div>
                  {expanded && allTools.length > 0 && (
                    <div style={{ borderTop: '1px solid rgba(255,255,255,0.06)', padding: '6px 9px', display: 'flex', flexDirection: 'column', gap: 3, background: 'rgba(0,0,0,0.15)' }}>
                      {allTools.map(tool => {
                        const toolEnabled = allowlist.length === 0 || allowlist.includes(tool.name);
                        return (
                          <div key={tool.name}
                            onClick={e => { e.stopPropagation(); if (isAttached) toggleTool(server.slug, tool.name); }}
                            style={{ display: 'flex', alignItems: 'flex-start', gap: 7, padding: '3px 2px', cursor: isAttached ? 'pointer' : 'default', borderRadius: 4, opacity: isAttached ? 1 : 0.45 }}>
                            <div style={{ width: 11, height: 11, marginTop: 1, borderRadius: 2, border: `1.5px solid ${toolEnabled ? C.purple : 'rgba(255,255,255,0.2)'}`, background: toolEnabled ? C.purple : 'transparent', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                              {toolEnabled && <span className="material-symbols-outlined" style={{ fontSize: 8, color: '#fff' }}>check</span>}
                            </div>
                            <div style={{ flex: 1, minWidth: 0 }}>
                              <div style={{ fontSize: 11, color: toolEnabled ? C.text : C.textMuted, fontFamily: 'JetBrains Mono, monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{tool.name}</div>
                              {tool.description && <div style={{ fontSize: 10, color: C.textMuted, lineHeight: 1.3, marginTop: 1 }}>{tool.description}</div>}
                            </div>
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
    </div>
  );
}
