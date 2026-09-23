'use client';
import { C } from '../constants';
import type { AppFlowDebugSessionState } from '../hooks/useAppFlowDebugSession';
import type { AppFlowRuntimeParamSpec, AppFlowLLMCredentialValue } from '../types';
import { AppFlowLLMCredentialField } from './AppFlowLLMCredentialField';

// AppFlowDebugPanel — App Canvas Debug Mode (docs/APP_CANVAS_DEBUG_PLAN.md
// Phase 5). Setup panel + Run All + status bar, mirroring the agent builder's
// DebugPanel.tsx layout/styling, but for a real WS-driven debug run of the
// draft canvas (not a client-side simulator) — see useAppFlowDebugSession.ts.
// Per-node LLM credential pickers (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md) render
// below the entry-point/message row — one per node that declares a runtime
// param, never merged into one shared selection.

const inputStyle: React.CSSProperties = {
  background: 'rgba(0,0,0,0.3)', border: `1px solid ${C.outline}`, borderRadius: 6,
  color: C.text, padding: '6px 10px', outline: 'none',
};

export function AppFlowDebugPanel({
  debug,
  entryPointOptions,
  runtimeParamSpecs,
  onSetEntryPointSlug,
  onSetUserMessage,
  onSetCredential,
  onRunAll,
  onReset,
  onClose,
}: {
  debug: AppFlowDebugSessionState;
  entryPointOptions: string[];
  runtimeParamSpecs: AppFlowRuntimeParamSpec[];
  onSetEntryPointSlug: (slug: string) => void;
  onSetUserMessage: (msg: string) => void;
  onSetCredential: (specKey: string, value: AppFlowLLMCredentialValue) => void;
  onRunAll: () => void;
  onReset: () => void;
  onClose: () => void;
}) {
  const credentialSpecs = runtimeParamSpecs.filter(s => s.type === 'llm_credential');
  return (
    <div style={{
      flexShrink: 0, borderBottom: `1px solid ${C.amberBorder}`,
      background: 'rgba(245,158,11,0.06)', padding: '10px 16px',
    }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'flex-end' }}>
        <span style={{ color: C.amber, fontSize: '11px', fontWeight: 700, letterSpacing: '0.08em', alignSelf: 'center' }}>
          DEBUG
          {debug.running && (
            <>
              <style>{`@keyframes afdbg-dot{0%,80%,100%{opacity:0.15}40%{opacity:1}}`}</style>
              {[0, 0.22, 0.44].map(delay => (
                <span key={delay} style={{ marginLeft: 3, width: 5, height: 5, borderRadius: '50%', background: C.amber, display: 'inline-block', animation: `afdbg-dot 1.1s ease-in-out ${delay}s infinite` }} />
              ))}
            </>
          )}
        </span>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <label style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>ENTRY POINT</label>
          <select
            value={debug.entryPointSlug}
            onChange={e => onSetEntryPointSlug(e.target.value)}
            disabled={debug.running}
            style={{ ...inputStyle, width: '180px', fontSize: '12px' }}
          >
            <option value="">— select —</option>
            {entryPointOptions.map(slug => <option key={slug} value={slug}>{slug}</option>)}
          </select>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <label style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>TEST MESSAGE</label>
          <input
            value={debug.userMessage}
            onChange={e => onSetUserMessage(e.target.value)}
            disabled={debug.running}
            placeholder="Message to seed the entry point with…"
            style={{ ...inputStyle, width: '300px', fontSize: '12px' }}
          />
        </div>

        <button
          onClick={onRunAll}
          disabled={debug.running || !debug.entryPointSlug}
          style={{
            background: 'rgba(74,222,128,0.12)', border: '1px solid rgba(74,222,128,0.5)',
            color: '#4ade80', padding: '6px 16px', borderRadius: '6px',
            cursor: debug.running || !debug.entryPointSlug ? 'not-allowed' : 'pointer',
            fontSize: '12px', fontWeight: 700, opacity: debug.running || !debug.entryPointSlug ? 0.6 : 1,
          }}
        >
          ▶ Run All
        </button>

        {debug.runId && (
          <button onClick={onReset} disabled={debug.running} style={{
            background: 'transparent', border: `1px solid ${C.outline}`, color: C.textMuted,
            padding: '5px 12px', borderRadius: '6px', cursor: debug.running ? 'not-allowed' : 'pointer', fontSize: '12px',
          }}>⏹ Reset</button>
        )}

        <button onClick={onClose} style={{
          background: 'transparent', border: `1px solid ${C.outline}`, color: C.textMuted,
          padding: '5px 12px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px',
        }}>✕ Close</button>

        {debug.runId && (
          <span style={{ color: '#64748b', fontSize: '11px' }}>
            run {debug.runId.slice(0, 8)}…
          </span>
        )}
        {debug.error && (
          <span style={{ color: '#f87171', fontSize: '11px', maxWidth: '320px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            ✗ {debug.error}
          </span>
        )}
        {!debug.error && debug.done && (
          <span style={{ color: '#4ade80', fontSize: '11px' }}>✓ Run complete</span>
        )}
      </div>
      <div style={{ marginTop: 6, color: '#475569', fontSize: '10px' }}>
        Runs the saved draft directly on the isolated debug worker pool — no publish required.
        Watch nodes light up on the canvas as they really execute.
      </div>

      {credentialSpecs.length > 0 && (
        <div style={{ marginTop: 8, display: 'flex', flexWrap: 'wrap', gap: 8 }}>
          {credentialSpecs.map(spec => (
            <AppFlowLLMCredentialField
              key={spec.specKey}
              spec={spec}
              value={debug.credentials[spec.specKey]}
              disabled={debug.running}
              onChange={value => onSetCredential(spec.specKey, value)}
            />
          ))}
        </div>
      )}
    </div>
  );
}
