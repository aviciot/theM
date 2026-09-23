'use client';
import type { Node } from '@xyflow/react';
import { C } from '../constants';
import type { AppFlowNodeDebugInfo } from '../types';

// AppFlowDebugInspector — App Canvas Debug Mode Phase 6
// (docs/APP_CANVAS_DEBUG_PLAN.md). Click-to-inspect: shows the clicked node's
// captured debug detail (the node_done/node_error/node_paused payload's
// `detail` string, already captured by useAppFlowDebugSession into
// nodeDetails/nodeErrors) in a dedicated side panel, mirroring the agent
// builder's StepDebugSection.tsx placement rather than a canvas popover — a
// popover would compete for space with CanvasNodes.tsx's existing per-node
// Ports popover.
//
// Rendered instead of CanvasNodePropertiesPanel while a debug session is
// active, since the two panels serve mutually exclusive purposes (editing
// canvas config vs. inspecting a live/finished debug run) and would otherwise
// need to be visually reconciled in the same space for no benefit.

const stateColor: Record<AppFlowNodeDebugInfo['state'], string> = {
  idle: '#64748b', pending: '#f59e0b', running: '#60a5fa', paused: '#c084fc', done: '#4ade80', error: '#f87171',
};

const stateLabel: Record<AppFlowNodeDebugInfo['state'], string> = {
  idle: 'Idle', pending: 'Pending', running: 'Running…', paused: 'Paused — waiting for Step', done: 'Done', error: 'Error',
};

export function AppFlowDebugInspector({ selectedNode }: { selectedNode: Node | null }) {
  const debugInfo = (selectedNode?.data as { _debug?: AppFlowNodeDebugInfo } | undefined)?._debug;

  if (!selectedNode) {
    return (
      <div style={{ padding: '16px', fontSize: '12px', color: C.textMuted }}>
        Click a node on the canvas to inspect its debug state.
      </div>
    );
  }

  const nodeType = (selectedNode.data as { node_type?: string })?.node_type ?? selectedNode.type ?? '';
  const displayName = (selectedNode.data as { display_name?: string })?.display_name;
  const state = debugInfo?.state ?? 'idle';

  return (
    <div style={{ padding: '14px 16px', fontSize: '12px', color: C.text, display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div>
        <div style={{ fontWeight: 700, fontSize: '13px' }}>{displayName || selectedNode.id}</div>
        <div style={{ color: C.textMuted, fontSize: '10px', textTransform: 'uppercase', letterSpacing: '0.06em', marginTop: 2 }}>
          {nodeType} · {selectedNode.id}
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ width: 8, height: 8, borderRadius: '50%', background: stateColor[state], flexShrink: 0 }} />
        <span style={{ color: stateColor[state], fontWeight: 700 }}>{stateLabel[state]}</span>
      </div>

      {debugInfo?.detail && (
        <div>
          <div style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em', marginBottom: 4 }}>OUTPUT / DETAIL</div>
          <pre style={{
            margin: 0, padding: '8px 10px', background: 'rgba(0,0,0,0.3)', border: `1px solid ${C.outline}`,
            borderRadius: 6, fontSize: '11px', color: '#4ade80', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            maxHeight: 240, overflowY: 'auto',
          }}>
            {debugInfo.detail}
          </pre>
        </div>
      )}

      {debugInfo?.error && (
        <div>
          <div style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em', marginBottom: 4 }}>ERROR</div>
          <pre style={{
            margin: 0, padding: '8px 10px', background: 'rgba(248,113,113,0.08)', border: '1px solid rgba(248,113,113,0.4)',
            borderRadius: 6, fontSize: '11px', color: '#f87171', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            maxHeight: 240, overflowY: 'auto',
          }}>
            {debugInfo.error}
          </pre>
        </div>
      )}

      {!debugInfo?.detail && !debugInfo?.error && (
        <div style={{ color: C.textMuted, fontSize: '11px' }}>
          {state === 'idle' ? 'This node has not run yet in the current debug session.' : 'No output captured for this node yet.'}
        </div>
      )}
    </div>
  );
}
