'use client';
import type { Node, Edge } from '@xyflow/react';
import type { InlineNodeData } from '../../../types';
import { C } from '../../../constants';
import { fieldStyle } from './panelShared';

// ── InlineNodePanel (LLM + Condition) ────────────────────────────────────────

interface Props {
  selectedNode: Node;
  nodes: Node[];
  edges: Edge[];
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
  setLogoResult: (v: 'none' | 'valid' | 'invalid' | 'warn') => void;
}

const CONDITION_EXAMPLES = [
  '{{eq .output "APPROVED"}}',
  '{{gt (len .output) 100}}',
  '{{contains .output "error"}}',
];

export function InlineNodePanel({
  selectedNode, nodes, edges, setNodes, setIsDirty, setLogoResult,
}: Props) {
  const liveNode = nodes.find(n => n.id === selectedNode.id);
  const d = (liveNode?.data ?? selectedNode.data) as unknown as InlineNodeData;
  const isLLM = d.node_type === 'llm';
  const isCondition = d.node_type === 'condition';

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
