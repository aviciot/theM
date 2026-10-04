'use client';
import type { Node, Edge } from '@xyflow/react';
import type { InlineNodeData } from '../../../types';
import { C } from '../../../constants';
import { fieldStyle } from './panelShared';
import { InlinePortsSection } from './InlinePortsSection';
import { AgentGuardsSection } from './AgentGuardsSection';
import { WritesSection } from './WritesSection';
import { NodeVarsSection } from './NodeVarsSection';
import type { Agent, MiddlewareWiring } from '@/lib/api';

// ── InlineNodePanel (LLM + Condition) ────────────────────────────────────────

interface Props {
  appId: string;
  selectedNode: Node;
  nodes: Node[];
  edges: Edge[];
  agents: Agent[];
  wirings: MiddlewareWiring[];
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
  setLogoResult: (v: 'none' | 'valid' | 'invalid' | 'warn') => void;
  showToast: (msg: string, ok: boolean) => void;
  onWiringChanged?: () => void;
}

const CONDITION_EXAMPLES = [
  '{{eq .output "APPROVED"}}',
  '{{gt (len .output) 100}}',
  '{{contains .output "error"}}',
  // Guard var example (docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 2/3):
  // Phase 2 decided (b) — FlowVars stays flat, so the real template
  // reference is the flat key (nodeID_defSlug_status), not the dotted
  // display form shown in the Writes panel. Drag a var in from an
  // upstream node's Writes section for a real, correct reference — this
  // is a template to hand-edit, same as the 3 above (which node/guard it
  // is can't be known generically, since a condition reads whatever an
  // UPSTREAM node wrote, not its own vars).
  '{{eq .agent1_pii_redact_status "flagged"}}',
];

export function InlineNodePanel({
  appId, selectedNode, nodes, edges, agents, wirings, setNodes, setIsDirty, setLogoResult, showToast, onWiringChanged,
}: Props) {
  const liveNode = nodes.find(n => n.id === selectedNode.id);
  const d = (liveNode?.data ?? selectedNode.data) as unknown as InlineNodeData;
  const isLLM = d.node_type === 'llm';
  const isCondition = d.node_type === 'condition';
  const isHTTP = d.node_type === 'http';

  const cfg = (d.config ?? {}) as Record<string, unknown>;

  function updateNodeConfig(patch: Record<string, unknown>) {
    setNodes(ns => ns.map(n => {
      if (n.id !== selectedNode.id) return n;
      const nd = n.data as unknown as InlineNodeData;
      return { ...n, data: { ...nd, config: { ...(nd.config ?? {}), ...patch } } as unknown as Record<string, unknown> };
    }));
    setIsDirty(true);
    setLogoResult('none');
  }

  function updateDisplayName(name: string) {
    setNodes(ns => ns.map(n => n.id === selectedNode.id ? { ...n, data: { ...n.data, display_name: name } } : n));
    setIsDirty(true);
    setLogoResult('none');
  }

  const displayNameField = (
    <div>
      <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Display Name</label>
      <input
        style={fieldStyle}
        value={d.display_name ?? ''}
        onChange={e => updateDisplayName(e.target.value)}
      />
    </div>
  );

  if (isLLM) {
    const provider = (cfg.provider as string) ?? '';
    const model = (cfg.model as string) ?? '';
    const systemPrompt = (cfg.system_prompt as string) ?? '';
    const userPrompt = (cfg.user_prompt as string) ?? '';
    const outputVar = (cfg.output_var as string) ?? '';
    const maxTokens = (cfg.max_tokens as number) ?? '';
    const temperature = (cfg.temperature as number | null) ?? '';

    return (
      <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
        <div style={{ fontSize: 12, fontWeight: 700, color: '#d0bcff', textTransform: 'uppercase', letterSpacing: '0.06em' }}>Inline LLM</div>
        <div style={{ fontSize: 11, color: C.textMuted }}>
          Calls an LLM directly inside the flow — no registered agent required. Runs only on the Temporal backend.
        </div>

        {displayNameField}

        <InlinePortsSection selectedNode={selectedNode} nodes={nodes} edges={edges} setNodes={setNodes} wirings={wirings} />

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Provider / Model</label>
          <div style={{ ...fieldStyle, display: 'flex', alignItems: 'center', gap: 8, color: C.textMuted, cursor: 'default' }}>
            <span style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
              {provider || model ? `${provider || 'inherit'} / ${model || 'inherit'}` : 'inherit from entry point'}
            </span>
          </div>
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 4 }}>
            Configured in Runtime → this application's Runtime tab, not on the canvas. Changing it there takes effect immediately — no re-publish needed.
          </div>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>System Prompt</label>
          <textarea
            style={{ ...fieldStyle, minHeight: 80, resize: 'vertical', fontFamily: 'inherit' }}
            value={systemPrompt}
            onChange={e => updateNodeConfig({ system_prompt: e.target.value })}
          />
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>User Prompt</label>
          <textarea
            style={{ ...fieldStyle, minHeight: 80, resize: 'vertical', fontFamily: 'inherit' }}
            value={userPrompt}
            onChange={e => updateNodeConfig({ user_prompt: e.target.value })}
          />
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 4 }}>
            Use {'{{.input}}'} for the previous node's output, or {'{{.varname}}'} for a named variable
          </div>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Output Variable</label>
          <input
            style={fieldStyle}
            placeholder="output"
            value={outputVar}
            onChange={e => updateNodeConfig({ output_var: e.target.value })}
          />
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Max Tokens</label>
          <input
            type="number" min={0}
            style={fieldStyle}
            value={maxTokens}
            onChange={e => updateNodeConfig({ max_tokens: e.target.value === '' ? undefined : Number(e.target.value) })}
          />
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Temperature</label>
          <input
            type="number" min={0} max={2} step={0.1}
            style={fieldStyle}
            value={temperature}
            onChange={e => updateNodeConfig({ temperature: e.target.value === '' ? undefined : Number(e.target.value) })}
          />
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 4 }}>
            Stored but not yet applied — the shared LLM provider interface takes no options (Phase 2).
          </div>
        </div>

        <div style={{ borderTop: '1px solid rgba(255,255,255,0.08)', paddingTop: 12 }}>
          <WritesSection appId={appId} selectedNode={selectedNode} outputVar={outputVar || 'output'} wirings={wirings} />
        </div>

        <div style={{ borderTop: '1px solid rgba(255,255,255,0.08)', paddingTop: 12 }}>
          <AgentGuardsSection appId={appId} selectedNode={selectedNode} agents={agents} showToast={showToast} onWiringChanged={onWiringChanged} />
        </div>
      </div>
    );
  }

  if (isCondition) {
    const expression = (cfg.expression as string) ?? '';

    function branchTarget(branch: 'true' | 'false'): string {
      const edge = edges.find(e => e.source === selectedNode.id && e.sourceHandle === `ctrl-out-${branch}`);
      if (!edge) return 'not connected';
      const target = nodes.find(n => n.id === edge.target);
      if (!target) return 'not connected';
      const td = target.data as unknown as Record<string, unknown>;
      return (td.display_name as string) || (td.label as string) || target.id;
    }

    return (
      <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
        <div style={{ fontSize: 12, fontWeight: 700, color: '#f97316', textTransform: 'uppercase', letterSpacing: '0.06em' }}>Condition</div>
        <div style={{ fontSize: 11, color: C.textMuted }}>
          Deterministic 2-way branch. Evaluated in-workflow — no LLM call. Runs only on the Temporal backend.
        </div>

        {displayNameField}

        <InlinePortsSection selectedNode={selectedNode} nodes={nodes} edges={edges} setNodes={setNodes} wirings={wirings} />

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Expression</label>
          <textarea
            style={{ ...fieldStyle, minHeight: 70, resize: 'vertical', fontFamily: 'JetBrains Mono, monospace', fontSize: 13 }}
            value={expression}
            onChange={e => updateNodeConfig({ expression: e.target.value })}
          />
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 6, marginBottom: 4 }}>Examples (click to use):</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {CONDITION_EXAMPLES.map(ex => (
              <button
                key={ex}
                onClick={() => updateNodeConfig({ expression: ex })}
                style={{
                  textAlign: 'left', padding: '5px 8px', borderRadius: 6,
                  border: '1px solid rgba(249,115,22,0.25)', background: 'rgba(249,115,22,0.06)',
                  color: C.text, fontFamily: 'JetBrains Mono, monospace', fontSize: 11, cursor: 'pointer',
                }}
              >
                {ex}
              </button>
            ))}
          </div>
        </div>

        <div>
          <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 6, fontWeight: 600 }}>Branch routing</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
              <span style={{ color: '#4ade80', fontWeight: 700, minWidth: 40 }}>true</span>
              <span style={{ color: C.text }}>{branchTarget('true')}</span>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
              <span style={{ color: '#f87171', fontWeight: 700, minWidth: 40 }}>false</span>
              <span style={{ color: C.text }}>{branchTarget('false')}</span>
            </div>
          </div>
        </div>

        <NodeVarsSection
          reads={[
            { name: 'input', note: 'accumulated flow text' },
            { name: 'router_confidence', note: 'if upstream router — confidence 0.0–1.0' },
            { name: 'router_label', note: 'if upstream router — chosen label' },
          ]}
        />
      </div>
    );
  }

  if (isHTTP) {
    const method = (cfg.method as string) ?? 'GET';
    const urlTemplate = (cfg.url_template as string) ?? '';
    const bodyTemplate = (cfg.body_template as string) ?? '';
    const timeoutSeconds = (cfg.timeout_seconds as number) ?? 30;
    const headers = (cfg.headers as Record<string, string>) ?? {};
    const extractions = (cfg.extractions as Array<{ var: string; path: string }>) ?? [];

    function setHeader(key: string, val: string) {
      const next = { ...headers, [key]: val };
      updateNodeConfig({ headers: next });
    }
    function removeHeader(key: string) {
      const next = { ...headers };
      delete next[key];
      updateNodeConfig({ headers: next });
    }
    function addHeader() {
      updateNodeConfig({ headers: { ...headers, '': '' } });
    }
    function setExtraction(i: number, field: 'var' | 'path', val: string) {
      const next = extractions.map((e, idx) => idx === i ? { ...e, [field]: val } : e);
      updateNodeConfig({ extractions: next });
    }
    function removeExtraction(i: number) {
      updateNodeConfig({ extractions: extractions.filter((_, idx) => idx !== i) });
    }
    function addExtraction() {
      updateNodeConfig({ extractions: [...extractions, { var: '', path: '' }] });
    }

    return (
      <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
        <div style={{ fontSize: 12, fontWeight: 700, color: '#38bdf8', textTransform: 'uppercase', letterSpacing: '0.06em' }}>HTTP Request</div>
        <div style={{ fontSize: 11, color: C.textMuted }}>
          Calls an external REST API and writes the response into flow variables. Credentials are set in the Runtime tab.
        </div>

        {displayNameField}

        <InlinePortsSection selectedNode={selectedNode} nodes={nodes} edges={edges} setNodes={setNodes} wirings={wirings} />

        <div style={{ display: 'flex', gap: 8 }}>
          <div style={{ width: 90, flexShrink: 0 }}>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Method</label>
            <select style={fieldStyle} value={method} onChange={e => updateNodeConfig({ method: e.target.value })}>
              {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => <option key={m} value={m}>{m}</option>)}
            </select>
          </div>
          <div style={{ flex: 1 }}>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>URL Template</label>
            <input
              style={{ ...fieldStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}
              placeholder="https://api.example.com/{{.order_id}}"
              value={urlTemplate}
              onChange={e => updateNodeConfig({ url_template: e.target.value })}
            />
          </div>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>
            Static Headers
            <button onClick={addHeader} style={{ marginLeft: 8, fontSize: 10, color: '#38bdf8', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}>+ Add</button>
          </label>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {Object.entries(headers).map(([k, v], i) => (
              <div key={i} style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                <input
                  style={{ ...fieldStyle, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                  placeholder="Header-Name"
                  value={k}
                  onChange={e => { const next: Record<string, string> = {}; Object.entries(headers).forEach(([hk, hv], hi) => { next[hi === i ? e.target.value : hk] = hv; }); updateNodeConfig({ headers: next }); }}
                />
                <input
                  style={{ ...fieldStyle, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                  placeholder="value"
                  value={v}
                  onChange={e => setHeader(k, e.target.value)}
                />
                <button onClick={() => removeHeader(k)} style={{ color: '#f87171', background: 'none', border: 'none', cursor: 'pointer', fontSize: 14, padding: '0 4px' }}>✕</button>
              </div>
            ))}
          </div>
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 4 }}>For auth headers (Authorization, X-API-Key), use the Runtime tab instead.</div>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Body Template</label>
          <textarea
            style={{ ...fieldStyle, minHeight: 70, resize: 'vertical', fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}
            placeholder={'{"amount": {{.amount}}}'}
            value={bodyTemplate}
            onChange={e => updateNodeConfig({ body_template: e.target.value })}
          />
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 4 }}>Leave empty for GET requests. Use {'{{.varname}}'} for flow variables.</div>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>
            JSON Extractions
            <button onClick={addExtraction} style={{ marginLeft: 8, fontSize: 10, color: '#38bdf8', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}>+ Add</button>
          </label>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {extractions.map((ext, i) => (
              <div key={i} style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                <input
                  style={{ ...fieldStyle, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                  placeholder="var_name"
                  value={ext.var}
                  onChange={e => setExtraction(i, 'var', e.target.value)}
                />
                <input
                  style={{ ...fieldStyle, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 11 }}
                  placeholder="$.data.id"
                  value={ext.path}
                  onChange={e => setExtraction(i, 'path', e.target.value)}
                />
                <button onClick={() => removeExtraction(i)} style={{ color: '#f87171', background: 'none', border: 'none', cursor: 'pointer', fontSize: 14, padding: '0 4px' }}>✕</button>
              </div>
            ))}
          </div>
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 4 }}>Each extraction writes one flow variable from the JSON response. Path uses dot notation: $.status, $.data.id</div>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Timeout (seconds)</label>
          <input
            type="number" min={1} max={300}
            style={{ ...fieldStyle, width: 100 }}
            value={timeoutSeconds}
            onChange={e => updateNodeConfig({ timeout_seconds: e.target.value === '' ? 30 : Number(e.target.value) })}
          />
        </div>

        <div style={{ padding: '8px 10px', borderRadius: 6, background: 'rgba(56,189,248,0.06)', border: '1px solid rgba(56,189,248,0.15)', fontSize: 11, color: C.textMuted }}>
          After the call: <code style={{ color: '#38bdf8' }}>http_status</code> (int) and <code style={{ color: '#38bdf8' }}>http_response</code> (full body) are always written to flow vars. Extraction vars listed above are also set.
        </div>

        <div style={{ borderTop: '1px solid rgba(255,255,255,0.08)', paddingTop: 12 }}>
          <WritesSection appId={appId} selectedNode={selectedNode} outputVar="http_response" wirings={wirings} />
        </div>
      </div>
    );
  }

  // Generic inline node (unknown type).
  return (
    <div style={{ padding: 20, color: C.textMuted, fontSize: 13 }}>
      Inline node: <strong style={{ color: C.text }}>{d.node_type}</strong>
    </div>
  );
}
