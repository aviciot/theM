import { useState, useEffect } from 'react';
import type { Node, Edge } from '@xyflow/react';
import { getNodeDef } from '@/lib/nodeRegistry';
import type { AgentRootData, SkillData, StepData, DebugState } from '../types';
import type { AgentIssue, MCPServer, MCPTool } from '@/lib/api';
import { themApi } from '@/lib/api';
import { C, labelStyle, inputStyle, textareaStyle, selectStyle, fieldGap, hint, LLM_MODELS } from '../constants';
import { stepMeta } from './StepNode';
import { extractNodeVars } from '../nodeVars';
import { TransformPanel } from './TransformPanel';

interface RightPanelProps {
  selectedNode: Node;
  setSelectedNode: (node: Node | null) => void;
  propertiesWidth: number;
  onResizeStart: (e: React.MouseEvent) => void;
  activeView: 'agent' | 'skill';
  agentNodes: Node[];
  localPipeNodes: Node[];
  localPipeEdges: Edge[];
  validationIssues: AgentIssue[];
  debug: DebugState;
  updateSelectedNodeField: (field: string, value: string) => void;
  updateStepConfig: (key: string, value: unknown) => void;
  setAgentNodes: (updater: (prev: Node[]) => Node[]) => void;
  setDirty: (dirty: boolean) => void;
  savePipelineState: () => void;
  setActiveSkillId: (id: string | null) => void;
  setActiveView: (view: 'agent' | 'skill') => void;
  setDebug: React.Dispatch<React.SetStateAction<DebugState>>;
  debugStep: () => void;
  nodeTypesReady?: boolean;
}

export function RightPanel({
  selectedNode,
  setSelectedNode,
  propertiesWidth,
  onResizeStart,
  activeView,
  agentNodes,
  localPipeNodes,
  localPipeEdges,
  validationIssues,
  debug,
  updateSelectedNodeField,
  updateStepConfig,
  setAgentNodes,
  setDirty,
  savePipelineState,
  setActiveSkillId,
  setActiveView,
  setDebug,
  debugStep,
  nodeTypesReady,
}: RightPanelProps) {
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  useEffect(() => {
    themApi.listMCPServers().then(s => setMcpServers(s ?? [])).catch(() => {});
  }, []);

  return (
    <div onKeyDown={e => e.stopPropagation()} style={{
      width: propertiesWidth, flexShrink: 0, borderLeft: `1px solid ${C.outline}`,
      background: C.surface, padding: '16px', overflowY: 'auto', position: 'relative',
    }} className="dark-scrollbar">
      <div
        onMouseDown={onResizeStart}
        style={{
          position: 'absolute', top: 0, left: -3, width: 6, height: '100%',
          cursor: 'col-resize', zIndex: 10,
        }}
      />
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '16px' }}>
        <span style={{ color: C.text, fontWeight: 700, fontSize: '13px' }}>Properties</span>
        <button onClick={() => setSelectedNode(null)} style={{ background: 'transparent', border: 'none', color: C.textMuted, cursor: 'pointer', fontSize: '16px' }}>x</button>
      </div>

      {selectedNode.type === 'agentRoot' && (() => {
        const d = (agentNodes.find(n => n.id === selectedNode.id)?.data ?? selectedNode.data) as unknown as AgentRootData;
        return (
          <>
            <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginBottom: '4px' }}>Display Name</label>
            <input
              value={d.display_name}
              onChange={e => updateSelectedNodeField('display_name', e.target.value)}
              style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
            />
            <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Description</label>
            <textarea
              value={d.description}
              onChange={e => updateSelectedNodeField('description', e.target.value)}
              rows={3}
              style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', resize: 'vertical', boxSizing: 'border-box' }}
            />
            <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Version</label>
            <input
              value={d.version}
              onChange={e => updateSelectedNodeField('version', e.target.value)}
              style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
            />
          </>
        );
      })()}

      {selectedNode.type === 'skill' && (() => {
        const liveSkillNode = agentNodes.find(n => n.id === selectedNode.id);
        const d = (liveSkillNode?.data ?? selectedNode.data) as unknown as SkillData;
        const skillNodeId = selectedNode.id;
        const MODES = ['text/plain', 'text/markdown', 'application/json', 'application/octet-stream'];
        function updateSkillArray(field: keyof SkillData, arr: string[]) {
          if (activeView === 'agent') {
            setAgentNodes(prev => prev.map(n =>
              n.id === skillNodeId ? { ...n, data: { ...n.data, [field]: arr } } : n
            ));
          }
          setDirty(true);
        }
        function toggleMode(field: 'input_modes' | 'output_modes', mode: string) {
          const current = (d[field] ?? []) as string[];
          const next = current.includes(mode) ? current.filter(m => m !== mode) : [...current, mode];
          updateSkillArray(field, next.length ? next : [mode]);
        }
        return (
          <>
            <label style={labelStyle}>Skill ID <span style={{ fontWeight: 400, color: '#475569' }}>(auto-generated)</span></label>
            <div style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: '10px', color: '#475569', userSelect: 'all', cursor: 'text', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {d.skill_id}
            </div>
            <div style={fieldGap}>
              <label style={labelStyle}>Name</label>
              <input value={d.name} onChange={e => updateSelectedNodeField('name', e.target.value)} style={inputStyle} />
            </div>
            <div style={fieldGap}>
              <label style={labelStyle}>Description</label>
              <textarea value={d.description} onChange={e => updateSelectedNodeField('description', e.target.value)} rows={2} style={textareaStyle} />
            </div>
            <div style={fieldGap}>
              <label style={labelStyle}>Input Modes</label>
              {MODES.map(m => (
                <label key={m} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, cursor: 'pointer', fontSize: '12px', color: '#ccc' }}>
                  <input type="checkbox" checked={(d.input_modes ?? []).includes(m)} onChange={() => toggleMode('input_modes', m)} style={{ accentColor: C.cyan }} />
                  {m}
                </label>
              ))}
            </div>
            <div style={fieldGap}>
              <label style={labelStyle}>Output Modes</label>
              {MODES.map(m => (
                <label key={m} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, cursor: 'pointer', fontSize: '12px', color: '#ccc' }}>
                  <input type="checkbox" checked={(d.output_modes ?? []).includes(m)} onChange={() => toggleMode('output_modes', m)} style={{ accentColor: C.purple }} />
                  {m}
                </label>
              ))}
            </div>
            <div style={fieldGap}>
              <label style={labelStyle}>Tags <span style={hint}>comma-separated</span></label>
              <input
                value={(d.tags ?? []).join(', ')}
                onChange={e => updateSkillArray('tags', e.target.value.split(',').map(t => t.trim()).filter(Boolean))}
                style={inputStyle}
                placeholder="search, nlp, ..."
              />
            </div>
            <div style={fieldGap}>
              <label style={labelStyle}>Examples</label>
              {(d.examples ?? []).map((ex, i) => (
                <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 4 }}>
                  <input
                    value={ex}
                    onChange={e => {
                      const next = [...(d.examples ?? [])];
                      next[i] = e.target.value;
                      updateSkillArray('examples', next);
                    }}
                    style={{ ...inputStyle, flex: 1, fontSize: '12px' }}
                    placeholder="e.g. Summarize this article"
                  />
                  <button
                    onClick={() => updateSkillArray('examples', (d.examples ?? []).filter((_, j) => j !== i))}
                    style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 4px' }}
                  >×</button>
                </div>
              ))}
              <button
                onClick={() => updateSkillArray('examples', [...(d.examples ?? []), ''])}
                style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}
              >+ Add example</button>
            </div>
            <button onClick={() => {
              savePipelineState();
              setActiveSkillId(d.skill_id);
              setActiveView('skill');
              setSelectedNode(null);
            }} style={{
              marginTop: '16px', width: '100%', background: C.purpleBg,
              border: `1px solid ${C.purpleBorder}`, color: C.purple,
              padding: '8px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px', fontWeight: 600,
            }}>
              Edit Pipeline
            </button>
          </>
        );
      })()}

      {selectedNode.type === 'step' && (() => {
        const d = (localPipeNodes.find(n => n.id === selectedNode.id)?.data ?? selectedNode.data) as unknown as StepData;
        const cfg = d.config ?? {};

        const nodeIssues = validationIssues.filter(iss => iss.node_id === d.step_id);
        const fieldIssues: Record<string, 'error' | 'warning'> = {};
        for (const iss of nodeIssues) {
          if (!iss.field) continue;
          if (!fieldIssues[iss.field] || (iss.severity === 'error' && fieldIssues[iss.field] === 'warning')) {
            fieldIssues[iss.field] = iss.severity;
          }
        }

        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        function issueStyle(field: string): React.CSSProperties {
          const sev = fieldIssues[field];
          if (!sev) return {};
          return {
            borderColor: sev === 'error' ? '#f87171' : '#f59e0b',
            boxShadow: sev === 'error' ? '0 0 0 1px rgba(248,113,113,0.4)' : '0 0 0 1px rgba(245,158,11,0.4)',
          };
        }

        function cfgStr(key: string): string { return (cfg[key] as string) ?? ''; }
        function cfgNum(key: string, def = 0): number { return (cfg[key] as number) ?? def; }

        return (
          <>
            {nodeIssues.length > 0 && (
              <div style={{ marginBottom: '12px', padding: '8px 10px', borderRadius: '6px', background: nodeIssues.some(i => i.severity === 'error') ? 'rgba(248,113,113,0.08)' : 'rgba(245,158,11,0.08)', border: `1px solid ${nodeIssues.some(i => i.severity === 'error') ? 'rgba(248,113,113,0.3)' : 'rgba(245,158,11,0.3)'}` }}>
                {nodeIssues.map((iss, i) => (
                  <div key={i} style={{ fontSize: '11px', color: iss.severity === 'error' ? '#f87171' : '#f59e0b', display: 'flex', gap: '6px', marginBottom: i < nodeIssues.length - 1 ? '4px' : 0 }}>
                    <span style={{ flexShrink: 0 }}>{iss.severity === 'error' ? '✗' : '⚠'}</span>
                    <span>{iss.message}{iss.field && <span style={{ color: '#64748b' }}> · <code style={{ color: iss.severity === 'error' ? '#f87171' : '#f59e0b' }}>{iss.field}</code></span>}</span>
                  </div>
                ))}
              </div>
            )}

            <div style={{ marginBottom: '2px' }}>
              <label style={labelStyle}>Label</label>
              <input value={d.label} onChange={e => updateSelectedNodeField('label', e.target.value)} style={inputStyle} />
            </div>
            <div style={fieldGap}>
              <label style={labelStyle}>Step ID <span style={{ fontWeight: 400, color: '#475569' }}>(auto-generated)</span></label>
              <div style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: '10px', color: '#475569', userSelect: 'all', cursor: 'text', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {d.step_id}
              </div>
            </div>
            <div style={{ ...fieldGap, marginBottom: '16px' }}>
              <label style={labelStyle}>Type</label>
              <div style={{ ...inputStyle, color: C.textMuted, cursor: 'default' }}>{d.step_type}</div>
            </div>

            {(() => {
              const thisNode = localPipeNodes.find(n => n.id === selectedNode.id);
              if (!thisNode) return null;
              const { reads, writes } = extractNodeVars(thisNode);
              const inEdges = localPipeEdges.filter(e => e.target === selectedNode.id);
              const outEdges = localPipeEdges.filter(e => e.source === selectedNode.id);

              // Build a map: var name → source node that writes it
              const varSource: Record<string, { label: string; step_type: string }> = {};
              for (const e of inEdges) {
                const src = localPipeNodes.find(n => n.id === e.source);
                if (!src) continue;
                const { writes: srcWrites } = extractNodeVars(src);
                const srcData = src.data as unknown as StepData;
                for (const v of srcWrites) {
                  varSource[v] = { label: srcData.label || stepMeta(srcData.step_type).label, step_type: srcData.step_type };
                }
              }

              return (
                <div style={{ marginBottom: '16px', display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {/* INPUTS */}
                  <div>
                    <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: C.cyan, marginBottom: '6px' }}>
                      READS {reads.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400 }}>— no variables consumed</span>}
                    </div>
                    {reads.map(v => {
                      const src = varSource[v];
                      const missing = !src && inEdges.length > 0;
                      return (
                        <div key={v} style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '5px 8px', borderRadius: '6px', marginBottom: '4px', background: missing ? 'rgba(248,113,113,0.06)' : 'rgba(0,240,255,0.05)', border: `1px solid ${missing ? 'rgba(248,113,113,0.3)' : 'rgba(0,240,255,0.15)'}` }}>
                          <code style={{ color: missing ? '#f87171' : C.cyan, fontSize: '11px', fontFamily: 'monospace', flexShrink: 0 }}>{`{{.${v}}}`}</code>
                          {src && <><span style={{ color: C.textMuted, fontSize: '10px' }}>from</span><span style={{ color: '#94a3b8', fontSize: '10px' }}>{stepMeta(src.step_type).emoji} {src.label}</span></>}
                          {missing && <span style={{ color: '#f87171', fontSize: '10px' }}>— not connected</span>}
                        </div>
                      );
                    })}
                    {reads.length === 0 && inEdges.length === 0 && d.step_type !== 'input' && (
                      <div style={{ fontSize: '11px', color: C.textMuted }}>Nothing connected yet.</div>
                    )}
                  </div>

                  {/* OUTPUTS */}
                  <div>
                    <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: '#a78bfa', marginBottom: '6px' }}>
                      WRITES {writes.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400 }}>— no variables produced</span>}
                    </div>
                    {writes.map(v => {
                      const consumed = outEdges.some(e => {
                        const tgt = localPipeNodes.find(n => n.id === e.target);
                        if (!tgt) return false;
                        const { reads: tgtReads } = extractNodeVars(tgt);
                        return tgtReads.includes(v) || tgtReads.length === 0;
                      });
                      return (
                        <div key={v} style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '5px 8px', borderRadius: '6px', marginBottom: '4px', background: 'rgba(167,139,250,0.05)', border: '1px solid rgba(167,139,250,0.2)' }}>
                          <code style={{ color: '#a78bfa', fontSize: '11px', fontFamily: 'monospace', flexShrink: 0 }}>{`{{.${v}}}`}</code>
                          {outEdges.length > 0 && !consumed && <span style={{ color: C.textMuted, fontSize: '10px' }}>— not read downstream</span>}
                        </div>
                      );
                    })}
                  </div>
                </div>
              );
            })()}

            {d.step_type === 'input' && (
              <>
                <div style={{ color: C.cyan, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>INPUT CONFIG</div>
                <label style={labelStyle}>Bind text input to variable</label>
                <input
                  value={cfgStr('text_var') || ((cfg.bindings as Record<string,string>)?.text ?? '')}
                  onChange={e => updateStepConfig('bindings', { text: e.target.value })}
                  style={inputStyle}
                  placeholder="e.g. user_query"
                />
                <div style={{ marginTop: 6, fontSize: 11, color: '#64748b' }}>
                  The caller's message text will be available as <code style={{ color: C.cyan }}>{'{{.' + (cfgStr('text_var') || ((cfg.bindings as Record<string,string>)?.text) || 'user_query') + '}}'}</code> in downstream steps.
                </div>
              </>
            )}

            {d.step_type === 'llm' && (() => {
              const MCP_ACCENT = '#818cf8';
              const MCP_BG = 'rgba(129,140,248,0.06)';
              const MCP_BORDER = 'rgba(129,140,248,0.25)';

              // mcp_servers config: array of { slug, tools: string[] }
              type MCPAttachment = { slug: string; tools: string[] };
              const mcpAttachments: MCPAttachment[] = (cfg.mcp_servers as MCPAttachment[] | undefined) ?? [];

              function setMCPAttachments(next: MCPAttachment[]) {
                updateStepConfig('mcp_servers', next);
              }
              function addMCPServer(slug: string) {
                if (!slug || mcpAttachments.some(a => a.slug === slug)) return;
                setMCPAttachments([...mcpAttachments, { slug, tools: [] }]);
              }
              function removeMCPServer(slug: string) {
                setMCPAttachments(mcpAttachments.filter(a => a.slug !== slug));
              }
              function toggleTool(slug: string, tool: string) {
                setMCPAttachments(mcpAttachments.map(a => {
                  if (a.slug !== slug) return a;
                  const has = a.tools.includes(tool);
                  return { ...a, tools: has ? a.tools.filter(t => t !== tool) : [...a.tools, tool] };
                }));
              }

              const attachedSlugs = new Set(mcpAttachments.map(a => a.slug));
              const availableToAdd = mcpServers.filter(s => !attachedSlugs.has(s.slug));

              return (
                <>
                  <div style={{ color: C.purple, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>LLM CONFIG</div>
                  <label style={labelStyle}>Model</label>
                  <select value={cfgStr('model') || 'claude-haiku-4-5-20251001'} onChange={e => updateStepConfig('model', e.target.value)} style={selectStyle}>
                    {LLM_MODELS.anthropic.map(m => (<option key={m} value={m}>{m}</option>))}
                  </select>
                  <div style={fieldGap}>
                    <label style={labelStyle}>Max Tokens</label>
                    <input type="number" min={1} max={32000} value={cfgNum('max_tokens', 4096)} onChange={e => updateStepConfig('max_tokens', parseInt(e.target.value) || 4096)} style={inputStyle} />
                  </div>
                  <div style={fieldGap}>
                    <label style={labelStyle}>System Prompt</label>
                    <textarea rows={4} value={cfgStr('system_prompt')} onChange={e => updateStepConfig('system_prompt', e.target.value)} style={textareaStyle} placeholder="You are a helpful assistant..." />
                  </div>
                  <div style={fieldGap}>
                    <label style={labelStyle}>User Prompt <span style={hint}>Go template · leave blank to pass caller input directly</span></label>
                    <textarea rows={3} value={cfgStr('user_prompt')} onChange={e => updateStepConfig('user_prompt', e.target.value)} style={textareaStyle} placeholder={'{{.user_query}}'} />
                  </div>
                  <div style={fieldGap}>
                    <label style={labelStyle}>Output Variable <span style={hint}>default: output</span></label>
                    <input value={cfgStr('output_var')} onChange={e => updateStepConfig('output_var', e.target.value)} style={inputStyle} placeholder="output" />
                  </div>

                  {/* ── MCP Servers ─────────────────────────────────────────── */}
                  <div style={{ marginTop: 20, borderTop: `1px solid rgba(255,255,255,0.07)`, paddingTop: 14 }}>
                    <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: MCP_ACCENT, marginBottom: 8 }}>
                      MCP SERVERS
                      <span style={{ marginLeft: 6, fontWeight: 400, color: '#475569', textTransform: 'none', letterSpacing: 0 }}>
                        — tools the LLM can call during reasoning
                      </span>
                    </div>

                    {/* Attached servers */}
                    {mcpAttachments.map(att => {
                      const srv = mcpServers.find(s => s.slug === att.slug);
                      const manifest: MCPTool[] = srv?.tools_manifest ?? [];
                      return (
                        <div key={att.slug} style={{ marginBottom: 8, borderRadius: 8, border: `1px solid ${MCP_BORDER}`, background: MCP_BG, overflow: 'hidden' }}>
                          {/* Server header */}
                          <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 10px' }}>
                            <span style={{ fontSize: 13 }}>🔌</span>
                            <span style={{ flex: 1, fontSize: 12, fontWeight: 600, color: MCP_ACCENT }}>{srv?.name ?? att.slug}</span>
                            {att.tools.length > 0 && (
                              <span style={{ fontSize: 10, color: '#64748b' }}>{att.tools.length} tool{att.tools.length !== 1 ? 's' : ''} selected</span>
                            )}
                            <button onClick={() => removeMCPServer(att.slug)} style={{ background: 'none', border: 'none', color: '#64748b', cursor: 'pointer', fontSize: 14, padding: '0 2px', lineHeight: 1 }} title="Remove">×</button>
                          </div>

                          {/* Tool filter — only shown when server has been probed */}
                          {manifest.length > 0 && (
                            <details style={{ borderTop: `1px solid ${MCP_BORDER}` }}>
                              <summary style={{ padding: '5px 10px', fontSize: 10, color: '#64748b', cursor: 'pointer', userSelect: 'none', listStyle: 'none' }}>
                                {att.tools.length === 0
                                  ? `▸ All ${manifest.length} tools visible · restrict?`
                                  : `▾ ${att.tools.length} of ${manifest.length} tools selected`
                                }
                              </summary>
                              <div style={{ padding: '6px 10px 8px', display: 'flex', flexDirection: 'column', gap: 4 }}>
                                {manifest.map(t => (
                                  <label key={t.name} style={{ display: 'flex', alignItems: 'flex-start', gap: 7, cursor: 'pointer' }}>
                                    <input
                                      type="checkbox"
                                      checked={att.tools.length === 0 || att.tools.includes(t.name)}
                                      onChange={() => {
                                        // If currently "all", clicking one tool means "restrict to all except this one"
                                        if (att.tools.length === 0) {
                                          const allExcept = manifest.map(m => m.name).filter(n => n !== t.name);
                                          setMCPAttachments(mcpAttachments.map(a => a.slug === att.slug ? { ...a, tools: allExcept } : a));
                                        } else {
                                          toggleTool(att.slug, t.name);
                                        }
                                      }}
                                      style={{ accentColor: MCP_ACCENT, marginTop: 2, flexShrink: 0 }}
                                    />
                                    <div>
                                      <div style={{ fontSize: 11, fontWeight: 600, color: '#e2e8f0', fontFamily: 'monospace' }}>{t.name}</div>
                                      {t.description && <div style={{ fontSize: 10, color: '#64748b', lineHeight: 1.4 }}>{t.description}</div>}
                                    </div>
                                  </label>
                                ))}
                                {att.tools.length > 0 && att.tools.length < manifest.length && (
                                  <button onClick={() => setMCPAttachments(mcpAttachments.map(a => a.slug === att.slug ? { ...a, tools: [] } : a))} style={{ marginTop: 4, background: 'none', border: `1px dashed ${MCP_BORDER}`, color: '#64748b', padding: '3px 8px', borderRadius: 4, cursor: 'pointer', fontSize: 10 }}>
                                    Reset — show all tools
                                  </button>
                                )}
                              </div>
                            </details>
                          )}
                          {manifest.length === 0 && (
                            <div style={{ padding: '4px 10px 7px', fontSize: 10, color: '#475569' }}>
                              Probe this server in MCP Store to see its tools
                            </div>
                          )}
                        </div>
                      );
                    })}

                    {/* Add server dropdown */}
                    {availableToAdd.length > 0 && (
                      <select
                        value=""
                        onChange={e => { addMCPServer(e.target.value); e.target.value = ''; }}
                        style={{ ...selectStyle, borderColor: MCP_BORDER, color: '#64748b', marginTop: mcpAttachments.length ? 4 : 0 }}
                      >
                        <option value="">+ Attach MCP server…</option>
                        {availableToAdd.map(s => (
                          <option key={s.id} value={s.slug}>{s.name} ({s.slug})</option>
                        ))}
                      </select>
                    )}
                    {mcpServers.length === 0 && (
                      <div style={{ fontSize: 11, color: '#475569', padding: '6px 0' }}>
                        No MCP servers registered — add servers in <strong style={{ color: MCP_ACCENT }}>MCP Store</strong> first.
                      </div>
                    )}
                  </div>
                </>
              );
            })()}

            {d.step_type === 'http' && (
              <>
                <div style={{ color: C.amber, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>HTTP CONFIG</div>
                <label style={labelStyle}>Method</label>
                <select value={cfgStr('method') || 'GET'} onChange={e => updateStepConfig('method', e.target.value)} style={selectStyle}>
                  {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => (<option key={m} value={m}>{m}</option>))}
                </select>
                <div style={fieldGap}>
                  <label style={labelStyle}>URL <span style={hint}>Go template</span></label>
                  <input value={cfgStr('url_template')} onChange={e => updateStepConfig('url_template', e.target.value)} style={inputStyle} placeholder="https://api.example.com/{{.resource}}" />
                </div>
                <div style={fieldGap}>
                  <label style={labelStyle}>Body Template <span style={hint}>Go template · optional</span></label>
                  <textarea rows={3} value={cfgStr('body_template')} onChange={e => updateStepConfig('body_template', e.target.value)} style={textareaStyle} placeholder={'{"query": "{{.user_query}}"}'} />
                </div>
                <div style={fieldGap}>
                  <label style={labelStyle}>Timeout (seconds)</label>
                  <input type="number" min={1} max={300} value={cfgNum('timeout_seconds', 30)} onChange={e => updateStepConfig('timeout_seconds', parseInt(e.target.value) || 30)} style={inputStyle} />
                </div>
                {(() => {
                  const httpDef = getNodeDef('http');
                  const appParams = httpDef.app_params ?? [];
                  // Always render the APP AUTH PARAM section for http nodes —
                  // if node-types haven't loaded yet, show a placeholder so the
                  // user sees the field exists. nodeTypesReady triggers a re-render
                  // once the fetch completes and populates _byType.
                  const currentParamKey = cfgStr('app_param_key');
                  const currentParamRef = cfgStr('app_param_ref');
                  const currentMode = cfgStr('inject_mode') || 'header';
                  // Source mode: 'global' = app-global param ref, 'binding' = per-binding agent param key
                  const sourceMode = currentParamRef ? 'global' : 'binding';
                  return (
                    <div style={{ ...fieldGap, marginTop: '16px', padding: '10px 12px', borderRadius: '6px', background: 'rgba(251,146,60,0.05)', border: '1px solid rgba(251,146,60,0.15)' }}>
                      <div style={{ fontSize: '10px', fontWeight: 700, color: C.amber, letterSpacing: '0.08em', marginBottom: '8px' }}>APP AUTH PARAM</div>

                      {/* Source mode toggle */}
                      <div style={{ display: 'flex', gap: 4, marginBottom: 8 }}>
                        <button
                          onClick={() => { updateStepConfig('app_param_ref', ''); }}
                          style={{ flex: 1, padding: '4px 0', borderRadius: 4, border: `1px solid ${sourceMode === 'binding' ? C.amber : 'rgba(251,146,60,0.3)'}`, background: sourceMode === 'binding' ? 'rgba(251,146,60,0.15)' : 'transparent', color: sourceMode === 'binding' ? C.amber : '#64748b', cursor: 'pointer', fontSize: 10, fontWeight: 700 }}
                        >
                          Per-binding param
                        </button>
                        <button
                          onClick={() => { updateStepConfig('app_param_key', ''); }}
                          style={{ flex: 1, padding: '4px 0', borderRadius: 4, border: `1px solid ${sourceMode === 'global' ? C.amber : 'rgba(251,146,60,0.3)'}`, background: sourceMode === 'global' ? 'rgba(251,146,60,0.15)' : 'transparent', color: sourceMode === 'global' ? C.amber : '#64748b', cursor: 'pointer', fontSize: 10, fontWeight: 700 }}
                        >
                          App global param
                        </button>
                      </div>

                      {sourceMode === 'binding' ? (
                        <>
                          <label style={labelStyle}>App Param Key <span style={hint}>optional — injects runtime param as auth</span></label>
                          <select value={currentParamKey} onChange={e => updateStepConfig('app_param_key', e.target.value)} style={selectStyle}>
                            <option value="">— none —</option>
                            {appParams.map(p => (<option key={p.key} value={p.key}>{p.label} ({p.key})</option>))}
                            {currentParamKey && !appParams.find(p => p.key === currentParamKey) && (
                              <option value={currentParamKey}>{currentParamKey}</option>
                            )}
                          </select>
                          {currentParamKey && (
                            <>
                              <div style={fieldGap}>
                                <label style={labelStyle}>Inject Mode</label>
                                <select value={currentMode} onChange={e => updateStepConfig('inject_mode', e.target.value)} style={selectStyle}>
                                  <option value="header">Bearer (Authorization: Bearer)</option>
                                  <option value="basic">Basic Auth (Authorization: Basic)</option>
                                  <option value="query">Query Parameter</option>
                                  <option value="custom_header">Custom Header</option>
                                </select>
                              </div>
                              {currentMode === 'custom_header' && (
                                <div style={fieldGap}>
                                  <label style={labelStyle}>Header Name</label>
                                  <input value={cfgStr('inject_header_name')} onChange={e => updateStepConfig('inject_header_name', e.target.value)} style={inputStyle} placeholder="X-Api-Key" />
                                </div>
                              )}
                              {currentMode === 'query' && (
                                <div style={{ marginTop: 4, fontSize: 11, color: '#64748b' }}>
                                  The param key name will be used as the query parameter name.
                                </div>
                              )}
                            </>
                          )}
                        </>
                      ) : (
                        <>
                          <label style={labelStyle}>Global Param Name <span style={hint}>name from App Global Parameters</span></label>
                          <input
                            value={currentParamRef}
                            onChange={e => updateStepConfig('app_param_ref', e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
                            style={inputStyle}
                            placeholder="e.g. my_api_key"
                          />
                          {currentParamRef && (
                            <>
                              <div style={fieldGap}>
                                <label style={labelStyle}>Inject Mode</label>
                                <select value={currentMode} onChange={e => updateStepConfig('inject_mode', e.target.value)} style={selectStyle}>
                                  <option value="header">Bearer (Authorization: Bearer)</option>
                                  <option value="basic">Basic Auth (Authorization: Basic)</option>
                                  <option value="query">Query Parameter</option>
                                  <option value="custom_header">Custom Header</option>
                                </select>
                              </div>
                              {currentMode === 'custom_header' && (
                                <div style={fieldGap}>
                                  <label style={labelStyle}>Header Name</label>
                                  <input value={cfgStr('inject_header_name')} onChange={e => updateStepConfig('inject_header_name', e.target.value)} style={inputStyle} placeholder="X-Api-Key" />
                                </div>
                              )}
                              {currentMode === 'query' && (
                                <div style={{ marginTop: 4, fontSize: 11, color: '#64748b' }}>
                                  The param key name will be used as the query parameter name.
                                </div>
                              )}
                            </>
                          )}
                          <div style={{ marginTop: 6, fontSize: 10, color: '#64748b' }}>
                            Takes precedence over per-binding param. Param must exist under App Global Parameters in the app runtime settings.
                          </div>
                        </>
                      )}
                    </div>
                  );
                })()}
                <div style={{ ...fieldGap, marginTop: '16px' }}>
                  <label style={labelStyle}>Response Extractions <span style={hint}>JSONPath → variable</span></label>
                  {((cfg.extractions as {var: string; json_path: string}[]) ?? []).map((ex, i) => (
                    <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 4 }}>
                      <input value={ex.json_path} onChange={e => { const next = [...((cfg.extractions as {var: string; json_path: string}[]) ?? [])]; next[i] = { ...next[i], json_path: e.target.value }; updateStepConfig('extractions', next); }} style={{ ...inputStyle, flex: 1, fontSize: '11px' }} placeholder="$.result" />
                      <input value={ex.var} onChange={e => { const next = [...((cfg.extractions as {var: string; json_path: string}[]) ?? [])]; next[i] = { ...next[i], var: e.target.value }; updateStepConfig('extractions', next); }} style={{ ...inputStyle, flex: 1, fontSize: '11px' }} placeholder="var_name" />
                      <button onClick={() => { const next = ((cfg.extractions as {var: string; json_path: string}[]) ?? []).filter((_, j) => j !== i); updateStepConfig('extractions', next); }} style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 4px' }}>×</button>
                    </div>
                  ))}
                  <button onClick={() => { const next = [...((cfg.extractions as {var: string; json_path: string}[]) ?? []), { json_path: '$.', var: '' }]; updateStepConfig('extractions', next); }} style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}>+ Add extraction</button>
                </div>
              </>
            )}

            {d.step_type === 'transform' && (() => {
              // Collect vars visible at this node: 'input' always present +
              // output_var from every upstream step (steps that have an edge into this one, recursively).
              const thisNodeId = selectedNode.id;
              const upstreamVars: string[] = ['input'];
              const visited = new Set<string>();
              const queue = [thisNodeId];
              while (queue.length) {
                const curr = queue.shift()!;
                if (visited.has(curr)) continue;
                visited.add(curr);
                for (const edge of localPipeEdges) {
                  if (edge.target === curr) {
                    const srcNode = localPipeNodes.find(n => n.id === edge.source);
                    if (srcNode) {
                      const srcCfg = (srcNode.data as unknown as { config?: Record<string, unknown> }).config ?? {};
                      if (srcCfg.output_var) upstreamVars.push(String(srcCfg.output_var));
                      if (srcCfg.var) upstreamVars.push(String(srcCfg.var));
                      queue.push(edge.source);
                    }
                  }
                }
              }
              const availableVars = Array.from(new Set(upstreamVars));
              return (
                <TransformPanel
                  cfg={cfg}
                  updateStepConfig={updateStepConfig}
                  availableVars={availableVars}
                />
              );
            })()}

            {d.step_type === 'response' && (
              <>
                <div style={{ color: C.cyan, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>RESPONSE CONFIG</div>
                <label style={labelStyle}>From Variable <span style={hint}>pipeline var to return</span></label>
                <input value={cfgStr('from_var') || 'output'} onChange={e => updateStepConfig('from_var', e.target.value)} style={inputStyle} placeholder="output" />
                <div style={fieldGap}>
                  <label style={labelStyle}>Media Type</label>
                  <select value={cfgStr('media_type') || 'text/plain'} onChange={e => updateStepConfig('media_type', e.target.value)} style={selectStyle}>
                    <option value="text/plain">text/plain</option>
                    <option value="text/html">text/html</option>
                    <option value="text/markdown">text/markdown</option>
                    <option value="application/json">application/json</option>
                  </select>
                </div>
              </>
            )}

            {d.step_type === 'branch' && (
              <>
                <div style={{ color: '#f97316', fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>BRANCH CONFIG</div>
                <div style={fieldGap}>
                  <label style={labelStyle}>Expression <span style={hint}>Go template</span></label>
                  <input value={cfgStr('expression')} onChange={e => updateStepConfig('expression', e.target.value)} style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: '12px' }} placeholder={'{{eq .status "ok"}}'} />
                  <div style={{ marginTop: 4, fontSize: 11, color: '#64748b' }}>
                    Examples: <code style={{ color: '#94a3b8' }}>{'{{eq .x "yes"}}'}</code> · <code style={{ color: '#94a3b8' }}>{'{{gt .count 0}}'}</code> · <code style={{ color: '#94a3b8' }}>{'{{.my_var}}'}</code>
                  </div>
                </div>
                <div style={{ marginTop: 12, padding: '10px', background: 'rgba(249,115,22,0.06)', border: '1px solid rgba(249,115,22,0.25)', borderRadius: 6, fontSize: 11, color: '#94a3b8', lineHeight: 1.7 }}>
                  <strong style={{ color: '#f97316' }}>Exactly 2 output edges required:</strong><br />
                  <span style={{ color: '#4ade80' }}>1st edge drawn</span> → taken when expression is <strong>truthy</strong><br />
                  <span style={{ color: '#f87171' }}>2nd edge drawn</span> → taken when expression is <strong>falsy</strong><br />
                  <span style={{ color: '#64748b', fontSize: 10 }}>Falsy values: empty string, <code style={{ color: '#94a3b8' }}>false</code>, <code style={{ color: '#94a3b8' }}>0</code>. Everything else is truthy.</span>
                </div>
              </>
            )}

            {!['input', 'llm', 'http', 'transform', 'response', 'branch'].includes(d.step_type) && (
              <div style={{ color: '#64748b', fontSize: '12px', padding: '12px', border: `1px dashed ${C.outline}`, borderRadius: '6px', textAlign: 'center' }}>
                Config for <strong style={{ color: C.text }}>{d.step_type}</strong> is not yet supported in the builder.
              </div>
            )}

            {d.step_type !== 'response' && (() => {
              const outVar = d.step_type === 'input'
                ? ((d.config?.bindings as Record<string,string>)?.text || 'input')
                : ((d.config?.output_var as string) || 'output');
              const outEdges = localPipeEdges.filter(e => e.source === selectedNode.id);
              return (
                <div style={{ marginTop: '16px' }}>
                  <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: C.green, marginBottom: '6px' }}>
                    OUTPUTS {outEdges.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400 }}>— nothing connected</span>}
                  </div>
                  {outEdges.map(e => {
                    const tgt = localPipeNodes.find(n => n.id === e.target);
                    const tgtData = tgt?.data as unknown as StepData | undefined;
                    const tgtMeta = tgtData ? stepMeta(tgtData.step_type) : { emoji: '?', label: 'unknown' };
                    return (
                      <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '6px 8px', borderRadius: '6px', marginBottom: '4px', background: 'rgba(74,222,128,0.05)', border: '1px solid rgba(74,222,128,0.2)' }}>
                        <code style={{ color: C.green, fontSize: '11px', fontFamily: 'monospace' }}>{`{{${outVar}}}`}</code>
                        <span style={{ color: C.textMuted, fontSize: '11px' }}>→</span>
                        <span style={{ fontSize: '14px' }}>{tgtMeta.emoji}</span>
                        <span style={{ color: '#e2e8f0', fontSize: '11px', fontWeight: 600 }}>{tgtData?.label || tgtMeta.label}</span>
                      </div>
                    );
                  })}
                </div>
              );
            })()}

            {debug.active && (() => {
              const nodeDebugState = debug.nodeStates[selectedNode.id];
              const nodeOutput = debug.nodeOutputs[selectedNode.id];
              const nodeError = debug.nodeErrors[selectedNode.id];
              const nodeInputVars = debug.nodeInputVars[selectedNode.id];

              if (debug.mode === 'step' && nodeDebugState === 'pending') {
                const inEdges = localPipeEdges.filter(e => e.target === selectedNode.id);
                const pendingVars = inEdges.map(e => {
                  const src = localPipeNodes.find(n => n.id === e.source);
                  const srcData = src?.data as unknown as StepData | undefined;
                  const varName = srcData?.step_type === 'input'
                    ? ((srcData?.config?.bindings as Record<string,string>)?.text || 'input')
                    : ((srcData?.config?.output_var as string) || 'output');
                  return { varName, currentVal: String(debug.vars[varName] ?? '') };
                });
                if (pendingVars.length > 0) {
                  return (
                    <div style={{ marginTop: '16px', padding: '10px', background: 'rgba(245,158,11,0.08)', border: `1px solid ${C.amberBorder}`, borderRadius: '8px' }}>
                      <div style={{ fontSize: '10px', fontWeight: 700, color: C.amber, marginBottom: '8px', letterSpacing: '0.08em' }}>
                        STEP OVERRIDE — edit values before step runs
                      </div>
                      {pendingVars.map(({ varName, currentVal }) => (
                        <div key={varName} style={{ marginBottom: '8px' }}>
                          <label style={{ ...labelStyle, color: C.amber }}>{`{{${varName}}}`}</label>
                          <textarea
                            rows={2}
                            value={debug.pendingVarOverrides[varName] ?? currentVal}
                            onChange={e => setDebug(prev => ({
                              ...prev,
                              pendingVarOverrides: { ...prev.pendingVarOverrides, [varName]: e.target.value },
                            }))}
                            style={{ ...textareaStyle, borderColor: C.amberBorder, fontSize: '11px' }}
                          />
                        </div>
                      ))}
                      <button onClick={debugStep} style={{
                        width: '100%', background: 'rgba(96,165,250,0.1)', border: `1px solid rgba(96,165,250,0.4)`,
                        color: '#60a5fa', padding: '6px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px', fontWeight: 600,
                      }}>
                        ⏭ Execute this step
                      </button>
                    </div>
                  );
                }
              }

              if (nodeDebugState === 'done' && nodeOutput !== undefined) {
                return (
                  <div style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {nodeInputVars && Object.keys(nodeInputVars).length > 0 && (
                      <details style={{ background: 'rgba(96,165,250,0.06)', border: '1px solid rgba(96,165,250,0.25)', borderRadius: '8px', padding: '8px 10px' }}>
                        <summary style={{ fontSize: '10px', fontWeight: 700, color: '#60a5fa', letterSpacing: '0.08em', cursor: 'pointer', userSelect: 'none' }}>
                          VARS IN ({Object.keys(nodeInputVars).length})
                        </summary>
                        <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          {Object.entries(nodeInputVars).map(([k, v]) => {
                            const str = typeof v === 'object' ? JSON.stringify(v, null, 2) : String(v);
                            const preview = str.length > 120 ? str.slice(0, 120) + '…' : str;
                            return (
                              <div key={k}>
                                <div style={{ fontSize: '10px', color: '#60a5fa', fontFamily: 'monospace', fontWeight: 600 }}>{`{{.${k}}}`}</div>
                                <pre style={{ color: '#94a3b8', fontSize: '10px', whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0, fontFamily: 'monospace' }}>{preview}</pre>
                              </div>
                            );
                          })}
                        </div>
                      </details>
                    )}
                    {(() => {
                      // Transform nodes produce many vars — show them individually
                      const exposedVars = (d.config?.exposed_vars as string[] | undefined) ?? [];
                      if (d.step_type === 'transform' && exposedVars.length > 0) {
                        return (
                          <div style={{ padding: '10px', background: 'rgba(74,222,128,0.06)', border: `1px solid rgba(74,222,128,0.3)`, borderRadius: '8px' }}>
                            <div style={{ fontSize: '10px', fontWeight: 700, color: C.green, marginBottom: '8px', letterSpacing: '0.08em' }}>
                              EXTRACTED VARS ({exposedVars.length})
                            </div>
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                              {exposedVars.map(v => {
                                const val = debug.vars[v];
                                const str = val === undefined ? '(missing)' : typeof val === 'object' ? JSON.stringify(val) : String(val);
                                const missing = val === undefined || str === '';
                                return (
                                  <div key={v} style={{ display: 'flex', gap: 6, alignItems: 'baseline' }}>
                                    <span style={{ fontFamily: 'monospace', fontSize: '10px', color: missing ? '#f87171' : '#60a5fa', flexShrink: 0, minWidth: 100 }}>{`{{.${v}}}`}</span>
                                    <span style={{ fontFamily: 'monospace', fontSize: '10px', color: missing ? '#f87171' : '#e2e8f0', wordBreak: 'break-all' }}>{str || '(empty)'}</span>
                                  </div>
                                );
                              })}
                            </div>
                          </div>
                        );
                      }
                      return (
                        <div style={{ padding: '10px', background: 'rgba(74,222,128,0.06)', border: `1px solid rgba(74,222,128,0.3)`, borderRadius: '8px' }}>
                          <div style={{ fontSize: '10px', fontWeight: 700, color: C.green, marginBottom: '6px', letterSpacing: '0.08em' }}>OUTPUT</div>
                          <pre style={{ color: '#e2e8f0', fontSize: '11px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontFamily: 'monospace' }}>
                            {nodeOutput || '(empty)'}
                          </pre>
                        </div>
                      );
                    })()}
                  </div>
                );
              }

              if (nodeDebugState === 'error' && nodeError) {
                return (
                  <div style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {nodeInputVars && Object.keys(nodeInputVars).length > 0 && (
                      <details style={{ background: 'rgba(96,165,250,0.06)', border: '1px solid rgba(96,165,250,0.25)', borderRadius: '8px', padding: '8px 10px' }}>
                        <summary style={{ fontSize: '10px', fontWeight: 700, color: '#60a5fa', letterSpacing: '0.08em', cursor: 'pointer', userSelect: 'none' }}>
                          VARS IN ({Object.keys(nodeInputVars).length})
                        </summary>
                        <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          {Object.entries(nodeInputVars).map(([k, v]) => {
                            const str = typeof v === 'object' ? JSON.stringify(v, null, 2) : String(v);
                            const preview = str.length > 120 ? str.slice(0, 120) + '…' : str;
                            return (
                              <div key={k}>
                                <div style={{ fontSize: '10px', color: '#60a5fa', fontFamily: 'monospace', fontWeight: 600 }}>{`{{.${k}}}`}</div>
                                <pre style={{ color: '#94a3b8', fontSize: '10px', whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0, fontFamily: 'monospace' }}>{preview}</pre>
                              </div>
                            );
                          })}
                        </div>
                      </details>
                    )}
                    <div style={{ padding: '10px', background: 'rgba(248,113,113,0.06)', border: `1px solid rgba(248,113,113,0.3)`, borderRadius: '8px' }}>
                      <div style={{ fontSize: '10px', fontWeight: 700, color: '#f87171', marginBottom: '6px', letterSpacing: '0.08em' }}>ERROR</div>
                      <pre style={{ color: '#f87171', fontSize: '11px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontFamily: 'monospace' }}>
                        {nodeError}
                      </pre>
                    </div>
                  </div>
                );
              }

              return null;
            })()}
          </>
        );
      })()}
    </div>
  );
}
