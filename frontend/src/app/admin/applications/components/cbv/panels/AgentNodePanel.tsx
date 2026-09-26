'use client';
import type { Node } from '@xyflow/react';
import type { AgentNodeData } from '../../../types';
import { C } from '../../../constants';
import { fieldStyle, chipStyle } from './panelShared';
import { AgentGuardsSection } from './AgentGuardsSection';
import type { Agent } from '@/lib/api';

// ── AgentNodePanel ────────────────────────────────────────────────────────────

interface Props {
  appId: string;
  selectedNode: Node;
  nodes: Node[];
  agents: Agent[];
  configPanelText: string;
  setConfigPanelText: React.Dispatch<React.SetStateAction<string>>;
  configPanelErr: boolean;
  setConfigPanelErr: React.Dispatch<React.SetStateAction<boolean>>;
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
  showToast: (msg: string, ok: boolean) => void;
}

export function AgentNodePanel({
  appId, selectedNode, nodes, agents,
  configPanelText, setConfigPanelText, configPanelErr, setConfigPanelErr,
  setNodes, setIsDirty, showToast,
}: Props) {
  const liveAgentNode = nodes.find(n => n.id === selectedNode.id);
  const d = (liveAgentNode?.data ?? selectedNode.data) as unknown as AgentNodeData;
  return (
    <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 12, overflowY: 'auto' }}>
      <div style={{ fontSize: 12, fontWeight: 700, color: C.green, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>Agent</div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Instance ID</label>
        <div style={chipStyle}>{d.instance_id}</div>
      </div>
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Display Name</label>
        <div style={{ fontSize: 13, color: C.text, padding: '6px 0' }}>{d.display_name}</div>
      </div>
      {d.description && <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Description</label>
        <div style={{ fontSize: 12, color: C.textMuted }}>{d.description}</div>
      </div>}
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Config (JSON)</label>
        <textarea
          style={{ ...fieldStyle, minHeight: 100, resize: 'vertical', fontFamily: 'JetBrains Mono, monospace', fontSize: 12, borderColor: configPanelErr ? C.error : undefined }}
          value={configPanelText}
          onChange={e => setConfigPanelText(e.target.value)}
          onBlur={() => {
            try {
              const parsed = JSON.parse(configPanelText);
              setNodes(ns => ns.map(n => n.id === selectedNode.id ? { ...n, data: { ...n.data, config: parsed } } : n));
              setIsDirty(true);
              setConfigPanelErr(false);
            } catch { setConfigPanelErr(true); showToast('Invalid JSON', false); }
          }}
        />
      </div>
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.08)', paddingTop: 12 }}>
        <AgentGuardsSection appId={appId} selectedNode={selectedNode} agents={agents} showToast={showToast} />
      </div>
    </div>
  );
}
