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

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  function copy() {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); }).catch(() => {});
    } else {
      const el = document.createElement('textarea');
      el.value = text;
      document.body.appendChild(el);
      el.select();
      document.execCommand('copy');
      document.body.removeChild(el);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }
  return (
    <button onClick={copy} style={{ padding: '2px 8px', borderRadius: 4, border: '1px solid rgba(255,255,255,0.15)', background: 'rgba(255,255,255,0.06)', color: copied ? '#4ade80' : C.textMuted, fontSize: 11, cursor: 'pointer', flexShrink: 0 }}>
      {copied ? '✓ Copied' : 'Copy'}
    </button>
  );
}

export function AppFlowDebugInspector({ appId, selectedNode, runId }: { appId: string; selectedNode: Node | null; runId: string | null }) {
  const debugInfo = (selectedNode?.data as { _debug?: AppFlowNodeDebugInfo } | undefined)?._debug;
  const [hilComment, setHilComment] = useState('');
  const [hilBusy, setHilBusy] = useState(false);
  const [hilDone, setHilDone] = useState<string | null>(null);
  const [hilPrompt, setHilPrompt] = useState<string | null>(null);
  const [wfiMessage, setWfiMessage] = useState('');
  const [wfiBusy, setWfiBusy] = useState(false);
  const [wfiDone, setWfiDone] = useState(false);
  const [wfiError, setWfiError] = useState<string | null>(null);

  const nodeType = (selectedNode?.data as { node_type?: string })?.node_type ?? selectedNode?.type ?? '';
  const state = debugInfo?.state ?? 'idle';

  // Reset wfiDone when the node comes back to running (cycle looped back to wait_for_input).
  useEffect(() => {
    if (nodeType === 'wait_for_input' && state === 'running') {
      setWfiDone(false);
      setWfiMessage('');
    }
  }, [nodeType, state]);

  // Fetch the rendered HIL prompt from pending-hil when this node is parked.
  useEffect(() => {
    setHilPrompt(null);
    if (nodeType !== 'hil' || (state !== 'running' && state !== 'paused') || !runId || !selectedNode) return;
    let cancelled = false;
    themApi.listPendingHIL().then(rows => {
      if (cancelled) return;
      const match = rows.find(r => r.run_id === runId && r.node_id === selectedNode.id);
      if (match?.prompt) setHilPrompt(match.prompt);
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [nodeType, state, runId, selectedNode?.id]);

  async function sendWFI() {
    if (!runId || !selectedNode || wfiBusy || !wfiMessage.trim()) return;
    setWfiBusy(true);
    setWfiError(null);
    try {
      await themApi.sendDebugUserInput(appId, runId, wfiMessage.trim());
      setWfiDone(true);
      setWfiMessage('');
    } catch (e) {
      setWfiError(e instanceof Error ? e.message : 'Failed to send message');
    }
    setWfiBusy(false);
  }

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

      {/* Wait-for-Input — shown when this is a wait_for_input node and it's running (parked) */}
      {nodeType === 'wait_for_input' && state === 'running' && !wfiDone && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, padding: '10px 0' }}>
          <div style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>SEND MESSAGE</div>
          <div style={{ fontSize: '11px', color: C.textMuted }}>This run is paused, waiting for your reply.</div>
          <textarea
            value={wfiMessage}
            onChange={e => setWfiMessage(e.target.value)}
            placeholder="Type your message…"
            rows={3}
            style={{ width: '100%', boxSizing: 'border-box', padding: '6px 8px', borderRadius: 6, border: '1px solid rgba(255,255,255,0.1)', background: 'rgba(0,0,0,0.25)', color: C.text, fontSize: 11, resize: 'vertical', outline: 'none' }}
            onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); void sendWFI(); } }}
          />
          {wfiError && <div style={{ color: '#f87171', fontSize: '11px' }}>✗ {wfiError}</div>}
          <button
            disabled={wfiBusy || !wfiMessage.trim()}
            onClick={() => void sendWFI()}
            style={{ padding: '7px 0', borderRadius: 6, border: 'none', background: '#4ade80', color: '#021520', fontWeight: 700, fontSize: 12, cursor: wfiBusy || !wfiMessage.trim() ? 'not-allowed' : 'pointer', opacity: wfiBusy || !wfiMessage.trim() ? 0.6 : 1 }}
          >
            {wfiBusy ? 'Sending…' : 'Send'}
          </button>
        </div>
      )}
      {nodeType === 'wait_for_input' && wfiDone && (
        <div style={{ padding: '8px 10px', borderRadius: 6, background: 'rgba(74,222,128,0.12)', color: '#4ade80', fontSize: 12, fontWeight: 700 }}>
          ✓ Message sent — run will continue
        </div>
      )}

      {/* HIL approve/reject — shown when this is a hil node and it's running or paused (step mode parks it as paused) */}
      {nodeType === 'hil' && (state === 'running' || state === 'paused') && !hilDone && (
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

      {debugInfo?.detail && (() => {
        // Attempt to parse detail as a structured transform trace card.
        type TransformStep = { fn: string; input_var: string; output_var: string; in: string; out?: string; error?: string; ok: boolean; duration_ns: number };
        let transformCard: { steps: TransformStep[] } | null = null;
        try {
          const parsed = JSON.parse(debugInfo.detail);
          if (typeof parsed === 'object' && parsed !== null && Array.isArray(parsed.steps) && parsed.steps.length > 0 && 'fn' in parsed.steps[0]) {
            transformCard = parsed;
          }
        } catch { /* not JSON */ }

        if (transformCard) {
          return (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>TRANSFORM STEPS</div>
              {transformCard.steps.map((step, i) => (
                <div key={i} style={{
                  background: step.ok ? 'rgba(74,222,128,0.06)' : 'rgba(248,113,113,0.06)',
                  border: `1px solid ${step.ok ? 'rgba(74,222,128,0.25)' : 'rgba(248,113,113,0.35)'}`,
                  borderRadius: 6, padding: '8px 10px',
                }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                    <span style={{ fontWeight: 700, fontSize: 11, color: step.ok ? '#4ade80' : '#f87171' }}>{step.ok ? '✓' : '✗'}</span>
                    <code style={{ fontSize: 11, color: '#c4b5fd' }}>{step.fn}</code>
                    <span style={{ fontSize: 10, color: C.textMuted }}>{step.input_var} → {step.output_var}</span>
                    <span style={{ fontSize: 10, color: C.textMuted, marginLeft: 'auto' }}>{(step.duration_ns / 1_000_000).toFixed(2)}ms</span>
                  </div>
                  {step.in && (
                    <div style={{ display: 'flex', gap: 6, fontSize: 10, marginBottom: 2 }}>
                      <span style={{ color: C.textMuted, flexShrink: 0 }}>in:</span>
                      <code style={{ color: '#94a3b8', wordBreak: 'break-all' }}>{step.in.length > 120 ? step.in.slice(0, 120) + '…' : step.in}</code>
                    </div>
                  )}
                  {step.ok && step.out !== undefined && (
                    <div style={{ display: 'flex', gap: 6, fontSize: 10 }}>
                      <span style={{ color: C.textMuted, flexShrink: 0 }}>out:</span>
                      <code style={{ color: '#4ade80', wordBreak: 'break-all' }}>{step.out.length > 120 ? step.out.slice(0, 120) + '…' : step.out}</code>
                    </div>
                  )}
                  {!step.ok && step.error && (
                    <div style={{ fontSize: 10, color: '#f87171', marginTop: 2 }}>{step.error}</div>
                  )}
                </div>
              ))}
            </div>
          );
        }

        // Attempt to parse detail as a structured HTTP trace card.
        let httpCard: { status?: number; url?: string; body?: string; extracted?: Record<string, string> } | null = null;
        try {
          const parsed = JSON.parse(debugInfo.detail);
          if (typeof parsed === 'object' && parsed !== null && typeof parsed.status === 'number') {
            httpCard = parsed;
          }
        } catch { /* not JSON — render as plain text below */ }

        if (httpCard) {
          const statusOk = (httpCard.status ?? 0) < 400;
          return (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>HTTP RESPONSE</div>
              {/* Status + URL */}
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8, flexWrap: 'wrap' }}>
                <span style={{
                  padding: '2px 8px', borderRadius: 4, fontWeight: 700, fontSize: 12, flexShrink: 0,
                  background: statusOk ? 'rgba(74,222,128,0.15)' : 'rgba(248,113,113,0.15)',
                  color: statusOk ? '#4ade80' : '#f87171',
                  border: `1px solid ${statusOk ? 'rgba(74,222,128,0.4)' : 'rgba(248,113,113,0.4)'}`,
                }}>
                  {httpCard.status}
                </span>
                {httpCard.url && (
                  <>
                    <span style={{ fontSize: 11, color: C.textMuted, wordBreak: 'break-all', flex: 1 }}>{httpCard.url}</span>
                    <CopyButton text={httpCard.url} />
                  </>
                )}
              </div>
              {/* Extractions */}
              {httpCard.extracted && Object.keys(httpCard.extracted).length > 0 && (
                <div>
                  <div style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em', marginBottom: 4 }}>EXTRACTED VARS</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                    {Object.entries(httpCard.extracted).map(([k, v]) => (
                      <div key={k} style={{ display: 'flex', gap: 6, fontSize: 11 }}>
                        <span style={{ color: '#38bdf8', fontWeight: 600, flexShrink: 0 }}>{k}</span>
                        <span style={{ color: C.text, wordBreak: 'break-all' }}>{v}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              {/* Response body */}
              {httpCard.body && (
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
                    <div style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>RESPONSE BODY</div>
                    <CopyButton text={httpCard.body} />
                  </div>
                  <pre style={{
                    margin: 0, padding: '8px 10px', background: 'rgba(0,0,0,0.3)', border: `1px solid ${C.outline}`,
                    borderRadius: 6, fontSize: '11px', color: '#4ade80', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                    maxHeight: 400, overflowY: 'auto',
                  }}>
                    {httpCard.body}
                  </pre>
                </div>
              )}
            </div>
          );
        }

        return (
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
        );
      })()}

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
