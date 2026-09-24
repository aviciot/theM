'use client';
import { useState } from 'react';
import { themApi } from '@/lib/api';
import type { AppFlowDebugResultSummary } from '@/lib/apiTypes';
import { C } from '../constants';

// AppFlowDebugLogView — App Canvas Debug Mode "smart debug log" follow-up
// (docs/APP_CANVAS_DEBUG_PLAN.md). Renders the same structured
// GET .../debug/{run_id}/result summary a future debugging-assistant LLM
// would read directly — a human-readable timeline here today, no separate
// UI-only data path to keep in sync later.

const statusColor: Record<string, string> = {
  completed: '#4ade80', failed: '#f87171', running: '#60a5fa',
};

export function AppFlowDebugLogView({ appId, runId }: { appId: string; runId: string }) {
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [summary, setSummary] = useState<AppFlowDebugResultSummary | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function handleToggle() {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    setLoading(true);
    setError(null);
    try {
      const result = await themApi.getAppFlowDebugResult(appId, runId);
      setSummary(result);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load debug log');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div style={{ marginTop: 10 }}>
      <button
        onClick={handleToggle}
        style={{
          background: 'transparent', border: `1px solid ${C.outline}`, color: C.textMuted,
          padding: '5px 12px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px',
          display: 'flex', alignItems: 'center', gap: 6,
        }}
      >
        <span className="material-symbols-outlined" style={{ fontSize: 14 }}>{open ? 'expand_less' : 'expand_more'}</span>
        {open ? 'Hide debug log' : 'View debug log'}
      </button>

      {open && (
        <div style={{
          marginTop: 8, padding: '10px 12px', borderRadius: 8,
          background: 'rgba(0,0,0,0.25)', border: `1px solid ${C.outline}`,
          maxHeight: 320, overflowY: 'auto',
        }}>
          {loading && <div style={{ fontSize: 12, color: C.textMuted }}>Loading…</div>}
          {error && <div style={{ fontSize: 12, color: '#f87171' }}>✗ {error}</div>}
          {summary && (
            <>
              <div style={{
                display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10,
                paddingBottom: 8, borderBottom: `1px solid ${C.outline}`,
              }}>
                <span className="material-symbols-outlined" style={{ fontSize: 16, color: summary.ok ? '#4ade80' : '#f87171' }}>
                  {summary.ok ? 'check_circle' : 'error'}
                </span>
                <span style={{ fontSize: 13, fontWeight: 700, color: summary.ok ? '#4ade80' : '#f87171' }}>
                  {summary.ok ? 'All nodes completed successfully' : `Failed at node "${summary.failed_node_id}"`}
                </span>
              </div>
              {!summary.ok && summary.failed_error && (
                <div style={{
                  marginBottom: 10, padding: '8px 10px', borderRadius: 6,
                  background: 'rgba(248,113,113,0.08)', border: '1px solid rgba(248,113,113,0.3)',
                  fontSize: 11, color: '#f87171', fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                }}>
                  {summary.failed_error}
                </div>
              )}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {summary.node_results.map((step, i) => (
                  <div key={`${step.node_id}-${i}`} style={{
                    display: 'flex', alignItems: 'flex-start', gap: 8,
                    padding: '6px 8px', borderRadius: 6,
                    background: step.status === 'failed' ? 'rgba(248,113,113,0.06)' : 'rgba(255,255,255,0.02)',
                  }}>
                    <span style={{ fontSize: 11, color: C.textMuted, minWidth: 16 }}>{i + 1}.</span>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <span style={{ fontSize: 12, fontWeight: 600, color: C.text, fontFamily: 'monospace' }}>{step.node_id}</span>
                        <span style={{ fontSize: 10, color: C.textMuted, textTransform: 'uppercase' }}>{step.node_kind}</span>
                        <span style={{ fontSize: 11, fontWeight: 700, color: statusColor[step.status] ?? C.textMuted }}>
                          {step.status}
                        </span>
                        {step.latency_ms !== undefined && (
                          <span style={{ fontSize: 10, color: C.textMuted }}>{step.latency_ms}ms</span>
                        )}
                      </div>
                      {step.output && (
                        <div style={{ fontSize: 11, color: '#94a3b8', marginTop: 2, wordBreak: 'break-word' }}>{step.output}</div>
                      )}
                      {step.error && (
                        <div style={{ fontSize: 11, color: '#f87171', marginTop: 2, fontFamily: 'monospace', wordBreak: 'break-word' }}>{step.error}</div>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
