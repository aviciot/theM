'use client';
import type { Node } from '@xyflow/react';
import type { FlowControlNodeData } from '../../../types';
import { C } from '../../../constants';
import { fieldStyle } from './panelShared';
import { getNodeDef } from '@/lib/nodeRegistry';
import { NodeVarsSection } from './NodeVarsSection';

// ── FlowControlNodePanel (Router + HIL) ──────────────────────────────────────

interface Props {
  selectedNode: Node;
  nodes: Node[];
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
  setLogoResult: (v: 'none' | 'valid' | 'invalid' | 'warn') => void;
}

export function FlowControlNodePanel({
  selectedNode, nodes, setNodes, setIsDirty, setLogoResult,
}: Props) {
  const liveFcNode = nodes.find(n => n.id === selectedNode.id);
  const d = (liveFcNode?.data ?? selectedNode.data) as unknown as FlowControlNodeData;
  const isRouter = d.node_type === 'router';
  const isHIL = d.node_type === 'hil';
  const nodeDef = getNodeDef(d.node_type, 'appflow');

  // Unpack current config.
  const cfg = (d.config ?? {}) as Record<string, unknown>;

  function updateFcConfig(patch: Record<string, unknown>) {
    setNodes(ns => ns.map(n => {
      if (n.id !== selectedNode.id) return n;
      const nd = n.data as unknown as FlowControlNodeData;
      return { ...n, data: { ...nd, config: { ...(nd.config ?? {}), ...patch } } as unknown as Record<string, unknown> };
    }));
    setIsDirty(true);
    setLogoResult('none');
  }

  if (isRouter) {
    const labels: string[] = Array.isArray(cfg.output_labels) ? cfg.output_labels as string[] : [];
    const classifierPrompt = (cfg.classifier_prompt as string) ?? '';

    function setLabels(newLabels: string[]) {
      updateFcConfig({ output_labels: newLabels });
    }
    function addLabel() {
      setLabels([...labels, '']);
    }
    function removeLabel(i: number) {
      setLabels(labels.filter((_, idx) => idx !== i));
    }
    function editLabel(i: number, val: string) {
      const next = [...labels];
      next[i] = val;
      setLabels(next);
    }

    const trimmed = labels.map(l => l.trim().toLowerCase());
    const duplicates = new Set(trimmed.filter((l, i) => l && trimmed.indexOf(l) !== i));

    return (
      <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
        <div style={{ fontSize: 12, fontWeight: 700, color: nodeDef.border, textTransform: 'uppercase', letterSpacing: '0.06em' }}>{nodeDef.label}</div>
        <div style={{ fontSize: 11, color: C.textMuted }}>{nodeDef.description}</div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Output Labels</label>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {labels.map((label, i) => {
              const isDup = label.trim() && duplicates.has(label.trim().toLowerCase());
              return (
                <div key={i} style={{ display: 'flex', gap: 6 }}>
                  <input
                    style={{ ...fieldStyle, flex: 1, ...(isDup ? { borderColor: '#f87171' } : {}) }}
                    placeholder={`label_${i + 1}`}
                    value={label}
                    onChange={e => editLabel(i, e.target.value)}
                  />
                  <button
                    onClick={() => removeLabel(i)}
                    style={{ padding: '4px 10px', borderRadius: 6, border: 'none', background: 'rgba(239,68,68,0.15)', color: '#f87171', cursor: 'pointer', fontSize: 12 }}
                  >✕</button>
                </div>
              );
            })}
          </div>
          {duplicates.size > 0 && (
            <div style={{ fontSize: 10, color: '#f87171', marginTop: 4 }}>Labels must be unique — duplicates highlighted in red.</div>
          )}
          <button
            onClick={addLabel}
            style={{ marginTop: 6, padding: '6px 12px', borderRadius: 6, border: '1px solid rgba(6,182,212,0.3)', background: 'rgba(6,182,212,0.08)', color: '#06b6d4', cursor: 'pointer', fontSize: 12 }}
          >+ Add Label</button>
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 4 }}>
            Each label must have a matching outgoing edge to an agent or node.
            {labels.length > 0 && (
              <span style={{ marginLeft: 6, color: nodeDef.border, fontWeight: 700 }}>
                {labels.length} port{labels.length === 1 ? '' : 's'} will appear on the node.
              </span>
            )}
          </div>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Classifier Prompt (optional)</label>
          <textarea
            style={{ ...fieldStyle, minHeight: 80, resize: 'vertical', fontFamily: 'inherit' }}
            placeholder="Leave empty for default: classify the user message and return one of the labels."
            value={classifierPrompt}
            onChange={e => updateFcConfig({ classifier_prompt: e.target.value })}
          />
        </div>

        <NodeVarsSection
          reads={[{ name: 'input', note: 'accumulated flow text passed to the classifier' }]}
          writes={[
            { name: 'router_label', note: 'chosen label (e.g. "billing")' },
            { name: 'router_confidence', note: 'LLM confidence 0.0–1.0' },
            { name: `${d.instance_id ?? 'node'}_label`, note: 'same as router_label — scoped to this node' },
            { name: `${d.instance_id ?? 'node'}_confidence`, note: 'same as router_confidence — scoped to this node' },
          ]}
        />
      </div>
    );
  }

  if (isHIL) {
    const approverRole = (cfg.approver_role as string) ?? 'admin';
    const timeoutSeconds = (cfg.timeout_seconds as number) ?? 0;
    const fallbackAction = (cfg.fallback_action as string) ?? 'reject';
    const prompt = (cfg.prompt as string) ?? '';

    return (
      <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
        <div style={{ fontSize: 12, fontWeight: 700, color: nodeDef.border, textTransform: 'uppercase', letterSpacing: '0.06em' }}>{nodeDef.label}</div>
        <div style={{ fontSize: 11, color: C.textMuted }}>{nodeDef.description}</div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Approver Role</label>
          <select
            style={{ ...fieldStyle, padding: '7px 10px', cursor: 'pointer' }}
            value={approverRole}
            onChange={e => updateFcConfig({ approver_role: e.target.value })}
          >
            <option value="admin">Admin</option>
            <option value="super_admin">Super Admin</option>
            <option value="member">Member</option>
          </select>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Approval Prompt</label>
          <textarea
            style={{ ...fieldStyle, minHeight: 70, resize: 'vertical', fontFamily: 'inherit' }}
            placeholder="e.g. Approve transfer of ${{.amount}} for {{.customer_name}}?"
            value={prompt}
            onChange={e => updateFcConfig({ prompt: e.target.value })}
          />
          <div style={{ fontSize: 10, color: C.textMuted, marginTop: 4 }}>
            Supports {'{{.varname}}'} — flow variables are substituted at runtime before the approver sees the message.
          </div>
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Timeout (seconds, 0 = wait indefinitely)</label>
          <input
            type="number" min={0} max={86400}
            style={fieldStyle}
            value={timeoutSeconds}
            onChange={e => updateFcConfig({ timeout_seconds: Number(e.target.value) || 0 })}
          />
        </div>

        <div>
          <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Timeout Fallback</label>
          <select
            style={{ ...fieldStyle, padding: '7px 10px', cursor: 'pointer' }}
            value={fallbackAction}
            onChange={e => updateFcConfig({ fallback_action: e.target.value })}
          >
            <option value="reject">Reject (block the flow)</option>
            <option value="approve">Auto-approve (continue)</option>
            <option value="abort">Abort (fail the run)</option>
          </select>
        </div>

        <NodeVarsSection
          readsNote="All flow variables available at this point in the flow — use {{.varname}} in the prompt above to reference them (e.g. {{.amount}}, {{.customer_name}})."
          writes={[
            { name: 'hil_decision', note: '"approved" or "rejected"' },
            { name: 'hil_comment', note: "approver's comment (empty if none given)" },
            { name: `${d.instance_id ?? 'node'}_decision`, note: 'same as hil_decision — scoped to this node' },
            { name: `${d.instance_id ?? 'node'}_comment`, note: 'same as hil_comment — scoped to this node' },
          ]}
        />
      </div>
    );
  }

  // Generic flow_control node (unknown type).
  return (
    <div style={{ padding: 20, color: C.textMuted, fontSize: 13 }}>
      Flow control node: <strong style={{ color: C.text }}>{d.node_type}</strong>
    </div>
  );
}
