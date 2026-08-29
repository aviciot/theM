import { useState, useEffect } from 'react';
import type { Node, Edge } from '@xyflow/react';
import { getNodeDef } from '@/lib/nodeRegistry';
import type { MCPServer, MCPTool } from '@/lib/api';
import { themApi } from '@/lib/api';
import type { StepData } from '../../types';
import { C, labelStyle, inputStyle, textareaStyle, selectStyle, fieldGap, hint, LLM_MODELS } from '../../constants';
import { TransformPanel } from '../TransformPanel';

interface StepConfigSectionProps {
  selectedNode: Node;
  d: StepData;
  cfg: Record<string, unknown>;
  localPipeNodes: Node[];
  localPipeEdges: Edge[];
  updateStepConfig: (key: string, value: unknown) => void;
  nodeTypesReady?: boolean;
}

export function StepConfigSection({
  selectedNode, d, cfg, localPipeNodes, localPipeEdges, updateStepConfig, nodeTypesReady,
}: StepConfigSectionProps) {
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  useEffect(() => {
    themApi.listMCPServers().then(s => setMcpServers(s ?? [])).catch(() => {});
  }, []);

  function cfgStr(key: string): string { return (cfg[key] as string) ?? ''; }
  function cfgNum(key: string, def = 0): number { return (cfg[key] as number) ?? def; }

  // nodeTypesReady triggers re-render when node types load (http app_params)
  void nodeTypesReady;

  return (
    <>
      {d.step_type === 'input' && (
        <>
          <div style={{ color: C.cyan, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>INPUT CONFIG</div>
          <label style={labelStyle}>Bind text input to variable</label>
          <input
            value={cfgStr('text_var') || ((cfg.bindings as Record<string, string>)?.text ?? '')}
            onChange={e => updateStepConfig('bindings', { text: e.target.value })}
            style={inputStyle}
            placeholder="e.g. user_query"
          />
          <div style={{ marginTop: 6, fontSize: 11, color: '#64748b' }}>
            The caller's message text will be available as <code style={{ color: C.cyan }}>{'{{.' + (cfgStr('text_var') || ((cfg.bindings as Record<string, string>)?.text) || 'user_query') + '}}'}</code> in downstream steps.
          </div>
        </>
      )}

      {d.step_type === 'llm' && (() => {
        const MCP_ACCENT = '#818cf8';
        const MCP_BG = 'rgba(129,140,248,0.06)';
        const MCP_BORDER = 'rgba(129,140,248,0.25)';

        type MCPAttachment = { slug: string; tools: string[] };
        const mcpAttachments: MCPAttachment[] = (cfg.mcp_servers as MCPAttachment[] | undefined) ?? [];

        function setMCPAttachments(next: MCPAttachment[]) { updateStepConfig('mcp_servers', next); }
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

            <div style={{ marginTop: 20, borderTop: `1px solid rgba(255,255,255,0.07)`, paddingTop: 14 }}>
              <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: MCP_ACCENT, marginBottom: 8 }}>
                MCP SERVERS
                <span style={{ marginLeft: 6, fontWeight: 400, color: '#475569', textTransform: 'none', letterSpacing: 0 }}>
                  — tools the LLM can call during reasoning
                </span>
              </div>

              {mcpAttachments.map(att => {
                const srv = mcpServers.find(s => s.slug === att.slug);
                const manifest: MCPTool[] = srv?.tools_manifest ?? [];
                return (
                  <div key={att.slug} style={{ marginBottom: 8, borderRadius: 8, border: `1px solid ${MCP_BORDER}`, background: MCP_BG, overflow: 'hidden' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 10px' }}>
                      <span style={{ fontSize: 13 }}>🔌</span>
                      <span style={{ flex: 1, fontSize: 12, fontWeight: 600, color: MCP_ACCENT }}>{srv?.name ?? att.slug}</span>
                      {att.tools.length > 0 && (
                        <span style={{ fontSize: 10, color: '#64748b' }}>{att.tools.length} tool{att.tools.length !== 1 ? 's' : ''} selected</span>
                      )}
                      <button onClick={() => removeMCPServer(att.slug)} style={{ background: 'none', border: 'none', color: '#64748b', cursor: 'pointer', fontSize: 14, padding: '0 2px', lineHeight: 1 }} title="Remove">×</button>
                    </div>
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
            const currentParamKey = cfgStr('app_param_key');
            const currentParamRef = cfgStr('app_param_ref');
            const currentMode = cfgStr('inject_mode') || 'header';
            const sourceMode = currentParamRef ? 'global' : 'binding';
            return (
              <div style={{ ...fieldGap, marginTop: '16px', padding: '10px 12px', borderRadius: '6px', background: 'rgba(251,146,60,0.05)', border: '1px solid rgba(251,146,60,0.15)' }}>
                <div style={{ fontSize: '10px', fontWeight: 700, color: C.amber, letterSpacing: '0.08em', marginBottom: '8px' }}>APP AUTH PARAM</div>
                <div style={{ display: 'flex', gap: 4, marginBottom: 8 }}>
                  <button
                    onClick={() => { updateStepConfig('app_param_ref', ''); }}
                    style={{ flex: 1, padding: '4px 0', borderRadius: 4, border: `1px solid ${sourceMode === 'binding' ? C.amber : 'rgba(251,146,60,0.3)'}`, background: sourceMode === 'binding' ? 'rgba(251,146,60,0.15)' : 'transparent', color: sourceMode === 'binding' ? C.amber : '#64748b', cursor: 'pointer', fontSize: 10, fontWeight: 700 }}
                  >Per-binding param</button>
                  <button
                    onClick={() => { updateStepConfig('app_param_key', ''); }}
                    style={{ flex: 1, padding: '4px 0', borderRadius: 4, border: `1px solid ${sourceMode === 'global' ? C.amber : 'rgba(251,146,60,0.3)'}`, background: sourceMode === 'global' ? 'rgba(251,146,60,0.15)' : 'transparent', color: sourceMode === 'global' ? C.amber : '#64748b', cursor: 'pointer', fontSize: 10, fontWeight: 700 }}
                  >App global param</button>
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
            {((cfg.extractions as { var: string; json_path: string }[]) ?? []).map((ex, i) => (
              <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 4 }}>
                <input value={ex.json_path} onChange={e => { const next = [...((cfg.extractions as { var: string; json_path: string }[]) ?? [])]; next[i] = { ...next[i], json_path: e.target.value }; updateStepConfig('extractions', next); }} style={{ ...inputStyle, flex: 1, fontSize: '11px' }} placeholder="$.result" />
                <input value={ex.var} onChange={e => { const next = [...((cfg.extractions as { var: string; json_path: string }[]) ?? [])]; next[i] = { ...next[i], var: e.target.value }; updateStepConfig('extractions', next); }} style={{ ...inputStyle, flex: 1, fontSize: '11px' }} placeholder="var_name" />
                <button onClick={() => { const next = ((cfg.extractions as { var: string; json_path: string }[]) ?? []).filter((_, j) => j !== i); updateStepConfig('extractions', next); }} style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 4px' }}>×</button>
              </div>
            ))}
            <button onClick={() => { const next = [...((cfg.extractions as { var: string; json_path: string }[]) ?? []), { json_path: '$.', var: '' }]; updateStepConfig('extractions', next); }} style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}>+ Add extraction</button>
          </div>
        </>
      )}

      {d.step_type === 'transform' && (() => {
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
          <TransformPanel cfg={cfg} updateStepConfig={updateStepConfig} availableVars={availableVars} />
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
    </>
  );
}
