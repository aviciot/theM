'use client';
import { useState, useEffect } from 'react';
import type { Node } from '@xyflow/react';
import { C } from '../constants';
import type { AppFlowNodeDebugInfo } from '../types';
import { themApi } from '@/lib/api';

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

export function AppFlowDebugInspector({ selectedNode, runId }: { selectedNode: Node | null; runId: string | null }) {
  const debugInfo = (selectedNode?.data as { _debug?: AppFlowNodeDebugInfo } | undefined)?._debug;
  const [hilComment, setHilComment] = useState('');
  const [hilBusy, setHilBusy] = useState(false);
  const [hilDone, setHilDone] = useState<string | null>(null);
  const [hilPrompt, setHilPrompt] = useState<string | null>(null);

  const nodeType = (selectedNode?.data as { node_type?: string })?.node_type ?? selectedNode?.type ?? '';
  const state = debugInfo?.state ?? 'idle';

  // Fetch the rendered HIL prompt from pending-hil when this node is parked.
  useEffect(() => {
    setHilPrompt(null);
    if (nodeType !== 'hil' || state !== 'running' || !runId || !selectedNode) return;
    let cancelled = false;
    themApi.listPendingHIL().then(rows => {
      if (cancelled) return;
      const match = rows.find(r => r.run_id === runId && r.node_id === selectedNode.id);
      if (match?.prompt) setHilPrompt(match.prompt);
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [nodeType, state, runId, selectedNode?.id]);

  async function sendHIL(approve: boolean) {
    if (!runId || !selectedNode || hilBusy) return;
    setHilBusy(true);
    try {
      if (approve) await themApi.hilApprove(runId, selectedNode.id, hilComment);
      else         await themApi.hilReject(runId, selectedNode.id, hilComment);
      setHilDone(approve ? 'approved' : 'rejected');
    } catch { /* ignore — user will see run status update */ }
    setHilBusy(false);
  }

  if (!selectedNode) {
    return (
      <div style={{ padding: '16px', fontSize: '12px', color: C.textMuted }}>
        Click a node on the canvas to inspect its debug state.
      </div>
    );
  }

  const displayName = (selectedNode.data as { display_name?: string })?.display_name;

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

      {/* HIL approve/reject — shown when this is a hil node and it's running (parked) */}
      {nodeType === 'hil' && state === 'running' && !hilDone && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, padding: '10px 0' }}>
          <div style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>HUMAN REVIEW</div>
          {hilPrompt ? (
            <div style={{ padding: '8px 10px', borderRadius: 6, background: 'rgba(168,85,247,0.08)', border: '1px solid rgba(168,85,247,0.25)', fontSize: '12px', color: '#e2d9f3', lineHeight: 1.5 }}>
              {hilPrompt}
            </div>
          ) : (
            <div style={{ fontSize: '11px', color: C.textMuted }}>This run is waiting for your decision.</div>
          )}
          <textarea
            value={hilComment}
            onChange={e => setHilComment(e.target.value)}
            placeholder="Optional comment…"
            rows={2}
            style={{ width: '100%', boxSizing: 'border-box', padding: '6px 8px', borderRadius: 6, border: '1px solid rgba(255,255,255,0.1)', background: 'rgba(0,0,0,0.25)', color: C.text, fontSize: 11, resize: 'vertical', outline: 'none' }}
          />
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              disabled={hilBusy}
              onClick={() => sendHIL(true)}
              style={{ flex: 1, padding: '7px 0', borderRadius: 6, border: 'none', background: '#22c55e', color: '#021520', fontWeight: 700, fontSize: 12, cursor: hilBusy ? 'not-allowed' : 'pointer', opacity: hilBusy ? 0.6 : 1 }}
            >Approve</button>
            <button
              disabled={hilBusy}
              onClick={() => sendHIL(false)}
              style={{ flex: 1, padding: '7px 0', borderRadius: 6, border: 'none', background: '#f87171', color: '#021520', fontWeight: 700, fontSize: 12, cursor: hilBusy ? 'not-allowed' : 'pointer', opacity: hilBusy ? 0.6 : 1 }}
            >Reject</button>
          </div>
        </div>
      )}
      {nodeType === 'hil' && hilDone && (
        <div style={{ padding: '8px 10px', borderRadius: 6, background: hilDone === 'approved' ? 'rgba(34,197,94,0.12)' : 'rgba(248,113,113,0.12)', color: hilDone === 'approved' ? '#22c55e' : '#f87171', fontSize: 12, fontWeight: 700 }}>
          {hilDone === 'approved' ? '✓ Approved' : '✗ Rejected'}
        </div>
      )}

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
